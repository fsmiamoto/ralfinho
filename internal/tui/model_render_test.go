package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/fsmiamoto/ralfinho/internal/runner"
)

func stripANSI(s string) string {
	return ansi.Strip(s)
}

func TestEventStyleUsesDefaultForUnknownType(t *testing.T) {
	if got := eventStyle("unknown").GetForeground(); got != defaultEventStyle.GetForeground() {
		t.Fatalf("eventStyle(unknown) foreground = %v, want default %v", got, defaultEventStyle.GetForeground())
	}
	if got := eventStyle(DisplayToolStart).GetForeground(); got == defaultEventStyle.GetForeground() {
		t.Fatalf("eventStyle(%q) should not use the default foreground", DisplayToolStart)
	}
}

func TestModelViewInitializingBeforeWindowSize(t *testing.T) {
	if got := (Model{}).View(); got != "Initializing..." {
		t.Fatalf("View() = %q, want %q", got, "Initializing...")
	}
}

func TestModelViewShowsErrorOverlay(t *testing.T) {
	m := Model{
		width:        60,
		height:       20,
		errorOverlay: "permission denied while writing meta.json after a failed run",
	}

	view := stripANSI(m.View())
	for _, want := range []string{"Error", "permission denied while writing", "meta.json after a failed run", "j/k:scroll", "any key:dismiss"} {
		if !strings.Contains(view, want) {
			t.Fatalf("overlay view = %q, want substring %q", view, want)
		}
	}
	if strings.Contains(view, "STREAM (") {
		t.Fatalf("overlay view should replace the main layout, got %q", view)
	}
}

func TestModelViewRendersAllPanes(t *testing.T) {
	m := Model{
		width:       80,
		height:      18,
		paneRatio:   0.4,
		status:      "Idle",
		focusedPane: 0,
		blocks:      []MainBlock{{Kind: BlockInfo, InfoText: "main pane content"}},
		events: []DisplayEvent{{
			Type:      DisplayInfo,
			Summary:   "summary line",
			Detail:    "detail pane content",
			Timestamp: time.Date(2026, 3, 15, 10, 11, 12, 0, time.UTC),
		}},
	}

	view := stripANSI(m.View())
	for _, want := range []string{"ralfinho", "LIVE", "STREAM (1)", "DETAIL", "Idle", "main pane content", "summary line", "detail pane content"} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() = %q, want substring %q", view, want)
		}
	}
}

func TestRenderHeaderIncludesOptionalSegmentsWhenWideAndDropsThemWhenNarrow(t *testing.T) {
	tests := []struct {
		name          string
		width         int
		wantContains  []string
		wantOmissions []string
	}{
		{
			name:          "wide",
			width:         40,
			wantContains:  []string{"ralfinho", "Iteration #7", "claude-4"},
			wantOmissions: nil,
		},
		{
			name:          "narrow",
			width:         20,
			wantContains:  []string{"ralfinho"},
			wantOmissions: []string{"Iteration #7", "claude-4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{width: tt.width, iteration: 7, modelName: "claude-4"}
			header := stripANSI(m.renderHeader())

			for _, want := range tt.wantContains {
				if !strings.Contains(header, want) {
					t.Fatalf("renderHeader() = %q, want substring %q", header, want)
				}
			}
			for _, unwanted := range tt.wantOmissions {
				if strings.Contains(header, unwanted) {
					t.Fatalf("renderHeader() = %q, should omit %q", header, unwanted)
				}
			}
		})
	}
}

func TestScrollIndicator(t *testing.T) {
	tests := []struct {
		name                          string
		scroll, visible, total int
		want                          string
	}{
		{"fits in view", 0, 10, 5, ""},
		{"exact fit", 0, 10, 10, ""},
		{"at top", 0, 10, 100, "Top"},
		{"at bottom", 90, 10, 100, "Bot"},
		{"middle", 50, 10, 100, "50%"},
		{"near top", 5, 10, 100, "5%"},
		{"near bottom", 85, 10, 100, "85%"},
		{"one before bottom", 89, 10, 100, "89%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scrollIndicator(tt.scroll, tt.visible, tt.total)
			if got != tt.want {
				t.Fatalf("scrollIndicator(%d, %d, %d) = %q, want %q",
					tt.scroll, tt.visible, tt.total, got, tt.want)
			}
		})
	}
}

func TestRenderMainShowsScrollTitleAndVisibleContent(t *testing.T) {
	m := Model{
		width:  80,
		height: 12,
		blocks: []MainBlock{{
			Kind:     BlockInfo,
			InfoText: "line1\nline2\nline3\nline4\nline5\nline6",
		}},
		mainScroll: 2,
	}

	main := stripANSI(m.renderMain())
	for _, want := range []string{"LIVE Bot", "line3", "line4", "line5", "line6"} {
		if !strings.Contains(main, want) {
			t.Fatalf("renderMain() = %q, want substring %q", main, want)
		}
	}
	if strings.Contains(main, "line1") || strings.Contains(main, "line2") {
		t.Fatalf("renderMain() should only show scrolled content, got %q", main)
	}
}

func TestRenderMainShowsAutoIndicatorWhenAutoScrolling(t *testing.T) {
	m := Model{
		width:          80,
		height:         12,
		mainAutoScroll: true,
		mainScroll:     999999, // clamped to max
		blocks: []MainBlock{{
			Kind:     BlockInfo,
			InfoText: "line1\nline2\nline3\nline4\nline5\nline6",
		}},
	}

	main := stripANSI(m.renderMain())
	if !strings.Contains(main, "LIVE [AUTO]") {
		t.Fatalf("renderMain() with autoScroll = %q, want 'LIVE [AUTO]'", main)
	}
}

