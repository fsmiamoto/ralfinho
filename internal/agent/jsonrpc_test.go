package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Codec round-trip: send a message, read it back, verify correctness
// ---------------------------------------------------------------------------

func TestCodec_RoundTrip_Request(t *testing.T) {
	var buf bytes.Buffer
	codec := newRPCCodec(&buf, &buf)

	req := rpcRequest{
		JSONRPC: jsonrpcVersion,
		ID:      1,
		Method:  "test/method",
		Params:  map[string]string{"key": "value"},
	}
	if err := codec.send(req); err != nil {
		t.Fatalf("send: %v", err)
	}

	msg, err := codec.readMessage()
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if msg.Method != "test/method" {
		t.Errorf("method = %q, want %q", msg.Method, "test/method")
	}
	id, ok := rpcIDInt(msg.ID)
	if !ok || id != 1 {
		t.Errorf("id = %v (ok=%v), want 1", id, ok)
	}

	var params map[string]string
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if params["key"] != "value" {
		t.Errorf("params[key] = %q, want %q", params["key"], "value")
	}
}

func TestCodec_RoundTrip_Response(t *testing.T) {
	var buf bytes.Buffer
	codec := newRPCCodec(&buf, &buf)

	resp := rpcResponse{
		JSONRPC: jsonrpcVersion,
		ID:      json.RawMessage(`42`),
		Result:  "ok",
	}
	if err := codec.send(resp); err != nil {
		t.Fatalf("send: %v", err)
	}

	msg, err := codec.readMessage()
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}

	id, ok := rpcIDInt(msg.ID)
	if !ok || id != 42 {
		t.Errorf("id = %v (ok=%v), want 42", id, ok)
	}

	var result string
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result != "ok" {
		t.Errorf("result = %q, want %q", result, "ok")
	}
}

func TestCodec_RoundTrip_Notification(t *testing.T) {
	var buf bytes.Buffer
	codec := newRPCCodec(&buf, &buf)

	notif := rpcNotification{
		JSONRPC: jsonrpcVersion,
		Method:  "initialized",
	}
	if err := codec.send(notif); err != nil {
		t.Fatalf("send: %v", err)
	}

	msg, err := codec.readMessage()
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if msg.Method != "initialized" {
		t.Errorf("method = %q, want %q", msg.Method, "initialized")
	}
	if msg.ID != nil {
		t.Error("notification should have no id")
	}
}

func TestCodec_RoundTrip_ErrorResponse(t *testing.T) {
	var buf bytes.Buffer
	codec := newRPCCodec(&buf, &buf)

	resp := rpcResponse{
		JSONRPC: jsonrpcVersion,
		ID:      json.RawMessage(`7`),
		Error: &rpcError{
			Code:    -32600,
			Message: "Invalid Request",
		},
	}
	if err := codec.send(resp); err != nil {
		t.Fatalf("send: %v", err)
	}

	msg, err := codec.readMessage()
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if msg.Error == nil {
		t.Fatal("expected error field")
	}
	if msg.Error.Code != -32600 {
		t.Errorf("error code = %d, want %d", msg.Error.Code, -32600)
	}
	if msg.Error.Message != "Invalid Request" {
		t.Errorf("error message = %q, want %q", msg.Error.Message, "Invalid Request")
	}
}

func TestCodec_RoundTrip_MultipleMessages(t *testing.T) {
	var buf bytes.Buffer
	codec := newRPCCodec(&buf, &buf)

	// Write three messages back-to-back.
	messages := []rpcRequest{
		{JSONRPC: jsonrpcVersion, ID: 1, Method: "first"},
		{JSONRPC: jsonrpcVersion, ID: 2, Method: "second"},
		{JSONRPC: jsonrpcVersion, ID: 3, Method: "third"},
	}
	for _, m := range messages {
		if err := codec.send(m); err != nil {
			t.Fatalf("send %q: %v", m.Method, err)
		}
	}

	// Read them back in order.
	for _, want := range messages {
		msg, err := codec.readMessage()
		if err != nil {
			t.Fatalf("readMessage: %v", err)
		}
		if msg.Method != want.Method {
			t.Errorf("method = %q, want %q", msg.Method, want.Method)
		}
		id, _ := rpcIDInt(msg.ID)
		if id != want.ID {
			t.Errorf("id = %d, want %d", id, want.ID)
		}
	}
}

