package tui

import (
	"fmt"
	"sort"
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

type browserSortMode string

const (
	browserSortNewest browserSortMode = "newest"
	browserSortOldest browserSortMode = "oldest"
	browserSortRunID  browserSortMode = "run id"
	browserSortAgent  browserSortMode = "agent"
	browserSortStatus browserSortMode = "status"
	browserSortPrompt browserSortMode = "prompt"
)

var browserSortModes = []browserSortMode{
	browserSortNewest,
	browserSortOldest,
	browserSortRunID,
	browserSortAgent,
	browserSortStatus,
	browserSortPrompt,
}

// BrowserModel renders the saved-session browser for `ralfinho view`.
type BrowserModel struct {
	allSummaries []viewer.RunSummary
	summaries    []viewer.RunSummary

	cursor        int
	scroll        int
	previewScroll int
	width         int
	height        int
	focusedPane   int // 0=sessions, 1=preview
	result        BrowserResult

	selectedRunID string
	sortMode      browserSortMode
	searchQuery   string
	searching     bool

	agentFilter  string
	agentOptions []string

	statusFilter  string
	statusOptions []string

	promptFilter  string
	promptOptions []string

	dateFilter  string
	dateOptions []string
}

// NewBrowserModel creates a browser over a preloaded in-memory session list.
func NewBrowserModel(summaries []viewer.RunSummary) BrowserModel {
	all := append([]viewer.RunSummary(nil), summaries...)
	m := BrowserModel{
		allSummaries: all,
		sortMode:     browserSortNewest,
		agentOptions: browserFilterOptions(all, func(summary viewer.RunSummary) string {
			return summary.Agent
		}),
		statusOptions: browserFilterOptions(all, func(summary viewer.RunSummary) string {
			return summary.Status
		}),
		promptOptions: browserFilterOptions(all, func(summary viewer.RunSummary) string {
			return summary.PromptSource
		}),
		dateOptions: browserDateOptions(all),
	}
	m.applyBrowserView()
	return m
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
	if m.searching {
		return m.handleSearchKey(msg)
	}

	switch msg.String() {
	case "/", "?":
		m.searching = true

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
			m.setCursor(0)
			m.scroll = 0
		} else {
			m.previewScroll = 0
		}

	case "G":
		if m.focusedPane == 0 {
			if len(m.summaries) > 0 {
				m.setCursor(len(m.summaries) - 1)
				m.ensureCursorVisible()
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

	case "s":
		m.cycleSortMode()

	case "a":
		m.agentFilter = cycleBrowserOption(m.agentFilter, m.agentOptions)
		m.applyBrowserView()

	case "t":
		m.statusFilter = cycleBrowserOption(m.statusFilter, m.statusOptions)
		m.applyBrowserView()

	case "p":
		m.promptFilter = cycleBrowserOption(m.promptFilter, m.promptOptions)
		m.applyBrowserView()

	case "d":
		m.dateFilter = cycleBrowserOption(m.dateFilter, m.dateOptions)
		m.applyBrowserView()

	case "c":
		m.clearBrowserFilters()
	}

	return m, nil
}

func (m BrowserModel) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc, tea.KeyEnter:
		m.searching = false
		return m, nil
	case tea.KeyBackspace, tea.KeyCtrlH:
		runes := []rune(m.searchQuery)
		if len(runes) > 0 {
			m.searchQuery = string(runes[:len(runes)-1])
			m.applyBrowserView()
		}
		return m, nil
	case tea.KeyDelete, tea.KeyCtrlU:
		if m.searchQuery != "" {
			m.searchQuery = ""
			m.applyBrowserView()
		}
		return m, nil
	case tea.KeyRunes:
		if len(msg.Runes) > 0 {
			m.searchQuery += string(msg.Runes)
			m.applyBrowserView()
		}
		return m, nil
	case tea.KeySpace:
		m.searchQuery += " "
		m.applyBrowserView()
		return m, nil
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
	m.selectedRunID = m.summaries[m.cursor].RunID
	m.ensureCursorVisible()
}

func (m *BrowserModel) setCursor(index int) {
	if len(m.summaries) == 0 {
		m.cursor = 0
		m.scroll = 0
		m.previewScroll = 0
		return
	}
	if index < 0 {
		index = 0
	}
	if index >= len(m.summaries) {
		index = len(m.summaries) - 1
	}
	if m.cursor != index {
		m.previewScroll = 0
	}
	m.cursor = index
	m.selectedRunID = m.summaries[m.cursor].RunID
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

func (m *BrowserModel) clearBrowserFilters() {
	m.searchQuery = ""
	m.searching = false
	m.agentFilter = ""
	m.statusFilter = ""
	m.promptFilter = ""
	m.dateFilter = ""
	m.applyBrowserView()
}

func (m *BrowserModel) cycleSortMode() {
	idx := 0
	for i, mode := range browserSortModes {
		if mode == m.sortMode {
			idx = i
			break
		}
	}
	m.sortMode = browserSortModes[(idx+1)%len(browserSortModes)]
	m.applyBrowserView()
}

func (m *BrowserModel) applyBrowserView() {
	selectedRunID := strings.TrimSpace(m.selectedRunID)
	if selectedRunID == "" {
		if current := m.currentSummary(); current != nil {
			selectedRunID = current.RunID
		}
	}

	filtered := make([]viewer.RunSummary, 0, len(m.allSummaries))
	for _, summary := range m.allSummaries {
		if !m.matchesBrowserFilters(summary) {
			continue
		}
		filtered = append(filtered, summary)
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		return browserSummaryLess(filtered[i], filtered[j], m.sortMode)
	})

	m.summaries = filtered
	if len(m.summaries) == 0 {
		m.cursor = 0
		m.scroll = 0
		m.previewScroll = 0
		return
	}

	if selectedRunID != "" {
		for i, summary := range m.summaries {
			if summary.RunID == selectedRunID {
				m.cursor = i
				m.selectedRunID = summary.RunID
				m.ensureCursorVisible()
				m.clampPreviewScroll()
				return
			}
		}
	}

	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.summaries) {
		m.cursor = len(m.summaries) - 1
	}
	m.selectedRunID = m.summaries[m.cursor].RunID
	m.ensureCursorVisible()
	m.previewScroll = 0
	m.clampPreviewScroll()
}

