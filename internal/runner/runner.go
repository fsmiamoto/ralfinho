package runner

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type Status string

const (
	StatusCompleted            Status = "completed"
	StatusFailed               Status = "failed"
	StatusInterrupted          Status = "interrupted"
	StatusMaxIterationsReached Status = "max_iterations_reached"
)

type IterationReport struct {
	Iteration   int
	Output      string
	Err         error
	Interrupted bool
}

type Config struct {
	Agent         string
	Prompt        string
	MaxIterations int
	SleepBetween  time.Duration
	Interrupt     <-chan struct{}
	OnInterrupt   func() (bool, error)
	OnIteration   func(IterationReport)
}

type Result struct {
	IterationsCompleted int
	Status              Status
	LastOutput          string
}

type ExecFunc func(ctx context.Context, agent, prompt string) (string, error)

func Run(ctx context.Context, cfg Config, execFn ExecFunc) (Result, error) {
	if cfg.MaxIterations < 0 {
		return Result{}, fmt.Errorf("max iterations must be >= 0")
	}
	if cfg.SleepBetween <= 0 {
		cfg.SleepBetween = 2 * time.Second
	}
	if execFn == nil {
		execFn = ExecOnce
	}

	completed := 0
	lastOutput := ""

	for iteration := 1; ; iteration++ {
		if cfg.MaxIterations > 0 && completed >= cfg.MaxIterations {
			return Result{IterationsCompleted: completed, Status: StatusMaxIterationsReached, LastOutput: lastOutput}, nil
		}

		output, err, interrupted := execWithInterrupt(ctx, cfg.Interrupt, execFn, cfg.Agent, cfg.Prompt)
		lastOutput = output
		if cfg.OnIteration != nil {
			cfg.OnIteration(IterationReport{Iteration: iteration, Output: output, Err: err, Interrupted: interrupted})
		}
		if interrupted {
			cont, interruptErr := continueAfterInterrupt(cfg.OnInterrupt)
			if interruptErr != nil {
				return Result{IterationsCompleted: completed, Status: StatusFailed, LastOutput: lastOutput}, interruptErr
			}
			if !cont {
				return Result{IterationsCompleted: completed, Status: StatusInterrupted, LastOutput: lastOutput}, nil
			}
			continue
		}

		if err != nil {
			return Result{IterationsCompleted: completed, Status: StatusFailed, LastOutput: lastOutput}, err
		}

		completed++
		if HasCompletionMarker(output) {
			return Result{IterationsCompleted: completed, Status: StatusCompleted, LastOutput: lastOutput}, nil
		}

		sleepInterrupted, sleepErr := sleepWithInterrupt(ctx, cfg.SleepBetween, cfg.Interrupt)
		if sleepErr != nil {
			return Result{IterationsCompleted: completed, Status: StatusFailed, LastOutput: lastOutput}, sleepErr
		}
		if sleepInterrupted {
			cont, interruptErr := continueAfterInterrupt(cfg.OnInterrupt)
			if interruptErr != nil {
				return Result{IterationsCompleted: completed, Status: StatusFailed, LastOutput: lastOutput}, interruptErr
			}
			if !cont {
				return Result{IterationsCompleted: completed, Status: StatusInterrupted, LastOutput: lastOutput}, nil
			}
		}
	}
}

func continueAfterInterrupt(onInterrupt func() (bool, error)) (bool, error) {
	if onInterrupt == nil {
		return false, nil
	}
	return onInterrupt()
}

func execWithInterrupt(ctx context.Context, interrupt <-chan struct{}, execFn ExecFunc, agent, prompt string) (string, error, bool) {
	if interrupt == nil {
		output, err := execFn(ctx, agent, prompt)
		return output, err, false
	}

	iterCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	interruptedCh := make(chan struct{}, 1)
	stop := make(chan struct{})
	go func() {
		select {
		case <-interrupt:
			select {
			case interruptedCh <- struct{}{}:
			default:
			}
			cancel()
		case <-iterCtx.Done():
		case <-stop:
		}
	}()

	output, err := execFn(iterCtx, agent, prompt)
	close(stop)

	wasInterrupted := false
	select {
	case <-interruptedCh:
		wasInterrupted = true
	default:
	}

	return output, err, wasInterrupted
}

func sleepWithInterrupt(ctx context.Context, d time.Duration, interrupt <-chan struct{}) (bool, error) {
	if interrupt == nil {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(d):
			return false, nil
		}
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-timer.C:
		return false, nil
	case <-interrupt:
		return true, nil
	}
}

func ExecOnce(ctx context.Context, agent, prompt string) (string, error) {
	var cmd *exec.Cmd

	switch agent {
	case "pi":
		cmd = exec.CommandContext(ctx, "pi", "--mode", "json", prompt)
	case "claude":
		cmd = exec.CommandContext(ctx, "/home/shigueo/.local/bin/claude", "--dangerously-skip-permissions", "--verbose", "--output-format", "stream-json", "-p", prompt)
	case "codex":
		cmd = exec.CommandContext(ctx, "codex", "exec", "--full-auto", prompt)
	default:
		return "", fmt.Errorf("unknown agent %q", agent)
	}

	b, err := cmd.CombinedOutput()
	return string(b), err
}

func HasCompletionMarker(raw string) bool {
	const marker = "<promise>COMPLETE</promise>"
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(strings.ToLower(line), "reply with:") {
			continue
		}
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}
