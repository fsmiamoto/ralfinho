package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fsmiamoto/ralfinho/internal/runner"
)

func TestNewModelInitializesDefaultsAndInitBatch(t *testing.T) {
	ch := make(chan runner.Event)
	m := NewModel(ch)

	if m.eventCh != ch {
		t.Fatal("NewModel() did not keep the supplied event channel")
	}
	if m.converter == nil {
		t.Fatal("NewModel() converter = nil, want initialized converter")
	}
	if !m.running {
		t.Fatal("NewModel() running = false, want true")
	}
	if m.status != "Starting..." {
		t.Fatalf("NewModel() status = %q, want %q", m.status, "Starting...")
	}
	if !m.autoScroll || !m.mainAutoScroll {
		t.Fatalf("NewModel() auto-scroll flags = (%v, %v), want both true", m.autoScroll, m.mainAutoScroll)
	}
	if m.activeToolIdx != -1 {
		t.Fatalf("NewModel() activeToolIdx = %d, want -1", m.activeToolIdx)
	}
	if m.startTime.IsZero() {
		t.Fatal("NewModel() startTime is zero, want initialization time")
	}
	if got := m.RunResult(); got != nil {
		t.Fatalf("RunResult() = %#v, want nil before DoneMsg", got)
	}

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() returned nil, want batched wait/tick commands")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init() message type = %T, want tea.BatchMsg", cmd())
	}
	if len(batch) != 2 {
		t.Fatalf("Init() batch length = %d, want 2 commands", len(batch))
	}
}

func TestNewViewerModelBuildsBlocksAndDefaultsAgentName(t *testing.T) {
	events := []DisplayEvent{
		{Type: DisplayIteration, Iteration: 2},
		{Type: DisplayAssistantText, Iteration: 2, Detail: "saved answer"},
		{Type: DisplayToolStart, Iteration: 2, ToolCallID: "tool-1", ToolName: "bash", ToolDisplayArgs: "$ ls"},
		{Type: DisplayToolEnd, Iteration: 2, ToolCallID: "tool-1", ToolName: "bash", ToolResultText: "ok"},
		{Type: DisplayUserMsg, Iteration: 2, Detail: "ignored in main view"},
	}
	meta := runner.RunMeta{
		RunID:               "1234567890abcdef",
		StartedAt:           "2026-03-15T12:00:00Z",
		Status:              "completed",
		IterationsCompleted: 2,
	}

	m := NewViewerModel(events, meta)

	if m.running {
		t.Fatal("NewViewerModel() running = true, want false")
	}
	if m.autoScroll || m.mainAutoScroll {
		t.Fatalf("NewViewerModel() auto-scroll flags = (%v, %v), want both false", m.autoScroll, m.mainAutoScroll)
	}
	if m.activeToolIdx != -1 {
		t.Fatalf("NewViewerModel() activeToolIdx = %d, want -1", m.activeToolIdx)
	}
	for _, want := range []string{"Run 12345678", "pi", "completed", "2026-03-15T12:00:00Z", "2 iterations"} {
		if !strings.Contains(m.status, want) {
			t.Fatalf("NewViewerModel() status = %q, want substring %q", m.status, want)
		}
	}
	if len(m.blocks) != 3 {
		t.Fatalf("NewViewerModel() built %d blocks, want 3", len(m.blocks))
	}
	if m.blocks[1].Kind != BlockAssistantText || m.blocks[1].Text != "saved answer" {
		t.Fatalf("assistant block = %#v, want saved assistant text block", m.blocks[1])
	}
	if m.blocks[2].Kind != BlockToolCall || !m.blocks[2].ToolDone || m.blocks[2].ToolResult != "ok" {
		t.Fatalf("tool block = %#v, want completed tool call", m.blocks[2])
	}
}

