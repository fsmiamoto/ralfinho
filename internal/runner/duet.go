package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DuetStatus describes the final outcome of a duet run.
type DuetStatus string

const (
	DuetStatusApproved        DuetStatus = "approved"
	DuetStatusMaxCycles       DuetStatus = "max_cycles_reached"
	DuetStatusBuilderFailed   DuetStatus = "builder_failed"
	DuetStatusVerifierFailed  DuetStatus = "verifier_failed"
	DuetStatusInterrupted     DuetStatus = "interrupted"
)

// DuetConfig holds the parameters for a builder-verifier duet run.
type DuetConfig struct {
	Builder       RunConfig
	Verifier      RunConfig
	MaxCycles     int    // 0 = unlimited
	RunsDir       string // parent directory for the duet run tree
	DuetID        string // optional: pre-generated duet ID; if empty, a UUID is generated
}

// DuetResult is the summary returned after a duet run finishes.
type DuetResult struct {
	DuetID       string
	Cycles       int
	Status       DuetStatus
	Duration     time.Duration
	LastFeedback string // last verifier rejection reason, if any
}

// DuetMeta is written to <duet-id>/meta.json.
type DuetMeta struct {
	DuetID    string `json:"duet_id"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at,omitempty"`
	Status    string `json:"status"`
	Cycles    int    `json:"cycles"`
	MaxCycles int    `json:"max_cycles"`
}

// DuetRunner orchestrates a builder-verifier loop above two Runner instances.
// It does not modify runner.go; it composes two Runner calls in sequence.
type DuetRunner struct {
	cfg       DuetConfig
	duetID    string
	startedAt time.Time
	eventChan chan<- Event // shared event channel for TUI
}

// NewDuetRunner creates a DuetRunner. The event channel (if set on either
// RunConfig) is used to emit EventDuetPhase transitions to the TUI.
func NewDuetRunner(cfg DuetConfig) *DuetRunner {
	id := cfg.DuetID
	if id == "" {
		id = newUUID()
	}
	return &DuetRunner{
		cfg:    cfg,
		duetID: id,
	}
}

// Run executes the builder-verifier loop until approved, max cycles, or an error.
func (d *DuetRunner) Run(ctx context.Context) DuetResult {
	d.startedAt = time.Now()
	// The event channel is taken from whichever RunConfig has one set.
	d.eventChan = d.cfg.Builder.EventChan
	if d.eventChan == nil {
		d.eventChan = d.cfg.Verifier.EventChan
	}

	result := DuetResult{DuetID: d.duetID}

	d.writeDuetMeta(DuetStatus("running"), 0)

	builderPromptBase := d.cfg.Builder.Prompt
	feedbackAccum := ""

	for {
		result.Cycles++

		// Cycle directory layout: <duet-id>/cycle-N/builder/ and /verifier/
		cycleDir := filepath.Join(d.cfg.RunsDir, d.duetID, fmt.Sprintf("cycle-%d", result.Cycles))

		// --- Builder phase ---
		d.emitPhase("BUILDER", result.Cycles)

		builderPrompt := builderPromptBase
		if feedbackAccum != "" {
			builderPrompt += "\n\n---\n## Previous verifier rejections\n\n" + feedbackAccum
		}

		builderCfg := d.cfg.Builder
		builderCfg.RunsDir = filepath.Join(cycleDir, "builder")
		builderCfg.Prompt = builderPrompt

		if err := os.MkdirAll(builderCfg.RunsDir, 0755); err != nil {
			result.Status = DuetStatusBuilderFailed
			result.Duration = time.Since(d.startedAt)
			d.writeDuetMeta(result.Status, result.Cycles)
			return result
		}

		builderResult := New(builderCfg).Run(ctx)

		switch builderResult.Status {
		case StatusInterrupted:
			result.Status = DuetStatusInterrupted
			result.Duration = time.Since(d.startedAt)
			d.writeDuetMeta(result.Status, result.Cycles)
			return result
		case StatusFailed, StatusMaxIterationsReached, StatusStuck:
			result.Status = DuetStatusBuilderFailed
			result.Duration = time.Since(d.startedAt)
			d.writeDuetMeta(result.Status, result.Cycles)
			return result
		}

		// --- Verifier phase ---
		d.emitPhase("VERIFIER", result.Cycles)

		// Feed PROGRESS.md from the builder run as verifier context.
		builderRunDir := filepath.Join(builderCfg.RunsDir, builderResult.RunID)
		progressContext := d.readProgress(builderRunDir)

		verifierCfg := d.cfg.Verifier
		verifierCfg.RunsDir = filepath.Join(cycleDir, "verifier")
		if progressContext != "" {
			verifierCfg.Prompt = d.cfg.Verifier.Prompt + "\n\n---\n## Builder progress (PROGRESS.md)\n\n" + progressContext
		}

		if err := os.MkdirAll(verifierCfg.RunsDir, 0755); err != nil {
			result.Status = DuetStatusVerifierFailed
			result.Duration = time.Since(d.startedAt)
			d.writeDuetMeta(result.Status, result.Cycles)
			return result
		}

		verifierResult := New(verifierCfg).Run(ctx)

		if verifierResult.Status == StatusInterrupted {
			result.Status = DuetStatusInterrupted
			result.Duration = time.Since(d.startedAt)
			d.writeDuetMeta(result.Status, result.Cycles)
			return result
		}

		// Parse the verifier's verdict from the last assistant text.
		verdict, reason := parseVerdict(d.readAssistantText(filepath.Join(verifierCfg.RunsDir, verifierResult.RunID)))

		switch verdict {
		case verdictApproved:
			result.Status = DuetStatusApproved
			result.Duration = time.Since(d.startedAt)
			d.writeDuetMeta(result.Status, result.Cycles)
			return result

		case verdictRejected:
			result.LastFeedback = reason
			feedbackAccum += fmt.Sprintf("### Cycle %d\n%s\n\n", result.Cycles, reason)

		default:
			// Verifier emitted neither APPROVED nor REJECTED — treat as
			// rejection so the builder gets another chance to self-heal.
			reason = "verifier did not emit an explicit verdict"
			result.LastFeedback = reason
			feedbackAccum += fmt.Sprintf("### Cycle %d\n%s\n\n", result.Cycles, reason)
		}

		// Check max cycles.
		if d.cfg.MaxCycles > 0 && result.Cycles >= d.cfg.MaxCycles {
			result.Status = DuetStatusMaxCycles
			result.Duration = time.Since(d.startedAt)
			d.writeDuetMeta(result.Status, result.Cycles)
			return result
		}
	}
}

