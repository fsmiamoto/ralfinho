// acp.go implements the ACP (Agent Communication Protocol) client that manages
// a kiro-cli subprocess communicating via JSON-RPC 2.0 over stdio.
//
// The acpClient handles:
//   - Spawning `kiro-cli acp` and wiring stdin/stdout to a JSON-RPC codec.
//   - A read goroutine that dispatches incoming messages by type:
//     responses → per-request channels, notifications → Notifications channel,
//     reverse requests → ReverseReqs channel.
//   - The initialize handshake (protocolVersion, capabilities, clientInfo).
//   - Clean teardown (process kill + wait + drain read goroutine).
//
// Higher-level methods (session/new, session/prompt) are added in later tasks.
package agent

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
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
	FS       *struct{} `json:"fs"`
	Terminal *struct{} `json:"terminal"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
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
//   - Notifications (server → client, no id) go to the Notifications channel.
//   - Reverse requests (server → client, has id+method) go to ReverseReqs.
//     Task 4 wires the auto-approve handler that consumes from this channel.
type acpClient struct {
	cmd   *exec.Cmd
	codec *rpcCodec

	// pending maps request IDs to channels waiting for the response.
	// Protected by pendingMu. Set to nil when the read loop exits to signal
	// that no more responses will arrive.
	pending   map[int64]chan<- *rpcMessage
	pendingMu sync.Mutex

	// Notifications receives server-initiated notifications (e.g.
	// session/notification events during prompt execution). Buffered to
	// avoid blocking the read loop.
	Notifications chan *rpcMessage

	// ReverseReqs receives server-initiated requests that expect a response
	// (e.g. session/request_permission). A separate goroutine should consume
	// from this channel and reply via the codec.
	ReverseReqs chan *rpcMessage

	// done is closed when the read goroutine exits. Any pending call()
	// waiters are unblocked.
	done    chan struct{}
	readErr error // the error that terminated the read loop (often io.EOF)

	rawWriter io.Writer
}

// newACPClient spawns `kiro-cli acp`, performs the ACP initialize handshake,
// and returns a ready-to-use client. The caller must call Close() when done.
//
// If rawWriter is non-nil, raw JSON-RPC messages from stdout are tee'd to it
// for debugging (raw-output.log).
func newACPClient(ctx context.Context, rawWriter io.Writer) (*acpClient, error) {
	cmd := exec.CommandContext(ctx, "kiro-cli", "acp")
	cmd.Stderr = io.Discard // don't mix kiro stderr with ralfinho output

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
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
		Notifications: make(chan *rpcMessage, 128),
		ReverseReqs:   make(chan *rpcMessage, 16),
		done:          make(chan struct{}),
		rawWriter:     rawWriter,
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
			c.readErr = err

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
			case c.Notifications <- msg:
			default:
				// Drop if buffer is full. In practice 128 is more than
				// enough for streaming text chunks between reads.
			}

		case msg.IsReverseRequest():
			select {
			case c.ReverseReqs <- msg:
			default:
				// Drop if buffer is full — shouldn't happen since the
				// permission handler should consume promptly.
			}
		}
	}
}

// ---------------------------------------------------------------------------
// RPC helpers
// ---------------------------------------------------------------------------

// call sends a JSON-RPC request and blocks until the matching response arrives,
// the context is cancelled, or the connection is closed.
func (c *acpClient) call(ctx context.Context, method string, params interface{}) (*rpcMessage, error) {
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
			return nil, fmt.Errorf("acp: connection closed while waiting for %s response: %v", method, c.readErr)
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("acp: %s: server error %d: %s", method, msg.Error.Code, msg.Error.Message)
		}
		return msg, nil

	case <-ctx.Done():
		return nil, ctx.Err()

	case <-c.done:
		return nil, fmt.Errorf("acp: connection closed while waiting for %s response: %v", method, c.readErr)
	}
}

// notify sends a JSON-RPC notification (no id, no response expected).
func (c *acpClient) notify(method string, params interface{}) error {
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
//  3. Send "initialized" notification to signal the handshake is complete.
func (c *acpClient) initialize(ctx context.Context) error {
	initCtx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()

	params := initializeParams{
		ProtocolVersion: acpProtocolVersion,
		ClientCapabilities: clientCapabilities{
			FS:       &struct{}{},
			Terminal: &struct{}{},
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

	// Complete the handshake by sending the "initialized" notification.
	if err := c.notify("initialized", nil); err != nil {
		return fmt.Errorf("acp: initialized notification failed: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Teardown
// ---------------------------------------------------------------------------

// Close terminates the kiro-cli subprocess and waits for the read goroutine
// to drain. Safe to call multiple times.
func (c *acpClient) Close() error {
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	// Wait collects the process exit status (may return "signal: killed").
	err := c.cmd.Wait()
	// Block until the read goroutine has fully exited.
	<-c.done
	return err
}
