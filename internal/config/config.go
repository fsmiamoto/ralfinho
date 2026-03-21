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
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// FileConfig represents the structure of a ralfinho TOML config file.
//
// Pointer fields (*int, *bool) distinguish an explicitly set zero/false value
// from a field that was not present in the file at all. String fields use the
// empty string as "not set" — a deliberate empty string in the config is
// therefore indistinguishable from omission, which is acceptable because
// neither Agent nor RunsDir are ever validly empty.
//
// Dir is populated after loading and records the directory containing the TOML
// file. It is not read from TOML. Template values also track their own origin
// directories internally so merged global/local configs can still resolve
// file-based template references relative to the file that defined each field.
type FileConfig struct {
	Agent             string                 `toml:"agent"`
	MaxIterations     *int                   `toml:"max-iterations"`
	InactivityTimeout *string                `toml:"inactivity-timeout"`
	RunsDir           string                 `toml:"runs-dir"`
	NoTUI             *bool                  `toml:"no-tui"`
	Agents            map[string]AgentConfig `toml:"agents"`
	Templates         TemplatesConfig        `toml:"templates"`
	Dir               string                 `toml:"-"`
}

// TemplatesConfig holds optional prompt template overrides loaded from TOML.
//
// Plan and Default are the raw config values. Each may be inline template text
// or a file: reference. The internal *_dir fields track which config file a
// given value came from so merged configs can resolve relative paths correctly
// on a per-field basis.
type TemplatesConfig struct {
	Plan    string `toml:"plan"`
	Default string `toml:"default"`

	planDir    string
	defaultDir string
}

// ResolvedTemplates contains prompt template overrides after any file:
// references have been resolved to their file contents.
type ResolvedTemplates struct {
	Plan    string
	Default string
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

	cfg.Dir = filepath.Dir(path)
	if cfg.Templates.Plan != "" {
		cfg.Templates.planDir = cfg.Dir
	}
	if cfg.Templates.Default != "" {
		cfg.Templates.defaultDir = cfg.Dir
	}

	return &cfg, nil
}

// merge combines base and override into a single FileConfig. For scalar fields,
// the override replaces the base only when it carries a non-zero value. For
// per-agent configs, the override replaces the base entry for the same agent
// name (no deep-merge of individual agent fields). Template fields merge
// independently so one file can override only templates.plan or
// templates.default.
//
// Neither argument is modified; a new FileConfig is always returned, except in
// the historical nil-base/nil-override fast paths which return the non-nil
// input directly.
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
	if override.InactivityTimeout != nil {
		result.InactivityTimeout = override.InactivityTimeout
	}
	if override.RunsDir != "" {
		result.RunsDir = override.RunsDir
	}
	if override.NoTUI != nil {
		result.NoTUI = override.NoTUI
	}
	if override.Dir != "" {
		result.Dir = override.Dir
	}
	if override.Templates.Plan != "" {
		result.Templates.Plan = override.Templates.Plan
		result.Templates.planDir = override.Templates.planDir
	}
	if override.Templates.Default != "" {
		result.Templates.Default = override.Templates.Default
		result.Templates.defaultDir = override.Templates.defaultDir
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

// ResolveTemplateValue resolves a config template value into template text.
//
// Values using the file: prefix are read from disk. Relative paths are
// resolved relative to configDir; absolute paths are used as-is. Any other
// value is treated as inline template text and returned verbatim.
func ResolveTemplateValue(value, configDir string) (string, error) {
	if !strings.HasPrefix(value, "file:") {
		return value, nil
	}

	templatePath := strings.TrimPrefix(value, "file:")
	if templatePath == "" {
		return "", fmt.Errorf("template file path is empty")
	}
	if !filepath.IsAbs(templatePath) {
		templatePath = filepath.Join(configDir, templatePath)
	}

	data, err := os.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("reading template file %q: %w", templatePath, err)
	}
	return string(data), nil
}

// ResolveTemplates resolves any configured prompt template overrides after
// global/local config merging.
func ResolveTemplates(cfg *FileConfig) (ResolvedTemplates, error) {
	if cfg == nil {
		return ResolvedTemplates{}, nil
	}

	planDir := cfg.Templates.planDir
	if planDir == "" {
		planDir = cfg.Dir
	}
	plan, err := ResolveTemplateValue(cfg.Templates.Plan, planDir)
	if err != nil {
		return ResolvedTemplates{}, fmt.Errorf("resolving templates.plan: %w", err)
	}

	defaultDir := cfg.Templates.defaultDir
	if defaultDir == "" {
		defaultDir = cfg.Dir
	}
	defaultTemplate, err := ResolveTemplateValue(cfg.Templates.Default, defaultDir)
	if err != nil {
		return ResolvedTemplates{}, fmt.Errorf("resolving templates.default: %w", err)
	}

	return ResolvedTemplates{
		Plan:    plan,
		Default: defaultTemplate,
	}, nil
}

// ParseInactivityTimeout parses the InactivityTimeout field from a merged
// FileConfig into a time.Duration. Returns 0 when the field is nil (not set),
// which the runner interprets as "use default". Returns an error if the value
// is present but cannot be parsed as a Go duration string (e.g. "5m", "30s").
func ParseInactivityTimeout(cfg *FileConfig) (time.Duration, error) {
	if cfg == nil || cfg.InactivityTimeout == nil {
		return 0, nil
	}
	d, err := time.ParseDuration(*cfg.InactivityTimeout)
	if err != nil {
		return 0, fmt.Errorf("parsing inactivity-timeout %q: %w", *cfg.InactivityTimeout, err)
	}
	return d, nil
}
