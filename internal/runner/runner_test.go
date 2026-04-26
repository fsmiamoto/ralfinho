package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fsmiamoto/ralfinho/internal/events"
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
		{"こんにちは世界", 5, "こんにち…"}, // multi-byte: truncates by rune, not byte
		{"abc", 0, "…"},         // n=0 edge case
		{"a", 1, "a"},           // exactly at limit
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
// askContinue
// ---------------------------------------------------------------------------

func TestRunner_AskContinue(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"y", "y\n", true},
		{"yes", "yes\n", true},
		{"Y uppercase", "Y\n", true},
		{"YES uppercase", "YES\n", true},
		{"n", "n\n", false},
		{"no", "no\n", false},
		{"empty", "\n", false},
		{"random text", "maybe\n", false},
		{"y with spaces", "  y  \n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Runner{
				stdin:  strings.NewReader(tt.input),
				stderr: io.Discard,
			}
			if got := r.askContinue(); got != tt.want {
				t.Errorf("askContinue() with input %q = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestRunner_AskContinue_ReadError(t *testing.T) {
	// Empty reader simulates EOF / read error.
	r := &Runner{
		stdin:  strings.NewReader(""),
		stderr: io.Discard,
	}
	if r.askContinue() {
		t.Error("askContinue() should return false on read error")
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
	runDir := filepath.Join(tmpDir, r.runID)
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

func TestRunner_HandleEvent_ToolUpdate_LogsArgs(t *testing.T) {
	r, runDir := newTestRunner(t)

	var err error
	r.sessionFile, err = os.Create(filepath.Join(runDir, "session.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.sessionFile.Close()

	// Simulate tool start without args (Claude pattern), then update with args.
	r.handleEvent(&Event{
		Type:       EventToolExecutionStart,
		ToolCallID: "tc-claude-1",
		ToolName:   "bash",
		// No Args — Claude sends them in the follow-up update.
	})
	r.handleEvent(&Event{
		Type:       EventToolExecutionUpdate,
		ToolCallID: "tc-claude-1",
		ToolName:   "bash",
		Args:       json.RawMessage(`{"command":"git status"}`),
	})

	data, err := os.ReadFile(filepath.Join(runDir, "session.log"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "git status") {
		t.Errorf("session.log should contain tool args from update event, got: %s", content)
	}
}

func TestRunner_HandleEvent_ToolUpdate_LogsNonCommandArgs(t *testing.T) {
	r, runDir := newTestRunner(t)

	var err error
	r.sessionFile, err = os.Create(filepath.Join(runDir, "session.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.sessionFile.Close()

	r.handleEvent(&Event{
		Type:       EventToolExecutionUpdate,
		ToolCallID: "tc-update-2",
		ToolName:   "read",
		Args:       json.RawMessage(`{"file_path":"/home/user/file.go"}`),
	})

	data, err := os.ReadFile(filepath.Join(runDir, "session.log"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "/home/user/file.go") {
		t.Errorf("session.log should contain non-command args from update, got: %s", content)
	}
}

func TestRunner_HandleEvent_ToolUpdate_NoArgsNoOutput(t *testing.T) {
	r, runDir := newTestRunner(t)

	var err error
	r.sessionFile, err = os.Create(filepath.Join(runDir, "session.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.sessionFile.Close()

	// Update without args (e.g. partial result only) — should not crash or log args.
	r.handleEvent(&Event{
		Type:       EventToolExecutionUpdate,
		ToolCallID: "tc-update-3",
		ToolName:   "bash",
		// No Args.
	})

	data, err := os.ReadFile(filepath.Join(runDir, "session.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 0 {
		t.Errorf("session.log should be empty when update has no args, got: %s", string(data))
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
		if _, werr := fmt.Fprintln(r.eventsFile, string(data)); werr != nil {
			t.Fatalf("write event %d: %v", i, werr)
		}
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

	r.writeMeta(StatusCompleted, 3)

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
	if meta.EndedAt == "" {
		t.Error("expected ended_at to be set for completed status")
	}
}

func TestRunner_WriteMeta_Running_EmptyEndedAt(t *testing.T) {
	r, runDir := newTestRunner(t)
	r.startedAt = time.Now()

	r.writeMeta(StatusRunning, 2)

	data, err := os.ReadFile(runDir + "/meta.json")
	if err != nil {
		t.Fatal(err)
	}

	var meta RunMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("meta.json is not valid JSON: %v", err)
	}

	if meta.Status != "running" {
		t.Errorf("expected status=running, got %q", meta.Status)
	}
	if meta.EndedAt != "" {
		t.Errorf("expected ended_at to be empty for running status, got %q", meta.EndedAt)
	}
	if meta.IterationsCompleted != 2 {
		t.Errorf("expected iterations_completed=2, got %d", meta.IterationsCompleted)
	}
}

func TestRunner_WriteMeta_Terminal_HasEndedAt(t *testing.T) {
	r, runDir := newTestRunner(t)
	r.startedAt = time.Now()

	for _, status := range []Status{StatusCompleted, StatusFailed, StatusInterrupted, StatusMaxIterationsReached, StatusStuck} {
		r.writeMeta(status, 5)

		data, err := os.ReadFile(runDir + "/meta.json")
		if err != nil {
			t.Fatal(err)
		}

		var meta RunMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			t.Fatalf("meta.json is not valid JSON: %v", err)
		}

		if meta.EndedAt == "" {
			t.Errorf("expected ended_at to be set for %s status", status)
		}
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

	data, err := os.ReadFile(filepath.Join(tmpDir, r.runID, "effective-prompt.md"))
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
	runDir := filepath.Join(tmpDir, r.runID)
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

// ---------------------------------------------------------------------------
// Fake agent for Run() integration tests
// ---------------------------------------------------------------------------

// fakeAgent implements the agent.Agent interface for testing the Run loop.
type fakeAgent struct {
	// responses is a queue of (assistantText, error) pairs, one per iteration.
	responses []fakeResponse
	callCount int
}

type fakeResponse struct {
	text   string
	err    error
	events []events.Event // events to emit via onEvent
}

func (f *fakeAgent) RunIteration(_ context.Context, _ string, onEvent func(events.Event)) (string, error) {
	if f.callCount >= len(f.responses) {
		return "", fmt.Errorf("fakeAgent: no more responses (call %d)", f.callCount)
	}
	resp := f.responses[f.callCount]
	f.callCount++

	for _, ev := range resp.events {
		onEvent(ev)
	}

	return resp.text, resp.err
}

// newTestRunnerWithAgent creates a Runner with a pre-injected fake agent.
func newTestRunnerWithAgent(t *testing.T, fa *fakeAgent, cfg RunConfig) *Runner {
	t.Helper()
	if cfg.RunsDir == "" {
		cfg.RunsDir = t.TempDir()
	}
	r := New(cfg)
	r.iterAgent = fa
	r.stderr = io.Discard
	return r
}

// ---------------------------------------------------------------------------
// Run() integration tests
// ---------------------------------------------------------------------------

func TestRun_SingleIterationComplete(t *testing.T) {
	fa := &fakeAgent{
		responses: []fakeResponse{
			{text: "Done! " + completionMarker},
		},
	}
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:  "test",
		Prompt: "do something",
	})

	result := r.Run(context.Background())

	if result.Status != StatusCompleted {
		t.Errorf("status = %s, want %s", result.Status, StatusCompleted)
	}
	if result.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", result.Iterations)
	}
	if fa.callCount != 1 {
		t.Errorf("agent called %d times, want 1", fa.callCount)
	}
}

func TestRun_MultipleIterationsBeforeComplete(t *testing.T) {
	fa := &fakeAgent{
		responses: []fakeResponse{
			{text: "working..."},
			{text: "still working..."},
			{text: "all done " + completionMarker},
		},
	}
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:  "test",
		Prompt: "do something complex",
	})

	result := r.Run(context.Background())

	if result.Status != StatusCompleted {
		t.Errorf("status = %s, want %s", result.Status, StatusCompleted)
	}
	if result.Iterations != 3 {
		t.Errorf("iterations = %d, want 3", result.Iterations)
	}
}

func TestRun_MaxIterationsReached(t *testing.T) {
	fa := &fakeAgent{
		responses: []fakeResponse{
			{text: "iteration 1"},
			{text: "iteration 2"},
			{text: "iteration 3 (should not run)"},
		},
	}
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:         "test",
		Prompt:        "never finishes",
		MaxIterations: 2,
	})

	result := r.Run(context.Background())

	if result.Status != StatusMaxIterationsReached {
		t.Errorf("status = %s, want %s", result.Status, StatusMaxIterationsReached)
	}
	if result.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", result.Iterations)
	}
	if fa.callCount != 2 {
		t.Errorf("agent called %d times, want 2", fa.callCount)
	}
}

func TestRun_AgentError(t *testing.T) {
	fa := &fakeAgent{
		responses: []fakeResponse{
			{text: "first ok"},
			{text: "", err: fmt.Errorf("subprocess crashed")},
		},
	}
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:  "test",
		Prompt: "will fail",
	})

	result := r.Run(context.Background())

	if result.Status != StatusFailed {
		t.Errorf("status = %s, want %s", result.Status, StatusFailed)
	}
	if result.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", result.Iterations)
	}
}

func TestRun_WritesMetaJSON(t *testing.T) {
	fa := &fakeAgent{
		responses: []fakeResponse{
			{text: completionMarker},
		},
	}
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:        "test",
		Prompt:       "check meta",
		PromptSource: "prompt",
		PromptFile:   "/tmp/test.md",
	})

	result := r.Run(context.Background())

	metaPath := filepath.Join(r.cfg.RunsDir, r.runID, "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("reading meta.json: %v", err)
	}

	var meta RunMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("parsing meta.json: %v", err)
	}

	if meta.RunID != result.RunID {
		t.Errorf("meta.run_id = %q, want %q", meta.RunID, result.RunID)
	}
	if meta.Status != "completed" {
		t.Errorf("meta.status = %q, want %q", meta.Status, "completed")
	}
	if meta.Agent != "test" {
		t.Errorf("meta.agent = %q, want %q", meta.Agent, "test")
	}
	if meta.PromptSource != "prompt" {
		t.Errorf("meta.prompt_source = %q, want %q", meta.PromptSource, "prompt")
	}
	if meta.PromptFile != "/tmp/test.md" {
		t.Errorf("meta.prompt_file = %q, want %q", meta.PromptFile, "/tmp/test.md")
	}
	if meta.IterationsCompleted != 1 {
		t.Errorf("meta.iterations_completed = %d, want 1", meta.IterationsCompleted)
	}
}