func TestRenderMainShowsScrollIndicatorWhenNotAutoScrolling(t *testing.T) {
	m := Model{
		width:          80,
		height:         12,
		mainAutoScroll: false,
		mainScroll:     999999, // clamped to max → "Bot"
		blocks: []MainBlock{{
			Kind:     BlockInfo,
			InfoText: "line1\nline2\nline3\nline4\nline5\nline6",
		}},
	}

	main := stripANSI(m.renderMain())
	if strings.Contains(main, "[AUTO]") {
		t.Fatalf("renderMain() without autoScroll should not show [AUTO], got %q", main)
	}
	if !strings.Contains(main, "LIVE Bot") {
		t.Fatalf("renderMain() without autoScroll = %q, want 'LIVE Bot'", main)
	}
}

func TestRenderStreamTruncatesLongSummariesAndShowsSelection(t *testing.T) {
	m := Model{
		width:     40,
		height:    18,
		paneRatio: 0.5,
		cursor:    1,
		events: []DisplayEvent{
			{Type: DisplayInfo, Summary: strings.Repeat("x", 40)},
			{Type: DisplayToolEnd, Summary: "! bash error"},
		},
	}

	stream := stripANSI(m.renderStream())
	for _, want := range []string{"STREAM (2)", "! bash error", "▌", "..."} {
		if !strings.Contains(stream, want) {
			t.Fatalf("renderStream() = %q, want substring %q", stream, want)
		}
	}
	if strings.Contains(stream, strings.Repeat("x", 40)) {
		t.Fatalf("renderStream() should truncate long summaries, got %q", stream)
	}
}

func TestRenderDetailSupportsRawAndRenderedAssistantModes(t *testing.T) {
	t.Run("raw", func(t *testing.T) {
		m := Model{
			width:     80,
			height:    24,
			paneRatio: 0.4,
			cursor:    0,
			rawMode:   true,
			events: []DisplayEvent{{
				Type:      DisplayAssistantText,
				Detail:    "hello *world*",
				Timestamp: time.Date(2026, 3, 15, 10, 11, 12, 0, time.UTC),
				Iteration: 2,
			}},
		}

		detail := stripANSI(m.renderDetail())
		for _, want := range []string{"Type: assistant_text", "Time: 10:11:12", "Iteration: 2", "hello *world*"} {
			if !strings.Contains(detail, want) {
				t.Fatalf("renderDetail() raw = %q, want substring %q", detail, want)
			}
		}
	})

	t.Run("rendered assistant", func(t *testing.T) {
		m := Model{
			width:     80,
			height:    24,
			paneRatio: 0.4,
			cursor:    0,
			events: []DisplayEvent{{
				Type:   DisplayAssistantText,
				Detail: "# Heading\n\nParagraph text",
			}},
		}

		detail := stripANSI(m.renderDetail())
		for _, want := range []string{"Heading", "Paragraph text"} {
			if !strings.Contains(detail, want) {
				t.Fatalf("renderDetail() rendered = %q, want substring %q", detail, want)
			}
		}
		if strings.Contains(detail, "Type: assistant_text") {
			t.Fatalf("renderDetail() rendered should not use raw metadata view, got %q", detail)
		}
	})
}

func TestRenderStatusAdjustsHintsForWidthAndConfirmationMode(t *testing.T) {
	t.Run("full width", func(t *testing.T) {
		m := Model{width: 80, status: "Idle"}
		status := stripANSI(m.renderStatus())
		for _, want := range []string{"Idle", "↑↓:nav", "Tab:pane", "r:rendered", "q:quit"} {
			if !strings.Contains(status, want) {
				t.Fatalf("renderStatus() = %q, want substring %q", status, want)
			}
		}
	})

	t.Run("narrow width", func(t *testing.T) {
		m := Model{width: 20, status: "Idle"}
		status := stripANSI(m.renderStatus())
		if !strings.Contains(status, "q:quit") {
			t.Fatalf("renderStatus() = %q, want compact quit hint", status)
		}
		for _, unwanted := range []string{"↑↓:nav", "Tab:pane", "r:rendered"} {
			if strings.Contains(status, unwanted) {
				t.Fatalf("renderStatus() = %q, should omit %q", status, unwanted)
			}
		}
	})

	t.Run("quit confirmation", func(t *testing.T) {
		m := Model{width: 40, confirmQuit: true}
		status := stripANSI(m.renderStatus())
		if !strings.Contains(status, "Press q again to quit") {
			t.Fatalf("renderStatus() = %q, want quit confirmation", status)
		}
	})

	t.Run("ctrl+c confirmation", func(t *testing.T) {
		m := Model{width: 40, confirmQuit: true, confirmCtrlC: true}
		status := stripANSI(m.renderStatus())
		if !strings.Contains(status, "Press Ctrl+C again to quit") {
			t.Fatalf("renderStatus() = %q, want ctrl+c confirmation", status)
		}
	})
}

func TestRenderHeaderShowsElapsedTimeForRunningModel(t *testing.T) {
	m := NewModel(nil, "", "", "", "")
	m.width = 80
	m.iteration = 3
	m.modelName = "claude-opus-4-1"
	m.startTime = time.Now().Add(-(2*time.Minute + 5*time.Second + 200*time.Millisecond))

	header := stripANSI(m.renderHeader())
	for _, want := range []string{"ralfinho", "Iteration #3", "claude-opus-4-1", "2m 5s"} {
		if !strings.Contains(header, want) {
			t.Fatalf("renderHeader() = %q, want substring %q", header, want)
		}
	}
}

func TestRenderHeaderHandlesVeryNarrowWidths(t *testing.T) {
	m := Model{width: 8, iteration: 9, modelName: "claude-4"}
	header := stripANSI(m.renderHeader())
	if got := strings.Join(strings.Fields(header), ""); !strings.Contains(got, "ralfinho") {
		t.Fatalf("renderHeader() compacted = %q, want base title even on narrow widths", got)
	}
	for _, unwanted := range []string{"Iteration #9", "claude-4"} {
		if strings.Contains(header, unwanted) {
			t.Fatalf("renderHeader() = %q, should omit %q when space is too tight", header, unwanted)
		}
	}
}

