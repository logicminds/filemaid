package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRootCommandRegistersAllSubcommands(t *testing.T) {
	want := map[string]bool{
		"process":   true,
		"scan":      true,
		"cleanup":   true,
		"review":    true,
		"logs":      true,
		"config":    true,
		"setup":     true,
		"uninstall": true,
	}

	for _, cmd := range rootCmd.Commands() {
		if want[cmd.Name()] {
			delete(want, cmd.Name())
		}
	}

	if len(want) > 0 {
		var missing []string
		for name := range want {
			missing = append(missing, name)
		}
		t.Errorf("missing subcommands: %v", missing)
	}
}

func TestProcessHelpText(t *testing.T) {
	cmd := findCmd(t, "process")
	if !strings.Contains(cmd.Use, "paths") {
		t.Errorf("process use text missing paths: %q", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("process short help is empty")
	}
}

func TestScanHelpFlags(t *testing.T) {
	cmd := findCmd(t, "scan")
	f := cmd.Flags().Lookup("dir")
	if f == nil {
		t.Fatal("scan missing --dir flag")
	}
	if f.Usage == "" {
		t.Error("scan --dir flag usage is empty")
	}
}

func TestCleanupHelpFlags(t *testing.T) {
	cmd := findCmd(t, "cleanup")
	if cmd.Flags().Lookup("dry-run") == nil {
		t.Error("cleanup missing --dry-run flag")
	}
	if cmd.Flags().Lookup("format") == nil {
		t.Error("cleanup missing --format flag")
	}
}

func TestReviewHelpFlags(t *testing.T) {
	cmd := findCmd(t, "review")
	if cmd.Flags().Lookup("open") == nil {
		t.Error("review missing --open flag")
	}
}

func TestLogsHelpFlags(t *testing.T) {
	cmd := findCmd(t, "logs")
	f := cmd.Flags().Lookup("tail")
	if f == nil {
		t.Fatal("logs missing --tail flag")
	}
	if f.DefValue != "20" {
		t.Errorf("logs --tail default = %q, want 20", f.DefValue)
	}
}

func TestSetupHelpFlags(t *testing.T) {
	cmd := findCmd(t, "setup")
	for _, name := range []string{"no-scan", "bin-dir", "config-dir", "data-dir"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("setup missing --%s flag", name)
		}
	}
}

func findCmd(t *testing.T, name string) *cobra.Command {
	t.Helper()
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == name {
			return cmd
		}
	}
	t.Fatalf("command %q not registered", name)
	return nil
}
