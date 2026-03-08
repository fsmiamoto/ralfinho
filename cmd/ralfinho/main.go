// Command ralfinho is an autonomous coding agent runner.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/term"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fsmiamoto/ralfinho/internal/agent"
	"github.com/fsmiamoto/ralfinho/internal/cli"
	"github.com/fsmiamoto/ralfinho/internal/prompt"
	"github.com/fsmiamoto/ralfinho/internal/runner"
	"github.com/fsmiamoto/ralfinho/internal/tui"
	"github.com/fsmiamoto/ralfinho/internal/viewer"
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

	// Handle --version flag.
	if cfg.ShowVersion {
		fmt.Printf("ralfinho %s\n", cli.Version)
		return
	}

	// Handle "view" subcommand.
	switch cfg.ResolveViewMode(isViewInteractiveTerminal()) {
	case cli.ViewModeBrowser:
		runBrowser(cfg)
		return
	case cli.ViewModeList:
		listRuns(cfg)
		return
	case cli.ViewModeReplay:
		runViewer(cfg)
		return
	}

	// Validate agent name early (before creating run dirs / prompt resolution).
	if !agent.IsValid(cfg.Agent) {
		fmt.Fprintf(os.Stderr, "ralfinho: unknown agent %q (supported: pi, kiro)\n", cfg.Agent)
		os.Exit(1)
	}

	// Resolve the prompt text.
	promptText, err := resolvePrompt(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ralfinho: %v\n", err)
		os.Exit(1)
	}

	// Auto-disable TUI when not connected to a terminal.
	if !cfg.NoTUI && !isTerminal() {
		cfg.NoTUI = true
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
	fmt.Fprintf(os.Stderr, "agent:      %s\n", result.Agent)
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

	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ralfinho: TUI error: %v\n", err)
		os.Exit(1)
	}

	// Print session summary to stderr.
	if m, ok := finalModel.(tui.Model); ok {
		if r := m.RunResult(); r != nil {
			fmt.Fprintf(os.Stderr, "\n=== run summary ===\n")
			fmt.Fprintf(os.Stderr, "run-id:     %s\n", r.RunID)
			fmt.Fprintf(os.Stderr, "agent:      %s\n", r.Agent)
			fmt.Fprintf(os.Stderr, "iterations: %d\n", r.Iterations)
			fmt.Fprintf(os.Stderr, "status:     %s\n", r.Status)
			exitForStatus(r.Status)
		} else {
			// User quit before runner finished — try to get result with a short timeout.
			select {
			case result := <-resultCh:
				fmt.Fprintf(os.Stderr, "\n=== run summary ===\n")
				fmt.Fprintf(os.Stderr, "run-id:     %s\n", result.RunID)
				fmt.Fprintf(os.Stderr, "agent:      %s\n", result.Agent)
				fmt.Fprintf(os.Stderr, "iterations: %d\n", result.Iterations)
				fmt.Fprintf(os.Stderr, "status:     %s\n", result.Status)
				exitForStatus(result.Status)
			case <-time.After(500 * time.Millisecond):
				// Runner still going; exit without summary.
			}
		}
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

// runViewer loads a saved run and opens it in a read-only TUI.
func runViewer(cfg *cli.Config) {
	if err := openRunViewer(cfg.RunsDir, cfg.ViewRunID); err != nil {
		fmt.Fprintf(os.Stderr, "ralfinho view: %v\n", err)
		os.Exit(1)
	}
}

// openRunViewer loads a single saved run and opens the replay TUI.
// It returns when the user exits the viewer.
func openRunViewer(runsDir, runID string) error {
	saved, err := viewer.LoadRun(runsDir, runID)
	if err != nil {
		return err
	}

	conv := tui.NewEventConverter()
	var displayEvents []tui.DisplayEvent
	for i := range saved.Events {
		des := conv.Convert(&saved.Events[i])
		displayEvents = append(displayEvents, des...)
	}

	model := tui.NewViewerModel(displayEvents, saved.Meta)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %v", err)
	}
	return nil
}

// runBrowser loads saved runs and opens the interactive session browser.
// When the user opens a session, the browser dispatches to the replay viewer
// and re-opens the browser afterward with the same session selected.
func runBrowser(cfg *cli.Config) {
	var lastSelectedRunID string

	for {
		summaries, err := viewer.ListRunSummaries(cfg.RunsDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ralfinho view: %v\n", err)
			os.Exit(1)
		}

		model := tui.NewBrowserModel(summaries)
		if lastSelectedRunID != "" {
			model = model.WithSelectedRunID(lastSelectedRunID)
		}

		p := tea.NewProgram(model, tea.WithAltScreen())

		finalModel, err := p.Run()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ralfinho: TUI error: %v\n", err)
			os.Exit(1)
		}

		m, ok := finalModel.(tui.BrowserModel)
		if !ok {
			return
		}

		result := m.Result()
		switch result.Action {
		case tui.BrowserActionOpen:
			lastSelectedRunID = result.RunID
			if err := openRunViewer(cfg.RunsDir, result.RunID); err != nil {
				fmt.Fprintf(os.Stderr, "ralfinho view: %v\n", err)
				// Don't exit — return to the browser so the user can try
				// another session or quit normally.
			}
			// Loop back to re-open the browser.
		default:
			return
		}
	}
}

// listRuns prints a readable summary of all available runs.
func listRuns(cfg *cli.Config) {
	summaries, err := viewer.ListRunSummaries(cfg.RunsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ralfinho view: %v\n", err)
		os.Exit(1)
	}

	if len(summaries) == 0 {
		fmt.Println("No runs found.")
		return
	}

	fmt.Println("Available runs:")
	for _, summary := range summaries {
		fmt.Println(formatRunSummary(summary))
	}
}

func formatRunSummary(summary viewer.RunSummary) string {
	id := summary.RunID
	if len(id) > 8 {
		id = id[:8]
	}

	details := fmt.Sprintf("%d iterations  (%s)", summary.IterationsCompleted, summary.PromptLabel)
	if summary.ArtifactError != "" {
		details = summary.ArtifactError
	}

	return fmt.Sprintf("  %s  %s  %-5s %-22s %s",
		id,
		formatRunSummaryDate(summary),
		summary.Agent,
		summary.Status,
		details,
	)
}

func formatRunSummaryDate(summary viewer.RunSummary) string {
	switch {
	case !summary.StartedAt.IsZero():
		return summary.StartedAt.Format("2006-01-02 15:04")
	case summary.StartedAtText != "":
		return formatMetaDate(summary.StartedAtText)
	case !summary.SortTime.IsZero():
		return summary.SortTime.Format("2006-01-02 15:04")
	default:
		return "unknown"
	}
}

// formatMetaDate parses an RFC3339 timestamp and returns a short date string.
func formatMetaDate(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			if len(s) >= 16 {
				return s[:16]
			}
			return s
		}
	}
	return t.Format("2006-01-02 15:04")
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

// isTerminal reports whether stderr is connected to a terminal.
func isTerminal() bool {
	return term.IsTerminal(int(os.Stderr.Fd()))
}

// isViewInteractiveTerminal reports whether `ralfinho view` can safely launch
// an interactive browser instead of printing plain text.
func isViewInteractiveTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}
