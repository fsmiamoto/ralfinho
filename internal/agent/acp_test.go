package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// waitFor polls fn until it returns true or timeout is reached.
func waitFor(t *testing.T, timeout time.Duration, interval time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(interval)
	}
	t.Fatal("waitFor: condition not met within timeout")
}

// waitForPending polls until c.pending has at least one entry.
func waitForPending(t *testing.T, c *acpClient) {
	t.Helper()
	waitFor(t, 2*time.Second, 5*time.Millisecond, func() bool {
		c.pendingMu.Lock()
		defer c.pendingMu.Unlock()
		return len(c.pending) > 0
	})
}

// waitForDrainBuf polls until drainBuf has at least minBytes bytes.
func waitForDrainBuf(t *testing.T, drainBuf *safeBuffer, minBytes int) {
	t.Helper()
	waitFor(t, 2*time.Second, 5*time.Millisecond, func() bool {
		return len(drainBuf.Bytes()) >= minBytes
	})
}

// mockACPClient builds an acpClient wired to in-memory pipes instead of a real
// subprocess. Returns the client, a writer to simulate kiro→client messages,
// and a function to stop the drain goroutine.
//
// Writes from client→kiro are drained into a buffer accessible via the
// returned drainBuf. This prevents send() from blocking on synchronous pipes.
func mockACPClient(t *testing.T) (c *acpClient, serverW io.WriteCloser, drainBuf *safeBuffer, cleanup func()) {
	t.Helper()

	// kiro stdout → client (client reads from here)
	serverToClientR, serverToClientW := io.Pipe()
	// client → kiro stdin (client writes here)
	clientToServerR, clientToServerW := io.Pipe()

	c = &acpClient{
		codec:         newRPCCodec(serverToClientR, clientToServerW),
		pending:       make(map[int64]chan<- *rpcMessage),
		notifications: make(chan *rpcMessage, 128),
		reverseReqs:   make(chan *rpcMessage, 16),
		done:          make(chan struct{}),
		logWriter:     io.Discard,
	}
	go c.readLoop()

	// Drain client→server pipe in background so send() doesn't block.
	buf := &safeBuffer{}
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		io.Copy(buf, clientToServerR)
	}()

	cleanup = func() {
		serverToClientW.Close()
		clientToServerW.Close()
		<-c.done
		clientToServerR.Close()
		<-drainDone
	}

	return c, serverToClientW, buf, cleanup
}

// safeBuffer is a bytes.Buffer protected by a mutex for concurrent access.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buf.Bytes())
}

// writeJSONLine writes a newline-delimited JSON-RPC message to w.
func writeJSONLine(w io.Writer, body []byte) error {
	if _, err := w.Write(body); err != nil {
		return err
	}
	_, err := w.Write([]byte("\n"))
	return err
}

func TestACPClient_CallResponse(t *testing.T) {
	c, serverW, _, cleanup := mockACPClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	type callResult struct {
		msg *rpcMessage
		err error
	}
	ch := make(chan callResult, 1)
	go func() {
		msg, err := c.call(ctx, "test/method", map[string]string{"key": "value"})
		ch <- callResult{msg, err}
	}()

	// Wait for the call to register in c.pending.
	waitForPending(t, c)

	// Simulate server sending a response with id=1 (first auto-incremented ID).
	resp := `{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}`
	if err := writeJSONLine(serverW, []byte(resp)); err != nil {
		t.Fatalf("writeJSONLine: %v", err)
	}

	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("call returned error: %v", r.err)
		}
		if r.msg.Result == nil {
			t.Fatal("expected non-nil result")
		}
		var result map[string]string
		if err := json.Unmarshal(r.msg.Result, &result); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		if result["status"] != "ok" {
			t.Errorf("expected status=ok, got %q", result["status"])
		}
	case <-ctx.Done():
		t.Fatal("call timed out")
	}
}

