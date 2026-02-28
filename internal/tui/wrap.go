// Package tui provides the terminal user interface for ralfinho.
// wrap.go contains utilities for soft-wrapping text to a given width.
package tui

import (
	"strings"

	runewidth "github.com/mattn/go-runewidth"
)

// WrapLine soft-wraps a single line at word boundaries (spaces) to fit within
// width visual columns. If a single word exceeds the width it is hard-broken.
// Returns a slice of wrapped lines.
func WrapLine(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}

	if runewidth.StringWidth(line) <= width {
		return []string{line}
	}

	words := strings.Split(line, " ")
	var result []string
	cur := ""
	curW := 0

	for i, word := range words {
		ww := runewidth.StringWidth(word)

		// Word alone exceeds width — hard-break it.
		if ww > width {
			// Flush anything already accumulated.
			if curW > 0 {
				result = append(result, cur)
				cur = ""
				curW = 0
			}
			// Break the long word into chunks.
			chunk := ""
			chunkW := 0
			for _, r := range word {
				rw := runewidth.RuneWidth(r)
				if chunkW+rw > width {
					result = append(result, chunk)
					chunk = ""
					chunkW = 0
				}
				chunk += string(r)
				chunkW += rw
			}
			// Keep the leftover chunk as the current line (may merge with next word).
			cur = chunk
			curW = chunkW
			continue
		}

		// Calculate the space needed: if cur is non-empty we need a separator space.
		sep := 0
		if curW > 0 {
			sep = 1
		}

		if curW+sep+ww > width {
			// Emit the current line and start a new one with this word.
			result = append(result, cur)
			cur = word
			curW = ww
		} else {
			if i > 0 && curW > 0 {
				cur += " "
				curW++
			}
			cur += word
			curW += ww
		}
	}

	// Flush remaining content.
	if curW > 0 {
		result = append(result, cur)
	}

	return result
}

// WrapText splits text on newlines, applies WrapLine to each line, and joins
// the results back with newlines. Existing line breaks are preserved.
// If width <= 0 the text is returned unchanged.
func WrapText(text string, width int) string {
	if width <= 0 {
		return text
	}

	lines := strings.Split(text, "\n")
	var out []string
	for _, line := range lines {
		wrapped := WrapLine(line, width)
		out = append(out, wrapped...)
	}
	return strings.Join(out, "\n")
}
