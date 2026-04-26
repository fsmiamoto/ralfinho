package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fsmiamoto/ralfinho/internal/runner"
	runewidth "github.com/mattn/go-runewidth"
)

// Bubble Tea message types.

// EventMsg delivers a new display event to the TUI.
type EventMsg DisplayEvent

// StatusMsg updates the status bar text.
type StatusMsg struct{ Text string }

// DoneMsg signals the runner has finished.
type DoneMsg struct{ Result runner.RunResult }

// Model is the Bubble Tea model for ralfinho's TUI.
type Model struct {
	events       []DisplayEvent
	cursor       int     // selected event index in stream pane
	detailScroll int     // scroll offset in detail pane
	streamScroll int     // scroll offset in stream pane
	width        int     // terminal width
	height       int     // terminal height
	paneRatio    float64 // fraction of width for left (stream) pane
	focusedPane  int     // 0=main, 1=stream, 2=detail
	rawMode      bool    // show raw detail vs rendered
	running      bool    // agent still running
	status       string  // status bar text
	eventCh      <-chan runner.Event
	converter    *EventConverter
	autoScroll   bool // auto-scroll stream when new events arrive
	confirmQuit  bool // waiting for quit confirmation
	confirmCtrlC bool // true if ctrl+c triggered confirm, false if q
	result    *runner.RunResult
	startTime time.Time
	modelName    string
	agentName    string
	iteration    int // current iteration count for header display

	lastEventTime      time.Time // time of last raw event, for inactivity indicator
	errorOverlay       string    // non-empty = show error modal overlay
	errorOverlayScroll int    // scroll offset within the error overlay
	promptText         string // full effective prompt text
	promptOverlay      bool   // whether the prompt overlay is shown
	promptOverlayScroll int   // scroll offset within the prompt overlay
	helpOverlay        bool   // whether the help/keybinding overlay is shown

	memoryOverlay       bool   // whether the memory overlay is shown
	memoryOverlayTab    int    // 0=NOTES, 1=PROGRESS
	memoryOverlayScroll int    // scroll offset within the memory overlay
	notesPath           string // path to NOTES.md in the run directory
	progressPath        string // path to PROGRESS.md in the run directory

	timeoutOverlay  bool                   // whether the timeout input overlay is shown
	timeoutInput    string                 // current text buffer in the timeout overlay
	timeoutError    string                 // populated when parse fails; cleared on next keystroke
	currentTimeout  *time.Duration         // local mirror of runner timeout: nil = default; pointer-to-0 = disabled; positive = custom
	controlSend     chan<- runner.ControlMsg // write end of the control channel; nil in viewer mode

	// Main view (top pane) state.
	blocks         []MainBlock // ordered content blocks for the main view
	mainScroll     int         // scroll offset in main view (line-based)
	mainAutoScroll bool        // auto-follow new content (default true)
	activeToolIdx  int         // index of in-progress tool block in blocks (-1 = none)

	// Main-pane line index (populated by ensureMainLayout).
	mainIndexDirtyFrom  int   // earliest block needing reindexing
	mainBlockStarts     []int // document line offset of each block
	mainBlockLineCounts []int // number of screen lines each block contributes
	mainTotalLines      int   // total lines in the virtual document
	mainLayoutWidth     int   // width the index was last computed for
}

// NewModel creates a TUI model that reads runner events from ch.
//
// currentTimeout mirrors RunConfig.InactivityTimeout (nil = default, pointer
// to 0 = disabled, positive = custom). controlSend is the write end of the
// runner control channel; nil disables the in-session control overlays.
func NewModel(ch <-chan runner.Event, agentName, promptText, notesPath, progressPath string, currentTimeout *time.Duration, controlSend chan<- runner.ControlMsg) Model {
	return Model{
		paneRatio:      0.3,
		running:        true,
		status:         "Starting...",
		eventCh:        ch,
		converter:      NewEventConverter(),
		autoScroll:     true,
		mainAutoScroll: true,
		activeToolIdx:  -1,
		startTime:      time.Now(),
		agentName:      agentName,
		promptText:     promptText,
		notesPath:      notesPath,
		progressPath:   progressPath,
		currentTimeout: currentTimeout,
		controlSend:    controlSend,
	}
}

// NewViewerModel creates a read-only TUI model pre-loaded with events.
// It is used for replaying a saved run — no event channel, not running.
func NewViewerModel(events []DisplayEvent, meta runner.RunMeta, promptText, notesPath, progressPath string) Model {
	agentName := meta.Agent
	if agentName == "" {
		agentName = "pi"
	}
	status := fmt.Sprintf("Run %s | %s | %s | %s | %d iterations",
		shortID(meta.RunID), agentName, meta.Status, meta.StartedAt, meta.IterationsCompleted)

	m := Model{
		events:         events,
		paneRatio:      0.3,
		running:        false,
		status:         status,
		autoScroll:     false,
		mainAutoScroll: false,
		activeToolIdx:  -1,
		agentName:      agentName,
		promptText:     promptText,
		notesPath:      notesPath,
		progressPath:   progressPath,
	}

	// Pre-build blocks from loaded display events.
	for _, de := range events {
		m.buildBlock(de)
	}

	return m
}

// RunResult returns the runner result if available, or nil.
func (m Model) RunResult() *runner.RunResult {
	return m.result
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// waitForEvent returns a Cmd that waits for the next event on the channel.
func (m Model) waitForEvent() tea.Cmd {
	ch := m.eventCh
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil // channel closed; DoneMsg comes separately
		}
		return rawEventMsg(ev)
	}
}

