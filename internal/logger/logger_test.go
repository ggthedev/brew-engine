package logger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// saveState / restoreState bracket each test that mutates the package-level
// Raw and Sugar globals so parallel execution remains safe.
func saveState() (*zap.Logger, *zap.SugaredLogger) { return Raw, Sugar }
func restoreState(r *zap.Logger, s *zap.SugaredLogger) {
	Raw = r
	Sugar = s
}

// ── resolveLogDir ────────────────────────────────────────────────────────────

func TestResolveLogDir_EnvVar(t *testing.T) {
	t.Setenv(envLogDir, "/tmp/custom-brew-log")
	if got := resolveLogDir(); got != "/tmp/custom-brew-log" {
		t.Errorf("expected /tmp/custom-brew-log, got %s", got)
	}
}

func TestResolveLogDir_DefaultContainsBrewEngine(t *testing.T) {
	t.Setenv(envLogDir, "")
	got := resolveLogDir()
	if got == "" {
		t.Fatal("resolveLogDir returned empty string")
	}
	if !strings.Contains(got, "brew-engine") {
		t.Errorf("expected brew-engine in default path, got %s", got)
	}
}

func TestResolveLogDir_TmpFallback_WhenUserHomeDirFails(t *testing.T) {
	origFn := userHomeDir
	t.Cleanup(func() { userHomeDir = origFn })

	t.Setenv(envLogDir, "")
	userHomeDir = func() (string, error) {
		return "", errors.New("no home directory configured")
	}

	got := resolveLogDir()
	if got != filepath.Join("/tmp", "brew-engine") {
		t.Errorf("expected /tmp/brew-engine, got %s", got)
	}
}

func TestResolveLogDir_DefaultUnderHome(t *testing.T) {
	t.Setenv(envLogDir, "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory:", err)
	}
	got := resolveLogDir()
	if !strings.HasPrefix(got, home) {
		t.Errorf("expected path under %s, got %s", home, got)
	}
}

// ── Init ─────────────────────────────────────────────────────────────────────

func TestInit_Success_CreatesLogFile(t *testing.T) {
	rawOld, sugarOld := saveState()
	t.Cleanup(func() { restoreState(rawOld, sugarOld) })

	dir := t.TempDir()
	t.Setenv(envLogDir, dir)

	if err := Init(); err != nil {
		t.Fatalf("Init() returned error: %v", err)
	}
	if Raw == nil {
		t.Error("Raw must not be nil after successful Init")
	}
	if Sugar == nil {
		t.Error("Sugar must not be nil after successful Init")
	}
	// With daily rotation the file lives in <dir>/daily/brew-engine-YYYY-MM-DD.log
	dailyDir := filepath.Join(dir, dailySubDir)
	entries, readErr := os.ReadDir(dailyDir)
	if readErr != nil || len(entries) == 0 {
		t.Errorf("daily log directory not populated at %s", dailyDir)
	}
}

func TestInit_CreatesNestedDirectory(t *testing.T) {
	rawOld, sugarOld := saveState()
	t.Cleanup(func() { restoreState(rawOld, sugarOld) })

	nested := filepath.Join(t.TempDir(), "deep", "nested", "brew-engine")
	t.Setenv(envLogDir, nested)

	if err := Init(); err != nil {
		t.Fatalf("Init() should create nested directories: %v", err)
	}
	if _, err := os.Stat(nested); os.IsNotExist(err) {
		t.Errorf("nested directory not created: %s", nested)
	}
}

func TestInit_Error_InvalidDirectory(t *testing.T) {
	rawOld, sugarOld := saveState()
	t.Cleanup(func() { restoreState(rawOld, sugarOld) })

	// Use a regular file as the parent so MkdirAll fails.
	tmpFile, err := os.CreateTemp("", "brew-test-parent-*")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	t.Setenv(envLogDir, filepath.Join(tmpFile.Name(), "subdir"))

	if err := Init(); err == nil {
		t.Error("Init() should return error when directory cannot be created")
	}
}

