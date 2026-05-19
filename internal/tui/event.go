// Package tui implements the Bubble Tea terminal UI for ralfinho.
package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fsmiamoto/ralfinho/internal/runner"
)

// DisplayEventType identifies the kind of display event.
type DisplayEventType = string

const (
	DisplaySession       DisplayEventType = "session"
	DisplayUserMsg       DisplayEventType = "user_msg"
	DisplayAssistantText DisplayEventType = "assistant_text"
	DisplayThinking      DisplayEventType = "thinking"
	DisplayToolStart     DisplayEventType = "tool_start"
	DisplayToolUpdate    DisplayEventType = "tool_update"
	DisplayToolEnd       DisplayEventType = "tool_end"
	DisplayTurnEnd       DisplayEventType = "turn_end"
	DisplayAgentEnd      DisplayEventType = "agent_end"
	DisplayIteration     DisplayEventType = "iteration"
	DisplayInfo          DisplayEventType = "info"
	DisplayRestart       DisplayEventType = "restart"
	DisplayReminderState DisplayEventType = "reminder_state"
	DisplayUsage         DisplayEventType = "usage"
)

// DisplayEvent is a UI-friendly representation of a runner event.
type DisplayEvent struct {
	Type      DisplayEventType
	Summary   string // one-line summary for the stream pane
	Detail    string // full content for the detail pane
	Timestamp time.Time
	// RawTimestamp preserves the original runner event timestamp string for
	// raw/detail views, especially when it is malformed and cannot be parsed.
	RawTimestamp string
	Iteration    int

	// Display-only lifecycle timing metadata. StartTime is the original
	// assistant/tool/iteration start timestamp. EndTime and Duration are set for
	// completed assistant/tool blocks when both lifecycle timestamps are valid.
	StartTime time.Time
	EndTime   time.Time
	Duration  time.Duration

	// AssistantFinal is true when this assistant text event represents
	// the completed message (i.e. produced by EventMessageEnd), false
	// while still streaming.
	AssistantFinal bool
	// AssistantModel is the structured model label for assistant block metadata.
	AssistantModel string

	// Tool-specific fields for block rendering in the main view.
	ToolCallID      string          // for matching tool_start with tool_end
	ToolName        string          // tool name (e.g. "bash", "read")
	RawArgs         json.RawMessage // raw tool arguments for formatToolArgs()
	ToolDisplayArgs string          // pre-formatted display string; when set, preferred over formatToolArgs()
	ToolResultText  string          // plain result text for tool_end events
	ToolIsError     bool            // true if tool execution had an error

	// RestartIter is the iteration that was restarted; populated only on
	// DisplayRestart events. Used by the model to bump its restart counter.
	RestartIter int

	// Reminders is the current reminder snapshot; populated only on
	// DisplayReminderState events. The TUI overwrites its mirror with this.
	Reminders []runner.Reminder

	// Usage and CumulativeUsage are populated only on DisplayUsage events.
	// They mirror the runner's per-iteration and run-total token counts so
	// the TUI status line can render context-window pressure.
	Usage           *runner.UsageInfo
	CumulativeUsage *runner.UsageInfo
}

// EventConverter accumulates runner events and produces DisplayEvents.
type EventConverter struct {
	iteration          int
	assistantText      strings.Builder
	thinkingText       strings.Builder
	currentModel       string
	inAssistant        bool
	inThinking         bool
	assistantStartTime time.Time
	toolStartTimes     map[string]time.Time
}

// NewEventConverter creates a new converter.
func NewEventConverter() *EventConverter {
	return &EventConverter{toolStartTimes: make(map[string]time.Time)}
}

func parseRunnerEventTimestamp(raw string) (time.Time, string) {
	if raw == "" {
		return time.Time{}, ""
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, raw
	}
	return parsed.Local(), raw
}

func displayDuration(start, end time.Time) time.Duration {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start)
}

