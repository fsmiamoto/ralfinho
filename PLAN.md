# Plan: Plain text while streaming, render Markdown when complete

## Goal

Reduce CPU during live assistant streaming by avoiding Markdown rendering until the assistant message is complete.

### User-visible behavior
- While an assistant message is still streaming:
  - show it as plain wrapped text
- When the assistant message ends:
  - replace that same content with rendered Markdown
- Saved-session viewer should continue to show final rendered Markdown
- Raw mode should keep its current behavior

## Scope

### In scope
- Main pane
- Detail pane
- Live sessions
- Saved-session replay
- Tests and benchmarks

### Out of scope
- Full viewport virtualization
- Per-block render caching
- Reworking the event/block architecture beyond what is needed for this change

This plan is intentionally low-risk and high-value.

## Design

### Key idea
Introduce an explicit assistant completion state instead of inferring from summaries.

Right now the code distinguishes assistant updates mostly by `DisplayAssistantText` events and summary text like:
- `< assistant (...) [...]`
- `+ assistant (...)`

That is too implicit.

### Proposed state
Add a boolean like:
- `DisplayEvent.AssistantFinal bool`
- `MainBlock.AssistantFinal bool`

Meaning:
- `false`: message still streaming
- `true`: message finished

### Rendering rule
Create one shared helper for assistant content:
- if `AssistantFinal == false`:
  - use `WrapText(text, width)`
- if `AssistantFinal == true`:
  - use `renderMarkdown(text, width)`

Use this helper in:
- main pane assistant block rendering
- detail pane assistant rendering

That keeps behavior consistent and prevents drift.

## Tasks

### Task 1: Add explicit assistant-final state to display events

#### Files
- `internal/tui/event.go`
- `internal/tui/event_test.go`

#### Changes
Add a new field to `DisplayEvent`:
- `AssistantFinal bool`

Update `EventConverter.Convert(...)` so that:
- `EventMessageStart` for assistant:
  - emits `DisplayAssistantText`
  - `AssistantFinal = false`
- `text_delta`:
  - emits `DisplayAssistantText`
  - `AssistantFinal = false`
- `EventMessageEnd`:
  - emits `DisplayAssistantText`
  - `AssistantFinal = true`

#### Notes
- Do not infer finality from summary strings
- This does not affect persisted run files, because `DisplayEvent` is derived in memory

#### Quality checks
Add or adjust tests:
- assistant `message_start` produces `AssistantFinal == false`
- assistant `text_delta` produces `AssistantFinal == false`
- assistant `message_end` produces `AssistantFinal == true`

Suggested test name:
- `TestEventConverter_AssistantFinalState`

---

### Task 2: Propagate assistant-final state through model and block updates

#### Files
- `internal/tui/model.go`
- `internal/tui/model_lifecycle_test.go`

#### Changes
Add corresponding state to `MainBlock`:
- `AssistantFinal bool`

Update all assistant merge/update paths to copy the final flag:
1. `addDisplayEvent(...)`
   - when merging the last `DisplayAssistantText` event, also copy `AssistantFinal`
2. `buildBlock(...)`
   - when appending/updating assistant blocks, also store/update `AssistantFinal`
3. `updateAssistantBlock(...)`
   - when streaming updates mutate the last assistant block, also update `AssistantFinal`

#### Important gotcha
If `AssistantFinal` is not propagated in all 3 places, the block may stay stuck in plain-text mode forever.

#### Quality checks
Add tests that simulate:
- assistant start
- one or more text deltas
- message end

And assert:
- still only one assistant block
- the same block transitions from `AssistantFinal=false` to `true`

Suggested test names:
- `TestModelAssistantBlockTransitionsToFinal`
- `TestAddDisplayEvent_MergesAssistantFinalState`

---

### Task 3: Introduce a shared assistant render helper

#### Files
- `internal/tui/block.go`
- optionally `internal/tui/assistant_render.go`
- `internal/tui/model_render_test.go`

#### Changes
Add a helper like:
- `renderAssistantContent(text string, width int, final bool) string`

Behavior:
- empty text -> `""`
- `final=false` -> `WrapText(text, width)`
- `final=true` -> `renderMarkdown(text, width)`

Then update:
1. `MainBlock.renderAssistantText(...)`
2. `Model.renderDetail()`

to use this helper.

#### Why shared helper?
Because otherwise main and detail will drift over time.

#### Quality checks
Add render tests for both states.

##### Main pane test
Use Markdown-like text such as:

```md
# Heading

- item one
- item two

```go
fmt.Println("hi")
```
```

Assert:
- streaming/plain render contains literal markers like `# Heading`
- final render does not contain the literal heading marker, but still contains `Heading`

Suggested test names:
- `TestRenderMain_AssistantStreamingUsesPlainText`
- `TestRenderMain_AssistantFinalUsesMarkdown`

