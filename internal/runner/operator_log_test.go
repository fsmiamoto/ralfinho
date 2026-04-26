package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fsmiamoto/ralfinho/internal/events"
)

// readOperatorLog returns the parsed entries from operator-log.jsonl in the
// given run directory. Empty lines are skipped.
func readOperatorLog(t *testing.T, runDir string) []operatorEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(runDir, "operator-log.jsonl"))
	if err != nil {
		t.Fatalf("read operator-log.jsonl: %v", err)
	}
	var entries []operatorEntry
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e operatorEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("parse line %q: %v", line, err)
		}
		entries = append(entries, e)
	}
	return entries
}

func TestOperatorLogger_NilReceiver_Noop(t *testing.T) {
	var l *operatorLogger
	// Each method must be safe to call on a nil receiver.
	l.logTimeoutSet(nil, nil)
	l.logReminderAdd(Reminder{ID: "rmd-x"})
	l.logReminderRemove("rmd-x")
	l.logRestartRequested(1, 1, []string{"rmd-x"})
	l.logOneOffConsumed([]string{"rmd-x"}, 1)
}

func TestOperatorLogger_TimeoutString(t *testing.T) {
	cases := []struct {
		name string
		in   *time.Duration
		want string
	}{
		{"nil", nil, ""},
		{"zero", durPtr(0), "0"},
		{"positive", durPtr(15 * time.Minute), "15m0s"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := timeoutString(c.in); got != c.want {
				t.Errorf("timeoutString(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func durPtr(d time.Duration) *time.Duration { return &d }

func TestOperatorLogger_WritesEachActionType(t *testing.T) {
	var buf bytes.Buffer
	l := &operatorLogger{w: &buf}

	prev := durPtr(5 * time.Minute)
	next := durPtr(15 * time.Minute)
	l.logTimeoutSet(prev, next)
	l.logReminderAdd(Reminder{ID: "rmd-aaaa", Kind: ReminderPersistent, Text: "always lint"})
	l.logReminderAdd(Reminder{ID: "rmd-bbbb", Kind: ReminderOneOff, Text: "focus on auth"})
	l.logReminderRemove("rmd-aaaa")
	l.logRestartRequested(3, 1, []string{"rmd-bbbb"})
	l.logOneOffConsumed([]string{"rmd-bbbb"}, 3)

	var entries []operatorEntry
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e operatorEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		entries = append(entries, e)
	}

	if len(entries) != 6 {
		t.Fatalf("entries = %d, want 6", len(entries))
	}

	tsRe := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)
	for i, e := range entries {
		if !tsRe.MatchString(e.TS) {
			t.Errorf("entry %d ts = %q, want RFC3339-second-precision UTC", i, e.TS)
		}
	}

	if e := entries[0]; e.Action != "timeout_set" || e.Value != "15m0s" || e.Previous != "5m0s" {
		t.Errorf("timeout_set entry = %+v", e)
	}
	if e := entries[1]; e.Action != "reminder_add" || e.Kind != "persistent" || e.ID != "rmd-aaaa" || e.Text != "always lint" {
		t.Errorf("reminder_add[persistent] entry = %+v", e)
	}
	if e := entries[2]; e.Action != "reminder_add" || e.Kind != "oneoff" || e.ID != "rmd-bbbb" || e.Text != "focus on auth" {
		t.Errorf("reminder_add[oneoff] entry = %+v", e)
	}
	if e := entries[3]; e.Action != "reminder_remove" || e.ID != "rmd-aaaa" {
		t.Errorf("reminder_remove entry = %+v", e)
	}
	if e := entries[4]; e.Action != "restart_requested" || e.Iteration != 3 || e.RestartCount != 1 || len(e.ReminderIDs) != 1 || e.ReminderIDs[0] != "rmd-bbbb" {
		t.Errorf("restart_requested entry = %+v", e)
	}
	if e := entries[5]; e.Action != "oneoff_consumed" || e.Iteration != 3 || len(e.IDs) != 1 || e.IDs[0] != "rmd-bbbb" {
		t.Errorf("oneoff_consumed entry = %+v", e)
	}
}

func TestOperatorLogger_OneOffConsumedEmptyIDs_NoEntry(t *testing.T) {
	var buf bytes.Buffer
	l := &operatorLogger{w: &buf}
	l.logOneOffConsumed(nil, 1)
	l.logOneOffConsumed([]string{}, 1)
	if buf.Len() != 0 {
		t.Errorf("empty IDs should not write a line; got %q", buf.String())
	}
}

// TestRun_OperatorLog_RecordsTimeoutAddRemoveRestart drives a runner through
// an iteration where the operator sets a timeout, adds two reminders (persistent
// + one-off), removes one, and requests a restart. After completion, the log
// is asserted line-by-line.
func TestRun_OperatorLog_RecordsTimeoutAddRemoveRestart(t *testing.T) {
	started := make(chan struct{}, 4)
	hangThenCancel := func(ctx context.Context, _ func(events.Event)) (string, error) {
		started <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	}
	complete := func(_ context.Context, _ func(events.Event)) (string, error) {
		started <- struct{}{}
		return completionMarker, nil
	}
	fa := &flexAgent{behaviors: []agentBehavior{hangThenCancel, complete}}

	controlCh := make(chan ControlMsg, 8)
	runsDir := t.TempDir()
	r := New(RunConfig{
		Agent:       "test",
		Prompt:      "base prompt",
		RunsDir:     runsDir,
		ControlChan: controlCh,
	})
	r.iterAgent = fa
	r.stderr = io.Discard

	runDone := make(chan RunResult, 1)
	go func() {
		runDone <- r.Run(context.Background())
	}()

	// Wait for the first agent call to start.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first agent call did not start")
	}

	// Send: timeout change, reminder add (persistent), reminder add (oneoff),
	// remove the persistent, request restart. The runner serialises these
	// through the control goroutine so they apply in order.
	newTimeout := durPtr(15 * time.Minute)
	controlCh <- ControlMsg{Kind: ControlSetTimeout, Timeout: newTimeout}
	controlCh <- ControlMsg{Kind: ControlAddReminder, Reminder: Reminder{Kind: ReminderPersistent, Text: "lint always"}}
	controlCh <- ControlMsg{Kind: ControlAddReminder, Reminder: Reminder{Kind: ReminderOneOff, Text: "fix auth"}}

	// Wait for the reminders to be added before requesting the restart so the
	// restart_requested entry's reminder_ids snapshot is populated.
	if !waitFor(func() bool { return len(r.control.snapshotReminders()) == 2 }, 2*time.Second) {
		t.Fatal("reminders not added in time")
	}
	// Capture IDs for later assertions.
	snap := r.control.snapshotReminders()
	var persistID, oneOffID string
	for _, rem := range snap {
		if rem.Kind == ReminderPersistent {
			persistID = rem.ID
		} else {
			oneOffID = rem.ID
		}
	}
	if persistID == "" || oneOffID == "" {
		t.Fatalf("reminder IDs not populated: %+v", snap)
	}

	controlCh <- ControlMsg{Kind: ControlRemoveReminder, ID: persistID}
	if !waitFor(func() bool { return len(r.control.snapshotReminders()) == 1 }, 2*time.Second) {
		t.Fatal("persistent reminder not removed in time")
	}

	controlCh <- ControlMsg{Kind: ControlRequestRestart}

	// Wait for the second (completing) agent call.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("second agent call did not start")
	}

	result := <-runDone
	if result.Status != StatusCompleted {
		t.Fatalf("status = %s, want %s", result.Status, StatusCompleted)
	}

	entries := readOperatorLog(t, filepath.Join(runsDir, result.RunID))
	if len(entries) < 6 {
		t.Fatalf("entries = %d (%+v), want at least 6", len(entries), entries)
	}

	// Check first 5 are in order: set, add, add, remove, restart.
	if e := entries[0]; e.Action != "timeout_set" || e.Value != "15m0s" || e.Previous != "" {
		t.Errorf("entry 0 = %+v, want timeout_set 15m0s from default", e)
	}
	if e := entries[1]; e.Action != "reminder_add" || e.Kind != "persistent" || e.ID != persistID {
		t.Errorf("entry 1 = %+v, want reminder_add persistent %s", e, persistID)
	}
	if e := entries[2]; e.Action != "reminder_add" || e.Kind != "oneoff" || e.ID != oneOffID {
		t.Errorf("entry 2 = %+v, want reminder_add oneoff %s", e, oneOffID)
	}
	if e := entries[3]; e.Action != "reminder_remove" || e.ID != persistID {
		t.Errorf("entry 3 = %+v, want reminder_remove %s", e, persistID)
	}
	if e := entries[4]; e.Action != "restart_requested" || e.Iteration != 1 || e.RestartCount != 1 {
		t.Errorf("entry 4 = %+v, want restart_requested iter=1 restart_count=1", e)
	}
	// At restart time, the only remaining reminder is the one-off.
	if got := entries[4].ReminderIDs; len(got) != 1 || got[0] != oneOffID {
		t.Errorf("entry 4 reminder_ids = %v, want [%s]", got, oneOffID)
	}

	// Find the oneoff_consumed entry (it follows the completing iteration).
	var consumed *operatorEntry
	for i := 5; i < len(entries); i++ {
		if entries[i].Action == "oneoff_consumed" {
			c := entries[i]
			consumed = &c
			break
		}
	}
	if consumed == nil {
		t.Fatalf("missing oneoff_consumed entry; entries=%+v", entries)
	}
	if consumed.Iteration != 1 || len(consumed.IDs) != 1 || consumed.IDs[0] != oneOffID {
		t.Errorf("oneoff_consumed = %+v, want iter=1 ids=[%s]", *consumed, oneOffID)
	}
}

// TestRun_OperatorLog_NilLoggerWhenFileFails verifies the runner does not
// panic when operator-log.jsonl cannot be opened. We simulate this by making
// the run directory unwritable after writeEffectivePrompt.
func TestRun_OperatorLog_NilLoggerWhenFileFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based denial doesn't apply on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses chmod-based denial")
	}

	runsDir := t.TempDir()
	runID := "no-perm-run"
	runDir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Drop write permissions on the run dir so OpenFile for operator-log fails.
	if err := os.Chmod(runDir, 0555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(runDir, 0755) })

	r := &Runner{
		cfg:          RunConfig{RunsDir: runsDir},
		runID:        runID,
		stderr:       io.Discard,
		control:      newControlState(nil),
		restartCount: make(map[int]int),
	}
	r.openRunFiles()
	if r.operatorLog != nil {
		t.Fatalf("operatorLog should be nil when file open fails")
	}

	// All log methods must be safe to call.
	r.operatorLog.logTimeoutSet(nil, durPtr(time.Minute))
	r.operatorLog.logReminderAdd(Reminder{ID: "rmd-1"})
	r.operatorLog.logReminderRemove("rmd-1")
	r.operatorLog.logRestartRequested(1, 1, nil)
	r.operatorLog.logOneOffConsumed([]string{"rmd-1"}, 1)

	// And handleControlMsg, which is the production caller, must also not panic.
	r.handleControlMsg(ControlMsg{Kind: ControlSetTimeout, Timeout: durPtr(time.Minute)})
	r.handleControlMsg(ControlMsg{Kind: ControlAddReminder, Reminder: Reminder{Kind: ReminderOneOff, Text: "x"}})

	r.closeRunFiles()
}

func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
