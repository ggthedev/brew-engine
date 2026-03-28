// Package cmd This file implements the `brew-engine list` subcommand.
//
// The list command runs `brew list --formula` and `brew list --cask` to
// collect the names of every installed package, then emits a single
// Type="list" [contract.Response] whose Data field is a [contract.NamesList].
//
// This approach is deliberately chosen over `brew info --installed --json=v2`
// because the latter resolves the full dependency graph and returns megabytes
// of JSON, whereas `brew list` is near-instant and returns only names.
package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

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

// runList is the RunE handler for [listCmd]. It calls [fetchNames] for both
// formulae and casks, assembles a [contract.NamesList], and writes it as a
// single Type="list" JSON event to stdout.
//
// If either brew invocation fails, a Type="error" JSON event is written and
// the function returns nil so Cobra does not produce additional output.
func runList(_ *cobra.Command, _ []string) error {
	formulae, err := fetchNames("--formula")
	if err != nil {
		contract.WriteJSON(os.Stdout, contract.Response{
			Success: false,
			Type:    "error",
			Error:   fmt.Sprintf("brew list --formula failed: %s", err),
		})
		return nil
	}

	casks, err := fetchNames("--cask")
	if err != nil {
		contract.WriteJSON(os.Stdout, contract.Response{
			Success: false,
			Type:    "error",
			Error:   fmt.Sprintf("brew list --cask failed: %s", err),
		})
		return nil
	}

	data := contract.NamesList{
		Formulae: formulae,
		Casks:    casks,
		Total:    len(formulae) + len(casks),
	}

	if logger.Sugar != nil {
		logger.Sugar.Debugw("list complete",
			"formulae", len(formulae),
			"casks", len(casks),
		)
	}

	contract.WriteJSON(os.Stdout, contract.Response{
		Success: true,
		Type:    "list",
		Data:    data,
	})
	return nil
}

// fetchNames runs `brew list <flag>` and returns the package names as a
// string slice. flag must be either "--formula" or "--cask".
//
// brew list outputs names separated by whitespace and/or newlines depending
// on the terminal width; [strings.Fields] splits on any whitespace,
// handling both cases uniformly and discarding any empty tokens.
//
// A non-zero exit from brew is returned as an error. An empty list (no
// packages installed of that type) is not an error — it returns a nil
// error with an empty slice.
func fetchNames(flag string) ([]string, error) {
	out, err := exec.Command("brew", "list", flag).Output()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return []string{}, nil
	}
	return strings.Fields(raw), nil
}
