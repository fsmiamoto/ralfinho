package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunner_WriteEffectivePromptReturnsReadableErrorWhenRunsDirIsFile(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	if err := os.WriteFile(runsDir, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runsDir, err)
	}

	r := &Runner{
		cfg: RunConfig{
			RunsDir: runsDir,
			Prompt:  "effective prompt text",
		},
		runID:  "write-prompt-error",
		stderr: io.Discard,
	}

	err := r.writeEffectivePrompt()
	if err == nil {
		t.Fatal("writeEffectivePrompt() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "creating run dir") {
		t.Fatalf("writeEffectivePrompt() error = %q, want creating-run-dir context", err)
	}
}

func TestRunner_OpenRunFilesLogsWarningsWhenArtifactsCannotBeCreated(t *testing.T) {
	runsDir := t.TempDir()
	runID := "open-files-error"
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", runDir, err)
	}

	for _, name := range []string{"events.jsonl", "raw-output.log", "session.log"} {
		path := filepath.Join(runDir, name)
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", path, err)
		}
	}

	var stderr bytes.Buffer
	r := &Runner{
		cfg:    RunConfig{RunsDir: runsDir},
		runID:  runID,
		stderr: &stderr,
	}

	r.openRunFiles()

	if r.eventsFile != nil {
		t.Fatalf("eventsFile = %#v, want nil", r.eventsFile)
	}
	if r.rawFile != nil {
		t.Fatalf("rawFile = %#v, want nil", r.rawFile)
	}
	if r.sessionFile != nil {
		t.Fatalf("sessionFile = %#v, want nil", r.sessionFile)
	}

	for _, want := range []string{
		"warning: could not create events.jsonl:",
		"warning: could not create raw-output.log:",
		"warning: could not create session.log:",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want substring %q", stderr.String(), want)
		}
	}
}

func TestRunner_CloseRunFilesLogsWarningsWhenFilesAreAlreadyClosed(t *testing.T) {
	runDir := t.TempDir()

	eventsFile, err := os.Create(filepath.Join(runDir, "events.jsonl"))
	if err != nil {
		t.Fatalf("Create(events.jsonl): %v", err)
	}
	rawFile, err := os.Create(filepath.Join(runDir, "raw-output.log"))
	if err != nil {
		t.Fatalf("Create(raw-output.log): %v", err)
	}
	sessionFile, err := os.Create(filepath.Join(runDir, "session.log"))
	if err != nil {
		t.Fatalf("Create(session.log): %v", err)
	}

	if err := eventsFile.Close(); err != nil {
		t.Fatalf("eventsFile.Close(): %v", err)
	}
	if err := rawFile.Close(); err != nil {
		t.Fatalf("rawFile.Close(): %v", err)
	}
	if err := sessionFile.Close(); err != nil {
		t.Fatalf("sessionFile.Close(): %v", err)
	}

	var stderr bytes.Buffer
	r := &Runner{
		eventsFile:  eventsFile,
		rawFile:     rawFile,
		sessionFile: sessionFile,
		stderr:      &stderr,
	}

	r.closeRunFiles()

	for _, want := range []string{
		"warning: closing events.jsonl:",
		"warning: closing raw-output.log:",
		"warning: closing session.log:",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want substring %q", stderr.String(), want)
		}
	}
}

func TestRunner_SessionLogfLogsWarningWhenWriteFails(t *testing.T) {
	sessionFile, err := os.Create(filepath.Join(t.TempDir(), "session.log"))
	if err != nil {
		t.Fatalf("Create(session.log): %v", err)
	}
	if err := sessionFile.Close(); err != nil {
		t.Fatalf("sessionFile.Close(): %v", err)
	}

	var stderr bytes.Buffer
	r := &Runner{
		sessionFile: sessionFile,
		stderr:      &stderr,
	}

	r.sessionLogf("hello %s\n", "world")

	if !strings.Contains(stderr.String(), "warning: writing to session.log:") {
		t.Fatalf("stderr = %q, want session-log warning", stderr.String())
	}
}

func TestRunner_WriteMetaLogsWarningWhenDirectoryUnavailable(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs")
	if err := os.WriteFile(runsDir, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runsDir, err)
	}

	var stderr bytes.Buffer
	r := &Runner{
		cfg: RunConfig{
			Agent:        "test",
			RunsDir:      runsDir,
			PromptSource: "prompt",
			PromptFile:   "tasks/prompt.md",
		},
		runID:     "meta-write-error",
		startedAt: time.Now(),
		stderr:    &stderr,
	}

	r.writeMeta(StatusFailed, 2)

	if !strings.Contains(stderr.String(), "warning: could not write meta.json: writing meta.json:") {
		t.Fatalf("stderr = %q, want meta warning", stderr.String())
	}
}

func TestWriteMetaJSONReturnsReadableWriteError(t *testing.T) {
	parentFile := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(parentFile, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("WriteFile(%q): %v", parentFile, err)
	}

	err := writeMetaJSON(filepath.Join(parentFile, "meta.json"), RunMeta{RunID: "run-1"})
	if err == nil {
		t.Fatal("writeMetaJSON() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "writing meta.json") {
		t.Fatalf("writeMetaJSON() error = %q, want write context", err)
	}
}

func TestRun_InvalidAgentReturnsFailureAndWritesFailedMeta(t *testing.T) {
	runsDir := t.TempDir()
	var stderr bytes.Buffer

	r := New(RunConfig{
		Agent:   "mystery-agent",
		Prompt:  "do the task",
		RunsDir: runsDir,
	})
	r.stderr = &stderr

	result := r.Run(context.Background())

	if result.Status != StatusFailed {
		t.Fatalf("result.Status = %q, want %q", result.Status, StatusFailed)
	}
	if result.Iterations != 0 {
		t.Fatalf("result.Iterations = %d, want 0", result.Iterations)
	}
	if !strings.Contains(result.Error, `unknown agent "mystery-agent"`) {
		t.Fatalf("result.Error = %q, want unknown-agent message", result.Error)
	}

	promptBytes, err := os.ReadFile(filepath.Join(runsDir, r.runID, "effective-prompt.md"))
	if err != nil {
		t.Fatalf("ReadFile(effective-prompt.md): %v", err)
	}
	if string(promptBytes) != "do the task" {
		t.Fatalf("effective prompt = %q, want %q", string(promptBytes), "do the task")
	}

	metaBytes, err := os.ReadFile(filepath.Join(runsDir, r.runID, "meta.json"))
	if err != nil {
		t.Fatalf("ReadFile(meta.json): %v", err)
	}

	var meta RunMeta
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		t.Fatalf("json.Unmarshal(meta.json): %v", err)
	}
	if meta.Status != string(StatusFailed) {
		t.Fatalf("meta.Status = %q, want %q", meta.Status, StatusFailed)
	}
	if meta.IterationsCompleted != 0 {
		t.Fatalf("meta.IterationsCompleted = %d, want 0", meta.IterationsCompleted)
	}
	if meta.Agent != "mystery-agent" {
		t.Fatalf("meta.Agent = %q, want %q", meta.Agent, "mystery-agent")
	}
	if !strings.Contains(stderr.String(), "error: unknown agent \"mystery-agent\"") {
		t.Fatalf("stderr = %q, want unknown-agent log", stderr.String())
	}
}
