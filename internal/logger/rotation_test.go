package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─── logFilePath ──────────────────────────────────────────────────────────────

func TestLogFilePath_NoSuffix(t *testing.T) {
	got := logFilePath("/logs", "2026-03-30", -1)
	want := "/logs/brew-engine-2026-03-30.log"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLogFilePath_ZeroSuffix(t *testing.T) {
	got := logFilePath("/logs", "2026-03-30", 0)
	want := "/logs/brew-engine-2026-03-30.0.log"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLogFilePath_NonZeroSuffix(t *testing.T) {
	got := logFilePath("/logs", "2026-03-30", 3)
	want := "/logs/brew-engine-2026-03-30.3.log"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ─── resolveLogFilePath ───────────────────────────────────────────────────────

func TestResolveLogFilePath_EmptyDir_ReturnsBase(t *testing.T) {
	dir := t.TempDir()
	path, suffix := resolveLogFilePath(dir, "2026-03-30")
	wantPath := filepath.Join(dir, "brew-engine-2026-03-30.log")
	if path != wantPath {
		t.Errorf("path: got %q, want %q", path, wantPath)
	}
	if suffix != -1 {
		t.Errorf("suffix: got %d, want -1", suffix)
	}
}

func TestResolveLogFilePath_ExistingBaseFile_ReturnsIt(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "brew-engine-2026-03-30.log"), []byte("x"), 0o644)

	path, suffix := resolveLogFilePath(dir, "2026-03-30")
	if !strings.HasSuffix(path, "brew-engine-2026-03-30.log") {
		t.Errorf("unexpected path: %q", path)
	}
	if suffix != -1 {
		t.Errorf("suffix: got %d, want -1", suffix)
	}
}

func TestResolveLogFilePath_ExistingOverflowFile_ReturnsHighest(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "brew-engine-2026-03-30.log"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "brew-engine-2026-03-30.1.log"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "brew-engine-2026-03-30.2.log"), []byte("x"), 0o644)

	_, suffix := resolveLogFilePath(dir, "2026-03-30")
	if suffix != 2 {
		t.Errorf("suffix: got %d, want 2", suffix)
	}
}

func TestResolveLogFilePath_DifferentDate_NotMatched(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "brew-engine-2026-03-29.log"), []byte("x"), 0o644)

	_, suffix := resolveLogFilePath(dir, "2026-03-30")
	if suffix != -1 {
		t.Errorf("suffix: got %d, want -1 (different date should not match)", suffix)
	}
}

// ─── newDailyWriter ───────────────────────────────────────────────────────────

func TestNewDailyWriter_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "daily")
	w, err := newDailyWriter(dir)
	if err != nil {
		t.Fatalf("newDailyWriter: %v", err)
	}
	defer w.currentFile.Close()

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("expected daily dir to be created")
	}
}

func TestNewDailyWriter_CreatesLogFile(t *testing.T) {
	dir := t.TempDir()
	w, err := newDailyWriter(dir)
	if err != nil {
		t.Fatalf("newDailyWriter: %v", err)
	}
	defer w.currentFile.Close()

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 log file, got %d", len(entries))
	}
	if !strings.HasPrefix(entries[0].Name(), "brew-engine-") {
		t.Errorf("unexpected file name: %s", entries[0].Name())
	}
}

func TestNewDailyWriter_Write_AppendsToFile(t *testing.T) {
	dir := t.TempDir()
	w, err := newDailyWriter(dir)
	if err != nil {
		t.Fatalf("newDailyWriter: %v", err)
	}
	defer w.currentFile.Close()

	msg := []byte(`{"level":"info","msg":"test"}` + "\n")
	n, err := w.Write(msg)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(msg) {
		t.Errorf("wrote %d bytes, want %d", n, len(msg))
	}

	content, _ := os.ReadFile(w.currentFile.Name())
	if !strings.Contains(string(content), "test") {
		t.Error("written content not found in log file")
	}
}

func TestNewDailyWriter_Sync_DoesNotError(t *testing.T) {
	dir := t.TempDir()
	w, err := newDailyWriter(dir)
	if err != nil {
		t.Fatalf("newDailyWriter: %v", err)
	}
	defer w.currentFile.Close()

	if err := w.Sync(); err != nil {
		t.Errorf("Sync() returned error: %v", err)
	}
}

// ─── rotation on size limit ───────────────────────────────────────────────────

func TestDailyWriter_RotatesOnSizeExceeded(t *testing.T) {
	dir := t.TempDir()
	w, err := newDailyWriter(dir)
	if err != nil {
		t.Fatalf("newDailyWriter: %v", err)
	}
	defer w.currentFile.Close()

	// Artificially set currentSize to just below limit, then write enough to trigger rotation.
	w.currentSize = maxLogFileSize - 1
	payload := make([]byte, 10) // 9 bytes over the cap
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("Write after size trigger: %v", err)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) < 2 {
		t.Errorf("expected at least 2 files after size rotation, got %d", len(entries))
	}
}

// ─── rotation on date change ──────────────────────────────────────────────────

