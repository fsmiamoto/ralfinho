package agent

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/fsmiamoto/ralfinho/internal/events"
)

// collectEvents creates an onEvent callback that collects events into a slice.
func collectEvents() (func(events.Event), func() []events.Event) {
	var mu sync.Mutex
	var evts []events.Event
	onEvent := func(ev events.Event) {
		mu.Lock()
		evts = append(evts, ev)
		mu.Unlock()
	}
	get := func() []events.Event {
		mu.Lock()
		defer mu.Unlock()
		result := make([]events.Event, len(evts))
		copy(result, evts)
		return result
	}
	return onEvent, get
}

// ---------------------------------------------------------------------------
// agent_message_chunk → EventMessageUpdate mapping
// ---------------------------------------------------------------------------

func TestKiroMapper_AgentMessage_EmitsMessageStart(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Hello"}}`),
	})

	evts := get()
	if len(evts) != 2 {
		t.Fatalf("expected 2 events (MessageStart + MessageUpdate), got %d", len(evts))
	}

	// First event should be MessageStart with role=assistant.
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
	if msg.Model != "kiro" {
		t.Errorf("expected model=kiro, got %q", msg.Model)
	}

	// Second event should be MessageUpdate with text_delta.
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

func TestKiroMapper_AgentMessage_AccumulatesText(t *testing.T) {
	onEvent, _ := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Hello "}}`),
	})
	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"world"}}`),
	})

	if got := m.assistantText(); got != "Hello world" {
		t.Errorf("expected assistantText=%q, got %q", "Hello world", got)
	}
}

func TestKiroMapper_AgentMessage_NoDoubleMessageStart(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"a"}}`),
	})
	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"b"}}`),
	})

	evts := get()
	// Should be: MessageStart, MessageUpdate("a"), MessageUpdate("b")
	if len(evts) != 3 {
		t.Fatalf("expected 3 events, got %d", len(evts))
	}

	startCount := 0
	for _, ev := range evts {
		if ev.Type == events.EventMessageStart {
			startCount++
		}
	}
	if startCount != 1 {
		t.Errorf("expected 1 MessageStart, got %d", startCount)
	}
}

func TestKiroMapper_AgentMessage_SkipsEmptyText(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":""}}`),
	})

	if len(get()) != 0 {
		t.Error("expected no events for empty text chunk")
	}
}

// ---------------------------------------------------------------------------
// tool_call → EventToolExecutionStart/End mapping
// ---------------------------------------------------------------------------

func TestKiroMapper_ToolCall_InProgressEmitsStart(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"sessionUpdate":"tool_call","title":"bash","toolCallId":"tc-1","rawInput":{"command":"ls"},"status":"in_progress"}`),
	})

	evts := get()
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	if evts[0].Type != events.EventToolExecutionStart {
		t.Errorf("expected %s, got %s", events.EventToolExecutionStart, evts[0].Type)
	}
	if evts[0].ToolName != "bash" {
		t.Errorf("expected toolName=bash, got %q", evts[0].ToolName)
	}
	if evts[0].ToolCallID != "tc-1" {
		t.Errorf("expected toolCallId=tc-1, got %q", evts[0].ToolCallID)
	}
}

func TestKiroMapper_ToolCall_CompletedEmitsEnd(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"sessionUpdate":"tool_call","title":"read","toolCallId":"tc-2","rawOutput":"file contents","status":"completed"}`),
	})

	evts := get()
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	if evts[0].Type != events.EventToolExecutionEnd {
		t.Errorf("expected %s, got %s", events.EventToolExecutionEnd, evts[0].Type)
	}
	if evts[0].IsError == nil || *evts[0].IsError {
		t.Error("expected isError=false for completed status")
	}
}

