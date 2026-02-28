# Implementation Plan: TUI Exit Confirmation & Session Summary

## Feature 1: Exit Confirmation (q and ctrl+c)

### Behavior
- **`q`**: First press shows "Quit? Press y to confirm, any other key to cancel" in the status bar. Second press of `y` confirms quit; any other key cancels.
- **`ctrl+c`**: First press shows "Press Ctrl+C again to quit" in the status bar. Second `ctrl+c` quits immediately. Any other key cancels.
- Both confirmations share a `confirmQuit` state but track which key triggered it so the confirmation message is appropriate.

### Changes in `internal/tui/model.go`

1. Add fields to `Model`:
   - `confirmQuit bool` — whether we're in the "confirm quit?" state
   - `confirmCtrlC bool` — true if ctrl+c triggered the confirm, false if q triggered it

2. Modify `handleKey`:
   - When `confirmQuit` is `true`, intercept keys **before** the normal switch:
     - If `confirmCtrlC` is true: only `ctrl+c` confirms → `tea.Quit`
     - If `confirmCtrlC` is false: `y` confirms → `tea.Quit`
     - Any other key: set `confirmQuit = false`, return (cancel)
   - Split the existing `"q", "ctrl+c"` case:
     - `"q"`: set `confirmQuit = true`, `confirmCtrlC = false`
     - `"ctrl+c"`: set `confirmQuit = true`, `confirmCtrlC = true`

3. Modify `renderStatus`:
   - When `confirmQuit && confirmCtrlC`: replace status bar with `"Press Ctrl+C again to quit, any other key to cancel"`
   - When `confirmQuit && !confirmCtrlC`: replace status bar with `"Quit? Press y to confirm, any other key to cancel"`

## Feature 2: Print Session Summary to stderr on TUI Exit

### Behavior
After the TUI program exits, print to stderr:
```
=== run summary ===
run-id:     <full-uuid>
iterations: <N>
status:     <status>
```
This matches the format already used in `runPlain` in `cmd/ralfinho/main.go`.
Also call `exitForStatus` to set the proper exit code.

### Changes in `internal/tui/model.go`

1. Add field: `result *runner.RunResult`
2. In `DoneMsg` handler: store `&msg.Result` into `m.result`
3. Add public accessor: `func (m Model) RunResult() *runner.RunResult`

### Changes in `cmd/ralfinho/main.go`

1. In `runTUI`, after `p.Run()` returns:
   - Type-assert the returned `tea.Model` to `tui.Model`
   - Call `.RunResult()` — if non-nil, print the summary to stderr and call `exitForStatus`
   - If nil (user quit before runner finished), read from `resultCh` with a short timeout to see if the result is available, print what we can

## Implementation Order
1. Feature 1 (confirmation) in `internal/tui/model.go`
2. Feature 2 (summary) in `internal/tui/model.go` + `cmd/ralfinho/main.go`
3. Build and verify with `go build ./...`
