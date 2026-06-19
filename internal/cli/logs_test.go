package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailLogs(t *testing.T) {
	tmp := t.TempDir()
	log := filepath.Join(tmp, "filemaid.log")
	os.WriteFile(log, []byte("line1\nline2\nline3\n"), 0644)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	err = tailLogs(log, 2)
	w.Close()
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	out := buf.String()
	if !strings.Contains(out, "line2") {
		t.Errorf("expected line2, got %q", out)
	}
	if !strings.Contains(out, "line3") {
		t.Errorf("expected line3, got %q", out)
	}
	if strings.Contains(out, "line1") {
		t.Errorf("did not expect line1, got %q", out)
	}
}

func TestTailLogsMissingFile(t *testing.T) {
	tmp := t.TempDir()
	log := filepath.Join(tmp, "missing.log")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	err = tailLogs(log, 5)
	w.Close()
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	out := buf.String()
	if !strings.Contains(out, "log file not found") {
		t.Errorf("expected 'log file not found', got %q", out)
	}
}
