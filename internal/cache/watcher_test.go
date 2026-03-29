package cache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/brewexplorer/brew-engine/internal/contract"
	"github.com/brewexplorer/brew-engine/internal/logger"
	"github.com/fsnotify/fsnotify"
)

// ── StartWatcher ──────────────────────────────────────────────────────────────

func TestStartWatcher_NewWatcherError_ReturnsError(t *testing.T) {
	orig := newFSWatcher
	t.Cleanup(func() { newFSWatcher = orig })
	newFSWatcher = func() (*fsnotify.Watcher, error) {
		return nil, errors.New("fake watcher creation failure")
	}

	err := StartWatcher(context.Background(), &bytes.Buffer{})
	if err == nil {
		t.Error("expected error when fsnotify.NewWatcher fails")
	}
}

func TestStartWatcher_NoValidDirs_ReturnsError(t *testing.T) {
	origDirs := BrewWatchDirs
	t.Cleanup(func() { BrewWatchDirs = origDirs })
	// Point at a non-existent path so every Add call fails.
	BrewWatchDirs = []string{"/nonexistent/path/that/does/not/exist/at/all"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := StartWatcher(ctx, &bytes.Buffer{})
	if err == nil {
		t.Error("expected error when no watch directories are available")
	}
}

func TestStartWatcher_ValidDir_StartsWithoutError(t *testing.T) {
	watchDir := t.TempDir()
	origDirs := BrewWatchDirs
	t.Cleanup(func() { BrewWatchDirs = origDirs })
	BrewWatchDirs = []string{watchDir}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := StartWatcher(ctx, &bytes.Buffer{}); err != nil {
		t.Fatalf("StartWatcher with valid dir must not return error: %v", err)
	}
}

func TestStartWatcher_Cancel_GoRoutineExits(t *testing.T) {
	watchDir := t.TempDir()
	origDirs := BrewWatchDirs
	t.Cleanup(func() { BrewWatchDirs = origDirs })
	BrewWatchDirs = []string{watchDir}

	ctx, cancel := context.WithCancel(context.Background())

	var buf bytes.Buffer
	if err := StartWatcher(ctx, &buf); err != nil {
		t.Fatal(err)
	}

	// Cancel should cleanly stop the goroutine (no hang).
	cancel()
	// Give the goroutine a moment to drain.
	time.Sleep(50 * time.Millisecond)
}

// ── rebuildListCache ──────────────────────────────────────────────────────────

func TestRebuildListCache_Success_EmitsCacheRebuiltEvent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envCacheDir, dir)

	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = fakeBrew(t, "#!/bin/sh\nprintf ''\nexit 0\n")

	var buf bytes.Buffer
	rebuildListCache(&buf)

	if buf.Len() == 0 {
		t.Fatal("expected JSON event on stdout, got nothing")
	}

	var r contract.Response
	if err := json.Unmarshal(bytes.TrimRight(buf.Bytes(), "\n"), &r); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, buf.String())
	}
	if !r.Success || r.Type != "event" {
		t.Errorf("expected success event response, got success=%v type=%s", r.Success, r.Type)
	}

	eventBytes, _ := json.Marshal(r.Data)
	var ev contract.CacheEvent
	if err := json.Unmarshal(eventBytes, &ev); err != nil {
		t.Fatalf("Data is not a CacheEvent: %v", err)
	}
	if ev.Action != "cache_rebuilt" || ev.Target != "list" {
		t.Errorf("unexpected CacheEvent: %+v", ev)
	}
}

func TestRebuildListCache_WritesNewListJson(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envCacheDir, dir)

	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = fakeBrew(t, "#!/bin/sh\nif [ \"$2\" = \"--formula\" ]; then printf 'git\\n'; else printf ''; fi\nexit 0\n")

	var buf bytes.Buffer
	rebuildListCache(&buf)

	if _, err := os.Stat(filepath.Join(dir, "list.json")); os.IsNotExist(err) {
		t.Error("expected list.json to be written after rebuild")
	}
}

func TestRebuildListCache_BrewFails_EmitsNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envCacheDir, dir)

	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = fakeBrew(t, "#!/bin/sh\nexit 1\n")

	var buf bytes.Buffer
	rebuildListCache(&buf)

	if buf.Len() != 0 {
		t.Errorf("expected no output when brew fails, got: %s", buf.String())
	}
}

// ── runWatcher debounce: direct invocation ────────────────────────────────────

