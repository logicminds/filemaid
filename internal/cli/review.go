package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/logicminds/filemaid/internal/actions"
	"github.com/logicminds/filemaid/internal/llm"
	"github.com/logicminds/filemaid/internal/state"

	"github.com/spf13/cobra"
)

var (
	reviewOpen    bool
	reviewApprove string
	reviewReject  string
	// reviewFS is the filesystem implementation used by review. Tests may
	// replace it with a recording or fake filesystem.
	reviewFS actions.FS = actions.NewOSFS()
)

func init() {
	reviewCmd.Flags().BoolVar(&reviewOpen, "open", false, "open review queue in Finder")
	reviewCmd.Flags().StringVar(&reviewApprove, "approve", "", "approve a review item by relative path")
	reviewCmd.Flags().StringVar(&reviewReject, "reject", "", "reject a review item by relative path")
	rootCmd.AddCommand(reviewCmd)
}

var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "List, open, or manage the review queue",
	Long:  "List files currently quarantined in the review queue, open the queue in Finder with --open, or approve/reject an item by relative path.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if reviewApprove != "" {
			return reviewApprovePath(cfg.ReviewDir, reviewApprove)
		}
		if reviewReject != "" {
			return reviewRejectPath(cfg.ReviewDir, reviewReject)
		}
		return reviewQueue(cfg.ReviewDir, reviewOpen)
	},
}

// reviewRecordMap loads recent history records and indexes them by final path.
func reviewRecordMap() (map[string]state.Record, error) {
	records, err := db.History(10000, "")
	if err != nil {
		return nil, err
	}
	m := make(map[string]state.Record, len(records))
	for _, r := range records {
		if r.FinalPath == "" {
			continue
		}
		if _, ok := m[r.FinalPath]; !ok {
			m[r.FinalPath] = r
		}
	}
	return m, nil
}

// reviewQueue lists files in the review queue or opens it in Finder. It mirrors
// the Python review_queue behaviour and augments the output with the original
// name and any proposed rename stored in state.
func reviewQueue(reviewDir string, openFinder bool) error {
	path := reviewDir
	if openFinder {
		return exec.Command("open", path).Run()
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Println("review queue is empty")
		return nil
	}

	records, err := reviewRecordMap()
	if err != nil {
		return err
	}

	type item struct {
		rel      string
		size     int64
		original string
		newName  string
		category string
		reason   string
	}
	var items []item
	err = filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, err := filepath.Rel(path, p)
			if err != nil {
				return err
			}
			rec, ok := records[p]
			if !ok {
				rec, ok = records[filepath.Join(path, rel)]
			}
			it := item{rel: rel, size: info.Size()}
			if ok {
				it.original = rec.OriginalName
				it.newName = rec.NewName
				it.category = rec.Category
				it.reason = rec.Reason
			}
			items = append(items, it)
		}
		return nil
	})
	if err != nil {
		return err
	}

	if len(items) == 0 {
		fmt.Println("review queue is empty")
		return nil
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].rel < items[j].rel
	})
	for _, it := range items {
		nameLine := it.original
		if nameLine == "" {
			nameLine = filepath.Base(it.rel)
		}
		if it.newName != "" && it.newName != nameLine {
			nameLine = fmt.Sprintf("%s -> %s", nameLine, it.newName)
		}
		parts := []string{it.rel, fmt.Sprintf("%d bytes", it.size), nameLine}
		if it.category != "" {
			parts = append(parts, it.category)
		}
		if it.reason != "" {
			parts = append(parts, it.reason)
		}
		fmt.Println(strings.Join(parts, " | "))
	}
	return nil
}

// reviewApprovePath moves a review item to its computed destination, restoring
// the proposed rename from state.
func reviewApprovePath(reviewDir, rel string) error {
	rec, err := reviewFindRecord(reviewDir, rel)
	if err != nil {
		return err
	}
	nameQuality := 0
	if rec.NameQuality.Valid {
		nameQuality = int(rec.NameQuality.Float64)
	}
	decision := llm.Decision{
		Category:    rec.Category,
		Action:      "move",
		Reason:      "approved from review",
		NewName:     rec.NewName,
		NameQuality: nameQuality,
	}
	_, err = actions.Apply(decision, rec.FinalPath, rec.SHA256, cfg, db, false, reviewFS, "", llm.Metrics{})
	if err != nil {
		return fmt.Errorf("approve failed: %w", err)
	}
	fmt.Printf("approved: %s\n", rel)
	return nil
}

// reviewRejectPath sends a review item to the trash.
func reviewRejectPath(reviewDir, rel string) error {
	rec, err := reviewFindRecord(reviewDir, rel)
	if err != nil {
		return err
	}
	if err := reviewFS.Trash(rec.FinalPath); err != nil {
		return fmt.Errorf("reject failed: %w", err)
	}
	fmt.Printf("rejected: %s\n", rel)
	return nil
}

// reviewFindRecord locates the most recent state record for a review item.
func reviewFindRecord(reviewDir, rel string) (state.Record, error) {
	full := filepath.Join(reviewDir, rel)
	records, err := db.History(10000, "")
	if err != nil {
		return state.Record{}, err
	}
	for _, r := range records {
		if r.FinalPath == full || r.FinalPath == rel {
			return r, nil
		}
	}
	return state.Record{}, fmt.Errorf("review item not found in state: %s", rel)
}
