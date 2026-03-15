package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/fsmiamoto/ralfinho/internal/viewer"
)

type browserNoopMsg struct{}

func TestBrowserInitAndUpdateWindowSizeAndNoOp(t *testing.T) {
	m := NewBrowserModel(makeSummaries(1))

	if cmd := m.Init(); cmd != nil {
		t.Fatalf("Init() = %v, want nil", cmd)
	}

	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 91, Height: 27})
	if cmd != nil {
		t.Fatalf("Update(WindowSizeMsg) cmd = %v, want nil", cmd)
	}

	next, ok := updated.(BrowserModel)
	if !ok {
		t.Fatalf("Update(WindowSizeMsg) model type = %T, want BrowserModel", updated)
	}
	if next.width != 91 || next.height != 27 {
		t.Fatalf("window size = %dx%d, want 91x27", next.width, next.height)
	}

	unchanged, cmd := next.Update(browserNoopMsg{})
	if cmd != nil {
		t.Fatalf("Update(no-op msg) cmd = %v, want nil", cmd)
	}

	final, ok := unchanged.(BrowserModel)
	if !ok {
		t.Fatalf("Update(no-op msg) model type = %T, want BrowserModel", unchanged)
	}
	if !reflect.DeepEqual(final, next) {
		t.Fatalf("Update(no-op msg) changed model:\n got: %#v\nwant: %#v", final, next)
	}
}

func TestBrowserQuestionMarkStartsSearchAndUnknownKeyIsNoOp(t *testing.T) {
	t.Run("question mark enters search mode", func(t *testing.T) {
		m := initBrowserModel(makeSummaries(1), 100, 30)
		m = pressKey(t, m, "?")
		if !m.searching {
			t.Fatal("searching = false after ?, want true")
		}
	})

	t.Run("unknown key leaves model unchanged", func(t *testing.T) {
		m := initBrowserModel(makeSummaries(2), 100, 30)
		before := m

		after, cmd := updateBrowserModelWithCmd(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'z'}}))
		if cmd != nil {
			t.Fatalf("Update(z) cmd = %v, want nil", cmd)
		}
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("Update(z) changed model:\n got: %#v\nwant: %#v", after, before)
		}
	})
}

func TestBrowserPreviewPanePagingKeysAndBounds(t *testing.T) {
	summaries := []viewer.RunSummary{
		browserTestSummaryResumable(
			"resumable-run",
			time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
			"pi",
			"completed",
			"prompt",
			viewer.ResumeSourceEffectivePrompt,
			"/tmp/resumable-run/effective-prompt.md",
		),
	}
	m := initBrowserModel(summaries, 100, 10)
	m.focusedPane = 1

	maxScroll := m.previewLineCount() - m.visiblePreviewLines()
	if maxScroll < 1 {
		t.Fatalf("preview maxScroll = %d, want at least 1 for paging test", maxScroll)
	}

	step := m.visiblePreviewLines() / 2
	if step < 1 {
		step = 1
	}

	m = pressKeyMsg(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyPgDown}))
	if m.previewScroll != step {
		t.Fatalf("previewScroll after PgDown = %d, want %d", m.previewScroll, step)
	}

	m = pressKey(t, m, "g")
	if m.previewScroll != 0 {
		t.Fatalf("previewScroll after g = %d, want 0", m.previewScroll)
	}

	m = pressKey(t, m, "G")
	if m.previewScroll != maxScroll {
		t.Fatalf("previewScroll after G = %d, want %d", m.previewScroll, maxScroll)
	}

	m = pressKeyMsg(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyPgUp}))
	want := maxScroll - step
	if want < 0 {
		want = 0
	}
	if m.previewScroll != want {
		t.Fatalf("previewScroll after PgUp = %d, want %d", m.previewScroll, want)
	}
}

