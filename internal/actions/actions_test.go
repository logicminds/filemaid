package actions

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

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
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false)
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
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false)
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
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false)
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
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false)
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
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false)
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
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false); err != nil {
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
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false); err != nil {
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
	if _, err := Apply(decision, src1, mustHash(t, src1), cfg, db, false, fs, "", llm.Metrics{}, false); err != nil {
		t.Fatal(err)
	}

	src2 := filepath.Join(tmp, "Desktop", "b.txt")
	if err := os.WriteFile(src2, []byte("same content"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Apply(decision, src2, mustHash(t, src2), cfg, db, false, fs, "", llm.Metrics{}, false)
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
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false); err != nil {
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
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false); err != nil {
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
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false); err != nil {
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
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false); err != nil {
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
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false); err != nil {
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
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false); err != nil {
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
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false); err != nil {
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
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false); err != nil {
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
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false); err != nil {
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
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false); err != nil {
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
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false); err != nil {
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
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false)
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
	_, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false)
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
	result, err := Apply(decision, src, hash, cfg, db, false, fs, "", llm.Metrics{}, false)
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
	result1, err := Apply(decision, src1, mustHash(t, src1), cfg, db, false, fs, "", llm.Metrics{}, false)
	if err != nil {
		t.Fatal(err)
	}
	result2, err := Apply(decision, src2, mustHash(t, src2), cfg, db, false, fs, "", llm.Metrics{}, false)
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
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result, cfg.ReviewDir) {
		t.Errorf("result = %q, want prefix %q", result, cfg.ReviewDir)
	}
}

func TestApplyRenameAtLevelGate(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Rename = true
	cfg.RenameLevel = 3
	cfg.RenameMinLength = 5
	cfg.RenameMaxLength = 120
	cfg.RenameInvalidChars = "<>:\"/\\\\|?*"
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "doc.txt")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("project notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{
		Category:    "Documents",
		Action:      "move",
		Reason:      "txt",
		NewName:     "Project Notes.txt",
		NameQuality: 3,
	}
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(result) != "Project Notes.txt" {
		t.Errorf("result base = %q, want %q", filepath.Base(result), "Project Notes.txt")
	}
	if _, err := os.Stat(filepath.Join(tmp, "Documents", "Project Notes.txt")); err != nil {
		t.Errorf("renamed dest missing: %v", err)
	}
}

func TestApplyRenameBelowLevelKeepsOriginal(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Rename = true
	cfg.RenameLevel = 4
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "notes.txt")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{
		Category:    "Documents",
		Action:      "move",
		Reason:      "txt",
		NewName:     "Better Name.txt",
		NameQuality: 3,
	}
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(result) != "notes.txt" {
		t.Errorf("result base = %q, want %q", filepath.Base(result), "notes.txt")
	}
}

