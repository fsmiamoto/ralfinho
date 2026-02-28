package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"ralfinho/internal/eventlog"
	"ralfinho/internal/promptinput"
	"ralfinho/internal/runner"
	"ralfinho/internal/runstore"
	"ralfinho/internal/tui"
)

const defaultRunsDir = ".ralfinho/runs"

var (
	errRunHelp  = errors.New("run help requested")
	errViewHelp = errors.New("view help requested")
)

type commandType string

const (
	commandRun  commandType = "run"
	commandView commandType = "view"
)

type runOptions struct {
	promptFile         string
	planFile           string
	promptTemplateFile string
	positionalPrompt   string
	agent              string
	maxIterations      int
	noTUI              bool
	runsDir            string
}

type viewOptions struct {
	runID   string
	runsDir string
}

type cliOptions struct {
	command commandType
	run     runOptions
	view    viewOptions
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	opts, err := parseCLI(os.Args[1:])
	if err != nil {
		switch {
		case errors.Is(err, errRunHelp):
			fmt.Fprint(os.Stdout, runUsage())
			return nil
		case errors.Is(err, errViewHelp):
			fmt.Fprint(os.Stdout, viewUsage())
			return nil
		default:
			return err
		}
	}

	switch opts.command {
	case commandRun:
		return runCommand(opts.run)
	case commandView:
		return viewCommand(opts.view)
	default:
		return fmt.Errorf("unsupported command %q", opts.command)
	}
}

func runCommand(opts runOptions) error {
	resolution, err := promptinput.ResolveAndBuild(promptinput.ResolveInput{
		PromptFlag:         opts.promptFile,
		PositionalPrompt:   opts.positionalPrompt,
		PlanFlag:           opts.planFile,
		PromptTemplateFlag: opts.promptTemplateFile,
	})
	if err != nil {
		return err
	}

	runID, runDir, err := runstore.CreateRunDir(opts.runsDir)
	if err != nil {
		return err
	}

	if _, err := promptinput.WriteEffectivePrompt(runDir, resolution.EffectivePrompt); err != nil {
		return err
	}

	artifacts, err := runstore.OpenArtifacts(runDir)
	if err != nil {
		return err
	}
	defer artifacts.Close()

	if !opts.noTUI && !isTerminal(os.Stdout) {
		opts.noTUI = true
		fmt.Fprintln(os.Stderr, "stdout is not a terminal; falling back to --no-tui")
	}

	meta := runstore.Meta{
		RunID:         runID,
		StartedAt:     time.Now(),
		Status:        "running",
		Agent:         opts.agent,
		PromptSource:  string(resolution.Source),
		PromptFile:    resolution.PromptFile,
		PlanFile:      resolution.PlanFile,
		MaxIterations: opts.maxIterations,
	}
	if err := runstore.WriteMeta(runDir, meta); err != nil {
		return err
	}

	result, eventsCount, runErr := executeRun(opts, resolution.EffectivePrompt, runID, meta, artifacts)

	meta.EndedAt = time.Now()
	meta.IterationsCompleted = result.IterationsCompleted
	meta.EventsCount = eventsCount
	if runErr != nil {
		meta.Status = string(runner.StatusFailed)
	} else {
		meta.Status = string(result.Status)
	}
	if err := runstore.WriteMeta(runDir, meta); err != nil {
		return err
	}

	if runErr != nil {
		return fmt.Errorf("run %s failed: %w", runID, runErr)
	}

	switch result.Status {
	case runner.StatusCompleted:
		fmt.Printf("Run %s completed after %d iteration(s).\n", runID, result.IterationsCompleted)
	case runner.StatusMaxIterationsReached:
		fmt.Printf("Run %s reached max iterations (%d).\n", runID, opts.maxIterations)
	case runner.StatusInterrupted:
		fmt.Printf("Run %s interrupted after %d completed iteration(s).\n", runID, result.IterationsCompleted)
	default:
		fmt.Printf("Run %s ended with status %s.\n", runID, result.Status)
	}
	fmt.Printf("Artifacts: %s\n", runDir)
	return nil
}

