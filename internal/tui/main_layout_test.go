package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

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

func TestModelInvalidateMainLayoutFromAssistantDelta(t *testing.T) {
	m := NewModel(nil)
	m.running = true
	m.width = 80
	m.height = 40

	// Build some history: iteration + assistant + tool.
	m.buildBlock(DisplayEvent{Type: DisplayIteration, Iteration: 1})
	m.buildBlock(DisplayEvent{Type: DisplayAssistantText, Detail: "first answer", Iteration: 1, AssistantFinal: true})
	m.buildBlock(DisplayEvent{Type: DisplayToolStart, Iteration: 1, ToolCallID: "t1", ToolName: "bash", ToolDisplayArgs: "$ ls"})
	m.buildBlock(DisplayEvent{Type: DisplayToolEnd, Iteration: 1, ToolCallID: "t1", ToolResultText: "ok"})
	m.buildBlock(DisplayEvent{Type: DisplayIteration, Iteration: 2})
	m.buildBlock(DisplayEvent{Type: DisplayAssistantText, Detail: "streaming", Iteration: 2})

	// Compute a clean layout.
	m.ensureMainLayout(76)
	if m.mainIndexDirtyFrom != len(m.blocks) {
		t.Fatalf("after initial layout: dirtyFrom=%d, want %d (all clean)", m.mainIndexDirtyFrom, len(m.blocks))
	}

	// Save earlier block layout pointers to verify they are NOT invalidated.
	block0Lines := m.blocks[0].layoutLines
	block1Lines := m.blocks[1].layoutLines

	// Simulate an assistant delta on the tail block.
	tailIdx := len(m.blocks) - 1
	m.blocks[tailIdx].Text = "streaming more text"
	m.blocks[tailIdx].InvalidateLayout()
	m.invalidateMainLayoutFrom(tailIdx)

	// Only the tail block should be dirty.
	if m.mainIndexDirtyFrom != tailIdx {
		t.Fatalf("after delta: dirtyFrom=%d, want %d", m.mainIndexDirtyFrom, tailIdx)
	}
	// Earlier block caches must be untouched.
	if m.blocks[0].layoutLines == nil || &m.blocks[0].layoutLines[0] != &block0Lines[0] {
		t.Fatal("block 0 layout was invalidated, want preserved")
	}
	if m.blocks[1].layoutLines == nil || &m.blocks[1].layoutLines[0] != &block1Lines[0] {
		t.Fatal("block 1 layout was invalidated, want preserved")
	}

	// Rebuild should only process from tailIdx onward.
	m.ensureMainLayout(76)
	if m.mainIndexDirtyFrom != len(m.blocks) {
		t.Fatalf("after rebuild: dirtyFrom=%d, want %d", m.mainIndexDirtyFrom, len(m.blocks))
	}
}

func TestModelInvalidateMainLayoutFromAssistantFinal(t *testing.T) {
	m := NewModel(nil)
	m.running = true

	// Build history + streaming assistant.
	m.buildBlock(DisplayEvent{Type: DisplayIteration, Iteration: 1})
	m.buildBlock(DisplayEvent{Type: DisplayAssistantText, Detail: "streaming text", Iteration: 1})

	// Compute clean layout.
	m.ensureMainLayout(76)
	assistantIdx := 1

	// Simulate finalization: same block transitions to final.
	m.blocks[assistantIdx].Text = "final text"
	m.blocks[assistantIdx].AssistantFinal = true
	m.blocks[assistantIdx].InvalidateLayout()
	m.invalidateMainLayoutFrom(assistantIdx)

	if m.mainIndexDirtyFrom != assistantIdx {
		t.Fatalf("after finalization: dirtyFrom=%d, want %d", m.mainIndexDirtyFrom, assistantIdx)
	}
	if m.blocks[assistantIdx].layoutLines != nil {
		t.Fatal("finalized block should have nil layoutLines (invalidated)")
	}

	// Rebuild should recompute the assistant block.
	m.ensureMainLayout(76)
	if m.blocks[assistantIdx].layoutLines == nil {
		t.Fatal("after rebuild: assistant block layoutLines should be populated")
	}
	if m.mainIndexDirtyFrom != len(m.blocks) {
		t.Fatalf("after rebuild: dirtyFrom=%d, want %d", m.mainIndexDirtyFrom, len(m.blocks))
	}
}

func TestModelInvalidateAllMainLayoutsOnWidthChange(t *testing.T) {
	m := NewModel(nil)
	m.width = 80
	m.height = 40

	// Build blocks and compute layout.
	m.buildBlock(DisplayEvent{Type: DisplayInfo, Detail: "info 1"})
	m.buildBlock(DisplayEvent{Type: DisplayInfo, Detail: "info 2"})
	m.ensureMainLayout(76)

	// Verify all blocks have cached layouts.
	for i := range m.blocks {
		if m.blocks[i].layoutLines == nil {
			t.Fatalf("block %d: layoutLines nil before width change", i)
		}
	}

	// Simulate a WindowSizeMsg that changes width.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m2 := updated.(Model)

	// All block caches should be invalidated.
	for i := range m2.blocks {
		if m2.blocks[i].layoutLines != nil {
			t.Fatalf("block %d: layoutLines should be nil after width change", i)
		}
	}
	if m2.mainIndexDirtyFrom != 0 {
		t.Fatalf("after width change: dirtyFrom=%d, want 0", m2.mainIndexDirtyFrom)
	}
}

func TestModelAppendBlockKeepsEarlierLayoutsReusable(t *testing.T) {
	m := NewModel(nil)
	m.running = true

	// Build initial blocks and compute layout.
	m.buildBlock(DisplayEvent{Type: DisplayIteration, Iteration: 1})
	m.buildBlock(DisplayEvent{Type: DisplayAssistantText, Detail: "answer", Iteration: 1, AssistantFinal: true})
	m.ensureMainLayout(76)

	// Save layout pointers for existing blocks.
	block0Lines := m.blocks[0].layoutLines
	block1Lines := m.blocks[1].layoutLines

	// Append a new block.
	m.buildBlock(DisplayEvent{Type: DisplayToolStart, Iteration: 1, ToolCallID: "t1", ToolName: "bash", ToolDisplayArgs: "$ echo hi"})

	// dirtyFrom should point to the new block, not earlier.
	newIdx := len(m.blocks) - 1
	if m.mainIndexDirtyFrom != newIdx {
		t.Fatalf("after append: dirtyFrom=%d, want %d", m.mainIndexDirtyFrom, newIdx)
	}

	// Earlier block layout caches must be untouched.
	if m.blocks[0].layoutLines == nil || &m.blocks[0].layoutLines[0] != &block0Lines[0] {
		t.Fatal("block 0 layout was invalidated after append, want preserved")
	}
	if m.blocks[1].layoutLines == nil || &m.blocks[1].layoutLines[0] != &block1Lines[0] {
		t.Fatal("block 1 layout was invalidated after append, want preserved")
	}

	// Rebuild should incorporate the new block without re-laying-out earlier ones.
	m.ensureMainLayout(76)
	if m.mainTotalLines == 0 {
		t.Fatal("after rebuild: totalLines=0, want non-zero")
	}
	if m.mainIndexDirtyFrom != len(m.blocks) {
		t.Fatalf("after rebuild: dirtyFrom=%d, want %d", m.mainIndexDirtyFrom, len(m.blocks))
	}
}
