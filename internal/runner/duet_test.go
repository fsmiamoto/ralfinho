package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakePI writes a shell-script fake pi binary that reads its @file prompt
// and emits different JSONL depending on whether the prompt contains a trigger
// string. If trigger is empty, the same body is always emitted.
//
// detectStr: if non-empty, script greps prompt file for this string.
// matchBody: JSONL emitted when trigger matches (or always when trigger="").
// noMatchBody: JSONL emitted when trigger does not match.
func writeFakePI(t *testing.T, path, detectStr, matchBody, noMatchBody string) {
	t.Helper()
	var script string
	if detectStr == "" {
		script = "#!/bin/sh\ncat <<'JSONL'\n" + matchBody + "\nJSONL\n"
	} else {
		script = `#!/bin/sh
PROMPT_FILE=""
for arg in "$@"; do
  case "$arg" in @*)
    PROMPT_FILE="${arg#@}"
    ;;
  esac
done
if [ -n "$PROMPT_FILE" ] && grep -q '` + detectStr + `' "$PROMPT_FILE" 2>/dev/null; then
cat <<'JSONL'
` + matchBody + `
JSONL
else
cat <<'JSONL'
` + noMatchBody + `
JSONL
fi
`
	}
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("writeFakePI(%q): %v", path, err)
	}
}

// piComplete is a minimal one-iteration complete sequence for the pi agent format.
func piComplete(text string) string {
	return `{"type":"message_start","message":{"role":"assistant","model":"fake"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"` + text + `"}}
{"type":"message_end"}
{"type":"turn_end"}`
}

func duetTestSetup(t *testing.T) (binDir, runsDir string) {
	t.Helper()
	binDir = t.TempDir()
	runsDir = t.TempDir()
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return
}