func TestApplyRenameCollisionUsesCounterSuffix(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Rename = true
	cfg.RenameLevel = 1
	cfg.RenameMinLength = 1
	db := state.NewFake()
	fs := NewRecordingFS()

	if err := os.MkdirAll(filepath.Join(tmp, "Documents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "Documents", "Report.txt"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(tmp, "Desktop", "doc.txt")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("report content"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{
		Category:    "Documents",
		Action:      "move",
		Reason:      "txt",
		NewName:     "Report.txt",
		NameQuality: 1,
	}
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(result) != "Report 1.txt" {
		t.Errorf("result base = %q, want %q", filepath.Base(result), "Report 1.txt")
	}
}

func TestApplyRenameInvalidNameKeepsOriginal(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Rename = true
	cfg.RenameLevel = 1
	cfg.RenameInvalidChars = "<>:\"/\\\\|?*"
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "doc.txt")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("doc"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{
		Category:    "Documents",
		Action:      "move",
		Reason:      "txt",
		NewName:     "???.txt",
		NameQuality: 1,
	}
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(result) != "doc.txt" {
		t.Errorf("result base = %q, want %q", filepath.Base(result), "doc.txt")
	}
}

func TestApplyDuplicateContentRoutesToReview(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Rename = true
	cfg.RenameLevel = 1
	db := state.NewFake()
	fs := NewRecordingFS()

	src1 := filepath.Join(tmp, "Desktop", "a.txt")
	if err := os.MkdirAll(filepath.Dir(src1), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src1, []byte("duplicate content"), 0o644); err != nil {
		t.Fatal(err)
	}
	decision := llm.Decision{Category: "Documents", Action: "move", Reason: "txt", NewName: "First.txt", NameQuality: 1}
	if _, err := Apply(decision, src1, mustHash(t, src1), cfg, db, false, fs, "", llm.Metrics{}, false); err != nil {
		t.Fatal(err)
	}

	src2 := filepath.Join(tmp, "Desktop", "b.txt")
	if err := os.WriteFile(src2, []byte("duplicate content"), 0o644); err != nil {
		t.Fatal(err)
	}
	decision2 := llm.Decision{Category: "Documents", Action: "move", Reason: "txt", NewName: "Second.txt", NameQuality: 1}
	result, err := Apply(decision2, src2, mustHash(t, src2), cfg, db, false, fs, "", llm.Metrics{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result, cfg.ReviewDir) {
		t.Errorf("result = %q, want prefix %q", result, cfg.ReviewDir)
	}
	recs := db.Records()
	if len(recs) != 2 {
		t.Fatalf("recorded %d rows, want 2", len(recs))
	}
	last := recs[len(recs)-1]
	if last.Action != "review" {
		t.Errorf("action = %q, want review", last.Action)
	}
	if !strings.Contains(last.Reason, "duplicate content detected") {
		t.Errorf("reason = %q, want duplicate mention", last.Reason)
	}
}

func TestApplySimilarContentRoutesToReview(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Rename = true
	cfg.RenameLevel = 1
	db := state.NewFake()
	fs := NewRecordingFS()

	src1 := filepath.Join(tmp, "Desktop", "a.txt")
	if err := os.MkdirAll(filepath.Dir(src1), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src1, []byte("hello world report"), 0o644); err != nil {
		t.Fatal(err)
	}
	decision := llm.Decision{Category: "Documents", Action: "move", Reason: "txt", NewName: "First.txt", NameQuality: 1}
	if _, err := Apply(decision, src1, mustHash(t, src1), cfg, db, false, fs, "", llm.Metrics{}, false); err != nil {
		t.Fatal(err)
	}

	src2 := filepath.Join(tmp, "Desktop", "b.txt")
	if err := os.WriteFile(src2, []byte("hello world report\x01"), 0o644); err != nil {
		t.Fatal(err)
	}
	decision2 := llm.Decision{Category: "Documents", Action: "move", Reason: "txt", NewName: "Second.txt", NameQuality: 1}
	result, err := Apply(decision2, src2, mustHash(t, src2), cfg, db, false, fs, "", llm.Metrics{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result, cfg.ReviewDir) {
		t.Errorf("result = %q, want prefix %q", result, cfg.ReviewDir)
	}
	recs := db.Records()
	if len(recs) != 2 {
		t.Fatalf("recorded %d rows, want 2", len(recs))
	}
	last := recs[len(recs)-1]
	if last.Action != "review" {
		t.Errorf("action = %q, want review", last.Action)
	}
	if !strings.Contains(last.Reason, "similar content detected") {
		t.Errorf("reason = %q, want similar mention", last.Reason)
	}
}

func TestApplyRenameRecordsNamesInHistory(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Rename = true
	cfg.RenameLevel = 1
	cfg.RenameMinLength = 1
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "doc.txt")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("doc"), 0o644); err != nil {
		t.Fatal(err)
	}

	decision := llm.Decision{
		Category:    "Documents",
		Action:      "move",
		Reason:      "txt",
		NewName:     "Renamed Document.txt",
		NameQuality: 1,
	}
	if _, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false); err != nil {
		t.Fatal(err)
	}
	recs := db.Records()
	if len(recs) != 1 {
		t.Fatalf("recorded %d rows, want 1", len(recs))
	}
	if recs[0].OriginalName != "doc.txt" {
		t.Errorf("original_name = %q, want %q", recs[0].OriginalName, "doc.txt")
	}
	if recs[0].NewName != "Renamed Document.txt" {
		t.Errorf("new_name = %q, want %q", recs[0].NewName, "Renamed Document.txt")
	}
	if recs[0].NameQuality.Float64 != 1 {
		t.Errorf("name_quality = %v, want 1", recs[0].NameQuality.Float64)
	}
	if recs[0].MediaKind == "" {
		t.Errorf("media_kind empty")
	}
}

