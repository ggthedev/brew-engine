package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/brewexplorer/brew-engine/internal/contract"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// runCleanCmd resets the package-level flag vars, sets them as requested, and
// calls runClean directly, capturing its stdout JSON output.
func runCleanCmd(t *testing.T, logs, cache bool) contract.Response {
	t.Helper()

	// Reset global flag state (Cobra flags share package-level vars).
	cleanLogs = logs
	cleanCache = cache
	t.Cleanup(func() {
		cleanLogs = false
		cleanCache = false
	})

	buf := captureStdoutBytes(t, func() {
		_ = runClean(nil, nil)
	})

	var resp contract.Response
	if err := json.Unmarshal(bytes.TrimSpace(buf), &resp); err != nil {
		t.Fatalf("failed to parse JSON response: %v\nraw: %s", err, buf)
	}
	return resp
}

// captureStdoutBytes redirects os.Stdout during fn, returning what was written.
func captureStdoutBytes(t *testing.T, fn func()) []byte {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = origStdout })

	fn()

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.Bytes()
}

// ─── no flags ─────────────────────────────────────────────────────────────────

func TestClean_NoFlags_EmitsError(t *testing.T) {
	resp := runCleanCmd(t, false, false)
	if resp.Success {
		t.Error("expected success=false when no flags given")
	}
	if resp.Type != "error" {
		t.Errorf("type: got %q, want %q", resp.Type, "error")
	}
}

// ─── --logs ───────────────────────────────────────────────────────────────────

func TestClean_Logs_WipesLogDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BREW_ENGINE_LOG_DIR", dir)

	// Populate the dir with some files.
	os.WriteFile(filepath.Join(dir, "foo.log"), []byte("data"), 0o644)
	os.MkdirAll(filepath.Join(dir, "daily"), 0o755)
	os.WriteFile(filepath.Join(dir, "daily", "brew-engine-2026-01-01.log"), []byte("data"), 0o644)

	resp := runCleanCmd(t, true, false)
	if !resp.Success {
		t.Fatalf("expected success=true, got error: %v", resp.Error)
	}
	if resp.Type != "clean" {
		t.Errorf("type: got %q, want %q", resp.Type, "clean")
	}

	// Dir itself must still exist, but be empty.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected log dir to be empty, got %d entries", len(entries))
	}
}

func TestClean_Logs_NonExistentDir_Succeeds(t *testing.T) {
	t.Setenv("BREW_ENGINE_LOG_DIR", filepath.Join(t.TempDir(), "does", "not", "exist"))
	resp := runCleanCmd(t, true, false)
	if !resp.Success {
		t.Fatalf("expected success=true for non-existent dir, got: %v", resp.Error)
	}
}

// ─── --cache ──────────────────────────────────────────────────────────────────

func TestClean_Cache_WipesCacheDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BREW_TUI_CACHE_DIR", dir)

	os.WriteFile(filepath.Join(dir, "list.json"), []byte("{}"), 0o644)
	os.MkdirAll(filepath.Join(dir, "info"), 0o755)
	os.WriteFile(filepath.Join(dir, "info", "curl.json"), []byte("{}"), 0o644)

	resp := runCleanCmd(t, false, true)
	if !resp.Success {
		t.Fatalf("expected success=true, got: %v", resp.Error)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected cache dir to be empty, got %d entries", len(entries))
	}
}

// ─── --logs --cache ───────────────────────────────────────────────────────────

func TestClean_Both_WipesBothDirs(t *testing.T) {
	logDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("BREW_ENGINE_LOG_DIR", logDir)
	t.Setenv("BREW_TUI_CACHE_DIR", cacheDir)

	os.WriteFile(filepath.Join(logDir, "a.log"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(cacheDir, "list.json"), []byte("{}"), 0o644)

	resp := runCleanCmd(t, true, true)
	if !resp.Success {
		t.Fatalf("expected success=true, got: %v", resp.Error)
	}

	logEntries, _ := os.ReadDir(logDir)
	if len(logEntries) != 0 {
		t.Errorf("log dir not empty: %d entries", len(logEntries))
	}
	cacheEntries, _ := os.ReadDir(cacheDir)
	if len(cacheEntries) != 0 {
		t.Errorf("cache dir not empty: %d entries", len(cacheEntries))
	}
}

func TestClean_Both_ResponseHasBothFields(t *testing.T) {
	logDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("BREW_ENGINE_LOG_DIR", logDir)
	t.Setenv("BREW_TUI_CACHE_DIR", cacheDir)

	resp := runCleanCmd(t, true, true)
	if !resp.Success {
		t.Fatalf("unexpected failure: %v", resp.Error)
	}

	m, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("data is not a map: %T", resp.Data)
	}
	if v, _ := m["logs_cleared"].(bool); !v {
		t.Error("logs_cleared should be true")
	}
	if v, _ := m["cache_cleared"].(bool); !v {
		t.Error("cache_cleared should be true")
	}
	if m["log_dir"] == "" {
		t.Error("log_dir should be populated")
	}
	if m["cache_dir"] == "" {
		t.Error("cache_dir should be populated")
	}
}

// ─── cleanDir ─────────────────────────────────────────────────────────────────

func TestCleanDir_RemovesFilesAndSubdirs(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("data"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub", "deep"), 0o755)

	if err := cleanDir(dir); err != nil {
		t.Fatalf("cleanDir returned error: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("expected empty dir, got %d entries", len(entries))
	}
}

func TestCleanDir_NonExistentDir_ReturnsNil(t *testing.T) {
	if err := cleanDir(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Errorf("expected nil for non-existent dir, got: %v", err)
	}
}

func TestCleanDir_EmptyDir_ReturnsNil(t *testing.T) {
	if err := cleanDir(t.TempDir()); err != nil {
		t.Errorf("expected nil for empty dir, got: %v", err)
	}
}

// ─── resolveCacheDir ──────────────────────────────────────────────────────────

func TestResolveCacheDir_UsesEnvVar(t *testing.T) {
	want := t.TempDir()
	t.Setenv("BREW_TUI_CACHE_DIR", want)
	if got := resolveCacheDir(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveCacheDir_FallbackContainsBrewExplorer(t *testing.T) {
	t.Setenv("BREW_TUI_CACHE_DIR", "")
	got := resolveCacheDir()
	if got == "" {
		t.Skip("UserHomeDir unavailable")
	}
	if filepath.Base(filepath.Dir(got)) != "BrewExplorer" {
		t.Errorf("expected BrewExplorer parent, got path: %s", got)
	}
}
