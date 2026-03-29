// Package cmd This file implements the `brew-engine list` subcommand.
//
// The list command serves the installed-package names from the file-based
// cache ([internal/cache.ListCachePath]) when the cache is warm, and falls
// back to running `brew list --formula` / `brew list --cask` on a cold miss.
// The result is always a single Type="list" [contract.Response] line whose
// Data field is a [contract.NamesList].
//
// Cache invalidation is handled externally by the `brew-engine watch`
// subcommand, which monitors the Homebrew Cellar and Caskroom directories
// via fsnotify and deletes list.json whenever an external mutation occurs.
package cmd

import (
	"os"

	"github.com/brewexplorer/brew-engine/internal/cache"
	"github.com/brewexplorer/brew-engine/internal/contract"
	"github.com/brewexplorer/brew-engine/internal/logger"
	"github.com/spf13/cobra"
)

// listCmd is the Cobra command for `brew-engine list`. It takes no positional
// arguments and emits exactly one JSON line on stdout: a Type="list"
// [contract.Response] whose Data field is a [contract.NamesList].
var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List names of all installed Homebrew formulae and casks",
	RunE:  runList,
}

func init() {
	rootCmd.AddCommand(listCmd)
}

// runList is the RunE handler for [listCmd].
//
// Cache hit: the raw bytes from list.json are written directly to stdout
// without any re-serialisation (zero-overhead path).
//
// Cache miss: [cache.BuildAndCacheList] runs `brew list`, builds and persists
// the [contract.NamesList] response, and returns the bytes to write.
//
// If brew itself fails, a Type="error" JSON event is written and the function
// returns nil so Cobra does not produce additional output.
func runList(_ *cobra.Command, _ []string) error {
	// ── Cache hit (infinite TTL, watcher-invalidated) ────────────────────────
	if cached, err := cache.ReadList(); err == nil {
		if logger.Sugar != nil {
			logger.Sugar.Debugw("list: cache hit")
		}
		_, _ = os.Stdout.Write(cached)
		return nil
	}

	// ── Cache miss: build and persist ────────────────────────────────────────
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
		logger.Sugar.Debugw("list: cache miss — rebuilt")
	}

	_, _ = os.Stdout.Write(data)
	return nil
}
