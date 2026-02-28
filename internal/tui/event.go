// Package tui implements the Bubble Tea terminal UI for ralfinho.
package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fsmiamoto/ralfinho/internal/runner"
)

// DisplayEvent is a UI-friendly representation of a runner event.
type DisplayEvent struct {
	Type      string // "session", "user_msg", "assistant_text", "thinking", "tool_start", "tool_end", "turn_end", "agent_end", "iteration", "info"
	Summary   string // one-line summary for the stream pane
	Detail    string // full content for the detail pane
	Timestamp time.Time
	Iteration int

	// Tool-specific fields for block rendering in the main view.
	ToolCallID     string          // for matching tool_start with tool_end
	ToolName       string          // tool name (e.g. "bash", "read")
	RawArgs        json.RawMessage // raw tool arguments for formatToolArgs()
	ToolResultText string          // plain result text for tool_end events
	ToolIsError    bool            // true if tool execution had an error
}

// EventConverter accumulates runner events and produces DisplayEvents.
type EventConverter struct {
	iteration     int
	assistantText strings.Builder
	thinkingText  strings.Builder
	currentModel  string
	inAssistant   bool
	inThinking    bool
}

// NewEventConverter creates a new converter.
func NewEventConverter() *EventConverter {
	return &EventConverter{}
}

// SetIteration sets the current iteration number.
func (c *EventConverter) SetIteration(n int) {
	c.iteration = n
}

