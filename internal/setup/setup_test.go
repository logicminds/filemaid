package setup

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	calls    []string
	runErr   error
	lookPath map[string]string
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{lookPath: map[string]string{}}
}

func (r *fakeRunner) Run(name string, arg ...string) error {
	r.calls = append(r.calls, name+" "+strings.Join(arg, " "))
	return r.runErr
}

func (r *fakeRunner) LookPath(name string) (string, error) {
	if p, ok := r.lookPath[name]; ok {
		return p, nil
	}
	return "", os.ErrNotExist
}

func writeFile(t *testing.T, path string, data []byte, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, perm); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func newTestInstaller(t *testing.T, exe string) (*Installer, *fakeRunner, string) {
	t.Helper()
	home := t.TempDir()
	runner := newFakeRunner()
	inst := &Installer{
		FS:             osFS{},
		Runner:         runner,
		Home:           home,
		UID:            501,
		Now:            time.Date(2026, 6, 19, 0, 0, 0, 0, time.UTC),
		ExecutablePath: exe,
	}
	return inst, runner, home
}

func prepareExecutable(t *testing.T, dir string) string {
	t.Helper()
	exe := filepath.Join(dir, "filemaid")
	writeFile(t, exe, []byte("binary"), 0o755)
	return exe
}

func TestCheckRequirements(t *testing.T) {
	runner := newFakeRunner()

	info, err := checkRequirements(runner)
	if err != nil {
		t.Fatalf("checkRequirements: %v", err)
	}
	if info.OllamaInstalled {
		t.Error("expected ollama not installed")
	}
	if info.Recommended == "" {
		t.Error("expected a recommended model")
	}

	runner.lookPath["ollama"] = "/usr/local/bin/ollama"
	info, err = checkRequirements(runner)
	if err != nil {
		t.Fatalf("checkRequirements: %v", err)
	}
	if !info.OllamaInstalled {
		t.Error("expected ollama installed")
	}
	if runtime.GOOS == "darwin" && info.TotalMemoryGB <= 0 {
		t.Error("expected positive memory on macOS")
	}
}

func TestSelectModel(t *testing.T) {
	info := &systemInfo{
		TotalMemoryGB: 24,
		Recommended:   "filemaid-gemma4-26b",
		Choices:       []string{"filemaid-gemma4-26b", "filemaid-gemma4-12b", "filemaid-metadata"},
	}

	// Empty input accepts the recommended model.
	reader := bufio.NewReader(strings.NewReader("\n"))
	got, err := selectModel(InstallOptions{}, info, reader)
	if err != nil {
		t.Fatalf("selectModel: %v", err)
	}
	if got != "filemaid-gemma4-26b" {
		t.Errorf("got %q, want 26b", got)
	}

	// Explicit choice.
	reader = bufio.NewReader(strings.NewReader("2\n"))
	got, err = selectModel(InstallOptions{}, info, reader)
	if err != nil {
		t.Fatalf("selectModel: %v", err)
	}
	if got != "filemaid-gemma4-12b" {
		t.Errorf("got %q, want 12b", got)
	}

	// --model flag.
	got, err = selectModel(InstallOptions{ModelName: "filemaid-metadata"}, info, nil)
	if err != nil {
		t.Fatalf("selectModel: %v", err)
	}
	if got != "filemaid-metadata" {
		t.Errorf("got %q, want metadata", got)
	}

	// Unknown flag.
	_, err = selectModel(InstallOptions{ModelName: "nope"}, info, nil)
	if err == nil {
		t.Error("expected error for unknown model")
	}
}

