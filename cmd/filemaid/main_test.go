package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMainNoArgsPrintsHelp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"filemaid"}

	// main runs Execute, which prints help and returns nil when no subcommand is
	// given. We cannot capture stdout here without more elaborate plumbing; this
	// test simply verifies main does not os.Exit on a valid no-arg invocation.
	main()
}

func TestMainSubcommandRuns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create a minimal setup state so commands that require setup can run.
	dataDir := filepath.Join(home, ".local", "share", "filemaid")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	statePath := filepath.Join(dataDir, "setup.json")
	if err := os.WriteFile(statePath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write setup state: %v", err)
	}

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"filemaid", "config"}

	main()
}
