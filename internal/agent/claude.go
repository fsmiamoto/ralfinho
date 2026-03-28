// claude.go implements the Agent interface using Claude Code's CLI (`claude -p`).
//
// Each call to RunIteration spawns a fresh `claude -p` subprocess with
// `--output-format stream-json` to get newline-delimited JSON. The stream-json
// lines wrap Anthropic API streaming events, which are translated into
// events.Event values matching ralfinho's lifecycle:
//
//	MessageStart(assistant) → MessageUpdate(text_delta)* → MessageEnd →
//	ToolExecutionStart → ToolExecutionEnd →
//	MessageStart(assistant) → MessageUpdate(text_delta)* → MessageEnd →
//	TurnEnd
//
// Claude Code runs tools internally — we observe tool_use blocks in assistant
// messages and tool_result entries in user messages, then translate those into
// the same ToolExecution events that pi emits natively.
//
// A single `claude -p` invocation may perform multiple internal turns
// (assistant → tool → assistant) before returning. Each ralfinho "iteration"
// is one subprocess invocation.
package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/fsmiamoto/ralfinho/internal/events"
)

// ---------------------------------------------------------------------------
// ClaudeAgent
// ---------------------------------------------------------------------------

// ClaudeAgent implements the Agent interface using the Claude Code CLI.
//
// Each call to RunIteration spawns a new `claude -p` subprocess with
// streaming JSON output. The agent manages line scanning, event mapping,
// and lifecycle closure.
type ClaudeAgent struct {
	binary string  // path or name of the claude binary (default: "claude")
	opts   Options // optional settings (raw writer, log writer, etc.)
}

// NewClaudeAgent creates a ClaudeAgent with the given options.
// The binary defaults to "claude". Pass WithRawWriter to capture raw
// stream-json lines for debugging.
func NewClaudeAgent(opts ...Option) *ClaudeAgent {
	return &ClaudeAgent{
		binary: "claude",
		opts:   applyOptions(opts),
	}
}

// RunIteration spawns a `claude -p` subprocess, streams parsed events via
// onEvent, and returns the accumulated assistant text.
func (a *ClaudeAgent) RunIteration(ctx context.Context, prompt string, onEvent func(events.Event)) (string, error) {
	// Build command args.
	cmdArgs := []string{
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--dangerously-skip-permissions",
		"--no-session-persistence",
	}
	cmdArgs = append(cmdArgs, a.opts.ExtraArgs...)

	cmd := exec.CommandContext(ctx, a.binary, cmdArgs...)
	stderrBuf := newLimitedBuffer(4096)
	cmd.Stderr = stderrBuf

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("claude: creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdout.Close()
		return "", fmt.Errorf("claude: starting agent: %w", err)
	}

	// Optionally tee raw stdout to RawWriter.
	var stdoutReader io.Reader = stdout
	if a.opts.RawWriter != nil {
		stdoutReader = io.TeeReader(stdout, a.opts.RawWriter)
	}

	scanner := bufio.NewScanner(stdoutReader)
	// Allow large lines (Claude Code can produce big JSON).
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	mapper := newClaudeEventMapper(onEvent)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Parse the top-level line to get type + raw payload.
		var cl claudeLine
		if err := json.Unmarshal([]byte(line), &cl); err != nil {
			continue // skip unparseable lines
		}

		mapper.handleLine(cl.Type, []byte(line))
	}

	waitErr := cmd.Wait()

	if err := scanner.Err(); err != nil {
		mapper.finalize()
		return mapper.assistantText(), fmt.Errorf("claude: reading agent output: %w", err)
	}

	// Ensure proper lifecycle closure after scan loop.
	mapper.finalize()

	// If the process exited successfully, return nil regardless of context
	// state. The context may have been cancelled concurrently (e.g. SIGINT
	// from child process group cleanup), but a clean exit means the
	// iteration completed normally.
	if waitErr == nil {
		return mapper.assistantText(), nil
	}

	// The process failed. Surface context cancellation over raw exit error.
	if ctx.Err() != nil {
		return mapper.assistantText(), ctx.Err()
	}

	stderr := strings.TrimSpace(stderrBuf.String())
	if stderr != "" {
		return mapper.assistantText(), fmt.Errorf("claude: agent exited with error: %w\nstderr: %s", waitErr, stderr)
	}
	return mapper.assistantText(), fmt.Errorf("claude: agent exited with error: %w", waitErr)
}

// ---------------------------------------------------------------------------
// JSON parse structs (unexported)
// ---------------------------------------------------------------------------

// claudeLine is the top-level envelope for each stream-json line.
type claudeLine struct {
	Type string `json:"type"`
}

// claudeStreamEventLine wraps a stream_event line with its nested event.
type claudeStreamEventLine struct {
	Event claudeStreamEvent `json:"event"`
}

// claudeStreamEvent represents an Anthropic API streaming event.
type claudeStreamEvent struct {
	Type         string              `json:"type"`
	Message      *claudeMessage      `json:"message,omitempty"`
	ContentBlock *claudeContentBlock `json:"content_block,omitempty"`
	Delta        *claudeDelta        `json:"delta,omitempty"`
}

