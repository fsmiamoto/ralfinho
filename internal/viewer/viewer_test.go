package viewer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsmiamoto/ralfinho/internal/runner"
)

// writeEventsJSONL writes a raw JSONL string to the run's events.jsonl file.
func writeEventsJSONL(t *testing.T, runsDir, runID, content string) {
	t.Helper()
	dir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(events.jsonl): %v", err)
	}
}

// --- ResolveRunID ---

func TestResolveRunIDExactMatch(t *testing.T) {
	runsDir := t.TempDir()
	writeRunMeta(t, runsDir, "abc123", runner.RunMeta{RunID: "abc123"})

	got, err := ResolveRunID(runsDir, "abc123")
	if err != nil {
		t.Fatalf("ResolveRunID() error = %v", err)
	}
	if got != "abc123" {
		t.Fatalf("ResolveRunID() = %q, want %q", got, "abc123")
	}
}

func TestResolveRunIDUniquePrefixMatch(t *testing.T) {
	runsDir := t.TempDir()
	writeRunMeta(t, runsDir, "abc123-full", runner.RunMeta{RunID: "abc123-full"})

	got, err := ResolveRunID(runsDir, "abc123")
	if err != nil {
		t.Fatalf("ResolveRunID() error = %v", err)
	}
	if got != "abc123-full" {
		t.Fatalf("ResolveRunID() = %q, want %q", got, "abc123-full")
	}
}

func TestResolveRunIDAmbiguousPrefix(t *testing.T) {
	runsDir := t.TempDir()
	writeRunMeta(t, runsDir, "abc123-first", runner.RunMeta{RunID: "abc123-first"})
	writeRunMeta(t, runsDir, "abc123-second", runner.RunMeta{RunID: "abc123-second"})

	_, err := ResolveRunID(runsDir, "abc123")
	if err == nil {
		t.Fatal("ResolveRunID() expected error for ambiguous prefix, got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %q, want to contain %q", err.Error(), "ambiguous")
	}
	if !strings.Contains(err.Error(), "abc123-first") {
		t.Fatalf("error = %q, want to list %q", err.Error(), "abc123-first")
	}
	if !strings.Contains(err.Error(), "abc123-second") {
		t.Fatalf("error = %q, want to list %q", err.Error(), "abc123-second")
	}
}

func TestResolveRunIDNoMatch(t *testing.T) {
	runsDir := t.TempDir()
	writeRunMeta(t, runsDir, "xyz999", runner.RunMeta{RunID: "xyz999"})

	_, err := ResolveRunID(runsDir, "abc")
	if err == nil {
		t.Fatal("ResolveRunID() expected error for no match, got nil")
	}
	if !strings.Contains(err.Error(), "no run found") {
		t.Fatalf("error = %q, want to contain %q", err.Error(), "no run found")
	}
}

func TestResolveRunIDNonExistentRunsDir(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "does-not-exist")

	_, err := ResolveRunID(runsDir, "any")
	if err == nil {
		t.Fatal("ResolveRunID() expected error for missing directory, got nil")
	}
}