func TestRenderStatusCoversRunningRawAndTruncationBranches(t *testing.T) {
	t.Run("running raw mode", func(t *testing.T) {
		m := Model{width: 100, status: "Iteration #4", running: true, rawMode: true}
		status := stripANSI(m.renderStatus())
		for _, want := range []string{"Running │ Iteration #4", "r:raw", "n:memory", "q:quit"} {
			if !strings.Contains(status, want) {
				t.Fatalf("renderStatus() = %q, want substring %q", status, want)
			}
		}
	})

	t.Run("very narrow width drops hints entirely", func(t *testing.T) {
		m := Model{width: 10, status: "super long status line"}
		status := stripANSI(m.renderStatus())
		if strings.Contains(status, "q:quit") {
			t.Fatalf("renderStatus() = %q, should drop right-side hints when nothing fits", status)
		}
		if !strings.Contains(status, "...") {
			t.Fatalf("renderStatus() = %q, want truncated left status after dropping hints", status)
		}
	})
}

func TestRenderErrorOverlayScrollsTallMessages(t *testing.T) {
	m := Model{
		width:  40,
		height: 10,
		errorOverlay: strings.Join([]string{
			"line 1",
			"line 2",
			"line 3",
			"line 4",
			"line 5",
		}, "\n"),
	}

	// At scroll=0, shows first visible lines and Top indicator.
	overlay := stripANSI(m.renderErrorOverlay())
	for _, want := range []string{"Error", "line 1", "line 2", "line 3", "j/k:scroll", "key:dismiss"} {
		if !strings.Contains(overlay, want) {
			t.Fatalf("renderErrorOverlay() = %q, want substring %q", overlay, want)
		}
	}

	// Scrolling down reveals later lines.
	m.errorOverlayScroll = 2
	overlay = stripANSI(m.renderErrorOverlay())
	for _, want := range []string{"line 3", "line 4", "line 5"} {
		if !strings.Contains(overlay, want) {
			t.Fatalf("renderErrorOverlay() scrolled = %q, want substring %q", overlay, want)
		}
	}
}

func TestRenderErrorOverlayUsesMinimumInnerWidthOnTinyTerminals(t *testing.T) {
	m := Model{width: 34, height: 12, errorOverlay: strings.Repeat("x", 25)}
	overlay := stripANSI(m.renderErrorOverlay())
	for _, want := range []string{"Error", "xxxxxxxxxxxxxxxxxxxx", "xxxxx", "j/k:scroll", "key:dismiss"} {
		if !strings.Contains(overlay, want) {
			t.Fatalf("renderErrorOverlay() = %q, want substring %q", overlay, want)
		}
	}
}

func TestRenderHeaderShowsAgentNameAfterRalfinho(t *testing.T) {
	tests := []struct {
		name         string
		width        int
		agentName    string
		wantContains []string
		wantOmissions []string
	}{
		{
			name:         "agent name shown when wide enough",
			width:        60,
			agentName:    "claude",
			wantContains: []string{"ralfinho", "claude"},
		},
		{
			name:         "agent name omitted when too narrow",
			width:        12,
			agentName:    "claude",
			wantContains: []string{"ralfinho"},
			wantOmissions: []string{"claude"},
		},
		{
			name:         "agent name appears before iteration",
			width:        80,
			agentName:    "pi",
			wantContains: []string{"ralfinho", "pi", "Iteration #3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{width: tt.width, agentName: tt.agentName, iteration: 3}
			header := stripANSI(m.renderHeader())

			for _, want := range tt.wantContains {
				if !strings.Contains(header, want) {
					t.Fatalf("renderHeader() = %q, want substring %q", header, want)
				}
			}
			for _, unwanted := range tt.wantOmissions {
				if strings.Contains(header, unwanted) {
					t.Fatalf("renderHeader() = %q, should omit %q", header, unwanted)
				}
			}
		})
	}
}

func TestRenderStatusIncludesPromptHint(t *testing.T) {
	m := Model{width: 80, status: "Idle"}
	status := stripANSI(m.renderStatus())
	if !strings.Contains(status, "p:prompt") {
		t.Fatalf("renderStatus() = %q, want p:prompt hint", status)
	}
}

func TestPromptOverlayToggledByPKey(t *testing.T) {
	m := Model{width: 80, height: 24, promptText: "Do the thing"}

	// p key opens the overlay.
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'p'}}))
	if !m.promptOverlay {
		t.Fatal("after p: promptOverlay = false, want true")
	}
	if m.promptOverlayScroll != 0 {
		t.Fatalf("after p: promptOverlayScroll = %d, want 0", m.promptOverlayScroll)
	}

	// p key again closes the overlay (any non-scroll key dismisses).
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'p'}}))
	if m.promptOverlay {
		t.Fatal("after second p: promptOverlay = true, want false")
	}
}

func TestPromptOverlayDismissedByEsc(t *testing.T) {
	m := Model{width: 80, height: 24, promptOverlay: true, promptText: "some prompt"}

	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyEscape}))
	if m.promptOverlay {
		t.Fatal("after Esc: promptOverlay = true, want false")
	}
}

func TestPromptOverlayRendersPromptText(t *testing.T) {
	m := Model{
		width:         80,
		height:        24,
		promptOverlay: true,
		promptText:    "Please fix all the bugs in the repository",
	}

	view := stripANSI(m.View())
	for _, want := range []string{"Effective Prompt", "Please fix all the bugs", "p/Esc:close", "j/k:scroll"} {
		if !strings.Contains(view, want) {
			t.Fatalf("prompt overlay view = %q, want substring %q", view, want)
		}
	}
	// Should not render the normal TUI panes.
	if strings.Contains(view, "STREAM (") {
		t.Fatalf("prompt overlay should replace the main layout, got %q", view)
	}
}

