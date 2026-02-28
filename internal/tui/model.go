package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	runewidth "github.com/mattn/go-runewidth"
	"github.com/dorayaki-do/ralfinho/internal/runner"
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
	paneRatio    float64 // fraction of height for top pane
	focusedPane  int     // 0=stream, 1=detail
	rawMode      bool    // show raw detail vs rendered
	running      bool    // agent still running
	status       string  // status bar text
	eventCh      <-chan runner.Event
	converter    *EventConverter
	autoScroll   bool // auto-scroll stream when new events arrive
}

// NewModel creates a TUI model that reads runner events from ch.
func NewModel(ch <-chan runner.Event) Model {
	return Model{
		paneRatio:  0.5,
		running:    true,
		status:     "Starting...",
		eventCh:    ch,
		converter:  NewEventConverter(),
		autoScroll: true,
	}
}

// NewViewerModel creates a read-only TUI model pre-loaded with events.
// It is used for replaying a saved run — no event channel, not running.
func NewViewerModel(events []DisplayEvent, meta runner.RunMeta) Model {
	status := fmt.Sprintf("Run %s | %s | %s | %d iterations",
		shortID(meta.RunID), meta.Status, meta.StartedAt, meta.IterationsCompleted)

	m := Model{
		events:     events,
		paneRatio:  0.5,
		running:    false,
		status:     status,
		autoScroll: false,
	}
	return m
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
	return m.waitForEvent()
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Re-init markdown renderer with new width.
		detailWidth := m.width - 4
		if detailWidth < 20 {
			detailWidth = 20
		}
		initRenderer(detailWidth)
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
		return m, nil
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
	// For assistant_text updates, merge with the last assistant_text event.
	if de.Type == "assistant_text" && len(m.events) > 0 {
		last := &m.events[len(m.events)-1]
		if last.Type == "assistant_text" && last.Iteration == de.Iteration {
			last.Summary = de.Summary
			last.Detail = de.Detail
			last.Timestamp = de.Timestamp
			return m, nil
		}
	}

	m.events = append(m.events, de)

	// Auto-scroll: if cursor is at/near the bottom, follow new events.
	if m.autoScroll {
		m.cursor = len(m.events) - 1
		m.detailScroll = 0
		m.ensureStreamCursorVisible()
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {

	case "q", "ctrl+c":
		return m, tea.Quit

	case "j", "down":
		if m.focusedPane == 0 && len(m.events) > 0 {
			if m.cursor < len(m.events)-1 {
				m.cursor++
				m.detailScroll = 0
			}
			m.autoScroll = m.cursor >= len(m.events)-1
			m.ensureStreamCursorVisible()
		} else if m.focusedPane == 1 {
			m.detailScroll++
		}

	case "k", "up":
		if m.focusedPane == 0 {
			if m.cursor > 0 {
				m.cursor--
				m.detailScroll = 0
			}
			m.autoScroll = false
			m.ensureStreamCursorVisible()
		} else if m.focusedPane == 1 {
			if m.detailScroll > 0 {
				m.detailScroll--
			}
		}

	case "g":
		if m.focusedPane == 0 {
			m.cursor = 0
			m.streamScroll = 0
			m.detailScroll = 0
			m.autoScroll = false
		}

	case "G":
		if m.focusedPane == 0 && len(m.events) > 0 {
			m.cursor = len(m.events) - 1
			m.detailScroll = 0
			m.autoScroll = true
			m.ensureStreamCursorVisible()
		}

	case "ctrl+d":
		pageSize := m.detailHeight() / 2
		if pageSize < 1 {
			pageSize = 1
		}
		m.detailScroll += pageSize

	case "ctrl+u":
		pageSize := m.detailHeight() / 2
		if pageSize < 1 {
			pageSize = 1
		}
		m.detailScroll -= pageSize
		if m.detailScroll < 0 {
			m.detailScroll = 0
		}

	case "tab":
		m.focusedPane = (m.focusedPane + 1) % 2

	case "r":
		m.rawMode = !m.rawMode
	}

	return m, nil
}

func (m *Model) ensureStreamCursorVisible() {
	streamH := m.streamHeight()
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

// Layout dimensions helpers.
func (m Model) streamHeight() int {
	// Top pane content height (minus borders=2, title area is inside border).
	h := int(float64(m.usableHeight()) * m.paneRatio)
	if h < 3 {
		h = 3
	}
	return h - 2 // subtract border lines
}

func (m Model) detailHeight() int {
	h := m.usableHeight() - int(float64(m.usableHeight())*m.paneRatio)
	if h < 3 {
		h = 3
	}
	return h - 2 // subtract border lines
}

func (m Model) usableHeight() int {
	return m.height - 1 // 1 for status bar
}

// View implements tea.Model.
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing..."
	}

	contentWidth := m.width - 2 // border left+right

	streamView := m.renderStream(contentWidth)
	detailView := m.renderDetail(contentWidth)
	statusBar := m.renderStatus()

	return lipgloss.JoinVertical(lipgloss.Left, streamView, detailView, statusBar)
}

