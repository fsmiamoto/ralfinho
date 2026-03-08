package agent

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsmiamoto/ralfinho/internal/events"
)

// makeScript creates an executable shell script in a temp dir and returns its path.
func makeScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-pi")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body), 0755); err != nil {
		t.Fatal(err)
	}
	return script
}

// tempPromptFiles returns the set of ralfinho-prompt-* files in os.TempDir().
func tempPromptFiles(t *testing.T) map[string]struct{} {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatalf("reading temp dir: %v", err)
	}
	result := make(map[string]struct{})
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "ralfinho-prompt-") {
			result[e.Name()] = struct{}{}
		}
	}
	return result
}

// --- Tests ---

func TestPiAgent_BinaryNotFound(t *testing.T) {
	a := NewPiAgent("/nonexistent/binary/that-does-not-exist-12345")
	onEvent, _ := collectEvents()

	_, err := a.RunIteration(context.Background(), "hello", onEvent)
	if err == nil {
		t.Fatal("expected error when binary does not exist")
	}
	if !strings.Contains(err.Error(), "starting agent") {
		t.Errorf("error should mention 'starting agent', got: %v", err)
	}
}

func TestPiAgent_RunIteration_ParsesJSONL(t *testing.T) {
	jsonlLines := []string{
		`{"type":"message_start","message":{"role":"assistant","model":"test-model"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"Hello "}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"world"}}`,
		`{"type":"message_end"}`,
		`{"type":"turn_end"}`,
	}

	script := makeScript(t, "cat <<'JSONL'\n"+strings.Join(jsonlLines, "\n")+"\nJSONL\n")
	a := NewPiAgent(script)
	onEvent, get := collectEvents()

	text, err := a.RunIteration(context.Background(), "test prompt", onEvent)
	if err != nil {
		t.Fatalf("RunIteration error: %v", err)
	}

	if text != "Hello world" {
		t.Errorf("expected assistant text %q, got %q", "Hello world", text)
	}

	evts := get()
	if len(evts) != 5 {
		t.Fatalf("expected 5 events, got %d", len(evts))
	}

	wantTypes := []events.EventType{
		events.EventMessageStart,
		events.EventMessageUpdate,
		events.EventMessageUpdate,
		events.EventMessageEnd,
		events.EventTurnEnd,
	}
	for i, want := range wantTypes {
		if evts[i].Type != want {
			t.Errorf("event %d: expected type %s, got %s", i, want, evts[i].Type)
		}
	}

	// Verify message_start contains the model info.
	if evts[0].Message == nil {
		t.Error("message_start event should have Message field")
	}
}

func TestPiAgent_RunIteration_SkipsInvalidJSON(t *testing.T) {
	lines := []string{
		"this is not json",
		"also not valid {{{",
		`{"type":"message_start","message":{"role":"assistant","model":"test"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"ok"}}`,
		`{"type":"turn_end"}`,
	}

	script := makeScript(t, "cat <<'JSONL'\n"+strings.Join(lines, "\n")+"\nJSONL\n")
	a := NewPiAgent(script)
	onEvent, get := collectEvents()

	text, err := a.RunIteration(context.Background(), "test", onEvent)
	if err != nil {
		t.Fatalf("RunIteration error: %v", err)
	}

	if text != "ok" {
		t.Errorf("expected assistant text %q, got %q", "ok", text)
	}

	evts := get()
	// Only the 3 valid JSONL lines should be parsed.
	if len(evts) != 3 {
		t.Fatalf("expected 3 events (skipping invalid JSON), got %d", len(evts))
	}

	if evts[0].Type != events.EventMessageStart {
		t.Errorf("first valid event: expected %s, got %s", events.EventMessageStart, evts[0].Type)
	}
}

func TestPiAgent_RunIteration_EmptyOutput(t *testing.T) {
	script := makeScript(t, "# output nothing\n")
	a := NewPiAgent(script)
	onEvent, get := collectEvents()

	text, err := a.RunIteration(context.Background(), "test", onEvent)
	if err != nil {
		t.Fatalf("RunIteration error: %v", err)
	}

	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}

	if len(get()) != 0 {
		t.Errorf("expected 0 events, got %d", len(get()))
	}
}

func TestPiAgent_RunIteration_SkipsBlankLines(t *testing.T) {
	script := makeScript(t, "printf '%s\\n' '' '{\"type\":\"message_start\",\"message\":{\"role\":\"assistant\",\"model\":\"test\"}}' '' '' '{\"type\":\"turn_end\"}' ''\n")
	a := NewPiAgent(script)
	onEvent, get := collectEvents()

	_, err := a.RunIteration(context.Background(), "test", onEvent)
	if err != nil {
		t.Fatalf("RunIteration error: %v", err)
	}

	evts := get()
	if len(evts) != 2 {
		t.Fatalf("expected 2 events (blank lines skipped), got %d", len(evts))
	}
}

func TestPiAgent_RunIteration_ContextCancellation(t *testing.T) {
	// Use exec to replace the shell process with sleep so that SIGKILL
	// from CommandContext actually terminates the sleep and closes stdout.
	script := makeScript(t, "exec sleep 60\n")
	a := NewPiAgent(script)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := a.RunIteration(ctx, "test", func(events.Event) {})
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
	if ctx.Err() == nil {
		t.Error("expected context to be done")
	}
}

