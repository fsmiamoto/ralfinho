package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fsmiamoto/ralfinho/internal/cli"
	"github.com/fsmiamoto/ralfinho/internal/config"
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
	clearConfiguredTemplates(t)

	t.Run("prompt mode reads file verbatim", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "prompt.md")
		content := "Do this specific task."
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		cfg := &cli.Config{InputMode: "prompt", PromptFile: path}
		got, err := resolvePrompt(cfg, "", "")
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

		cfg := &cli.Config{InputMode: "plan", PlanFile: path}
		got, err := resolvePrompt(cfg, "", "")
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
		cfg := &cli.Config{InputMode: "default"}
		got, err := resolvePrompt(cfg, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "task loop") {
			t.Error("default prompt missing 'task loop'")
		}
	})

	t.Run("plan mode uses configured template override", func(t *testing.T) {
		clearConfiguredTemplates(t)
		configuredTemplates = config.ResolvedTemplates{Plan: "Configured plan: {{.PlanContent}}"}

		dir := t.TempDir()
		path := filepath.Join(dir, "PLAN.md")
		if err := os.WriteFile(path, []byte("# Plan\n- configured task"), 0644); err != nil {
			t.Fatal(err)
		}

		cfg := &cli.Config{InputMode: "plan", PlanFile: path}
		got, err := resolvePrompt(cfg, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "Configured plan:") {
			t.Fatalf("configured template missing from output: %q", got)
		}
		if !strings.Contains(got, "configured task") {
			t.Fatalf("plan content missing from configured output: %q", got)
		}
	})

	t.Run("default mode uses configured template override", func(t *testing.T) {
		clearConfiguredTemplates(t)
		configuredTemplates = config.ResolvedTemplates{Default: "Configured default prompt"}

		cfg := &cli.Config{InputMode: "default"}
		got, err := resolvePrompt(cfg, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "Configured default prompt" {
			t.Fatalf("got %q, want %q", got, "Configured default prompt")
		}
	})

	t.Run("unknown mode returns error", func(t *testing.T) {
		cfg := &cli.Config{InputMode: "bogus"}
		_, err := resolvePrompt(cfg, "", "")
		if err == nil {
			t.Fatal("expected error for unknown input mode")
		}
		if !strings.Contains(err.Error(), "unknown input mode") {
			t.Errorf("error should mention 'unknown input mode', got: %v", err)
		}
	})

	t.Run("prompt mode with missing file returns error", func(t *testing.T) {
		cfg := &cli.Config{InputMode: "prompt", PromptFile: "/nonexistent/file.md"}
		_, err := resolvePrompt(cfg, "", "")
		if err == nil {
			t.Fatal("expected error for missing prompt file")
		}
	})

	t.Run("plan mode embeds memory file paths", func(t *testing.T) {
		clearConfiguredTemplates(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "PLAN.md")
		if err := os.WriteFile(path, []byte("# Plan"), 0644); err != nil {
			t.Fatal(err)
		}

		cfg := &cli.Config{InputMode: "plan", PlanFile: path}
		got, err := resolvePrompt(cfg, "/runs/abc/NOTES.md", "/runs/abc/PROGRESS.md")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "/runs/abc/NOTES.md") {
			t.Error("notes path missing from plan mode output")
		}
		if !strings.Contains(got, "/runs/abc/PROGRESS.md") {
			t.Error("progress path missing from plan mode output")
		}
	})

	t.Run("default mode embeds memory file paths", func(t *testing.T) {
		clearConfiguredTemplates(t)
		cfg := &cli.Config{InputMode: "default"}
		got, err := resolvePrompt(cfg, "/runs/xyz/NOTES.md", "/runs/xyz/PROGRESS.md")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "/runs/xyz/NOTES.md") {
			t.Error("notes path missing from default mode output")
		}
		if !strings.Contains(got, "/runs/xyz/PROGRESS.md") {
			t.Error("progress path missing from default mode output")
		}
	})

	t.Run("prompt mode ignores memory paths (verbatim)", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "prompt.md")
		content := "Custom prompt, no templates."
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		cfg := &cli.Config{InputMode: "prompt", PromptFile: path}
		got, err := resolvePrompt(cfg, "/runs/abc/NOTES.md", "/runs/abc/PROGRESS.md")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != content {
			t.Errorf("prompt mode should return verbatim content, got %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// resolveResumePrompt
// ---------------------------------------------------------------------------

func TestResolveResumePrompt(t *testing.T) {
	clearConfiguredTemplates(t)

	t.Run("effective prompt reads file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "effective-prompt.md")
		content := "Previously used prompt text."
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}

		got, err := resolveResumePrompt(viewer.ResumeSourceEffectivePrompt, path, "", "")
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

		got, err := resolveResumePrompt(viewer.ResumeSourcePromptFile, path, "", "")
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

		got, err := resolveResumePrompt(viewer.ResumeSourcePlanFile, path, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "task A") {
			t.Error("plan content missing")
		}
	})

	t.Run("default source", func(t *testing.T) {
		got, err := resolveResumePrompt(viewer.ResumeSourceDefault, "", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "task loop") {
			t.Error("default prompt missing 'task loop'")
		}
	})

	t.Run("plan source uses configured template override", func(t *testing.T) {
		clearConfiguredTemplates(t)
		configuredTemplates = config.ResolvedTemplates{Plan: "Resume template: {{.PlanContent}}"}

		dir := t.TempDir()
		path := filepath.Join(dir, "PLAN.md")
		if err := os.WriteFile(path, []byte("# Plan\n- resumed task"), 0644); err != nil {
			t.Fatal(err)
		}

		got, err := resolveResumePrompt(viewer.ResumeSourcePlanFile, path, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "Resume template:") || !strings.Contains(got, "resumed task") {
			t.Fatalf("configured resume plan template not applied: %q", got)
		}
	})

	t.Run("default source uses configured template override", func(t *testing.T) {
		clearConfiguredTemplates(t)
		configuredTemplates = config.ResolvedTemplates{Default: "Resume default prompt"}

		got, err := resolveResumePrompt(viewer.ResumeSourceDefault, "", "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "Resume default prompt" {
			t.Fatalf("got %q, want %q", got, "Resume default prompt")
		}
	})

	t.Run("unknown source returns error", func(t *testing.T) {
		_, err := resolveResumePrompt("unknown_source", "", "", "")
		if err == nil {
			t.Fatal("expected error for unknown resume source")
		}
	})

	t.Run("missing effective prompt file returns error", func(t *testing.T) {
		_, err := resolveResumePrompt(viewer.ResumeSourceEffectivePrompt, "/nonexistent/file.md", "", "")
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("plan source embeds memory file paths", func(t *testing.T) {
		clearConfiguredTemplates(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "PLAN.md")
		if err := os.WriteFile(path, []byte("# Plan"), 0644); err != nil {
			t.Fatal(err)
		}

		got, err := resolveResumePrompt(viewer.ResumeSourcePlanFile, path, "/runs/r1/NOTES.md", "/runs/r1/PROGRESS.md")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "/runs/r1/NOTES.md") {
			t.Error("notes path missing from resume plan output")
		}
		if !strings.Contains(got, "/runs/r1/PROGRESS.md") {
			t.Error("progress path missing from resume plan output")
		}
	})

	t.Run("default source embeds memory file paths", func(t *testing.T) {
		clearConfiguredTemplates(t)
		got, err := resolveResumePrompt(viewer.ResumeSourceDefault, "", "/runs/r2/NOTES.md", "/runs/r2/PROGRESS.md")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "/runs/r2/NOTES.md") {
			t.Error("notes path missing from resume default output")
		}
		if !strings.Contains(got, "/runs/r2/PROGRESS.md") {
			t.Error("progress path missing from resume default output")
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
// parseFlagSet
// ---------------------------------------------------------------------------

func TestParseFlagSetRecognizesSupportedForms(t *testing.T) {
	set := parseFlagSet([]string{
		"--agent=claude",
		"-a",
		"kiro",
		"--max-iterations",
		"5",
		"-m=7",
		"--runs-dir",
		"custom/runs",
		"-runs-dir=alt/runs",
		"--no-tui",
		"prompt.md",
		"-",
		"--",
	})

	for _, name := range []string{"agent", "a", "max-iterations", "m", "runs-dir", "no-tui"} {
		if !set[name] {
			t.Fatalf("parseFlagSet() missing flag %q in %#v", name, set)
		}
	}
	if set[""] {
		t.Fatalf("parseFlagSet() should ignore empty flag names: %#v", set)
	}
	if set["prompt.md"] || set["custom/runs"] || set["5"] {
		t.Fatalf("parseFlagSet() should ignore positional values: %#v", set)
	}
}

// ---------------------------------------------------------------------------
// applyFileConfig
// ---------------------------------------------------------------------------

func TestApplyFileConfigOnlyFillsUnsetFlags(t *testing.T) {
	maxIterations := 9
	noTUI := true

	cfg := &cli.Config{
		Agent:         "kiro",
		MaxIterations: 0,
		NoTUI:         false,
		RunsDir:       "cli/runs",
	}

	applyFileConfig(cfg, &config.FileConfig{
		Agent:         "claude",
		MaxIterations: &maxIterations,
		RunsDir:       "file/runs",
		NoTUI:         &noTUI,
	}, []string{"-a", "kiro", "--runs-dir", "cli/runs", "prompt.md"})

	if cfg.Agent != "kiro" {
		t.Fatalf("Agent = %q, want CLI value %q", cfg.Agent, "kiro")
	}
	if cfg.RunsDir != "cli/runs" {
		t.Fatalf("RunsDir = %q, want CLI value %q", cfg.RunsDir, "cli/runs")
	}
	if cfg.MaxIterations != 9 {
		t.Fatalf("MaxIterations = %d, want file-config value %d", cfg.MaxIterations, 9)
	}
	if !cfg.NoTUI {
		t.Fatal("NoTUI = false, want file-config default true")
	}
}

func TestApplyFileConfigAppliesAllFileDefaultsWhenFlagsAreUnset(t *testing.T) {
	maxIterations := 12
	noTUI := true

	cfg := &cli.Config{
		Agent:         "pi",
		MaxIterations: 0,
		NoTUI:         false,
		RunsDir:       ".ralfinho/runs",
	}

	applyFileConfig(cfg, &config.FileConfig{
		Agent:         "claude",
		MaxIterations: &maxIterations,
		RunsDir:       "file/runs",
		NoTUI:         &noTUI,
	}, []string{"prompt.md"})

	if cfg.Agent != "claude" {
		t.Fatalf("Agent = %q, want file-config value %q", cfg.Agent, "claude")
	}
	if cfg.MaxIterations != 12 {
		t.Fatalf("MaxIterations = %d, want file-config value %d", cfg.MaxIterations, 12)
	}
	if cfg.RunsDir != "file/runs" {
		t.Fatalf("RunsDir = %q, want file-config value %q", cfg.RunsDir, "file/runs")
	}
	if !cfg.NoTUI {
		t.Fatal("NoTUI = false, want file-config value true")
	}
}

func TestApplyFileConfigPreservesExplicitCLIValues(t *testing.T) {
	maxIterations := 9
	noTUI := false

	cfg := &cli.Config{
		Agent:         "pi",
		MaxIterations: 3,
		NoTUI:         true,
		RunsDir:       "cli/runs",
	}

	applyFileConfig(cfg, &config.FileConfig{
		Agent:         "claude",
		MaxIterations: &maxIterations,
		RunsDir:       "file/runs",
		NoTUI:         &noTUI,
	}, []string{"--agent", "pi", "-m", "3", "--runs-dir", "cli/runs", "--no-tui"})

	if cfg.Agent != "pi" {
		t.Fatalf("Agent = %q, want explicit CLI value %q", cfg.Agent, "pi")
	}
	if cfg.MaxIterations != 3 {
		t.Fatalf("MaxIterations = %d, want explicit CLI value %d", cfg.MaxIterations, 3)
	}
	if cfg.RunsDir != "cli/runs" {
		t.Fatalf("RunsDir = %q, want explicit CLI value %q", cfg.RunsDir, "cli/runs")
	}
	if !cfg.NoTUI {
		t.Fatal("NoTUI = false, want explicit CLI value true")
	}
}

func TestApplyFileConfigNilFileConfigIsNoOp(t *testing.T) {
	cfg := &cli.Config{
		Agent:         "pi",
		MaxIterations: 3,
		NoTUI:         true,
		RunsDir:       "cli/runs",
	}

	applyFileConfig(cfg, nil, []string{"--agent", "pi"})

	if cfg.Agent != "pi" || cfg.MaxIterations != 3 || cfg.RunsDir != "cli/runs" || !cfg.NoTUI {
		t.Fatalf("applyFileConfig(nil) changed config: %#v", cfg)
	}
}

// ---------------------------------------------------------------------------
// extraArgsForAgent
// ---------------------------------------------------------------------------

func TestExtraArgsForAgentUsesLoadedFileConfig(t *testing.T) {
	prev := fileCfg
	t.Cleanup(func() { fileCfg = prev })

	fileCfg = nil
	if got := extraArgsForAgent("claude"); got != nil {
		t.Fatalf("extraArgsForAgent() with nil fileCfg = %#v, want nil", got)
	}

	fileCfg = &config.FileConfig{
		Agents: map[string]config.AgentConfig{
			"claude": {ExtraArgs: []string{"--model", "claude-opus-4-5"}},
			"pi":     {ExtraArgs: []string{"--timeout", "30"}},
		},
	}

	if got := extraArgsForAgent("claude"); !reflect.DeepEqual(got, []string{"--model", "claude-opus-4-5"}) {
		t.Fatalf("extraArgsForAgent(claude) = %#v, want %#v", got, []string{"--model", "claude-opus-4-5"})
	}
	if got := extraArgsForAgent("kiro"); got != nil {
		t.Fatalf("extraArgsForAgent(kiro) = %#v, want nil", got)
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
