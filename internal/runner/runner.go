package runner

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsmiamoto/ralfinho/internal/agent"
)

// Status describes the final outcome of a run.
type Status string

const (
	StatusRunning              Status = "running"
	StatusCompleted            Status = "completed"
	StatusInterrupted          Status = "interrupted"
	StatusFailed               Status = "failed"
	StatusMaxIterationsReached Status = "max_iterations_reached"
	StatusStuck                Status = "stuck"
)

// completionMarker is the sentinel that signals the agent considers itself done.
const completionMarker = "<promise>COMPLETE</promise>"

// RunConfig holds the parameters for a single run.
type RunConfig struct {
	Agent             string
	Prompt            string        // the full prompt text to send each iteration
	MaxIterations     int           // 0 = unlimited
	InactivityTimeout time.Duration // 0 = use default (5m)
	RunsDir           string
	PromptSource      string       // "prompt", "plan", or "default"
	PromptFile        string       // path when PromptSource is "prompt"
	PlanFile          string       // path when PromptSource is "plan"
	EventChan         chan<- Event // optional: send events to TUI

	// AgentExtraArgs holds extra arguments to append to the agent subprocess
	// command line. Sourced from per-agent config file settings.
	AgentExtraArgs []string
}

// RunResult is the summary returned after the loop finishes.
type RunResult struct {
	RunID      string
	Iterations int
	Status     Status
	Agent      string
	Error      string // non-empty when Status == StatusFailed
}

// Runner drives the agent iteration loop.
type Runner struct {
	cfg                 RunConfig
	runID               string
	stdin               io.Reader // user input (for interactive prompts)
	stderr              io.Writer // progress output goes here
	events              []Event   // all parsed events across all iterations
	eventsFile          *os.File  // events.jsonl
	rawFile             *os.File  // raw-output.log
	sessionFile         *os.File  // session.log
	startedAt           time.Time
	iteration           int             // current iteration number
	sessionText         strings.Builder // accumulates assistant text for session.log
	iterAgent           agent.Agent     // agent implementation for running iterations
	consecutiveTimeouts int             // reset to 0 on any successful iteration
}

// New creates a Runner with the given config. Progress output goes to stderr.
func New(cfg RunConfig) *Runner {
	return &Runner{
		cfg:    cfg,
		runID:  newUUID(),
		stdin:  os.Stdin,
		stderr: os.Stderr,
	}
}

// Run executes the agent loop until completion, max iterations, or interruption.
func (r *Runner) Run(ctx context.Context) RunResult {
	r.startedAt = time.Now()
	if r.cfg.EventChan != nil {
		r.stderr = io.Discard
	}
	result := RunResult{
		RunID:  r.runID,
		Status: StatusRunning,
		Agent:  r.cfg.Agent,
	}

	r.logf("run %s started (agent=%s, max_iterations=%d)\n", r.runID, r.cfg.Agent, r.cfg.MaxIterations)

	// Write effective prompt for auditability.
	if err := r.writeEffectivePrompt(); err != nil {
		r.logf("warning: could not write effective prompt: %v\n", err)
	}

	// Open persistence files.
	r.openRunFiles()

	// Write initial meta.json so external tools can see the run immediately.
	r.writeMeta(StatusRunning, 0)

	// Construct the agent for this run (unless pre-set, e.g. in tests).
	if r.iterAgent == nil {
		var agentOpts []agent.Option
		if r.rawFile != nil {
			agentOpts = append(agentOpts, agent.WithRawWriter(r.rawFile))
		}
		agentOpts = append(agentOpts, agent.WithLogWriter(r.stderr))
		if len(r.cfg.AgentExtraArgs) > 0 {
			agentOpts = append(agentOpts, agent.WithExtraArgs(r.cfg.AgentExtraArgs))
		}
		resolved, err := agent.Resolve(r.cfg.Agent, agentOpts...)
		if err != nil {
			r.logf("error: %v\n", err)
			result.Status = StatusFailed
			result.Error = err.Error()
			r.writeMeta(result.Status, result.Iterations)
			r.closeRunFiles()
			return result
		}
		r.iterAgent = resolved
	}

	done := false
	for !done {
		result.Iterations++
		r.writeMeta(StatusRunning, result.Iterations)
		if r.cfg.MaxIterations > 0 && result.Iterations > r.cfg.MaxIterations {
			result.Iterations--
			result.Status = StatusMaxIterationsReached
			r.logf("max iterations (%d) reached\n", r.cfg.MaxIterations)
			break
		}

		r.iteration = result.Iterations
		r.sessionLogf("\n=== Iteration %d ===\n", r.iteration)
		r.logf("--- iteration %d ---\n", result.Iterations)

		// Send synthetic iteration event to TUI.
		r.sendEvent(Event{
			Type:      EventIteration,
			ID:        fmt.Sprintf("iteration-%d", r.iteration),
			Timestamp: time.Now().Format(time.RFC3339),
		})

		status, err := r.runIteration(ctx)
		if err != nil {
			r.logf("error: %v\n", err)
			r.sessionLogf("[%s] error: %v\n", r.timestamp(), err)
			result.Status = StatusFailed
			result.Error = err.Error()
			break
		}

		switch status {
		case iterComplete:
			r.consecutiveTimeouts = 0
			result.Status = StatusCompleted
			r.logf("agent signalled COMPLETE\n")
			done = true
		case iterContinue:
			r.consecutiveTimeouts = 0
			// next iteration
		case iterInterrupted:
			result.Status = StatusInterrupted
			done = true
		case iterTimedOut:
			timeout := r.cfg.InactivityTimeout
			if timeout == 0 {
				timeout = defaultInactivityTimeout
			}
			r.consecutiveTimeouts++
			if r.consecutiveTimeouts < 2 {
				r.logf("inactivity timeout — retrying iteration\n")
				r.sessionLogf("[%s] inactivity timeout — retrying iteration\n", r.timestamp())
				r.sendEvent(Event{
					Type:      EventInactivityTimeout,
					ID:        fmt.Sprintf("timeout-%d", r.iteration),
					Timestamp: time.Now().Format(time.RFC3339),
				})
				// Don't count the timed-out iteration.
				result.Iterations--
			} else {
				result.Status = StatusStuck
				result.Error = fmt.Sprintf("agent unresponsive for %s (2 consecutive timeouts)", timeout)
				r.logf("%s\n", result.Error)
				r.sessionLogf("[%s] %s\n", r.timestamp(), result.Error)
				done = true
			}
		}
	}

	// Write final meta.json and close persistence files.
	r.writeMeta(result.Status, result.Iterations)
	r.closeRunFiles()

	return result
}

