package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
)

// renderer is lazily initialized and re-created when width changes.
var (
	renderer      *glamour.TermRenderer
	rendererWidth int // width used to initialize current renderer
)

func initRenderer(width int) {
	if width < 20 {
		width = 80
	}
	// Skip re-init if width hasn't changed.
	if renderer != nil && rendererWidth == width {
		return
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		renderer = nil
		rendererWidth = 0
		return
	}
	renderer = r
	rendererWidth = width
}

// renderMarkdown renders markdown text to styled terminal output.
// Falls back to plain text on error.
func renderMarkdown(text string, width int) string {
	if text == "" {
		return ""
	}

	// Re-init if width changed or not initialized.
	if renderer == nil || rendererWidth != width {
		initRenderer(width)
	}

	if renderer == nil {
		return text
	}

	out, err := renderer.Render(text)
	if err != nil {
		return text
	}

	return strings.TrimRight(out, "\n")
}
