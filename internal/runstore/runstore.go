package runstore

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"ralfinho/internal/eventlog"
)

func CreateRunDir(runsRoot string) (runID string, runDir string, err error) {
	if runsRoot == "" {
		return "", "", fmt.Errorf("runs root cannot be empty")
	}
	if err := os.MkdirAll(runsRoot, 0o755); err != nil {
		return "", "", fmt.Errorf("create runs root: %w", err)
	}

	id, err := newID()
	if err != nil {
		return "", "", err
	}
	dir := filepath.Join(runsRoot, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", fmt.Errorf("create run directory: %w", err)
	}
	return id, dir, nil
}

type Artifacts struct {
	runDir      string
	eventsFile  *os.File
	rawFile     *os.File
	sessionFile *os.File
	EventsCount int
}

func OpenArtifacts(runDir string) (*Artifacts, error) {
	eventsFile, err := os.OpenFile(filepath.Join(runDir, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open events file: %w", err)
	}
	rawFile, err := os.OpenFile(filepath.Join(runDir, "raw-output.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		_ = eventsFile.Close()
		return nil, fmt.Errorf("open raw output log: %w", err)
	}
	sessionFile, err := os.OpenFile(filepath.Join(runDir, "session.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		_ = eventsFile.Close()
		_ = rawFile.Close()
		return nil, fmt.Errorf("open session log: %w", err)
	}

	return &Artifacts{runDir: runDir, eventsFile: eventsFile, rawFile: rawFile, sessionFile: sessionFile}, nil
}

func (a *Artifacts) Close() error {
	var firstErr error
	for _, f := range []*os.File{a.eventsFile, a.rawFile, a.sessionFile} {
		if f == nil {
			continue
		}
		if err := f.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (a *Artifacts) AppendRawOutput(iteration int, output string) error {
	if _, err := fmt.Fprintf(a.rawFile, "\n=== iteration %d (%s) ===\n", iteration, time.Now().Format(time.RFC3339)); err != nil {
		return err
	}
	_, err := a.rawFile.WriteString(output)
	return err
}

func (a *Artifacts) AppendSessionLine(line string) error {
	_, err := fmt.Fprintf(a.sessionFile, "%s %s\n", time.Now().Format(time.RFC3339), line)
	return err
}

func (a *Artifacts) AppendEvents(events []eventlog.Event) error {
	for _, ev := range events {
		b, err := json.Marshal(ev)
		if err != nil {
			return fmt.Errorf("marshal event: %w", err)
		}
		if _, err := a.eventsFile.Write(append(b, '\n')); err != nil {
			return fmt.Errorf("write event: %w", err)
		}
		a.EventsCount++
	}
	return nil
}

type Meta struct {
	RunID               string    `json:"run_id"`
	StartedAt           time.Time `json:"started_at"`
	EndedAt             time.Time `json:"ended_at,omitempty"`
	Status              string    `json:"status"`
	Agent               string    `json:"agent"`
	PromptSource        string    `json:"prompt_source"`
	PromptFile          string    `json:"prompt_file,omitempty"`
	PlanFile            string    `json:"plan_file,omitempty"`
	MaxIterations       int       `json:"max_iterations"`
	IterationsCompleted int       `json:"iterations_completed"`
	EventsCount         int       `json:"events_count"`
}

func WriteMeta(runDir string, meta Meta) error {
	path := filepath.Join(runDir, "meta.json")
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("write meta: %w", err)
	}
	return nil
}

func ReadMeta(runDir string) (Meta, error) {
	path := filepath.Join(runDir, "meta.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return Meta{}, fmt.Errorf("read meta: %w", err)
	}
	var meta Meta
	if err := json.Unmarshal(b, &meta); err != nil {
		return Meta{}, fmt.Errorf("parse meta: %w", err)
	}
	return meta, nil
}

func ReadEvents(runDir string) ([]eventlog.Event, error) {
	path := filepath.Join(runDir, "events.jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open events: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	events := make([]eventlog.Event, 0, 128)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev eventlog.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("parse events line %d: %w", lineNo, err)
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan events: %w", err)
	}
	return events, nil
}

func newID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
