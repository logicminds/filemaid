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
