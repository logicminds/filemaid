package state

import (
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
func (f *FakeRepo) Record(original, final, sha256, category string, tags []string, action, reason string) error {
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
		ID:           int64(len(f.records) + 1),
		OriginalPath: original,
		FinalPath:    final,
		SHA256:       sha256,
		Category:     category,
		Tags:         tagsStr,
		Action:       action,
		Reason:       reason,
		CreatedAt:    "",
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
