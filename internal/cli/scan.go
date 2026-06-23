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
	"github.com/logicminds/filemaid/internal/directory"
	"github.com/logicminds/filemaid/internal/llm"
	"github.com/logicminds/filemaid/internal/log"
	"github.com/spf13/cobra"
)

var scanDir string
var includeDirs int
var (
	// scanGetCandidates returns the candidate files and directories in a scan
	// directory up to the requested depth. Tests may replace it to avoid
	// filesystem dependencies.
	scanGetCandidates func(dir string, depth int) ([]processInput, []string, error) = defaultScanGetCandidates
)

func init() {
	scanCmd.Flags().StringVar(&scanDir, "dir", "", "directory to scan (default: watch_dirs)")
	scanCmd.Flags().StringVar(&processFormat, "format", "human", "output format (table|human|json)")
	scanCmd.Flags().BoolVar(&processJSON, "json", false, "output results as JSON (shorthand for --format json)")
	scanCmd.Flags().BoolVar(&processQuiet, "quiet", false, "suppress log output to stderr")
	scanCmd.Flags().StringVar(&renameFlag, "rename", "", "rename files using the LLM; optionally set minimum quality threshold 1-5 (e.g. --rename=3); 1=most aggressive, 5=most conservative, default is 2")
	scanCmd.Flags().Lookup("rename").NoOptDefVal = "default"
	scanCmd.Flags().BoolVar(&processForce, "force", false, "force processing even if the file is a duplicate or similar to existing history")
	scanCmd.Flags().BoolVar(&processDryRun, "dry-run", false, "preview changes without moving files")
	scanCmd.Flags().IntVar(&includeDirs, "include-dirs", 0, "descend into directories N levels (0 = file-only)")
	scanCmd.Flags().Lookup("include-dirs").NoOptDefVal = "1"
	rootCmd.AddCommand(scanCmd)
}

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Scan watch directories for stale files",
	RunE: func(cmd *cobra.Command, args []string) error {
		applyRenameFlags(cmd)
		applyForceFlags(cmd)
		if includeDirs < 0 {
			return fmt.Errorf("--include-dirs must be >= 0")
		}
		if err := classifier.Validate(cfg); err != nil {
			return fmt.Errorf("model validation failed: %w", err)
		}
		if c, ok := classifier.(*llm.Client); ok {
			c.SetDecisionCache(db)
			c.SetDirectoryDecisionCache(db)
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

	files, dirs, err := scanGetCandidates(root, includeDirs)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			slog.Warn("permission denied", "dir", root)
			return nil, nil
		}
		return nil, fmt.Errorf("list scan files: %w", err)
	}

	cutoff := time.Now().Add(-time.Duration(cfg.MinAgeHours) * time.Hour)
	var toProcess []processInput
	for _, in := range files {
		path := in.path
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
			toProcess = append(toProcess, in)
		}
	}

	if includeDirs > 0 {
		for _, path := range dirs {
			info, err := os.Stat(path)
			if err != nil {
				slog.Warn("scan directory stat failed", "path", path, "error", err)
				continue
			}
			if !info.IsDir() {
				continue
			}
			if isHidden(path) {
				slog.Info("skipping hidden directory in scan", "path", path)
				continue
			}
			if info.ModTime().Before(cutoff) {
				toProcess = append(toProcess, processInput{path: path})
			}
		}
	}

	if len(toProcess) == 0 {
		slog.Info("no files older than min_age_hours", "dir", root)
		return nil, nil
	}

	processIncludeDirs = includeDirs
	return processPaths(ctx, toProcess, runID, w, format)
}

// defaultScanGetCandidates lists candidate files and directories inside dir.
// When depth <= 0 it preserves the original file-only behavior: only immediate
// regular files are returned. When depth > 0 it descends up to depth levels,
// applying allowed_dirs, hidden-directory, and min_age_hours guardrails at each
// level. Directories containing a project marker from cfg.ProjectMarkers (or
// .app bundles) are emitted as directory candidates and are not recursed into.
func defaultScanGetCandidates(dir string, depth int) ([]processInput, []string, error) {
	if depth <= 0 {
		return listImmediateFiles(dir)
	}
	return scanCandidatesRecursive(dir, depth, 1)
}

// listImmediateFiles preserves the legacy file-only enumeration behavior.
func listImmediateFiles(dir string) ([]processInput, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	files := make([]processInput, 0, len(entries))
	dirs := make([]string, 0, len(entries))
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.IsDir() {
			dirs = append(dirs, path)
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Mode().IsRegular() {
			files = append(files, processInput{path: path})
		}
	}
	return files, dirs, nil
}

// scanCandidatesRecursive walks dir up to depth levels and returns file and
// directory candidates that pass configured guardrails. currentDepth is the
// level of dir relative to the scan root (1 = immediate child of root).
func scanCandidatesRecursive(dir string, depth int, currentDepth int) ([]processInput, []string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}

	var dirCtx *directory.Context
	if currentDepth > 0 {
		meta, _ := directory.Gather(dir, cfg)
		marker := ""
		if meta != nil && len(meta.Markers) > 0 {
			marker = meta.Markers[0]
		}
		dirCtx = &directory.Context{
			Ancestor: dir,
			Depth:    currentDepth,
			Marker:   marker,
		}
	}

	var files []processInput
	var dirs []string
	cutoff := time.Now().Add(-time.Duration(cfg.MinAgeHours) * time.Hour)

	for _, e := range entries {
		path := filepath.Join(dir, e.Name())

		if isHidden(path) {
			continue
		}
		if len(cfg.AllowedDirs) > 0 && !actions.WithinAllowed(path, cfg.AllowedDirs) {
			continue
		}

		info, err := e.Info()
		if err != nil {
			slog.Warn("scan entry info failed", "path", path, "error", err)
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}

		if e.IsDir() {
			meta, err := directory.Gather(path, cfg)
			if err != nil {
				slog.Warn("directory metadata failed", "path", path, "error", err)
				continue
			}
			dirs = append(dirs, path)
			if meta.IsAppBundle || len(meta.Markers) > 0 {
				continue
			}
			if depth > 1 {
				subFiles, subDirs, err := scanCandidatesRecursive(path, depth-1, currentDepth+1)
				if err != nil {
					slog.Warn("scan subdirectory failed", "path", path, "error", err)
					continue
				}
				files = append(files, subFiles...)
				dirs = append(dirs, subDirs...)
			}
			continue
		}

		if info.Mode().IsRegular() {
			files = append(files, processInput{path: path, dirCtx: dirCtx})
		}
	}

	return files, dirs, nil
}