func TestInit_Error_ReadOnlyDirectory(t *testing.T) {
	rawOld, sugarOld := saveState()
	t.Cleanup(func() { restoreState(rawOld, sugarOld) })

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o444); err != nil {
		t.Skip("cannot change directory permissions:", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	t.Setenv(envLogDir, dir)

	if err := Init(); err == nil {
		t.Error("Init() should return error when log file cannot be opened")
	}
}

// ── Sync ─────────────────────────────────────────────────────────────────────

func TestSync_NilRaw_IsNoOp(t *testing.T) {
	rawOld, sugarOld := saveState()
	t.Cleanup(func() { restoreState(rawOld, sugarOld) })

	Raw = nil
	Sugar = nil
	Sync() // must not panic
}

func TestSync_AfterInit_DoesNotPanic(t *testing.T) {
	rawOld, sugarOld := saveState()
	t.Cleanup(func() { restoreState(rawOld, sugarOld) })

	t.Setenv(envLogDir, t.TempDir())
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	Sync() // must not panic
}

// ── LogDir ───────────────────────────────────────────────────────────────────

func TestLogDir_ReturnsEnvVar(t *testing.T) {
	t.Setenv(envLogDir, "/custom/log/path")
	if got := LogDir(); got != "/custom/log/path" {
		t.Errorf("LogDir() = %s, want /custom/log/path", got)
	}
}

func TestLogDir_BeforeInit_DoesNotPanic(t *testing.T) {
	rawOld, sugarOld := saveState()
	t.Cleanup(func() { restoreState(rawOld, sugarOld) })
	Raw = nil
	_ = LogDir() // must not panic
}

// ── resolveLogLevel ──────────────────────────────────────────────────────────

func TestResolveLogLevel_Default(t *testing.T) {
	t.Setenv(envLogLevel, "")
	if got := resolveLogLevel(); got.String() != "info" {
		t.Errorf("expected info, got %s", got.String())
	}
}

func TestResolveLogLevel_Debug(t *testing.T) {
	t.Setenv(envLogLevel, "debug")
	if got := resolveLogLevel(); got.String() != "debug" {
		t.Errorf("expected debug, got %s", got.String())
	}
}

func TestResolveLogLevel_Warn(t *testing.T) {
	t.Setenv(envLogLevel, "warn")
	if got := resolveLogLevel(); got.String() != "warn" {
		t.Errorf("expected warn, got %s", got.String())
	}
}

func TestResolveLogLevel_Warning(t *testing.T) {
	t.Setenv(envLogLevel, "warning")
	if got := resolveLogLevel(); got.String() != "warn" {
		t.Errorf("expected warn (from warning), got %s", got.String())
	}
}

func TestResolveLogLevel_Error(t *testing.T) {
	t.Setenv(envLogLevel, "error")
	if got := resolveLogLevel(); got.String() != "error" {
		t.Errorf("expected error, got %s", got.String())
	}
}

func TestResolveLogLevel_Invalid_DefaultsToInfo(t *testing.T) {
	t.Setenv(envLogLevel, "invalid")
	if got := resolveLogLevel(); got.String() != "info" {
		t.Errorf("expected info (from invalid), got %s", got.String())
	}
}

func TestResolveLogLevel_CaseInsensitive(t *testing.T) {
	t.Setenv(envLogLevel, "DEBUG")
	if got := resolveLogLevel(); got.String() != "debug" {
		t.Errorf("expected debug (from DEBUG), got %s", got.String())
	}
}

func TestResolveLogLevel_TrimWhitespace(t *testing.T) {
	t.Setenv(envLogLevel, "  debug  ")
	if got := resolveLogLevel(); got.String() != "debug" {
		t.Errorf("expected debug (trimmed), got %s", got.String())
	}
}

// ── buildBaseFields ──────────────────────────────────────────────────────────

func TestBuildBaseFields_Empty(t *testing.T) {
	t.Setenv(envSessionID, "")
	t.Setenv(envRequestID, "")
	fields := buildBaseFields()
	if len(fields) != 0 {
		t.Errorf("expected 0 fields, got %d", len(fields))
	}
}

func TestBuildBaseFields_SessionOnly(t *testing.T) {
	t.Setenv(envSessionID, "sess-123")
	t.Setenv(envRequestID, "")
	fields := buildBaseFields()
	if len(fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(fields))
	}
	if fields[0].Key != "session_id" {
		t.Errorf("expected key session_id, got %s", fields[0].Key)
	}
}

func TestBuildBaseFields_RequestOnly(t *testing.T) {
	t.Setenv(envSessionID, "")
	t.Setenv(envRequestID, "req-456")
	fields := buildBaseFields()
	if len(fields) != 1 {
		t.Fatalf("expected 1 field, got %d", len(fields))
	}
	if fields[0].Key != "request_id" {
		t.Errorf("expected key request_id, got %s", fields[0].Key)
	}
}

func TestBuildBaseFields_Both(t *testing.T) {
	t.Setenv(envSessionID, "sess-123")
	t.Setenv(envRequestID, "req-456")
	fields := buildBaseFields()
	if len(fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(fields))
	}
}

// ── DebugEnabled ─────────────────────────────────────────────────────────────

func TestDebugEnabled_True(t *testing.T) {
	rawOld, sugarOld := saveState()
	oldLevel := Level
	t.Cleanup(func() {
		restoreState(rawOld, sugarOld)
		Level = oldLevel
	})

	t.Setenv(envLogLevel, "debug")
	t.Setenv(envLogDir, t.TempDir())
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if !DebugEnabled() {
		t.Error("DebugEnabled() should return true when level is debug")
	}
}

func TestDebugEnabled_False(t *testing.T) {
	rawOld, sugarOld := saveState()
	oldLevel := Level
	t.Cleanup(func() {
		restoreState(rawOld, sugarOld)
		Level = oldLevel
	})

	t.Setenv(envLogLevel, "info")
	t.Setenv(envLogDir, t.TempDir())
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if DebugEnabled() {
		t.Error("DebugEnabled() should return false when level is info")
	}
}

// ── Init with log level ──────────────────────────────────────────────────────

func TestInit_SetsLevelFromEnv(t *testing.T) {
	rawOld, sugarOld := saveState()
	oldLevel := Level
	t.Cleanup(func() {
		restoreState(rawOld, sugarOld)
		Level = oldLevel
	})

	t.Setenv(envLogLevel, "warn")
	t.Setenv(envLogDir, t.TempDir())
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if Level.String() != "warn" {
		t.Errorf("expected Level=warn after Init, got %s", Level.String())
	}
}
