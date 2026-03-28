package runner

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsmiamoto/ralfinho/internal/events"
)

type cancelOnContextAgent struct {
	started chan struct{}
}

func (a *cancelOnContextAgent) RunIteration(ctx context.Context, _ string, _ func(events.Event)) (string, error) {
	if a.started != nil {
		close(a.started)
	}
	<-ctx.Done()
	return "", ctx.Err()
}

type scriptedIterAgent struct {
	events []events.Event
	text   string
	err    error
}

func (a *scriptedIterAgent) RunIteration(_ context.Context, _ string, onEvent func(events.Event)) (string, error) {
	for _, ev := range a.events {
		onEvent(ev)
	}
	return a.text, a.err
}

func sendInterruptSignal(t *testing.T) {
	t.Helper()

	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess(%d): %v", os.Getpid(), err)
	}
	if err := proc.Signal(os.Interrupt); err != nil {
		t.Fatalf("Signal(os.Interrupt): %v", err)
	}
}

func waitForRunIterationResult(t *testing.T, resultCh <-chan struct {
	status iterStatus
	err    error
}) (iterStatus, error) {
	t.Helper()

	select {
	case result := <-resultCh:
		return result.status, result.err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for runIteration result")
		return iterContinue, nil
	}
}

func newTestRunnerWithIterAgent(t *testing.T, iterAgent interface {
	RunIteration(context.Context, string, func(events.Event)) (string, error)
}, cfg RunConfig) *Runner {
	t.Helper()

	if cfg.RunsDir == "" {
		cfg.RunsDir = t.TempDir()
	}
	r := New(cfg)
	r.iterAgent = iterAgent
	r.stderr = io.Discard
	return r
}

func TestRunner_RunIteration_ContinuesAfterSIGINTWhenUserAnswersYes(t *testing.T) {
	agent := &cancelOnContextAgent{started: make(chan struct{})}
	r := newTestRunnerWithIterAgent(t, agent, RunConfig{
		Agent:  "test",
		Prompt: "keep going",
	})
	r.stdin = strings.NewReader("yes\n")
	var stderr bytes.Buffer
	r.stderr = &stderr

	resultCh := make(chan struct {
		status iterStatus
		err    error
	}, 1)
	go func() {
		status, err := r.runIteration(context.Background())
		resultCh <- struct {
			status iterStatus
			err    error
		}{status: status, err: err}
	}()

	select {
	case <-agent.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for fake agent to start")
	}

	sendInterruptSignal(t)

	status, err := waitForRunIterationResult(t, resultCh)
	if err != nil {
		t.Fatalf("runIteration() error = %v, want nil", err)
	}
	if status != iterContinue {
		t.Fatalf("runIteration() status = %v, want %v", status, iterContinue)
	}
	if !strings.Contains(stderr.String(), "Interrupted. Continue to next iteration?") {
		t.Fatalf("stderr = %q, want interruption prompt", stderr.String())
	}
}

func TestRunner_RunIteration_StopsAfterSIGINTWhenUserAnswersNo(t *testing.T) {
	agent := &cancelOnContextAgent{started: make(chan struct{})}
	r := newTestRunnerWithIterAgent(t, agent, RunConfig{
		Agent:  "test",
		Prompt: "stop now",
	})
	r.stdin = strings.NewReader("n\n")
	var stderr bytes.Buffer
	r.stderr = &stderr

	resultCh := make(chan struct {
		status iterStatus
		err    error
	}, 1)
	go func() {
		status, err := r.runIteration(context.Background())
		resultCh <- struct {
			status iterStatus
			err    error
		}{status: status, err: err}
	}()

	select {
	case <-agent.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for fake agent to start")
	}

	sendInterruptSignal(t)

	status, err := waitForRunIterationResult(t, resultCh)
	if err != nil {
		t.Fatalf("runIteration() error = %v, want nil", err)
	}
	if status != iterInterrupted {
		t.Fatalf("runIteration() status = %v, want %v", status, iterInterrupted)
	}
	if !strings.Contains(stderr.String(), "Interrupted. Continue to next iteration?") {
		t.Fatalf("stderr = %q, want interruption prompt", stderr.String())
	}
}

func TestRunner_RunIteration_TreatsParentContextCancellationAsInterrupted(t *testing.T) {
	agent := &cancelOnContextAgent{started: make(chan struct{})}
	r := newTestRunnerWithIterAgent(t, agent, RunConfig{
		Agent:  "test",
		Prompt: "cancel from parent",
	})
	r.stdin = strings.NewReader("n\n")
	var stderr bytes.Buffer
	r.stderr = &stderr

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resultCh := make(chan struct {
		status iterStatus
		err    error
	}, 1)
	go func() {
		status, err := r.runIteration(ctx)
		resultCh <- struct {
			status iterStatus
			err    error
		}{status: status, err: err}
	}()

	select {
	case <-agent.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for fake agent to start")
	}

	cancel()

	status, err := waitForRunIterationResult(t, resultCh)
	if err != nil {
		t.Fatalf("runIteration() error = %v, want nil", err)
	}
	if status != iterInterrupted {
		t.Fatalf("runIteration() status = %v, want %v", status, iterInterrupted)
	}
	if strings.Contains(stderr.String(), "Interrupted. Continue to next iteration?") {
		t.Fatalf("stderr = %q, want no interactive prompt for parent cancellation", stderr.String())
	}
}

