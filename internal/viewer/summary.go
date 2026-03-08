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

// RunActionState captures whether a browser action is currently available.
type RunActionState struct {
	Available      bool
	DisabledReason string
}

// ResumeSource describes how a run can be resumed as a fresh run.
type ResumeSource string

const (
	ResumeSourceNone            ResumeSource = ""
	ResumeSourceEffectivePrompt ResumeSource = "effective_prompt"
	ResumeSourcePromptFile      ResumeSource = "prompt_file"
	ResumeSourcePlanFile        ResumeSource = "plan_file"
	ResumeSourceDefault         ResumeSource = "default"
)

// ResumeActionState captures resume availability plus the source to reuse.
type ResumeActionState struct {
	RunActionState
	Source ResumeSource
	Path   string
}

// RunActions collects per-row browser actions derived from cached artifacts.
type RunActions struct {
	Open   RunActionState
	Resume ResumeActionState
	Delete RunActionState
}

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

	EventsPath  string
	HasEvents   bool
	EventsError string

	EffectivePromptPath  string
	HasEffectivePrompt   bool
	EffectivePromptError string

	ArtifactError string // missing / unreadable / invalid meta.json details
	Actions       RunActions
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
// sorted newest-first. Per-run artifact failures are captured on the summary so
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
		RunID:               entry.Name(),
		Dir:                 dir,
		Status:              "unknown",
		Agent:               "unknown",
		PromptSource:        "unknown",
		PromptLabel:         "unknown",
		EventsPath:          filepath.Join(dir, "events.jsonl"),
		EffectivePromptPath: filepath.Join(dir, "effective-prompt.md"),
	}

	if info, err := entry.Info(); err == nil {
		summary.SortTime = info.ModTime()
	}

	summary.HasEvents, summary.EventsError = inspectRunArtifact(summary.EventsPath)
	summary.HasEffectivePrompt, summary.EffectivePromptError = inspectRunArtifact(summary.EffectivePromptPath)

	metaPath := filepath.Join(dir, "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			summary.ArtifactError = "meta.json missing"
		} else {
			summary.ArtifactError = fmt.Sprintf("reading meta.json: %v", err)
		}
		summary.Actions = buildRunActions(summary)
		summary.SearchText = buildSummarySearchText(summary)
		return summary
	}

	var meta runner.RunMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		summary.ArtifactError = fmt.Sprintf("parsing meta.json: %v", err)
		summary.Actions = buildRunActions(summary)
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

	summary.Actions = buildRunActions(summary)
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
		summary.ArtifactError,
		summary.EventsError,
		summary.EffectivePromptError,
		summary.Actions.Open.DisabledReason,
		summary.Actions.Resume.DisabledReason,
	}
	if !summary.SortTime.IsZero() {
		fields = append(fields, summary.SortTime.Format("2006-01-02 15:04"))
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

func buildRunActions(summary RunSummary) RunActions {
	actions := RunActions{}

	switch {
	case !summary.HasMeta:
		actions.Open.DisabledReason = defaultActionReason(summary.ArtifactError, "meta.json unavailable")
	case !summary.HasEvents:
		actions.Open.DisabledReason = defaultActionReason(summary.EventsError, "events.jsonl unavailable")
	default:
		actions.Open.Available = true
	}

	actions.Resume = buildResumeAction(summary)

	if summary.Dir == "" {
		actions.Delete.DisabledReason = "run directory unavailable"
	} else {
		actions.Delete.Available = true
	}

	return actions
}

func buildResumeAction(summary RunSummary) ResumeActionState {
	if summary.HasEffectivePrompt {
		return ResumeActionState{
			RunActionState: RunActionState{Available: true},
			Source:         ResumeSourceEffectivePrompt,
			Path:           summary.EffectivePromptPath,
		}
	}

	if summary.HasMeta {
		if source, path := resumeSourceFromMeta(summary.Meta); source != ResumeSourceNone {
			return ResumeActionState{
				RunActionState: RunActionState{Available: true},
				Source:         source,
				Path:           path,
			}
		}
	}

	return ResumeActionState{
		RunActionState: RunActionState{
			DisabledReason: buildResumeDisabledReason(summary),
		},
	}
}

func buildResumeDisabledReason(summary RunSummary) string {
	parts := make([]string, 0, 2)
	if summary.EffectivePromptError != "" {
		parts = append(parts, summary.EffectivePromptError)
	}
	if !summary.HasMeta {
		parts = append(parts, defaultActionReason(summary.ArtifactError, "meta.json unavailable"))
	} else {
		parts = append(parts, "meta.json does not describe a reusable prompt source")
	}
	return strings.Join(parts, "; ")
}

func resumeSourceFromMeta(meta runner.RunMeta) (ResumeSource, string) {
	switch strings.TrimSpace(meta.PromptSource) {
	case "prompt":
		if path := strings.TrimSpace(meta.PromptFile); path != "" {
			return ResumeSourcePromptFile, path
		}
	case "plan":
		if path := strings.TrimSpace(meta.PlanFile); path != "" {
			return ResumeSourcePlanFile, path
		}
	case "default":
		return ResumeSourceDefault, ""
	}

	if path := strings.TrimSpace(meta.PromptFile); path != "" {
		return ResumeSourcePromptFile, path
	}
	if path := strings.TrimSpace(meta.PlanFile); path != "" {
		return ResumeSourcePlanFile, path
	}
	return ResumeSourceNone, ""
}

func inspectRunArtifact(path string) (bool, string) {
	name := filepath.Base(path)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, fmt.Sprintf("%s missing", name)
		}
		return false, fmt.Sprintf("reading %s: %v", name, err)
	}
	if info.IsDir() {
		return false, fmt.Sprintf("%s is a directory", name)
	}

	f, err := os.Open(path)
	if err != nil {
		return false, fmt.Sprintf("reading %s: %v", name, err)
	}
	f.Close()
	return true, ""
}

func defaultActionReason(reason, fallback string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fallback
	}
	return reason
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
