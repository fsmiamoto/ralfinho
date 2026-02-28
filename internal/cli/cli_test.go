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

func TestParseAgentFlag(t *testing.T) {
	cfg, err := Parse([]string{"--agent", "myagent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Agent != "myagent" {
		t.Errorf("Agent = %q, want %q", cfg.Agent, "myagent")
	}
}
