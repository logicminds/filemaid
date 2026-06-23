package state_test

import (
	"bytes"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
			if err := repo.Record(state.RecordInput{OriginalPath: tc.original, FinalPath: tc.final, SHA256: tc.sha256, Category: tc.category, Tags: tc.tags, Action: tc.action, Reason: tc.reason, RunID: "", Metrics: llm.Metrics{}}); err != nil {
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
	if err := repo.Record(state.RecordInput{OriginalPath: "/a/file1.txt", FinalPath: "/b/file1.txt", SHA256: sha, Category: "A", Tags: []string{"a"}, Action: "move", Reason: "first", RunID: "", Metrics: llm.Metrics{}}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := repo.Record(state.RecordInput{OriginalPath: "/a/file2.txt", FinalPath: "/b/file2.txt", SHA256: sha, Category: "B", Tags: []string{"b"}, Action: "move", Reason: "second", RunID: "", Metrics: llm.Metrics{}}); err != nil {
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

	if err := fake.Record(state.RecordInput{OriginalPath: "/src/a.txt", FinalPath: "/dst/a.txt", SHA256: "hash1", Category: "Cat", Tags: []string{"x", "y"}, Action: "move", Reason: "reason", RunID: "", Metrics: llm.Metrics{}}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := fake.Record(state.RecordInput{OriginalPath: "/src/b.txt", FinalPath: "", SHA256: "hash2", Category: "Cat", Tags: nil, Action: "delete", Reason: "reason", RunID: "", Metrics: llm.Metrics{}}); err != nil {
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

	_ = fake.Record(state.RecordInput{OriginalPath: "/a/1", FinalPath: "/b/1", SHA256: "sha", Category: "A", Tags: nil, Action: "move", Reason: "first", RunID: "", Metrics: llm.Metrics{}})
	_ = fake.Record(state.RecordInput{OriginalPath: "/a/2", FinalPath: "/b/2", SHA256: "sha", Category: "B", Tags: nil, Action: "move", Reason: "second", RunID: "", Metrics: llm.Metrics{}})

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
	_ = fake.Record(state.RecordInput{OriginalPath: "/a", FinalPath: "/b", SHA256: "h1", Category: "C", Tags: []string{"t"}, Action: "move", Reason: "r", RunID: "", Metrics: llm.Metrics{}})
	_ = fake.Record(state.RecordInput{OriginalPath: "/c", FinalPath: "", SHA256: "h2", Category: "C", Tags: nil, Action: "delete", Reason: "r", RunID: "", Metrics: llm.Metrics{}})

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
func TestRecordAndFindDirectoryDecision(t *testing.T) {
	repo, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer repo.Close()

	decision := llm.DirectoryDecision{
		Recommendation: "archive",
		Reason:         "old project",
		Category:       "Projects",
		Tags:           []string{"code", "backup"},
	}

	if err := repo.RecordDirectoryDecision("key1", decision); err != nil {
		t.Fatalf("RecordDirectoryDecision failed: %v", err)
	}

	got, ok, err := repo.FindDirectoryDecision("key1")
	if err != nil {
		t.Fatalf("FindDirectoryDecision failed: %v", err)
	}
	if !ok {
		t.Fatal("expected directory decision to be found")
	}
	if got.Recommendation != decision.Recommendation {
		t.Errorf("Recommendation = %q, want %q", got.Recommendation, decision.Recommendation)
	}
	if got.Reason != decision.Reason {
		t.Errorf("Reason = %q, want %q", got.Reason, decision.Reason)
	}
	if got.Category != decision.Category {
		t.Errorf("Category = %q, want %q", got.Category, decision.Category)
	}
	if len(got.Tags) != len(decision.Tags) || got.Tags[0] != decision.Tags[0] || got.Tags[1] != decision.Tags[1] {
		t.Errorf("Tags = %v, want %v", got.Tags, decision.Tags)
	}
}

func TestFindDirectoryDecision_NotFound(t *testing.T) {
	repo, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer repo.Close()

	_, ok, err := repo.FindDirectoryDecision("missing")
	if err != nil {
		t.Fatalf("FindDirectoryDecision failed: %v", err)
	}
	if ok {
		t.Error("expected no directory decision for unknown key")
	}
}

func TestRecordDirectoryDecision_OverwritesExisting(t *testing.T) {
	repo, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer repo.Close()

	if err := repo.RecordDirectoryDecision("k1", llm.DirectoryDecision{Recommendation: "keep", Reason: "a"}); err != nil {
		t.Fatalf("RecordDirectoryDecision failed: %v", err)
	}
	if err := repo.RecordDirectoryDecision("k1", llm.DirectoryDecision{Recommendation: "trash", Reason: "b"}); err != nil {
		t.Fatalf("RecordDirectoryDecision overwrite failed: %v", err)
	}

	got, ok, err := repo.FindDirectoryDecision("k1")
	if err != nil {
		t.Fatalf("FindDirectoryDecision failed: %v", err)
	}
	if !ok {
		t.Fatal("expected directory decision to be found")
	}
	if got.Recommendation != "trash" {
		t.Errorf("Recommendation = %q, want trash", got.Recommendation)
	}
	if got.Reason != "b" {
		t.Errorf("Reason = %q, want b", got.Reason)
	}
}

func TestFakeRepoDirectoryDecisionCache(t *testing.T) {
	repo := state.NewFake()

	decision := llm.DirectoryDecision{Recommendation: "archive", Reason: "old project"}
	if err := repo.RecordDirectoryDecision("k1", decision); err != nil {
		t.Fatalf("RecordDirectoryDecision failed: %v", err)
	}

	got, ok, err := repo.FindDirectoryDecision("k1")
	if err != nil {
		t.Fatalf("FindDirectoryDecision failed: %v", err)
	}
	if !ok {
		t.Fatal("expected directory decision to be found")
	}
	if got.Recommendation != decision.Recommendation {
		t.Errorf("Recommendation = %q, want %q", got.Recommendation, decision.Recommendation)
	}
	if got.Reason != decision.Reason {
		t.Errorf("Reason = %q, want %q", got.Reason, decision.Reason)
	}
}

func TestOpen_CreatesDirectoryDecisionsTable(t *testing.T) {
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
	err = db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='directory_decisions'").Scan(&name)
	if err != nil {
		t.Fatalf("directory_decisions table not found: %v", err)
	}
	if name != "directory_decisions" {
		t.Fatalf("unexpected table name: %q", name)
	}
}

func TestHistory_ByRunID(t *testing.T) {
	repo, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer repo.Close()

	if err := repo.Record(state.RecordInput{OriginalPath: "/a", FinalPath: "/b", SHA256: "h1", Category: "C", Tags: nil, Action: "move", Reason: "r", RunID: "run-1", Metrics: llm.Metrics{DurationMs: 100}}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := repo.Record(state.RecordInput{OriginalPath: "/c", FinalPath: "/d", SHA256: "h2", Category: "C", Tags: nil, Action: "move", Reason: "r", RunID: "run-2", Metrics: llm.Metrics{DurationMs: 200}}); err != nil {
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
	fake.Record(state.RecordInput{OriginalPath: "/a", FinalPath: "/b", SHA256: "h1", Category: "C", Tags: nil, Action: "move", Reason: "r", RunID: "run-1", Metrics: llm.Metrics{}})
	fake.Record(state.RecordInput{OriginalPath: "/c", FinalPath: "/d", SHA256: "h2", Category: "C", Tags: nil, Action: "move", Reason: "r", RunID: "run-2", Metrics: llm.Metrics{}})

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

	if err := repo.Record(state.RecordInput{OriginalPath: "/a", FinalPath: "/b", SHA256: "h1", Category: "C", Tags: []string{"work", "filemaid", "  ", "personal"}, Action: "move", Reason: "r", RunID: "", Metrics: llm.Metrics{}}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := repo.Record(state.RecordInput{OriginalPath: "/c", FinalPath: "/d", SHA256: "h2", Category: "C", Tags: []string{"work", "archive"}, Action: "move", Reason: "r", RunID: "", Metrics: llm.Metrics{}}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := repo.Record(state.RecordInput{OriginalPath: "/e", FinalPath: "/f", SHA256: "h3", Category: "C", Tags: nil, Action: "move", Reason: "r", RunID: "", Metrics: llm.Metrics{}}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := repo.Record(state.RecordInput{OriginalPath: "/g", FinalPath: "/h", SHA256: "h4", Category: "C", Tags: []string{}, Action: "move", Reason: "r", RunID: "", Metrics: llm.Metrics{}}); err != nil {
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
	if err := fake.Record(state.RecordInput{OriginalPath: "/a", FinalPath: "/b", SHA256: "h1", Category: "C", Tags: []string{"work", "filemaid", "  ", "personal"}, Action: "move", Reason: "r", RunID: "", Metrics: llm.Metrics{}}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := fake.Record(state.RecordInput{OriginalPath: "/c", FinalPath: "/d", SHA256: "h2", Category: "C", Tags: []string{"work", "archive"}, Action: "move", Reason: "r", RunID: "", Metrics: llm.Metrics{}}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := fake.Record(state.RecordInput{OriginalPath: "/e", FinalPath: "/f", SHA256: "h3", Category: "C", Tags: nil, Action: "move", Reason: "r", RunID: "", Metrics: llm.Metrics{}}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := fake.Record(state.RecordInput{OriginalPath: "/g", FinalPath: "/h", SHA256: "h4", Category: "C", Tags: []string{}, Action: "move", Reason: "r", RunID: "", Metrics: llm.Metrics{}}); err != nil {
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
func TestOpen_MigratesHistoryColumns(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy.db")

	// Create a database with the pre-rename schema.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	oldSchema := `
		CREATE TABLE history (
			id INTEGER PRIMARY KEY,
			original_path TEXT NOT NULL,
			final_path TEXT,
			sha256 TEXT,
			category TEXT,
			tags TEXT,
			action TEXT,
			reason TEXT,
			created_at TEXT DEFAULT CURRENT_TIMESTAMP
		);
	`
	if _, err := db.Exec(oldSchema); err != nil {
		t.Fatalf("create old schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO history (original_path, final_path, sha256, category, action, reason) VALUES (?, ?, ?, ?, ?, ?)`,
		"/old/file.txt", "/new/file.txt", "legacysha", "Documents", "move", "kept"); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	db.Close()

	// Re-open with state.Open; migration should add all new columns.
	repo, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("Open existing db failed: %v", err)
	}
	defer repo.Close()

	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("re-open db for inspection: %v", err)
	}
	defer db.Close()

	wantColumns := []string{"original_name", "new_name", "name_quality", "media_kind", "perceptual_hash", "av_signature", "text_signature", "hash_algorithm"}
	for _, col := range wantColumns {
		var name string
		err := db.QueryRow("SELECT name FROM pragma_table_info('history') WHERE name = ?", col).Scan(&name)
		if err != nil {
			t.Fatalf("column %q missing after migration: %v", col, err)
		}
	}

	// Original row must remain queryable.
	got, err := repo.FindByHash("legacysha")
	if err != nil {
		t.Fatalf("FindByHash failed: %v", err)
	}
	if got == nil {
		t.Fatal("legacy row not found after migration")
	}
	if got.OriginalPath != "/old/file.txt" || got.FinalPath != "/new/file.txt" {
		t.Errorf("unexpected legacy row: %+v", got)
	}
}

func TestFindDuplicatesByHash(t *testing.T) {
	repo, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer repo.Close()

	sha := "dupsha"
	if err := repo.Record(state.RecordInput{OriginalPath: "/a/file1.txt", FinalPath: "/b/file1.txt", SHA256: sha, Category: "A", Tags: []string{"a"}, Action: "move", Reason: "first"}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := repo.Record(state.RecordInput{OriginalPath: "/a/file2.txt", FinalPath: "/b/file2.txt", SHA256: sha, Category: "B", Tags: []string{"b"}, Action: "move", Reason: "second"}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := repo.Record(state.RecordInput{OriginalPath: "/a/file3.txt", FinalPath: "/b/file3.txt", SHA256: "other", Category: "C", Action: "move", Reason: "other"}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}

	got, err := repo.FindDuplicatesByHash(sha)
	if err != nil {
		t.Fatalf("FindDuplicatesByHash failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 duplicates, got %d", len(got))
	}
	if got[0].OriginalPath != "/a/file2.txt" {
		t.Errorf("most recent duplicate = %q, want /a/file2.txt", got[0].OriginalPath)
	}
	if got[1].OriginalPath != "/a/file1.txt" {
		t.Errorf("older duplicate = %q, want /a/file1.txt", got[1].OriginalPath)
	}
}

func TestFindSimilarByFingerprints(t *testing.T) {
	repo, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer repo.Close()

	if err := repo.Record(state.RecordInput{OriginalPath: "/a/v1.mp4", SHA256: "h1", Category: "Video", Action: "move", Reason: "r", PerceptualHash: "phash1", AVSignature: "av1", TextSignature: "text1"}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := repo.Record(state.RecordInput{OriginalPath: "/a/v2.mp4", SHA256: "h2", Category: "Video", Action: "move", Reason: "r", PerceptualHash: "phash1", AVSignature: "av2", TextSignature: "text2"}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := repo.Record(state.RecordInput{OriginalPath: "/a/v3.mp4", SHA256: "h3", Category: "Video", Action: "move", Reason: "r", PerceptualHash: "phash3", AVSignature: "av1", TextSignature: "text3"}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if err := repo.Record(state.RecordInput{OriginalPath: "/a/v4.mp4", SHA256: "h4", Category: "Video", Action: "move", Reason: "r", PerceptualHash: "phash4", AVSignature: "av4", TextSignature: "text1"}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}

	got, err := repo.FindSimilarByFingerprints("phash1", "", "")
	if err != nil {
		t.Fatalf("FindSimilarByFingerprints failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 similar by perceptual hash, got %d", len(got))
	}

	got, err = repo.FindSimilarByFingerprints("", "av1", "")
	if err != nil {
		t.Fatalf("FindSimilarByFingerprints failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 similar by av signature, got %d", len(got))
	}

	got, err = repo.FindSimilarByFingerprints("", "", "text1")
	if err != nil {
		t.Fatalf("FindSimilarByFingerprints failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 similar by text signature, got %d", len(got))
	}

	got, err = repo.FindSimilarByFingerprints("", "", "")
	if err != nil {
		t.Fatalf("FindSimilarByFingerprints failed: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 matches for empty signatures, got %d", len(got))
	}
}

func TestFakeRepo_FindDuplicatesByHash(t *testing.T) {
	fake := state.NewFake()
	_ = fake.Record(state.RecordInput{OriginalPath: "/a/1", SHA256: "sha", Action: "move", Reason: "first"})
	_ = fake.Record(state.RecordInput{OriginalPath: "/a/2", SHA256: "sha", Action: "move", Reason: "second"})
	_ = fake.Record(state.RecordInput{OriginalPath: "/a/3", SHA256: "other", Action: "move", Reason: "other"})

	got, err := fake.FindDuplicatesByHash("sha")
	if err != nil {
		t.Fatalf("FindDuplicatesByHash failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 duplicates, got %d", len(got))
	}
	if got[0].OriginalPath != "/a/2" {
		t.Errorf("most recent duplicate = %q, want /a/2", got[0].OriginalPath)
	}
}

func TestFakeRepo_FindSimilarByFingerprints(t *testing.T) {
	fake := state.NewFake()
	_ = fake.Record(state.RecordInput{OriginalPath: "/a/1", SHA256: "h1", Action: "move", Reason: "r", PerceptualHash: "phash1"})
	_ = fake.Record(state.RecordInput{OriginalPath: "/a/2", SHA256: "h2", Action: "move", Reason: "r", PerceptualHash: "phash1"})
	_ = fake.Record(state.RecordInput{OriginalPath: "/a/3", SHA256: "h3", Action: "move", Reason: "r", AVSignature: "av1"})

	got, err := fake.FindSimilarByFingerprints("phash1", "", "")
	if err != nil {
		t.Fatalf("FindSimilarByFingerprints failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 similar, got %d", len(got))
	}

	got, err = fake.FindSimilarByFingerprints("", "av1", "")
	if err != nil {
		t.Fatalf("FindSimilarByFingerprints failed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 similar, got %d", len(got))
	}

	got, err = fake.FindSimilarByFingerprints("", "", "")
	if err != nil {
		t.Fatalf("FindSimilarByFingerprints failed: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 matches for empty signatures, got %d", len(got))
	}
}

func TestOpen_EnableWAL(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")

	repo, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	repo.Close()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite directly failed: %v", err)
	}
	defer db.Close()

	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("read journal_mode failed: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

func TestOpen_WALUnsupportedContinues(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	// An in-memory database does not support WAL; Open must still succeed and
	// log a warning instead of returning an error.
	repo, err := state.Open(":memory:")
	if err != nil {
		t.Fatalf("Open failed for unsupported WAL: %v", err)
	}
	defer repo.Close()

	if !strings.Contains(buf.String(), "WAL") {
		t.Errorf("expected WAL warning in logs, got %q", buf.String())
	}
}
