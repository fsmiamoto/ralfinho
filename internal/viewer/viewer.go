// Package viewer loads saved run data for read-only replay.
package viewer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fsmiamoto/ralfinho/internal/runner"
)

// SavedRun holds the loaded data for a past run.
type SavedRun struct {
	Meta   runner.RunMeta
	Events []runner.Event
	Prompt string // from effective-prompt.md
}

// LoadRun loads a saved run from disk. The runID may be a prefix;
// it is resolved to a full directory name via ResolveRunID.
func LoadRun(runsDir, runID string) (*SavedRun, error) {
	resolvedID, err := ResolveRunID(runsDir, runID)
	if err != nil {
		return nil, err
	}

	dir := filepath.Join(runsDir, resolvedID)

	// Read meta.json.
	var meta runner.RunMeta
	metaData, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return nil, fmt.Errorf("reading meta.json: %w", err)
	}
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return nil, fmt.Errorf("parsing meta.json: %w", err)
	}

	// Read events.jsonl.
	events, err := readEvents(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("reading events.jsonl: %w", err)
	}

	// Read effective-prompt.md (optional).
	prompt := ""
	if data, err := os.ReadFile(filepath.Join(dir, "effective-prompt.md")); err == nil {
		prompt = string(data)
	}

	return &SavedRun{
		Meta:   meta,
		Events: events,
		Prompt: prompt,
	}, nil
}

// ResolveRunID finds a run directory matching the given prefix.
// If exactly one directory starts with prefix, its name is returned.
// If multiple match, an error listing them is returned.
// If none match, a "not found" error is returned.
func ResolveRunID(runsDir, prefix string) (string, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", fmt.Errorf("reading runs directory: %w", err)
	}

	var matches []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), prefix) {
			matches = append(matches, e.Name())
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no run found matching %q", prefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous run-id %q matches %d runs:\n  %s",
			prefix, len(matches), strings.Join(matches, "\n  "))
	}
}

// ListRuns returns metadata for all runs that have a valid meta.json,
// sorted by start time (newest first).
func ListRuns(runsDir string) ([]runner.RunMeta, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading runs directory: %w", err)
	}

	var runs []runner.RunMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(runsDir, e.Name(), "meta.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue // skip runs without meta.json
		}
		var meta runner.RunMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		runs = append(runs, meta)
	}

	// Sort by started_at descending (newest first).
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].StartedAt > runs[j].StartedAt
	})

	return runs, nil
}

// readEvents parses an events.jsonl file into a slice of Events.
func readEvents(path string) ([]runner.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []runner.Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var ev runner.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue // skip unparseable lines
		}
		events = append(events, ev)
	}

	return events, scanner.Err()
}