func TestCodec_RoundTrip_LargePayload(t *testing.T) {
	var buf bytes.Buffer
	codec := newRPCCodec(&buf, &buf)

	// Create a payload larger than typical to exercise the framing.
	bigValue := strings.Repeat("x", 100_000)
	req := rpcRequest{
		JSONRPC: jsonrpcVersion,
		ID:      1,
		Method:  "large/payload",
		Params:  map[string]string{"data": bigValue},
	}
	if err := codec.send(req); err != nil {
		t.Fatalf("send: %v", err)
	}

	msg, err := codec.readMessage()
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}

	var params map[string]string
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if params["data"] != bigValue {
		t.Errorf("large payload round-trip failed: got %d bytes, want %d", len(params["data"]), len(bigValue))
	}
}

// ---------------------------------------------------------------------------
// Content-Length framing: read raw wire-format messages
// ---------------------------------------------------------------------------

func TestCodec_ReadMessage_ValidLine(t *testing.T) {
	body := `{"jsonrpc":"2.0","method":"test","params":{"a":1}}`
	wire := body + "\n"

	codec := newRPCCodec(strings.NewReader(wire), io.Discard)
	msg, err := codec.readMessage()
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if msg.Method != "test" {
		t.Errorf("method = %q, want %q", msg.Method, "test")
	}
}

func TestCodec_ReadMessage_SkipsBlankLines(t *testing.T) {
	// Blank lines between messages should be skipped.
	wire := "\n\n" + `{"jsonrpc":"2.0","id":1,"result":"ok"}` + "\n\n"

	codec := newRPCCodec(strings.NewReader(wire), io.Discard)
	msg, err := codec.readMessage()
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if msg.Result == nil {
		t.Error("expected non-nil result")
	}
}

// ---------------------------------------------------------------------------
// Newline-delimited JSON: error cases
// ---------------------------------------------------------------------------

func TestCodec_ReadMessage_MalformedJSON(t *testing.T) {
	wire := "{not json}\n"

	codec := newRPCCodec(strings.NewReader(wire), io.Discard)
	_, err := codec.readMessage()
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("error should mention unmarshal, got: %v", err)
	}
}

func TestCodec_ReadMessage_EmptyReader(t *testing.T) {
	codec := newRPCCodec(strings.NewReader(""), io.Discard)
	_, err := codec.readMessage()
	if err == nil {
		t.Fatal("expected error for empty reader")
	}
}

// ---------------------------------------------------------------------------
// Message classification
// ---------------------------------------------------------------------------

