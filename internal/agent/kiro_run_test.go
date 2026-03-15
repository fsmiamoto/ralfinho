package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsmiamoto/ralfinho/internal/events"
)

const fakeKiroCLIScript = `#!/usr/bin/env python3
import json
import os
import sys
import time


def write_text(path, text):
    if path:
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)


def send(obj):
    sys.stdout.write(json.dumps(obj) + "\n")
    sys.stdout.flush()


def recv():
    line = sys.stdin.readline()
    if not line:
        return None
    return json.loads(line)


mode = os.environ.get("KIRO_FAKE_MODE", "success")
write_text(os.environ.get("KIRO_ARGV_FILE"), "\n".join(sys.argv[1:]))

msg = recv()
if msg is None or msg.get("method") != "initialize":
    sys.exit("expected initialize")
send({"jsonrpc": "2.0", "id": msg["id"], "result": {"protocolVersion": msg.get("params", {}).get("protocolVersion", "")}})

msg = recv()
if msg is None or msg.get("method") != "initialized":
    sys.exit("expected initialized")

msg = recv()
if msg is None or msg.get("method") != "session/new":
    sys.exit("expected session/new")
write_text(os.environ.get("KIRO_CWD_FILE"), msg.get("params", {}).get("cwd", ""))
send({"jsonrpc": "2.0", "id": msg["id"], "result": {"sessionId": "sess-123"}})

msg = recv()
if msg is None or msg.get("method") != "session/prompt":
    sys.exit("expected session/prompt")
prompt_blocks = msg.get("params", {}).get("prompt", [])
prompt_text = ""
if isinstance(prompt_blocks, list) and prompt_blocks:
    prompt_text = prompt_blocks[0].get("text", "")
write_text(os.environ.get("KIRO_PROMPT_FILE"), prompt_text)
prompt_id = msg["id"]


def session_update(update):
    return {
        "jsonrpc": "2.0",
        "method": "session/update",
        "params": {
            "sessionId": "sess-123",
            "update": update,
        },
    }


if mode == "success":
    send(session_update({
        "sessionUpdate": "agent_message_chunk",
        "content": {"type": "text", "text": "Hello "},
    }))
    send(session_update({
        "sessionUpdate": "tool_call",
        "title": "shell",
        "toolCallId": "tc-1",
        "kind": "execute",
        "status": "in_progress",
    }))
    send({
        "jsonrpc": "2.0",
        "id": 77,
        "method": "session/request_permission",
        "params": {"permission": "bash"},
    })
    perm = recv()
    if perm is None or perm.get("id") != 77 or perm.get("result") != "allow_always":
        sys.exit("expected permission approval")
    send(session_update({
        "sessionUpdate": "tool_call",
        "title": "Running: pwd",
        "toolCallId": "tc-1",
        "kind": "execute",
        "rawInput": {"command": "pwd"},
    }))
    send(session_update({
        "sessionUpdate": "tool_call",
        "title": "shell",
        "toolCallId": "tc-1",
        "rawOutput": "ok",
        "status": "completed",
    }))
    send(session_update({
        "sessionUpdate": "agent_message_chunk",
        "content": {"type": "text", "text": "done"},
    }))
    send({"jsonrpc": "2.0", "id": prompt_id, "result": {"stopReason": "end_turn"}})
elif mode == "cancel":
    send(session_update({
        "sessionUpdate": "agent_message_chunk",
        "content": {"type": "text", "text": "partial "},
    }))
    sys.stdout.flush()
    time.sleep(60)
elif mode == "prompt_error":
    send(session_update({
        "sessionUpdate": "agent_message_chunk",
        "content": {"type": "text", "text": "oops"},
    }))
    send({
        "jsonrpc": "2.0",
        "id": prompt_id,
        "error": {"code": -32000, "message": "boom"},
    })
else:
    sys.exit("unknown mode: " + mode)
`