func TestPromptOverlayScrollsWithJAndK(t *testing.T) {
	// Build a prompt long enough to require scrolling.
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("Prompt line %d with some text to fill the width properly.", i+1))
	}
	promptText := strings.Join(lines, "\n")

	m := Model{
		width:         80,
		height:        24,
		promptOverlay: true,
		promptText:    promptText,
	}

	// j scrolls down.
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'j'}}))
	if m.promptOverlayScroll != 1 {
		t.Fatalf("after j: promptOverlayScroll = %d, want 1", m.promptOverlayScroll)
	}

	// j again scrolls further.
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'j'}}))
	if m.promptOverlayScroll != 2 {
		t.Fatalf("after second j: promptOverlayScroll = %d, want 2", m.promptOverlayScroll)
	}

	// k scrolls back up.
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'k'}}))
	if m.promptOverlayScroll != 1 {
		t.Fatalf("after k: promptOverlayScroll = %d, want 1", m.promptOverlayScroll)
	}

	// k at scroll=0 does not go negative.
	m.promptOverlayScroll = 0
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'k'}}))
	if m.promptOverlayScroll != 0 {
		t.Fatalf("k at top: promptOverlayScroll = %d, want 0", m.promptOverlayScroll)
	}
	// Overlay is still open after k.
	if !m.promptOverlay {
		t.Fatal("promptOverlay = false after k, want still open")
	}
}

func TestPromptOverlayShowsScrollIndicatorWhenScrollable(t *testing.T) {
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("Line %d of the effective prompt text.", i+1))
	}

	m := Model{
		width:               80,
		height:              24,
		promptOverlay:       true,
		promptOverlayScroll: 5,
		promptText:          strings.Join(lines, "\n"),
	}

	view := stripANSI(m.renderPromptOverlay())
	// Vim-style scroll indicator: should show a percentage or Top/Bot, not [N/M].
	hasIndicator := strings.Contains(view, "Effective Prompt Top") ||
		strings.Contains(view, "Effective Prompt Bot") ||
		strings.Contains(view, "Effective Prompt ") && strings.Contains(view, "%")
	if !hasIndicator {
		t.Fatalf("renderPromptOverlay() = %q, want vim-style scroll indicator in title", view)
	}
}

func TestMemoryOverlayToggledByNKey(t *testing.T) {
	m := Model{width: 80, height: 24}

	// n key opens the overlay.
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'n'}}))
	if !m.memoryOverlay {
		t.Fatal("after n: memoryOverlay = false, want true")
	}
	if m.memoryOverlayTab != 0 {
		t.Fatalf("after n: memoryOverlayTab = %d, want 0", m.memoryOverlayTab)
	}
	if m.memoryOverlayScroll != 0 {
		t.Fatalf("after n: memoryOverlayScroll = %d, want 0", m.memoryOverlayScroll)
	}

	// n key again closes the overlay.
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'n'}}))
	if m.memoryOverlay {
		t.Fatal("after second n: memoryOverlay = true, want false")
	}
}

func TestMemoryOverlayDismissedByEscAndQ(t *testing.T) {
	for _, key := range []tea.Key{
		{Type: tea.KeyEscape},
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
	} {
		m := Model{width: 80, height: 24, memoryOverlay: true}
		m = updateModel(t, m, tea.KeyMsg(key))
		if m.memoryOverlay {
			t.Fatalf("after %q: memoryOverlay = true, want false", key)
		}
	}
}

func TestMemoryOverlayTabSwitching(t *testing.T) {
	m := Model{width: 80, height: 24, memoryOverlay: true, memoryOverlayTab: 0}

	// Tab switches to PROGRESS (tab 1).
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyTab}))
	if m.memoryOverlayTab != 1 {
		t.Fatalf("after Tab: memoryOverlayTab = %d, want 1", m.memoryOverlayTab)
	}
	// Scroll resets on tab switch.
	if m.memoryOverlayScroll != 0 {
		t.Fatalf("after Tab: memoryOverlayScroll = %d, want 0", m.memoryOverlayScroll)
	}

	// Tab again wraps back to NOTES (tab 0).
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyTab}))
	if m.memoryOverlayTab != 0 {
		t.Fatalf("after second Tab: memoryOverlayTab = %d, want 0", m.memoryOverlayTab)
	}
}

func TestMemoryOverlayScrollsWithJAndK(t *testing.T) {
	m := Model{width: 80, height: 24, memoryOverlay: true}

	// j scrolls down.
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'j'}}))
	if m.memoryOverlayScroll != 1 {
		t.Fatalf("after j: memoryOverlayScroll = %d, want 1", m.memoryOverlayScroll)
	}

	// k scrolls back up.
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'k'}}))
	if m.memoryOverlayScroll != 0 {
		t.Fatalf("after k: memoryOverlayScroll = %d, want 0", m.memoryOverlayScroll)
	}

	// k at scroll=0 does not go negative.
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'k'}}))
	if m.memoryOverlayScroll != 0 {
		t.Fatalf("k at top: memoryOverlayScroll = %d, want 0", m.memoryOverlayScroll)
	}
	if !m.memoryOverlay {
		t.Fatal("memoryOverlay = false after k, want still open")
	}
}

func TestMemoryOverlayKeysDoNotLeakToMainModel(t *testing.T) {
	m := Model{width: 80, height: 24, memoryOverlay: true, focusedPane: 0, mainScroll: 5}

	// j in overlay should scroll the overlay, not the main view.
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'j'}}))
	if m.mainScroll != 5 {
		t.Fatalf("j in overlay changed mainScroll: got %d, want 5", m.mainScroll)
	}
	if m.memoryOverlayScroll != 1 {
		t.Fatalf("j in overlay: memoryOverlayScroll = %d, want 1", m.memoryOverlayScroll)
	}
}

