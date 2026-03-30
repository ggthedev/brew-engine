package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// stubHome overrides userHomeDir for the duration of the test.
func stubHome(t *testing.T, dir string) {
	t.Helper()
	orig := userHomeDir
	userHomeDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { userHomeDir = orig })
}

// stubPlutil overrides execPlutil to return a fake JSON payload.
func stubPlutil(t *testing.T, data map[string]string) {
	t.Helper()
	orig := execPlutil
	execPlutil = func(_ ...string) ([]byte, error) {
		b, _ := json.Marshal(data)
		return b, nil
	}
	t.Cleanup(func() { execPlutil = orig })
}

// stubPlistMissing overrides os.Stat indirectly by pointing home at a dir
// that has no Preferences/com.mobilityquarks.brewexplorer.plist file.
func stubPlistMissing(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	stubHome(t, home)
	return home
}

// clearEnv clears the brew-engine env vars for the duration of the test.
func clearEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		EnvLogDir, EnvLogFileName, EnvBrewOutputLogFileName, EnvLogLevel,
		EnvCacheDir, EnvListCacheFileName, EnvInfoCacheDirName, EnvBrewPath,
	}
	for _, k := range keys {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Cleanup(func() {
		for _, k := range keys {
			os.Unsetenv(k)
		}
	})
}

// ─── PlistPath ────────────────────────────────────────────────────────────────