// TestParseVerdict covers all verdict cases.
func TestParseVerdict(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		wantVerdict verdictKind
		wantReason  string
	}{
		{"approved", "text <promise>APPROVED</promise> more", verdictApproved, ""},
		{"rejected with reason", "bad work <promise>REJECTED: missing tests</promise> done", verdictRejected, "missing tests"},
		{"rejected no reason", "<promise>REJECTED:</promise>", verdictRejected, ""},
		{"none", "no markers at all", verdictNone, ""},
		{"partial approved", "<promise>APPROV</promise>", verdictNone, ""},
		// When both markers appear, the later marker wins (final stated verdict).
		{"approved then rejected", "<promise>APPROVED</promise> wait, on second look <promise>REJECTED: edge case</promise>", verdictRejected, "edge case"},
		{"rejected then approved", "<promise>REJECTED: noop</promise> actually fine <promise>APPROVED</promise>", verdictApproved, ""},
		// Rejected marker without a closing tag is treated as no verdict (verifier protocol violation).
		{"rejected unterminated", "<promise>REJECTED: forgot close", verdictNone, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, reason := parseVerdict(tt.text)
			if v != tt.wantVerdict {
				t.Errorf("verdict = %v, want %v", v, tt.wantVerdict)
			}
			if reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}

// TestDuetRunner_Approved verifies a single-cycle approve path.
func TestDuetRunner_Approved(t *testing.T) {
	binDir, runsDir := duetTestSetup(t)

	// Builder prompt contains "build"; verifier prompt contains "VERIFY_TOKEN".
	// The single pi binary dispatches on that token.
	// Verifier must emit COMPLETE so the Runner exits its loop; verdict is in
	// the same text so readAssistantText / parseVerdict finds it.
	writeFakePI(t, filepath.Join(binDir, "pi"),
		"VERIFY_TOKEN",
		piComplete("<promise>APPROVED</promise><promise>COMPLETE</promise>"),
		piComplete("built it<promise>COMPLETE</promise>"),
	)

	cfg := DuetConfig{
		Builder: RunConfig{
			Agent:  "pi",
			Prompt: "build the feature",
		},
		Verifier: RunConfig{
			Agent:  "pi",
			Prompt: "VERIFY_TOKEN check the work",
		},
		MaxCycles: 3,
		RunsDir:   runsDir,
		DuetID:    "test-approved",
	}

	dr := NewDuetRunner(cfg)
	result := dr.Run(context.Background())

	if result.Status != DuetStatusApproved {
		t.Errorf("Status = %q, want %q", result.Status, DuetStatusApproved)
	}
	if result.Cycles != 1 {
		t.Errorf("Cycles = %d, want 1", result.Cycles)
	}
	if result.DuetID != "test-approved" {
		t.Errorf("DuetID = %q, want %q", result.DuetID, "test-approved")
	}
}

// TestDuetRunner_RejectedThenApproved verifies that a rejection in cycle 1
// leads to a cycle 2 with feedback appended, and APPROVED in cycle 2 succeeds.
func TestDuetRunner_RejectedThenApproved(t *testing.T) {
	binDir, runsDir := duetTestSetup(t)

	// Single pi binary: builder outputs COMPLETE; verifier uses a sentinel file
	// to reject on first call and approve on second call.
	sentinelFile := filepath.Join(t.TempDir(), "verified.sentinel")
	combinedScript := `#!/bin/sh
PROMPT_FILE=""
for arg in "$@"; do
  case "$arg" in @*) PROMPT_FILE="${arg#@}" ;; esac
done
if [ -n "$PROMPT_FILE" ] && grep -q 'VERIFY_TOKEN' "$PROMPT_FILE" 2>/dev/null; then
  if [ ! -f "` + sentinelFile + `" ]; then
    touch "` + sentinelFile + `"
cat <<'JSONL'
{"type":"message_start","message":{"role":"assistant","model":"fake"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"<promise>REJECTED: missing docs</promise><promise>COMPLETE</promise>"}}
{"type":"message_end"}
{"type":"turn_end"}
JSONL
  else
cat <<'JSONL'
{"type":"message_start","message":{"role":"assistant","model":"fake"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"<promise>APPROVED</promise><promise>COMPLETE</promise>"}}
{"type":"message_end"}
{"type":"turn_end"}
JSONL
  fi
else
cat <<'JSONL'
{"type":"message_start","message":{"role":"assistant","model":"fake"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"builder work<promise>COMPLETE</promise>"}}
{"type":"message_end"}
{"type":"turn_end"}
JSONL
fi`

	if err := os.WriteFile(filepath.Join(binDir, "pi"), []byte(combinedScript), 0755); err != nil {
		t.Fatal(err)
	}

	cfg := DuetConfig{
		Builder: RunConfig{
			Agent:  "pi",
			Prompt: "build it",
		},
		Verifier: RunConfig{
			Agent:  "pi",
			Prompt: "VERIFY_TOKEN check the work",
		},
		MaxCycles: 5,
		RunsDir:   runsDir,
		DuetID:    "test-rejected-then-approved",
	}

	dr := NewDuetRunner(cfg)
	result := dr.Run(context.Background())

	if result.Status != DuetStatusApproved {
		t.Errorf("Status = %q, want %q", result.Status, DuetStatusApproved)
	}
	if result.Cycles != 2 {
		t.Errorf("Cycles = %d, want 2", result.Cycles)
	}

	// Verify that cycle-2 builder prompt included the verifier feedback.
	cycle2BuilderDir := filepath.Join(runsDir, "test-rejected-then-approved", "cycle-2", "builder")
	entries, err := os.ReadDir(cycle2BuilderDir)
	if err != nil {
		t.Fatalf("ReadDir cycle-2 builder: %v", err)
	}
	var runDirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			runDirs = append(runDirs, e.Name())
		}
	}
	if len(runDirs) != 1 {
		t.Fatalf("expected exactly 1 run dir in cycle-2 builder, got %d (%v)", len(runDirs), runDirs)
	}
	promptPath := filepath.Join(cycle2BuilderDir, runDirs[0], "effective-prompt.md")
	data, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatalf("reading cycle-2 effective prompt: %v", err)
	}
	if !strings.Contains(string(data), "missing docs") {
		t.Errorf("cycle-2 builder prompt does not contain verifier feedback; got:\n%s", data)
	}
}

// TestDuetRunner_VerifierFailed verifies the hard-fail path when the verifier
// emits neither APPROVED nor REJECTED.
func TestDuetRunner_VerifierFailed(t *testing.T) {
	binDir, runsDir := duetTestSetup(t)

	writeFakePI(t, filepath.Join(binDir, "pi"),
		"VERIFY_TOKEN",
		piComplete("I reviewed it.<promise>COMPLETE</promise>"), // verifier: no verdict, but COMPLETE
		piComplete("built it<promise>COMPLETE</promise>"),       // builder
	)

	cfg := DuetConfig{
		Builder: RunConfig{
			Agent:  "pi",
			Prompt: "build it",
		},
		Verifier: RunConfig{
			Agent:  "pi",
			Prompt: "VERIFY_TOKEN check the work",
		},
		MaxCycles: 5,
		RunsDir:   runsDir,
		DuetID:    "test-verifier-failed",
	}

	dr := NewDuetRunner(cfg)
	result := dr.Run(context.Background())

	if result.Status != DuetStatusVerifierFailed {
		t.Errorf("Status = %q, want %q", result.Status, DuetStatusVerifierFailed)
	}
	if result.Cycles != 1 {
		t.Errorf("Cycles = %d, want 1", result.Cycles)
	}
}

