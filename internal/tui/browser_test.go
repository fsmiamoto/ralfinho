package tui

import (
	"fmt"
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
	for _, r := range "kiro" {
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

func TestBrowserResumeActionOnR(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryResumable("resumable-run", time.Now(), "pi", "completed", "prompt",
			viewer.ResumeSourceEffectivePrompt, "/tmp/resumable-run/effective-prompt.md"),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'r'}}))

	result := m.Result()
	if result.Action != BrowserActionResume {
		t.Fatalf("Result().Action = %q, want %q", result.Action, BrowserActionResume)
	}
	if result.RunID != "resumable-run" {
		t.Fatalf("Result().RunID = %q, want %q", result.RunID, "resumable-run")
	}
	if result.ResumeAgent != "pi" {
		t.Fatalf("Result().ResumeAgent = %q, want %q", result.ResumeAgent, "pi")
	}
	if result.ResumeSource != viewer.ResumeSourceEffectivePrompt {
		t.Fatalf("Result().ResumeSource = %q, want %q", result.ResumeSource, viewer.ResumeSourceEffectivePrompt)
	}
	if result.ResumePath != "/tmp/resumable-run/effective-prompt.md" {
		t.Fatalf("Result().ResumePath = %q, want %q", result.ResumePath, "/tmp/resumable-run/effective-prompt.md")
	}
}

func TestBrowserResumeActionBlockedWhenUnavailable(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("no-resume-run", time.Now(), "pi", "unknown", "default", false),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'r'}}))

	result := m.Result()
	if result.Action != BrowserActionNone {
		t.Fatalf("Result().Action = %q, want %q (resume should be blocked)", result.Action, BrowserActionNone)
	}
}

func TestBrowserResumeActionBlockedFromPreviewPane(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryResumable("resumable-run", time.Now(), "pi", "completed", "prompt",
			viewer.ResumeSourceEffectivePrompt, "/tmp/resumable-run/effective-prompt.md"),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30
	m.focusedPane = 1

	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'r'}}))

	result := m.Result()
	if result.Action != BrowserActionNone {
		t.Fatalf("Result().Action = %q, want %q (resume should only work from sessions pane)", result.Action, BrowserActionNone)
	}
}

func TestBrowserResumeActionBlockedOnEmptyList(t *testing.T) {
	m := NewBrowserModel(nil)
	m.width = 100
	m.height = 30

	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'r'}}))

	result := m.Result()
	if result.Action != BrowserActionNone {
		t.Fatalf("Result().Action = %q, want %q (no sessions to resume)", result.Action, BrowserActionNone)
	}
}

func TestBrowserResumeResultIncludesMetadata(t *testing.T) {
	tests := []struct {
		name   string
		source viewer.ResumeSource
		path   string
		agent  string
	}{
		{"effective prompt", viewer.ResumeSourceEffectivePrompt, "/tmp/run1/effective-prompt.md", "pi"},
		{"prompt file", viewer.ResumeSourcePromptFile, "/home/user/prompt.md", "kiro"},
		{"plan file", viewer.ResumeSourcePlanFile, "/home/user/plan.md", "pi"},
		{"default", viewer.ResumeSourceDefault, "", "kiro"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summaries := []viewer.RunSummary{
				browserTestSummaryResumable("run-"+tt.name, time.Now(), tt.agent, "completed", "prompt", tt.source, tt.path),
			}
			m := NewBrowserModel(summaries)
			m.width = 100
			m.height = 30

			m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'r'}}))

			result := m.Result()
			if result.Action != BrowserActionResume {
				t.Fatalf("Action = %q, want %q", result.Action, BrowserActionResume)
			}
			if result.ResumeAgent != tt.agent {
				t.Errorf("ResumeAgent = %q, want %q", result.ResumeAgent, tt.agent)
			}
			if result.ResumeSource != tt.source {
				t.Errorf("ResumeSource = %q, want %q", result.ResumeSource, tt.source)
			}
			if result.ResumePath != tt.path {
				t.Errorf("ResumePath = %q, want %q", result.ResumePath, tt.path)
			}
		})
	}
}

func TestBrowserStatusHintsIncludeResumeWhenAvailable(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryResumable("resumable-run", time.Now(), "pi", "completed", "prompt",
			viewer.ResumeSourceEffectivePrompt, "/tmp/resumable-run/effective-prompt.md"),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	hints := m.browserStatusRightVariants()
	if len(hints) == 0 {
		t.Fatal("no status hints")
	}
	if !strings.Contains(hints[0], "resume") {
		t.Errorf("hints for resumable session = %q, want 'resume'", hints[0])
	}
	// Should also show open since the session has both actions available.
	if !strings.Contains(hints[0], "open") {
		t.Errorf("hints for resumable session = %q, want 'open' alongside 'resume'", hints[0])
	}
}

func TestBrowserStatusHintsExcludeResumeWhenUnavailable(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("no-resume-run", time.Now(), "pi", "completed", "default", true),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	hints := m.browserStatusRightVariants()
	if len(hints) == 0 {
		t.Fatal("no status hints")
	}
	for _, h := range hints {
		if strings.Contains(h, "resume") {
			t.Errorf("hints for non-resumable session = %q, should not contain 'resume'", h)
			break
		}
	}
}

func TestBrowserStatusHintsResumeOnlyNoOpen(t *testing.T) {
	// Session with resume available but open unavailable.
	s := browserTestSummary("resume-only", time.Now(), "pi", "unknown", "prompt")
	s.HasEffectivePrompt = true
	s.Actions.Open = viewer.RunActionState{DisabledReason: "events.jsonl unavailable"}
	s.Actions.Resume = viewer.ResumeActionState{
		RunActionState: viewer.RunActionState{Available: true},
		Source:         viewer.ResumeSourceEffectivePrompt,
		Path:           "/tmp/resume-only/effective-prompt.md",
	}
	s.Actions.Delete = viewer.RunActionState{Available: true}

	m := NewBrowserModel([]viewer.RunSummary{s})
	m.width = 100
	m.height = 30

	hints := m.browserStatusRightVariants()
	if len(hints) == 0 {
		t.Fatal("no status hints")
	}
	// Should show resume but not Enter:open.
	if !strings.Contains(hints[0], "resume") {
		t.Errorf("hints = %q, want 'resume'", hints[0])
	}
	if strings.Contains(hints[0], "Enter") {
		t.Errorf("hints = %q, should not contain 'Enter' (open unavailable)", hints[0])
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

// --- Delete confirmation tests ---

func TestBrowserDeleteEntersConfirmationOnX(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("del-run", time.Now(), "pi", "completed", "default", true),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'x'}}))

	if !m.confirmingDelete {
		t.Fatal("confirmingDelete = false, want true after x")
	}
	if m.confirmDeleteRunID != "del-run" {
		t.Fatalf("confirmDeleteRunID = %q, want %q", m.confirmDeleteRunID, "del-run")
	}
	if m.confirmDeleteDir != "/tmp/del-run" {
		t.Fatalf("confirmDeleteDir = %q, want %q", m.confirmDeleteDir, "/tmp/del-run")
	}
	// Should not have emitted a result yet.
	if m.Result().Action != BrowserActionNone {
		t.Fatalf("Result().Action = %q, want %q (no result before confirmation)", m.Result().Action, BrowserActionNone)
	}
}

