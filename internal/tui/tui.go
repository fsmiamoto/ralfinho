package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"ralfinho/internal/eventlog"
	"ralfinho/internal/runner"
	"ralfinho/internal/runstore"
)

type Mode int

const (
	ModeLive Mode = iota
	ModeView
)

type Focus int

const (
	FocusStream Focus = iota
	FocusDetails
)

type statusLevel int

const (
	statusInfo statusLevel = iota
	statusSuccess
	statusWarn
	statusError
)

type IterationMessage struct {
	Report runner.IterationReport
	Events []eventlog.Event
}

type StreamEventsMessage struct {
	Events []eventlog.Event
}

type ContinuePromptMessage struct{}

type RunFinishedMessage struct {
	Result runner.Result
	Err    error
}

type Model struct {
	mode      Mode
	runID     string
	meta      runstore.Meta
	events    []eventlog.Event
	selected  int
	detailPos int
	focus     Focus
	raw       bool

	width  int
	height int

	running          bool
	awaitingContinue bool
	continueCh       chan<- bool
	interruptCh      chan<- struct{}
	statusLine       string
	statusLevel      statusLevel

	renderer *glamour.TermRenderer
}

func NewLiveModel(runID string, meta runstore.Meta, continueCh chan<- bool, interruptCh chan<- struct{}) *Model {
	return &Model{
		mode:        ModeLive,
		runID:       runID,
		meta:        meta,
		events:      make([]eventlog.Event, 0, 128),
		focus:       FocusStream,
		running:     true,
		continueCh:  continueCh,
		interruptCh: interruptCh,
		statusLine:  "Running... Ctrl+C to interrupt.",
		statusLevel: statusInfo,
	}
}

func NewViewModel(runID string, meta runstore.Meta, events []eventlog.Event) *Model {
	return &Model{
		mode:        ModeView,
		runID:       runID,
		meta:        meta,
		events:      events,
		focus:       FocusStream,
		statusLine:  "Read-only viewer. q to quit.",
		statusLevel: statusInfo,
	}
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.height < 6 {
			m.height = 6
		}
		return m, nil
	case StreamEventsMessage:
		m.events = append(m.events, msg.Events...)
		if len(m.events) > 0 {
			m.selected = len(m.events) - 1
		}
		m.clamp()
		return m, nil
	case IterationMessage:
		m.events = append(m.events, msg.Events...)
		if len(m.events) > 0 {
			m.selected = len(m.events) - 1
		}
		if msg.Report.Interrupted {
			m.statusLine = fmt.Sprintf("Iteration %d interrupted.", msg.Report.Iteration)
			m.statusLevel = statusWarn
		} else if msg.Report.Err != nil {
			m.statusLine = fmt.Sprintf("Iteration %d failed: %v", msg.Report.Iteration, msg.Report.Err)
			m.statusLevel = statusError
		} else {
			m.statusLine = fmt.Sprintf("Iteration %d finished (%d new events).", msg.Report.Iteration, len(msg.Events))
			m.statusLevel = statusSuccess
		}
		m.clamp()
		return m, nil
	case ContinuePromptMessage:
		m.awaitingContinue = true
		m.statusLine = "Continue to next iteration? [y/n]"
		m.statusLevel = statusWarn
		return m, nil
	case RunFinishedMessage:
		m.running = false
		if msg.Err != nil {
			m.meta.Status = string(runner.StatusFailed)
			m.statusLine = fmt.Sprintf("Run failed: %v (q to quit)", msg.Err)
			m.statusLevel = statusError
		} else {
			m.meta.Status = string(msg.Result.Status)
			m.statusLine = fmt.Sprintf("Run finished with status: %s (q to quit)", msg.Result.Status)
			m.statusLevel = runStatusLevel(m.meta.Status)
		}
		return m, nil
	case tea.KeyMsg:
		if m.awaitingContinue {
			switch strings.ToLower(msg.String()) {
			case "y":
				m.awaitingContinue = false
				m.statusLine = "Continuing..."
				m.statusLevel = statusInfo
				select {
				case m.continueCh <- true:
				default:
				}
			case "n":
				m.awaitingContinue = false
				m.statusLine = "Stopping..."
				m.statusLevel = statusWarn
				select {
				case m.continueCh <- false:
				default:
				}
			}
			return m, nil
		}

		switch msg.String() {
		case "ctrl+c":
			if m.mode == ModeLive && m.running {
				select {
				case m.interruptCh <- struct{}{}:
				default:
				}
				m.statusLine = "Interrupt requested."
				m.statusLevel = statusWarn
				return m, nil
			}
			return m, tea.Quit
		case "q":
			if m.mode == ModeLive && m.running {
				m.statusLine = "Run active. Use Ctrl+C to interrupt first."
				m.statusLevel = statusWarn
				return m, nil
			}
			return m, tea.Quit
		case "tab":
			if m.focus == FocusStream {
				m.focus = FocusDetails
			} else {
				m.focus = FocusStream
			}
		case "r":
			m.raw = !m.raw
		case "g":
			if m.focus == FocusStream {
				m.selected = 0
			} else {
				m.detailPos = 0
			}
		case "G":
			if m.focus == FocusStream {
				m.selected = len(m.events) - 1
			} else {
				m.detailPos = len(m.detailLines())
			}
		case "j", "down":
			if m.focus == FocusStream {
				m.selected++
			} else {
				m.detailPos++
			}
		case "k", "up":
			if m.focus == FocusStream {
				m.selected--
			} else {
				m.detailPos--
			}
		case "ctrl+d":
			m.detailPos += m.detailsHeight() / 2
		case "ctrl+u":
			m.detailPos -= m.detailsHeight() / 2
		}
		m.clamp()
		return m, nil
	}

	return m, nil
}