type iterStatus int

const (
	iterContinue iterStatus = iota
	iterComplete
	iterInterrupted
	iterTimedOut
)

// defaultInactivityTimeout is the default duration before the watchdog fires.
const defaultInactivityTimeout = 5 * time.Minute

// runIteration runs one invocation of the agent and processes its output.
func (r *Runner) runIteration(ctx context.Context) (iterStatus, error) {
	// Set up signal handling: catch SIGINT, cancel the agent's context.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT)
	defer signal.Stop(sigCh)

	iterCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	interrupted := false
	timedOut := false
	var mu sync.Mutex

	// --- Inactivity watchdog ---
	timeout := r.cfg.InactivityTimeout
	if timeout == 0 {
		timeout = defaultInactivityTimeout
	}
	watchdog := time.NewTimer(timeout)
	defer watchdog.Stop()

	// Monitor for SIGINT and watchdog in the background.
	// The done channel ensures this goroutine exits when runIteration returns,
	// since signal.Stop does not close sigCh and the goroutine would leak.
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-sigCh:
				mu.Lock()
				interrupted = true
				mu.Unlock()
				cancel()
			case <-watchdog.C:
				mu.Lock()
				timedOut = true
				mu.Unlock()
				cancel()
			case <-done:
				return
			}
		}
	}()

	// Delegate to the agent. The onEvent callback persists, stores, and
	// processes each event as it arrives.
	assistantText, err := r.iterAgent.RunIteration(iterCtx, r.cfg.Prompt, func(ev Event) {
		// Reset the inactivity watchdog on every event.
		watchdog.Reset(timeout)

		// Persist to events.jsonl.
		if r.eventsFile != nil {
			if data, merr := json.Marshal(ev); merr == nil {
				if _, werr := fmt.Fprintln(r.eventsFile, string(data)); werr != nil {
					r.logf("warning: writing to events.jsonl: %v\n", werr)
				}
			}
		}

		// Store in memory.
		r.events = append(r.events, ev)

		// Handle the event (logging, session log, TUI forwarding).
		r.handleEvent(&ev)
	})

	// Check if we were interrupted (takes priority over other outcomes).
	mu.Lock()
	wasInterrupted := interrupted
	wasTimedOut := timedOut
	mu.Unlock()

	if wasInterrupted {
		if r.askContinue() {
			return iterContinue, nil
		}
		return iterInterrupted, nil
	}

	// Check if the inactivity watchdog fired.
	if wasTimedOut {
		return iterTimedOut, nil
	}

	// If the parent context was cancelled (e.g. user quit the TUI),
	// treat it as an interruption rather than a failure.
	if err != nil && ctx.Err() != nil {
		return iterInterrupted, nil
	}

	// Surface agent errors. The status value is ignored by the caller when
	// err != nil, so we return the zero value (iterContinue).
	if err != nil {
		return iterContinue, err
	}

	// Check if the assistant text contains the completion marker.
	if strings.Contains(assistantText, completionMarker) {
		return iterComplete, nil
	}

	return iterContinue, nil
}

