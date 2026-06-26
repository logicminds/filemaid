package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/logicminds/filemaid/internal/actions"
	"github.com/logicminds/filemaid/internal/llm"
	"github.com/logicminds/filemaid/internal/log"
	"github.com/logicminds/filemaid/internal/state"

	"github.com/spf13/cobra"
)

var (
	reviewOpen    bool
	reviewApprove string
	reviewReject  string
	reviewRetry   bool
	reviewRename  string
	// reviewFS is the filesystem implementation used by review. Tests may
	// replace it with a recording or fake filesystem.
	reviewFS actions.FS = actions.NewOSFS()
)

func init() {
	reviewCmd.Flags().BoolVar(&reviewRetry, "retry", false, "re-process review items whose reason indicates a transient LLM failure")
	reviewCmd.Flags().StringVar(&reviewRename, "rename", "", "when used with --retry, rename files using the LLM; optionally set minimum quality threshold 1-5")
	reviewCmd.Flags().Lookup("rename").NoOptDefVal = "default"
	reviewCmd.Flags().BoolVar(&reviewOpen, "open", false, "open review queue in Finder")
	reviewCmd.Flags().StringVar(&reviewApprove, "approve", "", "approve a review item by relative path")
	reviewCmd.Flags().StringVar(&reviewReject, "reject", "", "reject a review item by relative path")
	rootCmd.AddCommand(reviewCmd)
}

var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "List, open, or manage the review queue",
	Long:  "List files currently quarantined in the review queue, open the queue in Finder with --open, approve/reject an item by relative path, or retry transient LLM failures with --retry.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if reviewRetry {
			return reviewRetryItems(cmd)
		}
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
	approveCfg := *cfg
	approveCfg.MoveFiles = true
	_, err = actions.Apply(decision, rec.FinalPath, rec.SHA256, &approveCfg, db, false, reviewFS, "", llm.Metrics{}, false)
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

// applyReviewRenameFlags copies CLI flag overrides for rename settings into cfg
// when the user explicitly provided them. It mirrors applyRenameFlags used by
// process and scan.
func applyReviewRenameFlags(cmd *cobra.Command) {
	if cmd == nil {
		return
	}

	const defaultRenameLevel = 2

	if cmd.Flags().Changed("rename") {
		cfg.Rename = true
		switch reviewRename {
		case "", "default":
			cfg.RenameLevel = defaultRenameLevel
		default:
			if n, err := strconv.Atoi(reviewRename); err == nil && n >= 0 && n <= 5 {
				cfg.RenameLevel = n
			} else {
				cfg.RenameLevel = defaultRenameLevel
			}
		}
	}
}

// reviewRetryItems re-processes review-queue items whose stored reason
// indicates a transient LLM failure. Items that still fail or land in review
func reviewRetryItems(cmd *cobra.Command) error {
	applyReviewRenameFlags(cmd)

	// Retry is an explicit re-evaluation of review items; allow moves so
	// successful retries leave the review queue.
	oldMoveFiles := cfg.MoveFiles
	cfg.MoveFiles = true
	defer func() { cfg.MoveFiles = oldMoveFiles }()

	if err := classifier.Validate(cfg); err != nil {
		return fmt.Errorf("model validation failed: %w", err)
	}
	if c, ok := classifier.(*llm.Client); ok {
		c.SetDecisionCache(db)
		c.SetDirectoryDecisionCache(db)
	}

	records, err := db.History(10000, "")
	if err != nil {
		return fmt.Errorf("load history: %w", err)
	}

	var inputs []processInput
	seen := make(map[string]bool)
	for _, r := range records {
		if r.Action != "review" {
			continue
		}
		if !llm.IsTransientReason(r.Reason) {
			continue
		}
		if seen[r.FinalPath] {
			continue
		}
		seen[r.FinalPath] = true
		if _, err := os.Stat(r.FinalPath); err != nil {
			slog.Warn("review retry skipping missing file", "path", r.FinalPath, "error", err)
			continue
		}
		inputs = append(inputs, processInput{path: r.FinalPath})
	}

	if len(inputs) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No transient review items to retry.")
		return nil
	}

	ctx := context.Background()
	if cmd != nil {
		ctx = cmd.Context()
	}

	quiet := isTerminal(os.Stdout)
	if quiet {
		log.SetStderrEnabled(false)
		defer log.SetStderrEnabled(true)
	}

	runID := newRunID()
	results, err := processPaths(ctx, inputs, runID, cmd.OutOrStdout(), "human")
	if err != nil {
		return err
	}

	if err := regenerateSmartFolders(); err != nil {
		slog.Warn("smart folder regeneration failed", "error", err)
	}

	if len(results) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No files retried.")
		return nil
	}

	fmt.Fprintln(cmd.OutOrStdout(), formatSummary(results, isTerminal(os.Stdout)))
	return nil
}
