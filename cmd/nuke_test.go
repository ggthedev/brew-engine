package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/brewexplorer/brew-engine/internal/contract"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// runNukeCmd resets the package-level flag vars, sets them as requested, and
// calls runNuke directly, capturing its stdout JSON output.
func runNukeCmd(t *testing.T, brew, app, all bool) contract.Response {
	t.Helper()

	nukeBrewCache = brew
	nukeAppCache = app
	nukeAll = all
	t.Cleanup(func() {
		nukeBrewCache = false
		nukeAppCache = false
		nukeAll = false
	})

	buf := captureStdoutBytes(t, func() {
		_ = runNuke(nil, nil)
	})

	var resp contract.Response
	if err := json.Unmarshal(bytes.TrimSpace(buf), &resp); err != nil {
		t.Fatalf("invalid JSON response: %v\nraw: %s", err, buf)
	}
	return resp
}

// fakeBrewCleanup installs an execNukeCommand override that writes a script
// accepting `cleanup -s --prune=all` and exits with the given code.
func fakeBrewCleanup(t *testing.T, exitCode int) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n"
	if exitCode != 0 {
		script += "exit " + string(rune('0'+exitCode)) + "\n"
	} else {
		script += "exit 0\n"
	}
	brewBin := filepath.Join(dir, "brew")
	if err := os.WriteFile(brewBin, []byte(script), 0o755); err != nil {
		t.Fatalf("fakeBrewCleanup: %v", err)
	}
	origExec := execNukeCommand
	execNukeCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command(brewBin, args...)
	}
	t.Setenv("BREW_ENGINE_BREW_PATH", brewBin)
	t.Cleanup(func() { execNukeCommand = origExec })
}

// ─── default (no flags) ───────────────────────────────────────────────────────

func TestNuke_NoFlags_DefaultsToBrewCleanup(t *testing.T) {
	fakeBrewCleanup(t, 0)

	resp := runNukeCmd(t, false, false, false)

	if !resp.Success {
		t.Errorf("expected success=true, got false (error: %s)", resp.Error)
	}
	if resp.Type != "nuke" {
		t.Errorf("type: got %q, want %q", resp.Type, "nuke")
	}

	var data contract.NukeData
	raw, _ := json.Marshal(resp.Data)
	json.Unmarshal(raw, &data)

	if !data.BrewCacheNuked {
		t.Error("expected brew_cache_nuked=true for default invocation")
	}
	if data.AppCacheNuked {
		t.Error("expected app_cache_nuked=false for default invocation")
	}
}

// ─── --brew ───────────────────────────────────────────────────────────────────

func TestNuke_BrewFlag_RunsBrewCleanup(t *testing.T) {
	fakeBrewCleanup(t, 0)

	resp := runNukeCmd(t, true, false, false)

	if !resp.Success {
		t.Errorf("expected success=true, got false")
	}

	var data contract.NukeData
	raw, _ := json.Marshal(resp.Data)
	json.Unmarshal(raw, &data)

	if !data.BrewCacheNuked {
		t.Error("expected brew_cache_nuked=true")
	}
	if data.BrewExitCode != 0 {
		t.Errorf("expected brew_exit_code=0, got %d", data.BrewExitCode)
	}
}

func TestNuke_BrewFlag_NonZeroExitReportsFailure(t *testing.T) {
	fakeBrewCleanup(t, 1)

	resp := runNukeCmd(t, true, false, false)

	if resp.Success {
		t.Error("expected success=false when brew cleanup exits non-zero")
	}
	if resp.Type != "nuke" {
		t.Errorf("type: got %q, want %q", resp.Type, "nuke")
	}

	var data contract.NukeData
	raw, _ := json.Marshal(resp.Data)
	json.Unmarshal(raw, &data)

	if !data.BrewCacheNuked {
		t.Error("expected brew_cache_nuked=true even on non-zero exit")
	}
	if data.BrewExitCode != 1 {
		t.Errorf("expected brew_exit_code=1, got %d", data.BrewExitCode)
	}
}

// ─── --app ────────────────────────────────────────────────────────────────────

func TestNuke_AppFlag_WipesAppCache(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("BREW_TUI_CACHE_DIR", cacheDir)

	// Populate cache with some files.
	os.WriteFile(filepath.Join(cacheDir, "list.json"), []byte(`[]`), 0o644)
	infoDir := filepath.Join(cacheDir, "info")
	os.MkdirAll(infoDir, 0o755)
	os.WriteFile(filepath.Join(infoDir, "wget.json"), []byte(`{}`), 0o644)

	resp := runNukeCmd(t, false, true, false)

	if !resp.Success {
		t.Errorf("expected success=true, got false (error: %s)", resp.Error)
	}
	if resp.Type != "nuke" {
		t.Errorf("type: got %q, want %q", resp.Type, "nuke")
	}

	var data contract.NukeData
	raw, _ := json.Marshal(resp.Data)
	json.Unmarshal(raw, &data)

	if !data.AppCacheNuked {
		t.Error("expected app_cache_nuked=true")
	}
	if data.AppCacheDir != cacheDir {
		t.Errorf("app_cache_dir: got %q, want %q", data.AppCacheDir, cacheDir)
	}
	if data.BrewCacheNuked {
		t.Error("expected brew_cache_nuked=false for --app-only invocation")
	}

	// Verify contents were removed but dir still exists.
	entries, _ := os.ReadDir(cacheDir)
	if len(entries) != 0 {
		t.Errorf("expected empty cache dir after nuke, got %d entries", len(entries))
	}
}

func TestNuke_AppFlag_NonExistentCacheDir_Succeeds(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", filepath.Join(t.TempDir(), "nonexistent"))

	resp := runNukeCmd(t, false, true, false)

	if !resp.Success {
		t.Errorf("expected success=true for missing cache dir, got false: %s", resp.Error)
	}
}

// ─── --all ────────────────────────────────────────────────────────────────────

func TestNuke_AllFlag_RunsBothOperations(t *testing.T) {
	fakeBrewCleanup(t, 0)

	cacheDir := t.TempDir()
	t.Setenv("BREW_TUI_CACHE_DIR", cacheDir)
	os.WriteFile(filepath.Join(cacheDir, "list.json"), []byte(`[]`), 0o644)

	resp := runNukeCmd(t, false, false, true)

	if !resp.Success {
		t.Errorf("expected success=true, got false (error: %s)", resp.Error)
	}

	var data contract.NukeData
	raw, _ := json.Marshal(resp.Data)
	json.Unmarshal(raw, &data)

	if !data.BrewCacheNuked {
		t.Error("expected brew_cache_nuked=true for --all")
	}
	if !data.AppCacheNuked {
		t.Error("expected app_cache_nuked=true for --all")
	}

	entries, _ := os.ReadDir(cacheDir)
	if len(entries) != 0 {
		t.Errorf("expected empty cache dir after --all nuke, got %d entries", len(entries))
	}
}

// ─── runBrewCleanup ───────────────────────────────────────────────────────────

func TestRunBrewCleanup_MissingBinary_ReturnsError(t *testing.T) {
	origExec := execNukeCommand
	execNukeCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/nonexistent/path/to/brew", args...)
	}
	t.Setenv("BREW_ENGINE_BREW_PATH", "/nonexistent/path/to/brew")
	t.Cleanup(func() { execNukeCommand = origExec })

	exitCode, err := runBrewCleanup()
	if err == nil {
		t.Error("expected error when brew binary is missing")
	}
	_ = exitCode
}