func TestResolveRunIDSkipsNonDirectoryEntries(t *testing.T) {
	runsDir := t.TempDir()

	// A plain file whose name starts with the prefix must not count as a match.
	if err := os.WriteFile(filepath.Join(runsDir, "abc123-file.txt"), []byte("not a run"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := ResolveRunID(runsDir, "abc123")
	if err == nil {
		t.Fatal("ResolveRunID() expected error when only non-directory entries match, got nil")
	}
	if !strings.Contains(err.Error(), "no run found") {
		t.Fatalf("error = %q, want %q", err.Error(), "no run found")
	}
}

// --- ListRuns ---

func TestListRunsSortedByStartedAtDescending(t *testing.T) {
	runsDir := t.TempDir()

	writeRunMeta(t, runsDir, "older", runner.RunMeta{
		RunID:     "older",
		StartedAt: "2026-03-07T08:00:00Z",
		Status:    string(runner.StatusCompleted),
	})
	writeRunMeta(t, runsDir, "newer", runner.RunMeta{
		RunID:     "newer",
		StartedAt: "2026-03-08T09:00:00Z",
		Status:    string(runner.StatusCompleted),
	})
	writeRunMeta(t, runsDir, "middle", runner.RunMeta{
		RunID:     "middle",
		StartedAt: "2026-03-07T18:00:00Z",
		Status:    string(runner.StatusCompleted),
	})

	runs, err := ListRuns(runsDir)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("len(runs) = %d, want 3", len(runs))
	}

	wantOrder := []string{"newer", "middle", "older"}
	for i, want := range wantOrder {
		if runs[i].RunID != want {
			t.Fatalf("runs[%d].RunID = %q, want %q", i, runs[i].RunID, want)
		}
	}
}

func TestListRunsMissingRunsDirReturnsNilNil(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "missing")

	runs, err := ListRuns(runsDir)
	if err != nil {
		t.Fatalf("ListRuns() error = %v, want nil", err)
	}
	if runs != nil {
		t.Fatalf("ListRuns() = %v, want nil", runs)
	}
}

func TestListRunsNonDirectoryRunsDirReturnsReadableError(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "runs-file")
	if err := os.WriteFile(runsDir, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runsDir, err)
	}

	runs, err := ListRuns(runsDir)
	if err == nil {
		t.Fatal("ListRuns() expected error for non-directory runsDir, got nil")
	}
	if runs != nil {
		t.Fatalf("ListRuns() = %v, want nil on error", runs)
	}
	if !strings.Contains(err.Error(), "reading runs directory") {
		t.Fatalf("error = %q, want to mention %q", err.Error(), "reading runs directory")
	}
}

func TestListRunsSkipsDirectoriesWithoutMetaJSON(t *testing.T) {
	runsDir := t.TempDir()

	// A directory with no meta.json.
	noMeta := filepath.Join(runsDir, "no-meta")
	if err := os.MkdirAll(noMeta, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// A valid run.
	writeRunMeta(t, runsDir, "valid", runner.RunMeta{RunID: "valid", StartedAt: "2026-03-08T10:00:00Z"})

	runs, err := ListRuns(runsDir)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("len(runs) = %d, want 1", len(runs))
	}
	if runs[0].RunID != "valid" {
		t.Fatalf("runs[0].RunID = %q, want %q", runs[0].RunID, "valid")
	}
}

func TestListRunsSkipsDirectoriesWithCorruptMetaJSON(t *testing.T) {
	runsDir := t.TempDir()

	corruptDir := filepath.Join(runsDir, "corrupt")
	if err := os.MkdirAll(corruptDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(corruptDir, "meta.json"), []byte("{not valid json"), 0644); err != nil {
		t.Fatalf("WriteFile(corrupt meta.json): %v", err)
	}

	writeRunMeta(t, runsDir, "valid", runner.RunMeta{RunID: "valid", StartedAt: "2026-03-08T10:00:00Z"})

	runs, err := ListRuns(runsDir)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("len(runs) = %d, want 1 (corrupt entry should be skipped)", len(runs))
	}
}

func TestListRunsSkipsNonDirectoryEntries(t *testing.T) {
	runsDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(runsDir, "stray.txt"), []byte("stray"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	writeRunMeta(t, runsDir, "valid", runner.RunMeta{RunID: "valid", StartedAt: "2026-03-08T10:00:00Z"})

	runs, err := ListRuns(runsDir)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("len(runs) = %d, want 1", len(runs))
	}
}

func TestListRunsEmptyDirectoryReturnsEmptySlice(t *testing.T) {
	runsDir := t.TempDir()

	runs, err := ListRuns(runsDir)
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	// An empty directory is fine; nil or empty are both acceptable because
	// ListRuns only appends when it finds valid metas.
	if len(runs) != 0 {
		t.Fatalf("len(runs) = %d, want 0", len(runs))
	}
}

