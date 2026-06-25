package cli

import (
	"fmt"
	"os"

	"github.com/logicminds/filemaid/internal/actions"
	"github.com/logicminds/filemaid/internal/llm"
	"github.com/logicminds/filemaid/internal/state"

	"github.com/spf13/cobra"
)

var (
	undoFS     actions.FS = actions.NewOSFS()
	undoLast   bool
	undoRun    string
	undoForce  bool
	undoDryRun bool
)

func init() {
	undoCmd.Flags().BoolVar(&undoLast, "last", false, "undo the most recent run with move actions")
	undoCmd.Flags().StringVar(&undoRun, "run", "", "undo all moves for a specific run ID")
	undoCmd.Flags().BoolVar(&undoForce, "force", false, "overwrite existing destinations")
	undoCmd.Flags().BoolVar(&undoDryRun, "dry-run", false, "preview restores without moving files or recording history")
	rootCmd.AddCommand(undoCmd)
}

var undoCmd = &cobra.Command{
	Use:   "undo [--last|--run <id>|<final-path>]",
	Short: "Restore files to their original paths",
	Long:  "Reverse filemaid moves and renames by restoring items from their final paths back to their original paths.",
	RunE: func(cmd *cobra.Command, args []string) error {
		records, err := resolveUndoRecords(args)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			fmt.Println("no undoable moves found")
			return nil
		}

		for _, r := range records {
			if err := restoreRecord(r); err != nil {
				return err
			}
		}
		return nil
	},
}

// resolveUndoRecords returns the history rows that should be restored based on
// the selected mode. It validates that exactly one selection mode is provided.
func resolveUndoRecords(args []string) ([]state.Record, error) {
	modes := 0
	if undoLast {
		modes++
	}
	if undoRun != "" {
		modes++
	}
	if len(args) > 0 {
		modes++
	}
	if modes == 0 {
		return nil, fmt.Errorf("undo requires --last, --run, or a final-path argument")
	}
	if modes > 1 {
		return nil, fmt.Errorf("only one of --last, --run, or final-path may be used")
	}
	if len(args) > 1 {
		return nil, fmt.Errorf("undo accepts at most one final-path argument")
	}

	if undoRun != "" {
		return db.HistoryByRunID(undoRun)
	}
	if len(args) > 0 {
		r, err := db.HistoryByFinalPath(args[0])
		if err != nil {
			return nil, err
		}
		if r == nil {
			return nil, nil
		}
		return []state.Record{*r}, nil
	}

	runID, err := db.LastRunID()
	if err != nil {
		return nil, err
	}
	if runID == "" {
		return nil, nil
	}
	return db.HistoryByRunID(runID)
}

// restoreRecord moves a single history row back to its original path unless
// safety checks fail or dry-run mode is active.
func restoreRecord(r state.Record) error {
	if r.FinalPath == "trash" || r.Action == "undo" || r.Action == "classify" || r.FinalPath == r.OriginalPath {
		fmt.Fprintf(os.Stderr, "skip %s: %s\n", r.FinalPath, reasonSkip(r))
		return nil
	}
	if !undoFS.Exists(r.FinalPath) {
		fmt.Fprintf(os.Stderr, "skip %s: not found at final path\n", r.FinalPath)
		return nil
	}

	dest := r.OriginalPath
	if len(cfg.AllowedDirs) > 0 && !actions.WithinAllowed(dest, cfg.AllowedDirs) {
		fmt.Fprintf(os.Stderr, "skip %s: restore destination outside allowed dirs\n", r.FinalPath)
		return nil
	}
	if undoFS.Exists(dest) && !undoForce {
		fmt.Fprintf(os.Stderr, "skip %s: destination already exists (use --force)\n", r.FinalPath)
		return nil
	}

	if undoDryRun {
		fmt.Printf("would restore %s -> %s\n", r.FinalPath, dest)
		return nil
	}

	if err := undoFS.Move(r.FinalPath, dest); err != nil {
		return fmt.Errorf("move %s -> %s: %w", r.FinalPath, dest, err)
	}

	if err := db.Record(state.RecordInput{
		OriginalPath: r.FinalPath,
		FinalPath:    dest,
		SHA256:       r.SHA256,
		Category:     r.Category,
		Tags:         []string{},
		Action:       "undo",
		Reason:       "undo restore",
		RunID:        newRunID(),
		Metrics:      llm.Metrics{},
	}); err != nil {
		return fmt.Errorf("record undo: %w", err)
	}

	fmt.Printf("restored %s -> %s\n", r.FinalPath, dest)
	return nil
}

func reasonSkip(r state.Record) string {
	if r.FinalPath == "trash" {
		return "trashed items cannot be restored"
	}
	if r.Action == "classify" || r.FinalPath == r.OriginalPath {
		return "file was not moved"
	}
	return "already an undo record"
}