func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Loading TUI..."
	}

	headerText := fmt.Sprintf("ralfinho run=%s status=%s", m.runID, m.meta.Status)
	header := headerStyleForRunStatus(m.meta.Status).Render(headerText)
	streamW := max(24, m.width/3)
	detailW := m.width - streamW - 1
	bodyH := m.height - 3
	if bodyH < 3 {
		bodyH = 3
	}
	stream := m.renderStream(streamW, bodyH)
	details := m.renderDetails(detailW, bodyH)
	body := lipgloss.JoinHorizontal(lipgloss.Top, stream, details)
	status := statusStyleFor(m.statusLevel).Render(m.statusLine)
	return lipgloss.JoinVertical(lipgloss.Left, header, body, status)
}

func (m *Model) renderStream(w, h int) string {
	border := lipgloss.NormalBorder()
	style := lipgloss.NewStyle().Width(w).Height(h).Border(border).Padding(0, 1)
	if m.focus == FocusStream {
		style = style.BorderForeground(lipgloss.Color("63"))
	}

	if len(m.events) == 0 {
		return style.Render("No events yet.")
	}

	start := m.selected - h/2
	if start < 0 {
		start = 0
	}
	end := start + h
	if end > len(m.events) {
		end = len(m.events)
		start = max(0, end-h)
	}

	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		prefix := "  "
		if i == m.selected {
			prefix = "> "
		}
		line := prefix + summarizeEvent(m.events[i])
		hasError := isErrorEvent(m.events[i])
		switch {
		case i == m.selected && hasError:
			line = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196")).Render(line)
		case i == m.selected:
			line = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("45")).Render(line)
		case hasError:
			line = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(line)
		}
		lines = append(lines, line)
	}
	return style.Render(strings.Join(lines, "\n"))
}