func executeRun(opts runOptions, effectivePrompt, runID string, meta runstore.Meta, artifacts *runstore.Artifacts) (runner.Result, int, error) {
	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt)
	defer signal.Stop(signalCh)

	interruptCh := make(chan struct{}, 1)
	go func() {
		for range signalCh {
			select {
			case interruptCh <- struct{}{}:
			default:
			}
		}
	}()

	memoryEvents := make([]eventlog.Event, 0, 128)
	var artifactErr error
	var stateMu sync.Mutex
	recordArtifactErr := func(err error) {
		if err == nil {
			return
		}
		stateMu.Lock()
		defer stateMu.Unlock()
		if artifactErr == nil {
			artifactErr = err
		}
	}
	appendEvents := func(events []eventlog.Event) {
		if len(events) == 0 {
			return
		}
		stateMu.Lock()
		memoryEvents = append(memoryEvents, events...)
		stateMu.Unlock()
		recordArtifactErr(artifacts.AppendEvents(events))
	}
	appendIteration := func(report runner.IterationReport, parseEvents bool) []eventlog.Event {
		recordArtifactErr(artifacts.AppendRawOutput(report.Iteration, report.Output))
		events := []eventlog.Event{}
		if parseEvents {
			events = eventlog.ParseOutput(report.Output, report.Iteration, time.Now())
			appendEvents(events)
		}
		if report.Interrupted {
			recordArtifactErr(artifacts.AppendSessionLine(fmt.Sprintf("iteration %d interrupted", report.Iteration)))
			return events
		}
		if report.Err != nil {
			recordArtifactErr(artifacts.AppendSessionLine(fmt.Sprintf("iteration %d failed: %v", report.Iteration, report.Err)))
			return events
		}
		recordArtifactErr(artifacts.AppendSessionLine(fmt.Sprintf("iteration %d completed (%d parsed events)", report.Iteration, len(events))))
		return events
	}

	if opts.noTUI {
		result, runErr := runner.Run(context.Background(), runner.Config{
			Agent:         opts.agent,
			Prompt:        effectivePrompt,
			MaxIterations: opts.maxIterations,
			SleepBetween:  2 * time.Second,
			Interrupt:     interruptCh,
			OnInterrupt: func() (bool, error) {
				cont, err := promptContinue(os.Stdin, os.Stdout)
				if err == nil {
					decision := "stop"
					if cont {
						decision = "continue"
					}
					recordArtifactErr(artifacts.AppendSessionLine(fmt.Sprintf("interrupt received; decision=%s", decision)))
				}
				return cont, err
			},
			OnIteration: func(report runner.IterationReport) {
				appendIteration(report, true)
			},
		}, runner.ExecOnce)
		stateMu.Lock()
		eventsCount := len(memoryEvents)
		stateMu.Unlock()
		if artifactErr != nil && runErr == nil {
			runErr = fmt.Errorf("artifact persistence failed: %w", artifactErr)
		}
		return result, eventsCount, runErr
	}

	continueCh := make(chan bool, 1)
	model := tui.NewLiveModel(runID, meta, continueCh, interruptCh)
	program := tea.NewProgram(model, tea.WithAltScreen())

	execFn := func(ctx context.Context, iteration int, agent, prompt string) (string, error) {
		return runner.ExecOnceStream(ctx, agent, prompt, func(line string) {
			events := eventlog.ParseOutput(line, iteration, time.Now())
			appendEvents(events)
			if len(events) > 0 {
				program.Send(tui.StreamEventsMessage{Events: events})
			}
		})
	}

	var wg sync.WaitGroup
	var result runner.Result
	var runErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		result, runErr = runner.Run(context.Background(), runner.Config{
			Agent:         opts.agent,
			Prompt:        effectivePrompt,
			MaxIterations: opts.maxIterations,
			SleepBetween:  2 * time.Second,
			Interrupt:     interruptCh,
			OnInterrupt: func() (bool, error) {
				program.Send(tui.ContinuePromptMessage{})
				cont := <-continueCh
				decision := "stop"
				if cont {
					decision = "continue"
				}
				recordArtifactErr(artifacts.AppendSessionLine(fmt.Sprintf("interrupt received; decision=%s", decision)))
				return cont, nil
			},
			OnIteration: func(report runner.IterationReport) {
				appendIteration(report, false)
				program.Send(tui.IterationMessage{Report: report})
			},
		}, execFn)
		program.Send(tui.RunFinishedMessage{Result: result, Err: runErr})
	}()

	_, tuiErr := program.Run()
	if tuiErr != nil {
		return runner.Result{}, len(memoryEvents), tuiErr
	}
	wg.Wait()

	stateMu.Lock()
	eventsCount := len(memoryEvents)
	stateMu.Unlock()
	if artifactErr != nil && runErr == nil {
		runErr = fmt.Errorf("artifact persistence failed: %w", artifactErr)
	}
	return result, eventsCount, runErr
}

func promptContinue(in io.Reader, out io.Writer) (bool, error) {
	reader := bufio.NewReader(in)
	for {
		if _, err := fmt.Fprint(out, "\nContinue to next iteration? [y/n]: "); err != nil {
			return false, err
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, err
		}

		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			if _, err := fmt.Fprintln(out, "Please answer y or n."); err != nil {
				return false, err
			}
		}
	}
}

