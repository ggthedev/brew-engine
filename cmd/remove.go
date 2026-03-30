// Package cmd This file implements the `brew-engine remove` subcommand.
//
// The remove command delegates entirely to [parser.RunRemove], which
// executes `brew uninstall <package>` and translates its streaming output
// into newline-delimited JSON events on stdout:
//
//   - One Type="progress" event per "==>" phase header encountered.
//   - A terminal Type="done" event on success (exit code 0).
//   - A terminal Type="error" event on failure (non-zero exit code).
//
// Like install, this command is synchronous: it blocks until the brew
// subprocess exits, relying on Homebrew's system-wide lock to serialise
// concurrent mutation attempts.
package cmd

import (
	"os"

	"github.com/brewexplorer/brew-engine/internal/cache"
	"github.com/brewexplorer/brew-engine/internal/parser"
	"github.com/spf13/cobra"
)

// removeCmd is the Cobra command for `brew-engine remove <package>`.
// It requires exactly one positional argument: the formula or cask name.
// All output is streamed as newline-delimited JSON events; the command
// blocks until the brew subprocess exits.
var removeCmd = &cobra.Command{
	Use:   "remove <package>",
	Short: "Uninstall a Homebrew formula or cask",
	Args:  cobra.ExactArgs(1),
	RunE:  runRemove,
}

func init() {
	rootCmd.AddCommand(removeCmd)
}

// runRemove is the RunE handler for [removeCmd]. It forwards the package
// name and os.Stdout directly to [parser.RunRemove], which owns the full
// lifecycle of the brew subprocess, ANSI stripping, logging, and JSON
// event emission. The function always returns nil; any brew-level error is
// represented as a Type="error" JSON event written by the parser.
func runRemove(_ *cobra.Command, args []string) error {
	parser.RunRemove(args[0], os.Stdout)
	_ = cache.InvalidateList()
	return nil
}