func TestBrowserDeleteConfirmOnY(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("del-run", time.Now(), "pi", "completed", "default", true),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	// Enter confirmation, then confirm with y.
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'x'}}))
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'y'}}))

	result := m.Result()
	if result.Action != BrowserActionDelete {
		t.Fatalf("Result().Action = %q, want %q", result.Action, BrowserActionDelete)
	}
	if result.RunID != "del-run" {
		t.Fatalf("Result().RunID = %q, want %q", result.RunID, "del-run")
	}
	if result.DeleteDir != "/tmp/del-run" {
		t.Fatalf("Result().DeleteDir = %q, want %q", result.DeleteDir, "/tmp/del-run")
	}
}

func TestBrowserDeleteConfirmOnEnter(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("del-run", time.Now(), "pi", "completed", "default", true),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'x'}}))
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyEnter}))

	result := m.Result()
	if result.Action != BrowserActionDelete {
		t.Fatalf("Result().Action = %q, want %q", result.Action, BrowserActionDelete)
	}
	if result.RunID != "del-run" {
		t.Fatalf("Result().RunID = %q, want %q", result.RunID, "del-run")
	}
}

func TestBrowserDeleteCancelOnN(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("del-run", time.Now(), "pi", "completed", "default", true),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'x'}}))
	if !m.confirmingDelete {
		t.Fatal("not in confirmation mode after x")
	}

	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'n'}}))

	if m.confirmingDelete {
		t.Fatal("confirmingDelete = true, want false after n")
	}
	if m.confirmDeleteRunID != "" {
		t.Fatalf("confirmDeleteRunID = %q, want empty after cancel", m.confirmDeleteRunID)
	}
	if m.Result().Action != BrowserActionNone {
		t.Fatalf("Result().Action = %q, want %q after cancel", m.Result().Action, BrowserActionNone)
	}
}

func TestBrowserDeleteCancelOnEsc(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("del-run", time.Now(), "pi", "completed", "default", true),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'x'}}))
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyEsc}))

	if m.confirmingDelete {
		t.Fatal("confirmingDelete = true, want false after Esc")
	}
	if m.Result().Action != BrowserActionNone {
		t.Fatalf("Result().Action = %q, want %q after Esc cancel", m.Result().Action, BrowserActionNone)
	}
}

func TestBrowserDeleteIgnoresOtherKeysDuringConfirmation(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("del-run", time.Now(), "pi", "completed", "default", true),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'x'}}))

	// Press keys that are NOT y/n/Enter/Esc/Ctrl+C — should remain in confirmation.
	for _, key := range []rune{'j', 'k', 'q', 's', 'a', 'z'} {
		m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{key}}))
		if !m.confirmingDelete {
			t.Fatalf("confirmingDelete = false after pressing %q, want true (should be ignored)", string(key))
		}
	}
	if m.Result().Action != BrowserActionNone {
		t.Fatalf("Result().Action = %q, want %q (no action from ignored keys)", m.Result().Action, BrowserActionNone)
	}
}

func TestBrowserDeleteBlockedWhenUnavailable(t *testing.T) {
	s := browserTestSummary("no-del-run", time.Now(), "pi", "completed", "default")
	s.Actions.Delete = viewer.RunActionState{DisabledReason: "run directory unavailable"}

	m := NewBrowserModel([]viewer.RunSummary{s})
	m.width = 100
	m.height = 30

	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'x'}}))

	if m.confirmingDelete {
		t.Fatal("confirmingDelete = true, want false (delete unavailable)")
	}
}

func TestBrowserDeleteBlockedFromPreviewPane(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("del-run", time.Now(), "pi", "completed", "default", true),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30
	m.focusedPane = 1

	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'x'}}))

	if m.confirmingDelete {
		t.Fatal("confirmingDelete = true, want false (should only work from sessions pane)")
	}
}

func TestBrowserDeleteBlockedOnEmptyList(t *testing.T) {
	m := NewBrowserModel(nil)
	m.width = 100
	m.height = 30

	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'x'}}))

	if m.confirmingDelete {
		t.Fatal("confirmingDelete = true, want false (no sessions)")
	}
}

func TestBrowserDeleteNextRunIDMiddle(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("run-alpha", time.Date(2026, 3, 8, 11, 0, 0, 0, time.UTC), "pi", "completed", "default", true),
		browserTestSummaryWithActions("run-beta", time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC), "kiro", "completed", "plan", true),
		browserTestSummaryWithActions("run-gamma", time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC), "pi", "failed", "prompt", true),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	// Move to run-beta (middle).
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'j'}}))
	if current := m.currentSummary(); current == nil || current.RunID != "run-beta" {
		t.Fatalf("current = %v, want run-beta", current)
	}

	// Press x then y to confirm.
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'x'}}))
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'y'}}))

	result := m.Result()
	if result.DeleteNextRunID != "run-gamma" {
		t.Fatalf("DeleteNextRunID = %q, want %q (next run after middle)", result.DeleteNextRunID, "run-gamma")
	}
}

func TestBrowserDeleteNextRunIDEnd(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("run-alpha", time.Date(2026, 3, 8, 11, 0, 0, 0, time.UTC), "pi", "completed", "default", true),
		browserTestSummaryWithActions("run-beta", time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC), "kiro", "completed", "plan", true),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	// Move to run-beta (last).
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'j'}}))
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'x'}}))
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'y'}}))

	result := m.Result()
	if result.DeleteNextRunID != "run-alpha" {
		t.Fatalf("DeleteNextRunID = %q, want %q (previous run when at end)", result.DeleteNextRunID, "run-alpha")
	}
}

func TestBrowserDeleteNextRunIDOnly(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("only-run", time.Now(), "pi", "completed", "default", true),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'x'}}))
	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'y'}}))

	result := m.Result()
	if result.DeleteNextRunID != "" {
		t.Fatalf("DeleteNextRunID = %q, want empty (only item)", result.DeleteNextRunID)
	}
}

func TestBrowserStatusHintsIncludeDeleteWhenAvailable(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("del-run", time.Now(), "pi", "completed", "default", true),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	hints := m.browserStatusRightVariants()
	if len(hints) == 0 {
		t.Fatal("no status hints")
	}
	if !strings.Contains(hints[0], "delete") {
		t.Errorf("hints = %q, want 'delete'", hints[0])
	}
}

func TestBrowserStatusHintsExcludeDeleteWhenUnavailable(t *testing.T) {
	s := browserTestSummary("no-del-run", time.Now(), "pi", "completed", "default")
	s.Actions.Delete = viewer.RunActionState{DisabledReason: "run directory unavailable"}

	m := NewBrowserModel([]viewer.RunSummary{s})
	m.width = 100
	m.height = 30

	hints := m.browserStatusRightVariants()
	if len(hints) == 0 {
		t.Fatal("no status hints")
	}
	for _, h := range hints {
		if strings.Contains(h, "delete") {
			t.Errorf("hints = %q, should not contain 'delete' when unavailable", h)
			break
		}
	}
}