func TestRun_WritesEffectivePrompt(t *testing.T) {
	fa := &fakeAgent{
		responses: []fakeResponse{
			{text: completionMarker},
		},
	}
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:  "test",
		Prompt: "the effective prompt text",
	})

	r.Run(context.Background())

	promptPath := filepath.Join(r.cfg.RunsDir, r.runID, "effective-prompt.md")
	data, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("reading effective-prompt.md: %v", err)
	}
	if string(data) != "the effective prompt text" {
		t.Errorf("effective prompt = %q, want %q", string(data), "the effective prompt text")
	}
}

func TestRun_PersistsEventsToJSONL(t *testing.T) {
	fa := &fakeAgent{
		responses: []fakeResponse{
			{
				text: completionMarker,
				events: []events.Event{
					{Type: events.EventMessageStart, Message: json.RawMessage(`{"role":"assistant","model":"test"}`)},
					{Type: events.EventMessageEnd},
					{Type: events.EventTurnEnd},
				},
			},
		},
	}
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:  "test",
		Prompt: "check events",
	})

	r.Run(context.Background())

	eventsPath := filepath.Join(r.cfg.RunsDir, r.runID, "events.jsonl")
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("reading events.jsonl: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	// 3 agent events emitted
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines in events.jsonl, got %d", len(lines))
	}

	// Verify first event type.
	var first events.Event
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("parsing first event: %v", err)
	}
	if first.Type != events.EventMessageStart {
		t.Errorf("first event type = %s, want %s", first.Type, events.EventMessageStart)
	}
}

