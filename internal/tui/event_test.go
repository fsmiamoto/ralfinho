package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fsmiamoto/ralfinho/internal/runner"
)

// ---------------------------------------------------------------------------
// EventConverter.Convert
// ---------------------------------------------------------------------------

func TestEventConverter_Session(t *testing.T) {
	c := NewEventConverter()
	ev := &runner.Event{
		Type:      runner.EventSession,
		ID:        "sess-abcdef123456789",
		Timestamp: "2026-03-14T10:00:00Z",
		CWD:       "/home/user/project",
	}
	des := c.Convert(ev)
	if len(des) != 1 {
		t.Fatalf("expected 1 DisplayEvent, got %d", len(des))
	}
	de := des[0]
	if de.Type != DisplaySession {
		t.Errorf("type = %q, want %q", de.Type, DisplaySession)
	}
	// ID should be truncated to 12 chars in summary.
	if !strings.Contains(de.Summary, "sess-abcdef1") {
		t.Errorf("summary = %q, want truncated session ID", de.Summary)
	}
	// Detail should contain the full ID.
	if !strings.Contains(de.Detail, "sess-abcdef123456789") {
		t.Errorf("detail = %q, want full session ID", de.Detail)
	}
}

func TestEventConverter_MessageStart_User(t *testing.T) {
	c := NewEventConverter()
	ev := &runner.Event{
		Type:    runner.EventMessageStart,
		Message: json.RawMessage(`{"role":"user","content":[{"type":"text","text":"Hello agent"}]}`),
	}
	des := c.Convert(ev)
	if len(des) != 1 {
		t.Fatalf("expected 1 DisplayEvent, got %d", len(des))
	}
	de := des[0]
	if de.Type != DisplayUserMsg {
		t.Errorf("type = %q, want %q", de.Type, DisplayUserMsg)
	}
	if de.Summary != "> user" {
		t.Errorf("summary = %q, want %q", de.Summary, "> user")
	}
	if !strings.Contains(de.Detail, "Hello agent") {
		t.Errorf("detail = %q, want user content", de.Detail)
	}
}

func TestEventConverter_MessageStart_User_PlainString(t *testing.T) {
	c := NewEventConverter()
	ev := &runner.Event{
		Type:    runner.EventMessageStart,
		Message: json.RawMessage(`{"role":"user","content":"plain text prompt"}`),
	}
	des := c.Convert(ev)
	if len(des) != 1 {
		t.Fatalf("expected 1 DisplayEvent, got %d", len(des))
	}
	if !strings.Contains(des[0].Detail, "plain text prompt") {
		t.Errorf("detail = %q, want plain text content", des[0].Detail)
	}
}

func TestEventConverter_MessageStart_Assistant(t *testing.T) {
	c := NewEventConverter()
	ev := &runner.Event{
		Type:    runner.EventMessageStart,
		Message: json.RawMessage(`{"role":"assistant","model":"claude-4"}`),
	}
	des := c.Convert(ev)
	if len(des) != 1 {
		t.Fatalf("expected 1 DisplayEvent, got %d", len(des))
	}
	de := des[0]
	if de.Type != DisplayAssistantText {
		t.Errorf("type = %q, want %q", de.Type, DisplayAssistantText)
	}
	if !strings.Contains(de.Summary, "claude-4") {
		t.Errorf("summary = %q, want model name", de.Summary)
	}
}

func TestEventConverter_MessageStart_Assistant_NoModel(t *testing.T) {
	c := NewEventConverter()
	ev := &runner.Event{
		Type:    runner.EventMessageStart,
		Message: json.RawMessage(`{"role":"assistant"}`),
	}
	des := c.Convert(ev)
	if len(des) != 1 {
		t.Fatalf("expected 1 DisplayEvent, got %d", len(des))
	}
	if !strings.Contains(des[0].Summary, "unknown") {
		t.Errorf("summary = %q, want 'unknown' when no model", des[0].Summary)
	}
}

