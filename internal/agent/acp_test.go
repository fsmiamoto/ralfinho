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
		Notifications: make(chan *rpcMessage, 128),
		ReverseReqs:   make(chan *rpcMessage, 16),
		done:          make(chan struct{}),
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

// writeFramed writes a Content-Length framed JSON-RPC message to w.
func writeFramed(w io.Writer, body []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	_, err := w.Write(body)
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

	// Give the call a moment to send the request and register the channel.
	time.Sleep(50 * time.Millisecond)

	// Simulate server sending a response with id=1 (first auto-incremented ID).
	resp := `{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}`
	if err := writeFramed(serverW, []byte(resp)); err != nil {
		t.Fatalf("writeFramed: %v", err)
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

	time.Sleep(50 * time.Millisecond)

	resp := `{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"Invalid Request"}}`
	if err := writeFramed(serverW, []byte(resp)); err != nil {
		t.Fatalf("writeFramed: %v", err)
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
	if err := writeFramed(serverW, []byte(notif)); err != nil {
		t.Fatalf("writeFramed: %v", err)
	}

	select {
	case msg := <-c.Notifications:
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
	if err := writeFramed(serverW, []byte(req)); err != nil {
		t.Fatalf("writeFramed: %v", err)
	}

	select {
	case msg := <-c.ReverseReqs:
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

	time.Sleep(50 * time.Millisecond)
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

	time.Sleep(50 * time.Millisecond)
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

	// Give the goroutine time to send the request.
	time.Sleep(50 * time.Millisecond)

	// Server responds with a session ID.
	resp := `{"jsonrpc":"2.0","id":1,"result":{"sessionId":"sess-abc-123"}}`
	if err := writeFramed(serverW, []byte(resp)); err != nil {
		t.Fatalf("writeFramed: %v", err)
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

	time.Sleep(50 * time.Millisecond)

	// Server responds with an empty session ID.
	resp := `{"jsonrpc":"2.0","id":1,"result":{"sessionId":""}}`
	if err := writeFramed(serverW, []byte(resp)); err != nil {
		t.Fatalf("writeFramed: %v", err)
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

	time.Sleep(50 * time.Millisecond)

	// Send AgentMessageChunk notification.
	notif1 := `{"jsonrpc":"2.0","method":"session/notification","params":{"sessionId":"sess-123","updates":[{"kind":"AgentMessageChunk","text":"Hello "}]}}`
	if err := writeFramed(serverW, []byte(notif1)); err != nil {
		t.Fatalf("writeFramed notif1: %v", err)
	}

	// Send ToolCall notification.
	notif2 := `{"jsonrpc":"2.0","method":"session/notification","params":{"sessionId":"sess-123","updates":[{"kind":"ToolCall","toolName":"read_file","status":"completed"}]}}`
	if err := writeFramed(serverW, []byte(notif2)); err != nil {
		t.Fatalf("writeFramed notif2: %v", err)
	}

	// Send TurnEnd notification.
	notif3 := `{"jsonrpc":"2.0","method":"session/notification","params":{"sessionId":"sess-123","updates":[{"kind":"TurnEnd"}]}}`
	if err := writeFramed(serverW, []byte(notif3)); err != nil {
		t.Fatalf("writeFramed notif3: %v", err)
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

	if len(received) != 3 {
		t.Fatalf("expected 3 updates, got %d", len(received))
	}
	if received[0].Kind != updateKindAgentMessage {
		t.Errorf("update 0: expected %s, got %s", updateKindAgentMessage, received[0].Kind)
	}
	if received[1].Kind != updateKindToolCall {
		t.Errorf("update 1: expected %s, got %s", updateKindToolCall, received[1].Kind)
	}
	if received[2].Kind != updateKindTurnEnd {
		t.Errorf("update 2: expected %s, got %s", updateKindTurnEnd, received[2].Kind)
	}
}

func TestACPClient_SessionPrompt_MultipleUpdatesPerNotification(t *testing.T) {
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

	time.Sleep(50 * time.Millisecond)

	// Send a single notification with multiple updates including TurnEnd.
	notif := `{"jsonrpc":"2.0","method":"session/notification","params":{"sessionId":"sess-123","updates":[{"kind":"AgentMessageChunk","text":"done"},{"kind":"TurnEnd"}]}}`
	if err := writeFramed(serverW, []byte(notif)); err != nil {
		t.Fatalf("writeFramed: %v", err)
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
	if received[1].Kind != updateKindTurnEnd {
		t.Errorf("update 1: expected %s, got %s", updateKindTurnEnd, received[1].Kind)
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

	time.Sleep(50 * time.Millisecond)

	// Server responds with an error to the session/prompt request.
	resp := `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"session not found"}}`
	if err := writeFramed(serverW, []byte(resp)); err != nil {
		t.Fatalf("writeFramed: %v", err)
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

	time.Sleep(50 * time.Millisecond)

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
// parseNotificationUpdates tests
// ---------------------------------------------------------------------------

func TestParseNotificationUpdates(t *testing.T) {
	tests := []struct {
		name      string
		params    string
		wantKinds []string
		wantErr   bool
	}{
		{
			name:      "single update",
			params:    `{"updates":[{"kind":"TurnEnd"}]}`,
			wantKinds: []string{"TurnEnd"},
		},
		{
			name:      "multiple updates",
			params:    `{"updates":[{"kind":"AgentMessageChunk","text":"hi"},{"kind":"TurnEnd"}]}`,
			wantKinds: []string{"AgentMessageChunk", "TurnEnd"},
		},
		{
			name:      "empty updates array",
			params:    `{"updates":[]}`,
			wantKinds: []string{},
		},
		{
			name:    "nil params",
			params:  "",
			wantErr: true,
		},
		{
			name:      "skip invalid JSON element",
			params:    `{"updates":[{"kind":"Good"},42,{"kind":"AlsoGood"}]}`,
			wantKinds: []string{"Good", "AlsoGood"},
		},
		{
			name:      "skip empty kind",
			params:    `{"updates":[{"kind":""},{"kind":"Valid"}]}`,
			wantKinds: []string{"Valid"},
		},
		{
			name:      "no updates field",
			params:    `{"sessionId":"s1"}`,
			wantKinds: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &rpcMessage{}
			if tt.params != "" {
				msg.Params = json.RawMessage(tt.params)
			}
			updates, err := parseNotificationUpdates(msg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(updates) != len(tt.wantKinds) {
				t.Fatalf("expected %d updates, got %d", len(tt.wantKinds), len(updates))
			}
			for i, want := range tt.wantKinds {
				if updates[i].Kind != want {
					t.Errorf("update %d: expected kind %q, got %q", i, want, updates[i].Kind)
				}
			}
		})
	}
}

func TestParseNotificationUpdates_RawPreserved(t *testing.T) {
	params := `{"updates":[{"kind":"ToolCall","toolName":"bash","input":{"command":"ls"}}]}`
	msg := &rpcMessage{Params: json.RawMessage(params)}

	updates, err := parseNotificationUpdates(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}

	// Verify the Raw field contains the full JSON of the update element.
	var raw map[string]interface{}
	if err := json.Unmarshal(updates[0].Raw, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if raw["toolName"] != "bash" {
		t.Errorf("expected toolName=bash, got %v", raw["toolName"])
	}
	if raw["kind"] != "ToolCall" {
		t.Errorf("expected kind=ToolCall in raw, got %v", raw["kind"])
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
	if err := writeFramed(serverW, []byte(req)); err != nil {
		t.Fatalf("writeFramed: %v", err)
	}

	// Give the handler time to process and respond.
	time.Sleep(100 * time.Millisecond)

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
		if err := writeFramed(serverW, []byte(req)); err != nil {
			t.Fatalf("writeFramed id=%d: %v", id, err)
		}
	}

	// Give the handler time to process all three.
	time.Sleep(150 * time.Millisecond)

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
	if err := writeFramed(serverW, []byte(req)); err != nil {
		t.Fatalf("writeFramed: %v", err)
	}

	// Give time for potential processing.
	time.Sleep(100 * time.Millisecond)

	// No response should have been sent.
	captured := drainBuf.Bytes()
	if len(captured) > 0 {
		t.Errorf("expected no response for non-permission request, but got %d bytes: %s", len(captured), captured)
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

	time.Sleep(50 * time.Millisecond)

	// Simulate: kiro sends an agent message chunk.
	notif1 := `{"jsonrpc":"2.0","method":"session/notification","params":{"sessionId":"sess-perm","updates":[{"kind":"AgentMessageChunk","text":"Working..."}]}}`
	if err := writeFramed(serverW, []byte(notif1)); err != nil {
		t.Fatalf("writeFramed notif1: %v", err)
	}

	// Simulate: kiro sends a permission request mid-stream.
	permReq := `{"jsonrpc":"2.0","id":77,"method":"session/request_permission","params":{"permission":"fs_write"}}`
	if err := writeFramed(serverW, []byte(permReq)); err != nil {
		t.Fatalf("writeFramed perm: %v", err)
	}

	// Give auto-approve time to respond.
	time.Sleep(100 * time.Millisecond)

	// Simulate: kiro continues with more output after permission was granted.
	notif2 := `{"jsonrpc":"2.0","method":"session/notification","params":{"sessionId":"sess-perm","updates":[{"kind":"AgentMessageChunk","text":"Done."},{"kind":"TurnEnd"}]}}`
	if err := writeFramed(serverW, []byte(notif2)); err != nil {
		t.Fatalf("writeFramed notif2: %v", err)
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
	if len(received) != 3 {
		t.Fatalf("expected 3 updates, got %d", len(received))
	}
	if received[0].Kind != updateKindAgentMessage {
		t.Errorf("update 0: expected %s, got %s", updateKindAgentMessage, received[0].Kind)
	}
	if received[1].Kind != updateKindAgentMessage {
		t.Errorf("update 1: expected %s, got %s", updateKindAgentMessage, received[1].Kind)
	}
	if received[2].Kind != updateKindTurnEnd {
		t.Errorf("update 2: expected %s, got %s", updateKindTurnEnd, received[2].Kind)
	}
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

	// Give the drain goroutine a moment to capture the bytes.
	time.Sleep(50 * time.Millisecond)

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
