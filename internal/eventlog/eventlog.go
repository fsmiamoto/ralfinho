package eventlog

import (
	"bufio"
	"encoding/json"
	"strings"
	"time"
)

type Event struct {
	Timestamp time.Time       `json:"timestamp"`
	Iteration int             `json:"iteration"`
	Type      string          `json:"type"`
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	Raw       json.RawMessage `json:"raw,omitempty"`
}

func ParseOutput(raw string, iteration int, now time.Time) []Event {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	events := make([]Event, 0)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		event := Event{
			Timestamp: now,
			Iteration: iteration,
		}

		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			event.Type = "raw_line"
			event.Content = line
			events = append(events, event)
			continue
		}

		event.Raw = json.RawMessage(line)
		event.Type = firstString(obj, "type", "event", "kind")
		if event.Type == "" {
			event.Type = "json_event"
		}
		event.Role = firstString(obj, "role")
		event.Content = firstString(obj, "content", "text", "message")
		event.ToolName = detectToolName(obj)
		events = append(events, event)
	}

	return events
}

func firstString(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		v, ok := obj[key]
		if !ok {
			continue
		}
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func detectToolName(obj map[string]any) string {
	if tool := firstString(obj, "tool", "tool_name", "toolName", "name"); tool != "" {
		return tool
	}
	if nested, ok := obj["tool"].(map[string]any); ok {
		if name := firstString(nested, "name"); name != "" {
			return name
		}
	}
	return ""
}
