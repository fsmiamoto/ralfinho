package tui

import "github.com/charmbracelet/lipgloss"

// Color constants for the blue-accent theme.
var (
	ColorAccent    = lipgloss.Color("69")  // blue, primary accent
	colorUser      = lipgloss.Color("75")  // lighter blue
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

)

// Stream pane event styles — pre-computed to avoid allocations per render.
var eventStyles = map[DisplayEventType]lipgloss.Style{
	DisplayUserMsg:       lipgloss.NewStyle().Foreground(colorUser),
	DisplayAssistantText: lipgloss.NewStyle().Foreground(colorBright),
	DisplayToolStart:     lipgloss.NewStyle().Foreground(colorTool),
	DisplayToolUpdate:    lipgloss.NewStyle().Foreground(colorTool),
	DisplayToolEnd:       lipgloss.NewStyle().Foreground(colorTool),
	DisplayThinking:      lipgloss.NewStyle().Foreground(colorThinking),
	DisplayTurnEnd:       lipgloss.NewStyle().Foreground(colorDim),
	DisplayAgentEnd:      lipgloss.NewStyle().Foreground(colorDim),
	DisplayIteration:     lipgloss.NewStyle().Foreground(colorIteration).Bold(true),
	DisplaySession:       lipgloss.NewStyle().Foreground(colorInfo),
	DisplayInfo:          lipgloss.NewStyle().Foreground(colorInfo),
}

var defaultEventStyle = lipgloss.NewStyle().Foreground(colorBright)

// eventStyle returns the pre-computed style for a given event type.
func eventStyle(evType string) lipgloss.Style {
	if s, ok := eventStyles[evType]; ok {
		return s
	}
	return defaultEventStyle
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

// Tool header styles (the "toolname ok" line inside the box).
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

// Info text style (for BlockInfo blocks in the main view).
var infoTextStyle = lipgloss.NewStyle().
	Foreground(colorInfo)

// Browser state card styles (delete confirmation, resume confirmation, etc.).
var (
	browserCardBorder = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				Padding(0, 1).
				BorderForeground(colorDim)

	browserCardBorderWarning = lipgloss.NewStyle().
					Border(lipgloss.RoundedBorder()).
					Padding(0, 1).
					BorderForeground(colorTool)

	browserCardTitle = lipgloss.NewStyle().
				Foreground(ColorAccent).
				Bold(true)

	browserCardTitleWarning = lipgloss.NewStyle().
				Foreground(colorTool).
				Bold(true)

	dismissHintStyle = lipgloss.NewStyle().Faint(true)
)
