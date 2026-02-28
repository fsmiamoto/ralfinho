// Command ralfinho is an autonomous coding agent runner.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dorayaki-do/ralfinho/internal/cli"
	"github.com/dorayaki-do/ralfinho/internal/prompt"
	"github.com/dorayaki-do/ralfinho/internal/runner"
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
	prompt, err := resolvePrompt(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ralfinho: %v\n", err)
		os.Exit(1)
	}

	// Build the runner.
	r := runner.New(runner.RunConfig{
		Agent:         cfg.Agent,
		Prompt:        prompt,
		MaxIterations: cfg.MaxIterations,
		RunsDir:       cfg.RunsDir,
		PromptSource:  cfg.InputMode,
		PromptFile:    cfg.PromptFile,
		PlanFile:      cfg.PlanFile,
	})

	result := r.Run(context.Background())

	// Print summary.
	fmt.Fprintf(os.Stderr, "\n=== run summary ===\n")
	fmt.Fprintf(os.Stderr, "run-id:     %s\n", result.RunID)
	fmt.Fprintf(os.Stderr, "iterations: %d\n", result.Iterations)
	fmt.Fprintf(os.Stderr, "status:     %s\n", result.Status)

	switch result.Status {
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
