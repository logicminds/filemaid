package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/logicminds/filemaid/internal/actions"
	"github.com/logicminds/filemaid/internal/config"
	"github.com/logicminds/filemaid/internal/llm"
	"github.com/logicminds/filemaid/internal/state"

	"github.com/spf13/cobra"
)

func TestReviewQueueListsFiles(t *testing.T) {
	tmp := t.TempDir()
	reviewDir := filepath.Join(tmp, "review")
	sub := filepath.Join(reviewDir, "2026-01-01")
	os.MkdirAll(sub, 0755)
	os.WriteFile(filepath.Join(sub, "file.txt"), []byte("hello"), 0644)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	err = reviewQueue(reviewDir, false)
	w.Close()
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	out := buf.String()
	if !strings.Contains(out, "file.txt") {
		t.Errorf("expected file.txt in output, got %q", out)
	}
	if !strings.Contains(out, "bytes") {
		t.Errorf("expected size in output, got %q", out)
	}
}

func TestReviewQueueEmpty(t *testing.T) {
	tmp := t.TempDir()
	emptyDir := filepath.Join(tmp, "empty_review")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	err = reviewQueue(emptyDir, false)
	w.Close()
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	out := buf.String()
	if !strings.Contains(out, "review queue is empty") {
		t.Errorf("expected empty queue message, got %q", out)
	}
}

func TestReviewQueueOpenFinderError(t *testing.T) {
	tmp := t.TempDir()
	missing := filepath.Join(tmp, "does-not-exist")
	if err := reviewQueue(missing, true); err == nil {
		t.Fatal("expected error when opening missing review dir")
	}
}
func TestReviewQueueDisplaysRename(t *testing.T) {
	tmp := t.TempDir()
	reviewDir := filepath.Join(tmp, "review")
	sub := filepath.Join(reviewDir, "2026-01-01")
	os.MkdirAll(sub, 0755)
	os.WriteFile(filepath.Join(sub, "photo.jpg"), []byte("hello"), 0644)

	fakeDB := state.NewFake()
	fakeDB.Record(state.RecordInput{
		OriginalPath: filepath.Join(tmp, "Desktop", "photo.jpg"),
		FinalPath:    filepath.Join(sub, "photo.jpg"),
		SHA256:       "abc",
		Category:     "Images",
		Action:       "review",
		Reason:       "uncertain",
		OriginalName: "photo.jpg",
		NewName:      "vacation-photo.jpg",
	})
	db = fakeDB
	cfg = &config.Config{ReviewDir: reviewDir}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	err = reviewQueue(reviewDir, false)
	w.Close()
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	out := buf.String()
	if !strings.Contains(out, "photo.jpg") {
		t.Errorf("expected photo.jpg in output, got %q", out)
	}
	if !strings.Contains(out, "photo.jpg -> vacation-photo.jpg") {
		t.Errorf("expected rename in output, got %q", out)
	}
	if !strings.Contains(out, "Images") {
		t.Errorf("expected category in output, got %q", out)
	}
}

func TestReviewApproveMovesFile(t *testing.T) {
	tmp := t.TempDir()
	reviewDir := filepath.Join(tmp, "review")
	archiveDir := filepath.Join(tmp, "Documents", "Archive")
	sub := filepath.Join(reviewDir, "2026-01-01")
	os.MkdirAll(sub, 0755)
	src := filepath.Join(sub, "photo.jpg")
	os.WriteFile(src, []byte("hello"), 0644)

	fakeDB := state.NewFake()
	fakeDB.Record(state.RecordInput{
		OriginalPath: filepath.Join(tmp, "Desktop", "photo.jpg"),
		FinalPath:    src,
		SHA256:       "abc",
		Category:     "Images",
		Action:       "review",
		Reason:       "uncertain",
		OriginalName: "photo.jpg",
		NewName:      "vacation-photo.jpg",
	})
	db = fakeDB
	cfg = &config.Config{
		ReviewDir:   reviewDir,
		AllowedDirs: []string{tmp},
		Categories:  map[string]string{"Images": archiveDir},
		MoveFiles:   true,
	}
	reviewFS = actions.NewRecordingFS()

	if err := reviewApprovePath(reviewDir, "2026-01-01/photo.jpg"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("approved file should have been moved from review")
	}
}

