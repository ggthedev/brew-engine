package cache

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── CacheDir resolution ───────────────────────────────────────────────────────

func TestCacheDir_EnvVar_Wins(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envCacheDir, dir)
	if got := CacheDir(); got != dir {
		t.Errorf("expected %s, got %s", dir, got)
	}
}

func TestCacheDir_DefaultUnderHome(t *testing.T) {
	t.Setenv(envCacheDir, "")
	got := CacheDir()
	if !strings.Contains(got, "brew-engine") {
		t.Errorf("expected brew-engine in default path, got %s", got)
	}
	if !strings.Contains(got, "cache") {
		t.Errorf("expected cache in default path, got %s", got)
	}
}

func TestCacheDir_TmpFallback_WhenUserHomeDirFails(t *testing.T) {
	t.Setenv(envCacheDir, "")
	origFn := userHomeDir
	t.Cleanup(func() { userHomeDir = origFn })
	userHomeDir = func() (string, error) { return "", errors.New("no home") }

	got := CacheDir()
	if got != filepath.Join("/tmp", "brew-engine", "cache") {
		t.Errorf("expected /tmp/brew-engine/cache, got %s", got)
	}
}

func TestListCachePath_ContainsListJson(t *testing.T) {
	t.Setenv(envCacheDir, "/some/dir")
	if got := ListCachePath(); got != "/some/dir/list.json" {
		t.Errorf("unexpected path: %s", got)
	}
}

func TestInfoCachePath_ContainsPkgJson(t *testing.T) {
	t.Setenv(envCacheDir, "/some/dir")
	if got := InfoCachePath("wget"); got != "/some/dir/info/wget.json" {
		t.Errorf("unexpected path: %s", got)
	}
}

// ── list.json read/write/invalidate ──────────────────────────────────────────

func TestWriteReadList_RoundTrip(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	data := []byte(`{"success":true,"type":"list"}` + "\n")
	if err := WriteList(data); err != nil {
		t.Fatalf("WriteList: %v", err)
	}
	got, err := ReadList()
	if err != nil {
		t.Fatalf("ReadList: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("round-trip mismatch: got %q, want %q", got, data)
	}
}

func TestReadList_ErrNotCached_WhenMissing(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	_, err := ReadList()
	if !errors.Is(err, ErrNotCached) {
		t.Errorf("expected ErrNotCached, got %v", err)
	}
}

func TestInvalidateList_RemovesFile(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	if err := WriteList([]byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if err := InvalidateList(); err != nil {
		t.Fatalf("InvalidateList: %v", err)
	}
	_, err := ReadList()
	if !errors.Is(err, ErrNotCached) {
		t.Errorf("expected ErrNotCached after invalidation, got %v", err)
	}
}

func TestInvalidateList_NoOp_WhenMissing(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	if err := InvalidateList(); err != nil {
		t.Errorf("InvalidateList on missing file must be a no-op, got %v", err)
	}
}

func TestWriteList_EnsureParentDir_Error(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission tests do not apply when running as root")
	}
	base := t.TempDir()
	if err := os.Chmod(base, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(base, 0o755) })
	t.Setenv(envCacheDir, filepath.Join(base, "newsubdir"))

	if err := WriteList([]byte("x")); err == nil {
		t.Error("expected error when parent directory is not writable")
	}
}

// ── info/<pkg>.json read/write/invalidate ────────────────────────────────────

func TestWriteReadInfo_RoundTrip(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	data := []byte(`{"success":true,"type":"info"}` + "\n")
	if err := WriteInfo("wget", data); err != nil {
		t.Fatalf("WriteInfo: %v", err)
	}
	got, stale, err := ReadInfo("wget")
	if err != nil {
		t.Fatalf("ReadInfo: %v", err)
	}
	if stale {
		t.Error("freshly written entry must not be stale")
	}
	if string(got) != string(data) {
		t.Errorf("round-trip mismatch: got %q, want %q", got, data)
	}
}

func TestReadInfo_ErrNotCached_WhenMissing(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	_, _, err := ReadInfo("nonexistent")
	if !errors.Is(err, ErrNotCached) {
		t.Errorf("expected ErrNotCached, got %v", err)
	}
}

func TestReadInfo_StaleFlag_WhenOlderThanTTL(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envCacheDir, dir)

	if err := WriteInfo("wget", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	p := InfoCachePath("wget")
	past := time.Now().Add(-(InfoTTL + time.Minute))
	if err := os.Chtimes(p, past, past); err != nil {
		t.Fatal(err)
	}

	_, stale, err := ReadInfo("wget")
	if err != nil {
		t.Fatalf("ReadInfo: %v", err)
	}
	if !stale {
		t.Error("entry older than InfoTTL must be flagged stale")
	}
}