// rawEventMsg wraps a runner.Event for internal routing.
type rawEventMsg runner.Event

// tickMsg is sent by the 1Hz timer to trigger a View() refresh for the
// elapsed-time display while the agent is running.
type tickMsg time.Time

// tickCmd returns a Cmd that fires a tickMsg every second.
func tickCmd() tea.Cmd {
	return tea.Every(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	if !m.running {
		return nil
	}
	return tea.Batch(m.waitForEvent(), tickCmd())
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Re-init markdown renderer with main view content width (widest pane).
		// Main view spans full terminal width; content width is width minus
		// borders and padding. This width works for both main and detail panes.
		mainContentWidth := m.width - 4
		if mainContentWidth < 20 {
			mainContentWidth = 20
		}
		initRenderer(mainContentWidth)
		m.invalidateAllMainLayouts()
		return m, nil

	case rawEventMsg:
		return m.handleRawEvent(runner.Event(msg))

	case EventMsg:
		return m.addDisplayEvent(DisplayEvent(msg))

	case StatusMsg:
		m.status = msg.Text
		return m, nil

	case DoneMsg:
		m.running = false
		m.status = fmt.Sprintf("Done — %s | %s (%d iterations)", msg.Result.Agent, msg.Result.Status, msg.Result.Iterations)
		if msg.Result.Error != "" {
			m.errorOverlay = msg.Result.Error
			m.errorOverlayScroll = 0
		}
		m.result = &msg.Result
		return m, nil

	case tickMsg:
		if m.running {
			return m, tickCmd()
		}
		return m, nil
	}

	return m, nil
}

func (m Model) handleRawEvent(ev runner.Event) (tea.Model, tea.Cmd) {
	m.lastEventTime = time.Now()
	displayEvents := m.converter.Convert(&ev)
	var cmds []tea.Cmd
	for _, de := range displayEvents {
		updated, cmd := m.addDisplayEvent(de)
		m = updated.(Model)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	cmds = append(cmds, m.waitForEvent())
	return m, tea.Batch(cmds...)
}

func (m Model) addDisplayEvent(de DisplayEvent) (tea.Model, tea.Cmd) {
	// Update status bar and iteration counter on iteration boundaries.
	if de.Type == DisplayIteration && m.running {
		m.iteration = de.Iteration
		m.status = fmt.Sprintf("Iteration #%d", de.Iteration)
	}

	// Extract model name from assistant_text summaries like "← Assistant (claude-xxx)".
	if de.Type == DisplayAssistantText && de.Summary != "" {
		if start := strings.Index(de.Summary, "("); start != -1 {
			if end := strings.Index(de.Summary[start:], ")"); end != -1 {
				name := de.Summary[start+1 : start+end]
				if name != "" {
					m.modelName = name
				}
			}
		}
	}

	// For assistant_text updates, merge with the last assistant_text event.
	if de.Type == DisplayAssistantText && len(m.events) > 0 {
		last := &m.events[len(m.events)-1]
		if last.Type == DisplayAssistantText && last.Iteration == de.Iteration {
			last.Summary = de.Summary
			last.Detail = de.Detail
			last.Timestamp = de.Timestamp
			last.AssistantFinal = de.AssistantFinal
			// Also update the corresponding block.
			m.updateAssistantBlock(de)
			m.autoScrollMain()
			return m, nil
		}
	}

	m.events = append(m.events, de)

	// Build corresponding block for the main view.
	m.buildBlock(de)
	m.autoScrollMain()

	// Auto-scroll: if cursor is at/near the bottom, follow new events.
	if m.autoScroll {
		m.cursor = len(m.events) - 1
		m.detailScroll = 0
		m.ensureStreamCursorVisible()
	}

	return m, nil
}

// buildBlock appends or updates blocks based on the display event type.
func (m *Model) buildBlock(de DisplayEvent) {
	switch de.Type {
	case DisplayIteration:
		m.blocks = append(m.blocks, MainBlock{
			Kind:      BlockIteration,
			Iteration: de.Iteration,
		})
		m.invalidateMainLayoutFrom(len(m.blocks) - 1)
	case DisplayAssistantText:
		// Merge with last BlockAssistantText for the same iteration.
		if len(m.blocks) > 0 {
			last := &m.blocks[len(m.blocks)-1]
			if last.Kind == BlockAssistantText && last.Iteration == de.Iteration {
				last.Text = de.Detail
				last.AssistantFinal = de.AssistantFinal
				last.InvalidateLayout()
				m.invalidateMainLayoutFrom(len(m.blocks) - 1)
				return
			}
		}
		m.blocks = append(m.blocks, MainBlock{
			Kind:           BlockAssistantText,
			Iteration:      de.Iteration,
			Text:           de.Detail,
			AssistantFinal: de.AssistantFinal,
		})
		m.invalidateMainLayoutFrom(len(m.blocks) - 1)
	case DisplayThinking:
		m.blocks = append(m.blocks, MainBlock{
			Kind:        BlockThinking,
			Iteration:   de.Iteration,
			ThinkingLen: len(de.Detail),
		})
		m.invalidateMainLayoutFrom(len(m.blocks) - 1)
	case DisplayToolStart:
		toolArgs := de.ToolDisplayArgs
		if toolArgs == "" {
			toolArgs = formatToolArgs(de.ToolName, de.RawArgs)
		}
		m.blocks = append(m.blocks, MainBlock{
			Kind:       BlockToolCall,
			Iteration:  de.Iteration,
			ToolName:   de.ToolName,
			ToolCallID: de.ToolCallID,
			ToolArgs:   toolArgs,
		})
		m.activeToolIdx = len(m.blocks) - 1
		m.invalidateMainLayoutFrom(len(m.blocks) - 1)
	case DisplayToolUpdate:
		// Intermediate update — kiro sends the actual args in a follow-up.
		// Find the matching tool block and update its args.
		for i := len(m.blocks) - 1; i >= 0; i-- {
			if m.blocks[i].Kind == BlockToolCall && m.blocks[i].ToolCallID == de.ToolCallID {
				updatedArgs := de.ToolDisplayArgs
				if updatedArgs == "" {
					updatedArgs = formatToolArgs(de.ToolName, de.RawArgs)
				}
				m.blocks[i].ToolArgs = updatedArgs
				m.blocks[i].InvalidateLayout()
				m.invalidateMainLayoutFrom(i)
				break
			}
		}
	case DisplayToolEnd:
		// Find the matching tool_start block by ToolCallID.
		for i := len(m.blocks) - 1; i >= 0; i-- {
			if m.blocks[i].Kind == BlockToolCall && m.blocks[i].ToolCallID == de.ToolCallID {
				m.blocks[i].ToolDone = true
				m.blocks[i].ToolResult = de.ToolResultText
				m.blocks[i].ToolError = de.ToolIsError
				m.blocks[i].InvalidateLayout()
				m.invalidateMainLayoutFrom(i)
				break
			}
		}
		m.activeToolIdx = -1
	case DisplayInfo:
		m.blocks = append(m.blocks, MainBlock{
			Kind:     BlockInfo,
			InfoText: de.Detail,
		})
		m.invalidateMainLayoutFrom(len(m.blocks) - 1)
		// user_msg, turn_end, agent_end, session — skip (don't clutter main view)
	}
}

// updateAssistantBlock updates the last assistant text block when streaming.
func (m *Model) updateAssistantBlock(de DisplayEvent) {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].Kind == BlockAssistantText && m.blocks[i].Iteration == de.Iteration {
			m.blocks[i].Text = de.Detail
			m.blocks[i].AssistantFinal = de.AssistantFinal
			m.blocks[i].InvalidateLayout()
			m.invalidateMainLayoutFrom(i)
			return
		}
	}
}

