package cli

import (
	"fmt"
	"os"
	"sort"

	"github.com/logicminds/filemaid/internal/smartfolder"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(smartFoldersCmd)
}

var smartFoldersCmd = &cobra.Command{
	Use:   "smart-folders",
	Short: "Regenerate macOS Smart Folders",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !cfg.SmartFolders {
			return fmt.Errorf("smart folders are disabled in configuration")
		}
		return regenerateSmartFolders()
	},
}

// regenerateSmartFolders rebuilds the configured Smart Folders directory from
// the current categories, tags, and allowed scopes. It is a no-op when Smart
// Folders are disabled.
func regenerateSmartFolders() error {
	if !cfg.SmartFolders {
		return nil
	}

	categories := make([]string, 0, len(cfg.Categories))
	for name := range cfg.Categories {
		categories = append(categories, name)
	}
	sort.Strings(categories)

	tags, err := db.DistinctTags()
	if err != nil {
		return fmt.Errorf("list distinct tags: %w", err)
	}

	scopes := cfg.AllowedDirs
	if len(scopes) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("determine home directory: %w", err)
		}
		scopes = []string{home}
	}

	return smartfolder.Build(categories, tags, scopes, cfg.SmartFoldersDir)
}
