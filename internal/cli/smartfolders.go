package cli

import (
	"fmt"
	"os"
	"sort"

	"github.com/logicminds/filemaid/internal/hub"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(smartFoldersCmd)
}

var smartFoldersCmd = &cobra.Command{
	Use:   "smart-folders",
	Short: "Regenerate the Filemaid hub (Smart Folders, aliases, and sidebar)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !cfg.SmartFolders {
			return fmt.Errorf("smart folders are disabled in configuration")
		}
		return regenerateSmartFolders()
	},
}

// hubBuilder builds the Filemaid hub. It is overridable in tests so CLI
// commands can verify smart folder generation without touching the Finder
// sidebar or creating real aliases.
var hubBuilder interface {
	Build(hub.Options) error
} = hub.New()

// regenerateSmartFolders rebuilds the Filemaid hub, including Smart Folders,
// archive/review aliases, and the Finder sidebar entry. It is a no-op when
// Smart Folders are disabled.
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

	return hubBuilder.Build(hub.Options{
		HubDir:     cfg.SmartFoldersDir,
		ArchiveDir: hub.ArchiveRoot(cfg.Categories),
		ReviewDir:  cfg.ReviewDir,
		Categories: categories,
		Tags:       tags,
		Scopes:     scopes,
	})
}
