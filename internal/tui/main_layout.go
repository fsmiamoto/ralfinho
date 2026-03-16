package tui

// invalidateMainLayoutFrom marks the main-pane line index as needing
// rebuild from blockIdx onward. If the index is already dirty from an
// earlier position, that earlier position is preserved.
func (m *Model) invalidateMainLayoutFrom(blockIdx int) {
	if blockIdx < m.mainIndexDirtyFrom {
		m.mainIndexDirtyFrom = blockIdx
	}
}

// invalidateAllMainLayouts invalidates all block layout caches and
// marks the full main-pane line index for rebuild.
func (m *Model) invalidateAllMainLayouts() {
	for i := range m.blocks {
		m.blocks[i].InvalidateLayout()
	}
	m.mainIndexDirtyFrom = 0
}

// ensureMainLayout ensures all block layouts are computed for the given width
// and the main-pane line index is up to date.
//
// The line index tracks where each block starts in the virtual document and
// how many lines it contributes. Separators (one blank line between consecutive
// non-empty blocks) are modelled explicitly, matching the existing
// strings.Join(sections, "\n\n") semantics.
//
// Callers should set mainIndexDirtyFrom to the earliest changed block index
// before calling this method. ensureMainLayout rebuilds only from that point.
func (m *Model) ensureMainLayout(width int) {
	n := len(m.blocks)

	// Width change → full index rebuild. Block-level Layout handles per-block
	// width mismatch detection, so we only need to mark the index dirty here.
	// Explicit block cache invalidation is handled by invalidateAllMainLayouts()
	// in the WindowSizeMsg handler.
	if width != m.mainLayoutWidth {
		m.mainLayoutWidth = width
		m.mainIndexDirtyFrom = 0
	}

	// Grow index slices to match block count.
	m.mainBlockStarts = growIntSlice(m.mainBlockStarts, n)
	m.mainBlockLineCounts = growIntSlice(m.mainBlockLineCounts, n)

	dirtyFrom := m.mainIndexDirtyFrom
	if dirtyFrom >= n {
		return // nothing dirty
	}

	// Determine the current line position and whether any preceding block
	// was non-empty, by scanning backward from the dirty boundary.
	var linePos int
	var hadNonEmpty bool
	for i := dirtyFrom - 1; i >= 0; i-- {
		if m.mainBlockLineCounts[i] > 0 {
			linePos = m.mainBlockStarts[i] + m.mainBlockLineCounts[i]
			hadNonEmpty = true
			break
		}
	}

	// Rebuild from dirtyFrom onward.
	for i := dirtyFrom; i < n; i++ {
		lines := m.blocks[i].Layout(width)
		lc := len(lines) // nil → 0

		if lc == 0 {
			m.mainBlockStarts[i] = linePos
			m.mainBlockLineCounts[i] = 0
			continue
		}

		// Insert a blank separator line before this non-empty block if a
		// previous non-empty block exists (matching "\n\n" join semantics).
		if hadNonEmpty {
			linePos++
		}

		m.mainBlockStarts[i] = linePos
		m.mainBlockLineCounts[i] = lc
		linePos += lc
		hadNonEmpty = true
	}

	m.mainTotalLines = linePos
	m.mainIndexDirtyFrom = n // all clean
}

// collectViewportLines returns the visible lines for the viewport range
// [viewStart, viewEnd) from the cached block layouts and line index.
// Lines are clipped to contentWidth. Separator lines (between non-empty blocks)
// are returned as empty strings.
//
// ensureMainLayout must have been called for the current width before calling
// this method.
func (m *Model) collectViewportLines(viewStart, viewEnd, contentWidth int) []string {
	if viewStart >= viewEnd {
		return nil
	}

	result := make([]string, 0, viewEnd-viewStart)
	linePos := viewStart

	// Find the first non-empty block whose lines extend past viewStart.
	startBlock := 0
	for startBlock < len(m.blocks) {
		bc := m.mainBlockLineCounts[startBlock]
		if bc > 0 && m.mainBlockStarts[startBlock]+bc > viewStart {
			break
		}
		startBlock++
	}

	for i := startBlock; i < len(m.blocks) && linePos < viewEnd; i++ {
		bc := m.mainBlockLineCounts[i]
		if bc == 0 {
			continue
		}
		bs := m.mainBlockStarts[i]

		// Emit separator/gap lines before this block that fall in the viewport.
		for linePos < bs && linePos < viewEnd {
			result = append(result, "")
			linePos++
		}

		// Emit block lines that fall in the viewport.
		localStart := 0
		if linePos > bs {
			localStart = linePos - bs
		}
		for j := localStart; j < bc && linePos < viewEnd; j++ {
			result = append(result, clipToWidth(m.blocks[i].layoutLines[j], contentWidth))
			linePos++
		}
	}

	// Fill any remaining positions past all blocks.
	for linePos < viewEnd {
		result = append(result, "")
		linePos++
	}

	return result
}

// growIntSlice returns s resized to exactly n elements, preserving existing
// data. If cap is sufficient the slice is resliced; otherwise a new backing
// array is allocated and old data copied.
func growIntSlice(s []int, n int) []int {
	if n <= cap(s) {
		return s[:n]
	}
	ns := make([]int, n)
	copy(ns, s)
	return ns
}
