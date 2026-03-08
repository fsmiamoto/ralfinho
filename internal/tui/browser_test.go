package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fsmiamoto/ralfinho/internal/viewer"
)

func TestNewBrowserModelSortsAndBuildsFilterOptions(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("run-beta", time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC), "pi", "completed", "plan"),
		browserTestSummary("run-gamma", time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC), "kiro", "failed", "default"),
		browserTestSummary("run-alpha", time.Date(2026, 3, 8, 11, 30, 0, 0, time.UTC), "kiro", "interrupted", "prompt"),
	}

	m := NewBrowserModel(summaries)

	if got, want := browserRunIDs(m.summaries), []string{"run-alpha", "run-beta", "run-gamma"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("visible run IDs = %v, want %v", got, want)
	}
	if got, want := m.agentOptions, []string{"kiro", "pi"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("agentOptions = %v, want %v", got, want)
	}
	if got, want := m.statusOptions, []string{"completed", "failed", "interrupted"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("statusOptions = %v, want %v", got, want)
	}
	if got, want := m.promptOptions, []string{"default", "plan", "prompt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("promptOptions = %v, want %v", got, want)
	}
	if got, want := m.dateOptions, []string{"2026-03-08", "2026-03-07", "2026-03-06"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("dateOptions = %v, want %v", got, want)
	}
	if m.selectedRunID != "run-alpha" {
		t.Fatalf("selectedRunID = %q, want %q", m.selectedRunID, "run-alpha")
	}
}

func TestBrowserModelAppliesSearchAndFieldFilters(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("run-beta", time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC), "pi", "completed", "plan"),
		browserTestSummary("run-gamma", time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC), "kiro", "failed", "default"),
		browserTestSummary("run-alpha", time.Date(2026, 3, 8, 11, 30, 0, 0, time.UTC), "kiro", "interrupted", "prompt"),
	}

	m := NewBrowserModel(summaries)
	m.searchQuery = "kiro"
	m.applyBrowserView()
	if got, want := browserRunIDs(m.summaries), []string{"run-alpha", "run-gamma"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("search results = %v, want %v", got, want)
	}

	m.promptFilter = "prompt"
	m.applyBrowserView()
	if got, want := browserRunIDs(m.summaries), []string{"run-alpha"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("prompt-filtered results = %v, want %v", got, want)
	}

	m.searchQuery = ""
	m.promptFilter = ""
	m.agentFilter = "kiro"
	m.statusFilter = "failed"
	m.dateFilter = "2026-03-06"
	m.applyBrowserView()
	if got, want := browserRunIDs(m.summaries), []string{"run-gamma"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("combined filters = %v, want %v", got, want)
	}
}

func TestBrowserModelSearchEditingAndSortCyclePreserveSelection(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("run-beta", time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC), "pi", "completed", "plan"),
		browserTestSummary("run-gamma", time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC), "kiro", "failed", "default"),
		browserTestSummary("run-alpha", time.Date(2026, 3, 8, 11, 30, 0, 0, time.UTC), "kiro", "interrupted", "prompt"),
	}

	m := NewBrowserModel(summaries)
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'j'}}))
	if current := m.currentSummary(); current == nil || current.RunID != "run-beta" {
		t.Fatalf("current run after j = %#v, want run-beta", current)
	}

	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'/'}}))
	if !m.searching {
		t.Fatal("searching = false, want true after /")
	}
	for _, r := range []rune("kiro") {
		m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{r}}))
	}
	if m.searchQuery != "kiro" {
		t.Fatalf("searchQuery = %q, want %q", m.searchQuery, "kiro")
	}
	if got, want := browserRunIDs(m.summaries), []string{"run-alpha", "run-gamma"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("search-edit results = %v, want %v", got, want)
	}

	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyBackspace}))
	if m.searchQuery != "kir" {
		t.Fatalf("searchQuery after backspace = %q, want %q", m.searchQuery, "kir")
	}
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyEsc}))
	if m.searching {
		t.Fatal("searching = true, want false after esc")
	}

	m.clearBrowserFilters()
	if current := m.currentSummary(); current == nil || current.RunID != "run-gamma" {
		t.Fatalf("current run after clearing filters = %#v, want run-gamma", current)
	}
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'s'}}))
	if m.sortMode != browserSortOldest {
		t.Fatalf("sortMode = %q, want %q", m.sortMode, browserSortOldest)
	}
	if current := m.currentSummary(); current == nil || current.RunID != "run-gamma" {
		t.Fatalf("current run after sort cycle = %#v, want run-gamma", current)
	}
}

