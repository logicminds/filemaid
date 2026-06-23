package cli

import (
	"encoding/json"
	"fmt"

	"github.com/logicminds/filemaid/internal/state"

	"github.com/spf13/cobra"
)

var (
	historyRun   string
	historyLast  bool
	historyJSON  bool
	historyLimit int
)

func init() {
	historyCmd.Flags().StringVar(&historyRun, "run", "", "filter rows to a specific run ID")
	historyCmd.Flags().BoolVar(&historyLast, "last", false, "show only the most recent run and print a count summary")
	historyCmd.Flags().BoolVar(&historyJSON, "json", false, "output results as JSON")
	historyCmd.Flags().IntVar(&historyLimit, "limit", 100, "maximum rows to return")
	rootCmd.AddCommand(historyCmd)
}

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "Show processed-file history",
	Long:  "Display recent file processing history from the SQLite database, optionally filtered by run ID.",
	RunE: func(cmd *cobra.Command, args []string) error {
		records, runID, err := queryHistory(historyRun, historyLast, historyLimit)
		if err != nil {
			return err
		}

		if historyJSON {
			out, err := json.MarshalIndent(toHistoryRows(records), "", "  ")
			if err != nil {
				return fmt.Errorf("marshal history: %w", err)
			}
			fmt.Println(string(out))
			return nil
		}

		// Print a leading and trailing copy of the run summary so the run ID is
		// visible both at the top (for context) and at the bottom (after scrolling).
		if historyLast && len(records) > 0 {
			fmt.Printf("%d files processed in run %s\n\n", len(records), runID)
		}
		for _, r := range records {
			fmt.Printf("%s  %s\n  category=%s action=%s result=%s\n", r.CreatedAt, collapseHome(r.OriginalPath), r.Category, r.Action, collapseHome(r.FinalPath))
			if r.PromptTokens.Int64 > 0 || r.CompletionTokens.Int64 > 0 {
				fmt.Printf("  tokens=%d/%d (%.1f tok/s) duration=%dms ctx=%d\n", r.PromptTokens.Int64, r.CompletionTokens.Int64, r.TokensPerSec.Float64, r.LLMDurationMs.Int64, r.ContextSize.Int64)
			}
		}
		if historyLast && len(records) > 0 {
			fmt.Printf("\nRun ID: %s (use with `filemaid undo --run %s`)\n", runID, runID)
		}
		return nil
	},
}

// historyRow is a flattened, display-friendly history record.
type historyRow struct {
	ID               int64   `json:"id"`
	OriginalPath     string  `json:"original_path"`
	FinalPath        string  `json:"final_path"`
	SHA256           string  `json:"sha256"`
	Category         string  `json:"category"`
	Tags             string  `json:"tags"`
	Action           string  `json:"action"`
	Reason           string  `json:"reason"`
	CreatedAt        string  `json:"created_at"`
	RunID            string  `json:"run_id"`
	DurationMs       int64   `json:"duration_ms"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	TokensPerSec     float64 `json:"tokens_per_sec"`
	ContextSize      int     `json:"context_size"`
}

func toHistoryRows(records []state.Record) []historyRow {
	out := make([]historyRow, len(records))
	for i, r := range records {
		out[i] = historyRow{
			ID:               r.ID,
			OriginalPath:     r.OriginalPath,
			FinalPath:        r.FinalPath,
			SHA256:           r.SHA256,
			Category:         r.Category,
			Tags:             r.Tags,
			Action:           r.Action,
			Reason:           r.Reason,
			CreatedAt:        r.CreatedAt,
			RunID:            r.RunID.String,
			DurationMs:       r.LLMDurationMs.Int64,
			PromptTokens:     int(r.PromptTokens.Int64),
			CompletionTokens: int(r.CompletionTokens.Int64),
			TotalTokens:      int(r.TotalTokens.Int64),
			TokensPerSec:     r.TokensPerSec.Float64,
			ContextSize:      int(r.ContextSize.Int64),
		}
	}
	return out
}

// queryHistory returns history records and, when last is true, the runID they belong to.
func queryHistory(runID string, last bool, limit int) ([]state.Record, string, error) {
	if limit <= 0 {
		limit = 100
	}

	if last && runID == "" {
		all, err := db.History(1, "")
		if err != nil {
			return nil, "", err
		}
		if len(all) == 0 {
			return nil, "", nil
		}
		runID = all[0].RunID.String
	}

	records, err := db.History(limit, runID)
	if err != nil {
		return nil, "", err
	}
	return records, runID, nil
}
