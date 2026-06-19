package cli

import (
	"github.com/logicminds/filemaid/internal/setup"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(uninstallCmd)
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall filemaid agents and wrapper",
	Long:  "Read setup metadata, boot out agents, remove plists and the installed binary. Config, logs, DB, and review queue are preserved.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return setup.Uninstall(setup.UninstallOptions{})
	},
}
