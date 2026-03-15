// Package config handles loading and merging TOML configuration files
// for ralfinho. Two files are loaded and merged:
//
//   - Global:  ~/.config/ralfinho/config.toml  (XDG / os.UserConfigDir)
//   - Local:   .ralfinho/config.toml            (project-level)
//
// Local values take precedence over global values. Fields absent from a file
// are left at their zero value and are therefore skippable during merge.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// FileConfig represents the structure of a ralfinho TOML config file.
//
// Pointer fields (*int, *bool) distinguish an explicitly set zero/false value
// from a field that was not present in the file at all. String fields use the
// empty string as "not set" — a deliberate empty string in the config is
// therefore indistinguishable from omission, which is acceptable because
// neither Agent nor RunsDir are ever validly empty.
type FileConfig struct {
	Agent         string                 `toml:"agent"`
	MaxIterations *int                   `toml:"max-iterations"`
	RunsDir       string                 `toml:"runs-dir"`
	NoTUI         *bool                  `toml:"no-tui"`
	Agents        map[string]AgentConfig `toml:"agents"`
}

// AgentConfig holds per-agent settings that can be customised in the config
// file. At the moment only ExtraArgs is supported, which appends additional
// flags to the agent subprocess command line.
type AgentConfig struct {
	// ExtraArgs is appended verbatim to the agent subprocess command line
	// after all built-in flags. Useful for passing flags that ralfinho does
	// not expose directly (e.g. "--model" for claude).
	ExtraArgs []string `toml:"extra-args"`
}

// Load reads the global and local config files, merges them, and returns the
// result. Local values take precedence over global ones.
//
// Missing files are silently skipped (not an error). A read or parse failure
// is always returned as an error.
func Load() (*FileConfig, error) {
	globalDir, err := os.UserConfigDir()
	if err != nil {
		// Non-fatal: skip global config if we cannot locate the XDG dir.
		globalDir = ""
	}

	var global *FileConfig
	if globalDir != "" {
		global, err = loadFile(filepath.Join(globalDir, "ralfinho", "config.toml"))
		if err != nil {
			return nil, err
		}
	}

	local, err := loadFile(filepath.Join(".ralfinho", "config.toml"))
	if err != nil {
		return nil, err
	}

	return merge(global, local), nil
}

// loadFile reads and parses a single TOML config file at path.
// Returns nil, nil when the file does not exist.
func loadFile(path string) (*FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	var cfg FileConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &cfg, nil
}

// merge combines base and override into a single FileConfig. For scalar fields,
// the override replaces the base only when it carries a non-zero value. For
// per-agent configs, the override replaces the base entry for the same agent
// name (no deep-merge of individual agent fields).
//
// Neither argument is modified; a new FileConfig is always returned.
func merge(base, override *FileConfig) *FileConfig {
	if base == nil && override == nil {
		return &FileConfig{}
	}
	if base == nil {
		return override
	}
	if override == nil {
		return base
	}

	// Start from a shallow copy of base.
	result := *base

	if override.Agent != "" {
		result.Agent = override.Agent
	}
	if override.MaxIterations != nil {
		result.MaxIterations = override.MaxIterations
	}
	if override.RunsDir != "" {
		result.RunsDir = override.RunsDir
	}
	if override.NoTUI != nil {
		result.NoTUI = override.NoTUI
	}

	// Merge per-agent configs: override wins per agent name (full replacement,
	// not field-level merge within an agent). Build a new map to avoid aliasing
	// the base map.
	if len(override.Agents) > 0 {
		merged := make(map[string]AgentConfig, len(result.Agents)+len(override.Agents))
		for k, v := range result.Agents {
			merged[k] = v
		}
		for k, v := range override.Agents {
			merged[k] = v
		}
		result.Agents = merged
	} else if result.Agents != nil {
		// Re-allocate to avoid sharing the base map with the caller.
		copied := make(map[string]AgentConfig, len(result.Agents))
		for k, v := range result.Agents {
			copied[k] = v
		}
		result.Agents = copied
	}

	return &result
}