func TestBrowserHasArtifactIssues(t *testing.T) {
	tests := []struct {
		name string
		s    viewer.RunSummary
		want bool
	}{
		{"clean summary", browserTestSummary("r1", time.Now(), "pi", "completed", "default"), false},
		{"meta error", viewer.RunSummary{ArtifactError: "meta.json missing"}, true},
		{"events error", viewer.RunSummary{EventsError: "events.jsonl missing"}, true},
		{"prompt error", viewer.RunSummary{EffectivePromptError: "effective-prompt.md missing"}, true},
		{"all errors", viewer.RunSummary{ArtifactError: "x", EventsError: "y", EffectivePromptError: "z"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := browserHasArtifactIssues(tt.s); got != tt.want {
				t.Errorf("browserHasArtifactIssues() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBrowserVisibleIssueCount(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("r1", time.Now(), "pi", "completed", "default"),
		{RunID: "r2", ArtifactError: "meta.json missing", SearchText: "r2"},
		{RunID: "r3", EventsError: "events.jsonl missing", SearchText: "r3"},
		browserTestSummary("r4", time.Now().Add(-time.Hour), "kiro", "completed", "plan"),
	}
	m := NewBrowserModel(summaries)
	if got, want := m.browserVisibleIssueCount(), 2; got != want {
		t.Fatalf("browserVisibleIssueCount() = %d, want %d", got, want)
	}
}

func TestBrowserStackedLayoutThreshold(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("r1", time.Now(), "pi", "completed", "default"),
	}
	m := NewBrowserModel(summaries)

	// Wide terminal: side-by-side layout.
	m.width = 120
	m.height = 40
	if m.useStackedBrowserLayout() {
		t.Error("useStackedBrowserLayout() = true at width 120, want false")
	}

	// Narrow terminal: stacked layout.
	m.width = 60
	if !m.useStackedBrowserLayout() {
		t.Error("useStackedBrowserLayout() = false at width 60, want true")
	}

	// Boundary: 80 is the cutoff.
	m.width = 80
	if m.useStackedBrowserLayout() {
		t.Error("useStackedBrowserLayout() = true at width 80, want false")
	}
	m.width = 79
	if !m.useStackedBrowserLayout() {
		t.Error("useStackedBrowserLayout() = false at width 79, want true")
	}
}

func TestBrowserPaneHeightsInStackedLayout(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("r1", time.Now(), "pi", "completed", "default"),
	}
	m := NewBrowserModel(summaries)

	// Side-by-side: both panes share the full browser pane height.
	m.width = 120
	m.height = 40
	sideH := m.browserPaneHeight()
	if got := m.sessionsPaneHeight(); got != sideH {
		t.Errorf("side-by-side sessionsPaneHeight = %d, want %d", got, sideH)
	}
	if got := m.previewPaneHeight(); got != sideH {
		t.Errorf("side-by-side previewPaneHeight = %d, want %d", got, sideH)
	}

	// Stacked: sessions + preview split the height.
	m.width = 60
	sessH := m.sessionsPaneHeight()
	prevH := m.previewPaneHeight()
	if sessH+prevH != sideH {
		t.Errorf("stacked heights sum %d+%d=%d, want %d", sessH, prevH, sessH+prevH, sideH)
	}
	if sessH < 4 {
		t.Errorf("stacked sessionsPaneHeight = %d, want >= 4", sessH)
	}
	if prevH < 4 {
		t.Errorf("stacked previewPaneHeight = %d, want >= 4", prevH)
	}
}

func TestBrowserWidthsInStackedLayout(t *testing.T) {
	m := NewBrowserModel(nil)

	// Side-by-side: sessions is a fraction of width.
	m.width = 120
	m.height = 40
	sessW := m.sessionsWidth()
	prevW := m.previewWidth()
	if sessW+prevW != 120 {
		t.Errorf("side-by-side widths: %d+%d=%d, want 120", sessW, prevW, sessW+prevW)
	}

	// Stacked: both panes are full terminal width.
	m.width = 60
	if got := m.sessionsWidth(); got != 60 {
		t.Errorf("stacked sessionsWidth = %d, want 60", got)
	}
	if got := m.previewWidth(); got != 60 {
		t.Errorf("stacked previewWidth = %d, want 60", got)
	}
}

func TestBrowserEmptyRunsView(t *testing.T) {
	m := NewBrowserModel(nil)
	m.width = 100
	m.height = 30

	// Status bar should indicate no saved runs.
	statusLeft := m.browserStatusLeft()
	if !strings.Contains(statusLeft, "No saved runs") {
		t.Errorf("empty status left = %q, want it to contain 'No saved runs'", statusLeft)
	}

	// View should render without panic.
	view := m.View()
	if !strings.Contains(view, "SESSIONS") {
		t.Error("empty view does not contain SESSIONS pane")
	}
	if !strings.Contains(view, "PREVIEW") {
		t.Error("empty view does not contain PREVIEW pane")
	}

	// Status hints should only show quit.
	hints := m.browserStatusRightVariants()
	if len(hints) == 0 {
		t.Fatal("no status right variants for empty model")
	}
	if !strings.Contains(hints[0], "quit") {
		t.Errorf("empty hints = %q, want quit", hints[0])
	}
}

func TestBrowserNoMatchesView(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("r1", time.Now(), "pi", "completed", "default"),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30
	m.searchQuery = "zzznotfound"
	m.applyBrowserView()

	// Should have zero visible summaries but allSummaries > 0.
	if len(m.summaries) != 0 {
		t.Fatalf("visible summaries = %d, want 0", len(m.summaries))
	}

	statusLeft := m.browserStatusLeft()
	if !strings.Contains(statusLeft, "0/1") {
		t.Errorf("no-matches status left = %q, want it to contain '0/1'", statusLeft)
	}

	// Should render without panic and show NO MATCHES state.
	view := m.View()
	if !strings.Contains(view, "SESSIONS") {
		t.Error("no-matches view does not contain SESSIONS pane")
	}

	// Hints should include clear option.
	hints := m.browserStatusRightVariants()
	if len(hints) == 0 {
		t.Fatal("no status right variants for no-matches")
	}
	found := false
	for _, h := range hints {
		if strings.Contains(h, "clear") {
			found = true
			break
		}
	}
	if !found {
		t.Error("no-matches hints do not include 'clear'")
	}
}

func TestBrowserLoadingStateBeforeWindowSize(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("r1", time.Now(), "pi", "completed", "default"),
	}
	m := NewBrowserModel(summaries)
	// width/height are zero before first WindowSizeMsg.
	view := m.View()
	if !strings.Contains(view, "Opening session browser") {
		t.Errorf("loading view = %q, want 'Opening session browser'", view)
	}
	if !strings.Contains(view, "Loaded 1 saved sessions") {
		t.Errorf("loading view = %q, want 'Loaded 1 saved sessions'", view)
	}
}

func TestBrowserLoadingStateShowsArtifactWarnings(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("r1", time.Now(), "pi", "completed", "default"),
		{RunID: "r2", ArtifactError: "meta.json missing", SortTime: time.Now(), SearchText: "r2"},
	}
	m := NewBrowserModel(summaries)
	view := m.View()
	if !strings.Contains(view, "artifact warnings") {
		t.Errorf("loading view = %q, want 'artifact warnings'", view)
	}
}

