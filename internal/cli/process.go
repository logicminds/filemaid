package cli

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/logicminds/filemaid/internal/actions"
	"github.com/logicminds/filemaid/internal/config"
	"github.com/logicminds/filemaid/internal/llm"
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
var (
	processFormat string
	processJSON   bool
)

// applierFunc matches the signature of actions.Apply so it can be swapped in tests.
type applierFunc func(decision llm.Decision, src string, fileHash string, cfg *config.Config, db state.Repo, isDuplicate bool, fs actions.FS) (string, error)

func init() {
	processCmd.Flags().StringVar(&processFormat, "format", "table", "output format (table|json)")
	processCmd.Flags().BoolVar(&processJSON, "json", false, "output results as JSON (shorthand for --format json)")
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
		results, err := processPaths(args)
		if err != nil {
			return err
		}
		if len(results) == 0 {
			fmt.Println("No files processed.")
			return nil
		}
		format := processFormat
		if processJSON {
			format = "json"
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
	Path     string   `json:"path"`
	Category string   `json:"category"`
	Tags     []string `json:"tags"`
	Action   string   `json:"action"`
	Result   string   `json:"result"`
	OK       bool     `json:"ok"`
	Error    string   `json:"error,omitempty"`
}

// formatProcessResults renders process results as a table or JSON.
func formatProcessResults(results []processResult, format string) (string, error) {
	if format == "json" {
		out, err := json.MarshalIndent(results, "", "  ")
		return string(out), err
	}
	return formatProcessTable(results), nil
}

// formatProcessTable renders process results as an ASCII table.
func formatProcessTable(results []processResult) string {
	headers := []string{"File", "Category", "Tags", "Action", "Result", "Status"}
	rows := make([][]string, 0, len(results))
	for _, r := range results {
		status := "❌"
		if r.OK {
			status = "✅"
		}
		tags := strings.Join(r.Tags, ", ")
		rows = append(rows, []string{r.Path, r.Category, tags, r.Action, r.Result, status})
	}
	return renderTable(headers, rows)
}

// processPaths classifies and applies decisions to each path. It mirrors the
// Python process_paths behaviour: skip non-existent, non-file, hidden, and
// out-of-allowed files; honour age rules; detect duplicates; coerce unsafe
// deletes to review; and log the result.
// processPaths classifies and applies decisions to each path. It mirrors the
// Python process_paths behaviour: skip non-existent, non-file, hidden, and
// out-of-allowed files; honour age rules; detect duplicates; coerce unsafe
// deletes to review; log the result; and return a displayable result per file.
func processPaths(paths []string) ([]processResult, error) {
	var results []processResult
	for _, raw := range paths {
		src, err := filepath.Abs(raw)
		if err != nil {
			slog.Warn("path normalization failed", "path", raw, "error", err)
			continue
		}

		info, err := os.Stat(src)
		if err != nil {
			slog.Warn("path does not exist", "path", raw)
			continue
		}
		if !info.Mode().IsRegular() {
			slog.Warn("not a file", "path", src)
			continue
		}
		if isHidden(src) {
			slog.Info("skipping hidden file", "path", src)
			continue
		}
		if len(cfg.AllowedDirs) > 0 && !actions.WithinAllowed(src, cfg.AllowedDirs) {
			slog.Warn("skipping file outside allowed dirs", "path", src)
			continue
		}

		fileHash, err := actions.ComputeHash(src)
		if err != nil {
			slog.Warn("hash failed", "path", src, "error", err)
			continue
		}

		rec, err := db.FindByHash(fileHash)
		if err != nil {
			slog.Warn("duplicate lookup failed", "path", src, "error", err)
		}
		isDuplicate := rec != nil && processFS.Exists(rec.FinalPath)

		decision, matched := checkAgeRule(src, cfg)
		if !matched {
			fmt.Fprintf(os.Stderr, "Classifying %s...\n", src)
			decision, err = classifier.Classify(src, cfg)
			if err != nil {
				slog.Warn("classification failed", "path", src, "error", err)
				decision = llm.NewDecision()
			}
		}

		if isDuplicate {
			if actions.MatchesPatterns(src, cfg.SafeDeletePatterns) {
				decision.Action = "delete"
				decision.Reason += "; duplicate matches safe delete pattern"
			} else if decision.Action != "review" {
				decision.Action = "review"
				decision.Reason += "; duplicate detected"
			}
		}

		result, err := applyDecision(decision, src, fileHash, cfg, db, isDuplicate, processFS)
		if err != nil {
			slog.Error("apply failed", "path", src, "error", err)
			results = append(results, processResult{
				Path:     src,
				Category: decision.Category,
				Tags:     processTags(decision),
				Action:   decision.Action,
				Result:   "",
				OK:       false,
				Error:    err.Error(),
			})
			continue
		}

		slog.Info("processed",
			"src", src,
			"result", result,
			"category", decision.Category,
			"action", decision.Action,
			"reason", decision.Reason,
		)
		results = append(results, processResult{
			Path:     src,
			Category: decision.Category,
			Tags:     processTags(decision),
			Action:   decision.Action,
			Result:   result,
			OK:       true,
		})
	}
	return results, nil
}

// processTags returns the tags to display for a decision, including the
// subcategory when present.
func processTags(d llm.Decision) []string {
	tags := make([]string, len(d.Tags))
	copy(tags, d.Tags)
	if d.Subcategory == "" {
		return tags
	}
	for _, t := range tags {
		if t == d.Subcategory {
			return tags
		}
	}
	return append([]string{d.Subcategory}, tags...)
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
