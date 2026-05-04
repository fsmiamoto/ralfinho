package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	BlockIteration     BlockKind = iota // ── iteration N ──
	BlockAssistantText                  // rendered markdown prose
	BlockThinking                       // single dim summary line
	BlockToolCall                       // bordered tool box with args/result
	BlockInfo                           // informational text
)

// MainBlock represents a single rendered unit in the main (live) view.
type MainBlock struct {
	Kind           BlockKind
	Iteration      int
	Text           string // accumulated markdown for BlockAssistantText
	AssistantFinal bool   // true when the assistant message is complete
	AssistantModel string // model label for assistant metadata
	ToolName       string // for BlockToolCall
	ToolCallID     string // to match tool_start with tool_end
	ToolArgs       string // formatted: "$ cmd" for bash, filepath for read/edit/write
	ToolResult     string // raw result text
	ToolDone       bool
	ToolError      bool
	ThinkingLen    int    // char count for thinking summary
	InfoText       string // for BlockInfo
	StartTime      time.Time
	EndTime        time.Time
	Duration       time.Duration

	// Layout cache: rendered screen lines for a given width.
	// Nil layoutLines means the cache is stale and must be recomputed.
	layoutWidth int      // width the layout was last computed for
	layoutLines []string // cached rendered screen lines
}

// Render produces the styled string for this block at the given width.
func (b *MainBlock) Render(width int) string {
	switch b.Kind {
	case BlockIteration:
		return b.renderIteration(width)
	case BlockAssistantText:
		return b.renderAssistantText(width)
	case BlockThinking:
		return b.renderThinking()
	case BlockToolCall:
		return b.renderToolCall(width)
	case BlockInfo:
		return b.renderInfo()
	default:
		return ""
	}
}

// Layout returns the cached screen lines for this block at the given width.
// If the cache is stale (different width or invalidated), the block is
// re-rendered and the result cached. Returns nil for blocks that render to
// an empty string. Callers must not modify the returned slice.
func (b *MainBlock) Layout(width int) []string {
	if b.layoutLines != nil && b.layoutWidth == width {
		return b.layoutLines
	}
	rendered := b.Render(width)
	if rendered == "" {
		b.layoutLines = nil
		b.layoutWidth = width
		return nil
	}
	b.layoutLines = strings.Split(rendered, "\n")
	b.layoutWidth = width
	return b.layoutLines
}

// InvalidateLayout marks the block's cached layout as stale, forcing
// re-rendering on the next Layout call.
func (b *MainBlock) InvalidateLayout() {
	b.layoutLines = nil
}

func (b *MainBlock) renderIteration(width int) string {
	label := fmt.Sprintf("iteration %d", b.Iteration)
	if ts := formatBlockTime(b.StartTime); ts != "" {
		label += " · " + ts
	}
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
	content := renderAssistantContent(b.Text, width, b.AssistantFinal)
	header := b.assistantMetadataHeader()
	if header == "" {
		return content
	}
	if content == "" {
		return header
	}
	return header + "\n" + content
}

// renderAssistantContent is the shared helper for rendering assistant text in
// both the main pane and the detail pane. While streaming (final=false), it
// returns plain wrapped text to avoid expensive Markdown rendering. Once the
// message is complete (final=true), it renders full Markdown.
func renderAssistantContent(text string, width int, final bool) string {
	if text == "" {
		return ""
	}
	if final {
		return renderMarkdown(text, width)
	}
	return WrapText(text, width)
}

func (b *MainBlock) renderThinking() string {
	line := fmt.Sprintf("  thinking (%d chars)", b.ThinkingLen)
	return thinkingLineStyle.Render(line)
}

func formatBlockTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("15:04:05")
}

func formatBlockDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return compactDuration(d.Truncate(time.Second))
}

func blockTimingSegments(start time.Time, duration time.Duration) []string {
	var segments []string
	if ts := formatBlockTime(start); ts != "" {
		segments = append(segments, ts)
	}
	if elapsed := formatBlockDuration(duration); elapsed != "" {
		segments = append(segments, elapsed)
	}
	return segments
}

func (b *MainBlock) assistantMetadataHeader() string {
	start := formatBlockTime(b.StartTime)
	if start == "" && b.AssistantModel == "" && b.Duration <= 0 {
		return ""
	}

	// Keep start-time semantics visually first: "15:04:05 assistant · model · 38s".
	header := "assistant"
	if start != "" {
		header = start + " " + header
	}
	if b.AssistantModel != "" {
		header += " · " + b.AssistantModel
	}
	if elapsed := formatBlockDuration(b.Duration); elapsed != "" {
		header += " · " + elapsed
	}
	return thinkingLineStyle.Render(header)
}

func (b *MainBlock) toolHeader() string {
	var status string
	var style lipgloss.Style
	if b.ToolError {
		status = fmt.Sprintf("%s !", b.ToolName)
		style = toolHeaderErrorStyle
	} else if b.ToolDone {
		status = fmt.Sprintf("%s ok", b.ToolName)
		style = toolHeaderStyle
	} else {
		status = fmt.Sprintf("%s ...", b.ToolName)
		style = toolHeaderStyle
	}

	header := style.Render(status)
	if segments := blockTimingSegments(b.StartTime, b.Duration); len(segments) > 0 {
		header += toolSepStyle.Render(" · " + strings.Join(segments, " · "))
	}
	return header
}

func (b *MainBlock) renderToolCall(width int) string {
	// Build the header: toolname [status] plus optional timing metadata.
	header := b.toolHeader()

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
	return infoTextStyle.Render(b.InfoText)
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
