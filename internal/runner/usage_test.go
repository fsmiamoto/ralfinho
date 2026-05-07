package runner

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fsmiamoto/ralfinho/internal/events"
)

func TestRun_AccumulatesUsageAcrossIterations(t *testing.T) {
	fa := &fakeAgent{
		responses: []fakeResponse{
			{
				text: "iter 1",
				events: []events.Event{
					{Type: events.EventMessageStart},
					{Type: events.EventMessageEnd, Usage: &events.UsageInfo{
						InputTokens: 1000, OutputTokens: 200, CacheReadTokens: 500,
					}},
				},
			},
			{
				text: "iter 2 " + completionMarker,
				events: []events.Event{
					{Type: events.EventMessageStart},
					{Type: events.EventMessageEnd, Usage: &events.UsageInfo{
						InputTokens: 1500, OutputTokens: 350, CacheCreationTokens: 100,
					}},
				},
			},
		},
	}
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:  "test",
		Prompt: "track tokens",
	})

	result := r.Run(context.Background())

	if result.Status != StatusCompleted {
		t.Fatalf("status = %s, want %s", result.Status, StatusCompleted)
	}
	if r.totalUsage.InputTokens != 2500 {
		t.Errorf("totalUsage.InputTokens = %d, want 2500", r.totalUsage.InputTokens)
	}
	if r.totalUsage.OutputTokens != 550 {
		t.Errorf("totalUsage.OutputTokens = %d, want 550", r.totalUsage.OutputTokens)
	}
	if r.totalUsage.CacheReadTokens != 500 {
		t.Errorf("totalUsage.CacheReadTokens = %d, want 500", r.totalUsage.CacheReadTokens)
	}
	if r.totalUsage.CacheCreationTokens != 100 {
		t.Errorf("totalUsage.CacheCreationTokens = %d, want 100", r.totalUsage.CacheCreationTokens)
	}

	// iterUsage should reflect only the LAST iteration after the run completes.
	if r.iterUsage.InputTokens != 1500 {
		t.Errorf("iterUsage.InputTokens = %d, want 1500 (last iteration)", r.iterUsage.InputTokens)
	}
	if r.iterUsage.OutputTokens != 350 {
		t.Errorf("iterUsage.OutputTokens = %d, want 350", r.iterUsage.OutputTokens)
	}
}

func TestRun_PersistsTotalUsageToMeta(t *testing.T) {
	dir := t.TempDir()
	fa := &fakeAgent{
		responses: []fakeResponse{
			{
				text: "ok " + completionMarker,
				events: []events.Event{
					{Type: events.EventMessageStart},
					{Type: events.EventMessageEnd, Usage: &events.UsageInfo{
						InputTokens: 4242, OutputTokens: 99, CacheReadTokens: 7, CacheCreationTokens: 11,
					}},
				},
			},
		},
	}
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:   "test",
		Prompt:  "persist",
		RunsDir: dir,
	})

	result := r.Run(context.Background())

	metaPath := filepath.Join(dir, result.RunID, "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("reading meta.json: %v", err)
	}
	var meta RunMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("parsing meta.json: %v", err)
	}
	if meta.TotalInputTokens != 4242 {
		t.Errorf("meta.TotalInputTokens = %d, want 4242", meta.TotalInputTokens)
	}
	if meta.TotalOutputTokens != 99 {
		t.Errorf("meta.TotalOutputTokens = %d, want 99", meta.TotalOutputTokens)
	}
	if meta.TotalCacheReadTokens != 7 {
		t.Errorf("meta.TotalCacheReadTokens = %d, want 7", meta.TotalCacheReadTokens)
	}
	if meta.TotalCacheCreationTokens != 11 {
		t.Errorf("meta.TotalCacheCreationTokens = %d, want 11", meta.TotalCacheCreationTokens)
	}
}

func TestRun_EmitsUsageEventPerIteration(t *testing.T) {
	fa := &fakeAgent{
		responses: []fakeResponse{
			{
				text: "iter 1",
				events: []events.Event{
					{Type: events.EventMessageStart},
					{Type: events.EventMessageEnd, Usage: &events.UsageInfo{
						InputTokens: 100, OutputTokens: 50,
					}},
				},
			},
			{
				text: "iter 2 " + completionMarker,
				events: []events.Event{
					{Type: events.EventMessageStart},
					{Type: events.EventMessageEnd, Usage: &events.UsageInfo{
						InputTokens: 200, OutputTokens: 75,
					}},
				},
			},
		},
	}

	ch := make(chan Event, 256)
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:     "test",
		Prompt:    "emit events",
		EventChan: ch,
	})

	r.Run(context.Background())

	// Drain. sendEvent is non-blocking; the buffered channel captures all
	// events emitted during Run (256 is well above the test's footprint).
	var usageEvents []Event
	for {
		select {
		case ev := <-ch:
			if ev.Type == EventUsage {
				usageEvents = append(usageEvents, ev)
			}
		default:
			goto done
		}
	}
