package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fsmiamoto/ralfinho/internal/cli"
	"github.com/fsmiamoto/ralfinho/internal/config"
	"github.com/fsmiamoto/ralfinho/internal/runner"
	"github.com/fsmiamoto/ralfinho/internal/tui"
	"github.com/fsmiamoto/ralfinho/internal/viewer"
)

type scriptedTeaProgram struct {
	run  func() (tea.Model, error)
	send func(tea.Msg)
}

type noopTeaModel struct{}

func (noopTeaModel) Init() tea.Cmd                       { return nil }
func (noopTeaModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return noopTeaModel{}, nil }
func (noopTeaModel) View() string                        { return "" }

func (p *scriptedTeaProgram) Run() (tea.Model, error) {
	if p.run == nil {
		return nil, nil
	}
	return p.run()
}

func (p *scriptedTeaProgram) Send(msg tea.Msg) {
	if p.send != nil {
		p.send(msg)
	}
}

type doneAwareTeaProgram struct {
	mu     sync.Mutex
	model  tea.Model
	done   chan struct{}
	once   sync.Once
	runErr error
}

func newDoneAwareTeaProgram(model tea.Model) *doneAwareTeaProgram {
	return &doneAwareTeaProgram{model: model, done: make(chan struct{})}
}

func newDoneAwareTeaProgramWithError(model tea.Model, err error) *doneAwareTeaProgram {
	return &doneAwareTeaProgram{model: model, done: make(chan struct{}), runErr: err}
}

func (p *doneAwareTeaProgram) Run() (tea.Model, error) {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.model, p.runErr
}

func (p *doneAwareTeaProgram) Send(msg tea.Msg) {
	p.mu.Lock()
	updated, _ := p.model.Update(msg)
	p.model = updated
	p.mu.Unlock()

	if _, ok := msg.(tui.DoneMsg); ok {
		p.once.Do(func() { close(p.done) })
	}
}

func useTeaProgramFactory(t *testing.T, factory func(model tea.Model, opts ...tea.ProgramOption) teaProgram) {
	t.Helper()

	prev := newTeaProgram
	newTeaProgram = factory
	t.Cleanup(func() { newTeaProgram = prev })
}

func clearFileConfig(t *testing.T) {
	t.Helper()

	prev := fileCfg
	fileCfg = nil
	t.Cleanup(func() { fileCfg = prev })
}

func clearConfiguredTemplates(t *testing.T) {
	t.Helper()

	prev := configuredTemplates
	configuredTemplates = config.ResolvedTemplates{}
	t.Cleanup(func() { configuredTemplates = prev })
}

func installFakePIBinary(t *testing.T, body string) {
	t.Helper()

	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", binDir, err)
	}
	path := filepath.Join(binDir, "pi")
	if err := os.WriteFile(path, []byte(body), 0755); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func applyKeySequence(model tea.Model, msgs ...tea.KeyMsg) tea.Model {
	for _, msg := range msgs {
		updated, _ := model.Update(msg)
		model = updated
	}
	return model
}

func keyRune(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func writeRunEventsArtifact(t *testing.T, runsDir, runID string, lines ...string) {
	t.Helper()

	path := filepath.Join(runsDir, runID, "events.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func writeEffectivePromptArtifact(t *testing.T, runsDir, runID, promptText string) string {
	t.Helper()

	path := filepath.Join(runsDir, runID, "effective-prompt.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(promptText), 0644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	return path
}

func listRunDirs(t *testing.T, runsDir string) []string {
	t.Helper()

	entries, err := os.ReadDir(runsDir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", runsDir, err)
	}

	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}
	sort.Strings(dirs)
	return dirs
}

func readRunMetaFile(t *testing.T, path string) runner.RunMeta {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}

	var meta runner.RunMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", path, err)
	}
	return meta
}

