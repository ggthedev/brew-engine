package cmd

import (
	"os"

	"github.com/brewexplorer/brew-engine/internal/parser"
	"github.com/spf13/cobra"
)

var removeCmd = &cobra.Command{
	Use:   "remove <package>",
	Short: "Uninstall a Homebrew formula or cask",
	Args:  cobra.ExactArgs(1),
	RunE:  runRemove,
}

func init() {
	rootCmd.AddCommand(removeCmd)
}

func runRemove(_ *cobra.Command, args []string) error {
	parser.RunRemove(args[0], os.Stdout)
	return nil
}