func TestReviewRejectTrashesFile(t *testing.T) {
	tmp := t.TempDir()
	reviewDir := filepath.Join(tmp, "review")
	sub := filepath.Join(reviewDir, "2026-01-01")
	os.MkdirAll(sub, 0755)
	src := filepath.Join(sub, "photo.jpg")
	os.WriteFile(src, []byte("hello"), 0644)

	fakeDB := state.NewFake()
	fakeDB.Record(state.RecordInput{
		OriginalPath: filepath.Join(tmp, "Desktop", "photo.jpg"),
		FinalPath:    src,
		SHA256:       "abc",
		Category:     "Images",
		Action:       "review",
		Reason:       "uncertain",
		OriginalName: "photo.jpg",
		NewName:      "vacation-photo.jpg",
	})
	db = fakeDB
	cfg = &config.Config{ReviewDir: reviewDir}
	fs := actions.NewRecordingFS()
	reviewFS = fs

	if err := reviewRejectPath(reviewDir, "2026-01-01/photo.jpg"); err != nil {
		t.Fatal(err)
	}
	if len(fs.Trashed) != 1 {
		t.Errorf("expected 1 trash call, got %d", len(fs.Trashed))
	}
}
func TestReviewRetryReprocessesTransientFailures(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.MoveFiles = true
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	classifier = &fakeClassifier{decision: llm.Decision{
		Category:    "Documents",
		Action:      "move",
		Reason:      "text file",
		Destination: "",
	}}

	reviewPath := filepath.Join(cfg.ReviewDir, "note.txt")
	if err := os.WriteFile(reviewPath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.Record(state.RecordInput{
		OriginalPath: reviewPath,
		FinalPath:    reviewPath,
		SHA256:       "abc",
		Category:     "Unknown",
		Action:       "review",
		Reason:       "ollama error after 2 retries: context deadline exceeded",
		OriginalName: "note.txt",
	}); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{Use: "review"}
	cmd.Flags().BoolVar(&reviewRetry, "retry", false, "")
	cmd.Flags().StringVar(&reviewRename, "rename", "", "")
	if err := cmd.Flags().Set("retry", "true"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := reviewRetryItems(cmd); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "1 file processed") {
		t.Errorf("expected retry summary, got %q", out)
	}
	fs, ok := processFS.(*actions.RecordingFS)
	if !ok {
		t.Fatal("expected RecordingFS")
	}
	if len(fs.Moved) != 1 {
		t.Fatalf("expected 1 move, got %d", len(fs.Moved))
	}
	if !strings.Contains(fs.Moved[0][1], "Documents") {
		t.Errorf("expected move to Documents, got %q", fs.Moved[0][1])
	}
}

func TestReviewRetrySkipsNonTransientItems(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	classifier = &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Action:   "move",
		Reason:   "text file",
	}}

	reviewPath := filepath.Join(cfg.ReviewDir, "note.txt")
	if err := os.WriteFile(reviewPath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.Record(state.RecordInput{
		OriginalPath: reviewPath,
		FinalPath:    reviewPath,
		SHA256:       "abc",
		Category:     "Unknown",
		Action:       "review",
		Reason:       "duplicate content detected; original reason: text file",
		OriginalName: "note.txt",
	}); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{Use: "review"}
	cmd.Flags().BoolVar(&reviewRetry, "retry", false, "")
	cmd.Flags().StringVar(&reviewRename, "rename", "", "")
	if err := cmd.Flags().Set("retry", "true"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := reviewRetryItems(cmd); err != nil {
		t.Fatal(err)
	}

	out := buf.String()
	if !strings.Contains(out, "No transient review items to retry") {
		t.Errorf("expected no items message, got %q", out)
	}
}

func TestReviewRetryAppliesRenameOverride(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.Rename = false
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	classifier = &fakeClassifier{decision: llm.Decision{
		Category:    "Documents",
		Action:      "move",
		Reason:      "text file",
		NewName:     "renamed-note.txt",
		NameQuality: 3,
	}}

	reviewPath := filepath.Join(cfg.ReviewDir, "note.txt")
	if err := os.WriteFile(reviewPath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.Record(state.RecordInput{
		OriginalPath: reviewPath,
		FinalPath:    reviewPath,
		SHA256:       "abc",
		Category:     "Unknown",
		Action:       "review",
		Reason:       "ollama error: context deadline exceeded",
		OriginalName: "note.txt",
	}); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{Use: "review"}
	cmd.Flags().BoolVar(&reviewRetry, "retry", false, "")
	cmd.Flags().StringVar(&reviewRename, "rename", "", "")
	if err := cmd.Flags().Set("retry", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("rename", "2"); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := reviewRetryItems(cmd); err != nil {
		t.Fatal(err)
	}

	if !cfg.Rename {
		t.Error("expected cfg.Rename to be enabled by --rename")
	}
	if cfg.RenameLevel != 2 {
		t.Errorf("cfg.RenameLevel = %d, want 2", cfg.RenameLevel)
	}
	out := buf.String()
	if !strings.Contains(out, "1 file processed") {
		t.Errorf("expected retry summary, got %q", out)
	}
}
