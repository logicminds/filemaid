package cli

import (
	"fmt"
	"os"

	"github.com/logicminds/filemaid/internal/config"
	"github.com/logicminds/filemaid/internal/log"
	"github.com/logicminds/filemaid/internal/setup"
	"github.com/logicminds/filemaid/internal/state"

	"github.com/spf13/cobra"
)

var (
	cfg *config.Config
	db  state.Repo
	// Version is set at build time via -ldflags from the current git tag.
	Version = "dev"
	// Commit is set at build time via -ldflags from the current git SHA.
	Commit = "unknown"
)

var rootCmd = &cobra.Command{
	Use:     "filemaid",
	Short:   "AI-powered file organizer and cleanup assistant for macOS",
	Version: Version + " (commit " + Commit + ")",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if !requiresSetup(cmd) {
			return nil
		}
		setupOK, err := setup.IsSetup()
		if err != nil {
			return err
		}
		if !setupOK {
			return fmt.Errorf("filemaid has not been set up. Run 'filemaid setup' first")
		}

		var loadErr error
		cfg, loadErr = config.Load()
		if loadErr != nil {
			return loadErr
		}
		if loadErr := log.Init(cfg.LogPath); loadErr != nil {
			return loadErr
		}
		db, loadErr = state.Open(cfg.DBPath)
		return loadErr
	},
	PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
		if db != nil {
			return db.Close()
		}
		return nil
	},
}

// requiresSetup reports whether the command needs a completed setup to run.
// setup and uninstall are exempt because setup creates the state and uninstall
// degrades gracefully when no state exists.
func requiresSetup(cmd *cobra.Command) bool {
	if cmd == nil || cmd.Parent() == nil {
		return false
	}
	switch cmd.Name() {
	case "setup", "uninstall":
		return false
	default:
		return true
	}
}

func init() {
	rootCmd.Version = Version + " (commit " + Commit + ")"
	rootCmd.SetHelpTemplate("{{if .Version}}Version:\n  {{.Version}}\n\n{{end}}" + rootCmd.HelpTemplate())
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