func TestRunWatcher_Debounce_OnlyOneRebuild(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envCacheDir, dir)

	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = fakeBrew(t, "#!/bin/sh\nprintf ''\nexit 0\n")

	var buf bytes.Buffer
	rebuildListCache(&buf)

	lines := bytes.Split(bytes.TrimRight(buf.Bytes(), "\n"), []byte("\n"))
	if len(lines) != 1 {
		t.Errorf("expected exactly 1 rebuild event, got %d lines: %s", len(lines), buf.String())
	}
}

// TestRunWatcher_ChannelClose_Events_Returns verifies that runWatcher exits
// cleanly when the watcher's Events channel is closed (e.g. after w.Close()).
func TestRunWatcher_ChannelClose_Events_Returns(t *testing.T) {
	events := make(chan fsnotify.Event)
	errs := make(chan error)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		runWatcher(ctx, events, errs, func() error { return nil }, &bytes.Buffer{})
	}()

	// Closing events triggers the !ok return path.
	close(events)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("runWatcher did not exit after events channel was closed")
	}
}

func TestRunWatcher_ChannelClose_Errors_Returns(t *testing.T) {
	events := make(chan fsnotify.Event)
	errs := make(chan error)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		runWatcher(ctx, events, errs, func() error { return nil }, &bytes.Buffer{})
	}()

	// Closing errs (before events) triggers the errors !ok return path.
	close(errs)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("runWatcher did not exit after errors channel was closed")
	}
}

func TestRunWatcher_ErrorReceived_NoLogger(t *testing.T) {
	events := make(chan fsnotify.Event)
	errs := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		runWatcher(ctx, events, errs, func() error { return nil }, &bytes.Buffer{})
	}()

	// Send a watcher error — no logger, so just the branch condition runs.
	errs <- errors.New("fake watcher error")
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("runWatcher did not exit after context cancel")
	}
}

func TestRunWatcher_ErrorReceived_WithLogger(t *testing.T) {
	t.Setenv("BREW_TUI_LOG_DIR", t.TempDir())
	if err := logger.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Sync(); logger.Sugar = nil; logger.Raw = nil })

	events := make(chan fsnotify.Event)
	errs := make(chan error, 1)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		runWatcher(ctx, events, errs, func() error { return nil }, &bytes.Buffer{})
	}()

	// Send a watcher error with logger active — covers the Warnw branch.
	errs <- errors.New("fake watcher error")
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("runWatcher did not exit after context cancel")
	}
}

// TestRunWatcher_CtxCancel_WithActiveTimer_StopsTimer verifies the
// timer.Stop() branch: a filesystem event starts the debounce timer, then
// context cancellation fires before the debounce expires.
func TestRunWatcher_CtxCancel_WithActiveTimer_StopsTimer(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())

	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = fakeBrew(t, "#!/bin/sh\nprintf ''\nexit 0\n")

	events := make(chan fsnotify.Event, 1)
	errs := make(chan error)

	ctx, cancel := context.WithCancel(context.Background())

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		runWatcher(ctx, events, errs, func() error { return nil }, &buf)
	}()

	// Send a synthetic event to start the debounce timer.
	events <- fsnotify.Event{Name: "/fake/path", Op: fsnotify.Create}
	time.Sleep(20 * time.Millisecond)

	// Cancel before the 500 ms debounce fires — timer.Stop() runs.
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("runWatcher did not exit after context cancellation")
	}
}

// TestRunWatcher_TwoEvents_TimerReset_WithLogger writes two files in quick
// succession so that:
//   - the first event covers the logger.Debugw path (logger.Sugar != nil)
//   - the second event covers the timer-reset path (timer != nil → timer.Stop())
func TestRunWatcher_TwoEvents_TimerReset_WithLogger(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	t.Setenv("BREW_TUI_LOG_DIR", t.TempDir())
	if err := logger.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Sync(); logger.Sugar = nil; logger.Raw = nil })

	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = fakeBrew(t, "#!/bin/sh\nprintf ''\nexit 0\n")

	events := make(chan fsnotify.Event, 2)
	errs := make(chan error)

	ctx, cancel := context.WithCancel(context.Background())

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		runWatcher(ctx, events, errs, func() error { return nil }, &buf)
	}()

	// First event — starts debounce timer, covers logger.Debugw path.
	events <- fsnotify.Event{Name: "/fake/a", Op: fsnotify.Create}
	time.Sleep(20 * time.Millisecond)

	// Second event — timer != nil, so timer.Stop() + reset runs.
	events <- fsnotify.Event{Name: "/fake/b", Op: fsnotify.Write}
	time.Sleep(20 * time.Millisecond)

	// Cancel before the 500 ms debounce fires.
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("runWatcher did not exit after context cancellation")
	}
}