func writeFakeKiroCLI(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(fakeKiroCLIScript), 0755); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func setupFakeKiroCLI(t *testing.T, mode string) (argvFile, promptFile, cwdFile string) {
	t.Helper()

	binDir := t.TempDir()
	writeFakeKiroCLI(t, filepath.Join(binDir, "kiro-cli"))

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("KIRO_FAKE_MODE", mode)

	argvFile = filepath.Join(binDir, "argv.txt")
	promptFile = filepath.Join(binDir, "prompt.txt")
	cwdFile = filepath.Join(binDir, "cwd.txt")
	t.Setenv("KIRO_ARGV_FILE", argvFile)
	t.Setenv("KIRO_PROMPT_FILE", promptFile)
	t.Setenv("KIRO_CWD_FILE", cwdFile)

	return argvFile, promptFile, cwdFile
}

func TestKiroAgent_RunIteration_Success(t *testing.T) {
	argvFile, promptFile, cwdFile := setupFakeKiroCLI(t, "success")

	var rawBuf bytes.Buffer
	a := NewKiroAgent(
		WithRawWriter(&rawBuf),
		WithLogWriter(io.Discard),
		WithExtraArgs([]string{"--fake-extra", "value"}),
	)
	onEvent, get := collectEvents()

	text, err := a.RunIteration(context.Background(), "test prompt", onEvent)
	if err != nil {
		t.Fatalf("RunIteration() error = %v", err)
	}
	if text != "Hello done" {
		t.Fatalf("assistant text = %q, want %q", text, "Hello done")
	}

	evts := get()
	wantTypes := []events.EventType{
		events.EventMessageStart,
		events.EventMessageUpdate,
		events.EventMessageEnd,
		events.EventToolExecutionStart,
		events.EventToolExecutionEnd,
		events.EventMessageStart,
		events.EventMessageUpdate,
		events.EventMessageEnd,
		events.EventTurnEnd,
	}
	if len(evts) != len(wantTypes) {
		var got []string
		for _, ev := range evts {
			got = append(got, string(ev.Type))
		}
		t.Fatalf("event count = %d, want %d (%v)", len(evts), len(wantTypes), got)
	}
	for i, want := range wantTypes {
		if evts[i].Type != want {
			t.Fatalf("event %d type = %s, want %s", i, evts[i].Type, want)
		}
	}

	var msg events.MessageEnvelope
	if err := json.Unmarshal(evts[0].Message, &msg); err != nil {
		t.Fatalf("unmarshal MessageStart: %v", err)
	}
	if msg.Role != "assistant" || msg.Model != "kiro" {
		t.Fatalf("message start = %#v, want assistant/kiro", msg)
	}

	toolStart := evts[3]
	if toolStart.ToolName != "bash" {
		t.Fatalf("tool start name = %q, want %q", toolStart.ToolName, "bash")
	}
	if toolStart.ToolCallID != "tc-1" {
		t.Fatalf("tool start id = %q, want %q", toolStart.ToolCallID, "tc-1")
	}
	if toolStart.ToolDisplayArgs != "$ pwd" {
		t.Fatalf("tool display args = %q, want %q", toolStart.ToolDisplayArgs, "$ pwd")
	}
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(toolStart.Args, &args); err != nil {
		t.Fatalf("unmarshal tool args: %v", err)
	}
	if args.Command != "pwd" {
		t.Fatalf("tool command = %q, want %q", args.Command, "pwd")
	}

	toolEnd := evts[4]
	if toolEnd.ToolName != "shell" {
		t.Fatalf("tool end name = %q, want %q", toolEnd.ToolName, "shell")
	}
	if toolEnd.IsError == nil || *toolEnd.IsError {
		t.Fatalf("tool end isError = %#v, want false", toolEnd.IsError)
	}
	var result string
	if err := json.Unmarshal(toolEnd.Result, &result); err != nil {
		t.Fatalf("unmarshal tool result: %v", err)
	}
	if result != "ok" {
		t.Fatalf("tool result = %q, want %q", result, "ok")
	}

	argvBytes, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("ReadFile(argv): %v", err)
	}
	argv := strings.Split(strings.TrimSpace(string(argvBytes)), "\n")
	wantArgv := []string{"acp", "--trust-all-tools", "--fake-extra", "value"}
	if len(argv) != len(wantArgv) {
		t.Fatalf("argv = %#v, want %#v", argv, wantArgv)
	}
	for i, want := range wantArgv {
		if argv[i] != want {
			t.Fatalf("argv[%d] = %q, want %q (full argv: %#v)", i, argv[i], want, argv)
		}
	}

	promptBytes, err := os.ReadFile(promptFile)
	if err != nil {
		t.Fatalf("ReadFile(prompt): %v", err)
	}
	if string(promptBytes) != "test prompt" {
		t.Fatalf("prompt = %q, want %q", string(promptBytes), "test prompt")
	}

	cwdBytes, err := os.ReadFile(cwdFile)
	if err != nil {
		t.Fatalf("ReadFile(cwd): %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	if string(cwdBytes) != cwd {
		t.Fatalf("session/new cwd = %q, want %q", string(cwdBytes), cwd)
	}

	raw := rawBuf.String()
	for _, want := range []string{"session/update", "session/request_permission", "tool_call", "agent_message_chunk"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("raw output missing %q in %q", want, raw)
		}
	}
}

