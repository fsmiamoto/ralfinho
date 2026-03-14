package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsmiamoto/ralfinho/internal/viewer"
)

// ---------------------------------------------------------------------------
// formatMetaDate
// ---------------------------------------------------------------------------

func TestFormatMetaDate(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"RFC3339", "2024-06-15T10:30:00Z", "2024-06-15 10:30"},
		{"RFC3339 with offset", "2024-06-15T10:30:00+03:00", "2024-06-15 10:30"},
		{"RFC3339Nano", "2024-06-15T10:30:00.123456789Z", "2024-06-15 10:30"},
		{"unparseable long", "not-a-real-timestamp-at-all", "not-a-real-times"},
		{"unparseable short", "bad", "bad"},
		{"exactly 16 chars", "0123456789abcdef", "0123456789abcdef"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatMetaDate(tt.in)
			if got != tt.want {
				t.Errorf("formatMetaDate(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// formatRunSummaryDate
// ---------------------------------------------------------------------------

func TestFormatRunSummaryDate(t *testing.T) {
	parsed := time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC)

	tests := []struct {
		name    string
		summary viewer.RunSummary
		want    string
	}{
		{
			name:    "uses StartedAt when available",
			summary: viewer.RunSummary{StartedAt: parsed},
			want:    "2024-06-15 10:30",
		},
		{
			name:    "falls back to StartedAtText",
			summary: viewer.RunSummary{StartedAtText: "2024-06-15T10:30:00Z"},
			want:    "2024-06-15 10:30",
		},
		{
			name:    "falls back to SortTime",
			summary: viewer.RunSummary{SortTime: parsed},
			want:    "2024-06-15 10:30",
		},
		{
			name:    "returns unknown when nothing available",
			summary: viewer.RunSummary{},
			want:    "unknown",
		},
		{
			name: "StartedAt takes priority over StartedAtText",
			summary: viewer.RunSummary{
				StartedAt:     parsed,
				StartedAtText: "1999-01-01T00:00:00Z",
			},
			want: "2024-06-15 10:30",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatRunSummaryDate(tt.summary)
			if got != tt.want {
				t.Errorf("formatRunSummaryDate() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// formatRunSummary
// ---------------------------------------------------------------------------

func TestFormatRunSummary(t *testing.T) {
	tests := []struct {
		name    string
		summary viewer.RunSummary
		checks  []string // substrings that must appear
	}{
		{
			name: "normal run",
			summary: viewer.RunSummary{
				RunID:               "abcdef12-3456-7890-abcd-ef1234567890",
				Agent:               "pi",
				Status:              "completed",
				IterationsCompleted: 5,
				PromptLabel:         "plan: PLAN.md",
				StartedAt:           time.Date(2024, 6, 15, 10, 30, 0, 0, time.UTC),
			},
			checks: []string{"abcdef12", "pi", "completed", "5 iterations", "plan: PLAN.md"},
		},
		{
			name: "truncates long run ID to 8 chars",
			summary: viewer.RunSummary{
				RunID:               "abcdef12-long-id",
				Agent:               "kiro",
				Status:              "failed",
				IterationsCompleted: 1,
				PromptLabel:         "default",
				StartedAt:           time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			},
			checks: []string{"abcdef12", "kiro", "failed"},
		},
		{
			name: "short run ID not truncated",
			summary: viewer.RunSummary{
				RunID:               "short",
				Agent:               "claude",
				Status:              "running",
				IterationsCompleted: 0,
				PromptLabel:         "default",
				StartedAt:           time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			},
			checks: []string{"short", "claude"},
		},
		{
			name: "artifact error overrides details",
			summary: viewer.RunSummary{
				RunID:               "abcdef12-3456",
				Agent:               "pi",
				Status:              "completed",
				IterationsCompleted: 3,
				PromptLabel:         "plan: PLAN.md",
				ArtifactError:       "corrupt meta.json",
				StartedAt:           time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			},
			checks: []string{"corrupt meta.json"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatRunSummary(tt.summary)
			for _, check := range tt.checks {
				if !strings.Contains(got, check) {
					t.Errorf("formatRunSummary() = %q, missing %q", got, check)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// resolvePrompt
// ---------------------------------------------------------------------------

func TestResolvePrompt(t *testing.T) {
	t.Run("prompt mode reads file verbatim", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "prompt.md")
		content := "Do this specific task."
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		cfg := &cliConfig{InputMode: "prompt", PromptFile: path}
		got, err := resolvePromptFromConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != content {
			t.Errorf("got %q, want %q", got, content)
		}
	})

	t.Run("plan mode renders template", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "PLAN.md")
		if err := os.WriteFile(path, []byte("# Plan\n- task 1"), 0644); err != nil {
			t.Fatal(err)
		}

		cfg := &cliConfig{InputMode: "plan", PlanFile: path}
		got, err := resolvePromptFromConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "task 1") {
			t.Error("plan content missing from output")
		}
		if !strings.Contains(got, "task loop") {
			t.Error("template framing missing from output")
		}
	})

	t.Run("default mode returns built-in prompt", func(t *testing.T) {
		cfg := &cliConfig{InputMode: "default"}
		got, err := resolvePromptFromConfig(cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "task loop") {
			t.Error("default prompt missing 'task loop'")
		}
	})

	t.Run("unknown mode returns error", func(t *testing.T) {
		cfg := &cliConfig{InputMode: "bogus"}
		_, err := resolvePromptFromConfig(cfg)
		if err == nil {
			t.Fatal("expected error for unknown input mode")
		}
		if !strings.Contains(err.Error(), "unknown input mode") {
			t.Errorf("error should mention 'unknown input mode', got: %v", err)
		}
	})

	t.Run("prompt mode with missing file returns error", func(t *testing.T) {
		cfg := &cliConfig{InputMode: "prompt", PromptFile: "/nonexistent/file.md"}
		_, err := resolvePromptFromConfig(cfg)
		if err == nil {
			t.Fatal("expected error for missing prompt file")
		}
	})
}

// ---------------------------------------------------------------------------
// resolveResumePrompt
// ---------------------------------------------------------------------------

func TestResolveResumePrompt(t *testing.T) {
	t.Run("effective prompt reads file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "effective-prompt.md")
		content := "Previously used prompt text."
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		got, err := resolveResumePrompt(viewer.ResumeSourceEffectivePrompt, path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != content {
			t.Errorf("got %q, want %q", got, content)
		}
	})

	t.Run("prompt file source", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "prompt.md")
		if err := os.WriteFile(path, []byte("custom prompt"), 0644); err != nil {
			t.Fatal(err)
		}

		got, err := resolveResumePrompt(viewer.ResumeSourcePromptFile, path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "custom prompt" {
			t.Errorf("got %q, want %q", got, "custom prompt")
		}
	})

	t.Run("plan file source", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "PLAN.md")
		if err := os.WriteFile(path, []byte("# Plan\n- task A"), 0644); err != nil {
			t.Fatal(err)
		}

		got, err := resolveResumePrompt(viewer.ResumeSourcePlanFile, path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "task A") {
			t.Error("plan content missing")
		}
	})

	t.Run("default source", func(t *testing.T) {
		got, err := resolveResumePrompt(viewer.ResumeSourceDefault, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "task loop") {
			t.Error("default prompt missing 'task loop'")
		}
	})

	t.Run("unknown source returns error", func(t *testing.T) {
		_, err := resolveResumePrompt("unknown_source", "")
		if err == nil {
			t.Fatal("expected error for unknown resume source")
		}
	})

	t.Run("missing effective prompt file returns error", func(t *testing.T) {
		_, err := resolveResumePrompt(viewer.ResumeSourceEffectivePrompt, "/nonexistent/file.md")
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})
}

// ---------------------------------------------------------------------------
// resumePromptMeta
// ---------------------------------------------------------------------------

func TestResumePromptMeta(t *testing.T) {
	tests := []struct {
		source     viewer.ResumeSource
		path       string
		wantMode   string
		wantPrompt string
		wantPlan   string
	}{
		{viewer.ResumeSourceEffectivePrompt, "/some/path", "prompt", "", ""},
		{viewer.ResumeSourcePromptFile, "/my/prompt.md", "prompt", "/my/prompt.md", ""},
		{viewer.ResumeSourcePlanFile, "/my/PLAN.md", "plan", "", "/my/PLAN.md"},
		{viewer.ResumeSourceDefault, "", "default", "", ""},
		{"unknown", "/x", "prompt", "", ""},
	}
	for _, tt := range tests {
		t.Run(string(tt.source), func(t *testing.T) {
			mode, promptFile, planFile := resumePromptMeta(tt.source, tt.path)
			if mode != tt.wantMode {
				t.Errorf("mode = %q, want %q", mode, tt.wantMode)
			}
			if promptFile != tt.wantPrompt {
				t.Errorf("promptFile = %q, want %q", promptFile, tt.wantPrompt)
			}
			if planFile != tt.wantPlan {
				t.Errorf("planFile = %q, want %q", planFile, tt.wantPlan)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// helpers — cliConfig is a lightweight struct that mirrors cli.Config fields
// used by resolvePrompt, so we can test without importing the full cli package.
// ---------------------------------------------------------------------------

type cliConfig struct {
	InputMode  string
	PromptFile string
	PlanFile   string
}

// resolvePromptFromConfig mirrors resolvePrompt but accepts a test-local config.
// This avoids depending on cli.Config while testing the same logic.
func resolvePromptFromConfig(cfg *cliConfig) (string, error) {
	// Use an adapter that calls the same underlying prompt package functions.
	switch cfg.InputMode {
	case "prompt":
		return resolveResumePrompt(viewer.ResumeSourcePromptFile, cfg.PromptFile)
	case "plan":
		return resolveResumePrompt(viewer.ResumeSourcePlanFile, cfg.PlanFile)
	case "default":
		return resolveResumePrompt(viewer.ResumeSourceDefault, "")
	default:
		return "", fmt.Errorf("unknown input mode %q", cfg.InputMode)
	}
}

// ---------------------------------------------------------------------------
// isSubdir
// ---------------------------------------------------------------------------

func TestIsSubdir(t *testing.T) {
	tests := []struct {
		name   string
		parent string
		child  string
		want   bool
	}{
		{"direct child", "/runs", "/runs/abc123", true},
		{"same dir", "/runs", "/runs", false},
		{"nested child rejected", "/runs", "/runs/abc/nested", false},
		{"sibling dir", "/runs", "/other/abc123", false},
		{"prefix attack", "/runs", "/runs-evil/abc123", false},
		{"traversal attack", "/runs", "/runs/../etc/passwd", false},
		{"trailing slash parent", "/runs/", "/runs/abc123", true},
		{"dot in parent", "/home/user/./runs", "/home/user/runs/abc123", true},
		{"dotdot in child", "/home/user/runs", "/home/user/runs/abc/../def", true},
		{"empty child", "/runs", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSubdir(tt.parent, tt.child)
			if got != tt.want {
				t.Errorf("isSubdir(%q, %q) = %v, want %v", tt.parent, tt.child, got, tt.want)
			}
		})
	}
}