func TestBrowserArtifactWarningInPrimaryRow(t *testing.T) {
	clean := browserTestSummary("clean-run", time.Now(), "pi", "completed", "default")
	broken := viewer.RunSummary{
		RunID:         "broken-run",
		Dir:           "/tmp/broken-run",
		SortTime:      time.Now(),
		Agent:         "pi",
		Status:        "unknown",
		ArtifactError: "meta.json missing",
		SearchText:    "broken-run",
	}

	cleanRow := browserPrimaryRow(clean, 60)
	brokenRow := browserPrimaryRow(broken, 60)

	if strings.Contains(cleanRow, "⚠") {
		t.Errorf("clean row contains warning indicator: %q", cleanRow)
	}
	if !strings.Contains(brokenRow, "⚠") {
		t.Errorf("broken row missing warning indicator: %q", brokenRow)
	}
}

func TestBrowserPreviewShowsArtifactWarningTitle(t *testing.T) {
	m := NewBrowserModel([]viewer.RunSummary{
		{
			RunID:         "warn-run",
			Dir:           "/tmp/warn-run",
			SortTime:      time.Now(),
			Agent:         "pi",
			Status:        "unknown",
			ArtifactError: "meta.json missing",
			SearchText:    "warn-run",
		},
	})
	m.width = 100
	m.height = 30
	preview := m.renderPreviewPane()
	if !strings.Contains(preview, "⚠") {
		t.Error("preview pane for run with artifact issues does not show ⚠ indicator")
	}
}

