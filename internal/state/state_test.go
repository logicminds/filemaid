package state_test

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/logicminds/filemaid/internal/llm"
	"github.com/logicminds/filemaid/internal/state"

	_ "modernc.org/sqlite"
)

func TestOpen_CreatesParentDirAndSchema(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nested", "state.db")

	repo, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer repo.Close()

	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		t.Fatalf("database file was not created: %v", err)
	}

	// Re-opening an existing DB should succeed without error.
	repo2, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("Open existing db failed: %v", err)
	}
	repo2.Close()
}

func TestRecordAndFindByHash(t *testing.T) {
	repo, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer repo.Close()

	cases := []struct {
		name     string
		original string
		final    string
		sha256   string
		category string
		tags     []string
		action   string
		reason   string
	}{
		{
			name:     "move with tags",
			original: "/downloads/report.pdf",
			final:    "/docs/report.pdf",
			sha256:   "abc123",
			category: "Documents",
			tags:     []string{"work", "2024"},
			action:   "move",
			reason:   "classified by LLM",
		},
		{
			name:     "delete without tags",
			original: "/downloads/temp.tmp",
			final:    "",
			sha256:   "def456",
			category: "Temporary",
			tags:     nil,
			action:   "delete",
			reason:   "temporary file",
		},
		{
			name:     "review with empty tags",
			original: "/downloads/unknown.bin",
			final:    "",
			sha256:   "ghi789",
			category: "Unknown",
			tags:     []string{},
			action:   "review",
			reason:   "low confidence",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := repo.Record(tc.original, tc.final, tc.sha256, tc.category, tc.tags, tc.action, tc.reason, "", llm.Metrics{}); err != nil {
				t.Fatalf("Record failed: %v", err)
			}

			got, err := repo.FindByHash(tc.sha256)
			if err != nil {
				t.Fatalf("FindByHash failed: %v", err)
			}
			if got == nil {
				t.Fatal("FindByHash returned nil for recorded hash")
			}

			if got.OriginalPath != tc.original {
				t.Errorf("OriginalPath = %q, want %q", got.OriginalPath, tc.original)
			}
			if got.FinalPath != tc.final {
				t.Errorf("FinalPath = %q, want %q", got.FinalPath, tc.final)
			}
			if got.SHA256 != tc.sha256 {
				t.Errorf("SHA256 = %q, want %q", got.SHA256, tc.sha256)
			}
			if got.Category != tc.category {
				t.Errorf("Category = %q, want %q", got.Category, tc.category)
			}
			wantTags := ""
			if len(tc.tags) > 0 {
				for i, tag := range tc.tags {
					if i > 0 {
						wantTags += ","
					}
					wantTags += tag
				}
			}
			if got.Tags != wantTags {
				t.Errorf("Tags = %q, want %q", got.Tags, wantTags)
			}
			if got.Action != tc.action {
				t.Errorf("Action = %q, want %q", got.Action, tc.action)
			}
			if got.Reason != tc.reason {
				t.Errorf("Reason = %q, want %q", got.Reason, tc.reason)
			}
			if got.CreatedAt == "" {
				t.Error("CreatedAt is empty")
			}
		})
	}
}