func TestPiAgent_RunIteration_RawWriter(t *testing.T) {
	jsonlLines := []string{
		`{"type":"message_start","message":{"role":"assistant","model":"test"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"hi"}}`,
		`{"type":"turn_end"}`,
	}

	script := makeScript(t, "cat <<'JSONL'\n"+strings.Join(jsonlLines, "\n")+"\nJSONL\n")

	var rawBuf bytes.Buffer
	a := NewPiAgent(script, WithRawWriter(&rawBuf))
	onEvent, _ := collectEvents()

	_, err := a.RunIteration(context.Background(), "test", onEvent)
	if err != nil {
		t.Fatalf("RunIteration error: %v", err)
	}

	raw := rawBuf.String()
	if raw == "" {
		t.Fatal("expected raw output to be written, got empty string")
	}

	// Each JSONL line should appear in the raw output.
	for _, line := range jsonlLines {
		if !strings.Contains(raw, line) {
			t.Errorf("raw output missing line: %s", line)
		}
	}
}

func TestPiAgent_RunIteration_TempFileCleanup(t *testing.T) {
	// Record existing temp files before the test to avoid false positives
	// from other tests or prior runs.
	before := tempPromptFiles(t)

	script := makeScript(t, "# do nothing\n")
	a := NewPiAgent(script)
	onEvent, _ := collectEvents()

	_, err := a.RunIteration(context.Background(), "cleanup test prompt", onEvent)
	if err != nil {
		t.Fatalf("RunIteration error: %v", err)
	}

	after := tempPromptFiles(t)

	// Any new ralfinho-prompt-* file appearing after the test is a leak.
	for name := range after {
		if _, existed := before[name]; !existed {
			t.Errorf("temp file not cleaned up: %s", filepath.Join(os.TempDir(), name))
		}
	}
}

func TestPiAgent_RunIteration_ToolEvents(t *testing.T) {
	jsonlLines := []string{
		`{"type":"message_start","message":{"role":"assistant","model":"test"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"running tool"}}`,
		`{"type":"message_end"}`,
		`{"type":"tool_execution_start","toolCallId":"tc-1","toolName":"bash","args":{"command":"ls"}}`,
		`{"type":"tool_execution_end","toolCallId":"tc-1","toolName":"bash","result":"file1\nfile2","isError":false}`,
		`{"type":"turn_end"}`,
	}

	script := makeScript(t, "cat <<'JSONL'\n"+strings.Join(jsonlLines, "\n")+"\nJSONL\n")
	a := NewPiAgent(script)
	onEvent, get := collectEvents()

	text, err := a.RunIteration(context.Background(), "test", onEvent)
	if err != nil {
		t.Fatalf("RunIteration error: %v", err)
	}

	if text != "running tool" {
		t.Errorf("expected text %q, got %q", "running tool", text)
	}

	evts := get()
	if len(evts) != 6 {
		t.Fatalf("expected 6 events, got %d", len(evts))
	}

	// Verify tool events are correctly parsed.
	toolStart := evts[3]
	if toolStart.Type != events.EventToolExecutionStart {
		t.Errorf("event 3: expected %s, got %s", events.EventToolExecutionStart, toolStart.Type)
	}
	if toolStart.ToolName != "bash" {
		t.Errorf("expected toolName=bash, got %q", toolStart.ToolName)
	}
	if toolStart.ToolCallID != "tc-1" {
		t.Errorf("expected toolCallId=tc-1, got %q", toolStart.ToolCallID)
	}

	toolEnd := evts[4]
	if toolEnd.Type != events.EventToolExecutionEnd {
		t.Errorf("event 4: expected %s, got %s", events.EventToolExecutionEnd, toolEnd.Type)
	}
	if toolEnd.IsError == nil || *toolEnd.IsError {
		t.Error("expected isError=false for tool end")
	}
}

func TestPiAgent_RunIteration_NonTextDeltaIgnored(t *testing.T) {
	// An assistantMessageEvent that is NOT text_delta should not contribute
	// to the returned assistant text.
	jsonlLines := []string{
		`{"type":"message_start","message":{"role":"assistant","model":"test"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"thinking","contentIndex":0,"delta":"internal thoughts"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"visible"}}`,
		`{"type":"turn_end"}`,
	}

	script := makeScript(t, "cat <<'JSONL'\n"+strings.Join(jsonlLines, "\n")+"\nJSONL\n")
	a := NewPiAgent(script)
	onEvent, _ := collectEvents()

	text, err := a.RunIteration(context.Background(), "test", onEvent)
	if err != nil {
		t.Fatalf("RunIteration error: %v", err)
	}

	if text != "visible" {
		t.Errorf("expected only text_delta text %q, got %q", "visible", text)
	}
}

func TestPiAgent_RunIteration_LargeOutput(t *testing.T) {
	// Verify the scanner handles large lines (up to the configured buffer).
	largeDelta := strings.Repeat("x", 100_000)
	jsonlLines := []string{
		`{"type":"message_start","message":{"role":"assistant","model":"test"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"` + largeDelta + `"}}`,
		`{"type":"turn_end"}`,
	}

	script := makeScript(t, "cat <<'JSONL'\n"+strings.Join(jsonlLines, "\n")+"\nJSONL\n")
	a := NewPiAgent(script)
	onEvent, _ := collectEvents()

	text, err := a.RunIteration(context.Background(), "test", onEvent)
	if err != nil {
		t.Fatalf("RunIteration error: %v", err)
	}

	if len(text) != 100_000 {
		t.Errorf("expected text length 100000, got %d", len(text))
	}
}
