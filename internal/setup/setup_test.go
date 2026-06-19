package setup

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	calls      []string
	runErr     error
	runErrFor  map[string]error
	lookPath   map[string]string
	outputs    map[string]string
	outputsSeq map[string][]string
	seqIndex   map[string]int
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		lookPath:   map[string]string{},
		outputs:    map[string]string{},
		runErrFor:  map[string]error{},
		outputsSeq: map[string][]string{},
		seqIndex:   map[string]int{},
	}
}

func (r *fakeRunner) Run(name string, arg ...string) error {
	key := name + " " + strings.Join(arg, " ")
	r.calls = append(r.calls, key)
	if e, ok := r.runErrFor[key]; ok {
		return e
	}
	return r.runErr
}

func (r *fakeRunner) RunOutput(name string, arg ...string) (string, error) {
	key := name + " " + strings.Join(arg, " ")
	r.calls = append(r.calls, key)
	if seq, ok := r.outputsSeq[key]; ok {
		idx := r.seqIndex[key]
		if idx < len(seq) {
			out := seq[idx]
			r.seqIndex[key] = idx + 1
			return out, nil
		}
	}
	if out, ok := r.outputs[key]; ok {
		return out, nil
	}
	if e, ok := r.runErrFor[key]; ok {
		return "", e
	}
	return "", r.runErr
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
		QuietRunner:    runner,
		Home:           home,
		UID:            501,
		Now:            time.Date(2026, 6, 19, 0, 0, 0, 0, time.UTC),
		ExecutablePath: exe,
		Sleep:          func(time.Duration) {},
		FreeSpace:      func(string) (uint64, error) { return 100 * 1024 * 1024 * 1024, nil },
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
	if info.OllamaRunning {
		t.Error("expected ollama not running when not installed")
	}
	if info.Recommended == "" {
		t.Error("expected a recommended model")
	}

	runner.lookPath["ollama"] = "/usr/local/bin/ollama"
	runner.outputs["ollama list"] = "filemaid-gemma4-12b\n"
	info, err = checkRequirements(runner)
	if err != nil {
		t.Fatalf("checkRequirements: %v", err)
	}
	if !info.OllamaInstalled {
		t.Error("expected ollama installed")
	}
	if !info.OllamaRunning {
		t.Error("expected ollama running")
	}
	if runtime.GOOS == "darwin" && info.TotalMemoryGB <= 0 {
		t.Error("expected positive memory on macOS")
	}

	// Simulate ollama installed but not responding.
	runner.runErr = fmt.Errorf("ollama not running")
	info, err = checkRequirements(runner)
	if err != nil {
		t.Fatalf("checkRequirements: %v", err)
	}
	if !info.OllamaInstalled {
		t.Error("expected ollama installed")
	}
	if info.OllamaRunning {
		t.Error("expected ollama not running")
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
	got, err := selectModel(InstallOptions{Interactive: true}, info, reader)
	if err != nil {
		t.Fatalf("selectModel: %v", err)
	}
	if got != "filemaid-gemma4-26b" {
		t.Errorf("got %q, want 26b", got)
	}

	// Explicit choice.
	reader = bufio.NewReader(strings.NewReader("2\n"))
	got, err = selectModel(InstallOptions{Interactive: true}, info, reader)
	if err != nil {
		t.Fatalf("selectModel: %v", err)
	}
	if got != "filemaid-gemma4-12b" {
		t.Errorf("got %q, want 12b", got)
	}

	// Non-interactive auto-select.
	got, err = selectModel(InstallOptions{}, info, nil)
	if err != nil {
		t.Fatalf("selectModel: %v", err)
	}
	if got != "filemaid-gemma4-26b" {
		t.Errorf("got %q, want 26b", got)
	}

	// --model flag.
	got, err = selectModel(InstallOptions{ModelName: "filemaid-metadata"}, info, nil)
	if err != nil {
		t.Fatalf("selectModel: %v", err)
	}
	if got != "filemaid-metadata" {
		t.Errorf("got %q, want metadata", got)
	}

	// Unknown --model flag.
	_, err = selectModel(InstallOptions{ModelName: "nope"}, info, nil)
	if err == nil {
		t.Error("expected error for unknown model")
	}
}

func TestInstallFull(t *testing.T) {
	dir := t.TempDir()
	xe := prepareExecutable(t, dir)
	inst, runner, home := newTestInstaller(t, xe)
	runner.lookPath["ollama"] = "/usr/local/bin/ollama"
	runner.outputs["ollama list"] = "filemaid-gemma4-12b\n"
	runner.outputs["ollama list"] = "filemaid-gemma4-12b\n"

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
	assertCall(t, runner.calls, "ollama create filemaid-gemma4-12b -f "+filepath.Join(dstModelfiles, "Modelfile.filemaid-gemma4-12b"))
	for _, unwanted := range []string{"filemaid-gemma4-26b", "filemaid-metadata"} {
		for _, call := range runner.calls {
			if strings.Contains(call, "ollama create "+unwanted) {
				t.Errorf("unexpected model creation: %s", call)
			}
		}
	}

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
	if state.ModelHashes == nil {
		t.Error("expected model_hashes to be populated")
	}
	if _, ok := state.ModelHashes["Modelfile.filemaid-gemma4-12b"]; !ok {
		t.Error("expected hash for selected model")
	}
}
func TestInstallOverwritesExistingModelfiles(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, runner, home := newTestInstaller(t, exe)
	runner.lookPath["ollama"] = "/usr/local/bin/ollama"
	runner.outputs["ollama list"] = "filemaid-gemma4-12b\n"

	configDir := filepath.Join(home, ".config", "filemaid")
	mkdir(t, configDir)
	modelfilesDir := filepath.Join(configDir, "modelfiles")
	mkdir(t, modelfilesDir)
	stale := filepath.Join(modelfilesDir, "Modelfile.filemaid-gemma4-12b")
	writeFile(t, stale, []byte("stale content"), 0o644)

	if err := inst.Install(InstallOptions{ModelName: "filemaid-gemma4-12b"}); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	data, err := os.ReadFile(stale)
	if err != nil {
		t.Fatalf("read modelfile: %v", err)
	}
	if string(data) == "stale content" {
		t.Error("existing modelfile was not overwritten")
	}
}

func TestInstallSkipsOllamaCreateWhenHashUnchanged(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, runner, home := newTestInstaller(t, exe)
	runner.lookPath["ollama"] = "/usr/local/bin/ollama"
	runner.outputs["ollama list"] = "filemaid-gemma4-12b\n"

	if err := inst.Install(InstallOptions{ModelName: "filemaid-gemma4-12b"}); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	createCount := 0
	for _, c := range runner.calls {
		if c == "ollama create filemaid-gemma4-12b -f "+filepath.Join(home, ".config", "filemaid", "modelfiles", "Modelfile.filemaid-gemma4-12b") {
			createCount++
		}
	}
	if createCount != 1 {
		t.Fatalf("expected 1 ollama create on first install, got %d", createCount)
	}

	// Simulate an upgrade: re-run setup with the same embedded modelfiles.
	inst2, runner2, _ := newTestInstaller(t, exe)
	runner2.lookPath["ollama"] = "/usr/local/bin/ollama"
	runner2.outputs["ollama list"] = "filemaid-gemma4-12b\n"
	inst2.Home = inst.Home
	inst2.DataDir = inst.DataDir
	if err := inst2.Install(InstallOptions{ModelName: "filemaid-gemma4-12b"}); err != nil {
		t.Fatalf("second install failed: %v", err)
	}

	for _, c := range runner2.calls {
		if strings.Contains(c, "ollama create") {
			t.Errorf("expected no ollama create on unchanged hash, got %s", c)
		}
	}
}

func TestInstallRerunsOllamaCreateWhenHashChanged(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, runner, home := newTestInstaller(t, exe)
	runner.lookPath["ollama"] = "/usr/local/bin/ollama"
	runner.outputs["ollama list"] = "filemaid-gemma4-12b\n"

	if err := inst.Install(InstallOptions{ModelName: "filemaid-gemma4-12b"}); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	// Simulate a new binary with a different embedded modelfile hash by
	// mutating the stored hash. The disk modelfile is still overwritten from
	// the (unchanged) embedded asset, so the mismatch triggers recreation.
	statePath := filepath.Join(home, ".local", "share", "filemaid", "setup.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read setup.json: %v", err)
	}
	var state SetupState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("unmarshal setup.json: %v", err)
	}
	state.ModelHashes["Modelfile.filemaid-gemma4-12b"] = "deadbeef"
	updated, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal setup.json: %v", err)
	}
	writeFile(t, statePath, updated, 0o644)

	inst2, runner2, _ := newTestInstaller(t, exe)
	runner2.lookPath["ollama"] = "/usr/local/bin/ollama"
	runner2.outputs["ollama list"] = "filemaid-gemma4-12b\n"
	inst2.Home = inst.Home
	inst2.DataDir = inst.DataDir
	if err := inst2.Install(InstallOptions{ModelName: "filemaid-gemma4-12b"}); err != nil {
		t.Fatalf("second install failed: %v", err)
	}

	assertCall(t, runner2.calls, "ollama create filemaid-gemma4-12b -f "+filepath.Join(home, ".config", "filemaid", "modelfiles", "Modelfile.filemaid-gemma4-12b"))
}
func TestInstallCreatesModelWhenMissingFromOllama(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, runner, home := newTestInstaller(t, exe)
	runner.lookPath["ollama"] = "/usr/local/bin/ollama"
	runner.outputs["ollama list"] = "filemaid-gemma4-12b\n"

	if err := inst.Install(InstallOptions{ModelName: "filemaid-gemma4-12b"}); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	// Simulate a machine where setup.json was copied but the Ollama model was not created.
	inst2, runner2, _ := newTestInstaller(t, exe)
	inst2.Home = inst.Home
	inst2.DataDir = inst.DataDir
	runner2.lookPath["ollama"] = "/usr/local/bin/ollama"
	runner2.outputsSeq["ollama list"] = []string{"\n", "\n", "filemaid-gemma4-12b\n"}

	if err := inst2.Install(InstallOptions{ModelName: "filemaid-gemma4-12b"}); err != nil {
		t.Fatalf("second install failed: %v", err)
	}

	assertCall(t, runner2.calls, "ollama create filemaid-gemma4-12b -f "+filepath.Join(home, ".config", "filemaid", "modelfiles", "Modelfile.filemaid-gemma4-12b"))
}

