# Configuration

Ralfinho can load an optional TOML config file to provide default values for
its CLI flags. Config values are only used when the corresponding option was
not explicitly passed on the command line.

## File locations

Ralfinho looks for config in two places:

- Global: `<user-config-dir>/ralfinho/config.toml`
  - On Linux/XDG, this is typically `$XDG_CONFIG_HOME/ralfinho/config.toml`
    or `~/.config/ralfinho/config.toml`
- Local: `.ralfinho/config.toml` in the current project directory

## Precedence

Config is applied in this order:

1. Global config
2. Local config overrides global config
3. CLI flags override both

For `[agents.<name>]`, a local entry replaces the global entry for that same
agent.

## Example configuration

```toml
agent = "claude"
max-iterations = 5
inactivity-timeout = "10m"  # "0" disables the stuck-detection watchdog
runs-dir = ".ralfinho/runs"
no-tui = false

[templates]
plan = "file:prompts/plan.md"
default = "file:prompts/default.md"

[agents.claude]
extra-args = ["--model", "claude-opus-4-5"]

[agents.pi]
extra-args = ["--timeout", "30"]
```

Supported top-level keys:

- `agent` — default agent name (`pi`, `kiro`, or `claude`)
- `max-iterations` — default iteration limit (`0` means unlimited)
- `inactivity-timeout` — duration with no agent activity before the stuck-detection
  watchdog fires (e.g. `"10m"`, `"1h"`). `"0"` disables the watchdog entirely —
  useful when an agent step is expected to be slow. Omit the key to use the
  built-in default (5m). Also available as a CLI flag: `--inactivity-timeout`.
- `runs-dir` — default runs directory
- `no-tui` — disable the TUI by default
- `[templates]` — optional prompt template overrides
  - `plan` — overrides the built-in `--plan` prompt template
  - `default` — overrides the built-in fallback prompt

Template values may be either:

- inline template text, or
- a `file:` reference such as `file:prompts/plan.md`

`file:` paths are resolved relative to the config file that defines them. This
means a global config can point to files under `~/.config/ralfinho/`, while a
project-local `.ralfinho/config.toml` can point to files under `.ralfinho/`.
If both global and local config files define different template fields, each one
still resolves relative to its own source file.

Both template overrides are rendered as Go `text/template` templates. The plan
template receives:

- `{{.PlanPath}}`
- `{{.PlanContent}}`
- `{{.NotesPath}}`
- `{{.ProgressPath}}`

The `default` template receives:

- `{{.NotesPath}}`
- `{{.ProgressPath}}`

`NotesPath` and `ProgressPath` resolve to the per-run memory file paths
(e.g. `.ralfinho/runs/<uuid>/NOTES.md`). The built-in templates use these
to tell the agent where to read and write its cross-iteration memory.

The `--prompt <file>` CLI path is unchanged: it bypasses config templates and
uses the prompt file contents verbatim.

Per-agent settings:

- `[agents.<name>]`
  - `extra-args` — additional arguments appended to the agent subprocess
    command line

`extra-args` is useful for backend-specific flags that ralfinho does not expose
as first-class CLI options.

## Common pattern: global defaults

```toml
# ~/.config/ralfinho/config.toml
agent = "claude"
max-iterations = 10

[agents.claude]
extra-args = ["--model", "claude-opus-4-5"]
```

## Common pattern: project-local overrides

```toml
# .ralfinho/config.toml
agent = "pi"
max-iterations = 3
no-tui = true
```

If both files exist, the local `.ralfinho/config.toml` only overrides the
fields it sets. Any fields left unset there continue to use the global values.
