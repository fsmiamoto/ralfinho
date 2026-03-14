// Command ralfinho is an autonomous coding agent runner.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		fmt.Fprintf(os.Stderr, "ralfinho: unknown agent %q (supported: pi, kiro, claude)\n", cfg.Agent)
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

	printRunSummary("run summary", result)
	exitForStatus(result.Status)
}

// runTUI runs the agent with the Bubble Tea TUI.
func runTUI(cfg *cli.Config, promptText string) {
	result, err := runAgentWithTUI(runner.RunConfig{
		Agent:         cfg.Agent,
		Prompt:        promptText,
		MaxIterations: cfg.MaxIterations,
		RunsDir:       cfg.RunsDir,
		PromptSource:  cfg.InputMode,
		PromptFile:    cfg.PromptFile,
		PlanFile:      cfg.PlanFile,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ralfinho: %v\n", err)
		os.Exit(1)
	}

	printRunSummary("run summary", result)
	exitForStatus(result.Status)
}

// runAgentWithTUI runs the agent in a background goroutine with a Bubble Tea
// TUI in the foreground. It handles context cancellation, event forwarding,
// and waiting for the runner to finish writing artifacts when the user quits
// the TUI early. The caller is responsible for printing the result summary.
func runAgentWithTUI(runCfg runner.RunConfig) (runner.RunResult, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh := make(chan runner.Event, 256)
	runCfg.EventChan = eventCh

	r := runner.New(runCfg)

	// Start the runner in a goroutine. Use a close-signal pattern instead
	// of a buffered channel so both the DoneMsg goroutine and the early-quit
	// path can safely read the result without racing for a single channel value.
	var runResult runner.RunResult
	runDone := make(chan struct{})
	go func() {
		runResult = r.Run(ctx)
		close(runDone)
		close(eventCh) // signal TUI that no more events are coming
	}()

	model := tui.NewModel(eventCh)
	p := tea.NewProgram(model, tea.WithAltScreen())

	// Feed DoneMsg to the program when the runner finishes.
	go func() {
		<-runDone
		p.Send(tui.DoneMsg{Result: runResult})
	}()

	finalModel, err := p.Run()
	if err != nil {
		return runner.RunResult{}, fmt.Errorf("TUI error: %v", err)
	}

	if m, ok := finalModel.(tui.Model); ok {
		if res := m.RunResult(); res != nil {
			return *res, nil
		}
		// User quit before runner finished — cancel the runner and
		// wait for it to write meta.json before returning.
		cancel()
		<-runDone
	}

	return runResult, nil
}

// printRunSummary prints a run summary to stderr.
func printRunSummary(label string, result runner.RunResult) {
	fmt.Fprintf(os.Stderr, "\n=== %s ===\n", label)
	fmt.Fprintf(os.Stderr, "run-id:     %s\n", result.RunID)
	fmt.Fprintf(os.Stderr, "agent:      %s\n", result.Agent)
	fmt.Fprintf(os.Stderr, "iterations: %d\n", result.Iterations)
	fmt.Fprintf(os.Stderr, "status:     %s\n", result.Status)
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
		case tui.BrowserActionResume:
			lastSelectedRunID = result.RunID
			if err := resumeRunFromBrowser(cfg, result); err != nil {
				fmt.Fprintf(os.Stderr, "ralfinho view: resume: %v\n", err)
			}
			// Loop back to re-open the browser (the new run now appears
			// in the session list after the rescan).
		case tui.BrowserActionDelete:
			if result.DeleteDir != "" && isSubdir(cfg.RunsDir, result.DeleteDir) {
				if err := os.RemoveAll(result.DeleteDir); err != nil {
					fmt.Fprintf(os.Stderr, "ralfinho view: delete: %v\n", err)
				}
			}
			lastSelectedRunID = result.DeleteNextRunID
			// Loop back to re-open the browser; the deleted run
			// disappears after the rescan.
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

// resumeRunFromBrowser launches a fresh run using prompt artifacts recovered
// from a previous session. It blocks until the TUI-driven run finishes (or the
// user quits early) and then returns so the browser loop can reopen.
func resumeRunFromBrowser(cfg *cli.Config, result tui.BrowserResult) error {
	promptText, err := resolveResumePrompt(result.ResumeSource, result.ResumePath)
	if err != nil {
		return fmt.Errorf("resolving prompt: %w", err)
	}

	agentName := result.ResumeAgent
	if agentName == "" || agentName == "unknown" {
		agentName = cfg.Agent
	}
	if !agent.IsValid(agentName) {
		return fmt.Errorf("unknown agent %q from saved run", agentName)
	}

	// Map the resume source to runner metadata so the new run's meta.json
	// accurately describes how the prompt was obtained.
	inputMode, promptFile, planFile := resumePromptMeta(result.ResumeSource, result.ResumePath)

	runResult, err := runAgentWithTUI(runner.RunConfig{
		Agent:         agentName,
		Prompt:        promptText,
		MaxIterations: cfg.MaxIterations,
		RunsDir:       cfg.RunsDir,
		PromptSource:  inputMode,
		PromptFile:    promptFile,
		PlanFile:      planFile,
	})
	if err != nil {
		return err
	}

	printRunSummary("resumed run summary", runResult)
	return nil
}

// resolveResumePrompt reads the prompt text for a resumed run based on the
// saved artifact source. It never tries to restore an in-progress backend
// session; the result is always a fresh prompt string for a new run.
func resolveResumePrompt(source viewer.ResumeSource, path string) (string, error) {
	switch source {
	case viewer.ResumeSourceEffectivePrompt:
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("reading effective prompt %q: %w", path, err)
		}
		return string(data), nil
	case viewer.ResumeSourcePromptFile:
		return prompt.BuildFromPromptFile(path)
	case viewer.ResumeSourcePlanFile:
		return prompt.BuildFromPlan(path)
	case viewer.ResumeSourceDefault:
		return prompt.BuildDefault(), nil
	default:
		return "", fmt.Errorf("unknown resume source %q", source)
	}
}

// resumePromptMeta maps a resume source to the runner metadata fields so the
// new run's meta.json accurately reflects how the prompt was obtained.
func resumePromptMeta(source viewer.ResumeSource, path string) (inputMode, promptFile, planFile string) {
	switch source {
	case viewer.ResumeSourceEffectivePrompt:
		return "prompt", "", ""
	case viewer.ResumeSourcePromptFile:
		return "prompt", path, ""
	case viewer.ResumeSourcePlanFile:
		return "plan", "", path
	case viewer.ResumeSourceDefault:
		return "default", "", ""
	default:
		return "prompt", "", ""
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

// isSubdir reports whether child is a direct subdirectory of parent.
// Both paths are cleaned before comparison to prevent path traversal.
func isSubdir(parent, child string) bool {
	parent = filepath.Clean(parent) + string(filepath.Separator)
	child = filepath.Clean(child)
	return strings.HasPrefix(child, parent) && !strings.Contains(child[len(parent):], string(filepath.Separator))
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
