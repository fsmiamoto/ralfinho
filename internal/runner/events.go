// Package runner implements the ralfinho agent iteration loop.
package runner

import "encoding/json"

// EventType enumerates the pi JSON protocol event types.
type EventType string

const (
	EventSession            EventType = "session"
	EventAgentStart         EventType = "agent_start"
	EventTurnStart          EventType = "turn_start"
	EventMessageStart       EventType = "message_start"
	EventMessageUpdate      EventType = "message_update"
	EventMessageEnd         EventType = "message_end"
	EventToolExecutionStart EventType = "tool_execution_start"
	EventToolExecutionUpdate EventType = "tool_execution_update"
	EventToolExecutionEnd   EventType = "tool_execution_end"
	EventTurnEnd            EventType = "turn_end"
	EventAgentEnd           EventType = "agent_end"
)

// Event is the top-level envelope for every JSONL line emitted by pi.
type Event struct {
	Type EventType `json:"type"`

	// session fields
	Version   int    `json:"version,omitempty"`
	ID        string `json:"id,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	CWD       string `json:"cwd,omitempty"`

	// message_start / message_end / turn_end
	Message json.RawMessage `json:"message,omitempty"`

	// message_update
	AssistantMessageEvent json.RawMessage `json:"assistantMessageEvent,omitempty"`

	// tool_execution_*
	ToolCallID    string          `json:"toolCallId,omitempty"`
	ToolName      string          `json:"toolName,omitempty"`
	Args          json.RawMessage `json:"args,omitempty"`
	PartialResult json.RawMessage `json:"partialResult,omitempty"`
	Result        json.RawMessage `json:"result,omitempty"`
	IsError       *bool           `json:"isError,omitempty"`

	// agent_end
	Messages json.RawMessage `json:"messages,omitempty"`
}

// MessageEnvelope is used for message_start / message_end payloads.
type MessageEnvelope struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Model      string          `json:"model,omitempty"`
	Provider   string          `json:"provider,omitempty"`
	Usage      json.RawMessage `json:"usage,omitempty"`
	StopReason string          `json:"stopReason,omitempty"`
}

// AssistantEvent is the nested payload inside message_update events.
type AssistantEvent struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta,omitempty"`
	Content      string `json:"content,omitempty"`
}

// ContentBlock represents one block inside a message's content array.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ToolArgs holds the decoded arguments for a tool call.
type ToolArgs struct {
	Command string `json:"command,omitempty"`
}
