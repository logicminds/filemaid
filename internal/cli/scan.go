package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/logicminds/filemaid/internal/actions"
	"github.com/logicminds/filemaid/internal/log"

	"github.com/spf13/cobra"
)

var scanDir string
var (
	// scanGetFiles returns the candidate files in a scan directory. Tests may
	// replace it to avoid filesystem dependencies.
	scanGetFiles func(dir string) ([]string, error) = defaultScanGetFiles
)

func init() {
	scanCmd.Flags().StringVar(&scanDir, "dir", "", "directory to scan (default: watch_dirs)")
	scanCmd.Flags().StringVar(&processFormat, "format", "table", "output format (table|human|json)")
	scanCmd.Flags().BoolVar(&processJSON, "json", false, "output results as JSON (shorthand for --format json)")
	scanCmd.Flags().BoolVar(&processQuiet, "quiet", false, "suppress log output to stderr")
	scanCmd.Flags().BoolVar(&renameEnabled, "rename", false, "enable AI-generated file renaming")
	scanCmd.Flags().IntVar(&renameLevel, "rename-level", 0, "rename detail level (0-3)")
	scanCmd.Flags().BoolVar(&processDryRun, "dry-run", false, "preview changes without moving files")
	rootCmd.AddCommand(scanCmd)
}

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scan watch directories for stale files",
	RunE: func(cmd *cobra.Command, args []string) error {
		applyRenameFlags(cmd)
		if err := classifier.Validate(cfg); err != nil {
			return fmt.Errorf("model validation failed: %w", err)
		}

		ctx := context.Background()
		if cmd != nil {
			ctx = cmd.Context()
		}
		runID := newRunID()

		format := processFormat
		if processJSON {
			format = "json"
		}
		quiet := processQuiet || (format != "json" && isTerminal(os.Stdout))
		if quiet {
			log.SetStderrEnabled(false)
			defer log.SetStderrEnabled(true)
		}

		if processDryRun {
			oldFS := processFS
			oldDB := db
			processFS = noopFS{}
			db = &noopRecordRepo{Repo: oldDB}
			defer func() {
				processFS = oldFS
				db = oldDB
			}()
		}

		var allResults []processResult
		var err error
		if scanDir != "" {
			allResults, err = runScanDir(ctx, scanDir, runID)
		} else {
			for _, d := range cfg.WatchDirs {
				results, runErr := runScanDir(ctx, d, runID)
				if runErr != nil {
					return runErr
				}
				allResults = append(allResults, results...)
			}
		}
		if err != nil {
			return err
		}
		if !processDryRun {
			if err := regenerateSmartFolders(); err != nil {
				slog.Warn("smart folder regeneration failed", "error", err)
			}
		}
		if len(allResults) == 0 {
			fmt.Println("No files to process.")
			return nil
		}
		out, err := formatProcessResults(allResults, format)
		if err != nil {
			return err
		}
		fmt.Println(out)
		return nil
	},
}

// runScanDir scans a single directory for files older than min_age_hours and
// processes them. It mirrors the Python scan_dir behaviour.
func runScanDir(ctx context.Context, directory string, runID string) ([]processResult, error) {
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve scan directory: %w", err)
	}
	root = filepath.Clean(root)

	if len(cfg.AllowedDirs) > 0 && !actions.WithinAllowed(root, cfg.AllowedDirs) {
		slog.Warn("scan directory not allowed", "dir", root)
		return nil, nil
	}

	files, err := scanGetFiles(root)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			slog.Warn("permission denied", "dir", root)
			return nil, nil
		}
		return nil, fmt.Errorf("list scan files: %w", err)
	}

	cutoff := time.Now().Add(-time.Duration(cfg.MinAgeHours) * time.Hour)
	var toProcess []string
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			slog.Warn("scan file stat failed", "path", path, "error", err)
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if isHidden(path) {
			slog.Info("skipping hidden file in scan", "path", path)
			continue
		}
		if info.ModTime().Before(cutoff) {
			toProcess = append(toProcess, path)
		}
	}

	if len(toProcess) == 0 {
		slog.Info("no files older than min_age_hours", "dir", root)
		return nil, nil
	}

	return processPaths(ctx, toProcess, runID)
}

// defaultScanGetFiles lists regular files directly inside dir.
func defaultScanGetFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	return out, nil
}
