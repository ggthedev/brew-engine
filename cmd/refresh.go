// Package cmd This file implements the `brew-engine refresh` subcommand.
//
// The refresh command forces a cache rebuild by invalidating the existing
// list.json cache and fetching fresh data from `brew list`. This is useful
// when the watcher is not running or when the user wants to manually ensure
// the cache is up-to-date.
package cmd

import (
	"os"

	"github.com/brewexplorer/brew-engine/internal/cache"
	"github.com/brewexplorer/brew-engine/internal/contract"
	"github.com/brewexplorer/brew-engine/internal/logger"
	"github.com/spf13/cobra"
)

// refreshCmd is the Cobra command for `brew-engine refresh`.
// It takes no positional arguments and emits exactly one JSON line on stdout.
var refreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Invalidate and rebuild the list cache",
	Long: `Refresh forces a full rebuild of the list cache by:
  1. Deleting the existing list.json cache file (if any)
  2. Running brew list --formula and brew list --cask
  3. Writing the fresh result to the cache
  4. Emitting the result as a Type="list" JSON response

This is useful when the watch subcommand is not running or when you want
to manually ensure the cache reflects the current Homebrew state.`,
	RunE: runRefresh,
}

func init() {
	rootCmd.AddCommand(refreshCmd)
}

// runRefresh is the RunE handler for [refreshCmd].
func runRefresh(_ *cobra.Command, _ []string) error {
	// Invalidate existing cache (ignore error if file doesn't exist)
	_ = cache.InvalidateList()

	if logger.Sugar != nil {
		logger.Sugar.Infow("refresh: invalidated list cache")
	}

	// Rebuild from scratch
	data, err := cache.BuildAndCacheList()
	if err != nil {
		contract.WriteJSON(os.Stdout, contract.Response{
			Success: false,
			Type:    "error",
			Error:   err.Error(),
		})
		return nil
	}

	if logger.Sugar != nil {
		logger.Sugar.Infow("refresh: cache rebuilt")
	}

	_, _ = os.Stdout.Write(data)
	return nil
}
