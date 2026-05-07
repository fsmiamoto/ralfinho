package agent

import (
	"encoding/json"
	"testing"

	"github.com/fsmiamoto/ralfinho/internal/events"
)

// TestClaudeMapper_ExtractsUsageFromMessageStart verifies that input/cache
// token counts on message_start are surfaced on the corresponding
// EventMessageEnd.
func TestClaudeMapper_ExtractsUsageFromMessageStart(t *testing.T) {
	onEvent, get := collectEvents()
	m := newClaudeEventMapper(onEvent)

	// message_start with usage object.
	m.handleLine("stream_event", []byte(`{
		"type":"stream_event",
		"event":{
			"type":"message_start",
			"message":{
				"role":"assistant",
				"model":"claude-sonnet-4-20250514",
				"usage":{
					"input_tokens":12345,
					"cache_read_input_tokens":7000,
					"cache_creation_input_tokens":150,
					"output_tokens":1
				}
			}
		}
	}`))
	feedClaudeBlockStartText(m)
	feedClaudeTextDelta(m, "hello")
	feedClaudeMessageStop(m)

	evts := get()

	// Find MessageEnd.
	var end *events.Event
	for i := range evts {
		if evts[i].Type == events.EventMessageEnd {
			end = &evts[i]
			break
		}
	}
	if end == nil {
		t.Fatal("expected EventMessageEnd")
	}
	if end.Usage == nil {
		t.Fatal("expected MessageEnd to carry Usage")
	}
	if end.Usage.InputTokens != 12345 {
		t.Errorf("InputTokens = %d, want 12345", end.Usage.InputTokens)
	}
	if end.Usage.CacheReadTokens != 7000 {
		t.Errorf("CacheReadTokens = %d, want 7000", end.Usage.CacheReadTokens)
	}
	if end.Usage.CacheCreationTokens != 150 {
		t.Errorf("CacheCreationTokens = %d, want 150", end.Usage.CacheCreationTokens)
	}
	if end.Usage.OutputTokens != 1 {
		t.Errorf("OutputTokens = %d, want 1 (from message_start initial value)", end.Usage.OutputTokens)
	}
}

// TestClaudeMapper_MergesMessageDeltaUsage verifies that output_tokens
// reported via message_delta replaces the placeholder from message_start.
func TestClaudeMapper_MergesMessageDeltaUsage(t *testing.T) {
	onEvent, get := collectEvents()
	m := newClaudeEventMapper(onEvent)

	m.handleLine("stream_event", []byte(`{
		"type":"stream_event",
		"event":{
			"type":"message_start",
			"message":{
				"role":"assistant",
				"model":"claude-sonnet-4-20250514",
				"usage":{"input_tokens":1000,"output_tokens":1}
			}
		}
	}`))
	feedClaudeBlockStartText(m)
	feedClaudeTextDelta(m, "answering")
	feedClaudeBlockStop(m)

	// message_delta with the final output_tokens count.
	m.handleLine("stream_event", []byte(`{
		"type":"stream_event",
		"event":{
			"type":"message_delta",
			"delta":{"stop_reason":"end_turn"},
			"usage":{"output_tokens":523}
		}
	}`))
	feedClaudeMessageStop(m)

	evts := get()
	var end *events.Event
	for i := range evts {
		if evts[i].Type == events.EventMessageEnd {
			end = &evts[i]
		}
	}
	if end == nil || end.Usage == nil {
		t.Fatal("expected MessageEnd with Usage")
	}
	if end.Usage.InputTokens != 1000 {
		t.Errorf("InputTokens = %d, want 1000 (preserved from message_start)", end.Usage.InputTokens)
	}
	if end.Usage.OutputTokens != 523 {
		t.Errorf("OutputTokens = %d, want 523 (overwritten by message_delta)", end.Usage.OutputTokens)
	}
}

// TestClaudeMapper_NoUsage verifies that messages without usage info still
// produce a clean MessageEnd (Usage == nil) — non-Claude agents and older
// API responses should keep working.
func TestClaudeMapper_NoUsage(t *testing.T) {
	onEvent, get := collectEvents()
	m := newClaudeEventMapper(onEvent)

	feedClaudeMessageStart(m, "claude-sonnet-4-20250514")
	feedClaudeBlockStartText(m)
	feedClaudeTextDelta(m, "no usage")
	feedClaudeMessageStop(m)

	evts := get()
	for _, ev := range evts {
		if ev.Type == events.EventMessageEnd && ev.Usage != nil {
			t.Errorf("expected no Usage on MessageEnd, got %+v", ev.Usage)
		}
	}
}

// TestClaudeMapper_UsageResetsBetweenMessages verifies that the per-message
// usage tracker is cleared so a second message without usage doesn't inherit
// the first message's values.
func TestClaudeMapper_UsageResetsBetweenMessages(t *testing.T) {
	onEvent, get := collectEvents()
	m := newClaudeEventMapper(onEvent)

	// First message — has usage.
	m.handleLine("stream_event", []byte(`{
		"type":"stream_event",
		"event":{
			"type":"message_start",
			"message":{
				"role":"assistant",
				"model":"m",
				"usage":{"input_tokens":50,"output_tokens":10}
			}
		}
	}`))
	feedClaudeBlockStartText(m)
	feedClaudeTextDelta(m, "first")
	feedClaudeMessageStop(m)

	// Second message — no usage.
	feedClaudeMessageStart(m, "m")
	feedClaudeBlockStartText(m)
	feedClaudeTextDelta(m, "second")
	feedClaudeMessageStop(m)

	evts := get()

	var ends []events.Event
	for _, ev := range evts {
		if ev.Type == events.EventMessageEnd {
			ends = append(ends, ev)
		}
	}
	if len(ends) != 2 {
		t.Fatalf("expected 2 MessageEnd events, got %d", len(ends))
	}
	if ends[0].Usage == nil {
		t.Error("first MessageEnd should have Usage")
	}
	if ends[1].Usage != nil {
		t.Errorf("second MessageEnd should have nil Usage, got %+v", ends[1].Usage)
	}
}

// Sanity-check the JSON wire format against an actual Anthropic API line
// shape — defends against accidental drift in the parse structs.
func TestClaudeMapper_RealisticUsageJSON(t *testing.T) {
	raw := []byte(`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_x","type":"message","role":"assistant","model":"claude-sonnet-4-20250514","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":4,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1,"service_tier":"standard"}}}}`)

	var sel claudeStreamEventLine
	if err := json.Unmarshal(raw, &sel); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sel.Event.Message == nil || sel.Event.Message.Usage == nil {
		t.Fatal("expected nested usage struct to be populated")
	}
	got := sel.Event.Message.Usage
	if got.InputTokens == nil || *got.InputTokens != 4 {
		t.Errorf("InputTokens pointer = %v, want *4", got.InputTokens)
	}
	if got.CacheCreationTokens == nil || *got.CacheCreationTokens != 0 {
		t.Errorf("CacheCreationTokens pointer = %v, want *0", got.CacheCreationTokens)
	}
}