func TestEventConverter_TextDelta_AccumulatesText(t *testing.T) {
	c := NewEventConverter()
	// Start an assistant message.
	c.Convert(&runner.Event{
		Type:    runner.EventMessageStart,
		Message: json.RawMessage(`{"role":"assistant","model":"test"}`),
	})
	// Send two text deltas.
	des1 := c.Convert(&runner.Event{
		Type:                  runner.EventMessageUpdate,
		AssistantMessageEvent: json.RawMessage(`{"type":"text_delta","contentIndex":0,"delta":"Hello "}`),
	})
	if len(des1) != 1 || des1[0].Detail != "Hello " {
		t.Fatalf("first delta: expected detail=%q, got %q", "Hello ", des1[0].Detail)
	}

	des2 := c.Convert(&runner.Event{
		Type:                  runner.EventMessageUpdate,
		AssistantMessageEvent: json.RawMessage(`{"type":"text_delta","contentIndex":0,"delta":"world"}`),
	})
	if len(des2) != 1 || des2[0].Detail != "Hello world" {
		t.Fatalf("second delta: expected accumulated detail=%q, got %q", "Hello world", des2[0].Detail)
	}
}

func TestEventConverter_ThinkingLifecycle(t *testing.T) {
	c := NewEventConverter()
	c.Convert(&runner.Event{
		Type:    runner.EventMessageStart,
		Message: json.RawMessage(`{"role":"assistant","model":"test"}`),
	})

	// thinking_start returns nil.
	des := c.Convert(&runner.Event{
		Type:                  runner.EventMessageUpdate,
		AssistantMessageEvent: json.RawMessage(`{"type":"thinking_start"}`),
	})
	if des != nil {
		t.Fatalf("thinking_start should return nil, got %d events", len(des))
	}

	// thinking_delta accumulates silently.
	des = c.Convert(&runner.Event{
		Type:                  runner.EventMessageUpdate,
		AssistantMessageEvent: json.RawMessage(`{"type":"thinking_delta","delta":"considering options..."}`),
	})
	if des != nil {
		t.Fatalf("thinking_delta should return nil, got %d events", len(des))
	}

	// thinking_end produces a DisplayThinking event.
	des = c.Convert(&runner.Event{
		Type:                  runner.EventMessageUpdate,
		AssistantMessageEvent: json.RawMessage(`{"type":"thinking_end"}`),
	})
	if len(des) != 1 {
		t.Fatalf("thinking_end should return 1 event, got %d", len(des))
	}
	if des[0].Type != DisplayThinking {
		t.Errorf("type = %q, want %q", des[0].Type, DisplayThinking)
	}
	if des[0].Detail != "considering options..." {
		t.Errorf("detail = %q, want thinking text", des[0].Detail)
	}
}

func TestEventConverter_ThinkingLong_ShowsCharCount(t *testing.T) {
	c := NewEventConverter()
	c.Convert(&runner.Event{
		Type:    runner.EventMessageStart,
		Message: json.RawMessage(`{"role":"assistant","model":"test"}`),
	})
	c.Convert(&runner.Event{
		Type:                  runner.EventMessageUpdate,
		AssistantMessageEvent: json.RawMessage(`{"type":"thinking_start"}`),
	})
	longText := strings.Repeat("x", 100)
	c.Convert(&runner.Event{
		Type:                  runner.EventMessageUpdate,
		AssistantMessageEvent: json.RawMessage(`{"type":"thinking_delta","delta":"` + longText + `"}`),
	})
	des := c.Convert(&runner.Event{
		Type:                  runner.EventMessageUpdate,
		AssistantMessageEvent: json.RawMessage(`{"type":"thinking_end"}`),
	})
	if len(des) != 1 {
		t.Fatalf("expected 1 event, got %d", len(des))
	}
	if !strings.Contains(des[0].Summary, "100 chars") {
		t.Errorf("summary = %q, want char count for long thinking", des[0].Summary)
	}
}

