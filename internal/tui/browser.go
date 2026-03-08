package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	runewidth "github.com/mattn/go-runewidth"

	"github.com/fsmiamoto/ralfinho/internal/viewer"
)

// BrowserAction identifies the action the session browser wants main to run.
type BrowserAction string

const (
	BrowserActionNone   BrowserAction = ""
	BrowserActionOpen   BrowserAction = "open"
	BrowserActionResume BrowserAction = "resume"
	BrowserActionDelete BrowserAction = "delete"
)

// BrowserResult is returned by the browser TUI so main can dispatch actions.
type BrowserResult struct {
	Action BrowserAction
	RunID  string
}

// BrowserModel renders the saved-session browser for `ralfinho view`.
type BrowserModel struct {
	summaries     []viewer.RunSummary
	cursor        int
	scroll        int
	previewScroll int
	width         int
	height        int
	focusedPane   int // 0=sessions, 1=preview
	result        BrowserResult
}

// NewBrowserModel creates a browser over a preloaded in-memory session list.
func NewBrowserModel(summaries []viewer.RunSummary) BrowserModel {
	return BrowserModel{summaries: summaries}
}

// Result returns the action requested by the browser, if any.
func (m BrowserModel) Result() BrowserResult {
	return m.result
}

// Init implements tea.Model.
func (m BrowserModel) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model.
func (m BrowserModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m BrowserModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit

	case "tab":
		m.focusedPane = (m.focusedPane + 1) % 2

	case "j", "down":
		if m.focusedPane == 0 {
			m.moveCursor(1)
		} else {
			m.previewScroll++
			m.clampPreviewScroll()
		}

	case "k", "up":
		if m.focusedPane == 0 {
			m.moveCursor(-1)
		} else if m.previewScroll > 0 {
			m.previewScroll--
		}

	case "g":
		if m.focusedPane == 0 {
			m.cursor = 0
			m.scroll = 0
			m.previewScroll = 0
		} else {
			m.previewScroll = 0
		}

	case "G":
		if m.focusedPane == 0 {
			if len(m.summaries) > 0 {
				m.cursor = len(m.summaries) - 1
				m.ensureCursorVisible()
				m.previewScroll = 0
			}
		} else {
			m.previewScroll = 1 << 30
			m.clampPreviewScroll()
		}

	case "ctrl+d", "pgdown":
		if m.focusedPane == 0 {
			m.moveCursor(m.visibleSessionRows() / 2)
		} else {
			step := m.visiblePreviewLines() / 2
			if step < 1 {
				step = 1
			}
			m.previewScroll += step
			m.clampPreviewScroll()
		}

	case "ctrl+u", "pgup":
		if m.focusedPane == 0 {
			m.moveCursor(-(m.visibleSessionRows() / 2))
		} else {
			step := m.visiblePreviewLines() / 2
			if step < 1 {
				step = 1
			}
			m.previewScroll -= step
			if m.previewScroll < 0 {
				m.previewScroll = 0
			}
		}
	}

	return m, nil
}

func (m *BrowserModel) moveCursor(delta int) {
	if len(m.summaries) == 0 || delta == 0 {
		return
	}

	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.summaries) {
		m.cursor = len(m.summaries) - 1
	}
	m.previewScroll = 0
	m.ensureCursorVisible()
}

func (m *BrowserModel) ensureCursorVisible() {
	visible := m.visibleSessionRows()
	if visible < 1 {
		visible = 1
	}
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+visible {
		m.scroll = m.cursor - visible + 1
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}

func (m *BrowserModel) clampPreviewScroll() {
	maxScroll := m.previewLineCount() - m.visiblePreviewLines()
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.previewScroll > maxScroll {
		m.previewScroll = maxScroll
	}
	if m.previewScroll < 0 {
		m.previewScroll = 0
	}
}

func (m BrowserModel) View() string {
	if m.width == 0 || m.height == 0 {
		return "Loading session browser..."
	}

	header := m.renderBrowserHeader()
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.renderSessionsPane(), m.renderPreviewPane())
	status := m.renderBrowserStatus()
	return lipgloss.JoinVertical(lipgloss.Left, header, body, status)
}