func TestBrowserStatusHintsForSearchMode(t *testing.T) {
	m := NewBrowserModel([]viewer.RunSummary{
		browserTestSummary("r1", time.Now(), "pi", "completed", "default"),
	})
	m.width = 100
	m.height = 30
	m.searching = true

	hints := m.browserStatusRightVariants()
	if len(hints) == 0 {
		t.Fatal("no search-mode hints")
	}
	first := hints[0]
	for _, keyword := range []string{"search", "done", "cancel"} {
		if !strings.Contains(first, keyword) {
			t.Errorf("search hint %q missing keyword %q", first, keyword)
		}
	}
}

func TestBrowserStatusHintsForPreviewFocus(t *testing.T) {
	m := NewBrowserModel([]viewer.RunSummary{
		browserTestSummary("r1", time.Now(), "pi", "completed", "default"),
	})
	m.width = 100
	m.height = 30
	m.focusedPane = 1

	hints := m.browserStatusRightVariants()
	if len(hints) == 0 {
		t.Fatal("no preview-focus hints")
	}
	first := hints[0]
	if !strings.Contains(first, "scroll") {
		t.Errorf("preview hint %q missing 'scroll'", first)
	}
	if !strings.Contains(first, "sessions") {
		t.Errorf("preview hint %q missing 'sessions'", first)
	}
}

func TestBrowserStatusLeftShowsStackedIndicator(t *testing.T) {
	m := NewBrowserModel([]viewer.RunSummary{
		browserTestSummary("r1", time.Now(), "pi", "completed", "default"),
	})
	m.height = 30

	m.width = 120
	if strings.Contains(m.browserStatusLeft(), "stacked") {
		t.Error("wide-terminal status left contains 'stacked'")
	}

	m.width = 60
	if !strings.Contains(m.browserStatusLeft(), "stacked") {
		t.Error("narrow-terminal status left does not contain 'stacked'")
	}
}

func TestBrowserViewRendersWithSmallTerminal(t *testing.T) {
	m := NewBrowserModel([]viewer.RunSummary{
		browserTestSummary("r1", time.Now(), "pi", "completed", "default"),
	})
	// Very small terminal — should not panic.
	m.width = 40
	m.height = 12
	view := m.View()
	if view == "" {
		t.Error("small-terminal view is empty")
	}
}

func TestBrowserViewRendersWithVeryNarrowTerminal(t *testing.T) {
	m := NewBrowserModel([]viewer.RunSummary{
		browserTestSummary("r1", time.Now(), "pi", "completed", "default"),
	})
	m.width = 20
	m.height = 10
	view := m.View()
	if view == "" {
		t.Error("very-narrow-terminal view is empty")
	}
}

func TestBrowserOpenActionOnEnter(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("openable-run", time.Now(), "pi", "completed", "default", true),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	// Press Enter on the selected session.
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyEnter}))

	result := m.Result()
	if result.Action != BrowserActionOpen {
		t.Fatalf("Result().Action = %q, want %q", result.Action, BrowserActionOpen)
	}
	if result.RunID != "openable-run" {
		t.Fatalf("Result().RunID = %q, want %q", result.RunID, "openable-run")
	}
}

func TestBrowserOpenActionOnO(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("openable-run", time.Now(), "pi", "completed", "default", true),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	// Press 'o' on the selected session.
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'o'}}))

	result := m.Result()
	if result.Action != BrowserActionOpen {
		t.Fatalf("Result().Action = %q, want %q", result.Action, BrowserActionOpen)
	}
	if result.RunID != "openable-run" {
		t.Fatalf("Result().RunID = %q, want %q", result.RunID, "openable-run")
	}
}

func TestBrowserOpenActionBlockedWhenUnavailable(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("broken-run", time.Now(), "pi", "unknown", "default", false),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	// Press Enter on a session without open available.
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyEnter}))

	result := m.Result()
	if result.Action != BrowserActionNone {
		t.Fatalf("Result().Action = %q, want %q (open should be blocked)", result.Action, BrowserActionNone)
	}
}

func TestBrowserOpenActionBlockedFromPreviewPane(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("openable-run", time.Now(), "pi", "completed", "default", true),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30
	m.focusedPane = 1 // preview pane

	// Press Enter while preview is focused.
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyEnter}))

	result := m.Result()
	if result.Action != BrowserActionNone {
		t.Fatalf("Result().Action = %q, want %q (open should only work from sessions pane)", result.Action, BrowserActionNone)
	}
}