// sendEvent sends an event to the TUI channel if configured (non-blocking).
func (r *Runner) sendEvent(ev Event) {
	if r.cfg.EventChan != nil {
		select {
		case r.cfg.EventChan <- ev:
		default:
		}
	}
}

// handleEvent processes a single parsed event, printing a summary to stderr,
// accumulating assistant text, and writing to session.log.
func (r *Runner) handleEvent(ev *Event) {
	// Forward to TUI if channel is set.
	r.sendEvent(*ev)
	switch ev.Type {
	case EventSession:
		r.logf("  session id=%s\n", ev.ID)
		r.sessionLogf("[%s] session id=%s\n", r.timestamp(), ev.ID)

	case EventMessageStart:
		var msg MessageEnvelope
		if ev.Message != nil {
			_ = json.Unmarshal(ev.Message, &msg)
		}
		if msg.Role == "user" {
			r.logf("  → user message\n")
			r.sessionLogf("[%s] → user message\n", r.timestamp())
		} else if msg.Role == "assistant" {
			model := msg.Model
			if model == "" {
				model = "unknown"
			}
			r.logf("  ← assistant (%s)\n", model)
			r.sessionLogf("[%s] ← assistant (%s)\n", r.timestamp(), model)
			r.sessionText.Reset()
		}

	case EventMessageUpdate:
		if ev.AssistantMessageEvent != nil {
			var ae AssistantEvent
			if err := json.Unmarshal(ev.AssistantMessageEvent, &ae); err == nil {
				switch ae.Type {
				case "text_delta":
					r.sessionText.WriteString(ae.Delta)
				}
			}
		}

	case EventMessageEnd:
		r.flushSessionText()

	case EventToolExecutionStart:
		r.logf("  > tool: %s (id=%s)\n", ev.ToolName, truncate(ev.ToolCallID, 12))
		r.sessionLogf("[%s] > tool start: %s (id=%s)\n", r.timestamp(), ev.ToolName, truncate(ev.ToolCallID, 12))
		r.logToolArgs(ev.Args)

	case EventToolExecutionUpdate:
		// Log tool args that arrive after tool_start (common with Claude backend
		// where args are streamed incrementally and only emitted in the update).
		r.logToolArgs(ev.Args)

	case EventToolExecutionEnd:
		errStr := ""
		if ev.IsError != nil && *ev.IsError {
			errStr = " [ERROR]"
		}
		r.logf("  + tool done: %s%s\n", ev.ToolName, errStr)
		r.sessionLogf("[%s] + tool done: %s%s\n", r.timestamp(), ev.ToolName, errStr)
		if ev.Result != nil {
			r.sessionLogf("    result: %s\n", truncate(string(ev.Result), 200))
		}

	case EventTurnEnd:
		// Safety flush: write any remaining assistant text not flushed by message_end.
		r.flushSessionText()
		r.logf("  turn end\n")
		r.sessionLogf("[%s] turn end\n", r.timestamp())

	case EventAgentEnd:
		r.logf("  agent end\n")
		r.sessionLogf("[%s] agent end\n", r.timestamp())

	case EventRateLimit:
		if ev.RateLimit != nil {
			if ev.RateLimit.RequestsRemaining == 0 {
				r.logf("  ⚠ rate limited — waiting for capacity\n")
				r.sessionLogf("[%s] ⚠ rate limited — waiting for capacity\n", r.timestamp())
			} else {
				r.logf("  ⚠ rate limit: %d requests remaining\n", ev.RateLimit.RequestsRemaining)
				r.sessionLogf("[%s] ⚠ rate limit: %d requests remaining\n", r.timestamp(), ev.RateLimit.RequestsRemaining)
			}
		}
	}
}

// askContinue prompts the user on stderr whether to continue iterating.
func (r *Runner) askContinue() bool {
	fmt.Fprintf(r.stderr, "\nInterrupted. Continue to next iteration? [y/n]: ")

	var input string
	if _, err := fmt.Fscan(r.stdin, &input); err != nil {
		return false
	}
	input = strings.TrimSpace(strings.ToLower(input))
	return input == "y" || input == "yes"
}

// flushSessionText writes any accumulated assistant text to session.log
// and resets the buffer. Called at MessageEnd and as a safety flush at TurnEnd.
func (r *Runner) flushSessionText() {
	text := r.sessionText.String()
	if text == "" {
		return
	}
	r.sessionLogf("[%s] assistant text:\n", r.timestamp())
	for _, line := range strings.Split(text, "\n") {
		r.sessionLogf("    %s\n", line)
	}
	r.sessionText.Reset()
}