func TestInstallFull(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, runner, home := newTestInstaller(t, exe)
	runner.lookPath["ollama"] = "/usr/local/bin/ollama"

	if err := inst.Install(InstallOptions{ModelName: "filemaid-gemma4-12b"}); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	binaryPath := filepath.Join(home, ".local", "bin", "filemaid")
	if info, err := os.Stat(binaryPath); err != nil {
		t.Errorf("binary not copied: %v", err)
	} else if info.Mode().Perm()&0111 == 0 {
		t.Error("binary is not executable")
	}

	dstConfig := filepath.Join(home, ".config", "filemaid", "config.json")
	if _, err := os.Stat(dstConfig); err != nil {
		t.Errorf("config not copied: %v", err)
	}
	configData, err := os.ReadFile(dstConfig)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(configData), `"model": "filemaid-gemma4-12b"`) {
		t.Errorf("config does not contain selected model: %s", configData)
	}

	dstModelfiles := filepath.Join(home, ".config", "filemaid", "modelfiles")
	for _, name := range []string{"Modelfile.filemaid-gemma4-26b", "Modelfile.filemaid-gemma4-12b", "Modelfile.filemaid-metadata"} {
		if _, err := os.Stat(filepath.Join(dstModelfiles, name)); err != nil {
			t.Errorf("modelfile %s not copied: %v", name, err)
		}
	}

	scanPlist := filepath.Join(home, "Library", "LaunchAgents", "biz.logicminds.filemaid.scan.plist")
	cleanupPlist := filepath.Join(home, "Library", "LaunchAgents", "biz.logicminds.filemaid.cleanup.plist")
	if _, err := os.Stat(scanPlist); err != nil {
		t.Errorf("scan plist not written: %v", err)
	}
	if _, err := os.Stat(cleanupPlist); err != nil {
		t.Errorf("cleanup plist not written: %v", err)
	}

	cleanupData, err := os.ReadFile(cleanupPlist)
	if err != nil {
		t.Fatalf("read cleanup plist: %v", err)
	}
	cleanup := string(cleanupData)
	for _, want := range []string{"<integer>6</integer>", "<integer>12</integer>", "<integer>18</integer>", "<integer>23</integer>"} {
		if !strings.Contains(cleanup, want) {
			t.Errorf("cleanup plist missing entry %s", want)
		}
	}

	scanData, err := os.ReadFile(scanPlist)
	if err != nil {
		t.Fatalf("read scan plist: %v", err)
	}
	scan := string(scanData)
	if !strings.Contains(scan, "<integer>900</integer>") {
		t.Error("scan plist missing 900 interval")
	}
	if !strings.Contains(scan, binaryPath+`" scan`) {
		t.Errorf("scan plist does not reference binary path: %s", scan)
	}

	assertCall(t, runner.calls, "launchctl bootstrap gui/501 "+cleanupPlist)
	assertCall(t, runner.calls, "launchctl bootstrap gui/501 "+scanPlist)
	assertCall(t, runner.calls, "ollama create filemaid-gemma4-26b -f "+filepath.Join(dstModelfiles, "Modelfile.filemaid-gemma4-26b"))
	assertCall(t, runner.calls, "ollama create filemaid-gemma4-12b -f "+filepath.Join(dstModelfiles, "Modelfile.filemaid-gemma4-12b"))
	assertCall(t, runner.calls, "ollama create filemaid-metadata -f "+filepath.Join(dstModelfiles, "Modelfile.filemaid-metadata"))

	statePath := filepath.Join(home, ".local", "share", "filemaid", "setup.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("setup.json not written: %v", err)
	}
	var state SetupState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("invalid setup.json: %v", err)
	}
	if state.BinaryPath != binaryPath {
		t.Errorf("binary path mismatch: got %s want %s", state.BinaryPath, binaryPath)
	}
	if state.ScanAgent != scanLabel || state.CleanupAgent != cleanupLabel {
		t.Errorf("agent labels mismatch: got %+v", state)
	}
	if !state.InstalledAt.Equal(inst.Now) {
		t.Errorf("installed at mismatch: got %v want %v", state.InstalledAt, inst.Now)
	}
}

func TestInstallNoScan(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, runner, home := newTestInstaller(t, exe)
	runner.lookPath["ollama"] = "/usr/local/bin/ollama"

	launchdDir := filepath.Join(home, "Library", "LaunchAgents")
	mkdir(t, launchdDir)
	scanPlist := filepath.Join(launchdDir, "biz.logicminds.filemaid.scan.plist")
	writeFile(t, scanPlist, []byte("old"), 0o644)

	if err := inst.Install(InstallOptions{NoScan: true, ModelName: "filemaid-gemma4-12b"}); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	cleanupPlist := filepath.Join(home, "Library", "LaunchAgents", "biz.logicminds.filemaid.cleanup.plist")
	if _, err := os.Stat(cleanupPlist); err != nil {
		t.Errorf("cleanup plist not written: %v", err)
	}
	if _, err := os.Stat(scanPlist); !os.IsNotExist(err) {
		t.Errorf("scan plist should have been removed")
	}

	assertCall(t, runner.calls, "launchctl bootstrap gui/501 "+cleanupPlist)

	for _, call := range runner.calls {
		if strings.Contains(call, "scan.plist") {
			t.Errorf("unexpected scan-related call: %s", call)
		}
	}
}

func TestInstallPreservesExistingConfig(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, _, home := newTestInstaller(t, exe)
	runner := newFakeRunner()
	inst.Runner = runner
	runner.lookPath["ollama"] = "/usr/local/bin/ollama"

	configDir := filepath.Join(home, ".config", "filemaid")
	mkdir(t, configDir)
	existing := filepath.Join(configDir, "config.json")
	writeFile(t, existing, []byte(`{"model":"custom"}`), 0o644)

	if err := inst.Install(InstallOptions{ModelName: "filemaid-gemma4-12b"}); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	data, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(data) != `{"model":"custom"}` {
		t.Errorf("existing config was overwritten: %s", data)
	}
}