func TestWaitForEventHandlesNilBufferedAndClosedChannels(t *testing.T) {
	t.Run("nil channel", func(t *testing.T) {
		if cmd := (Model{}).waitForEvent(); cmd != nil {
			t.Fatalf("waitForEvent() = %v, want nil when event channel is nil", cmd)
		}
	})

	t.Run("buffered event", func(t *testing.T) {
		ch := make(chan runner.Event, 1)
		want := runner.Event{Type: runner.EventTurnEnd, ID: "turn-1"}
		ch <- want

		cmd := Model{eventCh: ch}.waitForEvent()
		if cmd == nil {
			t.Fatal("waitForEvent() returned nil for a live event channel")
		}
		msg := cmd()
		raw, ok := msg.(rawEventMsg)
		if !ok {
			t.Fatalf("waitForEvent() message type = %T, want rawEventMsg", msg)
		}
		got := runner.Event(raw)
		if got.Type != want.Type || got.ID != want.ID {
			t.Fatalf("waitForEvent() event = %#v, want %#v", got, want)
		}
	})

	t.Run("closed channel", func(t *testing.T) {
		ch := make(chan runner.Event)
		close(ch)

		cmd := Model{eventCh: ch}.waitForEvent()
		if cmd == nil {
			t.Fatal("waitForEvent() returned nil command for a closed channel")
		}
		if msg := cmd(); msg != nil {
			t.Fatalf("waitForEvent() on closed channel = %#v, want nil", msg)
		}
	})
}

func TestModelUpdateHandlesWindowSizeStatusEventDoneAndSpinner(t *testing.T) {
	m := NewModel(nil)

	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 72, Height: 24})
	if cmd != nil {
		t.Fatalf("Update(WindowSizeMsg) returned cmd %v, want nil", cmd)
	}
	m = updated.(Model)
	if m.width != 72 || m.height != 24 {
		t.Fatalf("Update(WindowSizeMsg) size = (%d, %d), want (72, 24)", m.width, m.height)
	}

	updated, cmd = m.Update(EventMsg(DisplayEvent{Type: DisplayInfo, Summary: "info", Detail: "info detail"}))
	if cmd != nil {
		t.Fatalf("Update(EventMsg) returned cmd %v, want nil", cmd)
	}
	m = updated.(Model)
	if len(m.events) != 1 || len(m.blocks) != 1 {
		t.Fatalf("Update(EventMsg) produced %d events and %d blocks, want 1 each", len(m.events), len(m.blocks))
	}

	updated, cmd = m.Update(StatusMsg{Text: "Working"})
	if cmd != nil {
		t.Fatalf("Update(StatusMsg) returned cmd %v, want nil", cmd)
	}
	m = updated.(Model)
	if m.status != "Working" {
		t.Fatalf("Update(StatusMsg) status = %q, want %q", m.status, "Working")
	}

	updated, cmd = m.Update(m.spinner.Tick())
	if cmd == nil {
		t.Fatal("Update(spinner.TickMsg) returned nil cmd, want follow-up spinner tick")
	}
	m = updated.(Model)

	result := runner.RunResult{Agent: "pi", Status: runner.StatusFailed, Iterations: 4, Error: "permission denied"}
	updated, cmd = m.Update(DoneMsg{Result: result})
	if cmd != nil {
		t.Fatalf("Update(DoneMsg) returned cmd %v, want nil", cmd)
	}
	m = updated.(Model)
	if m.running {
		t.Fatal("Update(DoneMsg) running = true, want false")
	}
	if m.errorOverlay != "permission denied" {
		t.Fatalf("Update(DoneMsg) errorOverlay = %q, want %q", m.errorOverlay, "permission denied")
	}
	if got := m.RunResult(); got == nil || got.Status != runner.StatusFailed || got.Iterations != 4 {
		t.Fatalf("RunResult() = %#v, want failed result with 4 iterations", got)
	}
	if !strings.Contains(m.status, "Done — pi | failed (4 iterations)") {
		t.Fatalf("Update(DoneMsg) status = %q, want formatted completion status", m.status)
	}
}

