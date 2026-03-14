package tui

import "testing"

func TestTruncateStr(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{name: "short string unchanged", s: "hello", n: 10, want: "hello"},
		{name: "exact length unchanged", s: "hello", n: 5, want: "hello"},
		{name: "truncated with ellipsis", s: "hello world", n: 8, want: "hello..."},
		{name: "very small n", s: "hello", n: 2, want: "he"},
		{name: "n equals zero", s: "hello", n: 0, want: ""},
		{name: "newlines replaced", s: "hello\nworld", n: 20, want: "hello world"},
		{name: "newlines replaced then truncated", s: "hello\nworld\nfoo", n: 10, want: "hello w..."},
		{name: "multi-byte runes within limit", s: "こんにちは", n: 5, want: "こんにちは"},
		{name: "multi-byte runes truncated", s: "こんにちは世界", n: 5, want: "こん..."},
		{name: "emoji truncated", s: "👋🌍🎉🔥💡✨", n: 4, want: "👋..."},
		{name: "mixed ascii and multi-byte", s: "abc日本語def", n: 6, want: "abc..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateStr(tt.s, tt.n)
			if got != tt.want {
				t.Errorf("truncateStr(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
		})
	}
}
