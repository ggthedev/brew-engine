// Package cmd contains all brew-engine subcommands.
package cmd

import (
	"os"
	"path/filepath"

	"github.com/brewexplorer/brew-engine/internal/contract"
	"github.com/brewexplorer/brew-engine/internal/logger"
	"github.com/spf13/cobra"
)

var (
	cleanLogs  bool
	cleanCache bool
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Delete log and/or cache files on demand",
	Long: `Clean removes brew-engine data directories on demand.

Flags control which directories are wiped:
  --logs   Remove all files under <LogDir>  (daily/, monthly/, brew-output.log)
  --cache  Remove all files under <CacheDir> (list.json, info/)

At least one of --logs or --cache must be provided.
The directories themselves are preserved; only their contents are deleted.`,
	RunE: runClean,
}

func init() {
	cleanCmd.Flags().BoolVar(&cleanLogs, "logs", false, "delete all log files")
	cleanCmd.Flags().BoolVar(&cleanCache, "cache", false, "delete all cache files")
	rootCmd.AddCommand(cleanCmd)
}

// cleanResult carries per-target outcome for the JSON response.
type cleanResult struct {
	LogsCleared  bool   `json:"logs_cleared,omitempty"`
	CacheCleared bool   `json:"cache_cleared,omitempty"`
	LogDir       string `json:"log_dir,omitempty"`
	CacheDir     string `json:"cache_dir,omitempty"`
}

func runClean(_ *cobra.Command, _ []string) error {
	if !cleanLogs && !cleanCache {
		contract.WriteJSON(os.Stdout, contract.Response{
			Success: false,
			Type:    "error",
			Error:   "specify at least one of --logs or --cache",
		})
		return nil
	}

	result := cleanResult{}

	if cleanLogs {
		logDir := logger.LogDir()
		if err := cleanDir(logDir); err != nil {
			contract.WriteJSON(os.Stdout, contract.Response{
				Success: false,
				Type:    "error",
				Error:   "failed to clean log directory: " + err.Error(),
			})
			return nil
		}
		result.LogsCleared = true
		result.LogDir = logDir
		if logger.Sugar != nil {
			logger.Sugar.Infow("clean: log directory wiped", "dir", logDir)
		}
	}

	if cleanCache {
		cacheDir := resolveCacheDir()
		if err := cleanDir(cacheDir); err != nil {
			contract.WriteJSON(os.Stdout, contract.Response{
				Success: false,
				Type:    "error",
				Error:   "failed to clean cache directory: " + err.Error(),
			})
			return nil
		}
		result.CacheCleared = true
		result.CacheDir = cacheDir
		if logger.Sugar != nil {
			logger.Sugar.Infow("clean: cache directory wiped", "dir", cacheDir)
		}
	}

	contract.WriteJSON(os.Stdout, contract.Response{
		Success: true,
		Type:    "clean",
		Data:    result,
	})
	return nil
}

// cleanDir removes all direct children of dir (files and subdirectories)
// without removing dir itself. Returns the first error encountered.
func cleanDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// resolveCacheDir returns the cache directory from the env var set by
// internal/config, falling back to the legacy default.
func resolveCacheDir() string {
	if d := os.Getenv("BREW_TUI_CACHE_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "BrewExplorer", "cache")
}
