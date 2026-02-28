package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// BlockKind identifies the type of content block rendered in the main view.
type BlockKind int

const (
	BlockIteration    BlockKind = iota // ━━━ Iteration N ━━━
	BlockAssistantText                 // rendered markdown prose
	BlockThinking                      // single dim summary line
	BlockToolCall                      // bordered tool box with args/result
	BlockInfo                          // informational text
)

// MainBlock represents a single rendered unit in the main (live) view.
type MainBlock struct {
	Kind        BlockKind
	Iteration   int
	Text        string          // accumulated markdown for BlockAssistantText
	ToolName    string          // for BlockToolCall
	ToolCallID  string          // to match tool_start with tool_end
	ToolArgs    string          // formatted: "$ cmd" for bash, filepath for read/edit/write
	ToolResult  string          // raw result text
	ToolDone    bool
	ToolError   bool
	ThinkingLen int             // char count for thinking summary
	InfoText    string          // for BlockInfo
}

// Render produces the styled string for this block at the given width.
// spinnerView is the current spinner frame (only used for in-progress tool calls).
func (b *MainBlock) Render(width int, spinnerView string) string {
	switch b.Kind {
	case BlockIteration:
		return b.renderIteration(width)
	case BlockAssistantText:
		return b.renderAssistantText(width)
	case BlockThinking:
		return b.renderThinking()
	case BlockToolCall:
		return b.renderToolCall(width, spinnerView)
	case BlockInfo:
		return b.renderInfo()
	default:
		return ""
	}
}

func (b *MainBlock) renderIteration(width int) string {
	label := fmt.Sprintf(" Iteration %d ", b.Iteration)
	// Fill remaining width with ━ characters.
	labelW := len(label) + 3 // "━━━" prefix
	remaining := width - labelW
	if remaining < 3 {
		remaining = 3
	}
	rule := "━━━" + label + strings.Repeat("━", remaining)
	return iterationRuleStyle.Render(rule)
}

func (b *MainBlock) renderAssistantText(width int) string {
	if b.Text == "" {
		return ""
	}
	return renderMarkdown(b.Text, width)
}

func (b *MainBlock) renderThinking() string {
	line := fmt.Sprintf("  💭 Thinking (%d chars)", b.ThinkingLen)
	return thinkingLineStyle.Render(line)
}

func (b *MainBlock) renderToolCall(width int, spinnerView string) string {
	// Build the header: ⚙ toolname [status]
	var header string
	if b.ToolError {
		header = toolHeaderErrorStyle.Render(fmt.Sprintf("⚙ %s ✗", b.ToolName))
	} else if b.ToolDone {
		header = toolHeaderStyle.Render(fmt.Sprintf("⚙ %s ✓", b.ToolName))
	} else {
		status := "◐"
		if spinnerView != "" {
			status = spinnerView
		}
		header = toolHeaderStyle.Render(fmt.Sprintf("⚙ %s %s", b.ToolName, status))
	}

	// Build inner content.
	var inner []string
	inner = append(inner, header)

	if b.ToolArgs != "" {
		inner = append(inner, b.ToolArgs)
	}

	if b.ToolDone && b.ToolResult != "" {
		// Inner content width: total width minus border (2) minus padding (2).
		sepW := width - 4
		if sepW < 10 {
			sepW = 10
		}
		sep := toolSepStyle.Render(strings.Repeat("─", sepW))
		inner = append(inner, sep)
		result := truncateResult(b.ToolResult, 6)
		inner = append(inner, toolResultStyle.Render(result))
	}

	content := strings.Join(inner, "\n")

	// Pick border style.
	boxWidth := width - 2 // account for border chars
	if boxWidth < 10 {
		boxWidth = 10
	}

	var style lipgloss.Style
	if b.ToolError {
		style = toolBoxError.Width(boxWidth)
	} else if b.ToolDone {
		style = toolBoxDone.Width(boxWidth)
	} else {
		style = toolBoxRunning.Width(boxWidth)
	}

	return style.Render(content)
}

func (b *MainBlock) renderInfo() string {
	return lipgloss.NewStyle().Foreground(colorInfo).Render(b.InfoText)
}

// formatToolArgs extracts a human-friendly summary from tool arguments.
func formatToolArgs(toolName string, rawArgs json.RawMessage) string {
	if rawArgs == nil {
		return ""
	}

	switch toolName {
	case "bash", "Bash":
		var args struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(rawArgs, &args) == nil && args.Command != "" {
			return "$ " + args.Command
		}
	case "read", "Read":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(rawArgs, &args) == nil && args.Path != "" {
			return args.Path
		}
	case "edit", "Edit":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(rawArgs, &args) == nil && args.Path != "" {
			return args.Path
		}
	case "write", "Write":
		var args struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(rawArgs, &args) == nil && args.Path != "" {
			return args.Path
		}
	}

	// Fallback: first 80 chars of raw JSON.
	s := string(rawArgs)
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}

// truncateResult shows the first maxLines lines of result text.
// If there are more, appends "… (N more lines)".
func truncateResult(result string, maxLines int) string {
	if result == "" {
		return ""
	}
	lines := strings.Split(result, "\n")
	if len(lines) <= maxLines {
		return result
	}
	remaining := len(lines) - maxLines
	truncated := strings.Join(lines[:maxLines], "\n")
	return truncated + fmt.Sprintf("\n… (%d more lines)", remaining)
}