func TestRunner_RunIteration_LogsWarningWhenEventsJSONLWriteFails(t *testing.T) {
	eventsFile, err := os.Create(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatalf("Create(events.jsonl): %v", err)
	}
	if err := eventsFile.Close(); err != nil {
		t.Fatalf("eventsFile.Close(): %v", err)
	}

	var stderr bytes.Buffer
	r := &Runner{
		cfg: RunConfig{Prompt: "persist event"},
		iterAgent: &scriptedIterAgent{
			events: []events.Event{{Type: events.EventTurnEnd}},
		},
		eventsFile: eventsFile,
		stderr:     &stderr,
	}

	status, err := r.runIteration(context.Background())
	if err != nil {
		t.Fatalf("runIteration() error = %v, want nil", err)
	}
	if status != iterContinue {
		t.Fatalf("runIteration() status = %v, want %v", status, iterContinue)
	}
	if len(r.events) != 1 || r.events[0].Type != events.EventTurnEnd {
		t.Fatalf("stored events = %#v, want one turn_end event", r.events)
	}
	if !strings.Contains(stderr.String(), "warning: writing to events.jsonl:") {
		t.Fatalf("stderr = %q, want events.jsonl warning", stderr.String())
	}
}

func TestRunner_RunIteration_TUIMode_IgnoresSIGINT(t *testing.T) {
	// In TUI mode (EventChan set), SIGINT should NOT trigger askContinue
	// because stdin is owned by Bubble Tea. Verify by sending SIGINT to
	// the process while a TUI-mode iteration is running — the runner
	// should NOT call askContinue (which would block forever on stdin).
	//
	// We register a temporary SIGINT catcher so the signal doesn't kill
	// the test process, then verify the runner returns iterContinue (the
	// agent returned normally with no completion marker).
	guardCh := make(chan os.Signal, 1)
	signal.Notify(guardCh, os.Interrupt)
	defer signal.Stop(guardCh)

	agent := &cancelOnContextAgent{started: make(chan struct{})}
	ch := make(chan Event, 100)
	r := newTestRunnerWithIterAgent(t, agent, RunConfig{
		Agent:     "test",
		Prompt:    "tui mode sigint",
		EventChan: ch,
	})
	r.stderr = io.Discard

	resultCh := make(chan struct {
		status iterStatus
		err    error
	}, 1)

	go func() {
		status, err := r.runIteration(context.Background())
		resultCh <- struct {
			status iterStatus
			err    error
		}{status: status, err: err}
	}()

	select {
	case <-agent.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for fake agent to start")
	}

	// Send SIGINT — in TUI mode the runner does not register for it,
	// so the agent's context should NOT be cancelled by the signal.
	sendInterruptSignal(t)

	// Drain our guard channel.
	select {
	case <-guardCh:
	case <-time.After(time.Second):
	}

	// The agent is still blocked on ctx.Done(). Since the runner didn't
	// catch SIGINT, we need to cancel via parent context. In production
	// this happens when the TUI exits.
	// Use a fresh context cancel to simulate TUI quit.
	// But first, the agent's context should still be alive (not cancelled
	// by SIGINT). We verify this by checking the agent is still running.
	select {
	case <-resultCh:
		t.Fatal("runIteration returned early — SIGINT should have been ignored in TUI mode")
	case <-time.After(100 * time.Millisecond):
		// Good — the runner is still running, SIGINT was ignored.
	}
}

func TestRun_TUIMode_ContinuesAfterIteration(t *testing.T) {
	// Verify the iteration loop continues in TUI mode (the bug was that
	// spurious SIGINTs from child process cleanup would cause askContinue
	// to block on stdin, preventing iteration 2 from ever starting).
	ch := make(chan Event, 100)
	fa := &fakeAgent{
		responses: []fakeResponse{
			{text: "working..."},
			{text: "still working..."},
			{text: "all done " + completionMarker},
		},
	}
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:     "test",
		Prompt:    "multi-iteration tui",
		EventChan: ch,
	})

	result := r.Run(context.Background())

	if result.Status != StatusCompleted {
		t.Errorf("status = %s, want %s", result.Status, StatusCompleted)
	}
	if result.Iterations != 3 {
		t.Errorf("iterations = %d, want 3", result.Iterations)
	}
}

func TestRunner_WriteEffectivePromptReturnsReadableErrorWhenPromptPathIsDirectory(t *testing.T) {
	runsDir := t.TempDir()
	runID := "prompt-write-error"
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(filepath.Join(runDir, "effective-prompt.md"), 0755); err != nil {
		t.Fatalf("MkdirAll(effective-prompt.md): %v", err)
	}

	r := &Runner{
		cfg: RunConfig{
			RunsDir: runsDir,
			Prompt:  "effective prompt text",
		},
		runID:  runID,
		stderr: io.Discard,
	}

	err := r.writeEffectivePrompt()
	if err == nil {
		t.Fatal("writeEffectivePrompt() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "writing effective prompt") {
		t.Fatalf("writeEffectivePrompt() error = %q, want write context", err)
	}
}
