package tui

import (
	"strings"
	"testing"
)

func TestWrapLine_ShortLine(t *testing.T) {
	got := WrapLine("hello", 80)
	if len(got) != 1 || got[0] != "hello" {
		t.Errorf("WrapLine short line = %v, want [hello]", got)
	}
}

func TestWrapLine_ExactWidth(t *testing.T) {
	got := WrapLine("hello", 5)
	if len(got) != 1 || got[0] != "hello" {
		t.Errorf("WrapLine exact width = %v, want [hello]", got)
	}
}

func TestWrapLine_WordWrap(t *testing.T) {
	got := WrapLine("hello world foo", 11)
	// "hello world" is 11 chars, fits. "foo" goes to next line.
	if len(got) != 2 {
		t.Fatalf("WrapLine word wrap got %d lines, want 2: %v", len(got), got)
	}
	if got[0] != "hello world" {
		t.Errorf("line 0 = %q, want %q", got[0], "hello world")
	}
	if got[1] != "foo" {
		t.Errorf("line 1 = %q, want %q", got[1], "foo")
	}
}

func TestWrapLine_MultipleWraps(t *testing.T) {
	got := WrapLine("aa bb cc dd", 5)
	// "aa bb" = 5, fits. "cc dd" = 5, fits.
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2: %v", len(got), got)
	}
	if got[0] != "aa bb" {
		t.Errorf("line 0 = %q, want %q", got[0], "aa bb")
	}
	if got[1] != "cc dd" {
		t.Errorf("line 1 = %q, want %q", got[1], "cc dd")
	}
}

func TestWrapLine_LongWordHardBreak(t *testing.T) {
	got := WrapLine("abcdefghij", 4)
	// Should hard-break: "abcd", "efgh", "ij"
	if len(got) != 3 {
		t.Fatalf("hard break got %d lines, want 3: %v", len(got), got)
	}
	if got[0] != "abcd" {
		t.Errorf("line 0 = %q, want %q", got[0], "abcd")
	}
	if got[1] != "efgh" {
		t.Errorf("line 1 = %q, want %q", got[1], "efgh")
	}
	if got[2] != "ij" {
		t.Errorf("line 2 = %q, want %q", got[2], "ij")
	}
}

func TestWrapLine_LongWordFollowedByShort(t *testing.T) {
	got := WrapLine("abcdefgh xy", 4)
	// "abcdefgh" hard-breaks to "abcd", "efgh"; then "xy" may merge or go next line.
	// "efgh" is 4 wide, adding " xy" would be 7 > 4, so "xy" goes to next line.
	if len(got) != 3 {
		t.Fatalf("got %d lines, want 3: %v", len(got), got)
	}
	if got[0] != "abcd" {
		t.Errorf("line 0 = %q, want %q", got[0], "abcd")
	}
	if got[1] != "efgh" {
		t.Errorf("line 1 = %q, want %q", got[1], "efgh")
	}
	if got[2] != "xy" {
		t.Errorf("line 2 = %q, want %q", got[2], "xy")
	}
}

func TestWrapLine_EmptyString(t *testing.T) {
	got := WrapLine("", 10)
	if len(got) != 1 || got[0] != "" {
		t.Errorf("WrapLine empty = %v, want [\"\"]", got)
	}
}

func TestWrapLine_ZeroWidth(t *testing.T) {
	got := WrapLine("hello", 0)
	// width <= 0 returns the line unchanged.
	if len(got) != 1 || got[0] != "hello" {
		t.Errorf("WrapLine zero width = %v, want [hello]", got)
	}
}

func TestWrapLine_NegativeWidth(t *testing.T) {
	got := WrapLine("hello", -5)
	if len(got) != 1 || got[0] != "hello" {
		t.Errorf("WrapLine negative width = %v, want [hello]", got)
	}
}

func TestWrapLine_SingleCharWidth(t *testing.T) {
	got := WrapLine("abc", 1)
	// Each char becomes its own line via hard-break.
	if len(got) != 3 {
		t.Fatalf("got %d lines, want 3: %v", len(got), got)
	}
	if got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("got %v, want [a b c]", got)
	}
}

func TestWrapLine_MultiByte(t *testing.T) {
	// CJK characters are typically 2 visual columns wide.
	got := WrapLine("日本語テスト", 6)
	// Each char is 2 wide, so 3 chars fit per line.
	// "日本語" = 6 wide, "テスト" = 6 wide.
	if len(got) != 2 {
		t.Fatalf("multi-byte got %d lines, want 2: %v", len(got), got)
	}
	if got[0] != "日本語" {
		t.Errorf("line 0 = %q, want %q", got[0], "日本語")
	}
	if got[1] != "テスト" {
		t.Errorf("line 1 = %q, want %q", got[1], "テスト")
	}
}

func TestWrapLine_MultiByteMixed(t *testing.T) {
	// "Hi 日本" = 2 + 1 + 2 + 2 = 7 visual columns
	got := WrapLine("Hi 日本", 5)
	// "Hi" fits (2), adding " 日" would be 2+1+2=5, fits. "本" is 2 more = 7, doesn't fit.
	// So line 1 = "Hi 日" (5 wide), line 2 = "本"
	if len(got) != 2 {
		t.Fatalf("mixed got %d lines, want 2: %v", len(got), got)
	}
}

func TestWrapLine_TrailingSpaces(t *testing.T) {
	// Words are split by space, so trailing spaces produce empty words.
	got := WrapLine("hello ", 10)
	// "hello " is 6 wide, fits in 10.
	if len(got) != 1 {
		t.Fatalf("trailing spaces got %d lines, want 1: %v", len(got), got)
	}
}

