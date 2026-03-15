package viewer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fsmiamoto/ralfinho/internal/runner"
)

func TestListRunSummariesOrdersNewestFirstAndCachesSearchFields(t *testing.T) {
	runsDir := t.TempDir()

	writeRunMeta(t, runsDir, "older-run", runner.RunMeta{
		RunID:               "older-run",
		StartedAt:           "2026-03-07T10:00:00Z",
		Status:              string(runner.StatusCompleted),
		Agent:               "pi",
		PromptSource:        "plan",
		PlanFile:            "plans/PLAN.md",
		IterationsCompleted: 2,
	})
	writeRunEvents(t, runsDir, "older-run")
	writeEffectivePrompt(t, runsDir, "older-run", "older prompt")

	writeRunMeta(t, runsDir, "newer-run", runner.RunMeta{
		RunID:               "newer-run",
		StartedAt:           "2026-03-08T11:30:00Z",
		Status:              string(runner.StatusInterrupted),
		Agent:               "kiro",
		PromptSource:        "prompt",
		PromptFile:          "tasks/browser-prompt.md",
		IterationsCompleted: 4,
	})
	writeRunEvents(t, runsDir, "newer-run")
	writeEffectivePrompt(t, runsDir, "newer-run", "newer prompt")

	summaries, err := ListRunSummaries(runsDir)
	if err != nil {
		t.Fatalf("ListRunSummaries() error = %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("len(summaries) = %d, want 2", len(summaries))
	}

	if summaries[0].RunID != "newer-run" {
		t.Fatalf("summaries[0].RunID = %q, want %q", summaries[0].RunID, "newer-run")
	}
	if summaries[1].RunID != "older-run" {
		t.Fatalf("summaries[1].RunID = %q, want %q", summaries[1].RunID, "older-run")
	}

	newer := summaries[0]
	if !newer.HasMeta {
		t.Fatal("newer summary should have parsed meta")
	}
	if newer.StartedAt.IsZero() {
		t.Fatal("newer summary should have parsed StartedAt")
	}
	if !newer.SortTime.Equal(newer.StartedAt) {
		t.Fatalf("SortTime = %v, want parsed StartedAt %v", newer.SortTime, newer.StartedAt)
	}
	if newer.PromptLabel != "browser-prompt.md" {
		t.Fatalf("PromptLabel = %q, want %q", newer.PromptLabel, "browser-prompt.md")
	}
	if newer.ArtifactError != "" {
		t.Fatalf("ArtifactError = %q, want empty", newer.ArtifactError)
	}
	if !newer.Actions.Open.Available {
		t.Fatalf("Open action = %#v, want available", newer.Actions.Open)
	}
	if !newer.Actions.Resume.Available || newer.Actions.Resume.Source != ResumeSourceEffectivePrompt {
		t.Fatalf("Resume action = %#v, want effective-prompt resume", newer.Actions.Resume)
	}
	if !newer.Actions.Delete.Available {
		t.Fatalf("Delete action = %#v, want available", newer.Actions.Delete)
	}

	for _, want := range []string{"newer-run", "kiro", "interrupted", "prompt", "browser-prompt.md", "2026-03-08 11:30"} {
		if !strings.Contains(newer.SearchText, strings.ToLower(want)) {
			t.Fatalf("SearchText = %q, want substring %q", newer.SearchText, strings.ToLower(want))
		}
	}
	if !newer.Matches("BROWSER-PROMPT") {
		t.Fatal("Matches should be case-insensitive")
	}
	if newer.Matches("does-not-exist") {
		t.Fatal("Matches should reject unrelated queries")
	}
}

func TestListRunSummariesKeepsRunsWithMissingOrCorruptMeta(t *testing.T) {
	runsDir := t.TempDir()

	writeRunMeta(t, runsDir, "valid-run", runner.RunMeta{
		RunID:               "valid-run",
		StartedAt:           "2026-03-08T10:00:00Z",
		Status:              string(runner.StatusCompleted),
		Agent:               "pi",
		PromptSource:        "default",
		IterationsCompleted: 1,
	})
	writeRunEvents(t, runsDir, "valid-run")

	missingDir := filepath.Join(runsDir, "missing-meta")
	if err := os.MkdirAll(missingDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", missingDir, err)
	}

	corruptDir := filepath.Join(runsDir, "corrupt-meta")
	if err := os.MkdirAll(corruptDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", corruptDir, err)
	}
	if err := os.WriteFile(filepath.Join(corruptDir, "meta.json"), []byte("{not json\n"), 0644); err != nil {
		t.Fatalf("WriteFile(corrupt meta): %v", err)
	}
	writeEffectivePrompt(t, runsDir, "corrupt-meta", "recoverable prompt")

	summaries, err := ListRunSummaries(runsDir)
	if err != nil {
		t.Fatalf("ListRunSummaries() error = %v", err)
	}
	if len(summaries) != 3 {
		t.Fatalf("len(summaries) = %d, want 3", len(summaries))
	}

	byID := make(map[string]RunSummary, len(summaries))
	for _, summary := range summaries {
		byID[summary.RunID] = summary
	}

	missing := byID["missing-meta"]
	if missing.HasMeta {
		t.Fatal("missing-meta should not have parsed meta")
	}
	if missing.ArtifactError != "meta.json missing" {
		t.Fatalf("missing-meta ArtifactError = %q, want %q", missing.ArtifactError, "meta.json missing")
	}
	if missing.Actions.Open.Available {
		t.Fatalf("missing-meta Open action = %#v, want unavailable", missing.Actions.Open)
	}
	if missing.Actions.Resume.Available {
		t.Fatalf("missing-meta Resume action = %#v, want unavailable", missing.Actions.Resume)
	}
	if !missing.Actions.Delete.Available {
		t.Fatalf("missing-meta Delete action = %#v, want available", missing.Actions.Delete)
	}
	if !missing.Matches("missing") {
		t.Fatalf("missing-meta SearchText = %q, expected query to match", missing.SearchText)
	}

	corrupt := byID["corrupt-meta"]
	if corrupt.HasMeta {
		t.Fatal("corrupt-meta should not have parsed meta")
	}
	if !strings.Contains(corrupt.ArtifactError, "parsing meta.json") {
		t.Fatalf("corrupt-meta ArtifactError = %q, want parsing error", corrupt.ArtifactError)
	}
	if corrupt.Actions.Open.Available {
		t.Fatalf("corrupt-meta Open action = %#v, want unavailable", corrupt.Actions.Open)
	}
	if !corrupt.Actions.Resume.Available || corrupt.Actions.Resume.Source != ResumeSourceEffectivePrompt {
		t.Fatalf("corrupt-meta Resume action = %#v, want effective-prompt fallback", corrupt.Actions.Resume)
	}
	if !corrupt.Matches("parsing meta.json") {
		t.Fatalf("corrupt-meta SearchText = %q, expected parsing error to be searchable", corrupt.SearchText)
	}

	valid := byID["valid-run"]
	if !valid.HasMeta {
		t.Fatal("valid-run should have parsed meta")
	}
	if valid.ArtifactError != "" {
		t.Fatalf("valid-run ArtifactError = %q, want empty", valid.ArtifactError)
	}
	if !valid.Actions.Resume.Available || valid.Actions.Resume.Source != ResumeSourceDefault {
		t.Fatalf("valid-run Resume action = %#v, want default-source fallback", valid.Actions.Resume)
	}
}

func TestListRunSummariesDefinesActionEligibilityFromArtifacts(t *testing.T) {
	runsDir := t.TempDir()

	writeRunMeta(t, runsDir, "complete-run", runner.RunMeta{
		RunID:        "complete-run",
		StartedAt:    "2026-03-08T09:00:00Z",
		Status:       string(runner.StatusCompleted),
		Agent:        "pi",
		PromptSource: "prompt",
		PromptFile:   "tasks/original.md",
	})
	writeRunEvents(t, runsDir, "complete-run")
	writeEffectivePrompt(t, runsDir, "complete-run", "saved prompt")

	writeRunMeta(t, runsDir, "missing-events", runner.RunMeta{
		RunID:        "missing-events",
		StartedAt:    "2026-03-08T08:00:00Z",
		Status:       string(runner.StatusFailed),
		Agent:        "pi",
		PromptSource: "plan",
		PlanFile:     "plans/PLAN.md",
	})
	writeEffectivePrompt(t, runsDir, "missing-events", "plan prompt")

	writeRunMeta(t, runsDir, "meta-fallback", runner.RunMeta{
		RunID:        "meta-fallback",
		StartedAt:    "2026-03-08T07:00:00Z",
		Status:       string(runner.StatusInterrupted),
		Agent:        "kiro",
		PromptSource: "plan",
		PlanFile:     "plans/resume.md",
	})
	writeRunEvents(t, runsDir, "meta-fallback")

	writeRunMeta(t, runsDir, "no-resume", runner.RunMeta{
		RunID:        "no-resume",
		StartedAt:    "2026-03-08T06:00:00Z",
		Status:       string(runner.StatusCompleted),
		Agent:        "pi",
		PromptSource: "unknown",
	})
	writeRunEvents(t, runsDir, "no-resume")
	artifactDir := filepath.Join(runsDir, "no-resume", "effective-prompt.md")
	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", artifactDir, err)
	}

	promptOnlyDir := filepath.Join(runsDir, "prompt-only")
	if err := os.MkdirAll(promptOnlyDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", promptOnlyDir, err)
	}
	writeEffectivePrompt(t, runsDir, "prompt-only", "resume from prompt only")

	summaries, err := ListRunSummaries(runsDir)
	if err != nil {
		t.Fatalf("ListRunSummaries() error = %v", err)
	}

	byID := make(map[string]RunSummary, len(summaries))
	for _, summary := range summaries {
		byID[summary.RunID] = summary
	}

	complete := byID["complete-run"]
	if !complete.HasEvents || complete.EventsError != "" {
		t.Fatalf("complete-run events = (%v, %q), want ready", complete.HasEvents, complete.EventsError)
	}
	if !complete.HasEffectivePrompt || complete.EffectivePromptError != "" {
		t.Fatalf("complete-run prompt = (%v, %q), want ready", complete.HasEffectivePrompt, complete.EffectivePromptError)
	}
	if !complete.Actions.Open.Available {
		t.Fatalf("complete-run Open action = %#v, want available", complete.Actions.Open)
	}
	if !complete.Actions.Resume.Available || complete.Actions.Resume.Source != ResumeSourceEffectivePrompt {
		t.Fatalf("complete-run Resume action = %#v, want effective-prompt source", complete.Actions.Resume)
	}
	if complete.Actions.Resume.Path != filepath.Join(runsDir, "complete-run", "effective-prompt.md") {
		t.Fatalf("complete-run Resume path = %q, want effective-prompt path", complete.Actions.Resume.Path)
	}
	if !complete.Actions.Delete.Available {
		t.Fatalf("complete-run Delete action = %#v, want available", complete.Actions.Delete)
	}

	missingEvents := byID["missing-events"]
	if missingEvents.HasEvents {
		t.Fatal("missing-events should not have events.jsonl")
	}
	if missingEvents.EventsError != "events.jsonl missing" {
		t.Fatalf("missing-events EventsError = %q, want %q", missingEvents.EventsError, "events.jsonl missing")
	}
	if missingEvents.Actions.Open.Available {
		t.Fatalf("missing-events Open action = %#v, want unavailable", missingEvents.Actions.Open)
	}
	if missingEvents.Actions.Open.DisabledReason != "events.jsonl missing" {
		t.Fatalf("missing-events Open disabled reason = %q, want %q", missingEvents.Actions.Open.DisabledReason, "events.jsonl missing")
	}
	if !missingEvents.Actions.Resume.Available || missingEvents.Actions.Resume.Source != ResumeSourceEffectivePrompt {
		t.Fatalf("missing-events Resume action = %#v, want effective-prompt source", missingEvents.Actions.Resume)
	}

	metaFallback := byID["meta-fallback"]
	if metaFallback.HasEffectivePrompt {
		t.Fatal("meta-fallback should not have effective-prompt.md")
	}
	if !metaFallback.Actions.Open.Available {
		t.Fatalf("meta-fallback Open action = %#v, want available", metaFallback.Actions.Open)
	}
	if !metaFallback.Actions.Resume.Available || metaFallback.Actions.Resume.Source != ResumeSourcePlanFile {
		t.Fatalf("meta-fallback Resume action = %#v, want plan-file source", metaFallback.Actions.Resume)
	}
	if metaFallback.Actions.Resume.Path != "plans/resume.md" {
		t.Fatalf("meta-fallback Resume path = %q, want %q", metaFallback.Actions.Resume.Path, "plans/resume.md")
	}

	noResume := byID["no-resume"]
	if noResume.EffectivePromptError != "effective-prompt.md is a directory" {
		t.Fatalf("no-resume EffectivePromptError = %q, want directory error", noResume.EffectivePromptError)
	}
	if noResume.Actions.Resume.Available {
		t.Fatalf("no-resume Resume action = %#v, want unavailable", noResume.Actions.Resume)
	}
	if !strings.Contains(noResume.Actions.Resume.DisabledReason, "reusable prompt source") {
		t.Fatalf("no-resume Resume disabled reason = %q, want reusable prompt source explanation", noResume.Actions.Resume.DisabledReason)
	}
	if !noResume.Matches("effective-prompt.md is a directory") {
		t.Fatalf("no-resume SearchText = %q, expected prompt artifact error to be searchable", noResume.SearchText)
	}

	promptOnly := byID["prompt-only"]
	if promptOnly.Actions.Open.Available {
		t.Fatalf("prompt-only Open action = %#v, want unavailable without meta", promptOnly.Actions.Open)
	}
	if promptOnly.Actions.Open.DisabledReason != "meta.json missing" {
		t.Fatalf("prompt-only Open disabled reason = %q, want %q", promptOnly.Actions.Open.DisabledReason, "meta.json missing")
	}
	if !promptOnly.Actions.Resume.Available || promptOnly.Actions.Resume.Source != ResumeSourceEffectivePrompt {
		t.Fatalf("prompt-only Resume action = %#v, want effective-prompt source", promptOnly.Actions.Resume)
	}
	if !promptOnly.Actions.Delete.Available {
		t.Fatalf("prompt-only Delete action = %#v, want available", promptOnly.Actions.Delete)
	}
}

