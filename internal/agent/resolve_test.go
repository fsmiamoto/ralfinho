package agent

import (
	"bytes"
	"strings"
	"testing"
)

func TestResolve_Pi(t *testing.T) {
	a, err := Resolve("pi")
	if err != nil {
		t.Fatalf("Resolve('pi') error: %v", err)
	}
	if _, ok := a.(*PiAgent); !ok {
		t.Errorf("expected *PiAgent, got %T", a)
	}
}

func TestResolve_Kiro(t *testing.T) {
	a, err := Resolve("kiro")
	if err != nil {
		t.Fatalf("Resolve('kiro') error: %v", err)
	}
	if _, ok := a.(*KiroAgent); !ok {
		t.Errorf("expected *KiroAgent, got %T", a)
	}
}

func TestResolve_Claude(t *testing.T) {
	a, err := Resolve("claude")
	if err != nil {
		t.Fatalf("Resolve('claude') error: %v", err)
	}
	if _, ok := a.(*ClaudeAgent); !ok {
		t.Errorf("expected *ClaudeAgent, got %T", a)
	}
}

func TestResolve_Unknown(t *testing.T) {
	_, err := Resolve("unknown-agent")
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
	if !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("error should mention 'unknown agent', got: %v", err)
	}
	if !strings.Contains(err.Error(), "pi, kiro, claude") {
		t.Errorf("error should list supported agents, got: %v", err)
	}
}

func TestResolve_ForwardsOptions(t *testing.T) {
	var buf bytes.Buffer
	a, err := Resolve("pi", WithRawWriter(&buf))
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	pa := a.(*PiAgent)
	if pa.opts.RawWriter != &buf {
		t.Error("RawWriter option was not forwarded to PiAgent")
	}
}

func TestResolve_ForwardsOptionsToKiro(t *testing.T) {
	var buf bytes.Buffer
	a, err := Resolve("kiro", WithRawWriter(&buf))
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	ka := a.(*KiroAgent)
	if ka.opts.RawWriter != &buf {
		t.Error("RawWriter option was not forwarded to KiroAgent")
	}
}

func TestResolve_ForwardsOptionsToClaude(t *testing.T) {
	var buf bytes.Buffer
	a, err := Resolve("claude", WithRawWriter(&buf))
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	ca := a.(*ClaudeAgent)
	if ca.opts.RawWriter != &buf {
		t.Error("RawWriter option was not forwarded to ClaudeAgent")
	}
}

func TestIsValid(t *testing.T) {
	valid := []string{"pi", "kiro", "claude"}
	for _, name := range valid {
		if !IsValid(name) {
			t.Errorf("IsValid(%q) = false, want true", name)
		}
	}

	invalid := []string{"", "unknown", "Pi", "KIRO", "Claude", "openai", "gemini"}
	for _, name := range invalid {
		if IsValid(name) {
			t.Errorf("IsValid(%q) = true, want false", name)
		}
	}
}

func TestIsValid_ConsistentWithResolve(t *testing.T) {
	// IsValid and Resolve must agree: every name that IsValid accepts
	// should Resolve without error, and vice versa.
	names := []string{"pi", "kiro", "claude", "unknown", ""}
	for _, name := range names {
		valid := IsValid(name)
		_, err := Resolve(name)
		resolves := err == nil
		if valid != resolves {
			t.Errorf("IsValid(%q)=%v but Resolve(%q) err=%v — they must agree",
				name, valid, name, err)
		}
	}
}

func TestWithLogWriter(t *testing.T) {
	var buf bytes.Buffer
	opts := applyOptions([]Option{WithLogWriter(&buf)})
	if opts.LogWriter != &buf {
		t.Error("WithLogWriter did not set LogWriter")
	}
}

func TestApplyOptions_DefaultLogWriter(t *testing.T) {
	opts := applyOptions(nil)
	if opts.LogWriter == nil {
		t.Error("applyOptions should default LogWriter to os.Stderr, got nil")
	}
}
