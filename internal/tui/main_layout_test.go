package tui

import "testing"

func TestMainIndex_ComputesStartsAndTotalLines(t *testing.T) {
	m := &Model{activeToolIdx: -1}
	m.blocks = []MainBlock{
		{Kind: BlockInfo, InfoText: "hello"},
		{Kind: BlockInfo, InfoText: "world"},
		{Kind: BlockIteration, Iteration: 1},
	}

	m.ensureMainLayout(40)

	// Each block renders to 1 line. With separators between them:
	// block0(1) + sep(1) + block1(1) + sep(1) + block2(1) = 5
	wantStarts := []int{0, 2, 4}
	wantCounts := []int{1, 1, 1}
	wantTotal := 5

	for i, ws := range wantStarts {
		if m.mainBlockStarts[i] != ws {
			t.Errorf("block %d: start=%d, want %d", i, m.mainBlockStarts[i], ws)
		}
	}
	for i, wc := range wantCounts {
		if m.mainBlockLineCounts[i] != wc {
			t.Errorf("block %d: count=%d, want %d", i, m.mainBlockLineCounts[i], wc)
		}
	}
	if m.mainTotalLines != wantTotal {
		t.Errorf("totalLines=%d, want %d", m.mainTotalLines, wantTotal)
	}
}

func TestMainIndex_PreservesBlankLineSeparators(t *testing.T) {
	m := &Model{activeToolIdx: -1}
	m.blocks = []MainBlock{
		{Kind: BlockInfo, InfoText: "a"},
		{Kind: BlockInfo, InfoText: "b"},
	}

	m.ensureMainLayout(40)

	// 2 single-line blocks: a(1) + sep(1) + b(1) = 3 total lines.
	// Block 1 starts at line 2, not line 1, because the separator occupies line 1.
	if m.mainBlockStarts[1] != 2 {
		t.Errorf("block 1 start=%d, want 2 (separator at line 1)", m.mainBlockStarts[1])
	}
	if m.mainTotalLines != 3 {
		t.Errorf("totalLines=%d, want 3", m.mainTotalLines)
	}
}

func TestMainIndex_SkipsEmptyBlocks(t *testing.T) {
	m := &Model{activeToolIdx: -1}
	m.blocks = []MainBlock{
		{Kind: BlockInfo, InfoText: "hello"},
		{Kind: BlockAssistantText, Text: ""}, // renders empty
		{Kind: BlockInfo, InfoText: "world"},
	}

	m.ensureMainLayout(40)

	// Block 0: start=0, count=1
	// Block 1: empty, count=0
	// Block 2: start=2, count=1 (separator between block 0 and 2)
	// Total: 3
	if m.mainBlockLineCounts[1] != 0 {
		t.Errorf("empty block count=%d, want 0", m.mainBlockLineCounts[1])
	}
	if m.mainBlockStarts[2] != 2 {
		t.Errorf("block 2 start=%d, want 2", m.mainBlockStarts[2])
	}
	if m.mainTotalLines != 3 {
		t.Errorf("totalLines=%d, want 3", m.mainTotalLines)
	}
}

func TestMainIndex_RebuildFromDirtyTail(t *testing.T) {
	m := &Model{activeToolIdx: -1}
	m.blocks = []MainBlock{
		{Kind: BlockInfo, InfoText: "hello"},
		{Kind: BlockInfo, InfoText: "world"},
	}

	m.ensureMainLayout(40)

	// Record original values.
	origStart0 := m.mainBlockStarts[0]
	origCount0 := m.mainBlockLineCounts[0]
	origStart1 := m.mainBlockStarts[1]
	origCount1 := m.mainBlockLineCounts[1]

	// Append a new block. mainIndexDirtyFrom is 2 from the previous call,
	// which correctly covers the new block at index 2.
	m.blocks = append(m.blocks, MainBlock{Kind: BlockInfo, InfoText: "new"})
	m.ensureMainLayout(40)

	// Earlier blocks must be preserved.
	if m.mainBlockStarts[0] != origStart0 || m.mainBlockLineCounts[0] != origCount0 {
		t.Errorf("block 0 changed: start %d→%d, count %d→%d",
			origStart0, m.mainBlockStarts[0], origCount0, m.mainBlockLineCounts[0])
	}
	if m.mainBlockStarts[1] != origStart1 || m.mainBlockLineCounts[1] != origCount1 {
		t.Errorf("block 1 changed: start %d→%d, count %d→%d",
			origStart1, m.mainBlockStarts[1], origCount1, m.mainBlockLineCounts[1])
	}

	// New block 2: start=4 (after block1 end at 3 + separator), count=1.
	if m.mainBlockStarts[2] != 4 {
		t.Errorf("block 2 start=%d, want 4", m.mainBlockStarts[2])
	}
	if m.mainBlockLineCounts[2] != 1 {
		t.Errorf("block 2 count=%d, want 1", m.mainBlockLineCounts[2])
	}
	if m.mainTotalLines != 5 {
		t.Errorf("totalLines=%d, want 5", m.mainTotalLines)
	}
}