func TestMemoryOverlayRendersFileContent(t *testing.T) {
	// Create a temp file to simulate NOTES.md.
	dir := t.TempDir()
	notesPath := dir + "/NOTES.md"
	os.WriteFile(notesPath, []byte("some notes here"), 0644)

	m := Model{
		width:         80,
		height:        24,
		memoryOverlay: true,
		notesPath:     notesPath,
		progressPath:  dir + "/PROGRESS.md", // does not exist
	}

	view := stripANSI(m.View())
	for _, want := range []string{"Memory Files", "NOTES", "PROGRESS", "some notes here", "n/Esc:close", "Tab:switch", "j/k:scroll"} {
		if !strings.Contains(view, want) {
			t.Fatalf("memory overlay view missing %q, got:\n%s", want, view)
		}
	}
	// Should not render the normal TUI panes.
	if strings.Contains(view, "STREAM (") {
		t.Fatal("memory overlay should replace the main layout")
	}
}

func TestMemoryOverlayShowsMissingFileMessage(t *testing.T) {
	m := Model{
		width:         80,
		height:        24,
		memoryOverlay: true,
		notesPath:     "/nonexistent/NOTES.md",
	}

	view := stripANSI(m.renderMemoryOverlay())
	if !strings.Contains(view, "(file not found)") {
		t.Fatalf("renderMemoryOverlay() missing '(file not found)', got:\n%s", view)
	}
}

func TestMemoryOverlayShowsEmptyMessage(t *testing.T) {
	dir := t.TempDir()
	notesPath := dir + "/NOTES.md"
	os.WriteFile(notesPath, []byte(""), 0644)

	m := Model{
		width:         80,
		height:        24,
		memoryOverlay: true,
		notesPath:     notesPath,
	}

	view := stripANSI(m.renderMemoryOverlay())
	if !strings.Contains(view, "(empty)") {
		t.Fatalf("renderMemoryOverlay() missing '(empty)', got:\n%s", view)
	}
}

func TestMemoryOverlayShowsNoPathMessage(t *testing.T) {
	m := Model{
		width:         80,
		height:        24,
		memoryOverlay: true,
		// notesPath and progressPath are empty
	}

	view := stripANSI(m.renderMemoryOverlay())
	if !strings.Contains(view, "(no path configured)") {
		t.Fatalf("renderMemoryOverlay() missing '(no path configured)', got:\n%s", view)
	}
}

func TestStatusBarContainsMemoryHint(t *testing.T) {
	m := NewModel(nil, "", "", "", "")
	m.width = 200 // wide enough to show all hints
	m.height = 24
	status := stripANSI(m.renderStatus())
	if !strings.Contains(status, "n:memory") {
		t.Fatalf("renderStatus() = %q, want n:memory hint", status)
	}
}

func TestHelpOverlayContainsMemoryKeybinding(t *testing.T) {
	m := Model{width: 80, height: 40, helpOverlay: true}
	view := stripANSI(m.renderHelpOverlay())
	if !strings.Contains(view, "n") || !strings.Contains(view, "memory") {
		t.Fatalf("renderHelpOverlay() missing memory keybinding, got:\n%s", view)
	}
}

func TestViewerModelSupportsMemoryOverlay(t *testing.T) {
	dir := t.TempDir()
	notesPath := dir + "/NOTES.md"
	os.WriteFile(notesPath, []byte("viewer notes"), 0644)

	m := NewViewerModel(nil, runner.RunMeta{}, "", notesPath, "")
	m.width = 80
	m.height = 24

	// n key opens the overlay.
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'n'}}))
	if !m.memoryOverlay {
		t.Fatal("viewer model: after n: memoryOverlay = false, want true")
	}

	view := stripANSI(m.renderMemoryOverlay())
	if !strings.Contains(view, "viewer notes") {
		t.Fatalf("viewer model memory overlay missing content, got:\n%s", view)
	}
}

func TestPaneHeightUsesComputedBottomPaneHeightWhenRoomy(t *testing.T) {
	m := Model{height: 20}
	if got := m.paneHeight(); got != 5 {
		t.Fatalf("paneHeight() = %d, want computed height 5", got)
	}
}

// Markdown-like input used to distinguish plain text from rendered Markdown.
const testAssistantMD = "# Heading\n\n- item one\n- item two\n\n```go\nfmt.Println(\"hi\")\n```"

func TestRenderAssistantContent(t *testing.T) {
	t.Run("empty text returns empty", func(t *testing.T) {
		if got := renderAssistantContent("", 80, true); got != "" {
			t.Fatalf("renderAssistantContent(\"\", 80, true) = %q, want empty", got)
		}
		if got := renderAssistantContent("", 80, false); got != "" {
			t.Fatalf("renderAssistantContent(\"\", 80, false) = %q, want empty", got)
		}
	})

	t.Run("streaming uses plain text", func(t *testing.T) {
		got := stripANSI(renderAssistantContent(testAssistantMD, 80, false))
		// Plain text preserves literal Markdown markers.
		if !strings.Contains(got, "# Heading") {
			t.Fatalf("streaming render = %q, want literal '# Heading'", got)
		}
	})

	t.Run("final uses markdown", func(t *testing.T) {
		got := stripANSI(renderAssistantContent(testAssistantMD, 80, true))
		// Rendered Markdown strips the heading marker but keeps the text.
		if !strings.Contains(got, "Heading") {
			t.Fatalf("final render = %q, want 'Heading'", got)
		}
		if strings.Contains(got, "# Heading") {
			t.Fatalf("final render = %q, should not contain literal '# Heading'", got)
		}
	})
}

