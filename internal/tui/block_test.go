package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Layout cache
// ---------------------------------------------------------------------------

func TestMainBlockLayoutCache_ReusesSameWidth(t *testing.T) {
	b := &MainBlock{Kind: BlockInfo, InfoText: "hello"}

	lines1 := b.Layout(40)
	if lines1 == nil {
		t.Fatal("expected non-nil lines from Layout")
	}
	if b.layoutWidth != 40 {
		t.Errorf("expected layoutWidth=40, got %d", b.layoutWidth)
	}

	// Second call with same width should return the cached slice.
	lines2 := b.Layout(40)
	if &lines1[0] != &lines2[0] {
		t.Error("expected Layout to reuse cached lines for same width")
	}
}

func TestMainBlockLayoutCache_InvalidatesOnWidthChange(t *testing.T) {
	b := &MainBlock{Kind: BlockInfo, InfoText: "hello"}

	lines40 := b.Layout(40)
	if lines40 == nil {
		t.Fatal("expected non-nil lines")
	}
	cachedPtr := &lines40[0]

	// Different width should recompute.
	lines60 := b.Layout(60)
	if lines60 == nil {
		t.Fatal("expected non-nil lines at new width")
	}
	if b.layoutWidth != 60 {
		t.Errorf("expected layoutWidth=60, got %d", b.layoutWidth)
	}
	if &lines60[0] == cachedPtr {
		t.Error("expected Layout to recompute for different width")
	}
}

func TestMainBlockLayoutCache_InvalidatesOnAssistantFinalTransition(t *testing.T) {
	// Use markdown syntax so streaming (plain wrap) and final (markdown render)
	// produce visibly different output.
	b := &MainBlock{Kind: BlockAssistantText, Iteration: 1, Text: "**bold** text"}

	streamingLines := b.Layout(40)
	if streamingLines == nil {
		t.Fatal("expected non-nil streaming lines")
	}
	streamingJoined := strings.Join(streamingLines, "\n")

	// Transition to final and invalidate.
	b.AssistantFinal = true
	b.InvalidateLayout()

	finalLines := b.Layout(40)
	if finalLines == nil {
		t.Fatal("expected non-nil final lines")
	}
	finalJoined := strings.Join(finalLines, "\n")

	if streamingJoined == finalJoined {
		t.Error("expected different layout after assistant final transition")
	}
}

func TestMainBlockLayoutCache_InvalidatesOnToolStateChange(t *testing.T) {
	b := &MainBlock{
		Kind:     BlockToolCall,
		ToolName: "bash",
		ToolArgs: "$ echo hi",
	}

	runningLines := b.Layout(40)
	if runningLines == nil {
		t.Fatal("expected non-nil running lines")
	}
	runningJoined := strings.Join(runningLines, "\n")

	// Complete the tool and invalidate.
	b.ToolDone = true
	b.ToolResult = "hi"
	b.InvalidateLayout()

	doneLines := b.Layout(40)
	if doneLines == nil {
		t.Fatal("expected non-nil done lines")
	}
	doneJoined := strings.Join(doneLines, "\n")

	if runningJoined == doneJoined {
		t.Error("expected different layout after tool state change")
	}
}

func TestMainBlockLayoutCache_EmptyRenderReturnsNil(t *testing.T) {
	// An empty assistant text block renders to "".
	b := &MainBlock{Kind: BlockAssistantText, Text: ""}

	lines := b.Layout(40)
	if lines != nil {
		t.Errorf("expected nil for empty render, got %v", lines)
	}
	if b.layoutWidth != 40 {
		t.Errorf("expected layoutWidth=40 even for empty render, got %d", b.layoutWidth)
	}
}

func TestMainBlockLayoutCache_InvalidateLayoutClearsCache(t *testing.T) {
	b := &MainBlock{Kind: BlockInfo, InfoText: "test"}

	_ = b.Layout(40)
	if b.layoutLines == nil {
		t.Fatal("expected cache to be populated")
	}

	b.InvalidateLayout()
	if b.layoutLines != nil {
		t.Error("expected layoutLines to be nil after InvalidateLayout")
	}
}

// ---------------------------------------------------------------------------
// normalizeToolName
// ---------------------------------------------------------------------------

