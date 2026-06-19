package cli

import (
	"os"

	"github.com/logicminds/filemaid/internal/config"
	"github.com/logicminds/filemaid/internal/log"
	"github.com/logicminds/filemaid/internal/state"

	"github.com/spf13/cobra"
)

var (
	cfg *config.Config
	db  state.Repo
	// Version is set at build time via -ldflags from the current git tag.
	Version = "dev"
	// Commit is set at build time via -ldflags from the current git SHA.
	Commit  = "unknown"
	rootCmd = &cobra.Command{
		Use:     "filemaid",
		Short:   "AI-powered file organizer and cleanup assistant for macOS",
		Version: Version + " (commit " + Commit + ")",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			var err error
			cfg, err = config.Load()
			if err != nil {
				return err
			}
			if err := log.Init(cfg.LogPath); err != nil {
				return err
			}
			db, err = state.Open(cfg.DBPath)
			return err
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			if db != nil {
				return db.Close()
			}
			return nil
		},
	}
)

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
