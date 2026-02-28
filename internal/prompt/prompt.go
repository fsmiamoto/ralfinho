package prompt

import (
	"bytes"
	"fmt"
	"os"
	"text/template"
)

// planData is the data passed into the default plan template.
type planData struct {
	PlanPath    string
	PlanContent string
}

// BuildFromPlan reads planPath, renders the default template with its content,
// and returns the final prompt string.
func BuildFromPlan(planPath string) (string, error) {
	data, err := os.ReadFile(planPath)
	if err != nil {
		return "", fmt.Errorf("reading plan file %q: %w", planPath, err)
	}

	tmpl, err := template.New("prompt").Parse(defaultTemplate)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, planData{
		PlanPath:    planPath,
		PlanContent: string(data),
	})
	if err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}

	return buf.String(), nil
}

// BuildFromPromptFile reads the file at promptPath and returns its contents
// verbatim as the prompt.
func BuildFromPromptFile(promptPath string) (string, error) {
	data, err := os.ReadFile(promptPath)
	if err != nil {
		return "", fmt.Errorf("reading prompt file %q: %w", promptPath, err)
	}
	return string(data), nil
}

// BuildDefault returns the built-in default prompt for when no plan or prompt
// file is specified.
func BuildDefault() string {
	return defaultPrompt
}
