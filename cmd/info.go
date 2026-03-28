// Package cmd This file implements the `brew-engine info` subcommand.
//
// The info command invokes `brew info --json=v2 <package>`, which returns
// a JSON payload describing a single formula or cask (installed or not).
// The raw response is unmarshalled into [contract.BrewInfoV2], and the
// first result in either the Formulae or Casks bucket is projected into
// a [contract.FormulaInfo] or [contract.CaskInfo] and emitted as a single
// Type="info" [contract.Response] on stdout.
//
// Precedence: if brew returns results in both buckets (a theoretical name
// collision between a formula and a cask), the formula takes precedence.
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/brewexplorer/brew-engine/internal/contract"
	"github.com/brewexplorer/brew-engine/internal/logger"
	"github.com/spf13/cobra"
)

// infoCmd is the Cobra command for `brew-engine info <package>`.
// It requires exactly one positional argument: the formula or cask name.
// It emits exactly one JSON line: either a Type="info" [contract.Response]
// with a [contract.FormulaInfo] or [contract.CaskInfo] payload, or a
// Type="error" response if the package is not found or brew fails.
var infoCmd = &cobra.Command{
	Use:   "info <package>",
	Short: "Show detailed info for a formula or cask",
	Args:  cobra.ExactArgs(1),
	RunE:  runInfo,
}

func init() {
	rootCmd.AddCommand(infoCmd)
}

// runInfo is the RunE handler for [infoCmd]. It executes
// `brew info --json=v2 <pkg>`, unmarshals the response, and emits one
// of the following JSON events to stdout:
//
//   - Type="info" with Data=[contract.FormulaInfo] if the package is a formula.
//   - Type="info" with Data=[contract.CaskInfo] if the package is a cask.
//   - Type="error" if brew exits non-zero (package not found, network error,
//     etc.) or if JSON unmarshalling of brew's output fails.
//
// The function always returns nil so that Cobra does not attempt additional
// error formatting; all error reporting goes through [contract.WriteJSON].
func runInfo(_ *cobra.Command, args []string) error {
	pkg := args[0]

	out, err := exec.Command("brew", "info", "--json=v2", pkg).Output()
	if err != nil {
		contract.WriteJSON(os.Stdout, contract.Response{
			Success: false,
			Type:    "error",
			Error:   fmt.Sprintf("brew info failed for %q: %s", pkg, err),
		})
		return nil
	}

	if logger.Sugar != nil {
		logger.Sugar.Debugw("brew info response received", "package", pkg, "bytes", len(out))
	}

	var raw contract.BrewInfoV2
	if err := json.Unmarshal(out, &raw); err != nil {
		contract.WriteJSON(os.Stdout, contract.Response{
			Success: false,
			Type:    "error",
			Error:   fmt.Sprintf("unmarshal brew output: %s", err),
		})
		return nil
	}

	// brew info --json=v2 returns the package in the appropriate bucket.
	// Formulae take precedence if a name collision exists (extremely rare).
	if len(raw.Formulae) > 0 {
		f := raw.Formulae[0]
		info := contract.FormulaInfo{
			Name:        f.Name,
			FullName:    f.FullName,
			Tap:         f.Tap,
			Description: f.Desc,
			Homepage:    f.Homepage,
			Version:     f.Versions.Stable,
			Installed:   len(f.Installed) > 0,
			Outdated:    f.Outdated,
			Pinned:      f.Pinned,
		}
		if len(f.Installed) > 0 {
			info.InstalledVersion = f.Installed[0].Version
		}
		contract.WriteJSON(os.Stdout, contract.Response{Success: true, Type: "info", Data: info})
		return nil
	}

	if len(raw.Casks) > 0 {
		c := raw.Casks[0]
		name := c.Token
		if len(c.Name) > 0 {
			name = c.Name[0]
		}
		contract.WriteJSON(os.Stdout, contract.Response{
			Success: true,
			Type:    "info",
			Data: contract.CaskInfo{
				Token:       c.Token,
				FullToken:   c.FullToken,
				Tap:         c.Tap,
				Name:        name,
				Description: c.Desc,
				Homepage:    c.Homepage,
				Version:     c.Version,
				Installed:   c.Installed != "",
				Outdated:    c.Outdated,
			},
		})
		return nil
	}

	contract.WriteJSON(os.Stdout, contract.Response{
		Success: false,
		Type:    "error",
		Error:   fmt.Sprintf("package %q not found", pkg),
	})
	return nil
}
