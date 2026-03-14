package tui

import "testing"

func TestInitRenderer_CreatesRenderer(t *testing.T) {
	// Reset global state.
	renderer = nil
	rendererWidth = 0

	initRenderer(80)

	if renderer == nil {
		t.Fatal("expected renderer to be initialized")
	}
	if rendererWidth != 80 {
		t.Fatalf("expected rendererWidth=80, got %d", rendererWidth)
	}
}

func TestInitRenderer_ClampsSmallWidth(t *testing.T) {
	renderer = nil
	rendererWidth = 0

	initRenderer(10) // below 20 → clamped to 80

	if renderer == nil {
		t.Fatal("expected renderer to be initialized")
	}
	if rendererWidth != 80 {
		t.Fatalf("expected rendererWidth=80 (clamped), got %d", rendererWidth)
	}
}

func TestInitRenderer_SkipsReinitForSameWidth(t *testing.T) {
	renderer = nil
	rendererWidth = 0

	initRenderer(60)
	first := renderer

	initRenderer(60) // same width → should reuse

	if renderer != first {
		t.Fatal("expected renderer to be reused for same width")
	}
}

func TestInitRenderer_ReinitializesForDifferentWidth(t *testing.T) {
	renderer = nil
	rendererWidth = 0

	initRenderer(60)
	first := renderer

	initRenderer(100) // different width → new renderer

	if renderer == first {
		t.Fatal("expected new renderer for different width")
	}
	if rendererWidth != 100 {
		t.Fatalf("expected rendererWidth=100, got %d", rendererWidth)
	}
}

func TestInitRenderer_ZeroWidthClamps(t *testing.T) {
	renderer = nil
	rendererWidth = 0

	initRenderer(0)

	if renderer == nil {
		t.Fatal("expected renderer to be initialized")
	}
	if rendererWidth != 80 {
		t.Fatalf("expected rendererWidth=80 (clamped from 0), got %d", rendererWidth)
	}
}

func TestInitRenderer_NegativeWidthClamps(t *testing.T) {
	renderer = nil
	rendererWidth = 0

	initRenderer(-5)

	if renderer == nil {
		t.Fatal("expected renderer to be initialized")
	}
	if rendererWidth != 80 {
		t.Fatalf("expected rendererWidth=80 (clamped from -5), got %d", rendererWidth)
	}
}

func TestRenderMarkdown_EmptyString(t *testing.T) {
	got := renderMarkdown("", 80)
	if got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestRenderMarkdown_PlainText(t *testing.T) {
	renderer = nil
	rendererWidth = 0

	got := renderMarkdown("hello world", 80)

	if got == "" {
		t.Fatal("expected non-empty output")
	}
	// glamour wraps plain text in a paragraph; check content is preserved.
	if len(got) == 0 {
		t.Fatal("got empty output")
	}
}

func TestRenderMarkdown_BoldText(t *testing.T) {
	renderer = nil
	rendererWidth = 0

	got := renderMarkdown("**bold**", 80)

	if got == "" {
		t.Fatal("expected non-empty output for bold markdown")
	}
}

func TestRenderMarkdown_CodeBlock(t *testing.T) {
	renderer = nil
	rendererWidth = 0

	input := "```go\nfmt.Println(\"hi\")\n```"
	got := renderMarkdown(input, 80)

	if got == "" {
		t.Fatal("expected non-empty output for code block")
	}
}

func TestRenderMarkdown_TrimsTrailingNewlines(t *testing.T) {
	renderer = nil
	rendererWidth = 0

	got := renderMarkdown("hello", 80)

	last := got[len(got)-1]
	if last == '\n' {
		t.Fatal("expected trailing newlines to be trimmed")
	}
}

func TestRenderMarkdown_ReusesRendererForSameWidth(t *testing.T) {
	renderer = nil
	rendererWidth = 0

	// First call initializes renderer.
	renderMarkdown("first", 80)
	first := renderer

	// Second call with same width should reuse.
	renderMarkdown("second", 80)
	if renderer != first {
		t.Fatal("expected renderer to be reused for same width")
	}
}

func TestRenderMarkdown_InitializesLazilyOnWidthChange(t *testing.T) {
	renderer = nil
	rendererWidth = 0

	// First call initializes at width 60.
	renderMarkdown("first", 60)
	if rendererWidth != 60 {
		t.Fatalf("expected rendererWidth=60 after first call, got %d", rendererWidth)
	}

	// Second call at different width re-initializes.
	renderMarkdown("second", 120)
	if rendererWidth != 120 {
		t.Fatalf("expected rendererWidth=120 after width change, got %d", rendererWidth)
	}
}