func TestPlistPath_ReturnsCorrectPath(t *testing.T) {
	home := t.TempDir()
	stubHome(t, home)

	got, err := PlistPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := filepath.Join(home, "Library", "Preferences", "com.mobilityquarks.brewexplorer.plist")
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// ─── Load — no plist file ─────────────────────────────────────────────────────

func TestLoad_NoPlistFile_SetsDefaults(t *testing.T) {
	home := stubPlistMissing(t)
	clearEnv(t)

	if err := Load(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := os.Getenv(EnvLogLevel); got != DefaultLogLevel {
		t.Errorf("LogLevel: got %q, want %q", got, DefaultLogLevel)
	}
	if got := os.Getenv(EnvLogFileName); got != DefaultLogFileName {
		t.Errorf("LogFileName: got %q, want %q", got, DefaultLogFileName)
	}
	if got := os.Getenv(EnvBrewOutputLogFileName); got != DefaultBrewOutputLogFileName {
		t.Errorf("BrewOutputLogFileName: got %q, want %q", got, DefaultBrewOutputLogFileName)
	}
	if got := os.Getenv(EnvListCacheFileName); got != DefaultListCacheFileName {
		t.Errorf("ListCacheFileName: got %q, want %q", got, DefaultListCacheFileName)
	}
	if got := os.Getenv(EnvInfoCacheDirName); got != DefaultInfoCacheDirName {
		t.Errorf("InfoCacheDirName: got %q, want %q", got, DefaultInfoCacheDirName)
	}
	if got := os.Getenv(EnvLogDir); got != defaultLogDir(home) {
		t.Errorf("LogDir: got %q, want %q", got, defaultLogDir(home))
	}
	if got := os.Getenv(EnvCacheDir); got != defaultCacheDir(home) {
		t.Errorf("CacheDir: got %q, want %q", got, defaultCacheDir(home))
	}
}

// ─── Load — plist present ─────────────────────────────────────────────────────

func TestLoad_PlistOverridesDefaults(t *testing.T) {
	home := t.TempDir()
	stubHome(t, home)
	clearEnv(t)

	// Create a real plist file so os.Stat finds it, but stub plutil output.
	plistDir := filepath.Join(home, "Library", "Preferences")
	os.MkdirAll(plistDir, 0o755)
	os.WriteFile(filepath.Join(plistDir, "com.mobilityquarks.brewexplorer.plist"), []byte("placeholder"), 0o644)

	stubPlutil(t, map[string]string{
		KeyLogLevel:  "debug",
		KeyLogDir:    "/custom/logs",
		KeyCacheDir:  "/custom/cache",
	})

	if err := Load(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := os.Getenv(EnvLogLevel); got != "debug" {
		t.Errorf("LogLevel: got %q, want %q", got, "debug")
	}
	if got := os.Getenv(EnvLogDir); got != "/custom/logs" {
		t.Errorf("LogDir: got %q, want %q", got, "/custom/logs")
	}
	if got := os.Getenv(EnvCacheDir); got != "/custom/cache" {
		t.Errorf("CacheDir: got %q, want %q", got, "/custom/cache")
	}
	// Unspecified keys should fall back to defaults
	if got := os.Getenv(EnvLogFileName); got != DefaultLogFileName {
		t.Errorf("LogFileName: got %q, want %q", got, DefaultLogFileName)
	}
}

func TestLoad_EnvVarWinsOverPlist(t *testing.T) {
	home := t.TempDir()
	stubHome(t, home)

	plistDir := filepath.Join(home, "Library", "Preferences")
	os.MkdirAll(plistDir, 0o755)
	os.WriteFile(filepath.Join(plistDir, "com.mobilityquarks.brewexplorer.plist"), []byte("placeholder"), 0o644)

	stubPlutil(t, map[string]string{
		KeyLogLevel: "debug",
	})

	// Pre-set the env var — it should not be overwritten.
	t.Setenv(EnvLogLevel, "warn")

	if err := Load(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := os.Getenv(EnvLogLevel); got != "warn" {
		t.Errorf("env var should win: got %q, want %q", got, "warn")
	}
}

// ─── resolveBrewPath ──────────────────────────────────────────────────────────

func TestResolveBrewPath_UsesPlistValue(t *testing.T) {
	// Create a fake executable.
	dir := t.TempDir()
	fakeBrew := filepath.Join(dir, "brew")
	os.WriteFile(fakeBrew, []byte("#!/bin/sh\n"), 0o755)

	got := resolveBrewPath(map[string]string{KeyBrewPath: fakeBrew})
	if got != fakeBrew {
		t.Errorf("got %q, want %q", got, fakeBrew)
	}
}

func TestResolveBrewPath_SkipsNonExecutablePlistValue(t *testing.T) {
	// File exists but is not executable — should skip to next candidate.
	dir := t.TempDir()
	notExec := filepath.Join(dir, "brew")
	os.WriteFile(notExec, []byte(""), 0o644)

	got := resolveBrewPath(map[string]string{KeyBrewPath: notExec})
	// Should not return the non-executable path (may find real brew or return "")
	if got == notExec {
		t.Errorf("should not return non-executable path %q", notExec)
	}
}

func TestResolveBrewPath_EmptyPlist_FindsSystemBrew(t *testing.T) {
	got := resolveBrewPath(map[string]string{})
	// On a macOS machine with brew installed, this should find it.
	// We just verify it doesn't panic and returns a non-empty string or "".
	_ = got
}

// ─── isExecutable ─────────────────────────────────────────────────────────────

func TestIsExecutable_TrueForExecutableFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "bin")
	os.WriteFile(f, []byte("#!/bin/sh\n"), 0o755)
	if !isExecutable(f) {
		t.Error("expected true for executable file")
	}
}

func TestIsExecutable_FalseForNonExecutable(t *testing.T) {
	f := filepath.Join(t.TempDir(), "data")
	os.WriteFile(f, []byte("data"), 0o644)
	if isExecutable(f) {
		t.Error("expected false for non-executable file")
	}
}

func TestIsExecutable_FalseForMissingFile(t *testing.T) {
	if isExecutable("/nonexistent/path/brew") {
		t.Error("expected false for missing file")
	}
}

func TestIsExecutable_FalseForDirectory(t *testing.T) {
	if isExecutable(t.TempDir()) {
		t.Error("expected false for directory")
	}
}

// ─── defaultLogDir / defaultCacheDir ─────────────────────────────────────────

func TestDefaultLogDir_CorrectPath(t *testing.T) {
	got := defaultLogDir("/Users/alice")
	want := "/Users/alice/Library/Application Support/BrewExplorer/logs"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDefaultCacheDir_CorrectPath(t *testing.T) {
	got := defaultCacheDir("/Users/alice")
	want := "/Users/alice/Library/Application Support/BrewExplorer/cache"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
