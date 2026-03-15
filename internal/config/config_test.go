package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// loadFile tests
// ---------------------------------------------------------------------------

func TestLoadFile_NonExistent(t *testing.T) {
	t.Parallel()

	cfg, err := loadFile(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config for missing file, got: %+v", cfg)
	}
}

func TestLoadFile_ValidTOML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
agent = "claude"
runs-dir = "/tmp/runs"
no-tui = true
max-iterations = 5

[agents.claude]
extra-args = ["--model", "claude-opus-4-5"]

[agents.pi]
extra-args = ["--timeout", "30"]
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	cfg, err := loadFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}

	if cfg.Agent != "claude" {
		t.Errorf("Agent: got %q, want %q", cfg.Agent, "claude")
	}
	if cfg.RunsDir != "/tmp/runs" {
		t.Errorf("RunsDir: got %q, want %q", cfg.RunsDir, "/tmp/runs")
	}
	if cfg.NoTUI == nil || !*cfg.NoTUI {
		t.Errorf("NoTUI: expected *true, got %v", cfg.NoTUI)
	}
	if cfg.MaxIterations == nil || *cfg.MaxIterations != 5 {
		t.Errorf("MaxIterations: expected *5, got %v", cfg.MaxIterations)
	}

	claudeArgs, ok := cfg.Agents["claude"]
	if !ok {
		t.Fatal("expected agents.claude to be present")
	}
	if len(claudeArgs.ExtraArgs) != 2 || claudeArgs.ExtraArgs[0] != "--model" || claudeArgs.ExtraArgs[1] != "claude-opus-4-5" {
		t.Errorf("claude extra-args: got %v", claudeArgs.ExtraArgs)
	}

	piArgs, ok := cfg.Agents["pi"]
	if !ok {
		t.Fatal("expected agents.pi to be present")
	}
	if len(piArgs.ExtraArgs) != 2 || piArgs.ExtraArgs[0] != "--timeout" {
		t.Errorf("pi extra-args: got %v", piArgs.ExtraArgs)
	}
}

func TestLoadFile_ParseError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")

	if err := os.WriteFile(path, []byte("this is not valid toml = = ="), 0600); err != nil {
		t.Fatalf("writing bad config: %v", err)
	}

	cfg, err := loadFile(path)
	if err == nil {
		t.Fatalf("expected a parse error, got nil; cfg=%+v", cfg)
	}
}

func TestLoadFile_ReadError(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("creating config dir %q: %v", path, err)
	}

	cfg, err := loadFile(path)
	if err == nil {
		t.Fatalf("expected a read error, got nil; cfg=%+v", cfg)
	}
	if cfg != nil {
		t.Fatalf("expected nil config on read error, got %+v", cfg)
	}
	if !strings.Contains(err.Error(), "reading config") {
		t.Fatalf("error should mention reading config, got: %v", err)
	}
}

