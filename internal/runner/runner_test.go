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

	// The agent returns context.Canceled, which surfaces as a failed status
	// since the runner treats all errors from the agent as failures.
	if result.Status != StatusFailed {
		t.Errorf("status = %s, want %s", result.Status, StatusFailed)
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