// autoScrollMain adjusts mainScroll to keep the bottom visible when auto-scrolling.
func (m *Model) autoScrollMain() {
	if !m.mainAutoScroll {
		return
	}
	// Compute total rendered line count. Use a rough estimate: each block
	// contributes at least 1 line. The precise count is computed in renderMain()
	// but we need to set mainScroll high enough that renderMain() shows the bottom.
	// Setting mainScroll to a very large value works because renderMain() clamps it.
	m.mainScroll = 999999
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle timeout overlay keys.
	if m.timeoutOverlay {
		return m.handleTimeoutKey(msg)
	}

	// Handle prompt overlay keys.
	if m.promptOverlay {
		switch msg.String() {
		case "j", "down":
			m.promptOverlayScroll++
		case "k", "up":
			if m.promptOverlayScroll > 0 {
				m.promptOverlayScroll--
			}
		default:
			// p, Esc, or any other key dismisses the overlay.
			m.promptOverlay = false
		}
		return m, nil
	}

	// Handle error overlay keys (j/k scroll, any other key dismisses).
	if m.errorOverlay != "" {
		switch msg.String() {
		case "j", "down":
			m.errorOverlayScroll++
		case "k", "up":
			if m.errorOverlayScroll > 0 {
				m.errorOverlayScroll--
			}
		default:
			m.errorOverlay = ""
			m.errorOverlayScroll = 0
		}
		return m, nil
	}

	// Handle memory overlay keys.
	if m.memoryOverlay {
		switch msg.String() {
		case "j", "down":
			m.memoryOverlayScroll++
		case "k", "up":
			if m.memoryOverlayScroll > 0 {
				m.memoryOverlayScroll--
			}
		case "tab":
			m.memoryOverlayTab = (m.memoryOverlayTab + 1) % 2
			m.memoryOverlayScroll = 0
		case "n", "esc", "q":
			m.memoryOverlay = false
		}
		return m, nil
	}

	// Handle help overlay keys.
	if m.helpOverlay {
		switch msg.String() {
		case "?", "q", "esc":
			m.helpOverlay = false
		}
		return m, nil
	}

	// Handle quit confirmation state.
	if m.confirmQuit {
		if m.confirmCtrlC && msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if !m.confirmCtrlC && msg.String() == "q" {
			return m, tea.Quit
		}
		m.confirmQuit = false
		return m, nil
	}

	switch msg.String() {

	case "q":
		m.confirmQuit = true
		m.confirmCtrlC = false
		return m, nil

	case "ctrl+c":
		m.confirmQuit = true
		m.confirmCtrlC = true
		return m, nil

	case "j", "down":
		if m.focusedPane == 0 {
			// Scroll main view down.
			m.mainScroll++
			m.mainAutoScroll = false
		} else if m.focusedPane == 1 && len(m.events) > 0 {
			if m.cursor < len(m.events)-1 {
				m.cursor++
				m.detailScroll = 0
			}
			m.autoScroll = m.cursor >= len(m.events)-1
			m.ensureStreamCursorVisible()
		} else if m.focusedPane == 2 {
			m.detailScroll++
		}

	case "k", "up":
		if m.focusedPane == 0 {
			// Scroll main view up.
			if m.mainScroll > 0 {
				m.mainScroll--
			}
			m.mainAutoScroll = false
		} else if m.focusedPane == 1 {
			if m.cursor > 0 {
				m.cursor--
				m.detailScroll = 0
			}
			m.autoScroll = false
			m.ensureStreamCursorVisible()
		} else if m.focusedPane == 2 {
			if m.detailScroll > 0 {
				m.detailScroll--
			}
		}

	case "g":
		if m.focusedPane == 0 {
			m.mainScroll = 0
			m.mainAutoScroll = false
		} else if m.focusedPane == 1 {
			m.cursor = 0
			m.streamScroll = 0
			m.detailScroll = 0
			m.autoScroll = false
		}

	case "G":
		if m.focusedPane == 0 {
			m.mainScroll = 999999 // clamped in renderMain()
			m.mainAutoScroll = true
		} else if m.focusedPane == 1 && len(m.events) > 0 {
			m.cursor = len(m.events) - 1
			m.detailScroll = 0
			m.autoScroll = true
			m.ensureStreamCursorVisible()
		}

	case "ctrl+d":
		if m.focusedPane == 0 {
			pageSize := m.mainHeight() / 2
			if pageSize < 1 {
				pageSize = 1
			}
			m.mainScroll += pageSize
			m.mainAutoScroll = false
		} else {
			pageSize := m.paneHeight() / 2
			if pageSize < 1 {
				pageSize = 1
			}
			m.detailScroll += pageSize
		}

	case "ctrl+u":
		if m.focusedPane == 0 {
			pageSize := m.mainHeight() / 2
			if pageSize < 1 {
				pageSize = 1
			}
			m.mainScroll -= pageSize
			if m.mainScroll < 0 {
				m.mainScroll = 0
			}
			m.mainAutoScroll = false
		} else {
			pageSize := m.paneHeight() / 2
			if pageSize < 1 {
				pageSize = 1
			}
			m.detailScroll -= pageSize
			if m.detailScroll < 0 {
				m.detailScroll = 0
			}
		}

	case "tab":
		m.focusedPane = (m.focusedPane + 1) % 3

	case "p":
		m.promptOverlay = true
		m.promptOverlayScroll = 0

	case "r":
		m.rawMode = !m.rawMode

	case "n":
		m.memoryOverlay = true
		m.memoryOverlayTab = 0
		m.memoryOverlayScroll = 0

	case "t":
		// In viewer mode (no controlSend), the timeout overlay is disabled.
		if m.controlSend == nil {
			break
		}
		m.timeoutOverlay = true
		m.timeoutInput = ""
		m.timeoutError = ""

	case "?":
		m.helpOverlay = true
	}

	return m, nil
}

