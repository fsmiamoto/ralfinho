package viewer

import (
	"encoding/json"
	"os"
	"path/filepath"
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
