// jsonrpc.go implements JSON-RPC 2.0 message types and newline-delimited
// framing for the ACP (Agent Communication Protocol) transport.
//
// Kiro-cli ACP uses plain newline-delimited JSON over stdio: each message
// is a single JSON object terminated by a newline. This file provides the
// low-level codec for reading and writing these messages.
//
// The codec is intentionally transport-agnostic — it operates on an io.Reader
// and io.Writer which are typically connected to a subprocess's stdout/stdin.
package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
)

const jsonrpcVersion = "2.0"

// malformedError indicates a JSON-RPC message that was successfully read from
// the wire (Content-Length framing intact) but failed to parse as valid JSON.
// The stream position is still valid — the next readMessage call will succeed.
//
// This is distinct from I/O errors (broken pipe, EOF) which leave the stream
// in an unrecoverable state.
type malformedError struct {
	detail string
}

func (e *malformedError) Error() string { return e.detail }

// ---------------------------------------------------------------------------
// Outgoing message types
// ---------------------------------------------------------------------------

// rpcRequest is an outgoing JSON-RPC 2.0 request (has id, expects a response).
type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// rpcNotification is an outgoing JSON-RPC 2.0 notification (no id, no
// response expected). Used for messages like "initialized".
type rpcNotification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// rpcResponse is an outgoing JSON-RPC 2.0 response (reply to a reverse request
// initiated by the server, such as permission prompts).
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is a JSON-RPC 2.0 error object embedded in a response.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// ---------------------------------------------------------------------------
// Incoming message type (unified)
// ---------------------------------------------------------------------------

// rpcMessage is the unified type for any incoming JSON-RPC 2.0 message.
//
// Classification based on field presence:
//   - ID != nil && Method != "" → reverse request (server → client, expects response)
//   - ID == nil && Method != "" → notification (server → client, no response expected)
//   - ID != nil && Method == "" → response (to a previous client request)
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// IsResponse returns true if this is a response to a previous client request
// (has id, no method).
func (m *rpcMessage) IsResponse() bool {
	return m.ID != nil && m.Method == ""
}

// IsNotification returns true if this is a server-initiated notification
// (has method, no id — no response expected).
func (m *rpcMessage) IsNotification() bool {
	return m.ID == nil && m.Method != ""
}

// IsReverseRequest returns true if this is a server-initiated request that
// expects a response from the client (has both id and method).
func (m *rpcMessage) IsReverseRequest() bool {
	return m.ID != nil && m.Method != ""
}

// rpcIDInt extracts an integer ID from a raw JSON id field.
// Returns 0, false if the id is nil, null, or not an integer.
func rpcIDInt(raw json.RawMessage) (int64, bool) {
	if raw == nil {
		return 0, false
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	return n, true
}

// ---------------------------------------------------------------------------
// Codec: Content-Length framed JSON-RPC 2.0 read/write
// ---------------------------------------------------------------------------

// rpcCodec handles reading and writing Content-Length framed JSON-RPC 2.0
// messages over stdio-style streams.
//
// Write safety: writes are serialized with a mutex, so send() may be called
// from multiple goroutines (e.g. main goroutine sending requests while the
// read goroutine replies to reverse requests).
//
// Read safety: readMessage() is NOT thread-safe and must be called from a
// single goroutine.
type rpcCodec struct {
	reader *bufio.Reader
	writer io.Writer
	mu     sync.Mutex   // serializes writes
	nextID atomic.Int64 // auto-incrementing request ID counter
}

// newRPCCodec creates a codec for the given reader (typically subprocess
// stdout) and writer (typically subprocess stdin).
func newRPCCodec(r io.Reader, w io.Writer) *rpcCodec {
	return &rpcCodec{
		reader: bufio.NewReaderSize(r, 64*1024), // 64KB buffer for large messages
		writer: w,
	}
}

// send writes a JSON-RPC 2.0 message as a single newline-delimited JSON line.
// The msg can be any JSON-marshalable value (rpcRequest, rpcResponse, etc.).
//
// Wire format: <json>\n
//
// Kiro-cli ACP uses plain newline-delimited JSON (not LSP-style
// Content-Length framing).
//
// Thread-safe: concurrent calls are serialized via mutex.
func (c *rpcCodec) send(msg interface{}) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("jsonrpc: marshal: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Write JSON followed by newline.
	if _, err := c.writer.Write(body); err != nil {
		return fmt.Errorf("jsonrpc: write: %w", err)
	}
	if _, err := c.writer.Write([]byte("\n")); err != nil {
		return fmt.Errorf("jsonrpc: write newline: %w", err)
	}

	return nil
}

// readMessage reads and parses a single newline-delimited JSON-RPC 2.0
// message. Blocks until a complete line is available or an error occurs
// (including io.EOF when the remote process exits).
//
// Kiro-cli ACP uses plain newline-delimited JSON (not LSP-style
// Content-Length framing). Each message is a single JSON object per line.
//
// NOT thread-safe: must be called from a single goroutine.
func (c *rpcCodec) readMessage() (*rpcMessage, error) {
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			if len(strings.TrimSpace(line)) > 0 {
				// Partial line before EOF — try to parse it.
				var msg rpcMessage
				if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(line)), &msg); jsonErr == nil {
					return &msg, nil
				}
			}
			return nil, fmt.Errorf("jsonrpc: read: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue // skip blank lines
		}

		var msg rpcMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			return nil, &malformedError{detail: fmt.Sprintf("jsonrpc: unmarshal: %v", err)}
		}

		return &msg, nil
	}
}

// newRequest creates an rpcRequest with an auto-incremented ID.
// The returned request is ready to be passed to send().
func (c *rpcCodec) newRequest(method string, params interface{}) rpcRequest {
	return rpcRequest{
		JSONRPC: jsonrpcVersion,
		ID:      c.nextID.Add(1),
		Method:  method,
		Params:  params,
	}
}

// newResponse creates an rpcResponse for replying to a reverse request.
// The id should come from the incoming rpcMessage.ID that is being replied to.
func newResponse(id json.RawMessage, result interface{}) rpcResponse {
	return rpcResponse{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Result:  result,
	}
}
