package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"
)

var reviewOpen bool

func init() {
	reviewCmd.Flags().BoolVar(&reviewOpen, "open", false, "open review queue in Finder")
	rootCmd.AddCommand(reviewCmd)
}

var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "List or open the review queue",
	Long:  "List files currently quarantined in the review queue, or open the queue in Finder with --open.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return reviewQueue(cfg.ReviewDir, reviewOpen)
	},
}

// reviewQueue lists files in the review queue or opens it in Finder. It mirrors
// the Python review_queue behaviour.
func reviewQueue(reviewDir string, openFinder bool) error {
	path := reviewDir
	if openFinder {
		return exec.Command("open", path).Run()
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Println("review queue is empty")
		return nil
	}

	var files []string
	err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, err := filepath.Rel(path, p)
			if err != nil {
				return err
			}
			files = append(files, rel+": "+fmt.Sprintf("%d bytes", info.Size()))
		}
		return nil
	})
	if err != nil {
		return err
	}

	if len(files) == 0 {
		fmt.Println("review queue is empty")
		return nil
	}

	sort.Strings(files)
	for _, f := range files {
		fmt.Println(f)
	}
	return nil
}
