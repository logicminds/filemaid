package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	scanGetCandidates = func(dir string) ([]string, []string, error) {
		return []string{src}, nil, nil
	}
	t.Cleanup(func() { scanGetCandidates = defaultScanGetCandidates })

	if _, err := runScanDir(context.Background(), filepath.Join(tmp, "Desktop"), "run-test", io.Discard, ""); err != nil {
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
	_, _ = runScanDir(context.Background(), filepath.Join(tmp, "NotAllowed"), "run-test", io.Discard, "")

	if !bytes.Contains(buf.Bytes(), []byte("scan directory not allowed")) {
		t.Errorf("expected 'scan directory not allowed' log, got %q", buf.String())
	}
}

func TestScanDirPermissionError(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()

	scanGetCandidates = func(dir string) ([]string, []string, error) {
		return nil, nil, os.ErrPermission
	}
	t.Cleanup(func() { scanGetCandidates = defaultScanGetCandidates })

	buf := captureSlog(t)
	_, _ = runScanDir(context.Background(), filepath.Join(tmp, "Desktop"), "run-test", io.Discard, "")

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

	scanGetCandidates = func(dir string) ([]string, []string, error) {
		t.Fatal("scanGetCandidates should not be called when validation fails")
		return nil, nil, nil
	}
	t.Cleanup(func() { scanGetCandidates = defaultScanGetCandidates })

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

	scanGetCandidates = func(dir string) ([]string, []string, error) {
		return []string{src}, nil, nil
	}
	t.Cleanup(func() { scanGetCandidates = defaultScanGetCandidates })

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

func TestScanDirIncludesDirectoriesWhenFlagSet(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	dirPath := filepath.Join(tmp, "Desktop", "project-folder")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}

	includeDirs = true
	t.Cleanup(func() { includeDirs = false })
	scanGetCandidates = func(dir string) ([]string, []string, error) {
		return nil, []string{dirPath}, nil
	}
	t.Cleanup(func() { scanGetCandidates = defaultScanGetCandidates })

	results, err := runScanDir(context.Background(), filepath.Join(tmp, "Desktop"), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Kind != "directory" {
		t.Errorf("kind = %q, want directory", results[0].Kind)
	}
	if results[0].Action != "review" {
		t.Errorf("action = %q, want review", results[0].Action)
	}
	records := db.(*state.FakeRepo).Records()
	if len(records) != 0 {
		t.Errorf("expected no history records for directory candidates, got %d", len(records))
	}
}

func TestScanDirSkipsHiddenDirectories(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	dirPath := filepath.Join(tmp, "Desktop", ".hidden-dir")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}

	includeDirs = true
	t.Cleanup(func() { includeDirs = false })
	scanGetCandidates = func(dir string) ([]string, []string, error) {
		return nil, []string{dirPath}, nil
	}
	t.Cleanup(func() { scanGetCandidates = defaultScanGetCandidates })

	results, err := runScanDir(context.Background(), filepath.Join(tmp, "Desktop"), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestScanDirSkipsDirectoriesOutsideAllowedDirs(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	dirPath := filepath.Join(tmp, "Outside", "project-folder")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}

	includeDirs = true
	t.Cleanup(func() { includeDirs = false })
	scanGetCandidates = func(dir string) ([]string, []string, error) {
		return nil, []string{dirPath}, nil
	}
	t.Cleanup(func() { scanGetCandidates = defaultScanGetCandidates })

	results, err := runScanDir(context.Background(), filepath.Join(tmp, "Outside"), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestScanDirRespectsMinAgeForDirectories(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.MinAgeHours = 1
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	recentDir := filepath.Join(tmp, "Desktop", "recent-project")
	if err := os.MkdirAll(recentDir, 0755); err != nil {
		t.Fatal(err)
	}
	recentTime := time.Now()
	if err := os.Chtimes(recentDir, recentTime, recentTime); err != nil {
		t.Fatal(err)
	}

	includeDirs = true
	t.Cleanup(func() { includeDirs = false })
	scanGetCandidates = func(dir string) ([]string, []string, error) {
		return nil, []string{recentDir}, nil
	}
	t.Cleanup(func() { scanGetCandidates = defaultScanGetCandidates })

	results, err := runScanDir(context.Background(), filepath.Join(tmp, "Desktop"), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for recent directory, got %d", len(results))
	}
}

func TestScanDirWithoutFlagSkipsDirectories(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	dirPath := filepath.Join(tmp, "Desktop", "project-folder")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}

	includeDirs = false
	scanGetCandidates = func(dir string) ([]string, []string, error) {
		return nil, []string{dirPath}, nil
	}
	t.Cleanup(func() { scanGetCandidates = defaultScanGetCandidates })

	results, err := runScanDir(context.Background(), filepath.Join(tmp, "Desktop"), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}
