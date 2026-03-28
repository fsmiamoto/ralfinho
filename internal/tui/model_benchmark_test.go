package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fsmiamoto/ralfinho/internal/runner"
)

var (
	benchmarkViewerStringSink string
	benchmarkViewerModelSink  Model
)

func BenchmarkNewViewerModelLongSession(b *testing.B) {
	events := benchmarkLongSessionDisplayEvents()
	meta := benchmarkLongSessionMeta()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkViewerModelSink = NewViewerModel(events, meta, "", "", "")
	}
}

func BenchmarkViewerRenderMainLongSession(b *testing.B) {
	m := benchmarkLongSessionViewerModel()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkViewerStringSink = m.renderMain()
	}
}

func BenchmarkViewerViewLongSession(b *testing.B) {
	m := benchmarkLongSessionViewerModel()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkViewerStringSink = m.View()
	}
}

func benchmarkLongSessionViewerModel() Model {
	m := NewViewerModel(benchmarkLongSessionDisplayEvents(), benchmarkLongSessionMeta(), "", "", "")
	m.width = 120
	m.height = 40
	m.cursor = len(m.events) - 1
	initRenderer(m.width - 4)
	return m
}

func benchmarkLongSessionMeta() runner.RunMeta {
	return runner.RunMeta{
		RunID:               "8a122dc0-f206-43d9-8369-bfe60503879e",
		StartedAt:           "2026-03-16T08:34:59+09:00",
		EndedAt:             "2026-03-16T09:57:09+09:00",
		Status:              "completed",
		Agent:               "claude",
		PromptSource:        "plan",
		PlanFile:            "UI_REFINEMENT_PLAN.md",
		IterationsCompleted: 19,
	}
}

// ---------- streaming vs final assistant benchmarks ----------

// benchmarkStreamingAssistantModel builds a Model with a single long in-progress
// assistant block (AssistantFinal=false), simulating a live streaming session.
func benchmarkStreamingAssistantModel() Model {
	text := benchmarkAssistantMarkdownText()
	m := Model{
		width:  120,
		height: 40,
		blocks: []MainBlock{{
			Kind:           BlockAssistantText,
			Iteration:      1,
			Text:           text,
			AssistantFinal: false,
		}},
	}
	initRenderer(m.width - 4)
	return m
}

// benchmarkFinalAssistantModel builds the same Model but with AssistantFinal=true,
// simulating a completed assistant message that triggers Markdown rendering.
func benchmarkFinalAssistantModel() Model {
	m := benchmarkStreamingAssistantModel()
	m.blocks[0].AssistantFinal = true
	return m
}

// benchmarkAssistantMarkdownText returns a realistic Markdown body (~4 KB)
// with headings, lists, code blocks, and prose — representative of a real
// assistant response.
func benchmarkAssistantMarkdownText() string {
	var b strings.Builder
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&b, "## Section %d\n\n", i)
		b.WriteString("This is a moderately long paragraph that simulates the kind of assistant output one sees during a typical code review or implementation discussion. It contains enough text to exercise word-wrapping and Markdown rendering pipelines meaningfully.\n\n")
		b.WriteString("- first item in the list\n- second item with **bold** and *italic*\n- third item mentioning `inlineCode()`\n\n")
		fmt.Fprintf(&b, "```go\nfunc example%d() {\n\tfmt.Println(\"hello from section %d\")\n\tfor i := 0; i < 10; i++ {\n\t\tfmt.Println(i)\n\t}\n}\n```\n\n", i, i)
	}
	return b.String()
}

func BenchmarkRenderMain_StreamingAssistantPlainText(b *testing.B) {
	m := benchmarkStreamingAssistantModel()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkViewerStringSink = m.renderMain()
	}
}

func BenchmarkRenderMain_FinalAssistantMarkdown(b *testing.B) {
	m := benchmarkFinalAssistantModel()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkViewerStringSink = m.renderMain()
	}
}

func BenchmarkRenderDetail_StreamingAssistantPlainText(b *testing.B) {
	text := benchmarkAssistantMarkdownText()
	m := Model{
		width:  120,
		height: 40,
		cursor: 0,
		events: []DisplayEvent{{
			Type:           DisplayAssistantText,
			Iteration:      1,
			Detail:         text,
			AssistantFinal: false,
		}},
	}
	initRenderer(m.detailWidth() - 2)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkViewerStringSink = m.renderDetail()
	}
}

func BenchmarkRenderDetail_FinalAssistantMarkdown(b *testing.B) {
	text := benchmarkAssistantMarkdownText()
	m := Model{
		width:  120,
		height: 40,
		cursor: 0,
		events: []DisplayEvent{{
			Type:           DisplayAssistantText,
			Iteration:      1,
			Detail:         text,
			AssistantFinal: true,
		}},
	}
	initRenderer(m.detailWidth() - 2)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkViewerStringSink = m.renderDetail()
	}
}

// ---------- live session benchmarks ----------