func TestRunAgentWithTUI(t *testing.T) {
	clearFileConfig(t)

	const completePI = `#!/bin/sh
cat <<'JSONL'
{"type":"message_start","message":{"role":"assistant","model":"fake-pi"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"<promise>COMPLETE</promise>"}}
{"type":"message_end"}
{"type":"turn_end"}
JSONL
`

	const slowPI = `#!/bin/sh
cat <<'JSONL'
{"type":"message_start","message":{"role":"assistant","model":"fake-pi"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"still working"}}
JSONL
sleep 60
`

	t.Run("returns runner result after DoneMsg", func(t *testing.T) {
		installFakePIBinary(t, completePI)
		useTeaProgramFactory(t, func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
			if _, ok := model.(tui.Model); !ok {
				t.Fatalf("model = %T, want tui.Model", model)
			}
			return newDoneAwareTeaProgram(model)
		})

		result, err := runAgentWithTUI(runner.RunConfig{
			Agent:        "pi",
			Prompt:       "finish immediately",
			RunsDir:      t.TempDir(),
			PromptSource: "default",
		})
		if err != nil {
			t.Fatalf("runAgentWithTUI() error = %v", err)
		}
		if result.Status != runner.StatusCompleted {
			t.Fatalf("Status = %q, want %q", result.Status, runner.StatusCompleted)
		}
		if result.Iterations != 1 {
			t.Fatalf("Iterations = %d, want 1", result.Iterations)
		}
		if result.Agent != "pi" {
			t.Fatalf("Agent = %q, want %q", result.Agent, "pi")
		}
		if result.RunID == "" {
			t.Fatal("RunID = empty, want generated run ID")
		}
	})

	t.Run("cancels runner when program exits before completion", func(t *testing.T) {
		installFakePIBinary(t, slowPI)
		useTeaProgramFactory(t, func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
			return &scriptedTeaProgram{
				run: func() (tea.Model, error) { return model, nil },
			}
		})

		result, err := runAgentWithTUI(runner.RunConfig{
			Agent:        "pi",
			Prompt:       "keep working",
			RunsDir:      t.TempDir(),
			PromptSource: "default",
		})
		if err != nil {
			t.Fatalf("runAgentWithTUI() error = %v", err)
		}
		if result.Status != runner.StatusInterrupted {
			t.Fatalf("Status = %q, want %q", result.Status, runner.StatusInterrupted)
		}
		if result.Error != "" {
			t.Fatalf("Error = %q, want empty", result.Error)
		}
	})

	t.Run("wraps TUI program errors", func(t *testing.T) {
		installFakePIBinary(t, completePI)
		useTeaProgramFactory(t, func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
			return newDoneAwareTeaProgramWithError(model, errors.New("boom"))
		})

		_, err := runAgentWithTUI(runner.RunConfig{
			Agent:        "pi",
			Prompt:       "finish immediately",
			RunsDir:      t.TempDir(),
			PromptSource: "default",
		})
		if err == nil {
			t.Fatal("runAgentWithTUI() error = nil, want TUI error")
		}
		if !strings.Contains(err.Error(), "TUI error: boom") {
			t.Fatalf("runAgentWithTUI() error = %q, want wrapped TUI error", err)
		}
	})
}

func TestRunTUIPrintsCompletedSummary(t *testing.T) {
	clearFileConfig(t)

	installFakePIBinary(t, `#!/bin/sh
cat <<'JSONL'
{"type":"message_start","message":{"role":"assistant","model":"fake-pi"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"<promise>COMPLETE</promise>"}}
{"type":"message_end"}
{"type":"turn_end"}
JSONL
`)
	useTeaProgramFactory(t, func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
		return newDoneAwareTeaProgram(model)
	})

	stdout, stderr := captureCommandOutput(t, func() {
		runTUI(&cli.Config{Agent: "pi", RunsDir: t.TempDir()}, "finish immediately")
	})

	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{"=== run summary ===", "agent:      pi", "status:     completed", "iterations: 1"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, missing %q", stderr, want)
		}
	}
}