// emitPhase sends an EventDuetPhase event to the TUI channel.
func (d *DuetRunner) emitPhase(phase string, cycle int) {
	if d.eventChan == nil {
		return
	}
	select {
	case d.eventChan <- Event{
		Type:          EventDuetPhase,
		Timestamp:     time.Now().Format(time.RFC3339),
		DuetPhase:     phase,
		DuetCycle:     cycle,
		DuetMaxCycles: d.cfg.MaxCycles,
	}:
	default:
	}
}

// writeDuetMeta writes meta.json to the duet root directory.
func (d *DuetRunner) writeDuetMeta(status DuetStatus, cycles int) {
	dir := filepath.Join(d.cfg.RunsDir, d.duetID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	endedAt := ""
	if status != "running" {
		endedAt = time.Now().Format(time.RFC3339)
	}
	meta := DuetMeta{
		DuetID:    d.duetID,
		StartedAt: d.startedAt.Format(time.RFC3339),
		EndedAt:   endedAt,
		Status:    string(status),
		Cycles:    cycles,
		MaxCycles: d.cfg.MaxCycles,
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return
	}
	data = append(data, '\n')
	_ = os.WriteFile(filepath.Join(dir, "meta.json"), data, 0644)
}

// readProgress reads PROGRESS.md from a run directory. Returns empty string if missing.
func (d *DuetRunner) readProgress(runDir string) string {
	data, err := os.ReadFile(filepath.Join(runDir, "PROGRESS.md"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// readAssistantText reads the full session.log for verdict parsing.
// parseVerdict searches the entire content so verdicts in any message block are found.
func (d *DuetRunner) readAssistantText(runDir string) string {
	data, err := os.ReadFile(filepath.Join(runDir, "session.log"))
	if err != nil {
		return ""
	}
	return string(data)
}

type verdictKind int

const (
	verdictNone     verdictKind = iota
	verdictApproved
	verdictRejected
)

const (
	approvedMarker = "<promise>APPROVED</promise>"
	rejectedPrefix = "<promise>REJECTED:"
	markerClose    = "</promise>"
)

// parseVerdict scans text for APPROVED or REJECTED markers. When both appear
// (e.g. the verifier flips its verdict mid-message), the later marker wins so
// the final stated verdict is honored.
func parseVerdict(text string) (verdictKind, string) {
	approvedIdx := strings.LastIndex(text, approvedMarker)
	rejectedIdx := strings.LastIndex(text, rejectedPrefix)

	if rejectedIdx > approvedIdx {
		rest := text[rejectedIdx+len(rejectedPrefix):]
		end := strings.Index(rest, markerClose)
		if end < 0 {
			return verdictNone, ""
		}
		return verdictRejected, strings.TrimSpace(rest[:end])
	}
	if approvedIdx >= 0 {
		return verdictApproved, ""
	}
	return verdictNone, ""
}