func TestBrowserSetCursorClampsAndResetsPreviewScrollOnlyOnSelectionChange(t *testing.T) {
	t.Run("clamps indices and preserves preview scroll when selection is unchanged", func(t *testing.T) {
		m := initBrowserModel(makeSummaries(3), 100, 30)
		m.previewScroll = 4

		m.setCursor(0)
		if m.previewScroll != 4 {
			t.Fatalf("previewScroll after setCursor(current) = %d, want 4", m.previewScroll)
		}

		m.setCursor(99)
		if m.cursor != 2 {
			t.Fatalf("cursor after setCursor(99) = %d, want 2", m.cursor)
		}
		if m.selectedRunID != m.summaries[2].RunID {
			t.Fatalf("selectedRunID after setCursor(99) = %q, want %q", m.selectedRunID, m.summaries[2].RunID)
		}
		if m.previewScroll != 0 {
			t.Fatalf("previewScroll after changing selection = %d, want 0", m.previewScroll)
		}

		m.previewScroll = 6
		m.setCursor(-7)
		if m.cursor != 0 {
			t.Fatalf("cursor after setCursor(-7) = %d, want 0", m.cursor)
		}
		if m.selectedRunID != m.summaries[0].RunID {
			t.Fatalf("selectedRunID after setCursor(-7) = %q, want %q", m.selectedRunID, m.summaries[0].RunID)
		}
		if m.previewScroll != 0 {
			t.Fatalf("previewScroll after setCursor(-7) = %d, want 0", m.previewScroll)
		}
	})

	t.Run("empty list resets cursor state", func(t *testing.T) {
		m := NewBrowserModel(nil)
		m.cursor = 4
		m.scroll = 3
		m.previewScroll = 2

		m.setCursor(10)
		if m.cursor != 0 || m.scroll != 0 || m.previewScroll != 0 {
			t.Fatalf("empty setCursor state = cursor:%d scroll:%d preview:%d, want all zeros", m.cursor, m.scroll, m.previewScroll)
		}
	})
}

func TestBrowserEnsureCursorVisibleAndClampPreviewScrollEdgeCases(t *testing.T) {
	t.Run("ensureCursorVisible keeps cursor inside the viewport", func(t *testing.T) {
		m := initBrowserModel(makeSummaries(10), 100, 12)
		visible := m.visibleSessionRows()

		m.scroll = 4
		m.cursor = 1
		m.ensureCursorVisible()
		if m.scroll != 1 {
			t.Fatalf("scroll after moving cursor above viewport = %d, want 1", m.scroll)
		}

		m.scroll = 0
		m.cursor = 8
		m.ensureCursorVisible()
		want := 8 - visible + 1
		if m.scroll != want {
			t.Fatalf("scroll after moving cursor below viewport = %d, want %d", m.scroll, want)
		}

		m.scroll = -3
		m.cursor = 0
		m.ensureCursorVisible()
		if m.scroll != 0 {
			t.Fatalf("scroll after negative input = %d, want 0", m.scroll)
		}
	})

	t.Run("clampPreviewScroll constrains high and low values", func(t *testing.T) {
		m := initBrowserModel([]viewer.RunSummary{
			browserTestSummaryResumable(
				"resumable-run",
				time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
				"pi",
				"completed",
				"prompt",
				viewer.ResumeSourceEffectivePrompt,
				"/tmp/resumable-run/effective-prompt.md",
			),
		}, 100, 10)

		maxScroll := m.previewLineCount() - m.visiblePreviewLines()
		if maxScroll < 1 {
			t.Fatalf("preview maxScroll = %d, want at least 1 for clamp test", maxScroll)
		}

		m.previewScroll = maxScroll + 10
		m.clampPreviewScroll()
		if m.previewScroll != maxScroll {
			t.Fatalf("previewScroll after high clamp = %d, want %d", m.previewScroll, maxScroll)
		}

		m.previewScroll = -2
		m.clampPreviewScroll()
		if m.previewScroll != 0 {
			t.Fatalf("previewScroll after low clamp = %d, want 0", m.previewScroll)
		}
	})
}

func TestBrowserStateTokensAndPreviewTextFallbacks(t *testing.T) {
	t.Run("state tokens include all active filters and editing marker", func(t *testing.T) {
		summaries := []viewer.RunSummary{
			browserTestSummary("run-one", time.Date(2026, 3, 8, 11, 0, 0, 0, time.UTC), "pi", "completed", "default"),
		}
		m := initBrowserModel(summaries, 100, 30)
		m.agentFilter = "pi"
		m.statusFilter = "completed"
		m.promptFilter = "default"
		m.dateFilter = "2026-03-08"
		m.searching = true

		tokens := m.browserStateTokens()
		want := map[string]bool{
			"sort:newest":      false,
			"agent:pi":         false,
			"status:completed": false,
			"prompt:default":   false,
			"date:2026-03-08":  false,
			"/…_":              false,
		}
		for _, token := range tokens {
			if _, ok := want[token]; ok {
				want[token] = true
			}
		}
		for token, found := range want {
			if !found {
				t.Fatalf("browserStateTokens() missing %q in %v", token, tokens)
			}
		}
	})

	t.Run("preview text explains empty and no-match states", func(t *testing.T) {
		if got := NewBrowserModel(nil).browserPreviewText(); !strings.Contains(got, "No saved runs found.") {
			t.Fatalf("browserPreviewText() for empty browser = %q, want no-runs message", got)
		}

		summaries := []viewer.RunSummary{
			browserTestSummary("run-one", time.Date(2026, 3, 8, 11, 0, 0, 0, time.UTC), "pi", "completed", "default"),
		}
		m := initBrowserModel(summaries, 100, 30)
		m.searchQuery = "missing"
		m.applyBrowserView()

		got := m.browserPreviewText()
		for _, want := range []string{
			"No sessions match the current search/filter.",
			"Visible: 0/1",
			"Search: missing",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("browserPreviewText() = %q, missing %q", got, want)
			}
		}
	})
}