func TestOpenRunViewerWrapsTUIErrors(t *testing.T) {
	runsDir := t.TempDir()
	writeMetaOnlyRun(t, runsDir, "saved-run", runner.RunMeta{
		RunID:               "saved-run",
		StartedAt:           "2026-03-08T10:00:00Z",
		Status:              string(runner.StatusCompleted),
		Agent:               "pi",
		PromptSource:        "default",
		IterationsCompleted: 1,
	})
	writeRunEventsArtifact(t, runsDir, "saved-run", `{"type":"turn_end"}`)

	useTeaProgramFactory(t, func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
		if _, ok := model.(tui.Model); !ok {
			t.Fatalf("model = %T, want tui.Model", model)
		}
		return &scriptedTeaProgram{
			run: func() (tea.Model, error) { return model, errors.New("viewer boom") },
		}
	})

	err := openRunViewer(runsDir, "saved")
	if err == nil {
		t.Fatal("openRunViewer() error = nil, want wrapped TUI error")
	}
	if !strings.Contains(err.Error(), "TUI error: viewer boom") {
		t.Fatalf("openRunViewer() error = %q, want wrapped TUI error", err)
	}
}

func TestRunBrowserInteractiveFlows(t *testing.T) {
	const completePI = `#!/bin/sh
cat <<'JSONL'
{"type":"message_start","message":{"role":"assistant","model":"fake-pi"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"<promise>COMPLETE</promise>"}}
{"type":"message_end"}
{"type":"turn_end"}
JSONL
`

	t.Run("open action launches viewer and reopens browser", func(t *testing.T) {
		runsDir := t.TempDir()
		writeMetaOnlyRun(t, runsDir, "open-run", runner.RunMeta{
			RunID:               "open-run",
			StartedAt:           "2026-03-08T10:00:00Z",
			Status:              string(runner.StatusCompleted),
			Agent:               "pi",
			PromptSource:        "default",
			IterationsCompleted: 1,
		})
		writeRunEventsArtifact(t, runsDir, "open-run", `{"type":"turn_end"}`)

		var calls []string
		useTeaProgramFactory(t, func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
			switch len(calls) {
			case 0:
				calls = append(calls, "browser-open")
				if _, ok := model.(tui.BrowserModel); !ok {
					t.Fatalf("model = %T, want tui.BrowserModel", model)
				}
				return &scriptedTeaProgram{run: func() (tea.Model, error) {
					return applyKeySequence(model, tea.KeyMsg{Type: tea.KeyEnter}), nil
				}}
			case 1:
				calls = append(calls, "viewer")
				if _, ok := model.(tui.Model); !ok {
					t.Fatalf("model = %T, want tui.Model", model)
				}
				return &scriptedTeaProgram{run: func() (tea.Model, error) { return model, nil }}
			case 2:
				calls = append(calls, "browser-quit")
				if _, ok := model.(tui.BrowserModel); !ok {
					t.Fatalf("model = %T, want tui.BrowserModel", model)
				}
				return &scriptedTeaProgram{run: func() (tea.Model, error) {
					return applyKeySequence(model, keyRune('q')), nil
				}}
			default:
				t.Fatalf("unexpected program call %d for model %T", len(calls)+1, model)
				return nil
			}
		})

		runBrowser(&cli.Config{RunsDir: runsDir})

		want := []string{"browser-open", "viewer", "browser-quit"}
		if strings.Join(calls, ",") != strings.Join(want, ",") {
			t.Fatalf("program call sequence = %#v, want %#v", calls, want)
		}
	})

	t.Run("viewer errors are logged and browser stays open", func(t *testing.T) {
		runsDir := t.TempDir()
		writeMetaOnlyRun(t, runsDir, "open-run", runner.RunMeta{
			RunID:               "open-run",
			StartedAt:           "2026-03-08T10:00:00Z",
			Status:              string(runner.StatusCompleted),
			Agent:               "pi",
			PromptSource:        "default",
			IterationsCompleted: 1,
		})
		writeRunEventsArtifact(t, runsDir, "open-run", `{"type":"turn_end"}`)

		var calls []string
		useTeaProgramFactory(t, func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
			switch len(calls) {
			case 0:
				calls = append(calls, "browser-open")
				if _, ok := model.(tui.BrowserModel); !ok {
					t.Fatalf("model = %T, want tui.BrowserModel", model)
				}
				return &scriptedTeaProgram{run: func() (tea.Model, error) {
					return applyKeySequence(model, tea.KeyMsg{Type: tea.KeyEnter}), nil
				}}
			case 1:
				calls = append(calls, "viewer-fail")
				if _, ok := model.(tui.Model); !ok {
					t.Fatalf("model = %T, want tui.Model", model)
				}
				return &scriptedTeaProgram{run: func() (tea.Model, error) {
					return model, errors.New("viewer boom")
				}}
			case 2:
				calls = append(calls, "browser-quit")
				if _, ok := model.(tui.BrowserModel); !ok {
					t.Fatalf("model = %T, want tui.BrowserModel", model)
				}
				return &scriptedTeaProgram{run: func() (tea.Model, error) {
					return applyKeySequence(model, keyRune('q')), nil
				}}
			default:
				t.Fatalf("unexpected program call %d for model %T", len(calls)+1, model)
				return nil
			}
		})

		stdout, stderr := captureCommandOutput(t, func() {
			runBrowser(&cli.Config{RunsDir: runsDir})
		})

		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
		if !strings.Contains(stderr, "ralfinho view: TUI error: viewer boom") {
			t.Fatalf("stderr = %q, want logged viewer error", stderr)
		}

		want := []string{"browser-open", "viewer-fail", "browser-quit"}
		if strings.Join(calls, ",") != strings.Join(want, ",") {
			t.Fatalf("program call sequence = %#v, want %#v", calls, want)
		}
	})

	t.Run("resume action launches a fresh run and reopens browser", func(t *testing.T) {
		clearFileConfig(t)
		installFakePIBinary(t, completePI)

		runsDir := t.TempDir()
		writeMetaOnlyRun(t, runsDir, "source-run", runner.RunMeta{
			RunID:               "source-run",
			StartedAt:           "2026-03-08T10:00:00Z",
			Status:              string(runner.StatusCompleted),
			Agent:               "pi",
			PromptSource:        "default",
			IterationsCompleted: 1,
		})
		writeEffectivePromptArtifact(t, runsDir, "source-run", "resumed prompt text")

		before := listRunDirs(t, runsDir)
		var calls []string
		useTeaProgramFactory(t, func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
			switch len(calls) {
			case 0:
				calls = append(calls, "browser-resume")
				if _, ok := model.(tui.BrowserModel); !ok {
					t.Fatalf("model = %T, want tui.BrowserModel", model)
				}
				return &scriptedTeaProgram{run: func() (tea.Model, error) {
					return applyKeySequence(model, keyRune('r')), nil
				}}
			case 1:
				calls = append(calls, "resume-tui")
				if _, ok := model.(tui.Model); !ok {
					t.Fatalf("model = %T, want tui.Model", model)
				}
				return newDoneAwareTeaProgram(model)
			case 2:
				calls = append(calls, "browser-quit")
				if _, ok := model.(tui.BrowserModel); !ok {
					t.Fatalf("model = %T, want tui.BrowserModel", model)
				}
				return &scriptedTeaProgram{run: func() (tea.Model, error) {
					return applyKeySequence(model, keyRune('q')), nil
				}}
			default:
				t.Fatalf("unexpected program call %d for model %T", len(calls)+1, model)
				return nil
			}
		})

		stdout, stderr := captureCommandOutput(t, func() {
			runBrowser(&cli.Config{Agent: "pi", RunsDir: runsDir})
		})

		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
		for _, want := range []string{"=== resumed run summary ===", "agent:      pi", "status:     completed", "iterations: 1"} {
			if !strings.Contains(stderr, want) {
				t.Fatalf("stderr = %q, missing %q", stderr, want)
			}
		}

		after := listRunDirs(t, runsDir)
		if len(after) != len(before)+1 {
			t.Fatalf("run dir count = %d, want %d (before=%#v after=%#v)", len(after), len(before)+1, before, after)
		}

		want := []string{"browser-resume", "resume-tui", "browser-quit"}
		if strings.Join(calls, ",") != strings.Join(want, ",") {
			t.Fatalf("program call sequence = %#v, want %#v", calls, want)
		}
	})

	t.Run("resume prompt resolution failures are logged and browser reopens", func(t *testing.T) {
		runsDir := t.TempDir()
		missingPrompt := filepath.Join(runsDir, "missing-prompt.md")
		writeMetaOnlyRun(t, runsDir, "source-run", runner.RunMeta{
			RunID:               "source-run",
			StartedAt:           "2026-03-08T10:00:00Z",
			Status:              string(runner.StatusCompleted),
			Agent:               "pi",
			PromptSource:        "prompt",
			PromptFile:          missingPrompt,
			IterationsCompleted: 1,
		})

		var calls []string
		useTeaProgramFactory(t, func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
			switch len(calls) {
			case 0:
				calls = append(calls, "browser-resume")
				if _, ok := model.(tui.BrowserModel); !ok {
					t.Fatalf("model = %T, want tui.BrowserModel", model)
				}
				return &scriptedTeaProgram{run: func() (tea.Model, error) {
					return applyKeySequence(model, keyRune('r')), nil
				}}
			case 1:
				calls = append(calls, "browser-quit")
				if _, ok := model.(tui.BrowserModel); !ok {
					t.Fatalf("model = %T, want tui.BrowserModel", model)
				}
				return &scriptedTeaProgram{run: func() (tea.Model, error) {
					return applyKeySequence(model, keyRune('q')), nil
				}}
			default:
				t.Fatalf("unexpected program call %d for model %T", len(calls)+1, model)
				return nil
			}
		})

		stdout, stderr := captureCommandOutput(t, func() {
			runBrowser(&cli.Config{Agent: "pi", RunsDir: runsDir})
		})

		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
		if !strings.Contains(stderr, "ralfinho view: resume: resolving prompt:") {
			t.Fatalf("stderr = %q, want logged resume prompt error", stderr)
		}

		want := []string{"browser-resume", "browser-quit"}
		if strings.Join(calls, ",") != strings.Join(want, ",") {
			t.Fatalf("program call sequence = %#v, want %#v", calls, want)
		}
	})

	t.Run("resume invalid agent errors are logged and browser reopens", func(t *testing.T) {
		runsDir := t.TempDir()
		writeMetaOnlyRun(t, runsDir, "source-run", runner.RunMeta{
			RunID:               "source-run",
			StartedAt:           "2026-03-08T10:00:00Z",
			Status:              string(runner.StatusCompleted),
			Agent:               "mystery-agent",
			PromptSource:        "default",
			IterationsCompleted: 1,
		})

		var calls []string
		useTeaProgramFactory(t, func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
			switch len(calls) {
			case 0:
				calls = append(calls, "browser-resume")
				if _, ok := model.(tui.BrowserModel); !ok {
					t.Fatalf("model = %T, want tui.BrowserModel", model)
				}
				return &scriptedTeaProgram{run: func() (tea.Model, error) {
					return applyKeySequence(model, keyRune('r')), nil
				}}
			case 1:
				calls = append(calls, "browser-quit")
				if _, ok := model.(tui.BrowserModel); !ok {
					t.Fatalf("model = %T, want tui.BrowserModel", model)
				}
				return &scriptedTeaProgram{run: func() (tea.Model, error) {
					return applyKeySequence(model, keyRune('q')), nil
				}}
			default:
				t.Fatalf("unexpected program call %d for model %T", len(calls)+1, model)
				return nil
			}
		})

		stdout, stderr := captureCommandOutput(t, func() {
			runBrowser(&cli.Config{Agent: "pi", RunsDir: runsDir})
		})

		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
		if !strings.Contains(stderr, `ralfinho view: resume: unknown agent "mystery-agent" from saved run`) {
			t.Fatalf("stderr = %q, want logged invalid-agent error", stderr)
		}

		want := []string{"browser-resume", "browser-quit"}
		if strings.Join(calls, ",") != strings.Join(want, ",") {
			t.Fatalf("program call sequence = %#v, want %#v", calls, want)
		}
	})

	t.Run("resume TUI failures are logged and browser reopens", func(t *testing.T) {
		clearFileConfig(t)
		installFakePIBinary(t, completePI)

		runsDir := t.TempDir()
		writeMetaOnlyRun(t, runsDir, "source-run", runner.RunMeta{
			RunID:               "source-run",
			StartedAt:           "2026-03-08T10:00:00Z",
			Status:              string(runner.StatusCompleted),
			Agent:               "pi",
			PromptSource:        "default",
			IterationsCompleted: 1,
		})
		writeEffectivePromptArtifact(t, runsDir, "source-run", "resumed prompt text")

		var calls []string
		useTeaProgramFactory(t, func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
			switch len(calls) {
			case 0:
				calls = append(calls, "browser-resume")
				if _, ok := model.(tui.BrowserModel); !ok {
					t.Fatalf("model = %T, want tui.BrowserModel", model)
				}
				return &scriptedTeaProgram{run: func() (tea.Model, error) {
					return applyKeySequence(model, keyRune('r')), nil
				}}
			case 1:
				calls = append(calls, "resume-tui-fail")
				if _, ok := model.(tui.Model); !ok {
					t.Fatalf("model = %T, want tui.Model", model)
				}
				return newDoneAwareTeaProgramWithError(model, errors.New("resume boom"))
			case 2:
				calls = append(calls, "browser-quit")
				if _, ok := model.(tui.BrowserModel); !ok {
					t.Fatalf("model = %T, want tui.BrowserModel", model)
				}
				return &scriptedTeaProgram{run: func() (tea.Model, error) {
					return applyKeySequence(model, keyRune('q')), nil
				}}
			default:
				t.Fatalf("unexpected program call %d for model %T", len(calls)+1, model)
				return nil
			}
		})

		stdout, stderr := captureCommandOutput(t, func() {
			runBrowser(&cli.Config{Agent: "pi", RunsDir: runsDir})
		})

		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
		if !strings.Contains(stderr, "ralfinho view: resume: TUI error: resume boom") {
			t.Fatalf("stderr = %q, want logged resume TUI error", stderr)
		}

		want := []string{"browser-resume", "resume-tui-fail", "browser-quit"}
		if strings.Join(calls, ",") != strings.Join(want, ",") {
			t.Fatalf("program call sequence = %#v, want %#v", calls, want)
		}
	})

	t.Run("delete action removes the selected run directory", func(t *testing.T) {
		runsDir := t.TempDir()
		writeMetaOnlyRun(t, runsDir, "delete-run", runner.RunMeta{
			RunID:               "delete-run",
			StartedAt:           "2026-03-08T10:00:00Z",
			Status:              string(runner.StatusCompleted),
			Agent:               "pi",
			PromptSource:        "default",
			IterationsCompleted: 1,
		})
		writeRunEventsArtifact(t, runsDir, "delete-run", `{"type":"turn_end"}`)

		callCount := 0
		useTeaProgramFactory(t, func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
			callCount++
			if _, ok := model.(tui.BrowserModel); !ok {
				t.Fatalf("model = %T, want tui.BrowserModel", model)
			}

			switch callCount {
			case 1:
				return &scriptedTeaProgram{run: func() (tea.Model, error) {
					return applyKeySequence(model, keyRune('x'), keyRune('y')), nil
				}}
			case 2:
				return &scriptedTeaProgram{run: func() (tea.Model, error) {
					return applyKeySequence(model, keyRune('q')), nil
				}}
			default:
				t.Fatalf("unexpected browser program call %d", callCount)
				return nil
			}
		})

		runBrowser(&cli.Config{RunsDir: runsDir})

		if _, err := os.Stat(filepath.Join(runsDir, "delete-run")); !os.IsNotExist(err) {
			t.Fatalf("deleted run dir still exists, stat err = %v", err)
		}
		if callCount != 2 {
			t.Fatalf("browser program calls = %d, want 2", callCount)
		}
	})
}

