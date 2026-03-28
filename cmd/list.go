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

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all installed Homebrew formulae and casks",
	RunE:  runList,
}

func init() {
	rootCmd.AddCommand(listCmd)
}

func runList(_ *cobra.Command, _ []string) error {
	out, err := exec.Command("brew", "info", "--installed", "--json=v2").Output()
	if err != nil {
		contract.WriteJSON(os.Stdout, contract.Response{
			Success: false,
			Type:    "error",
			Error:   fmt.Sprintf("brew info --installed failed: %s", err),
		})
		return nil // error already written as JSON; don't let Cobra print it too
	}

	if logger.Sugar != nil {
		logger.Sugar.Debugw("brew info --installed response received", "bytes", len(out))
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

	data := buildListData(raw)

	contract.WriteJSON(os.Stdout, contract.Response{
		Success: true,
		Type:    "list",
		Data:    data,
	})
	return nil
}

// buildListData projects the raw Homebrew JSON v2 payload into our clean schema.
func buildListData(raw contract.BrewInfoV2) contract.ListData {
	data := contract.ListData{
		Formulae: make([]contract.FormulaInfo, 0, len(raw.Formulae)),
		Casks:    make([]contract.CaskInfo, 0, len(raw.Casks)),
	}

	for _, f := range raw.Formulae {
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
		data.Formulae = append(data.Formulae, info)
	}

	for _, c := range raw.Casks {
		name := c.Token
		if len(c.Name) > 0 {
			name = c.Name[0]
		}
		data.Casks = append(data.Casks, contract.CaskInfo{
			Token:       c.Token,
			FullToken:   c.FullToken,
			Tap:         c.Tap,
			Name:        name,
			Description: c.Desc,
			Homepage:    c.Homepage,
			Version:     c.Version,
			Installed:   c.Installed != "",
			Outdated:    c.Outdated,
		})
	}

	data.Total = len(data.Formulae) + len(data.Casks)
	return data
}