func TestKiroMapper_ToolCall_ErrorEmitsEndWithError(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"sessionUpdate":"tool_call","title":"bash","toolCallId":"tc-3","rawOutput":"command failed","status":"error"}`),
	})

	evts := get()
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	if evts[0].Type != events.EventToolExecutionEnd {
		t.Errorf("expected %s, got %s", events.EventToolExecutionEnd, evts[0].Type)
	}
	if evts[0].IsError == nil || !*evts[0].IsError {
		t.Error("expected isError=true for error status")
	}
}

func TestKiroMapper_ToolCall_ClosesMessageBlock(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	// Start a message block.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"thinking..."}}`),
	})

	// Tool call without rawInput — message block is closed immediately, but
	// ToolExecutionStart is buffered until args arrive.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"sessionUpdate":"tool_call","title":"bash","toolCallId":"tc-4","status":"in_progress"}`),
	})

	evts := get()
	// Expected: MessageStart, MessageUpdate, MessageEnd (tool start is buffered).
	if len(evts) != 3 {
		t.Fatalf("expected 3 events, got %d", len(evts))
	}
	if evts[2].Type != events.EventMessageEnd {
		t.Errorf("event 2: expected %s, got %s", events.EventMessageEnd, evts[2].Type)
	}

	// Flush the buffered tool by sending completed.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"sessionUpdate":"tool_call","title":"bash","toolCallId":"tc-4","rawOutput":"done","status":"completed"}`),
	})

	evts = get()
	// Expected: MessageStart, MessageUpdate, MessageEnd, ToolExecutionStart, ToolExecutionEnd
	if len(evts) != 5 {
		t.Fatalf("expected 5 events after completed, got %d", len(evts))
	}
	if evts[3].Type != events.EventToolExecutionStart {
		t.Errorf("event 3: expected %s, got %s", events.EventToolExecutionStart, evts[3].Type)
	}
	if evts[4].Type != events.EventToolExecutionEnd {
		t.Errorf("event 4: expected %s, got %s", events.EventToolExecutionEnd, evts[4].Type)
	}
}

// ---------------------------------------------------------------------------
// tool_call_update → EventToolExecutionUpdate mapping
// ---------------------------------------------------------------------------

func TestKiroMapper_ToolCallUpdate(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCallUpdate,
		Raw:  json.RawMessage(`{"sessionUpdate":"tool_call_update","toolCallId":"tc-5","toolName":"bash","partialResult":"partial output"}`),
	})

	evts := get()
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	if evts[0].Type != events.EventToolExecutionUpdate {
		t.Errorf("expected %s, got %s", events.EventToolExecutionUpdate, evts[0].Type)
	}
	if evts[0].ToolCallID != "tc-5" {
		t.Errorf("expected toolCallId=tc-5, got %q", evts[0].ToolCallID)
	}
}

// ---------------------------------------------------------------------------
// tool_call without status → EventToolExecutionUpdate (intermediate args)
// ---------------------------------------------------------------------------

func TestKiroMapper_ToolCall_IntermediateUpdateForwardsArgs(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	// Phase 1: in_progress with no rawInput — buffered, no event emitted yet.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"sessionUpdate":"tool_call","title":"shell","toolCallId":"tc-6","kind":"execute","status":"in_progress"}`),
	})

	// Phase 2: follow-up without status carrying the actual rawInput — merges
	// with buffered data and emits a single complete ToolExecutionStart.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"sessionUpdate":"tool_call","title":"Running: git status","toolCallId":"tc-6","kind":"execute","rawInput":{"command":"git status"}}`),
	})

	evts := get()
	if len(evts) != 1 {
		t.Fatalf("expected 1 event (single ToolExecutionStart), got %d", len(evts))
	}

	// Single event: ToolExecutionStart with the canonical tool name and args.
	if evts[0].Type != events.EventToolExecutionStart {
		t.Errorf("event 0: expected %s, got %s", events.EventToolExecutionStart, evts[0].Type)
	}
	// kind="execute" → canonical name "bash"
	if evts[0].ToolName != "bash" {
		t.Errorf("event 0: expected toolName=bash (from kind=execute), got %q", evts[0].ToolName)
	}
	if evts[0].ToolCallID != "tc-6" {
		t.Errorf("event 0: expected toolCallId=tc-6, got %q", evts[0].ToolCallID)
	}
	if evts[0].Args == nil {
		t.Fatal("event 0: expected non-nil Args")
	}
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(evts[0].Args, &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if args.Command != "git status" {
		t.Errorf("expected command=%q, got %q", "git status", args.Command)
	}

	// Phase 2 title "Running: git status" → ToolDisplayArgs "$ git status".
	if evts[0].ToolDisplayArgs != "$ git status" {
		t.Errorf("expected ToolDisplayArgs=%q, got %q", "$ git status", evts[0].ToolDisplayArgs)
	}
}

