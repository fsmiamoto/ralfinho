package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestNewUUID_Format(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for i := 0; i < 20; i++ {
		id := newUUID()
		if !re.MatchString(id) {
			t.Errorf("newUUID() = %q, does not match UUID v4 pattern", id)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		s    string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hell…"},
		{"", 5, ""},
		{"こんにちは世界", 5, "こんにち…"},  // multi-byte: truncates by rune, not byte
		{"abc", 0, "…"},                     // n=0 edge case
		{"a", 1, "a"},                       // exactly at limit
	}
	for _, tt := range tests {
		got := truncate(tt.s, tt.n)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Completion marker detection
// ---------------------------------------------------------------------------

func TestCompletionMarker_DetectedInText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		contains bool
	}{
		{"exact marker", completionMarker, true},
		{"marker in surrounding text", "All done. " + completionMarker + " Goodbye.", true},
		{"marker at end", "Task finished " + completionMarker, true},
		{"no marker", "This is normal text without completion signal", false},
		{"partial marker", "<promise>COMPLE</promise>", false},
		{"empty text", "", false},
		{"wrong tag", "<promise>INCOMPLETE</promise>", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Contains(tt.text, completionMarker)
			if got != tt.contains {
				t.Errorf("strings.Contains(%q, completionMarker) = %v, want %v", tt.text, got, tt.contains)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// handleEvent
// ---------------------------------------------------------------------------

func newTestRunner(t *testing.T) (*Runner, string) {
	t.Helper()
	tmpDir := t.TempDir()
	r := &Runner{
		cfg: RunConfig{
			Agent:   "test",
			RunsDir: tmpDir,
			Prompt:  "test prompt",
		},
		runID:  "test-run-id",
		stderr: io.Discard,
	}
	// Create run dir and open files.
	runDir := fmt.Sprintf("%s/%s", tmpDir, r.runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}
	return r, runDir
}

func TestRunner_HandleEvent_MessageStart_Assistant(t *testing.T) {
	r, _ := newTestRunner(t)

	ev := Event{
		Type:    EventMessageStart,
		Message: json.RawMessage(`{"role":"assistant","model":"claude-4"}`),
	}
	// Should not panic even without session file.
	r.handleEvent(&ev)
}

func TestRunner_HandleEvent_MessageStart_User(t *testing.T) {
	r, _ := newTestRunner(t)

	ev := Event{
		Type:    EventMessageStart,
		Message: json.RawMessage(`{"role":"user"}`),
	}
	r.handleEvent(&ev)
}

func TestRunner_HandleEvent_MessageUpdate_AccumulatesText(t *testing.T) {
	r, _ := newTestRunner(t)

	// Start an assistant message.
	r.handleEvent(&Event{
		Type:    EventMessageStart,
		Message: json.RawMessage(`{"role":"assistant","model":"test"}`),
	})

	r.handleEvent(&Event{
		Type:                  EventMessageUpdate,
		AssistantMessageEvent: json.RawMessage(`{"type":"text_delta","contentIndex":0,"delta":"Hello "}`),
	})
	r.handleEvent(&Event{
		Type:                  EventMessageUpdate,
		AssistantMessageEvent: json.RawMessage(`{"type":"text_delta","contentIndex":0,"delta":"world"}`),
	})

	if got := r.sessionText.String(); got != "Hello world" {
		t.Errorf("sessionText = %q, want %q", got, "Hello world")
	}
}

func TestRunner_HandleEvent_MessageEnd_FlushesText(t *testing.T) {
	r, runDir := newTestRunner(t)

	// Open session file for writing.
	var err error
	r.sessionFile, err = os.Create(runDir + "/session.log")
	if err != nil {
		t.Fatal(err)
	}
	defer r.sessionFile.Close()

	// Simulate assistant message start → update → end.
	r.handleEvent(&Event{
		Type:    EventMessageStart,
		Message: json.RawMessage(`{"role":"assistant","model":"test"}`),
	})
	r.handleEvent(&Event{
		Type:                  EventMessageUpdate,
		AssistantMessageEvent: json.RawMessage(`{"type":"text_delta","contentIndex":0,"delta":"Some text"}`),
	})
	r.handleEvent(&Event{
		Type: EventMessageEnd,
	})

	// sessionText should be reset after message_end.
	if got := r.sessionText.String(); got != "" {
		t.Errorf("sessionText should be empty after message_end, got %q", got)
	}

	// Verify session.log was written.
	data, err := os.ReadFile(runDir + "/session.log")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "Some text") {
		t.Errorf("session.log should contain the assistant text, got: %s", content)
	}
}

func TestRunner_HandleEvent_ToolExecution(t *testing.T) {
	r, runDir := newTestRunner(t)

	var err error
	r.sessionFile, err = os.Create(runDir + "/session.log")
	if err != nil {
		t.Fatal(err)
	}
	defer r.sessionFile.Close()

	isErr := false
	r.handleEvent(&Event{
		Type:       EventToolExecutionStart,
		ToolCallID: "tc-123456789012",
		ToolName:   "bash",
		Args:       json.RawMessage(`{"command":"ls -la"}`),
	})
	r.handleEvent(&Event{
		Type:       EventToolExecutionEnd,
		ToolCallID: "tc-123456789012",
		ToolName:   "bash",
		Result:     json.RawMessage(`"file1.go\nfile2.go"`),
		IsError:    &isErr,
	})

	data, err := os.ReadFile(runDir + "/session.log")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "tool start: bash") {
		t.Errorf("session.log should mention tool start, got: %s", content)
	}
	if !strings.Contains(content, "tool done: bash") {
		t.Errorf("session.log should mention tool done, got: %s", content)
	}
	if !strings.Contains(content, "ls -la") {
		t.Errorf("session.log should contain the command, got: %s", content)
	}
}

func TestRunner_HandleEvent_ToolError(t *testing.T) {
	r, runDir := newTestRunner(t)

	var err error
	r.sessionFile, err = os.Create(runDir + "/session.log")
	if err != nil {
		t.Fatal(err)
	}
	defer r.sessionFile.Close()

	isErr := true
	r.handleEvent(&Event{
		Type:       EventToolExecutionEnd,
		ToolCallID: "tc-err",
		ToolName:   "bash",
		Result:     json.RawMessage(`"command not found"`),
		IsError:    &isErr,
	})

	data, err := os.ReadFile(runDir + "/session.log")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "[ERROR]") {
		t.Errorf("session.log should contain [ERROR] for tool errors, got: %s", content)
	}
}

func TestRunner_HandleEvent_TurnEnd_FlushesRemainingText(t *testing.T) {
	r, runDir := newTestRunner(t)

	var err error
	r.sessionFile, err = os.Create(runDir + "/session.log")
	if err != nil {
		t.Fatal(err)
	}
	defer r.sessionFile.Close()

	// Accumulate text without a message_end (safety flush scenario).
	r.handleEvent(&Event{
		Type:    EventMessageStart,
		Message: json.RawMessage(`{"role":"assistant","model":"test"}`),
	})
	r.handleEvent(&Event{
		Type:                  EventMessageUpdate,
		AssistantMessageEvent: json.RawMessage(`{"type":"text_delta","contentIndex":0,"delta":"leftover text"}`),
	})
	// No message_end, directly turn_end.
	r.handleEvent(&Event{
		Type: EventTurnEnd,
	})

	if got := r.sessionText.String(); got != "" {
		t.Errorf("sessionText should be flushed after turn_end, got %q", got)
	}

	data, err := os.ReadFile(runDir + "/session.log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "leftover text") {
		t.Error("turn_end should flush remaining assistant text to session.log")
	}
}

func TestRunner_HandleEvent_Session(t *testing.T) {
	r, runDir := newTestRunner(t)

	var err error
	r.sessionFile, err = os.Create(runDir + "/session.log")
	if err != nil {
		t.Fatal(err)
	}
	defer r.sessionFile.Close()

	r.handleEvent(&Event{
		Type: EventSession,
		ID:   "sess-42",
	})

	data, err := os.ReadFile(runDir + "/session.log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "sess-42") {
		t.Error("session.log should contain the session ID")
	}
}

func TestRunner_HandleEvent_AgentEnd(t *testing.T) {
	r, runDir := newTestRunner(t)

	var err error
	r.sessionFile, err = os.Create(runDir + "/session.log")
	if err != nil {
		t.Fatal(err)
	}
	defer r.sessionFile.Close()

	r.handleEvent(&Event{
		Type: EventAgentEnd,
	})

	data, err := os.ReadFile(runDir + "/session.log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "agent end") {
		t.Error("session.log should contain 'agent end'")
	}
}

// ---------------------------------------------------------------------------
// EventChan forwarding
// ---------------------------------------------------------------------------

func TestRunner_HandleEvent_SendsToTUI(t *testing.T) {
	ch := make(chan Event, 10)
	r := &Runner{
		cfg: RunConfig{
			EventChan: ch,
		},
		stderr: io.Discard,
	}

	ev := Event{
		Type:    EventMessageStart,
		Message: json.RawMessage(`{"role":"assistant","model":"test"}`),
	}
	r.handleEvent(&ev)

	select {
	case got := <-ch:
		if got.Type != EventMessageStart {
			t.Errorf("expected %s on channel, got %s", EventMessageStart, got.Type)
		}
	default:
		t.Error("expected event to be sent to EventChan")
	}
}

func TestRunner_SendEvent_NoChannelNoPanic(t *testing.T) {
	r := &Runner{
		cfg:    RunConfig{}, // no EventChan
		stderr: io.Discard,
	}
	// Should not panic.
	r.sendEvent(Event{Type: EventTurnEnd})
}

func TestRunner_SendEvent_FullChannelDoesNotBlock(t *testing.T) {
	ch := make(chan Event) // unbuffered
	r := &Runner{
		cfg: RunConfig{
			EventChan: ch,
		},
		stderr: io.Discard,
	}
	// Should not block — sendEvent uses select with default.
	done := make(chan struct{})
	go func() {
		r.sendEvent(Event{Type: EventTurnEnd})
		close(done)
	}()

	select {
	case <-done:
		// OK, didn't block.
	case <-time.After(time.Second):
		t.Fatal("sendEvent blocked on full channel")
	}
}

// ---------------------------------------------------------------------------
// Event persistence (events.jsonl)
// ---------------------------------------------------------------------------

func TestRunner_EventPersistence(t *testing.T) {
	r, runDir := newTestRunner(t)

	var err error
	r.eventsFile, err = os.Create(runDir + "/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	testEvents := []Event{
		{Type: EventMessageStart, Message: json.RawMessage(`{"role":"assistant","model":"test"}`)},
		{Type: EventMessageUpdate, AssistantMessageEvent: json.RawMessage(`{"type":"text_delta","contentIndex":0,"delta":"hi"}`)},
		{Type: EventMessageEnd},
		{Type: EventTurnEnd},
	}

	for i := range testEvents {
		// Simulate what runIteration does: persist + handleEvent.
		data, merr := json.Marshal(testEvents[i])
		if merr != nil {
			t.Fatalf("marshal event %d: %v", i, merr)
		}
		fmt.Fprintln(r.eventsFile, string(data))
	}
	r.eventsFile.Close()

	// Read and verify events.jsonl.
	data, err := os.ReadFile(runDir + "/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines in events.jsonl, got %d", len(lines))
	}

	// Verify each line is valid JSON with correct type.
	for i, line := range lines {
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Errorf("line %d: invalid JSON: %v", i, err)
			continue
		}
		if ev.Type != testEvents[i].Type {
			t.Errorf("line %d: expected type %s, got %s", i, testEvents[i].Type, ev.Type)
		}
	}
}

