// Package cli handles flag parsing and configuration for ralfinho.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
)

// Config holds the parsed CLI configuration.
type Config struct {
	// Run mode
	PromptFile string // resolved path to prompt file
	PlanFile   string // resolved path to plan file
	InputMode  string // "prompt", "plan", or "default"

	Agent         string // agent executable name (default: "pi")
	MaxIterations int    // 0 = unlimited
	NoTUI         bool   // disable TUI
	RunsDir       string // directory for run storage

	// Subcommand
	ViewRunID string // non-empty means "view <run-id>" subcommand
	ViewList  bool   // true means "view" without a run-id (list mode)
}

const usage = `Usage: ralfinho [flags] [PROMPT_FILE]
       ralfinho view [--runs-dir <path>] <run-id>

An autonomous coding agent runner.

Flags:
  --prompt <file>         Explicit prompt file (conflicts with --plan)
  --plan <file>           Plan file for template-based prompt (conflicts with --prompt)
  -a, --agent <name>      Agent executable (default: "pi")
  -m, --max-iterations <n> Max iterations, 0=unlimited (default: 0)
  --no-tui                Disable TUI, use plain stderr output
  --runs-dir <path>       Runs directory (default: ".ralfinho/runs")
  -h, --help              Show this help

Subcommands:
  view <run-id>           View a past run
`

// Parse parses command-line arguments and returns a Config.
// It writes usage/error output to stderr and returns an error
// if the arguments are invalid. A nil error with showHelp=true
// means the caller should exit 0.
func Parse(args []string) (*Config, error) {
	if len(args) > 0 && args[0] == "view" {
		return parseView(args[1:])
	}

	fs := flag.NewFlagSet("ralfinho", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // we handle output ourselves

	var (
		promptFlag string
		planFlag   string
		agentFlag  string
		agentShort string
		maxIter    string
		maxShort   string
		noTUI      bool
		runsDir    string
		help       bool
		helpShort  bool
	)

	fs.StringVar(&promptFlag, "prompt", "", "")
	fs.StringVar(&planFlag, "plan", "", "")
	fs.StringVar(&agentFlag, "agent", "", "")
	fs.StringVar(&agentShort, "a", "", "")
	fs.StringVar(&maxIter, "max-iterations", "", "")
	fs.StringVar(&maxShort, "m", "", "")
	fs.BoolVar(&noTUI, "no-tui", false, "")
	fs.StringVar(&runsDir, "runs-dir", ".ralfinho/runs", "")
	fs.BoolVar(&help, "help", false, "")
	fs.BoolVar(&helpShort, "h", false, "")

	if err := fs.Parse(args); err != nil {
		fmt.Fprint(os.Stderr, usage)
		return nil, fmt.Errorf("invalid flags: %w", err)
	}

	if help || helpShort {
		fmt.Fprint(os.Stderr, usage)
		return nil, errors.New("") // signals help-requested; caller exits 0
	}

	// Resolve agent: short flag wins if set, then long flag, then default.
	agent := "pi"
	if agentFlag != "" {
		agent = agentFlag
	}
	if agentShort != "" {
		agent = agentShort
	}

	// Resolve max-iterations.
	maxIterations := 0
	raw := maxIter
	if maxShort != "" {
		raw = maxShort
	}
	if raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("--max-iterations must be a non-negative integer, got %q", raw)
		}
		maxIterations = n
	}

	// Conflict check.
	if promptFlag != "" && planFlag != "" {
		return nil, fmt.Errorf("--prompt and --plan are mutually exclusive")
	}

	positional := fs.Args()
	if promptFlag != "" && len(positional) > 0 {
		return nil, fmt.Errorf("unexpected positional argument %q with --prompt", positional[0])
	}
	if planFlag != "" && len(positional) > 0 {
		return nil, fmt.Errorf("unexpected positional argument %q with --plan", positional[0])
	}
	if len(positional) > 1 {
		return nil, fmt.Errorf("expected at most one prompt file, got %d", len(positional))
	}

	// Determine input mode and file.
	cfg := &Config{
		Agent:         agent,
		MaxIterations: maxIterations,
		NoTUI:         noTUI,
		RunsDir:       runsDir,
	}

	switch {
	case promptFlag != "":
		cfg.InputMode = "prompt"
		cfg.PromptFile = promptFlag
	case len(positional) > 0:
		cfg.InputMode = "prompt"
		cfg.PromptFile = positional[0]
	case planFlag != "":
		cfg.InputMode = "plan"
		cfg.PlanFile = planFlag
	default:
		// Fallback: look for ./PLAN.md
		if _, err := os.Stat("PLAN.md"); err == nil {
			cfg.InputMode = "plan"
			cfg.PlanFile = "PLAN.md"
		} else {
			cfg.InputMode = "default"
		}
	}

	return cfg, nil
}

func parseView(args []string) (*Config, error) {
	fs := flag.NewFlagSet("view", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var runsDir string
	fs.StringVar(&runsDir, "runs-dir", ".ralfinho/runs", "")

	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("invalid view flags: %w", err)
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		return &Config{
			ViewList: true,
			RunsDir:  runsDir,
		}, nil
	}

	return &Config{
		ViewRunID: remaining[0],
		RunsDir:   runsDir,
	}, nil
}