func TestNormalizeToolName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// bash variants
		{"bash", "bash"},
		{"Bash", "bash"},
		{"BASH", "bash"},
		{"shell", "bash"},
		{"Shell", "bash"},
		{"execute", "bash"},
		{"Execute", "bash"},
		// read variants
		{"read", "read"},
		{"Read", "read"},
		// edit variants
		{"edit", "edit"},
		{"Edit", "edit"},
		// write variants
		{"write", "write"},
		{"Write", "write"},
		// unknown names pass through unchanged
		{"list_files", "list_files"},
		{"unknown_tool", "unknown_tool"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeToolName(tt.input)
			if got != tt.want {
				t.Errorf("normalizeToolName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// formatToolArgs — name-based detection
// ---------------------------------------------------------------------------

func TestFormatToolArgs_NilArgs(t *testing.T) {
	if got := formatToolArgs("bash", nil); got != "" {
		t.Errorf("expected empty string for nil args, got %q", got)
	}
}

func TestFormatToolArgs_BashCommand(t *testing.T) {
	args := json.RawMessage(`{"command":"git status"}`)
	got := formatToolArgs("bash", args)
	if got != "$ git status" {
		t.Errorf("expected %q, got %q", "$ git status", got)
	}
}

func TestFormatToolArgs_ShellCommand(t *testing.T) {
	// "shell" normalizes to "bash", so the name-based path handles it.
	args := json.RawMessage(`{"command":"ls -la"}`)
	got := formatToolArgs("shell", args)
	if got != "$ ls -la" {
		t.Errorf("expected %q, got %q", "$ ls -la", got)
	}
}

func TestFormatToolArgs_ReadPath(t *testing.T) {
	args := json.RawMessage(`{"path":"/home/user/file.go"}`)
	got := formatToolArgs("read", args)
	if got != "/home/user/file.go" {
		t.Errorf("expected %q, got %q", "/home/user/file.go", got)
	}
}

func TestFormatToolArgs_EditFilePath(t *testing.T) {
	args := json.RawMessage(`{"file_path":"/home/user/edit.go"}`)
	got := formatToolArgs("edit", args)
	if got != "/home/user/edit.go" {
		t.Errorf("expected %q, got %q", "/home/user/edit.go", got)
	}
}

func TestFormatToolArgs_WritePath(t *testing.T) {
	args := json.RawMessage(`{"path":"/tmp/output.txt"}`)
	got := formatToolArgs("write", args)
	if got != "/tmp/output.txt" {
		t.Errorf("expected %q, got %q", "/tmp/output.txt", got)
	}
}

// ---------------------------------------------------------------------------
// formatToolArgs — content-based detection (unrecognized tool name)
// ---------------------------------------------------------------------------

func TestFormatToolArgs_ContentBased_CommandKey(t *testing.T) {
	// Unknown tool name, but args have a "command" key — should render as "$ cmd".
	args := json.RawMessage(`{"command":"make build"}`)
	got := formatToolArgs("unknown_execute_tool", args)
	if got != "$ make build" {
		t.Errorf("expected %q, got %q", "$ make build", got)
	}
}

func TestFormatToolArgs_ContentBased_PathKey(t *testing.T) {
	// Unknown tool name, but args have a "path" key.
	args := json.RawMessage(`{"path":"/some/file.txt","extra":"ignored"}`)
	got := formatToolArgs("unknown_file_tool", args)
	if got != "/some/file.txt" {
		t.Errorf("expected %q, got %q", "/some/file.txt", got)
	}
}

func TestFormatToolArgs_ContentBased_FilePathKey(t *testing.T) {
	// Unknown tool name, but args have a "file_path" key.
	args := json.RawMessage(`{"file_path":"/src/main.go"}`)
	got := formatToolArgs("unknown_file_tool", args)
	if got != "/src/main.go" {
		t.Errorf("expected %q, got %q", "/src/main.go", got)
	}
}

func TestFormatToolArgs_ContentBased_PathPreferredOverFilePath(t *testing.T) {
	// When both "path" and "file_path" are present, "path" takes precedence
	// (it is checked first in the iteration order).
	args := json.RawMessage(`{"path":"/primary.go","file_path":"/secondary.go"}`)
	got := formatToolArgs("some_tool", args)
	if got != "/primary.go" {
		t.Errorf("expected %q, got %q", "/primary.go", got)
	}
}

func TestFormatToolArgs_ContentBased_EmptyCommandFallsThrough(t *testing.T) {
	// Empty "command" value should not produce "$ " — fall through to raw JSON.
	args := json.RawMessage(`{"command":""}`)
	got := formatToolArgs("unknown_tool", args)
	// Should fall through to raw JSON (the map has a "command" key but its value
	// is empty, so we skip it and reach the raw JSON fallback).
	if got == "$ " {
		t.Error("expected raw JSON fallback, not empty command prefix")
	}
	// Raw JSON output should contain the original JSON.
	if got != `{"command":""}` {
		t.Errorf("expected raw JSON fallback, got %q", got)
	}
}

func TestFormatToolArgs_RawJSONFallback(t *testing.T) {
	// No recognized keys — should render raw JSON.
	args := json.RawMessage(`{"unknown_key":"some_value"}`)
	got := formatToolArgs("totally_unknown", args)
	if got != `{"unknown_key":"some_value"}` {
		t.Errorf("expected raw JSON, got %q", got)
	}
}

func TestFormatToolArgs_RawJSONFallback_Truncation(t *testing.T) {
	// More than 80 characters of raw JSON should be truncated.
	longValue := `{"key":"` + string(make([]byte, 80)) + `"}`
	args := json.RawMessage(longValue)
	got := formatToolArgs("totally_unknown", args)
	if len([]rune(got)) > 80 {
		t.Errorf("expected truncation to 80 runes, got %d runes", len([]rune(got)))
	}
	if got[len(got)-3:] != "..." {
		t.Errorf("expected truncated string to end with ..., got %q", got[len(got)-3:])
	}
}

func TestFormatToolArgs_RawJSONFallback_Truncation_MultiByte(t *testing.T) {
	// Multi-byte characters must be truncated by rune, not by byte,
	// to avoid splitting a character in the middle.
	longValue := `{"key":"` + strings.Repeat("日本語", 30) + `"}`
	args := json.RawMessage(longValue)
	got := formatToolArgs("totally_unknown", args)
	runes := []rune(got)
	if len(runes) > 80 {
		t.Errorf("expected truncation to ≤80 runes, got %d runes", len(runes))
	}
	if got[len(got)-3:] != "..." {
		t.Errorf("expected truncated string to end with ..., got %q", got[len(got)-3:])
	}
	// Verify the result is valid UTF-8 by checking round-trip.
	if string([]rune(got)) != got {
		t.Error("truncated result is not valid UTF-8")
	}
}

// ---------------------------------------------------------------------------
// buildBlock — ToolDisplayArgs takes precedence over formatToolArgs
// ---------------------------------------------------------------------------

func TestBuildBlock_ToolDisplayArgs_UsedDirectly(t *testing.T) {
	m := &Model{activeToolIdx: -1}

	// Emit a DisplayToolStart with both RawArgs and ToolDisplayArgs set.
	// The pre-formatted string must win over the content-based detection.
	de := DisplayEvent{
		Type:            DisplayToolStart,
		ToolCallID:      "tc-display",
		ToolName:        "bash",
		RawArgs:         json.RawMessage(`{"command":"git status"}`),
		ToolDisplayArgs: "$ git status", // already formatted by the mapper
	}
	m.buildBlock(de)

	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.blocks))
	}
	if m.blocks[0].ToolArgs != "$ git status" {
		t.Errorf("expected ToolArgs=%q (from ToolDisplayArgs), got %q", "$ git status", m.blocks[0].ToolArgs)
	}
}

