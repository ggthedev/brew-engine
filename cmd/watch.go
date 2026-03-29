// Package cmd This file implements the `brew-engine watch` subcommand.
//
// The watch command registers fsnotify listeners on the Homebrew Cellar,
// Caskroom, and locks directories. When an external mutation is detected
// (e.g. the user runs `brew install` in a separate terminal), the engine
// debounces the events, invalidates list.json, rebuilds the cache via
// `brew list`, and emits a Type="event" JSON line to stdout so that
// connected frontends know to refresh their installed-packages view.
//
// The command blocks until it receives SIGINT or SIGTERM.
package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/brewexplorer/brew-engine/internal/cache"
	"github.com/brewexplorer/brew-engine/internal/contract"
	"github.com/spf13/cobra"
)

// watchCmd is the Cobra command for `brew-engine watch`. It takes no
// positional arguments and runs until the process receives SIGINT or SIGTERM.
var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Watch Homebrew directories and emit cache-rebuilt events",
	Long: `watch registers fsnotify listeners on the Homebrew Cellar, Caskroom,
and locks directories. When an external mutation is detected the engine
invalidates list.json, rebuilds it in the background, and emits:

  {"success":true,"type":"event","data":{"action":"cache_rebuilt","target":"list"}}

The command blocks until SIGINT or SIGTERM is received.`,
	RunE: runWatch,
}

func init() {
	rootCmd.AddCommand(watchCmd)
}

// runWatch is the RunE handler for [watchCmd]. It starts the fsnotify watcher
// and blocks until the process receives an interrupt signal.
func runWatch(_ *cobra.Command, _ []string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := cache.StartWatcher(ctx, os.Stdout); err != nil {
		contract.WriteJSON(os.Stdout, contract.Response{
			Success: false,
			Type:    "error",
			Error:   err.Error(),
		})
		return nil
	}

	<-ctx.Done()
	return nil
}
