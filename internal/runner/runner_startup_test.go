package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun_ContinuesAfterEffectivePromptWriteWarningAndResolvesRealAgent(t *testing.T) {
	runsDir := t.TempDir()
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", binDir, err)
	}

	argvFile := filepath.Join(t.TempDir(), "pi-argv.txt")
	fakePI := filepath.Join(binDir, "pi")
	fakePIBody := `#!/bin/sh
printf '%s\n' "$@" > "$RALFINHO_ARGV_FILE"
cat <<'JSONL'
{"type":"message_start","message":{"role":"assistant","model":"fake-pi"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"<promise>COMPLETE</promise>"}}
{"type":"message_end"}
{"type":"turn_end"}
JSONL
`
	if err := os.WriteFile(fakePI, []byte(fakePIBody), 0755); err != nil {
		t.Fatalf("WriteFile(%q): %v", fakePI, err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RALFINHO_ARGV_FILE", argvFile)

	r := New(RunConfig{
		Agent:          "pi",
		Prompt:         "runner startup prompt",
		RunsDir:        runsDir,
		AgentExtraArgs: []string{"--runner-flag", "from-test"},
	})
	r.runID = "startup-warning-run"
	var stderr bytes.Buffer
	r.stderr = &stderr

	runDir := filepath.Join(runsDir, r.runID)
	if err := os.MkdirAll(filepath.Join(runDir, "effective-prompt.md"), 0755); err != nil {
		t.Fatalf("MkdirAll(effective-prompt.md): %v", err)
	}

	result := r.Run(context.Background())

	if result.Status != StatusCompleted {
		t.Fatalf("result.Status = %q, want %q", result.Status, StatusCompleted)
	}
	if result.Iterations != 1 {
		t.Fatalf("result.Iterations = %d, want 1", result.Iterations)
	}
	if result.Error != "" {
		t.Fatalf("result.Error = %q, want empty", result.Error)
	}

	if !strings.Contains(stderr.String(), "warning: could not write effective prompt:") {
		t.Fatalf("stderr = %q, want effective-prompt warning", stderr.String())
	}
	if !strings.Contains(stderr.String(), "agent signalled COMPLETE") {
		t.Fatalf("stderr = %q, want completion log", stderr.String())
	}

	argvBytes, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("ReadFile(argv): %v", err)
	}
	argv := strings.Split(strings.TrimSpace(string(argvBytes)), "\n")
	if len(argv) != 7 {
		t.Fatalf("argv length = %d, want 7: %#v", len(argv), argv)
	}
	for i, want := range []string{"--mode", "json", "-p", "--no-session"} {
		if argv[i] != want {
			t.Fatalf("argv[%d] = %q, want %q (full argv: %#v)", i, argv[i], want, argv)
		}
	}
	if !strings.HasPrefix(argv[4], "@") || !strings.Contains(argv[4], "ralfinho-prompt-") {
		t.Fatalf("argv[4] = %q, want @<temp prompt file>", argv[4])
	}
	if argv[5] != "--runner-flag" || argv[6] != "from-test" {
		t.Fatalf("argv tail = %#v, want runner extra args", argv[5:])
	}

	rawBytes, err := os.ReadFile(filepath.Join(runDir, "raw-output.log"))
	if err != nil {
		t.Fatalf("ReadFile(raw-output.log): %v", err)
	}
	if !strings.Contains(string(rawBytes), `"type":"message_start"`) || !strings.Contains(string(rawBytes), completionMarker) {
		t.Fatalf("raw-output.log = %q, want streamed fake pi output", string(rawBytes))
	}

	metaBytes, err := os.ReadFile(filepath.Join(runDir, "meta.json"))
	if err != nil {
		t.Fatalf("ReadFile(meta.json): %v", err)
	}
	var meta RunMeta
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatalf("json.Unmarshal(meta.json): %v", err)
	}
	if meta.Status != string(StatusCompleted) {
		t.Fatalf("meta.Status = %q, want %q", meta.Status, StatusCompleted)
	}
	if meta.Agent != "pi" {
		t.Fatalf("meta.Agent = %q, want %q", meta.Agent, "pi")
	}
	if meta.IterationsCompleted != 1 {
		t.Fatalf("meta.IterationsCompleted = %d, want 1", meta.IterationsCompleted)
	}

	promptInfo, err := os.Stat(filepath.Join(runDir, "effective-prompt.md"))
	if err != nil {
		t.Fatalf("Stat(effective-prompt.md): %v", err)
	}
	if !promptInfo.IsDir() {
		t.Fatalf("effective-prompt.md should still be the pre-created directory after the warning")
	}
}
