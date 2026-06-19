package cli

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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

// applierFunc matches the signature of actions.Apply so it can be swapped in tests.
type applierFunc func(decision llm.Decision, src string, cfg *config.Config, db state.Repo, isDuplicate bool, fs actions.FS) (string, error)

func init() {
	rootCmd.AddCommand(processCmd)
}

var processCmd = &cobra.Command{
	Use:   "process <paths...>",
	Short: "Classify and apply decisions to files",
	Long:  "Process one or more files: classify them with the configured LLM and apply the resulting move/tag/delete/review decision.",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return processPaths(args)
	},
}

// processPaths classifies and applies decisions to each path. It mirrors the
// Python process_paths behaviour: skip non-existent, non-file, hidden, and
// out-of-allowed files; honour age rules; detect duplicates; coerce unsafe
// deletes to review; and log the result.
func processPaths(paths []string) error {
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

		result, err := applyDecision(decision, src, cfg, db, isDuplicate, processFS)
		if err != nil {
			slog.Error("apply failed", "path", src, "error", err)
			continue
		}

		slog.Info("processed",
			"src", src,
			"result", result,
			"category", decision.Category,
			"action", decision.Action,
			"reason", decision.Reason,
		)
	}
	return nil
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
