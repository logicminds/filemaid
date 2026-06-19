package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUninstallFromState(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, ".local", "share", "filemaid")
	launchdDir := filepath.Join(home, "Library", "LaunchAgents")
	binDir := filepath.Join(home, ".local", "bin")
	mkdir(t, dataDir)
	mkdir(t, launchdDir)
	mkdir(t, binDir)

	binaryPath := filepath.Join(binDir, "filemaid")
	writeFile(t, binaryPath, []byte("binary"), 0o755)
	writeFile(t, filepath.Join(launchdDir, "biz.logicminds.filemaid.scan.plist"), []byte("scan"), 0o644)
	writeFile(t, filepath.Join(launchdDir, "biz.logicminds.filemaid.cleanup.plist"), []byte("cleanup"), 0o644)

	state := SetupState{
		BinaryPath:   binaryPath,
		ConfigDir:    filepath.Join(home, ".config", "filemaid"),
		DataDir:      dataDir,
		LaunchdDir:   launchdDir,
		ScanAgent:    scanLabel,
		CleanupAgent: cleanupLabel,
	}
	stateJSON, _ := json.MarshalIndent(state, "", "  ")
	writeFile(t, filepath.Join(dataDir, "setup.json"), stateJSON, 0o644)

	runner := newFakeRunner()
	un := &Uninstaller{
		FS:     osFS{},
		Runner: runner,
		Home:   home,
		UID:    501,
	}

	if err := un.Uninstall(UninstallOptions{}); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	assertCall(t, runner.calls, "launchctl bootout gui/501/biz.logicminds.filemaid.scan")
	assertCall(t, runner.calls, "launchctl bootout gui/501/biz.logicminds.filemaid.cleanup")

	if _, err := os.Stat(binaryPath); !os.IsNotExist(err) {
		t.Error("binary should have been removed")
	}
	if _, err := os.Stat(filepath.Join(launchdDir, "biz.logicminds.filemaid.scan.plist")); !os.IsNotExist(err) {
		t.Error("scan plist should have been removed")
	}
	if _, err := os.Stat(filepath.Join(launchdDir, "biz.logicminds.filemaid.cleanup.plist")); !os.IsNotExist(err) {
		t.Error("cleanup plist should have been removed")
	}
}

func TestUninstallFallbackDefaults(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, ".local", "share", "filemaid")
	launchdDir := filepath.Join(home, "Library", "LaunchAgents")
	binDir := filepath.Join(home, ".local", "bin")
	mkdir(t, dataDir)
	mkdir(t, launchdDir)
	mkdir(t, binDir)

	binaryPath := filepath.Join(binDir, "filemaid")
	writeFile(t, binaryPath, []byte("binary"), 0o755)
	writeFile(t, filepath.Join(launchdDir, "biz.logicminds.filemaid.scan.plist"), []byte("scan"), 0o644)
	writeFile(t, filepath.Join(launchdDir, "biz.logicminds.filemaid.cleanup.plist"), []byte("cleanup"), 0o644)

	runner := newFakeRunner()
	un := &Uninstaller{
		FS:     osFS{},
		Runner: runner,
		Home:   home,
		UID:    502,
	}

	if err := un.Uninstall(UninstallOptions{}); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	assertCall(t, runner.calls, "launchctl bootout gui/502/biz.logicminds.filemaid.scan")
	assertCall(t, runner.calls, "launchctl bootout gui/502/biz.logicminds.filemaid.cleanup")
	if _, err := os.Stat(binaryPath); !os.IsNotExist(err) {
		t.Error("binary should have been removed")
	}
}

func TestUninstallPreservesConfigAndData(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, ".local", "share", "filemaid")
	configDir := filepath.Join(home, ".config", "filemaid")
	mkdir(t, dataDir)
	mkdir(t, configDir)

	writeFile(t, filepath.Join(configDir, "config.json"), []byte("config"), 0o644)
	writeFile(t, filepath.Join(dataDir, "filemaid.db"), []byte("db"), 0o644)
	writeFile(t, filepath.Join(dataDir, "filemaid.log"), []byte("log"), 0o644)
	writeFile(t, filepath.Join(dataDir, "setup.json"), []byte(`{}`), 0o644)

	un := &Uninstaller{
		FS:     osFS{},
		Runner: newFakeRunner(),
		Home:   home,
		UID:    501,
	}

	if err := un.Uninstall(UninstallOptions{}); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	for _, path := range []string{
		filepath.Join(configDir, "config.json"),
		filepath.Join(dataDir, "filemaid.db"),
		filepath.Join(dataDir, "filemaid.log"),
		filepath.Join(dataDir, "setup.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("preserved file missing %s: %v", path, err)
		}
	}
}