func TestEventConverter_MessageEnd_FlushesAssistantText(t *testing.T) {
	c := NewEventConverter()
	c.Convert(&runner.Event{
		Type:    runner.EventMessageStart,
		Message: json.RawMessage(`{"role":"assistant","model":"test"}`),
	})
	c.Convert(&runner.Event{
		Type:                  runner.EventMessageUpdate,
		AssistantMessageEvent: json.RawMessage(`{"type":"text_delta","contentIndex":0,"delta":"final text"}`),
	})

	des := c.Convert(&runner.Event{Type: runner.EventMessageEnd})
	if len(des) != 1 {
		t.Fatalf("expected 1 event from message_end, got %d", len(des))
	}
	if des[0].Type != DisplayAssistantText {
		t.Errorf("type = %q, want %q", des[0].Type, DisplayAssistantText)
	}
	if des[0].Detail != "final text" {
		t.Errorf("detail = %q, want %q", des[0].Detail, "final text")
	}
	if !strings.Contains(des[0].Summary, "10 chars") {
		t.Errorf("summary = %q, want char count", des[0].Summary)
	}
}

func TestEventConverter_MessageEnd_NoTextReturnsNil(t *testing.T) {
	c := NewEventConverter()
	c.Convert(&runner.Event{
		Type:    runner.EventMessageStart,
		Message: json.RawMessage(`{"role":"assistant","model":"test"}`),
	})
	// No text deltas, just message_end.
	des := c.Convert(&runner.Event{Type: runner.EventMessageEnd})
	if des != nil {
		t.Fatalf("expected nil for message_end with no text, got %d events", len(des))
	}
}

func TestEventConverter_ToolStart(t *testing.T) {
	c := NewEventConverter()
	ev := &runner.Event{
		Type:       runner.EventToolExecutionStart,
		ToolCallID: "tc-123",
		ToolName:   "bash",
		Args:       json.RawMessage(`{"command":"git status"}`),
	}
	des := c.Convert(ev)
	if len(des) != 1 {
		t.Fatalf("expected 1 event, got %d", len(des))
	}
	de := des[0]
	if de.Type != DisplayToolStart {
		t.Errorf("type = %q, want %q", de.Type, DisplayToolStart)
	}
	if de.ToolCallID != "tc-123" {
		t.Errorf("ToolCallID = %q, want %q", de.ToolCallID, "tc-123")
	}
	if de.ToolName != "bash" {
		t.Errorf("ToolName = %q, want %q", de.ToolName, "bash")
	}
	if !strings.Contains(de.Summary, "bash") {
		t.Errorf("summary = %q, want tool name", de.Summary)
	}
	if !strings.Contains(de.Summary, "git status") {
		t.Errorf("summary = %q, want command in summary", de.Summary)
	}
}

func TestEventConverter_ToolStart_NoArgs(t *testing.T) {
	c := NewEventConverter()
	ev := &runner.Event{
		Type:       runner.EventToolExecutionStart,
		ToolCallID: "tc-456",
		ToolName:   "read",
	}
	des := c.Convert(ev)
	if len(des) != 1 {
		t.Fatalf("expected 1 event, got %d", len(des))
	}
	if !strings.Contains(des[0].Summary, "read") {
		t.Errorf("summary = %q, want tool name", des[0].Summary)
	}
}

func TestEventConverter_ToolUpdate(t *testing.T) {
	c := NewEventConverter()
	ev := &runner.Event{
		Type:            runner.EventToolExecutionUpdate,
		ToolCallID:      "tc-789",
		ToolName:        "edit",
		Args:            json.RawMessage(`{"file_path":"/src/main.go"}`),
		ToolDisplayArgs: "/src/main.go",
	}
	des := c.Convert(ev)
	if len(des) != 1 {
		t.Fatalf("expected 1 event, got %d", len(des))
	}
	de := des[0]
	if de.Type != DisplayToolUpdate {
		t.Errorf("type = %q, want %q", de.Type, DisplayToolUpdate)
	}
	if de.ToolDisplayArgs != "/src/main.go" {
		t.Errorf("ToolDisplayArgs = %q, want %q", de.ToolDisplayArgs, "/src/main.go")
	}
}

