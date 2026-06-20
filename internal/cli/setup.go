package cli

import (
	"os"
	"path/filepath"

	"github.com/logicminds/filemaid/internal/setup"

	"github.com/spf13/cobra"
)

var (
	setupAgents        bool
	setupNoScan        bool
	setupNoInteractive bool
	setupBinDir        string
	setupConfigDir     string
	setupDataDir       string
	setupModel         string
	setupShortcuts     bool
)

func init() {
	setupCmd.Flags().BoolVar(&setupAgents, "agents", false, "install and bootstrap the launchd scan and cleanup agents")
	setupCmd.Flags().BoolVar(&setupNoScan, "no-scan", false, "when --agents is used, skip the periodic scan LaunchAgent")
	setupCmd.Flags().BoolVar(&setupNoInteractive, "no-interactive", false, "skip the interactive configuration interview")
	setupCmd.Flags().StringVar(&setupBinDir, "bin-dir", "", "directory for the binary (default: ~/.local/bin)")
	setupCmd.Flags().StringVar(&setupConfigDir, "config-dir", "", "directory for config files (default: ~/.config/filemaid)")
	setupCmd.Flags().StringVar(&setupDataDir, "data-dir", "", "directory for runtime data (default: ~/.local/share/filemaid)")
	setupCmd.Flags().StringVar(&setupModel, "model", "", "Ollama model to use (default: auto-select by RAM)")
	setupCmd.Flags().BoolVar(&setupShortcuts, "shortcuts", false, "output the macOS Shortcuts folder-automation setup steps")
	rootCmd.AddCommand(setupCmd)
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Install filemaid binary, config, and optional agents",
	Long:  "Copy the binary, install config/modelfiles, and optionally generate launchd plists and bootstrap agents.",
	RunE: func(cmd *cobra.Command, args []string) error {
		binDir := setupBinDir
		if binDir == "" {
			home, _ := os.UserHomeDir()
			binDir = filepath.Join(home, ".local", "bin")
		}
		binaryPath := filepath.Join(binDir, "filemaid")

		if setupShortcuts {
			setup.PrintShortcutsInstructions(os.Stdout, binaryPath)
			if !setupAgents && !setupNoInteractive && setupBinDir == "" && setupConfigDir == "" && setupDataDir == "" && setupModel == "" {
				return nil
			}
		}

		return setup.Install(setup.InstallOptions{
			Agents:      setupAgents,
			NoScan:      setupNoScan,
			Interactive: !setupNoInteractive,
			BinDir:      setupBinDir,
			ConfigDir:   setupConfigDir,
			DataDir:     setupDataDir,
			ModelName:   setupModel,
		})
	},
}
