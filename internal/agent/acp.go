//go:build unix

// acp.go implements the ACP (Agent Communication Protocol) client that manages
// a kiro-cli subprocess communicating via JSON-RPC 2.0 over stdio.
//
// The acpClient handles:
//   - Spawning `kiro-cli acp` and wiring stdin/stdout to a JSON-RPC codec.
//   - A read goroutine that dispatches incoming messages by type:
//     responses → per-request channels, notifications → notifications channel,
//     reverse requests → reverseReqs channel.
//   - The initialize handshake (protocolVersion, capabilities, clientInfo).
//   - Clean teardown (process kill + wait + drain read goroutine).
//
// Higher-level methods (session/new, session/prompt) are added in later tasks.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/fsmiamoto/ralfinho/internal/events"
)

const (
	// acpProtocolVersion is the ACP protocol version sent during initialization.
	acpProtocolVersion = "2025-03-26"

	// initializeTimeout is the maximum time allowed for the ACP handshake.
	initializeTimeout = 10 * time.Second
)

// ---------------------------------------------------------------------------
// ACP initialize handshake types
// ---------------------------------------------------------------------------

type initializeParams struct {
	ProtocolVersion    string             `json:"protocolVersion"`
	ClientCapabilities clientCapabilities `json:"clientCapabilities"`
	ClientInfo         clientInfo         `json:"clientInfo"`
}

type clientCapabilities struct {
	FS       *struct{} `json:"fs"`       // empty struct → {} (kiro expects a FileSystemCapability object)
	Terminal bool      `json:"terminal"` // kiro expects a boolean
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ---------------------------------------------------------------------------
// limitedBuffer — ring buffer capturing the last N bytes
// ---------------------------------------------------------------------------

// limitedBuffer captures the last N bytes written to it.
type limitedBuffer struct {
	buf  []byte
	size int
	pos  int
	full bool
}

func newLimitedBuffer(size int) *limitedBuffer {
	return &limitedBuffer{buf: make([]byte, size), size: size}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	for _, c := range p {
		b.buf[b.pos] = c
		b.pos = (b.pos + 1) % b.size
		if b.pos == 0 {
			b.full = true
		}
	}
	return n, nil
}

func (b *limitedBuffer) String() string {
	if !b.full {
		return string(b.buf[:b.pos])
	}
	return string(b.buf[b.pos:]) + string(b.buf[:b.pos])
}

// ---------------------------------------------------------------------------
// ACPClient
// ---------------------------------------------------------------------------

// acpClient manages a kiro-cli ACP subprocess and speaks JSON-RPC 2.0 over
// its stdin/stdout.
//
// Message dispatch:
//   - Responses (to our requests) are routed to per-request channels registered
//     by call(). The caller blocks until the matching response arrives.
//   - Notifications (server → client, no id) go to the notifications channel.
//   - Reverse requests (server → client, has id+method) go to reverseReqs.
//     Task 4 wires the auto-approve handler that consumes from this channel.
type acpClient struct {
	cmd   *exec.Cmd
	codec *rpcCodec

	// pending maps request IDs to channels waiting for the response.
	// Protected by pendingMu. Set to nil when the read loop exits to signal
	// that no more responses will arrive.
	pending   map[int64]chan<- *rpcMessage
	pendingMu sync.Mutex

	// notifications receives server-initiated notifications (e.g.
	// session/notification events during prompt execution). Buffered to
	// avoid blocking the read loop.
	notifications chan *rpcMessage

	// reverseReqs receives server-initiated requests that expect a response
	// (e.g. session/request_permission). A separate goroutine should consume
	// from this channel and reply via the codec.
	reverseReqs chan *rpcMessage

	// done is closed when the read goroutine exits. Any pending call()
	// waiters are unblocked.
	done      chan struct{}
	readErr   error // the error that terminated the read loop (often io.EOF)
	readErrMu sync.Mutex

	// logWriter receives diagnostic/warning messages.
	logWriter io.Writer

	// stderrBuf captures the last N bytes of kiro-cli stderr for diagnostics.
	stderrBuf *limitedBuffer

	// closeOnce ensures Close() body runs exactly once.
	closeOnce sync.Once
	closeErr  error
}

// getReadErr returns the error that terminated the read loop, if any.
func (c *acpClient) getReadErr() error {
	c.readErrMu.Lock()
	defer c.readErrMu.Unlock()
	return c.readErr
}

// newACPClient spawns `kiro-cli acp`, performs the ACP initialize handshake,
// and returns a ready-to-use client. The caller must call Close() when done.
//
// If rawWriter is non-nil, raw JSON-RPC messages from stdout are tee'd to it
// for debugging (raw-output.log).
func newACPClient(ctx context.Context, rawWriter io.Writer, logWriter io.Writer) (*acpClient, error) {
	cmd := exec.CommandContext(ctx, "kiro-cli", "acp", "--trust-all-tools")
	stderrBuf := newLimitedBuffer(4096)
	cmd.Stderr = stderrBuf // capture last 4KB of kiro-cli stderr for diagnostics
	// Use a process group so we can kill kiro-cli and all its children.
	// Without this, child processes keep the stdout pipe open after kill.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("kiro-cli not found in PATH. Install from https://kiro.dev/cli/")
		}
		return nil, fmt.Errorf("acp: start kiro-cli: %w", err)
	}

	// Optionally tee raw stdout for debugging.
	var reader io.Reader = stdout
	if rawWriter != nil {
		reader = io.TeeReader(stdout, rawWriter)
	}

	c := &acpClient{
		cmd:           cmd,
		codec:         newRPCCodec(reader, stdin),
		pending:       make(map[int64]chan<- *rpcMessage),
		notifications: make(chan *rpcMessage, 128),
		reverseReqs:   make(chan *rpcMessage, 16),
		done:          make(chan struct{}),
		logWriter:     logWriter,
		stderrBuf:     stderrBuf,
	}

	// Start the background read goroutine before the handshake so we can
	// receive the initialize response.
	go c.readLoop()

	// Perform the initialize handshake with a timeout.
	if err := c.initialize(ctx); err != nil {
		c.Close()
		return nil, err
	}

	return c, nil
}