func TestReadInfo_NotStale_WhenFresh(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	if err := WriteInfo("wget", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	_, stale, err := ReadInfo("wget")
	if err != nil || stale {
		t.Errorf("fresh entry must not be stale: stale=%v err=%v", stale, err)
	}
}

func TestInvalidateInfo_RemovesFile(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	if err := WriteInfo("wget", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if err := InvalidateInfo("wget"); err != nil {
		t.Fatalf("InvalidateInfo: %v", err)
	}
	_, _, err := ReadInfo("wget")
	if !errors.Is(err, ErrNotCached) {
		t.Errorf("expected ErrNotCached after invalidation, got %v", err)
	}
}

func TestInvalidateInfo_NoOp_WhenMissing(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	if err := InvalidateInfo("ghost"); err != nil {
		t.Errorf("InvalidateInfo on missing file must be a no-op, got %v", err)
	}
}

func TestWriteInfo_EnsureParentDir_Error(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission tests do not apply when running as root")
	}
	base := t.TempDir()
	if err := os.Chmod(base, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(base, 0o755) })
	t.Setenv(envCacheDir, filepath.Join(base, "newsubdir"))

	if err := WriteInfo("wget", []byte("x")); err == nil {
		t.Error("expected error when parent directory is not writable")
	}
}

// ── BuildAndCacheList ─────────────────────────────────────────────────────────

func TestBuildAndCacheList_Success_ReturnsJSON(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = fakeBrew(t, "#!/bin/sh\nif [ \"$2\" = \"--formula\" ]; then printf 'git\\nwget\\n'; else printf 'firefox\\n'; fi\nexit 0\n")

	data, err := BuildAndCacheList()
	if err != nil {
		t.Fatalf("BuildAndCacheList: %v", err)
	}
	if !strings.Contains(string(data), `"type":"list"`) {
		t.Errorf("expected list response JSON, got: %s", data)
	}
}

func TestBuildAndCacheList_WritesListJson(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(envCacheDir, dir)
	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = fakeBrew(t, "#!/bin/sh\nprintf ''\nexit 0\n")

	if _, err := BuildAndCacheList(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "list.json")); os.IsNotExist(err) {
		t.Error("expected list.json to be written")
	}
}

func TestBuildAndCacheList_FormulaError_ReturnsError(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = fakeBrew(t, "#!/bin/sh\nexit 1\n")

	_, err := BuildAndCacheList()
	if err == nil {
		t.Error("expected error when brew exits non-zero")
	}
}

func TestBuildAndCacheList_CaskError_ReturnsError(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = fakeBrew(t, "#!/bin/sh\nif [ \"$2\" = \"--formula\" ]; then printf 'git\\n'; exit 0; fi\nexit 1\n")

	_, err := BuildAndCacheList()
	if err == nil {
		t.Error("expected error when brew --cask exits non-zero")
	}
}

func TestBuildAndCacheList_Empty_TotalIsZero(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = fakeBrew(t, "#!/bin/sh\nprintf ''\nexit 0\n")

	data, err := BuildAndCacheList()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"total":0`) {
		t.Errorf("expected total:0 in response, got: %s", data)
	}
}

func TestBuildAndCacheList_SpaceSeparated_ParsedCorrectly(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = fakeBrew(t, "#!/bin/sh\nif [ \"$2\" = \"--formula\" ]; then printf 'git wget zsh'; exit 0; fi\nprintf ''\nexit 0\n")

	data, err := BuildAndCacheList()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"total":3`) {
		t.Errorf("expected total:3, got: %s", data)
	}
}

func TestBuildAndCacheList_NewlineTerminated(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	orig := execCommand
	t.Cleanup(func() { execCommand = orig })
	execCommand = fakeBrew(t, "#!/bin/sh\nprintf ''\nexit 0\n")

	data, err := BuildAndCacheList()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Error("BuildAndCacheList result must be newline-terminated")
	}
}

// ── execCommand default body ──────────────────────────────────────────────────

func TestExecCommand_Default_ReturnsNonNilCmd(t *testing.T) {
	cmd := execCommand("echo", "hello")
	if cmd == nil {
		t.Error("expected non-nil *exec.Cmd from default execCommand")
	}
}

// ── ReadInfo non-ErrNotCached stat error ─────────────────────────────────────

func TestReadInfo_StatError_NonErrNotExist(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission tests do not apply when running as root")
	}
	dir := t.TempDir()
	t.Setenv(envCacheDir, dir)

	if err := WriteInfo("wget", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	// Remove execute permission from the info directory so os.Stat on the
	// file inside it returns EACCES (not ENOENT).
	infoDir := filepath.Join(dir, "info")
	if err := os.Chmod(infoDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(infoDir, 0o755) })

	_, _, err := ReadInfo("wget")
	if err == nil || errors.Is(err, ErrNotCached) {
		t.Errorf("expected a non-ErrNotCached error from os.Stat, got %v", err)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// fakeBrew writes a shell script as a temp executable and returns a drop-in
// replacement for [execCommand] that runs it when "brew" is requested.
// The script receives the same argument list as the real brew invocation
// (e.g. "list", "--formula"), available as $1, $2, etc.
func fakeBrew(t *testing.T, script string) func(string, ...string) *exec.Cmd {
	t.Helper()
	brewPath := filepath.Join(t.TempDir(), "brew")
	if err := os.WriteFile(brewPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return func(name string, args ...string) *exec.Cmd {
		if name == "brew" {
			return exec.Command(brewPath, args...)
		}
		return exec.Command(name, args...)
	}
}
