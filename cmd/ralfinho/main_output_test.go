package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fsmiamoto/ralfinho/internal/cli"
	"github.com/fsmiamoto/ralfinho/internal/runner"
)

func TestPrintRunSummaryWritesExpectedLines(t *testing.T) {
	t.Run("without error", func(t *testing.T) {
		stdout, stderr := captureCommandOutput(t, func() {
			printRunSummary("run summary", runner.RunResult{
				RunID:      "run-1234",
				Agent:      "pi",
				Iterations: 2,
				Status:     runner.StatusCompleted,
			})
		})

		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}

		want := "\n=== run summary ===\n" +
			"run-id:     run-1234\n" +
			"agent:      pi\n" +
			"iterations: 2\n" +
			"status:     completed\n"
		if stderr != want {
			t.Fatalf("stderr = %q, want %q", stderr, want)
		}
	})

	t.Run("with error", func(t *testing.T) {
		_, stderr := captureCommandOutput(t, func() {
			printRunSummary("resumed run summary", runner.RunResult{
				RunID:      "run-5678",
				Agent:      "claude",
				Iterations: 4,
				Status:     runner.StatusFailed,
				Error:      "agent crashed",
			})
		})

		want := "\n=== resumed run summary ===\n" +
			"run-id:     run-5678\n" +
			"agent:      claude\n" +
			"iterations: 4\n" +
			"status:     failed\n" +
			"error:      agent crashed\n"
		if stderr != want {
			t.Fatalf("stderr = %q, want %q", stderr, want)
		}
	})
}

func TestListRunsPrintsReadableOutput(t *testing.T) {
	t.Run("empty runs dir", func(t *testing.T) {
		stdout, stderr := captureCommandOutput(t, func() {
			listRuns(&cli.Config{RunsDir: t.TempDir()})
		})

		if stdout != "No runs found.\n" {
			t.Fatalf("stdout = %q, want %q", stdout, "No runs found.\\n")
		}
		if stderr != "" {
			t.Fatalf("stderr = %q, want empty", stderr)
		}
	})

	t.Run("available runs", func(t *testing.T) {
		runsDir := t.TempDir()
		writeMetaOnlyRun(t, runsDir, "11111111-old", runner.RunMeta{
			RunID:               "11111111-old",
			StartedAt:           "2026-03-07T10:00:00Z",
			Status:              string(runner.StatusCompleted),
			Agent:               "pi",
			PromptSource:        "plan",
			PlanFile:            "plans/PLAN.md",
			IterationsCompleted: 2,
		})
		writeMetaOnlyRun(t, runsDir, "22222222-new", runner.RunMeta{
			RunID:               "22222222-new",
			StartedAt:           "2026-03-08T11:30:00Z",
			Status:              string(runner.StatusInterrupted),
			Agent:               "kiro",
			PromptSource:        "prompt",
			PromptFile:          "tasks/browser-prompt.md",
			IterationsCompleted: 4,
		})

		stdout, stderr := captureCommandOutput(t, func() {
			listRuns(&cli.Config{RunsDir: runsDir})
		})
		if stderr != "" {
			t.Fatalf("stderr = %q, want empty", stderr)
		}

		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		if len(lines) != 3 {
			t.Fatalf("stdout lines = %q, want 3 lines", stdout)
		}
		if lines[0] != "Available runs:" {
			t.Fatalf("header = %q, want %q", lines[0], "Available runs:")
		}

		for _, want := range []string{"22222222", "2026-03-08 11:30", "kiro", "interrupted", "4 iterations", "browser-prompt.md"} {
			if !strings.Contains(lines[1], want) {
				t.Fatalf("newest run line = %q, missing %q", lines[1], want)
			}
		}
		for _, want := range []string{"11111111", "2026-03-07 10:00", "pi", "completed", "2 iterations", "PLAN.md"} {
			if !strings.Contains(lines[2], want) {
				t.Fatalf("older run line = %q, missing %q", lines[2], want)
			}
		}
	})
}