func TestRun_ForwardsEventsToTUIChannel(t *testing.T) {
	ch := make(chan Event, 100)
	fa := &fakeAgent{
		responses: []fakeResponse{
			{
				text: completionMarker,
				events: []events.Event{
					{Type: events.EventMessageStart, Message: json.RawMessage(`{"role":"assistant","model":"test"}`)},
					{Type: events.EventTurnEnd},
				},
			},
		},
	}
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:     "test",
		Prompt:    "check tui",
		EventChan: ch,
	})

	r.Run(context.Background())

	// Drain the channel and count events.
	// We expect: 1 synthetic iteration event + 2 agent events = 3.
	var received []Event
	for {
		select {
		case ev := <-ch:
			received = append(received, ev)
		default:
			goto done
		}
	}
done:
	if len(received) < 3 {
		t.Errorf("expected at least 3 events on TUI channel, got %d", len(received))
	}

	// First event should be the synthetic iteration event.
	if received[0].Type != EventIteration {
		t.Errorf("first TUI event type = %s, want %s", received[0].Type, EventIteration)
	}
}

func TestRun_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fa := &fakeAgent{
		responses: []fakeResponse{
			{text: "", err: context.Canceled},
		},
	}
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:  "test",
		Prompt: "will cancel",
	})

	cancel() // Cancel before running.
	result := r.Run(ctx)

	// The agent returns context.Canceled, which the runner recognises as an
	// interruption (e.g. user quit the TUI) rather than a failure.
	if result.Status != StatusInterrupted {
		t.Errorf("status = %s, want %s", result.Status, StatusInterrupted)
	}
}

// metaSnapshotAgent wraps an agent.Agent and reads meta.json before each
// iteration, capturing snapshots of the on-disk metadata while the run
// is still in progress.
type metaSnapshotAgent struct {
	inner     *fakeAgent
	runner    *Runner
	snapshots *[]RunMeta
}

func (m *metaSnapshotAgent) RunIteration(ctx context.Context, prompt string, onEvent func(events.Event)) (string, error) {
	metaPath := filepath.Join(m.runner.cfg.RunsDir, m.runner.runID, "meta.json")
	if data, err := os.ReadFile(metaPath); err == nil {
		var meta RunMeta
		if err := json.Unmarshal(data, &meta); err == nil {
			*m.snapshots = append(*m.snapshots, meta)
		}
	}
	return m.inner.RunIteration(ctx, prompt, onEvent)
}

func TestRun_WritesRunningMetaDuringLoop(t *testing.T) {
	var metaSnapshots []RunMeta

	fa := &fakeAgent{
		responses: []fakeResponse{
			{text: "working..."},
			{text: "still going..."},
			{text: completionMarker},
		},
	}

	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:  "test",
		Prompt: "check running meta",
	})

	// Replace the fake agent with a snapshot wrapper.
	r.iterAgent = &metaSnapshotAgent{
		inner:     fa,
		runner:    r,
		snapshots: &metaSnapshots,
	}

	result := r.Run(context.Background())

	// Should have captured meta during each of the 3 iterations.
	if len(metaSnapshots) != 3 {
		t.Fatalf("expected 3 meta snapshots, got %d", len(metaSnapshots))
	}

	// All mid-run snapshots should be "running" with empty ended_at.
	for i, snap := range metaSnapshots {
		if snap.Status != "running" {
			t.Errorf("snapshot %d: status = %q, want %q", i, snap.Status, "running")
		}
		if snap.EndedAt != "" {
			t.Errorf("snapshot %d: ended_at = %q, want empty", i, snap.EndedAt)
		}
		if snap.IterationsCompleted != i+1 {
			t.Errorf("snapshot %d: iterations_completed = %d, want %d", i, snap.IterationsCompleted, i+1)
		}
	}

	// Final meta on disk should be terminal.
	metaPath := filepath.Join(r.cfg.RunsDir, r.runID, "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	var finalMeta RunMeta
	if err := json.Unmarshal(data, &finalMeta); err != nil {
		t.Fatal(err)
	}
	if finalMeta.Status != string(result.Status) {
		t.Errorf("final status = %q, want %q", finalMeta.Status, result.Status)
	}
	if finalMeta.EndedAt == "" {
		t.Error("final ended_at should be populated")
	}
	if finalMeta.IterationsCompleted != 3 {
		t.Errorf("final iterations = %d, want 3", finalMeta.IterationsCompleted)
	}
}

func TestRun_InitialMetaBeforeAgentStarts(t *testing.T) {
	fa := &fakeAgent{
		responses: []fakeResponse{
			{text: completionMarker},
		},
	}
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:  "test",
		Prompt: "check initial meta",
	})

	// Wrap to capture meta at iteration 1 — the initial write (iterations=0)
	// should already be on disk before this, so we check that the snapshot
	// at iteration 1 shows iterations_completed=1 (overwritten by the
	// per-iteration write).
	var snapshots []RunMeta
	r.iterAgent = &metaSnapshotAgent{
		inner:     fa,
		runner:    r,
		snapshots: &snapshots,
	}

	r.Run(context.Background())

	if len(snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snapshots))
	}
	// By the time the agent runs, the per-iteration write has set iterations=1.
	if snapshots[0].IterationsCompleted != 1 {
		t.Errorf("snapshot iterations_completed = %d, want 1", snapshots[0].IterationsCompleted)
	}
	if snapshots[0].Agent != "test" {
		t.Errorf("snapshot agent = %q, want %q", snapshots[0].Agent, "test")
	}
}

func TestRun_StoresEventsInMemory(t *testing.T) {
	fa := &fakeAgent{
		responses: []fakeResponse{
			{
				text: completionMarker,
				events: []events.Event{
					{Type: events.EventSession, ID: "sess-1"},
					{Type: events.EventTurnEnd},
				},
			},
		},
	}
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:  "test",
		Prompt: "check memory",
	})

	r.Run(context.Background())

	if len(r.events) != 2 {
		t.Fatalf("expected 2 events in memory, got %d", len(r.events))
	}
	if r.events[0].Type != events.EventSession {
		t.Errorf("first in-memory event = %s, want %s", r.events[0].Type, events.EventSession)
	}
}

// ---------------------------------------------------------------------------
// Inactivity watchdog
// ---------------------------------------------------------------------------

// agentBehavior defines a single iteration's behavior for flexAgent.
type agentBehavior = func(ctx context.Context, onEvent func(events.Event)) (string, error)

// flexAgent allows per-call behavior specification for testing the watchdog.
// Each call's prompt is recorded in prompts so tests can assert what the
// runner actually sent the agent.
type flexAgent struct {
	behaviors []agentBehavior
	callCount int
	prompts   []string
}