// TestRunWatcher_DebounceFiresAfterQuiet lets the debounce timer expire so the
// time.AfterFunc callback body (rebuildListCache) is executed inside runWatcher.
// It uses a signalWriter to detect the write race-free.
func TestRunWatcher_DebounceFiresAfterQuiet(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())

	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = fakeBrew(t, "#!/bin/sh\nprintf ''\nexit 0\n")

	events := make(chan fsnotify.Event, 1)
	errs := make(chan error)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sw := &signalWriter{signal: make(chan struct{}, 1)}
	go runWatcher(ctx, events, errs, func() error { return nil }, sw)

	events <- fsnotify.Event{Name: "/fake/pkg", Op: fsnotify.Create}

	// Block until rebuildListCache writes to sw (signals debounce callback ran).
	select {
	case <-sw.signal:
	case <-time.After(5 * time.Second):
		t.Error("debounce callback did not fire within 5 s timeout")
	}
}

// ── rebuildListCache — logger branches ───────────────────────────────────────

func TestRebuildListCache_Success_WithLogger(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	t.Setenv("BREW_TUI_LOG_DIR", t.TempDir())

	if err := logger.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Sync(); logger.Sugar = nil; logger.Raw = nil })

	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = fakeBrew(t, "#!/bin/sh\nprintf ''\nexit 0\n")

	var buf bytes.Buffer
	rebuildListCache(&buf)

	if buf.Len() == 0 {
		t.Error("expected a cache_rebuilt event with logger active")
	}
}

func TestRebuildListCache_InvalidateError_NoLogger(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission tests do not apply when running as root")
	}
	dir := t.TempDir()
	t.Setenv(envCacheDir, dir)

	// Write list.json, then make the cache dir read-only so Remove fails.
	if err := WriteList([]byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	var buf bytes.Buffer
	rebuildListCache(&buf) // should return early on InvalidateList error

	if buf.Len() != 0 {
		t.Errorf("expected no output when InvalidateList fails, got: %s", buf.String())
	}
}

func TestRebuildListCache_InvalidateError_WithLogger(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission tests do not apply when running as root")
	}
	dir := t.TempDir()
	t.Setenv(envCacheDir, dir)
	t.Setenv("BREW_TUI_LOG_DIR", t.TempDir())

	if err := logger.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Sync(); logger.Sugar = nil; logger.Raw = nil })

	if err := WriteList([]byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	var buf bytes.Buffer
	rebuildListCache(&buf)

	if buf.Len() != 0 {
		t.Errorf("expected no output when InvalidateList fails with logger, got: %s", buf.String())
	}
}

func TestRebuildListCache_BrewFail_WithLogger(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	t.Setenv("BREW_TUI_LOG_DIR", t.TempDir())

	if err := logger.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Sync(); logger.Sugar = nil; logger.Raw = nil })

	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = fakeBrew(t, "#!/bin/sh\nexit 1\n")

	var buf bytes.Buffer
	rebuildListCache(&buf)

	if buf.Len() != 0 {
		t.Errorf("expected no output when brew fails with logger active, got: %s", buf.String())
	}
}

// ── StartWatcher — partial dirs + logger ────────────────────────────────────

func TestStartWatcher_PartialDirs_LogsWarnAndStarts(t *testing.T) {
	validDir := t.TempDir()
	origDirs := BrewWatchDirs
	t.Cleanup(func() { BrewWatchDirs = origDirs })
	// One valid, one invalid — the invalid one should log a Warnw and be skipped.
	BrewWatchDirs = []string{validDir, "/nonexistent/path/that/does/not/exist"}

	t.Setenv("BREW_TUI_LOG_DIR", t.TempDir())
	if err := logger.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logger.Sync(); logger.Sugar = nil; logger.Raw = nil })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := StartWatcher(ctx, &bytes.Buffer{}); err != nil {
		t.Fatalf("StartWatcher with one valid dir must not return error: %v", err)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// signalWriter is a thread-safe io.Writer that closes its signal channel the
// first time any data is written to it. Tests use it to detect when a
// goroutine (e.g. a time.AfterFunc callback) has written output without
// relying on fixed sleep durations.
type signalWriter struct {
	mu     sync.Mutex
	once   sync.Once
	signal chan struct{}
}

func (w *signalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.once.Do(func() { close(w.signal) })
	return len(p), nil
}