func TestBuildBlock_ToolDisplayArgs_FallsBackWhenEmpty(t *testing.T) {
	m := &Model{activeToolIdx: -1}

	// No ToolDisplayArgs — should fall through to formatToolArgs.
	de := DisplayEvent{
		Type:       DisplayToolStart,
		ToolCallID: "tc-fallback",
		ToolName:   "bash",
		RawArgs:    json.RawMessage(`{"command":"ls -la"}`),
		// ToolDisplayArgs intentionally omitted
	}
	m.buildBlock(de)

	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.blocks))
	}
	if m.blocks[0].ToolArgs != "$ ls -la" {
		t.Errorf("expected ToolArgs=%q (from formatToolArgs fallback), got %q", "$ ls -la", m.blocks[0].ToolArgs)
	}
}

// ---------------------------------------------------------------------------
// truncateResult
// ---------------------------------------------------------------------------

func TestTruncateResult(t *testing.T) {
	tests := []struct {
		name     string
		result   string
		maxLines int
		want     string
	}{
		{
			name:     "empty string",
			result:   "",
			maxLines: 6,
			want:     "",
		},
		{
			name:     "single line within limit",
			result:   "hello world",
			maxLines: 6,
			want:     "hello world",
		},
		{
			name:     "exactly at limit",
			result:   "line1\nline2\nline3",
			maxLines: 3,
			want:     "line1\nline2\nline3",
		},
		{
			name:     "exceeds limit by one",
			result:   "line1\nline2\nline3\nline4",
			maxLines: 3,
			want:     "line1\nline2\nline3\n… (1 more lines)",
		},
		{
			name:     "exceeds limit by many",
			result:   "a\nb\nc\nd\ne\nf\ng\nh\ni\nj",
			maxLines: 3,
			want:     "a\nb\nc\n… (7 more lines)",
		},
		{
			name:     "maxLines 1",
			result:   "first\nsecond\nthird",
			maxLines: 1,
			want:     "first\n… (2 more lines)",
		},
		{
			name:     "trailing newline counts as extra line",
			result:   "line1\nline2\n",
			maxLines: 2,
			want:     "line1\nline2\n… (1 more lines)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateResult(tt.result, tt.maxLines)
			if got != tt.want {
				t.Errorf("truncateResult(%q, %d) = %q, want %q", tt.result, tt.maxLines, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// buildBlock — all block types
// ---------------------------------------------------------------------------

func TestBuildBlock_Iteration(t *testing.T) {
	m := &Model{activeToolIdx: -1}
	m.buildBlock(DisplayEvent{Type: DisplayIteration, Iteration: 3})

	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.blocks))
	}
	if m.blocks[0].Kind != BlockIteration {
		t.Errorf("expected BlockIteration, got %d", m.blocks[0].Kind)
	}
	if m.blocks[0].Iteration != 3 {
		t.Errorf("expected Iteration=3, got %d", m.blocks[0].Iteration)
	}
}

func TestBuildBlock_AssistantText_NewBlock(t *testing.T) {
	m := &Model{activeToolIdx: -1}
	m.buildBlock(DisplayEvent{Type: DisplayAssistantText, Iteration: 1, Detail: "hello"})

	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.blocks))
	}
	if m.blocks[0].Kind != BlockAssistantText {
		t.Errorf("expected BlockAssistantText, got %d", m.blocks[0].Kind)
	}
	if m.blocks[0].Text != "hello" {
		t.Errorf("expected Text=%q, got %q", "hello", m.blocks[0].Text)
	}
}

