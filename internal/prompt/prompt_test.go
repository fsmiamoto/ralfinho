package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildFromPlan(t *testing.T) {
	// Create a temp plan file.
	dir := t.TempDir()
	planPath := filepath.Join(dir, "PLAN.md")
	planContent := "# My Plan\n- task 1\n- task 2\n"
	if err := os.WriteFile(planPath, []byte(planContent), 0644); err != nil {
		t.Fatal(err)
	}

	notesPath := filepath.Join(dir, "run-abc", "NOTES.md")
	progressPath := filepath.Join(dir, "run-abc", "PROGRESS.md")

	got, err := BuildFromPlan(planPath, "", notesPath, progressPath)
	if err != nil {
		t.Fatalf("BuildFromPlan() error: %v", err)
	}

	// Must contain the plan content.
	if !strings.Contains(got, "task 1") {
		t.Error("output missing plan content 'task 1'")
	}
	if !strings.Contains(got, "task 2") {
		t.Error("output missing plan content 'task 2'")
	}

	// Must contain the plan path reference.
	if !strings.Contains(got, planPath) {
		t.Errorf("output missing plan path %q", planPath)
	}

	// Must contain the COMPLETE marker instruction.
	if !strings.Contains(got, "<promise>COMPLETE</promise>") {
		t.Error("output missing COMPLETE marker instruction")
	}

	// Must contain the task-loop framing.
	if !strings.Contains(got, "task loop") {
		t.Error("output missing 'task loop' framing")
	}
	if !strings.Contains(got, "fresh context") {
		t.Error("output missing 'fresh context' framing")
	}

	// Must reference the provided memory file paths (not hardcoded names).
	if !strings.Contains(got, notesPath) {
		t.Errorf("output missing notes path %q", notesPath)
	}
	if !strings.Contains(got, progressPath) {
		t.Errorf("output missing progress path %q", progressPath)
	}

	// Must instruct to do only one task.
	if !strings.Contains(got, "one task") {
		t.Error("output missing 'one task' instruction")
	}

	// Must instruct to git commit.
	if !strings.Contains(got, "Git commit") {
		t.Error("output missing git commit instruction")
	}
}

func TestBuildFromPlan_CustomTemplate(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "PLAN.md")
	if err := os.WriteFile(planPath, []byte("- custom task\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := BuildFromPlan(planPath, "Plan={{.PlanPath}}\n{{.PlanContent}}\nNotes={{.NotesPath}}", "/run/NOTES.md", "/run/PROGRESS.md")
	if err != nil {
		t.Fatalf("BuildFromPlan() error: %v", err)
	}

	if !strings.Contains(got, "Plan="+planPath) {
		t.Fatalf("custom template output missing plan path: %q", got)
	}
	if !strings.Contains(got, "custom task") {
		t.Fatalf("custom template output missing plan content: %q", got)
	}
	if !strings.Contains(got, "Notes=/run/NOTES.md") {
		t.Fatalf("custom template output missing notes path: %q", got)
	}
}

func TestBuildFromPlan_NonExistent(t *testing.T) {
	_, err := BuildFromPlan("/nonexistent/path/PLAN.md", "", "", "")
	if err == nil {
		t.Fatal("expected error for non-existent plan file")
	}
	if !strings.Contains(err.Error(), "reading plan file") {
		t.Errorf("error should mention reading plan file, got: %v", err)
	}
}

func TestBuildFromPlan_InvalidTemplate(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "PLAN.md")
	if err := os.WriteFile(planPath, []byte("- task\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := BuildFromPlan(planPath, "{{if .PlanContent}}", "", "")
	if err == nil {
		t.Fatal("expected template parse error")
	}
	if !strings.Contains(err.Error(), "parsing template") {
		t.Fatalf("error should mention parsing template, got: %v", err)
	}
}

func TestBuildFromPromptFile(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.md")
	content := "Do exactly this specific thing.\nNo more, no less."
	if err := os.WriteFile(promptPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := BuildFromPromptFile(promptPath)
	if err != nil {
		t.Fatalf("BuildFromPromptFile() error: %v", err)
	}

	if got != content {
		t.Errorf("BuildFromPromptFile() = %q, want %q", got, content)
	}
}

func TestBuildFromPromptFile_NonExistent(t *testing.T) {
	_, err := BuildFromPromptFile("/nonexistent/path/prompt.md")
	if err == nil {
		t.Fatal("expected error for non-existent prompt file")
	}
	if !strings.Contains(err.Error(), "reading prompt file") {
		t.Errorf("error should mention reading prompt file, got: %v", err)
	}
}

func TestBuildDefault(t *testing.T) {
	notesPath := "/runs/abc/NOTES.md"
	progressPath := "/runs/abc/PROGRESS.md"

	got, err := BuildDefault("", notesPath, progressPath)
	if err != nil {
		t.Fatalf("BuildDefault() error: %v", err)
	}

	if got == "" {
		t.Error("BuildDefault() returned empty string")
	}

	if !strings.Contains(got, "<promise>COMPLETE</promise>") {
		t.Error("BuildDefault() missing COMPLETE marker instruction")
	}

	// Must contain the task-loop framing.
	if !strings.Contains(got, "task loop") {
		t.Error("BuildDefault() missing 'task loop' framing")
	}
	if !strings.Contains(got, "fresh context") {
		t.Error("BuildDefault() missing 'fresh context' framing")
	}

	// Must reference the provided memory file paths.
	if !strings.Contains(got, notesPath) {
		t.Errorf("BuildDefault() missing notes path %q", notesPath)
	}
	if !strings.Contains(got, progressPath) {
		t.Errorf("BuildDefault() missing progress path %q", progressPath)
	}

	// Must instruct to do only one task.
	if !strings.Contains(got, "one task") {
		t.Error("BuildDefault() missing 'one task' instruction")
	}

	// Must instruct to git commit.
	if !strings.Contains(got, "Git commit") {
		t.Error("BuildDefault() missing git commit instruction")
	}
}

func TestBuildDefault_CustomTemplate(t *testing.T) {
	got, err := BuildDefault("custom default template notes={{.NotesPath}}", "/run/NOTES.md", "/run/PROGRESS.md")
	if err != nil {
		t.Fatalf("BuildDefault() error: %v", err)
	}
	want := "custom default template notes=/run/NOTES.md"
	if got != want {
		t.Fatalf("BuildDefault() = %q, want %q", got, want)
	}
}

func TestBuildDefault_InvalidTemplate(t *testing.T) {
	_, err := BuildDefault("{{if .PlanPath}}", "", "")
	if err == nil {
		t.Fatal("expected template parse error")
	}
	if !strings.Contains(err.Error(), "parsing template") {
		t.Fatalf("error should mention parsing template, got: %v", err)
	}
}

func TestBuildFromPlan_EmptyPaths(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "PLAN.md")
	if err := os.WriteFile(planPath, []byte("- task\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Empty paths should not cause template errors — they render as empty strings.
	got, err := BuildFromPlan(planPath, "", "", "")
	if err != nil {
		t.Fatalf("BuildFromPlan() with empty paths: %v", err)
	}
	if !strings.Contains(got, "task loop") {
		t.Error("output missing expected content")
	}
}

func TestBuildDefault_EmptyPaths(t *testing.T) {
	// Empty paths should not cause template errors.
	got, err := BuildDefault("", "", "")
	if err != nil {
		t.Fatalf("BuildDefault() with empty paths: %v", err)
	}
	if !strings.Contains(got, "task loop") {
		t.Error("output missing expected content")
	}
}
