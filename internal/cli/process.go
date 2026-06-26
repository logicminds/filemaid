package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/logicminds/filemaid/internal/actions"
	"github.com/logicminds/filemaid/internal/config"
	"github.com/logicminds/filemaid/internal/directory"
	"github.com/logicminds/filemaid/internal/llm"
	"github.com/logicminds/filemaid/internal/log"
	"github.com/logicminds/filemaid/internal/state"

	"github.com/spf13/cobra"
)

var (
	// classifier is the LLM-based file classifier used by process. Tests may
	// replace it with a deterministic implementation.
	classifier llm.Classifier = llm.NewClient(nil)

	// applyDecision carries out a classification Decision. Tests may replace
	// it to observe decisions without touching the filesystem.
	applyDecision applierFunc = actions.Apply

	// processFS is the filesystem implementation used by process. Tests may
	// replace it with a recording or fake filesystem.
	processFS actions.FS = actions.NewOSFS()

	// computeHash is the hash implementation used during preprocessing. Tests may
	// replace it to observe or slow down hashing without touching real files.
	computeHash = actions.ComputeHash

	// nowFunc returns the current time. Tests may replace it with a fixed clock.
	nowFunc = time.Now

	// moveProjects enables whole-directory moves for recognized project
	// directories in process and scan.
	moveProjects bool

	// applyDirectoryFunc carries out a DirectoryDecision. Tests may replace it
	// to observe directory applies without touching the filesystem.
	applyDirectoryFunc func(decision llm.DirectoryDecision, src string, cfg *config.Config, db state.Repo, fs actions.FS, runID string) (string, error) = actions.ApplyDirectory
)

// noopFS is an actions.FS implementation that performs no side effects. It is
// used by dry-run mode to compute destinations without moving files.
type noopFS struct{}

func (n noopFS) Move(src, dest string) error                  { return nil }
func (n noopFS) MkdirAll(path string) error                   { return nil }
func (n noopFS) Exists(path string) bool                      { return false }
func (n noopFS) SetTags(path string, tags []string)           {}
func (n noopFS) SetFinderComment(path string, comment string) {}
func (n noopFS) FlushMDImport()                               {}
func (n noopFS) Trash(path string) error                      { return nil }

// noopRecordRepo wraps a state.Repo and suppresses writes so dry-run mode does
// not persist history or cached decisions.
type noopRecordRepo struct {
	state.Repo
}

func (n *noopRecordRepo) Record(input state.RecordInput) error                      { return nil }
func (n *noopRecordRepo) RecordDecision(sha256 string, decision llm.Decision) error { return nil }

// processInput carries a candidate path and optional directory context for
// files discovered while scanning with --depth.
type processInput struct {
	path        string
	dirCtx      *directory.Context
	dirDecision *llm.DirectoryDecision // pre-computed directory decision from scan
}

// processItem carries the sequential preprocessing state for a single accepted
// file into the concurrent classify+apply stage.
type processItem struct {
	// pos is the input index into the original paths slice, used to write the
	// final result back into the correct position.
	pos         int
	src         string
	fileHash    string
	isDuplicate bool
	ageDecision llm.Decision
	ageMatched  bool
	model       string
	dirCtx      *directory.Context
}

// modelForPath returns the configured model to use for the given path.
// Image files route to ImageModel; everything else routes to TextModel.
// Either falls back to Model when the specific model is not configured.
func modelForPath(path string, cfg *config.Config) string {
	if llm.IsImageFile(path) {
		if cfg.ImageModel != "" {
			return cfg.ImageModel
		}
	} else {
		if cfg.TextModel != "" {
			return cfg.TextModel
		}
	}
	return cfg.Model
}

// groupItemsByModel groups process items by their target model name.
func groupItemsByModel(items []processItem) map[string][]processItem {
	groups := make(map[string][]processItem)
	for _, item := range items {
		groups[item.model] = append(groups[item.model], item)
	}
	return groups
}