// TestDuetRunner_MaxCycles verifies that the loop stops at MaxCycles.
func TestDuetRunner_MaxCycles(t *testing.T) {
	binDir, runsDir := duetTestSetup(t)

	writeFakePI(t, filepath.Join(binDir, "pi"),
		"VERIFY_TOKEN",
		piComplete("<promise>REJECTED: not good enough</promise><promise>COMPLETE</promise>"),
		piComplete("built it<promise>COMPLETE</promise>"),
	)

	cfg := DuetConfig{
		Builder: RunConfig{
			Agent:  "pi",
			Prompt: "build it",
		},
		Verifier: RunConfig{
			Agent:  "pi",
			Prompt: "VERIFY_TOKEN check the work",
		},
		MaxCycles: 2,
		RunsDir:   runsDir,
		DuetID:    "test-max-cycles",
	}

	dr := NewDuetRunner(cfg)
	result := dr.Run(context.Background())

	if result.Status != DuetStatusMaxCycles {
		t.Errorf("Status = %q, want %q", result.Status, DuetStatusMaxCycles)
	}
	if result.Cycles != 2 {
		t.Errorf("Cycles = %d, want 2", result.Cycles)
	}
}

// TestDuetRunner_MetaJSON verifies that meta.json is written to the duet root.
func TestDuetRunner_MetaJSON(t *testing.T) {
	binDir, runsDir := duetTestSetup(t)

	writeFakePI(t, filepath.Join(binDir, "pi"),
		"VERIFY_TOKEN",
		piComplete("<promise>APPROVED</promise><promise>COMPLETE</promise>"),
		piComplete("built it<promise>COMPLETE</promise>"),
	)

	cfg := DuetConfig{
		Builder: RunConfig{
			Agent:  "pi",
			Prompt: "build it",
		},
		Verifier: RunConfig{
			Agent:  "pi",
			Prompt: "VERIFY_TOKEN check the work",
		},
		MaxCycles: 3,
		RunsDir:   runsDir,
		DuetID:    "test-meta",
	}

	dr := NewDuetRunner(cfg)
	result := dr.Run(context.Background())

	if result.Status != DuetStatusApproved {
		t.Fatalf("Status = %q, want approved", result.Status)
	}

	metaPath := filepath.Join(runsDir, "test-meta", "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("reading meta.json: %v", err)
	}
	var meta DuetMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("unmarshaling meta.json: %v\nraw: %s", err, data)
	}
	if meta.Status != string(DuetStatusApproved) {
		t.Errorf("meta.json status = %q, want %q", meta.Status, DuetStatusApproved)
	}
	if meta.DuetID != "test-meta" {
		t.Errorf("meta.json duet_id = %q, want %q", meta.DuetID, "test-meta")
	}
	if meta.Cycles != 1 {
		t.Errorf("meta.json cycles = %d, want 1", meta.Cycles)
	}
}

// TestDuetRunner_DuetPhaseEventsEmitted verifies that EventDuetPhase events
// are emitted on the event channel for each builder/verifier phase.
func TestDuetRunner_DuetPhaseEventsEmitted(t *testing.T) {
	binDir, runsDir := duetTestSetup(t)

	writeFakePI(t, filepath.Join(binDir, "pi"),
		"VERIFY_TOKEN",
		piComplete("<promise>APPROVED</promise><promise>COMPLETE</promise>"),
		piComplete("done<promise>COMPLETE</promise>"),
	)

	eventCh := make(chan Event, 64)
	cfg := DuetConfig{
		Builder: RunConfig{
			Agent:     "pi",
			Prompt:    "build it",
			EventChan: eventCh,
		},
		Verifier: RunConfig{
			Agent:  "pi",
			Prompt: "VERIFY_TOKEN check the work",
		},
		MaxCycles: 1,
		RunsDir:   runsDir,
		DuetID:    "test-events",
	}

	dr := NewDuetRunner(cfg)
	_ = dr.Run(context.Background())

	close(eventCh)

	var phaseEvents []Event
	for ev := range eventCh {
		if ev.Type == EventDuetPhase {
			phaseEvents = append(phaseEvents, ev)
		}
	}

	if len(phaseEvents) != 2 {
		t.Fatalf("got %d EventDuetPhase events, want 2", len(phaseEvents))
	}
	if phaseEvents[0].DuetPhase != "BUILDER" {
		t.Errorf("phaseEvents[0].DuetPhase = %q, want BUILDER", phaseEvents[0].DuetPhase)
	}
	if phaseEvents[1].DuetPhase != "VERIFIER" {
		t.Errorf("phaseEvents[1].DuetPhase = %q, want VERIFIER", phaseEvents[1].DuetPhase)
	}
	if phaseEvents[0].DuetCycle != 1 || phaseEvents[1].DuetCycle != 1 {
		t.Errorf("expected cycle=1 for both, got %d and %d", phaseEvents[0].DuetCycle, phaseEvents[1].DuetCycle)
	}
}