func (m BrowserModel) matchesBrowserFilters(summary viewer.RunSummary) bool {
	if m.agentFilter != "" && !strings.EqualFold(summary.Agent, m.agentFilter) {
		return false
	}
	if m.statusFilter != "" && !strings.EqualFold(summary.Status, m.statusFilter) {
		return false
	}
	if m.promptFilter != "" && !strings.EqualFold(summary.PromptSource, m.promptFilter) {
		return false
	}
	if m.dateFilter != "" && browserSummaryDate(summary) != m.dateFilter {
		return false
	}
	if strings.TrimSpace(m.searchQuery) != "" && !summary.Matches(m.searchQuery) {
		return false
	}
	return true
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

	bar := fmt.Sprintf("ralfinho view │ %d/%d sessions", len(m.summaries), len(m.allSummaries))
	for _, token := range m.browserStateTokens() {
		bar += " │ " + token
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
	if len(m.summaries) == 0 {
		primary, secondary := m.browserEmptySessionRows(lineWidth)
		lines = append(lines,
			" "+browserRowStyle.Render(primary),
			" "+browserSubtleStyle.Render(secondary),
		)
	} else {
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
	}

	for len(lines) < visibleLines {
		lines = append(lines, strings.Repeat(" ", contentWidth))
	}
	if len(lines) > visibleLines {
		lines = lines[:visibleLines]
	}

	content := strings.Join(lines, "\n")
	title := fmt.Sprintf(" SESSIONS (%d/%d) ", len(m.summaries), len(m.allSummaries))
	if len(m.summaries) > 0 {
		title = fmt.Sprintf(" SESSIONS (%d/%d) [%d/%d] ", len(m.summaries), len(m.allSummaries), m.cursor+1, len(m.summaries))
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

	raw := m.browserPreviewText()
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

	left := m.browserStatusLeft()
	right := m.browserStatusRight(maxWidth)

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	if rightW > 0 && leftW+1+rightW > maxWidth {
		right = browserCompactStatusRight(m.searching)
		rightW = lipgloss.Width(right)
	}
	if rightW > 0 && leftW+1+rightW > maxWidth {
		right = statusKeyStyle.Render("q") + ":quit"
		rightW = lipgloss.Width(right)
	}
	if leftW+1+rightW > maxWidth {
		if rightW > 0 {
			left = truncateToWidth(left, maxWidth-rightW-1)
			leftW = lipgloss.Width(left)
		} else {
			left = truncateToWidth(left, maxWidth)
			leftW = lipgloss.Width(left)
		}
	}

	gap := maxWidth - leftW - rightW
	if gap < 1 {
		gap = 1
	}

	return statusBarStyle.Width(m.width).Render(left + strings.Repeat(" ", gap) + right)
}

func (m BrowserModel) browserStatusLeft() string {
	if len(m.allSummaries) == 0 {
		return "No saved runs"
	}
	if len(m.summaries) == 0 {
		return fmt.Sprintf("0/%d runs │ no matches", len(m.allSummaries))
	}
	return fmt.Sprintf("%d/%d runs │ focus:%s │ %s", len(m.summaries), len(m.allSummaries), m.focusedPaneLabel(), browserSelectionStatus(m.summaries[m.cursor]))
}

func (m BrowserModel) browserStatusRight(maxWidth int) string {
	sep := statusSepStyle.Render(" │ ")
	if m.searching {
		return statusKeyStyle.Render("type") + ":search" +
			sep + statusKeyStyle.Render("Enter") + ":done" +
			sep + statusKeyStyle.Render("Bksp") + ":del" +
			sep + statusKeyStyle.Render("Ctrl+u") + ":clear"
	}
	return statusKeyStyle.Render("↑↓") + ":move" +
		sep + statusKeyStyle.Render("/") + ":search" +
		sep + statusKeyStyle.Render("s") + ":sort" +
		sep + statusKeyStyle.Render("a/t/p/d") + ":filter" +
		sep + statusKeyStyle.Render("c") + ":clear" +
		sep + statusKeyStyle.Render("Tab") + ":pane" +
		sep + statusKeyStyle.Render("q") + ":quit"
}

func browserCompactStatusRight(searching bool) string {
	sep := statusSepStyle.Render(" │ ")
	if searching {
		return statusKeyStyle.Render("Enter") + ":done" + sep + statusKeyStyle.Render("Esc") + ":close"
	}
	return statusKeyStyle.Render("/") + ":search" + sep + statusKeyStyle.Render("s") + ":sort" + sep + statusKeyStyle.Render("q") + ":quit"
}

func (m BrowserModel) browserStateTokens() []string {
	tokens := []string{fmt.Sprintf("sort:%s", m.sortMode)}
	if m.agentFilter != "" {
		tokens = append(tokens, "agent:"+m.agentFilter)
	}
	if m.statusFilter != "" {
		tokens = append(tokens, "status:"+m.statusFilter)
	}
	if m.promptFilter != "" {
		tokens = append(tokens, "prompt:"+m.promptFilter)
	}
	if m.dateFilter != "" {
		tokens = append(tokens, "date:"+m.dateFilter)
	}
	query := strings.TrimSpace(m.searchQuery)
	if query != "" || m.searching {
		if query == "" {
			query = "…"
		}
		if m.searching {
			query += "_"
		}
		tokens = append(tokens, "/"+query)
	}
	return tokens
}

func (m BrowserModel) browserPreviewText() string {
	summary := m.currentSummary()
	if summary != nil {
		return browserPreviewText(summary)
	}
	if len(m.allSummaries) == 0 {
		return "No saved runs found.\n\nRun ralfinho to create a session, then open `ralfinho view` again."
	}

	var lines []string
	lines = append(lines,
		"No sessions match the current search/filter.",
		"",
		fmt.Sprintf("Visible: %d/%d", len(m.summaries), len(m.allSummaries)),
		fmt.Sprintf("Sort: %s", m.sortMode),
		fmt.Sprintf("Agent filter: %s", browserFilterLabel(m.agentFilter)),
		fmt.Sprintf("Status filter: %s", browserFilterLabel(m.statusFilter)),
		fmt.Sprintf("Prompt filter: %s", browserFilterLabel(m.promptFilter)),
		fmt.Sprintf("Date filter: %s", browserFilterLabel(m.dateFilter)),
		fmt.Sprintf("Search: %s", browserSearchLabel(m.searchQuery, m.searching)),
		"",
		"Press c to clear filters, or / to refine the search.",
	)
	return strings.Join(lines, "\n")
}

func (m BrowserModel) browserEmptySessionRows(width int) (string, string) {
	if len(m.allSummaries) == 0 {
		return padToWidth("No saved runs found", width), padToWidth("Run ralfinho to create the first session.", width)
	}
	return padToWidth("No sessions match current filters", width), padToWidth("Press c to clear filters or / to search again.", width)
}

func browserFilterOptions(summaries []viewer.RunSummary, valueFn func(viewer.RunSummary) string) []string {
	seen := make(map[string]string, len(summaries))
	for _, summary := range summaries {
		value := strings.TrimSpace(valueFn(summary))
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; !ok {
			seen[key] = value
		}
	}

	options := make([]string, 0, len(seen))
	for _, value := range seen {
		options = append(options, value)
	}
	sort.Slice(options, func(i, j int) bool {
		return browserFacetLess(options[i], options[j])
	})
	return options
}

func browserDateOptions(summaries []viewer.RunSummary) []string {
	seen := make(map[string]struct{}, len(summaries))
	options := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		value := browserSummaryDate(summary)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		options = append(options, value)
	}
	sort.Slice(options, func(i, j int) bool {
		if options[i] == "unknown" {
			return false
		}
		if options[j] == "unknown" {
			return true
		}
		return options[i] > options[j]
	})
	return options
}

func cycleBrowserOption(current string, options []string) string {
	if len(options) == 0 {
		return ""
	}
	if current == "" {
		return options[0]
	}
	for i, option := range options {
		if strings.EqualFold(option, current) {
			if i == len(options)-1 {
				return ""
			}
			return options[i+1]
		}
	}
	return ""
}

func browserSummaryLess(a, b viewer.RunSummary, mode browserSortMode) bool {
	switch mode {
	case browserSortOldest:
		if !a.SortTime.Equal(b.SortTime) {
			return a.SortTime.Before(b.SortTime)
		}
		return strings.ToLower(a.RunID) < strings.ToLower(b.RunID)
	case browserSortRunID:
		return browserCompareTextField(a.RunID, b.RunID, a, b)
	case browserSortAgent:
		return browserCompareTextField(a.Agent, b.Agent, a, b)
	case browserSortStatus:
		return browserCompareTextField(a.Status, b.Status, a, b)
	case browserSortPrompt:
		return browserCompareTextField(a.PromptSource, b.PromptSource, a, b)
	case browserSortNewest:
		fallthrough
	default:
		if !a.SortTime.Equal(b.SortTime) {
			return a.SortTime.After(b.SortTime)
		}
		return strings.ToLower(a.RunID) > strings.ToLower(b.RunID)
	}
}

func browserCompareTextField(aValue, bValue string, a, b viewer.RunSummary) bool {
	aRank, aKey := browserFacetSortKey(aValue)
	bRank, bKey := browserFacetSortKey(bValue)
	if aRank != bRank {
		return aRank < bRank
	}
	if aKey != bKey {
		return aKey < bKey
	}
	if !a.SortTime.Equal(b.SortTime) {
		return a.SortTime.After(b.SortTime)
	}
	return strings.ToLower(a.RunID) < strings.ToLower(b.RunID)
}

func browserFacetLess(a, b string) bool {
	aRank, aKey := browserFacetSortKey(a)
	bRank, bKey := browserFacetSortKey(b)
	if aRank != bRank {
		return aRank < bRank
	}
	return aKey < bKey
}

func browserFacetSortKey(value string) (int, string) {
	key := strings.ToLower(strings.TrimSpace(value))
	if key == "" || key == "unknown" {
		return 1, key
	}
	return 0, key
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
	return len(strings.Split(WrapText(m.browserPreviewText(), contentWidth), "\n"))
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

func browserSummaryDate(summary viewer.RunSummary) string {
	t := browserSummaryTime(summary)
	if t.IsZero() {
		return "unknown"
	}
	return t.Format("2006-01-02")
}

func browserFilterLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "all"
	}
	return value
}

func browserSearchLabel(query string, searching bool) string {
	query = strings.TrimSpace(query)
	if query == "" {
		if searching {
			return "(editing)"
		}
		return "all"
	}
	if searching {
		return query + "_"
	}
	return query
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