func viewCommand(opts viewOptions) error {
	runDir := filepath.Join(opts.runsDir, opts.runID)
	meta, err := runstore.ReadMeta(runDir)
	if err != nil {
		return err
	}
	events, err := runstore.ReadEvents(runDir)
	if err != nil {
		return err
	}

	model := tui.NewViewModel(opts.runID, meta, events)
	program := tea.NewProgram(model, tea.WithAltScreen())
	_, err = program.Run()
	return err
}

func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func parseCLI(args []string) (cliOptions, error) {
	if len(args) > 0 && args[0] == string(commandView) {
		if hasHelpFlag(args[1:]) {
			return cliOptions{}, errViewHelp
		}
		view, err := parseViewArgs(args[1:])
		if err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return cliOptions{}, errViewHelp
			}
			return cliOptions{}, err
		}
		return cliOptions{command: commandView, view: view}, nil
	}

	if hasHelpFlag(args) {
		return cliOptions{}, errRunHelp
	}
	runOpts, err := parseRunArgs(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return cliOptions{}, errRunHelp
		}
		return cliOptions{}, err
	}

	return cliOptions{command: commandRun, run: runOpts}, nil
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" || arg == "help" {
			return true
		}
	}
	return false
}

func runUsage() string {
	return `Usage:
  ralfinho [options] [prompt-file]
  ralfinho view [options] <run-id>

Run options:
  --prompt <path>           Path to prompt file
  --plan <path>             Path to plan file
  --prompt-template <path>  Path to prompt template file
  --agent, -a <value>       Agent executable or profile (default: pi)
  --max-iterations, -m <n>  Maximum iterations (0 for unlimited)
  --no-tui                  Disable TUI output
  --runs-dir <path>         Runs directory (default: .ralfinho/runs)

View options:
  --runs-dir <path>         Runs directory (default: .ralfinho/runs)
`
}

func viewUsage() string {
	return `Usage:
  ralfinho view [--runs-dir <path>] <run-id>
`
}

func parseRunArgs(args []string) (runOptions, error) {
	fs := flag.NewFlagSet("ralfinho", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	opts := runOptions{}
	fs.StringVar(&opts.promptFile, "prompt", "", "Path to prompt file")
	fs.StringVar(&opts.planFile, "plan", "", "Path to plan file")
	fs.StringVar(&opts.promptTemplateFile, "prompt-template", "", "Path to prompt template file (supports {{PLAN_INSTRUCTION}} and {{PLAN_PATH}})")
	fs.StringVar(&opts.agent, "agent", "pi", "Agent executable or profile")
	fs.StringVar(&opts.agent, "a", "pi", "Agent executable or profile")
	fs.IntVar(&opts.maxIterations, "max-iterations", 0, "Maximum iterations (0 for unlimited)")
	fs.IntVar(&opts.maxIterations, "m", 0, "Maximum iterations (0 for unlimited)")
	fs.BoolVar(&opts.noTUI, "no-tui", false, "Disable TUI output")
	fs.StringVar(&opts.runsDir, "runs-dir", defaultRunsDir, "Runs directory")

	if err := fs.Parse(args); err != nil {
		return runOptions{}, err
	}

	remaining := fs.Args()
	if len(remaining) > 1 {
		return runOptions{}, errors.New("too many positional arguments: expected at most one prompt file")
	}
	if len(remaining) == 1 {
		opts.positionalPrompt = remaining[0]
	}

	if opts.promptFile != "" && opts.planFile != "" {
		return runOptions{}, errors.New("--prompt and --plan cannot be used together")
	}

	if opts.promptTemplateFile != "" && (opts.promptFile != "" || opts.positionalPrompt != "") {
		return runOptions{}, errors.New("--prompt-template cannot be used with --prompt or positional prompt file")
	}

	if opts.maxIterations < 0 {
		return runOptions{}, errors.New("--max-iterations must be >= 0")
	}

	if opts.runsDir == "" {
		return runOptions{}, errors.New("--runs-dir cannot be empty")
	}

	return opts, nil
}

func parseViewArgs(args []string) (viewOptions, error) {
	fs := flag.NewFlagSet("ralfinho view", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	opts := viewOptions{}
	fs.StringVar(&opts.runsDir, "runs-dir", defaultRunsDir, "Runs directory")

	if err := fs.Parse(args); err != nil {
		return viewOptions{}, err
	}

	remaining := fs.Args()
	if len(remaining) != 1 {
		return viewOptions{}, errors.New("usage: ralfinho view [--runs-dir <path>] <run-id>")
	}

	opts.runID = remaining[0]
	if opts.runID == "" {
		return viewOptions{}, errors.New("run-id cannot be empty")
	}
	if opts.runsDir == "" {
		return viewOptions{}, errors.New("--runs-dir cannot be empty")
	}

	return opts, nil
}
