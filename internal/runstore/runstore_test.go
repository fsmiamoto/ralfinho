package runstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ralfinho/internal/eventlog"
)

func TestArtifactsAndMeta(t *testing.T) {
	runDir := t.TempDir()
	a, err := OpenArtifacts(runDir)
	if err != nil {
		t.Fatalf("open artifacts: %v", err)
	}
	defer a.Close()

	ts := time.Now().UTC().Truncate(time.Second)
	event := eventlog.Event{Type: "assistant", Iteration: 1, Timestamp: ts, Content: "hello"}

	if err := a.AppendRawOutput(1, "hello\n"); err != nil {
		t.Fatalf("append raw: %v", err)
	}
	if err := a.AppendSessionLine("iteration complete"); err != nil {
		t.Fatalf("append session: %v", err)
	}
	if err := a.AppendEvents([]eventlog.Event{event}); err != nil {
		t.Fatalf("append events: %v", err)
	}

	meta := Meta{RunID: "abc", StartedAt: ts, EndedAt: ts.Add(time.Second), Status: "completed", Agent: "pi", PromptSource: "plan", MaxIterations: 0, IterationsCompleted: 1, EventsCount: a.EventsCount}
	if err := WriteMeta(runDir, meta); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	for _, name := range []string{"events.jsonl", "raw-output.log", "session.log", "meta.json"} {
		if _, err := os.Stat(filepath.Join(runDir, name)); err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
	}

	loadedMeta, err := ReadMeta(runDir)
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if loadedMeta.RunID != meta.RunID || loadedMeta.Status != meta.Status {
		t.Fatalf("meta mismatch: got %+v want %+v", loadedMeta, meta)
	}

	loadedEvents, err := ReadEvents(runDir)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(loadedEvents) != 1 {
		t.Fatalf("expected 1 event, got %d", len(loadedEvents))
	}
	got := loadedEvents[0]
	if got.Type != event.Type || got.Content != event.Content || got.Iteration != event.Iteration {
		t.Fatalf("event mismatch: got %+v want %+v", got, event)
	}
}

func TestReadEvents_InvalidLine(t *testing.T) {
	runDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte("{invalid}\n"), 0o644); err != nil {
		t.Fatalf("write events: %v", err)
	}
	_, err := ReadEvents(runDir)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestReadMeta_InvalidJSON(t *testing.T) {
	runDir := t.TempDir()
	broken, _ := json.Marshal(map[string]any{"run_id": 123})
	if err := os.WriteFile(filepath.Join(runDir, "meta.json"), broken, 0o644); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	if _, err := ReadMeta(runDir); err == nil {
		t.Fatal("expected read meta error")
	}
}