func (f *flexAgent) RunIteration(ctx context.Context, prompt string, onEvent func(events.Event)) (string, error) {
	if f.callCount >= len(f.behaviors) {
		return "", fmt.Errorf("flexAgent: no more behaviors (call %d)", f.callCount)
	}
	f.prompts = append(f.prompts, prompt)
	fn := f.behaviors[f.callCount]
	f.callCount++
	return fn(ctx, onEvent)
}

func TestRun_InactivityTimeout_RetriesOnce(t *testing.T) {
	// Agent hangs on every call — never sends events.
	hang := func(ctx context.Context, _ func(events.Event)) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	ch := make(chan Event, 100)
	fa := &flexAgent{behaviors: []agentBehavior{hang, hang}}

	timeout := 100 * time.Millisecond
	r := New(RunConfig{
		Agent:             "test",
		Prompt:            "test",
		InactivityTimeout: &timeout,
		RunsDir:           t.TempDir(),
		EventChan:         ch,
	})
	r.iterAgent = fa
	r.stderr = io.Discard

	result := r.Run(context.Background())

	if result.Status != StatusStuck {
		t.Errorf("status = %s, want %s", result.Status, StatusStuck)
	}
	if fa.callCount != 2 {
		t.Errorf("agent called %d times, want 2", fa.callCount)
	}
	if result.Error == "" {
		t.Error("expected non-empty error message")
	}
	if !strings.Contains(result.Error, "2 consecutive timeouts") {
		t.Errorf("error = %q, want it to contain '2 consecutive timeouts'", result.Error)
	}
	// The timed-out iteration is not counted, so 1 iteration is recorded
	// (the second timeout doesn't decrement).
	if result.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", result.Iterations)
	}

	// Verify an EventInactivityTimeout was sent to the TUI channel.
	var gotTimeout bool
	for {
		select {
		case ev := <-ch:
			if ev.Type == EventInactivityTimeout {
				gotTimeout = true
			}
		default:
			goto drainDone
		}
	}
drainDone:
	if !gotTimeout {
		t.Error("expected EventInactivityTimeout on TUI channel")
	}
}

func TestRun_InactivityTimeout_ResetsOnEvent(t *testing.T) {
	// Agent sends events at intervals shorter than the timeout — no timeout.
	slowComplete := func(ctx context.Context, onEvent func(events.Event)) (string, error) {
		for i := 0; i < 5; i++ {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(30 * time.Millisecond):
				onEvent(events.Event{Type: events.EventMessageUpdate})
			}
		}
		return completionMarker, nil
	}
	fa := &flexAgent{behaviors: []agentBehavior{slowComplete}}

	timeout := 200 * time.Millisecond
	r := New(RunConfig{
		Agent:             "test",
		Prompt:            "test",
		InactivityTimeout: &timeout,
		RunsDir:           t.TempDir(),
	})
	r.iterAgent = fa
	r.stderr = io.Discard

	result := r.Run(context.Background())

	if result.Status != StatusCompleted {
		t.Errorf("status = %s, want %s", result.Status, StatusCompleted)
	}
	if result.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", result.Iterations)
	}
}

func TestRun_InactivityTimeout_ResetsAfterSuccess(t *testing.T) {
	hang := func(ctx context.Context, _ func(events.Event)) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	continueIter := func(_ context.Context, onEvent func(events.Event)) (string, error) {
		onEvent(events.Event{Type: events.EventTurnEnd})
		return "continuing", nil
	}
	complete := func(_ context.Context, onEvent func(events.Event)) (string, error) {
		onEvent(events.Event{Type: events.EventTurnEnd})
		return completionMarker, nil
	}

	// Sequence: hang → succeed(continue) → hang → succeed(complete)
	// First hang triggers timeout+retry, succeed resets the counter,
	// second hang triggers timeout+retry (proving the reset worked),
	// final call completes.
	fa := &flexAgent{behaviors: []agentBehavior{hang, continueIter, hang, complete}}

	timeout := 100 * time.Millisecond
	r := New(RunConfig{
		Agent:             "test",
		Prompt:            "test",
		InactivityTimeout: &timeout,
		RunsDir:           t.TempDir(),
	})
	r.iterAgent = fa
	r.stderr = io.Discard

	result := r.Run(context.Background())

	if result.Status != StatusCompleted {
		t.Errorf("status = %s, want %s", result.Status, StatusCompleted)
	}
	if fa.callCount != 4 {
		t.Errorf("agent called %d times, want 4", fa.callCount)
	}
	// Two successful iterations (timed-out ones are not counted).
	if result.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", result.Iterations)
	}
}

func TestRun_InactivityTimeout_ZeroDisablesWatchdog(t *testing.T) {
	// Agent stays silent longer than the default 5m would normally allow,
	// then completes. With the watchdog disabled (zero pointer) the run
	// should complete normally — no timeout, no retry.
	silentThenComplete := func(ctx context.Context, onEvent func(events.Event)) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
		onEvent(events.Event{Type: events.EventTurnEnd})
		return completionMarker, nil
	}
	fa := &flexAgent{behaviors: []agentBehavior{silentThenComplete}}

	disabled := time.Duration(0)
	r := New(RunConfig{
		Agent:             "test",
		Prompt:            "test",
		InactivityTimeout: &disabled,
		RunsDir:           t.TempDir(),
	})
	r.iterAgent = fa
	r.stderr = io.Discard

	// Use a safety-net context so a regression doesn't hang CI forever.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := r.Run(ctx)

	if result.Status != StatusCompleted {
		t.Errorf("status = %s, want %s", result.Status, StatusCompleted)
	}
	if fa.callCount != 1 {
		t.Errorf("agent called %d times, want 1 (no retry)", fa.callCount)
	}
}

