package tui

import "github.com/charmbracelet/lipgloss"

// Color constants for the blue-accent theme.
var (
	ColorAccent    = lipgloss.Color("69")  // blue, primary accent
	colorUser      = lipgloss.Color("75")  // lighter blue
	colorAssistant = lipgloss.Color("114") // soft green
	colorTool      = lipgloss.Color("214") // orange
	colorError     = lipgloss.Color("196") // red
	colorThinking  = lipgloss.Color("183") // lighter purple
	colorDim       = lipgloss.Color("242") // gray
	colorBright    = lipgloss.Color("255") // white
	colorIteration = lipgloss.Color("111") // blue-cyan
	colorInfo      = lipgloss.Color("248") // light gray
)

// Pane border styles.
var (
	focusedBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorAccent)

	unfocusedBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorDim)
)

// Header styles for the top bar.
var (
	headerStyle = lipgloss.NewStyle().
			Background(ColorAccent).
			Foreground(lipgloss.Color("255")).
			Bold(true).
			Padding(0, 1)

	headerDimStyle = lipgloss.NewStyle().
			Background(ColorAccent).
			Foreground(lipgloss.Color("153")).
			Bold(true).
			Padding(0, 1)
)

// Selection styles.
var (
	selectedStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			Foreground(lipgloss.Color("255")).
			Bold(true)

	selectedIndicator = lipgloss.NewStyle().
				Foreground(ColorAccent).
				Bold(true)
)

// Status bar styles.
var (
	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Background(lipgloss.Color("237")).
			Padding(0, 1)

	statusKeyStyle = lipgloss.NewStyle().
			Foreground(ColorAccent).
			Bold(true)

	statusSepStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
)

// Pane title styles.
var (
	titleStyle = lipgloss.NewStyle().
			Foreground(ColorAccent).
			Bold(true)

	titleCountStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))
)

// iterationBarStyle is used for iteration separators in the stream.
var iterationBarStyle = lipgloss.NewStyle().
	Foreground(colorIteration).
	Bold(true)

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

// Tool box border styles (for MainBlock tool rendering in the main view).
var (
	toolBoxRunning = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorDim).
			Padding(0, 1)

	toolBoxDone = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorTool).
			Padding(0, 1)

	toolBoxError = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorError).
			Padding(0, 1)
)

// Tool header styles (the "⚙ toolname ✓" line inside the box).
var (
	toolHeaderStyle = lipgloss.NewStyle().
			Foreground(colorTool).
			Bold(true)

	toolHeaderErrorStyle = lipgloss.NewStyle().
				Foreground(colorError).
				Bold(true)
)

// Thinking line style.
var thinkingLineStyle = lipgloss.NewStyle().
	Foreground(colorThinking).
	Italic(true)

// Iteration rule style (for the ━━━ line in main view).
var iterationRuleStyle = lipgloss.NewStyle().
	Foreground(colorIteration).
	Bold(true)

// Tool result separator.
var toolSepStyle = lipgloss.NewStyle().
	Foreground(colorDim)

// Tool result text.
var toolResultStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("250"))
