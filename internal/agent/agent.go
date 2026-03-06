// Package agent defines the Agent interface for running coding agent iterations.
//
// Each Agent implementation wraps a specific backend (e.g. pi, kiro-cli).
// The runner delegates prompt execution to the agent while retaining ownership
// of signal handling, completion detection, and iteration control.
package agent

import (
	"context"
	"io"

	"github.com/fsmiamoto/ralfinho/internal/events"
)

// Agent is the contract for a coding-agent backend.
//
// Implementations are responsible for:
//   - Spawning and managing the underlying subprocess (or connection).
//   - Sending the prompt text to the agent.
//   - Parsing the agent's output into events.Event values.
//   - Calling onEvent for each parsed event so the runner can persist and
//     forward them in real time.
//   - Returning the accumulated assistant text (used by the runner to detect
//     the completion marker).
//
// Implementations must NOT:
//   - Handle SIGINT or other signals (the runner cancels via ctx).
//   - Check for the completion marker (the runner owns that).
//   - Drive the iteration loop (one call = one iteration).
type Agent interface {
	// RunIteration executes a single agent iteration with the given prompt.
	//
	// The agent streams parsed events through the onEvent callback. When the
	// agent process finishes (or ctx is cancelled), RunIteration returns the
	// full assistant text accumulated during this iteration and any error.
	//
	// Context cancellation should cause the agent to terminate its subprocess
	// promptly. The returned error may wrap context.Canceled in that case.
	RunIteration(ctx context.Context, prompt string, onEvent func(events.Event)) (assistantText string, err error)
}

// Option configures optional agent behavior.
type Option func(*Options)

// Options holds optional settings shared across agent implementations.
type Options struct {
	// RawWriter, when non-nil, receives a copy of the raw agent output
	// (e.g. JSONL lines from pi, JSON-RPC frames from kiro) for debugging.
	RawWriter io.Writer
}

// WithRawWriter returns an Option that sets the raw output writer.
func WithRawWriter(w io.Writer) Option {
	return func(o *Options) {
		o.RawWriter = w
	}
}

// ApplyOptions applies the given options to an Options struct and returns it.
func ApplyOptions(opts []Option) Options {
	var o Options
	for _, fn := range opts {
		fn(&o)
	}
	return o
}
