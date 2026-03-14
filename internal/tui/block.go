package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)
// normalizeToolName maps tool name variants from different agent backends
// to a canonical lowercase form. Comparison is case-insensitive so that
// "Bash", "bash", "BASH" all normalize to "bash".
func normalizeToolName(name string) string {
	switch strings.ToLower(name) {
	case "bash", "shell", "execute":
		return "bash"
	case "read":
		return "read"
	case "edit":
		return "edit"
	case "write":
		return "write"
	default:
		return name
	}
}


// BlockKind identifies the type of content block rendered in the main view.
type BlockKind int

const (
	BlockIteration    BlockKind = iota // ── iteration N ──
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
	label := fmt.Sprintf("iteration %d", b.Iteration)
	// Fill remaining width with ─ characters.
	labelW := 3 + len(label) + 1 // "── " prefix + label + " " trailing
	remaining := width - labelW
	if remaining < 3 {
		remaining = 3
	}
	rule := "── " + label + " " + strings.Repeat("─", remaining)
	return iterationRuleStyle.Render(rule)
}

func (b *MainBlock) renderAssistantText(width int) string {
	if b.Text == "" {
		return ""
	}
	return renderMarkdown(b.Text, width)
}

func (b *MainBlock) renderThinking() string {
	line := fmt.Sprintf("  thinking (%d chars)", b.ThinkingLen)
	return thinkingLineStyle.Render(line)
}

func (b *MainBlock) renderToolCall(width int, spinnerView string) string {
	// Build the header: toolname [status]
	var header string
	if b.ToolError {
		header = toolHeaderErrorStyle.Render(fmt.Sprintf("%s !", b.ToolName))
	} else if b.ToolDone {
		header = toolHeaderStyle.Render(fmt.Sprintf("%s ok", b.ToolName))
	} else {
		status := "..."
		if spinnerView != "" {
			status = spinnerView
		}
		header = toolHeaderStyle.Render(fmt.Sprintf("%s %s", b.ToolName, status))
	}

	// Build inner content.
	var inner []string
	inner = append(inner, header)

	if b.ToolArgs != "" {
		inner = append(inner, b.ToolArgs)
	}

	if b.ToolDone && b.ToolResult != "" {
		// For read/write/edit, don't dump file contents — just show a
		// line count summary.  Full output is in the Detail pane.
		normalized := normalizeToolName(b.ToolName)
		if normalized == "read" || normalized == "write" || normalized == "edit" {
			lines := strings.Count(b.ToolResult, "\n") + 1
			summary := fmt.Sprintf("(%d lines)", lines)
			inner = append(inner, toolResultStyle.Render(summary))
		} else {
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
//
// Detection proceeds in three stages:
//  1. Name-based: use the normalized tool name to pick the right field.
//  2. Content-based: inspect the JSON keys as a fallback for unrecognized names.
//  3. Raw JSON: truncate to 80 characters as a last resort.
func formatToolArgs(toolName string, rawArgs json.RawMessage) string {
	if rawArgs == nil {
		return ""
	}

	switch normalizeToolName(toolName) {
	case "bash":
		var args struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(rawArgs, &args) == nil && args.Command != "" {
			return "$ " + args.Command
		}
	case "read", "edit", "write":
		var args struct {
			Path     string `json:"path"`
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal(rawArgs, &args) == nil {
			if args.Path != "" {
				return args.Path
			}
			if args.FilePath != "" {
				return args.FilePath
			}
		}
	}

	// Content-based fallback: detect tool type from JSON structure without
	// relying on the tool name. This handles cases where the tool name is an
	// unrecognized variant (e.g. a kiro-specific label).
	var generic map[string]json.RawMessage
	if json.Unmarshal(rawArgs, &generic) == nil {
		// A "command" key strongly suggests a shell/exec tool.
		if raw, ok := generic["command"]; ok {
			var cmd string
			if json.Unmarshal(raw, &cmd) == nil && cmd != "" {
				return "$ " + cmd
			}
		}
		// A "path" or "file_path" key suggests a file operation.
		for _, key := range []string{"path", "file_path"} {
			if raw, ok := generic[key]; ok {
				var p string
				if json.Unmarshal(raw, &p) == nil && p != "" {
					return p
				}
			}
		}
	}

	// Last resort: first 80 runes of raw JSON.
	s := string(rawArgs)
	s = strings.ReplaceAll(s, "\n", " ")
	runes := []rune(s)
	if len(runes) > 80 {
		return string(runes[:77]) + "..."
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