func TestListRunSummariesMissingRunsDir(t *testing.T) {
	runsDir := filepath.Join(t.TempDir(), "missing-runs")

	summaries, err := ListRunSummaries(runsDir)
	if err != nil {
		t.Fatalf("ListRunSummaries() error = %v", err)
	}
	if summaries != nil {
		t.Fatalf("summaries = %#v, want nil", summaries)
	}
}

func writeRunMeta(t *testing.T, runsDir, runID string, meta runner.RunMeta) {
	t.Helper()

	dir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}

	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("json.Marshal(meta): %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), append(data, '\n'), 0644); err != nil {
		t.Fatalf("WriteFile(meta.json): %v", err)
	}

	if meta.StartedAt != "" {
		if ts, err := time.Parse(time.RFC3339, meta.StartedAt); err == nil {
			if err := os.Chtimes(dir, ts, ts); err != nil {
				t.Fatalf("Chtimes(%q): %v", dir, err)
			}
		}
	}
}

func writeRunEvents(t *testing.T, runsDir, runID string) {
	t.Helper()

	dir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte("{\"type\":\"turn_end\"}\n"), 0644); err != nil {
		t.Fatalf("WriteFile(events.jsonl): %v", err)
	}
}

func writeEffectivePrompt(t *testing.T, runsDir, runID, prompt string) {
	t.Helper()

	dir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "effective-prompt.md"), []byte(prompt), 0644); err != nil {
		t.Fatalf("WriteFile(effective-prompt.md): %v", err)
	}
}