// ---------------------------------------------------------------------------
// kiroDisplayArgs — prefix stripping
// ---------------------------------------------------------------------------

func TestKiroDisplayArgs(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		// "Running: <cmd>" → "$ <cmd>"
		{"Running: git status", "$ git status"},
		{"Running: git log --oneline -10", "$ git log --oneline -10"},
		// "Reading <files>" → "<files>"
		{"Reading kiro.go:1", "kiro.go:1"},
		{"Reading kiro.go:1, plan-claude-agent.md:1", "kiro.go:1, plan-claude-agent.md:1"},
		// "Listing <dir>" → "<dir>"
		{"Listing .", "."},
		{"Listing ralfinho", "ralfinho"},
		// "Writing <file>" → "<file>"
		{"Writing file.go", "file.go"},
		// Unrecognized prefix — pass through unchanged.
		{"Generating codebase overview", "Generating codebase overview"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			got := kiroDisplayArgs(tt.title)
			if got != tt.want {
				t.Errorf("kiroDisplayArgs(%q) = %q, want %q", tt.title, got, tt.want)
			}
		})
	}
}

func TestKiroMapper_ToolCall_IntermediateUpdateNoArgsIgnored(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	// Phase 1: in_progress without rawInput — tool is buffered, no event yet.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"sessionUpdate":"tool_call","title":"shell","toolCallId":"tc-7","status":"in_progress"}`),
	})

	// Follow-up without status AND without rawInput — should be ignored;
	// the pending tool remains buffered.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"sessionUpdate":"tool_call","title":"shell","toolCallId":"tc-7"}`),
	})

	// No events emitted yet — tool is still pending.
	if evts := get(); len(evts) != 0 {
		t.Fatalf("expected 0 events (tool still buffered), got %d", len(evts))
	}

	// Finalize flushes the pending tool, then emits TurnEnd.
	m.finalize()
	evts := get()
	if len(evts) != 2 {
		t.Fatalf("expected 2 events after finalize (ToolExecutionStart + TurnEnd), got %d", len(evts))
	}
	if evts[0].Type != events.EventToolExecutionStart {
		t.Errorf("event 0: expected %s, got %s", events.EventToolExecutionStart, evts[0].Type)
	}
	if evts[1].Type != events.EventTurnEnd {
		t.Errorf("event 1: expected %s, got %s", events.EventTurnEnd, evts[1].Type)
	}
}

// ---------------------------------------------------------------------------
// Buffered tool call: flush on completed / finalize
// ---------------------------------------------------------------------------

func TestKiroMapper_ToolCall_BufferedFlushOnCompleted(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	// Phase 1: in_progress without rawInput — buffered.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"sessionUpdate":"tool_call","title":"shell","toolCallId":"tc-8","kind":"execute","status":"in_progress"}`),
	})

	// No events yet.
	if evts := get(); len(evts) != 0 {
		t.Fatalf("expected 0 events after in_progress, got %d", len(evts))
	}

	// completed arrives before the follow-up — pending start is flushed first,
	// then the end event is emitted.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"sessionUpdate":"tool_call","title":"shell","toolCallId":"tc-8","rawOutput":"ok","status":"completed"}`),
	})

	evts := get()
	if len(evts) != 2 {
		t.Fatalf("expected 2 events (ToolExecutionStart + ToolExecutionEnd), got %d", len(evts))
	}
	if evts[0].Type != events.EventToolExecutionStart {
		t.Errorf("event 0: expected %s, got %s", events.EventToolExecutionStart, evts[0].Type)
	}
	// kind="execute" → canonical name "bash"
	if evts[0].ToolName != "bash" {
		t.Errorf("event 0: expected toolName=bash, got %q", evts[0].ToolName)
	}
	if evts[1].Type != events.EventToolExecutionEnd {
		t.Errorf("event 1: expected %s, got %s", events.EventToolExecutionEnd, evts[1].Type)
	}
	if evts[1].IsError == nil || *evts[1].IsError {
		t.Error("event 1: expected isError=false")
	}
}