// handleTimeoutKey handles raw key input while the timeout overlay is open.
// Mirrors browser.handleSearchKey for the input pattern.
func (m Model) handleTimeoutKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.timeoutOverlay = false
		m.timeoutInput = ""
		m.timeoutError = ""
		return m, nil
	case tea.KeyEnter:
		return m.submitTimeoutInput()
	case tea.KeyBackspace, tea.KeyCtrlH:
		runes := []rune(m.timeoutInput)
		if len(runes) > 0 {
			m.timeoutInput = string(runes[:len(runes)-1])
		}
		m.timeoutError = ""
		return m, nil
	case tea.KeyCtrlU:
		m.timeoutInput = ""
		m.timeoutError = ""
		return m, nil
	case tea.KeyRunes:
		if len(msg.Runes) > 0 {
			m.timeoutInput += string(msg.Runes)
			m.timeoutError = ""
		}
		return m, nil
	case tea.KeySpace:
		m.timeoutInput += " "
		m.timeoutError = ""
		return m, nil
	}
	return m, nil
}

// submitTimeoutInput parses the timeout input and, on success, sends a
// ControlSetTimeout message to the runner. On parse failure the overlay
// stays open with an error message.
func (m Model) submitTimeoutInput() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.timeoutInput)
	// Empty input = no change; close overlay.
	if input == "" {
		m.timeoutOverlay = false
		m.timeoutError = ""
		return m, nil
	}

	var newTimeout *time.Duration
	switch input {
	case "default":
		newTimeout = nil
	case "0":
		zero := time.Duration(0)
		newTimeout = &zero
	default:
		d, err := time.ParseDuration(input)
		if err != nil {
			m.timeoutError = err.Error()
			return m, nil
		}
		if d < 0 {
			m.timeoutError = "duration must be non-negative"
			return m, nil
		}
		newTimeout = &d
	}

	// Send to runner non-blocking so a stalled runner cannot lock the TUI.
	if m.controlSend != nil {
		select {
		case m.controlSend <- runner.ControlMsg{Kind: runner.ControlSetTimeout, Timeout: newTimeout}:
		default:
		}
	}

	m.currentTimeout = newTimeout
	m.timeoutOverlay = false
	m.timeoutInput = ""
	m.timeoutError = ""
	return m, nil
}