func TestUninstallMalformedState(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, ".local", "share", "filemaid")
	launchdDir := filepath.Join(home, "Library", "LaunchAgents")
	binDir := filepath.Join(home, ".local", "bin")
	mkdir(t, dataDir)
	mkdir(t, launchdDir)
	mkdir(t, binDir)

	binaryPath := filepath.Join(binDir, "filemaid")
	writeFile(t, binaryPath, []byte("binary"), 0o755)
	writeFile(t, filepath.Join(dataDir, "setup.json"), []byte("not-json"), 0o644)
	writeFile(t, filepath.Join(launchdDir, "biz.logicminds.filemaid.cleanup.plist"), []byte("cleanup"), 0o644)

	runner := newFakeRunner()
	un := &Uninstaller{
		FS:     osFS{},
		Runner: runner,
		Home:   home,
		UID:    501,
	}

	if err := un.Uninstall(UninstallOptions{}); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	assertCall(t, runner.calls, "launchctl bootout gui/501/biz.logicminds.filemaid.cleanup")
	if _, err := os.Stat(binaryPath); !os.IsNotExist(err) {
		t.Error("binary should have been removed")
	}
}

func TestUninstallIgnoresMissingFiles(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, ".local", "share", "filemaid")
	mkdir(t, dataDir)
	writeFile(t, filepath.Join(dataDir, "setup.json"), []byte(`{}`), 0o644)

	un := &Uninstaller{
		FS:     osFS{},
		Runner: newFakeRunner(),
		Home:   home,
		UID:    501,
	}

	if err := un.Uninstall(UninstallOptions{}); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}
}

func TestDefaultSetupState(t *testing.T) {
	home := "/Users/test"
	state := defaultSetupState(home)
	if state.BinaryPath != "/Users/test/.local/bin/filemaid" {
		t.Errorf("binary path: %s", state.BinaryPath)
	}
	if state.ConfigDir != "/Users/test/.config/filemaid" {
		t.Errorf("config dir: %s", state.ConfigDir)
	}
	if state.DataDir != "/Users/test/.local/share/filemaid" {
		t.Errorf("data dir: %s", state.DataDir)
	}
	if state.LaunchdDir != "/Users/test/Library/LaunchAgents" {
		t.Errorf("launchd dir: %s", state.LaunchdDir)
	}
}

func TestUninstallNoDuplicateCalls(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, ".local", "share", "filemaid")
	mkdir(t, dataDir)

	state := SetupState{
		BinaryPath:   filepath.Join(home, ".local", "bin", "filemaid"),
		ConfigDir:    filepath.Join(home, ".config", "filemaid"),
		DataDir:      dataDir,
		LaunchdDir:   filepath.Join(home, "Library", "LaunchAgents"),
		ScanAgent:    "",
		CleanupAgent: cleanupLabel,
	}
	stateJSON, _ := json.MarshalIndent(state, "", "  ")
	writeFile(t, filepath.Join(dataDir, "setup.json"), stateJSON, 0o644)

	runner := newFakeRunner()
	un := &Uninstaller{
		FS:     osFS{},
		Runner: runner,
		Home:   home,
		UID:    501,
	}

	if err := un.Uninstall(UninstallOptions{}); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}

	count := 0
	for _, call := range runner.calls {
		if strings.Contains(call, "launchctl bootout") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 bootout call, got %d", count)
	}
}

func TestNewDefaultUninstaller(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	un, err := newDefaultUninstaller()
	if err != nil {
		t.Fatalf("newDefaultUninstaller: %v", err)
	}
	if un == nil {
		t.Fatal("expected uninstaller")
	}
	if un.Home != home {
		t.Errorf("home mismatch: got %s want %s", un.Home, home)
	}
}

func TestUninstallDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := Uninstall(UninstallOptions{}); err != nil {
		t.Errorf("Uninstall: %v", err)
	}
}
