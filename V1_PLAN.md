# Ralfinho v1 Plan

## Objective
Build `ralfinho`, a Go-based autonomous coding agent runner with a default TUI focused on real-time inspection, interruption control, and run history.

---

## Confirmed Requirements

1. Primary use case: autonomous coding agent runner.
2. Must make agent behavior easy to inspect (remove noisy/truncated JSON experience).
3. Build as a proper Go binary.
4. TUI should be enabled by default.
5. TUI should stream continuously and allow scrolling.
6. Assistant text should render markdown when possible.
7. Tool results can remain raw initially.
8. Persist runs in `.ralfinho/runs/<uuid>/`.
9. Support viewing past runs via `ralfinho view <run-id>`.
10. Single-agent flow for now.
11. On interruption/pause, ask confirmation to continue iteration (`y/n`).
12. No built-in safety policy (sandboxing is external).
13. Keep event data in memory during active session.
14. **New requirement:** accept either a full prompt file or a plan file and construct the effective prompt from a template (like current `PROMPT.md`).

---

## CLI Shape (v1)

- Binary: `ralfinho`
- Default command: run loop
- Subcommand: `ralfinho view <run-id>`

### Run flags
- `ralfinho [PROMPT_FILE]`
- `--prompt <file>` explicit prompt file
- `--plan <file>` plan file to inject into template-generated prompt
- `-a, --agent <name>` (default: `pi`)
- `-m, --max-iterations <n>` (default: `0` = unlimited)
- `--no-tui` for plain output
- `--runs-dir <path>` (default: `.ralfinho/runs`)

### Input precedence
1. `--prompt` (highest)
2. positional prompt file
3. `--plan` (template-based prompt generation)
4. fallback default prompt template + `./PLAN.md` if it exists

If both `--prompt` and `--plan` are set, return a CLI error.

---

## Prompt Builder (New)

### Goal
When `--plan <file>` is passed, generate an effective prompt that follows the same style/instructions as `PROMPT.md`.

### Approach
- Add an internal prompt template (string constant or external `templates/default_prompt.md.tmpl`).
- Inject plan path into the template instructions (e.g., `Study ./<plan-path>`).
- Preserve the same operational contract as current prompt, including stop condition `<promise>COMPLETE</promise>`.
- Save generated prompt to run artifacts for auditability:
  - `.ralfinho/runs/<uuid>/effective-prompt.md`

### Optional extension (post-v1)
- `--prompt-template <file>` to allow custom template override.

---

## TUI v1 Design (Bubble Tea)

### Layout
- Top pane: live event stream (selectable, scrollable)
- Bottom pane: selected event details (scrollable)

### Rendering
- Assistant text: markdown-rendered
- Tool lines: compact summary in stream
- Tool details: raw payload in details pane
- Toggle rendered/raw with `r`

### Keybindings
- `j/k` or arrows: move event selection
- `Ctrl+d` / `Ctrl+u`: page down/up in details
- `Tab`: switch pane focus
- `g` / `G`: jump stream top/bottom
- `r`: rendered/raw toggle
- `q`: quit (confirm if run active)
- `Ctrl+C`: interrupt current run, then prompt `Continue to next iteration? [y/n]`

---

## Run Storage

Per run directory: `.ralfinho/runs/<uuid>/`
- `meta.json`
- `events.jsonl`
- `raw-output.log`
- `session.log`
- `effective-prompt.md`

`meta.json` includes:
- run_id, started_at, ended_at
- status (`running|completed|interrupted|failed|max_iterations_reached`)
- agent, prompt_source (`prompt|plan|default`), prompt_file/plan_file
- max_iterations, iterations_completed

---

## Implementation Milestones

### M1 — CLI + Runner core
- [ ] Create Go module + `cmd/ralfinho`
- [ ] Implement flags and input validation
- [ ] Port iteration loop and completion detection
- [ ] Add signal handling + interrupt confirmation flow

### M2 — Prompt input system
- [ ] Implement prompt file loading path
- [ ] Implement plan-to-prompt builder
- [ ] Write `effective-prompt.md` to run directory
- [ ] Add tests for precedence and conflict cases

### M3 — Event pipeline + persistence
- [ ] Parse `pi --mode json` output into normalized events
- [ ] Keep events in memory
- [ ] Append events to `events.jsonl`
- [ ] Write `raw-output.log`, `session.log`, `meta.json`

### M4 — TUI (default)
- [ ] Build two-pane Bubble Tea UI
- [ ] Markdown rendering for assistant output
- [ ] Raw details pane for tool output
- [ ] Add keybindings and resize behavior

### M5 — History viewer
- [ ] Implement `ralfinho view <run-id>`
- [ ] Load saved metadata/events and replay in read-only TUI

### M6 — Polish
- [ ] Improve status/error highlighting
- [ ] Document usage and examples
- [ ] Ensure graceful behavior in non-TTY/CI environments

---

## Acceptance Criteria (v1)

- `ralfinho --plan tasks/PLAN.md` runs agent with a generated prompt equivalent in intent to current `PROMPT.md` workflow.
- Default launch opens TUI and shows live, scrollable event stream.
- Interrupting run prompts for continue/stop decision.
- Full run artifacts are saved under `.ralfinho/runs/<uuid>/`.
- `ralfinho view <run-id>` opens replay view for completed/saved runs.