func (m *Model) ensureStreamCursorVisible() {
	streamH := m.paneHeight() - 1
	if streamH <= 0 {
		return
	}
	if m.cursor < m.streamScroll {
		m.streamScroll = m.cursor
	}
	if m.cursor >= m.streamScroll+streamH {
		m.streamScroll = m.cursor - streamH + 1
	}
}

// Layout dimension helpers.

func (m Model) usableHeight() int {
	return m.height - 4 // 1 header + 1 status + 2 main view borders
}

func (m Model) mainHeight() int {
	h := int(float64(m.usableHeight()) * 0.6)
	if h < 5 {
		h = 5
	}
	return h
}

func (m Model) bottomHeight() int {
	h := m.usableHeight() - m.mainHeight()
	if h < 5 {
		h = 5
	}
	return h
}

func (m Model) streamWidth() int {
	w := int(float64(m.width) * m.paneRatio)
	if w < 16 {
		w = 16
	}
	return w
}

func (m Model) detailWidth() int {
	w := m.width - m.streamWidth()
	if w < 30 {
		w = 30
	}
	return w
}

func (m Model) paneHeight() int {
	h := m.bottomHeight() - 2 // account for borders
	if h < 3 {
		h = 3
	}
	return h
}

// View implements tea.Model.
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing..."
	}

	if m.helpOverlay {
		return m.renderHelpOverlay()
	}

	if m.memoryOverlay {
		return m.renderMemoryOverlay()
	}

	if m.promptOverlay {
		return m.renderPromptOverlay()
	}

	if m.timeoutOverlay {
		return m.renderTimeoutOverlay()
	}

	if m.errorOverlay != "" {
		return m.renderErrorOverlay()
	}

	headerBar := m.renderHeader()
	mainView := m.renderMain()
	streamView := m.renderStream()
	detailView := m.renderDetail()
	statusBar := m.renderStatus()

	bottomRow := lipgloss.JoinHorizontal(lipgloss.Top, streamView, detailView)
	return lipgloss.JoinVertical(lipgloss.Left, headerBar, mainView, bottomRow, statusBar)
}

// scrollIndicator returns a vim-style scroll position string:
// "" if everything fits, "Top", "Bot", or "N%".
func scrollIndicator(scroll, visibleLines, totalLines int) string {
	if totalLines <= visibleLines {
		return ""
	}
	if scroll == 0 {
		return "Top"
	}
	maxScroll := totalLines - visibleLines
	if scroll >= maxScroll {
		return "Bot"
	}
	pct := scroll * 100 / totalLines
	return fmt.Sprintf("%d%%", pct)
}

func (m Model) renderMain() string {
	w := m.width
	ph := m.mainHeight()
	contentWidth := w - 4 // inside borders + padding

	// Ensure block layouts and line index are up to date.
	m.ensureMainLayout(contentWidth)

	visibleLines := ph - 1 // minus title line
	totalLines := m.mainTotalLines

	maxScroll := totalLines - visibleLines
	if maxScroll < 0 {
		maxScroll = 0
	}

	scroll := m.mainScroll
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}

	viewStart := scroll
	viewEnd := viewStart + visibleLines
	if viewEnd > totalLines {
		viewEnd = totalLines
	}

	// Collect only the visible lines from cached block layouts.
	lines := m.collectViewportLines(viewStart, viewEnd, contentWidth)

	// Pad remaining.
	for len(lines) < visibleLines {
		lines = append(lines, "")
	}

	displayContent := strings.Join(lines, "\n")

	if len(m.blocks) == 0 {
		msg := lipgloss.NewStyle().Foreground(colorDim).Render("Waiting for agent output…")
		displayContent = lipgloss.Place(contentWidth, visibleLines, lipgloss.Center, lipgloss.Center, msg)
	}

	title := " LIVE "
	if m.mainAutoScroll && totalLines > visibleLines {
		title = " LIVE [AUTO] "
	} else if ind := scrollIndicator(scroll, visibleLines, totalLines); ind != "" {
		title = fmt.Sprintf(" LIVE %s ", ind)
	}

	border := focusedBorder
	ts := focusedTitleStyle
	if m.focusedPane != 0 {
		border = unfocusedBorder
		ts = titleStyle
	}

	return border.
		Width(w - 2).
		Height(ph).
		Render(ts.Render(title) + "\n" + displayContent)
}

func (m Model) renderHeader() string {
	maxWidth := m.width - 2 // account for headerStyle Padding(0,1)
	if maxWidth < 10 {
		maxWidth = 10
	}

	var parts []string

	if m.running {
		parts = append(parts, "●")
	}
	parts = append(parts, "ralfinho")

	sep := " │ "

	// Build optional segments, only adding them if they fit.
	var optional []string
	if m.agentName != "" {
		optional = append(optional, m.agentName)
	}
	if m.iteration > 0 {
		optional = append(optional, fmt.Sprintf("Iteration #%d", m.iteration))
	}
	if m.modelName != "" {
		optional = append(optional, m.modelName)
	}
	if m.running && !m.startTime.IsZero() {
		optional = append(optional, formatElapsed(time.Since(m.startTime)))
	}

	bar := strings.Join(parts, sep)
	for _, seg := range optional {
		candidate := bar + sep + seg
		if lipgloss.Width(candidate) <= maxWidth {
			bar = candidate
		}
	}

	return headerStyle.Width(m.width).Render(bar)
}