func TestInstallCustomDirs(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, runner, home := newTestInstaller(t, exe)
	runner.lookPath["ollama"] = "/usr/local/bin/ollama"

	opts := InstallOptions{
		BinDir:    filepath.Join(home, "bin"),
		ConfigDir: filepath.Join(home, "etc", "filemaid"),
		DataDir:   filepath.Join(home, "var", "filemaid"),
		ModelName: "filemaid-gemma4-12b",
	}
	if err := inst.Install(opts); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, "bin", "filemaid")); err != nil {
		t.Errorf("binary not in custom bin dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "etc", "filemaid", "config.json")); err != nil {
		t.Errorf("config not in custom config dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "var", "filemaid", "setup.json")); err != nil {
		t.Errorf("state not in custom data dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "biz.logicminds.filemaid.cleanup.plist")); err != nil {
		t.Errorf("plist not in launchd dir: %v", err)
	}
}

func TestInstallNoOllama(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, _, _ := newTestInstaller(t, exe)

	if err := inst.Install(InstallOptions{}); err == nil {
		t.Fatal("expected error when ollama is not installed")
	}
}

func TestInstallOllamaNotRunning(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, _, _ := newTestInstaller(t, exe)
	// LookPath finds it, but Run("ollama", "list") will fail because it is a fake.
	fr := inst.Runner.(*fakeRunner)
	fr.lookPath["ollama"] = "/usr/local/bin/ollama"

	if err := inst.Install(InstallOptions{}); err == nil {
		t.Fatal("expected error when ollama is not running")
	}
}

func TestRenderPlists(t *testing.T) {
	scan, err := RenderScanPlist("/usr/local/bin", "/usr/local/share/filemaid")
	if err != nil {
		t.Fatalf("render scan plist: %v", err)
	}
	if !strings.Contains(scan, "<string>biz.logicminds.filemaid.scan</string>") {
		t.Error("scan plist missing label")
	}
	if !strings.Contains(scan, "/usr/local/bin/filemaid\"") {
		t.Error("scan plist missing binary path")
	}

	cleanup, err := RenderCleanupPlist("/usr/local/bin", "/usr/local/share/filemaid")
	if err != nil {
		t.Fatalf("render cleanup plist: %v", err)
	}
	if !strings.Contains(cleanup, "<string>biz.logicminds.filemaid.cleanup</string>") {
		t.Error("cleanup plist missing label")
	}
	if !strings.Contains(cleanup, "<integer>6</integer>") {
		t.Error("cleanup plist missing 06:00")
	}

	// On macOS, validate that the rendered plists are well-formed.
	if runtime.GOOS == "darwin" {
		dir := t.TempDir()
		scanPath := filepath.Join(dir, "scan.plist")
		cleanupPath := filepath.Join(dir, "cleanup.plist")
		writeFile(t, scanPath, []byte(scan), 0o644)
		writeFile(t, cleanupPath, []byte(cleanup), 0o644)

		for _, p := range []string{scanPath, cleanupPath} {
			cmd := exec.Command("plutil", "-lint", p)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("plutil -lint %s: %v\n%s", p, err, out)
			}
		}
	}
}

func TestXmlEscape(t *testing.T) {
	got := xmlEscape(`a "quoted" & 'tagged' <value>`)
	want := `a &quot;quoted&quot; &amp; &apos;tagged&apos; &lt;value&gt;`
	if got != want {
		t.Errorf("xmlEscape = %q, want %q", got, want)
	}
}

func assertCall(t *testing.T, calls []string, want string) {
	t.Helper()
	for _, c := range calls {
		if c == want {
			return
		}
	}
	t.Errorf("expected call %q not found in %v", want, calls)
}

func TestNewDefaultInstaller(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	inst, err := newDefaultInstaller()
	if err != nil {
		t.Fatalf("newDefaultInstaller: %v", err)
	}
	if inst == nil {
		t.Fatal("expected installer")
	}
	if inst.Home != home {
		t.Errorf("home mismatch: got %s want %s", inst.Home, home)
	}
}

func TestLoudRunner(t *testing.T) {
	r := loudRunner{}
	if err := r.Run("go", "version"); err != nil {
		t.Errorf("Run go version: %v", err)
	}
	if _, err := r.LookPath("go"); err != nil {
		t.Errorf("LookPath go: %v", err)
	}
}

func TestQuietRunner(t *testing.T) {
	r := quietRunner{}
	// quietRunner should return an error for a missing command, but not print anything.
	if err := r.Run("/nonexistent-command-xyz"); err == nil {
		t.Error("expected error for missing command")
	}
}

func TestOSCopyFileErrors(t *testing.T) {
	fs := osFS{}
	if err := fs.CopyFile("/nonexistent/file", filepath.Join(t.TempDir(), "dst")); err == nil {
		t.Error("expected error for missing source")
	}

	readonly := filepath.Join(t.TempDir(), "readonly")
	mkdir(t, readonly)
	if err := os.Chmod(readonly, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer os.Chmod(readonly, 0o755)

	src := filepath.Join(t.TempDir(), "src")
	writeFile(t, src, []byte("x"), 0o644)
	if err := fs.CopyFile(src, filepath.Join(readonly, "file")); err == nil {
		t.Error("expected error for unwritable destination")
	}
}

func TestInstallBootstrapFailure(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, runner, _ := newTestInstaller(t, exe)
	runner.lookPath["ollama"] = "/usr/local/bin/ollama"
	runner.runErr = fmt.Errorf("launchctl failed")

	if err := inst.Install(InstallOptions{ModelName: "filemaid-gemma4-12b"}); err == nil {
		t.Fatal("expected error when bootstrap fails")
	}
}
