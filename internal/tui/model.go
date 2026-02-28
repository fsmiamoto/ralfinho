package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	runewidth "github.com/mattn/go-runewidth"
	"github.com/fsmiamoto/ralfinho/internal/runner"
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
	result       *runner.RunResult
	spinner      spinner.Model
	startTime    time.Time
	modelName    string
	iteration    int // current iteration count for header display

	// Main view (top pane) state.
	blocks         []MainBlock // ordered content blocks for the main view
	mainScroll     int         // scroll offset in main view (line-based)
	mainAutoScroll bool        // auto-follow new content (default true)
	activeToolIdx  int         // index of in-progress tool block in blocks (-1 = none)
}

// NewModel creates a TUI model that reads runner events from ch.
func NewModel(ch <-chan runner.Event) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	return Model{
		paneRatio:      0.3,
		running:        true,
		status:         "Starting...",
		eventCh:        ch,
		converter:      NewEventConverter(),
		autoScroll:     true,
		mainAutoScroll: true,
		activeToolIdx:  -1,
		spinner:        s,
		startTime:      time.Now(),
	}
}

// NewViewerModel creates a read-only TUI model pre-loaded with events.
// It is used for replaying a saved run — no event channel, not running.
func NewViewerModel(events []DisplayEvent, meta runner.RunMeta) Model {
	status := fmt.Sprintf("Run %s | %s | %s | %d iterations",
		shortID(meta.RunID), meta.Status, meta.StartedAt, meta.IterationsCompleted)

	m := Model{
		events:         events,
		paneRatio:      0.3,
		running:        false,
		status:         status,
		autoScroll:     false,
		mainAutoScroll: false,
		activeToolIdx:  -1,
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

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.waitForEvent(), m.spinner.Tick)
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
		m.status = fmt.Sprintf("Done — %s (%d iterations)", msg.Result.Status, msg.Result.Iterations)
		m.result = &msg.Result
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m Model) handleRawEvent(ev runner.Event) (tea.Model, tea.Cmd) {
	displayEvents := m.converter.Convert(&ev)
	var cmds []tea.Cmd

	for _, de := range displayEvents {
		updated, _ := m.addDisplayEvent(de)
		m = updated.(Model)
	}

	// Continue listening for more events.
	cmds = append(cmds, m.waitForEvent())
	return m, tea.Batch(cmds...)
}