func TestRenderBrowserStateCardAndStatusNarrowFallbacks(t *testing.T) {
	t.Run("very narrow state cards fall back to plain text", func(t *testing.T) {
		got := renderBrowserStateCard(10, 4, "IGNORED", []string{"first line", "second line"}, true)
		want := "first line\nsecond line"
		if got != want {
			t.Fatalf("renderBrowserStateCard() = %q, want %q", got, want)
		}
	})

	t.Run("warning state cards still render title and body when space is tight", func(t *testing.T) {
		got := stripANSI(renderBrowserStateCard(18, 6, "WARNING", []string{"artifact warning body"}, true))
		for _, want := range []string{"WARNING", "artifact", "warning", "body"} {
			if !strings.Contains(got, want) {
				t.Fatalf("renderBrowserStateCard() = %q, missing %q", got, want)
			}
		}
	})

	t.Run("status falls back to the shortest hint set on narrow terminals", func(t *testing.T) {
		m := initBrowserModel([]viewer.RunSummary{
			browserTestSummaryResumable(
				"run-with-a-very-long-id",
				time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
				"pi",
				"completed",
				"prompt",
				viewer.ResumeSourceEffectivePrompt,
				"/tmp/run-with-a-very-long-id/effective-prompt.md",
			),
		}, 24, 10)
		m.focusedPane = 1

		status := stripANSI(m.renderBrowserStatus())
		if !strings.Contains(status, "q:quit") {
			t.Fatalf("renderBrowserStatus() = %q, want shortest quit hint", status)
		}
		if strings.Contains(status, "resume") || strings.Contains(status, "open") {
			t.Fatalf("renderBrowserStatus() = %q, want long action hints dropped", status)
		}
		if width := lipgloss.Width(status); width != 24 {
			t.Fatalf("renderBrowserStatus() width = %d, want 24", width)
		}
	})
}

func TestBrowserSearchKeyCtrlCAndUnhandledKey(t *testing.T) {
	t.Run("ctrl+c quits while searching", func(t *testing.T) {
		m := initBrowserModel(makeSummaries(1), 100, 30)
		m.searching = true

		next, cmd := updateBrowserModelWithCmd(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyCtrlC}))
		if !isQuitCmd(cmd) {
			t.Fatal("Ctrl+C in search mode did not emit tea.Quit")
		}
		if !next.searching {
			t.Fatal("searching = false after Ctrl+C, want model to stay in search mode until quit")
		}
	})

	t.Run("unhandled keys are ignored while searching", func(t *testing.T) {
		m := initBrowserModel(makeSummaries(1), 100, 30)
		m.searching = true
		m.searchQuery = "kiro"
		before := m

		next, cmd := updateBrowserModelWithCmd(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyLeft}))
		if cmd != nil {
			t.Fatalf("Update(left) cmd = %v, want nil", cmd)
		}
		if !reflect.DeepEqual(next, before) {
			t.Fatalf("Update(left) changed search model:\n got: %#v\nwant: %#v", next, before)
		}
	})
}

func TestBrowserMoveCursorAndApplyBrowserViewEdgeCases(t *testing.T) {
	t.Run("moveCursor ignores zero delta and empty lists", func(t *testing.T) {
		m := initBrowserModel(makeSummaries(3), 100, 30)
		before := m
		m.moveCursor(0)
		if !reflect.DeepEqual(m, before) {
			t.Fatalf("moveCursor(0) changed model:\n got: %#v\nwant: %#v", m, before)
		}

		empty := NewBrowserModel(nil)
		empty.cursor = 3
		empty.scroll = 2
		empty.previewScroll = 1
		emptyBefore := empty
		empty.moveCursor(1)
		if !reflect.DeepEqual(empty, emptyBefore) {
			t.Fatalf("moveCursor() on empty model changed state:\n got: %#v\nwant: %#v", empty, emptyBefore)
		}
	})

	t.Run("applyBrowserView preserves the current row when selectedRunID is blank", func(t *testing.T) {
		m := initBrowserModel(makeSummaries(3), 100, 30)
		m.cursor = 1
		m.selectedRunID = ""

		m.applyBrowserView()

		if m.cursor != 1 {
			t.Fatalf("cursor after applyBrowserView() = %d, want 1", m.cursor)
		}
		if m.selectedRunID != "run-001" {
			t.Fatalf("selectedRunID after applyBrowserView() = %q, want %q", m.selectedRunID, "run-001")
		}
	})

	t.Run("applyBrowserView clamps missing selections back into range", func(t *testing.T) {
		m := initBrowserModel(makeSummaries(3), 100, 30)
		m.selectedRunID = "missing"
		m.cursor = -4

		m.applyBrowserView()
		if m.cursor != 0 || m.selectedRunID != "run-000" {
			t.Fatalf("negative cursor clamp = cursor:%d selected:%q, want 0/run-000", m.cursor, m.selectedRunID)
		}

		m = initBrowserModel(makeSummaries(3), 100, 30)
		m.selectedRunID = "missing"
		m.cursor = 99

		m.applyBrowserView()
		if m.cursor != 2 || m.selectedRunID != "run-002" {
			t.Fatalf("high cursor clamp = cursor:%d selected:%q, want 2/run-002", m.cursor, m.selectedRunID)
		}
	})
}