func TestKiroAgent_RunIteration_ContextCancellationFinalizesOpenMessage(t *testing.T) {
	setupFakeKiroCLI(t, "cancel")

	a := NewKiroAgent(WithLogWriter(io.Discard))
	onEvent, get := collectEvents()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	text, err := a.RunIteration(ctx, "cancel prompt", onEvent)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunIteration() error = %v, want deadline exceeded", err)
	}
	if text != "partial " {
		t.Fatalf("assistant text = %q, want %q", text, "partial ")
	}

	evts := get()
	wantTypes := []events.EventType{
		events.EventMessageStart,
		events.EventMessageUpdate,
		events.EventMessageEnd,
		events.EventTurnEnd,
	}
	if len(evts) != len(wantTypes) {
		var got []string
		for _, ev := range evts {
			got = append(got, string(ev.Type))
		}
		t.Fatalf("event count = %d, want %d (%v)", len(evts), len(wantTypes), got)
	}
	for i, want := range wantTypes {
		if evts[i].Type != want {
			t.Fatalf("event %d type = %s, want %s", i, evts[i].Type, want)
		}
	}
}

func TestKiroAgent_RunIteration_PromptErrorReturnsPartialText(t *testing.T) {
	setupFakeKiroCLI(t, "prompt_error")

	a := NewKiroAgent(WithLogWriter(io.Discard))
	onEvent, get := collectEvents()

	text, err := a.RunIteration(context.Background(), "broken prompt", onEvent)
	if err == nil {
		t.Fatal("RunIteration() error = nil, want wrapped prompt error")
	}
	if !strings.Contains(err.Error(), "kiro: acp: session/prompt error -32000: boom") {
		t.Fatalf("RunIteration() error = %q, want wrapped session/prompt error", err)
	}
	if text != "oops" {
		t.Fatalf("assistant text = %q, want %q", text, "oops")
	}

	evts := get()
	wantTypes := []events.EventType{
		events.EventMessageStart,
		events.EventMessageUpdate,
		events.EventMessageEnd,
		events.EventTurnEnd,
	}
	if len(evts) != len(wantTypes) {
		var got []string
		for _, ev := range evts {
			got = append(got, string(ev.Type))
		}
		t.Fatalf("event count = %d, want %d (%v)", len(evts), len(wantTypes), got)
	}
	for i, want := range wantTypes {
		if evts[i].Type != want {
			t.Fatalf("event %d type = %s, want %s", i, evts[i].Type, want)
		}
	}
}