// sortedModelNames returns the model keys of groups in deterministic order.
func sortedModelNames(groups map[string][]processItem) []string {
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

var (
	processFormat string
	processJSON   bool
	processQuiet  bool
	renameFlag    string
	processDryRun bool
	processForce  bool
	processMove   bool
	processDepth  int
)

// applierFunc matches the signature of actions.Apply so it can be swapped in tests.
type applierFunc func(decision llm.Decision, src string, fileHash string, cfg *config.Config, db state.Repo, isDuplicate bool, fs actions.FS, runID string, metrics llm.Metrics, force bool) (string, error)

func init() {
	processCmd.Flags().StringVar(&processFormat, "format", "human", "output format (table|human|json)")
	processCmd.Flags().BoolVar(&processJSON, "json", false, "output results as JSON (shorthand for --format json)")
	processCmd.Flags().BoolVar(&processQuiet, "quiet", false, "suppress log output to stderr")
	processCmd.Flags().StringVar(&renameFlag, "rename", "", "rename files using the LLM; optionally set minimum quality threshold 1-5 (e.g. --rename=3); 1=most aggressive, 5=most conservative, default is 2")
	processCmd.Flags().Lookup("rename").NoOptDefVal = "default"
	processCmd.Flags().BoolVar(&processForce, "force", false, "force processing even if the file is a duplicate or similar to existing history")
	processCmd.Flags().BoolVar(&processDryRun, "dry-run", false, "preview changes without moving files")
	processCmd.Flags().BoolVar(&processMove, "move", false, "move files to their classified destination")
	processCmd.Flags().IntVar(&processDepth, "depth", 0, "descend into directories N levels (0 = file-only)")
	processCmd.Flags().Lookup("depth").NoOptDefVal = "1"
	processCmd.Flags().BoolVar(&moveProjects, "move-projects", false, "move recognized project directories as atomic units")
	rootCmd.AddCommand(processCmd)
}

// applyMoveFlags copies CLI flag overrides for move settings into cfg when
// the user explicitly provided them. It keeps config-file defaults intact for
// flags that were not set.
func applyMoveFlags(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	if cmd.Flags().Changed("move") {
		cfg.MoveFiles = processMove
	}
}

// applyForceFlags copies CLI flag overrides for force settings into cfg when
// the user explicitly provided them. It keeps config-file defaults intact for
// flags that were not set.
// applyForceFlags copies CLI flag overrides for force settings into cfg when
// the user explicitly provided them. It keeps config-file defaults intact for
// flags that were not set.
func applyForceFlags(cmd *cobra.Command) {
	if cmd == nil {
		return
	}
	if cmd.Flags().Changed("force") {
		cfg.Force = processForce
	}
}

// applyRenameFlags copies CLI flag overrides for rename settings into cfg when
// the user explicitly provided them. It keeps config-file defaults intact for
// flags that were not set.
func applyRenameFlags(cmd *cobra.Command) {
	if cmd == nil {
		return
	}

	const defaultRenameLevel = 2

	// --rename (with or without a value) enables renaming.
	if cmd.Flags().Changed("rename") {
		cfg.Rename = true
		switch renameFlag {
		case "", "default":
			cfg.RenameLevel = defaultRenameLevel
		default:
			if n, err := strconv.Atoi(renameFlag); err == nil && n >= 0 && n <= 5 {
				cfg.RenameLevel = n
			} else {
				cfg.RenameLevel = defaultRenameLevel
			}
		}
	}
}

// processCmd handles file processing.
var processCmd = &cobra.Command{
	Use:   "process <paths...>",
	Short: "Classify and apply decisions to files",
	Long:  "Process one or more files: classify them with the configured LLM and apply the resulting move/tag/delete/review decision.",
	RunE: func(cmd *cobra.Command, args []string) error {
		applyRenameFlags(cmd)
		applyMoveFlags(cmd)
		applyForceFlags(cmd)

		if processDepth < 0 {
			return fmt.Errorf("--depth must be >= 0")
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

		format := processFormat
		if processJSON {
			format = "json"
		}
		// For interactive human-readable output, keep structured logs in the
		// log file but suppress JSON spam on stderr so the table/list is clean.
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

		runID := newRunID()
		var out io.Writer = os.Stdout
		if cmd != nil {
			out = cmd.OutOrStdout()
		}
		inputs := make([]processInput, len(args))
		for i, a := range args {
			inputs[i] = processInput{path: a}
		}
		results, err := processPaths(ctx, inputs, runID, out, format)
		if err != nil {
			return err
		}
		if !processDryRun {
			if err := regenerateSmartFolders(); err != nil {
				slog.Warn("smart folder regeneration failed", "error", err)
			}
		}
		if len(results) == 0 {
			fmt.Fprintln(out, "No files processed.")
			return nil
		}
		if format == "json" {
			outBytes, err := formatProcessResults(results, format)
			if err != nil {
				return err
			}
			fmt.Fprintln(out, outBytes)
			return nil
		}
		if format == "table" {
			writeProcessTableFooter(out)
		}
		fmt.Fprintln(out, formatSummary(results, isTerminal(os.Stdout)))
		return nil
	},
}

// processResult captures the outcome of processing a single candidate for display.
type processResult struct {
	Path             string   `json:"path"`
	Category         string   `json:"category"`
	Tags             []string `json:"tags"`
	Action           string   `json:"action"`
	Recommendation   string   `json:"recommendation,omitempty"` // for directory rows
	Result           string   `json:"result"`
	OriginalName     string   `json:"original_name,omitempty"`
	NewName          string   `json:"new_name,omitempty"`
	OK               bool     `json:"ok"`
	Error            string   `json:"error,omitempty"`
	Reason           string   `json:"reason,omitempty"`
	DurationMs       int64    `json:"duration_ms"`
	PromptTokens     int      `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	TotalTokens      int      `json:"total_tokens"`
	TokensPerSec     float64  `json:"tokens_per_sec"`
	ContextSize      int      `json:"context_size"`
	Kind             string   `json:"kind,omitempty"` // "file" or "directory"
}

// formatProcessResults renders process results as a table, human-readable list, or JSON.
func formatProcessResults(results []processResult, format string) (string, error) {
	switch format {
	case "json":
		out, err := json.MarshalIndent(results, "", "  ")
		return string(out), err
	case "human":
		return formatHuman(results), nil
	default:
		return formatProcessTable(results), nil
	}
}

// column width caps for the process output table. These keep the table usable
// on typical terminals without excessive wrapping.
const (
	maxFileLen   = 42
	maxCatLen    = 16
	maxTagsLen   = 30
	maxActionLen = 8
	maxNameLen   = 32
	maxResultLen = 48
)

// formatNameChange returns a compact old -> new name summary for display.
func formatNameChange(r processResult) string {
	original := r.OriginalName
	if original == "" {
		original = filepath.Base(r.Path)
	}
	if r.NewName != "" && r.NewName != original {
		return fmt.Sprintf("%s -> %s", original, r.NewName)
	}
	if r.Action == "skip" || r.Action == "delete" {
		return "-"
	}
	return "kept name"
}

// formatProcessTable renders process results as a compact ASCII table.
func formatProcessTable(results []processResult) string {
	useColor := isTerminal(os.Stdout)
	headers := []string{"Item", "Category", "Tags", "Action", "Name", "Result", "Status"}
	rows := make([][]string, 0, len(results))
	for _, r := range results {
		status := statusSymbol(r.OK, r.Error, useColor)
		action := r.Action
		if action == "" {
			action = "-"
		}
		category := r.Category
		if category == "" {
			category = "-"
		}
		itemPath := truncatePath(collapseHome(r.Path), maxFileLen)
		if r.Kind == "directory" {
			itemPath = "dir:" + itemPath
		}
		resultPath := truncatePath(collapseHome(r.Result), maxResultLen)
		if (r.Kind == "" || r.Kind == "file") && (r.Action == "move" || r.Action == "classify") && r.NewName != "" && filepath.Dir(r.Result) == filepath.Dir(r.Path) {
			resultPath = "in-place: " + resultPath
		}
		rows = append(rows, []string{
			itemPath,
			truncateTags([]string{category}, maxCatLen),
			truncateTags(r.Tags, maxTagsLen),
			truncateTags([]string{action}, maxActionLen),
			truncateTags([]string{formatNameChange(r)}, maxNameLen),
			resultPath,
			status,
		})
	}
	var lines []string
	lines = append(lines, renderTable(headers, rows))
	lines = append(lines, "")
	lines = append(lines, formatSummary(results, useColor))
	return strings.Join(lines, "\n")
}

// formatHuman renders results as a compact list designed for readability.
func formatHuman(results []processResult) string {
	useColor := isTerminal(os.Stdout)
	var lines []string
	for _, r := range results {
		name := filepath.Base(r.Path)
		status := statusSymbol(r.OK, r.Error, useColor)
		if r.Kind == "directory" && r.Action == "move" {
			dest := collapseHome(r.Result)
			lines = append(lines, fmt.Sprintf("%s  moved directory %s → %s", status, colorize(name, colorBold, useColor), colorize(dest, colorBold, useColor)))
		} else {
			if r.Kind == "directory" {
				name = "[dir] " + name
			}
			lines = append(lines, fmt.Sprintf("%s  %s", status, colorize(name, colorBold, useColor)))
		}
		if r.Category != "" {
			lines = append(lines, fmt.Sprintf("   Category: %s", r.Category))
		}
		if len(r.Tags) > 0 {
			lines = append(lines, fmt.Sprintf("   Tags:     %s", strings.Join(r.Tags, ", ")))
		}
		if r.Action != "" {
			lines = append(lines, fmt.Sprintf("   Action:   %s", r.Action))
		}
		nameChange := formatNameChange(r)
		if nameChange != "-" {
			lines = append(lines, fmt.Sprintf("   Name:     %s", nameChange))
		}
		if r.Result != "" {
			result := collapseHome(r.Result)
			if (r.Kind == "" || r.Kind == "file") && (r.Action == "move" || r.Action == "classify") && r.NewName != "" && filepath.Dir(r.Result) == filepath.Dir(r.Path) {
				result = "in-place: " + result
			}
			lines = append(lines, fmt.Sprintf("   Result:   %s", result))
		}
		if r.Reason != "" {
			lines = append(lines, fmt.Sprintf("   Reason:   %s", r.Reason))
		}
		if r.Error != "" {
			lines = append(lines, colorize(fmt.Sprintf("   Error:    %s", r.Error), colorRed, useColor))
		}
	}
	lines = append(lines, "")
	lines = append(lines, formatSummary(results, useColor))
	return strings.Join(lines, "\n")
}

// statusSymbol returns the status glyph for a result, optionally colored.
func statusSymbol(ok bool, err string, useColor bool) string {
	if err != "" {
		return colorize("⚠", colorYellow, useColor)
	}
	if ok {
		return colorize("✓", colorGreen, useColor)
	}
	return colorize("✗", colorRed, useColor)
}

// formatSummary returns a one-line summary of the results.
func formatSummary(results []processResult, useColor bool) string {
	var ok, review, skip, failed int
	var files, dirs int
	for _, r := range results {
		if r.Kind == "directory" {
			dirs++
		} else {
			files++
		}
		if r.Action == "skip" {
			skip++
		} else if r.Error != "" {
			failed++
		} else if r.Action == "review" {
			review++
		} else if r.OK {
			ok++
		}
	}
	var itemSummary string
	if dirs == 0 {
		itemSummary = fmt.Sprintf("%d file%s processed", files, plural(files))
	} else if files == 0 {
		itemSummary = fmt.Sprintf("%d director%s processed", dirs, plural(dirs))
	} else {
		itemSummary = fmt.Sprintf("%d file%s, %d director%s processed", files, plural(files), dirs, plural(dirs))
	}
	parts := []string{itemSummary}
	if ok > 0 {
		parts = append(parts, colorize(fmt.Sprintf("%d ok", ok), colorGreen, useColor))
	}
	if review > 0 {
		parts = append(parts, colorize(fmt.Sprintf("%d review", review), colorYellow, useColor))
	}
	if skip > 0 {
		parts = append(parts, colorize(fmt.Sprintf("%d skip", skip), colorCyan, useColor))
	}
	if failed > 0 {
		parts = append(parts, colorize(fmt.Sprintf("%d failed", failed), colorRed, useColor))
	}
	return strings.Join(parts, " · ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// processStreamer writes human-readable progress lines for each result as it
// becomes available. JSON output is batched by the caller, so the streamer
// ignores the "json" format.
type processStreamer struct {
	w             io.Writer
	format        string
	mu            sync.Mutex
	headerPrinted bool
}

func newProcessStreamer(w io.Writer, format string) *processStreamer {
	return &processStreamer{w: w, format: format}
}

func (s *processStreamer) writeResult(r processResult) {
	if s == nil || s.w == nil || s.format == "" || s.format == "json" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	useColor := isTerminal(os.Stdout)
	switch s.format {
	case "human":
		s.writeHumanResult(r, useColor)
	default:
		s.writeTableResult(r, useColor)
	}
}

func (s *processStreamer) writeHumanResult(r processResult, useColor bool) {
	name := filepath.Base(r.Path)
	if r.Kind == "directory" {
		name = "[dir] " + name
	}
	status := statusSymbol(r.OK, r.Error, useColor)
	fmt.Fprintf(s.w, "%s  %s\n", status, colorize(name, colorBold, useColor))
	if r.Category != "" {
		fmt.Fprintf(s.w, "   Category: %s\n", r.Category)
	}
	if len(r.Tags) > 0 {
		fmt.Fprintf(s.w, "   Tags:     %s\n", strings.Join(r.Tags, ", "))
	}
	if r.Kind == "directory" {
		fmt.Fprintf(s.w, "   Recommendation: %s\n", r.Recommendation)
	} else if r.Action != "" {
		fmt.Fprintf(s.w, "   Action:   %s\n", r.Action)
	}
	nameChange := formatNameChange(r)
	if nameChange != "-" {
		fmt.Fprintf(s.w, "   Name:     %s\n", nameChange)
	}
	if r.Result != "" {
		result := collapseHome(r.Result)
		if r.Kind == "file" && (r.Action == "move" || r.Action == "classify") && r.NewName != "" && filepath.Dir(r.Result) == filepath.Dir(r.Path) {
			result = "in-place: " + result
		}
		fmt.Fprintf(s.w, "   Result:   %s\n", result)
	}
	if r.Error != "" {
		fmt.Fprintf(s.w, "%s\n", colorize(fmt.Sprintf("   Error:    %s", r.Error), colorRed, useColor))
	}
}

func (s *processStreamer) writeTableResult(r processResult, useColor bool) {
	headers := []string{"Item", "Category", "Tags", "Action", "Name", "Result", "Status"}
	statusWidth := max(len("Status"), max(len(statusSymbol(true, "", useColor)), max(len(statusSymbol(false, "", useColor)), len(statusSymbol(false, "err", useColor)))))
	widths := []int{maxFileLen, maxCatLen, maxTagsLen, maxActionLen, maxNameLen, maxResultLen, statusWidth}

	if !s.headerPrinted {
		sep := "+" + strings.Join(mapSlice(widths, func(w int) string { return strings.Repeat("-", w+2) }), "+") + "+"
		fmt.Fprintln(s.w, sep)
		fmt.Fprintln(s.w, "| "+strings.Join(mapSliceIndex(headers, widths, func(h string, w int) string { return padRight(h, w) }), " | ")+" |")
		fmt.Fprintln(s.w, sep)
		s.headerPrinted = true
	}

	status := statusSymbol(r.OK, r.Error, useColor)
	action := r.Action
	if r.Kind == "directory" {
		action = r.Recommendation
	} else if action == "" {
		action = "-"
	}
	category := r.Category
	if category == "" {
		category = "-"
	}
	itemPath := truncatePath(collapseHome(r.Path), maxFileLen)
	if r.Kind == "directory" {
		itemPath = "dir:" + itemPath
	}
	resultPath := truncatePath(collapseHome(r.Result), maxResultLen)
	if (r.Kind == "" || r.Kind == "file") && (r.Action == "move" || r.Action == "classify") && r.NewName != "" && filepath.Dir(r.Result) == filepath.Dir(r.Path) {
		resultPath = "in-place: " + resultPath
	}
	row := []string{
		itemPath,
		truncateTags([]string{category}, maxCatLen),
		truncateTags(r.Tags, maxTagsLen),
		truncateTags([]string{action}, maxActionLen),
		truncateTags([]string{formatNameChange(r)}, maxNameLen),
		resultPath,
		status,
	}
	fmt.Fprintln(s.w, "| "+strings.Join(mapSliceIndex(row, widths, func(cell string, w int) string { return padRight(cell, w) }), " | ")+" |")
}

// writeProcessTableFooter prints the closing separator for a streamed table.
func writeProcessTableFooter(w io.Writer) {
	useColor := isTerminal(os.Stdout)
	statusWidth := max(len("Status"), max(len(statusSymbol(true, "", useColor)), max(len(statusSymbol(false, "", useColor)), len(statusSymbol(false, "err", useColor)))))
	widths := []int{maxFileLen, maxCatLen, maxTagsLen, maxActionLen, maxNameLen, maxResultLen, statusWidth}
	sep := "+" + strings.Join(mapSlice(widths, func(w int) string { return strings.Repeat("-", w+2) }), "+") + "+"
	fmt.Fprintln(w, sep)
}

// dirLockMap provides a mutex for each destination directory so independent
// apply operations can run concurrently while operations targeting the same
// directory are serialized.
type dirLockMap struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newDirLockMap() *dirLockMap {
	return &dirLockMap{locks: make(map[string]*sync.Mutex)}
}

// lock acquires the mutex for dir and returns a function that releases it.
func (d *dirLockMap) lock(dir string) func() {
	d.mu.Lock()
	m, ok := d.locks[dir]
	if !ok {
		m = &sync.Mutex{}
		d.locks[dir] = m
	}
	d.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// processPaths classifies and applies decisions to each input. It mirrors the
// Python process_paths behaviour: skip non-existent, non-file, hidden, and
// out-of-allowed files; honour age rules; detect duplicates; coerce unsafe
// deletes to review; log the result; and return a displayable result per file.
func processPaths(ctx context.Context, inputs []processInput, runID string, w io.Writer, format string) ([]processResult, error) {
	streamer := newProcessStreamer(w, format)

	// Preprocess in two stages so that skipping and duplicate detection remain
	// deterministic while file hashing runs concurrently. Skipped paths still
	// produce a result so the final table is complete.
	results := make([]processResult, len(inputs))
	dirLocks := newDirLockMap()

	type candidate struct {
		pos    int
		src    string
		dirCtx *directory.Context
	}
	candidates := make([]candidate, 0, len(inputs))

	for i, in := range inputs {
		raw := in.path
		src, err := filepath.Abs(raw)
		if err != nil {
			results[i] = skipResult(raw, fmt.Sprintf("path normalization failed: %v", err))
			streamer.writeResult(results[i])
			slog.Warn("path normalization failed", "path", raw, "error", err)
			continue
		}

		info, err := os.Stat(src)
		if err != nil {
			results[i] = skipResult(src, "path does not exist")
			streamer.writeResult(results[i])
			slog.Warn("path does not exist", "path", raw)
			continue
		}
		if !info.Mode().IsRegular() {
			if processDepth > 0 && info.IsDir() {
				if isHidden(src) {
					results[i] = skipResult(src, "hidden directory")
					streamer.writeResult(results[i])
					slog.Info("skipping hidden directory", "path", src)
					continue
				}
				if len(cfg.AllowedDirs) > 0 && !actions.WithinAllowed(src, cfg.AllowedDirs) {
					results[i] = skipResult(src, "outside allowed dirs")
					streamer.writeResult(results[i])
					slog.Warn("skipping directory outside allowed dirs", "path", src)
					continue
				}
				decision, metrics := directoryDecisionForPath(ctx, src, in)
				if cfg.MoveFiles && moveProjects && decision.Action == "move" {
					results[i] = applyDirectory(src, decision, metrics, runID, dirLocks)
					slog.Info("moved directory", "path", src, "destination", results[i].Result, "reason", decision.Reason)
				} else {
					results[i] = recommendDirectory(src, decision, metrics)
					slog.Info("directory candidate", "path", src, "recommendation", decision.Recommendation, "reason", decision.Reason)
				}
				streamer.writeResult(results[i])
				continue
			}
			results[i] = skipResult(src, "not a regular file")
			streamer.writeResult(results[i])
			slog.Warn("not a file", "path", src)
			continue
		}
		if isHidden(src) {
			results[i] = skipResult(src, "hidden file")
			streamer.writeResult(results[i])
			slog.Info("skipping hidden file", "path", src)
			continue
		}
		if len(cfg.AllowedDirs) > 0 && !actions.WithinAllowed(src, cfg.AllowedDirs) {
			results[i] = skipResult(src, "outside allowed dirs")
			streamer.writeResult(results[i])
			slog.Warn("skipping file outside allowed dirs", "path", src)
			continue
		}

		candidates = append(candidates, candidate{pos: i, src: src, dirCtx: in.dirCtx})
	}

	// Compute hashes concurrently up to ProcessWorkers goroutines while writing
	// results back by input position so downstream ordering stays deterministic.
	fileHashes := make([]string, len(inputs))
	hashErrs := make([]error, len(inputs))

	hashWorkers := cfg.ProcessWorkers
	if hashWorkers < 1 {
		hashWorkers = 1
	}
	hashSem := make(chan struct{}, hashWorkers)
	var hashWg sync.WaitGroup
	for _, c := range candidates {
		hashWg.Add(1)
		go func(c candidate) {
			defer hashWg.Done()
			hashSem <- struct{}{}
			defer func() { <-hashSem }()
			h, err := computeHash(c.src)
			fileHashes[c.pos] = h
			hashErrs[c.pos] = err
		}(c)
	}
	hashWg.Wait()

	// Sequential post-hash stage preserves ordering for duplicate detection, age
	// rules, cached decisions, and the final result table.
	items := make([]processItem, 0, len(candidates))
	seenHashes := make(map[string]bool)
	for _, c := range candidates {
		i := c.pos
		src := c.src
		if hashErrs[i] != nil {
			results[i] = skipResult(src, fmt.Sprintf("hash failed: %v", hashErrs[i]))
			streamer.writeResult(results[i])
			slog.Warn("hash failed", "path", src, "error", hashErrs[i])
			continue
		}
		fileHash := fileHashes[i]

		rec, err := db.FindByHash(fileHash)
		if err != nil {
			slog.Warn("duplicate lookup failed", "path", src, "error", err)
		}
		isDuplicate := rec != nil && processFS.Exists(rec.FinalPath)
		if seenHashes[fileHash] {
			isDuplicate = true
		}
		seenHashes[fileHash] = true

		ageDecision, ageMatched := checkAgeRule(src, cfg)

		// Decision cache hit: apply the cached decision directly without
		// acquiring a worker or calling the LLM.
		if !ageMatched {
			if cached, ok, err := db.FindDecisionByHash(fileHash); err == nil && ok {
				decision := coerceDuplicateDecision(cached, src, cfg, isDuplicate)
				results[i] = applyFile(src, fileHash, isDuplicate, decision, llm.Metrics{}, runID)
				streamer.writeResult(results[i])
				continue
			}
		}

		// Duplicates that cannot be promoted to delete are reviewed immediately
		// without an LLM round-trip.
		if isDuplicate && !actions.MatchesPatterns(src, cfg.SafeDeletePatterns) {
			var decision llm.Decision
			if ageMatched {
				decision = ageDecision
			} else {
				decision = llm.NewDecision()
				decision.Reason = "duplicate detected"
			}
			decision = coerceDuplicateDecision(decision, src, cfg, isDuplicate)
			results[i] = applyFile(src, fileHash, isDuplicate, decision, llm.Metrics{}, runID)
			streamer.writeResult(results[i])
			continue
		}

		items = append(items, processItem{
			pos:         i,
			src:         src,
			fileHash:    fileHash,
			isDuplicate: isDuplicate,
			ageDecision: ageDecision,
			ageMatched:  ageMatched,
			model:       modelForPath(src, cfg),
			dirCtx:      c.dirCtx,
		})
	}

	var wg sync.WaitGroup
	workers := cfg.ProcessWorkers
	if workers < 1 {
		workers = 1
	}
	classifySem := make(chan struct{}, workers)
	applySem := make(chan struct{}, workers)

	groups := groupItemsByModel(items)

	for _, model := range sortedModelNames(groups) {
		groupItems := groups[model]
		groupCfg := *cfg
		groupCfg.Model = model

		for _, item := range groupItems {
			wg.Add(1)
			go func(item processItem, cfg *config.Config) {
				defer wg.Done()

				classifySem <- struct{}{}
				decision := item.ageDecision
				metrics := llm.Metrics{}
				if !item.ageMatched {
					var err error
					decision, metrics, err = classifier.Classify(ctx, item.src, item.fileHash, cfg, item.dirCtx)
					if err != nil {
						slog.Warn("classification failed", "path", item.src, "error", err)
						decision = llm.NewDecision()
					}
				}
				decision = coerceDuplicateDecision(decision, item.src, cfg, item.isDuplicate)
				<-classifySem

				destDir := actions.DestinationDir(decision, item.src, cfg)
				unlock := dirLocks.lock(destDir)
				defer unlock()

				applySem <- struct{}{}
				defer func() { <-applySem }()

				results[item.pos] = applyFile(item.src, item.fileHash, item.isDuplicate, decision, metrics, runID)
				streamer.writeResult(results[item.pos])
			}(item, &groupCfg)
		}
	}
	wg.Wait()

	processFS.FlushMDImport()

	return results, nil
}

// skipResult builds a processResult for a path that was skipped during preprocessing.
func skipResult(path string, reason string) processResult {
	return processResult{
		Path:         path,
		Action:       "skip",
		Result:       "-",
		OriginalName: filepath.Base(path),
		OK:           false,
		Error:        reason,
	}
}

// coerceDuplicateDecision applies duplicate safety rules to a decision.
// Safe-delete duplicates are promoted to delete; all other duplicates are
// coerced to review.
func coerceDuplicateDecision(decision llm.Decision, src string, cfg *config.Config, isDuplicate bool) llm.Decision {
	if !isDuplicate {
		return decision
	}
	if actions.MatchesPatterns(src, cfg.SafeDeletePatterns) {
		decision.Action = "delete"
		decision.Reason += "; duplicate matches safe delete pattern"
	} else if decision.Action != "review" {
		decision.Action = "review"
		decision.Reason += "; duplicate detected"
	}
	return decision
}

// applyFile applies a decision to a single file and builds a processResult.
func applyFile(src, fileHash string, isDuplicate bool, decision llm.Decision, metrics llm.Metrics, runID string) processResult {
	result, err := applyDecision(decision, src, fileHash, cfg, db, isDuplicate, processFS, runID, metrics, cfg.Force)
	originalName := filepath.Base(src)
	if err != nil {
		slog.Error("apply failed", "path", src, "error", err)
		return processResult{
			Path:         src,
			Category:     decision.Category,
			Tags:         processTags(decision),
			Action:       decision.Action,
			Result:       "",
			OriginalName: originalName,
			OK:           false,
			Error:        err.Error(),
		}
	}
	slog.Info("processed",
		"src", src,
		"result", result,
		"category", decision.Category,
		"action", decision.Action,
		"reason", decision.Reason,
	)
	newName := ""
	if result != "trash" && filepath.Base(result) != originalName {
		newName = filepath.Base(result)
	}
	return processResult{
		Path:             src,
		Category:         decision.Category,
		Tags:             processTags(decision),
		Action:           decision.Action,
		Result:           result,
		OriginalName:     originalName,
		NewName:          newName,
		OK:               true,
		DurationMs:       metrics.DurationMs,
		PromptTokens:     metrics.PromptTokens,
		CompletionTokens: metrics.CompletionTokens,
		TotalTokens:      metrics.TotalTokens,
		TokensPerSec:     metrics.TokensPerSec,
		ContextSize:      metrics.ContextSize,
	}
}

// directoryDecisionForPath returns a DirectoryDecision for a directory input.
// It uses a pre-computed decision from scan when available, otherwise checks
// for project markers and falls back to LLM classification.
func directoryDecisionForPath(ctx context.Context, src string, in processInput) (llm.DirectoryDecision, llm.Metrics) {
	if in.dirDecision != nil {
		return *in.dirDecision, llm.Metrics{}
	}
	meta, err := directory.Gather(src, cfg)
	if err == nil && len(meta.Markers) > 0 {
		return llm.DirectoryDecision{
			Recommendation: "archive",
			Action:         "move",
			Reason:         fmt.Sprintf("project marker(s): %s", strings.Join(meta.Markers, ", ")),
		}, llm.Metrics{}
	}
	return classifyDirectory(ctx, src, cfg)
}

// directoryDestinationDir returns the destination directory used to serialize
// directory moves. It mirrors the resolution order in actions.ApplyDirectory
// without unique-name expansion.
func directoryDestinationDir(decision llm.DirectoryDecision, cfg *config.Config) string {
	if decision.Destination != "" {
		return filepath.Dir(decision.Destination)
	}
	if cfg.ProjectDirCategory != "" {
		if catDir, ok := cfg.Categories[cfg.ProjectDirCategory]; ok {
			return catDir
		}
	}
	return cfg.ReviewDir
}

// applyDirectory carries out a DirectoryDecision and builds a processResult.
func applyDirectory(src string, decision llm.DirectoryDecision, metrics llm.Metrics, runID string, dirLocks *dirLockMap) processResult {
	destDir := directoryDestinationDir(decision, cfg)
	unlock := dirLocks.lock(destDir)
	dest, err := applyDirectoryFunc(decision, src, cfg, db, processFS, runID)
	unlock()

	res := processResult{
		Path:             src,
		Kind:             "directory",
		Recommendation:   decision.Recommendation,
		Category:         decision.Category,
		Tags:             decision.Tags,
		Action:           "move",
		Result:           dest,
		OriginalName:     filepath.Base(src),
		OK:               err == nil,
		Reason:           decision.Reason,
		DurationMs:       metrics.DurationMs,
		PromptTokens:     metrics.PromptTokens,
		CompletionTokens: metrics.CompletionTokens,
		TotalTokens:      metrics.TotalTokens,
		TokensPerSec:     metrics.TokensPerSec,
		ContextSize:      metrics.ContextSize,
	}
	if err != nil {
		res.Error = err.Error()
	}
	return res
}

// recommendDirectory builds a read-only processResult for a directory.
func recommendDirectory(src string, decision llm.DirectoryDecision, metrics llm.Metrics) processResult {
	return processResult{
		Path:             src,
		Kind:             "directory",
		Recommendation:   decision.Recommendation,
		Category:         decision.Category,
		Tags:             decision.Tags,
		Action:           decision.Recommendation,
		Result:           "-",
		OriginalName:     filepath.Base(src),
		OK:               true,
		Reason:           decision.Reason,
		DurationMs:       metrics.DurationMs,
		PromptTokens:     metrics.PromptTokens,
		CompletionTokens: metrics.CompletionTokens,
		TotalTokens:      metrics.TotalTokens,
		TokensPerSec:     metrics.TokensPerSec,
		ContextSize:      metrics.ContextSize,
	}
}

// classifyDirectory returns a DirectoryDecision for a directory candidate.
// If the classifier does not support directory classification, it falls back
// to a review recommendation.
func classifyDirectory(ctx context.Context, src string, cfg *config.Config) (llm.DirectoryDecision, llm.Metrics) {
	dc, ok := classifier.(llm.DirectoryClassifier)
	if !ok {
		return llm.DirectoryDecision{Recommendation: "review", Reason: "directory classifier not available"}, llm.Metrics{}
	}
	meta, err := directory.Gather(src, cfg)
	if err != nil {
		return llm.DirectoryDecision{Recommendation: "review", Reason: fmt.Sprintf("metadata error: %v", err)}, llm.Metrics{}
	}
	decision, metrics, err := dc.ClassifyDirectory(ctx, meta, cfg)
	if err != nil {
		return llm.DirectoryDecision{Recommendation: "review", Reason: fmt.Sprintf("classification error: %v", err)}, metrics
	}
	return decision, metrics
}

// newRunID returns a deterministic, unique run identifier.
func newRunID() string {
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		// Fall back to a timestamp-only ID if crypto/rand fails.
		return fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	}
	return fmt.Sprintf("%d-%x", time.Now().UTC().UnixNano(), b)
}

// processTags returns the tags to display for a decision, including the
// category and subcategory when present and not already in the LLM tags.
func processTags(d llm.Decision) []string {
	tags := make([]string, len(d.Tags))
	copy(tags, d.Tags)
	if d.Subcategory != "" && !stringSliceContains(tags, d.Subcategory) {
		tags = append([]string{d.Subcategory}, tags...)
	}
	if d.Category != "" && !stringSliceContains(tags, d.Category) {
		tags = append([]string{d.Category}, tags...)
	}
	return tags
}

func stringSliceContains(ss []string, s string) bool {
	for _, item := range ss {
		if item == s {
			return true
		}
	}
	return false
}

// checkAgeRule returns a review Decision when a file matches a configured age
// rule and is older than the rule's threshold.
func checkAgeRule(path string, cfg *config.Config) (llm.Decision, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return llm.Decision{}, false
	}

	ageDays := nowFunc().Sub(info.ModTime()).Hours() / 24
	for _, rule := range cfg.AgeRules {
		if actions.MatchesPatterns(path, []string{rule.Pattern}) {
			if ageDays >= float64(rule.Days) {
				return llm.Decision{
					Category: "Unknown",
					Tags:     []string{"age-rule"},
					Action:   rule.Action,
					Reason:   fmt.Sprintf("age rule %s > %d days", rule.Pattern, rule.Days),
				}, true
			}
		}
	}
	return llm.Decision{}, false
}

// isHidden reports whether path is a hidden macOS file.
func isHidden(path string) bool {
	name := filepath.Base(path)
	if name == "" {
		return false
	}
	if name[0] == '.' {
		return true
	}
	return name == ".localized" || name == ".DS_Store"
}