func TestOpenRunViewerReturnsLoadErrorBeforeStartingTUI(t *testing.T) {
	err := openRunViewer(t.TempDir(), "missing")
	if err == nil {
		t.Fatal("openRunViewer() error = nil, want error")
	}
	if !strings.Contains(err.Error(), `no run found matching "missing"`) {
		t.Fatalf("openRunViewer() error = %q, want missing-run message", err)
	}
}

func TestExitForStatusUsesExpectedExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		status   runner.Status
		wantCode int
	}{
		{name: "completed returns zero", status: runner.StatusCompleted, wantCode: 0},
		{name: "max iterations returns zero", status: runner.StatusMaxIterationsReached, wantCode: 0},
		{name: "failed exits one", status: runner.StatusFailed, wantCode: 1},
		{name: "interrupted exits two", status: runner.StatusInterrupted, wantCode: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, exitCode := runHelperProcess(t, map[string]string{
				"HELPER_ACTION": "exit-status",
				"HELPER_STATUS": string(tt.status),
			})
			if exitCode != tt.wantCode {
				t.Fatalf("exit code = %d, want %d", exitCode, tt.wantCode)
			}
		})
	}
}

func TestListRunsExitsWithReadableError(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "not-a-dir")
	if err != nil {
		t.Fatalf("CreateTemp(): %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	_, stderr, exitCode := runHelperProcess(t, map[string]string{
		"HELPER_ACTION":   "list-runs",
		"HELPER_RUNS_DIR": file.Name(),
	})
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr, "ralfinho view: reading runs directory:") {
		t.Fatalf("stderr = %q, want readable list-runs error", stderr)
	}
}

func TestRunViewerExitsWithReadableError(t *testing.T) {
	_, stderr, exitCode := runHelperProcess(t, map[string]string{
		"HELPER_ACTION":   "run-viewer",
		"HELPER_RUNS_DIR": t.TempDir(),
		"HELPER_RUN_ID":   "missing",
	})
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr, `ralfinho view: no run found matching "missing"`) {
		t.Fatalf("stderr = %q, want readable run-viewer error", stderr)
	}
}

func TestRunBrowserExitsWithReadableError(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "not-a-dir")
	if err != nil {
		t.Fatalf("CreateTemp(): %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}

	_, stderr, exitCode := runHelperProcess(t, map[string]string{
		"HELPER_ACTION":   "run-browser",
		"HELPER_RUNS_DIR": file.Name(),
	})
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr, "ralfinho view: reading runs directory:") {
		t.Fatalf("stderr = %q, want readable run-browser error", stderr)
	}
}

func TestRunBrowserExitsWhenBrowserProgramFails(t *testing.T) {
	runsDir := t.TempDir()
	writeMetaOnlyRun(t, runsDir, "saved-run", runner.RunMeta{
		RunID:               "saved-run",
		StartedAt:           "2026-03-08T10:00:00Z",
		Status:              string(runner.StatusCompleted),
		Agent:               "pi",
		PromptSource:        "default",
		IterationsCompleted: 1,
	})
	writeRunEventsArtifact(t, runsDir, "saved-run", `{"type":"turn_end"}`)

	_, stderr, exitCode := runHelperProcess(t, map[string]string{
		"HELPER_ACTION":    "run-browser-tui-error",
		"HELPER_RUNS_DIR":  runsDir,
		"HELPER_TUI_ERROR": "browser boom",
	})
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr, "ralfinho: TUI error: browser boom") {
		t.Fatalf("stderr = %q, want readable browser-program error", stderr)
	}
}

func TestRunTUIExitsWithReadableTUIError(t *testing.T) {
	clearFileConfig(t)
	installFakePIBinary(t, `#!/bin/sh
cat <<'JSONL'
{"type":"message_start","message":{"role":"assistant","model":"fake-pi"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"<promise>COMPLETE</promise>"}}
{"type":"message_end"}
{"type":"turn_end"}
JSONL
`)

	stdout, stderr, exitCode := runHelperProcess(t, map[string]string{
		"HELPER_ACTION":    "run-tui-error",
		"HELPER_RUNS_DIR":  t.TempDir(),
		"HELPER_TUI_ERROR": "run boom",
	})
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "ralfinho: TUI error: run boom") {
		t.Fatalf("stderr = %q, want readable runTUI error", stderr)
	}
}