func TestLoadFile_PointerFieldsDistinguishZeroFromAbsent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// File with zero values explicitly set.
	zeroPath := filepath.Join(dir, "zero.toml")
	if err := os.WriteFile(zeroPath, []byte("max-iterations = 0\nno-tui = false\n"), 0600); err != nil {
		t.Fatalf("writing zero config: %v", err)
	}
	zeroCfg, err := loadFile(zeroPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if zeroCfg.MaxIterations == nil {
		t.Error("MaxIterations should be a non-nil pointer when explicitly set to 0")
	} else if *zeroCfg.MaxIterations != 0 {
		t.Errorf("MaxIterations: got %d, want 0", *zeroCfg.MaxIterations)
	}
	if zeroCfg.NoTUI == nil {
		t.Error("NoTUI should be a non-nil pointer when explicitly set to false")
	} else if *zeroCfg.NoTUI != false {
		t.Errorf("NoTUI: got %v, want false", *zeroCfg.NoTUI)
	}

	// File with these fields completely absent.
	emptyPath := filepath.Join(dir, "empty.toml")
	if err := os.WriteFile(emptyPath, []byte("agent = \"pi\"\n"), 0600); err != nil {
		t.Fatalf("writing empty config: %v", err)
	}
	emptyCfg, err := loadFile(emptyPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emptyCfg.MaxIterations != nil {
		t.Errorf("MaxIterations should be nil when absent, got %v", emptyCfg.MaxIterations)
	}
	if emptyCfg.NoTUI != nil {
		t.Errorf("NoTUI should be nil when absent, got %v", emptyCfg.NoTUI)
	}
}

// ---------------------------------------------------------------------------
// merge tests
// ---------------------------------------------------------------------------

func TestMerge_BothNil(t *testing.T) {
	t.Parallel()

	got := merge(nil, nil)
	if got == nil {
		t.Fatal("merge(nil, nil) should return a non-nil empty config")
	}
}

func TestMerge_NilBase(t *testing.T) {
	t.Parallel()

	override := &FileConfig{Agent: "claude"}
	got := merge(nil, override)
	if got != override {
		t.Errorf("merge(nil, override): expected override to be returned")
	}
}

func TestMerge_NilOverride(t *testing.T) {
	t.Parallel()

	base := &FileConfig{Agent: "pi"}
	got := merge(base, nil)
	if got != base {
		t.Errorf("merge(base, nil): expected base to be returned")
	}
}

func TestMerge_ScalarOverride(t *testing.T) {
	t.Parallel()

	n := 10
	noTUI := true
	base := &FileConfig{
		Agent:   "pi",
		RunsDir: "/base/runs",
	}
	override := &FileConfig{
		Agent:         "claude",
		MaxIterations: &n,
		NoTUI:         &noTUI,
	}

	got := merge(base, override)

	if got.Agent != "claude" {
		t.Errorf("Agent: got %q, want %q", got.Agent, "claude")
	}
	// RunsDir not set in override → base value kept.
	if got.RunsDir != "/base/runs" {
		t.Errorf("RunsDir: got %q, want %q", got.RunsDir, "/base/runs")
	}
	if got.MaxIterations == nil || *got.MaxIterations != 10 {
		t.Errorf("MaxIterations: expected *10, got %v", got.MaxIterations)
	}
	if got.NoTUI == nil || !*got.NoTUI {
		t.Errorf("NoTUI: expected *true, got %v", got.NoTUI)
	}
}

func TestMerge_ScalarOverride_ZeroValues(t *testing.T) {
	t.Parallel()

	// Explicitly set zero/false should still take effect over a non-zero base.
	n := 0
	noTUI := false
	five := 5
	trueVal := true
	base := &FileConfig{
		MaxIterations: &five,
		NoTUI:         &trueVal,
	}
	override := &FileConfig{
		MaxIterations: &n,
		NoTUI:         &noTUI,
	}

	got := merge(base, override)

	if got.MaxIterations == nil || *got.MaxIterations != 0 {
		t.Errorf("MaxIterations: expected *0, got %v", got.MaxIterations)
	}
	if got.NoTUI == nil || *got.NoTUI != false {
		t.Errorf("NoTUI: expected *false, got %v", got.NoTUI)
	}
}

func TestMerge_AgentConfigOverrideReplaces(t *testing.T) {
	t.Parallel()

	base := &FileConfig{
		Agents: map[string]AgentConfig{
			"claude": {ExtraArgs: []string{"--base-flag"}},
			"pi":     {ExtraArgs: []string{"--pi-flag"}},
		},
	}
	override := &FileConfig{
		Agents: map[string]AgentConfig{
			// claude entry is fully replaced, not appended to.
			"claude": {ExtraArgs: []string{"--override-flag"}},
			// kiro is new in the override.
			"kiro": {ExtraArgs: []string{"--kiro-flag"}},
		},
	}

	got := merge(base, override)

	claudeArgs := got.Agents["claude"].ExtraArgs
	if len(claudeArgs) != 1 || claudeArgs[0] != "--override-flag" {
		t.Errorf("claude extra-args: expected [--override-flag], got %v", claudeArgs)
	}

	piArgs := got.Agents["pi"].ExtraArgs
	if len(piArgs) != 1 || piArgs[0] != "--pi-flag" {
		t.Errorf("pi extra-args: expected [--pi-flag] (from base), got %v", piArgs)
	}

	kiroArgs := got.Agents["kiro"].ExtraArgs
	if len(kiroArgs) != 1 || kiroArgs[0] != "--kiro-flag" {
		t.Errorf("kiro extra-args: expected [--kiro-flag] (from override), got %v", kiroArgs)
	}
}

func TestMerge_DoesNotMutateInputs(t *testing.T) {
	t.Parallel()

	base := &FileConfig{
		Agent: "pi",
		Agents: map[string]AgentConfig{
			"pi": {ExtraArgs: []string{"--base"}},
		},
	}
	override := &FileConfig{
		Agent: "claude",
		Agents: map[string]AgentConfig{
			"pi": {ExtraArgs: []string{"--override"}},
		},
	}

	_ = merge(base, override)

	// Base must be unchanged.
	if base.Agent != "pi" {
		t.Errorf("base.Agent mutated: got %q", base.Agent)
	}
	if got := base.Agents["pi"].ExtraArgs; len(got) != 1 || got[0] != "--base" {
		t.Errorf("base.Agents[pi] mutated: got %v", got)
	}
}

func TestMerge_OverrideWithoutAgentsStillCopiesBaseMap(t *testing.T) {
	t.Parallel()

	base := &FileConfig{
		Agent: "pi",
		Agents: map[string]AgentConfig{
			"pi": {ExtraArgs: []string{"--base"}},
		},
	}
	override := &FileConfig{Agent: "claude"}

	got := merge(base, override)
	got.Agents["pi"] = AgentConfig{ExtraArgs: []string{"--mutated"}}

	if got.Agent != "claude" {
		t.Fatalf("Agent: got %q, want %q", got.Agent, "claude")
	}
	if base.Agents["pi"].ExtraArgs[0] != "--base" {
		t.Fatalf("base.Agents mutated through merged config: got %v", base.Agents["pi"].ExtraArgs)
	}
}

// ---------------------------------------------------------------------------
// Load integration tests
// ---------------------------------------------------------------------------

func TestLoad_NeitherFileExists(t *testing.T) {
	// Cannot use t.Parallel() alongside t.Setenv.

	// Override both config dir locations so no real config file is found.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// Run from a temp dir that has no .ralfinho/config.toml.
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error when no files exist: %v", err)
	}
	if cfg == nil {
		t.Fatal("Load should return a non-nil config even when no files exist")
	}
	// All fields should be at their zero values.
	if cfg.Agent != "" || cfg.RunsDir != "" {
		t.Errorf("expected zero-value config, got %+v", cfg)
	}
}

