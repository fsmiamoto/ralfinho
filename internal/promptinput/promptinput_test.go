package promptinput

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAndBuildPrecedence(t *testing.T) {
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "prompt.md")
	positionalPath := filepath.Join(dir, "positional.md")
	planPath := filepath.Join(dir, "PLAN_TASKS.md")
	defaultPlanPath := filepath.Join(dir, "PLAN.md")

	mustWrite(t, promptPath, "prompt file")
	mustWrite(t, positionalPath, "positional file")
	mustWrite(t, planPath, "# plan")
	mustWrite(t, defaultPlanPath, "# default plan")

	t.Run("prompt flag wins", func(t *testing.T) {
		res, err := ResolveAndBuild(ResolveInput{
			PromptFlag:       promptPath,
			PositionalPrompt: positionalPath,
			PlanFlag:         planPath,
			CWD:              dir,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Source != SourcePrompt {
			t.Fatalf("expected prompt source, got %s", res.Source)
		}
		if res.EffectivePrompt != "prompt file" {
			t.Fatalf("unexpected prompt content: %q", res.EffectivePrompt)
		}
	})

	t.Run("positional beats plan", func(t *testing.T) {
		res, err := ResolveAndBuild(ResolveInput{
			PositionalPrompt: positionalPath,
			PlanFlag:         planPath,
			CWD:              dir,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Source != SourcePrompt {
			t.Fatalf("expected prompt source, got %s", res.Source)
		}
		if res.EffectivePrompt != "positional file" {
			t.Fatalf("unexpected prompt content: %q", res.EffectivePrompt)
		}
	})

	t.Run("plan generates template", func(t *testing.T) {
		res, err := ResolveAndBuild(ResolveInput{PlanFlag: planPath, CWD: dir})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Source != SourcePlan {
			t.Fatalf("expected plan source, got %s", res.Source)
		}
		if !strings.Contains(res.EffectivePrompt, "<promise>COMPLETE</promise>") {
			t.Fatal("expected completion marker in effective prompt")
		}
		if !strings.Contains(res.EffectivePrompt, planPath) {
			t.Fatalf("expected plan path in template, got: %s", res.EffectivePrompt)
		}
	})

	t.Run("default uses PLAN.md", func(t *testing.T) {
		res, err := ResolveAndBuild(ResolveInput{CWD: dir})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Source != SourceDefault {
			t.Fatalf("expected default source, got %s", res.Source)
		}
		if !strings.Contains(res.EffectivePrompt, "./PLAN.md") {
			t.Fatalf("expected default plan reference, got %s", res.EffectivePrompt)
		}
	})
}

func TestResolveAndBuildNoDefaultPlan(t *testing.T) {
	dir := t.TempDir()
	res, err := ResolveAndBuild(ResolveInput{CWD: dir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Source != SourceDefault {
		t.Fatalf("expected default source, got %s", res.Source)
	}
	if strings.Contains(res.EffectivePrompt, "./PLAN.md") {
		t.Fatalf("did not expect default plan reference: %s", res.EffectivePrompt)
	}
}

func TestWriteEffectivePrompt(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "runs", "id")
	path, err := WriteEffectivePrompt(runDir, "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(b) != "hello" {
		t.Fatalf("unexpected content: %q", string(b))
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
