package runner

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// ControlKind identifies the kind of a ControlMsg.
type ControlKind int

const (
	// ControlSetTimeout updates the inactivity timeout. Timeout semantics:
	// nil = default; non-nil pointer to 0 = disabled; positive = custom.
	ControlSetTimeout ControlKind = iota
	// ControlAddReminder appends a reminder. The runner assigns the ID.
	ControlAddReminder
	// ControlRemoveReminder removes a reminder by ID.
	ControlRemoveReminder
	// ControlRequestRestart cancels the current iteration and redoes it
	// without incrementing the iteration counter.
	ControlRequestRestart
)

// ReminderKind distinguishes one-off vs persistent reminders.
type ReminderKind int

const (
	// ReminderOneOff is consumed when an iteration runs to completion or
	// continues normally. Restart and watchdog timeout do not consume it.
	ReminderOneOff ReminderKind = iota
	// ReminderPersistent stays until explicitly removed.
	ReminderPersistent
)

// Reminder is a single steering note appended to the prompt.
type Reminder struct {
	ID   string // assigned by runner on Add
	Kind ReminderKind
	Text string
}

// ControlMsg is the typed message sent from the TUI to the runner.
// Exactly one of the per-kind fields is meaningful for each Kind.
type ControlMsg struct {
	Kind     ControlKind
	Timeout  *time.Duration // for ControlSetTimeout
	Reminder Reminder       // for ControlAddReminder
	ID       string         // for ControlRemoveReminder
}

// controlState holds the live, mutex-guarded mutable parameters of a run.
//
// All access goes through the accessor methods so the lock discipline stays
// in one place. The struct is created in newControlState from RunConfig and
// lives on the Runner for its lifetime.
type controlState struct {
	mu sync.Mutex

	// timeout follows the same semantics as RunConfig.InactivityTimeout:
	// nil = default; pointer to 0 = disabled; positive = custom.
	timeout *time.Duration

	reminders        []Reminder
	restartRequested bool
}

// newControlState constructs a controlState seeded from the initial timeout.
func newControlState(initial *time.Duration) *controlState {
	return &controlState{timeout: initial}
}

// effectiveTimeout returns the watchdog duration. The caller must check
// watchdogDisabled() first; if disabled, the returned value is meaningless.
func (c *controlState) effectiveTimeout() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return resolveInactivityTimeout(c.timeout)
}

// watchdogDisabled reports whether the watchdog should be turned off entirely.
func (c *controlState) watchdogDisabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.timeout != nil && *c.timeout == 0
}

// watchdogState returns whether the watchdog is currently disabled and the
// effective timeout duration in a single atomic snapshot. When disabled is
// true, the timeout value is meaningless.
func (c *controlState) watchdogState() (disabled bool, timeout time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timeout != nil && *c.timeout == 0 {
		return true, 0
	}
	return false, resolveInactivityTimeout(c.timeout)
}

// setTimeout replaces the live timeout pointer and returns the previous value
// in a single critical section, so callers can record the transition without
// racing other writers.
func (c *controlState) setTimeout(d *time.Duration) (prev *time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prev = c.timeout
	c.timeout = d
	return prev
}

// snapshotReminders returns a copy of the current reminders; mutating the
// returned slice does not affect runner state.
func (c *controlState) snapshotReminders() []Reminder {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reminders) == 0 {
		return nil
	}
	out := make([]Reminder, len(c.reminders))
	copy(out, c.reminders)
	return out
}

// addReminder appends a reminder, assigning a unique ID. Returns the stored
// reminder (with ID populated).
func (c *controlState) addReminder(r Reminder) Reminder {
	c.mu.Lock()
	defer c.mu.Unlock()
	r.ID = newReminderID()
	c.reminders = append(c.reminders, r)
	return r
}

// removeReminder removes the reminder with the given ID. Returns true if a
// reminder was found and removed.
func (c *controlState) removeReminder(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, r := range c.reminders {
		if r.ID == id {
			c.reminders = append(c.reminders[:i], c.reminders[i+1:]...)
			return true
		}
	}
	return false
}

// consumeOneOffs removes all one-off reminders and returns their IDs.
// Persistent reminders are left in place.
func (c *controlState) consumeOneOffs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reminders) == 0 {
		return nil
	}
	var consumed []string
	kept := c.reminders[:0]
	for _, r := range c.reminders {
		if r.Kind == ReminderOneOff {
			consumed = append(consumed, r.ID)
			continue
		}
		kept = append(kept, r)
	}
	c.reminders = kept
	return consumed
}

// requestRestart sets the restart flag.
func (c *controlState) requestRestart() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.restartRequested = true
}

// takeRestartRequested returns the current restart flag and clears it.
func (c *controlState) takeRestartRequested() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.restartRequested
	c.restartRequested = false
	return v
}

// newReminderID returns a short unique ID like "rmd-1a2b3c4d".
func newReminderID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "rmd-00000000"
	}
	return "rmd-" + hex.EncodeToString(buf[:])
}