// Convert transforms a runner.Event into zero or more DisplayEvents.
// It may return nil if the event is accumulated (e.g. text_delta).
func (c *EventConverter) Convert(ev *runner.Event) []DisplayEvent {
	eventTime, rawTimestamp := parseRunnerEventTimestamp(ev.Timestamp)

	switch ev.Type {
	case runner.EventSession:
		id := ev.ID
		if len(id) > 12 {
			id = id[:12]
		}
		return []DisplayEvent{{
			Type:         DisplaySession,
			Summary:      fmt.Sprintf("session %s", id),
			Detail:       fmt.Sprintf("Session ID: %s\nTimestamp: %s\nCWD: %s", ev.ID, ev.Timestamp, ev.CWD),
			Timestamp:    eventTime,
			RawTimestamp: rawTimestamp,
			Iteration:    c.iteration,
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
				Type:         DisplayUserMsg,
				Summary:      "> user",
				Detail:       detail,
				Timestamp:    eventTime,
				RawTimestamp: rawTimestamp,
				Iteration:    c.iteration,
			}}
		} else if msg.Role == "assistant" {
			c.currentModel = msg.Model
			if c.currentModel == "" {
				c.currentModel = "unknown"
			}
			c.assistantText.Reset()
			c.inAssistant = true
			c.assistantStartTime = eventTime
			return []DisplayEvent{{
				Type:           DisplayAssistantText,
				Summary:        fmt.Sprintf("< assistant (%s)", c.currentModel),
				Detail:         "",
				Timestamp:      eventTime,
				RawTimestamp:   rawTimestamp,
				Iteration:      c.iteration,
				StartTime:      c.assistantStartTime,
				AssistantModel: c.currentModel,
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
				Type:           DisplayAssistantText,
				Summary:        fmt.Sprintf("< assistant (%s) [%d chars]", c.currentModel, charCount),
				Detail:         text,
				Timestamp:      eventTime,
				RawTimestamp:   rawTimestamp,
				Iteration:      c.iteration,
				StartTime:      c.assistantStartTime,
				AssistantModel: c.currentModel,
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
				summary := "thinking"
				if len(text) > 60 {
					summary = fmt.Sprintf("thinking (%d chars)", len(text))
				}
				return []DisplayEvent{{
					Type:         DisplayThinking,
					Summary:      summary,
					Detail:       text,
					Timestamp:    eventTime,
					RawTimestamp: rawTimestamp,
					Iteration:    c.iteration,
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
			startTime := c.assistantStartTime
			c.assistantStartTime = time.Time{}
			text := c.assistantText.String()
			if text != "" {
				charCount := len(text)
				return []DisplayEvent{{
					Type:           DisplayAssistantText,
					Summary:        fmt.Sprintf("+ assistant (%d chars)", charCount),
					Detail:         text,
					Timestamp:      eventTime,
					RawTimestamp:   rawTimestamp,
					Iteration:      c.iteration,
					AssistantFinal: true,
					StartTime:      startTime,
					EndTime:        eventTime,
					Duration:       displayDuration(startTime, eventTime),
					AssistantModel: c.currentModel,
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
		summary := fmt.Sprintf("> %s", ev.ToolName)
		if argsSummary != "" {
			summary = fmt.Sprintf("> %s: %s", ev.ToolName, truncateStr(argsSummary, 60))
		}
		detail := fmt.Sprintf("Tool: %s\nCall ID: %s", ev.ToolName, ev.ToolCallID)
		if argsSummary != "" {
			detail += fmt.Sprintf("\nArgs: %s", argsSummary)
		}
		if c.toolStartTimes == nil {
			c.toolStartTimes = make(map[string]time.Time)
		}
		c.toolStartTimes[ev.ToolCallID] = eventTime
		return []DisplayEvent{{
			Type:            DisplayToolStart,
			Summary:         summary,
			Detail:          detail,
			Timestamp:       eventTime,
			RawTimestamp:    rawTimestamp,
			Iteration:       c.iteration,
			ToolCallID:      ev.ToolCallID,
			ToolName:        ev.ToolName,
			RawArgs:         ev.Args,
			ToolDisplayArgs: ev.ToolDisplayArgs,
			StartTime:       eventTime,
		}}

	case runner.EventToolExecutionUpdate:
		// Intermediate tool update — carries the actual arguments for a tool
		// that was previously started with minimal info.
		startTime := time.Time{}
		if c.toolStartTimes != nil {
			startTime = c.toolStartTimes[ev.ToolCallID]
		}
		return []DisplayEvent{{
			Type:            DisplayToolUpdate,
			Summary:         fmt.Sprintf("~ %s", ev.ToolName),
			Detail:          fmt.Sprintf("Tool: %s\nCall ID: %s", ev.ToolName, ev.ToolCallID),
			Timestamp:       eventTime,
			RawTimestamp:    rawTimestamp,
			Iteration:       c.iteration,
			ToolCallID:      ev.ToolCallID,
			ToolName:        ev.ToolName,
			RawArgs:         ev.Args,
			ToolDisplayArgs: ev.ToolDisplayArgs,
			StartTime:       startTime,
		}}

	case runner.EventToolExecutionEnd:
		isErr := ev.IsError != nil && *ev.IsError
		var summary string
		if isErr {
			summary = fmt.Sprintf("! %s error", ev.ToolName)
		} else {
			summary = fmt.Sprintf("+ %s done", ev.ToolName)
		}
		detail := fmt.Sprintf("Tool: %s\nCall ID: %s\nError: %v", ev.ToolName, ev.ToolCallID, isErr)
		var resultText string
		if ev.Result != nil {
			resultText = jsonToText(ev.Result)
			detail += fmt.Sprintf("\nResult:\n%s", resultText)
		}
		startTime := time.Time{}
		if c.toolStartTimes != nil {
			startTime = c.toolStartTimes[ev.ToolCallID]
			delete(c.toolStartTimes, ev.ToolCallID)
		}
		return []DisplayEvent{{
			Type:           DisplayToolEnd,
			Summary:        summary,
			Detail:         detail,
			Timestamp:      eventTime,
			RawTimestamp:   rawTimestamp,
			Iteration:      c.iteration,
			ToolCallID:     ev.ToolCallID,
			ToolName:       ev.ToolName,
			ToolResultText: resultText,
			ToolIsError:    isErr,
			StartTime:      startTime,
			EndTime:        eventTime,
			Duration:       displayDuration(startTime, eventTime),
		}}

	case runner.EventTurnEnd:
		return []DisplayEvent{{
			Type:         DisplayTurnEnd,
			Summary:      "-- turn end --",
			Detail:       "Turn completed.",
			Timestamp:    eventTime,
			RawTimestamp: rawTimestamp,
			Iteration:    c.iteration,
		}}

	case runner.EventAgentEnd:
		return []DisplayEvent{{
			Type:         DisplayAgentEnd,
			Summary:      "-- agent end --",
			Detail:       "Agent process ended.",
			Timestamp:    eventTime,
			RawTimestamp: rawTimestamp,
			Iteration:    c.iteration,
		}}

	case runner.EventIteration:
		// Extract iteration number from the ID field ("iteration-N").
		n := 0
		if _, err := fmt.Sscanf(ev.ID, "iteration-%d", &n); err == nil {
			c.iteration = n
		}
		return []DisplayEvent{makeIterationEvent(c.iteration, eventTime, rawTimestamp)}

	case runner.EventInactivityTimeout:
		return []DisplayEvent{{
			Type:         DisplayInfo,
			Summary:      "Inactivity timeout — retrying iteration",
			Detail:       "Inactivity timeout — retrying iteration",
			Timestamp:    eventTime,
			RawTimestamp: rawTimestamp,
			Iteration:    c.iteration,
		}}

	case runner.EventIterationRestart:
		// ID format is "restart-<iteration>-<attempt>".
		iter, attempt := 0, 0
		_, _ = fmt.Sscanf(ev.ID, "restart-%d-%d", &iter, &attempt)
		text := fmt.Sprintf("Iteration %d restarted (attempt %d)", iter, attempt)
		return []DisplayEvent{{
			Type:         DisplayRestart,
			Summary:      text,
			Detail:       text,
			Timestamp:    eventTime,
			RawTimestamp: rawTimestamp,
			Iteration:    c.iteration,
			RestartIter:  iter,
		}}

	case runner.EventReminderState:
		return []DisplayEvent{{
			Type:         DisplayReminderState,
			Summary:      "reminder state update",
			Detail:       "",
			Timestamp:    eventTime,
			RawTimestamp: rawTimestamp,
			Iteration:    c.iteration,
			Reminders:    ev.Reminders,
		}}

	case runner.EventUsage:
		return []DisplayEvent{{
			Type:            DisplayUsage,
			Summary:         "usage update",
			Detail:          "",
			Timestamp:       eventTime,
			RawTimestamp:    rawTimestamp,
			Iteration:       c.iteration,
			Usage:           ev.Usage,
			CumulativeUsage: ev.CumulativeUsage,
		}}

	case runner.EventRateLimit:
		summary := "Rate limit event"
		if ev.RateLimit != nil {
			if ev.RateLimit.RequestsRemaining == 0 {
				summary = "Rate limited — waiting for capacity"
			} else {
				summary = fmt.Sprintf("Rate limit: %d requests remaining", ev.RateLimit.RequestsRemaining)
			}
		}
		return []DisplayEvent{{
			Type:         DisplayInfo,
			Summary:      summary,
			Detail:       summary,
			Timestamp:    eventTime,
			RawTimestamp: rawTimestamp,
			Iteration:    c.iteration,
		}}

	default:
		return nil
	}
}

func makeIterationEvent(n int, timestamp time.Time, rawTimestamp string) DisplayEvent {
	return DisplayEvent{
		Type:         DisplayIteration,
		Summary:      fmt.Sprintf("-- iteration %d --", n),
		Detail:       fmt.Sprintf("Starting iteration %d", n),
		Timestamp:    timestamp,
		RawTimestamp: rawTimestamp,
		Iteration:    n,
		StartTime:    timestamp,
	}
}

// MakeIterationEvent creates a synthetic iteration boundary event.
func MakeIterationEvent(n int) DisplayEvent {
	return makeIterationEvent(n, time.Now(), "")
}

// MakeInfoEvent creates a general info event.
func MakeInfoEvent(text string) DisplayEvent {
	return DisplayEvent{
		Type:      DisplayInfo,
		Summary:   text,
		Detail:    text,
		Timestamp: time.Now(),
	}
}

// jsonToText converts a json.RawMessage to a human-readable string.
// If the value is a JSON string, it is unquoted (quotes removed, escape
// sequences decoded). Otherwise the raw JSON is returned as-is.
func jsonToText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

func truncateStr(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n < 4 {
		return string(runes[:n])
	}
	return string(runes[:n-3]) + "..."
}