func TestBrowserStatusHintsForDeleteConfirmation(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("del-run", time.Now(), "pi", "completed", "default", true),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'x'}}))

	hints := m.browserStatusRightVariants()
	if len(hints) == 0 {
		t.Fatal("no confirmation hints")
	}
	if !strings.Contains(hints[0], "confirm") {
		t.Errorf("confirmation hints = %q, want 'confirm'", hints[0])
	}
	if !strings.Contains(hints[0], "cancel") {
		t.Errorf("confirmation hints = %q, want 'cancel'", hints[0])
	}
}

func TestBrowserStatusLeftForDeleteConfirmation(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("del-run", time.Now(), "pi", "completed", "default", true),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 30

	m = updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'x'}}))

	left := m.browserStatusLeft()
	if !strings.Contains(left, "Delete") {
		t.Errorf("confirmation status left = %q, want 'Delete'", left)
	}
	if !strings.Contains(left, "del-run") {
		t.Errorf("confirmation status left = %q, want run ID", left)
	}
	if !strings.Contains(left, "cannot be undone") {
		t.Errorf("confirmation status left = %q, want 'cannot be undone'", left)
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

func browserTestSummaryResumable(runID string, startedAt time.Time, agent, status, promptSource string, source viewer.ResumeSource, path string) viewer.RunSummary {
	s := browserTestSummaryWithActions(runID, startedAt, agent, status, promptSource, true)
	s.HasEffectivePrompt = true
	s.Actions.Resume = viewer.ResumeActionState{
		RunActionState: viewer.RunActionState{Available: true},
		Source:         source,
		Path:           path,
	}
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

// updateBrowserModelWithCmd returns both the updated model and the tea.Cmd so
// tests can verify whether tea.Quit was returned.
func updateBrowserModelWithCmd(t *testing.T, model BrowserModel, key tea.KeyMsg) (BrowserModel, tea.Cmd) {
	t.Helper()
	updated, cmd := model.Update(key)
	next, ok := updated.(BrowserModel)
	if !ok {
		t.Fatalf("updated model has type %T, want BrowserModel", updated)
	}
	return next, cmd
}

func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	_, ok := msg.(tea.QuitMsg)
	return ok
}

// --- Phase 4 task 2: focused model-level tests ---

// ===== Selection movement =====

func TestBrowserCursorClampAtEnd(t *testing.T) {
	summaries := makeSummaries(3)
	m := initBrowserModel(summaries, 100, 40)

	// Move to last row.
	m = pressKey(t, m, "j")
	m = pressKey(t, m, "j")
	if m.cursor != 2 {
		t.Fatalf("cursor = %d, want 2", m.cursor)
	}

	// j again should clamp at 2.
	m = pressKey(t, m, "j")
	if m.cursor != 2 {
		t.Fatalf("cursor = %d after j past end, want 2", m.cursor)
	}
}

func TestBrowserCursorClampAtStart(t *testing.T) {
	summaries := makeSummaries(3)
	m := initBrowserModel(summaries, 100, 40)

	// Cursor starts at 0. k should keep it there.
	if m.cursor != 0 {
		t.Fatalf("initial cursor = %d, want 0", m.cursor)
	}
	m = pressKey(t, m, "k")
	if m.cursor != 0 {
		t.Fatalf("cursor = %d after k at start, want 0", m.cursor)
	}
}

func TestBrowserGoToTop(t *testing.T) {
	summaries := makeSummaries(5)
	m := initBrowserModel(summaries, 100, 40)

	// Move to row 3.
	m = pressKey(t, m, "j")
	m = pressKey(t, m, "j")
	m = pressKey(t, m, "j")
	if m.cursor != 3 {
		t.Fatalf("cursor = %d, want 3", m.cursor)
	}

	// g should go to top.
	m = pressKey(t, m, "g")
	if m.cursor != 0 {
		t.Fatalf("cursor = %d after g, want 0", m.cursor)
	}
	if m.scroll != 0 {
		t.Fatalf("scroll = %d after g, want 0", m.scroll)
	}
}

func TestBrowserGoToBottom(t *testing.T) {
	summaries := makeSummaries(10)
	m := initBrowserModel(summaries, 100, 40)

	// G should go to last row.
	m = pressKey(t, m, "G")
	if m.cursor != 9 {
		t.Fatalf("cursor = %d after G, want 9", m.cursor)
	}
	if m.selectedRunID != summaries[9].RunID {
		t.Fatalf("selectedRunID = %q, want %q", m.selectedRunID, summaries[9].RunID)
	}
}

func TestBrowserHalfPageDown(t *testing.T) {
	summaries := makeSummaries(20)
	m := initBrowserModel(summaries, 100, 40)

	half := m.visibleSessionRows() / 2
	m = pressKeyMsg(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyCtrlD}))
	if m.cursor != half {
		t.Fatalf("cursor = %d after Ctrl+D, want %d", m.cursor, half)
	}
}

func TestBrowserHalfPageUp(t *testing.T) {
	summaries := makeSummaries(20)
	m := initBrowserModel(summaries, 100, 40)

	// Go to bottom first.
	m = pressKey(t, m, "G")
	lastCursor := m.cursor

	half := m.visibleSessionRows() / 2
	m = pressKeyMsg(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyCtrlU}))
	want := lastCursor - half
	if want < 0 {
		want = 0
	}
	if m.cursor != want {
		t.Fatalf("cursor = %d after Ctrl+U from bottom, want %d", m.cursor, want)
	}
}

func TestBrowserScrollFollowsCursor(t *testing.T) {
	summaries := makeSummaries(30)
	m := initBrowserModel(summaries, 100, 20)

	visible := m.visibleSessionRows()
	if visible < 2 {
		t.Skip("terminal too small for scroll test")
	}

	// Move cursor past the visible area.
	for i := 0; i < visible+2; i++ {
		m = pressKey(t, m, "j")
	}

	// Scroll must have advanced so cursor is visible.
	if m.cursor < m.scroll || m.cursor >= m.scroll+visible {
		t.Fatalf("cursor %d not in visible range [%d, %d)", m.cursor, m.scroll, m.scroll+visible)
	}

	// Now go back up past scroll.
	for i := 0; i < visible+2; i++ {
		m = pressKey(t, m, "k")
	}
	if m.cursor < m.scroll || m.cursor >= m.scroll+visible {
		t.Fatalf("cursor %d not in visible range after scrolling up [%d, %d)", m.cursor, m.scroll, m.scroll+visible)
	}
}

