# Ralfinho

My take on the [Ralph Wiggum](https://ghuntley.com/ralph/) technique.

## Aren't there already one hundred million tools for doing this?

Yes, but where's the fun and what do I learn by just using those?

## Usage

Run with a plan (builds an effective prompt from the template):

```bash
ralfinho --plan docs/V1_PLAN.md
```

Run with an explicit prompt file:

```bash
ralfinho --prompt PROMPT.md
# or
ralfinho PROMPT.md
```

Disable TUI:

```bash
ralfinho --no-tui --plan docs/V1_PLAN.md
```

View a saved run:

```bash
ralfinho view <run-id>
```

Runs are persisted under:

```text
.ralfinho/runs/<run-id>/
```