// --- LoadRun ---

func TestLoadRunFullLoad(t *testing.T) {
	runsDir := t.TempDir()
	meta := runner.RunMeta{
		RunID:               "full-run",
		StartedAt:           "2026-03-08T10:00:00Z",
		EndedAt:             "2026-03-08T10:05:00Z",
		Status:              string(runner.StatusCompleted),
		Agent:               "pi",
		PromptSource:        "prompt",
		PromptFile:          "tasks/my-task.md",
		MaxIterations:       10,
		IterationsCompleted: 3,
	}
	writeRunMeta(t, runsDir, "full-run", meta)
	writeRunEvents(t, runsDir, "full-run")
	writeEffectivePrompt(t, runsDir, "full-run", "the effective prompt text")

	run, err := LoadRun(runsDir, "full-run")
	if err != nil {
		t.Fatalf("LoadRun() error = %v", err)
	}

	if run.Meta.RunID != "full-run" {
		t.Fatalf("Meta.RunID = %q, want %q", run.Meta.RunID, "full-run")
	}
	if run.Meta.Agent != "pi" {
		t.Fatalf("Meta.Agent = %q, want %q", run.Meta.Agent, "pi")
	}
	if run.Meta.IterationsCompleted != 3 {
		t.Fatalf("Meta.IterationsCompleted = %d, want 3", run.Meta.IterationsCompleted)
	}
	if run.Prompt != "the effective prompt text" {
		t.Fatalf("Prompt = %q, want %q", run.Prompt, "the effective prompt text")
	}
	if len(run.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1", len(run.Events))
	}
	if run.Events[0].Type != runner.EventTurnEnd {
		t.Fatalf("Events[0].Type = %q, want %q", run.Events[0].Type, runner.EventTurnEnd)
	}
}

func TestLoadRunMissingEffectivePromptSucceeds(t *testing.T) {
	runsDir := t.TempDir()
	writeRunMeta(t, runsDir, "no-prompt-run", runner.RunMeta{RunID: "no-prompt-run"})
	writeRunEvents(t, runsDir, "no-prompt-run")
	// Deliberately do not call writeEffectivePrompt.

	run, err := LoadRun(runsDir, "no-prompt-run")
	if err != nil {
		t.Fatalf("LoadRun() error = %v, want nil (effective-prompt.md is optional)", err)
	}
	if run.Prompt != "" {
		t.Fatalf("Prompt = %q, want empty string when effective-prompt.md is absent", run.Prompt)
	}
}

func TestLoadRunIgnoresUnreadableEffectivePromptDirectory(t *testing.T) {
	runsDir := t.TempDir()
	writeRunMeta(t, runsDir, "dir-prompt-run", runner.RunMeta{RunID: "dir-prompt-run"})
	writeRunEvents(t, runsDir, "dir-prompt-run")

	promptDir := filepath.Join(runsDir, "dir-prompt-run", "effective-prompt.md")
	if err := os.MkdirAll(promptDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", promptDir, err)
	}

	run, err := LoadRun(runsDir, "dir-prompt-run")
	if err != nil {
		t.Fatalf("LoadRun() error = %v, want nil when effective-prompt.md cannot be read", err)
	}
	if run.Prompt != "" {
		t.Fatalf("Prompt = %q, want empty string when effective-prompt.md is unreadable", run.Prompt)
	}
}

func TestLoadRunMissingMetaJSONErrors(t *testing.T) {
	runsDir := t.TempDir()

	// Create the run directory but omit meta.json.
	dir := filepath.Join(runsDir, "no-meta")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeEventsJSONL(t, runsDir, "no-meta", "{\"type\":\"turn_end\"}\n")

	_, err := LoadRun(runsDir, "no-meta")
	if err == nil {
		t.Fatal("LoadRun() expected error for missing meta.json, got nil")
	}
	if !strings.Contains(err.Error(), "meta.json") {
		t.Fatalf("error = %q, want to mention %q", err.Error(), "meta.json")
	}
}