// benchmarkLiveLongHistoryModel builds a live Model with long completed history
// and one in-progress streaming assistant tail block, simulating a real live
// session that has been running for many iterations.
func benchmarkLiveLongHistoryModel() Model {
	historyEvents := benchmarkLongSessionDisplayEvents()

	m := Model{
		width:          120,
		height:         40,
		running:        true,
		paneRatio:      0.3,
		mainAutoScroll: true,
		autoScroll:     true,
		activeToolIdx:  -1,
		startTime:      time.Now(),
	}
	initRenderer(m.width - 4)

	// Load completed history.
	for _, de := range historyEvents {
		m.events = append(m.events, de)
		m.buildBlock(de)
	}

	// Add a streaming (non-final) assistant block for the current iteration.
	streamDE := DisplayEvent{
		Type:      DisplayAssistantText,
		Iteration: 20,
		Summary:   "< assistant (claude-opus-4-6) [100 chars]",
		Detail:    "Starting to think about the next step...",
	}
	m.events = append(m.events, streamDE)
	m.buildBlock(streamDE)
	m.cursor = len(m.events) - 1

	return m
}

func benchmarkAssistantDelta() DisplayEvent {
	return DisplayEvent{
		Type:      DisplayAssistantText,
		Iteration: 20,
		Summary:   "< assistant (claude-opus-4-6) [500 chars]",
		Detail:    "Starting to think about the next step and analyzing the codebase structure to determine what changes are needed. Let me look at the relevant files and understand the architecture before proposing modifications.",
	}
}

// BenchmarkLiveLongHistoryAssistantDeltaView is the primary benchmark for the
// live viewport optimization. It measures the cost of processing one assistant
// streaming delta and rendering the full View() on a model with long history.
//
// Baseline (before optimization): ~472 ms/op, ~172 MB/op, ~3.9M allocs/op
func BenchmarkLiveLongHistoryAssistantDeltaView(b *testing.B) {
	m := benchmarkLiveLongHistoryModel()
	m.ensureMainLayout(m.width - 4) // warm up layout caches

	delta := benchmarkAssistantDelta()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		updated, _ := m.addDisplayEvent(delta)
		m = updated.(Model)
		benchmarkViewerStringSink = m.View()
	}
}

// BenchmarkLiveLongHistoryAssistantDeltaViewBaseline measures the same scenario
// using the old render approach (re-render all blocks, flatten, slice viewport).
// This provides a direct A/B comparison for the optimization.
func BenchmarkLiveLongHistoryAssistantDeltaViewBaseline(b *testing.B) {
	m := benchmarkLiveLongHistoryModel()

	delta := benchmarkAssistantDelta()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		updated, _ := m.addDisplayEvent(delta)
		m = updated.(Model)
		benchmarkViewerStringSink = renderMainBaseline(m)
	}
}

// BenchmarkLiveLongHistoryIdleView measures the cost of View() when nothing
// has changed — all layout caches are warm and no blocks are dirty.
func BenchmarkLiveLongHistoryIdleView(b *testing.B) {
	m := benchmarkLiveLongHistoryModel()
	m.ensureMainLayout(m.width - 4)
	_ = m.View() // warm all render paths

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkViewerStringSink = m.View()
	}
}

// ---------- data generators ----------

func benchmarkLongSessionDisplayEvents() []DisplayEvent {
	const (
		iterations                 = 19
		assistantBlocksPerIter     = 28 // 532 assistant blocks total
		toolExecutionsPerIteration = 32 // 608 tool blocks total
	)

	events := make([]DisplayEvent, 0, iterations*(1+assistantBlocksPerIter+toolExecutionsPerIteration*2))
	assistantBody := strings.Repeat("This paragraph simulates a moderately long assistant response rendered through Glamour. ", 3)
	toolResult := strings.Repeat("line\n", 40)

	for iter := 1; iter <= iterations; iter++ {
		events = append(events, DisplayEvent{
			Type:      DisplayIteration,
			Iteration: iter,
			Summary:   fmt.Sprintf("iteration %d", iter),
		})

		for step := 0; step < toolExecutionsPerIteration; step++ {
			if step < assistantBlocksPerIter {
				text := fmt.Sprintf(
					"### Iteration %d Step %d\n\n%s\n\n- inspect component state\n- verify layout changes\n- summarize the next action\n\n```tsx\nexport const Step%d%d = () => <div>ok</div>\n```",
					iter,
					step+1,
					assistantBody,
					iter,
					step,
				)
				// This benchmark simulates a completed saved session viewed via
				// NewViewerModel, so assistant events must be marked final to
				// measure the Markdown-rendered path rather than the cheaper
				// live-streaming plain-text path.
				events = append(events, DisplayEvent{
					Type:           DisplayAssistantText,
					Iteration:      iter,
					Summary:        fmt.Sprintf("< assistant (claude-opus-4-6) [%d chars]", len(text)),
					Detail:         text,
					AssistantFinal: true,
				})
			}

			toolID := fmt.Sprintf("tool-%02d-%02d", iter, step)
			path := fmt.Sprintf("/workspace/src/component-%02d-%02d.tsx", iter, step)
			events = append(events,
				DisplayEvent{
					Type:            DisplayToolStart,
					Iteration:       iter,
					Summary:         fmt.Sprintf("> read: %s", path),
					ToolCallID:      toolID,
					ToolName:        "read",
					ToolDisplayArgs: path,
				},
				DisplayEvent{
					Type:           DisplayToolEnd,
					Iteration:      iter,
					Summary:        "+ read done",
					ToolCallID:     toolID,
					ToolName:       "read",
					ToolResultText: toolResult,
				},
			)
		}
	}

	return events
}
