package runner

import "strings"

// buildIterationPrompt returns the prompt that should be passed to the agent
// for one iteration. When there are no reminders, the base prompt is returned
// verbatim. Otherwise an `## Important Reminders` section is appended with one
// bullet per reminder, in the order they were added (oldest first). Persistent
// and one-off reminders render identically — persistence is a runner-side
// concern (whether to consume after the iteration completes).
func buildIterationPrompt(base string, reminders []Reminder) string {
	if len(reminders) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n## Important Reminders\n")
	for _, r := range reminders {
		b.WriteString("\n- ")
		b.WriteString(r.Text)
	}
	return b.String()
}
