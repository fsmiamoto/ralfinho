package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// syncBuffer is a thread-safe bytes.Buffer for tests that read and write
// concurrently (e.g. polling log output while a goroutine is still writing).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type failNthWriter struct {
	mu      sync.Mutex
	writes  int
	failOn  int
	err     error
	written bytes.Buffer
}

func (w *failNthWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes++
	if w.writes == w.failOn {
		return 0, w.err
	}
	return w.written.Write(p)
}

func TestACPClient_CallConnectionAlreadyClosed(t *testing.T) {
	c := &acpClient{
		codec:   newRPCCodec(strings.NewReader(""), io.Discard),
		pending: nil,
		done:    make(chan struct{}),
	}

	_, err := c.call(context.Background(), "test/closed", nil)
	if err == nil {
		t.Fatal("expected error for closed connection, got nil")
	}
	if !strings.Contains(err.Error(), "connection already closed") {
		t.Fatalf("call error = %v, want closed-connection message", err)
	}
}

func TestACPClient_CallSendErrorCleansPending(t *testing.T) {
	writer := &failNthWriter{failOn: 2, err: errors.New("boom")}
	c := &acpClient{
		codec:   newRPCCodec(strings.NewReader(""), writer),
		pending: make(map[int64]chan<- *rpcMessage),
		done:    make(chan struct{}),
	}

	_, err := c.call(context.Background(), "test/send", map[string]string{"k": "v"})
	if err == nil {
		t.Fatal("expected send error, got nil")
	}
	if !strings.Contains(err.Error(), "acp: send test/send") {
		t.Fatalf("call error = %v, want wrapped send error", err)
	}

	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	if len(c.pending) != 0 {
		t.Fatalf("pending map = %#v, want empty after send failure", c.pending)
	}
}

func TestACPClient_NotifySendError(t *testing.T) {
	writer := &failNthWriter{failOn: 2, err: errors.New("boom")}
	c := &acpClient{codec: newRPCCodec(strings.NewReader(""), writer)}

	err := c.notify("initialized", nil)
	if err == nil {
		t.Fatal("expected notify error, got nil")
	}
	if !strings.Contains(err.Error(), "acp: notify initialized") {
		t.Fatalf("notify error = %v, want wrapped notify error", err)
	}
}

func TestACPClient_Initialize_ServerError(t *testing.T) {
	c, serverW, _, cleanup := mockACPClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.initialize(ctx)
	}()

	waitForPending(t, c)

	resp := `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"init failed"}}`
	if err := writeJSONLine(serverW, []byte(resp)); err != nil {
		t.Fatalf("writeJSONLine: %v", err)
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected initialize error, got nil")
		}
		if !strings.Contains(err.Error(), "initialize handshake failed") || !strings.Contains(err.Error(), "init failed") {
			t.Fatalf("initialize error = %v, want wrapped handshake failure", err)
		}
	case <-ctx.Done():
		t.Fatal("initialize timed out")
	}
}

func TestACPClient_SessionNew_UnmarshalResultError(t *testing.T) {
	c, serverW, _, cleanup := mockACPClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := c.sessionNew(ctx, "/workspace")
		errCh <- err
	}()

	waitForPending(t, c)

	resp := `{"jsonrpc":"2.0","id":1,"result":{"sessionId":123}}`
	if err := writeJSONLine(serverW, []byte(resp)); err != nil {
		t.Fatalf("writeJSONLine: %v", err)
	}

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected sessionNew error, got nil")
		}
		if !strings.Contains(err.Error(), "session/new: unmarshal result") {
			t.Fatalf("sessionNew error = %v, want unmarshal failure", err)
		}
	case <-ctx.Done():
		t.Fatal("sessionNew timed out")
	}
}