func TestModelUpdateRawEventMsgConvertsAndSchedulesNextRead(t *testing.T) {
	ch := make(chan runner.Event, 1)
	next := runner.Event{Type: runner.EventTurnEnd, ID: "turn-2"}
	ch <- next

	m := NewModel(ch)
	updated, cmd := m.Update(rawEventMsg(runner.Event{Type: runner.EventIteration, ID: "iteration-2"}))
	if cmd == nil {
		t.Fatal("Update(rawEventMsg) returned nil cmd, want follow-up waitForEvent command")
	}
	m = updated.(Model)

	if m.iteration != 2 {
		t.Fatalf("iteration = %d, want 2", m.iteration)
	}
	if m.status != "Iteration #2" {
		t.Fatalf("status = %q, want %q", m.status, "Iteration #2")
	}
	if len(m.events) != 1 || m.events[0].Type != DisplayIteration {
		t.Fatalf("events = %#v, want one DisplayIteration event", m.events)
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

func TestModelKeyHandlingDismissesOverlayAndManagesQuitConfirmation(t *testing.T) {
	m := Model{errorOverlay: "boom"}
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'a'}}))
	if m.errorOverlay != "" {
		t.Fatalf("errorOverlay = %q, want dismissal on any key", m.errorOverlay)
	}

	m, cmd := updateModelWithCmd(t, Model{}, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'q'}}))
	if cmd != nil {
		t.Fatalf("q returned cmd %v, want nil before confirmation", cmd)
	}
	if !m.confirmQuit || m.confirmCtrlC {
		t.Fatalf("after q confirm flags = (%v, %v), want (true, false)", m.confirmQuit, m.confirmCtrlC)
	}

	m, cmd = updateModelWithCmd(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'n'}}))
	if isQuitCmd(cmd) {
		t.Fatal("non-confirmation key should cancel quit, not quit")
	}
	if m.confirmQuit {
		t.Fatal("confirmQuit = true after cancellation, want false")
	}

	m, cmd = updateModelWithCmd(t, Model{}, tea.KeyMsg(tea.Key{Type: tea.KeyCtrlC}))
	if !m.confirmQuit || !m.confirmCtrlC {
		t.Fatalf("after first ctrl+c confirm flags = (%v, %v), want (true, true)", m.confirmQuit, m.confirmCtrlC)
	}
	m, cmd = updateModelWithCmd(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyCtrlC}))
	if !isQuitCmd(cmd) {
		t.Fatal("second ctrl+c should return tea.Quit")
	}

	m, cmd = updateModelWithCmd(t, Model{}, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'q'}}))
	m, cmd = updateModelWithCmd(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'y'}}))
	if !isQuitCmd(cmd) {
		t.Fatal("q followed by y should return tea.Quit")
	}
}

