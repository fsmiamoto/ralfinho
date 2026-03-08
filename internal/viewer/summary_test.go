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
	writeRunMeta(t, runsDir, "newer-run", runner.RunMeta{
		RunID:               "newer-run",
		StartedAt:           "2026-03-08T11:30:00Z",
		Status:              string(runner.StatusInterrupted),
		Agent:               "kiro",
		PromptSource:        "prompt",
		PromptFile:          "tasks/browser-prompt.md",
		IterationsCompleted: 4,
	})

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
