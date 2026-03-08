// kiro.go implements the Agent interface using kiro-cli's ACP protocol.
//
// Each call to RunIteration spawns a fresh kiro-cli subprocess, performs the
// ACP handshake, creates a session, sends the prompt, and streams events back
// to the runner through the onEvent callback. The ACP notification types are
// translated into events.Event values that match pi's event lifecycle:
//
//   MessageStart(assistant) → MessageUpdate(text_delta)* → MessageEnd →
//   ToolExecutionStart → ToolExecutionEnd →
//   MessageStart(assistant) → MessageUpdate(text_delta)* → MessageEnd →
//   TurnEnd
//
// This ensures the TUI's EventConverter renders kiro output identically to pi.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/fsmiamoto/ralfinho/internal/events"
)

// KiroAgent implements the Agent interface using kiro-cli's ACP protocol.
//
// Each call to RunIteration spawns a new kiro-cli ACP subprocess. The agent
// manages the full lifecycle: initialize handshake → session creation →
// prompt execution → event streaming → teardown.
type KiroAgent struct {
	opts Options
}

// NewKiroAgent creates a KiroAgent with the given options.
// Pass WithRawWriter to capture raw JSON-RPC messages for debugging.
func NewKiroAgent(options ...Option) *KiroAgent {
	return &KiroAgent{
		opts: applyOptions(options),
	}
}

// RunIteration spawns kiro-cli, sends the prompt via ACP, maps streaming
// notifications to events.Event values, and returns the accumulated assistant
// text.
func (a *KiroAgent) RunIteration(ctx context.Context, prompt string, onEvent func(events.Event)) (string, error) {
	// Spawn ACP client (includes initialize handshake).
	client, err := newACPClient(ctx, a.opts.RawWriter, a.opts.LogWriter)
	if err != nil {
		return "", fmt.Errorf("kiro: %w", err)
	}
	defer client.Close()

	// Create a session with the current working directory.
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("kiro: getwd: %w", err)
	}

	sessionID, err := client.sessionNew(ctx, cwd)
	if err != nil {
		return "", fmt.Errorf("kiro: %w", err)
	}

	// Start the permission auto-approve handler so tool use is unblocked.
	approveCtx, approveCancel := context.WithCancel(ctx)
	defer approveCancel()
	go client.autoApprovePermissions(approveCtx)

	// State tracker for translating ACP updates into events.Event values.
	mapper := newKiroEventMapper(onEvent)

	// Send the prompt and stream updates until TurnEnd.
	err = client.sessionPrompt(ctx, sessionID, prompt, func(u sessionUpdate) {
		mapper.handleUpdate(u)
	})

	// Ensure proper event lifecycle closure even on error/cancel.
	mapper.finalize()

	// Surface context cancellation so the runner knows the iteration was
	// interrupted rather than completed normally.
	if ctx.Err() != nil {
		return mapper.assistantText(), ctx.Err()
	}
	if err != nil {
		return mapper.assistantText(), fmt.Errorf("kiro: %w", err)
	}

	return mapper.assistantText(), nil
}

// ---------------------------------------------------------------------------
// Event mapper: ACP session updates → events.Event values
// ---------------------------------------------------------------------------

// kiroEventMapper translates ACP session updates into events.Event values,
// maintaining the MessageStart/MessageEnd lifecycle around text chunks that
// the TUI's EventConverter expects.
//
// State machine:
//   - When an AgentMessageChunk arrives and we're not in a message block,
//     emit MessageStart(assistant) first, then MessageUpdate.
//   - When a ToolCall(pending/running) arrives and we're in a message block,
//     emit MessageEnd first, then ToolExecutionStart.
//   - On TurnEnd, close any open message block, then emit TurnEnd.
type kiroEventMapper struct {
	onEvent   func(events.Event)
	text      strings.Builder // accumulated assistant text for the return value
	inMessage bool            // true between MessageStart and MessageEnd
	turnEnded bool            // true after TurnEnd was emitted
}

// newKiroEventMapper creates a mapper that forwards events through onEvent.
func newKiroEventMapper(onEvent func(events.Event)) *kiroEventMapper {
	return &kiroEventMapper{onEvent: onEvent}
}

// assistantText returns the accumulated text from all AgentMessageChunk
// updates. The runner uses this to detect the completion marker.
func (m *kiroEventMapper) assistantText() string {
	return m.text.String()
}

// handleUpdate dispatches a session update to the appropriate mapping method.
func (m *kiroEventMapper) handleUpdate(u sessionUpdate) {
	switch u.Kind {
	case updateKindAgentMessage:
		m.mapAgentMessage(u)
	case updateKindToolCall:
		m.mapToolCall(u)
	case updateKindToolCallUpdate:
		m.mapToolCallUpdate(u)
	}
}

// finalize ensures proper event lifecycle closure. Called after sessionPrompt
// returns. Kiro signals turn completion via the prompt response (not a
// TurnEnd update), so finalize always closes the message block and emits
// TurnEnd.
func (m *kiroEventMapper) finalize() {
	if m.inMessage {
		m.emitMessageEnd()
	}
	if !m.turnEnded {
		m.onEvent(events.Event{Type: events.EventTurnEnd})
		m.turnEnded = true
	}
}

