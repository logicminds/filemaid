package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsSetup_Missing(t *testing.T) {
	tmp := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	ok, err := IsSetup()
	if err != nil {
		t.Fatalf("IsSetup: unexpected error %v", err)
	}
	if ok {
		t.Fatal("IsSetup = true, want false")
	}
}

func TestIsSetup_Present(t *testing.T) {
	tmp := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	dataDir := filepath.Join(tmp, ".local", "share", "filemaid")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	statePath := filepath.Join(dataDir, "setup.json")
	if err := os.WriteFile(statePath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	ok, err := IsSetup()
	if err != nil {
		t.Fatalf("IsSetup: unexpected error %v", err)
	}
	if !ok {
		t.Fatal("IsSetup = false, want true")
	}
}