func TestLoadRunCorruptMetaJSONErrors(t *testing.T) {
	runsDir := t.TempDir()

	dir := filepath.Join(runsDir, "corrupt-meta")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte("{bad json"), 0644); err != nil {
		t.Fatalf("WriteFile(corrupt meta.json): %v", err)
	}
	writeEventsJSONL(t, runsDir, "corrupt-meta", "{\"type\":\"turn_end\"}\n")

	_, err := LoadRun(runsDir, "corrupt-meta")
	if err == nil {
		t.Fatal("LoadRun() expected error for corrupt meta.json, got nil")
	}
	if !strings.Contains(err.Error(), "parsing meta.json") {
		t.Fatalf("error = %q, want to contain %q", err.Error(), "parsing meta.json")
	}
}

func TestLoadRunMissingEventsJSONLErrors(t *testing.T) {
	runsDir := t.TempDir()
	writeRunMeta(t, runsDir, "no-events", runner.RunMeta{RunID: "no-events"})
	// Deliberately do not write events.jsonl.

	_, err := LoadRun(runsDir, "no-events")
	if err == nil {
		t.Fatal("LoadRun() expected error for missing events.jsonl, got nil")
	}
	if !strings.Contains(err.Error(), "events.jsonl") {
		t.Fatalf("error = %q, want to mention %q", err.Error(), "events.jsonl")
	}
}

func TestLoadRunDelegatesResolutionToResolveRunID(t *testing.T) {
	runsDir := t.TempDir()
	writeRunMeta(t, runsDir, "prefix-abc123", runner.RunMeta{RunID: "prefix-abc123"})
	writeRunEvents(t, runsDir, "prefix-abc123")

	// Pass a prefix rather than the full ID.
	run, err := LoadRun(runsDir, "prefix-abc")
	if err != nil {
		t.Fatalf("LoadRun() error = %v", err)
	}
	if run.Meta.RunID != "prefix-abc123" {
		t.Fatalf("Meta.RunID = %q, want %q", run.Meta.RunID, "prefix-abc123")
	}
}

func TestLoadRunPropagatesResolveRunIDErrors(t *testing.T) {
	runsDir := t.TempDir()
	writeRunMeta(t, runsDir, "other-run", runner.RunMeta{RunID: "other-run"})
	writeRunEvents(t, runsDir, "other-run")

	_, err := LoadRun(runsDir, "missing")
	if err == nil {
		t.Fatal("LoadRun() expected ResolveRunID error for unknown prefix, got nil")
	}
	if !strings.Contains(err.Error(), "no run found") {
		t.Fatalf("error = %q, want to contain %q", err.Error(), "no run found")
	}
}

// --- readEvents (exercised via LoadRun) ---

func TestReadEventsMultipleValidEventsParsedCorrectly(t *testing.T) {
	runsDir := t.TempDir()
	writeRunMeta(t, runsDir, "multi-events", runner.RunMeta{RunID: "multi-events"})

	events := []runner.Event{
		{Type: runner.EventSession, ID: "sess-1"},
		{Type: runner.EventTurnEnd},
		{Type: runner.EventAgentEnd},
	}
	var lines strings.Builder
	for _, ev := range events {
		data, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("json.Marshal(event): %v", err)
		}
		lines.Write(data)
		lines.WriteByte('\n')
	}
	writeEventsJSONL(t, runsDir, "multi-events", lines.String())

	run, err := LoadRun(runsDir, "multi-events")
	if err != nil {
		t.Fatalf("LoadRun() error = %v", err)
	}
	if len(run.Events) != 3 {
		t.Fatalf("len(Events) = %d, want 3", len(run.Events))
	}
	if run.Events[0].Type != runner.EventSession {
		t.Fatalf("Events[0].Type = %q, want %q", run.Events[0].Type, runner.EventSession)
	}
	if run.Events[1].Type != runner.EventTurnEnd {
		t.Fatalf("Events[1].Type = %q, want %q", run.Events[1].Type, runner.EventTurnEnd)
	}
	if run.Events[2].Type != runner.EventAgentEnd {
		t.Fatalf("Events[2].Type = %q, want %q", run.Events[2].Type, runner.EventAgentEnd)
	}
}

