package log

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitEmptyPath(t *testing.T) {
	if err := Init(""); err != nil {
		t.Fatalf("Init(\"\") failed: %v", err)
	}
}

func TestInitCreatesLogFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "filemaid.log")

	if err := Init(logPath); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("log file was not created: %v", err)
	}

	if logFile != nil {
		_ = logFile.Close()
	}
}

func TestInitCreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "nested", "filemaid.log")

	if err := Init(logPath); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("log file was not created: %v", err)
	}

	if logFile != nil {
		_ = logFile.Close()
	}
}
func TestInitWithOptionsDisablesStderr(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "filemaid.log")

	if err := InitWithOptions(logPath, Options{DisableStderr: true}); err != nil {
		t.Fatalf("InitWithOptions failed: %v", err)
	}

	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("log file was not created: %v", err)
	}

	if logFile != nil {
		_ = logFile.Close()
	}
}

func TestSetStderrEnabled(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "filemaid.log")

	if err := Init(logPath); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer func() {
		if logFile != nil {
			_ = logFile.Close()
		}
	}()

	// Disabling and re-enabling should not error.
	SetStderrEnabled(false)
	SetStderrEnabled(true)
}