func formatElapsed(d time.Duration) string {
	totalSecs := int(d.Truncate(time.Second).Seconds())
	switch {
	case totalSecs < 60:
		return fmt.Sprintf("%ds", totalSecs)
	case totalSecs < 3600:
		return fmt.Sprintf("%dm %ds", totalSecs/60, totalSecs%60)
	default:
		return fmt.Sprintf("%dh %dm", totalSecs/3600, (totalSecs%3600)/60)
	}
}

func (m Model) renderStream() string {
	sw := m.streamWidth()
	ph := m.paneHeight()
	contentWidth := sw - 2 // inside borders

	indicatorWidth := lipgloss.Width(selectedIndicator.Render("▌"))
	lineWidth := contentWidth - indicatorWidth
	if lineWidth < 1 {
		lineWidth = 1
	}

	visibleLines := ph - 1 // minus title line

	var lines []string
	for i := m.streamScroll; i < len(m.events) && i < m.streamScroll+visibleLines; i++ {
		ev := m.events[i]
		line := ev.Summary
		if lineWidth > 0 && lipgloss.Width(line) > lineWidth {
			line = truncateToWidth(line, lineWidth)
		}

		// Pad to fill width.
		if lw := lipgloss.Width(line); lw < lineWidth {
			line = line + strings.Repeat(" ", lineWidth-lw)
		}

		style := eventStyle(ev.Type)
		// Tool errors get special coloring.
		if ev.Type == DisplayToolEnd && strings.HasPrefix(ev.Summary, "!") {
			style = errorEventStyle
		}

		if i == m.cursor {
			lines = append(lines, selectedIndicator.Render("▌")+selectedStyle.Render(line))
		} else {
			lines = append(lines, " "+style.Render(line))
		}
	}

	// Pad remaining lines if not enough events.
	for len(lines) < visibleLines {
		lines = append(lines, strings.Repeat(" ", contentWidth))
	}

	content := strings.Join(lines, "\n")

	title := fmt.Sprintf(" STREAM (%d) ", len(m.events))
	border := focusedBorder
	ts := focusedTitleStyle
	if m.focusedPane != 1 {
		border = unfocusedBorder
		ts = titleStyle
	}

	return border.
		Width(sw - 2).
		Height(ph).
		Render(ts.Render(title) + "\n" + content)
}

func (m Model) renderDetail() string {
	dw := m.detailWidth()
	ph := m.paneHeight()
	contentWidth := dw - 2 // inside borders

	var content string

	if m.cursor >= 0 && m.cursor < len(m.events) {
		ev := m.events[m.cursor]
		if m.rawMode {
			content = fmt.Sprintf("Type: %s\nTime: %s\nIteration: %d\n\n%s",
				ev.Type, ev.Timestamp.Format("15:04:05"), ev.Iteration, ev.Detail)
			content = WrapText(content, contentWidth)
		} else if ev.Type == DisplayAssistantText && ev.Detail != "" {
			content = renderAssistantContent(ev.Detail, contentWidth, ev.AssistantFinal)
		} else {
			content = WrapText(ev.Detail, contentWidth)
		}
	}

	visibleLines := ph - 1
	var displayContent string
	title := " DETAIL "

	if content == "" {
		hint := lipgloss.NewStyle().Foreground(colorDim).Render("Select an event to see details")
		displayContent = lipgloss.Place(contentWidth, visibleLines, lipgloss.Center, lipgloss.Center, hint)
	} else {
		// Split into lines and apply scroll.
		allLines := strings.Split(content, "\n")
		totalLines := len(allLines)

		maxScroll := totalLines - visibleLines
		if maxScroll < 0 {
			maxScroll = 0
		}

		scroll := m.detailScroll
		if scroll > maxScroll {
			scroll = maxScroll
		}

		start := scroll
		end := start + visibleLines
		if end > totalLines {
			end = totalLines
		}

		var lines []string
		for i := start; i < end; i++ {
			lines = append(lines, clipToWidth(allLines[i], contentWidth))
		}

		// Pad.
		for len(lines) < visibleLines {
			lines = append(lines, "")
		}

		displayContent = strings.Join(lines, "\n")

		if ind := scrollIndicator(scroll, visibleLines, totalLines); ind != "" {
			title = fmt.Sprintf(" DETAIL %s ", ind)
		}
	}

	border := focusedBorder
	ts := focusedTitleStyle
	if m.focusedPane != 2 {
		border = unfocusedBorder
		ts = titleStyle
	}

	return border.
		Width(dw - 2).
		Height(ph).
		Render(ts.Render(title) + "\n" + displayContent)
}

