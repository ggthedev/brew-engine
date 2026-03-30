// Package main is the entry point for brew-engine, a headless CLI binary
// that wraps the Homebrew package manager and surfaces all output as
// newline-delimited JSON on stdout.
//
// Startup sequence:
//  1. Load configuration via [config.Load]: reads the application plist
//     (~/Library/Preferences/com.mobilityquarks.brewexplorer.plist) via
//     plutil, falls back to compiled defaults, and exports all values as
//     environment variables for the rest of the process lifetime.
//  2. Initialise the background file logger via [logger.Init].
//  3. Verify Homebrew is present and executable. The result is always logged
//     to the audit trail (Zap). If brew is absent, emit a [contract.Response]
//     with Type="brew_not_found" to stdout and exit 2.
//  4. Delegate all CLI routing to [cmd.Execute], which dispatches to the
//     appropriate Cobra subcommand (list, info, install, remove, refresh).
//
// Exit codes:
//
//	0 — success
//	1 — config or logger initialisation failure (written to stderr only)
//	2 — Homebrew not found (JSON written to stdout; also logged to audit file)
//
// stdout invariant: the only bytes ever written to stdout are complete,
// newline-terminated [contract.Response] JSON objects. If logger
// initialisation fails, the diagnostic is written to stderr only — the
// stdout channel is never contaminated.
package main

import (
	"fmt"
	"os"

	"github.com/brewexplorer/brew-engine/cmd"
	"github.com/brewexplorer/brew-engine/internal/config"
	"github.com/brewexplorer/brew-engine/internal/contract"
	"github.com/brewexplorer/brew-engine/internal/logger"
)

// main initialises the Zap file logger and then hands control to
// [cmd.Execute]. It is the only place in the binary where os.Exit is
// called outside of an error path — every other exit is either through
// normal Cobra completion or a JSON error payload written by a subcommand.
func main() {
	if err := config.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: failed to load config: %s\n", err)
		os.Exit(1)
	}

	if err := logger.Init(); err != nil {
		// stdout is reserved for JSON only; logger failures go to stderr
		fmt.Fprintf(os.Stderr, "fatal: failed to initialize logger: %s\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	// Audit: log the brew check outcome before any subcommand runs.
	// Logger is live here, so every start-up is traceable in the log file.
	brewPath := config.ResolvedBrewPath()
	if !config.IsBrewExecutable(brewPath) {
		if logger.Sugar != nil {
			logger.Sugar.Errorw("brew not found",
				"checked_paths", config.BrewCandidatePaths(),
			)
		}
		contract.WriteJSON(os.Stdout, contract.Response{
			Success: false,
			Type:    "brew_not_found",
			Error:   "Homebrew is not installed or not found at any known location",
			Data: contract.BrewNotFoundData{
				CheckedPaths: config.BrewCandidatePaths(),
			},
		})
		os.Exit(2)
	}
	if logger.Sugar != nil {
		logger.Sugar.Infow("brew verified", "path", brewPath)
	}

	cmd.Execute()
}
