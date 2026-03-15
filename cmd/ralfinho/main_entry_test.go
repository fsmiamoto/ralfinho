package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fsmiamoto/ralfinho/internal/cli"
	"github.com/fsmiamoto/ralfinho/internal/runner"
)

func TestMainHelpShowsUsageAndExitsZero(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, exitCode := runMainHelperProcess(t, dir, []string{"--help"}, map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(dir, "xdg"),
	})

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "Usage: ralfinho") {
		t.Fatalf("stderr = %q, want usage text", stderr)
	}
	if !strings.Contains(stderr, "Session browser keybindings:") {
		t.Fatalf("stderr = %q, want browser keybindings help", stderr)
	}
}

func TestMainVersionPrintsVersionAndExitsZero(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, exitCode := runMainHelperProcess(t, dir, []string{"--version"}, map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(dir, "xdg"),
	})

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	want := "ralfinho " + cli.Version + "\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestMainViewNoTUIDispatchesToListMode(t *testing.T) {
	dir := t.TempDir()
	runsDir := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runsDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", runsDir, err)
	}

	stdout, stderr, exitCode := runMainHelperProcess(t, dir, []string{"view", "--no-tui", "--runs-dir", runsDir}, map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(dir, "xdg"),
	})

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if stdout != "No runs found.\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "No runs found.\\n")
	}
}

func TestMainViewReplayDispatchesToViewer(t *testing.T) {
	dir := t.TempDir()
	runsDir := filepath.Join(dir, "runs")
	writeMetaOnlyRun(t, runsDir, "saved-run", runner.RunMeta{
		RunID:               "saved-run",
		StartedAt:           "2026-03-08T10:00:00Z",
		Status:              string(runner.StatusCompleted),
		Agent:               "pi",
		PromptSource:        "default",
		IterationsCompleted: 1,
	})
	writeRunEventsArtifact(t, runsDir, "saved-run", `{"type":"turn_end"}`)

	stdout, stderr, exitCode := runMainHelperProcess(t, dir, []string{"view", "--runs-dir", runsDir, "saved-run"}, map[string]string{
		"XDG_CONFIG_HOME":      filepath.Join(dir, "xdg"),
		"HELPER_MAIN_TUI_MODE": "return-model",
	})

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout=%q\nstderr=%q", exitCode, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestMainConfigLoadErrorIsReadable(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, ".ralfinho")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", configDir, err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("agent = [\n"), 0644); err != nil {
		t.Fatalf("WriteFile(config.toml): %v", err)
	}

	stdout, stderr, exitCode := runMainHelperProcess(t, dir, nil, map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(dir, "xdg"),
	})

	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "ralfinho: config: parsing .ralfinho/config.toml:") {
		t.Fatalf("stderr = %q, want readable config parse error", stderr)
	}
}

func TestMainMissingPromptFileErrorIsReadable(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, exitCode := runMainHelperProcess(t, dir, []string{"--prompt", "missing-prompt.md"}, map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(dir, "xdg"),
	})

	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, `ralfinho: reading prompt file "missing-prompt.md":`) {
		t.Fatalf("stderr = %q, want readable prompt error", stderr)
	}
}

func TestMainInvalidAgentPrintsReadableError(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, exitCode := runMainHelperProcess(t, dir, []string{"--agent", "mystery-agent"}, map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(dir, "xdg"),
	})

	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, `ralfinho: unknown agent "mystery-agent"`) {
		t.Fatalf("stderr = %q, want readable unknown-agent error", stderr)
	}
}

func TestMainFallsBackToPlainModeAndPassesConfiguredExtraArgs(t *testing.T) {
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "pi-argv.txt")
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", binDir, err)
	}
	writeFakePiBinary(t, filepath.Join(binDir, "pi"))

	configDir := filepath.Join(dir, ".ralfinho")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", configDir, err)
	}
	configText := `[agents.pi]
extra-args = ["--test-flag", "from-config"]
`
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(configText), 0644); err != nil {
		t.Fatalf("WriteFile(config.toml): %v", err)
	}

	stdout, stderr, exitCode := runMainHelperProcess(t, dir, nil, map[string]string{
		"XDG_CONFIG_HOME":    filepath.Join(dir, "xdg"),
		"PATH":               binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"RALFINHO_ARGV_FILE": argvFile,
	})

	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout=%q\nstderr=%q", exitCode, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{"run summary", "agent:      pi", "status:     completed", "iterations: 1"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, missing %q", stderr, want)
		}
	}

	argvBytes, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("ReadFile(argv): %v", err)
	}
	argv := strings.Split(strings.TrimSpace(string(argvBytes)), "\n")
	if len(argv) != 7 {
		t.Fatalf("argv length = %d, want 7: %#v", len(argv), argv)
	}

	wantPrefix := []string{"--mode", "json", "-p", "--no-session"}
	for i, want := range wantPrefix {
		if argv[i] != want {
			t.Fatalf("argv[%d] = %q, want %q (full argv: %#v)", i, argv[i], want, argv)
		}
	}
	if !strings.HasPrefix(argv[4], "@") || !strings.Contains(argv[4], "ralfinho-prompt-") {
		t.Fatalf("argv[4] = %q, want @<temp prompt file>", argv[4])
	}
	if argv[5] != "--test-flag" || argv[6] != "from-config" {
		t.Fatalf("argv tail = %#v, want extra args from config", argv[5:])
	}
}

func TestMainHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MAIN_HELPER_PROCESS") != "1" {
		return
	}

	if mode := os.Getenv("HELPER_MAIN_TUI_MODE"); mode != "" {
		switch mode {
		case "return-model":
			newTeaProgram = func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
				return &scriptedTeaProgram{run: func() (tea.Model, error) { return model, nil }}
			}
		default:
			t.Fatalf("unknown HELPER_MAIN_TUI_MODE %q", mode)
		}
	}

	var args []string
	if raw := os.Getenv("HELPER_MAIN_ARGS"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			t.Fatalf("unmarshal HELPER_MAIN_ARGS: %v", err)
		}
	}

	oldArgs := os.Args
	os.Args = append([]string{"ralfinho"}, args...)
	defer func() { os.Args = oldArgs }()

	main()
	os.Exit(0)
}

func runMainHelperProcess(t *testing.T, dir string, args []string, env map[string]string) (stdout, stderr string, exitCode int) {
	t.Helper()

	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("json.Marshal(args): %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestMainHelperProcess$")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GO_WANT_MAIN_HELPER_PROCESS=1",
		"HELPER_MAIN_ARGS="+string(argsJSON),
	)
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()
	exitCode = 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("main helper process failed unexpectedly: %v", err)
		}
		exitCode = exitErr.ExitCode()
	}

	return stdoutBuf.String(), stderrBuf.String(), exitCode
}

func writeFakePiBinary(t *testing.T, path string) {
	t.Helper()

	body := `#!/bin/sh
printf '%s\n' "$@" > "$RALFINHO_ARGV_FILE"
cat <<'JSONL'
{"type":"message_start","message":{"role":"assistant","model":"fake-pi"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"<promise>COMPLETE</promise>"}}
{"type":"message_end"}
{"type":"turn_end"}
JSONL
`
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