// claudeMessage is the message payload in message_start events.
type claudeMessage struct {
	Role  string `json:"role"`
	Model string `json:"model"`
}

// claudeContentBlock describes a content block in content_block_start events.
type claudeContentBlock struct {
	Type string `json:"type"` // "text" or "tool_use"
	Name string `json:"name,omitempty"`
	ID   string `json:"id,omitempty"`
}

// claudeDelta carries incremental content in content_block_delta events.
type claudeDelta struct {
	Type        string `json:"type"` // "text_delta" or "input_json_delta"
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

// claudeUserLine represents a `user` line containing tool results.
type claudeUserLine struct {
	Message struct {
		Content []claudeToolResult `json:"content"`
	} `json:"message"`
}

// claudeToolResult represents a single tool_result entry in a user message.
type claudeToolResult struct {
	Type       string          `json:"type"`
	ToolUseID  string          `json:"tool_use_id"`
	Content    json.RawMessage `json:"content"`
	IsError    bool            `json:"is_error"`
}

// ---------------------------------------------------------------------------
// Event mapper: Claude Code stream-json → events.Event values
// ---------------------------------------------------------------------------

// claudeEventMapper translates Claude Code's Anthropic API streaming events
// into events.Event values, maintaining the MessageStart/MessageEnd lifecycle
// that the TUI's EventConverter expects.
//
// State machine with three states:
//   - idle: no open message or tool block
//   - inMessage: between MessageStart and MessageEnd (text content)
//   - inToolBlock: inside a tool_use content block (accumulating args)
//
// Transitions follow the plan's event mapping specification.
type claudeEventMapper struct {
	onEvent          func(events.Event)
	text             strings.Builder    // accumulated assistant text
	toolRegistry     map[string]string  // toolCallId → toolName
	inMessage        bool               // true between MessageStart and MessageEnd
	inToolBlock      bool               // true inside a tool_use content block
	turnEnded        bool               // true after TurnEnd was emitted
	currentBlockType string             // "text" or "tool_use"
	argsAccumulator  strings.Builder    // accumulates input_json_delta partials
	currentToolID    string             // id of the current tool_use block
}

// newClaudeEventMapper creates a mapper that forwards events through onEvent.
func newClaudeEventMapper(onEvent func(events.Event)) *claudeEventMapper {
	return &claudeEventMapper{
		onEvent:      onEvent,
		toolRegistry: make(map[string]string),
	}
}

// assistantText returns the accumulated text from all text_delta events.
func (m *claudeEventMapper) assistantText() string {
	return m.text.String()
}

// handleLine dispatches a top-level stream-json line by its type field.
func (m *claudeEventMapper) handleLine(lineType string, raw []byte) {
	switch lineType {
	case "stream_event":
		m.handleStreamEvent(raw)
	case "user":
		m.handleUserEvent(raw)
	case "result":
		m.handleResult()
	case "rate_limit_event":
		m.handleRateLimitEvent(raw)
	case "assistant", "system":
		// Skip — assistant is redundant with stream_events, system is
		// informational only.
	}
}

// handleStreamEvent dispatches on the nested event.type field.
func (m *claudeEventMapper) handleStreamEvent(raw []byte) {
	var sel claudeStreamEventLine
	if err := json.Unmarshal(raw, &sel); err != nil {
		return
	}

	switch sel.Event.Type {
	case "message_start":
		m.mapMessageStart(sel.Event)
	case "content_block_start":
		m.mapContentBlockStart(sel.Event)
	case "content_block_delta":
		m.mapContentBlockDelta(sel.Event)
	case "content_block_stop":
		m.mapContentBlockStop()
	case "message_stop":
		m.mapMessageStop()
	// message_delta, ping, etc. — ignored
	}
}

// handleUserEvent processes a `user` line containing tool_result entries.
// Each tool_result is mapped to an EventToolExecutionEnd.
func (m *claudeEventMapper) handleUserEvent(raw []byte) {
	var ul claudeUserLine
	if err := json.Unmarshal(raw, &ul); err != nil {
		return
	}

	for _, tr := range ul.Message.Content {
		if tr.Type != "tool_result" {
			continue
		}

		isErr := tr.IsError
		toolName := m.toolRegistry[tr.ToolUseID]

		m.onEvent(events.Event{
			Type:       events.EventToolExecutionEnd,
			ToolCallID: tr.ToolUseID,
			ToolName:   toolName,
			Result:     tr.Content,
			IsError:    &isErr,
		})
	}
}

// handleResult processes a `result` line — closes any open message block
// and emits TurnEnd.
func (m *claudeEventMapper) handleResult() {
	if m.inMessage {
		m.emitMessageEnd()
	}
	if !m.turnEnded {
		m.onEvent(events.Event{Type: events.EventTurnEnd})
		m.turnEnded = true
	}
}

// finalize ensures proper event lifecycle closure after the scan loop.
// Delegates to handleResult which is idempotent — safe to call even if the
// result line was already processed.
func (m *claudeEventMapper) finalize() {
	m.handleResult()
}

// ---------------------------------------------------------------------------
// Stream event mapping methods
// ---------------------------------------------------------------------------

// mapMessageStart handles a message_start event. Extracts the model from
// event.message and emits EventMessageStart(role=assistant).
func (m *claudeEventMapper) mapMessageStart(ev claudeStreamEvent) {
	if ev.Message == nil {
		return
	}

	model := ev.Message.Model
	m.emitMessageStart(model)
}

// mapContentBlockStart handles a content_block_start event.
//
// For text blocks: records the block type (no event emitted).
// For tool_use blocks: closes any open message block, registers the tool
// in the registry, and emits EventToolExecutionStart.
func (m *claudeEventMapper) mapContentBlockStart(ev claudeStreamEvent) {
	if ev.ContentBlock == nil {
		return
	}

	m.currentBlockType = ev.ContentBlock.Type

	switch ev.ContentBlock.Type {
	case "text":
		// Just note we're in a text block — events come from deltas.

	case "tool_use":
		// Close any open message block before tool use.
		if m.inMessage {
			m.emitMessageEnd()
		}

		// Register tool name by id for later lookup in user events.
		m.toolRegistry[ev.ContentBlock.ID] = ev.ContentBlock.Name
		m.currentToolID = ev.ContentBlock.ID

		// Emit tool execution start.
		m.onEvent(events.Event{
			Type:       events.EventToolExecutionStart,
			ToolCallID: ev.ContentBlock.ID,
			ToolName:   ev.ContentBlock.Name,
		})

		// Enter tool block state, reset args accumulator.
		m.inToolBlock = true
		m.argsAccumulator.Reset()
	}
}

// mapContentBlockDelta handles a content_block_delta event.
//
// For text_delta: accumulates text and emits EventMessageUpdate.
// For input_json_delta: accumulates partial JSON for tool args.
func (m *claudeEventMapper) mapContentBlockDelta(ev claudeStreamEvent) {
	if ev.Delta == nil {
		return
	}

	switch ev.Delta.Type {
	case "text_delta":
		// Accumulate for return value.
		m.text.WriteString(ev.Delta.Text)

		// Build the text_delta AssistantEvent payload.
		ae := events.AssistantEvent{
			Type:         "text_delta",
			ContentIndex: 0,
			Delta:        ev.Delta.Text,
		}
		aeJSON, _ := json.Marshal(ae)

		m.onEvent(events.Event{
			Type:                  events.EventMessageUpdate,
			AssistantMessageEvent: aeJSON,
		})

	case "input_json_delta":
		// Accumulate partial JSON for tool args.
		m.argsAccumulator.WriteString(ev.Delta.PartialJSON)
	}
}

// mapContentBlockStop handles a content_block_stop event.
//
// If the block was a tool_use block with accumulated args, emits
// EventToolExecutionUpdate with the parsed args. Exits tool block state.
func (m *claudeEventMapper) mapContentBlockStop() {
	if m.inToolBlock {
		// Attempt to parse and emit accumulated args as an update.
		if args := m.argsAccumulator.String(); args != "" {
			var parsed json.RawMessage
			if json.Unmarshal([]byte(args), &parsed) == nil {
				m.onEvent(events.Event{
					Type:       events.EventToolExecutionUpdate,
					ToolCallID: m.currentToolID,
					ToolName:   m.toolRegistry[m.currentToolID],
					Args:       parsed,
				})
			}
		}

		m.inToolBlock = false
		m.currentBlockType = ""
	}
}

// mapMessageStop handles a message_stop event. Closes any open message block.
func (m *claudeEventMapper) mapMessageStop() {
	if m.inMessage {
		m.emitMessageEnd()
	}
}

// handleRateLimitEvent parses a rate_limit_event line and emits an
// EventRateLimit with the requests_remaining count.
func (m *claudeEventMapper) handleRateLimitEvent(raw []byte) {
	var rl struct {
		RateLimit struct {
			RequestsRemaining int `json:"requests_remaining"`
		} `json:"rate_limit"`
	}
	if err := json.Unmarshal(raw, &rl); err != nil {
		return
	}
	m.onEvent(events.Event{
		Type: events.EventRateLimit,
		RateLimit: &events.RateLimitInfo{
			RequestsRemaining: rl.RateLimit.RequestsRemaining,
		},
	})
}

// ---------------------------------------------------------------------------
// Synthetic event helpers
// ---------------------------------------------------------------------------

// emitMessageStart sends an EventMessageStart with role "assistant" and the
// given model name.
func (m *claudeEventMapper) emitMessageStart(model string) {
	msg := events.MessageEnvelope{
		Role:  "assistant",
		Model: model,
	}
	msgJSON, _ := json.Marshal(msg)

	m.onEvent(events.Event{
		Type:    events.EventMessageStart,
		Message: msgJSON,
	})
	m.inMessage = true
}

// emitMessageEnd sends an EventMessageEnd and clears the inMessage flag.
func (m *claudeEventMapper) emitMessageEnd() {
	m.onEvent(events.Event{Type: events.EventMessageEnd})
	m.inMessage = false
}
