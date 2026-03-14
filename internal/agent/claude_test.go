package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsmiamoto/ralfinho/internal/events"
)

// ---------------------------------------------------------------------------
// Claude test helpers — feed specific line types to the event mapper.
// ---------------------------------------------------------------------------

func feedClaudeMessageStart(m *claudeEventMapper, model string) {
	m.handleLine("stream_event", []byte(fmt.Sprintf(
		`{"type":"stream_event","event":{"type":"message_start","message":{"role":"assistant","model":"%s"}}}`, model)))
}

func feedClaudeBlockStartText(m *claudeEventMapper) {
	m.handleLine("stream_event", []byte(
		`{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"text"}}}`))
}

func feedClaudeBlockStartToolUse(m *claudeEventMapper, name, id string) {
	m.handleLine("stream_event", []byte(fmt.Sprintf(
		`{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"tool_use","name":"%s","id":"%s"}}}`, name, id)))
}

func feedClaudeTextDelta(m *claudeEventMapper, text string) {
	// Use json.Marshal to handle text containing quotes/backslashes.
	delta, _ := json.Marshal(struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{Type: "text_delta", Text: text})
	m.handleLine("stream_event", []byte(fmt.Sprintf(
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":%s}}`, delta)))
}

func feedClaudeInputJSONDelta(m *claudeEventMapper, partialJSON string) {
	// Use json.Marshal to properly escape the partial JSON fragment.
	delta, _ := json.Marshal(struct {
		Type        string `json:"type"`
		PartialJSON string `json:"partial_json"`
	}{Type: "input_json_delta", PartialJSON: partialJSON})
	m.handleLine("stream_event", []byte(fmt.Sprintf(
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":%s}}`, delta)))
}

func feedClaudeBlockStop(m *claudeEventMapper) {
	m.handleLine("stream_event", []byte(
		`{"type":"stream_event","event":{"type":"content_block_stop"}}`))
}

func feedClaudeMessageStop(m *claudeEventMapper) {
	m.handleLine("stream_event", []byte(
		`{"type":"stream_event","event":{"type":"message_stop"}}`))
}

func feedClaudeUserResult(m *claudeEventMapper, toolUseID, content string, isError bool) {
	tr, _ := json.Marshal(struct {
		Type      string `json:"type"`
		ToolUseID string `json:"tool_use_id"`
		Content   string `json:"content"`
		IsError   bool   `json:"is_error"`
	}{Type: "tool_result", ToolUseID: toolUseID, Content: content, IsError: isError})
	m.handleLine("user", []byte(fmt.Sprintf(
		`{"type":"user","message":{"content":[%s]}}`, tr)))
}

func feedClaudeUserResults(m *claudeEventMapper, results ...json.RawMessage) {
	parts := ""
	for i, r := range results {
		if i > 0 {
			parts += ","
		}
		parts += string(r)
	}
	m.handleLine("user", []byte(fmt.Sprintf(
		`{"type":"user","message":{"content":[%s]}}`, parts)))
}

func feedClaudeResult(m *claudeEventMapper) {
	m.handleLine("result", []byte(`{"type":"result"}`))
}

func makeToolResultJSON(toolUseID, content string, isError bool) json.RawMessage {
	b, _ := json.Marshal(struct {
		Type      string `json:"type"`
		ToolUseID string `json:"tool_use_id"`
		Content   string `json:"content"`
		IsError   bool   `json:"is_error"`
	}{Type: "tool_result", ToolUseID: toolUseID, Content: content, IsError: isError})
	return b
}

// ---------------------------------------------------------------------------
// Test 1: text_delta emits MessageStart and MessageUpdate
// ---------------------------------------------------------------------------

func TestClaudeMapper_TextDelta_EmitsMessageStartAndUpdate(t *testing.T) {
	onEvent, get := collectEvents()
	m := newClaudeEventMapper(onEvent)

	feedClaudeMessageStart(m, "claude-sonnet-4-20250514")
	feedClaudeBlockStartText(m)
	feedClaudeTextDelta(m, "Hello")

	evts := get()
	if len(evts) != 2 {
		t.Fatalf("expected 2 events (MessageStart + MessageUpdate), got %d", len(evts))
	}

	// Event 0: MessageStart with model and role.
	if evts[0].Type != events.EventMessageStart {
		t.Errorf("event 0: expected %s, got %s", events.EventMessageStart, evts[0].Type)
	}
	var msg events.MessageEnvelope
	if err := json.Unmarshal(evts[0].Message, &msg); err != nil {
		t.Fatalf("unmarshal MessageStart: %v", err)
	}
	if msg.Role != "assistant" {
		t.Errorf("expected role=assistant, got %q", msg.Role)
	}
	if msg.Model != "claude-sonnet-4-20250514" {
		t.Errorf("expected model=claude-sonnet-4-20250514, got %q", msg.Model)
	}

	// Event 1: MessageUpdate with text_delta.
	if evts[1].Type != events.EventMessageUpdate {
		t.Errorf("event 1: expected %s, got %s", events.EventMessageUpdate, evts[1].Type)
	}
	var ae events.AssistantEvent
	if err := json.Unmarshal(evts[1].AssistantMessageEvent, &ae); err != nil {
		t.Fatalf("unmarshal AssistantEvent: %v", err)
	}
	if ae.Type != "text_delta" {
		t.Errorf("expected type=text_delta, got %q", ae.Type)
	}
	if ae.Delta != "Hello" {
		t.Errorf("expected delta=%q, got %q", "Hello", ae.Delta)
	}
}

// ---------------------------------------------------------------------------
// Test 2: multiple text_deltas accumulate in assistantText()
// ---------------------------------------------------------------------------

func TestClaudeMapper_TextDelta_AccumulatesText(t *testing.T) {
	onEvent, _ := collectEvents()
	m := newClaudeEventMapper(onEvent)

	feedClaudeMessageStart(m, "claude-sonnet-4-20250514")
	feedClaudeBlockStartText(m)
	feedClaudeTextDelta(m, "Hello ")
	feedClaudeTextDelta(m, "world")

	if got := m.assistantText(); got != "Hello world" {
		t.Errorf("expected assistantText=%q, got %q", "Hello world", got)
	}
}

// ---------------------------------------------------------------------------
// Test 3: tool_use emits tool execution events
// ---------------------------------------------------------------------------

func TestClaudeMapper_ToolUse_EmitsToolEvents(t *testing.T) {
	onEvent, get := collectEvents()
	m := newClaudeEventMapper(onEvent)

	// Text block followed by tool_use.
	feedClaudeMessageStart(m, "claude-sonnet-4-20250514")
	feedClaudeBlockStartText(m)
	feedClaudeTextDelta(m, "thinking...")
	feedClaudeBlockStop(m)

	// Tool use block with input.
	feedClaudeBlockStartToolUse(m, "Read", "tu-1")
	feedClaudeInputJSONDelta(m, `{"path":`)
	feedClaudeInputJSONDelta(m, `"go.mod"}`)
	feedClaudeBlockStop(m)
	feedClaudeMessageStop(m)

	// User sends tool result.
	feedClaudeUserResult(m, "tu-1", "module content", false)

	evts := get()

	// Expected sequence:
	// [0] MessageStart
	// [1] MessageUpdate("thinking...")
	// [2] MessageEnd           (tool_use closes message block)
	// [3] ToolExecutionStart   (Read, tu-1)
	// [4] ToolExecutionUpdate  (accumulated args)
	// [5] ToolExecutionEnd     (tu-1, result)
	if len(evts) != 6 {
		t.Fatalf("expected 6 events, got %d", len(evts))
	}

	// Verify the tool-related events.
	if evts[2].Type != events.EventMessageEnd {
		t.Errorf("event 2: expected %s, got %s", events.EventMessageEnd, evts[2].Type)
	}
	if evts[3].Type != events.EventToolExecutionStart {
		t.Errorf("event 3: expected %s, got %s", events.EventToolExecutionStart, evts[3].Type)
	}
	if evts[3].ToolName != "Read" {
		t.Errorf("event 3: expected toolName=Read, got %q", evts[3].ToolName)
	}
	if evts[3].ToolCallID != "tu-1" {
		t.Errorf("event 3: expected toolCallId=tu-1, got %q", evts[3].ToolCallID)
	}
	if evts[5].Type != events.EventToolExecutionEnd {
		t.Errorf("event 5: expected %s, got %s", events.EventToolExecutionEnd, evts[5].Type)
	}
	if evts[5].ToolCallID != "tu-1" {
		t.Errorf("event 5: expected toolCallId=tu-1, got %q", evts[5].ToolCallID)
	}
	if evts[5].ToolName != "Read" {
		t.Errorf("event 5: expected toolName=Read (from registry), got %q", evts[5].ToolName)
	}
}

// ---------------------------------------------------------------------------
// Test 4: tool_use closes the open message block first
// ---------------------------------------------------------------------------

func TestClaudeMapper_ToolUse_ClosesMessageBlock(t *testing.T) {
	onEvent, get := collectEvents()
	m := newClaudeEventMapper(onEvent)

	// Open a message block with text.
	feedClaudeMessageStart(m, "claude-sonnet-4-20250514")
	feedClaudeBlockStartText(m)
	feedClaudeTextDelta(m, "let me look")

	// Tool use block — should close the message first.
	feedClaudeBlockStartToolUse(m, "Bash", "tu-2")

	evts := get()

	// Find the positions of MessageEnd and ToolExecutionStart.
	msgEndIdx := -1
	toolStartIdx := -1
	for i, ev := range evts {
		if ev.Type == events.EventMessageEnd && msgEndIdx == -1 {
			msgEndIdx = i
		}
		if ev.Type == events.EventToolExecutionStart && toolStartIdx == -1 {
			toolStartIdx = i
		}
	}

	if msgEndIdx == -1 {
		t.Fatal("expected EventMessageEnd to be emitted")
	}
	if toolStartIdx == -1 {
		t.Fatal("expected EventToolExecutionStart to be emitted")
	}
	if msgEndIdx >= toolStartIdx {
		t.Errorf("expected MessageEnd (index %d) before ToolExecutionStart (index %d)", msgEndIdx, toolStartIdx)
	}
}

// ---------------------------------------------------------------------------
// Test 5: user event emits ToolExecutionEnd with correct fields
// ---------------------------------------------------------------------------

func TestClaudeMapper_UserEvent_EmitsToolEnd(t *testing.T) {
	onEvent, get := collectEvents()
	m := newClaudeEventMapper(onEvent)

	// Register a tool via content_block_start.
	feedClaudeMessageStart(m, "claude-sonnet-4-20250514")
	feedClaudeBlockStartToolUse(m, "Read", "tu-1")
	feedClaudeBlockStop(m)
	feedClaudeMessageStop(m)

	// Feed user result.
	feedClaudeUserResult(m, "tu-1", "file contents", false)

	evts := get()

	// Find the ToolExecutionEnd event.
	var toolEnd *events.Event
	for i := range evts {
		if evts[i].Type == events.EventToolExecutionEnd {
			toolEnd = &evts[i]
			break
		}
	}

	if toolEnd == nil {
		t.Fatal("expected EventToolExecutionEnd")
	}
	if toolEnd.ToolCallID != "tu-1" {
		t.Errorf("expected toolCallId=tu-1, got %q", toolEnd.ToolCallID)
	}
	if toolEnd.ToolName != "Read" {
		t.Errorf("expected toolName=Read (from registry), got %q", toolEnd.ToolName)
	}
	if toolEnd.IsError == nil || *toolEnd.IsError {
		t.Error("expected isError=false for successful tool result")
	}

	// Verify result content.
	var resultStr string
	if err := json.Unmarshal(toolEnd.Result, &resultStr); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if resultStr != "file contents" {
		t.Errorf("expected result=%q, got %q", "file contents", resultStr)
	}
}

// ---------------------------------------------------------------------------
// Test 6: user event with is_error=true emits ToolExecutionEnd with isError
// ---------------------------------------------------------------------------

func TestClaudeMapper_UserEvent_ErrorResult(t *testing.T) {
	onEvent, get := collectEvents()
	m := newClaudeEventMapper(onEvent)

	// Register tool.
	feedClaudeMessageStart(m, "claude-sonnet-4-20250514")
	feedClaudeBlockStartToolUse(m, "Bash", "tu-err")
	feedClaudeBlockStop(m)
	feedClaudeMessageStop(m)

	// Feed error result.
	feedClaudeUserResult(m, "tu-err", "command not found", true)

	evts := get()

	var toolEnd *events.Event
	for i := range evts {
		if evts[i].Type == events.EventToolExecutionEnd {
			toolEnd = &evts[i]
			break
		}
	}

	if toolEnd == nil {
		t.Fatal("expected EventToolExecutionEnd")
	}
	if toolEnd.IsError == nil || !*toolEnd.IsError {
		t.Error("expected isError=true for error tool result")
	}
	if toolEnd.ToolName != "Bash" {
		t.Errorf("expected toolName=Bash, got %q", toolEnd.ToolName)
	}
}

// ---------------------------------------------------------------------------
// Test 7: user event with multiple tool_result entries
// ---------------------------------------------------------------------------

func TestClaudeMapper_MultipleToolResults(t *testing.T) {
	onEvent, get := collectEvents()
	m := newClaudeEventMapper(onEvent)

	// Register two tools.
	feedClaudeMessageStart(m, "claude-sonnet-4-20250514")
	feedClaudeBlockStartToolUse(m, "Read", "tu-1")
	feedClaudeBlockStop(m)
	feedClaudeBlockStartToolUse(m, "Bash", "tu-2")
	feedClaudeBlockStop(m)
	feedClaudeMessageStop(m)

	// Feed user line with two tool_result entries.
	feedClaudeUserResults(m,
		makeToolResultJSON("tu-1", "file A", false),
		makeToolResultJSON("tu-2", "output B", false),
	)

	evts := get()

	// Count ToolExecutionEnd events.
	var toolEnds []events.Event
	for _, ev := range evts {
		if ev.Type == events.EventToolExecutionEnd {
			toolEnds = append(toolEnds, ev)
		}
	}

	if len(toolEnds) != 2 {
		t.Fatalf("expected 2 ToolExecutionEnd events, got %d", len(toolEnds))
	}

	// Verify first tool result.
	if toolEnds[0].ToolCallID != "tu-1" {
		t.Errorf("toolEnd 0: expected toolCallId=tu-1, got %q", toolEnds[0].ToolCallID)
	}
	if toolEnds[0].ToolName != "Read" {
		t.Errorf("toolEnd 0: expected toolName=Read, got %q", toolEnds[0].ToolName)
	}

	// Verify second tool result.
	if toolEnds[1].ToolCallID != "tu-2" {
		t.Errorf("toolEnd 1: expected toolCallId=tu-2, got %q", toolEnds[1].ToolCallID)
	}
	if toolEnds[1].ToolName != "Bash" {
		t.Errorf("toolEnd 1: expected toolName=Bash, got %q", toolEnds[1].ToolName)
	}
}

// ---------------------------------------------------------------------------
// Test 8: finalize closes open message block
// ---------------------------------------------------------------------------

func TestClaudeMapper_Finalize_ClosesOpenMessage(t *testing.T) {
	onEvent, get := collectEvents()
	m := newClaudeEventMapper(onEvent)

	// Open a message block (no message_stop).
	feedClaudeMessageStart(m, "claude-sonnet-4-20250514")
	feedClaudeBlockStartText(m)
	feedClaudeTextDelta(m, "interrupted")

	m.finalize()

	evts := get()

	// Expected: MessageStart, MessageUpdate, MessageEnd, TurnEnd
	if len(evts) != 4 {
		t.Fatalf("expected 4 events, got %d", len(evts))
	}
	if evts[2].Type != events.EventMessageEnd {
		t.Errorf("event 2: expected %s (from finalize), got %s", events.EventMessageEnd, evts[2].Type)
	}
	if evts[3].Type != events.EventTurnEnd {
		t.Errorf("event 3: expected %s (from finalize), got %s", events.EventTurnEnd, evts[3].Type)
	}
}

// ---------------------------------------------------------------------------
// Test 9: finalize is idempotent
// ---------------------------------------------------------------------------

func TestClaudeMapper_Finalize_Idempotent(t *testing.T) {
	onEvent, get := collectEvents()
	m := newClaudeEventMapper(onEvent)

	feedClaudeMessageStart(m, "claude-sonnet-4-20250514")
	feedClaudeBlockStartText(m)
	feedClaudeTextDelta(m, "done")

	m.finalize()
	countAfterFirst := len(get())

	m.finalize()
	countAfterSecond := len(get())

	if countAfterSecond != countAfterFirst {
		t.Errorf("second finalize should be a no-op, but emitted %d extra events",
			countAfterSecond-countAfterFirst)
	}
}

// ---------------------------------------------------------------------------
// Test 10: finalize with no prior events emits just TurnEnd
// ---------------------------------------------------------------------------

func TestClaudeMapper_Finalize_NoopEmitsTurnEnd(t *testing.T) {
	onEvent, get := collectEvents()
	m := newClaudeEventMapper(onEvent)

	m.finalize()

	evts := get()
	if len(evts) != 1 {
		t.Fatalf("expected 1 event (TurnEnd), got %d", len(evts))
	}
	if evts[0].Type != events.EventTurnEnd {
		t.Errorf("expected %s, got %s", events.EventTurnEnd, evts[0].Type)
	}
}

// ---------------------------------------------------------------------------
// Test 11: full lifecycle — text → tool → text → result
// ---------------------------------------------------------------------------

func TestClaudeMapper_FullLifecycle(t *testing.T) {
	onEvent, get := collectEvents()
	m := newClaudeEventMapper(onEvent)

	// --- First assistant message with text ---
	feedClaudeMessageStart(m, "claude-sonnet-4-20250514")
	feedClaudeBlockStartText(m)
	feedClaudeTextDelta(m, "Let me ")
	feedClaudeTextDelta(m, "check...")
	feedClaudeBlockStop(m)

	// --- Tool use block ---
	feedClaudeBlockStartToolUse(m, "Read", "tu-1")
	feedClaudeInputJSONDelta(m, `{"path":`)
	feedClaudeInputJSONDelta(m, `"go.mod"}`)
	feedClaudeBlockStop(m)
	feedClaudeMessageStop(m)

	// --- Tool result ---
	feedClaudeUserResult(m, "tu-1", "module github.com/test", false)

	// --- Second assistant message with text ---
	feedClaudeMessageStart(m, "claude-sonnet-4-20250514")
	feedClaudeBlockStartText(m)
	feedClaudeTextDelta(m, "The module ")
	feedClaudeTextDelta(m, "is test")
	feedClaudeBlockStop(m)
	feedClaudeMessageStop(m)

	// --- Result ---
	feedClaudeResult(m)

	evts := get()

	// Expected sequence:
	//  [0] MessageStart
	//  [1] MessageUpdate ("Let me ")
	//  [2] MessageUpdate ("check...")
	//  [3] MessageEnd
	//  [4] ToolExecutionStart (Read, tu-1)
	//  [5] ToolExecutionUpdate (accumulated args)
	//  [6] ToolExecutionEnd (tu-1)
	//  [7] MessageStart
	//  [8] MessageUpdate ("The module ")
	//  [9] MessageUpdate ("is test")
	// [10] MessageEnd
	// [11] TurnEnd
	wantTypes := []events.EventType{
		events.EventMessageStart,
		events.EventMessageUpdate,
		events.EventMessageUpdate,
		events.EventMessageEnd,
		events.EventToolExecutionStart,
		events.EventToolExecutionUpdate,
		events.EventToolExecutionEnd,
		events.EventMessageStart,
		events.EventMessageUpdate,
		events.EventMessageUpdate,
		events.EventMessageEnd,
		events.EventTurnEnd,
	}

	if len(evts) != len(wantTypes) {
		t.Fatalf("expected %d events, got %d", len(wantTypes), len(evts))
	}
	for i, want := range wantTypes {
		if evts[i].Type != want {
			t.Errorf("event %d: expected %s, got %s", i, want, evts[i].Type)
		}
	}

	// Verify accumulated text spans both messages.
	wantText := "Let me check...The module is test"
	if got := m.assistantText(); got != wantText {
		t.Errorf("assistantText=%q, want %q", got, wantText)
	}

	// Verify ToolExecutionUpdate carries parsed args.
	if evts[5].Args == nil {
		t.Fatal("event 5 (ToolExecutionUpdate): expected non-nil Args")
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(evts[5].Args, &args); err != nil {
		t.Fatalf("unmarshal tool args: %v", err)
	}
	if args.Path != "go.mod" {
		t.Errorf("expected args.path=%q, got %q", "go.mod", args.Path)
	}
}

// ---------------------------------------------------------------------------
// Test 12: tool_use as first content block (no text before it)
// ---------------------------------------------------------------------------

func TestClaudeMapper_ToolWithoutPrecedingText(t *testing.T) {
	onEvent, get := collectEvents()
	m := newClaudeEventMapper(onEvent)

	// message_start followed immediately by a tool_use block (no text).
	feedClaudeMessageStart(m, "claude-sonnet-4-20250514")
	feedClaudeBlockStartToolUse(m, "Read", "tu-1")
	feedClaudeBlockStop(m)
	feedClaudeMessageStop(m)

	// Tool result.
	feedClaudeUserResult(m, "tu-1", "contents", false)

	m.finalize()

	evts := get()

	// message_start emits MessageStart, then tool_use closes it with
	// MessageEnd before emitting ToolExecutionStart. This is expected —
	// the MessageStart/MessageEnd pair happens BEFORE the tool events.
	// Verify no MessageStart/MessageEnd appears BETWEEN tool events.
	toolStartIdx := -1
	toolEndIdx := -1
	for i, ev := range evts {
		if ev.Type == events.EventToolExecutionStart && toolStartIdx == -1 {
			toolStartIdx = i
		}
		if ev.Type == events.EventToolExecutionEnd {
			toolEndIdx = i
		}
	}

	if toolStartIdx == -1 || toolEndIdx == -1 {
		t.Fatal("expected both ToolExecutionStart and ToolExecutionEnd")
	}

	// No MessageStart or MessageEnd between tool events.
	for i := toolStartIdx; i <= toolEndIdx; i++ {
		if evts[i].Type == events.EventMessageStart || evts[i].Type == events.EventMessageEnd {
			t.Errorf("event %d: unexpected %s between tool events", i, evts[i].Type)
		}
	}

	// Verify ToolExecutionStart has correct fields.
	if evts[toolStartIdx].ToolName != "Read" {
		t.Errorf("expected toolName=Read, got %q", evts[toolStartIdx].ToolName)
	}
	if evts[toolStartIdx].ToolCallID != "tu-1" {
		t.Errorf("expected toolCallId=tu-1, got %q", evts[toolStartIdx].ToolCallID)
	}

	// Verify TurnEnd is the last event.
	last := evts[len(evts)-1]
	if last.Type != events.EventTurnEnd {
		t.Errorf("expected last event to be %s, got %s", events.EventTurnEnd, last.Type)
	}
}

// ---------------------------------------------------------------------------
// Test 13: result line emits TurnEnd
// ---------------------------------------------------------------------------

func TestClaudeMapper_ResultEmitsTurnEnd(t *testing.T) {
	onEvent, get := collectEvents()
	m := newClaudeEventMapper(onEvent)

	// Simple message then result.
	feedClaudeMessageStart(m, "claude-sonnet-4-20250514")
	feedClaudeBlockStartText(m)
	feedClaudeTextDelta(m, "done")
	feedClaudeMessageStop(m)

	feedClaudeResult(m)

	evts := get()

	// Last event should be TurnEnd.
	if len(evts) == 0 {
		t.Fatal("expected at least one event")
	}
	last := evts[len(evts)-1]
	if last.Type != events.EventTurnEnd {
		t.Errorf("expected last event to be %s, got %s", events.EventTurnEnd, last.Type)
	}

	// TurnEnd should appear exactly once.
	turnEndCount := 0
	for _, ev := range evts {
		if ev.Type == events.EventTurnEnd {
			turnEndCount++
		}
	}
	if turnEndCount != 1 {
		t.Errorf("expected 1 TurnEnd, got %d", turnEndCount)
	}
}

// ---------------------------------------------------------------------------
// Test 14: assistant and rate_limit_event lines are skipped
// ---------------------------------------------------------------------------

func TestClaudeMapper_SkipsAssistantAndRateLimitLines(t *testing.T) {
	onEvent, get := collectEvents()
	m := newClaudeEventMapper(onEvent)

	// Feed lines that should be ignored.
	m.handleLine("assistant", []byte(
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello"}]}}`))
	m.handleLine("rate_limit_event", []byte(
		`{"type":"rate_limit_event","rate_limit":{"requests_remaining":100}}`))
	m.handleLine("system", []byte(
		`{"type":"system","session_id":"sess-1","model":"claude-sonnet-4-20250514"}`))

	evts := get()
	if len(evts) != 0 {
		t.Errorf("expected 0 events for skipped line types, got %d", len(evts))
	}
}

// ===========================================================================
// Integration tests: ClaudeAgent.RunIteration with fake scripts
// ===========================================================================

// claudeTestScript creates a shell script that outputs the given lines to
// stdout. Lines are written to a temp file to avoid heredoc escaping issues
// with JSON that contains backslash-escaped characters (e.g. input_json_delta).
func claudeTestScript(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	outFile := filepath.Join(dir, "claude-output.jsonl")
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(outFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return makeScript(t, "cat '"+outFile+"'")
}

// realisticClaudeOutput returns stream-json lines simulating a Claude Code
// session: system init, text message, tool use, tool result, second text
// message, and result.
func realisticClaudeOutput(t *testing.T) []string {
	t.Helper()

	inputDelta1, _ := json.Marshal(struct {
		Type        string `json:"type"`
		PartialJSON string `json:"partial_json"`
	}{Type: "input_json_delta", PartialJSON: `{"command":`})

	inputDelta2, _ := json.Marshal(struct {
		Type        string `json:"type"`
		PartialJSON string `json:"partial_json"`
	}{Type: "input_json_delta", PartialJSON: `"ls"}`})

	toolResult, _ := json.Marshal(struct {
		Type      string `json:"type"`
		ToolUseID string `json:"tool_use_id"`
		Content   string `json:"content"`
		IsError   bool   `json:"is_error"`
	}{Type: "tool_result", ToolUseID: "tu-1", Content: "file1\nfile2", IsError: false})

	return []string{
		// System init (skipped by mapper).
		`{"type":"system","session_id":"sess-1","model":"claude-sonnet-4-20250514"}`,
		// First assistant message with text.
		`{"type":"stream_event","event":{"type":"message_start","message":{"role":"assistant","model":"claude-sonnet-4-20250514"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"text"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Let me "}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"run that."}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop"}}`,
		// Tool use block.
		`{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"tool_use","name":"Bash","id":"tu-1"}}}`,
		fmt.Sprintf(`{"type":"stream_event","event":{"type":"content_block_delta","delta":%s}}`, inputDelta1),
		fmt.Sprintf(`{"type":"stream_event","event":{"type":"content_block_delta","delta":%s}}`, inputDelta2),
		`{"type":"stream_event","event":{"type":"content_block_stop"}}`,
		// message_stop.
		`{"type":"stream_event","event":{"type":"message_stop"}}`,
		// Assistant completed message (should be skipped).
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Let me run that."}]}}`,
		// User tool_result.
		fmt.Sprintf(`{"type":"user","message":{"content":[%s]}}`, toolResult),
		// Rate limit event (should be skipped).
		`{"type":"rate_limit_event","rate_limit":{"requests_remaining":100}}`,
		// Second assistant message.
		`{"type":"stream_event","event":{"type":"message_start","message":{"role":"assistant","model":"claude-sonnet-4-20250514"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"text"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Done."}}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop"}}`,
		`{"type":"stream_event","event":{"type":"message_stop"}}`,
		// Result.
		`{"type":"result","cost":0.001,"duration_ms":1500,"is_error":false,"stop_reason":"end_turn"}`,
	}
}

// ---------------------------------------------------------------------------
// Integration test: full stream-json parsing
// ---------------------------------------------------------------------------

func TestClaudeAgent_RunIteration_ParsesStreamJSON(t *testing.T) {
	lines := realisticClaudeOutput(t)
	script := claudeTestScript(t, lines)

	a := NewClaudeAgent()
	a.binary = script

	onEvent, get := collectEvents()

	text, err := a.RunIteration(context.Background(), "test prompt", onEvent)
	if err != nil {
		t.Fatalf("RunIteration error: %v", err)
	}

	// Verify accumulated text from both messages.
	wantText := "Let me run that.Done."
	if text != wantText {
		t.Errorf("expected text %q, got %q", wantText, text)
	}

	// Verify events in correct order and types.
	evts := get()
	wantTypes := []events.EventType{
		events.EventMessageStart,        // first message
		events.EventMessageUpdate,       // "Let me "
		events.EventMessageUpdate,       // "run that."
		events.EventMessageEnd,          // tool_use closes message
		events.EventToolExecutionStart,  // Bash, tu-1
		events.EventToolExecutionUpdate, // accumulated args
		events.EventToolExecutionEnd,    // tool result
		events.EventMessageStart,        // second message
		events.EventMessageUpdate,       // "Done."
		events.EventMessageEnd,          // message_stop
		events.EventTurnEnd,             // result
	}

	if len(evts) != len(wantTypes) {
		var got []string
		for _, ev := range evts {
			got = append(got, string(ev.Type))
		}
		t.Fatalf("expected %d events, got %d: %v", len(wantTypes), len(evts), got)
	}
	for i, want := range wantTypes {
		if evts[i].Type != want {
			t.Errorf("event %d: expected %s, got %s", i, want, evts[i].Type)
		}
	}

	// Verify MessageStart has model and role.
	var msg events.MessageEnvelope
	if err := json.Unmarshal(evts[0].Message, &msg); err != nil {
		t.Fatalf("unmarshal MessageStart: %v", err)
	}
	if msg.Model != "claude-sonnet-4-20250514" {
		t.Errorf("expected model=claude-sonnet-4-20250514, got %q", msg.Model)
	}
	if msg.Role != "assistant" {
		t.Errorf("expected role=assistant, got %q", msg.Role)
	}

	// Verify ToolExecutionStart fields.
	if evts[4].ToolName != "Bash" {
		t.Errorf("expected toolName=Bash, got %q", evts[4].ToolName)
	}
	if evts[4].ToolCallID != "tu-1" {
		t.Errorf("expected toolCallId=tu-1, got %q", evts[4].ToolCallID)
	}

	// Verify ToolExecutionUpdate has parsed args.
	if evts[5].Args == nil {
		t.Fatal("expected ToolExecutionUpdate to have non-nil Args")
	}
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(evts[5].Args, &args); err != nil {
		t.Fatalf("unmarshal tool args: %v", err)
	}
	if args.Command != "ls" {
		t.Errorf("expected args.command=%q, got %q", "ls", args.Command)
	}

	// Verify ToolExecutionEnd fields.
	if evts[6].ToolCallID != "tu-1" {
		t.Errorf("expected toolCallId=tu-1, got %q", evts[6].ToolCallID)
	}
	if evts[6].ToolName != "Bash" {
		t.Errorf("expected toolName=Bash (from registry), got %q", evts[6].ToolName)
	}
	if evts[6].IsError == nil || *evts[6].IsError {
		t.Error("expected isError=false for tool result")
	}
}

// ---------------------------------------------------------------------------
// Integration test: raw output capture via WithRawWriter
// ---------------------------------------------------------------------------

func TestClaudeAgent_RunIteration_RawWriter(t *testing.T) {
	lines := realisticClaudeOutput(t)
	script := claudeTestScript(t, lines)

	var rawBuf bytes.Buffer
	a := NewClaudeAgent(WithRawWriter(&rawBuf))
	a.binary = script

	onEvent, _ := collectEvents()

	_, err := a.RunIteration(context.Background(), "test prompt", onEvent)
	if err != nil {
		t.Fatalf("RunIteration error: %v", err)
	}

	raw := rawBuf.String()
	if raw == "" {
		t.Fatal("expected raw output to be written, got empty string")
	}

	// Each stream-json line should appear in the raw output.
	for _, line := range lines {
		if !strings.Contains(raw, line) {
			t.Errorf("raw output missing line: %s", line)
		}
	}
}

// ---------------------------------------------------------------------------
// Integration test: context cancellation returns ctx.Err()
// ---------------------------------------------------------------------------

func TestClaudeAgent_RunIteration_ContextCancellation(t *testing.T) {
	// Use exec to replace the shell process with sleep so that SIGKILL
	// from CommandContext actually terminates the sleep and closes stdout.
	script := makeScript(t, "exec sleep 60\n")
	a := NewClaudeAgent()
	a.binary = script

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

// ---------------------------------------------------------------------------
// Integration test: binary not found returns clear error
// ---------------------------------------------------------------------------

func TestClaudeAgent_RunIteration_BinaryNotFound(t *testing.T) {
	a := NewClaudeAgent()
	a.binary = "/nonexistent/binary/that-does-not-exist-12345"

	_, err := a.RunIteration(context.Background(), "hello", func(events.Event) {})
	if err == nil {
		t.Fatal("expected error when binary does not exist")
	}
	if !strings.Contains(err.Error(), "starting agent") {
		t.Errorf("error should mention 'starting agent', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Integration test: empty output — finalize still emits TurnEnd
// ---------------------------------------------------------------------------

func TestClaudeAgent_RunIteration_EmptyOutput(t *testing.T) {
	script := makeScript(t, "# output nothing\n")
	a := NewClaudeAgent()
	a.binary = script

	onEvent, get := collectEvents()

	text, err := a.RunIteration(context.Background(), "test", onEvent)
	if err != nil {
		t.Fatalf("RunIteration error: %v", err)
	}

	if text != "" {
		t.Errorf("expected empty text, got %q", text)
	}

	// finalize() emits TurnEnd even with no stream input.
	evts := get()
	if len(evts) != 1 {
		t.Fatalf("expected 1 event (TurnEnd from finalize), got %d", len(evts))
	}
	if evts[0].Type != events.EventTurnEnd {
		t.Errorf("expected %s, got %s", events.EventTurnEnd, evts[0].Type)
	}
}

// ---------------------------------------------------------------------------
// Integration test: invalid JSON lines are skipped gracefully
// ---------------------------------------------------------------------------

func TestClaudeAgent_RunIteration_SkipsInvalidJSON(t *testing.T) {
	lines := []string{
		"this is not json",
		"also not valid {{{",
		`{"type":"stream_event","event":{"type":"message_start","message":{"role":"assistant","model":"test"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"text"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"ok"}}}`,
		`{"type":"result","stop_reason":"end_turn"}`,
	}

	script := claudeTestScript(t, lines)
	a := NewClaudeAgent()
	a.binary = script

	onEvent, get := collectEvents()

	text, err := a.RunIteration(context.Background(), "test", onEvent)
	if err != nil {
		t.Fatalf("RunIteration error: %v", err)
	}

	if text != "ok" {
		t.Errorf("expected assistant text %q, got %q", "ok", text)
	}

	evts := get()
	// Only valid stream-json lines should produce events.
	// Expected: MessageStart, MessageUpdate, MessageEnd (from result), TurnEnd.
	if len(evts) != 4 {
		var got []string
		for _, ev := range evts {
			got = append(got, string(ev.Type))
		}
		t.Fatalf("expected 4 events (skipping invalid JSON), got %d: %v", len(evts), got)
	}

	if evts[0].Type != events.EventMessageStart {
		t.Errorf("first valid event: expected %s, got %s", events.EventMessageStart, evts[0].Type)
	}
}