func TestLoad_LocalOverridesGlobal(t *testing.T) {
	// Cannot use t.Parallel() alongside t.Setenv.

	tmpDir := t.TempDir()

	// Set up global config.
	globalCfgDir := filepath.Join(tmpDir, "xdg")
	if err := os.MkdirAll(filepath.Join(globalCfgDir, "ralfinho"), 0755); err != nil {
		t.Fatalf("mkdir global config dir: %v", err)
	}
	globalCfg := `
agent = "pi"
runs-dir = "/global/runs"
`
	if err := os.WriteFile(filepath.Join(globalCfgDir, "ralfinho", "config.toml"), []byte(globalCfg), 0600); err != nil {
		t.Fatalf("writing global config: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", globalCfgDir)

	// Set up local config in a temporary project directory.
	projectDir := filepath.Join(tmpDir, "project")
	localCfgDir := filepath.Join(projectDir, ".ralfinho")
	if err := os.MkdirAll(localCfgDir, 0755); err != nil {
		t.Fatalf("mkdir local config dir: %v", err)
	}
	localCfg := `
agent = "claude"
`
	if err := os.WriteFile(filepath.Join(localCfgDir, "config.toml"), []byte(localCfg), 0600); err != nil {
		t.Fatalf("writing local config: %v", err)
	}

	// Change into the project directory so Load() picks up .ralfinho/config.toml.
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	// Local agent overrides global.
	if cfg.Agent != "claude" {
		t.Errorf("Agent: got %q, want %q", cfg.Agent, "claude")
	}
	// RunsDir comes from global (not set locally).
	if cfg.RunsDir != "/global/runs" {
		t.Errorf("RunsDir: got %q, want %q", cfg.RunsDir, "/global/runs")
	}
}

func TestLoad_SkipsGlobalWhenUserConfigDirIsUnavailable(t *testing.T) {
	// Cannot use t.Parallel() alongside t.Setenv.

	projectDir := t.TempDir()
	localCfgDir := filepath.Join(projectDir, ".ralfinho")
	if err := os.MkdirAll(localCfgDir, 0755); err != nil {
		t.Fatalf("mkdir local config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localCfgDir, "config.toml"), []byte("agent = \"kiro\"\n"), 0600); err != nil {
		t.Fatalf("writing local config: %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", "relative-config-home")

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Agent != "kiro" {
		t.Fatalf("Agent: got %q, want %q", cfg.Agent, "kiro")
	}
}

func TestLoad_ReturnsGlobalConfigParseError(t *testing.T) {
	// Cannot use t.Parallel() alongside t.Setenv.

	tmpDir := t.TempDir()
	globalCfgDir := filepath.Join(tmpDir, "xdg")
	globalCfgPath := filepath.Join(globalCfgDir, "ralfinho", "config.toml")
	if err := os.MkdirAll(filepath.Dir(globalCfgPath), 0755); err != nil {
		t.Fatalf("mkdir global config dir: %v", err)
	}
	if err := os.WriteFile(globalCfgPath, []byte("agent = [\n"), 0600); err != nil {
		t.Fatalf("writing bad global config: %v", err)
	}
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", globalCfgDir)

	cfg, err := Load()
	if err == nil {
		t.Fatalf("expected global parse error, got nil; cfg=%+v", cfg)
	}
	if !strings.Contains(err.Error(), globalCfgPath) {
		t.Fatalf("error should mention global config path %q, got: %v", globalCfgPath, err)
	}
	if !strings.Contains(err.Error(), "parsing") {
		t.Fatalf("error should mention parsing, got: %v", err)
	}
}

func TestLoad_ReturnsLocalConfigParseError(t *testing.T) {
	// Cannot use t.Parallel() alongside t.Setenv.

	tmpDir := t.TempDir()
	globalCfgDir := filepath.Join(tmpDir, "xdg")
	if err := os.MkdirAll(filepath.Join(globalCfgDir, "ralfinho"), 0755); err != nil {
		t.Fatalf("mkdir global config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalCfgDir, "ralfinho", "config.toml"), []byte("agent = \"pi\"\n"), 0600); err != nil {
		t.Fatalf("writing global config: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", globalCfgDir)

	projectDir := filepath.Join(tmpDir, "project")
	localCfgDir := filepath.Join(projectDir, ".ralfinho")
	localCfgPath := filepath.Join(localCfgDir, "config.toml")
	if err := os.MkdirAll(localCfgDir, 0755); err != nil {
		t.Fatalf("mkdir local config dir: %v", err)
	}
	if err := os.WriteFile(localCfgPath, []byte("agent = {\n"), 0600); err != nil {
		t.Fatalf("writing bad local config: %v", err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := Load()
	if err == nil {
		t.Fatalf("expected local parse error, got nil; cfg=%+v", cfg)
	}
	if !strings.Contains(err.Error(), filepath.Join(".ralfinho", "config.toml")) {
		t.Fatalf("error should mention local config path %q, got: %v", filepath.Join(".ralfinho", "config.toml"), err)
	}
	if !strings.Contains(err.Error(), "parsing") {
		t.Fatalf("error should mention parsing, got: %v", err)
	}
}

func TestLoad_LocalFileOnly(t *testing.T) {
	// Cannot use t.Parallel() alongside t.Setenv.

	tmpDir := t.TempDir()

	// Redirect XDG config to an empty directory so there is no global config.
	emptyGlobalDir := filepath.Join(tmpDir, "empty-xdg")
	if err := os.MkdirAll(emptyGlobalDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", emptyGlobalDir)

	// Create a project dir with local config only.
	projectDir := filepath.Join(tmpDir, "project")
	localCfgDir := filepath.Join(projectDir, ".ralfinho")
	if err := os.MkdirAll(localCfgDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	n := 3
	_ = n
	if err := os.WriteFile(filepath.Join(localCfgDir, "config.toml"), []byte("max-iterations = 3\n"), 0600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if cfg.MaxIterations == nil || *cfg.MaxIterations != 3 {
		t.Errorf("MaxIterations: expected *3, got %v", cfg.MaxIterations)
	}
}
