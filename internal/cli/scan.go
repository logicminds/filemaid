package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/logicminds/filemaid/internal/actions"

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
	scanCmd.Flags().StringVar(&processFormat, "format", "table", "output format (table|json)")
	scanCmd.Flags().BoolVar(&processJSON, "json", false, "output results as JSON (shorthand for --format json)")
	rootCmd.AddCommand(scanCmd)
}

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scan watch directories for stale files",
	Long:  "Scan the configured watch directories (or a single directory with --dir) for files matching age rules and queue them for processing.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := classifier.Validate(cfg); err != nil {
			return fmt.Errorf("model validation failed: %w", err)
		}

		ctx := context.Background()
		if cmd != nil {
			ctx = cmd.Context()
		}

		var allResults []processResult
		var err error
		if scanDir != "" {
			allResults, err = runScanDir(ctx, scanDir)
		} else {
			for _, d := range cfg.WatchDirs {
				results, runErr := runScanDir(ctx, d)
				if runErr != nil {
					return runErr
				}
				allResults = append(allResults, results...)
			}
		}
		if err != nil {
			return err
		}
		if len(allResults) == 0 {
			fmt.Println("No files to process.")
			return nil
		}
		format := processFormat
		if processJSON {
			format = "json"
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
func runScanDir(ctx context.Context, directory string) ([]processResult, error) {
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve scan directory: %w", err)
	}
	root = filepath.Clean(root)

	if len(cfg.AllowedDirs) > 0 && !actions.WithinAllowed(root, cfg.AllowedDirs) {
		slog.Error("scan directory not allowed", "dir", root)
		return nil, nil
	}

	files, err := scanGetFiles(root)
	if err != nil {
		slog.Error("permission denied scanning", "dir", root, "error", err)
		return nil, nil
	}

	cutoff := time.Now().Add(-time.Duration(cfg.MinAgeHours) * time.Hour)
	var toProcess []string
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if isHidden(path) {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if len(cfg.AllowedDirs) > 0 && !actions.WithinAllowed(path, cfg.AllowedDirs) {
			continue
		}
		toProcess = append(toProcess, path)
	}

	if len(toProcess) == 0 {
		slog.Info("no files to scan in", "dir", root)
		return nil, nil
	}

	return processPaths(ctx, toProcess)
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
