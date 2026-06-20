package cli

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/logicminds/filemaid/internal/cleaners"
	"github.com/logicminds/filemaid/internal/log"

	"github.com/spf13/cobra"
)

var (
	cleanupDryRun bool
	cleanupFormat string
)

var (
	// cleanerRegistry returns the list of cleaners to run. Tests may replace it
	// with a deterministic registry.
	cleanerRegistry = cleaners.Registry
)

func init() {
	cleanupCmd.Flags().BoolVar(&cleanupDryRun, "dry-run", false, "do not actually clean anything")
	cleanupCmd.Flags().StringVar(&cleanupFormat, "format", "table", "output format (table|json)")
	cleanupCmd.Flags().BoolVar(&processQuiet, "quiet", false, "suppress log output to stderr")
	rootCmd.AddCommand(cleanupCmd)
}

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Run dev artifact cleaners",
	Long:  "Run the configured dev-artifact and review-queue cleaners (docker, npm, cargo, pip, brew, xcode, review).",
	RunE: func(cmd *cobra.Command, args []string) error {
		quiet := processQuiet || (cleanupFormat != "json" && isTerminal(os.Stdout))
		if quiet {
			log.SetStderrEnabled(false)
			defer log.SetStderrEnabled(true)
		}

		results := runCleanup(cleanupDryRun)
		out, err := formatCleanupResults(results, cleanupFormat)
		if err != nil {
			return err
		}
		fmt.Println(out)
		return nil
	},
}

// runCleanup executes the cleaner registry and returns the results. It mirrors
// the Python run_cleanup behaviour: filter by allowed_cleaners, skip cleaners
// that cannot run or are disabled, and run the rest.
func runCleanup(dryRun bool) []cleaners.CleanupResult {
	allowed := make(map[string]bool)
	for _, name := range cfg.AllowedCleaners {
		allowed[name] = true
	}

	var results []cleaners.CleanupResult
	for _, cleaner := range cleanerRegistry() {
		name := cleaner.Name
		if !allowed[name] {
			slog.Info(fmt.Sprintf("%s: not in allowed_cleaners whitelist", name))
			results = append(results, cleaners.CleanupResult{
				Name:   name,
				Status: "not_allowed",
				Detail: "not in allowed_cleaners whitelist",
			})
			continue
		}
		if !cleaner.CanRun() {
			slog.Info(fmt.Sprintf("%s: skipped (not installed)", name))
			results = append(results, cleaners.CleanupResult{
				Name:   name,
				Status: "not_installed",
				Detail: "not installed",
			})
			continue
		}
		if devCfg, ok := cfg.DevCleanup[name]; ok && !devCfg.Enabled {
			slog.Info(fmt.Sprintf("%s: disabled in config", name))
			results = append(results, cleaners.CleanupResult{
				Name:   name,
				Status: "disabled",
				Detail: "disabled in config",
			})
			continue
		}

		result := cleaner.Run(dryRun, cfg)
		if result.Name == "" {
			result.Name = name
		}
		detail := result.Status
		if result.Detail != "" {
			detail = strings.Split(result.Detail, "\n")[0]
		}
		slog.Info(fmt.Sprintf("%s: %s", result.Name, detail))
		results = append(results, result)
	}
	return results
}

// formatCleanupResults renders cleaner results as a table or JSON.
func formatCleanupResults(results []cleaners.CleanupResult, format string) (string, error) {
	if format == "json" {
		out, err := json.MarshalIndent(results, "", "  ")
		return string(out), err
	}
	return formatCleanupTable(results), nil
}

// formatCleanupTable renders cleaner results as an ASCII table.
func formatCleanupTable(results []cleaners.CleanupResult) string {
	useColor := isTerminal(os.Stdout)
	headers := []string{"Cleaner", "Status", "Space saved", "Details"}
	rows := make([][]string, 0, len(results))
	for _, r := range results {
		saved := "-"
		if r.SavedHuman != "" {
			saved = r.SavedHuman
		} else if r.Saved != nil {
			saved = cleaners.Humanize(*r.Saved)
		}
		detail := r.Status
		if r.Detail != "" {
			detail = strings.Split(r.Detail, "\n")[0]
		}
		status := cleanupStatusSymbol(r.Status, useColor)
		rows = append(rows, []string{r.Name, status, saved, detail})
	}
	return renderTable(headers, rows)
}

func cleanupStatusSymbol(status string, useColor bool) string {
	switch status {
	case "success", "ok":
		return colorize("✓", colorGreen, useColor)
	case "disabled", "not_installed", "not_allowed":
		return colorize("-", colorCyan, useColor)
	default:
		return colorize("⚠", colorYellow, useColor)
	}
}