func TestBrowserPreviewScrollInPreviewPane(t *testing.T) {
	summaries := makeSummaries(3)
	m := initBrowserModel(summaries, 100, 20)

	initialCursor := m.cursor

	// Switch to preview pane.
	m = pressKey(t, m, "tab")
	if m.focusedPane != 1 {
		t.Fatalf("focusedPane = %d after Tab, want 1", m.focusedPane)
	}

	// j/k in preview should scroll preview, not move cursor.
	m = pressKey(t, m, "j")
	if m.cursor != initialCursor {
		t.Fatalf("cursor moved in preview pane: got %d, want %d", m.cursor, initialCursor)
	}
	if m.previewScroll < 0 {
		t.Fatalf("previewScroll = %d, want >= 0", m.previewScroll)
	}
}

func TestBrowserTabTogglesFocus(t *testing.T) {
	summaries := makeSummaries(1)
	m := initBrowserModel(summaries, 100, 30)

	if m.focusedPane != 0 {
		t.Fatalf("initial focusedPane = %d, want 0", m.focusedPane)
	}
	m = pressKey(t, m, "tab")
	if m.focusedPane != 1 {
		t.Fatalf("focusedPane = %d after 1st Tab, want 1", m.focusedPane)
	}
	m = pressKey(t, m, "tab")
	if m.focusedPane != 0 {
		t.Fatalf("focusedPane = %d after 2nd Tab, want 0", m.focusedPane)
	}
}

func TestBrowserMoveCursorResetsPreviewScroll(t *testing.T) {
	summaries := makeSummaries(5)
	m := initBrowserModel(summaries, 100, 20)

	// Switch to preview, scroll it.
	m = pressKey(t, m, "tab")
	m = pressKey(t, m, "j")
	m = pressKey(t, m, "j")
	if m.previewScroll == 0 {
		// Preview content may be short — that's okay, test still valid.
	}

	// Switch back to sessions and move cursor.
	m = pressKey(t, m, "tab")
	m = pressKey(t, m, "j")

	// Preview scroll should reset when cursor changes.
	if m.previewScroll != 0 {
		t.Fatalf("previewScroll = %d after cursor move, want 0", m.previewScroll)
	}
}

func TestBrowserArrowKeysMoveCursor(t *testing.T) {
	summaries := makeSummaries(5)
	m := initBrowserModel(summaries, 100, 40)

	m = pressKey(t, m, "down")
	if m.cursor != 1 {
		t.Fatalf("cursor = %d after down, want 1", m.cursor)
	}
	m = pressKey(t, m, "up")
	if m.cursor != 0 {
		t.Fatalf("cursor = %d after up, want 0", m.cursor)
	}
}

// ===== Sort state =====

func TestBrowserSortCycleAllModes(t *testing.T) {
	summaries := makeSummaries(3)
	m := initBrowserModel(summaries, 100, 40)

	expected := []browserSortMode{
		browserSortOldest,
		browserSortRunID,
		browserSortAgent,
		browserSortStatus,
		browserSortPrompt,
		browserSortNewest, // wraps back
	}
	for _, want := range expected {
		m = pressKey(t, m, "s")
		if m.sortMode != want {
			t.Fatalf("after s: sortMode = %q, want %q", m.sortMode, want)
		}
	}
}

func TestBrowserSortOrderNewest(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("run-c", time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC), "pi", "completed", "default"),
		browserTestSummary("run-a", time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC), "pi", "completed", "default"),
		browserTestSummary("run-b", time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC), "pi", "completed", "default"),
	}
	m := NewBrowserModel(summaries)

	// Default is newest-first.
	got := browserRunIDs(m.summaries)
	want := []string{"run-a", "run-b", "run-c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("newest order = %v, want %v", got, want)
	}
}

func TestBrowserSortOrderOldest(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("run-c", time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC), "pi", "completed", "default"),
		browserTestSummary("run-a", time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC), "pi", "completed", "default"),
		browserTestSummary("run-b", time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC), "pi", "completed", "default"),
	}
	m := NewBrowserModel(summaries)
	m.width = 100
	m.height = 40

	// Cycle to oldest.
	m = pressKey(t, m, "s")
	got := browserRunIDs(m.summaries)
	want := []string{"run-c", "run-b", "run-a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("oldest order = %v, want %v", got, want)
	}
}

func TestBrowserSortOrderByRunID(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("gamma", time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC), "pi", "completed", "default"),
		browserTestSummary("alpha", time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC), "pi", "completed", "default"),
		browserTestSummary("beta", time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC), "pi", "completed", "default"),
	}
	m := NewBrowserModel(summaries)
	m.sortMode = browserSortRunID
	m.applyBrowserView()

	got := browserRunIDs(m.summaries)
	want := []string{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("runID order = %v, want %v", got, want)
	}
}

func TestBrowserSortOrderByAgent(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("r1", time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC), "pi", "completed", "default"),
		browserTestSummary("r2", time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC), "kiro", "completed", "default"),
		browserTestSummary("r3", time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC), "aider", "completed", "default"),
	}
	m := NewBrowserModel(summaries)
	m.sortMode = browserSortAgent
	m.applyBrowserView()

	got := browserRunIDs(m.summaries)
	want := []string{"r3", "r2", "r1"} // aider < kiro < pi
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agent order = %v, want %v", got, want)
	}
}

func TestBrowserSortOrderByStatus(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("r1", time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC), "pi", "interrupted", "default"),
		browserTestSummary("r2", time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC), "pi", "completed", "default"),
		browserTestSummary("r3", time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC), "pi", "failed", "default"),
	}
	m := NewBrowserModel(summaries)
	m.sortMode = browserSortStatus
	m.applyBrowserView()

	got := browserRunIDs(m.summaries)
	want := []string{"r2", "r3", "r1"} // completed < failed < interrupted
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status order = %v, want %v", got, want)
	}
}

func TestBrowserSortOrderByPrompt(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("r1", time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC), "pi", "completed", "prompt"),
		browserTestSummary("r2", time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC), "pi", "completed", "default"),
		browserTestSummary("r3", time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC), "pi", "completed", "plan"),
	}
	m := NewBrowserModel(summaries)
	m.sortMode = browserSortPrompt
	m.applyBrowserView()

	got := browserRunIDs(m.summaries)
	want := []string{"r2", "r3", "r1"} // default < plan < prompt
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("prompt order = %v, want %v", got, want)
	}
}

// ===== Filter/search state =====

func TestBrowserFilterCycleAgentKey(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("r1", time.Now(), "pi", "completed", "default"),
		browserTestSummary("r2", time.Now().Add(-time.Hour), "kiro", "completed", "default"),
		browserTestSummary("r3", time.Now().Add(-2*time.Hour), "pi", "failed", "plan"),
	}
	m := initBrowserModel(summaries, 100, 40)

	// First press: cycles to first agent option.
	m = pressKey(t, m, "a")
	if m.agentFilter == "" {
		t.Fatal("agentFilter still empty after a")
	}
	first := m.agentFilter

	// Keep pressing until back to empty.
	for i := 0; i < len(m.agentOptions); i++ {
		m = pressKey(t, m, "a")
	}
	if m.agentFilter != "" {
		t.Fatalf("agentFilter = %q after full cycle, want empty", m.agentFilter)
	}

	// Filter should actually reduce visible rows.
	m = pressKey(t, m, "a")
	if m.agentFilter != first {
		t.Fatalf("agentFilter = %q, want %q", m.agentFilter, first)
	}
	for _, s := range m.summaries {
		if !strings.EqualFold(s.Agent, first) {
			t.Fatalf("visible run %q has agent %q, want %q", s.RunID, s.Agent, first)
		}
	}
}