func TestACPClient_CallError(t *testing.T) {
	c, serverW, _, cleanup := mockACPClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch := make(chan error, 1)
	go func() {
		_, err := c.call(ctx, "test/error", nil)
		ch <- err
	}()

	// Wait for the call to register in c.pending.
	waitForPending(t, c)

	resp := `{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"Invalid Request"}}`
	if err := writeJSONLine(serverW, []byte(resp)); err != nil {
		t.Fatalf("writeJSONLine: %v", err)
	}

	select {
	case err := <-ch:
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "Invalid Request") {
			t.Errorf("error should contain 'Invalid Request', got: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("call timed out")
	}
}

func TestACPClient_NotificationDispatch(t *testing.T) {
	c, serverW, _, cleanup := mockACPClient(t)
	defer cleanup()

	notif := `{"jsonrpc":"2.0","method":"session/notification","params":{"kind":"test"}}`
	if err := writeJSONLine(serverW, []byte(notif)); err != nil {
		t.Fatalf("writeJSONLine: %v", err)
	}

	select {
	case msg := <-c.notifications:
		if msg.Method != "session/notification" {
			t.Errorf("expected method session/notification, got %q", msg.Method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notification not received")
	}
}

func TestACPClient_ReverseRequestDispatch(t *testing.T) {
	c, serverW, _, cleanup := mockACPClient(t)
	defer cleanup()

	req := `{"jsonrpc":"2.0","id":99,"method":"session/request_permission","params":{"permission":"fs_write"}}`
	if err := writeJSONLine(serverW, []byte(req)); err != nil {
		t.Fatalf("writeJSONLine: %v", err)
	}

	select {
	case msg := <-c.reverseReqs:
		if msg.Method != "session/request_permission" {
			t.Errorf("expected method session/request_permission, got %q", msg.Method)
		}
		resp := newResponse(msg.ID, "allow_always")
		if resp.JSONRPC != jsonrpcVersion {
			t.Errorf("expected jsonrpc %q, got %q", jsonrpcVersion, resp.JSONRPC)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reverse request not received")
	}
}

func TestACPClient_CallContextCancellation(t *testing.T) {
	c, _, _, cleanup := mockACPClient(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())

	ch := make(chan error, 1)
	go func() {
		_, err := c.call(ctx, "test/cancelled", nil)
		ch <- err
	}()

	// Wait for the call to register in c.pending before cancelling.
	waitForPending(t, c)
	cancel()

	select {
	case err := <-ch:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("call did not return after context cancellation")
	}
}

func TestACPClient_ConnectionClosed(t *testing.T) {
	c, serverW, _, cleanup := mockACPClient(t)
	// Don't defer cleanup — we manually close serverW mid-test.

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch := make(chan error, 1)
	go func() {
		_, err := c.call(ctx, "test/closed", nil)
		ch <- err
	}()

	// Wait for the call to register in c.pending before closing.
	waitForPending(t, c)
	serverW.Close() // simulate kiro process exit

	select {
	case err := <-ch:
		if err == nil {
			t.Fatal("expected error on connection close, got nil")
		}
		if !strings.Contains(err.Error(), "connection closed") {
			t.Errorf("error should mention 'connection closed', got: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("call did not return after connection close")
	}

	cleanup()
}

// ---------------------------------------------------------------------------
// Session method tests
// ---------------------------------------------------------------------------

func TestACPClient_SessionNew(t *testing.T) {
	c, serverW, _, cleanup := mockACPClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	type result struct {
		id  string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		id, err := c.sessionNew(ctx, "/workspace")
		ch <- result{id, err}
	}()

	// Wait for the call to register in c.pending.
	waitForPending(t, c)

	// Server responds with a session ID.
	resp := `{"jsonrpc":"2.0","id":1,"result":{"sessionId":"sess-abc-123"}}`
	if err := writeJSONLine(serverW, []byte(resp)); err != nil {
		t.Fatalf("writeJSONLine: %v", err)
	}

	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("sessionNew returned error: %v", r.err)
		}
		if r.id != "sess-abc-123" {
			t.Errorf("expected session ID %q, got %q", "sess-abc-123", r.id)
		}
	case <-ctx.Done():
		t.Fatal("sessionNew timed out")
	}
}

func TestACPClient_SessionNew_EmptyID(t *testing.T) {
	c, serverW, _, cleanup := mockACPClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch := make(chan error, 1)
	go func() {
		_, err := c.sessionNew(ctx, "/workspace")
		ch <- err
	}()

	// Wait for the call to register in c.pending.
	waitForPending(t, c)

	// Server responds with an empty session ID.
	resp := `{"jsonrpc":"2.0","id":1,"result":{"sessionId":""}}`
	if err := writeJSONLine(serverW, []byte(resp)); err != nil {
		t.Fatalf("writeJSONLine: %v", err)
	}

	select {
	case err := <-ch:
		if err == nil {
			t.Fatal("expected error for empty session ID, got nil")
		}
		if !strings.Contains(err.Error(), "empty session ID") {
			t.Errorf("error should mention 'empty session ID', got: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("sessionNew timed out")
	}
}

func TestACPClient_SessionPrompt(t *testing.T) {
	c, serverW, _, cleanup := mockACPClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var received []sessionUpdate
	var mu sync.Mutex

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.sessionPrompt(ctx, "sess-123", "hello world", func(u sessionUpdate) {
			mu.Lock()
			received = append(received, u)
			mu.Unlock()
		})
	}()

	// Wait for the call to register in c.pending.
	waitForPending(t, c)

	// Send agent_message_chunk update.
	notif1 := `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess-123","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Hello "}}}}`
	if err := writeJSONLine(serverW, []byte(notif1)); err != nil {
		t.Fatalf("writeJSONLine notif1: %v", err)
	}

	// Send tool_call update.
	notif2 := `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess-123","update":{"sessionUpdate":"tool_call","toolCallId":"tc1","title":"read_file","status":"completed"}}}`
	if err := writeJSONLine(serverW, []byte(notif2)); err != nil {
		t.Fatalf("writeJSONLine notif2: %v", err)
	}

	// Send prompt response to signal turn completion.
	resp := `{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}`
	if err := writeJSONLine(serverW, []byte(resp)); err != nil {
		t.Fatalf("writeJSONLine resp: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("sessionPrompt returned error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("sessionPrompt timed out")
	}

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(received))
	}
	if received[0].Kind != updateKindAgentMessage {
		t.Errorf("update 0: expected %s, got %s", updateKindAgentMessage, received[0].Kind)
	}
	if received[1].Kind != updateKindToolCall {
		t.Errorf("update 1: expected %s, got %s", updateKindToolCall, received[1].Kind)
	}
}

func TestACPClient_SessionPrompt_ResponseCompletesPrompt(t *testing.T) {
	c, serverW, _, cleanup := mockACPClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var received []sessionUpdate
	var mu sync.Mutex

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.sessionPrompt(ctx, "sess-123", "test", func(u sessionUpdate) {
			mu.Lock()
			received = append(received, u)
			mu.Unlock()
		})
	}()

	// Wait for the call to register in c.pending.
	waitForPending(t, c)

	// Send an update followed by the prompt response.
	notif := `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess-123","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"done"}}}}`
	if err := writeJSONLine(serverW, []byte(notif)); err != nil {
		t.Fatalf("writeJSONLine: %v", err)
	}

	// Wait for the notification to be consumed before sending the response.
	waitFor(t, 2*time.Second, 5*time.Millisecond, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(received) >= 1
	})

	resp := `{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}`
	if err := writeJSONLine(serverW, []byte(resp)); err != nil {
		t.Fatalf("writeJSONLine resp: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("sessionPrompt returned error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("sessionPrompt timed out")
	}

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("expected 1 update, got %d", len(received))
	}
	if received[0].Kind != updateKindAgentMessage {
		t.Errorf("update 0: expected %s, got %s", updateKindAgentMessage, received[0].Kind)
	}
}

func TestACPClient_SessionPromptError(t *testing.T) {
	c, serverW, _, cleanup := mockACPClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.sessionPrompt(ctx, "sess-bad", "hello", func(u sessionUpdate) {})
	}()

	// Wait for the call to register in c.pending.
	waitForPending(t, c)

	// Server responds with an error to the session/prompt request.
	resp := `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"session not found"}}`
	if err := writeJSONLine(serverW, []byte(resp)); err != nil {
		t.Fatalf("writeJSONLine: %v", err)
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "session not found") {
			t.Errorf("error should contain 'session not found', got: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("sessionPrompt did not return after error")
	}
}

func TestACPClient_SessionPrompt_ConnectionClose(t *testing.T) {
	c, serverW, _, cleanup := mockACPClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.sessionPrompt(ctx, "sess-123", "hello", func(u sessionUpdate) {})
	}()

	// Wait for the call to register in c.pending.
	waitForPending(t, c)

	// Close the server writer to simulate kiro process exit.
	serverW.Close()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error on connection close, got nil")
		}
		if !strings.Contains(err.Error(), "connection closed") {
			t.Errorf("error should mention 'connection closed', got: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("sessionPrompt did not return after connection close")
	}

	cleanup()
}

// ---------------------------------------------------------------------------
// parseSessionUpdate tests
// ---------------------------------------------------------------------------

func TestParseSessionUpdate(t *testing.T) {
	tests := []struct {
		name     string
		params   string
		wantKind string
		wantErr  bool
	}{
		{
			name:     "agent_message_chunk",
			params:   `{"sessionId":"s1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hi"}}}`,
			wantKind: "agent_message_chunk",
		},
		{
			name:     "tool_call",
			params:   `{"sessionId":"s1","update":{"sessionUpdate":"tool_call","toolCallId":"tc1","title":"ls","status":"in_progress"}}`,
			wantKind: "tool_call",
		},
		{
			name:    "nil params",
			params:  "",
			wantErr: true,
		},
		{
			name:    "no update field",
			params:  `{"sessionId":"s1"}`,
			wantErr: true,
		},
		{
			name:    "empty sessionUpdate",
			params:  `{"sessionId":"s1","update":{"sessionUpdate":""}}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &rpcMessage{}
			if tt.params != "" {
				msg.Params = json.RawMessage(tt.params)
			}
			u, err := parseSessionUpdate(msg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if u.Kind != tt.wantKind {
				t.Errorf("expected kind %q, got %q", tt.wantKind, u.Kind)
			}
		})
	}
}

func TestParseSessionUpdate_RawPreserved(t *testing.T) {
	params := `{"sessionId":"s1","update":{"sessionUpdate":"tool_call","toolCallId":"tc1","title":"bash","rawInput":{"command":"ls"}}}`
	msg := &rpcMessage{Params: json.RawMessage(params)}

	u, err := parseSessionUpdate(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the Raw field contains the full JSON of the update object.
	var raw map[string]interface{}
	if err := json.Unmarshal(u.Raw, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if raw["title"] != "bash" {
		t.Errorf("expected title=bash, got %v", raw["title"])
	}
	if raw["sessionUpdate"] != "tool_call" {
		t.Errorf("expected sessionUpdate=tool_call in raw, got %v", raw["sessionUpdate"])
	}
}

// ---------------------------------------------------------------------------
// Permission auto-approve tests
// ---------------------------------------------------------------------------

func TestACPClient_AutoApprovePermissions(t *testing.T) {
	c, serverW, drainBuf, cleanup := mockACPClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start the auto-approve handler.
	go c.autoApprovePermissions(ctx)

	// Simulate kiro sending a permission request.
	req := `{"jsonrpc":"2.0","id":42,"method":"session/request_permission","params":{"permission":"fs_write","path":"/foo"}}`
	if err := writeJSONLine(serverW, []byte(req)); err != nil {
		t.Fatalf("writeJSONLine: %v", err)
	}

	// Wait for the handler to process and write the response.
	waitForDrainBuf(t, drainBuf, 1)

	// Read the response from the drain buffer.
	captured := drainBuf.Bytes()
	codec := newRPCCodec(bytes.NewReader(captured), io.Discard)
	msg, err := codec.readMessage()
	if err != nil {
		t.Fatalf("readMessage from captured bytes: %v", err)
	}

	// Verify the response has the correct ID (42).
	id, ok := rpcIDInt(msg.ID)
	if !ok {
		t.Fatal("response should have a numeric ID")
	}
	if id != 42 {
		t.Errorf("expected response ID 42, got %d", id)
	}

	// Verify the result is "allow_always".
	var result string
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result != "allow_always" {
		t.Errorf("expected result %q, got %q", "allow_always", result)
	}
}

func TestACPClient_AutoApprovePermissions_Multiple(t *testing.T) {
	c, serverW, drainBuf, cleanup := mockACPClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go c.autoApprovePermissions(ctx)

	// Send three permission requests with different IDs.
	for _, id := range []int{10, 20, 30} {
		req := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"session/request_permission","params":{"permission":"bash"}}`, id)
		if err := writeJSONLine(serverW, []byte(req)); err != nil {
			t.Fatalf("writeJSONLine id=%d: %v", id, err)
		}
	}

	// Wait until all three responses are in the drain buffer.
	// We poll by trying to parse 3 messages from the captured bytes.
	waitFor(t, 2*time.Second, 5*time.Millisecond, func() bool {
		captured := drainBuf.Bytes()
		codec := newRPCCodec(bytes.NewReader(captured), io.Discard)
		count := 0
		for {
			_, err := codec.readMessage()
			if err != nil {
				break
			}
			count++
		}
		return count >= 3
	})

	// Read all three responses.
	captured := drainBuf.Bytes()
	codec := newRPCCodec(bytes.NewReader(captured), io.Discard)

	gotIDs := make(map[int64]bool)
	for i := 0; i < 3; i++ {
		msg, err := codec.readMessage()
		if err != nil {
			t.Fatalf("readMessage %d: %v", i, err)
		}
		id, ok := rpcIDInt(msg.ID)
		if !ok {
			t.Fatalf("response %d: expected numeric ID", i)
		}
		gotIDs[id] = true

		var result string
		if err := json.Unmarshal(msg.Result, &result); err != nil {
			t.Fatalf("response %d: unmarshal result: %v", i, err)
		}
		if result != "allow_always" {
			t.Errorf("response %d: expected %q, got %q", i, "allow_always", result)
		}
	}

	// Verify all three IDs were responded to.
	for _, id := range []int64{10, 20, 30} {
		if !gotIDs[id] {
			t.Errorf("missing response for ID %d", id)
		}
	}
}

func TestACPClient_AutoApprovePermissions_IgnoresOtherMethods(t *testing.T) {
	c, serverW, drainBuf, cleanup := mockACPClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go c.autoApprovePermissions(ctx)

	// Send a reverse request that is NOT a permission request.
	req := `{"jsonrpc":"2.0","id":99,"method":"session/some_other_thing","params":{}}`
	if err := writeJSONLine(serverW, []byte(req)); err != nil {
		t.Fatalf("writeJSONLine: %v", err)
	}

	// For a negative test (nothing should happen), we need a brief wait.
	// We send a follow-up permission request and wait for THAT response,
	// which proves the first non-permission request was processed (skipped).
	followUp := `{"jsonrpc":"2.0","id":100,"method":"session/request_permission","params":{"permission":"bash"}}`
	if err := writeJSONLine(serverW, []byte(followUp)); err != nil {
		t.Fatalf("writeJSONLine follow-up: %v", err)
	}

	// Wait for the follow-up response to appear.
	waitForDrainBuf(t, drainBuf, 1)

	// Verify only one response was sent (the follow-up, not the other method).
	captured := drainBuf.Bytes()
	codec := newRPCCodec(bytes.NewReader(captured), io.Discard)

	msg, err := codec.readMessage()
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	id, ok := rpcIDInt(msg.ID)
	if !ok {
		t.Fatal("expected numeric ID")
	}
	if id != 100 {
		t.Errorf("expected response ID 100 (follow-up), got %d", id)
	}

	// Ensure no second message exists (the non-permission request should have been ignored).
	_, err = codec.readMessage()
	if err == nil {
		t.Error("expected no more messages, but got another response")
	}
}

func TestACPClient_AutoApprovePermissions_ContextCancel(t *testing.T) {
	c, _, _, cleanup := mockACPClient(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.autoApprovePermissions(ctx)
		close(done)
	}()

	// Cancel the context and verify the handler exits promptly.
	cancel()

	select {
	case <-done:
		// Handler exited as expected.
	case <-time.After(2 * time.Second):
		t.Fatal("autoApprovePermissions did not exit after context cancellation")
	}
}

func TestACPClient_AutoApprovePermissions_WithSessionPrompt(t *testing.T) {
	c, serverW, drainBuf, cleanup := mockACPClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start auto-approve handler concurrently.
	go c.autoApprovePermissions(ctx)

	var received []sessionUpdate
	var mu sync.Mutex

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.sessionPrompt(ctx, "sess-perm", "do something", func(u sessionUpdate) {
			mu.Lock()
			received = append(received, u)
			mu.Unlock()
		})
	}()

	// Wait for the call to register in c.pending.
	waitForPending(t, c)

	// Simulate: kiro sends an agent message chunk.
	notif1 := `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess-perm","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Working..."}}}}`
	if err := writeJSONLine(serverW, []byte(notif1)); err != nil {
		t.Fatalf("writeJSONLine notif1: %v", err)
	}

	// Simulate: kiro sends a permission request mid-stream.
	permReq := `{"jsonrpc":"2.0","id":77,"method":"session/request_permission","params":{"permission":"fs_write"}}`
	if err := writeJSONLine(serverW, []byte(permReq)); err != nil {
		t.Fatalf("writeJSONLine perm: %v", err)
	}

	// Wait for auto-approve to write the permission response.
	// The drainBuf will contain the session/prompt request + the permission response.
	// We need at least 2 messages.
	waitFor(t, 2*time.Second, 5*time.Millisecond, func() bool {
		captured := drainBuf.Bytes()
		codec := newRPCCodec(bytes.NewReader(captured), io.Discard)
		count := 0
		for {
			_, err := codec.readMessage()
			if err != nil {
				break
			}
			count++
		}
		return count >= 2
	})

	// Simulate: kiro continues with more output after permission was granted.
	notif2 := `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess-perm","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Done."}}}}`
	if err := writeJSONLine(serverW, []byte(notif2)); err != nil {
		t.Fatalf("writeJSONLine notif2: %v", err)
	}

	// Send prompt response to signal turn completion.
	promptResp := `{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}`
	if err := writeJSONLine(serverW, []byte(promptResp)); err != nil {
		t.Fatalf("writeJSONLine resp: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("sessionPrompt returned error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("sessionPrompt timed out")
	}

	// Verify the permission request was auto-approved.
	captured := drainBuf.Bytes()
	codec := newRPCCodec(bytes.NewReader(captured), io.Discard)

	// Read messages from captured — first is the session/prompt request, then the permission response.
	foundApproval := false
	for {
		msg, err := codec.readMessage()
		if err != nil {
			break
		}
		// Look for the permission response (id=77).
		if msg.Result != nil {
			id, ok := rpcIDInt(msg.ID)
			if ok && id == 77 {
				var result string
				if err := json.Unmarshal(msg.Result, &result); err == nil && result == "allow_always" {
					foundApproval = true
				}
			}
		}
	}

	if !foundApproval {
		t.Error("expected permission auto-approval response with id=77, not found in captured output")
	}

	// Verify all notification updates were received.
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Fatalf("expected 2 updates, got %d", len(received))
	}
	if received[0].Kind != updateKindAgentMessage {
		t.Errorf("update 0: expected %s, got %s", updateKindAgentMessage, received[0].Kind)
	}
	if received[1].Kind != updateKindAgentMessage {
		t.Errorf("update 1: expected %s, got %s", updateKindAgentMessage, received[1].Kind)
	}
}