func TestWrapLine_MultipleSpaces(t *testing.T) {
	// "a  b" splits into ["a", "", "b"]; empty word has 0 width.
	got := WrapLine("a  b", 10)
	if len(got) != 1 {
		t.Fatalf("multiple spaces got %d lines, want 1: %v", len(got), got)
	}
}

// --- WrapText tests ---

func TestWrapText_PreservesNewlines(t *testing.T) {
	input := "line one\nline two\nline three"
	got := WrapText(input, 80)
	if got != input {
		t.Errorf("WrapText should preserve newlines.\ngot:  %q\nwant: %q", got, input)
	}
}

func TestWrapText_WrapsEachLine(t *testing.T) {
	input := "hello world\nfoo bar baz"
	got := WrapText(input, 5)
	lines := strings.Split(got, "\n")
	// "hello world" wraps to "hello" + "world" (2 lines)
	// "foo bar baz" wraps to at least 2 lines
	if len(lines) < 4 {
		t.Errorf("expected at least 4 lines, got %d: %v", len(lines), lines)
	}
}

func TestWrapText_ZeroWidth(t *testing.T) {
	input := "hello world"
	got := WrapText(input, 0)
	if got != input {
		t.Errorf("WrapText with width 0 should return unchanged, got %q", got)
	}
}

func TestWrapText_NegativeWidth(t *testing.T) {
	input := "hello world"
	got := WrapText(input, -1)
	if got != input {
		t.Errorf("WrapText with negative width should return unchanged, got %q", got)
	}
}

func TestWrapText_EmptyString(t *testing.T) {
	got := WrapText("", 80)
	if got != "" {
		t.Errorf("WrapText empty = %q, want empty", got)
	}
}

func TestWrapText_EmptyLines(t *testing.T) {
	input := "hello\n\nworld"
	got := WrapText(input, 80)
	if got != input {
		t.Errorf("WrapText should preserve empty lines.\ngot:  %q\nwant: %q", got, input)
	}
}

func TestWrapText_SingleLine(t *testing.T) {
	input := "short"
	got := WrapText(input, 80)
	if got != input {
		t.Errorf("WrapText single line = %q, want %q", got, input)
	}
}

// ---------------------------------------------------------------------------
// clipToWidth
// ---------------------------------------------------------------------------

func TestClipToWidth_ShortString(t *testing.T) {
	got := clipToWidth("hello", 80)
	if got != "hello" {
		t.Errorf("clipToWidth short = %q, want %q", got, "hello")
	}
}

func TestClipToWidth_ExactFit(t *testing.T) {
	got := clipToWidth("abcde", 5)
	if got != "abcde" {
		t.Errorf("clipToWidth exact = %q, want %q", got, "abcde")
	}
}

func TestClipToWidth_Truncates(t *testing.T) {
	got := clipToWidth("hello world", 5)
	if got != "hello" {
		t.Errorf("clipToWidth truncate = %q, want %q", got, "hello")
	}
}

func TestClipToWidth_Empty(t *testing.T) {
	got := clipToWidth("", 10)
	if got != "" {
		t.Errorf("clipToWidth empty = %q, want %q", got, "")
	}
}

func TestClipToWidth_CJK(t *testing.T) {
	// CJK characters are 2 columns wide.
	// "你好世" = 6 columns. With maxW=5, only "你好" (4 cols) fits.
	got := clipToWidth("你好世", 5)
	if got != "你好" {
		t.Errorf("clipToWidth CJK = %q, want %q", got, "你好")
	}
}

func TestClipToWidth_MixedASCIICJK(t *testing.T) {
	// "a你b" = 1+2+1 = 4 columns. With maxW=3, only "a你" won't fit (3 cols needed for 你).
	// "a" (1 col) fits, "你" needs 2 more → total 3, fits maxW=3.
	got := clipToWidth("a你b", 3)
	if got != "a你" {
		t.Errorf("clipToWidth mixed = %q, want %q", got, "a你")
	}
}

func TestClipToWidth_ZeroWidth(t *testing.T) {
	got := clipToWidth("hello", 0)
	if got != "" {
		t.Errorf("clipToWidth zero = %q, want %q", got, "")
	}
}

// ---------------------------------------------------------------------------
// truncateToWidth
// ---------------------------------------------------------------------------

func TestTruncateToWidth_ShortString(t *testing.T) {
	got := truncateToWidth("hello", 80)
	if got != "hello" {
		t.Errorf("truncateToWidth short = %q, want %q", got, "hello")
	}
}

func TestTruncateToWidth_Truncates(t *testing.T) {
	got := truncateToWidth("hello world!", 8)
	// maxW=8, reserves 3 for "...", clips to 5 cols → "hello" + "..."
	if got != "hello..." {
		t.Errorf("truncateToWidth truncate = %q, want %q", got, "hello...")
	}
}

func TestTruncateToWidth_MinWidth(t *testing.T) {
	// maxW < 4 is clamped to 4, so reserves 3 for "...", clips to 1 col.
	got := truncateToWidth("hello world", 2)
	if got != "h..." {
		t.Errorf("truncateToWidth minWidth = %q, want %q", got, "h...")
	}
}

func TestTruncateToWidth_ExactFit(t *testing.T) {
	got := truncateToWidth("abcde", 5)
	if got != "abcde" {
		t.Errorf("truncateToWidth exact = %q, want %q", got, "abcde")
	}
}

func TestTruncateToWidth_CJK(t *testing.T) {
	// "你好世界" = 8 columns. With maxW=7, reserves 3 for "...", clips to 4 cols → "你好" + "..."
	got := truncateToWidth("你好世界", 7)
	if got != "你好..." {
		t.Errorf("truncateToWidth CJK = %q, want %q", got, "你好...")
	}
}