func TestBrowserFilterCycleDateKey(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("r1", time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC), "pi", "completed", "default"),
		browserTestSummary("r2", time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC), "pi", "completed", "default"),
	}
	m := initBrowserModel(summaries, 100, 40)

	// First d: filters to one date.
	m = pressKey(t, m, "d")
	if m.dateFilter == "" {
		t.Fatal("dateFilter still empty after d")
	}
	if len(m.summaries) != 1 {
		t.Fatalf("visible after date filter = %d, want 1", len(m.summaries))
	}
}

func TestBrowserFilterCycleStatusKey(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("r1", time.Now(), "pi", "completed", "default"),
		browserTestSummary("r2", time.Now().Add(-time.Hour), "pi", "failed", "default"),
	}
	m := initBrowserModel(summaries, 100, 40)

	m = pressKey(t, m, "t")
	if m.statusFilter == "" {
		t.Fatal("statusFilter still empty after t")
	}
	if len(m.summaries) != 1 {
		t.Fatalf("visible after status filter = %d, want 1", len(m.summaries))
	}
}

func TestBrowserFilterCyclePromptKey(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("r1", time.Now(), "pi", "completed", "prompt"),
		browserTestSummary("r2", time.Now().Add(-time.Hour), "pi", "completed", "plan"),
	}
	m := initBrowserModel(summaries, 100, 40)

	m = pressKey(t, m, "p")
	if m.promptFilter == "" {
		t.Fatal("promptFilter still empty after p")
	}
	if len(m.summaries) != 1 {
		t.Fatalf("visible after prompt filter = %d, want 1", len(m.summaries))
	}
}

func TestBrowserSearchEnterPreservesQuery(t *testing.T) {
	summaries := makeSummaries(3)
	m := initBrowserModel(summaries, 100, 40)

	m = pressKey(t, m, "/")
	m = pressRune(t, m, 't')
	m = pressRune(t, m, 'e')
	m = pressRune(t, m, 's')
	m = pressRune(t, m, 't')

	if m.searchQuery != "test" {
		t.Fatalf("searchQuery = %q, want %q", m.searchQuery, "test")
	}

	// Enter exits search mode but keeps the query.
	m = pressKeyMsg(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyEnter}))
	if m.searching {
		t.Fatal("searching = true after Enter, want false")
	}
	if m.searchQuery != "test" {
		t.Fatalf("searchQuery = %q after Enter, want %q", m.searchQuery, "test")
	}
}

func TestBrowserSearchEscPreservesQuery(t *testing.T) {
	summaries := makeSummaries(3)
	m := initBrowserModel(summaries, 100, 40)

	m = pressKey(t, m, "/")
	m = pressRune(t, m, 'q')
	m = pressRune(t, m, 'r')
	m = pressRune(t, m, 'y')

	// Esc exits search mode but preserves what was typed.
	m = pressKeyMsg(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyEsc}))
	if m.searching {
		t.Fatal("searching = true after Esc, want false")
	}
	if m.searchQuery != "qry" {
		t.Fatalf("searchQuery = %q after Esc, want %q", m.searchQuery, "qry")
	}
}

func TestBrowserSearchCtrlUClearsText(t *testing.T) {
	summaries := makeSummaries(3)
	m := initBrowserModel(summaries, 100, 40)

	m = pressKey(t, m, "/")
	m = pressRune(t, m, 'a')
	m = pressRune(t, m, 'b')
	m = pressRune(t, m, 'c')

	m = pressKeyMsg(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyCtrlU}))
	if m.searchQuery != "" {
		t.Fatalf("searchQuery = %q after Ctrl+U, want empty", m.searchQuery)
	}
	if !m.searching {
		t.Fatal("searching = false after Ctrl+U, want true (still in search mode)")
	}
}

func TestBrowserSearchDeleteClearsText(t *testing.T) {
	summaries := makeSummaries(3)
	m := initBrowserModel(summaries, 100, 40)

	m = pressKey(t, m, "/")
	m = pressRune(t, m, 'x')
	m = pressRune(t, m, 'y')

	m = pressKeyMsg(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyDelete}))
	if m.searchQuery != "" {
		t.Fatalf("searchQuery = %q after Delete, want empty", m.searchQuery)
	}
}

func TestBrowserSearchSpaceInQuery(t *testing.T) {
	summaries := makeSummaries(3)
	m := initBrowserModel(summaries, 100, 40)

	m = pressKey(t, m, "/")
	m = pressRune(t, m, 'a')
	m = pressKeyMsg(t, m, tea.KeyMsg(tea.Key{Type: tea.KeySpace}))
	m = pressRune(t, m, 'b')

	if m.searchQuery != "a b" {
		t.Fatalf("searchQuery = %q, want %q", m.searchQuery, "a b")
	}
}

func TestBrowserClearAllFiltersKey(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("r1", time.Now(), "pi", "completed", "default"),
		browserTestSummary("r2", time.Now().Add(-time.Hour), "kiro", "failed", "plan"),
	}
	m := initBrowserModel(summaries, 100, 40)

	// Set filters.
	m = pressKey(t, m, "a") // agent filter
	m = pressKey(t, m, "/") // enter search
	m = pressRune(t, m, 'z')
	m = pressKeyMsg(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyEnter})) // exit search

	// Verify filters are set.
	if m.agentFilter == "" {
		t.Fatal("agentFilter not set")
	}
	if m.searchQuery == "" {
		t.Fatal("searchQuery not set")
	}

	// c should clear everything.
	m = pressKey(t, m, "c")
	if m.agentFilter != "" {
		t.Fatalf("agentFilter = %q after c, want empty", m.agentFilter)
	}
	if m.searchQuery != "" {
		t.Fatalf("searchQuery = %q after c, want empty", m.searchQuery)
	}
	if m.searching {
		t.Fatal("searching = true after c, want false")
	}
	if len(m.summaries) != 2 {
		t.Fatalf("visible summaries = %d after clear, want 2", len(m.summaries))
	}
}

func TestBrowserHeaderShowsStateTokens(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummary("r1", time.Now(), "pi", "completed", "default"),
		browserTestSummary("r2", time.Now().Add(-time.Hour), "kiro", "failed", "plan"),
	}
	m := initBrowserModel(summaries, 200, 40) // wide enough for all tokens

	tokens := m.browserStateTokens()
	if len(tokens) == 0 || tokens[0] != "sort:newest" {
		t.Fatalf("first token = %v, want sort:newest", tokens)
	}

	// Set an agent filter and search.
	m.agentFilter = "pi"
	m.searchQuery = "test"
	tokens = m.browserStateTokens()

	found := map[string]bool{"sort:newest": false, "agent:pi": false, "/test": false}
	for _, tok := range tokens {
		for k := range found {
			if tok == k {
				found[k] = true
			}
		}
	}
	for k, v := range found {
		if !v {
			t.Errorf("token %q not found in %v", k, tokens)
		}
	}
}