func TestRPCMessage_Classification(t *testing.T) {
	tests := []struct {
		name             string
		msg              rpcMessage
		wantResponse     bool
		wantNotification bool
		wantReverse      bool
	}{
		{
			name:         "response (id, no method)",
			msg:          rpcMessage{ID: json.RawMessage(`1`), Result: json.RawMessage(`"ok"`)},
			wantResponse: true,
		},
		{
			name:             "notification (method, no id)",
			msg:              rpcMessage{Method: "session/notification"},
			wantNotification: true,
		},
		{
			name:        "reverse request (both id and method)",
			msg:         rpcMessage{ID: json.RawMessage(`99`), Method: "session/request_permission"},
			wantReverse: true,
		},
		{
			name: "empty message (none match)",
			msg:  rpcMessage{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.msg.IsResponse(); got != tt.wantResponse {
				t.Errorf("IsResponse() = %v, want %v", got, tt.wantResponse)
			}
			if got := tt.msg.IsNotification(); got != tt.wantNotification {
				t.Errorf("IsNotification() = %v, want %v", got, tt.wantNotification)
			}
			if got := tt.msg.IsReverseRequest(); got != tt.wantReverse {
				t.Errorf("IsReverseRequest() = %v, want %v", got, tt.wantReverse)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// rpcIDInt helper
// ---------------------------------------------------------------------------

func TestRPCIDInt(t *testing.T) {
	tests := []struct {
		name    string
		raw     json.RawMessage
		wantID  int64
		wantOK  bool
	}{
		{"integer", json.RawMessage(`42`), 42, true},
		{"zero", json.RawMessage(`0`), 0, true},
		{"negative", json.RawMessage(`-1`), -1, true},
		{"nil", nil, 0, false},
		{"string id", json.RawMessage(`"abc"`), 0, false},
		{"null", json.RawMessage(`null`), 0, true}, // json.Unmarshal unmarshals null → 0
		{"float", json.RawMessage(`1.5`), 0, false},
		{"object", json.RawMessage(`{}`), 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, ok := rpcIDInt(tt.raw)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if id != tt.wantID {
				t.Errorf("id = %d, want %d", id, tt.wantID)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// newRequest auto-incrementing IDs
// ---------------------------------------------------------------------------

func TestCodec_NewRequest_AutoIncrementIDs(t *testing.T) {
	codec := newRPCCodec(strings.NewReader(""), io.Discard)

	r1 := codec.newRequest("method/a", nil)
	r2 := codec.newRequest("method/b", nil)
	r3 := codec.newRequest("method/c", nil)

	if r1.ID != 1 {
		t.Errorf("r1.ID = %d, want 1", r1.ID)
	}
	if r2.ID != 2 {
		t.Errorf("r2.ID = %d, want 2", r2.ID)
	}
	if r3.ID != 3 {
		t.Errorf("r3.ID = %d, want 3", r3.ID)
	}
	if r1.JSONRPC != jsonrpcVersion {
		t.Errorf("JSONRPC = %q, want %q", r1.JSONRPC, jsonrpcVersion)
	}
	if r1.Method != "method/a" {
		t.Errorf("Method = %q, want %q", r1.Method, "method/a")
	}
}

// ---------------------------------------------------------------------------
// newResponse helper
// ---------------------------------------------------------------------------

func TestNewResponse(t *testing.T) {
	id := json.RawMessage(`55`)
	resp := newResponse(id, "allow_always")

	if resp.JSONRPC != jsonrpcVersion {
		t.Errorf("JSONRPC = %q, want %q", resp.JSONRPC, jsonrpcVersion)
	}
	if string(resp.ID) != "55" {
		t.Errorf("ID = %s, want 55", resp.ID)
	}

	// Verify the response round-trips through the codec correctly.
	var buf bytes.Buffer
	codec := newRPCCodec(&buf, &buf)
	if err := codec.send(resp); err != nil {
		t.Fatalf("send: %v", err)
	}

	msg, err := codec.readMessage()
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}

	gotID, ok := rpcIDInt(msg.ID)
	if !ok || gotID != 55 {
		t.Errorf("round-trip ID = %d (ok=%v), want 55", gotID, ok)
	}

	var result string
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result != "allow_always" {
		t.Errorf("result = %q, want %q", result, "allow_always")
	}
}

// ---------------------------------------------------------------------------
// Wire format verification
// ---------------------------------------------------------------------------

func TestCodec_Send_WireFormat(t *testing.T) {
	var buf bytes.Buffer
	codec := newRPCCodec(strings.NewReader(""), &buf)

	req := rpcRequest{
		JSONRPC: jsonrpcVersion,
		ID:      1,
		Method:  "test",
	}
	if err := codec.send(req); err != nil {
		t.Fatalf("send: %v", err)
	}

	wire := buf.String()

	// Must end with a newline.
	if !strings.HasSuffix(wire, "\n") {
		t.Errorf("wire should end with newline, got: %q", wire)
	}

	// Must be a single JSON line (no Content-Length framing).
	body := strings.TrimSuffix(wire, "\n")
	if strings.Contains(body, "\n") {
		t.Errorf("wire should be a single JSON line, got: %q", wire)
	}

	// Body must be valid JSON.
	var msg map[string]interface{}
	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if msg["method"] != "test" {
		t.Errorf("body method = %v, want %q", msg["method"], "test")
	}
}

// ---------------------------------------------------------------------------
// malformedError tests
// ---------------------------------------------------------------------------

func TestReadMessage_MalformedJSON_ReturnsMalformedError(t *testing.T) {
	wire := "{not valid json}\n"

	codec := newRPCCodec(strings.NewReader(wire), io.Discard)
	_, err := codec.readMessage()

	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}

	var me *malformedError
	if !errors.As(err, &me) {
		t.Errorf("expected *malformedError, got %T: %v", err, err)
	}
}

func TestReadMessage_IOError_NotMalformedError(t *testing.T) {
	// An empty reader should return an I/O error, not a malformedError.
	codec := newRPCCodec(strings.NewReader(""), io.Discard)
	_, err := codec.readMessage()

	if err == nil {
		t.Fatal("expected error for empty input, got nil")
	}

	var me *malformedError
	if errors.As(err, &me) {
		t.Error("I/O error should not be a *malformedError")
	}
}

func TestReadMessage_MalformedJSON_StreamPositionValid(t *testing.T) {
	// After a malformed message, the stream position should be valid for
	// the next readMessage call.
	wire := "{\"broken\":\n" +
		`{"jsonrpc":"2.0","method":"test/ok","params":{}}` + "\n"

	codec := newRPCCodec(strings.NewReader(wire), io.Discard)

	// First read should return malformedError.
	_, err := codec.readMessage()
	var me *malformedError
	if !errors.As(err, &me) {
		t.Fatalf("first read: expected *malformedError, got %T: %v", err, err)
	}

	// Second read should succeed.
	msg, err := codec.readMessage()
	if err != nil {
		t.Fatalf("second read: unexpected error: %v", err)
	}
	if msg.Method != "test/ok" {
		t.Errorf("expected method test/ok, got %q", msg.Method)
	}
}