func TestBuildBlock_AssistantText_MergesSameIteration(t *testing.T) {
	m := &Model{activeToolIdx: -1}
	m.buildBlock(DisplayEvent{Type: DisplayAssistantText, Iteration: 1, Detail: "hello"})
	m.buildBlock(DisplayEvent{Type: DisplayAssistantText, Iteration: 1, Detail: "hello world"})

	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 block (merged), got %d", len(m.blocks))
	}
	if m.blocks[0].Text != "hello world" {
		t.Errorf("expected merged Text=%q, got %q", "hello world", m.blocks[0].Text)
	}
}

func TestBuildBlock_AssistantText_NewBlockForDifferentIteration(t *testing.T) {
	m := &Model{activeToolIdx: -1}
	m.buildBlock(DisplayEvent{Type: DisplayAssistantText, Iteration: 1, Detail: "first"})
	m.buildBlock(DisplayEvent{Type: DisplayAssistantText, Iteration: 2, Detail: "second"})

	if len(m.blocks) != 2 {
		t.Fatalf("expected 2 blocks (different iterations), got %d", len(m.blocks))
	}
	if m.blocks[0].Text != "first" {
		t.Errorf("block 0: expected Text=%q, got %q", "first", m.blocks[0].Text)
	}
	if m.blocks[1].Text != "second" {
		t.Errorf("block 1: expected Text=%q, got %q", "second", m.blocks[1].Text)
	}
}