func TestRenderMain_AssistantStreamingUsesPlainText(t *testing.T) {
	m := Model{
		width:  80,
		height: 30,
		blocks: []MainBlock{{
			Kind:           BlockAssistantText,
			Text:           testAssistantMD,
			AssistantFinal: false,
		}},
	}

	main := stripANSI(m.renderMain())
	if !strings.Contains(main, "# Heading") {
		t.Fatalf("renderMain() streaming = %q, want literal '# Heading'", main)
	}
}

func TestRenderMain_AssistantFinalUsesMarkdown(t *testing.T) {
	m := Model{
		width:  80,
		height: 30,
		blocks: []MainBlock{{
			Kind:           BlockAssistantText,
			Text:           testAssistantMD,
			AssistantFinal: true,
		}},
	}

	main := stripANSI(m.renderMain())
	if !strings.Contains(main, "Heading") {
		t.Fatalf("renderMain() final = %q, want 'Heading'", main)
	}
	if strings.Contains(main, "# Heading") {
		t.Fatalf("renderMain() final = %q, should not contain literal '# Heading'", main)
	}
}

func TestRenderDetail_AssistantStreamingUsesPlainText(t *testing.T) {
	m := Model{
		width:     80,
		height:    24,
		paneRatio: 0.4,
		cursor:    0,
		events: []DisplayEvent{{
			Type:           DisplayAssistantText,
			Detail:         testAssistantMD,
			AssistantFinal: false,
		}},
	}

	detail := stripANSI(m.renderDetail())
	if !strings.Contains(detail, "# Heading") {
		t.Fatalf("renderDetail() streaming = %q, want literal '# Heading'", detail)
	}
}

func TestRenderDetail_AssistantFinalUsesMarkdown(t *testing.T) {
	m := Model{
		width:     80,
		height:    24,
		paneRatio: 0.4,
		cursor:    0,
		events: []DisplayEvent{{
			Type:           DisplayAssistantText,
			Detail:         testAssistantMD,
			AssistantFinal: true,
		}},
	}

	detail := stripANSI(m.renderDetail())
	if !strings.Contains(detail, "Heading") {
		t.Fatalf("renderDetail() final = %q, want 'Heading'", detail)
	}
	if strings.Contains(detail, "# Heading") {
		t.Fatalf("renderDetail() final = %q, should not contain literal '# Heading'", detail)
	}
}

func TestRenderDetail_RawModeIgnoresAssistantFinal(t *testing.T) {
	// Raw mode should always show metadata + plain text, regardless of AssistantFinal.
	for _, final := range []bool{false, true} {
		name := "streaming"
		if final {
			name = "final"
		}
		t.Run(name, func(t *testing.T) {
			m := Model{
				width:     80,
				height:    24,
				paneRatio: 0.4,
				cursor:    0,
				rawMode:   true,
				events: []DisplayEvent{{
					Type:           DisplayAssistantText,
					Detail:         testAssistantMD,
					AssistantFinal: final,
					Timestamp:      time.Date(2026, 3, 16, 14, 30, 0, 0, time.UTC),
					Iteration:      3,
				}},
			}

			detail := stripANSI(m.renderDetail())

			// Raw mode must show metadata header regardless of AssistantFinal.
			for _, want := range []string{"Type: assistant_text", "Time: 14:30:00", "Iteration: 3"} {
				if !strings.Contains(detail, want) {
					t.Fatalf("renderDetail() raw (final=%v) = %q, want substring %q", final, detail, want)
				}
			}

			// Raw mode must preserve literal Markdown markers (no rendering).
			if !strings.Contains(detail, "# Heading") {
				t.Fatalf("renderDetail() raw (final=%v) = %q, want literal '# Heading'", final, detail)
			}
		})
	}
}