// TestRun_InactivityTimeout_LiveUpdate_Extends verifies that ControlSetTimeout
// sent mid-iteration is picked up by the next watchdog.Reset, extending the
// timer so that the iteration does not time out at the original (shorter)
// duration.
func TestRun_InactivityTimeout_LiveUpdate_Extends(t *testing.T) {
	initial := 100 * time.Millisecond
	extended := 2 * time.Second

	firstEventEmitted := make(chan struct{})
	secondEventCanFire := make(chan struct{})

	behavior := func(ctx context.Context, onEvent func(events.Event)) (string, error) {
		// First event: at this point the watchdog is set to the initial 100ms.
		// Reset here also reads the initial value (test hasn't pushed yet).
		onEvent(events.Event{Type: events.EventTurnEnd})
		close(firstEventEmitted)

		// Wait for the test to push the extended timeout into controlState.
		select {
		case <-secondEventCanFire:
		case <-ctx.Done():
			return "", ctx.Err()
		}

		// Second event: Reset reads the live (now 2s) value, extending the
		// watchdog so the upcoming hang doesn't trigger a timeout.
		onEvent(events.Event{Type: events.EventTurnEnd})

		// Hang for longer than the original 100ms but well within 2s.
		select {
		case <-time.After(250 * time.Millisecond):
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return completionMarker, nil
	}
	fa := &flexAgent{behaviors: []agentBehavior{behavior}}

	controlCh := make(chan ControlMsg, 4)
	r := New(RunConfig{
		Agent:             "test",
		Prompt:            "test",
		InactivityTimeout: &initial,
		RunsDir:           t.TempDir(),
		ControlChan:       controlCh,
	})
	r.iterAgent = fa
	r.stderr = io.Discard

	runDone := make(chan RunResult, 1)
	go func() {
		runDone <- r.Run(context.Background())
	}()

	// Wait for the first event so we know the watchdog is armed with 100ms.
	select {
	case <-firstEventEmitted:
	case <-time.After(2 * time.Second):
		t.Fatal("first event not emitted in time")
	}

	// Push the extended timeout. The runner's goroutine processes this in
	// its select loop almost immediately.
	controlCh <- ControlMsg{Kind: ControlSetTimeout, Timeout: &extended}

	// Wait for controlState to reflect the change. This must complete well
	// before the 100ms initial timer fires.
	deadline := time.Now().Add(80 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, got := r.control.watchdogState(); got == extended {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, got := r.control.watchdogState(); got != extended {
		t.Fatal("controlState not updated to extended timeout in time")
	}

	// Signal the agent to emit its second event, picking up the new timeout.
	close(secondEventCanFire)

	select {
	case result := <-runDone:
		if result.Status != StatusCompleted {
			t.Errorf("status = %s, want %s (timed out before live update could take effect)", result.Status, StatusCompleted)
		}
		if fa.callCount != 1 {
			t.Errorf("agent called %d times, want 1 (no retry)", fa.callCount)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not complete in time")
	}
}

// TestRun_InactivityTimeout_LiveUpdate_Disables verifies that sending
// ControlSetTimeout with a zero pointer mid-iteration stops the live
// watchdog timer, allowing the iteration to run uninterrupted.
func TestRun_InactivityTimeout_LiveUpdate_Disables(t *testing.T) {
	initial := 100 * time.Millisecond
	disabled := time.Duration(0)

	canComplete := make(chan struct{})

	behavior := func(ctx context.Context, _ func(events.Event)) (string, error) {
		// Hang until either the test signals completion or the context is
		// cancelled (would happen if the watchdog fired).
		select {
		case <-canComplete:
			return completionMarker, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	fa := &flexAgent{behaviors: []agentBehavior{behavior}}

	controlCh := make(chan ControlMsg, 4)
	r := New(RunConfig{
		Agent:             "test",
		Prompt:            "test",
		InactivityTimeout: &initial,
		RunsDir:           t.TempDir(),
		ControlChan:       controlCh,
	})
	r.iterAgent = fa
	r.stderr = io.Discard

	runDone := make(chan RunResult, 1)
	go func() {
		runDone <- r.Run(context.Background())
	}()

	// Disable the watchdog quickly, before the initial 100ms can fire.
	controlCh <- ControlMsg{Kind: ControlSetTimeout, Timeout: &disabled}

	// Wait for controlState to reflect the disable.
	deadline := time.Now().Add(80 * time.Millisecond)
	for time.Now().Before(deadline) {
		if d, _ := r.control.watchdogState(); d {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if d, _ := r.control.watchdogState(); !d {
		t.Fatal("controlState not updated to disabled in time")
	}

	// Sleep well past 2× the original timeout (which would otherwise have
	// produced StatusStuck via two consecutive timeouts).
	time.Sleep(300 * time.Millisecond)

	// The run should still be in progress.
	select {
	case result := <-runDone:
		t.Fatalf("run completed unexpectedly: status=%s iterations=%d", result.Status, result.Iterations)
	default:
	}

	// Let the agent finish.
	close(canComplete)

	select {
	case result := <-runDone:
		if result.Status != StatusCompleted {
			t.Errorf("status = %s, want %s", result.Status, StatusCompleted)
		}
		if fa.callCount != 1 {
			t.Errorf("agent called %d times, want 1 (no retry)", fa.callCount)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not complete in time")
	}
}

// ---------------------------------------------------------------------------
// NewRunID and RunConfig.RunID
// ---------------------------------------------------------------------------

func TestNewRunID_IsValidUUID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	id := NewRunID()
	if !re.MatchString(id) {
		t.Errorf("NewRunID() = %q, does not match UUID v4 pattern", id)
	}
}

func TestNew_UsesProvidedRunID(t *testing.T) {
	r := New(RunConfig{
		Agent:  "test",
		Prompt: "test",
		RunID:  "my-custom-run-id",
	})
	if r.runID != "my-custom-run-id" {
		t.Errorf("runID = %q, want %q", r.runID, "my-custom-run-id")
	}
}

func TestNew_GeneratesRunIDWhenEmpty(t *testing.T) {
	r := New(RunConfig{
		Agent:  "test",
		Prompt: "test",
	})
	if r.runID == "" {
		t.Error("runID should not be empty when RunID is not set")
	}
}

func TestRun_UsesProvidedRunID(t *testing.T) {
	fa := &fakeAgent{
		responses: []fakeResponse{
			{text: completionMarker},
		},
	}
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:  "test",
		Prompt: "check run id",
		RunID:  "provided-run-id",
	})

	result := r.Run(context.Background())

	if result.RunID != "provided-run-id" {
		t.Errorf("result.RunID = %q, want %q", result.RunID, "provided-run-id")
	}
}

// ---------------------------------------------------------------------------
// Memory file creation
// ---------------------------------------------------------------------------

func TestRun_CreatesMemoryFiles(t *testing.T) {
	fa := &fakeAgent{
		responses: []fakeResponse{
			{text: completionMarker},
		},
	}
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:  "test",
		Prompt: "check memory files",
	})

	r.Run(context.Background())

	runDir := filepath.Join(r.cfg.RunsDir, r.runID)
	for _, name := range []string{"NOTES.md", "PROGRESS.md"} {
		path := filepath.Join(runDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("expected %s to exist: %v", name, err)
			continue
		}
		if len(data) != 0 {
			t.Errorf("%s should be empty, got %q", name, string(data))
		}
	}
}

func TestInitMemoryFiles_SkipsExistingFiles(t *testing.T) {
	tmpDir := t.TempDir()
	runID := "skip-existing"
	runDir := filepath.Join(tmpDir, runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Pre-create NOTES.md with content (simulating resume copy).
	existing := "# Previous session notes"
	if err := os.WriteFile(filepath.Join(runDir, "NOTES.md"), []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}

	r := &Runner{
		cfg: RunConfig{
			RunsDir: tmpDir,
		},
		runID:  runID,
		stderr: io.Discard,
	}

	r.initMemoryFiles()

	// NOTES.md should keep its content.
	data, err := os.ReadFile(filepath.Join(runDir, "NOTES.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != existing {
		t.Errorf("NOTES.md = %q, want %q (should not be overwritten)", string(data), existing)
	}

	// PROGRESS.md should be created empty.
	data, err = os.ReadFile(filepath.Join(runDir, "PROGRESS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("PROGRESS.md should be empty, got %q", string(data))
	}
}

// ---------------------------------------------------------------------------
// controlState accessors (Task 1)
// ---------------------------------------------------------------------------

func TestControlState_AddRemoveReminder(t *testing.T) {
	c := newControlState(nil)

	r1 := c.addReminder(Reminder{Kind: ReminderOneOff, Text: "first"})
	r2 := c.addReminder(Reminder{Kind: ReminderPersistent, Text: "second"})

	if r1.ID == "" || r2.ID == "" {
		t.Fatalf("addReminder should assign IDs, got %q and %q", r1.ID, r2.ID)
	}
	if r1.ID == r2.ID {
		t.Fatalf("addReminder should assign unique IDs, got duplicate %q", r1.ID)
	}

	snap := c.snapshotReminders()
	if len(snap) != 2 {
		t.Fatalf("snapshotReminders len = %d, want 2", len(snap))
	}
	if snap[0].Text != "first" || snap[1].Text != "second" {
		t.Errorf("snapshot order wrong: %+v", snap)
	}

	if !c.removeReminder(r1.ID) {
		t.Errorf("removeReminder(%q) returned false; want true", r1.ID)
	}
	if c.removeReminder(r1.ID) {
		t.Errorf("second removeReminder(%q) returned true; want false", r1.ID)
	}

	snap = c.snapshotReminders()
	if len(snap) != 1 || snap[0].ID != r2.ID {
		t.Errorf("after remove, snapshot = %+v", snap)
	}
}

func TestControlState_SnapshotReturnsCopy(t *testing.T) {
	c := newControlState(nil)
	c.addReminder(Reminder{Kind: ReminderPersistent, Text: "keep"})

	snap := c.snapshotReminders()
	if len(snap) != 1 {
		t.Fatalf("snapshot len = %d, want 1", len(snap))
	}
	snap[0].Text = "mutated"

	snap2 := c.snapshotReminders()
	if snap2[0].Text != "keep" {
		t.Errorf("mutating snapshot affected internal state: got %q, want %q", snap2[0].Text, "keep")
	}
}

func TestControlState_ConsumeOneOffsLeavesPersistent(t *testing.T) {
	c := newControlState(nil)
	o1 := c.addReminder(Reminder{Kind: ReminderOneOff, Text: "one"})
	p := c.addReminder(Reminder{Kind: ReminderPersistent, Text: "persist"})
	o2 := c.addReminder(Reminder{Kind: ReminderOneOff, Text: "two"})

	consumed := c.consumeOneOffs()
	if len(consumed) != 2 {
		t.Fatalf("consumeOneOffs returned %d IDs, want 2", len(consumed))
	}
	got := map[string]bool{consumed[0]: true, consumed[1]: true}
	if !got[o1.ID] || !got[o2.ID] {
		t.Errorf("consumed IDs = %v, want both %q and %q", consumed, o1.ID, o2.ID)
	}

	snap := c.snapshotReminders()
	if len(snap) != 1 || snap[0].ID != p.ID {
		t.Errorf("after consume, snapshot = %+v; expected only persistent %q", snap, p.ID)
	}
}

func TestControlState_RestartFlag(t *testing.T) {
	c := newControlState(nil)

	if c.takeRestartRequested() {
		t.Error("takeRestartRequested should be false initially")
	}

	c.requestRestart()
	if !c.takeRestartRequested() {
		t.Error("takeRestartRequested should return true after requestRestart()")
	}
	if c.takeRestartRequested() {
		t.Error("takeRestartRequested should clear the flag after returning true")
	}
}

func TestControlState_TimeoutSemantics(t *testing.T) {
	// Default (nil) → not disabled, default duration.
	c := newControlState(nil)
	if c.watchdogDisabled() {
		t.Error("nil timeout should not be disabled")
	}
	if got := c.effectiveTimeout(); got != defaultInactivityTimeout {
		t.Errorf("default effectiveTimeout = %s, want %s", got, defaultInactivityTimeout)
	}

	// Disabled (pointer to 0).
	zero := time.Duration(0)
	c.setTimeout(&zero)
	if !c.watchdogDisabled() {
		t.Error("pointer to 0 should be disabled")
	}

	// Custom value.
	custom := 250 * time.Millisecond
	c.setTimeout(&custom)
	if c.watchdogDisabled() {
		t.Error("positive timeout should not be disabled")
	}
	if got := c.effectiveTimeout(); got != custom {
		t.Errorf("effectiveTimeout = %s, want %s", got, custom)
	}
}

func TestNewReminderID_Format(t *testing.T) {
	re := regexp.MustCompile(`^rmd-[0-9a-f]{8}$`)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		id := newReminderID()
		if !re.MatchString(id) {
			t.Errorf("newReminderID() = %q, want pattern rmd-<8 hex>", id)
		}
		if seen[id] {
			t.Errorf("newReminderID() returned duplicate %q", id)
		}
		seen[id] = true
	}
}

// TestRun_ControlChan_SetTimeoutUpdatesState verifies the integration: a
// Runner constructed with a ControlChan dispatches ControlSetTimeout messages
// to controlState. Behavioural watchdog updates land in Task 3; here we only
// check that the state mutates.
func TestRun_ControlChan_SetTimeoutUpdatesState(t *testing.T) {
	timeoutApplied := make(chan struct{})
	hang := func(ctx context.Context, _ func(events.Event)) (string, error) {
		// Wait until the test sees the timeout update, then return.
		select {
		case <-timeoutApplied:
			return completionMarker, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	fa := &flexAgent{behaviors: []agentBehavior{hang}}

	controlCh := make(chan ControlMsg, 4)
	r := New(RunConfig{
		Agent:       "test",
		Prompt:      "test",
		RunsDir:     t.TempDir(),
		ControlChan: controlCh,
	})
	r.iterAgent = fa
	r.stderr = io.Discard

	runDone := make(chan RunResult, 1)
	go func() {
		runDone <- r.Run(context.Background())
	}()

	// Send a custom timeout via the control channel.
	custom := 750 * time.Millisecond
	controlCh <- ControlMsg{Kind: ControlSetTimeout, Timeout: &custom}

	// Wait until the runner has dispatched the message into controlState.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := r.control.effectiveTimeout(); got == custom {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := r.control.effectiveTimeout(); got != custom {
		t.Fatalf("effectiveTimeout after ControlSetTimeout = %s, want %s", got, custom)
	}

	// Let the agent finish so Run returns.
	close(timeoutApplied)
	result := <-runDone
	if result.Status != StatusCompleted {
		t.Errorf("status = %s, want %s", result.Status, StatusCompleted)
	}
}

// ---------------------------------------------------------------------------
// Iteration restart (Task 2)
// ---------------------------------------------------------------------------

// TestRun_ControlChan_RequestRestart_RedoesIteration verifies that sending
// ControlRequestRestart cancels the in-flight iteration, the runner reruns
// the same iteration, and an EventIterationRestart is emitted.
func TestRun_ControlChan_RequestRestart_RedoesIteration(t *testing.T) {
	started := make(chan int, 4)
	hangThenCancel := func(ctx context.Context, _ func(events.Event)) (string, error) {
		started <- 1
		<-ctx.Done()
		return "", ctx.Err()
	}
	complete := func(_ context.Context, _ func(events.Event)) (string, error) {
		started <- 2
		return completionMarker, nil
	}
	fa := &flexAgent{behaviors: []agentBehavior{hangThenCancel, complete}}

	eventCh := make(chan Event, 64)
	controlCh := make(chan ControlMsg, 4)
	r := New(RunConfig{
		Agent:       "test",
		Prompt:      "test",
		RunsDir:     t.TempDir(),
		EventChan:   eventCh,
		ControlChan: controlCh,
	})
	r.iterAgent = fa
	r.stderr = io.Discard

	runDone := make(chan RunResult, 1)
	go func() {
		runDone <- r.Run(context.Background())
	}()

	// Wait for the first agent call to reach its blocking point, then ask
	// for a restart.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first agent call did not start in time")
	}
	controlCh <- ControlMsg{Kind: ControlRequestRestart}

	result := <-runDone

	if result.Status != StatusCompleted {
		t.Errorf("status = %s, want %s", result.Status, StatusCompleted)
	}
	if result.Iterations != 1 {
		t.Errorf("iterations = %d, want 1 (restart should not increment)", result.Iterations)
	}
	if fa.callCount != 2 {
		t.Errorf("agent called %d times, want 2 (one cancelled + one completing)", fa.callCount)
	}

	// Drain events and look for the restart event.
	var gotRestart bool
	for {
		select {
		case ev := <-eventCh:
			if ev.Type == EventIterationRestart {
				gotRestart = true
				if !strings.HasPrefix(ev.ID, "restart-1-") {
					t.Errorf("restart event ID = %q, want prefix %q", ev.ID, "restart-1-")
				}
			}
		default:
			goto drainDone
		}
	}
drainDone:
	if !gotRestart {
		t.Error("expected EventIterationRestart on event channel")
	}
}

// TestRun_ControlChan_RequestRestart_RespectsMaxIterations verifies that
// restarts neither bypass nor exhaust the MaxIterations budget. Two real
// iterations complete after restarts; the third would-be iteration is
// stopped by MaxIterations.
func TestRun_ControlChan_RequestRestart_RespectsMaxIterations(t *testing.T) {
	type signal struct{}
	started := make(chan signal, 16)
	hangThenCancel := func(ctx context.Context, _ func(events.Event)) (string, error) {
		started <- signal{}
		<-ctx.Done()
		return "", ctx.Err()
	}
	continueIter := func(_ context.Context, onEvent func(events.Event)) (string, error) {
		started <- signal{}
		onEvent(events.Event{Type: events.EventTurnEnd})
		return "still working", nil
	}
	fa := &flexAgent{behaviors: []agentBehavior{
		hangThenCancel, continueIter,
		hangThenCancel, continueIter,
	}}

	controlCh := make(chan ControlMsg, 4)
	r := New(RunConfig{
		Agent:         "test",
		Prompt:        "test",
		RunsDir:       t.TempDir(),
		MaxIterations: 2,
		ControlChan:   controlCh,
	})
	r.iterAgent = fa
	r.stderr = io.Discard

	runDone := make(chan RunResult, 1)
	go func() {
		runDone <- r.Run(context.Background())
	}()

	// Wait for the first hang, send restart.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first agent call did not start in time")
	}
	controlCh <- ControlMsg{Kind: ControlRequestRestart}

	// Wait for the continue call (iteration 1's real attempt).
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("continue agent call did not start in time")
	}

	// Wait for iteration 2's first hang, send restart again.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("iteration 2 hang did not start in time")
	}
	controlCh <- ControlMsg{Kind: ControlRequestRestart}

	// Wait for iteration 2's real continue.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("iteration 2 continue did not start in time")
	}

	result := <-runDone

	if result.Status != StatusMaxIterationsReached {
		t.Errorf("status = %s, want %s", result.Status, StatusMaxIterationsReached)
	}
	if result.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", result.Iterations)
	}
	if fa.callCount != 4 {
		t.Errorf("agent called %d times, want 4 (two hangs + two continues)", fa.callCount)
	}
}

// ---------------------------------------------------------------------------
// Reminder injection and consumption (Task 4)
// ---------------------------------------------------------------------------

// TestRun_OneOff_ConsumedAfterIteration verifies that a one-off reminder is
// included in the prompt for the iteration in which it was active and removed
// afterwards, so the next iteration's prompt does not contain it.
func TestRun_OneOff_ConsumedAfterIteration(t *testing.T) {
	const reminderText = "REMEMBER-THIS-ONE-OFF"

	cont := func(_ context.Context, onEvent func(events.Event)) (string, error) {
		onEvent(events.Event{Type: events.EventTurnEnd})
		return "still working", nil
	}
	complete := func(_ context.Context, onEvent func(events.Event)) (string, error) {
		onEvent(events.Event{Type: events.EventTurnEnd})
		return completionMarker, nil
	}
	fa := &flexAgent{behaviors: []agentBehavior{cont, complete}}

	r := New(RunConfig{
		Agent:   "test",
		Prompt:  "base prompt",
		RunsDir: t.TempDir(),
	})
	r.iterAgent = fa
	r.stderr = io.Discard

	// Seed a one-off reminder before Run starts so it is present when the
	// first iteration builds its prompt.
	r.control.addReminder(Reminder{Kind: ReminderOneOff, Text: reminderText})

	result := r.Run(context.Background())

	if result.Status != StatusCompleted {
		t.Fatalf("status = %s, want %s", result.Status, StatusCompleted)
	}
	if len(fa.prompts) != 2 {
		t.Fatalf("agent called with %d prompts, want 2", len(fa.prompts))
	}
	if !strings.Contains(fa.prompts[0], reminderText) {
		t.Errorf("iteration 1 prompt missing reminder %q: %q", reminderText, fa.prompts[0])
	}
	if strings.Contains(fa.prompts[1], reminderText) {
		t.Errorf("iteration 2 prompt should NOT contain consumed one-off %q: %q", reminderText, fa.prompts[1])
	}
	if got := r.control.snapshotReminders(); len(got) != 0 {
		t.Errorf("after run, reminders = %+v, want empty", got)
	}
}

// TestRun_OneOff_NotConsumedOnRestart verifies that iterRestart does not
// consume one-off reminders: the first (cancelled) call and the second
// (completing) call both see the same reminder in their prompts. Consumption
// happens only after the completing call.
func TestRun_OneOff_NotConsumedOnRestart(t *testing.T) {
	const reminderText = "REMINDER-SURVIVES-RESTART"

	started := make(chan struct{}, 4)
	hangThenCancel := func(ctx context.Context, _ func(events.Event)) (string, error) {
		started <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	}
	complete := func(_ context.Context, _ func(events.Event)) (string, error) {
		started <- struct{}{}
		return completionMarker, nil
	}
	fa := &flexAgent{behaviors: []agentBehavior{hangThenCancel, complete}}

	controlCh := make(chan ControlMsg, 4)
	r := New(RunConfig{
		Agent:       "test",
		Prompt:      "base prompt",
		RunsDir:     t.TempDir(),
		ControlChan: controlCh,
	})
	r.iterAgent = fa
	r.stderr = io.Discard

	r.control.addReminder(Reminder{Kind: ReminderOneOff, Text: reminderText})

	runDone := make(chan RunResult, 1)
	go func() {
		runDone <- r.Run(context.Background())
	}()

	// Wait for the first agent call to block, then send a restart.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first agent call did not start in time")
	}
	controlCh <- ControlMsg{Kind: ControlRequestRestart}

	// Wait for the completing call.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("second agent call did not start in time")
	}

	result := <-runDone

	if result.Status != StatusCompleted {
		t.Fatalf("status = %s, want %s", result.Status, StatusCompleted)
	}
	if len(fa.prompts) != 2 {
		t.Fatalf("agent called with %d prompts, want 2", len(fa.prompts))
	}
	if !strings.Contains(fa.prompts[0], reminderText) {
		t.Errorf("first (cancelled) prompt missing reminder %q: %q", reminderText, fa.prompts[0])
	}
	if !strings.Contains(fa.prompts[1], reminderText) {
		t.Errorf("second (completing) prompt missing reminder %q — restart wrongly consumed it: %q", reminderText, fa.prompts[1])
	}
	if got := r.control.snapshotReminders(); len(got) != 0 {
		t.Errorf("after completing run, reminders = %+v, want empty", got)
	}
}

