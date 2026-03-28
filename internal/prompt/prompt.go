package prompt

import (
	"bytes"
	"fmt"
	"os"
	"text/template"
)

// planData is the data passed into the plan/default templates.
type planData struct {
	PlanPath     string
	PlanContent  string
	NotesPath    string
	ProgressPath string
}

// BuildFromPlan reads planPath, renders either the built-in plan template or a
// caller-provided override with the plan content, and returns the final prompt
// string.
func BuildFromPlan(planPath, templateOverride, notesPath, progressPath string) (string, error) {
	data, err := os.ReadFile(planPath)
	if err != nil {
		return "", fmt.Errorf("reading plan file %q: %w", planPath, err)
	}

	templateText := defaultTemplate
	if templateOverride != "" {
		templateText = templateOverride
	}

	return renderTemplate(templateText, planData{
		PlanPath:     planPath,
		PlanContent:  string(data),
		NotesPath:    notesPath,
		ProgressPath: progressPath,
	})
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

// BuildDefault returns the built-in default prompt or a caller-provided
// template override, rendered with the given memory file paths.
func BuildDefault(templateOverride, notesPath, progressPath string) (string, error) {
	templateText := defaultPromptTemplate
	if templateOverride != "" {
		templateText = templateOverride
	}
	return renderTemplate(templateText, planData{
		NotesPath:    notesPath,
		ProgressPath: progressPath,
	})
}

func renderTemplate(templateText string, data planData) (string, error) {
	tmpl, err := template.New("prompt").Parse(templateText)
	if err != nil {
		return "", fmt.Errorf("parsing template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}
	return buf.String(), nil
}