func TestBuildBlock_AssistantText_NoMergeAfterDifferentBlockKind(t *testing.T) {
	m := &Model{activeToolIdx: -1}
	m.buildBlock(DisplayEvent{Type: DisplayAssistantText, Iteration: 1, Detail: "first"})
	m.buildBlock(DisplayEvent{Type: DisplayIteration, Iteration: 1})
	m.buildBlock(DisplayEvent{Type: DisplayAssistantText, Iteration: 1, Detail: "second"})

	// Should be 3 blocks: assistant, iteration, assistant (not merged)
	if len(m.blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(m.blocks))
	}
	if m.blocks[2].Text != "second" {
		t.Errorf("block 2: expected Text=%q, got %q", "second", m.blocks[2].Text)
	}
}

func TestBuildBlock_Thinking(t *testing.T) {
	m := &Model{activeToolIdx: -1}
	m.buildBlock(DisplayEvent{Type: DisplayThinking, Iteration: 2, Detail: "some thinking content"})

	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.blocks))
	}
	if m.blocks[0].Kind != BlockThinking {
		t.Errorf("expected BlockThinking, got %d", m.blocks[0].Kind)
	}
	if m.blocks[0].ThinkingLen != len("some thinking content") {
		t.Errorf("expected ThinkingLen=%d, got %d", len("some thinking content"), m.blocks[0].ThinkingLen)
	}
}

func TestBuildBlock_ToolLifecycle_StartUpdateEnd(t *testing.T) {
	m := &Model{activeToolIdx: -1}

	// Start
	m.buildBlock(DisplayEvent{
		Type:       DisplayToolStart,
		ToolCallID: "tc-1",
		ToolName:   "bash",
		RawArgs:    json.RawMessage(`{"command":"echo hi"}`),
	})
	if len(m.blocks) != 1 {
		t.Fatalf("after start: expected 1 block, got %d", len(m.blocks))
	}
	if m.activeToolIdx != 0 {
		t.Errorf("activeToolIdx should be 0 after tool start, got %d", m.activeToolIdx)
	}
	if m.blocks[0].ToolDone {
		t.Error("tool should not be done after start")
	}

	// Update
	m.buildBlock(DisplayEvent{
		Type:            DisplayToolUpdate,
		ToolCallID:      "tc-1",
		ToolName:        "bash",
		ToolDisplayArgs: "$ echo updated",
	})
	if len(m.blocks) != 1 {
		t.Fatalf("after update: expected 1 block, got %d", len(m.blocks))
	}
	if m.blocks[0].ToolArgs != "$ echo updated" {
		t.Errorf("after update: expected ToolArgs=%q, got %q", "$ echo updated", m.blocks[0].ToolArgs)
	}

	// End
	m.buildBlock(DisplayEvent{
		Type:           DisplayToolEnd,
		ToolCallID:     "tc-1",
		ToolName:       "bash",
		ToolResultText: "hi\n",
		ToolIsError:    false,
	})
	if m.blocks[0].ToolDone != true {
		t.Error("tool should be done after end")
	}
	if m.blocks[0].ToolResult != "hi\n" {
		t.Errorf("expected ToolResult=%q, got %q", "hi\n", m.blocks[0].ToolResult)
	}
	if m.blocks[0].ToolError {
		t.Error("tool should not be error")
	}
	if m.activeToolIdx != -1 {
		t.Errorf("activeToolIdx should be -1 after tool end, got %d", m.activeToolIdx)
	}
}

func TestBuildBlock_ToolEnd_WithError(t *testing.T) {
	m := &Model{activeToolIdx: -1}

	m.buildBlock(DisplayEvent{
		Type:       DisplayToolStart,
		ToolCallID: "tc-err",
		ToolName:   "bash",
	})
	m.buildBlock(DisplayEvent{
		Type:           DisplayToolEnd,
		ToolCallID:     "tc-err",
		ToolName:       "bash",
		ToolResultText: "command not found",
		ToolIsError:    true,
	})

	if !m.blocks[0].ToolError {
		t.Error("tool should be marked as error")
	}
	if m.blocks[0].ToolResult != "command not found" {
		t.Errorf("expected ToolResult=%q, got %q", "command not found", m.blocks[0].ToolResult)
	}
}

