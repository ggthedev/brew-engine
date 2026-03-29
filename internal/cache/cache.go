// Package cache implements the brew-engine file-based caching layer.
//
// All cached artefacts are flat JSON files stored under a configurable
// directory (default: ~/.local/state/brew-engine/cache/). Two namespaces exist:
//
//   - list.json             — monolithic snapshot of installed package names.
//     TTL is infinite; invalidation is driven by the [StartWatcher] fsnotify
//     watcher that monitors the Homebrew Cellar and Caskroom directories.
//
//   - info/<pkg>.json       — per-package remote-state snapshot.
//     TTL is 24 hours, enforced by the stale-while-revalidate strategy in
//     cmd/info: a stale entry is returned immediately (flagged with
//     [contract.Response.IsStale]=true) while a fresh fetch runs in the
//     background.
//
// Every cache file stores a complete, newline-terminated [contract.Response]
// JSON object so that a cache hit can be written directly to stdout with
// zero re-parsing overhead.
package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/brewexplorer/brew-engine/internal/contract"
)

// ─── Constants ───────────────────────────────────────────────────────────────

const (
	// envCacheDir is the environment variable that overrides the default cache
	// directory. When set, the value is used verbatim (no tilde expansion).
	envCacheDir = "BREW_TUI_CACHE_DIR"

	// InfoTTL is the maximum age of a per-package info cache entry before it
	// is considered stale and eligible for background revalidation.
	InfoTTL = 24 * time.Hour

	// listFile is the filename for the monolithic installed-names cache.
	listFile = "list.json"

	// infoDirName is the subdirectory that holds per-package info caches.
	infoDirName = "info"
)

// ErrNotCached is returned by ReadList and ReadInfo when no cache file exists
// for the requested key.
var ErrNotCached = errors.New("cache: entry not found")

// ─── Injected dependencies (overridable in tests) ────────────────────────────

// userHomeDir is used to resolve the default cache directory.
// Tests override it to simulate a missing home directory.
var userHomeDir = os.UserHomeDir

// execCommand constructs exec.Cmd instances used by BuildAndCacheList.
// Tests override it to inject a fake brew binary without touching PATH.
var execCommand = func(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

// ─── Directory resolution ─────────────────────────────────────────────────────

// CacheDir resolves the cache root directory using the following priority:
//  1. BREW_TUI_CACHE_DIR environment variable (used verbatim).
//  2. ~/.local/state/brew-engine/cache/ (XDG-compliant default).
//  3. /tmp/brew-engine/cache/ (last-resort fallback when UserHomeDir fails).
func CacheDir() string {
	if d := os.Getenv(envCacheDir); d != "" {
		return d
	}
	home, err := userHomeDir()
	if err != nil {
		return filepath.Join("/tmp", "brew-engine", "cache")
	}
	return filepath.Join(home, ".local", "state", "brew-engine", "cache")
}

// ListCachePath returns the absolute path to the list.json cache file.
func ListCachePath() string {
	return filepath.Join(CacheDir(), listFile)
}

// InfoCachePath returns the absolute path to the per-package info cache file.
func InfoCachePath(pkg string) string {
	return filepath.Join(CacheDir(), infoDirName, pkg+".json")
}

// ensureParentDir creates all directories leading to filePath if they do not
// already exist.
func ensureParentDir(filePath string) error {
	return os.MkdirAll(filepath.Dir(filePath), 0o755)
}

// ─── list.json ────────────────────────────────────────────────────────────────

// WriteList atomically writes data to the list.json cache file, creating the
// cache directory if required.
func WriteList(data []byte) error {
	p := ListCachePath()
	if err := ensureParentDir(p); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// ReadList returns the raw bytes stored in list.json.
// Returns [ErrNotCached] when the file does not exist.
func ReadList() ([]byte, error) {
	data, err := os.ReadFile(ListCachePath())
	if os.IsNotExist(err) {
		return nil, ErrNotCached
	}
	return data, err
}

// InvalidateList removes list.json. It is a no-op when the file is absent.
func InvalidateList() error {
	err := os.Remove(ListCachePath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ─── info/<pkg>.json ──────────────────────────────────────────────────────────

// WriteInfo writes data to cache/info/<pkg>.json, creating the directory tree
// if required.
func WriteInfo(pkg string, data []byte) error {
	p := InfoCachePath(pkg)
	if err := ensureParentDir(p); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// ReadInfo reads the cached info JSON for pkg.
//
// Return values:
//   - (data, false, nil)          — cache hit, entry is fresh (< [InfoTTL] old).
//   - (data, true,  nil)          — cache hit, entry is stale (≥ [InfoTTL] old).
//   - (nil,  false, [ErrNotCached]) — no cache file exists.
//   - (nil,  false, err)          — unexpected I/O error.
func ReadInfo(pkg string) (data []byte, isStale bool, err error) {
	p := InfoCachePath(pkg)
	info, statErr := os.Stat(p)
	if os.IsNotExist(statErr) {
		return nil, false, ErrNotCached
	}
	if statErr != nil {
		return nil, false, statErr
	}
	isStale = time.Since(info.ModTime()) > InfoTTL
	data, err = os.ReadFile(p)
	return data, isStale, err
}

// InvalidateInfo removes the per-package info cache entry. It is a no-op when
// the file is absent.
func InvalidateInfo(pkg string) error {
	err := os.Remove(InfoCachePath(pkg))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ─── List rebuild ─────────────────────────────────────────────────────────────

// BuildAndCacheList runs `brew list --formula` and `brew list --cask`,
// assembles a [contract.NamesList], marshals it as a newline-terminated
// [contract.Response] JSON line, writes the result to list.json, and returns
// the raw bytes.
//
// The returned bytes are ready to be written directly to stdout without any
// further serialisation. The write to list.json is best-effort: if it fails,
// BuildAndCacheList still returns the bytes so the caller can serve the
// response without caching.
func BuildAndCacheList() ([]byte, error) {
	formulae, err := fetchNames("--formula")
	if err != nil {
		return nil, fmt.Errorf("brew list --formula: %w", err)
	}
	casks, err := fetchNames("--cask")
	if err != nil {
		return nil, fmt.Errorf("brew list --cask: %w", err)
	}

	resp := contract.Response{
		Success: true,
		Type:    "list",
		Data: contract.NamesList{
			Formulae: formulae,
			Casks:    casks,
			Total:    len(formulae) + len(casks),
		},
	}

	b, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	b = append(b, '\n')

	// Best-effort cache write — do not propagate this error to the caller.
	_ = WriteList(b)

	return b, nil
}

// fetchNames runs `brew list <flag>` and returns the whitespace-separated
// package names as a string slice. flag must be "--formula" or "--cask".
func fetchNames(flag string) ([]string, error) {
	out, err := execCommand("brew", "list", flag).Output()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return []string{}, nil
	}
	return strings.Fields(raw), nil
}