func TestRunTUIInterruptedRunPrintsSummaryAndExitsTwo(t *testing.T) {
	clearFileConfig(t)
	installFakePIBinary(t, `#!/bin/sh
cat <<'JSONL'
{"type":"message_start","message":{"role":"assistant","model":"fake-pi"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"still working"}}
JSONL
sleep 60
`)

	stdout, stderr, exitCode := runHelperProcess(t, map[string]string{
		"HELPER_ACTION":   "run-tui-interrupted",
		"HELPER_RUNS_DIR": t.TempDir(),
	})
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2", exitCode)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{"=== run summary ===", "agent:      pi", "status:     interrupted"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, missing %q", stderr, want)
		}
	}
}

func TestCommandHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	switch os.Getenv("HELPER_ACTION") {
	case "exit-status":
		exitForStatus(runner.Status(os.Getenv("HELPER_STATUS")))
	case "list-runs":
		listRuns(&cli.Config{RunsDir: os.Getenv("HELPER_RUNS_DIR")})
	case "run-viewer":
		runViewer(&cli.Config{
			RunsDir:   os.Getenv("HELPER_RUNS_DIR"),
			ViewRunID: os.Getenv("HELPER_RUN_ID"),
		})
	case "run-browser":
		runBrowser(&cli.Config{RunsDir: os.Getenv("HELPER_RUNS_DIR")})
	case "run-browser-tui-error":
		newTeaProgram = func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
			return &scriptedTeaProgram{run: func() (tea.Model, error) {
				return nil, errors.New(os.Getenv("HELPER_TUI_ERROR"))
			}}
		}
		runBrowser(&cli.Config{RunsDir: os.Getenv("HELPER_RUNS_DIR")})
	case "run-tui-error":
		newTeaProgram = func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
			return newDoneAwareTeaProgramWithError(model, errors.New(os.Getenv("HELPER_TUI_ERROR")))
		}
		runTUI(&cli.Config{Agent: "pi", RunsDir: os.Getenv("HELPER_RUNS_DIR")}, "finish immediately")
	case "run-tui-interrupted":
		newTeaProgram = func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
			return &scriptedTeaProgram{run: func() (tea.Model, error) { return model, nil }}
		}
		runTUI(&cli.Config{Agent: "pi", RunsDir: os.Getenv("HELPER_RUNS_DIR")}, "keep working")
	default:
		t.Fatalf("unknown HELPER_ACTION %q", os.Getenv("HELPER_ACTION"))
	}

	os.Exit(0)
}

func captureCommandOutput(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(stdout): %v", err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(stderr): %v", err)
	}

	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter

	fn()

	os.Stdout = oldStdout
	os.Stderr = oldStderr

	if err := stdoutWriter.Close(); err != nil {
		t.Fatalf("stdoutWriter.Close(): %v", err)
	}
	if err := stderrWriter.Close(); err != nil {
		t.Fatalf("stderrWriter.Close(): %v", err)
	}

	stdoutBytes, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatalf("io.ReadAll(stdout): %v", err)
	}
	stderrBytes, err := io.ReadAll(stderrReader)
	if err != nil {
		t.Fatalf("io.ReadAll(stderr): %v", err)
	}

	if err := stdoutReader.Close(); err != nil {
		t.Fatalf("stdoutReader.Close(): %v", err)
	}
	if err := stderrReader.Close(); err != nil {
		t.Fatalf("stderrReader.Close(): %v", err)
	}

	return string(stdoutBytes), string(stderrBytes)
}

func runHelperProcess(t *testing.T, env map[string]string) (stdout, stderr string, exitCode int) {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=^TestCommandHelperProcess$")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("helper process failed unexpectedly: %v", err)
		}
		exitCode = exitErr.ExitCode()
	}

	return stdoutBuf.String(), stderrBuf.String(), exitCode
}

func writeMetaOnlyRun(t *testing.T, runsDir, runID string, meta runner.RunMeta) {
	t.Helper()

	dir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(meta): %v", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(filepath.Join(dir, "meta.json"), data, 0644); err != nil {
		t.Fatalf("WriteFile(meta.json): %v", err)
	}
}