func TestInstallFailsWhenOllamaCreateFails(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, runner, home := newTestInstaller(t, exe)
	runner.lookPath["ollama"] = "/usr/local/bin/ollama"
	runner.outputs["ollama list"] = "\n"
	runner.runErrFor["ollama create filemaid-gemma4-12b -f "+filepath.Join(home, ".config", "filemaid", "modelfiles", "Modelfile.filemaid-gemma4-12b")] = fmt.Errorf("pull failed")

	if err := inst.Install(InstallOptions{ModelName: "filemaid-gemma4-12b"}); err == nil {
		t.Fatal("expected error when ollama create fails")
	}

	assertCall(t, runner.calls, "ollama create filemaid-gemma4-12b -f "+filepath.Join(home, ".config", "filemaid", "modelfiles", "Modelfile.filemaid-gemma4-12b"))
}

func TestInstallFailsWhenModelMissingAndDiskSpaceInsufficient(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, runner, home := newTestInstaller(t, exe)
	runner.lookPath["ollama"] = "/usr/local/bin/ollama"
	runner.outputsSeq["ollama list"] = []string{"\n", "\n"}
	inst.FreeSpace = func(string) (uint64, error) { return 1 * 1024 * 1024 * 1024, nil }

	if err := inst.Install(InstallOptions{ModelName: "filemaid-gemma4-12b"}); err == nil {
		t.Fatal("expected error when disk space is insufficient")
	}

	for _, call := range runner.calls {
		if strings.Contains(call, "ollama create") {
			t.Errorf("expected no ollama create when disk space is insufficient, got %s", call)
		}
	}

	statePath := filepath.Join(home, ".local", "share", "filemaid", "setup.json")
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Error("setup state should not be written when disk check fails")
	}
}

