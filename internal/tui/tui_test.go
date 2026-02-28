package tui

import (
	"errors"
	"testing"

	"ralfinho/internal/eventlog"
	"ralfinho/internal/runner"
	"ralfinho/internal/runstore"
)

func TestRunStatusLevel(t *testing.T) {
	cases := []struct {
		status string
		want   statusLevel
	}{
		{status: string(runner.StatusCompleted), want: statusSuccess},
		{status: string(runner.StatusInterrupted), want: statusWarn},
		{status: string(runner.StatusMaxIterationsReached), want: statusWarn},
		{status: string(runner.StatusFailed), want: statusError},
		{status: "running", want: statusInfo},
	}

	for _, tc := range cases {
		if got := runStatusLevel(tc.status); got != tc.want {
			t.Fatalf("runStatusLevel(%q)=%v want %v", tc.status, got, tc.want)
		}
	}
}

func TestIsErrorEvent(t *testing.T) {
	if !isErrorEvent(eventlog.Event{Type: "tool_error"}) {
		t.Fatal("expected tool_error to be detected as error")
	}
	if !isErrorEvent(eventlog.Event{Content: "command failed with exit code 1"}) {
		t.Fatal("expected failed content to be detected as error")
	}
	if isErrorEvent(eventlog.Event{Type: "assistant", Content: "all good"}) {
		t.Fatal("did not expect normal event to be detected as error")
	}
}

func TestUpdateRunFinishedSetsStatusAndHighlight(t *testing.T) {
	m := NewLiveModel("run-1", runstore.Meta{Status: "running"}, make(chan bool, 1), make(chan struct{}, 1))

	_, _ = m.Update(RunFinishedMessage{Result: runner.Result{Status: runner.StatusCompleted}})
	if m.meta.Status != string(runner.StatusCompleted) {
		t.Fatalf("meta status not updated: got %q", m.meta.Status)
	}
	if m.statusLevel != statusSuccess {
		t.Fatalf("expected success status level, got %v", m.statusLevel)
	}

	_, _ = m.Update(RunFinishedMessage{Result: runner.Result{Status: runner.StatusFailed}, Err: errors.New("boom")})
	if m.meta.Status != string(runner.StatusFailed) {
		t.Fatalf("meta status not set to failed: got %q", m.meta.Status)
	}
	if m.statusLevel != statusError {
		t.Fatalf("expected error status level, got %v", m.statusLevel)
	}
}

func TestUpdateIterationErrorHighlight(t *testing.T) {
	m := NewLiveModel("run-1", runstore.Meta{Status: "running"}, make(chan bool, 1), make(chan struct{}, 1))

	_, _ = m.Update(IterationMessage{Report: runner.IterationReport{Iteration: 2, Err: errors.New("bad")}})
	if m.statusLevel != statusError {
		t.Fatalf("expected error status level, got %v", m.statusLevel)
	}
}
