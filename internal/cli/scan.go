package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	scanCmd.Flags().StringVar(&renameFlag, "rename", "", "rename files using the LLM; optionally set minimum quality threshold 1-5 (e.g. --rename=3); 1=most aggressive, 5=most conservative, default is 2")
	scanCmd.Flags().Lookup("rename").NoOptDefVal = "default"
	scanCmd.Flags().BoolVar(&processForce, "force", false, "force processing even if the file is a duplicate or similar to existing history")
	scanCmd.Flags().BoolVar(&processDryRun, "dry-run", false, "preview changes without moving files")
	rootCmd.AddCommand(scanCmd)
}

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scan watch directories for stale files",
	RunE: func(cmd *cobra.Command, args []string) error {
		applyRenameFlags(cmd)
		applyForceFlags(cmd)
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
		var out io.Writer = os.Stdout
		if cmd != nil {
			out = cmd.OutOrStdout()
		}

		var allResults []processResult
		var err error
		if scanDir != "" {
			allResults, err = runScanDir(ctx, scanDir, runID, out, format)
		} else {
			for _, d := range cfg.WatchDirs {
				results, runErr := runScanDir(ctx, d, runID, out, format)
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
			fmt.Fprintln(out, "No files to process.")
			return nil
		}
		if format == "json" {
			outBytes, err := formatProcessResults(allResults, format)
			if err != nil {
				return err
			}
			fmt.Fprintln(out, outBytes)
			return nil
		}
		if format == "table" {
			writeProcessTableFooter(out)
		}
		fmt.Fprintln(out, formatSummary(allResults, isTerminal(os.Stdout)))
		return nil
	},
}

func runScanDir(ctx context.Context, directory string, runID string, w io.Writer, format string) ([]processResult, error) {
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

	return processPaths(ctx, toProcess, runID, w, format)
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