func (m BrowserModel) renderBrowserHeader() string {
	maxWidth := m.width - 2
	if maxWidth < 10 {
		maxWidth = 10
	}

	bar := "ralfinho view"
	if len(m.summaries) > 0 {
		bar += fmt.Sprintf(" │ %d sessions", len(m.summaries))
		bar += fmt.Sprintf(" │ %s", shortID(m.summaries[m.cursor].RunID))
	}
	if lipgloss.Width(bar) > maxWidth {
		bar = truncateToWidth(bar, maxWidth)
	}

	return headerStyle.Width(m.width).Render(bar)
}

func (m BrowserModel) renderSessionsPane() string {
	w := m.sessionsWidth()
	ph := m.browserPaneHeight()
	contentWidth := w - 2
	if contentWidth < 12 {
		contentWidth = 12
	}

	indicatorWidth := lipgloss.Width(selectedIndicator.Render("▌"))
	lineWidth := contentWidth - indicatorWidth
	if lineWidth < 8 {
		lineWidth = 8
	}

	visibleLines := ph - 1
	if visibleLines < 1 {
		visibleLines = 1
	}

	visibleRows := m.visibleSessionRows()
	var lines []string
	for i := m.scroll; i < len(m.summaries) && i < m.scroll+visibleRows; i++ {
		summary := m.summaries[i]
		primary := padToWidth(browserPrimaryRow(summary, lineWidth), lineWidth)
		secondary := padToWidth(browserSecondaryRow(summary, lineWidth), lineWidth)

		if i == m.cursor {
			lines = append(lines,
				selectedIndicator.Render("▌")+selectedStyle.Render(primary),
				" "+selectedStyle.Render(secondary),
			)
		} else {
			lines = append(lines,
				" "+browserRowStyle.Render(primary),
				" "+browserSubtleStyle.Render(secondary),
			)
		}
	}

	for len(lines) < visibleLines {
		lines = append(lines, strings.Repeat(" ", contentWidth))
	}
	if len(lines) > visibleLines {
		lines = lines[:visibleLines]
	}

	content := strings.Join(lines, "\n")
	title := fmt.Sprintf(" SESSIONS (%d) ", len(m.summaries))
	if len(m.summaries) > 0 {
		title = fmt.Sprintf(" SESSIONS (%d) [%d/%d] ", len(m.summaries), m.cursor+1, len(m.summaries))
	}

	border := unfocusedBorder
	if m.focusedPane == 0 {
		border = focusedBorder
	}

	return border.
		Width(w - 2).
		Height(ph).
		Render(titleStyle.Render(title) + "\n" + content)
}

func (m BrowserModel) renderPreviewPane() string {
	w := m.previewWidth()
	ph := m.browserPaneHeight()
	contentWidth := w - 2
	if contentWidth < 20 {
		contentWidth = 20
	}

	raw := browserPreviewText(m.currentSummary())
	wrapped := WrapText(raw, contentWidth)
	allLines := strings.Split(wrapped, "\n")
	visibleLines := m.visiblePreviewLines()

	maxScroll := len(allLines) - visibleLines
	if maxScroll < 0 {
		maxScroll = 0
	}
	scroll := m.previewScroll
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}

	end := scroll + visibleLines
	if end > len(allLines) {
		end = len(allLines)
	}

	lines := make([]string, 0, visibleLines)
	for i := scroll; i < end; i++ {
		line := allLines[i]
		if runewidth.StringWidth(line) > contentWidth {
			line = truncateToWidth(line, contentWidth)
		}
		lines = append(lines, line)
	}
	for len(lines) < visibleLines {
		lines = append(lines, "")
	}

	title := " PREVIEW "
	if len(allLines) > visibleLines {
		title = fmt.Sprintf(" PREVIEW [%d/%d] ", scroll+1, len(allLines))
	}

	border := unfocusedBorder
	if m.focusedPane == 1 {
		border = focusedBorder
	}

	return border.
		Width(w - 2).
		Height(ph).
		Render(titleStyle.Render(title) + "\n" + strings.Join(lines, "\n"))
}

