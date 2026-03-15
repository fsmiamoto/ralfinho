package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/fsmiamoto/ralfinho/internal/events"
)

// PiAgent implements the Agent interface using the pi CLI tool.
//
// Each call to RunIteration spawns a new pi subprocess with the prompt
// written to a temp file (via @file syntax). JSONL output is parsed into
// events.Event values and forwarded through the onEvent callback. Raw
// stdout is optionally tee'd to Options.RawWriter for debugging.
type PiAgent struct {
	binary string  // path or name of the pi binary
	opts   Options // optional settings (raw writer, etc.)
}

// NewPiAgent creates a PiAgent that invokes the given binary name.
// Typically binary is "pi" (the default agent), but it can be an absolute path.
func NewPiAgent(binary string, options ...Option) *PiAgent {
	return &PiAgent{
		binary: binary,
		opts:   applyOptions(options),
	}
}

// RunIteration spawns a pi subprocess, streams parsed events via onEvent,
// and returns the accumulated assistant text.
func (a *PiAgent) RunIteration(ctx context.Context, prompt string, onEvent func(events.Event)) (string, error) {
	// Write prompt to a temp file so we can use @file syntax for long prompts.
	tmpFile, err := os.CreateTemp("", "ralfinho-prompt-*.md")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if err := os.Chmod(tmpPath, 0600); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("setting temp file permissions: %w", err)
	}

	if _, err := tmpFile.WriteString(prompt); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("writing prompt: %w", err)
	}
	tmpFile.Close()

	// Build command: <binary> --mode json -p --no-session @<tempfile> [extra-args...]
	cmdArgs := []string{"--mode", "json", "-p", "--no-session", "@" + tmpPath}
	cmdArgs = append(cmdArgs, a.opts.ExtraArgs...)
	cmd := exec.CommandContext(ctx, a.binary, cmdArgs...)
	stderrBuf := newLimitedBuffer(4096)
	cmd.Stderr = stderrBuf

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdout.Close()
		return "", fmt.Errorf("starting agent: %w", err)
	}

	// Process JSONL output. Optionally tee raw stdout to RawWriter.
	var stdoutReader io.Reader = stdout
	if a.opts.RawWriter != nil {
		stdoutReader = io.TeeReader(stdout, a.opts.RawWriter)
	}

	scanner := bufio.NewScanner(stdoutReader)
	// Allow large lines (pi can produce big JSON).
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	var assistantText strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var ev events.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			// Skip unparseable lines silently — the runner logs warnings
			// through its own logging, and we don't want to couple the
			// agent to a logger.
			continue
		}

		// Accumulate assistant text from text_delta events for the return value.
		if ev.Type == events.EventMessageUpdate && ev.AssistantMessageEvent != nil {
			var ae events.AssistantEvent
			if err := json.Unmarshal(ev.AssistantMessageEvent, &ae); err == nil {
				if ae.Type == "text_delta" {
					assistantText.WriteString(ae.Delta)
				}
			}
		}

		onEvent(ev)
	}

	waitErr := cmd.Wait()

	if err := scanner.Err(); err != nil {
		return assistantText.String(), fmt.Errorf("reading agent output: %w", err)
	}

	// Surface context cancellation so the runner knows the iteration was
	// interrupted rather than completed normally. Check before waitErr
	// because CommandContext SIGKILLs the process, making cmd.Wait()
	// return "signal: killed" — that's expected, not an agent error.
	if ctx.Err() != nil {
		return assistantText.String(), ctx.Err()
	}

	if waitErr != nil {
		stderr := strings.TrimSpace(stderrBuf.String())
		if stderr != "" {
			return assistantText.String(), fmt.Errorf("agent exited with error: %w\nstderr: %s", waitErr, stderr)
		}
		return assistantText.String(), fmt.Errorf("agent exited with error: %w", waitErr)
	}

	return assistantText.String(), nil
}
