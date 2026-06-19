package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