done:

	if len(usageEvents) != 2 {
		t.Fatalf("expected 2 EventUsage events, got %d", len(usageEvents))
	}

	// First iteration: per-iter == cumulative since it's the first.
	first := usageEvents[0]
	if first.Usage == nil || first.CumulativeUsage == nil {
		t.Fatal("first usage event: expected non-nil Usage and CumulativeUsage")
	}
	if first.Usage.InputTokens != 100 {
		t.Errorf("first.Usage.InputTokens = %d, want 100", first.Usage.InputTokens)
	}
	if first.CumulativeUsage.InputTokens != 100 {
		t.Errorf("first.CumulativeUsage.InputTokens = %d, want 100", first.CumulativeUsage.InputTokens)
	}

	// Second iteration: cumulative reflects sum.
	second := usageEvents[1]
	if second.Usage.InputTokens != 200 {
		t.Errorf("second.Usage.InputTokens = %d, want 200", second.Usage.InputTokens)
	}
	if second.CumulativeUsage.InputTokens != 300 {
		t.Errorf("second.CumulativeUsage.InputTokens = %d, want 300", second.CumulativeUsage.InputTokens)
	}
	if second.CumulativeUsage.OutputTokens != 125 {
		t.Errorf("second.CumulativeUsage.OutputTokens = %d, want 125", second.CumulativeUsage.OutputTokens)
	}
}

func TestRun_NoUsageEventWhenAgentDoesNotReport(t *testing.T) {
	fa := &fakeAgent{
		responses: []fakeResponse{
			{
				text: "no usage info " + completionMarker,
				events: []events.Event{
					{Type: events.EventMessageStart},
					{Type: events.EventMessageEnd}, // no usage
				},
			},
		},
	}
	ch := make(chan Event, 64)
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:     "no-usage-agent",
		Prompt:    "agent doesn't report usage",
		EventChan: ch,
	})

	r.Run(context.Background())

	for {
		select {
		case ev := <-ch:
			if ev.Type == EventUsage {
				t.Fatalf("expected no EventUsage events, got one: %+v", ev)
			}
		default:
			return
		}
	}
}

func TestRun_PersistsUsageEventsToJSONL(t *testing.T) {
	dir := t.TempDir()
	fa := &fakeAgent{
		responses: []fakeResponse{
			{
				text: "ok " + completionMarker,
				events: []events.Event{
					{Type: events.EventMessageStart},
					{Type: events.EventMessageEnd, Usage: &events.UsageInfo{
						InputTokens: 42, OutputTokens: 7,
					}},
				},
			},
		},
	}
	r := newTestRunnerWithAgent(t, fa, RunConfig{
		Agent:   "test",
		Prompt:  "p",
		RunsDir: dir,
	})

	result := r.Run(context.Background())

	jsonlPath := filepath.Join(dir, result.RunID, "events.jsonl")
	data, err := os.ReadFile(jsonlPath)
	if err != nil {
		t.Fatalf("reading events.jsonl: %v", err)
	}

	// Find the usage line.
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type == EventUsage {
			found = true
			if ev.Usage == nil || ev.Usage.InputTokens != 42 {
				t.Errorf("persisted usage event has wrong Usage: %+v", ev.Usage)
			}
			if ev.CumulativeUsage == nil || ev.CumulativeUsage.OutputTokens != 7 {
				t.Errorf("persisted usage event has wrong CumulativeUsage: %+v", ev.CumulativeUsage)
			}
		}
	}
	if !found {
		t.Error("EventUsage was not persisted to events.jsonl")
	}
}

func TestFormatTokens(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{-5, "0"},
		{12, "12"},
		{1234, "1,234"},
		{12345, "12,345"},
		{1234567, "1,234,567"},
	}
	for _, tc := range cases {
		if got := formatTokens(tc.n); got != tc.want {
			t.Errorf("formatTokens(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// silence unused import linter when go test trims this
var _ = io.Discard