func (m Model) renderStatus() string {
	if m.confirmQuit {
		var bar string
		if m.confirmCtrlC {
			bar = "Press Ctrl+C again to quit"
		} else {
			bar = "Press q again to quit"
		}
		return statusBarStyle.Width(m.width).Render(bar)
	}

	maxWidth := m.width - 2 // account for statusBarStyle Padding(0,1)
	if maxWidth < 10 {
		maxWidth = 10
	}

	left := m.status
	if m.running {
		left = "Running │ " + left
		// Show inactivity indicator when no events received for >30s.
		if !m.lastEventTime.IsZero() {
			idle := time.Since(m.lastEventTime)
			if idle > 30*time.Second {
				left += fmt.Sprintf(" (no activity for %ds)", int(idle.Seconds()))
			}
		}
	}

	modeStr := "rendered"
	if m.rawMode {
		modeStr = "raw"
	}

	sep := statusSepStyle.Render(" │ ")
	right := statusKeyStyle.Render("↑↓") + ":nav" +
		sep + statusKeyStyle.Render("Tab") + ":pane" +
		sep + statusKeyStyle.Render("r") + ":" + modeStr +
		sep + statusKeyStyle.Render("t") + ":" + timeoutSegmentValue(m.currentTimeout) +
		sep + statusKeyStyle.Render("p") + ":prompt" +
		sep + statusKeyStyle.Render("n") + ":memory" +
		sep + statusKeyStyle.Render("?") + ":help" +
		sep + statusKeyStyle.Render("q") + ":quit"

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)

	// If left + right won't fit, drop the right side progressively.
	if leftW+1+rightW > maxWidth {
		// Try shorter right: just "q:quit"
		right = statusKeyStyle.Render("q") + ":quit"
		rightW = lipgloss.Width(right)
	}
	if leftW+1+rightW > maxWidth {
		// Drop right entirely.
		right = ""
		rightW = 0
	}

	// Truncate left if still too wide.
	if leftW > maxWidth-rightW-1 && rightW > 0 {
		left = truncateToWidth(left, maxWidth-rightW-1)
		leftW = lipgloss.Width(left)
	} else if rightW == 0 && leftW > maxWidth {
		left = truncateToWidth(left, maxWidth)
		leftW = lipgloss.Width(left)
	}

	gap := maxWidth - leftW - rightW
	if gap < 1 {
		gap = 1
	}

	bar := left + strings.Repeat(" ", gap) + right
	return statusBarStyle.Width(m.width).Render(bar)
}

// clipToWidth truncates s so its visual width does not exceed maxW columns.
// No suffix is appended; the string is simply clipped.
func clipToWidth(s string, maxW int) string {
	if lipgloss.Width(s) <= maxW {
		return s
	}
	var b strings.Builder
	b.Grow(len(s)) // at most as long as the original
	w := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if w+rw > maxW {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String()
}

// truncateToWidth truncates a string to fit within maxW visual columns,
// appending "..." when truncation occurs.
func truncateToWidth(s string, maxW int) string {
	if maxW < 4 {
		maxW = 4
	}
	if lipgloss.Width(s) <= maxW {
		return s
	}
	return clipToWidth(s, maxW-3) + "..."
}

// renderHelpOverlay renders a centered keybinding reference card.
func (m Model) renderHelpOverlay() string {
	maxWidth := min(m.width*7/10, 60)

	body := "" +
		"Navigation\n" +
		"  j/k, ↑/↓     Scroll / move cursor\n" +
		"  g / G         Jump to top / bottom\n" +
		"  Ctrl+d/u      Half-page scroll\n" +
		"  Tab           Cycle pane focus\n" +
		"\n" +
		"View\n" +
		"  r             Toggle raw / rendered\n" +
		"  p             Show effective prompt\n" +
		"  n             Show memory files (NOTES / PROGRESS)\n" +
		"\n" +
		"Control\n" +
		"  t             Set inactivity timeout\n" +
		"\n" +
		"Other\n" +
		"  q             Quit (press again to confirm)\n" +
		"  Ctrl+C        Quit (press again to confirm)\n" +
		"  ?             Toggle this help"

	title := browserCardTitle.Render("Keybindings")
	hint := dismissHintStyle.Render("?/q/Esc:close")

	content := title + "\n\n" + body + "\n\n" + hint
	card := browserCardBorder.Width(maxWidth).Render(content)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, card,
		lipgloss.WithWhitespaceChars(" "),
	)
}

// overlayContent holds the varying parts of a scrollable overlay card.
type overlayContent struct {
	body          string         // raw text (will be word-wrapped)
	scroll        int            // current scroll offset
	reservedLines int            // lines reserved for chrome (title, hint, borders, etc.)
	title         string         // title label
	titleStyle    lipgloss.Style // style for the title
	hint          string         // hint text at the bottom
	cardBorder    lipgloss.Style // card border style
	extraHeader   string         // optional content between title and body (e.g., tab bar)
}

// renderOverlayCard renders a scrollable overlay card centered on screen.
func (m Model) renderOverlayCard(oc overlayContent) string {
	maxWidth := min(m.width*7/10, 80)
	maxHeight := m.height * 6 / 10

	innerWidth := maxWidth - 4 // 2 border + 2 padding
	if innerWidth < 20 {
		innerWidth = 20
	}
	body := WrapText(oc.body, innerWidth)

	lines := strings.Split(body, "\n")
	totalLines := len(lines)

	visibleLines := maxHeight - oc.reservedLines
	if visibleLines < 3 {
		visibleLines = 3
	}

	scroll := oc.scroll
	maxScroll := totalLines - visibleLines
	if maxScroll < 0 {
		maxScroll = 0
	}
	if scroll > maxScroll {
		scroll = maxScroll
	}

	end := scroll + visibleLines
	if end > totalLines {
		end = totalLines
	}
	body = strings.Join(lines[scroll:end], "\n")

	titleText := oc.title
	if ind := scrollIndicator(scroll, visibleLines, totalLines); ind != "" {
		titleText = fmt.Sprintf("%s %s", oc.title, ind)
	}
	title := oc.titleStyle.Render(titleText)
	hint := dismissHintStyle.Render(oc.hint)

	content := title + "\n\n"
	if oc.extraHeader != "" {
		content += oc.extraHeader + "\n\n"
	}
	content += body + "\n\n" + hint
	card := oc.cardBorder.Width(maxWidth).Render(content)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, card,
		lipgloss.WithWhitespaceChars(" "),
	)
}

