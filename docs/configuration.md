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
runs-dir = ".ralfinho/runs"
no-tui = false

[agents.claude]
extra-args = ["--model", "claude-opus-4-5"]

[agents.pi]
extra-args = ["--timeout", "30"]
```

Supported top-level keys:

- `agent` — default agent name (`pi`, `kiro`, or `claude`)
- `max-iterations` — default iteration limit (`0` means unlimited)
- `runs-dir` — default runs directory
- `no-tui` — disable the TUI by default

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
