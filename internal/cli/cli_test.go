package cli

import (
	"os"
	"testing"
)

func TestParseNoArgs_Default(t *testing.T) {
	// Use a temp dir without PLAN.md so we get InputMode="default".
	tmp := t.TempDir()
	orig, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(orig)

	cfg, err := Parse([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.InputMode != "default" {
		t.Errorf("InputMode = %q, want %q", cfg.InputMode, "default")
	}
}

func TestParseNoArgs_PlanMDExists(t *testing.T) {
	tmp := t.TempDir()
	orig, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(orig)

	if err := os.WriteFile("PLAN.md", []byte("plan"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Parse([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.InputMode != "plan" {
		t.Errorf("InputMode = %q, want %q", cfg.InputMode, "plan")
	}
	if cfg.PlanFile != "PLAN.md" {
		t.Errorf("PlanFile = %q, want %q", cfg.PlanFile, "PLAN.md")
	}
}

func TestParsePositionalPromptFile(t *testing.T) {
	cfg, err := Parse([]string{"todo.md"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.InputMode != "prompt" {
		t.Errorf("InputMode = %q, want %q", cfg.InputMode, "prompt")
	}
	if cfg.PromptFile != "todo.md" {
		t.Errorf("PromptFile = %q, want %q", cfg.PromptFile, "todo.md")
	}
}

func TestParsePromptFlag(t *testing.T) {
	cfg, err := Parse([]string{"--prompt", "my-prompt.md"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.InputMode != "prompt" {
		t.Errorf("InputMode = %q, want %q", cfg.InputMode, "prompt")
	}
	if cfg.PromptFile != "my-prompt.md" {
		t.Errorf("PromptFile = %q, want %q", cfg.PromptFile, "my-prompt.md")
	}
}

func TestParsePromptFlagWithPositional(t *testing.T) {
	_, err := Parse([]string{"--prompt", "my-prompt.md", "extra.md"})
	if err == nil {
		t.Fatal("expected error for --prompt with positional arg, got nil")
	}
}

func TestParsePlanFlagWithPositional(t *testing.T) {
	_, err := Parse([]string{"--plan", "plan.md", "extra.md"})
	if err == nil {
		t.Fatal("expected error for --plan with positional arg, got nil")
	}
}

func TestParseMultiplePositional(t *testing.T) {
	_, err := Parse([]string{"one.md", "two.md"})
	if err == nil {
		t.Fatal("expected error for multiple positional args, got nil")
	}
}

func TestParsePlanFlag(t *testing.T) {
	cfg, err := Parse([]string{"--plan", "plan.md"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.InputMode != "plan" {
		t.Errorf("InputMode = %q, want %q", cfg.InputMode, "plan")
	}
	if cfg.PlanFile != "plan.md" {
		t.Errorf("PlanFile = %q, want %q", cfg.PlanFile, "plan.md")
	}
}

func TestParsePromptAndPlanConflict(t *testing.T) {
	_, err := Parse([]string{"--prompt", "p.md", "--plan", "plan.md"})
	if err == nil {
		t.Fatal("expected error for --prompt + --plan, got nil")
	}
}

func TestParseViewRunID(t *testing.T) {
	cfg, err := Parse([]string{"view", "abc-123"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ViewRunID != "abc-123" {
		t.Errorf("ViewRunID = %q, want %q", cfg.ViewRunID, "abc-123")
	}
}

func TestParseViewList(t *testing.T) {
	cfg, err := Parse([]string{"view"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.ViewList {
		t.Error("ViewList = false, want true")
	}
}

func TestParseViewListNoTUI(t *testing.T) {
	cfg, err := Parse([]string{"view", "--no-tui"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.ViewList {
		t.Error("ViewList = false, want true")
	}
	if !cfg.NoTUI {
		t.Error("NoTUI = false, want true")
	}
}

func TestParseViewRejectsMultipleRunIDs(t *testing.T) {
	_, err := Parse([]string{"view", "abc-123", "def-456"})
	if err == nil {
		t.Fatal("expected error for multiple run IDs, got nil")
	}
}

func TestResolveViewMode(t *testing.T) {
	tests := []struct {
		name        string
		cfg         Config
		interactive bool
		want        ViewMode
	}{
		{
			name:        "browser on interactive tty",
			cfg:         Config{ViewList: true},
			interactive: true,
			want:        ViewModeBrowser,
		},
		{
			name:        "plain list on non-tty",
			cfg:         Config{ViewList: true},
			interactive: false,
			want:        ViewModeList,
		},
		{
			name:        "plain list when opted out",
			cfg:         Config{ViewList: true, NoTUI: true},
			interactive: true,
			want:        ViewModeList,
		},
		{
			name:        "replay preserved for run id",
			cfg:         Config{ViewRunID: "abc-123"},
			interactive: true,
			want:        ViewModeReplay,
		},
		{
			name:        "replay wins even with no-tui",
			cfg:         Config{ViewRunID: "abc-123", NoTUI: true},
			interactive: false,
			want:        ViewModeReplay,
		},
		{
			name:        "non-view command",
			cfg:         Config{},
			interactive: true,
			want:        ViewModeNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.ResolveViewMode(tt.interactive); got != tt.want {
				t.Fatalf("ResolveViewMode(%v) = %q, want %q", tt.interactive, got, tt.want)
			}
		})
	}
}

func TestParseMaxIterations(t *testing.T) {
	cfg, err := Parse([]string{"-m", "5"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxIterations != 5 {
		t.Errorf("MaxIterations = %d, want %d", cfg.MaxIterations, 5)
	}
}

func TestParseNoTUI(t *testing.T) {
	cfg, err := Parse([]string{"--no-tui"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.NoTUI {
		t.Error("NoTUI = false, want true")
	}
}

func TestParseHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		_, err := Parse([]string{flag})
		if err == nil {
			t.Fatalf("Parse(%q): expected error, got nil", flag)
		}
		if err.Error() != "" {
			t.Errorf("Parse(%q): error = %q, want empty string", flag, err.Error())
		}
	}
}

func TestParseVersion(t *testing.T) {
	for _, flag := range []string{"--version", "-v"} {
		cfg, err := Parse([]string{flag})
		if err != nil {
			t.Fatalf("Parse(%q): unexpected error: %v", flag, err)
		}
		if !cfg.ShowVersion {
			t.Errorf("Parse(%q): ShowVersion = false, want true", flag)
		}
	}
}

func TestParseAgentFlag(t *testing.T) {
	cfg, err := Parse([]string{"--agent", "myagent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Agent != "myagent" {
		t.Errorf("Agent = %q, want %q", cfg.Agent, "myagent")
	}
}