// ---------------------------------------------------------------------------
// Mapping methods
// ---------------------------------------------------------------------------

// mapAgentMessage translates an agent_message_chunk update into
// EventMessageUpdate with a text_delta AssistantEvent payload.
//
// Kiro sends: {"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"..."}}
//
// If no message block is currently open, a synthetic MessageStart(assistant)
// is emitted first to satisfy the TUI's event lifecycle expectations.
func (m *kiroEventMapper) mapAgentMessage(u sessionUpdate) {
	var chunk struct {
		Content struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(u.Raw, &chunk); err != nil || chunk.Content.Text == "" {
		return
	}

	// Open a message block if needed.
	if !m.inMessage {
		m.emitMessageStart()
	}

	// Accumulate for the return value.
	m.text.WriteString(chunk.Content.Text)

	// Build the text_delta AssistantEvent payload.
	ae := events.AssistantEvent{
		Type:         "text_delta",
		ContentIndex: 0,
		Delta:        chunk.Content.Text,
	}
	aeJSON, _ := json.Marshal(ae)

	m.onEvent(events.Event{
		Type:                  events.EventMessageUpdate,
		AssistantMessageEvent: aeJSON,
	})
}

// mapToolCall translates a tool_call update into tool execution events.
//
// Kiro sends tool_call updates with fields: toolCallId, title, kind, status,
// rawInput, rawOutput. Multiple tool_call updates may arrive for the same
// toolCallId as its status progresses.
//
// Status mapping:
//   - "in_progress" → EventToolExecutionStart (close message block first)
//   - "completed" → EventToolExecutionEnd with isError=false
//   - "error" → EventToolExecutionEnd with isError=true
func (m *kiroEventMapper) mapToolCall(u sessionUpdate) {
	var tc struct {
		ToolCallID string          `json:"toolCallId"`
		Title      string          `json:"title"`
		Status     string          `json:"status"`
		RawInput   json.RawMessage `json:"rawInput"`
		RawOutput  json.RawMessage `json:"rawOutput"`
	}
	if err := json.Unmarshal(u.Raw, &tc); err != nil {
		return
	}

	switch tc.Status {
	case "in_progress":
		// Close any open message block before tool use.
		if m.inMessage {
			m.emitMessageEnd()
		}
		m.onEvent(events.Event{
			Type:       events.EventToolExecutionStart,
			ToolCallID: tc.ToolCallID,
			ToolName:   tc.Title,
			Args:       tc.RawInput,
		})

	case "completed":
		isErr := false
		m.onEvent(events.Event{
			Type:       events.EventToolExecutionEnd,
			ToolCallID: tc.ToolCallID,
			ToolName:   tc.Title,
			Result:     tc.RawOutput,
			IsError:    &isErr,
		})

	case "error":
		isErr := true
		m.onEvent(events.Event{
			Type:       events.EventToolExecutionEnd,
			ToolCallID: tc.ToolCallID,
			ToolName:   tc.Title,
			Result:     tc.RawOutput,
			IsError:    &isErr,
		})

	default:
		// Intermediate update — kiro sends a follow-up tool_call without
		// a status field that carries the actual rawInput and an updated
		// title (e.g. "Running: git status"). Forward as a tool execution
		// update so the TUI can display the real arguments.
		if tc.RawInput != nil {
			m.onEvent(events.Event{
				Type:       events.EventToolExecutionUpdate,
				ToolCallID: tc.ToolCallID,
				ToolName:   tc.Title,
				Args:       tc.RawInput,
			})
		}
	}
}

// mapToolCallUpdate translates intermediate tool call progress into
// EventToolExecutionUpdate. These are non-critical streaming updates that
// the TUI can optionally render.
func (m *kiroEventMapper) mapToolCallUpdate(u sessionUpdate) {
	var tc struct {
		ToolCallID    string          `json:"toolCallId"`
		ToolName      string          `json:"toolName"`
		PartialResult json.RawMessage `json:"partialResult"`
	}
	if err := json.Unmarshal(u.Raw, &tc); err != nil {
		return
	}

	m.onEvent(events.Event{
		Type:          events.EventToolExecutionUpdate,
		ToolCallID:    tc.ToolCallID,
		ToolName:      tc.ToolName,
		PartialResult: tc.PartialResult,
	})
}

// ---------------------------------------------------------------------------
// Synthetic event helpers
// ---------------------------------------------------------------------------

// emitMessageStart sends a synthetic MessageStart with role "assistant" and
// model "kiro". This matches pi's MessageStart so the TUI's EventConverter
// initializes its state correctly.
func (m *kiroEventMapper) emitMessageStart() {
	msg := events.MessageEnvelope{
		Role:  "assistant",
		Model: "kiro",
	}
	msgJSON, _ := json.Marshal(msg)

	m.onEvent(events.Event{
		Type:    events.EventMessageStart,
		Message: msgJSON,
	})
	m.inMessage = true
}

// emitMessageEnd sends a synthetic MessageEnd event. The TUI uses this to
// finalize the current assistant text block (e.g. display the char count).
func (m *kiroEventMapper) emitMessageEnd() {
	m.onEvent(events.Event{Type: events.EventMessageEnd})
	m.inMessage = false
}
