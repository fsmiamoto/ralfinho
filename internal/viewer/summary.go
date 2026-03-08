package viewer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fsmiamoto/ralfinho/internal/runner"
)

// RunSummary is a browser-friendly summary of a saved run.
//
// It caches the parsed meta.json alongside normalized fields used for
// newest-first sorting and in-memory search, while keeping per-run artifact
// problems on the summary instead of failing the entire listing.
type RunSummary struct {
	RunID string // directory name used to identify the saved run
	Dir   string // full path to the run directory

	Meta    runner.RunMeta
	HasMeta bool

	StartedAt     time.Time // parsed meta.json started_at; zero when unavailable
	StartedAtText string    // raw started_at text from meta.json
	SortTime      time.Time // ordering key; falls back to directory modtime

	Status              string
	Agent               string
	PromptSource        string
	PromptPath          string
	PromptLabel         string
	IterationsCompleted int

	ArtifactError string // missing / unreadable / invalid meta.json details
	SearchText    string // lower-cased cached text for search/filter matching
}

// Matches reports whether the summary matches a case-insensitive query.
func (s RunSummary) Matches(query string) bool {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return true
	}
	return strings.Contains(s.SearchText, query)
}

// ListRunSummaries returns browser-friendly summaries for all run directories,
// sorted newest-first. Per-run meta.json failures are captured on the summary so
// one bad run does not prevent browsing the rest.
func ListRunSummaries(runsDir string) ([]RunSummary, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading runs directory: %w", err)
	}

	summaries := make([]RunSummary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		summaries = append(summaries, summarizeRunDir(runsDir, entry))
	}

	sort.SliceStable(summaries, func(i, j int) bool {
		if !summaries[i].SortTime.Equal(summaries[j].SortTime) {
			return summaries[i].SortTime.After(summaries[j].SortTime)
		}
		return summaries[i].RunID > summaries[j].RunID
	})

	return summaries, nil
}

func summarizeRunDir(runsDir string, entry os.DirEntry) RunSummary {
	dir := filepath.Join(runsDir, entry.Name())
	summary := RunSummary{
		RunID:        entry.Name(),
		Dir:          dir,
		Status:       "unknown",
		Agent:        "unknown",
		PromptSource: "unknown",
		PromptLabel:  "unknown",
	}

	if info, err := entry.Info(); err == nil {
		summary.SortTime = info.ModTime()
	}

	metaPath := filepath.Join(dir, "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			summary.ArtifactError = "meta.json missing"
		} else {
			summary.ArtifactError = fmt.Sprintf("reading meta.json: %v", err)
		}
		summary.SearchText = buildSummarySearchText(summary)
		return summary
	}

	var meta runner.RunMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		summary.ArtifactError = fmt.Sprintf("parsing meta.json: %v", err)
		summary.SearchText = buildSummarySearchText(summary)
		return summary
	}

	summary.Meta = meta
	summary.HasMeta = true
	summary.Status = normalizeSummaryValue(meta.Status, "unknown")
	summary.Agent = normalizeSummaryAgent(meta.Agent)
	summary.PromptSource = normalizeSummaryValue(meta.PromptSource, "unknown")
	summary.PromptPath = summaryPromptPath(meta)
	summary.PromptLabel = summaryPromptLabel(meta)
	summary.IterationsCompleted = meta.IterationsCompleted
	summary.StartedAtText = meta.StartedAt

	if startedAt, ok := parseSummaryTime(meta.StartedAt); ok {
		summary.StartedAt = startedAt
		summary.SortTime = startedAt
	}

	summary.SearchText = buildSummarySearchText(summary)
	return summary
}

func buildSummarySearchText(summary RunSummary) string {
	fields := []string{
		summary.RunID,
		summary.Meta.RunID,
		summary.Agent,
		summary.Status,
		summary.PromptSource,
		summary.PromptLabel,
		summary.PromptPath,
		summary.StartedAtText,
	}
	if !summary.SortTime.IsZero() {
		fields = append(fields, summary.SortTime.Format("2006-01-02 15:04"))
	}
	if summary.ArtifactError != "" {
		fields = append(fields, summary.ArtifactError)
	}

	searchFields := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		searchFields = append(searchFields, strings.ToLower(field))
	}
	return strings.Join(searchFields, "\n")
}

func normalizeSummaryValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func normalizeSummaryAgent(agent string) string {
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return "pi"
	}
	return agent
}

func summaryPromptPath(meta runner.RunMeta) string {
	switch meta.PromptSource {
	case "prompt":
		if meta.PromptFile != "" {
			return meta.PromptFile
		}
	case "plan":
		if meta.PlanFile != "" {
			return meta.PlanFile
		}
	}

	if meta.PromptFile != "" {
		return meta.PromptFile
	}
	return meta.PlanFile
}

func summaryPromptLabel(meta runner.RunMeta) string {
	if promptPath := summaryPromptPath(meta); promptPath != "" {
		return filepath.Base(promptPath)
	}
	if meta.PromptSource != "" {
		return meta.PromptSource
	}
	return "unknown"
}

func parseSummaryTime(raw string) (time.Time, bool) {
	if raw == "" {
		return time.Time{}, false
	}

	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, true
	}
	return time.Time{}, false
}