// ---------------------------------------------------------------------------
// Meta writing
// ---------------------------------------------------------------------------

func TestRunner_WriteMeta(t *testing.T) {
	r, runDir := newTestRunner(t)
	r.startedAt = time.Now()

	result := RunResult{
		RunID:      r.runID,
		Iterations: 3,
		Status:     StatusCompleted,
		Agent:      "test",
	}
	r.writeMeta(result)

	data, err := os.ReadFile(runDir + "/meta.json")
	if err != nil {
		t.Fatal(err)
	}

	var meta RunMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("meta.json is not valid JSON: %v", err)
	}

	if meta.RunID != "test-run-id" {
		t.Errorf("expected run_id=%q, got %q", "test-run-id", meta.RunID)
	}
	if meta.Status != "completed" {
		t.Errorf("expected status=completed, got %q", meta.Status)
	}
	if meta.IterationsCompleted != 3 {
		t.Errorf("expected iterations_completed=3, got %d", meta.IterationsCompleted)
	}
	if meta.Agent != "test" {
		t.Errorf("expected agent=test, got %q", meta.Agent)
	}
}

// ---------------------------------------------------------------------------
// Effective prompt writing
// ---------------------------------------------------------------------------

func TestRunner_WriteEffectivePrompt(t *testing.T) {
	tmpDir := t.TempDir()
	r := &Runner{
		cfg: RunConfig{
			RunsDir: tmpDir,
			Prompt:  "This is the effective prompt.",
		},
		runID:  "prompt-test-run",
		stderr: io.Discard,
	}

	if err := r.writeEffectivePrompt(); err != nil {
		t.Fatalf("writeEffectivePrompt: %v", err)
	}

	data, err := os.ReadFile(fmt.Sprintf("%s/%s/effective-prompt.md", tmpDir, r.runID))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "This is the effective prompt." {
		t.Errorf("effective prompt = %q, want %q", string(data), "This is the effective prompt.")
	}
}

// ---------------------------------------------------------------------------
// File lifecycle
// ---------------------------------------------------------------------------

func TestRunner_OpenCloseRunFiles(t *testing.T) {
	tmpDir := t.TempDir()
	r := &Runner{
		cfg: RunConfig{
			RunsDir: tmpDir,
		},
		runID:  "files-test",
		stderr: io.Discard,
	}

	// Create the run directory (normally done by writeEffectivePrompt).
	runDir := fmt.Sprintf("%s/%s", tmpDir, r.runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}

	r.openRunFiles()

	// Verify files were opened.
	if r.eventsFile == nil {
		t.Error("eventsFile should be non-nil")
	}
	if r.rawFile == nil {
		t.Error("rawFile should be non-nil")
	}
	if r.sessionFile == nil {
		t.Error("sessionFile should be non-nil")
	}

	r.closeRunFiles()

	// Verify files exist on disk.
	for _, name := range []string{"events.jsonl", "raw-output.log", "session.log"} {
		path := filepath.Join(runDir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected %s to exist after openRunFiles", name)
		}
	}
}