func TestApplyRenameEnforcesMinMaxLength(t *testing.T) {
	tmp := t.TempDir()
	cfg := testConfig(t, tmp)
	cfg.Rename = true
	cfg.RenameLevel = 1
	cfg.RenameMinLength = 10
	cfg.RenameMaxLength = 25
	cfg.RenameInvalidChars = "<>:\"/\\\\|?*"
	db := state.NewFake()
	fs := NewRecordingFS()

	src := filepath.Join(tmp, "Desktop", "doc.txt")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("doc"), 0o644); err != nil {
		t.Fatal(err)
	}

	longName := strings.Repeat("a", 100) + ".txt"
	decision := llm.Decision{
		Category:    "Documents",
		Action:      "move",
		Reason:      "txt",
		NewName:     longName,
		NameQuality: 1,
	}
	result, err := Apply(decision, src, mustHash(t, src), cfg, db, false, fs, "", llm.Metrics{}, false)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(result)
	if base == longName {
		t.Errorf("long name was not truncated")
	}
	if len(base) > cfg.RenameMaxLength {
		t.Errorf("result length %d > max %d", len(base), cfg.RenameMaxLength)
	}

	shortSrc := filepath.Join(tmp, "Desktop", "short.txt")
	if err := os.WriteFile(shortSrc, []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}
	decision2 := llm.Decision{
		Category:    "Documents",
		Action:      "move",
		Reason:      "txt",
		NewName:     "tiny.txt",
		NameQuality: 1,
	}
	result2, err := Apply(decision2, shortSrc, mustHash(t, shortSrc), cfg, db, false, fs, "", llm.Metrics{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(result2) != "short.txt" {
		t.Errorf("below min length should keep original, got %q", filepath.Base(result2))
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

func TestMDImportBatcherDedupesDirectories(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	b := newMDImportBatcher(func(dir string) error {
		mu.Lock()
		calls = append(calls, dir)
		mu.Unlock()
		return nil
	})
	b.Add("/tmp/a")
	b.Add("/tmp/a") // duplicate
	b.Add("/tmp/b")
	b.Flush()

	mu.Lock()
	n := len(calls)
	mu.Unlock()
	if n != 2 {
		t.Fatalf("expected 2 mdimport calls, got %d", n)
	}
	sort.Strings(calls)
	if calls[0] != "/tmp/a" || calls[1] != "/tmp/b" {
		t.Errorf("unexpected calls: %v", calls)
	}
}

func TestMDImportBatcherFlushesAfterDelay(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	b := newMDImportBatcher(func(dir string) error {
		mu.Lock()
		calls = append(calls, dir)
		mu.Unlock()
		return nil
	})
	b.delay = 10 * time.Millisecond
	b.Add("/tmp/a")

	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	n := len(calls)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("expected 1 mdimport call after delay, got %d", n)
	}
	if calls[0] != "/tmp/a" {
		t.Errorf("expected mdimport /tmp/a, got %q", calls[0])
	}
}

func TestOSFSBatchesMDImportPerDirectory(t *testing.T) {
	tmp := t.TempDir()
	var mu sync.Mutex
	var calls []string
	fs := newOSFSWithBatcher(func(dir string) error {
		mu.Lock()
		calls = append(calls, dir)
		mu.Unlock()
		return nil
	})

	path1 := filepath.Join(tmp, "a.txt")
	path2 := filepath.Join(tmp, "b.txt")
	if err := os.WriteFile(path1, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path2, []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	fs.SetTags(path1, []string{"red"})
	fs.SetFinderComment(path2, "classified")
	fs.FlushMDImport()

	mu.Lock()
	n := len(calls)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("expected 1 mdimport call for same dir, got %d", n)
	}
	if calls[0] != tmp {
		t.Errorf("expected mdimport %q, got %q", tmp, calls[0])
	}
}

func TestOSFSBatchesMDImportPerDistinctDirectories(t *testing.T) {
	tmp := t.TempDir()
	dirA := filepath.Join(tmp, "a")
	dirB := filepath.Join(tmp, "b")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var calls []string
	fs := newOSFSWithBatcher(func(dir string) error {
		mu.Lock()
		calls = append(calls, dir)
		mu.Unlock()
		return nil
	})

	fs.SetTags(filepath.Join(dirA, "f.txt"), []string{"red"})
	fs.SetTags(filepath.Join(dirB, "f.txt"), []string{"blue"})
	fs.FlushMDImport()

	mu.Lock()
	n := len(calls)
	mu.Unlock()
	if n != 2 {
		t.Fatalf("expected 2 mdimport calls for distinct dirs, got %d", n)
	}
	sort.Strings(calls)
	if calls[0] != dirA || calls[1] != dirB {
		t.Errorf("unexpected calls: %v", calls)
	}
}
func TestMergeTags(t *testing.T) {
	cases := []struct {
		name     string
		existing []string
		new      []string
		want     []string
	}{
		{
			name:     "empty existing writes new tags",
			existing: nil,
			new:      []string{"red", "blue"},
			want:     []string{"red", "blue"},
		},
		{
			name:     "merges without duplicates",
			existing: []string{"red", "green"},
			new:      []string{"blue", "yellow"},
			want:     []string{"red", "green", "blue", "yellow"},
		},
		{
			name:     "case insensitive dedup prefers new casing",
			existing: []string{"images", "photo"},
			new:      []string{"Images", "new"},
			want:     []string{"Images", "photo", "new"},
		},
		{
			name:     "new tag earlier in list updates casing of existing",
			existing: []string{"documents", "work"},
			new:      []string{"Documents", "work"},
			want:     []string{"Documents", "work"},
		},
		{
			name:     "exact duplicates removed",
			existing: []string{"a", "b"},
			new:      []string{"a", "c"},
			want:     []string{"a", "b", "c"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mergeTags(c.existing, c.new)
			if !stringSliceEqual(got, c.want) {
				t.Errorf("mergeTags(%v, %v) = %v, want %v", c.existing, c.new, got, c.want)
			}
		})
	}
}

func TestMergeFinderComment(t *testing.T) {
	cases := []struct {
		name     string
		existing string
		reason   string
		want     string
	}{
		{
			name:     "empty existing writes reason",
			existing: "",
			reason:   "classified as document",
			want:     "classified as document",
		},
		{
			name:     "appends with separator",
			existing: "classified as document",
			reason:   "moved to Documents",
			want:     "classified as document; moved to Documents",
		},
		{
			name:     "skips exact duplicate",
			existing: "classified as document",
			reason:   "classified as document",
			want:     "classified as document",
		},
		{
			name:     "skips case insensitive duplicate",
			existing: "Classified as Document",
			reason:   "classified as document",
			want:     "Classified as Document",
		},
		{
			name:     "skips when reason is substring",
			existing: "classified as document; moved to Documents",
			reason:   "document",
			want:     "classified as document; moved to Documents",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mergeFinderComment(c.existing, c.reason)
			if got != c.want {
				t.Errorf("mergeFinderComment(%q, %q) = %q, want %q", c.existing, c.reason, got, c.want)
			}
		})
	}
}

func requireXattr(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("xattr tests require macOS")
	}
	if _, err := exec.LookPath("xattr"); err != nil {
		t.Skip("xattr binary not found")
	}
}

