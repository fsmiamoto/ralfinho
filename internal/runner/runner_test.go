package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestHasCompletionMarker(t *testing.T) {
	t.Run("detects marker", func(t *testing.T) {
		if !HasCompletionMarker("hello\n<promise>COMPLETE</promise>\n") {
			t.Fatal("expected marker detection")
		}
	})

	t.Run("ignores instruction line", func(t *testing.T) {
		text := "If all complete, reply with: <promise>COMPLETE</promise>"
		if HasCompletionMarker(text) {
			t.Fatal("expected marker to be ignored in instruction line")
		}
	})
}

func TestRun_CompletesOnSecondIteration(t *testing.T) {
	calls := 0
	execFn := func(ctx context.Context, agent, prompt string) (string, error) {
		calls++
		if calls == 2 {
			return "done <promise>COMPLETE</promise>", nil
		}
		return "still running", nil
	}

	result, err := Run(context.Background(), Config{Agent: "pi", Prompt: "p", SleepBetween: time.Millisecond}, execFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if result.IterationsCompleted != 2 {
		t.Fatalf("expected 2 iterations, got %d", result.IterationsCompleted)
	}
}

func TestRun_RespectsMaxIterations(t *testing.T) {
	execFn := func(ctx context.Context, agent, prompt string) (string, error) {
		return "not done", nil
	}

	result, err := Run(context.Background(), Config{Agent: "pi", Prompt: "p", MaxIterations: 1, SleepBetween: time.Millisecond}, execFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusMaxIterationsReached {
		t.Fatalf("expected max iteration status, got %s", result.Status)
	}
	if result.IterationsCompleted != 1 {
		t.Fatalf("expected 1 iteration, got %d", result.IterationsCompleted)
	}
}

func TestRun_ExecError(t *testing.T) {
	execFn := func(ctx context.Context, agent, prompt string) (string, error) {
		return "partial output", errors.New("boom")
	}

	result, err := Run(context.Background(), Config{Agent: "pi", Prompt: "p"}, execFn)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusFailed {
		t.Fatalf("expected failed status, got %s", result.Status)
	}
}

func TestRun_InterruptStop(t *testing.T) {
	interrupt := make(chan struct{}, 1)
	interrupt <- struct{}{}

	execFn := func(ctx context.Context, agent, prompt string) (string, error) {
		<-ctx.Done()
		return "interrupted", ctx.Err()
	}

	result, err := Run(context.Background(), Config{
		Agent:        "pi",
		Prompt:       "p",
		SleepBetween: time.Millisecond,
		Interrupt:    interrupt,
		OnInterrupt: func() (bool, error) {
			return false, nil
		},
	}, execFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusInterrupted {
		t.Fatalf("expected interrupted status, got %s", result.Status)
	}
	if result.IterationsCompleted != 0 {
		t.Fatalf("expected 0 completed iterations, got %d", result.IterationsCompleted)
	}
}

func TestRun_InterruptContinue(t *testing.T) {
	interrupt := make(chan struct{}, 1)
	calls := 0
	continueCalls := 0
	reports := make([]IterationReport, 0, 2)

	execFn := func(ctx context.Context, agent, prompt string) (string, error) {
		calls++
		if calls == 1 {
			<-ctx.Done()
			return "interrupted", ctx.Err()
		}
		return "done <promise>COMPLETE</promise>", nil
	}

	go func() {
		interrupt <- struct{}{}
	}()

	result, err := Run(context.Background(), Config{
		Agent:        "pi",
		Prompt:       "p",
		SleepBetween: time.Millisecond,
		Interrupt:    interrupt,
		OnInterrupt: func() (bool, error) {
			continueCalls++
			return true, nil
		},
		OnIteration: func(report IterationReport) {
			reports = append(reports, report)
		},
	}, execFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusCompleted {
		t.Fatalf("expected completed status, got %s", result.Status)
	}
	if result.IterationsCompleted != 1 {
		t.Fatalf("expected 1 completed iteration, got %d", result.IterationsCompleted)
	}
	if continueCalls != 1 {
		t.Fatalf("expected 1 continue prompt, got %d", continueCalls)
	}
	if len(reports) != 2 {
		t.Fatalf("expected 2 iteration reports, got %d", len(reports))
	}
	if !reports[0].Interrupted {
		t.Fatalf("expected first report to be interrupted: %+v", reports[0])
	}
	if reports[1].Interrupted {
		t.Fatalf("expected second report to complete: %+v", reports[1])
	}
}
