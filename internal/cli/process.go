package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/logicminds/filemaid/internal/actions"
	"github.com/logicminds/filemaid/internal/config"
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

	// nowFunc returns the current time. Tests may replace it with a fixed clock.
	nowFunc = time.Now
)

// defaultProcessWorkers limits how many files are classified and applied
// concurrently. Classification is the expensive LLM-bound step; apply is
// mutex-serialized to keep filesystem and history operations deterministic.
const defaultProcessWorkers = 4

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
)

// applierFunc matches the signature of actions.Apply so it can be swapped in tests.
type applierFunc func(decision llm.Decision, src string, fileHash string, cfg *config.Config, db state.Repo, isDuplicate bool, fs actions.FS, runID string, metrics llm.Metrics) (string, error)

func init() {
	processCmd.Flags().StringVar(&processFormat, "format", "table", "output format (table|human|json)")
	processCmd.Flags().BoolVar(&processJSON, "json", false, "output results as JSON (shorthand for --format json)")
	processCmd.Flags().BoolVar(&processQuiet, "quiet", false, "suppress log output to stderr")
	rootCmd.AddCommand(processCmd)
}

var processCmd = &cobra.Command{
	Use:   "process <paths...>",
	Short: "Classify and apply decisions to files",
	Long:  "Process one or more files: classify them with the configured LLM and apply the resulting move/tag/delete/review decision.",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := classifier.Validate(cfg); err != nil {
			return fmt.Errorf("model validation failed: %w", err)
		}
		if c, ok := classifier.(*llm.Client); ok {
			c.SetDecisionCache(db)
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

		runID := newRunID()
		results, err := processPaths(ctx, args, runID)
		if err != nil {
			return err
		}
		if err := regenerateSmartFolders(); err != nil {
			slog.Warn("smart folder regeneration failed", "error", err)
		}
		if len(results) == 0 {
			fmt.Println("No files processed.")
			return nil
		}
		out, err := formatProcessResults(results, format)
		if err != nil {
			return err
		}
		fmt.Println(out)
		return nil
	},
}