func TestFindByHash_MostRecent(t *testing.T) {
	repo, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer repo.Close()

	sha := "dupsha"
	if err := repo.Record("/a/file1.txt", "/b/file1.txt", sha, "A", []string{"a"}, "move", "first", "", llm.Metrics{}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := repo.Record("/a/file2.txt", "/b/file2.txt", sha, "B", []string{"b"}, "move", "second", "", llm.Metrics{}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}

	got, err := repo.FindByHash(sha)
	if err != nil {
		t.Fatalf("FindByHash failed: %v", err)
	}
	if got == nil {
		t.Fatal("FindByHash returned nil")
	}
	if got.OriginalPath != "/a/file2.txt" {
		t.Errorf("most recent OriginalPath = %q, want %q", got.OriginalPath, "/a/file2.txt")
	}
	if got.Category != "B" {
		t.Errorf("most recent Category = %q, want %q", got.Category, "B")
	}
}

func TestFindByHash_NotFound(t *testing.T) {
	repo, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer repo.Close()

	got, err := repo.FindByHash("does-not-exist")
	if err != nil {
		t.Fatalf("FindByHash failed: %v", err)
	}
	if got != nil {
		t.Fatalf("FindByHash = %+v, want nil", got)
	}
}

func TestFakeRepo_RecordAndFindByHash(t *testing.T) {
	fake := state.NewFake()

	if err := fake.Record("/src/a.txt", "/dst/a.txt", "hash1", "Cat", []string{"x", "y"}, "move", "reason", "", llm.Metrics{}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := fake.Record("/src/b.txt", "", "hash2", "Cat", nil, "delete", "reason", "", llm.Metrics{}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}

	got, err := fake.FindByHash("hash1")
	if err != nil {
		t.Fatalf("FindByHash failed: %v", err)
	}
	if got == nil {
		t.Fatal("FindByHash returned nil")
	}
	if got.OriginalPath != "/src/a.txt" || got.Tags != "x,y" || got.Action != "move" {
		t.Errorf("unexpected record: %+v", got)
	}

	if err := fake.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestFakeRepo_FindByHash_MostRecent(t *testing.T) {
	fake := state.NewFake()

	_ = fake.Record("/a/1", "/b/1", "sha", "A", nil, "move", "first", "", llm.Metrics{})
	_ = fake.Record("/a/2", "/b/2", "sha", "B", nil, "move", "second", "", llm.Metrics{})

	got, err := fake.FindByHash("sha")
	if err != nil {
		t.Fatalf("FindByHash failed: %v", err)
	}
	if got == nil || got.OriginalPath != "/a/2" {
		t.Fatalf("most recent record = %+v, want /a/2", got)
	}
}

func TestFakeRepo_FindByHash_NotFound(t *testing.T) {
	fake := state.NewFake()
	got, err := fake.FindByHash("missing")
	if err != nil {
		t.Fatalf("FindByHash failed: %v", err)
	}
	if got != nil {
		t.Fatalf("FindByHash = %+v, want nil", got)
	}
}
func TestFakeRepo_Records_Snapshot(t *testing.T) {
	fake := state.NewFake()
	_ = fake.Record("/a", "/b", "h1", "C", []string{"t"}, "move", "r", "", llm.Metrics{})
	_ = fake.Record("/c", "", "h2", "C", nil, "delete", "r", "", llm.Metrics{})

	records := fake.Records()
	if len(records) != 2 {
		t.Fatalf("len(Records) = %d, want 2", len(records))
	}
	if records[0].SHA256 != "h1" || records[1].SHA256 != "h2" {
		t.Errorf("unexpected records: %+v", records)
	}

	// Mutating the returned snapshot must not affect internal state.
	records[0].SHA256 = "mutated"
	again := fake.Records()
	if again[0].SHA256 != "h1" {
		t.Error("Records returned a reference to internal state")
	}
}

func TestFakeRepo_Close_NoOp(t *testing.T) {
	fake := state.NewFake()
	if err := fake.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

func TestOpen_CreatesIndex(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "filemaid.db")

	repo, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer repo.Close()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db for inspection: %v", err)
	}
	defer db.Close()

	var name string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name='idx_sha256'").Scan(&name)
	if err != nil {
		t.Fatalf("idx_sha256 index not found: %v", err)
	}
	if name != "idx_sha256" {
		t.Fatalf("unexpected index name: %q", name)
	}
}

func TestRecordAndFindDecisionByHash(t *testing.T) {
	repo, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer repo.Close()

	decision := llm.Decision{
		Category:    "Documents",
		Subcategory: "report",
		Tags:        []string{"work", "2024"},
		Action:      "move",
		Destination: "/docs",
		Reason:      "text file",
	}

	if err := repo.RecordDecision("abc123", decision); err != nil {
		t.Fatalf("RecordDecision failed: %v", err)
	}

	got, ok, err := repo.FindDecisionByHash("abc123")
	if err != nil {
		t.Fatalf("FindDecisionByHash failed: %v", err)
	}
	if !ok {
		t.Fatal("expected decision to be found")
	}
	if got.Category != decision.Category {
		t.Errorf("Category = %q, want %q", got.Category, decision.Category)
	}
	if got.Subcategory != decision.Subcategory {
		t.Errorf("Subcategory = %q, want %q", got.Subcategory, decision.Subcategory)
	}
	if len(got.Tags) != len(decision.Tags) || got.Tags[0] != decision.Tags[0] || got.Tags[1] != decision.Tags[1] {
		t.Errorf("Tags = %v, want %v", got.Tags, decision.Tags)
	}
	if got.Action != decision.Action {
		t.Errorf("Action = %q, want %q", got.Action, decision.Action)
	}
	if got.Destination != decision.Destination {
		t.Errorf("Destination = %q, want %q", got.Destination, decision.Destination)
	}
	if got.Reason != decision.Reason {
		t.Errorf("Reason = %q, want %q", got.Reason, decision.Reason)
	}
}

func TestFindDecisionByHash_NotFound(t *testing.T) {
	repo, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer repo.Close()

	_, ok, err := repo.FindDecisionByHash("missing")
	if err != nil {
		t.Fatalf("FindDecisionByHash failed: %v", err)
	}
	if ok {
		t.Error("expected no decision for unknown hash")
	}
}

func TestRecordDecision_OverwritesExisting(t *testing.T) {
	repo, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer repo.Close()

	if err := repo.RecordDecision("h1", llm.Decision{Category: "A", Action: "move"}); err != nil {
		t.Fatalf("RecordDecision failed: %v", err)
	}
	if err := repo.RecordDecision("h1", llm.Decision{Category: "B", Action: "review"}); err != nil {
		t.Fatalf("RecordDecision overwrite failed: %v", err)
	}

	got, ok, err := repo.FindDecisionByHash("h1")
	if err != nil {
		t.Fatalf("FindDecisionByHash failed: %v", err)
	}
	if !ok {
		t.Fatal("expected decision to be found")
	}
	if got.Category != "B" {
		t.Errorf("Category = %q, want B", got.Category)
	}
	if got.Action != "review" {
		t.Errorf("Action = %q, want review", got.Action)
	}
}

func TestFakeRepoDecisionCache(t *testing.T) {
	repo := state.NewFake()

	decision := llm.Decision{Category: "Images", Action: "move", Reason: "cached"}
	if err := repo.RecordDecision("h1", decision); err != nil {
		t.Fatalf("RecordDecision failed: %v", err)
	}

	got, ok, err := repo.FindDecisionByHash("h1")
	if err != nil {
		t.Fatalf("FindDecisionByHash failed: %v", err)
	}
	if !ok {
		t.Fatal("expected decision to be found")
	}
	if got.Category != decision.Category {
		t.Errorf("Category = %q, want %q", got.Category, decision.Category)
	}
}

func TestOpen_CreatesDecisionsTable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "filemaid.db")

	repo, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	repo.Close()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db for inspection: %v", err)
	}
	defer db.Close()

	var name string
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='decisions'").Scan(&name)
	if err != nil {
		t.Fatalf("decisions table not found: %v", err)
	}
	if name != "decisions" {
		t.Fatalf("unexpected table name: %q", name)
	}
}

