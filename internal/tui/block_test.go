package tui

import (
	"encoding/json"
	"testing"
)

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
	if len(got) > 80 {
		t.Errorf("expected truncation to 80 chars, got %d chars", len(got))
	}
	if got[len(got)-3:] != "..." {
		t.Errorf("expected truncated string to end with ..., got %q", got[len(got)-3:])
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