// processResult captures the outcome of processing a single file for display.
type processResult struct {
	Path             string   `json:"path"`
	Category         string   `json:"category"`
	Tags             []string `json:"tags"`
	Action           string   `json:"action"`
	Result           string   `json:"result"`
	OK               bool     `json:"ok"`
	Error            string   `json:"error,omitempty"`
	DurationMs       int64    `json:"duration_ms"`
	PromptTokens     int      `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	TotalTokens      int      `json:"total_tokens"`
	TokensPerSec     float64  `json:"tokens_per_sec"`
	ContextSize      int      `json:"context_size"`
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
	maxResultLen = 48
)

// formatProcessTable renders process results as a compact ASCII table.
func formatProcessTable(results []processResult) string {
	useColor := isTerminal(os.Stdout)
	headers := []string{"File", "Category", "Tags", "Action", "Result", "Status"}
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
		rows = append(rows, []string{
			truncatePath(collapseHome(r.Path), maxFileLen),
			truncateTags([]string{category}, maxCatLen),
			truncateTags(r.Tags, maxTagsLen),
			truncateTags([]string{action}, maxActionLen),
			truncatePath(collapseHome(r.Result), maxResultLen),
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
		lines = append(lines, fmt.Sprintf("%s  %s", status, colorize(name, colorBold, useColor)))
		if r.Category != "" {
			lines = append(lines, fmt.Sprintf("   Category: %s", r.Category))
		}
		if len(r.Tags) > 0 {
			lines = append(lines, fmt.Sprintf("   Tags:     %s", strings.Join(r.Tags, ", ")))
		}
		if r.Action != "" {
			lines = append(lines, fmt.Sprintf("   Action:   %s", r.Action))
		}
		if r.Result != "" {
			lines = append(lines, fmt.Sprintf("   Result:   %s", collapseHome(r.Result)))
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
	for _, r := range results {
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
	parts := []string{fmt.Sprintf("%d file%s processed", len(results), plural(len(results)))}
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

// processPaths classifies and applies decisions to each path. It mirrors the
// Python process_paths behaviour: skip non-existent, non-file, hidden, and
// out-of-allowed files; honour age rules; detect duplicates; coerce unsafe
// deletes to review; log the result; and return a displayable result per file.
func processPaths(ctx context.Context, paths []string, runID string) ([]processResult, error) {
	// Preprocess sequentially so skipping, hash/duplicate detection, age-rule
	// matching and their logs remain deterministic and ordered. Skipped paths
	// still produce a result so the final table is complete.
	results := make([]processResult, len(paths))
	items := make([]processItem, 0, len(paths))
	seenHashes := make(map[string]bool)
	for i, raw := range paths {
		src, err := filepath.Abs(raw)
		if err != nil {
			results[i] = skipResult(raw, fmt.Sprintf("path normalization failed: %v", err))
			slog.Warn("path normalization failed", "path", raw, "error", err)
			continue
		}

		info, err := os.Stat(src)
		if err != nil {
			results[i] = skipResult(src, "path does not exist")
			slog.Warn("path does not exist", "path", raw)
			continue
		}
		if !info.Mode().IsRegular() {
			results[i] = skipResult(src, "not a regular file")
			slog.Warn("not a file", "path", src)
			continue
		}
		if isHidden(src) {
			results[i] = skipResult(src, "hidden file")
			slog.Info("skipping hidden file", "path", src)
			continue
		}
		if len(cfg.AllowedDirs) > 0 && !actions.WithinAllowed(src, cfg.AllowedDirs) {
			results[i] = skipResult(src, "outside allowed dirs")
			slog.Warn("skipping file outside allowed dirs", "path", src)
			continue
		}

		fileHash, err := actions.ComputeHash(src)
		if err != nil {
			results[i] = skipResult(src, fmt.Sprintf("hash failed: %v", err))
			slog.Warn("hash failed", "path", src, "error", err)
			continue
		}

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
		})
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, defaultProcessWorkers)
	var applyMu sync.Mutex

	groups := groupItemsByModel(items)
	for _, model := range sortedModelNames(groups) {
		groupItems := groups[model]
		groupCfg := *cfg
		groupCfg.Model = model

		for _, item := range groupItems {
			wg.Add(1)
			go func(item processItem, cfg *config.Config) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				decision := item.ageDecision
				metrics := llm.Metrics{}
				if !item.ageMatched {
					var err error
					decision, metrics, err = classifier.Classify(ctx, item.src, item.fileHash, cfg)
					if err != nil {
						slog.Warn("classification failed", "path", item.src, "error", err)
						decision = llm.NewDecision()
					}
				}

				decision = coerceDuplicateDecision(decision, item.src, cfg, item.isDuplicate)

				applyMu.Lock()
				results[item.pos] = applyFile(item.src, item.fileHash, item.isDuplicate, decision, metrics, runID)
				applyMu.Unlock()
			}(item, &groupCfg)
		}
		wg.Wait()
	}
	return results, nil
}

// skipResult builds a processResult for a path that was skipped during preprocessing.
func skipResult(path string, reason string) processResult {
	return processResult{
		Path:   path,
		Action: "skip",
		Result: "-",
		OK:     false,
		Error:  reason,
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
	result, err := applyDecision(decision, src, fileHash, cfg, db, isDuplicate, processFS, runID, metrics)
	if err != nil {
		slog.Error("apply failed", "path", src, "error", err)
		return processResult{
			Path:     src,
			Category: decision.Category,
			Tags:     processTags(decision),
			Action:   decision.Action,
			Result:   "",
			OK:       false,
			Error:    err.Error(),
		}
	}
	slog.Info("processed",
		"src", src,
		"result", result,
		"category", decision.Category,
		"action", decision.Action,
		"reason", decision.Reason,
	)
	return processResult{
		Path:             src,
		Category:         decision.Category,
		Tags:             processTags(decision),
		Action:           decision.Action,
		Result:           result,
		OK:               true,
		DurationMs:       metrics.DurationMs,
		PromptTokens:     metrics.PromptTokens,
		CompletionTokens: metrics.CompletionTokens,
		TotalTokens:      metrics.TotalTokens,
		TokensPerSec:     metrics.TokensPerSec,
		ContextSize:      metrics.ContextSize,
	}
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