func TestListRunSummariesSortTimeFallsBackToModtime(t *testing.T) {
	runsDir := t.TempDir()

	// Run with valid started_at.
	writeRunMeta(t, runsDir, "has-started", runner.RunMeta{
		RunID:        "has-started",
		StartedAt:    "2026-03-08T12:00:00Z",
		Status:       string(runner.StatusCompleted),
		Agent:        "pi",
		PromptSource: "default",
	})
	writeRunEvents(t, runsDir, "has-started")

	// Run without started_at – SortTime should fall back to modtime.
	writeRunMeta(t, runsDir, "no-started", runner.RunMeta{
		RunID:        "no-started",
		StartedAt:    "",
		Status:       string(runner.StatusCompleted),
		Agent:        "pi",
		PromptSource: "default",
	})
	writeRunEvents(t, runsDir, "no-started")

	// Set the modtime of no-started to a time AFTER the started_at of has-started.
	laterTime := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	noStartedDir := filepath.Join(runsDir, "no-started")
	if err := os.Chtimes(noStartedDir, laterTime, laterTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	summaries, err := ListRunSummaries(runsDir)
	if err != nil {
		t.Fatalf("ListRunSummaries() error = %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("len(summaries) = %d, want 2", len(summaries))
	}

	// no-started has later modtime, so it should come first.
	if summaries[0].RunID != "no-started" {
		t.Fatalf("summaries[0].RunID = %q, want %q", summaries[0].RunID, "no-started")
	}
	if summaries[1].RunID != "has-started" {
		t.Fatalf("summaries[1].RunID = %q, want %q", summaries[1].RunID, "has-started")
	}

	// Verify fallback run has non-zero SortTime (from modtime).
	if summaries[0].SortTime.IsZero() {
		t.Fatal("no-started SortTime should not be zero (should use modtime)")
	}
	if summaries[0].StartedAt.IsZero() == false {
		t.Fatal("no-started StartedAt should be zero (no started_at in meta)")
	}
}

func TestListRunSummariesTieBreaksOnRunID(t *testing.T) {
	runsDir := t.TempDir()

	sameTime := "2026-03-08T10:00:00Z"

	writeRunMeta(t, runsDir, "run-aaa", runner.RunMeta{
		RunID:        "run-aaa",
		StartedAt:    sameTime,
		Status:       string(runner.StatusCompleted),
		Agent:        "pi",
		PromptSource: "default",
	})
	writeRunEvents(t, runsDir, "run-aaa")

	writeRunMeta(t, runsDir, "run-zzz", runner.RunMeta{
		RunID:        "run-zzz",
		StartedAt:    sameTime,
		Status:       string(runner.StatusCompleted),
		Agent:        "pi",
		PromptSource: "default",
	})
	writeRunEvents(t, runsDir, "run-zzz")

	summaries, err := ListRunSummaries(runsDir)
	if err != nil {
		t.Fatalf("ListRunSummaries() error = %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("len(summaries) = %d, want 2", len(summaries))
	}

	// Same SortTime → tie-break by RunID descending (zzz before aaa).
	if summaries[0].RunID != "run-zzz" {
		t.Fatalf("summaries[0].RunID = %q, want %q", summaries[0].RunID, "run-zzz")
	}
	if summaries[1].RunID != "run-aaa" {
		t.Fatalf("summaries[1].RunID = %q, want %q", summaries[1].RunID, "run-aaa")
	}
}

func TestListRunSummariesSkipsNonDirectoryEntries(t *testing.T) {
	runsDir := t.TempDir()

	// Create a regular file in the runs directory.
	if err := os.WriteFile(filepath.Join(runsDir, "stray-file.txt"), []byte("not a run"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Create a valid run directory.
	writeRunMeta(t, runsDir, "valid-run", runner.RunMeta{
		RunID:        "valid-run",
		StartedAt:    "2026-03-08T10:00:00Z",
		Status:       string(runner.StatusCompleted),
		Agent:        "pi",
		PromptSource: "default",
	})
	writeRunEvents(t, runsDir, "valid-run")

	summaries, err := ListRunSummaries(runsDir)
	if err != nil {
		t.Fatalf("ListRunSummaries() error = %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("len(summaries) = %d, want 1", len(summaries))
	}
	if summaries[0].RunID != "valid-run" {
		t.Fatalf("summaries[0].RunID = %q, want %q", summaries[0].RunID, "valid-run")
	}
}

func TestRunSummaryAgentNormalization(t *testing.T) {
	runsDir := t.TempDir()

	// Empty agent → "pi".
	writeRunMeta(t, runsDir, "empty-agent", runner.RunMeta{
		RunID:        "empty-agent",
		StartedAt:    "2026-03-08T10:00:00Z",
		Status:       string(runner.StatusCompleted),
		Agent:        "",
		PromptSource: "default",
	})
	writeRunEvents(t, runsDir, "empty-agent")

	// Whitespace-only agent → "pi".
	writeRunMeta(t, runsDir, "space-agent", runner.RunMeta{
		RunID:        "space-agent",
		StartedAt:    "2026-03-08T09:00:00Z",
		Status:       string(runner.StatusCompleted),
		Agent:        "   ",
		PromptSource: "default",
	})
	writeRunEvents(t, runsDir, "space-agent")

	summaries, err := ListRunSummaries(runsDir)
	if err != nil {
		t.Fatalf("ListRunSummaries() error = %v", err)
	}

	byID := make(map[string]RunSummary, len(summaries))
	for _, s := range summaries {
		byID[s.RunID] = s
	}

	if got := byID["empty-agent"].Agent; got != "pi" {
		t.Fatalf("empty-agent Agent = %q, want %q", got, "pi")
	}
	if got := byID["space-agent"].Agent; got != "pi" {
		t.Fatalf("space-agent Agent = %q, want %q", got, "pi")
	}
}

func TestRunSummaryPromptLabelDerivation(t *testing.T) {
	runsDir := t.TempDir()

	cases := []struct {
		runID     string
		meta      runner.RunMeta
		wantLabel string
	}{
		{
			runID: "prompt-file",
			meta: runner.RunMeta{
				RunID:        "prompt-file",
				StartedAt:    "2026-03-08T10:00:00Z",
				Status:       string(runner.StatusCompleted),
				Agent:        "pi",
				PromptSource: "prompt",
				PromptFile:   "tasks/my-task.md",
			},
			wantLabel: "my-task.md",
		},
		{
			runID: "plan-file",
			meta: runner.RunMeta{
				RunID:        "plan-file",
				StartedAt:    "2026-03-08T09:00:00Z",
				Status:       string(runner.StatusCompleted),
				Agent:        "pi",
				PromptSource: "plan",
				PlanFile:     "plans/my-plan.md",
			},
			wantLabel: "my-plan.md",
		},
		{
			runID: "default-source",
			meta: runner.RunMeta{
				RunID:        "default-source",
				StartedAt:    "2026-03-08T08:00:00Z",
				Status:       string(runner.StatusCompleted),
				Agent:        "pi",
				PromptSource: "default",
			},
			wantLabel: "default",
		},
		{
			runID: "empty-source",
			meta: runner.RunMeta{
				RunID:        "empty-source",
				StartedAt:    "2026-03-08T07:00:00Z",
				Status:       string(runner.StatusCompleted),
				Agent:        "pi",
				PromptSource: "",
			},
			wantLabel: "unknown",
		},
	}

	for _, tc := range cases {
		writeRunMeta(t, runsDir, tc.runID, tc.meta)
		writeRunEvents(t, runsDir, tc.runID)
	}

	summaries, err := ListRunSummaries(runsDir)
	if err != nil {
		t.Fatalf("ListRunSummaries() error = %v", err)
	}

	byID := make(map[string]RunSummary, len(summaries))
	for _, s := range summaries {
		byID[s.RunID] = s
	}

	for _, tc := range cases {
		got := byID[tc.runID].PromptLabel
		if got != tc.wantLabel {
			t.Fatalf("%s: PromptLabel = %q, want %q", tc.runID, got, tc.wantLabel)
		}
	}
}

func TestRunSummaryTimeParsing(t *testing.T) {
	runsDir := t.TempDir()

	cases := []struct {
		runID     string
		startedAt string
		wantValid bool
		wantZero  bool
	}{
		{"rfc3339", "2026-03-08T10:00:00Z", true, false},
		{"rfc3339nano", "2026-03-08T10:00:00.123456789Z", true, false},
		{"empty", "", false, true},
		{"invalid", "not-a-date", false, true},
	}

	for _, tc := range cases {
		writeRunMeta(t, runsDir, tc.runID, runner.RunMeta{
			RunID:        tc.runID,
			StartedAt:    tc.startedAt,
			Status:       string(runner.StatusCompleted),
			Agent:        "pi",
			PromptSource: "default",
		})
		writeRunEvents(t, runsDir, tc.runID)
	}

	summaries, err := ListRunSummaries(runsDir)
	if err != nil {
		t.Fatalf("ListRunSummaries() error = %v", err)
	}

	byID := make(map[string]RunSummary, len(summaries))
	for _, s := range summaries {
		byID[s.RunID] = s
	}

	for _, tc := range cases {
		s := byID[tc.runID]
		if tc.wantZero && !s.StartedAt.IsZero() {
			t.Fatalf("%s: StartedAt = %v, want zero", tc.runID, s.StartedAt)
		}
		if tc.wantValid && s.StartedAt.IsZero() {
			t.Fatalf("%s: StartedAt is zero, want parsed time", tc.runID)
		}
		if s.StartedAtText != tc.startedAt {
			t.Fatalf("%s: StartedAtText = %q, want %q", tc.runID, s.StartedAtText, tc.startedAt)
		}
	}
}

func TestRunSummaryMatchesEmptyQuery(t *testing.T) {
	s := RunSummary{RunID: "test-run", SearchText: "test-run\npi\ncompleted"}

	if !s.Matches("") {
		t.Fatal("Matches(\"\") should return true")
	}
	if !s.Matches("   ") {
		t.Fatal("Matches(\"   \") should return true")
	}
}

func TestRunSummaryEventsAsDirectory(t *testing.T) {
	runsDir := t.TempDir()

	writeRunMeta(t, runsDir, "events-dir", runner.RunMeta{
		RunID:        "events-dir",
		StartedAt:    "2026-03-08T10:00:00Z",
		Status:       string(runner.StatusCompleted),
		Agent:        "pi",
		PromptSource: "default",
	})

	// Create events.jsonl as a directory instead of a file.
	eventsDir := filepath.Join(runsDir, "events-dir", "events.jsonl")
	if err := os.MkdirAll(eventsDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", eventsDir, err)
	}

	summaries, err := ListRunSummaries(runsDir)
	if err != nil {
		t.Fatalf("ListRunSummaries() error = %v", err)
	}

	byID := make(map[string]RunSummary, len(summaries))
	for _, s := range summaries {
		byID[s.RunID] = s
	}

	s := byID["events-dir"]
	if s.HasEvents {
		t.Fatal("HasEvents should be false when events.jsonl is a directory")
	}
	if !strings.Contains(s.EventsError, "is a directory") {
		t.Fatalf("EventsError = %q, want to contain %q", s.EventsError, "is a directory")
	}
	if s.Actions.Open.Available {
		t.Fatalf("Open action should be unavailable when events.jsonl is a directory")
	}
}

func TestRunSummaryResumeSourceFromMetaEdgeCases(t *testing.T) {
	runsDir := t.TempDir()

	cases := []struct {
		runID      string
		meta       runner.RunMeta
		wantAvail  bool
		wantSource ResumeSource
		wantPath   string
	}{
		{
			runID: "prompt-source",
			meta: runner.RunMeta{
				RunID:        "prompt-source",
				StartedAt:    "2026-03-08T10:00:00Z",
				Status:       string(runner.StatusCompleted),
				Agent:        "pi",
				PromptSource: "prompt",
				PromptFile:   "tasks/p.md",
			},
			wantAvail:  true,
			wantSource: ResumeSourcePromptFile,
			wantPath:   "tasks/p.md",
		},
		{
			runID: "plan-source",
			meta: runner.RunMeta{
				RunID:        "plan-source",
				StartedAt:    "2026-03-08T09:00:00Z",
				Status:       string(runner.StatusCompleted),
				Agent:        "pi",
				PromptSource: "plan",
				PlanFile:     "plans/pl.md",
			},
			wantAvail:  true,
			wantSource: ResumeSourcePlanFile,
			wantPath:   "plans/pl.md",
		},
		{
			runID: "default-source",
			meta: runner.RunMeta{
				RunID:        "default-source",
				StartedAt:    "2026-03-08T08:00:00Z",
				Status:       string(runner.StatusCompleted),
				Agent:        "pi",
				PromptSource: "default",
			},
			wantAvail:  true,
			wantSource: ResumeSourceDefault,
			wantPath:   "",
		},
		{
			runID: "fallback-prompt",
			meta: runner.RunMeta{
				RunID:        "fallback-prompt",
				StartedAt:    "2026-03-08T07:00:00Z",
				Status:       string(runner.StatusCompleted),
				Agent:        "pi",
				PromptSource: "",
				PromptFile:   "tasks/fallback.md",
			},
			wantAvail:  true,
			wantSource: ResumeSourcePromptFile,
			wantPath:   "tasks/fallback.md",
		},
		{
			runID: "fallback-plan",
			meta: runner.RunMeta{
				RunID:        "fallback-plan",
				StartedAt:    "2026-03-08T06:00:00Z",
				Status:       string(runner.StatusCompleted),
				Agent:        "pi",
				PromptSource: "",
				PlanFile:     "plans/fallback.md",
			},
			wantAvail:  true,
			wantSource: ResumeSourcePlanFile,
			wantPath:   "plans/fallback.md",
		},
		{
			runID: "no-resume",
			meta: runner.RunMeta{
				RunID:        "no-resume",
				StartedAt:    "2026-03-08T05:00:00Z",
				Status:       string(runner.StatusCompleted),
				Agent:        "pi",
				PromptSource: "unknown",
			},
			wantAvail:  false,
			wantSource: ResumeSourceNone,
			wantPath:   "",
		},
	}

	for _, tc := range cases {
		writeRunMeta(t, runsDir, tc.runID, tc.meta)
		writeRunEvents(t, runsDir, tc.runID)
		// Do NOT write effective-prompt.md so the meta fallback path is exercised.
	}

	summaries, err := ListRunSummaries(runsDir)
	if err != nil {
		t.Fatalf("ListRunSummaries() error = %v", err)
	}

	byID := make(map[string]RunSummary, len(summaries))
	for _, s := range summaries {
		byID[s.RunID] = s
	}

	for _, tc := range cases {
		s := byID[tc.runID]
		if s.Actions.Resume.Available != tc.wantAvail {
			t.Fatalf("%s: Resume.Available = %v, want %v (action = %#v)", tc.runID, s.Actions.Resume.Available, tc.wantAvail, s.Actions.Resume)
		}
		if s.Actions.Resume.Source != tc.wantSource {
			t.Fatalf("%s: Resume.Source = %q, want %q", tc.runID, s.Actions.Resume.Source, tc.wantSource)
		}
		if s.Actions.Resume.Path != tc.wantPath {
			t.Fatalf("%s: Resume.Path = %q, want %q", tc.runID, s.Actions.Resume.Path, tc.wantPath)
		}
	}
}

func TestListRunSummariesEmptyRunsDir(t *testing.T) {
	runsDir := t.TempDir()

	summaries, err := ListRunSummaries(runsDir)
	if err != nil {
		t.Fatalf("ListRunSummaries() error = %v", err)
	}
	if summaries == nil {
		t.Fatal("summaries should be non-nil empty slice, got nil")
	}
	if len(summaries) != 0 {
		t.Fatalf("len(summaries) = %d, want 0", len(summaries))
	}
}

func TestListRunSummariesRunsDirReadError(t *testing.T) {
	runsPath := filepath.Join(t.TempDir(), "runs-file")
	if err := os.WriteFile(runsPath, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runsPath, err)
	}

	summaries, err := ListRunSummaries(runsPath)
	if err == nil {
		t.Fatal("ListRunSummaries() expected error for file runsDir, got nil")
	}
	if summaries != nil {
		t.Fatalf("summaries = %#v, want nil on read error", summaries)
	}
	if !strings.Contains(err.Error(), "reading runs directory") {
		t.Fatalf("error = %q, want to contain %q", err.Error(), "reading runs directory")
	}
}

func TestListRunSummariesKeepsMetaReadErrorsOnSummary(t *testing.T) {
	runsDir := t.TempDir()
	writeRunEvents(t, runsDir, "meta-dir")
	writeEffectivePrompt(t, runsDir, "meta-dir", "recover from saved prompt")

	metaDir := filepath.Join(runsDir, "meta-dir", "meta.json")
	if err := os.MkdirAll(metaDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", metaDir, err)
	}

	summaries, err := ListRunSummaries(runsDir)
	if err != nil {
		t.Fatalf("ListRunSummaries() error = %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("len(summaries) = %d, want 1", len(summaries))
	}

	summary := summaries[0]
	if summary.HasMeta {
		t.Fatal("summary should not mark a directory meta.json as parsed meta")
	}
	if !strings.Contains(summary.ArtifactError, "reading meta.json") {
		t.Fatalf("ArtifactError = %q, want reading meta.json error", summary.ArtifactError)
	}
	if summary.Actions.Open.Available {
		t.Fatalf("Open action = %#v, want unavailable when meta.json is unreadable", summary.Actions.Open)
	}
	if summary.Actions.Open.DisabledReason != summary.ArtifactError {
		t.Fatalf("Open disabled reason = %q, want ArtifactError %q", summary.Actions.Open.DisabledReason, summary.ArtifactError)
	}
	if !summary.Actions.Resume.Available || summary.Actions.Resume.Source != ResumeSourceEffectivePrompt {
		t.Fatalf("Resume action = %#v, want effective-prompt fallback", summary.Actions.Resume)
	}
	if !summary.Matches("reading meta.json") {
		t.Fatalf("SearchText = %q, expected reading error to be searchable", summary.SearchText)
	}
}

func TestBuildRunActionsUsesGenericFallbackReasonsForSparseSummaries(t *testing.T) {
	t.Run("missing meta and dir", func(t *testing.T) {
		actions := buildRunActions(RunSummary{})
		if actions.Open.Available {
			t.Fatalf("Open action = %#v, want unavailable", actions.Open)
		}
		if actions.Open.DisabledReason != "meta.json unavailable" {
			t.Fatalf("Open disabled reason = %q, want %q", actions.Open.DisabledReason, "meta.json unavailable")
		}
		if actions.Delete.Available {
			t.Fatalf("Delete action = %#v, want unavailable", actions.Delete)
		}
		if actions.Delete.DisabledReason != "run directory unavailable" {
			t.Fatalf("Delete disabled reason = %q, want %q", actions.Delete.DisabledReason, "run directory unavailable")
		}
		if actions.Resume.Available {
			t.Fatalf("Resume action = %#v, want unavailable", actions.Resume)
		}
		if actions.Resume.DisabledReason != "meta.json unavailable" {
			t.Fatalf("Resume disabled reason = %q, want %q", actions.Resume.DisabledReason, "meta.json unavailable")
		}
	})

	t.Run("missing events without artifact error", func(t *testing.T) {
		actions := buildRunActions(RunSummary{HasMeta: true, Dir: t.TempDir()})
		if actions.Open.Available {
			t.Fatalf("Open action = %#v, want unavailable", actions.Open)
		}
		if actions.Open.DisabledReason != "events.jsonl unavailable" {
			t.Fatalf("Open disabled reason = %q, want %q", actions.Open.DisabledReason, "events.jsonl unavailable")
		}
		if !actions.Delete.Available {
			t.Fatalf("Delete action = %#v, want available", actions.Delete)
		}
		if actions.Resume.Available {
			t.Fatalf("Resume action = %#v, want unavailable without prompt metadata", actions.Resume)
		}
		if actions.Resume.DisabledReason != "meta.json does not describe a reusable prompt source" {
			t.Fatalf("Resume disabled reason = %q, want reusable prompt source explanation", actions.Resume.DisabledReason)
		}
	})
}

func TestInspectRunArtifactReadErrors(t *testing.T) {
	t.Run("stat error through non-directory parent", func(t *testing.T) {
		parentFile := filepath.Join(t.TempDir(), "parent-file")
		if err := os.WriteFile(parentFile, []byte("not a directory"), 0644); err != nil {
			t.Fatalf("WriteFile(%q): %v", parentFile, err)
		}

		ok, msg := inspectRunArtifact(filepath.Join(parentFile, "events.jsonl"))
		if ok {
			t.Fatal("inspectRunArtifact() = ok for path inside regular file parent, want false")
		}
		if !strings.Contains(msg, "reading events.jsonl:") {
			t.Fatalf("inspectRunArtifact() message = %q, want reading error", msg)
		}
	})

	t.Run("open error on unreadable file", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("chmod-based permission checks are not reliable on Windows")
		}

		path := filepath.Join(t.TempDir(), "events.jsonl")
		if err := os.WriteFile(path, []byte("{}\n"), 0644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
		if err := os.Chmod(path, 0); err != nil {
			t.Fatalf("Chmod(%q, 0): %v", path, err)
		}
		defer os.Chmod(path, 0600)

		if f, err := os.Open(path); err == nil {
			f.Close()
			t.Skip("current user can still open chmod 000 files; skipping open-error path")
		}

		ok, msg := inspectRunArtifact(path)
		if ok {
			t.Fatal("inspectRunArtifact() = ok for unreadable file, want false")
		}
		if !strings.Contains(msg, "reading events.jsonl:") {
			t.Fatalf("inspectRunArtifact() message = %q, want reading error", msg)
		}
	})
}