func TestReadFinderTagsRoundTrip(t *testing.T) {
	requireXattr(t)
	tmp := t.TempDir()
	path := filepath.Join(tmp, "tagged.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fs := NewOSFS()
	fs.SetTags(path, []string{"red", "blue"})

	got, err := readFinderTags(path)
	if err != nil {
		t.Fatalf("readFinderTags: %v", err)
	}
	want := []string{"red", "blue"}
	if !stringSliceEqual(got, want) {
		t.Errorf("tags = %v, want %v", got, want)
	}
}

func TestReadFinderCommentRoundTrip(t *testing.T) {
	requireXattr(t)
	tmp := t.TempDir()
	path := filepath.Join(tmp, "commented.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fs := NewOSFS()
	fs.SetFinderComment(path, "classified as document")

	got, err := readFinderComment(path)
	if err != nil {
		t.Fatalf("readFinderComment: %v", err)
	}
	want := "classified as document"
	if got != want {
		t.Errorf("comment = %q, want %q", got, want)
	}
}

func TestOSFSSetTagsMergesWithExisting(t *testing.T) {
	requireXattr(t)
	tmp := t.TempDir()
	path := filepath.Join(tmp, "tagged.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fs := NewOSFS()
	fs.SetTags(path, []string{"images", "photo"})
	fs.SetTags(path, []string{"Images", "new"})

	got, err := readFinderTags(path)
	if err != nil {
		t.Fatalf("readFinderTags: %v", err)
	}
	want := []string{"Images", "photo", "new"}
	if !stringSliceEqual(got, want) {
		t.Errorf("merged tags = %v, want %v", got, want)
	}
}

func TestOSFSSetFinderCommentAppends(t *testing.T) {
	requireXattr(t)
	tmp := t.TempDir()
	path := filepath.Join(tmp, "commented.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fs := NewOSFS()
	fs.SetFinderComment(path, "classified as document")
	fs.SetFinderComment(path, "moved to Documents")

	got, err := readFinderComment(path)
	if err != nil {
		t.Fatalf("readFinderComment: %v", err)
	}
	want := "classified as document; moved to Documents"
	if got != want {
		t.Errorf("merged comment = %q, want %q", got, want)
	}
}

func TestOSFSSetFinderCommentSkipsDuplicate(t *testing.T) {
	requireXattr(t)
	tmp := t.TempDir()
	path := filepath.Join(tmp, "commented.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	fs := NewOSFS()
	fs.SetFinderComment(path, "classified as document")
	fs.SetFinderComment(path, "classified as document")

	got, err := readFinderComment(path)
	if err != nil {
		t.Fatalf("readFinderComment: %v", err)
	}
	want := "classified as document"
	if got != want {
		t.Errorf("comment = %q, want %q", got, want)
	}
}

func TestOSFSSetTagsFallbackOnInvalidExisting(t *testing.T) {
	requireXattr(t)
	tmp := t.TempDir()
	path := filepath.Join(tmp, "tagged.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write an invalid xattr value that readFinderTags cannot parse.
	if err := exec.Command("xattr", "-w", "-x", "com.apple.metadata:_kMDItemUserTags", "deadbeef", path).Run(); err != nil {
		t.Fatalf("write invalid xattr: %v", err)
	}

	fs := NewOSFS()
	fs.SetTags(path, []string{"red", "blue"})

	got, err := readFinderTags(path)
	if err != nil {
		t.Fatalf("readFinderTags after fallback write: %v", err)
	}
	want := []string{"red", "blue"}
	if !stringSliceEqual(got, want) {
		t.Errorf("tags after fallback = %v, want %v", got, want)
	}
}

func TestOSFSSetFinderCommentFallbackOnInvalidExisting(t *testing.T) {
	requireXattr(t)
	tmp := t.TempDir()
	path := filepath.Join(tmp, "commented.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write an invalid xattr value that readFinderComment cannot parse.
	if err := exec.Command("xattr", "-w", "-x", "com.apple.metadata:kMDItemFinderComment", "deadbeef", path).Run(); err != nil {
		t.Fatalf("write invalid xattr: %v", err)
	}

	fs := NewOSFS()
	fs.SetFinderComment(path, "classified as document")

	got, err := readFinderComment(path)
	if err != nil {
		t.Fatalf("readFinderComment after fallback write: %v", err)
	}
	want := "classified as document"
	if got != want {
		t.Errorf("comment after fallback = %q, want %q", got, want)
	}
}