func TestBuildBlock_ToolEnd_UnmatchedID(t *testing.T) {
	m := &Model{activeToolIdx: -1}

	m.buildBlock(DisplayEvent{
		Type:       DisplayToolStart,
		ToolCallID: "tc-a",
		ToolName:   "bash",
	})
	// End with a different ID — should not match any block.
	m.buildBlock(DisplayEvent{
		Type:           DisplayToolEnd,
		ToolCallID:     "tc-b",
		ToolName:       "bash",
		ToolResultText: "result",
	})

	if m.blocks[0].ToolDone {
		t.Error("tool tc-a should not be marked done when tc-b ends")
	}
}

func TestBuildBlock_Info(t *testing.T) {
	m := &Model{activeToolIdx: -1}
	m.buildBlock(DisplayEvent{Type: DisplayInfo, Detail: "some info"})

	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.blocks))
	}
	if m.blocks[0].Kind != BlockInfo {
		t.Errorf("expected BlockInfo, got %d", m.blocks[0].Kind)
	}
	if m.blocks[0].InfoText != "some info" {
		t.Errorf("expected InfoText=%q, got %q", "some info", m.blocks[0].InfoText)
	}
}

func TestBuildBlock_SkippedTypes(t *testing.T) {
	m := &Model{activeToolIdx: -1}

	// These display types should not produce blocks.
	for _, typ := range []DisplayEventType{DisplaySession, DisplayUserMsg, DisplayTurnEnd, DisplayAgentEnd} {
		m.buildBlock(DisplayEvent{Type: typ, Detail: "ignored"})
	}

	if len(m.blocks) != 0 {
		t.Errorf("expected 0 blocks for skipped types, got %d", len(m.blocks))
	}
}

// ---------------------------------------------------------------------------
// updateAssistantBlock
// ---------------------------------------------------------------------------

func TestUpdateAssistantBlock_UpdatesMatchingIteration(t *testing.T) {
	m := &Model{activeToolIdx: -1}
	m.blocks = []MainBlock{
		{Kind: BlockIteration, Iteration: 1},
		{Kind: BlockAssistantText, Iteration: 1, Text: "old"},
	}

	m.updateAssistantBlock(DisplayEvent{Iteration: 1, Detail: "new text"})

	if m.blocks[1].Text != "new text" {
		t.Errorf("expected Text=%q, got %q", "new text", m.blocks[1].Text)
	}
}

func TestUpdateAssistantBlock_NoMatchDoesNothing(t *testing.T) {
	m := &Model{activeToolIdx: -1}
	m.blocks = []MainBlock{
		{Kind: BlockAssistantText, Iteration: 1, Text: "original"},
	}

	m.updateAssistantBlock(DisplayEvent{Iteration: 2, Detail: "should not appear"})

	if m.blocks[0].Text != "original" {
		t.Errorf("expected Text unchanged, got %q", m.blocks[0].Text)
	}
}

// ---------------------------------------------------------------------------
// addDisplayEvent — model name extraction and iteration counter
// ---------------------------------------------------------------------------

func TestAddDisplayEvent_ExtractsModelName(t *testing.T) {
	m := Model{activeToolIdx: -1, running: true, converter: NewEventConverter()}

	de := DisplayEvent{
		Type:    DisplayAssistantText,
		Summary: "← Assistant (claude-4-opus)",
		Detail:  "hello",
	}
	updated, _ := m.addDisplayEvent(de)
	model := updated.(Model)

	if model.modelName != "claude-4-opus" {
		t.Errorf("expected modelName=%q, got %q", "claude-4-opus", model.modelName)
	}
}

func TestAddDisplayEvent_ModelNameNotExtractedWithoutParens(t *testing.T) {
	m := Model{activeToolIdx: -1, running: true, converter: NewEventConverter()}

	de := DisplayEvent{
		Type:    DisplayAssistantText,
		Summary: "← Assistant text only",
		Detail:  "hello",
	}
	updated, _ := m.addDisplayEvent(de)
	model := updated.(Model)

	if model.modelName != "" {
		t.Errorf("expected empty modelName, got %q", model.modelName)
	}
}

