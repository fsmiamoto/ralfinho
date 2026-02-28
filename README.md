# Ralfinho

An autonomous coding agent runner with real-time TUI inspection.

My take on the [Ralph Wiggum](https://ghuntley.com/ralph/) technique.

> _Aren't there already one hundred million tools for doing this?_
>
> Yes, but where's the fun and what do I learn by just using those?

## Installation

```bash
go install github.com/dorayaki-do/ralfinho/cmd/ralfinho@latest
```

Or build from source:

```bash
just build
# Binary at ./bin/ralfinho
```

## Usage

### Run with a prompt file

```bash
ralfinho prompt.md
ralfinho --prompt prompt.md
```

### Run with a plan file (template-based prompt)

```bash
ralfinho --plan PLAN.md
```

### Default behavior

If no prompt or plan is given, ralfinho looks for `./PLAN.md`. If found, it uses
that as the plan. Otherwise it runs with a minimal default prompt.

### Options

```
--prompt <file>           Explicit prompt file
--plan <file>             Plan file (generates prompt from template)
-a, --agent <name>        Agent executable (default: pi)
-m, --max-iterations <n>  Max iterations, 0=unlimited (default: 0)
--no-tui                  Disable TUI, plain stderr output
--runs-dir <path>         Runs directory (default: .ralfinho/runs)
```

### View past runs

```bash
ralfinho view              # List all runs
ralfinho view <run-id>     # View a specific run (supports prefix matching)
```

### TUI Keybindings

| Key         | Action                      |
| ----------- | --------------------------- |
| j/k, ↑/↓   | Navigate events             |
| Tab         | Switch pane focus           |
| Ctrl+d/u    | Page detail pane            |
| g/G         | Jump to top/bottom          |
| r           | Toggle raw/rendered         |
| q           | Quit                        |

## Run Artifacts

Each run is saved to `.ralfinho/runs/<uuid>/`:

- `meta.json` — run metadata (status, agent, iterations, timing)
- `events.jsonl` — raw event stream from the agent
- `effective-prompt.md` — the prompt that was sent
- `session.log` — human-readable timestamped log
- `raw-output.log` — raw agent stdout

## How It Works

Ralfinho runs an AI coding agent (default: [pi](https://github.com/mariozechner/pi-coding-agent))
in an iteration loop. Each iteration sends the prompt to the agent and monitors
its JSON output stream. The agent signals completion by outputting
`<promise>COMPLETE</promise>`.

The TUI provides a two-pane view: an event stream on top (showing tool calls,
assistant text, iteration boundaries) and a detail pane on the bottom (showing
full content for the selected event, with markdown rendering for assistant text).

In non-TTY environments (CI, pipes), the TUI is automatically disabled and
output goes to stderr as plain text.