func TestModelKeyHandlingNavigatesMainStreamAndDetailPanes(t *testing.T) {
	m := Model{height: 20, focusedPane: 0, mainAutoScroll: true}

	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'j'}}))
	if m.mainScroll != 1 || m.mainAutoScroll {
		t.Fatalf("after main j: mainScroll=%d mainAutoScroll=%v, want 1/false", m.mainScroll, m.mainAutoScroll)
	}
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'k'}}))
	if m.mainScroll != 0 {
		t.Fatalf("after main k: mainScroll=%d, want 0", m.mainScroll)
	}
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyCtrlD}))
	if m.mainScroll != m.mainHeight()/2 {
		t.Fatalf("after main ctrl+d: mainScroll=%d, want %d", m.mainScroll, m.mainHeight()/2)
	}
	m.mainScroll = 1
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyCtrlU}))
	if m.mainScroll != 0 {
		t.Fatalf("after main ctrl+u: mainScroll=%d, want 0", m.mainScroll)
	}
	m.mainScroll = 3
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'g'}}))
	if m.mainScroll != 0 || m.mainAutoScroll {
		t.Fatalf("after main g: mainScroll=%d mainAutoScroll=%v, want 0/false", m.mainScroll, m.mainAutoScroll)
	}
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'G'}}))
	if m.mainScroll != 999999 || !m.mainAutoScroll {
		t.Fatalf("after main G: mainScroll=%d mainAutoScroll=%v, want 999999/true", m.mainScroll, m.mainAutoScroll)
	}
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyTab}))
	if m.focusedPane != 1 {
		t.Fatalf("after tab focusedPane=%d, want 1", m.focusedPane)
	}
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'r'}}))
	if !m.rawMode {
		t.Fatal("after r rawMode = false, want true")
	}

	m.height = 16
	m.events = []DisplayEvent{
		{Summary: "event 1", Detail: "detail 1"},
		{Summary: "event 2", Detail: "detail 2"},
		{Summary: "event 3", Detail: "detail 3"},
		{Summary: "event 4", Detail: "detail 4"},
		{Summary: "event 5", Detail: "detail 5"},
		{Summary: "event 6", Detail: "detail 6"},
	}
	m.cursor = 0
	m.streamScroll = 0
	m.detailScroll = 2
	m.autoScroll = false

	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'G'}}))
	if m.cursor != len(m.events)-1 || !m.autoScroll || m.detailScroll != 0 {
		t.Fatalf("after stream G: cursor=%d autoScroll=%v detailScroll=%d, want last/true/0", m.cursor, m.autoScroll, m.detailScroll)
	}
	if m.streamScroll == 0 {
		t.Fatalf("after stream G: streamScroll=%d, want non-zero to keep cursor visible", m.streamScroll)
	}
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'k'}}))
	if m.cursor != len(m.events)-2 || m.autoScroll {
		t.Fatalf("after stream k: cursor=%d autoScroll=%v, want %d/false", m.cursor, m.autoScroll, len(m.events)-2)
	}
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'g'}}))
	if m.cursor != 0 || m.streamScroll != 0 || m.detailScroll != 0 || m.autoScroll {
		t.Fatalf("after stream g: cursor=%d streamScroll=%d detailScroll=%d autoScroll=%v, want 0/0/0/false", m.cursor, m.streamScroll, m.detailScroll, m.autoScroll)
	}
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'j'}}))
	if m.cursor != 1 {
		t.Fatalf("after stream j: cursor=%d, want 1", m.cursor)
	}

	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyTab}))
	if m.focusedPane != 2 {
		t.Fatalf("after second tab focusedPane=%d, want 2", m.focusedPane)
	}
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'j'}}))
	if m.detailScroll != 1 {
		t.Fatalf("after detail j: detailScroll=%d, want 1", m.detailScroll)
	}
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyCtrlD}))
	if m.detailScroll != 1+m.paneHeight()/2 {
		t.Fatalf("after detail ctrl+d: detailScroll=%d, want %d", m.detailScroll, 1+m.paneHeight()/2)
	}
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyCtrlU}))
	if m.detailScroll != 1 {
		t.Fatalf("after detail ctrl+u: detailScroll=%d, want 1", m.detailScroll)
	}
	m = updateModel(t, m, tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'k'}}))
	if m.detailScroll != 0 {
		t.Fatalf("after detail k: detailScroll=%d, want 0", m.detailScroll)
	}
}

func updateModel(t *testing.T, model Model, msg tea.Msg) Model {
	t.Helper()
	updated, _ := model.Update(msg)
	next, ok := updated.(Model)
	if !ok {
		t.Fatalf("updated model has type %T, want Model", updated)
	}
	return next
}

func updateModelWithCmd(t *testing.T, model Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := model.Update(msg)
	next, ok := updated.(Model)
	if !ok {
		t.Fatalf("updated model has type %T, want Model", updated)
	}
	return next, cmd
}
