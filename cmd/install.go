package cmd

import (
	"os"

	"github.com/brewexplorer/brew-engine/internal/parser"
	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install <package>",
	Short: "Install a Homebrew formula or cask",
	Args:  cobra.ExactArgs(1),
	RunE:  runInstall,
}

func init() {
	rootCmd.AddCommand(installCmd)
}

func runInstall(_ *cobra.Command, args []string) error {
	parser.RunInstall(args[0], os.Stdout)
	return nil
}
