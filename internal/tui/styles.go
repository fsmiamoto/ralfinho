package tui

import "github.com/charmbracelet/lipgloss"

// Color constants for event types.
var (
	colorUser      = lipgloss.Color("69")  // blue
	colorAssistant = lipgloss.Color("78")  // green
	colorTool      = lipgloss.Color("214") // yellow/orange
	colorError     = lipgloss.Color("196") // red
	colorThinking  = lipgloss.Color("139") // muted purple
	colorDim       = lipgloss.Color("240") // gray
	colorBright    = lipgloss.Color("255") // white
	colorIteration = lipgloss.Color("117") // cyan
	colorInfo      = lipgloss.Color("248") // light gray
)

// Pane border styles.
var (
	focusedBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("69"))

	unfocusedBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240"))
)

// selectedStyle highlights the currently selected stream item.
var selectedStyle = lipgloss.NewStyle().
	Reverse(true).
	Bold(true)

// statusBarStyle renders the bottom status bar.
var statusBarStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("252")).
	Background(lipgloss.Color("236")).
	Padding(0, 1)

// titleStyle renders pane titles.
var titleStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("252"))

// eventStyle returns the style for a given event type.
func eventStyle(evType string) lipgloss.Style {
	switch evType {
	case "user_msg":
		return lipgloss.NewStyle().Foreground(colorUser)
	case "assistant_text":
		return lipgloss.NewStyle().Foreground(colorAssistant)
	case "tool_start":
		return lipgloss.NewStyle().Foreground(colorTool)
	case "tool_end":
		return lipgloss.NewStyle().Foreground(colorTool)
	case "thinking":
		return lipgloss.NewStyle().Foreground(colorThinking)
	case "turn_end", "agent_end":
		return lipgloss.NewStyle().Foreground(colorDim)
	case "iteration":
		return lipgloss.NewStyle().Foreground(colorIteration).Bold(true)
	case "session":
		return lipgloss.NewStyle().Foreground(colorInfo)
	case "info":
		return lipgloss.NewStyle().Foreground(colorInfo)
	default:
		return lipgloss.NewStyle().Foreground(colorBright)
	}
}

// errorEventStyle is for tool errors.
var errorEventStyle = lipgloss.NewStyle().Foreground(colorError)