func TestBrowserRenderHelpersCoverRemainingBrowserBranches(t *testing.T) {
	t.Run("tiny headers still show the app name and drop extra state tokens", func(t *testing.T) {
		m := initBrowserModel(makeSummaries(1), 9, 10)
		m.searching = true
		m.searchQuery = "long search"
		m.agentFilter = "pi"

		header := stripANSI(m.renderBrowserHeader())
		compact := strings.Join(strings.Fields(header), "")
		if !strings.HasPrefix(compact, "ralfinh") {
			t.Fatalf("renderBrowserHeader() = %q, want compact output to keep the app-name prefix", header)
		}
		if strings.Contains(header, "sort:") || strings.Contains(header, "agent:") {
			t.Fatalf("renderBrowserHeader() = %q, want extra state tokens dropped on tiny widths", header)
		}
	})

	t.Run("sessions pane renders both selected and non-selected rows", func(t *testing.T) {
		m := initBrowserModel(makeSummaries(3), 100, 12)
		m.cursor = 1

		pane := stripANSI(m.renderSessionsPane())
		for _, want := range []string{"run-000", "run-001", "run-002"} {
			if !strings.Contains(pane, want) {
				t.Fatalf("renderSessionsPane() = %q, missing %q", pane, want)
			}
		}
		if strings.Count(pane, "▌") != 1 {
			t.Fatalf("renderSessionsPane() = %q, want exactly one selected-row indicator", pane)
		}
	})

	t.Run("status includes artifact warnings and still renders on ultra-narrow widths", func(t *testing.T) {
		summary := browserTestSummaryWithActions(
			"artifact-run",
			time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
			"pi",
			"completed",
			"prompt",
			true,
		)
		summary.ArtifactError = "meta.json missing"

		m := initBrowserModel([]viewer.RunSummary{summary}, 9, 10)
		if left := m.browserStatusLeft(); !strings.Contains(left, "artifact warnings") {
			t.Fatalf("browserStatusLeft() = %q, want artifact warning marker", left)
		}

		status := stripANSI(m.renderBrowserStatus())
		if !strings.Contains(status, "q:quit") {
			t.Fatalf("renderBrowserStatus() = %q, want the narrow quit hint", status)
		}
	})
}

func TestBrowserRenderPreviewPaneClampsLocalScrollAndShowsWarningPaging(t *testing.T) {
	summary := browserTestSummaryResumable(
		"warn-run",
		time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
		"pi",
		"completed",
		"prompt",
		viewer.ResumeSourceEffectivePrompt,
		"/tmp/warn-run/effective-prompt.md",
	)
	summary.ArtifactError = "meta.json missing"
	summary.EventsError = "events.jsonl missing"
	summary.EffectivePromptError = "effective-prompt.md missing"

	t.Run("large previewScroll values clamp to the last page", func(t *testing.T) {
		m := initBrowserModel([]viewer.RunSummary{summary}, 90, 10)
		m.focusedPane = 1
		m.previewScroll = 1 << 20

		preview := stripANSI(m.renderPreviewPane())
		if !strings.Contains(preview, "PREVIEW ⚠ [") {
			t.Fatalf("renderPreviewPane() = %q, want warning title with paging", preview)
		}
	})

	t.Run("negative previewScroll values clamp back to the first page", func(t *testing.T) {
		m := initBrowserModel([]viewer.RunSummary{summary}, 90, 10)
		m.focusedPane = 1
		m.previewScroll = -5

		preview := stripANSI(m.renderPreviewPane())
		if !strings.Contains(preview, "PREVIEW ⚠ [1/") {
			t.Fatalf("renderPreviewPane() = %q, want warning title clamped to the first page", preview)
		}
		if !strings.Contains(preview, "Run: warn-run") {
			t.Fatalf("renderPreviewPane() = %q, want the first preview page content", preview)
		}
	})
}
