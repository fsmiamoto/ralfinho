# Ralfinho

My take on the [Ralph Wiggum](https://ghuntley.com/ralph/) technique.

> _Aren't there already one hundred million tools for doing this?_
>
> Yes, but what do I learn by just using those?
>
> Paraphrashing [pi](https://pi.dev): There are many Ralphs out there, but this one is mine.

<p align="center">
  <img src="docs/screenshot.png" alt="ralfinho screenshot" width="800">
</p>

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
that as the plan. Otherwise it runs with a built-in self-improvement prompt.

The built-in prompt tells the agent to inspect the current project and keep looping
on useful improvements, a nice way to try out ralfinho for the first time.

### Options

```
--prompt <file>           Explicit prompt file
--plan <file>             Plan file (generates prompt from template)
-a, --agent <name>        Agent backend: "pi", "kiro", or "claude" (default: pi)
-m, --max-iterations <n>  Max iterations, 0=unlimited (default: 0)
--inactivity-timeout <d>  Stuck-detection watchdog duration; 0 disables (default: 5m)
--no-tui                  Disable TUI, plain stderr output
--runs-dir <path>         Runs directory (default: .ralfinho/runs)
```

### Config file

Ralfinho supports both global and project-local TOML config files. In addition
to flag defaults, config can override the built-in `plan` and `default` prompt
templates via a `[templates]` section, using either inline text or `file:`
references.

See [docs/configuration.md](docs/configuration.md) for examples and details.

### Browse and manage past runs

```bash
ralfinho view              # Open session browser TUI (interactive terminals)
ralfinho view <run-id>     # Replay a specific run (supports prefix matching)
ralfinho view --no-tui     # Plain text listing (also used in non-TTY environments)
```

On interactive terminals, `ralfinho view` opens a full-screen session browser
with a sessions list and a metadata preview pane. Sessions are shown newest-first
by default. Runs with missing or corrupt artifacts are included but marked with a
⚠ warning indicator.

## Agent Backends

Ralfinho supports multiple AI agent backends via the `--agent` flag:
[pi](https://pi.dev) (default), [kiro](https://kiro.dev), and
[claude-code](https://code.claude.com).

## TUI Keybindings

During a live run or when viewing a past session:

| Key | Action |
|-----|--------|
| `Tab` | Cycle focus between panes |
| `j`/`k` | Scroll in focused pane |
| `r` | Toggle raw / rendered detail view |
| `p` | Show effective prompt |
| `n` | Show memory files (NOTES.md / PROGRESS.md) |
| `t` | Set inactivity timeout (e.g. `30s`, `0` to disable, `default`) |
| `m` | Add a reminder for the next iteration (`Ctrl+P` toggles persistent, `Ctrl+Enter` applies now via restart) |
| `M` | Remove a pending reminder |
| `?` | Show keybinding help |
| `q` / `Ctrl+C` | Quit (press twice to confirm) |

The memory overlay (`n`) reads files from disk on each open, so it always
shows the latest content written by the agent. Use `Tab` inside the overlay
to switch between NOTES and PROGRESS.

### Session controls

While a run is live, `t` adjusts the inactivity timeout without restarting
ralfinho, and `m` opens an editor to append a steering note to the prompt
the agent sees on its next iteration. One-off reminders are consumed when
the iteration completes; persistent reminders (toggled with `Ctrl+P`)
survive until you remove them with `M`. `Ctrl+Enter` queues the reminder
and immediately restarts the current iteration so the agent picks it up
without waiting for the next turn. Every control action is appended to
`operator-log.jsonl` in the run directory.

## Run Artifacts

Each run is saved to `.ralfinho/runs/<uuid>/`:

- `meta.json` — run metadata (status, agent, iterations, timing)
- `events.jsonl` — raw event stream from the agent
- `effective-prompt.md` — the prompt that was sent at startup
- `operator-log.jsonl` — append-only audit of in-session control actions (timeout changes, reminders, restarts)
- `session.log` — human-readable timestamped log
- `raw-output.log` — raw agent stdout
- `NOTES.md` — agent's cross-iteration notes and decisions
- `PROGRESS.md` — agent's task completion tracking

NOTES.md and PROGRESS.md are the agent's session-scoped memory. They are
created per-run (not in the project root), so multiple runs never clobber
each other's state. When resuming a past session, memory files are
automatically copied into the new run so the agent picks up where it left off.