func TestRunBrowserEdgeBranches(t *testing.T) {
	t.Run("non-browser final model returns without reopening", func(t *testing.T) {
		runsDir := t.TempDir()
		writeMetaOnlyRun(t, runsDir, "saved-run", runner.RunMeta{
			RunID:               "saved-run",
			StartedAt:           "2026-03-08T10:00:00Z",
			Status:              string(runner.StatusCompleted),
			Agent:               "pi",
			PromptSource:        "default",
			IterationsCompleted: 1,
		})
		writeRunEventsArtifact(t, runsDir, "saved-run", `{"type":"turn_end"}`)

		callCount := 0
		useTeaProgramFactory(t, func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
			callCount++
			if _, ok := model.(tui.BrowserModel); !ok {
				t.Fatalf("model = %T, want tui.BrowserModel", model)
			}
			return &scriptedTeaProgram{run: func() (tea.Model, error) {
				return noopTeaModel{}, nil
			}}
		})

		stdout, stderr := captureCommandOutput(t, func() {
			runBrowser(&cli.Config{RunsDir: runsDir})
		})

		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
		if stderr != "" {
			t.Fatalf("stderr = %q, want empty", stderr)
		}
		if callCount != 1 {
			t.Fatalf("browser program calls = %d, want 1", callCount)
		}
	})

	t.Run("delete failures are logged and browser reopens", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("delete warning path relies on unix directory permissions")
		}
		runsDir := t.TempDir()
		writeMetaOnlyRun(t, runsDir, "delete-run", runner.RunMeta{
			RunID:               "delete-run",
			StartedAt:           "2026-03-08T10:00:00Z",
			Status:              string(runner.StatusCompleted),
			Agent:               "pi",
			PromptSource:        "default",
			IterationsCompleted: 1,
		})
		writeRunEventsArtifact(t, runsDir, "delete-run", `{"type":"turn_end"}`)

		if err := os.Chmod(runsDir, 0500); err != nil {
			t.Fatalf("Chmod(%q, 0500): %v", runsDir, err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(runsDir, 0700); err != nil {
				t.Fatalf("restoring permissions on %q: %v", runsDir, err)
			}
		})

		callCount := 0
		useTeaProgramFactory(t, func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
			callCount++
			if _, ok := model.(tui.BrowserModel); !ok {
				t.Fatalf("model = %T, want tui.BrowserModel", model)
			}

			switch callCount {
			case 1:
				return &scriptedTeaProgram{run: func() (tea.Model, error) {
					return applyKeySequence(model, keyRune('x'), keyRune('y')), nil
				}}
			case 2:
				return &scriptedTeaProgram{run: func() (tea.Model, error) {
					return applyKeySequence(model, keyRune('q')), nil
				}}
			default:
				t.Fatalf("unexpected browser program call %d", callCount)
				return nil
			}
		})

		stdout, stderr := captureCommandOutput(t, func() {
			runBrowser(&cli.Config{RunsDir: runsDir})
		})

		if stdout != "" {
			t.Fatalf("stdout = %q, want empty", stdout)
		}
		if !strings.Contains(stderr, "ralfinho view: delete:") {
			t.Fatalf("stderr = %q, want logged delete warning", stderr)
		}
		if _, err := os.Stat(filepath.Join(runsDir, "delete-run")); err != nil {
			t.Fatalf("deleted run dir missing after warning, stat err = %v", err)
		}
		if callCount != 2 {
			t.Fatalf("browser program calls = %d, want 2", callCount)
		}
	})
}