func (m Model) addDisplayEvent(de DisplayEvent) (tea.Model, tea.Cmd) {
	// Update status bar and iteration counter on iteration boundaries.
	if de.Type == "iteration" && m.running {
		m.iteration = de.Iteration
		m.status = fmt.Sprintf("Iteration #%d", de.Iteration)
	}

	// Extract model name from assistant_text summaries like "← Assistant (claude-xxx)".
	if de.Type == "assistant_text" && de.Summary != "" {
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
	if de.Type == "assistant_text" && len(m.events) > 0 {
		last := &m.events[len(m.events)-1]
		if last.Type == "assistant_text" && last.Iteration == de.Iteration {
			last.Summary = de.Summary
			last.Detail = de.Detail
			last.Timestamp = de.Timestamp
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
	case "iteration":
		m.blocks = append(m.blocks, MainBlock{
			Kind:      BlockIteration,
			Iteration: de.Iteration,
		})
	case "assistant_text":
		// Merge with last BlockAssistantText for the same iteration.
		if len(m.blocks) > 0 {
			last := &m.blocks[len(m.blocks)-1]
			if last.Kind == BlockAssistantText && last.Iteration == de.Iteration {
				last.Text = de.Detail
				return
			}
		}
		m.blocks = append(m.blocks, MainBlock{
			Kind:      BlockAssistantText,
			Iteration: de.Iteration,
			Text:      de.Detail,
		})
	case "thinking":
		m.blocks = append(m.blocks, MainBlock{
			Kind:        BlockThinking,
			Iteration:   de.Iteration,
			ThinkingLen: len(de.Detail),
		})
	case "tool_start":
		m.blocks = append(m.blocks, MainBlock{
			Kind:       BlockToolCall,
			Iteration:  de.Iteration,
			ToolName:   de.ToolName,
			ToolCallID: de.ToolCallID,
			ToolArgs:   formatToolArgs(de.ToolName, de.RawArgs),
		})
		m.activeToolIdx = len(m.blocks) - 1
	case "tool_end":
		// Find the matching tool_start block by ToolCallID.
		for i := len(m.blocks) - 1; i >= 0; i-- {
			if m.blocks[i].Kind == BlockToolCall && m.blocks[i].ToolCallID == de.ToolCallID {
				m.blocks[i].ToolDone = true
				m.blocks[i].ToolResult = de.ToolResultText
				m.blocks[i].ToolError = de.ToolIsError
				break
			}
		}
		m.activeToolIdx = -1
	case "info":
		m.blocks = append(m.blocks, MainBlock{
			Kind:     BlockInfo,
			InfoText: de.Detail,
		})
	// user_msg, turn_end, agent_end, session — skip (don't clutter main view)
	}
}

// updateAssistantBlock updates the last assistant text block when streaming.
func (m *Model) updateAssistantBlock(de DisplayEvent) {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		if m.blocks[i].Kind == BlockAssistantText && m.blocks[i].Iteration == de.Iteration {
			m.blocks[i].Text = de.Detail
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
	// Handle quit confirmation state.
	if m.confirmQuit {
		if m.confirmCtrlC && msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if !m.confirmCtrlC && msg.String() == "y" {
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

	case "r":
		m.rawMode = !m.rawMode
	}

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
	return m.height - 2 // 1 for header + 1 for status bar
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

	headerBar := m.renderHeader()
	mainView := m.renderMain()
	streamView := m.renderStream()
	detailView := m.renderDetail()
	statusBar := m.renderStatus()

	bottomRow := lipgloss.JoinHorizontal(lipgloss.Top, streamView, detailView)
	return lipgloss.JoinVertical(lipgloss.Left, headerBar, mainView, bottomRow, statusBar)
}

func (m Model) renderMain() string {
	w := m.width
	ph := m.mainHeight()
	contentWidth := w - 4 // inside borders + padding

	// Build content from blocks.
	var sections []string
	for i := range m.blocks {
		spinnerView := ""
		if i == m.activeToolIdx {
			spinnerView = m.spinner.View()
		}
		rendered := m.blocks[i].Render(contentWidth, spinnerView)
		if rendered != "" {
			sections = append(sections, rendered)
		}
	}
	content := strings.Join(sections, "\n\n")

	// Split into lines and apply scroll.
	var allLines []string
	if content != "" {
		allLines = strings.Split(content, "\n")
	}
	visibleLines := ph - 1 // minus title line

	maxScroll := len(allLines) - visibleLines
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

	start := scroll
	end := start + visibleLines
	if end > len(allLines) {
		end = len(allLines)
	}

	var lines []string
	for i := start; i < end; i++ {
		line := allLines[i]
		if lipgloss.Width(line) > contentWidth {
			w := 0
			truncated := ""
			for _, r := range line {
				rw := runewidth.RuneWidth(r)
				if w+rw > contentWidth {
					break
				}
				truncated += string(r)
				w += rw
			}
			line = truncated
		}
		lines = append(lines, line)
	}

	// Pad remaining.
	for len(lines) < visibleLines {
		lines = append(lines, "")
	}

	displayContent := strings.Join(lines, "\n")

	title := " 📺 Live "
	if len(allLines) > visibleLines {
		title = fmt.Sprintf(" 📺 Live [%d/%d] ", scroll+1, len(allLines))
	}

	border := focusedBorder
	if m.focusedPane != 0 {
		border = unfocusedBorder
	}

	return border.
		Width(w - 2).
		Height(ph).
		Render(titleStyle.Render(title) + "\n" + displayContent)
}

func (m Model) renderHeader() string {
	maxWidth := m.width - 2 // account for headerStyle Padding(0,1)
	if maxWidth < 10 {
		maxWidth = 10
	}

	var parts []string

	if m.running {
		parts = append(parts, m.spinner.View())
	}
	parts = append(parts, "ralfinho")

	sep := " ─── "

	// Build optional segments, only adding them if they fit.
	var optional []string
	if m.iteration > 0 {
		optional = append(optional, fmt.Sprintf("Iteration #%d", m.iteration))
	}
	if m.modelName != "" {
		optional = append(optional, m.modelName)
	}
	if m.running && !m.startTime.IsZero() {
		elapsed := time.Since(m.startTime).Truncate(time.Second)
		mins := int(elapsed.Minutes())
		secs := int(elapsed.Seconds()) % 60
		optional = append(optional, fmt.Sprintf("%dm %ds", mins, secs))
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
			w := 0
			truncated := ""
			for _, r := range line {
				rw := runewidth.RuneWidth(r)
				if w+rw > lineWidth-3 {
					break
				}
				truncated += string(r)
				w += rw
			}
			line = truncated + "..."
		}

		// Pad to fill width.
		if lw := lipgloss.Width(line); lw < lineWidth {
			line = line + strings.Repeat(" ", lineWidth-lw)
		}

		style := eventStyle(ev.Type)
		// Tool errors get special coloring.
		if ev.Type == "tool_end" && strings.HasPrefix(ev.Summary, "✗") {
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

	title := fmt.Sprintf(" 📡 Stream (%d) ", len(m.events))
	border := focusedBorder
	if m.focusedPane != 1 {
		border = unfocusedBorder
	}

	return border.
		Width(sw - 2).
		Height(ph).
		Render(titleStyle.Render(title) + "\n" + content)
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
		} else if ev.Type == "assistant_text" && ev.Detail != "" {
			content = renderMarkdown(ev.Detail, contentWidth)
		} else {
			content = WrapText(ev.Detail, contentWidth)
		}
	}

	if content == "" {
		content = "(no detail)"
	}

	// Split into lines and apply scroll.
	allLines := strings.Split(content, "\n")
	totalLines := len(allLines)
	visibleLines := ph - 1

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
		line := allLines[i]
		if lipgloss.Width(line) > contentWidth {
			w := 0
			truncated := ""
			for _, r := range line {
				rw := runewidth.RuneWidth(r)
				if w+rw > contentWidth {
					break
				}
				truncated += string(r)
				w += rw
			}
			line = truncated
		}
		lines = append(lines, line)
	}

	// Pad.
	for len(lines) < visibleLines {
		lines = append(lines, "")
	}

	displayContent := strings.Join(lines, "\n")

	title := " 📋 Detail "
	if totalLines > visibleLines {
		title = fmt.Sprintf(" 📋 Detail [%d/%d] ", scroll+1, totalLines)
	}

	border := focusedBorder
	if m.focusedPane != 2 {
		border = unfocusedBorder
	}

	return border.
		Width(dw - 2).
		Height(ph).
		Render(titleStyle.Render(title) + "\n" + displayContent)
}

func (m Model) renderStatus() string {
	if m.confirmQuit {
		var bar string
		if m.confirmCtrlC {
			bar = "Press Ctrl+C again to quit, any other key to cancel"
		} else {
			bar = "Quit? Press y to confirm, any other key to cancel"
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
	}

	modeStr := "rendered"
	if m.rawMode {
		modeStr = "raw"
	}

	sep := statusSepStyle.Render(" │ ")
	right := statusKeyStyle.Render("↑↓") + ":nav" +
		sep + statusKeyStyle.Render("Tab") + ":pane" +
		sep + statusKeyStyle.Render("r") + ":" + modeStr +
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

// truncateToWidth truncates a string to fit within maxW visual columns.
func truncateToWidth(s string, maxW int) string {
	if maxW < 4 {
		maxW = 4
	}
	w := 0
	truncated := ""
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if w+rw > maxW-3 {
			return truncated + "..."
		}
		truncated += string(r)
		w += rw
	}
	return s
}
