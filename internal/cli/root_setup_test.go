package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func buildFilemaid(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "filemaid")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/filemaid")
	cmd.Dir = "../.."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build filemaid: %v\n%s", err, out)
	}
	return bin
}

func TestRequiresSetup(t *testing.T) {
	tests := []struct {
		name string
		cmd  *cobra.Command
		want bool
	}{
		{"root", rootCmd, false},
		{"setup", findCmd(t, "setup"), false},
		{"uninstall", findCmd(t, "uninstall"), false},
		{"scan", findCmd(t, "scan"), true},
		{"process", findCmd(t, "process"), true},
		{"cleanup", findCmd(t, "cleanup"), true},
		{"review", findCmd(t, "review"), true},
		{"logs", findCmd(t, "logs"), true},
		{"config", findCmd(t, "config"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requiresSetup(tt.cmd); got != tt.want {
				t.Errorf("requiresSetup(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestSetupWithoutSetupRuns(t *testing.T) {
	// setup and uninstall commands must not perform the setup check.
	if requiresSetup(findCmd(t, "setup")) {
		t.Fatal("setup command should not require prior setup")
	}
	if requiresSetup(findCmd(t, "uninstall")) {
		t.Fatal("uninstall command should not require prior setup")
	}
}

func TestScanBinaryRequiresSetup(t *testing.T) {
	bin := buildFilemaid(t)
	tmpHome := t.TempDir()

	cmd := exec.Command(bin, "scan", "--dir", t.TempDir())
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("scan without setup should exit non-zero")
	}
	if !strings.Contains(stderr.String(), "has not been set up") {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestHelpBinaryWorksWithoutSetup(t *testing.T) {
	bin := buildFilemaid(t)
	tmpHome := t.TempDir()

	cmd := exec.Command(bin, "--help")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("--help should exit zero without setup, got: %v\nstderr: %s", err, stderr.String())
	}
}

func TestSetupHelpBinaryWorksWithoutSetup(t *testing.T) {
	bin := buildFilemaid(t)
	tmpHome := t.TempDir()

	cmd := exec.Command(bin, "setup", "--help")
	cmd.Env = append(os.Environ(), "HOME="+tmpHome)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("setup --help should exit zero without setup, got: %v\nstderr: %s", err, stderr.String())
	}
}
