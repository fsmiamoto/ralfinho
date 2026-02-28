package promptinput

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Source string

const (
	SourcePrompt  Source = "prompt"
	SourcePlan    Source = "plan"
	SourceDefault Source = "default"
)

type ResolveInput struct {
	PromptFlag         string
	PositionalPrompt   string
	PlanFlag           string
	PromptTemplateFlag string
	CWD                string
}

type Resolution struct {
	Source          Source
	PromptFile      string
	PlanFile        string
	EffectivePrompt string
}

func ResolveAndBuild(in ResolveInput) (Resolution, error) {
	cwd := in.CWD
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return Resolution{}, fmt.Errorf("get working directory: %w", err)
		}
	}

	if in.PromptFlag != "" {
		content, err := os.ReadFile(in.PromptFlag)
		if err != nil {
			return Resolution{}, fmt.Errorf("read prompt file %q: %w", in.PromptFlag, err)
		}
		return Resolution{Source: SourcePrompt, PromptFile: in.PromptFlag, EffectivePrompt: string(content)}, nil
	}

	if in.PositionalPrompt != "" {
		content, err := os.ReadFile(in.PositionalPrompt)
		if err != nil {
			return Resolution{}, fmt.Errorf("read prompt file %q: %w", in.PositionalPrompt, err)
		}
		return Resolution{Source: SourcePrompt, PromptFile: in.PositionalPrompt, EffectivePrompt: string(content)}, nil
	}

	if in.PlanFlag != "" {
		if _, err := os.ReadFile(in.PlanFlag); err != nil {
			return Resolution{}, fmt.Errorf("read plan file %q: %w", in.PlanFlag, err)
		}
		effectivePrompt, err := buildTemplate(in.PlanFlag, in.PromptTemplateFlag)
		if err != nil {
			return Resolution{}, err
		}
		return Resolution{Source: SourcePlan, PlanFile: in.PlanFlag, EffectivePrompt: effectivePrompt}, nil
	}

	defaultPlanPath := filepath.Join(cwd, "PLAN.md")
	if _, err := os.Stat(defaultPlanPath); err == nil {
		if _, err := os.ReadFile(defaultPlanPath); err != nil {
			return Resolution{}, fmt.Errorf("read default plan file %q: %w", defaultPlanPath, err)
		}
		effectivePrompt, err := buildTemplate("./PLAN.md", in.PromptTemplateFlag)
		if err != nil {
			return Resolution{}, err
		}
		return Resolution{Source: SourceDefault, PlanFile: "./PLAN.md", EffectivePrompt: effectivePrompt}, nil
	}

	effectivePrompt, err := buildTemplate("", in.PromptTemplateFlag)
	if err != nil {
		return Resolution{}, err
	}
	return Resolution{Source: SourceDefault, EffectivePrompt: effectivePrompt}, nil
}

func WriteEffectivePrompt(runDir string, effectivePrompt string) (string, error) {
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return "", fmt.Errorf("create run directory: %w", err)
	}
	path := filepath.Join(runDir, "effective-prompt.md")
	if err := os.WriteFile(path, []byte(effectivePrompt), 0o644); err != nil {
		return "", fmt.Errorf("write effective prompt: %w", err)
	}
	return path, nil
}

func buildTemplate(planPath string, templatePath string) (string, error) {
	planInstruction := "Read docs/V1_PLAN.md and docs/V1_PROGRESS.md to find the highest-priority incomplete task."
	if strings.TrimSpace(planPath) != "" {
		planInstruction = fmt.Sprintf("Study %s and docs/V1_PROGRESS.md to find the highest-priority incomplete task.", planPath)
	}

	if strings.TrimSpace(templatePath) == "" {
		return fmt.Sprintf(`You are an elite coding agent executing an implementation plan.

## Your Task

1. %s
2. Implement that single highest-priority incomplete task
3. Run quality checks: go vet ./... && go build ./...
4. Mark the completed task in docs/V1_PROGRESS.md
5. Append a progress report to log.txt

## Stop Condition

If ALL tasks in docs/V1_PROGRESS.md are complete, reply with:

<promise>COMPLETE</promise>
`, planInstruction), nil
	}

	templateBytes, err := os.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("read prompt template %q: %w", templatePath, err)
	}
	prompt := strings.ReplaceAll(string(templateBytes), "{{PLAN_INSTRUCTION}}", planInstruction)
	prompt = strings.ReplaceAll(prompt, "{{PLAN_PATH}}", planPath)
	if !strings.Contains(prompt, "<promise>COMPLETE</promise>") {
		prompt = strings.TrimRight(prompt, "\n") + "\n\n<promise>COMPLETE</promise>\n"
	}
	return prompt, nil
}
