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
// AgentMessageChunk → EventMessageUpdate mapping
// ---------------------------------------------------------------------------

func TestKiroMapper_AgentMessage_EmitsMessageStart(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"kind":"AgentMessageChunk","text":"Hello"}`),
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
		Raw:  json.RawMessage(`{"kind":"AgentMessageChunk","text":"Hello "}`),
	})
	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"kind":"AgentMessageChunk","text":"world"}`),
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
		Raw:  json.RawMessage(`{"kind":"AgentMessageChunk","text":"a"}`),
	})
	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"kind":"AgentMessageChunk","text":"b"}`),
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
		Raw:  json.RawMessage(`{"kind":"AgentMessageChunk","text":""}`),
	})

	if len(get()) != 0 {
		t.Error("expected no events for empty text chunk")
	}
}

// ---------------------------------------------------------------------------
// ToolCall → EventToolExecutionStart/End mapping
// ---------------------------------------------------------------------------

func TestKiroMapper_ToolCall_PendingEmitsStart(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"kind":"ToolCall","toolName":"bash","toolCallId":"tc-1","input":{"command":"ls"},"status":"pending"}`),
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
		Raw:  json.RawMessage(`{"kind":"ToolCall","toolName":"read","toolCallId":"tc-2","output":"file contents","status":"completed"}`),
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
		Raw:  json.RawMessage(`{"kind":"ToolCall","toolName":"bash","toolCallId":"tc-3","output":"command failed","status":"error"}`),
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
		Raw:  json.RawMessage(`{"kind":"AgentMessageChunk","text":"thinking..."}`),
	})

	// Tool call should close the message block first.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"kind":"ToolCall","toolName":"bash","toolCallId":"tc-4","status":"pending"}`),
	})

	evts := get()
	// Expected: MessageStart, MessageUpdate, MessageEnd, ToolExecutionStart
	if len(evts) != 4 {
		t.Fatalf("expected 4 events, got %d", len(evts))
	}
	if evts[2].Type != events.EventMessageEnd {
		t.Errorf("event 2: expected %s, got %s", events.EventMessageEnd, evts[2].Type)
	}
	if evts[3].Type != events.EventToolExecutionStart {
		t.Errorf("event 3: expected %s, got %s", events.EventToolExecutionStart, evts[3].Type)
	}
}

// ---------------------------------------------------------------------------
// ToolCallUpdate → EventToolExecutionUpdate mapping
// ---------------------------------------------------------------------------

func TestKiroMapper_ToolCallUpdate(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCallUpdate,
		Raw:  json.RawMessage(`{"kind":"ToolCallUpdate","toolCallId":"tc-5","toolName":"bash","partialResult":"partial output"}`),
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
// TurnEnd → EventTurnEnd mapping
// ---------------------------------------------------------------------------

func TestKiroMapper_TurnEnd(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.handleUpdate(sessionUpdate{
		Kind: updateKindTurnEnd,
		Raw:  json.RawMessage(`{"kind":"TurnEnd"}`),
	})

	evts := get()
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	if evts[0].Type != events.EventTurnEnd {
		t.Errorf("expected %s, got %s", events.EventTurnEnd, evts[0].Type)
	}
}

func TestKiroMapper_TurnEnd_ClosesMessageBlock(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"kind":"AgentMessageChunk","text":"done"}`),
	})
	m.handleUpdate(sessionUpdate{
		Kind: updateKindTurnEnd,
		Raw:  json.RawMessage(`{"kind":"TurnEnd"}`),
	})

	evts := get()
	// Expected: MessageStart, MessageUpdate, MessageEnd, TurnEnd
	if len(evts) != 4 {
		t.Fatalf("expected 4 events, got %d", len(evts))
	}
	if evts[2].Type != events.EventMessageEnd {
		t.Errorf("event 2: expected %s, got %s", events.EventMessageEnd, evts[2].Type)
	}
	if evts[3].Type != events.EventTurnEnd {
		t.Errorf("event 3: expected %s, got %s", events.EventTurnEnd, evts[3].Type)
	}
}

// ---------------------------------------------------------------------------
// Finalize behavior
// ---------------------------------------------------------------------------

func TestKiroMapper_Finalize_ClosesOpenMessage(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"kind":"AgentMessageChunk","text":"interrupted"}`),
	})
	// No TurnEnd — simulate an error/cancel scenario.
	m.finalize()

	evts := get()
	// Expected: MessageStart, MessageUpdate, MessageEnd (from finalize)
	if len(evts) != 3 {
		t.Fatalf("expected 3 events, got %d", len(evts))
	}
	if evts[2].Type != events.EventMessageEnd {
		t.Errorf("event 2: expected %s (from finalize), got %s", events.EventMessageEnd, evts[2].Type)
	}
}

func TestKiroMapper_Finalize_NoopAfterTurnEnd(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"kind":"AgentMessageChunk","text":"done"}`),
	})
	m.handleUpdate(sessionUpdate{
		Kind: updateKindTurnEnd,
		Raw:  json.RawMessage(`{"kind":"TurnEnd"}`),
	})

	countBefore := len(get())
	m.finalize()
	countAfter := len(get())

	if countAfter != countBefore {
		t.Errorf("finalize should be a no-op after TurnEnd, but emitted %d extra events", countAfter-countBefore)
	}
}

func TestKiroMapper_Finalize_NoopWhenNoEvents(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	m.finalize()

	if len(get()) != 0 {
		t.Error("finalize should emit nothing when no events were processed")
	}
}

// ---------------------------------------------------------------------------
// Full lifecycle: text → tool → text → TurnEnd
// ---------------------------------------------------------------------------

func TestKiroMapper_FullLifecycle(t *testing.T) {
	onEvent, get := collectEvents()
	m := newKiroEventMapper(onEvent)

	// Agent writes some text.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"kind":"AgentMessageChunk","text":"Let me check..."}`),
	})

	// Agent calls a tool.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"kind":"ToolCall","toolName":"bash","toolCallId":"tc-1","input":{"command":"ls"},"status":"running"}`),
	})

	// Tool completes.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"kind":"ToolCall","toolName":"bash","toolCallId":"tc-1","output":"file1.go\nfile2.go","status":"completed"}`),
	})

	// Agent writes more text.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindAgentMessage,
		Raw:  json.RawMessage(`{"kind":"AgentMessageChunk","text":"Found 2 files."}`),
	})

	// Turn ends.
	m.handleUpdate(sessionUpdate{
		Kind: updateKindTurnEnd,
		Raw:  json.RawMessage(`{"kind":"TurnEnd"}`),
	})

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
		Raw:  json.RawMessage(`{"kind":"ToolCall","toolName":"read","toolCallId":"tc-1","status":"pending"}`),
	})
	m.handleUpdate(sessionUpdate{
		Kind: updateKindToolCall,
		Raw:  json.RawMessage(`{"kind":"ToolCall","toolName":"read","toolCallId":"tc-1","output":"contents","status":"completed"}`),
	})
	m.handleUpdate(sessionUpdate{
		Kind: updateKindTurnEnd,
		Raw:  json.RawMessage(`{"kind":"TurnEnd"}`),
	})

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
