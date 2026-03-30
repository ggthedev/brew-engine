// Package config loads brew-engine configuration from three sources in
// ascending priority order:
//
//  1. Compiled defaults (internal/config/defaults.go)
//  2. macOS plist file  (~/Library/Preferences/com.mobilityquarks.brewexplorer.plist)
//  3. Environment variables (already set in the process)
//
// Load() must be called once at engine startup, before logger.Init() and
// cmd.Execute(). After Load() returns, all configuration is available as
// standard environment variables — no other package needs to import this one.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ─── Injected dependencies (overridable in tests) ────────────────────────────

// userHomeDir is used to resolve home-relative paths.
// Tests override this to avoid touching the real home directory.
var userHomeDir = os.UserHomeDir

// execPlutil runs plutil and returns its stdout output.
// Tests override this to inject fake plist data without touching the filesystem.
var execPlutil = func(args ...string) ([]byte, error) {
	return exec.Command("/usr/bin/plutil", args...).Output()
}

// ─── Public API ───────────────────────────────────────────────────────────────

// Load resolves configuration from plist + compiled defaults and exports each
// value as an environment variable. Env vars that are already set by the
// caller are never overwritten (env vars win over plist and defaults).
//
// If the plist file does not exist or plutil is unavailable, Load falls back
// silently to compiled defaults. Only unexpected I/O or parse errors are
// returned.
func Load() error {
	home, err := userHomeDir()
	if err != nil {
		return fmt.Errorf("config: cannot resolve home directory: %w", err)
	}

	plistPath := plistFilePath(home)
	values, err := loadPlist(plistPath)
	if err != nil {
		return err
	}

	apply(home, values)
	return nil
}

// PlistPath returns the absolute path to the application plist file.
// Useful for logging and diagnostics.
func PlistPath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return plistFilePath(home), nil
}

// ResolvedBrewPath returns the Homebrew executable path that was resolved
// during Load(). An empty string means brew was not found at any candidate
// location; the engine should not proceed in that case.
func ResolvedBrewPath() string {
	return os.Getenv(EnvBrewPath)
}

// IsBrewExecutable reports whether path is a regular executable file.
// Callers use this to verify that a pre-set BREW_ENGINE_BREW_PATH env var
// actually points to a usable binary.
func IsBrewExecutable(path string) bool {
	return isExecutable(path)
}

// BrewCandidatePaths returns the well-known Homebrew install locations that
// are checked during Load(). Included in the brew_not_found JSON payload so
// the frontend can show a helpful diagnostic without guessing.
func BrewCandidatePaths() []string {
	return []string{
		"/opt/homebrew/bin/brew",
		"/usr/local/bin/brew",
	}
}

// ─── Internal ─────────────────────────────────────────────────────────────────

// plistFilePath returns ~/Library/Preferences/com.mobilityquarks.brewexplorer.plist
func plistFilePath(home string) string {
	return filepath.Join(home, "Library", "Preferences", PlistBundleID+".plist")
}

// loadPlist reads the plist at path and returns its top-level string keys as a
// map. If the file does not exist, an empty map is returned (not an error).
// plutil converts the plist to JSON so we can parse it with encoding/json —
// no external Go dependency required.
func loadPlist(path string) (map[string]string, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return map[string]string{}, nil
	}

	out, err := execPlutil("-convert", "json", "-o", "-", path)
	if err != nil {
		return nil, fmt.Errorf("config: plutil failed for %s: %w", path, err)
	}

	var raw map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("config: cannot parse plist JSON from %s: %w", path, err)
	}

	result := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	return result, nil
}

// apply sets each env var from the resolved value chain:
//
//	env var (already set) > plist value > compiled default
//
// It never overwrites an env var that is already present in the process.
func apply(home string, plist map[string]string) {
	setIfAbsent(EnvLogDir, resolve(KeyLogDir, plist, defaultLogDir(home)))
	setIfAbsent(EnvLogFileName, resolve(KeyLogFileName, plist, DefaultLogFileName))
	setIfAbsent(EnvBrewOutputLogFileName, resolve(KeyBrewOutputLogFileName, plist, DefaultBrewOutputLogFileName))
	setIfAbsent(EnvLogLevel, resolve(KeyLogLevel, plist, DefaultLogLevel))
	setIfAbsent(EnvCacheDir, resolve(KeyCacheDir, plist, defaultCacheDir(home)))
	setIfAbsent(EnvListCacheFileName, resolve(KeyListCacheFileName, plist, DefaultListCacheFileName))
	setIfAbsent(EnvInfoCacheDirName, resolve(KeyInfoCacheDirName, plist, DefaultInfoCacheDirName))

	if brewPath := resolveBrewPath(plist); brewPath != "" {
		setIfAbsent(EnvBrewPath, brewPath)
	}
}

// resolve returns the plist value for key if present, otherwise the fallback.
func resolve(key string, plist map[string]string, fallback string) string {
	if v, ok := plist[key]; ok && v != "" {
		return v
	}
	return fallback
}

// setIfAbsent sets an env var only when it is not already set in the process.
// This preserves the highest-priority override (explicit env var from caller).
func setIfAbsent(key, value string) {
	if os.Getenv(key) == "" {
		_ = os.Setenv(key, value)
	}
}

// resolveBrewPath finds the brew binary using a 4-step priority chain:
//  1. Plist BrewPath key
//  2. /opt/homebrew/bin/brew  (Apple Silicon default)
//  3. /usr/local/bin/brew     (Intel default)
//  4. exec.LookPath("brew")   (current PATH fallback)
//
// Returns empty string if brew cannot be located.
func resolveBrewPath(plist map[string]string) string {
	if v, ok := plist[KeyBrewPath]; ok && v != "" {
		if isExecutable(v) {
			return v
		}
	}

	candidates := []string{
		"/opt/homebrew/bin/brew",
		"/usr/local/bin/brew",
	}
	for _, p := range candidates {
		if isExecutable(p) {
			return p
		}
	}

	if p, err := exec.LookPath("brew"); err == nil {
		return p
	}
	return ""
}

// isExecutable returns true when the path exists and is a regular executable file.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir() && info.Mode()&0o111 != 0
}
