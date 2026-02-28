# Ralfinho

My take on the [Ralph Wiggum](https://ghuntley.com/ralph/) technique.

> _Aren't there already one hundred million tools for doing this?_
>
> Yes, but where's the fun and what do I learn by just using those?

## Installation

```bash
go install github.com/fsmiamoto/ralfinho/cmd/ralfinho@latest
```

Or install from source:

```bash
just install
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

## Run Artifacts

Each run is saved to `.ralfinho/runs/<uuid>/`:

- `meta.json` — run metadata (status, agent, iterations, timing)
- `events.jsonl` — raw event stream from the agent
- `effective-prompt.md` — the prompt that was sent
- `session.log` — human-readable timestamped log
- `raw-output.log` — raw agent stdout
