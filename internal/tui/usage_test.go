package tui

import (
	"strings"
	"testing"

	"github.com/fsmiamoto/ralfinho/internal/runner"
)

func TestUsageStrip(t *testing.T) {
	cases := []struct {
		name    string
		last    *runner.UsageInfo
		total   *runner.UsageInfo
		want    string
		hasRun  bool // run-total parenthetical expected
	}{
		{
			name: "no usage",
			want: "",
		},
		{
			name:  "only total (e.g. viewer mode)",
			total: &runner.UsageInfo{InputTokens: 12500, OutputTokens: 3200},
			want:  "tokens: 12.5k in / 3.2k out",
		},
		{
			name:   "first iteration: last == total, no parenthetical",
			last:   &runner.UsageInfo{InputTokens: 1000, OutputTokens: 200},
			total:  &runner.UsageInfo{InputTokens: 1000, OutputTokens: 200},
			want:   "tokens: 1.0k in / 200 out",
			hasRun: false,
		},
		{
			name:   "subsequent iteration: shows run total",
			last:   &runner.UsageInfo{InputTokens: 500, OutputTokens: 50},
			total:  &runner.UsageInfo{InputTokens: 5000, OutputTokens: 800},
			want:   "tokens: 500 in / 50 out (run: 5.0k / 800)",
			hasRun: true,
		},
		{
			name: "millions",
			last: &runner.UsageInfo{InputTokens: 1_500_000, OutputTokens: 50},
			total: &runner.UsageInfo{InputTokens: 1_500_000, OutputTokens: 50},
			want: "tokens: 1.5M in / 50 out",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := usageStrip(tc.last, tc.total)
			if got != tc.want {
				t.Errorf("usageStrip = %q, want %q", got, tc.want)
			}
			if tc.hasRun && !strings.Contains(got, "run:") {
				t.Errorf("expected run-total parenthetical in %q", got)
			}
		})
	}
}

func TestCompactTokens(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1000, "1.0k"},
		{12345, "12.3k"},
		{999999, "1000.0k"}, // boundary just below 1M still uses k
		{1_000_000, "1.0M"},
		{1_500_000, "1.5M"},
	}
	for _, tc := range cases {
		if got := compactTokens(tc.n); got != tc.want {
			t.Errorf("compactTokens(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// TestEventConverter_Usage verifies that runner.EventUsage maps to
// DisplayUsage with the per-iteration and cumulative payloads attached.
func TestEventConverter_Usage(t *testing.T) {
	c := NewEventConverter()
	ev := &runner.Event{
		Type:            runner.EventUsage,
		ID:              "usage-1",
		Timestamp:       "2026-05-07T12:00:00Z",
		Usage:           &runner.UsageInfo{InputTokens: 100, OutputTokens: 20},
		CumulativeUsage: &runner.UsageInfo{InputTokens: 500, OutputTokens: 75},
	}
	out := c.Convert(ev)
	if len(out) != 1 {
		t.Fatalf("expected 1 display event, got %d", len(out))
	}
	if out[0].Type != DisplayUsage {
		t.Errorf("Type = %q, want %q", out[0].Type, DisplayUsage)
	}
	if out[0].Usage == nil || out[0].Usage.InputTokens != 100 {
		t.Errorf("Usage = %+v, want InputTokens=100", out[0].Usage)
	}
	if out[0].CumulativeUsage == nil || out[0].CumulativeUsage.OutputTokens != 75 {
		t.Errorf("CumulativeUsage = %+v, want OutputTokens=75", out[0].CumulativeUsage)
	}
}

// TestModel_UsageEventUpdatesStateOnly verifies that DisplayUsage updates
// model state without producing a stream entry or main-view block.
func TestModel_UsageEventUpdatesStateOnly(t *testing.T) {
	m := Model{
		activeToolIdx: -1,
		running:       true,
		converter:     NewEventConverter(),
	}
	de := DisplayEvent{
		Type:            DisplayUsage,
		Usage:           &runner.UsageInfo{InputTokens: 1234, OutputTokens: 56},
		CumulativeUsage: &runner.UsageInfo{InputTokens: 9999, OutputTokens: 100},
	}
	updated, _ := m.addDisplayEvent(de)
	m2 := updated.(Model)

	if len(m2.events) != 0 {
		t.Errorf("expected 0 stream entries, got %d", len(m2.events))
	}
	if len(m2.blocks) != 0 {
		t.Errorf("expected 0 main blocks, got %d", len(m2.blocks))
	}
	if m2.lastUsage == nil || m2.lastUsage.InputTokens != 1234 {
		t.Errorf("lastUsage = %+v, want InputTokens=1234", m2.lastUsage)
	}
	if m2.totalUsage == nil || m2.totalUsage.InputTokens != 9999 {
		t.Errorf("totalUsage = %+v, want InputTokens=9999", m2.totalUsage)
	}
}

// TestNewViewerModel_SeedsTotalUsageFromMeta verifies that the viewer
// pre-populates the usage display from saved meta.json totals.
func TestNewViewerModel_SeedsTotalUsageFromMeta(t *testing.T) {
	meta := runner.RunMeta{
		RunID:                    "test",
		TotalInputTokens:         55000,
		TotalOutputTokens:        4200,
		TotalCacheReadTokens:     30000,
		TotalCacheCreationTokens: 200,
	}
	m := NewViewerModel(nil, meta, "", "", "")
	if m.totalUsage == nil {
		t.Fatal("expected totalUsage to be seeded from meta")
	}
	if m.totalUsage.InputTokens != 55000 {
		t.Errorf("InputTokens = %d, want 55000", m.totalUsage.InputTokens)
	}
	if m.totalUsage.CacheReadTokens != 30000 {
		t.Errorf("CacheReadTokens = %d, want 30000", m.totalUsage.CacheReadTokens)
	}
}

func TestNewViewerModel_NoUsageWhenMetaEmpty(t *testing.T) {
	m := NewViewerModel(nil, runner.RunMeta{}, "", "", "")
	if m.totalUsage != nil {
		t.Errorf("expected totalUsage = nil for empty meta, got %+v", m.totalUsage)
	}
}
