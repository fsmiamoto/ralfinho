package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// operatorLogger writes operator actions to <run-dir>/operator-log.jsonl.
//
// The file is opened with O_APPEND and synced after every entry so a kill
// between writes never corrupts earlier lines. Concurrency is provided by the
// controlState mutex held by the runner around handleControlMsg and the
// consumeOneOffs call sites — the logger itself does not lock.
//
// A nil receiver is a no-op for every method, mirroring how runner.eventsFile
// is treated when the file fails to open.
type operatorLogger struct {
	w   io.Writer // typically *os.File; tests may inject a buffer
	sf  syncer    // optional, for *os.File durability
	log func(format string, args ...any)
}

type syncer interface {
	Sync() error
}

type operatorEntry struct {
	TS           string   `json:"ts"`
	Action       string   `json:"action"`
	Value        string   `json:"value,omitempty"`
	Previous     string   `json:"previous,omitempty"`
	Kind         string   `json:"kind,omitempty"`
	ID           string   `json:"id,omitempty"`
	IDs          []string `json:"ids,omitempty"`
	Text         string   `json:"text,omitempty"`
	Iteration    int      `json:"iteration,omitempty"`
	RestartCount int      `json:"restart_count,omitempty"`
	ReminderIDs  []string `json:"reminder_ids,omitempty"`
}

// newOperatorLogger wraps an open file handle. logf is used for warnings on
// write failures; it may be nil.
func newOperatorLogger(f *os.File, logf func(format string, args ...any)) *operatorLogger {
	if f == nil {
		return nil
	}
	return &operatorLogger{w: f, sf: f, log: logf}
}

// timeoutString formats a timeout pointer for the audit log:
//   - nil pointer (default) → ""
//   - pointer to 0 (disabled) → "0"
//   - positive → time.Duration.String()
func timeoutString(d *time.Duration) string {
	if d == nil {
		return ""
	}
	if *d == 0 {
		return "0"
	}
	return d.String()
}

func (l *operatorLogger) write(e operatorEntry) {
	if l == nil {
		return
	}
	e.TS = time.Now().UTC().Format(time.RFC3339)
	data, err := json.Marshal(e)
	if err != nil {
		if l.log != nil {
			l.log("warning: marshal operator entry: %v\n", err)
		}
		return
	}
	if _, err := fmt.Fprintln(l.w, string(data)); err != nil {
		if l.log != nil {
			l.log("warning: writing operator-log.jsonl: %v\n", err)
		}
		return
	}
	if l.sf != nil {
		if err := l.sf.Sync(); err != nil && l.log != nil {
			l.log("warning: sync operator-log.jsonl: %v\n", err)
		}
	}
}

func (l *operatorLogger) logTimeoutSet(prev, next *time.Duration) {
	l.write(operatorEntry{
		Action:   "timeout_set",
		Value:    timeoutString(next),
		Previous: timeoutString(prev),
	})
}

func (l *operatorLogger) logReminderAdd(r Reminder) {
	kind := "oneoff"
	if r.Kind == ReminderPersistent {
		kind = "persistent"
	}
	l.write(operatorEntry{
		Action: "reminder_add",
		Kind:   kind,
		ID:     r.ID,
		Text:   r.Text,
	})
}

func (l *operatorLogger) logReminderRemove(id string) {
	l.write(operatorEntry{
		Action: "reminder_remove",
		ID:     id,
	})
}

// logRestartRequested records a restart request. restartCount is the
// upcoming attempt number (e.g. 1 for the first restart of an iteration).
func (l *operatorLogger) logRestartRequested(iteration, restartCount int, reminderIDs []string) {
	l.write(operatorEntry{
		Action:       "restart_requested",
		Iteration:    iteration,
		RestartCount: restartCount,
		ReminderIDs:  reminderIDs,
	})
}

func (l *operatorLogger) logOneOffConsumed(ids []string, iteration int) {
	if len(ids) == 0 {
		return
	}
	l.write(operatorEntry{
		Action:    "oneoff_consumed",
		IDs:       ids,
		Iteration: iteration,
	})
}