func (m BrowserModel) renderBrowserStatus() string {
	maxWidth := m.width - 2
	if maxWidth < 10 {
		maxWidth = 10
	}

	left := "No saved runs"
	if len(m.summaries) > 0 {
		left = fmt.Sprintf("%d runs │ focus:%s │ %s", len(m.summaries), m.focusedPaneLabel(), browserSelectionStatus(m.summaries[m.cursor]))
	}

	sep := statusSepStyle.Render(" │ ")
	right := statusKeyStyle.Render("↑↓") + ":move" +
		sep + statusKeyStyle.Render("Tab") + ":pane" +
		sep + statusKeyStyle.Render("g/G") + ":top/bottom" +
		sep + statusKeyStyle.Render("q") + ":quit"

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	if leftW+1+rightW > maxWidth {
		right = statusKeyStyle.Render("q") + ":quit"
		rightW = lipgloss.Width(right)
	}
	if leftW+1+rightW > maxWidth {
		right = ""
		rightW = 0
	}
	if rightW > 0 && leftW > maxWidth-rightW-1 {
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

	return statusBarStyle.Width(m.width).Render(left + strings.Repeat(" ", gap) + right)
}

func (m BrowserModel) currentSummary() *viewer.RunSummary {
	if len(m.summaries) == 0 || m.cursor < 0 || m.cursor >= len(m.summaries) {
		return nil
	}
	return &m.summaries[m.cursor]
}

func (m BrowserModel) focusedPaneLabel() string {
	if m.focusedPane == 1 {
		return "preview"
	}
	return "sessions"
}

func (m BrowserModel) browserPaneHeight() int {
	h := m.height - 4
	if h < 6 {
		h = 6
	}
	return h
}

func (m BrowserModel) sessionsWidth() int {
	w := int(float64(m.width) * 0.38)
	if w < 34 {
		w = 34
	}
	maxW := m.width - 26
	if maxW < 20 {
		maxW = 20
	}
	if w > maxW {
		w = maxW
	}
	if w < 20 {
		w = 20
	}
	return w
}

func (m BrowserModel) previewWidth() int {
	w := m.width - m.sessionsWidth()
	if w < 26 {
		w = 26
	}
	return w
}

func (m BrowserModel) visibleSessionRows() int {
	rows := (m.browserPaneHeight() - 1) / 2
	if rows < 1 {
		rows = 1
	}
	return rows
}

func (m BrowserModel) visiblePreviewLines() int {
	lines := m.browserPaneHeight() - 1
	if lines < 1 {
		lines = 1
	}
	return lines
}

func (m BrowserModel) previewLineCount() int {
	contentWidth := m.previewWidth() - 2
	if contentWidth < 20 {
		contentWidth = 20
	}
	return len(strings.Split(WrapText(browserPreviewText(m.currentSummary()), contentWidth), "\n"))
}

var (
	browserRowStyle    = lipgloss.NewStyle().Foreground(colorBright)
	browserSubtleStyle = lipgloss.NewStyle().Foreground(colorDim)
)

func browserPrimaryRow(summary viewer.RunSummary, width int) string {
	date := browserCompactDate(summary)
	row := fmt.Sprintf("%s  %s", shortID(summary.RunID), date)
	return truncateToWidth(row, width)
}

func browserSecondaryRow(summary viewer.RunSummary, width int) string {
	prompt := browserPromptDescriptor(summary)
	row := fmt.Sprintf("%s • %s • %s", summary.Agent, summary.Status, prompt)
	return truncateToWidth(row, width)
}

func browserPreviewText(summary *viewer.RunSummary) string {
	if summary == nil {
		return "No saved runs found.\n\nRun ralfinho to create a session, then open `ralfinho view` again."
	}

	var lines []string
	lines = append(lines,
		fmt.Sprintf("Run: %s", summary.RunID),
		fmt.Sprintf("Started: %s", browserLongDate(*summary)),
		fmt.Sprintf("Agent: %s", summary.Agent),
		fmt.Sprintf("Status: %s", summary.Status),
		fmt.Sprintf("Iterations: %d", summary.IterationsCompleted),
		fmt.Sprintf("Directory: %s", summary.Dir),
		"",
		fmt.Sprintf("Prompt: %s", browserPromptDescriptor(*summary)),
	)
	if summary.PromptPath != "" {
		lines = append(lines, fmt.Sprintf("Prompt path: %s", summary.PromptPath))
	}
	if summary.StartedAtText != "" && summary.StartedAt.IsZero() {
		lines = append(lines, fmt.Sprintf("Started raw: %s", summary.StartedAtText))
	}

	lines = append(lines,
		"",
		"Artifacts",
		fmt.Sprintf("  meta.json: %s", browserMetaState(*summary)),
		fmt.Sprintf("  events.jsonl: %s", browserArtifactState(summary.HasEvents, summary.EventsError)),
		fmt.Sprintf("  effective-prompt.md: %s", browserArtifactState(summary.HasEffectivePrompt, summary.EffectivePromptError)),
		"",
		"Actions",
		fmt.Sprintf("  open: %s", browserOpenState(summary.Actions.Open)),
		fmt.Sprintf("  resume: %s", browserResumeState(summary.Actions.Resume)),
		fmt.Sprintf("  delete: %s", browserDeleteState(summary.Actions.Delete)),
	)
	if summary.Actions.Resume.Available && summary.Actions.Resume.Path != "" {
		lines = append(lines, fmt.Sprintf("    source path: %s", summary.Actions.Resume.Path))
	}

	if summary.ArtifactError != "" || summary.EventsError != "" || summary.EffectivePromptError != "" {
		lines = append(lines, "", "Notes")
		if summary.ArtifactError != "" {
			lines = append(lines, fmt.Sprintf("  %s", summary.ArtifactError))
		}
		if summary.EventsError != "" {
			lines = append(lines, fmt.Sprintf("  %s", summary.EventsError))
		}
		if summary.EffectivePromptError != "" {
			lines = append(lines, fmt.Sprintf("  %s", summary.EffectivePromptError))
		}
	}

	return strings.Join(lines, "\n")
}

func browserSelectionStatus(summary viewer.RunSummary) string {
	return fmt.Sprintf("%s • %s", shortID(summary.RunID), browserPromptDescriptor(summary))
}

func browserPromptDescriptor(summary viewer.RunSummary) string {
	label := strings.TrimSpace(summary.PromptLabel)
	if label == "" || label == "unknown" {
		if summary.HasEffectivePrompt {
			label = "saved prompt"
		} else {
			label = "unknown"
		}
	}

	source := strings.TrimSpace(summary.PromptSource)
	if source != "" && source != "unknown" && source != label {
		return fmt.Sprintf("%s (%s)", label, source)
	}
	return label
}

func browserMetaState(summary viewer.RunSummary) string {
	if summary.HasMeta {
		return "ok"
	}
	if summary.ArtifactError != "" {
		return summary.ArtifactError
	}
	return "unavailable"
}

func browserArtifactState(ok bool, err string) string {
	if ok {
		return "ok"
	}
	if err != "" {
		return err
	}
	return "unavailable"
}

func browserOpenState(state viewer.RunActionState) string {
	if state.Available {
		return "available"
	}
	return "unavailable — " + defaultBrowserReason(state.DisabledReason)
}

func browserResumeState(state viewer.ResumeActionState) string {
	if !state.Available {
		return "unavailable — " + defaultBrowserReason(state.DisabledReason)
	}
	return fmt.Sprintf("available from %s", browserResumeSourceLabel(state.Source))
}

func browserResumeSourceLabel(source viewer.ResumeSource) string {
	switch source {
	case viewer.ResumeSourceEffectivePrompt:
		return "effective prompt"
	case viewer.ResumeSourcePromptFile:
		return "prompt file"
	case viewer.ResumeSourcePlanFile:
		return "plan file"
	case viewer.ResumeSourceDefault:
		return "default prompt"
	default:
		return "saved artifacts"
	}
}

func browserDeleteState(state viewer.RunActionState) string {
	if state.Available {
		return "available"
	}
	return "unavailable — " + defaultBrowserReason(state.DisabledReason)
}

func defaultBrowserReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "not available"
	}
	return reason
}

func browserCompactDate(summary viewer.RunSummary) string {
	t := browserSummaryTime(summary)
	if t.IsZero() {
		return "unknown"
	}
	return t.Format("01-02 15:04")
}

func browserLongDate(summary viewer.RunSummary) string {
	t := browserSummaryTime(summary)
	if t.IsZero() {
		if summary.StartedAtText != "" {
			return summary.StartedAtText
		}
		return "unknown"
	}
	return t.Format("2006-01-02 15:04:05")
}

func browserSummaryTime(summary viewer.RunSummary) time.Time {
	switch {
	case !summary.StartedAt.IsZero():
		return summary.StartedAt
	case !summary.SortTime.IsZero():
		return summary.SortTime
	default:
		return time.Time{}
	}
}

func padToWidth(s string, width int) string {
	if width < 1 {
		return s
	}
	if runewidth.StringWidth(s) > width {
		return truncateToWidth(s, width)
	}
	if pad := width - runewidth.StringWidth(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}
