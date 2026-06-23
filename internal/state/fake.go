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
	mu                 sync.Mutex
	records            []Record
	decisions          map[string]llm.Decision
	directoryDecisions map[string]llm.DirectoryDecision
}

// NewFake returns a new empty FakeRepo.
func NewFake() *FakeRepo {
	return &FakeRepo{}
}

// Record stores a history row in memory.
func (f *FakeRepo) Record(input RecordInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	tagsStr := ""
	if len(input.Tags) > 0 {
		for i, t := range input.Tags {
			if i > 0 {
				tagsStr += ","
			}
			tagsStr += t
		}
	}

	f.records = append(f.records, Record{
		ID:               int64(len(f.records) + 1),
		OriginalPath:     input.OriginalPath,
		FinalPath:        input.FinalPath,
		SHA256:           input.SHA256,
		Category:         input.Category,
		Tags:             tagsStr,
		Action:           input.Action,
		Reason:           input.Reason,
		RunID:            sql.NullString{String: input.RunID, Valid: input.RunID != ""},
		LLMDurationMs:    sql.NullInt64{Int64: input.Metrics.DurationMs, Valid: input.Metrics.DurationMs != 0},
		PromptTokens:     sql.NullInt64{Int64: int64(input.Metrics.PromptTokens), Valid: input.Metrics.PromptTokens != 0},
		CompletionTokens: sql.NullInt64{Int64: int64(input.Metrics.CompletionTokens), Valid: input.Metrics.CompletionTokens != 0},
		TotalTokens:      sql.NullInt64{Int64: int64(input.Metrics.TotalTokens), Valid: input.Metrics.TotalTokens != 0},
		TokensPerSec:     sql.NullFloat64{Float64: input.Metrics.TokensPerSec, Valid: input.Metrics.TokensPerSec != 0},
		ContextSize:      sql.NullInt64{Int64: int64(input.Metrics.ContextSize), Valid: input.Metrics.ContextSize != 0},
		OriginalName:     input.OriginalName,
		NewName:          input.NewName,
		NameQuality:      sql.NullFloat64{Float64: input.NameQuality, Valid: input.NameQuality != 0},
		MediaKind:        input.MediaKind,
		PerceptualHash:   input.PerceptualHash,
		AVSignature:      input.AVSignature,
		TextSignature:    input.TextSignature,
		HashAlgorithm:    input.HashAlgorithm,
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

// FindDuplicatesByHash returns all in-memory records matching sha256, ordered by most recent first.
func (f *FakeRepo) FindDuplicatesByHash(sha256 string) ([]Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []Record
	for i := len(f.records) - 1; i >= 0; i-- {
		if f.records[i].SHA256 == sha256 {
			out = append(out, f.records[i])
		}
	}
	return out, nil
}

// FindSimilarByFingerprints returns in-memory records that share any of the
// provided non-empty fingerprint signatures, ordered by most recent first.
func (f *FakeRepo) FindSimilarByFingerprints(perceptualHash, avSignature, textSignature string) ([]Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []Record
	for i := len(f.records) - 1; i >= 0; i-- {
		r := f.records[i]
		match := (perceptualHash != "" && r.PerceptualHash == perceptualHash) ||
			(avSignature != "" && r.AVSignature == avSignature) ||
			(textSignature != "" && r.TextSignature == textSignature)
		if match {
			out = append(out, r)
		}
	}
	return out, nil
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

// FindDirectoryDecision returns a cached directory decision for key, if one exists.
func (f *FakeRepo) FindDirectoryDecision(key string) (llm.DirectoryDecision, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.directoryDecisions == nil {
		return llm.DirectoryDecision{}, false, nil
	}
	d, ok := f.directoryDecisions[key]
	return d, ok, nil
}

// RecordDirectoryDecision stores a directory decision keyed by key.
func (f *FakeRepo) RecordDirectoryDecision(key string, decision llm.DirectoryDecision) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.directoryDecisions == nil {
		f.directoryDecisions = make(map[string]llm.DirectoryDecision)
	}
	f.directoryDecisions[key] = decision
	return nil
}

// DirectoryDecisions returns a snapshot of all cached directory decisions.
func (f *FakeRepo) DirectoryDecisions() map[string]llm.DirectoryDecision {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make(map[string]llm.DirectoryDecision, len(f.directoryDecisions))
	for k, v := range f.directoryDecisions {
		out[k] = v
	}
	return out
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

// HistoryByRunID returns all in-memory records for the given run, excluding undo rows.
func (f *FakeRepo) HistoryByRunID(runID string) ([]Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]Record, 0)
	for i := len(f.records) - 1; i >= 0; i-- {
		r := f.records[i]
		if r.RunID.String != runID || r.Action == "undo" {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// HistoryByFinalPath returns the most recent in-memory record with the given
// final path, excluding undo rows.
func (f *FakeRepo) HistoryByFinalPath(finalPath string) (*Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i := len(f.records) - 1; i >= 0; i-- {
		r := f.records[i]
		if r.FinalPath == finalPath && r.Action != "undo" {
			return &r, nil
		}
	}
	return nil, nil
}

// LastRunID returns the most recent run_id that has move actions, ignoring undo
// rows and trashed items.
func (f *FakeRepo) LastRunID() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i := len(f.records) - 1; i >= 0; i-- {
		r := f.records[i]
		if r.Action != "undo" && r.FinalPath != "trash" {
			return r.RunID.String, nil
		}
	}
	return "", nil
}
