package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
)

// renderer is lazily initialized.
var renderer *glamour.TermRenderer

func initRenderer(width int) {
	if width < 20 {
		width = 80
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		renderer = nil
		return
	}
	renderer = r
}

// renderMarkdown renders markdown text to styled terminal output.
// Falls back to plain text on error.
func renderMarkdown(text string, width int) string {
	if text == "" {
		return ""
	}

	// Re-init if width changed significantly or not initialized.
	if renderer == nil {
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