// TestRun_Persistent_SurvivesIterations verifies that a persistent reminder
// is included in every iteration's prompt and is not consumed when an
// iteration completes or continues.
func TestRun_Persistent_SurvivesIterations(t *testing.T) {
	const reminderText = "STICKY-REMINDER"

	cont := func(_ context.Context, onEvent func(events.Event)) (string, error) {
		onEvent(events.Event{Type: events.EventTurnEnd})
		return "still working", nil
	}
	complete := func(_ context.Context, onEvent func(events.Event)) (string, error) {
		onEvent(events.Event{Type: events.EventTurnEnd})
		return completionMarker, nil
	}
	fa := &flexAgent{behaviors: []agentBehavior{cont, complete}}

	r := New(RunConfig{
		Agent:   "test",
		Prompt:  "base prompt",
		RunsDir: t.TempDir(),
	})
	r.iterAgent = fa
	r.stderr = io.Discard

	r.control.addReminder(Reminder{Kind: ReminderPersistent, Text: reminderText})

	result := r.Run(context.Background())

	if result.Status != StatusCompleted {
		t.Fatalf("status = %s, want %s", result.Status, StatusCompleted)
	}
	if len(fa.prompts) != 2 {
		t.Fatalf("agent called with %d prompts, want 2", len(fa.prompts))
	}
	for i, p := range fa.prompts {
		if !strings.Contains(p, reminderText) {
			t.Errorf("iteration %d prompt missing persistent reminder %q: %q", i+1, reminderText, p)
		}
	}
	snap := r.control.snapshotReminders()
	if len(snap) != 1 || snap[0].Text != reminderText {
		t.Errorf("after run, reminders = %+v; persistent should remain", snap)
	}
}