func TestAddDisplayEvent_IterationUpdatesStatus(t *testing.T) {
	m := Model{activeToolIdx: -1, running: true, converter: NewEventConverter()}

	de := DisplayEvent{
		Type:      DisplayIteration,
		Iteration: 5,
	}
	updated, _ := m.addDisplayEvent(de)
	model := updated.(Model)

	if model.iteration != 5 {
		t.Errorf("expected iteration=5, got %d", model.iteration)
	}
	if model.status != "Iteration #5" {
		t.Errorf("expected status=%q, got %q", "Iteration #5", model.status)
	}
}

func TestAddDisplayEvent_IterationIgnoredWhenNotRunning(t *testing.T) {
	m := Model{activeToolIdx: -1, running: false, converter: NewEventConverter(), status: "Done"}

	de := DisplayEvent{
		Type:      DisplayIteration,
		Iteration: 5,
	}
	updated, _ := m.addDisplayEvent(de)
	model := updated.(Model)

	if model.status != "Done" {
		t.Errorf("expected status unchanged when not running, got %q", model.status)
	}
}

func TestAddDisplayEvent_AssistantTextMergesConsecutive(t *testing.T) {
	m := Model{activeToolIdx: -1, running: true, converter: NewEventConverter()}

	// First assistant text event.
	updated, _ := m.addDisplayEvent(DisplayEvent{
		Type:      DisplayAssistantText,
		Iteration: 1,
		Summary:   "← Assistant (test)",
		Detail:    "hello",
	})
	m = updated.(Model)

	// Second assistant text event for same iteration — should merge.
	updated, _ = m.addDisplayEvent(DisplayEvent{
		Type:      DisplayAssistantText,
		Iteration: 1,
		Summary:   "← Assistant (test)",
		Detail:    "hello world",
	})
	m = updated.(Model)

	if len(m.events) != 1 {
		t.Errorf("expected 1 merged event, got %d", len(m.events))
	}
	if m.events[0].Detail != "hello world" {
		t.Errorf("expected merged Detail=%q, got %q", "hello world", m.events[0].Detail)
	}
}

func TestAddDisplayEvent_AssistantTextNoMergeDifferentIteration(t *testing.T) {
	m := Model{activeToolIdx: -1, running: true, converter: NewEventConverter()}

	updated, _ := m.addDisplayEvent(DisplayEvent{
		Type: DisplayAssistantText, Iteration: 1, Detail: "first",
	})
	m = updated.(Model)

	updated, _ = m.addDisplayEvent(DisplayEvent{
		Type: DisplayAssistantText, Iteration: 2, Detail: "second",
	})
	m = updated.(Model)

	if len(m.events) != 2 {
		t.Errorf("expected 2 events (different iterations), got %d", len(m.events))
	}
}

func TestAddDisplayEvent_AutoScrollFollowsNewEvents(t *testing.T) {
	m := Model{activeToolIdx: -1, running: true, converter: NewEventConverter(), autoScroll: true}

	for i := 0; i < 5; i++ {
		updated, _ := m.addDisplayEvent(DisplayEvent{
			Type: DisplayInfo, Detail: fmt.Sprintf("event %d", i),
		})
		m = updated.(Model)
	}

	if m.cursor != len(m.events)-1 {
		t.Errorf("expected cursor at last event (%d), got %d", len(m.events)-1, m.cursor)
	}
}

func TestAddDisplayEvent_NoAutoScrollWhenDisabled(t *testing.T) {
	m := Model{activeToolIdx: -1, running: true, converter: NewEventConverter(), autoScroll: false}

	updated, _ := m.addDisplayEvent(DisplayEvent{Type: DisplayInfo, Detail: "event"})
	model := updated.(Model)

	// cursor should stay at 0 (default), not jump to the new event
	if model.cursor != 0 {
		t.Errorf("expected cursor at 0 when autoScroll disabled, got %d", model.cursor)
	}
}

func TestBuildBlock_ToolUpdate_ToolDisplayArgs_UsedDirectly(t *testing.T) {
	m := &Model{activeToolIdx: -1}

	// First create a tool start block to update.
	m.buildBlock(DisplayEvent{
		Type:       DisplayToolStart,
		ToolCallID: "tc-upd",
		ToolName:   "bash",
	})

	// Now send an update with ToolDisplayArgs set.
	m.buildBlock(DisplayEvent{
		Type:            DisplayToolUpdate,
		ToolCallID:      "tc-upd",
		ToolName:        "bash",
		RawArgs:         json.RawMessage(`{"command":"make build"}`),
		ToolDisplayArgs: "$ make build",
	})

	if len(m.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(m.blocks))
	}
	if m.blocks[0].ToolArgs != "$ make build" {
		t.Errorf("expected ToolArgs=%q (from ToolDisplayArgs in update), got %q", "$ make build", m.blocks[0].ToolArgs)
	}
}

