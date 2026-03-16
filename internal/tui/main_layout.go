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

	// Width change → invalidate all block layout caches and rebuild fully.
	if width != m.mainLayoutWidth {
		for i := range m.blocks {
			m.blocks[i].InvalidateLayout()
		}
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
