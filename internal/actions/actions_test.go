package actions

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/logicminds/filemaid/internal/config"
	"github.com/logicminds/filemaid/internal/llm"
	"github.com/logicminds/filemaid/internal/state"
)

func testConfig(t *testing.T, tmp string) *config.Config {
	t.Helper()
	reviewDir := filepath.Join(tmp, "review")
	if err := os.MkdirAll(reviewDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return &config.Config{
		AllowedDirs: []string{
			filepath.Join(tmp, "Desktop"),
			filepath.Join(tmp, "Downloads"),
			filepath.Join(tmp, "Images"),
			filepath.Join(tmp, "Documents"),
			reviewDir,
		},
		ReviewDir: reviewDir,
		Categories: map[string]string{
			"Images":    filepath.Join(tmp, "Images"),
			"Documents": filepath.Join(tmp, "Documents"),
			"Unknown":   reviewDir,
		},
		Tags:               false,
		Comments:           false,
		SafeDeletePatterns: []string{"~/Downloads/*.tmp"},
	}
}

func TestNormalize(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "foo"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join(tmp, "..", filepath.Base(tmp), "foo")
	got := Normalize(rel)
	want := filepath.Join(tmp, "foo")
	if got != want {
		t.Errorf("Normalize(%q) = %q, want %q", rel, got, want)
	}
}

func TestWithinAllowed(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"same dir", filepath.Join(tmp, "Desktop", "foo.txt"), true},
		{"nested", filepath.Join(tmp, "Desktop", "a", "b.txt"), true},
		{"review dir", filepath.Join(tmp, "review", "foo.txt"), true},
		{"outside", filepath.Join(tmp, "elsewhere", "foo.txt"), false},
		{"prefix trap", filepath.Join(tmp, "DesktopXYZ", "foo.txt"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WithinAllowed(tt.path, cfg.AllowedDirs)
			if got != tt.want {
				t.Errorf("WithinAllowed(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestComputeHash(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "data.bin")
	if err := os.WriteFile(f, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	h1, err := ComputeHash(f)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := ComputeHash(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(h1) != 64 {
		t.Errorf("hash length = %d, want 64", len(h1))
	}
	if h1 != h2 {
		t.Errorf("hashes differ: %q vs %q", h1, h2)
	}
}

func TestMatchesPatterns(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(filepath.Join(home, "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	patterns := []string{"~/Downloads/*.tmp"}
	path := filepath.Join(home, "Downloads", "old.tmp")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !MatchesPatterns(path, patterns) {
		t.Errorf("expected %q to match %v", path, patterns)
	}
	if MatchesPatterns(filepath.Join(home, "Desktop", "old.tmp"), patterns) {
		t.Errorf("expected Desktop file not to match %v", patterns)
	}
}

func TestUniqueDest(t *testing.T) {
	tmp := t.TempDir()
	fs := NewRecordingFS()

	t.Run("no conflict", func(t *testing.T) {
		dest := filepath.Join(tmp, "Images", "foo.png")
		got := UniqueDest(dest, fs)
		if got != dest {
			t.Errorf("UniqueDest = %q, want %q", got, dest)
		}
	})

	t.Run("conflict", func(t *testing.T) {
		dest := filepath.Join(tmp, "Images", "foo.png")
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dest, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		got := UniqueDest(dest, fs)
		if got == dest {
			t.Errorf("UniqueDest did not change %q", got)
		}
		if !strings.HasPrefix(filepath.Base(got), "foo-") {
			t.Errorf("UniqueDest = %q, want foo-*", got)
		}
	})
}

func TestApplyMovesFileToCategory(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "img.png")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Images", Action: "move", Reason: "png"}
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result, cfg.Categories["Images"]) {
		t.Errorf("result = %q, want prefix %q", result, cfg.Categories["Images"])
	}
	if _, err := os.Stat(filepath.Join(tmp, "Images", "img.png")); err != nil {
		t.Errorf("dest missing: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src still exists")
	}
}

func TestApplySkipDisallowedSource(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "outside", "img.png")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Images", Action: "move", Reason: "png"}
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "skipped") {
		t.Errorf("result = %q, want skipped", result)
	}
	if len(db.Records()) != 0 {
		t.Errorf("recorded %d rows, want 0", len(db.Records()))
	}
}

func TestApplyForcesReviewOnUnsafeDelete(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "note.txt")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Documents", Action: "delete", Reason: "delete it"}
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{})
	if err != nil {
		t.Fatal(err)
	}
	wantSubdir := filepath.Join(cfg.ReviewDir, time.Now().Format("2006-01-02"))
	if !strings.HasPrefix(result, wantSubdir) {
		t.Errorf("result = %q, want prefix %q", result, wantSubdir)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("src still exists")
	}
	recs := db.Records()
	if len(recs) != 1 || recs[0].Action != "review" {
		t.Errorf("record = %+v, want review action", recs)
	}
	if !strings.Contains(recs[0].Reason, "delete refused") {
		t.Errorf("reason = %q, want delete refused", recs[0].Reason)
	}
}

func TestApplyAllowsDeleteForSafePattern(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(filepath.Join(home, "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	reviewDir := filepath.Join(tmp, "review")
	if err := os.MkdirAll(reviewDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		AllowedDirs:        []string{filepath.Join(home, "Downloads"), reviewDir},
		ReviewDir:          reviewDir,
		Categories:         map[string]string{"Unknown": reviewDir},
		Tags:               false,
		SafeDeletePatterns: []string{"~/Downloads/*.tmp"},
	}

	db := state.NewFake()
	fs := NewRecordingFS()
	src := filepath.Join(home, "Downloads", "junk.tmp")
	if err := os.WriteFile(src, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Unknown", Action: "delete", Reason: "temp file"}
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{})
	if err != nil {
		t.Fatal(err)
	}
	if result != "trash" {
		t.Errorf("result = %q, want trash", result)
	}
	if len(fs.Trashed) != 1 {
		t.Errorf("trashed %d items, want 1", len(fs.Trashed))
	}
	recs := db.Records()
	if len(recs) != 1 || recs[0].Action != "delete" {
		t.Errorf("record = %+v, want delete action", recs)
	}
}

func TestApplyRedirectsOutsideAllowedDestination(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "file.txt")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Images", Action: "move", Destination: "/tmp/evil", Reason: "hack"}
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result, cfg.ReviewDir) {
		t.Errorf("result = %q, want prefix %q", result, cfg.ReviewDir)
	}
}

func TestApplyDestinationOutsideAllowedRecordsReason(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "file.txt")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Images", Action: "move", Destination: "/tmp/evil", Reason: "hack"}
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}); err != nil {
		t.Fatal(err)
	}
	recs := db.Records()
	if len(recs) != 1 || !strings.Contains(recs[0].Reason, "destination outside allowed dirs") {
		t.Errorf("reason = %q, want destination outside allowed dirs", recs[0].Reason)
	}
}

func TestApplyRecordsHistory(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "doc.txt")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("document"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Documents", Action: "move", Reason: "txt"}
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}); err != nil {
		t.Fatal(err)
	}
	recs := db.Records()
	if len(recs) != 1 {
		t.Fatalf("recorded %d rows, want 1", len(recs))
	}
	if recs[0].Action != "move" {
		t.Errorf("action = %q, want move", recs[0].Action)
	}
	if recs[0].SHA256 == "" {
		t.Errorf("sha256 empty")
	}
}

func TestApplyDuplicateCoercesDeleteToReview(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	db := state.NewFake()
	fs := NewRecordingFS()

	src1 := filepath.Join(tmp, "Desktop", "a.txt")
	if err := os.MkdirAll(filepath.Dir(src1), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src1, []byte("same content"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Documents", Action: "delete", Reason: "delete dup"}
	if _, err := Apply(decision, src1, mustHash(t, src1), cfg, db, false, fs, "", llm.Metrics{}); err != nil {
		t.Fatal(err)
	}

	src2 := filepath.Join(tmp, "Desktop", "b.txt")
	if err := os.WriteFile(src2, []byte("same content"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Apply(decision, src2, mustHash(t, src2), cfg, db, false, fs, "", llm.Metrics{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result, cfg.ReviewDir) {
		t.Errorf("result = %q, want prefix %q", result, cfg.ReviewDir)
	}
	if _, err := os.Stat(filepath.Join(tmp, "Documents", "b.txt")); err == nil {
		t.Errorf("b.txt should not have been moved to Documents")
	}
}

func TestApplySetsTags(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Tags = true
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "img.png")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Images", Tags: []string{"image", "desktop"}, Action: "move", Reason: "png"}
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}); err != nil {
		t.Fatal(err)
	}
	if len(fs.Tags) != 1 {
		t.Fatalf("tagged %d times, want 1", len(fs.Tags))
	}
	if !stringSliceEqual(fs.Tags[0].Tags, []string{"Images", "image", "desktop"}) {
		t.Errorf("tags = %v, want [Images image desktop]", fs.Tags[0].Tags)
	}
}

func TestApplyAddsSubcategoryAsTag(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Tags = true
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "cat.png")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Images", Subcategory: "cat", Tags: []string{"photo"}, Action: "move", Reason: "png"}
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}); err != nil {
		t.Fatal(err)
	}
	if len(fs.Tags) != 1 {
		t.Fatalf("tagged %d times, want 1", len(fs.Tags))
	}
	want := []string{"Images", "cat", "photo"}
	if !stringSliceEqual(fs.Tags[0].Tags, want) {
		t.Errorf("tags = %v, want %v", fs.Tags[0].Tags, want)
	}
}

func TestApplyDoesNotDuplicateSubcategoryTag(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Tags = true
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "cat.png")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Images", Subcategory: "cat", Tags: []string{"cat", "photo"}, Action: "move", Reason: "png"}
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}); err != nil {
		t.Fatal(err)
	}
	if len(fs.Tags) != 1 {
		t.Fatalf("tagged %d times, want 1", len(fs.Tags))
	}
	want := []string{"Images", "cat", "photo"}
	if !stringSliceEqual(fs.Tags[0].Tags, want) {
		t.Errorf("tags = %v, want %v", fs.Tags[0].Tags, want)
	}
}
func TestApplyDoesNotDuplicateCategoryTag(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Tags = true
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "cat.png")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Images", Tags: []string{"Images", "photo"}, Action: "move", Reason: "png"}
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}); err != nil {
		t.Fatal(err)
	}
	if len(fs.Tags) != 1 {
		t.Fatalf("tagged %d times, want 1", len(fs.Tags))
	}
	want := []string{"Images", "photo"}
	if !stringSliceEqual(fs.Tags[0].Tags, want) {
		t.Errorf("tags = %v, want %v", fs.Tags[0].Tags, want)
	}
}

func TestApplyAddsFilemaidTagWhenSmartFoldersEnabled(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Tags = true
	cfg.SmartFolders = true
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "img.png")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Images", Tags: []string{"image", "desktop"}, Action: "move", Reason: "png"}
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}); err != nil {
		t.Fatal(err)
	}
	if len(fs.Tags) != 1 {
		t.Fatalf("tagged %d times, want 1", len(fs.Tags))
	}
	want := []string{"filemaid", "Images", "image", "desktop"}
	if !stringSliceEqual(fs.Tags[0].Tags, want) {
		t.Errorf("tags = %v, want %v", fs.Tags[0].Tags, want)
	}
}

func TestApplyNoFilemaidTagWhenSmartFoldersDisabled(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Tags = true
	cfg.SmartFolders = false
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "img.png")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Images", Tags: []string{"image", "desktop"}, Action: "move", Reason: "png"}
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}); err != nil {
		t.Fatal(err)
	}
	if len(fs.Tags) != 1 {
		t.Fatalf("tagged %d times, want 1", len(fs.Tags))
	}
	want := []string{"Images", "image", "desktop"}
	if !stringSliceEqual(fs.Tags[0].Tags, want) {
		t.Errorf("tags = %v, want %v", fs.Tags[0].Tags, want)
	}
}

func TestApplyOnlyFilemaidTagWhenTagsDisabledButSmartFoldersEnabled(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Tags = false
	cfg.SmartFolders = true
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "img.png")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Images", Tags: []string{"image", "desktop"}, Action: "move", Reason: "png"}
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}); err != nil {
		t.Fatal(err)
	}
	if len(fs.Tags) != 1 {
		t.Fatalf("tagged %d times, want 1", len(fs.Tags))
	}
	want := []string{"filemaid"}
	if !stringSliceEqual(fs.Tags[0].Tags, want) {
		t.Errorf("tags = %v, want %v", fs.Tags[0].Tags, want)
	}
}

func TestApplyNilTagsDoesNotPanicWithSmartFolders(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Tags = false
	cfg.SmartFolders = true
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "img.png")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Images", Action: "move", Reason: "png"}
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}); err != nil {
		t.Fatal(err)
	}
	if len(fs.Tags) != 1 {
		t.Fatalf("tagged %d times, want 1", len(fs.Tags))
	}
	want := []string{"filemaid"}
	if !stringSliceEqual(fs.Tags[0].Tags, want) {
		t.Errorf("tags = %v, want %v", fs.Tags[0].Tags, want)
	}
}

func TestApplySetsFinderComment(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Comments = true
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "note.txt")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Documents", Tags: []string{"txt"}, Action: "move", Reason: "simple text file"}
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}); err != nil {
		t.Fatal(err)
	}
	if len(fs.Comments) != 1 {
		t.Fatalf("commented %d times, want 1", len(fs.Comments))
	}
	if fs.Comments[0].Comment != "simple text file" {
		t.Errorf("comment = %q, want %q", fs.Comments[0].Comment, "simple text file")
	}
}

func TestApplySkipsCommentWhenDisabled(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Comments = false
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "note.txt")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Documents", Action: "move", Reason: "simple text file"}
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}); err != nil {
		t.Fatal(err)
	}
	if len(fs.Comments) != 0 {
		t.Errorf("commented %d times, want 0", len(fs.Comments))
	}
}

func TestApplySkipsEmptyComment(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Comments = true
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "note.txt")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Documents", Action: "move", Reason: ""}
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}); err != nil {
		t.Fatal(err)
	}
	if len(fs.Comments) != 0 {
		t.Errorf("commented %d times, want 0 for empty reason", len(fs.Comments))
	}
}

func TestApplyTrashFailureForcesReview(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(filepath.Join(home, "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	reviewDir := filepath.Join(tmp, "review")
	if err := os.MkdirAll(reviewDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		AllowedDirs:        []string{filepath.Join(home, "Downloads"), reviewDir},
		ReviewDir:          reviewDir,
		Categories:         map[string]string{"Unknown": reviewDir},
		Tags:               false,
		SafeDeletePatterns: []string{"~/Downloads/*.tmp"},
	}

	db := state.NewFake()
	fs := &failingFS{FS: NewRecordingFS(), failTrash: true}
	src := filepath.Join(home, "Downloads", "junk.tmp")
	if err := os.WriteFile(src, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Unknown", Action: "delete", Reason: "temp file"}
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result, cfg.ReviewDir) {
		t.Errorf("result = %q, want prefix %q", result, cfg.ReviewDir)
	}
	recs := db.Records()
	if len(recs) != 1 || recs[0].Action != "review" {
		t.Errorf("record = %+v, want review action", recs)
	}
	if !strings.Contains(recs[0].Reason, "trash failed") {
		t.Errorf("reason = %q, want trash failed", recs[0].Reason)
	}
}

func TestApplyMoveFailureReturnsError(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	db := state.NewFake()
	fs := &failingFS{FS: NewRecordingFS(), failMove: true}

	src := filepath.Join(tmp, "Desktop", "file.txt")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Documents", Action: "move", Reason: "txt"}
	_, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "move failed") {
		t.Errorf("error = %q, want move failed", err)
	}
}

// TestApplySkipsGracefullyWhenFileDisappears verifies that if a file is moved
// by a concurrent process between hash computation and the Apply call, Apply
// returns a "skipped" result (no error, no DB record) instead of failing.
// This models the scenario where the LaunchAgent and a manual scan run at the
// same time and both try to process the same file.
func TestApplySkipsGracefullyWhenFileDisappears(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "screenshot.png")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate: hash was computed before the LLM call, then the file was moved
	// away by a concurrent process.
	hash := mustHash(t, src)
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Images", Action: "move", Reason: "png"}
	result, err := Apply(decision, src, hash, cfg, db, false, fs, "", llm.Metrics{})
	if err != nil {
		t.Fatalf("expected no error when file is gone, got: %v", err)
	}
	if !strings.Contains(result, "skipped") {
		t.Errorf("result = %q, want to contain 'skipped'", result)
	}
	if len(db.Records()) != 0 {
		t.Errorf("expected no DB record for disappeared file, got %d", len(db.Records()))
	}
}

func TestApplyUniqueDestCollision(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	db := state.NewFake()
	fs := NewRecordingFS()

	src1 := filepath.Join(tmp, "Desktop", "file.txt")
	src2 := filepath.Join(tmp, "Desktop", "file2.txt")
	if err := os.MkdirAll(filepath.Dir(src1), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src1, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src2, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "Documents", Action: "move", Reason: "doc"}
	result1, err := Apply(decision, src1, mustHash(t, src1), cfg, db, false, fs, "", llm.Metrics{})
	if err != nil {
		t.Fatal(err)
	}
	result2, err := Apply(decision, src2, mustHash(t, src2), cfg, db, false, fs, "", llm.Metrics{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(result1, "file.txt") {
		t.Errorf("result1 = %q, want suffix file.txt", result1)
	}
	if !strings.HasSuffix(result2, "file2.txt") {
		t.Errorf("result2 = %q, want suffix file2.txt", result2)
	}
}

func TestApplyUnknownCategoryGoesToReview(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "weird.bin")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("bin"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{Category: "NoSuchCategory", Action: "move", Reason: "unknown"}
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result, cfg.ReviewDir) {
		t.Errorf("result = %q, want prefix %q", result, cfg.ReviewDir)
	}
}

func TestOSFSMoveAndExists(t *testing.T) {
	tmp := t.TempDir()
	fs := NewOSFS()

	src := filepath.Join(tmp, "a.txt")
	dest := filepath.Join(tmp, "b", "c.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if fs.Exists(dest) {
		t.Errorf("dest should not exist")
	}
	if err := fs.MkdirAll(filepath.Dir(dest)); err != nil {
		t.Fatal(err)
	}
	if err := fs.Move(src, dest); err != nil {
		t.Fatal(err)
	}
	if !fs.Exists(dest) {
		t.Errorf("dest should exist")
	}
	if fs.Exists(src) {
		t.Errorf("src should not exist")
	}
}

func TestEncodeStringArrayPlist(t *testing.T) {
	plist := encodeStringArrayPlist([]string{"red", "blue"})
	if len(plist) == 0 {
		t.Fatal("empty plist")
	}
	if string(plist[:8]) != "bplist00" {
		t.Errorf("bad header: %q", plist[:8])
	}
	// Verify the generated plist round-trips to the original strings.
	got, err := parseStringArrayPlist(plist)
	if err != nil {
		t.Fatalf("parse generated plist: %v", err)
	}
	want := []string{"red", "blue"}
	if !stringSliceEqual(got, want) {
		t.Errorf("parsed tags = %v, want %v", got, want)
	}
}

// mustHash computes the SHA-256 hash of path or fatals the test if the file
// cannot be read.
func mustHash(t *testing.T, path string) string {
	t.Helper()
	h, err := ComputeHash(path)
	if err != nil {
		t.Fatalf("ComputeHash(%q): %v", path, err)
	}
	return h
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// parseStringArrayPlist is a minimal binary plist parser sufficient to verify
// that encodeStringArrayPlist emits a valid array of ASCII strings.
func parseStringArrayPlist(data []byte) ([]string, error) {
	if len(data) < 32 || string(data[:8]) != "bplist00" {
		return nil, fmt.Errorf("invalid bplist header")
	}

	trailer := data[len(data)-32:]
	offsetIntSize := int(trailer[6])
	objectRefSize := int(trailer[7])
	numObjects := int(binary.BigEndian.Uint64(trailer[8:16]))
	topObject := int(binary.BigEndian.Uint64(trailer[16:24]))
	offsetTableOffset := int(binary.BigEndian.Uint64(trailer[24:32]))

	if offsetIntSize != 1 || objectRefSize != 1 {
		return nil, fmt.Errorf("unsupported offset/ref size: %d/%d", offsetIntSize, objectRefSize)
	}
	if numObjects < 1 || topObject >= numObjects {
		return nil, fmt.Errorf("invalid object count or top object")
	}

	offsetTable := make([]int, numObjects)
	for i := 0; i < numObjects; i++ {
		off := offsetTableOffset + i*offsetIntSize
		if off < 0 || off >= len(data) {
			return nil, fmt.Errorf("offset table entry %d out of range", i)
		}
		offsetTable[i] = int(data[off])
	}

	var parseObject func(int) ([]string, error)
	parseObject = func(idx int) ([]string, error) {
		if idx < 0 || idx >= numObjects {
			return nil, fmt.Errorf("object index out of range: %d", idx)
		}
		off := offsetTable[idx]
		if off < 0 || off >= len(data) {
			return nil, fmt.Errorf("object offset out of range: %d", off)
		}
		marker := data[off]
		switch {
		case marker&0xF0 == 0xA0:
			count := int(marker & 0x0F)
			if count == 0x0F {
				return nil, fmt.Errorf("extended array count not supported")
			}
			var result []string
			for i := 0; i < count; i++ {
				refOff := off + 1 + i*objectRefSize
				if refOff >= len(data) {
					return nil, fmt.Errorf("array ref %d out of range", i)
				}
				ref := int(data[refOff])
				items, err := parseObject(ref)
				if err != nil {
					return nil, err
				}
				result = append(result, items...)
			}
			return result, nil
		case marker&0xF0 == 0x50:
			length := int(marker & 0x0F)
			if length == 0x0F {
				return nil, fmt.Errorf("extended string count not supported")
			}
			start := off + 1
			end := start + length
			if end > len(data) {
				return nil, fmt.Errorf("string extends past data")
			}
			return []string{string(data[start:end])}, nil
		case marker&0xF0 == 0x60:
			length := int(marker & 0x0F)
			start := off + 1
			if length == 0x0F {
				var n int64
				switch data[off+1] {
				case 0x10:
					n = int64(data[off+2])
					start = off + 3
				case 0x11:
					n = int64(binary.BigEndian.Uint16(data[off+2 : off+4]))
					start = off + 4
				case 0x12:
					n = int64(binary.BigEndian.Uint32(data[off+2 : off+6]))
					start = off + 6
				case 0x13:
					n = int64(binary.BigEndian.Uint64(data[off+2 : off+10]))
					start = off + 10
				default:
					return nil, fmt.Errorf("unsupported unicode string length int marker: 0x%02X", data[off+1])
				}
				length = int(n)
			}
			end := start + length*2
			if end > len(data) {
				return nil, fmt.Errorf("unicode string extends past data")
			}
			utf16Bytes := data[start:end]
			runes := make([]uint16, length)
			for i := 0; i < length; i++ {
				runes[i] = binary.BigEndian.Uint16(utf16Bytes[i*2 : (i+1)*2])
			}
			s := string(utf16.Decode(runes))
			return []string{s}, nil
		default:
			return nil, fmt.Errorf("unexpected object marker: 0x%02X", marker)
		}
	}

	return parseObject(topObject)
}

// parseStringPlist parses a minimal binary plist containing a single Unicode
// string. It is sufficient to verify encodeStringPlist output.
func parseStringPlist(data []byte) (string, error) {
	items, err := parseStringArrayPlist(data)
	if err != nil {
		return "", err
	}
	if len(items) != 1 {
		return "", fmt.Errorf("expected 1 object, got %d", len(items))
	}
	return items[0], nil
}
func TestExpandTilde(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"~", tmp},
		{"~/foo", filepath.Join(tmp, "foo")},
		{"/absolute/path", "/absolute/path"},
		{"plain", "plain"},
	}
	for _, c := range cases {
		got := expandTilde(c.in)
		if got != c.want {
			t.Errorf("expandTilde(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestOSFSMoveFallback(t *testing.T) {
	tmp := t.TempDir()
	fs := NewOSFS()

	src := filepath.Join(tmp, "a.txt")
	dest := filepath.Join(tmp, "missing", "dir", "b.txt")
	if err := os.WriteFile(src, []byte("fallback"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := fs.Move(src, dest); err != nil {
		t.Fatalf("Move fallback failed: %v", err)
	}
	if !fs.Exists(dest) {
		t.Errorf("dest should exist")
	}
	if fs.Exists(src) {
		t.Errorf("src should not exist")
	}
}

func TestOSFSSetTags(t *testing.T) {
	tmp := t.TempDir()
	fs := NewOSFS()
	path := filepath.Join(tmp, "tagged.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Should not panic; actual Finder tags require macOS metadata.
	fs.SetTags(path, []string{"red", "blue"})
}

func TestOSFSTrash(t *testing.T) {
	tmp := t.TempDir()
	fs := NewOSFS()
	path := filepath.Join(tmp, "trash.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Best-effort; on macOS Finder may or may not cooperate in tests.
	_ = fs.Trash(path)
}

func TestOSFSSetFinderComment(t *testing.T) {
	tmp := t.TempDir()
	fs := NewOSFS()
	path := filepath.Join(tmp, "commented.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Should not panic; actual Finder comments require macOS metadata.
	fs.SetFinderComment(path, "classified as document")
}

func TestEncodeStringPlist(t *testing.T) {
	want := "hello world"
	plist := encodeStringPlist(want)
	if string(plist[:8]) != "bplist00" {
		t.Errorf("bad header: %q", plist[:8])
	}
	got, err := parseStringPlist(plist)
	if err != nil {
		t.Fatalf("parse generated plist: %v", err)
	}
	if got != want {
		t.Errorf("parsed comment = %q, want %q", got, want)
	}
}

func TestEncodeStringPlistLarge(t *testing.T) {
	want := "this is a very long comment that exceeds fifteen bytes"
	plist := encodeStringPlist(want)
	if string(plist[:8]) != "bplist00" {
		t.Errorf("bad header: %q", plist[:8])
	}
	got, err := parseStringPlist(plist)
	if err != nil {
		t.Fatalf("parse generated plist: %v", err)
	}
	if got != want {
		t.Errorf("parsed comment = %q, want %q", got, want)
	}
}

func TestEncodeStringArrayPlistLarge(t *testing.T) {
	items := make([]string, 16)
	for i := range items {
		items[i] = "tag-number-"
	}
	// One long string to exercise the length >= 15 path.
	items[15] = "this-is-a-very-long-tag-name"
	plist := encodeStringArrayPlist(items)
	if string(plist[:8]) != "bplist00" {
		t.Errorf("bad header: %q", plist[:8])
	}
}