// ===== Confirmation flows =====

func TestBrowserDeleteConfirmCtrlCQuits(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("del-run", time.Now(), "pi", "completed", "default", true),
	}
	m := initBrowserModel(summaries, 100, 30)

	// Enter confirmation.
	m = pressKey(t, m, "x")
	if !m.confirmingDelete {
		t.Fatal("not in confirmation mode after x")
	}

	// Ctrl+C during confirmation should quit without performing delete.
	m, cmd := updateBrowserModelWithCmd(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyCtrlC}))
	if !isQuitCmd(cmd) {
		t.Fatal("Ctrl+C during confirmation did not emit tea.Quit")
	}
	if m.Result().Action != BrowserActionNone {
		t.Fatalf("Result().Action = %q after Ctrl+C, want %q", m.Result().Action, BrowserActionNone)
	}
	if m.confirmingDelete {
		t.Fatal("confirmingDelete still true after Ctrl+C")
	}
}

func TestBrowserConfirmDeleteViewShowsPrompt(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("del-target", time.Now(), "pi", "completed", "default", true),
	}
	m := initBrowserModel(summaries, 100, 30)

	m = pressKey(t, m, "x")

	view := m.View()
	if !strings.Contains(view, "del-target") {
		t.Error("confirmation view does not contain the run ID")
	}
}

// ===== Action results emitted back to main =====

func TestBrowserResultDefaultNone(t *testing.T) {
	m := NewBrowserModel(makeSummaries(3))
	result := m.Result()
	if result.Action != BrowserActionNone {
		t.Fatalf("default Result().Action = %q, want %q", result.Action, BrowserActionNone)
	}
	if result.RunID != "" {
		t.Fatalf("default Result().RunID = %q, want empty", result.RunID)
	}
}

func TestBrowserQuitOnQ(t *testing.T) {
	m := initBrowserModel(makeSummaries(3), 100, 40)

	m, cmd := updateBrowserModelWithCmd(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'q'}}))
	if !isQuitCmd(cmd) {
		t.Fatal("q did not emit tea.Quit")
	}
	if m.Result().Action != BrowserActionNone {
		t.Fatalf("Result().Action after q = %q, want %q", m.Result().Action, BrowserActionNone)
	}
}

func TestBrowserQuitOnEsc(t *testing.T) {
	m := initBrowserModel(makeSummaries(3), 100, 40)

	m, cmd := updateBrowserModelWithCmd(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyEsc}))
	if !isQuitCmd(cmd) {
		t.Fatal("Esc did not emit tea.Quit")
	}
	if m.Result().Action != BrowserActionNone {
		t.Fatalf("Result().Action after Esc = %q, want %q", m.Result().Action, BrowserActionNone)
	}
}

func TestBrowserQuitOnCtrlC(t *testing.T) {
	m := initBrowserModel(makeSummaries(3), 100, 40)

	m, cmd := updateBrowserModelWithCmd(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyCtrlC}))
	if !isQuitCmd(cmd) {
		t.Fatal("Ctrl+C did not emit tea.Quit")
	}
	if m.Result().Action != BrowserActionNone {
		t.Fatalf("Result().Action after Ctrl+C = %q, want %q", m.Result().Action, BrowserActionNone)
	}
}

func TestBrowserOpenResultPreservesRunID(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("run-one", time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC), "pi", "completed", "default", true),
		browserTestSummaryWithActions("run-two", time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC), "kiro", "completed", "plan", true),
	}
	m := initBrowserModel(summaries, 100, 40)

	// Select second row and open.
	m = pressKey(t, m, "j")
	m, cmd := updateBrowserModelWithCmd(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyEnter}))
	if !isQuitCmd(cmd) {
		t.Fatal("Enter did not emit tea.Quit")
	}
	result := m.Result()
	if result.Action != BrowserActionOpen {
		t.Fatalf("Action = %q, want %q", result.Action, BrowserActionOpen)
	}
	if result.RunID != "run-two" {
		t.Fatalf("RunID = %q, want %q", result.RunID, "run-two")
	}
}

func TestBrowserDeleteResultIncludesAllFields(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryWithActions("del-first", time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC), "pi", "completed", "default", true),
		browserTestSummaryWithActions("del-second", time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC), "pi", "completed", "default", true),
	}
	m := initBrowserModel(summaries, 100, 40)

	// Confirm delete of first row.
	m = pressKey(t, m, "x")
	m, cmd := updateBrowserModelWithCmd(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'y'}}))
	if !isQuitCmd(cmd) {
		t.Fatal("confirm did not emit tea.Quit")
	}

	result := m.Result()
	if result.Action != BrowserActionDelete {
		t.Fatalf("Action = %q, want %q", result.Action, BrowserActionDelete)
	}
	if result.RunID != "del-first" {
		t.Fatalf("RunID = %q, want %q", result.RunID, "del-first")
	}
	if result.DeleteDir != "/tmp/del-first" {
		t.Fatalf("DeleteDir = %q, want %q", result.DeleteDir, "/tmp/del-first")
	}
	if result.DeleteNextRunID != "del-second" {
		t.Fatalf("DeleteNextRunID = %q, want %q", result.DeleteNextRunID, "del-second")
	}
}

func TestBrowserResumeResultIncludesAllFields(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryResumable("res-run", time.Now(), "kiro", "interrupted", "plan",
			viewer.ResumeSourcePlanFile, "/path/to/plan.md"),
	}
	m := initBrowserModel(summaries, 100, 40)

	m, cmd := updateBrowserModelWithCmd(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'r'}}))
	if !isQuitCmd(cmd) {
		t.Fatal("r did not emit tea.Quit")
	}

	result := m.Result()
	if result.Action != BrowserActionResume {
		t.Fatalf("Action = %q, want %q", result.Action, BrowserActionResume)
	}
	if result.RunID != "res-run" {
		t.Fatalf("RunID = %q, want %q", result.RunID, "res-run")
	}
	if result.ResumeAgent != "kiro" {
		t.Fatalf("ResumeAgent = %q, want %q", result.ResumeAgent, "kiro")
	}
	if result.ResumeSource != viewer.ResumeSourcePlanFile {
		t.Fatalf("ResumeSource = %q, want %q", result.ResumeSource, viewer.ResumeSourcePlanFile)
	}
	if result.ResumePath != "/path/to/plan.md" {
		t.Fatalf("ResumePath = %q, want %q", result.ResumePath, "/path/to/plan.md")
	}
}

// ===== Pure helper function tests =====