// logToolArgs logs tool arguments to stderr and session.log. If the args
// contain a "command" field, it is logged as a cmd line; otherwise the raw
// JSON is logged. Called from both tool_start and tool_update handlers.
func (r *Runner) logToolArgs(args json.RawMessage) {
	if args == nil {
		return
	}
	var ta ToolArgs
	if err := json.Unmarshal(args, &ta); err == nil && ta.Command != "" {
		r.logf("    cmd: %s\n", truncate(ta.Command, 120))
		r.sessionLogf("    cmd: %s\n", truncate(ta.Command, 120))
	} else {
		r.sessionLogf("    args: %s\n", truncate(string(args), 200))
	}
}

func (r *Runner) logf(format string, args ...any) {
	fmt.Fprintf(r.stderr, format, args...)
}

// writeEffectivePrompt creates the run directory and writes the prompt text
// to <runs-dir>/<run-id>/effective-prompt.md for auditability.
func (r *Runner) writeEffectivePrompt() error {
	dir := filepath.Join(r.cfg.RunsDir, r.runID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating run dir: %w", err)
	}
	path := filepath.Join(dir, "effective-prompt.md")
	if err := os.WriteFile(path, []byte(r.cfg.Prompt), 0644); err != nil {
		return fmt.Errorf("writing effective prompt: %w", err)
	}
	r.logf("effective prompt written to %s\n", path)
	return nil
}

// openRunFiles opens the persistence files for the run.
func (r *Runner) openRunFiles() {
	dir := filepath.Join(r.cfg.RunsDir, r.runID)
	// Directory should already exist from writeEffectivePrompt.

	var err error

	r.eventsFile, err = os.Create(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		r.logf("warning: could not create events.jsonl: %v\n", err)
		r.eventsFile = nil
	}

	r.rawFile, err = os.Create(filepath.Join(dir, "raw-output.log"))
	if err != nil {
		r.logf("warning: could not create raw-output.log: %v\n", err)
		r.rawFile = nil
	}

	r.sessionFile, err = os.Create(filepath.Join(dir, "session.log"))
	if err != nil {
		r.logf("warning: could not create session.log: %v\n", err)
		r.sessionFile = nil
	}
}

// closeRunFiles closes all persistence files.
func (r *Runner) closeRunFiles() {
	if r.eventsFile != nil {
		if err := r.eventsFile.Close(); err != nil {
			r.logf("warning: closing events.jsonl: %v\n", err)
		}
	}
	if r.rawFile != nil {
		if err := r.rawFile.Close(); err != nil {
			r.logf("warning: closing raw-output.log: %v\n", err)
		}
	}
	if r.sessionFile != nil {
		if err := r.sessionFile.Close(); err != nil {
			r.logf("warning: closing session.log: %v\n", err)
		}
	}
}

// sessionLogf writes a formatted line to session.log.
func (r *Runner) sessionLogf(format string, args ...any) {
	if r.sessionFile != nil {
		if _, err := fmt.Fprintf(r.sessionFile, format, args...); err != nil {
			r.logf("warning: writing to session.log: %v\n", err)
		}
	}
}

// timestamp returns the current time in RFC3339 format for session log entries.
func (r *Runner) timestamp() string {
	return time.Now().Format(time.RFC3339)
}

// writeMeta writes meta.json to the run directory. For terminal statuses
// (anything other than StatusRunning), EndedAt is populated with the current
// time. For StatusRunning, EndedAt is left empty to signal the run is still
// in progress.
func (r *Runner) writeMeta(status Status, iterations int) {
	dir := filepath.Join(r.cfg.RunsDir, r.runID)
	var endedAt string
	if status != StatusRunning {
		endedAt = time.Now().Format(time.RFC3339)
	}
	meta := RunMeta{
		RunID:               r.runID,
		StartedAt:           r.startedAt.Format(time.RFC3339),
		EndedAt:             endedAt,
		Status:              string(status),
		Agent:               r.cfg.Agent,
		PromptSource:        r.cfg.PromptSource,
		PromptFile:          r.cfg.PromptFile,
		PlanFile:            r.cfg.PlanFile,
		MaxIterations:       r.cfg.MaxIterations,
		IterationsCompleted: iterations,
	}
	if err := writeMetaJSON(filepath.Join(dir, "meta.json"), meta); err != nil {
		r.logf("warning: could not write meta.json: %v\n", err)
	}
}

// --- helpers ---

// newUUID generates a UUID v4 using crypto/rand.
func newUUID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Extremely unlikely; fall back to a zero UUID.
		return "00000000-0000-4000-8000-000000000000"
	}
	buf[6] = (buf[6] & 0x0f) | 0x40 // version 4
	buf[8] = (buf[8] & 0x3f) | 0x80 // variant 2
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

// truncate shortens s to at most n runes, adding "…" if truncated.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n < 1 {
		return "…"
	}
	return string(runes[:n-1]) + "…"
}
