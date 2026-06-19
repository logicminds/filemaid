package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

	if _, err := runScanDir(filepath.Join(tmp, "Desktop")); err != nil {
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
	_, _ = runScanDir(filepath.Join(tmp, "NotAllowed"))

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
	_, _ = runScanDir(filepath.Join(tmp, "Desktop"))

	if !bytes.Contains(buf.Bytes(), []byte("permission denied")) {
		t.Errorf("expected 'permission denied' log, got %q", buf.String())
	}
}

func TestScanCommandFailsValidationBeforeScanning(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	fake := &fakeClassifier{validate: errors.New("model not ready")}
	classifier = fake

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("hello"), 0644)

	scanGetFiles = func(dir string) ([]string, error) {
		t.Fatal("scanGetFiles should not be called when validation fails")
		return nil, nil
	}
	t.Cleanup(func() { scanGetFiles = defaultScanGetFiles })

	err := scanCmd.RunE(nil, []string{})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "model validation failed") {
		t.Errorf("expected model validation failed error, got %v", err)
	}
	if !fake.validated {
		t.Error("expected Validate to be called")
	}

	records := db.(*state.FakeRepo).Records()
	if len(records) != 0 {
		t.Errorf("expected no history records, got %d", len(records))
	}
}

func TestScanCommandSucceedsWhenValidationPasses(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	fake := &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Tags:     []string{},
		Action:   "move",
		Reason:   "text",
	}}
	classifier = fake

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("hello"), 0644)

	scanGetFiles = func(dir string) ([]string, error) {
		return []string{src}, nil
	}
	t.Cleanup(func() { scanGetFiles = defaultScanGetFiles })

	scanDir = filepath.Join(tmp, "Desktop")
	t.Cleanup(func() { scanDir = "" })

	if err := scanCmd.RunE(nil, []string{}); err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if !fake.validated {
		t.Error("expected Validate to be called")
	}

	records := db.(*state.FakeRepo).Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(records))
	}
}
