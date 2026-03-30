// Package cmd This file implements the `brew-engine install` subcommand.
//
// The install command delegates entirely to [parser.RunInstall], which
// executes `brew install -v <package>` and translates its streaming output
// into newline-delimited JSON events on stdout:
//
//   - One Type="progress" event per "==>" phase header encountered.
//   - A terminal Type="done" event on success (exit code 0).
//   - A terminal Type="error" event on failure (non-zero exit code).
//
// The frontend is expected to display a blocking modal overlay that renders
// each incoming progress step and provides a link to the log file for the
// full verbose output.
package cmd

import (
	"os"

	"github.com/brewexplorer/brew-engine/internal/cache"
	"github.com/brewexplorer/brew-engine/internal/parser"
	"github.com/spf13/cobra"
)

// installCmd is the Cobra command for `brew-engine install <package>`.
// It requires exactly one positional argument: the formula or cask name.
// All output is streamed as newline-delimited JSON events; the command
// blocks until the brew subprocess exits.
var installCmd = &cobra.Command{
	Use:   "install <package>",
	Short: "Install a Homebrew formula or cask",
	Args:  cobra.ExactArgs(1),
	RunE:  runInstall,
}

func init() {
	rootCmd.AddCommand(installCmd)
}

// runInstall is the RunE handler for [installCmd]. It forwards the package
// name and os.Stdout directly to [parser.RunInstall], which owns the full
// lifecycle of the brew subprocess, ANSI stripping, logging, and JSON
// event emission. The function always returns nil; any brew-level error is
// represented as a Type="error" JSON event written by the parser.
func runInstall(_ *cobra.Command, args []string) error {
	parser.RunInstall(args[0], os.Stdout)
	_ = cache.InvalidateList()
	return nil
}
