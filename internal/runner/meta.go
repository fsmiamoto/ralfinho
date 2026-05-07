package runner

import (
	"encoding/json"
	"fmt"
	"os"
)

// RunMeta is the structure written to meta.json at the end of a run.
type RunMeta struct {
	RunID               string `json:"run_id"`
	StartedAt           string `json:"started_at"`
	EndedAt             string `json:"ended_at"`
	Status              string `json:"status"`
	Agent               string `json:"agent"`
	PromptSource        string `json:"prompt_source"`
	PromptFile          string `json:"prompt_file"`
	PlanFile            string `json:"plan_file"`
	MaxIterations       int    `json:"max_iterations"`
	IterationsCompleted int    `json:"iterations_completed"`
	DurationMs          int64  `json:"duration_ms,omitempty"`

	// Token usage totals across the entire run. Populated when the agent
	// backend reports usage (currently Claude). Omitted when zero so old
	// runs and non-reporting agents stay clean.
	TotalInputTokens         int `json:"total_input_tokens,omitempty"`
	TotalOutputTokens        int `json:"total_output_tokens,omitempty"`
	TotalCacheReadTokens     int `json:"total_cache_read_tokens,omitempty"`
	TotalCacheCreationTokens int `json:"total_cache_creation_tokens,omitempty"`
}

// writeMetaJSON writes meta.json to the given path.
func writeMetaJSON(path string, meta RunMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling meta: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing meta.json: %w", err)
	}
	return nil
}
