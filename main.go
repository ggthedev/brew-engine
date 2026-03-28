// Package main is the entry point for brew-engine, a headless CLI binary
// that wraps the Homebrew package manager and surfaces all output as
// newline-delimited JSON on stdout.
//
// Startup sequence:
//  1. Initialise the background file logger via [logger.Init].
//  2. Delegate all CLI routing to [cmd.Execute], which dispatches to the
//     appropriate Cobra subcommand (list, info, install, remove).
//
// stdout invariant: the only bytes ever written to stdout are complete,
// newline-terminated [contract.Response] JSON objects. If logger
// initialisation fails, the diagnostic is written to stderr only, and the
// process exits with code 1 — the stdout channel is never contaminated.
package main

import (
	"fmt"
	"os"

	"github.com/brewexplorer/brew-engine/cmd"
	"github.com/brewexplorer/brew-engine/internal/logger"
)

// main initialises the Zap file logger and then hands control to
// [cmd.Execute]. It is the only place in the binary where os.Exit is
// called outside of an error path — every other exit is either through
// normal Cobra completion or a JSON error payload written by a subcommand.
func main() {
	if err := logger.Init(); err != nil {
		// stdout is reserved for JSON only; logger failures go to stderr
		fmt.Fprintf(os.Stderr, "fatal: failed to initialize logger: %s\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	cmd.Execute()
}