// Convert transforms a runner.Event into zero or more DisplayEvents.
// It may return nil if the event is accumulated (e.g. text_delta).
func (c *EventConverter) Convert(ev *runner.Event) []DisplayEvent {
	now := time.Now()

	switch ev.Type {
	case runner.EventSession:
		id := ev.ID
		if len(id) > 12 {
			id = id[:12]
		}
		return []DisplayEvent{{
			Type:      "session",
			Summary:   fmt.Sprintf("📡 Session %s", id),
			Detail:    fmt.Sprintf("Session ID: %s\nTimestamp: %s\nCWD: %s", ev.ID, ev.Timestamp, ev.CWD),
			Timestamp: now,
			Iteration: c.iteration,
		}}

	case runner.EventMessageStart:
		var msg runner.MessageEnvelope
		if ev.Message != nil {
			_ = json.Unmarshal(ev.Message, &msg)
		}
		if msg.Role == "user" {
			detail := "User message"
			if msg.Content != nil {
				// Try to extract text content.
				var blocks []runner.ContentBlock
				if err := json.Unmarshal(msg.Content, &blocks); err == nil {
					var parts []string
					for _, b := range blocks {
						if b.Text != "" {
							parts = append(parts, b.Text)
						}
					}
					if len(parts) > 0 {
						detail = strings.Join(parts, "\n")
					}
				} else {
					// Maybe it's a plain string.
					var s string
					if err := json.Unmarshal(msg.Content, &s); err == nil && s != "" {
						detail = s
					}
				}
			}
			return []DisplayEvent{{
				Type:      "user_msg",
				Summary:   "→ User message",
				Detail:    detail,
				Timestamp: now,
				Iteration: c.iteration,
			}}
		} else if msg.Role == "assistant" {
			c.currentModel = msg.Model
			if c.currentModel == "" {
				c.currentModel = "unknown"
			}
			c.assistantText.Reset()
			c.inAssistant = true
			return []DisplayEvent{{
				Type:      "assistant_text",
				Summary:   fmt.Sprintf("← Assistant (%s)", c.currentModel),
				Detail:    "",
				Timestamp: now,
				Iteration: c.iteration,
			}}
		}
		return nil

	case runner.EventMessageUpdate:
		if ev.AssistantMessageEvent == nil {
			return nil
		}
		var ae runner.AssistantEvent
		if err := json.Unmarshal(ev.AssistantMessageEvent, &ae); err != nil {
			return nil
		}
		switch ae.Type {
		case "text_delta":
			c.assistantText.WriteString(ae.Delta)
			text := c.assistantText.String()
			charCount := len(text)
			return []DisplayEvent{{
				Type:      "assistant_text",
				Summary:   fmt.Sprintf("← Assistant (%s) [%d chars]", c.currentModel, charCount),
				Detail:    text,
				Timestamp: now,
				Iteration: c.iteration,
			}}
		case "thinking_delta":
			c.thinkingText.WriteString(ae.Delta)
			c.inThinking = true
			return nil // accumulate silently
		case "thinking_end":
			if c.inThinking {
				c.inThinking = false
				text := c.thinkingText.String()
				c.thinkingText.Reset()
				summary := "💭 Thinking"
				if len(text) > 60 {
					summary = fmt.Sprintf("💭 Thinking (%d chars)", len(text))
				}
				return []DisplayEvent{{
					Type:      "thinking",
					Summary:   summary,
					Detail:    text,
					Timestamp: now,
					Iteration: c.iteration,
				}}
			}
			return nil
		case "thinking_start":
			c.thinkingText.Reset()
			c.inThinking = true
			return nil
		}
		return nil

	case runner.EventMessageEnd:
		if c.inAssistant {
			c.inAssistant = false
			text := c.assistantText.String()
			if text != "" {
				charCount := len(text)
				return []DisplayEvent{{
					Type:      "assistant_text",
					Summary:   fmt.Sprintf("✓ Assistant text (%d chars)", charCount),
					Detail:    text,
					Timestamp: now,
					Iteration: c.iteration,
				}}
			}
		}
		return nil

	case runner.EventToolExecutionStart:
		argsSummary := ""
		if ev.Args != nil {
			var args runner.ToolArgs
			if err := json.Unmarshal(ev.Args, &args); err == nil && args.Command != "" {
				argsSummary = args.Command
			} else {
				argsSummary = truncateStr(string(ev.Args), 80)
			}
		}
		summary := fmt.Sprintf("⚙ %s", ev.ToolName)
		if argsSummary != "" {
			summary = fmt.Sprintf("⚙ %s: %s", ev.ToolName, truncateStr(argsSummary, 60))
		}
		detail := fmt.Sprintf("Tool: %s\nCall ID: %s", ev.ToolName, ev.ToolCallID)
		if argsSummary != "" {
			detail += fmt.Sprintf("\nArgs: %s", argsSummary)
		}
		return []DisplayEvent{{
			Type:       "tool_start",
			Summary:    summary,
			Detail:     detail,
			Timestamp:  now,
			Iteration:  c.iteration,
			ToolCallID: ev.ToolCallID,
			ToolName:   ev.ToolName,
			RawArgs:    ev.Args,
		}}

	case runner.EventToolExecutionEnd:
		isErr := ev.IsError != nil && *ev.IsError
		var summary string
		if isErr {
			summary = fmt.Sprintf("✗ %s error", ev.ToolName)
		} else {
			summary = fmt.Sprintf("✓ %s done", ev.ToolName)
		}
		detail := fmt.Sprintf("Tool: %s\nCall ID: %s\nError: %v", ev.ToolName, ev.ToolCallID, isErr)
		var resultText string
		if ev.Result != nil {
			resultText = string(ev.Result)
			detail += fmt.Sprintf("\nResult:\n%s", resultText)
		}
		return []DisplayEvent{{
			Type:           "tool_end",
			Summary:        summary,
			Detail:         detail,
			Timestamp:      now,
			Iteration:      c.iteration,
			ToolCallID:     ev.ToolCallID,
			ToolName:       ev.ToolName,
			ToolResultText: resultText,
			ToolIsError:    isErr,
		}}

	case runner.EventTurnEnd:
		return []DisplayEvent{{
			Type:      "turn_end",
			Summary:   "── turn end ──",
			Detail:    "Turn completed.",
			Timestamp: now,
			Iteration: c.iteration,
		}}

	case runner.EventAgentEnd:
		return []DisplayEvent{{
			Type:      "agent_end",
			Summary:   "── agent end ──",
			Detail:    "Agent process ended.",
			Timestamp: now,
			Iteration: c.iteration,
		}}

	case runner.EventIteration:
		// Extract iteration number from the ID field ("iteration-N").
		n := 0
		if _, err := fmt.Sscanf(ev.ID, "iteration-%d", &n); err == nil {
			c.iteration = n
		}
		return []DisplayEvent{MakeIterationEvent(c.iteration)}

	default:
		return nil
	}
}

// MakeIterationEvent creates a synthetic iteration boundary event.
func MakeIterationEvent(n int) DisplayEvent {
	return DisplayEvent{
		Type:      "iteration",
		Summary:   fmt.Sprintf("═══ Iteration %d ═══", n),
		Detail:    fmt.Sprintf("Starting iteration %d", n),
		Timestamp: time.Now(),
		Iteration: n,
	}
}

// MakeInfoEvent creates a general info event.
func MakeInfoEvent(text string) DisplayEvent {
	return DisplayEvent{
		Type:      "info",
		Summary:   text,
		Detail:    text,
		Timestamp: time.Now(),
	}
}

func truncateStr(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
