package cli

import (
	"github.com/logicminds/filemaid/internal/setup"

	"github.com/spf13/cobra"
)

var (
	setupNoScan        bool
	setupNoInteractive bool
	setupBinDir        string
	setupConfigDir     string
	setupDataDir       string
	setupModel         string
)

func init() {
	setupCmd.Flags().BoolVar(&setupNoScan, "no-scan", false, "do not install the periodic scan LaunchAgent")
	setupCmd.Flags().BoolVar(&setupNoInteractive, "no-interactive", false, "skip the interactive configuration interview")
	setupCmd.Flags().StringVar(&setupBinDir, "bin-dir", "", "directory for the binary (default: ~/.local/bin)")
	setupCmd.Flags().StringVar(&setupConfigDir, "config-dir", "", "directory for config files (default: ~/.config/filemaid)")
	setupCmd.Flags().StringVar(&setupDataDir, "data-dir", "", "directory for runtime data (default: ~/.local/share/filemaid)")
	setupCmd.Flags().StringVar(&setupModel, "model", "", "Ollama model to use (default: auto-select by RAM)")
	rootCmd.AddCommand(setupCmd)
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Install filemaid agents and wrapper",
	Long:  "Copy the binary, install config/modelfiles, generate launchd plists, and bootstrap agents.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return setup.Install(setup.InstallOptions{
			NoScan:      setupNoScan,
			Interactive: !setupNoInteractive,
			BinDir:      setupBinDir,
			ConfigDir:   setupConfigDir,
			DataDir:     setupDataDir,
			ModelName:   setupModel,
		})
	},
}