func TestACPClient_SessionPrompt_IgnoresUnrelatedAndMalformedNotifications(t *testing.T) {
	c, serverW, _, cleanup := mockACPClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var mu sync.Mutex
	var got []sessionUpdate
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.sessionPrompt(ctx, "sess-1", "hello", func(u sessionUpdate) {
			mu.Lock()
			got = append(got, u)
			mu.Unlock()
		})
	}()

	waitForPending(t, c)

	unrelated := `{"jsonrpc":"2.0","method":"_kiro.dev/progress","params":{"state":"ignore-me"}}`
	if err := writeJSONLine(serverW, []byte(unrelated)); err != nil {
		t.Fatalf("writeJSONLine unrelated: %v", err)
	}

	malformed := `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess-1"}}`
	if err := writeJSONLine(serverW, []byte(malformed)); err != nil {
		t.Fatalf("writeJSONLine malformed: %v", err)
	}

	valid := `{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess-1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"ok"}}}}`
	if err := writeJSONLine(serverW, []byte(valid)); err != nil {
		t.Fatalf("writeJSONLine valid: %v", err)
	}

	resp := `{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}`
	if err := writeJSONLine(serverW, []byte(resp)); err != nil {
		t.Fatalf("writeJSONLine response: %v", err)
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
	if len(got) != 1 {
		t.Fatalf("received %d updates, want 1 valid update", len(got))
	}
	if got[0].Kind != updateKindAgentMessage {
		t.Fatalf("update kind = %q, want %q", got[0].Kind, updateKindAgentMessage)
	}
}

func TestParseSessionUpdate_InvalidJSON(t *testing.T) {
	tests := []struct {
		name   string
		params json.RawMessage
		want   string
	}{
		{
			name:   "params not json",
			params: json.RawMessage(`{"update":`),
			want:   "unmarshal notification params",
		},
		{
			name:   "update not object",
			params: json.RawMessage(`{"update":"oops"}`),
			want:   "unmarshal update header",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSessionUpdate(&rpcMessage{Params: tt.params})
			if err == nil {
				t.Fatal("expected parseSessionUpdate error, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseSessionUpdate error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestACPClient_AutoApprovePermissions_SendErrorLogsWarning(t *testing.T) {
	writer := &failNthWriter{failOn: 2, err: errors.New("boom")}
	var logs syncBuffer
	c := &acpClient{
		codec:       newRPCCodec(strings.NewReader(""), writer),
		reverseReqs: make(chan *rpcMessage, 1),
		done:        make(chan struct{}),
		logWriter:   &logs,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.autoApprovePermissions(ctx)
	}()

	c.reverseReqs <- &rpcMessage{ID: json.RawMessage("7"), Method: "session/request_permission"}

	waitFor(t, 2*time.Second, 5*time.Millisecond, func() bool {
		out := logs.String()
		return strings.Contains(out, "auto-approved permission") && strings.Contains(out, "failed to send permission response")
	})

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("autoApprovePermissions did not exit after cancellation")
	}
}

func TestACPClient_Close_IdempotentIncludesStderr(t *testing.T) {
	stderrBuf := newLimitedBuffer(4096)
	cmd := exec.Command("sh", "-c", "echo boom >&2; sleep 60")
	cmd.Stderr = stderrBuf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start(): %v", err)
	}
	defer func() {
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
		}
	}()

	time.Sleep(50 * time.Millisecond)

	done := make(chan struct{})
	close(done)
	c := &acpClient{
		cmd:       cmd,
		done:      done,
		logWriter: io.Discard,
		stderrBuf: stderrBuf,
	}

	err1 := c.Close()
	if err1 == nil {
		t.Fatal("expected Close error from killed process, got nil")
	}
	if !strings.Contains(err1.Error(), "kiro-cli stderr: boom") {
		t.Fatalf("Close error = %v, want stderr details", err1)
	}

	err2 := c.Close()
	if err2 == nil {
		t.Fatal("expected repeated Close to return same error, got nil")
	}
	if err2.Error() != err1.Error() {
		t.Fatalf("second Close error = %v, want %v", err2, err1)
	}
}
