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
