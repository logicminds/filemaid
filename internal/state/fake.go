package state

import (
	"database/sql"
	"sort"
	"strings"
	"sync"

	"github.com/logicminds/filemaid/internal/llm"
)

// FakeRepo is an in-memory implementation of Repo for tests.
type FakeRepo struct {
	mu        sync.Mutex
	records   []Record
	decisions map[string]llm.Decision
}

// NewFake returns a new empty FakeRepo.
func NewFake() *FakeRepo {
	return &FakeRepo{}
}

// Record stores a history row in memory.
func (f *FakeRepo) Record(original, final, sha256, category string, tags []string, action, reason string, runID string, metrics llm.Metrics) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	tagsStr := ""
	if len(tags) > 0 {
		for i, t := range tags {
			if i > 0 {
				tagsStr += ","
			}
			tagsStr += t
		}
	}

	f.records = append(f.records, Record{
		ID:               int64(len(f.records) + 1),
		OriginalPath:     original,
		FinalPath:        final,
		SHA256:           sha256,
		Category:         category,
		Tags:             tagsStr,
		Action:           action,
		Reason:           reason,
		RunID:            sql.NullString{String: runID, Valid: runID != ""},
		LLMDurationMs:    sql.NullInt64{Int64: metrics.DurationMs, Valid: metrics.DurationMs != 0},
		PromptTokens:     sql.NullInt64{Int64: int64(metrics.PromptTokens), Valid: metrics.PromptTokens != 0},
		CompletionTokens: sql.NullInt64{Int64: int64(metrics.CompletionTokens), Valid: metrics.CompletionTokens != 0},
		TotalTokens:      sql.NullInt64{Int64: int64(metrics.TotalTokens), Valid: metrics.TotalTokens != 0},
		TokensPerSec:     sql.NullFloat64{Float64: metrics.TokensPerSec, Valid: metrics.TokensPerSec != 0},
		ContextSize:      sql.NullInt64{Int64: int64(metrics.ContextSize), Valid: metrics.ContextSize != 0},
	})
	return nil
}

// FindByHash returns the most recent in-memory record matching sha256.
func (f *FakeRepo) FindByHash(sha256 string) (*Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i := len(f.records) - 1; i >= 0; i-- {
		if f.records[i].SHA256 == sha256 {
			r := f.records[i]
			return &r, nil
		}
	}
	return nil, nil
}

// Close is a no-op for the fake repository.
func (f *FakeRepo) Close() error {
	return nil
}

// FindDecisionByHash returns a cached decision for sha256, if one exists.
func (f *FakeRepo) FindDecisionByHash(sha256 string) (llm.Decision, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.decisions == nil {
		return llm.Decision{}, false, nil
	}
	d, ok := f.decisions[sha256]
	return d, ok, nil
}

// RecordDecision stores a decision keyed by sha256.
func (f *FakeRepo) RecordDecision(sha256 string, decision llm.Decision) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.decisions == nil {
		f.decisions = make(map[string]llm.Decision)
	}
	f.decisions[sha256] = decision
	return nil
}

// Decisions returns a snapshot of all cached decisions.
func (f *FakeRepo) Decisions() map[string]llm.Decision {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make(map[string]llm.Decision, len(f.decisions))
	for k, v := range f.decisions {
		out[k] = v
	}
	return out
}

// Records returns a snapshot of all stored records.
func (f *FakeRepo) Records() []Record {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]Record, len(f.records))
	copy(out, f.records)
	return out
}

// DistinctTags returns all unique tags stored across in-memory records,
// excluding the reserved 'filemaid' tag and any empty or whitespace-only values.
func (f *FakeRepo) DistinctTags() ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	seen := make(map[string]struct{})
	for _, r := range f.records {
		if r.Tags == "" {
			continue
		}
		for _, t := range strings.Split(r.Tags, ",") {
			t = strings.TrimSpace(t)
			if t == "" || t == "filemaid" {
				continue
			}
			seen[t] = struct{}{}
		}
	}

	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}

// History returns up to limit in-memory records ordered by insertion order reversed.
func (f *FakeRepo) History(limit int, runID string) ([]Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if limit <= 0 {
		limit = 100
	}
	out := make([]Record, 0, limit)
	for i := len(f.records) - 1; i >= 0; i-- {
		r := f.records[i]
		if runID != "" && r.RunID.String != runID {
			continue
		}
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}
