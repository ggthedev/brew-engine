// Package cmd defines the Cobra command tree for brew-engine.
//
// # Subcommands
//
//   - list    — lists all installed Homebrew formulae and casks.
//   - info    — returns detailed info for a single formula or cask.
//   - install — installs a formula or cask and streams progress events.
//   - remove  — uninstalls a formula or cask and streams progress events.
//
// # stdout contract
//
// Every subcommand MUST write ALL output exclusively through
// [contract.WriteJSON]. No raw text, usage messages, or diagnostic strings
// are ever written to stdout. Cobra's own error and usage output is
// suppressed via SilenceErrors and SilenceUsage on [rootCmd] so that even
// framework-level errors reach the consumer as valid JSON.
package cmd

import (
	"os"

	"github.com/brewexplorer/brew-engine/internal/contract"
	"github.com/spf13/cobra"
)

// rootCmd is the top-level Cobra command. It does not run any action itself;
// it exists solely as the parent that aggregates the subcommands registered
// via init() functions in the other files in this package.
//
// SilenceErrors and SilenceUsage are both set to true to prevent Cobra from
// writing its own formatted error strings or usage blocks to os.Stderr.
// All error reporting is handled explicitly by the subcommands and by
// [Execute], which converts any residual Cobra error into a JSON payload.
var rootCmd = &cobra.Command{
	Use:   "brew-engine",
	Short: "Headless JSON engine for Homebrew",
	Long: `brew-engine wraps the brew CLI and communicates exclusively via
newline-delimited JSON on stdout. Every response — including errors — is a
valid contract.Response payload.`,
	// Suppress Cobra's default error/usage output so that stdout stays clean JSON.
	SilenceErrors: true,
	SilenceUsage:  true,
}

// Execute is the single entry point called from main. It runs the Cobra
// command tree, routing os.Args to the correct subcommand.
//
// If Cobra itself returns an error (e.g. unrecognised subcommand, wrong
// argument count), Execute writes a Type="error" [contract.Response] to
// stdout and exits with code 1, preserving the JSON-only stdout invariant.
// Subcommands that encounter brew errors handle their own JSON output and
// return nil to Cobra, so Execute only sees framework-level errors here.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		contract.WriteJSON(os.Stdout, contract.Response{
			Success: false,
			Type:    "error",
			Error:   err.Error(),
		})
		os.Exit(1)
	}
}
