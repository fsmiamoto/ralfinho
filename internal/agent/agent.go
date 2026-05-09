// Package agent defines the Agent interface for running coding agent iterations.
//
// Each Agent implementation wraps a specific backend (e.g. pi, claude).
// The runner delegates prompt execution to the agent while retaining ownership
// of signal handling, completion detection, and iteration control.
package agent

import (
	"context"
	"fmt"
	"io"
	"os"

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
	// The onEvent callback is called synchronously from a single goroutine.
	// Implementations must not call onEvent concurrently from multiple goroutines.
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
	// (e.g. JSONL lines from pi) for debugging.
	RawWriter io.Writer

	// LogWriter receives diagnostic/warning messages from the agent backend.
	// Defaults to os.Stderr if not set.
	LogWriter io.Writer

	// ExtraArgs is appended verbatim to the agent subprocess command line
	// after all built-in flags. Sourced from per-agent config file settings.
	ExtraArgs []string
}

// WithRawWriter returns an Option that sets the raw output writer.
func WithRawWriter(w io.Writer) Option {
	return func(o *Options) {
		o.RawWriter = w
	}
}

// WithLogWriter returns an Option that sets the diagnostic log writer.
func WithLogWriter(w io.Writer) Option {
	return func(o *Options) {
		o.LogWriter = w
	}
}

// WithExtraArgs returns an Option that appends extra arguments to the agent
// subprocess command line. Multiple calls accumulate (args are appended in
// the order the options are applied).
func WithExtraArgs(args []string) Option {
	return func(o *Options) {
		o.ExtraArgs = append(o.ExtraArgs, args...)
	}
}

// applyOptions applies the given options to an Options struct and returns it.
func applyOptions(opts []Option) Options {
	var o Options
	for _, fn := range opts {
		fn(&o)
	}
	if o.LogWriter == nil {
		o.LogWriter = os.Stderr
	}
	return o
}

// limitedBuffer captures the last N bytes written to it.
type limitedBuffer struct {
	buf  []byte
	size int
	pos  int
	full bool
}

func newLimitedBuffer(size int) *limitedBuffer {
	return &limitedBuffer{buf: make([]byte, size), size: size}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	for _, c := range p {
		b.buf[b.pos] = c
		b.pos = (b.pos + 1) % b.size
		if b.pos == 0 {
			b.full = true
		}
	}
	return n, nil
}

func (b *limitedBuffer) String() string {
	if !b.full {
		return string(b.buf[:b.pos])
	}
	return string(b.buf[b.pos:]) + string(b.buf[:b.pos])
}

// IsValid reports whether name is a recognized agent name.
func IsValid(name string) bool {
	switch name {
	case "pi", "claude":
		return true
	default:
		return false
	}
}

// Resolve maps an agent name to a concrete Agent implementation.
//
// Supported names:
//   - "pi"    → PiAgent (invokes the pi CLI tool)
//   - "claude" → ClaudeAgent (invokes Claude Code CLI in streaming mode)
//
// Unknown names produce a clear error listing the supported agents.
// Options (e.g. WithRawWriter) are forwarded to the chosen implementation.
func Resolve(name string, opts ...Option) (Agent, error) {
	switch name {
	case "pi":
		return NewPiAgent("pi", opts...), nil
	case "claude":
		return NewClaudeAgent(opts...), nil
	default:
		return nil, fmt.Errorf("unknown agent %q (supported: pi, claude)", name)
	}
}