func (m *Model) renderDetails(w, h int) string {
	border := lipgloss.NormalBorder()
	style := lipgloss.NewStyle().Width(w).Height(h).Border(border).Padding(0, 1)
	if m.focus == FocusDetails {
		style = style.BorderForeground(lipgloss.Color("63"))
	}
	if len(m.events) == 0 {
		return style.Render("No event selected.")
	}

	lines := m.detailLines()
	if len(lines) == 0 {
		return style.Render("")
	}

	if m.detailPos < 0 {
		m.detailPos = 0
	}
	if m.detailPos >= len(lines) {
		m.detailPos = len(lines) - 1
	}

	maxLines := h
	if m.detailPos+maxLines > len(lines) {
		m.detailPos = max(0, len(lines)-maxLines)
	}
	end := m.detailPos + maxLines
	if end > len(lines) {
		end = len(lines)
	}

	return style.Render(strings.Join(lines[m.detailPos:end], "\n"))
}

func (m *Model) detailLines() []string {
	if len(m.events) == 0 {
		return nil
	}
	ev := m.events[m.selected]

	var text string
	if m.raw {
		if len(ev.Raw) > 0 {
			text = string(ev.Raw)
		} else {
			text = ev.Content
		}
	} else {
		if ev.Role == "assistant" {
			text = m.renderMarkdown(ev.Content)
		} else if len(ev.Raw) > 0 {
			text = string(ev.Raw)
		} else {
			text = ev.Content
		}
	}
	if text == "" {
		text = "(empty)"
	}
	return strings.Split(text, "\n")
}

func (m *Model) renderMarkdown(in string) string {
	if in == "" {
		return ""
	}
	if m.renderer == nil {
		r, err := glamour.NewTermRenderer(glamour.WithAutoStyle())
		if err == nil {
			m.renderer = r
		}
	}
	if m.renderer == nil {
		return in
	}
	out, err := m.renderer.Render(in)
	if err != nil {
		return in
	}
	return out
}

func (m *Model) clamp() {
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= len(m.events) && len(m.events) > 0 {
		m.selected = len(m.events) - 1
	}
	if m.detailPos < 0 {
		m.detailPos = 0
	}
	maxLines := len(m.detailLines())
	if maxLines > 0 && m.detailPos >= maxLines {
		m.detailPos = maxLines - 1
	}
}

func (m *Model) detailsHeight() int {
	h := m.height - 3
	if h < 3 {
		return 3
	}
	return h
}

func summarizeEvent(ev eventlog.Event) string {
	parts := []string{ev.Type}
	if isErrorEvent(ev) {
		parts = append(parts, "level=error")
	}
	if ev.ToolName != "" {
		parts = append(parts, "tool="+ev.ToolName)
	}
	if ev.Role != "" {
		parts = append(parts, "role="+ev.Role)
	}
	if ev.Content != "" {
		c := strings.ReplaceAll(ev.Content, "\n", " ")
		if len(c) > 48 {
			c = c[:48] + "..."
		}
		parts = append(parts, c)
	}
	return strings.Join(parts, " | ")
}

func statusStyleFor(level statusLevel) lipgloss.Style {
	switch level {
	case statusSuccess:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	case statusWarn:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	case statusError:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	}
}

func headerStyleForRunStatus(status string) lipgloss.Style {
	base := lipgloss.NewStyle().Bold(true)
	switch runStatusLevel(status) {
	case statusSuccess:
		return base.Foreground(lipgloss.Color("42"))
	case statusWarn:
		return base.Foreground(lipgloss.Color("220"))
	case statusError:
		return base.Foreground(lipgloss.Color("196"))
	default:
		return base.Foreground(lipgloss.Color("45"))
	}
}

func runStatusLevel(status string) statusLevel {
	switch status {
	case string(runner.StatusCompleted):
		return statusSuccess
	case string(runner.StatusInterrupted), string(runner.StatusMaxIterationsReached):
		return statusWarn
	case string(runner.StatusFailed):
		return statusError
	default:
		return statusInfo
	}
}

func isErrorEvent(ev eventlog.Event) bool {
	check := strings.ToLower(strings.Join([]string{ev.Type, ev.ToolName, ev.Content}, " "))
	for _, marker := range []string{"error", "fail", "panic", "fatal", "exception"} {
		if strings.Contains(check, marker) {
			return true
		}
	}
	return false
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