func TestBrowserOpenActionOnEmptyList(t *testing.T) {
	m := NewBrowserModel(nil)
	m.width = 100
	m.height = 30

	// Press Enter with no sessions.
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyEnter}))

	result := m.Result()
	if result.Action != BrowserActionNone {
		t.Fatalf("Result().Action = %q, want %q (no sessions to open)", result.Action, BrowserActionNone)
	}
}

func TestBrowserWithSelectedRunID(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("run-alpha", time.Date(2026, 3, 8, 11, 0, 0, 0, time.UTC), "pi", "completed", "default", true),
		browserTestSummaryWithActions("run-beta", time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC), "kiro", "completed", "plan", true),
		browserTestSummaryWithActions("run-gamma", time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC), "pi", "failed", "prompt", true),
	}

	// Default selection is the first (newest) run.
	m := NewBrowserModel(summaries)
	if m.selectedRunID != "run-alpha" {
		t.Fatalf("default selectedRunID = %q, want %q", m.selectedRunID, "run-alpha")
	}

	// Pre-select run-beta.
	m = NewBrowserModel(summaries).WithSelectedRunID("run-beta")
	if m.selectedRunID != "run-beta" {
		t.Fatalf("WithSelectedRunID selectedRunID = %q, want %q", m.selectedRunID, "run-beta")
	}
	if current := m.currentSummary(); current == nil || current.RunID != "run-beta" {
		t.Fatalf("WithSelectedRunID currentSummary = %v, want run-beta", current)
	}

	// Pre-select non-existent run ID should fall back gracefully.
	m = NewBrowserModel(summaries).WithSelectedRunID("run-nonexistent")
	if current := m.currentSummary(); current == nil {
		t.Fatal("WithSelectedRunID for non-existent ID returned nil currentSummary")
	}
}

func TestBrowserStatusHintsIncludeOpenWhenAvailable(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("openable-run", time.Now(), "pi", "completed", "default", true),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	hints := m.browserStatusRightVariants()
	if len(hints) == 0 {
		t.Fatal("no status hints")
	}
	if !strings.Contains(hints[0], "open") {
		t.Errorf("hints for openable session = %q, want 'open'", hints[0])
	}
}

func TestBrowserStatusHintsExcludeOpenWhenUnavailable(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("broken-run", time.Now(), "pi", "unknown", "default", false),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	hints := m.browserStatusRightVariants()
	if len(hints) == 0 {
		t.Fatal("no status hints")
	}
	if strings.Contains(hints[0], "Enter") {
		t.Errorf("hints for non-openable session = %q, should not contain 'Enter'", hints[0])
	}
}

func browserTestSummaryWithActions(runID string, startedAt time.Time, agent, status, promptSource string, openable bool) viewer.RunSummary {
	s := browserTestSummary(runID, startedAt, agent, status, promptSource)
	if openable {
		s.HasMeta = true
		s.HasEvents = true
		s.Actions.Open = viewer.RunActionState{Available: true}
	} else {
		s.Actions.Open = viewer.RunActionState{DisabledReason: "events.jsonl unavailable"}
	}
	s.Actions.Delete = viewer.RunActionState{Available: true}
	return s
}

func browserTestSummary(runID string, startedAt time.Time, agent, status, promptSource string) viewer.RunSummary {
	searchParts := []string{runID, agent, status, promptSource}
	if !startedAt.IsZero() {
		searchParts = append(searchParts, startedAt.Format("2006-01-02 15:04"))
	}
	return viewer.RunSummary{
		RunID:         runID,
		Dir:           "/tmp/" + runID,
		StartedAt:     startedAt,
		StartedAtText: startedAt.Format(time.RFC3339),
		SortTime:      startedAt,
		Agent:         agent,
		Status:        status,
		PromptSource:  promptSource,
		PromptLabel:   promptSource,
		SearchText:    strings.ToLower(strings.Join(searchParts, "\n")),
	}
}

func browserRunIDs(summaries []viewer.RunSummary) []string {
	ids := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		ids = append(ids, summary.RunID)
	}
	return ids
}

func updateBrowserModel(t *testing.T, model BrowserModel, key tea.KeyMsg) BrowserModel {
	t.Helper()
	updated, _ := model.Update(key)
	next, ok := updated.(BrowserModel)
	if !ok {
		t.Fatalf("updated model has type %T, want BrowserModel", updated)
	}
	return next
}
