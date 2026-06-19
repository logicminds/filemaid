package actions

import (
	"os"
	"path/filepath"
	"strings"
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
	result, err := Apply(decision, src, cfg, db, false, fs)
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
	result, err := Apply(decision, src, cfg, db, false, fs)
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
	result, err := Apply(decision, src, cfg, db, false, fs)
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
	result, err := Apply(decision, src, cfg, db, false, fs)
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
	result, err := Apply(decision, src, cfg, db, false, fs)
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
	if _, err := Apply(decision, src, cfg, db, false, fs); err != nil {
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
	if _, err := Apply(decision, src, cfg, db, false, fs); err != nil {
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
	if _, err := Apply(decision, src1, cfg, db, false, fs); err != nil {
		t.Fatal(err)
	}

	src2 := filepath.Join(tmp, "Desktop", "b.txt")
	if err := os.WriteFile(src2, []byte("same content"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Apply(decision, src2, cfg, db, false, fs)
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
	if _, err := Apply(decision, src, cfg, db, false, fs); err != nil {
		t.Fatal(err)
	}
	if len(fs.Tags) != 1 {
		t.Fatalf("tagged %d times, want 1", len(fs.Tags))
	}
	if !stringSliceEqual(fs.Tags[0].Tags, []string{"image", "desktop"}) {
		t.Errorf("tags = %v, want [image desktop]", fs.Tags[0].Tags)
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
	if _, err := Apply(decision, src, cfg, db, false, fs); err != nil {
		t.Fatal(err)
	}
	if len(fs.Tags) != 1 {
		t.Fatalf("tagged %d times, want 1", len(fs.Tags))
	}
	want := []string{"cat", "photo"}
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
	if _, err := Apply(decision, src, cfg, db, false, fs); err != nil {
		t.Fatal(err)
	}
	if len(fs.Tags) != 1 {
		t.Fatalf("tagged %d times, want 1", len(fs.Tags))
	}
	want := []string{"cat", "photo"}
	if !stringSliceEqual(fs.Tags[0].Tags, want) {
		t.Errorf("tags = %v, want %v", fs.Tags[0].Tags, want)
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
	result, err := Apply(decision, src, cfg, db, false, fs)
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
	_, err := Apply(decision, src, cfg, db, false, fs)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "move failed") {
		t.Errorf("error = %q, want move failed", err)
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
	result1, err := Apply(decision, src1, cfg, db, false, fs)
	if err != nil {
		t.Fatal(err)
	}
	result2, err := Apply(decision, src2, cfg, db, false, fs)
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
	result, err := Apply(decision, src, cfg, db, false, fs)
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
