// Package cmd defines all Cobra subcommands for brew-engine.
// Every command must route ALL output through contract.WriteJSON —
// stdout is an exclusive JSON channel; no raw text is ever written there.
package cmd

import (
	"os"

	"github.com/brewexplorer/brew-engine/internal/contract"
	"github.com/spf13/cobra"
)

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

// Execute is the single entry point called from main.
// Any error from Cobra itself (e.g. unknown command) is surfaced as JSON.
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