func TestMainBlockRender_IterationUsesMinimumRuleLength(t *testing.T) {
	got := stripANSI((&MainBlock{Kind: BlockIteration, Iteration: 4}).Render(5))
	want := "── iteration 4 ───"
	if got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestMainBlockRender_AssistantTextHandlesEmptyAndNonEmptyText(t *testing.T) {
	if got := (&MainBlock{Kind: BlockAssistantText}).Render(40); got != "" {
		t.Fatalf("Render() with empty assistant text = %q, want empty string", got)
	}

	renderer = nil
	rendererWidth = 0

	got := stripANSI((&MainBlock{Kind: BlockAssistantText, Text: "assistant paragraph"}).Render(40))
	if !strings.Contains(got, "assistant paragraph") {
		t.Fatalf("Render() = %q, want assistant text content", got)
	}
}

func TestMainBlockRender_Thinking(t *testing.T) {
	got := stripANSI((&MainBlock{Kind: BlockThinking, ThinkingLen: 42}).Render(40))
	want := "  thinking (42 chars)"
	if got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestMainBlockRender_ToolCallRunningShowsStaticDots(t *testing.T) {
	running := stripANSI((&MainBlock{
		Kind:     BlockToolCall,
		ToolName: "bash",
		ToolArgs: "$ make test",
	}).Render(30))

	for _, want := range []string{"bash ...", "$ make test"} {
		if !strings.Contains(running, want) {
			t.Fatalf("running Render() = %q, want substring %q", running, want)
		}
	}
}

func TestMainBlockRender_ToolCallDoneReadSummarizesFileOutput(t *testing.T) {
	got := stripANSI((&MainBlock{
		Kind:       BlockToolCall,
		ToolName:   "Read",
		ToolArgs:   "/tmp/output.txt",
		ToolDone:   true,
		ToolResult: "alpha\nbeta\ngamma",
	}).Render(30))

	for _, want := range []string{"Read ok", "/tmp/output.txt", "(3 lines)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Render() = %q, want substring %q", got, want)
		}
	}
	for _, unwanted := range []string{"alpha", "beta", "gamma"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("Render() = %q, should summarize file output instead of showing %q", got, unwanted)
		}
	}
}

func TestMainBlockRender_ToolCallDoneBashTruncatesResultAndClampsWidths(t *testing.T) {
	got := stripANSI((&MainBlock{
		Kind:       BlockToolCall,
		ToolName:   "bash",
		ToolDone:   true,
		ToolResult: strings.Join([]string{"l1", "l2", "l3", "l4", "l5", "l6", "l7"}, "\n"),
	}).Render(8))

	for _, want := range []string{"bash ok", "l1", "l6", "… (1", "more", "lines)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Render() = %q, want substring %q", got, want)
		}
	}
	if strings.Contains(got, "l7") {
		t.Fatalf("Render() = %q, should omit truncated lines", got)
	}
}

func TestMainBlockRender_ToolCallErrorShowsErrorHeader(t *testing.T) {
	got := stripANSI((&MainBlock{Kind: BlockToolCall, ToolName: "write", ToolError: true}).Render(24))
	if !strings.Contains(got, "write !") {
		t.Fatalf("Render() = %q, want error header", got)
	}
	if strings.Contains(got, "write ok") {
		t.Fatalf("Render() = %q, should not look successful", got)
	}
}

func TestMainBlockRender_InfoAndUnknownKinds(t *testing.T) {
	info := stripANSI((&MainBlock{Kind: BlockInfo, InfoText: "some info"}).Render(20))
	if !strings.Contains(info, "some info") {
		t.Fatalf("Render() = %q, want info text", info)
	}

	if got := (&MainBlock{Kind: BlockKind(99)}).Render(20); got != "" {
		t.Fatalf("Render() with unknown kind = %q, want empty string", got)
	}
}