func TestResumeRunFromBrowserCreatesFreshRunAndPrintsSummary(t *testing.T) {
	clearFileConfig(t)

	installFakePIBinary(t, `#!/bin/sh
cat <<'JSONL'
{"type":"message_start","message":{"role":"assistant","model":"fake-pi"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"<promise>COMPLETE</promise>"}}
{"type":"message_end"}
{"type":"turn_end"}
JSONL
`)
	useTeaProgramFactory(t, func(model tea.Model, _ ...tea.ProgramOption) teaProgram {
		if _, ok := model.(tui.Model); !ok {
			t.Fatalf("model = %T, want tui.Model", model)
		}
		return newDoneAwareTeaProgram(model)
	})

	runsDir := t.TempDir()
	writeMetaOnlyRun(t, runsDir, "source-run", runner.RunMeta{
		RunID:               "source-run",
		StartedAt:           "2026-03-08T10:00:00Z",
		Status:              string(runner.StatusCompleted),
		Agent:               "pi",
		PromptSource:        "default",
		IterationsCompleted: 1,
	})
	resumePath := writeEffectivePromptArtifact(t, runsDir, "source-run", "resumed prompt text")

	before := listRunDirs(t, runsDir)
	stdout, stderr := captureCommandOutput(t, func() {
		err := resumeRunFromBrowser(&cli.Config{Agent: "pi", RunsDir: runsDir}, tui.BrowserResult{
			RunID:        "source-run",
			ResumeSource: viewer.ResumeSourceEffectivePrompt,
			ResumePath:   resumePath,
		})
		if err != nil {
			t.Fatalf("resumeRunFromBrowser() error = %v", err)
		}
	})

	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	for _, want := range []string{"=== resumed run summary ===", "agent:      pi", "status:     completed", "iterations: 1"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, missing %q", stderr, want)
		}
	}

	after := listRunDirs(t, runsDir)
	if len(after) != len(before)+1 {
		t.Fatalf("run dir count = %d, want %d (before=%#v after=%#v)", len(after), len(before)+1, before, after)
	}

	var newRunID string
	beforeSet := make(map[string]bool, len(before))
	for _, id := range before {
		beforeSet[id] = true
	}
	for _, id := range after {
		if !beforeSet[id] {
			newRunID = id
			break
		}
	}
	if newRunID == "" {
		t.Fatalf("could not identify new run directory: before=%#v after=%#v", before, after)
	}

	meta := readRunMetaFile(t, filepath.Join(runsDir, newRunID, "meta.json"))
	if meta.Agent != "pi" {
		t.Fatalf("new run Agent = %q, want %q", meta.Agent, "pi")
	}
	if meta.PromptSource != "prompt" {
		t.Fatalf("new run PromptSource = %q, want %q", meta.PromptSource, "prompt")
	}
	if meta.PromptFile != "" {
		t.Fatalf("new run PromptFile = %q, want empty", meta.PromptFile)
	}
	if meta.PlanFile != "" {
		t.Fatalf("new run PlanFile = %q, want empty", meta.PlanFile)
	}
	if meta.Status != string(runner.StatusCompleted) {
		t.Fatalf("new run Status = %q, want %q", meta.Status, runner.StatusCompleted)
	}
}
