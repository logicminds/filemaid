package main

import (
	"os"
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

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"filemaid", "config"}

	main()
}
