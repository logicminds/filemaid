// Package setup provides installation and uninstallation support for filemaid.
package setup

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

// UninstallOptions controls the uninstall command.
type UninstallOptions struct{}

// Uninstaller removes filemaid agents, plists, and the installed binary. All
// dependencies are fields so tests can inject fakes.
type Uninstaller struct {
	FS     FS
	Runner Runner
	Home   string
	UID    int
}

// Uninstall removes filemaid agents, plists, and the installed binary. Config,
// logs, DB, and review queue are preserved.
func Uninstall(opts UninstallOptions) error {
	un, err := newDefaultUninstaller()
	if err != nil {
		return err
	}
	return un.Uninstall(opts)
}

func newDefaultUninstaller() (*Uninstaller, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	return &Uninstaller{
		FS:     osFS{},
		Runner: quietRunner{},
		Home:   home,
		UID:    os.Getuid(),
	}, nil
}

// Uninstall runs the uninstallation with the configured dependencies.
func (u *Uninstaller) Uninstall(opts UninstallOptions) error {
	dataDir := filepath.Join(u.Home, ".local", "share", "filemaid")
	statePath := filepath.Join(dataDir, "setup.json")

	state := defaultSetupState(u.Home)
	if data, err := u.FS.ReadFile(statePath); err == nil {
		if err := json.Unmarshal(data, &state); err != nil {
			state = defaultSetupState(u.Home)
		}
	}

	for _, label := range []string{state.ScanAgent, state.CleanupAgent} {
		if label == "" {
			continue
		}
		target := fmt.Sprintf("gui/%d/%s", u.UID, label)
		if err := u.Runner.Run("launchctl", "bootout", target); err != nil {
			slog.Debug("bootout agent", "target", target, "error", err)
		}
	}

	_ = u.FS.Remove(filepath.Join(state.LaunchdDir, scanLabel+".plist"))
	_ = u.FS.Remove(filepath.Join(state.LaunchdDir, cleanupLabel+".plist"))
	_ = u.FS.Remove(state.BinaryPath)

	return nil
}

func defaultSetupState(home string) SetupState {
	return SetupState{
		BinaryPath:   filepath.Join(home, ".local", "bin", "filemaid"),
		ConfigDir:    filepath.Join(home, ".config", "filemaid"),
		DataDir:      filepath.Join(home, ".local", "share", "filemaid"),
		LaunchdDir:   filepath.Join(home, "Library", "LaunchAgents"),
		ScanAgent:    scanLabel,
		CleanupAgent: cleanupLabel,
	}
}
