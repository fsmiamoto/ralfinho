package runner

import (
	"strings"
	"testing"
)

func TestBuildIterationPrompt_NoReminders_ReturnsBase(t *testing.T) {
	base := "do the thing"
	got := buildIterationPrompt(base, nil)
	if got != base {
		t.Errorf("got %q, want %q", got, base)
	}
	if got := buildIterationPrompt(base, []Reminder{}); got != base {
		t.Errorf("empty slice: got %q, want %q", got, base)
	}
}

func TestBuildIterationPrompt_AppendsHeaderAndBullets(t *testing.T) {
	base := "do the thing"
	reminders := []Reminder{
		{ID: "r1", Kind: ReminderOneOff, Text: "first"},
		{ID: "r2", Kind: ReminderPersistent, Text: "second"},
	}

	got := buildIterationPrompt(base, reminders)

	if !strings.HasPrefix(got, base) {
		t.Errorf("output does not start with base prompt: %q", got)
	}
	if strings.Count(got, "## Important Reminders") != 1 {
		t.Errorf("section header should appear exactly once, got: %q", got)
	}
	if !strings.Contains(got, "- first") {
		t.Errorf("first bullet missing: %q", got)
	}
	if !strings.Contains(got, "- second") {
		t.Errorf("second bullet missing: %q", got)
	}
	// Bullets must appear in input order (oldest first).
	if strings.Index(got, "- first") > strings.Index(got, "- second") {
		t.Errorf("ordering broken; first should precede second: %q", got)
	}
}

func TestBuildIterationPrompt_NoKindPrefix(t *testing.T) {
	// A persistent and a one-off with the same text must render identically —
	// the agent does not need to know which is which.
	reminders := []Reminder{
		{ID: "a", Kind: ReminderOneOff, Text: "same"},
		{ID: "b", Kind: ReminderPersistent, Text: "same"},
	}
	got := buildIterationPrompt("base", reminders)
	if strings.Contains(got, "[P]") || strings.Contains(got, "persistent") || strings.Contains(got, "one-off") {
		t.Errorf("output should not leak reminder kind to the agent: %q", got)
	}
	// Both bullets render the same way; expect two `- same` lines.
	if c := strings.Count(got, "- same"); c != 2 {
		t.Errorf("expected 2 `- same` bullets, got %d in %q", c, got)
	}
}

func TestBuildIterationPrompt_BaseWithTrailingNewline(t *testing.T) {
	base := "do the thing\n"
	reminders := []Reminder{{ID: "r1", Text: "x"}}
	got := buildIterationPrompt(base, reminders)
	// The "\n\n" separator works whether the base ends with a newline or not;
	// we just need the base to be intact and the section to appear.
	if !strings.HasPrefix(got, base) {
		t.Errorf("base prompt not preserved: %q", got)
	}
	if !strings.Contains(got, "## Important Reminders") {
		t.Errorf("section header missing: %q", got)
	}
}