// renderPromptOverlay renders the effective prompt text as a centered modal
// card with j/k scrolling.
func (m Model) renderPromptOverlay() string {
	return m.renderOverlayCard(overlayContent{
		body:          m.promptText,
		scroll:        m.promptOverlayScroll,
		reservedLines: 6,
		title:         "Effective Prompt",
		titleStyle:    browserCardTitle,
		hint:          "p/Esc:close  j/k:scroll",
		cardBorder:    browserCardBorder,
	})
}

// renderMemoryOverlay renders the NOTES/PROGRESS memory files as a centered
// modal card with tab switching and j/k scrolling.
func (m Model) renderMemoryOverlay() string {
	tabNames := [2]string{"NOTES", "PROGRESS"}
	paths := [2]string{m.notesPath, m.progressPath}
	filePath := paths[m.memoryOverlayTab]

	var body string
	if filePath == "" {
		body = lipgloss.NewStyle().Faint(true).Render("(no path configured)")
	} else if data, err := os.ReadFile(filePath); err != nil {
		body = lipgloss.NewStyle().Faint(true).Render("(file not found)")
	} else if len(data) == 0 {
		body = lipgloss.NewStyle().Faint(true).Render("(empty)")
	} else {
		body = string(data)
	}

	var tabs []string
	for i, name := range tabNames {
		if i == m.memoryOverlayTab {
			tabs = append(tabs, lipgloss.NewStyle().Bold(true).Foreground(colorBright).Render("["+name+"]"))
		} else {
			tabs = append(tabs, lipgloss.NewStyle().Faint(true).Render(" "+name+" "))
		}
	}

	return m.renderOverlayCard(overlayContent{
		body:          body,
		scroll:        m.memoryOverlayScroll,
		reservedLines: 8,
		title:         "Memory Files",
		titleStyle:    browserCardTitle,
		hint:          "n/Esc:close  Tab:switch  j/k:scroll",
		cardBorder:    browserCardBorder,
		extraHeader:   strings.Join(tabs, "  "),
	})
}

// renderErrorOverlay renders the error text as a centered modal card.
// Supports j/k scrolling; any other key dismisses.
func (m Model) renderErrorOverlay() string {
	return m.renderOverlayCard(overlayContent{
		body:          m.errorOverlay,
		scroll:        m.errorOverlayScroll,
		reservedLines: 6,
		title:         "Error",
		titleStyle:    browserCardTitleWarning,
		hint:          "j/k:scroll  any key:dismiss",
		cardBorder:    browserCardBorderWarning,
	})
}

// renderTimeoutOverlay renders the inactivity timeout input overlay as a
// centered modal card with a single-line input field and hint text.
func (m Model) renderTimeoutOverlay() string {
	current := formatTimeoutValue(m.currentTimeout)
	body := fmt.Sprintf("Current: %s\n\n> %s_\n\n(e.g. 10m, 1h, 0=disable, default=reset)", current, m.timeoutInput)
	if m.timeoutError != "" {
		body += "\n\nError: " + m.timeoutError
	}
	return m.renderOverlayCard(overlayContent{
		body:          body,
		scroll:        0,
		reservedLines: 6,
		title:         "Set Inactivity Timeout",
		titleStyle:    browserCardTitle,
		hint:          "Enter:apply  Esc:cancel",
		cardBorder:    browserCardBorder,
	})
}

// formatTimeoutValue renders a timeout pointer for display in the overlay:
// nil → "default", pointer-to-0 → "disabled", positive → compact duration.
func formatTimeoutValue(d *time.Duration) string {
	if d == nil {
		return "default"
	}
	if *d == 0 {
		return "disabled"
	}
	return compactDuration(*d)
}

// timeoutSegmentValue returns the status-bar value portion of the timeout
// segment: "def" (default), "off" (disabled), or a compact duration string
// like "5m". The status bar prepends "t:" via styled key glyph + ":" +
// this value.
func timeoutSegmentValue(d *time.Duration) string {
	if d == nil {
		return "def"
	}
	if *d == 0 {
		return "off"
	}
	return compactDuration(*d)
}

// compactDuration formats a positive duration with whole-unit suppression:
// 5m0s → "5m", 1h0m0s → "1h", 1h30m0s → "1h30m". Sub-second precision falls
// back to time.Duration's String().
func compactDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d%time.Second != 0 {
		return d.String()
	}
	h := int(d / time.Hour)
	d -= time.Duration(h) * time.Hour
	mins := int(d / time.Minute)
	d -= time.Duration(mins) * time.Minute
	secs := int(d / time.Second)
	switch {
	case h > 0 && mins == 0 && secs == 0:
		return fmt.Sprintf("%dh", h)
	case h > 0 && secs == 0:
		return fmt.Sprintf("%dh%dm", h, mins)
	case h > 0:
		return fmt.Sprintf("%dh%dm%ds", h, mins, secs)
	case mins > 0 && secs == 0:
		return fmt.Sprintf("%dm", mins)
	case mins > 0:
		return fmt.Sprintf("%dm%ds", mins, secs)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}