// renderMainBaseline is a test-only reference implementation that reproduces
// the old renderMain logic: render all blocks, join with double newlines, split
// into lines, and slice the viewport. Used for parity testing against the new
// viewport-based renderMain.
func renderMainBaseline(m Model) string {
	w := m.width
	ph := m.mainHeight()
	contentWidth := w - 4

	var sections []string
	for i := range m.blocks {
		rendered := m.blocks[i].Render(contentWidth)
		if rendered != "" {
			sections = append(sections, rendered)
		}
	}
	content := strings.Join(sections, "\n\n")

	var allLines []string
	if content != "" {
		allLines = strings.Split(content, "\n")
	}
	visibleLines := ph - 1

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
		lines = append(lines, clipToWidth(allLines[i], contentWidth))
	}
	for len(lines) < visibleLines {
		lines = append(lines, "")
	}

	displayContent := strings.Join(lines, "\n")

	if len(m.blocks) == 0 {
		msg := lipgloss.NewStyle().Foreground(colorDim).Render("Waiting for agent output…")
		displayContent = lipgloss.Place(contentWidth, visibleLines, lipgloss.Center, lipgloss.Center, msg)
	}

	title := " LIVE "
	if m.mainAutoScroll && len(allLines) > visibleLines {
		title = " LIVE [AUTO] "
	} else if ind := scrollIndicator(scroll, visibleLines, len(allLines)); ind != "" {
		title = fmt.Sprintf(" LIVE %s ", ind)
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

func TestRenderMainViewport_MatchesBaselineShort(t *testing.T) {
	m := Model{
		width:  80,
		height: 30,
		blocks: []MainBlock{
			{Kind: BlockIteration, Iteration: 1},
			{Kind: BlockAssistantText, Text: "Hello world", AssistantFinal: true},
			{Kind: BlockToolCall, ToolName: "bash", ToolArgs: "$ ls", ToolDone: true, ToolResult: "file1\nfile2"},
		},
	}
	got := stripANSI(m.renderMain())
	want := stripANSI(renderMainBaseline(m))
	if got != want {
		t.Fatalf("viewport mismatch with baseline:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderMainViewport_MatchesBaselineScrolled(t *testing.T) {
	m := Model{
		width:      80,
		height:     12,
		mainScroll: 3,
		blocks: []MainBlock{
			{Kind: BlockIteration, Iteration: 1},
			{Kind: BlockAssistantText, Text: "Line1\nLine2\nLine3\nLine4\nLine5\nLine6\nLine7\nLine8", AssistantFinal: true},
			{Kind: BlockToolCall, ToolName: "bash", ToolArgs: "$ echo test", ToolDone: true, ToolResult: "test"},
		},
	}
	got := stripANSI(m.renderMain())
	want := stripANSI(renderMainBaseline(m))
	if got != want {
		t.Fatalf("viewport mismatch with baseline:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderMainViewport_MatchesBaselineWithEmptyBlocks(t *testing.T) {
	m := Model{
		width:  80,
		height: 30,
		blocks: []MainBlock{
			{Kind: BlockIteration, Iteration: 1},
			{Kind: BlockAssistantText, Text: ""}, // renders empty
			{Kind: BlockToolCall, ToolName: "read", ToolArgs: "/path/file", ToolDone: true, ToolResult: "content"},
			{Kind: BlockAssistantText, Text: ""}, // renders empty
			{Kind: BlockInfo, InfoText: "done"},
		},
	}
	got := stripANSI(m.renderMain())
	want := stripANSI(renderMainBaseline(m))
	if got != want {
		t.Fatalf("viewport mismatch with baseline:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderMainViewport_MatchesBaselineStreamingAssistant(t *testing.T) {
	m := Model{
		width:   80,
		height:  30,
		running: true,
		blocks: []MainBlock{
			{Kind: BlockIteration, Iteration: 1},
			{Kind: BlockAssistantText, Text: "# Heading\n\nSome text being typed...", AssistantFinal: false},
		},
	}
	got := stripANSI(m.renderMain())
	want := stripANSI(renderMainBaseline(m))
	if got != want {
		t.Fatalf("viewport mismatch with baseline:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderMainViewport_MatchesBaselineCompletedSession(t *testing.T) {
	m := Model{
		width:  80,
		height: 30,
		blocks: []MainBlock{
			{Kind: BlockIteration, Iteration: 1},
			{Kind: BlockAssistantText, Text: "# Analysis\n\nHere is my analysis of the code.", AssistantFinal: true},
			{Kind: BlockToolCall, ToolName: "bash", ToolArgs: "$ go test ./...", ToolDone: true, ToolResult: "ok\nPASS"},
			{Kind: BlockIteration, Iteration: 2},
			{Kind: BlockAssistantText, Text: "Tests pass. Let me fix the issue.", AssistantFinal: true},
			{Kind: BlockToolCall, ToolName: "edit", ToolArgs: "/path/to/file.go", ToolDone: true, ToolResult: "edited successfully"},
			{Kind: BlockThinking, ThinkingLen: 150},
			{Kind: BlockInfo, InfoText: "Run completed"},
		},
	}
	got := stripANSI(m.renderMain())
	want := stripANSI(renderMainBaseline(m))
	if got != want {
		t.Fatalf("viewport mismatch with baseline:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderMain_EmptyState(t *testing.T) {
	m := Model{
		width:  80,
		height: 30,
		blocks: nil,
	}
	got := stripANSI(m.renderMain())
	if !strings.Contains(got, "Waiting for agent output") {
		t.Fatalf("empty LIVE pane should show waiting message, got:\n%s", got)
	}
	// Baseline should also match.
	want := stripANSI(renderMainBaseline(m))
	if got != want {
		t.Fatalf("viewport mismatch with baseline:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// --- Phase 6 QA tests ---

// TestQA_WidthChangePreservesBaselineParity verifies that rendering at one width,
// then changing to a different width, produces output matching the baseline at
// the new width. Covers Scenario C from the QA plan.
func TestQA_WidthChangePreservesBaselineParity(t *testing.T) {
	m := Model{
		width:  80,
		height: 30,
		blocks: []MainBlock{
			{Kind: BlockIteration, Iteration: 1},
			{Kind: BlockAssistantText, Text: "# Wide and Narrow\n\nThis text should reflow when width changes.", AssistantFinal: true},
			{Kind: BlockToolCall, ToolName: "bash", ToolArgs: "$ echo hello", ToolDone: true, ToolResult: "hello"},
			{Kind: BlockIteration, Iteration: 2},
			{Kind: BlockAssistantText, Text: "More content that wraps differently at different widths.", AssistantFinal: true},
		},
	}

	// Render at initial width to populate caches.
	_ = m.renderMain()

	// Change width and verify parity.
	for _, newWidth := range []int{60, 120, 40, 200} {
		m.width = newWidth
		// Invalidate as WindowSizeMsg handler would.
		m.invalidateAllMainLayouts()
		// Reset index width so ensureMainLayout does a full rebuild.
		m.mainLayoutWidth = 0

		got := stripANSI(m.renderMain())
		want := stripANSI(renderMainBaseline(m))
		if got != want {
			t.Fatalf("width %d: viewport mismatch with baseline:\ngot:\n%s\nwant:\n%s", newWidth, got, want)
		}
	}
}

// TestQA_ScrollPositionsMatchBaseline checks parity at multiple scroll positions
// across a tall document. Covers scroll boundary testing from Scenario A.
func TestQA_ScrollPositionsMatchBaseline(t *testing.T) {
	// Build a model with enough content to scroll through.
	var blocks []MainBlock
	for i := 1; i <= 5; i++ {
		blocks = append(blocks,
			MainBlock{Kind: BlockIteration, Iteration: i},
			MainBlock{Kind: BlockAssistantText, Text: fmt.Sprintf("## Iteration %d\n\nParagraph one.\n\nParagraph two with more text to create multiple lines when wrapped.", i), AssistantFinal: true},
			MainBlock{Kind: BlockToolCall, ToolName: "bash", ToolArgs: fmt.Sprintf("$ cmd_%d", i), ToolDone: true, ToolResult: "result line 1\nresult line 2\nresult line 3"},
		)
	}

	m := Model{
		width:  80,
		height: 15, // short viewport to force scrolling
		blocks: blocks,
	}

	// Test at scroll positions: start, middle, end, and beyond.
	for _, scroll := range []int{0, 1, 5, 10, 20, 50, 999999} {
		m.mainScroll = scroll
		// Reset layout state so both paths start fresh.
		m.mainLayoutWidth = 0
		m.mainIndexDirtyFrom = 0
		for i := range m.blocks {
			m.blocks[i].InvalidateLayout()
		}

		got := stripANSI(m.renderMain())
		want := stripANSI(renderMainBaseline(m))
		if got != want {
			t.Fatalf("scroll %d: viewport mismatch with baseline:\ngot:\n%s\nwant:\n%s", scroll, got, want)
		}
	}
}

// TestQA_LiveSessionIncrementalEventsMatchBaseline simulates a live session where
// events arrive one at a time and verifies parity after each event. Covers
// Scenario A (long live session) with incremental correctness.
func TestQA_LiveSessionIncrementalEventsMatchBaseline(t *testing.T) {
	m := Model{
		width:          80,
		height:         20,
		running:        true,
		paneRatio:      0.3,
		mainAutoScroll: true,
		activeToolIdx:  -1,
	}
	initRenderer(m.width - 4)

	events := []DisplayEvent{
		{Type: DisplayIteration, Iteration: 1, Summary: "iteration 1"},
		{Type: DisplayAssistantText, Iteration: 1, Detail: "Starting analysis...", Summary: "< assistant"},
		{Type: DisplayAssistantText, Iteration: 1, Detail: "Starting analysis of the codebase structure.", Summary: "< assistant", AssistantFinal: true},
		{Type: DisplayToolStart, Iteration: 1, ToolCallID: "t1", ToolName: "bash", ToolDisplayArgs: "$ ls", Summary: "> bash"},
		{Type: DisplayToolEnd, Iteration: 1, ToolCallID: "t1", ToolName: "bash", ToolResultText: "file1\nfile2\nfile3", Summary: "+ bash done"},
		{Type: DisplayIteration, Iteration: 2, Summary: "iteration 2"},
		{Type: DisplayAssistantText, Iteration: 2, Detail: "Found the files. Now editing.", Summary: "< assistant", AssistantFinal: true},
		{Type: DisplayToolStart, Iteration: 2, ToolCallID: "t2", ToolName: "edit", ToolDisplayArgs: "/src/main.go", Summary: "> edit"},
		{Type: DisplayToolEnd, Iteration: 2, ToolCallID: "t2", ToolName: "edit", ToolResultText: "edited", Summary: "+ edit done"},
	}

	for step, de := range events {
		m.events = append(m.events, de)
		m.buildBlock(de)
		m.autoScrollMain()

		// Reset caches for fair comparison.
		baseline := m
		baseline.mainLayoutWidth = 0
		baseline.mainIndexDirtyFrom = 0
		for i := range baseline.blocks {
			baseline.blocks[i].InvalidateLayout()
		}

		got := stripANSI(m.renderMain())
		want := stripANSI(renderMainBaseline(baseline))
		if got != want {
			t.Fatalf("step %d (%s): viewport mismatch with baseline:\ngot:\n%s\nwant:\n%s", step, de.Type, got, want)
		}
	}
}

// TestQA_LargeModelAllScrollPositionsMatchBaseline exercises the large benchmark
// model at many scroll positions to verify no off-by-one or stale cache issues.
func TestQA_LargeModelAllScrollPositionsMatchBaseline(t *testing.T) {
	events := benchmarkLongSessionDisplayEvents()
	initRenderer(120 - 4)

	// Build a fresh model for each scroll position to avoid shared-slice issues
	// between the viewport path and baseline path.
	buildModel := func() Model {
		m := Model{
			width:          120,
			height:         40,
			paneRatio:      0.3,
			mainAutoScroll: false,
			activeToolIdx:  -1,
		}
		for _, de := range events {
			m.events = append(m.events, de)
			m.buildBlock(de)
		}
		return m
	}

	// Compute total lines.
	probe := buildModel()
	probe.ensureMainLayout(probe.width - 4)
	total := probe.mainTotalLines

	// Test at a sampling of scroll positions.
	positions := []int{0, 1, total / 4, total / 2, total * 3 / 4, total - 1, total, total + 100}
	for _, scroll := range positions {
		m := buildModel()
		m.mainScroll = scroll

		got := stripANSI(m.renderMain())
		want := stripANSI(renderMainBaseline(m))
		if got != want {
			t.Fatalf("scroll %d/%d: viewport mismatch with baseline (output too long to show)", scroll, total)
		}
	}
}

func TestFormatElapsed(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{5 * time.Second, "5s"},
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m 0s"},
		{90 * time.Second, "1m 30s"},
		{2*time.Minute + 12*time.Second, "2m 12s"},
		{59*time.Minute + 59*time.Second, "59m 59s"},
		{time.Hour, "1h 0m"},
		{time.Hour + 2*time.Minute, "1h 2m"},
		{2*time.Hour + 30*time.Minute + 45*time.Second, "2h 30m"},
		// sub-second fractions are truncated
		{5*time.Second + 999*time.Millisecond, "5s"},
	}
	for _, tt := range tests {
		if got := formatElapsed(tt.d); got != tt.want {
			t.Errorf("formatElapsed(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}