func TestHistory_ByRunID(t *testing.T) {
	repo, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer repo.Close()

	if err := repo.Record("/a", "/b", "h1", "C", nil, "move", "r", "run-1", llm.Metrics{DurationMs: 100}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := repo.Record("/c", "/d", "h2", "C", nil, "move", "r", "run-2", llm.Metrics{DurationMs: 200}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}

	records, err := repo.History(10, "run-2")
	if err != nil {
		t.Fatalf("History failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].RunID.String != "run-2" {
		t.Errorf("RunID = %q, want run-2", records[0].RunID.String)
	}
	if records[0].LLMDurationMs.Int64 != 200 {
		t.Errorf("LLMDurationMs = %d, want 200", records[0].LLMDurationMs.Int64)
	}
}

func TestFakeRepo_History(t *testing.T) {
	fake := state.NewFake()
	fake.Record("/a", "/b", "h1", "C", nil, "move", "r", "run-1", llm.Metrics{})
	fake.Record("/c", "/d", "h2", "C", nil, "move", "r", "run-2", llm.Metrics{})

	records, err := fake.History(10, "run-1")
	if err != nil {
		t.Fatalf("History failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].RunID.String != "run-1" {
		t.Errorf("RunID = %q, want run-1", records[0].RunID.String)
	}
}

func TestDistinctTags(t *testing.T) {
	repo, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer repo.Close()

	if err := repo.Record("/a", "/b", "h1", "C", []string{"work", "filemaid", "  ", "personal"}, "move", "r", "", llm.Metrics{}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := repo.Record("/c", "/d", "h2", "C", []string{"work", "archive"}, "move", "r", "", llm.Metrics{}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := repo.Record("/e", "/f", "h3", "C", nil, "move", "r", "", llm.Metrics{}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := repo.Record("/g", "/h", "h4", "C", []string{}, "move", "r", "", llm.Metrics{}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}

	got, err := repo.DistinctTags()
	if err != nil {
		t.Fatalf("DistinctTags failed: %v", err)
	}
	want := []string{"archive", "personal", "work"}
	if !slices.Equal(got, want) {
		t.Errorf("DistinctTags() = %v, want %v", got, want)
	}
}

func TestFakeRepo_DistinctTags(t *testing.T) {
	fake := state.NewFake()
	if err := fake.Record("/a", "/b", "h1", "C", []string{"work", "filemaid", "  ", "personal"}, "move", "r", "", llm.Metrics{}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := fake.Record("/c", "/d", "h2", "C", []string{"work", "archive"}, "move", "r", "", llm.Metrics{}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := fake.Record("/e", "/f", "h3", "C", nil, "move", "r", "", llm.Metrics{}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := fake.Record("/g", "/h", "h4", "C", []string{}, "move", "r", "", llm.Metrics{}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}

	got, err := fake.DistinctTags()
	if err != nil {
		t.Fatalf("DistinctTags failed: %v", err)
	}
	want := []string{"archive", "personal", "work"}
	if !slices.Equal(got, want) {
		t.Errorf("DistinctTags() = %v, want %v", got, want)
	}
}
