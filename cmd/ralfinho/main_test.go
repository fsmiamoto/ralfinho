package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestParseCLI_DefaultRun(t *testing.T) {
	opts, err := parseCLI([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.command != commandRun {
		t.Fatalf("expected run command, got %s", opts.command)
	}
	if opts.run.agent != "pi" {
		t.Fatalf("expected default agent pi, got %s", opts.run.agent)
	}
}

func TestParseViewArgs(t *testing.T) {
	opts, err := parseCLI([]string{"view", "--runs-dir", "tmp/runs", "abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.command != commandView {
		t.Fatalf("expected view command, got %s", opts.command)
	}
	if opts.view.runID != "abc" {
		t.Fatalf("unexpected run id: %s", opts.view.runID)
	}
}

func TestParseRunArgsPromptTemplate(t *testing.T) {
	opts, err := parseRunArgs([]string{"--plan", "docs/V1_PLAN.md", "--prompt-template", "templates/default.md"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.promptTemplateFile != "templates/default.md" {
		t.Fatalf("unexpected prompt template path: %s", opts.promptTemplateFile)
	}
}

func TestParseRunArgsValidation(t *testing.T) {
	if _, err := parseRunArgs([]string{"a.md", "b.md"}); err == nil {
		t.Fatal("expected too many positional args error")
	}
	if _, err := parseRunArgs([]string{"--prompt", "p.md", "--plan", "plan.md"}); err == nil {
		t.Fatal("expected prompt/plan conflict error")
	}
	if _, err := parseRunArgs([]string{"--prompt-template", "tmpl.md", "--prompt", "p.md"}); err == nil {
		t.Fatal("expected prompt-template with prompt conflict error")
	}
	if _, err := parseRunArgs([]string{"--prompt-template", "tmpl.md", "positional.md"}); err == nil {
		t.Fatal("expected prompt-template with positional prompt conflict error")
	}
	if _, err := parseRunArgs([]string{"--max-iterations", "-1"}); err == nil {
		t.Fatal("expected max iterations validation error")
	}
}

func TestPromptContinue(t *testing.T) {
	in := strings.NewReader("maybe\ny\n")
	var out bytes.Buffer

	cont, err := promptContinue(in, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cont {
		t.Fatal("expected continue=true")
	}
	if !strings.Contains(out.String(), "Please answer y or n.") {
		t.Fatalf("expected validation message, got %q", out.String())
	}
}

func TestParseCLI_Help(t *testing.T) {
	if _, err := parseCLI([]string{"--help"}); !errors.Is(err, errRunHelp) {
		t.Fatalf("expected errRunHelp, got %v", err)
	}

	if _, err := parseCLI([]string{"view", "--help"}); !errors.Is(err, errViewHelp) {
		t.Fatalf("expected errViewHelp, got %v", err)
	}
}

func TestPromptContinueEOFStops(t *testing.T) {
	in := strings.NewReader("")
	var out bytes.Buffer

	cont, err := promptContinue(in, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cont {
		t.Fatal("expected continue=false on EOF")
	}
}