// ---------------------------------------------------------------------------
// Read loop — single goroutine dispatching incoming messages
// ---------------------------------------------------------------------------

// readLoop reads messages from the codec and dispatches them to the
// appropriate destination. Runs in its own goroutine until the codec returns
// an error (typically io.EOF when the subprocess exits).
func (c *acpClient) readLoop() {
	defer close(c.done)

	for {
		msg, err := c.codec.readMessage()
		if err != nil {
			// Malformed messages (valid framing but invalid JSON) are
			// recoverable — the stream position is still correct. Skip
			// the bad message and continue reading.
			var me *malformedError
			if errors.As(err, &me) {
				continue
			}

			// I/O errors (EOF, broken pipe) are fatal — the stream is
			// in an unknown state or the subprocess has exited.
			c.readErrMu.Lock()
			c.readErr = err
			c.readErrMu.Unlock()

			// Unblock all pending callers by closing their channels.
			c.pendingMu.Lock()
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.pending = nil // signal that no more responses will arrive
			c.pendingMu.Unlock()

			return
		}

		switch {
		case msg.IsResponse():
			// Route to the caller waiting for this response ID.
			id, ok := rpcIDInt(msg.ID)
			if !ok {
				continue // malformed id — skip
			}
			c.pendingMu.Lock()
			ch, exists := c.pending[id]
			if exists {
				delete(c.pending, id)
			}
			c.pendingMu.Unlock()
			if exists {
				ch <- msg
			}

		case msg.IsNotification():
			select {
			case c.notifications <- msg:
			default:
				// Drop if buffer is full. In practice 128 is more than
				// enough for streaming text chunks between reads.
				fmt.Fprintf(c.logWriter, "acp: warning: notification buffer full, dropping %s\n", msg.Method)
			}

		case msg.IsReverseRequest():
			select {
			case c.reverseReqs <- msg:
			default:
				// Drop if buffer is full — shouldn't happen since the
				// permission handler should consume promptly.
				fmt.Fprintf(c.logWriter, "acp: warning: reverse request buffer full, dropping %s\n", msg.Method)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// RPC helpers
// ---------------------------------------------------------------------------

// call sends a JSON-RPC request and blocks until the matching response arrives,
// the context is cancelled, or the connection is closed.
func (c *acpClient) call(ctx context.Context, method string, params any) (*rpcMessage, error) {
	req := c.codec.newRequest(method, params)

	// Register a channel for the response before sending, so we can't miss
	// a fast reply.
	ch := make(chan *rpcMessage, 1)

	c.pendingMu.Lock()
	if c.pending == nil {
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("acp: connection already closed")
	}
	c.pending[req.ID] = ch
	c.pendingMu.Unlock()

	// Clean up the registration on return (may already be removed by readLoop).
	defer func() {
		c.pendingMu.Lock()
		if c.pending != nil {
			delete(c.pending, req.ID)
		}
		c.pendingMu.Unlock()
	}()

	if err := c.codec.send(req); err != nil {
		return nil, fmt.Errorf("acp: send %s: %w", method, err)
	}

	select {
	case msg, ok := <-ch:
		if !ok {
			// Channel closed by readLoop — connection died.
			return nil, fmt.Errorf("acp: connection closed while waiting for %s response: %v", method, c.getReadErr())
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("acp: %s: server error %d: %s", method, msg.Error.Code, msg.Error.Message)
		}
		return msg, nil

	case <-ctx.Done():
		return nil, ctx.Err()

	case <-c.done:
		return nil, fmt.Errorf("acp: connection closed while waiting for %s response: %v", method, c.getReadErr())
	}
}

// notify sends a JSON-RPC notification (no id, no response expected).
func (c *acpClient) notify(method string, params any) error {
	msg := rpcNotification{
		JSONRPC: jsonrpcVersion,
		Method:  method,
		Params:  params,
	}
	if err := c.codec.send(msg); err != nil {
		return fmt.Errorf("acp: notify %s: %w", method, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Initialize handshake
// ---------------------------------------------------------------------------

// initialize performs the ACP initialize handshake:
//  1. Send "initialize" request with protocol version, capabilities, client info.
//  2. Wait for the server's "initialize" response.
func (c *acpClient) initialize(ctx context.Context) error {
	initCtx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()

	params := initializeParams{
		ProtocolVersion: acpProtocolVersion,
		ClientCapabilities: clientCapabilities{
			FS:       &struct{}{},
			Terminal: true,
		},
		ClientInfo: clientInfo{
			Name:    "ralfinho",
			Version: "1.0.0",
		},
	}

	_, err := c.call(initCtx, "initialize", params)
	if err != nil {
		return fmt.Errorf("acp: initialize handshake failed: %w", err)
	}

	// Send "initialized" notification per LSP-style protocol convention.
	if err := c.notify("initialized", nil); err != nil {
		return fmt.Errorf("acp: initialized notification: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Session update kinds
// ---------------------------------------------------------------------------

const (
	// updateKindAgentMessage is emitted when the agent produces a text chunk.
	updateKindAgentMessage = "agent_message_chunk"

	// updateKindToolCall is emitted when a tool call is created or completes.
	updateKindToolCall = "tool_call"

	// updateKindToolCallUpdate is emitted for tool call progress updates.
	updateKindToolCallUpdate = "tool_call_update"
)

// ---------------------------------------------------------------------------
// Session types
// ---------------------------------------------------------------------------

type sessionNewParams struct {
	CWD        string        `json:"cwd"`
	MCPServers []any `json:"mcpServers"`
}

type sessionNewResult struct {
	SessionID string `json:"sessionId"`
}

type sessionPromptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []events.ContentBlock `json:"prompt"`
}

// sessionUpdate represents a single update from a session/update notification.
// Kind identifies the update type (see updateKind* constants). Raw holds the
// full JSON of the update object for downstream parsing of kind-specific fields.
type sessionUpdate struct {
	Kind string
	Raw  json.RawMessage
}

// ---------------------------------------------------------------------------
// Session methods
// ---------------------------------------------------------------------------

// sessionNew creates a new ACP session with the given working directory.
// Returns the session ID assigned by the server.
func (c *acpClient) sessionNew(ctx context.Context, cwd string) (string, error) {
	resp, err := c.call(ctx, "session/new", sessionNewParams{CWD: cwd, MCPServers: []any{}})
	if err != nil {
		return "", fmt.Errorf("session/new: %w", err)
	}
	var result sessionNewResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("session/new: unmarshal result: %w", err)
	}
	if result.SessionID == "" {
		return "", fmt.Errorf("session/new: empty session ID in response")
	}
	return result.SessionID, nil
}

// sessionPrompt sends a prompt to the given session and streams updates
// back to the caller via onUpdate. Blocks until the prompt response arrives
// (signaling turn completion), the context is cancelled, or the connection
// is closed.
//
// The onUpdate callback is called synchronously for each update — it should
// not block.
//
// Note: permission requests (session/request_permission) arriving from the
// server during prompt execution must be handled separately by consuming
// from c.reverseReqs concurrently.
func (c *acpClient) sessionPrompt(ctx context.Context, sessionID, prompt string, onUpdate func(sessionUpdate)) error {
	params := sessionPromptParams{
		SessionID: sessionID,
		Prompt: []events.ContentBlock{
			{Type: "text", Text: prompt},
		},
	}

	// Build the request manually instead of using call() because we need
	// to process streaming notifications concurrently with waiting for the
	// final response.
	req := c.codec.newRequest("session/prompt", params)

	// Register a response channel to detect errors or completion.
	respCh := make(chan *rpcMessage, 1)
	c.pendingMu.Lock()
	if c.pending == nil {
		c.pendingMu.Unlock()
		return fmt.Errorf("acp: connection already closed")
	}
	c.pending[req.ID] = respCh
	c.pendingMu.Unlock()

	defer func() {
		c.pendingMu.Lock()
		if c.pending != nil {
			delete(c.pending, req.ID)
		}
		c.pendingMu.Unlock()
	}()

	if err := c.codec.send(req); err != nil {
		return fmt.Errorf("acp: send session/prompt: %w", err)
	}

	// Read events until prompt response, an error, or cancellation.
	// Turn completion is signaled by the prompt response (with stopReason),
	// not by a TurnEnd update.
	for {
		select {
		case msg := <-c.notifications:
			if msg.Method != "session/update" {
				continue // ignore unrelated notifications (_kiro.dev/*, etc.)
			}
			u, err := parseSessionUpdate(msg)
			if err != nil {
				continue // skip malformed updates
			}
			onUpdate(u)

		case msg, ok := <-respCh:
			if !ok {
				// Channel closed by readLoop — connection died.
				return fmt.Errorf("acp: connection closed during session/prompt: %v", c.getReadErr())
			}
			if msg.Error != nil {
				return fmt.Errorf("acp: session/prompt error %d: %s", msg.Error.Code, msg.Error.Message)
			}
			// Drain any remaining notifications that arrived before/with the response.
			for {
				select {
				case pending := <-c.notifications:
					if pending.Method != "session/update" {
						continue
					}
					if u, err := parseSessionUpdate(pending); err == nil {
						onUpdate(u)
					}
				default:
					return nil
				}
			}

		case <-ctx.Done():
			return ctx.Err()

		case <-c.done:
			return fmt.Errorf("acp: connection closed during session/prompt: %v", c.getReadErr())
		}
	}
}

// parseSessionUpdate extracts the single update object from a session/update
// notification's params. The update has a "sessionUpdate" field identifying
// the kind (e.g. "agent_message_chunk", "tool_call").
func parseSessionUpdate(msg *rpcMessage) (sessionUpdate, error) {
	if msg.Params == nil {
		return sessionUpdate{}, fmt.Errorf("notification has no params")
	}

	var params struct {
		Update json.RawMessage `json:"update"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return sessionUpdate{}, fmt.Errorf("unmarshal notification params: %w", err)
	}
	if params.Update == nil {
		return sessionUpdate{}, fmt.Errorf("notification has no update field")
	}

	var header struct {
		SessionUpdate string `json:"sessionUpdate"`
	}
	if err := json.Unmarshal(params.Update, &header); err != nil {
		return sessionUpdate{}, fmt.Errorf("unmarshal update header: %w", err)
	}
	if header.SessionUpdate == "" {
		return sessionUpdate{}, fmt.Errorf("update has no sessionUpdate field")
	}

	return sessionUpdate{Kind: header.SessionUpdate, Raw: params.Update}, nil
}

// ---------------------------------------------------------------------------
// Permission auto-approve handler
// ---------------------------------------------------------------------------

// autoApprovePermissions consumes reverse requests from the server and
// auto-approves all permission requests by responding with "allow_always".
//
// This should be run as a goroutine alongside sessionPrompt so that
// permission requests are handled while the prompt is streaming. It returns
// when ctx is cancelled or the connection is closed.
//
// Non-permission reverse requests are silently ignored (dropped). In
// ralfinho's design, the only expected reverse request type is
// session/request_permission.
func (c *acpClient) autoApprovePermissions(ctx context.Context) {
	for {
		select {
		case msg, ok := <-c.reverseReqs:
			if !ok {
				return // channel closed
			}
			if msg.Method == "session/request_permission" {
				fmt.Fprintf(c.logWriter, "acp: auto-approved permission: %s (id=%s)\n", msg.Method, string(msg.ID))
				resp := newResponse(msg.ID, "allow_always")
				if err := c.codec.send(resp); err != nil {
					// Log error but don't crash — connection is likely dying
					fmt.Fprintf(c.logWriter, "acp: warning: failed to send permission response: %v\n", err)
				}
			}

		case <-ctx.Done():
			return

		case <-c.done:
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Teardown
// ---------------------------------------------------------------------------

// Close terminates the kiro-cli subprocess (and all its children) and waits
// for cleanup. Safe to call multiple times.
func (c *acpClient) Close() error {
	c.closeOnce.Do(func() {
		if c.cmd != nil && c.cmd.Process != nil {
			// Kill the entire process group. kiro-cli spawns child processes
			// that inherit the stdout pipe; killing only the parent leaves
			// the pipe open and cmd.Wait() hangs.
			if err := syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
				fmt.Fprintf(c.logWriter, "acp: warning: failed to kill process group: %v\n", err)
			}
		}
		// Collect the process exit status (may return "signal: killed").
		if c.cmd != nil {
			c.closeErr = c.cmd.Wait()
			if c.closeErr != nil {
				if stderr := c.stderrBuf.String(); stderr != "" {
					c.closeErr = fmt.Errorf("%w\nkiro-cli stderr: %s", c.closeErr, stderr)
				}
			}
		}
		// Wait for the read goroutine to fully exit.
		<-c.done
	})
	return c.closeErr
}
