// Package cli handles flag parsing and configuration for ralfinho.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"
)

// Config holds the parsed CLI configuration.
type Config struct {
	// Run mode
	PromptFile string // resolved path to prompt file
	PlanFile   string // resolved path to plan file
	InputMode  string // "prompt", "plan", or "default"

	Agent             string         // agent executable name (default: "pi")
	MaxIterations     int            // 0 = unlimited
	InactivityTimeout *time.Duration // nil = not provided on CLI; 0 = disabled; >0 = custom
	NoTUI             bool           // disable TUI / browser TUI when viewing runs
	RunsDir           string         // directory for run storage

	// Subcommand
	ViewRunID   string // non-empty means "view <run-id>" replay mode
	ViewList    bool   // true means "view" without a run-id
	ShowVersion bool   // true means --version was requested
}

// ViewMode is the resolved execution mode for the "view" subcommand.
type ViewMode string

const (
	ViewModeNone    ViewMode = ""
	ViewModeBrowser ViewMode = "browser"
	ViewModeList    ViewMode = "list"
	ViewModeReplay  ViewMode = "replay"
)

const usage = `Usage: ralfinho [flags] [PROMPT_FILE]
       ralfinho view [--runs-dir <path>] [--no-tui] [<run-id>]

An autonomous coding agent runner.

Flags:
  --prompt <file>         Explicit prompt file (conflicts with --plan)
  --plan <file>           Plan file for template-based prompt (conflicts with --prompt)
  -a, --agent <name>      Agent executable (default: "pi")
  -m, --max-iterations <n> Max iterations, 0=unlimited (default: 0)
  --inactivity-timeout <d> Duration with no agent activity before the stuck-detection
                          watchdog fires (e.g. "10m", "1h"). Pass "0" to disable the
                          watchdog entirely — useful when an agent step is expected
                          to be slow. Omit the flag to use the default (5m).
  --no-tui                Disable TUI, use plain stderr output
  --runs-dir <path>       Runs directory (default: ".ralfinho/runs")
  -v, --version           Show version
  -h, --help              Show this help

Subcommands:
  view                    Open the session browser TUI (interactive terminals)
                          or list saved runs (non-TTY / --no-tui)
  view <run-id>           Replay a specific run (supports prefix matching)

Session browser keybindings:
  j/k, arrows             Navigate sessions
  Enter, o                Open selected session in replay viewer
  r                       Resume: start a new run from saved prompt artifacts
  x                       Delete selected session (with confirmation)
  Tab                     Switch focus between sessions and preview panes
  s                       Cycle sort mode (newest/oldest/run id/agent/status/prompt)
  /                       Search sessions by text
  a/t/p/d                 Filter by agent/status/prompt source/date
  c                       Clear all filters and search
  g/G                     Jump to first/last session
  Ctrl+d/u, PgDn/PgUp    Half-page scroll
  q, Esc                  Quit browser
`

// ResolveViewMode returns the concrete execution mode for the "view"
// subcommand after terminal/opt-out decisions are applied.
func (c Config) ResolveViewMode(interactive bool) ViewMode {
	switch {
	case c.ViewRunID != "":
		return ViewModeReplay
	case c.ViewList:
		if interactive && !c.NoTUI {
			return ViewModeBrowser
		}
		return ViewModeList
	default:
		return ViewModeNone
	}
}

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
		promptFlag     string
		planFlag       string
		agentFlag      string
		agentShort     string
		maxIter        string
		maxShort       string
		inactivityFlag string
		noTUI          bool
		runsDir        string
		help           bool
		helpShort      bool
		version        bool
		versionShort   bool
	)

	fs.StringVar(&promptFlag, "prompt", "", "")
	fs.StringVar(&planFlag, "plan", "", "")
	fs.StringVar(&agentFlag, "agent", "", "")
	fs.StringVar(&agentShort, "a", "", "")
	fs.StringVar(&maxIter, "max-iterations", "", "")
	fs.StringVar(&maxShort, "m", "", "")
	fs.StringVar(&inactivityFlag, "inactivity-timeout", "", "")
	fs.BoolVar(&noTUI, "no-tui", false, "")
	fs.StringVar(&runsDir, "runs-dir", ".ralfinho/runs", "")
	fs.BoolVar(&help, "help", false, "")
	fs.BoolVar(&helpShort, "h", false, "")
	fs.BoolVar(&version, "version", false, "")
	fs.BoolVar(&versionShort, "v", false, "")

	if err := fs.Parse(args); err != nil {
		fmt.Fprint(os.Stderr, usage)
		return nil, fmt.Errorf("invalid flags: %w", err)
	}

	if help || helpShort {
		fmt.Fprint(os.Stderr, usage)
		return nil, errors.New("") // signals help-requested; caller exits 0
	}

	if version || versionShort {
		return &Config{ShowVersion: true}, nil
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

	// Resolve inactivity timeout. Empty = flag omitted (caller falls back to
	// config/default). Otherwise parse as a Go duration; "0" explicitly
	// disables the watchdog.
	var inactivityTimeout *time.Duration
	if inactivityFlag != "" {
		d, err := time.ParseDuration(inactivityFlag)
		if err != nil {
			return nil, fmt.Errorf("--inactivity-timeout %q: %w", inactivityFlag, err)
		}
		if d < 0 {
			return nil, fmt.Errorf("--inactivity-timeout must be zero or positive, got %q", inactivityFlag)
		}
		inactivityTimeout = &d
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
		Agent:             agent,
		MaxIterations:     maxIterations,
		InactivityTimeout: inactivityTimeout,
		NoTUI:             noTUI,
		RunsDir:           runsDir,
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

	var (
		runsDir string
		noTUI   bool
	)
	fs.StringVar(&runsDir, "runs-dir", ".ralfinho/runs", "")
	fs.BoolVar(&noTUI, "no-tui", false, "")

	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("invalid view flags: %w", err)
	}

	remaining := fs.Args()
	if len(remaining) > 1 {
		return nil, fmt.Errorf("expected at most one run-id, got %d", len(remaining))
	}
	if len(remaining) == 0 {
		return &Config{
			ViewList: true,
			NoTUI:    noTUI,
			RunsDir:  runsDir,
		}, nil
	}

	return &Config{
		ViewRunID: remaining[0],
		NoTUI:     noTUI,
		RunsDir:   runsDir,
	}, nil
}