func TestInstallSkipsDiskCheckWhenModelExists(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, runner, _ := newTestInstaller(t, exe)
	runner.lookPath["ollama"] = "/usr/local/bin/ollama"
	runner.outputs["ollama list"] = "filemaid-gemma4-12b\n"
	inst.FreeSpace = func(string) (uint64, error) { return 1 * 1024 * 1024 * 1024, nil }

	if err := inst.Install(InstallOptions{ModelName: "filemaid-gemma4-12b"}); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	assertCall(t, runner.calls, "ollama create filemaid-gemma4-12b -f "+filepath.Join(inst.Home, ".config", "filemaid", "modelfiles", "Modelfile.filemaid-gemma4-12b"))
}

func TestInstallNoScan(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, runner, home := newTestInstaller(t, exe)
	runner.lookPath["ollama"] = "/usr/local/bin/ollama"
	runner.outputs["ollama list"] = "filemaid-gemma4-12b\n"

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
	inst.QuietRunner = runner
	runner.lookPath["ollama"] = "/usr/local/bin/ollama"
	runner.outputs["ollama list"] = "filemaid-gemma4-12b\n"

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
func TestInstallInteractiveDefaults(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, runner, home := newTestInstaller(t, exe)
	runner.lookPath["ollama"] = "/usr/local/bin/ollama"
	runner.outputs["ollama list"] = "filemaid-gemma4-12b\n"
	inst.Reader = bufio.NewReader(strings.NewReader("\n"))

	if err := inst.Install(InstallOptions{ModelName: "filemaid-gemma4-12b", Interactive: true}); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	configData, err := os.ReadFile(filepath.Join(home, ".config", "filemaid", "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(configData), `"model": "filemaid-gemma4-12b"`) {
		t.Errorf("config missing model: %s", configData)
	}
	if !strings.Contains(string(configData), `"~/Desktop"`) {
		t.Errorf("config missing default watch dir: %s", configData)
	}
}

func TestInstallInteractiveCustom(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, runner, home := newTestInstaller(t, exe)
	runner.lookPath["ollama"] = "/usr/local/bin/ollama"
	runner.outputs["ollama list"] = "filemaid-gemma4-12b\n"

	input := strings.Join([]string{
		"n",                     // do not use defaults
		"~/Desktop,~/Downloads", // watch dirs
		"~/Archive",             // archive base
		"n",                     // no finder tags
		"n",                     // no dev cleanup
		"7",                     // review max age days
		"~/Downloads/*.dmg",     // safe delete patterns
		"",
	}, "\n")
	inst.Reader = bufio.NewReader(strings.NewReader(input))

	if err := inst.Install(InstallOptions{ModelName: "filemaid-gemma4-12b", Interactive: true}); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	configData, err := os.ReadFile(filepath.Join(home, ".config", "filemaid", "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	cfg := string(configData)
	for _, want := range []string{
		`"model": "filemaid-gemma4-12b"`,
		`"tags": false`,
		`"~/Archive/Screenshots"`,
		`"~/Downloads/*.dmg"`,
		`"max_age_days": 7`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config missing %q: %s", want, cfg)
		}
	}
}

func TestInstallCustomDirs(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, runner, home := newTestInstaller(t, exe)
	runner.lookPath["ollama"] = "/usr/local/bin/ollama"
	runner.outputs["ollama list"] = "filemaid-gemma4-12b\n"

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
}

func TestInstallOllamaNotRunning(t *testing.T) {
	dir := t.TempDir()
	exe := prepareExecutable(t, dir)
	inst, _, _ := newTestInstaller(t, exe)
	// LookPath finds it, but Run("ollama", "list") will fail because it is a fake.
	fr := inst.QuietRunner.(*fakeRunner)
	fr.lookPath["ollama"] = "/usr/local/bin/ollama"
	fr.outputs["ollama list"] = "filemaid-gemma4-12b\n"
	fr.runErr = fmt.Errorf("ollama not running")

	if err := inst.Install(InstallOptions{Interactive: true}); err == nil {
		t.Fatal("expected error when ollama is not running")
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
	runner.outputs["ollama list"] = "filemaid-gemma4-12b\n"
	runner.runErr = fmt.Errorf("launchctl failed")

	if err := inst.Install(InstallOptions{ModelName: "filemaid-gemma4-12b"}); err == nil {
		t.Fatal("expected error when bootstrap fails")
	}
}
func TestPromptYesNo(t *testing.T) {
	tests := []struct {
		name string
		in   string
		def  bool
		want bool
	}{
		{"default true empty", "\n", true, true},
		{"default false empty", "\n", false, false},
		{"yes lower", "y\n", false, true},
		{"yes full", "yes\n", false, true},
		{"no upper", "N\n", true, false},
		{"no full", "no\n", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := promptYesNo(bufio.NewReader(strings.NewReader(tt.in)), "test", tt.def)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPromptYesNoInvalid(t *testing.T) {
	_, err := promptYesNo(bufio.NewReader(strings.NewReader("maybe\n")), "test", true)
	if err == nil {
		t.Error("expected error for invalid input")
	}
}

func TestPromptInt(t *testing.T) {
	got, err := promptInt(bufio.NewReader(strings.NewReader("42\n")), "test", 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}

func TestPromptIntDefault(t *testing.T) {
	got, err := promptInt(bufio.NewReader(strings.NewReader("\n")), "test", 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 7 {
		t.Errorf("got %d, want 7", got)
	}
}

func TestPromptIntInvalid(t *testing.T) {
	_, err := promptInt(bufio.NewReader(strings.NewReader("abc\n")), "test", 7)
	if err == nil {
		t.Error("expected error for invalid input")
	}
}

func TestPromptList(t *testing.T) {
	got, err := promptList(bufio.NewReader(strings.NewReader("a, b, c\n")), "test", []string{"x", "y"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"a", "b", "c"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestPromptListDefault(t *testing.T) {
	got, err := promptList(bufio.NewReader(strings.NewReader("\n")), "test", []string{"x", "y"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"x", "y"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBuildCategories(t *testing.T) {
	got := buildCategories("~/Archive")
	if got["Documents"] != "~/Archive/Documents" {
		t.Errorf("unexpected Documents path: %s", got["Documents"])
	}
	if got["Unknown"] != "~/.filemaid/review" {
		t.Errorf("unexpected Unknown path: %s", got["Unknown"])
	}
}

func TestDedupeStrings(t *testing.T) {
	got := dedupeStrings([]string{"a", "b", "a", "c", "b"})
	want := []string{"a", "b", "c"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