func TestEventConverter_ToolEnd_Success(t *testing.T) {
	c := NewEventConverter()
	isErr := false
	ev := &runner.Event{
		Type:       runner.EventToolExecutionEnd,
		ToolCallID: "tc-123",
		ToolName:   "bash",
		Result:     json.RawMessage(`"output text"`),
		IsError:    &isErr,
	}
	des := c.Convert(ev)
	if len(des) != 1 {
		t.Fatalf("expected 1 event, got %d", len(des))
	}
	de := des[0]
	if de.Type != DisplayToolEnd {
		t.Errorf("type = %q, want %q", de.Type, DisplayToolEnd)
	}
	if de.ToolIsError {
		t.Error("ToolIsError should be false")
	}
	if !strings.Contains(de.Summary, "done") {
		t.Errorf("summary = %q, want 'done'", de.Summary)
	}
	if de.ToolResultText != "output text" {
		t.Errorf("ToolResultText = %q, want unquoted result", de.ToolResultText)
	}
}

func TestEventConverter_ToolEnd_Error(t *testing.T) {
	c := NewEventConverter()
	isErr := true
	ev := &runner.Event{
		Type:       runner.EventToolExecutionEnd,
		ToolCallID: "tc-err",
		ToolName:   "bash",
		Result:     json.RawMessage(`"command not found"`),
		IsError:    &isErr,
	}
	des := c.Convert(ev)
	if len(des) != 1 {
		t.Fatalf("expected 1 event, got %d", len(des))
	}
	de := des[0]
	if !de.ToolIsError {
		t.Error("ToolIsError should be true")
	}
	if !strings.Contains(de.Summary, "error") {
		t.Errorf("summary = %q, want 'error'", de.Summary)
	}
}

func TestEventConverter_TurnEnd(t *testing.T) {
	c := NewEventConverter()
	des := c.Convert(&runner.Event{Type: runner.EventTurnEnd})
	if len(des) != 1 {
		t.Fatalf("expected 1 event, got %d", len(des))
	}
	if des[0].Type != DisplayTurnEnd {
		t.Errorf("type = %q, want %q", des[0].Type, DisplayTurnEnd)
	}
}

func TestEventConverter_AgentEnd(t *testing.T) {
	c := NewEventConverter()
	des := c.Convert(&runner.Event{Type: runner.EventAgentEnd})
	if len(des) != 1 {
		t.Fatalf("expected 1 event, got %d", len(des))
	}
	if des[0].Type != DisplayAgentEnd {
		t.Errorf("type = %q, want %q", des[0].Type, DisplayAgentEnd)
	}
}

func TestEventConverter_Iteration(t *testing.T) {
	c := NewEventConverter()
	des := c.Convert(&runner.Event{
		Type: runner.EventIteration,
		ID:   "iteration-3",
	})
	if len(des) != 1 {
		t.Fatalf("expected 1 event, got %d", len(des))
	}
	de := des[0]
	if de.Type != DisplayIteration {
		t.Errorf("type = %q, want %q", de.Type, DisplayIteration)
	}
	if de.Iteration != 3 {
		t.Errorf("Iteration = %d, want 3", de.Iteration)
	}
	if !strings.Contains(de.Summary, "3") {
		t.Errorf("summary = %q, want iteration number", de.Summary)
	}
}

func TestEventConverter_Iteration_SetsConverterState(t *testing.T) {
	c := NewEventConverter()
	c.Convert(&runner.Event{Type: runner.EventIteration, ID: "iteration-5"})

	// Subsequent events should carry iteration=5.
	des := c.Convert(&runner.Event{Type: runner.EventTurnEnd})
	if len(des) != 1 {
		t.Fatalf("expected 1 event, got %d", len(des))
	}
	if des[0].Iteration != 5 {
		t.Errorf("Iteration = %d, want 5 (set by prior iteration event)", des[0].Iteration)
	}
}

func TestEventConverter_UnknownEventReturnsNil(t *testing.T) {
	c := NewEventConverter()
	des := c.Convert(&runner.Event{Type: "unknown_type"})
	if des != nil {
		t.Fatalf("expected nil for unknown event type, got %d events", len(des))
	}
}

func TestEventConverter_MessageUpdate_NilAssistantEvent(t *testing.T) {
	c := NewEventConverter()
	des := c.Convert(&runner.Event{Type: runner.EventMessageUpdate})
	if des != nil {
		t.Fatalf("expected nil for message_update with nil AssistantMessageEvent, got %d events", len(des))
	}
}