func (m Model) renderStream(contentWidth int) string {
	streamH := m.streamHeight()

	var lines []string
	for i := m.streamScroll; i < len(m.events) && i < m.streamScroll+streamH-1; i++ {
		ev := m.events[i]
		line := ev.Summary
		if contentWidth > 0 && lipgloss.Width(line) > contentWidth {
			w := 0
			truncated := ""
			for _, r := range line {
				rw := runewidth.RuneWidth(r)
				if w+rw > contentWidth-3 {
					break
				}
				truncated += string(r)
				w += rw
			}
			line = truncated + "..."
		}

		// Pad to fill width.
		if lw := lipgloss.Width(line); lw < contentWidth {
			line = line + strings.Repeat(" ", contentWidth-lw)
		}

		style := eventStyle(ev.Type)
		// Tool errors get special coloring.
		if ev.Type == "tool_end" && strings.HasPrefix(ev.Summary, "✗") {
			style = errorEventStyle
		}

		if i == m.cursor {
			lines = append(lines, selectedStyle.Render(line))
		} else {
			lines = append(lines, style.Render(line))
		}
	}

	// Pad remaining lines if not enough events.
	for len(lines) < streamH-1 {
		lines = append(lines, strings.Repeat(" ", contentWidth))
	}

	content := strings.Join(lines, "\n")

	title := " Stream "
	border := focusedBorder
	if m.focusedPane != 0 {
		border = unfocusedBorder
	}

	return border.
		Width(m.width - 2).
		Height(streamH).
		Render(titleStyle.Render(title) + "\n" + content)
}

func (m Model) renderDetail(contentWidth int) string {
	detailH := m.detailHeight()

	var content string

	if m.cursor >= 0 && m.cursor < len(m.events) {
		ev := m.events[m.cursor]
		if m.rawMode {
			content = fmt.Sprintf("Type: %s\nTime: %s\nIteration: %d\n\n%s",
				ev.Type, ev.Timestamp.Format("15:04:05"), ev.Iteration, ev.Detail)
		} else if ev.Type == "assistant_text" && ev.Detail != "" {
			content = renderMarkdown(ev.Detail, contentWidth)
		} else {
			content = ev.Detail
		}
	}

	if content == "" {
		content = "(no detail)"
	}

	// Split into lines and apply scroll.
	allLines := strings.Split(content, "\n")
	totalLines := len(allLines)

	// Clamp detailScroll.
	maxScroll := totalLines - (detailH - 1)
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.detailScroll > maxScroll {
		// We can't mutate m here, but the view is best-effort.
		// Just clamp for display.
	}

	scroll := m.detailScroll
	if scroll > maxScroll {
		scroll = maxScroll
	}

	start := scroll
	end := start + detailH - 1
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
	for len(lines) < detailH-1 {
		lines = append(lines, "")
	}

	displayContent := strings.Join(lines, "\n")

	title := " Detail "
	if totalLines > detailH {
		title = fmt.Sprintf(" Detail [%d/%d] ", scroll+1, totalLines)
	}

	border := focusedBorder
	if m.focusedPane != 1 {
		border = unfocusedBorder
	}

	return border.
		Width(m.width - 2).
		Height(detailH).
		Render(titleStyle.Render(title) + "\n" + displayContent)
}

func (m Model) renderStatus() string {
	left := m.status
	if m.running {
		left = "Running | " + left
	}

	modeStr := "rendered"
	if m.rawMode {
		modeStr = "raw"
	}

	right := fmt.Sprintf("↑↓:select  Tab:pane  r:%s  q:quit", modeStr)

	// Pad to fill width.
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}

	bar := left + strings.Repeat(" ", gap) + right
	return statusBarStyle.Width(m.width).Render(bar)
}
