package events

import (
	"encoding/json"
	"testing"
)

func TestEventUnmarshalSessionFields(t *testing.T) {
	raw := []byte(`{"type":"session","version":1,"id":"sess-1","timestamp":"2026-03-15T12:00:00Z","cwd":"/tmp/work"}`)

	var ev Event
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if ev.Type != EventSession {
		t.Fatalf("Type = %q, want %q", ev.Type, EventSession)
	}
	if ev.Version != 1 {
		t.Errorf("Version = %d, want 1", ev.Version)
	}
	if ev.ID != "sess-1" {
		t.Errorf("ID = %q, want %q", ev.ID, "sess-1")
	}
	if ev.Timestamp != "2026-03-15T12:00:00Z" {
		t.Errorf("Timestamp = %q, want %q", ev.Timestamp, "2026-03-15T12:00:00Z")
	}
	if ev.CWD != "/tmp/work" {
		t.Errorf("CWD = %q, want %q", ev.CWD, "/tmp/work")
	}
}

func TestEventUnmarshalMessagePayloads(t *testing.T) {
	raw := []byte(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"Hello"},"message":{"role":"assistant","content":[{"type":"text","text":"ignored for update"}]}}`)

	var ev Event
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if ev.Type != EventMessageUpdate {
		t.Fatalf("Type = %q, want %q", ev.Type, EventMessageUpdate)
	}

	var assistant AssistantEvent
	if err := json.Unmarshal(ev.AssistantMessageEvent, &assistant); err != nil {
		t.Fatalf("json.Unmarshal(AssistantMessageEvent) error = %v", err)
	}
	if assistant.Type != "text_delta" {
		t.Errorf("AssistantEvent.Type = %q, want %q", assistant.Type, "text_delta")
	}
	if assistant.ContentIndex != 0 {
		t.Errorf("AssistantEvent.ContentIndex = %d, want 0", assistant.ContentIndex)
	}
	if assistant.Delta != "Hello" {
		t.Errorf("AssistantEvent.Delta = %q, want %q", assistant.Delta, "Hello")
	}

	var msg MessageEnvelope
	if err := json.Unmarshal(ev.Message, &msg); err != nil {
		t.Fatalf("json.Unmarshal(Message) error = %v", err)
	}
	if msg.Role != "assistant" {
		t.Errorf("MessageEnvelope.Role = %q, want %q", msg.Role, "assistant")
	}

	var blocks []ContentBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		t.Fatalf("json.Unmarshal(Content) error = %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if blocks[0].Type != "text" || blocks[0].Text != "ignored for update" {
		t.Errorf("blocks[0] = %+v, want text block with expected content", blocks[0])
	}
}

func TestEventMarshalOmitsEmptyFields(t *testing.T) {
	ev := Event{Type: EventTurnStart}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	if string(data) != `{"type":"turn_start"}` {
		t.Errorf("json.Marshal() = %s, want only the type field", data)
	}
}

func TestEventMarshalKeepsFalseIsError(t *testing.T) {
	isError := false
	ev := Event{
		Type:       EventToolExecutionEnd,
		ToolCallID: "tc-1",
		ToolName:   "bash",
		Args:       json.RawMessage(`{"command":"ls -la"}`),
		Result:     json.RawMessage(`"ok"`),
		IsError:    &isError,
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal(marshaled event) error = %v", err)
	}

	for _, field := range []string{"type", "toolCallId", "toolName", "args", "result", "isError"} {
		if _, ok := got[field]; !ok {
			t.Fatalf("marshaled event missing %q: %s", field, data)
		}
	}
	if _, ok := got["message"]; ok {
		t.Fatalf("marshaled event unexpectedly included empty message field: %s", data)
	}

	var decoded struct {
		Type       EventType `json:"type"`
		ToolCallID string    `json:"toolCallId"`
		ToolName   string    `json:"toolName"`
		IsError    bool      `json:"isError"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(decoded event) error = %v", err)
	}
	if decoded.Type != EventToolExecutionEnd {
		t.Errorf("Type = %q, want %q", decoded.Type, EventToolExecutionEnd)
	}
	if decoded.ToolCallID != "tc-1" {
		t.Errorf("ToolCallID = %q, want %q", decoded.ToolCallID, "tc-1")
	}
	if decoded.ToolName != "bash" {
		t.Errorf("ToolName = %q, want %q", decoded.ToolName, "bash")
	}
	if decoded.IsError {
		t.Errorf("IsError = %v, want false", decoded.IsError)
	}

	var args ToolArgs
	if err := json.Unmarshal(got["args"], &args); err != nil {
		t.Fatalf("json.Unmarshal(args) error = %v", err)
	}
	if args.Command != "ls -la" {
		t.Errorf("args.Command = %q, want %q", args.Command, "ls -la")
	}
}

func TestMessageEnvelopeAndToolArgsRoundTrip(t *testing.T) {
	raw := []byte(`{"role":"assistant","content":[{"type":"text","text":"Hello"}],"model":"claude-4","provider":"anthropic","usage":{"input_tokens":12},"stopReason":"end_turn"}`)

	var msg MessageEnvelope
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if msg.Role != "assistant" {
		t.Errorf("Role = %q, want %q", msg.Role, "assistant")
	}
	if msg.Model != "claude-4" {
		t.Errorf("Model = %q, want %q", msg.Model, "claude-4")
	}
	if msg.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", msg.Provider, "anthropic")
	}
	if msg.StopReason != "end_turn" {
		t.Errorf("StopReason = %q, want %q", msg.StopReason, "end_turn")
	}

	var usage struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal(msg.Usage, &usage); err != nil {
		t.Fatalf("json.Unmarshal(Usage) error = %v", err)
	}
	if usage.InputTokens != 12 {
		t.Errorf("usage.InputTokens = %d, want 12", usage.InputTokens)
	}

	args := ToolArgs{Command: "go test ./..."}
	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("json.Marshal(ToolArgs) error = %v", err)
	}
	if string(data) != `{"command":"go test ./..."}` {
		t.Errorf("json.Marshal(ToolArgs) = %s, want command field", data)
	}
}
