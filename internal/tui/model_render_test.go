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

func TestRenderHeaderShowsElapsedTimeForRunningModel(t *testing.T) {
	m := NewModel(nil)
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
		m := Model{width: 80, status: "Iteration #4", running: true, rawMode: true}
		status := stripANSI(m.renderStatus())
		for _, want := range []string{"Running │ Iteration #4", "r:raw", "q:quit"} {
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

func TestRenderErrorOverlayTruncatesTallMessages(t *testing.T) {
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

	overlay := stripANSI(m.renderErrorOverlay())
	for _, want := range []string{"Error", "line 1", "line 2", "line 3", "...", "Press any key to dismiss"} {
		if !strings.Contains(overlay, want) {
			t.Fatalf("renderErrorOverlay() = %q, want substring %q", overlay, want)
		}
	}
	for _, unwanted := range []string{"line 4", "line 5"} {
		if strings.Contains(overlay, unwanted) {
			t.Fatalf("renderErrorOverlay() = %q, should omit truncated line %q", overlay, unwanted)
		}
	}
}

func TestRenderErrorOverlayUsesMinimumInnerWidthOnTinyTerminals(t *testing.T) {
	m := Model{width: 34, height: 12, errorOverlay: strings.Repeat("x", 25)}
	overlay := stripANSI(m.renderErrorOverlay())
	for _, want := range []string{"Error", "xxxxxxxxxxxxxxxxxxxx", "xxxxx", "Press any key to", "dismiss"} {
		if !strings.Contains(overlay, want) {
			t.Fatalf("renderErrorOverlay() = %q, want substring %q", overlay, want)
		}
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
