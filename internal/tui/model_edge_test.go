package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fsmiamoto/ralfinho/internal/runner"
)

func TestModelUpdateUnknownMessageIsNoOp(t *testing.T) {
	type noopMsg struct{}

	original := Model{
		status:      "Idle",
		width:       80,
		height:      24,
		mainScroll:  2,
		focusedPane: 1,
	}

	updated, cmd := original.Update(noopMsg{})
	if cmd != nil {
		t.Fatalf("Update(noopMsg) returned cmd %v, want nil", cmd)
	}

	m, ok := updated.(Model)
	if !ok {
		t.Fatalf("updated model has type %T, want Model", updated)
	}
	if m.status != original.status || m.width != original.width || m.height != original.height {
		t.Fatalf("Update(noopMsg) changed model basics to status=%q size=(%d,%d), want status=%q size=(%d,%d)", m.status, m.width, m.height, original.status, original.width, original.height)
	}
	if m.mainScroll != original.mainScroll || m.focusedPane != original.focusedPane {
		t.Fatalf("Update(noopMsg) changed navigation state to mainScroll=%d focusedPane=%d, want %d/%d", m.mainScroll, m.focusedPane, original.mainScroll, original.focusedPane)
	}
}

func TestModelUpdateRawEventWithoutDisplayEventsStillSchedulesNextRead(t *testing.T) {
	ch := make(chan runner.Event, 1)
	next := runner.Event{Type: runner.EventAgentEnd, ID: "agent-1"}
	ch <- next

	m := NewModel(ch)
	updated, cmd := m.Update(rawEventMsg(runner.Event{Type: runner.EventMessageUpdate}))
	if cmd == nil {
		t.Fatal("Update(rawEventMsg with no display events) returned nil cmd, want follow-up waitForEvent command")
	}
	m = updated.(Model)

	if len(m.events) != 0 {
		t.Fatalf("events = %#v, want none when converter returns no display events", m.events)
	}
	if len(m.blocks) != 0 {
		t.Fatalf("blocks = %#v, want none when converter returns no display events", m.blocks)
	}

	msg := cmd()
	raw, ok := msg.(rawEventMsg)
	if !ok {
		t.Fatalf("follow-up cmd message type = %T, want rawEventMsg", msg)
	}
	got := runner.Event(raw)
	if got.Type != next.Type || got.ID != next.ID {
		t.Fatalf("follow-up event = %#v, want %#v", got, next)
	}
}

func TestModelHandleKeyEdgeCaseNoOps(t *testing.T) {
	t.Run("ctrl+c confirmation cancels on other keys", func(t *testing.T) {
		m, _ := updateModelWithCmd(t, Model{}, tea.KeyMsg(tea.Key{Type: tea.KeyCtrlC}))
		m, cmd := updateModelWithCmd(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'x'}}))
		if isQuitCmd(cmd) {
			t.Fatal("non-ctrl+c key during ctrl+c confirmation should cancel quit, not quit")
		}
		if m.confirmQuit {
			t.Fatal("confirmQuit = true after cancelling ctrl+c confirmation, want false")
		}
	})

	t.Run("stream pane with no events ignores navigation keys", func(t *testing.T) {
		m := Model{focusedPane: 1, detailScroll: 2, autoScroll: false}
		m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'j'}}))
		m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'G'}}))

		if m.cursor != 0 || m.streamScroll != 0 || m.detailScroll != 2 || m.autoScroll {
			t.Fatalf("stream no-op navigation changed state to cursor=%d streamScroll=%d detailScroll=%d autoScroll=%v, want 0/0/2/false", m.cursor, m.streamScroll, m.detailScroll, m.autoScroll)
		}
	})

	t.Run("stream pane at bottom keeps cursor in place and re-enables auto-scroll", func(t *testing.T) {
		m := Model{
			height:       16,
			focusedPane:  1,
			events:       []DisplayEvent{{Summary: "only event", Detail: "detail"}},
			cursor:       0,
			detailScroll: 5,
			autoScroll:   false,
		}
		m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'j'}}))

		if m.cursor != 0 {
			t.Fatalf("cursor = %d, want to stay at 0 when already at the last event", m.cursor)
		}
		if m.detailScroll != 5 {
			t.Fatalf("detailScroll = %d, want unchanged when cursor does not move", m.detailScroll)
		}
		if !m.autoScroll {
			t.Fatal("autoScroll = false, want true when stream cursor is already at the end")
		}
	})

	t.Run("detail pane up at zero is a no-op", func(t *testing.T) {
		m := Model{focusedPane: 2, detailScroll: 0}
		m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'k'}}))
		if m.detailScroll != 0 {
			t.Fatalf("detailScroll = %d, want 0", m.detailScroll)
		}
	})

	t.Run("unhandled key leaves state unchanged", func(t *testing.T) {
		m := Model{status: "Idle", mainScroll: 3, focusedPane: 0}
		m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'x'}}))
		if m.status != "Idle" || m.mainScroll != 3 || m.focusedPane != 0 {
			t.Fatalf("unhandled key changed state to status=%q mainScroll=%d focusedPane=%d, want Idle/3/0", m.status, m.mainScroll, m.focusedPane)
		}
	})
}

func TestEnsureStreamCursorVisibleHandlesEarlierCursorAndVisibleCursor(t *testing.T) {
	t.Run("cursor above current window", func(t *testing.T) {
		m := Model{height: 16, cursor: 1, streamScroll: 4}
		m.ensureStreamCursorVisible()
		if m.streamScroll != 1 {
			t.Fatalf("streamScroll = %d, want 1 to bring the cursor back into view", m.streamScroll)
		}
	})

	t.Run("cursor already visible", func(t *testing.T) {
		m := Model{height: 16, cursor: 2, streamScroll: 1}
		m.ensureStreamCursorVisible()
		if m.streamScroll != 1 {
			t.Fatalf("streamScroll = %d, want unchanged visible window", m.streamScroll)
		}
	})
}

func TestModelLayoutHelpersClampMinimumDimensions(t *testing.T) {
	m := Model{width: 10, height: 4, paneRatio: 0.3}

	if got := m.streamWidth(); got != 16 {
		t.Fatalf("streamWidth() = %d, want minimum width 16", got)
	}
	if got := m.detailWidth(); got != 30 {
		t.Fatalf("detailWidth() = %d, want minimum width 30", got)
	}
	if got := m.paneHeight(); got != 3 {
		t.Fatalf("paneHeight() = %d, want minimum height 3", got)
	}
}
