// Command ralfinho is an autonomous coding agent runner.
package main

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dorayaki-do/ralfinho/internal/cli"
	"github.com/dorayaki-do/ralfinho/internal/prompt"
	"github.com/dorayaki-do/ralfinho/internal/runner"
	"github.com/dorayaki-do/ralfinho/internal/tui"
)

func main() {
	cfg, err := cli.Parse(os.Args[1:])
	if err != nil {
		// Empty message means --help was requested.
		if err.Error() == "" {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "ralfinho: %v\n", err)
		os.Exit(1)
	}

	// Handle "view" subcommand.
	if cfg.ViewRunID != "" {
		fmt.Fprintf(os.Stderr, "ralfinho view: not yet implemented (run-id=%s, runs-dir=%s)\n",
			cfg.ViewRunID, cfg.RunsDir)
		os.Exit(0)
	}

	// Resolve the prompt text.
	promptText, err := resolvePrompt(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ralfinho: %v\n", err)
		os.Exit(1)
	}

	if cfg.NoTUI {
		runPlain(cfg, promptText)
	} else {
		runTUI(cfg, promptText)
	}
}

// runPlain runs the agent with plain stderr output (original behavior).
func runPlain(cfg *cli.Config, promptText string) {
	r := runner.New(runner.RunConfig{
		Agent:         cfg.Agent,
		Prompt:        promptText,
		MaxIterations: cfg.MaxIterations,
		RunsDir:       cfg.RunsDir,
		PromptSource:  cfg.InputMode,
		PromptFile:    cfg.PromptFile,
		PlanFile:      cfg.PlanFile,
	})

	result := r.Run(context.Background())

	fmt.Fprintf(os.Stderr, "\n=== run summary ===\n")
	fmt.Fprintf(os.Stderr, "run-id:     %s\n", result.RunID)
	fmt.Fprintf(os.Stderr, "iterations: %d\n", result.Iterations)
	fmt.Fprintf(os.Stderr, "status:     %s\n", result.Status)

	exitForStatus(result.Status)
}

// runTUI runs the agent with the Bubble Tea TUI.
func runTUI(cfg *cli.Config, promptText string) {
	eventCh := make(chan runner.Event, 256)

	r := runner.New(runner.RunConfig{
		Agent:         cfg.Agent,
		Prompt:        promptText,
		MaxIterations: cfg.MaxIterations,
		RunsDir:       cfg.RunsDir,
		PromptSource:  cfg.InputMode,
		PromptFile:    cfg.PromptFile,
		PlanFile:      cfg.PlanFile,
		EventChan:     eventCh,
	})

	// Start the runner in a goroutine.
	resultCh := make(chan runner.RunResult, 1)
	go func() {
		result := r.Run(context.Background())
		resultCh <- result
		close(eventCh) // signal TUI that no more events are coming
	}()

	model := tui.NewModel(eventCh)
	p := tea.NewProgram(model, tea.WithAltScreen())

	// Feed DoneMsg to the program when the runner finishes.
	go func() {
		result := <-resultCh
		p.Send(tui.DoneMsg{Result: result})
	}()

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "ralfinho: TUI error: %v\n", err)
		os.Exit(1)
	}
}

func exitForStatus(status runner.Status) {
	switch status {
	case runner.StatusFailed:
		os.Exit(1)
	case runner.StatusInterrupted:
		os.Exit(2)
	}
}

// resolvePrompt reads the prompt content based on the CLI config.
func resolvePrompt(cfg *cli.Config) (string, error) {
	switch cfg.InputMode {
	case "prompt":
		return prompt.BuildFromPromptFile(cfg.PromptFile)
	case "plan":
		return prompt.BuildFromPlan(cfg.PlanFile)
	case "default":
		return prompt.BuildDefault(), nil
	default:
		return "", fmt.Errorf("unknown input mode %q", cfg.InputMode)
	}
}