func TestKiroMapper_ToolCall_BufferedFlushOnFinalize(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	// Phase 1: in_progress without rawInput — buffered.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"sessionUpdate":"tool_call","title":"shell","toolCallId":"tc-9","kind":"execute","status":"in_progress"}`),
	})

	// No events yet.
	if evts := get(); len(evts) != 0 {
		t.Fatalf("expected 0 events after in_progress, got %d", len(evts))
	}

	// finalize flushes the pending tool, then emits TurnEnd.
	m.finalize()

	evts := get()
	if len(evts) != 2 {
		t.Fatalf("expected 2 events (ToolExecutionStart + TurnEnd), got %d", len(evts))
	}
	if evts[0].Type != events.EventToolExecutionStart {
		t.Errorf("event 0: expected %s, got %s", events.EventToolExecutionStart, evts[0].Type)
	}
	if evts[0].ToolName != "bash" {
		t.Errorf("event 0: expected toolName=bash, got %q", evts[0].ToolName)
	}
	if evts[1].Type != events.EventTurnEnd {
		t.Errorf("event 1: expected %s, got %s", events.EventTurnEnd, evts[1].Type)
	}
}

// ---------------------------------------------------------------------------
// Finalize behavior (replaces TurnEnd update handling)
// ---------------------------------------------------------------------------

func TestKiroMapper_Finalize_ClosesOpenMessage(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"interrupted"}}`),
	})
	// Finalize closes the message block and emits TurnEnd.
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

func TestKiroMapper_Finalize_Idempotent(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"done"}}`),
	})

	m.finalize()
	countAfterFirst := len(get())
	m.finalize()
	countAfterSecond := len(get())

	if countAfterSecond != countAfterFirst {
		t.Errorf("second finalize should be a no-op, but emitted %d extra events", countAfterSecond-countAfterFirst)
	}
}

func TestKiroMapper_Finalize_NoopWhenNoEvents(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.finalize()

	// Even with no events, finalize emits TurnEnd.
	evts := get()
	if len(evts) != 1 {
		t.Fatalf("expected 1 event (TurnEnd), got %d", len(evts))
	}
	if evts[0].Type != events.EventTurnEnd {
		t.Errorf("expected %s, got %s", events.EventTurnEnd, evts[0].Type)
	}
}

// ---------------------------------------------------------------------------
// Full lifecycle: text → tool → text → finalize
// ---------------------------------------------------------------------------

func TestKiroMapper_FullLifecycle(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	// Agent writes some text.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Let me check..."}}`),
	})

	// Agent calls a tool.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"sessionUpdate":"tool_call","title":"bash","toolCallId":"tc-1","rawInput":{"command":"ls"},"status":"in_progress"}`),
	})

	// Tool completes.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"sessionUpdate":"tool_call","title":"bash","toolCallId":"tc-1","rawOutput":"file1.go\nfile2.go","status":"completed"}`),
	})

	// Agent writes more text.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Found 2 files."}}`),
	})

	// Turn ends (via finalize, since kiro signals completion via prompt response).
	m.finalize()

	evts := get()

	// Expected sequence:
	// 0: MessageStart(assistant)
	// 1: MessageUpdate(text_delta: "Let me check...")
	// 2: MessageEnd
	// 3: ToolExecutionStart(bash, tc-1)
	// 4: ToolExecutionEnd(bash, tc-1)
	// 5: MessageStart(assistant)
	// 6: MessageUpdate(text_delta: "Found 2 files.")
	// 7: MessageEnd
	// 8: TurnEnd

	wantTypes := []events.EventType{
		events.EventMessageStart,
		events.EventMessageUpdate,
		events.EventMessageEnd,
		events.EventToolExecutionStart,
		events.EventToolExecutionEnd,
		events.EventMessageStart,
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

	// Verify accumulated text.
	if got := m.assistantText(); got != "Let me check...Found 2 files." {
		t.Errorf("assistantText=%q, want %q", got, "Let me check...Found 2 files.")
	}
}

func TestKiroMapper_ToolCallWithoutPrecedingText(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	// Tool call before any text — no MessageStart/End needed.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"sessionUpdate":"tool_call","title":"read","toolCallId":"tc-1","status":"in_progress"}`),
	})
	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"sessionUpdate":"tool_call","title":"read","toolCallId":"tc-1","rawOutput":"contents","status":"completed"}`),
	})

	// Turn ends via finalize.
	m.finalize()

	evts := get()
	// Expected: ToolStart, ToolEnd, TurnEnd (no MessageStart/End).
	wantTypes := []events.EventType{
		events.EventToolExecutionStart,
		events.EventToolExecutionEnd,
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
}
