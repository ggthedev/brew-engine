// Package config provides compiled-in default values for all brew-engine
// configuration keys. These are the lowest-priority values in the chain:
//
//	compiled defaults < plist < env vars
package config

import "path/filepath"

// ─── Plist identity ───────────────────────────────────────────────────────────

const (
	// PlistBundleID is the reverse-DNS identifier for the application plist.
	// The plist file lives at:
	//   ~/Library/Preferences/<PlistBundleID>.plist
	PlistBundleID = "com.mobilityquarks.brewexplorer"

	// AppSupportDirName is the name of the app's directory inside
	// ~/Library/Application Support/.
	AppSupportDirName = "BrewExplorer"
)

// ─── Plist keys ───────────────────────────────────────────────────────────────

const (
	KeyLogDir                 = "LogDir"
	KeyLogFileName            = "LogFileName"
	KeyBrewOutputLogFileName  = "BrewOutputLogFileName"
	KeyLogLevel               = "LogLevel"
	KeyCacheDir               = "CacheDir"
	KeyListCacheFileName      = "ListCacheFileName"
	KeyInfoCacheDirName       = "InfoCacheDirName"
	KeyBrewPath               = "BrewPath"
)

// ─── Env var names ────────────────────────────────────────────────────────────

const (
	EnvLogDir                = "BREW_ENGINE_LOG_DIR"
	EnvLogFileName           = "BREW_ENGINE_LOG_FILE_NAME"
	EnvBrewOutputLogFileName = "BREW_ENGINE_BREW_OUTPUT_LOG_FILE_NAME"
	EnvLogLevel              = "BREW_ENGINE_LOG_LEVEL"
	EnvCacheDir              = "BREW_TUI_CACHE_DIR"
	EnvListCacheFileName     = "BREW_ENGINE_LIST_CACHE_FILE_NAME"
	EnvInfoCacheDirName      = "BREW_ENGINE_INFO_CACHE_DIR_NAME"
	EnvBrewPath              = "BREW_ENGINE_BREW_PATH"
)

// ─── Compiled defaults ────────────────────────────────────────────────────────

const (
	DefaultLogFileName           = "brew-engine.log"
	DefaultBrewOutputLogFileName = "brew-output.log"
	DefaultLogLevel              = "info"
	DefaultListCacheFileName     = "list.json"
	DefaultInfoCacheDirName      = "info"
)

// defaultLogDir returns the default log directory using the provided home path.
// Result: ~/Library/Application Support/BrewExplorer/logs
func defaultLogDir(home string) string {
	return filepath.Join(home, "Library", "Application Support", AppSupportDirName, "logs")
}

// defaultCacheDir returns the default cache directory using the provided home path.
// Result: ~/Library/Application Support/BrewExplorer/cache
func defaultCacheDir(home string) string {
	return filepath.Join(home, "Library", "Application Support", AppSupportDirName, "cache")
}