// ---------------------------------------------------------------------------
// Error handling tests
// ---------------------------------------------------------------------------

func TestACPClient_ReadLoop_SkipsMalformedJSON(t *testing.T) {
	c, serverW, _, cleanup := mockACPClient(t)
	defer cleanup()

	// Send a properly framed message with invalid JSON body.
	// The Content-Length framing is correct, but the JSON is garbage.
	badBody := []byte(`{this is not valid json!!!}`)
	if err := writeJSONLine(serverW, badBody); err != nil {
		t.Fatalf("writeJSONLine (bad): %v", err)
	}

	// Send a valid notification after the malformed one.
	validNotif := `{"jsonrpc":"2.0","method":"test/survived","params":{"ok":true}}`
	if err := writeJSONLine(serverW, []byte(validNotif)); err != nil {
		t.Fatalf("writeJSONLine (good): %v", err)
	}

	// The readLoop should have skipped the malformed message and delivered
	// the valid notification.
	select {
	case msg := <-c.notifications:
		if msg.Method != "test/survived" {
			t.Errorf("expected method test/survived, got %q", msg.Method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("valid notification not received after malformed message — readLoop may have terminated")
	}
}

func TestACPClient_ReadLoop_SkipsMultipleMalformed(t *testing.T) {
	c, serverW, _, cleanup := mockACPClient(t)
	defer cleanup()

	// Send several malformed messages in a row.
	for i := 0; i < 5; i++ {
		badBody := []byte(fmt.Sprintf(`{bad json #%d}`, i))
		if err := writeJSONLine(serverW, badBody); err != nil {
			t.Fatalf("writeJSONLine bad %d: %v", i, err)
		}
	}

	// Then send a valid notification.
	validNotif := `{"jsonrpc":"2.0","method":"test/still-alive","params":{}}`
	if err := writeJSONLine(serverW, []byte(validNotif)); err != nil {
		t.Fatalf("writeJSONLine (good): %v", err)
	}

	select {
	case msg := <-c.notifications:
		if msg.Method != "test/still-alive" {
			t.Errorf("expected method test/still-alive, got %q", msg.Method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("valid notification not received after multiple malformed messages")
	}
}

func TestACPClient_ReadLoop_IOErrorStillTerminates(t *testing.T) {
	c, serverW, _, cleanup := mockACPClient(t)

	// Close the server writer to trigger an I/O error (EOF) in readLoop.
	serverW.Close()

	// The readLoop should terminate (close c.done).
	select {
	case <-c.done:
		// Good — I/O errors still terminate the readLoop.
	case <-time.After(2 * time.Second):
		t.Fatal("readLoop did not terminate on I/O error")
	}

	cleanup()
}

func TestNewACPClient_KiroNotFound(t *testing.T) {
	// Verify that attempting to spawn a non-existent binary produces
	// an actionable error message.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Temporarily override PATH so kiro-cli definitely won't be found,
	// even if it happens to be installed on the test machine.
	t.Setenv("PATH", "/nonexistent-dir-for-test")

	_, err := newACPClient(ctx, nil, io.Discard, nil)
	if err == nil {
		t.Fatal("expected error when kiro-cli is not in PATH, got nil")
	}
	if !strings.Contains(err.Error(), "kiro-cli not found") {
		t.Errorf("expected error to contain 'kiro-cli not found', got: %v", err)
	}
	if !strings.Contains(err.Error(), "https://kiro.dev/cli/") {
		t.Errorf("expected error to contain install URL, got: %v", err)
	}
}

func TestACPClient_SessionPrompt_ProcessCrash(t *testing.T) {
	c, serverW, _, cleanup := mockACPClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start a session prompt that will be interrupted by process crash.
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.sessionPrompt(ctx, "sess-crash", "hello", func(u sessionUpdate) {})
	}()

	// Wait for the call to register in c.pending.
	waitForPending(t, c)

	// Simulate process crash by closing the server writer (broken pipe/EOF).
	serverW.Close()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error on process crash, got nil")
		}
		// Should mention connection closed.
		if !strings.Contains(err.Error(), "connection closed") {
			t.Errorf("error should mention 'connection closed', got: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("sessionPrompt did not return after process crash")
	}

	cleanup()
}

// ---------------------------------------------------------------------------
// Existing tests
// ---------------------------------------------------------------------------

func TestACPClient_Notify(t *testing.T) {
	c, _, drainBuf, cleanup := mockACPClient(t)
	defer cleanup()

	if err := c.notify("initialized", nil); err != nil {
		t.Fatalf("notify: %v", err)
	}

	// Wait for the drain goroutine to capture the bytes.
	waitForDrainBuf(t, drainBuf, 1)

	// Parse what was captured and verify it's a proper notification.
	captured := drainBuf.Bytes()
	codec := newRPCCodec(bytes.NewReader(captured), io.Discard)
	msg, err := codec.readMessage()
	if err != nil {
		t.Fatalf("readMessage from captured bytes: %v", err)
	}
	if msg.Method != "initialized" {
		t.Errorf("expected method 'initialized', got %q", msg.Method)
	}
	if msg.ID != nil {
		t.Error("notification should not have an id")
	}
}

// ---------------------------------------------------------------------------
// limitedBuffer tests
// ---------------------------------------------------------------------------

func TestLimitedBuffer_Empty(t *testing.T) {
	b := newLimitedBuffer(16)
	if got := b.String(); got != "" {
		t.Errorf("empty buffer: String() = %q, want %q", got, "")
	}
}

func TestLimitedBuffer_WriteFitsInBuffer(t *testing.T) {
	b := newLimitedBuffer(16)
	n, err := b.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 5 {
		t.Errorf("Write returned n=%d, want 5", n)
	}
	if got := b.String(); got != "hello" {
		t.Errorf("String() = %q, want %q", got, "hello")
	}
}

func TestLimitedBuffer_MultipleWritesFitInBuffer(t *testing.T) {
	b := newLimitedBuffer(16)
	b.Write([]byte("hello"))
	b.Write([]byte(" "))
	b.Write([]byte("world"))
	if got := b.String(); got != "hello world" {
		t.Errorf("String() = %q, want %q", got, "hello world")
	}
}

func TestLimitedBuffer_ExactlyFillsBuffer(t *testing.T) {
	b := newLimitedBuffer(5)
	b.Write([]byte("abcde"))
	if got := b.String(); got != "abcde" {
		t.Errorf("String() = %q, want %q", got, "abcde")
	}
}

func TestLimitedBuffer_Wraps_KeepsLastNBytes(t *testing.T) {
	b := newLimitedBuffer(5)
	b.Write([]byte("abcdefgh"))
	// Buffer holds last 5 bytes: "defgh"
	if got := b.String(); got != "defgh" {
		t.Errorf("String() = %q, want %q", got, "defgh")
	}
}

func TestLimitedBuffer_WrapsMultipleTimes(t *testing.T) {
	b := newLimitedBuffer(4)
	// Write more than 2x the buffer size to wrap multiple times.
	b.Write([]byte("abcdefghijklm"))
	// Buffer holds last 4 bytes: "jklm"
	if got := b.String(); got != "jklm" {
		t.Errorf("String() = %q, want %q", got, "jklm")
	}
}

func TestLimitedBuffer_IncrementalWrapAcrossWrites(t *testing.T) {
	b := newLimitedBuffer(6)
	b.Write([]byte("abcd")) // 4 bytes, no wrap yet
	b.Write([]byte("efgh")) // wraps: buffer now holds "efgh" in last 6 = "cdefgh"
	if got := b.String(); got != "cdefgh" {
		t.Errorf("String() = %q, want %q", got, "cdefgh")
	}
}

func TestLimitedBuffer_SingleByteWrites(t *testing.T) {
	b := newLimitedBuffer(3)
	for _, c := range []byte("abcde") {
		b.Write([]byte{c})
	}
	// Last 3 bytes: "cde"
	if got := b.String(); got != "cde" {
		t.Errorf("String() = %q, want %q", got, "cde")
	}
}

func TestLimitedBuffer_WriteLargerThanBuffer(t *testing.T) {
	b := newLimitedBuffer(3)
	n, err := b.Write([]byte("hello world"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != 11 {
		t.Errorf("Write returned n=%d, want 11", n)
	}
	// Last 3 bytes: "rld"
	if got := b.String(); got != "rld" {
		t.Errorf("String() = %q, want %q", got, "rld")
	}
}

func TestLimitedBuffer_ImplementsIOWriter(t *testing.T) {
	b := newLimitedBuffer(64)
	// Verify it satisfies io.Writer at compile time via the variable.
	var w io.Writer = b
	_, err := w.Write([]byte("test"))
	if err != nil {
		t.Fatalf("Write via io.Writer: %v", err)
	}
	if got := b.String(); got != "test" {
		t.Errorf("String() = %q, want %q", got, "test")
	}
}