func TestReadEventsSkipsBlankLines(t *testing.T) {
	runsDir := t.TempDir()
	writeRunMeta(t, runsDir, "blank-lines", runner.RunMeta{RunID: "blank-lines"})

	// Surround a valid event with blank lines.
	content := "\n\n{\"type\":\"turn_end\"}\n\n"
	writeEventsJSONL(t, runsDir, "blank-lines", content)

	run, err := LoadRun(runsDir, "blank-lines")
	if err != nil {
		t.Fatalf("LoadRun() error = %v", err)
	}
	if len(run.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1 (blank lines must be skipped)", len(run.Events))
	}
}

func TestReadEventsSkipsUnparsableJSONLines(t *testing.T) {
	runsDir := t.TempDir()
	writeRunMeta(t, runsDir, "bad-json", runner.RunMeta{RunID: "bad-json"})

	content := "not json at all\n{\"type\":\"turn_end\"}\n{bad}\n"
	writeEventsJSONL(t, runsDir, "bad-json", content)

	run, err := LoadRun(runsDir, "bad-json")
	if err != nil {
		t.Fatalf("LoadRun() error = %v", err)
	}
	if len(run.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1 (invalid JSON lines must be skipped)", len(run.Events))
	}
}

func TestReadEventsMixedValidAndInvalidLines(t *testing.T) {
	runsDir := t.TempDir()
	writeRunMeta(t, runsDir, "mixed-lines", runner.RunMeta{RunID: "mixed-lines"})

	// Interleave valid JSON, blank, invalid JSON, and another valid line.
	lines := fmt.Sprintf(
		"%s\n\nnot-json\n%s\n{oops}\n%s\n",
		`{"type":"session"}`,
		`{"type":"turn_end"}`,
		`{"type":"agent_end"}`,
	)
	writeEventsJSONL(t, runsDir, "mixed-lines", lines)

	run, err := LoadRun(runsDir, "mixed-lines")
	if err != nil {
		t.Fatalf("LoadRun() error = %v", err)
	}
	if len(run.Events) != 3 {
		t.Fatalf("len(Events) = %d, want 3", len(run.Events))
	}

	wantTypes := []runner.EventType{runner.EventSession, runner.EventTurnEnd, runner.EventAgentEnd}
	for i, want := range wantTypes {
		if run.Events[i].Type != want {
			t.Fatalf("Events[%d].Type = %q, want %q", i, run.Events[i].Type, want)
		}
	}
}

func TestReadEventsPreservesEventFields(t *testing.T) {
	runsDir := t.TempDir()
	writeRunMeta(t, runsDir, "field-check", runner.RunMeta{RunID: "field-check"})

	// Write an event with several populated fields.
	line := `{"type":"session","id":"sess-42","timestamp":"2026-03-08T10:00:00Z","cwd":"/home/user"}` + "\n"
	writeEventsJSONL(t, runsDir, "field-check", line)

	run, err := LoadRun(runsDir, "field-check")
	if err != nil {
		t.Fatalf("LoadRun() error = %v", err)
	}
	if len(run.Events) != 1 {
		t.Fatalf("len(Events) = %d, want 1", len(run.Events))
	}
	ev := run.Events[0]
	if ev.ID != "sess-42" {
		t.Fatalf("Events[0].ID = %q, want %q", ev.ID, "sess-42")
	}
	if ev.CWD != "/home/user" {
		t.Fatalf("Events[0].CWD = %q, want %q", ev.CWD, "/home/user")
	}
	if ev.Timestamp != "2026-03-08T10:00:00Z" {
		t.Fatalf("Events[0].Timestamp = %q, want %q", ev.Timestamp, "2026-03-08T10:00:00Z")
	}
}
