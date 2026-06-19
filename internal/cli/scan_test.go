package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/logicminds/filemaid/internal/actions"
	"github.com/logicminds/filemaid/internal/llm"
	"github.com/logicminds/filemaid/internal/state"
)

func TestScanDirProcessesFiles(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	classifier = &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Tags:     []string{},
		Action:   "move",
		Reason:   "text",
	}}

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("hello"), 0644)

	scanGetFiles = func(dir string) ([]string, error) {
		return []string{src}, nil
	}
	t.Cleanup(func() { scanGetFiles = defaultScanGetFiles })

	if err := runScanDir(filepath.Join(tmp, "Desktop")); err != nil {
		t.Fatal(err)
	}

	records := db.(*state.FakeRepo).Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(records))
	}
}

func TestScanDirRejectsNotAllowed(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()

	buf := captureSlog(t)
	runScanDir(filepath.Join(tmp, "NotAllowed"))

	if !bytes.Contains(buf.Bytes(), []byte("scan directory not allowed")) {
		t.Errorf("expected 'scan directory not allowed' log, got %q", buf.String())
	}
}

func TestScanDirPermissionError(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()

	scanGetFiles = func(dir string) ([]string, error) {
		return nil, os.ErrPermission
	}
	t.Cleanup(func() { scanGetFiles = defaultScanGetFiles })

	buf := captureSlog(t)
	runScanDir(filepath.Join(tmp, "Desktop"))

	if !bytes.Contains(buf.Bytes(), []byte("permission denied")) {
		t.Errorf("expected 'permission denied' log, got %q", buf.String())
	}
}
