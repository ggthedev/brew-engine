// Package cmd contains all brew-engine subcommands.
package cmd

import (
	"os"
	"os/exec"

	"github.com/brewexplorer/brew-engine/internal/contract"
	"github.com/brewexplorer/brew-engine/internal/logger"
	"github.com/spf13/cobra"
)

var (
	nukeBrewCache bool
	nukeAppCache  bool
	nukeAll       bool
)

// execNukeCommand is the exec.Cmd factory used by runBrewCleanup.
// Tests override this to inject a fake brew binary without touching PATH.
var execNukeCommand = func(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

var nukeCmd = &cobra.Command{
	Use:   "nuke",
	Short: "Run brew cleanup and/or wipe the app cache",
	Long: `Nuke aggressively frees disk space used by Homebrew and/or brew-engine.

Flags control what is cleaned:
  --brew   Run brew cleanup -s --prune=all  (default when no flags are given)
  --app    Wipe the brew-engine app cache directory (list.json, info/)
  --all    Both --brew and --app

With no flags, --brew is assumed.`,
	RunE: runNuke,
}

func init() {
	nukeCmd.Flags().BoolVar(&nukeBrewCache, "brew", false, "run brew cleanup -s --prune=all")
	nukeCmd.Flags().BoolVar(&nukeAppCache, "app", false, "wipe the app cache directory")
	nukeCmd.Flags().BoolVar(&nukeAll, "all", false, "run both --brew and --app")
	rootCmd.AddCommand(nukeCmd)
}

func runNuke(_ *cobra.Command, _ []string) error {
	// Default to --brew when no flags are specified.
	doBrew := nukeBrewCache || nukeAll || (!nukeBrewCache && !nukeAppCache && !nukeAll)
	doApp := nukeAppCache || nukeAll

	result := contract.NukeData{}
	success := true

	if doBrew {
		exitCode, err := runBrewCleanup()
		if err != nil {
			contract.WriteJSON(os.Stdout, contract.Response{
				Success: false,
				Type:    "error",
				Error:   "failed to run brew cleanup: " + err.Error(),
			})
			return nil
		}
		result.BrewCacheNuked = true
		result.BrewExitCode = exitCode
		if exitCode != 0 {
			success = false
		}
		if logger.Sugar != nil {
			logger.Sugar.Infow("nuke: brew cleanup completed", "exit_code", exitCode)
		}
	}

	if doApp {
		cacheDir := resolveCacheDir()
		if err := cleanDir(cacheDir); err != nil {
			contract.WriteJSON(os.Stdout, contract.Response{
				Success: false,
				Type:    "error",
				Error:   "failed to wipe app cache: " + err.Error(),
			})
			return nil
		}
		result.AppCacheNuked = true
		result.AppCacheDir = cacheDir
		if logger.Sugar != nil {
			logger.Sugar.Infow("nuke: app cache wiped", "dir", cacheDir)
		}
	}

	contract.WriteJSON(os.Stdout, contract.Response{
		Success: success,
		Type:    "nuke",
		Data:    result,
	})
	return nil
}

// runBrewCleanup executes `brew cleanup -s --prune=all` and returns the exit
// code. Returns an error only if the process could not be started at all (e.g.
// binary not found); a non-zero exit from brew itself is returned as exitCode.
func runBrewCleanup() (int, error) {
	brewPath := os.Getenv("BREW_ENGINE_BREW_PATH")
	if brewPath == "" {
		brewPath = "brew"
	}
	cmd := execNukeCommand(brewPath, "cleanup", "-s", "--prune=all")
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}