##### Detail pane test
Same idea for detail pane:
- streaming assistant in rendered mode should show plain wrapped text
- final assistant in rendered mode should show Markdown-rendered output

Suggested test names:
- `TestRenderDetail_AssistantStreamingUsesPlainText`
- `TestRenderDetail_AssistantFinalUsesMarkdown`

---

### Task 4: Preserve raw-mode semantics

#### Files
- `internal/tui/model.go`
- `internal/tui/model_render_test.go`

#### Changes
No functional redesign is needed here, but behavior must be verified:
- `rawMode == true` continues to show raw metadata/detail text
- the new plain-vs-markdown behavior only applies when `rawMode == false`

#### Quality checks
Add or extend a test to verify:
- raw mode ignores `AssistantFinal`
- rendered mode uses `AssistantFinal`

Suggested test name:
- `TestRenderDetail_RawModeIgnoresAssistantFinal`

---

### Task 5: Add focused performance benchmarks

#### Files
- `internal/tui/model_benchmark_test.go`

#### Changes
Add benchmarks specifically for the live-streaming case.

#### Benchmarks to add

##### A. Streaming assistant render benchmark
Simulate:
- a model with a long in-progress assistant block
- repeated `renderMain()` calls while `AssistantFinal=false`

Purpose:
- confirm this path is much cheaper than Markdown

Suggested name:
- `BenchmarkRenderMain_StreamingAssistantPlainText`

##### B. Final assistant render benchmark
Same content, but `AssistantFinal=true`

Purpose:
- provide a side-by-side comparison

Suggested name:
- `BenchmarkRenderMain_FinalAssistantMarkdown`

##### C. Optional detail-pane benchmark
If easy:
- selected assistant event in detail pane, streaming vs final

#### Quality checks
Record benchmark output in the PR description.

Recommended commands:
- `go test ./internal/tui -run '^$' -bench 'StreamingAssistant|FinalAssistant|Viewer' -benchmem`

Manual pprof check:
- `go test ./internal/tui -run '^$' -bench '^BenchmarkRenderMain_StreamingAssistantPlainText$' -benchtime=5x -cpuprofile /tmp/tui-stream.pprof`
- `go tool pprof -top /tmp/tui-stream.pprof`

Expected result:
- streaming benchmark should show little or no time in `tui.renderMarkdown`
- final benchmark may still show `tui.renderMarkdown`, which is expected

---

### Task 6: Manual QA on real scenarios

#### Scenario A: Live run
Start a run that streams Markdown-like output for several seconds.

Verify:
- content appears immediately
- while streaming, formatting is plain text
- when the message ends, the same content becomes rendered Markdown
- no duplicate assistant block appears
- auto-scroll still behaves normally

#### Scenario B: Saved session viewer
Open a known long completed run, for example:
- `../mireru/webapp/.ralfinho/runs/8a122dc0-f206-43d9-8369-bfe60503879e`

Verify:
- final assistant content still renders as Markdown
- viewer remains idle when not interacting
- no regressions in scroll/detail/raw toggle behavior

#### Scenario C: Raw mode
During a live streaming assistant message:
- toggle raw mode
- verify raw output still works as before

## Acceptance criteria

The implementation is done when all of these are true.

### Functional
- Live assistant text is plain wrapped while streaming
- The same assistant content becomes Markdown-rendered only after `message_end`
- Saved sessions still render final assistant content as Markdown
- Raw mode behavior is unchanged
- No duplicate assistant block is created during finalization

### Correctness
- New unit tests cover:
  - event conversion final state
  - block/model state propagation
  - main-pane rendering behavior
  - detail-pane rendering behavior
- `go test ./...` passes

### Performance
- Streaming benchmark is materially cheaper than final Markdown benchmark
- Streaming pprof no longer shows `renderMarkdown` as a dominant hot path

## Risks and mitigations

### Risk 1: visual jump when final Markdown appears
This is expected:
- plain text while streaming
- styled/rendered text on completion

Mitigation:
- document this as intended behavior
- keep the block stable so only styling/layout changes, not block identity

### Risk 2: state not propagated everywhere
If `AssistantFinal` is not copied in all merge/update paths, the block can get stuck in the wrong mode.

Mitigation:
- explicit tests for the start -> delta -> end transition

### Risk 3: saved viewer regressions
Saved sessions are built from replayed events, so the final event must properly mark the block final.

Mitigation:
- include replay/viewer test coverage

## Nice-to-have follow-up, but not part of this task

If this lands and we still want better performance for large completed sessions:
1. per-block rendered-line caching by width
2. main-pane viewport rendering instead of full join/split
3. optional throttling for extremely frequent streaming updates

Do not mix those into this change.

## Recommended implementation order

1. Task 1: event final-state field
2. Task 2: propagate through model/block updates
3. Task 3: shared render helper and main/detail rendering change
4. Task 4: raw-mode regression coverage
5. Task 5: benchmarks
6. Task 6: manual QA