func TestDailyWriter_RotatesOnDateChange(t *testing.T) {
	dir := t.TempDir()

	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	today := time.Now().Format("2006-01-02")

	// Start with currentDate returning yesterday so the initial file is yesterday's.
	orig := currentDate
	currentDate = func() string { return yesterday }
	t.Cleanup(func() { currentDate = orig })

	w, err := newDailyWriter(dir)
	if err != nil {
		t.Fatalf("newDailyWriter: %v", err)
	}
	defer w.currentFile.Close()

	if _, err := w.Write([]byte("yesterday\n")); err != nil {
		t.Fatalf("Write (yesterday): %v", err)
	}

	// Advance to today — next Write must trigger a date rotation.
	currentDate = func() string { return today }

	if _, err := w.Write([]byte("today\n")); err != nil {
		t.Fatalf("Write (today): %v", err)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) < 2 {
		t.Errorf("expected 2 files (yesterday + today) after date rotation, got %d", len(entries))
	}
}

// ─── MergeOldMonths ───────────────────────────────────────────────────────────

func TestMergeOldMonths_MergesOldFiles(t *testing.T) {
	logDir := t.TempDir()
	dailyDir := filepath.Join(logDir, dailySubDir)
	os.MkdirAll(dailyDir, 0o755)

	// Write two old-month daily files.
	os.WriteFile(filepath.Join(dailyDir, "brew-engine-2025-12-30.log"), []byte("line1\n"), 0o644)
	os.WriteFile(filepath.Join(dailyDir, "brew-engine-2025-12-31.log"), []byte("line2\n"), 0o644)
	// Write a current-month file — should NOT be merged.
	today := time.Now().Format("2006-01-02")
	os.WriteFile(filepath.Join(dailyDir, "brew-engine-"+today+".log"), []byte("today\n"), 0o644)

	MergeOldMonths(logDir)

	monthlyDir := filepath.Join(logDir, monthlySubDir)
	merged := filepath.Join(monthlyDir, "brew-engine-2025-12.log")
	data, err := os.ReadFile(merged)
	if err != nil {
		t.Fatalf("merged file not created: %v", err)
	}
	if !strings.Contains(string(data), "line1") || !strings.Contains(string(data), "line2") {
		t.Errorf("merged file missing expected content: %s", data)
	}

	// Old daily files should be deleted.
	if _, err := os.Stat(filepath.Join(dailyDir, "brew-engine-2025-12-30.log")); !os.IsNotExist(err) {
		t.Error("old daily file should have been deleted after merge")
	}

	// Today's file must be preserved.
	if _, err := os.Stat(filepath.Join(dailyDir, "brew-engine-"+today+".log")); os.IsNotExist(err) {
		t.Error("current-month file should NOT have been deleted")
	}
}

func TestMergeOldMonths_NoOldFiles_DoesNothing(t *testing.T) {
	logDir := t.TempDir()
	dailyDir := filepath.Join(logDir, dailySubDir)
	os.MkdirAll(dailyDir, 0o755)

	today := time.Now().Format("2006-01-02")
	os.WriteFile(filepath.Join(dailyDir, "brew-engine-"+today+".log"), []byte("now\n"), 0o644)

	MergeOldMonths(logDir)

	// Monthly dir should not be created when there's nothing to merge.
	if _, err := os.Stat(filepath.Join(logDir, monthlySubDir)); !os.IsNotExist(err) {
		t.Error("monthly dir should not be created when there is nothing to merge")
	}
}

func TestMergeOldMonths_EmptyDailyDir_IsNoOp(t *testing.T) {
	logDir := t.TempDir()
	os.MkdirAll(filepath.Join(logDir, dailySubDir), 0o755)
	MergeOldMonths(logDir) // must not panic
}

func TestMergeOldMonths_NoDailyDir_IsNoOp(t *testing.T) {
	logDir := t.TempDir()
	MergeOldMonths(logDir) // must not panic
}

// ─── mergeFiles ───────────────────────────────────────────────────────────────

func TestMergeFiles_ConcatenatesContent(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.log")
	b := filepath.Join(dir, "b.log")
	os.WriteFile(a, []byte("AAA\n"), 0o644)
	os.WriteFile(b, []byte("BBB\n"), 0o644)

	dest := filepath.Join(dir, "merged.log")
	if err := mergeFiles(dest, []string{a, b}); err != nil {
		t.Fatalf("mergeFiles: %v", err)
	}

	content, _ := os.ReadFile(dest)
	if !strings.Contains(string(content), "AAA") || !strings.Contains(string(content), "BBB") {
		t.Errorf("merged content missing expected data: %s", content)
	}
}

func TestMergeFiles_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.log")
	dest := filepath.Join(dir, "dest.log")
	os.WriteFile(src, []byte("NEW\n"), 0o644)
	os.WriteFile(dest, []byte("OLD\n"), 0o644)

	if err := mergeFiles(dest, []string{src}); err != nil {
		t.Fatalf("mergeFiles: %v", err)
	}

	content, _ := os.ReadFile(dest)
	if !strings.Contains(string(content), "OLD") || !strings.Contains(string(content), "NEW") {
		t.Errorf("expected both OLD and NEW in merged file: %s", content)
	}
}
