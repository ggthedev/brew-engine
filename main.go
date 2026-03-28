package main

import (
	"fmt"
	"os"

	"github.com/brewexplorer/brew-engine/cmd"
	"github.com/brewexplorer/brew-engine/internal/logger"
)

func main() {
	if err := logger.Init(); err != nil {
		// stdout is reserved for JSON only; logger failures go to stderr
		fmt.Fprintf(os.Stderr, "fatal: failed to initialize logger: %s\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	cmd.Execute()
}
