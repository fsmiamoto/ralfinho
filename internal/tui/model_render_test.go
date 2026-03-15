package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
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
	for _, want := range []string{"Error", "permission denied while writing", "meta.json after a failed run", "Press any key to dismiss"} {
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
	for _, want := range []string{"LIVE [3/6]", "line3", "line4", "line5", "line6"} {
		if !strings.Contains(main, want) {
			t.Fatalf("renderMain() = %q, want substring %q", main, want)
		}
	}
	if strings.Contains(main, "line1") || strings.Contains(main, "line2") {
		t.Fatalf("renderMain() should only show scrolled content, got %q", main)
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
		if !strings.Contains(status, "Quit? Press y to confirm") {
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
