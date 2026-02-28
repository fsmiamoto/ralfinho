package eventlog

import (
	"testing"
	"time"
)

func TestParseOutput(t *testing.T) {
	now := time.Date(2026, 2, 28, 10, 0, 0, 0, time.UTC)
	raw := "{\"type\":\"assistant\",\"role\":\"assistant\",\"content\":\"hello\"}\nplain text\n{\"event\":\"tool_call\",\"tool\":{\"name\":\"read\"}}\n"

	events := ParseOutput(raw, 2, now)
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	if events[0].Type != "assistant" || events[0].Role != "assistant" || events[0].Content != "hello" {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
	if events[1].Type != "raw_line" || events[1].Content != "plain text" {
		t.Fatalf("unexpected second event: %+v", events[1])
	}
	if events[2].Type != "tool_call" || events[2].ToolName != "read" {
		t.Fatalf("unexpected third event: %+v", events[2])
	}
	for _, ev := range events {
		if !ev.Timestamp.Equal(now) {
			t.Fatalf("expected timestamp %v, got %v", now, ev.Timestamp)
		}
		if ev.Iteration != 2 {
			t.Fatalf("expected iteration 2, got %d", ev.Iteration)
		}
	}
}