func TestEventConverter_MessageUpdate_InvalidJSON(t *testing.T) {
	c := NewEventConverter()
	des := c.Convert(&runner.Event{
		Type:                  runner.EventMessageUpdate,
		AssistantMessageEvent: json.RawMessage(`{invalid`),
	})
	if des != nil {
		t.Fatalf("expected nil for invalid JSON, got %d events", len(des))
	}
}

func TestMakeIterationEvent(t *testing.T) {
	de := MakeIterationEvent(7)
	if de.Type != DisplayIteration {
		t.Errorf("type = %q, want %q", de.Type, DisplayIteration)
	}
	if de.Iteration != 7 {
		t.Errorf("Iteration = %d, want 7", de.Iteration)
	}
}

func TestMakeInfoEvent(t *testing.T) {
	de := MakeInfoEvent("run started")
	if de.Type != DisplayInfo {
		t.Errorf("type = %q, want %q", de.Type, DisplayInfo)
	}
	if de.Summary != "run started" {
		t.Errorf("Summary = %q, want %q", de.Summary, "run started")
	}
	if de.Detail != "run started" {
		t.Errorf("Detail = %q, want %q", de.Detail, "run started")
	}
}

// ---------------------------------------------------------------------------
// truncateStr
// ---------------------------------------------------------------------------

func TestTruncateStr(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{name: "short string unchanged", s: "hello", n: 10, want: "hello"},
		{name: "exact length unchanged", s: "hello", n: 5, want: "hello"},
		{name: "truncated with ellipsis", s: "hello world", n: 8, want: "hello..."},
		{name: "very small n", s: "hello", n: 2, want: "he"},
		{name: "n equals zero", s: "hello", n: 0, want: ""},
		{name: "newlines replaced", s: "hello\nworld", n: 20, want: "hello world"},
		{name: "newlines replaced then truncated", s: "hello\nworld\nfoo", n: 10, want: "hello w..."},
		{name: "multi-byte runes within limit", s: "こんにちは", n: 5, want: "こんにちは"},
		{name: "multi-byte runes truncated", s: "こんにちは世界", n: 5, want: "こん..."},
		{name: "emoji truncated", s: "👋🌍🎉🔥💡✨", n: 4, want: "👋..."},
		{name: "mixed ascii and multi-byte", s: "abc日本語def", n: 6, want: "abc..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateStr(tt.s, tt.n)
			if got != tt.want {
				t.Errorf("truncateStr(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// jsonToText
// ---------------------------------------------------------------------------

func TestJsonToText(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{
			name: "json string is unquoted",
			raw:  json.RawMessage(`"hello world"`),
			want: "hello world",
		},
		{
			name: "json string with escaped newlines",
			raw:  json.RawMessage(`"line1\nline2\nline3"`),
			want: "line1\nline2\nline3",
		},
		{
			name: "json string with escaped quotes",
			raw:  json.RawMessage(`"he said \"hi\""`),
			want: `he said "hi"`,
		},
		{
			name: "json string with unicode escapes",
			raw:  json.RawMessage(`"caf\u00e9"`),
			want: "café",
		},
		{
			name: "json object returned as-is",
			raw:  json.RawMessage(`{"key":"value"}`),
			want: `{"key":"value"}`,
		},
		{
			name: "json array returned as-is",
			raw:  json.RawMessage(`[1,2,3]`),
			want: `[1,2,3]`,
		},
		{
			name: "json number returned as-is",
			raw:  json.RawMessage(`42`),
			want: `42`,
		},
		{
			name: "json null unmarshals to empty string",
			raw:  json.RawMessage(`null`),
			want: "",
		},
		{
			name: "json boolean returned as-is",
			raw:  json.RawMessage(`true`),
			want: `true`,
		},
		{
			name: "empty string is unquoted",
			raw:  json.RawMessage(`""`),
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jsonToText(tt.raw)
			if got != tt.want {
				t.Errorf("jsonToText(%s) = %q, want %q", string(tt.raw), got, tt.want)
			}
		})
	}
}