func TestBrowserPromptDescriptor(t *testing.T) {
	tests := []struct {
		name   string
		s      viewer.RunSummary
		want   string
	}{
		{
			name: "label and source",
			s:    viewer.RunSummary{PromptLabel: "my-plan", PromptSource: "plan"},
			want: "my-plan (plan)",
		},
		{
			name: "label same as source",
			s:    viewer.RunSummary{PromptLabel: "plan", PromptSource: "plan"},
			want: "plan",
		},
		{
			name: "empty label with effective prompt",
			s:    viewer.RunSummary{PromptLabel: "", HasEffectivePrompt: true},
			want: "saved prompt",
		},
		{
			name: "unknown label with effective prompt",
			s:    viewer.RunSummary{PromptLabel: "unknown", HasEffectivePrompt: true},
			want: "saved prompt",
		},
		{
			name: "empty label without effective prompt",
			s:    viewer.RunSummary{PromptLabel: ""},
			want: "unknown",
		},
		{
			name: "source is unknown",
			s:    viewer.RunSummary{PromptLabel: "my-plan", PromptSource: "unknown"},
			want: "my-plan",
		},
		{
			name: "source is empty",
			s:    viewer.RunSummary{PromptLabel: "my-plan", PromptSource: ""},
			want: "my-plan",
		},
		{
			name: "whitespace label trimmed",
			s:    viewer.RunSummary{PromptLabel: "  ", HasEffectivePrompt: false},
			want: "unknown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := browserPromptDescriptor(tt.s)
			if got != tt.want {
				t.Errorf("browserPromptDescriptor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBrowserResumeSourceLabel(t *testing.T) {
	tests := []struct {
		source viewer.ResumeSource
		want   string
	}{
		{viewer.ResumeSourceEffectivePrompt, "effective prompt"},
		{viewer.ResumeSourcePromptFile, "prompt file"},
		{viewer.ResumeSourcePlanFile, "plan file"},
		{viewer.ResumeSourceDefault, "default prompt"},
		{viewer.ResumeSource("something_else"), "saved artifacts"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := browserResumeSourceLabel(tt.source)
			if got != tt.want {
				t.Errorf("browserResumeSourceLabel(%q) = %q, want %q", tt.source, got, tt.want)
			}
		})
	}
}

func TestBrowserSummaryTime(t *testing.T) {
	now := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	earlier := now.Add(-time.Hour)

	tests := []struct {
		name string
		s    viewer.RunSummary
		want time.Time
	}{
		{"prefers StartedAt", viewer.RunSummary{StartedAt: now, SortTime: earlier}, now},
		{"falls back to SortTime", viewer.RunSummary{SortTime: earlier}, earlier},
		{"zero when both empty", viewer.RunSummary{}, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := browserSummaryTime(tt.s)
			if !got.Equal(tt.want) {
				t.Errorf("browserSummaryTime() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBrowserCompactDate(t *testing.T) {
	now := time.Date(2026, 3, 8, 14, 30, 0, 0, time.UTC)
	got := browserCompactDate(viewer.RunSummary{StartedAt: now})
	if got != "03-08 14:30" {
		t.Errorf("browserCompactDate() = %q, want %q", got, "03-08 14:30")
	}

	got = browserCompactDate(viewer.RunSummary{})
	if got != "unknown" {
		t.Errorf("browserCompactDate(zero) = %q, want %q", got, "unknown")
	}
}

func TestBrowserLongDate(t *testing.T) {
	now := time.Date(2026, 3, 8, 14, 30, 45, 0, time.UTC)

	tests := []struct {
		name string
		s    viewer.RunSummary
		want string
	}{
		{"with time", viewer.RunSummary{StartedAt: now}, "2026-03-08 14:30:45"},
		{"zero with text", viewer.RunSummary{StartedAtText: "march 8"}, "march 8"},
		{"zero without text", viewer.RunSummary{}, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := browserLongDate(tt.s)
			if got != tt.want {
				t.Errorf("browserLongDate() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBrowserFilterLabel(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"", "all"},
		{"  ", "all"},
		{"pi", "pi"},
	}
	for _, tt := range tests {
		got := browserFilterLabel(tt.input)
		if got != tt.want {
			t.Errorf("browserFilterLabel(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBrowserSearchLabel(t *testing.T) {
	tests := []struct {
		query     string
		searching bool
		want      string
	}{
		{"", false, "all"},
		{"", true, "(editing)"},
		{"foo", false, "foo"},
		{"foo", true, "foo_"},
	}
	for _, tt := range tests {
		got := browserSearchLabel(tt.query, tt.searching)
		if got != tt.want {
			t.Errorf("browserSearchLabel(%q, %v) = %q, want %q", tt.query, tt.searching, got, tt.want)
		}
	}
}

func TestPadToWidth(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{"pad short string", "hi", 5, "hi   "},
		{"exact width", "hello", 5, "hello"},
		{"truncate long", "hello world", 5, "he..."},
		{"zero width", "hi", 0, "hi"},
		{"negative width", "hi", -1, "hi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := padToWidth(tt.input, tt.width)
			if got != tt.want {
				t.Errorf("padToWidth(%q, %d) = %q, want %q", tt.input, tt.width, got, tt.want)
			}
		})
	}
}

func TestBrowserFacetSortKey(t *testing.T) {
	tests := []struct {
		input    string
		wantRank int
		wantKey  string
	}{
		{"Pi", 0, "pi"},
		{"unknown", 1, "unknown"},
		{"", 1, ""},
		{"  ", 1, ""},
		{"Kiro", 0, "kiro"},
	}
	for _, tt := range tests {
		rank, key := browserFacetSortKey(tt.input)
		if rank != tt.wantRank || key != tt.wantKey {
			t.Errorf("browserFacetSortKey(%q) = (%d, %q), want (%d, %q)", tt.input, rank, key, tt.wantRank, tt.wantKey)
		}
	}
}

func TestBrowserFacetLess(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"pi", "unknown", true},         // known < unknown
		{"unknown", "pi", false},        // unknown > known
		{"alpha", "beta", true},         // alphabetical within same rank
		{"", "unknown", true},           // both rank 1, "" < "unknown" alphabetically
		{"unknown", "", false},          // both rank 1, "unknown" > "" alphabetically
	}
	for _, tt := range tests {
		got := browserFacetLess(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("browserFacetLess(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestBrowserArtifactState(t *testing.T) {
	tests := []struct {
		ok   bool
		err  string
		want string
	}{
		{true, "", "ok"},
		{true, "ignored error", "ok"},
		{false, "corrupt file", "corrupt file"},
		{false, "", "unavailable"},
	}
	for _, tt := range tests {
		got := browserArtifactState(tt.ok, tt.err)
		if got != tt.want {
			t.Errorf("browserArtifactState(%v, %q) = %q, want %q", tt.ok, tt.err, got, tt.want)
		}
	}
}

func TestBrowserMetaState(t *testing.T) {
	tests := []struct {
		name string
		s    viewer.RunSummary
		want string
	}{
		{"has meta", viewer.RunSummary{HasMeta: true}, "ok"},
		{"no meta with error", viewer.RunSummary{ArtifactError: "missing"}, "missing"},
		{"no meta no error", viewer.RunSummary{}, "unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := browserMetaState(tt.s)
			if got != tt.want {
				t.Errorf("browserMetaState() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBrowserOpenState(t *testing.T) {
	got := browserOpenState(viewer.RunActionState{Available: true})
	if got != "available" {
		t.Errorf("browserOpenState(available) = %q, want %q", got, "available")
	}

	got = browserOpenState(viewer.RunActionState{DisabledReason: "no events"})
	if got != "unavailable — no events" {
		t.Errorf("browserOpenState(disabled) = %q, want %q", got, "unavailable — no events")
	}
}

func TestBrowserDeleteState(t *testing.T) {
	got := browserDeleteState(viewer.RunActionState{Available: true})
	if got != "available" {
		t.Errorf("browserDeleteState(available) = %q, want %q", got, "available")
	}

	got = browserDeleteState(viewer.RunActionState{DisabledReason: "protected"})
	if got != "unavailable — protected" {
		t.Errorf("browserDeleteState(disabled) = %q, want %q", got, "unavailable — protected")
	}
}

func TestBrowserResumeState(t *testing.T) {
	got := browserResumeState(viewer.ResumeActionState{
		RunActionState: viewer.RunActionState{Available: true},
		Source:         viewer.ResumeSourcePlanFile,
	})
	if got != "available from plan file" {
		t.Errorf("browserResumeState(available) = %q, want %q", got, "available from plan file")
	}

	got = browserResumeState(viewer.ResumeActionState{
		RunActionState: viewer.RunActionState{DisabledReason: "no prompt"},
	})
	if got != "unavailable — no prompt" {
		t.Errorf("browserResumeState(disabled) = %q, want %q", got, "unavailable — no prompt")
	}
}

func TestDefaultBrowserReason(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"", "not available"},
		{"  ", "not available"},
		{"missing file", "missing file"},
	}
	for _, tt := range tests {
		got := defaultBrowserReason(tt.input)
		if got != tt.want {
			t.Errorf("defaultBrowserReason(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBrowserPreviewTextStandalone(t *testing.T) {
	t.Run("nil summary", func(t *testing.T) {
		got := browserPreviewText(nil)
		if !strings.Contains(got, "No saved runs") {
			t.Errorf("nil summary text should mention no saved runs, got %q", got)
		}
	})

	t.Run("full summary", func(t *testing.T) {
		now := time.Date(2026, 3, 8, 14, 30, 45, 0, time.UTC)
		s := &viewer.RunSummary{
			RunID:               "abc-123",
			Dir:                 "/tmp/abc-123",
			StartedAt:           now,
			Agent:               "pi",
			Status:              "completed",
			IterationsCompleted: 3,
			PromptLabel:         "my-plan",
			PromptSource:        "plan",
			PromptPath:          "/path/to/plan.md",
			HasMeta:             true,
			HasEvents:           true,
			HasEffectivePrompt:  true,
			Actions: viewer.RunActions{
				Open:   viewer.RunActionState{Available: true},
				Delete: viewer.RunActionState{Available: true},
				Resume: viewer.ResumeActionState{
					RunActionState: viewer.RunActionState{Available: true},
					Source:         viewer.ResumeSourcePlanFile,
					Path:           "/path/to/plan.md",
				},
			},
		}
		got := browserPreviewText(s)
		for _, want := range []string{
			"abc-123", "pi", "completed", "3", "/tmp/abc-123",
			"my-plan (plan)", "/path/to/plan.md",
			"meta.json: ok", "events.jsonl: ok", "effective-prompt.md: ok",
			"open: available", "resume: available from plan file", "delete: available",
			"source path: /path/to/plan.md",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("browserPreviewText missing %q in:\n%s", want, got)
			}
		}
	})

	t.Run("summary with errors shows notes", func(t *testing.T) {
		s := &viewer.RunSummary{
			RunID:         "err-run",
			Dir:           "/tmp/err-run",
			Agent:         "pi",
			Status:        "failed",
			ArtifactError: "meta.json missing",
			EventsError:   "events corrupt",
		}
		got := browserPreviewText(s)
		if !strings.Contains(got, "Notes") {
			t.Error("expected Notes section for errors")
		}
		if !strings.Contains(got, "meta.json missing") {
			t.Error("expected artifact error in notes")
		}
		if !strings.Contains(got, "events corrupt") {
			t.Error("expected events error in notes")
		}
	})

	t.Run("zero time with text shows raw date", func(t *testing.T) {
		s := &viewer.RunSummary{
			RunID:         "raw-date",
			Dir:           "/tmp/raw-date",
			Agent:         "pi",
			Status:        "completed",
			StartedAtText: "march 8 2026",
		}
		got := browserPreviewText(s)
		if !strings.Contains(got, "Started raw: march 8 2026") {
			t.Errorf("expected raw date in output, got:\n%s", got)
		}
	})
}

func TestBrowserSummaryDate(t *testing.T) {
	now := time.Date(2026, 3, 8, 14, 30, 0, 0, time.UTC)

	got := browserSummaryDate(viewer.RunSummary{StartedAt: now})
	if got != "2026-03-08" {
		t.Errorf("browserSummaryDate() = %q, want %q", got, "2026-03-08")
	}

	got = browserSummaryDate(viewer.RunSummary{})
	if got != "unknown" {
		t.Errorf("browserSummaryDate(zero) = %q, want %q", got, "unknown")
	}
}

func TestBrowserJoinHints(t *testing.T) {
	prefix := []browserHint{{Key: "a", Label: "1"}}
	rest := browserHint{Key: "b", Label: "2"}

	got := browserJoinHints(prefix, rest)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Key != "a" || got[1].Key != "b" {
		t.Errorf("got %v, want [{a 1} {b 2}]", got)
	}
	// Verify it doesn't modify the prefix slice.
	if len(prefix) != 1 {
		t.Error("prefix was modified")
	}
}

// ===== Test helpers =====

// makeSummaries creates N test summaries ordered by decreasing time.
func makeSummaries(n int) []viewer.RunSummary {
	base := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)
	summaries := make([]viewer.RunSummary, n)
	for i := 0; i < n; i++ {
		summaries[i] = browserTestSummaryWithActions(
			fmt.Sprintf("run-%03d", i),
			base.Add(-time.Duration(i)*time.Hour),
			"pi", "completed", "default", true,
		)
	}
	return summaries
}

// initBrowserModel creates a browser model with the given window size applied.
func initBrowserModel(summaries []viewer.RunSummary, width, height int) BrowserModel {
	m := NewBrowserModel(summaries)
	m.width = width
	m.height = height
	return m
}

// pressKey sends a string-keyed message (for simple keys like "j", "k", "s").
func pressKey(t *testing.T, m BrowserModel, key string) BrowserModel {
	t.Helper()
	return updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune(key)}))
}

// pressRune sends a single rune keypress.
func pressRune(t *testing.T, m BrowserModel, r rune) BrowserModel {
	t.Helper()
	return updateBrowserModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{r}}))
}

// pressKeyMsg sends a tea.KeyMsg directly.
func pressKeyMsg(t *testing.T, m BrowserModel, msg tea.KeyMsg) BrowserModel {
	t.Helper()
	return updateBrowserModel(t, m, msg)
}
