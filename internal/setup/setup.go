// Package setup installs filemaid binaries, configuration, LaunchAgent plists,
// and Ollama models.
package setup

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/logicminds/filemaid/internal/setup/assets"
	"golang.org/x/sys/unix"
)

// InstallOptions controls the setup command.
type InstallOptions struct {
	NoScan    bool
	BinDir    string
	ConfigDir string
	DataDir   string
	// ModelName, if non-empty, selects the model to write into config.json.
	// If empty, setup detects RAM and prompts the user for confirmation.
	ModelName string
	// Interactive enables the configuration interview. When false, the
	// embedded default config is used with only ModelName substituted.
	Interactive bool
}

// systemInfo collects host facts needed for installation and prompts.
type systemInfo struct {
	OllamaInstalled bool
	OllamaRunning   bool
	TotalMemoryGB   int
	Recommended     string
	VisionChoices   []string
	Choices         []string
}

// checkRequirements inspects the host and returns a systemInfo summary.
func checkRequirements(runner Runner) (*systemInfo, error) {
	info := &systemInfo{
		VisionChoices: []string{
			"filemaid-gemma4-26b",
			"filemaid-gemma4-12b",
		},
		Choices: []string{
			"filemaid-gemma4-26b",
			"filemaid-gemma4-12b",
			"filemaid-metadata",
		},
	}

	if _, err := runner.LookPath("ollama"); err == nil {
		info.OllamaInstalled = true
		if err := runner.Run("ollama", "list"); err == nil {
			info.OllamaRunning = true
		}
	}

	memGB, err := totalMemoryGB()
	if err == nil {
		info.TotalMemoryGB = memGB
		switch {
		case memGB >= 24:
			info.Recommended = "filemaid-gemma4-26b"
		case memGB >= 16:
			info.Recommended = "filemaid-gemma4-12b"
		default:
			info.Recommended = "filemaid-metadata"
		}
	} else {
		info.Recommended = "filemaid-metadata"
	}

	return info, nil
}

func totalMemoryGB() (int, error) {
	if runtime.GOOS != "darwin" {
		return 0, fmt.Errorf("not macOS")
	}
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0, err
	}
	b, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, err
	}
	return int(b / (1024 * 1024 * 1024)), nil
}

// modelSpaceRequirements is the approximate disk space needed to download and
// create a model from its Modelfile. The check only runs when the model is not
// already present in Ollama.
var modelSpaceRequirements = map[string]uint64{
	"filemaid-gemma4-26b": 16 * 1024 * 1024 * 1024,
	"filemaid-gemma4-12b": 8 * 1024 * 1024 * 1024,
	"filemaid-metadata":   5 * 1024 * 1024 * 1024,
}

// modelBaseNames maps filemaid wrapper model names to the underlying Ollama
// base model names shown to users during setup.
var modelBaseNames = map[string]string{
	"filemaid-gemma4-26b": "gemma4:26b-a4b-it-qat",
	"filemaid-gemma4-12b": "gemma4:12b-it-qat",
	"filemaid-metadata":   "qwen2.5:7b",
}

// baseModelName returns the user-visible base model name for a filemaid model.
// If the model is unknown, the original name is returned.
func baseModelName(model string) string {
	if base, ok := modelBaseNames[model]; ok {
		return base
	}
	return model
}

// defaultFreeSpace returns the bytes available to the caller on the filesystem
// that contains path. It uses unix.Statfs and is macOS-specific.
func defaultFreeSpace(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// ollamaModelsDir returns the directory where Ollama stores downloaded models.
// It respects the OLLAMA_MODELS environment variable and otherwise defaults to
// ~/.ollama.
func ollamaModelsDir(home string) string {
	if d := os.Getenv("OLLAMA_MODELS"); d != "" {
		return d
	}
	return filepath.Join(home, ".ollama")
}

// formatBytes renders a byte count in GB or MB with one decimal place.
func formatBytes(b uint64) string {
	const gb = 1024 * 1024 * 1024
	const mb = 1024 * 1024
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/gb)
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/mb)
	default:
		return fmt.Sprintf("%.0f KB", float64(b)/1024)
	}
}

func selectModel(opts InstallOptions, info *systemInfo, reader *bufio.Reader) (string, error) {
	if opts.ModelName != "" {
		for _, c := range info.Choices {
			if c == opts.ModelName {
				return c, nil
			}
		}
		return "", fmt.Errorf("unknown model %q; choose one of: %s", opts.ModelName, strings.Join(info.Choices, ", "))
	}
	if !opts.Interactive {
		return info.Recommended, nil
	}

	// Metadata-only mode is selected automatically on low-memory machines.
	if info.Recommended == "filemaid-metadata" {
		fmt.Fprintf(os.Stderr, "\nDetected %d GB of memory.\n", info.TotalMemoryGB)
		fmt.Fprintf(os.Stderr, "filemaid will use filemaid-metadata for all files.\n")
		fmt.Fprintf(os.Stderr, "No vision model will be installed.\n")
		fmt.Fprintln(os.Stderr, "Press Enter to continue, or type 'vision' to install a vision model anyway: ")
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("read choice: %w", err)
		}
		if strings.TrimSpace(strings.ToLower(line)) == "vision" {
			return selectVisionModel(info, reader)
		}
		return "filemaid-metadata", nil
	}

	return selectVisionModel(info, reader)
}

// selectVisionModel prompts the user to choose a vision model for image classification.
func selectVisionModel(info *systemInfo, reader *bufio.Reader) (string, error) {
	fmt.Fprintf(os.Stderr, "\nDetected %d GB of memory.\n", info.TotalMemoryGB)
	fmt.Fprintf(os.Stderr, "filemaid uses two Ollama models:\n")
	fmt.Fprintf(os.Stderr, "  • Text/documents model: filemaid-metadata (%s) (always installed)\n", baseModelName("filemaid-metadata"))
	fmt.Fprintf(os.Stderr, "  • Image/vision model: your choice below\n")
	fmt.Fprintf(os.Stderr, "Recommended vision model: %s (%s)\n", info.Recommended, baseModelName(info.Recommended))
	fmt.Fprintln(os.Stderr, "Available vision models:")
	for i, c := range info.VisionChoices {
		marker := " "
		if c == info.Recommended {
			marker = "*"
		}
		fmt.Fprintf(os.Stderr, "  %s %d) %s (%s)\n", marker, i+1, c, baseModelName(c))
	}
	fmt.Fprintf(os.Stderr, "Press Enter to use %s (%s) for images, or type 1-%d to choose another vision model: ", info.Recommended, baseModelName(info.Recommended), len(info.VisionChoices))

	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read choice: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return info.Recommended, nil
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(info.VisionChoices) {
		return "", fmt.Errorf("invalid choice %q; expected 1-%d", line, len(info.VisionChoices))
	}
	return info.VisionChoices[n-1], nil
}

func printOllamaInstructions() {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Ollama is not installed or not on PATH.")
	fmt.Fprintln(os.Stderr, "filemaid needs Ollama to classify files locally.")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Install Ollama with Homebrew:")
	fmt.Fprintln(os.Stderr, "  brew install ollama")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Or download it from https://ollama.com/")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "After installing, start Ollama and run:")
	fmt.Fprintln(os.Stderr, "  filemaid setup")
	fmt.Fprintln(os.Stderr)
}

// SetupState records the paths chosen during installation so uninstall can
// remove only the files that were installed.
type SetupState struct {
	BinaryPath   string            `json:"binary_path"`
	ConfigDir    string            `json:"config_dir"`
	DataDir      string            `json:"data_dir"`
	LaunchdDir   string            `json:"launchd_dir"`
	ScanAgent    string            `json:"scan_agent"`
	CleanupAgent string            `json:"cleanup_agent"`
	InstalledAt  time.Time         `json:"installed_at"`
	ModelHashes  map[string]string `json:"model_hashes"`
}

// FS abstracts filesystem operations so tests can substitute a fake.
type FS interface {
	MkdirAll(path string, perm os.FileMode) error
	CopyFile(src, dst string) error
	Exists(path string) bool
	ReadDir(path string) ([]os.DirEntry, error)
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm os.FileMode) error
	Remove(path string) error
}

// Runner abstracts subprocess execution so tests can substitute a fake.
type Runner interface {
	Run(name string, arg ...string) error
	LookPath(name string) (string, error)
}

// outputRunner extends Runner with a way to capture command stdout.
type outputRunner interface {
	Runner
	RunOutput(name string, arg ...string) (string, error)
}

// IsSetup reports whether filemaid has been installed by checking for the
// setup state file in the default data directory.
func IsSetup() (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, fmt.Errorf("home dir: %w", err)
	}
	statePath := filepath.Join(home, ".local", "share", "filemaid", "setup.json")
	_, err = os.Stat(statePath)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

type osFS struct{}

func (osFS) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func (osFS) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
func (osFS) ReadDir(path string) ([]os.DirEntry, error) { return os.ReadDir(path) }
func (osFS) ReadFile(path string) ([]byte, error)       { return os.ReadFile(path) }
func (osFS) WriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}
func (osFS) Remove(path string) error { return os.Remove(path) }

func (osFS) CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// loudRunner pipes subprocess stdout/stderr to the terminal. Use it for
// long-running commands whose progress the user should see.
type loudRunner struct{}

func (loudRunner) Run(name string, arg ...string) error {
	cmd := exec.Command(name, arg...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (loudRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (loudRunner) RunOutput(name string, arg ...string) (string, error) {
	cmd := exec.Command(name, arg...)
	out, err := cmd.Output()
	return string(out), err
}

// quietRunner captures subprocess output and discards it. Use it for
// idempotent teardown commands that are expected to fail on first install.
type quietRunner struct{}

func (quietRunner) Run(name string, arg ...string) error {
	cmd := exec.Command(name, arg...)
	return cmd.Run()
}

func (quietRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (quietRunner) RunOutput(name string, arg ...string) (string, error) {
	cmd := exec.Command(name, arg...)
	out, err := cmd.Output()
	return string(out), err
}

// Installer performs a filemaid installation. All dependencies are fields so
// tests can inject fakes.
type Installer struct {
	FS             FS
	Runner         Runner
	QuietRunner    Runner
	Home           string
	UID            int
	Now            time.Time
	ExecutablePath string
	DataDir        string
	// Sleep is called between polling attempts. Defaults to time.Sleep.
	Sleep func(time.Duration)
	// Reader supplies interactive input. Defaults to os.Stdin.
	Reader *bufio.Reader
	// FreeSpace returns the bytes available on the filesystem containing path.
	// Defaults to defaultFreeSpace. Tests may override it.
	FreeSpace func(path string) (uint64, error)
}

const (
	scanLabel    = "biz.logicminds.filemaid.scan"
	cleanupLabel = "biz.logicminds.filemaid.cleanup"
)

// Install performs the filemaid installation using host defaults.
func Install(opts InstallOptions) error {
	inst, err := newDefaultInstaller()
	if err != nil {
		return err
	}
	return inst.Install(opts)
}
func newDefaultInstaller() (*Installer, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("executable: %w", err)
	}
	return &Installer{
		FS:             osFS{},
		Runner:         loudRunner{},
		QuietRunner:    quietRunner{},
		Home:           home,
		UID:            os.Getuid(),
		Now:            time.Now(),
		ExecutablePath: exe,
		DataDir:        filepath.Join(home, ".local", "share", "filemaid"),
		Sleep:          time.Sleep,
		Reader:         bufio.NewReader(os.Stdin),
		FreeSpace:      defaultFreeSpace,
	}, nil
}

// Install runs the installation with the configured dependencies.
func (i *Installer) Install(opts InstallOptions) error {
	info, err := checkRequirements(i.QuietRunner)
	if err != nil {
		return fmt.Errorf("check requirements: %w", err)
	}

	candidate := opts.ModelName
	if candidate == "" {
		candidate = info.Recommended
	} else {
		valid := false
		for _, c := range info.Choices {
			if c == candidate {
				valid = true
				break
			}
		}
		if !valid {
			candidate = info.Recommended
		}
	}

	preInstallModels := dedupeStrings([]string{candidate, "filemaid-metadata"})

	var allExist bool
	var free, required uint64
	var diskErr error
	if info.OllamaRunning {
		missing, err := i.missingModels(preInstallModels)
		if err != nil {
			return fmt.Errorf("list ollama models: %w", err)
		}
		allExist = len(missing) == 0
		if !allExist {
			free, required, diskErr = i.ollamaFreeSpace(missing)
			if diskErr != nil {
				return diskErr
			}
		}
	} else {
		for _, m := range preInstallModels {
			r := modelSpaceRequirements[m]
			if r == 0 {
				r = modelSpaceRequirements["filemaid-gemma4-26b"]
			}
			required += r
		}
	}

	ok := printRequirementsCheck(info, preInstallModels, allExist, free, required)
	if !info.OllamaInstalled {
		printOllamaInstructions()
		return fmt.Errorf("ollama not found")
	}
	if !info.OllamaRunning {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Ollama is installed but not responding.")
		fmt.Fprintln(os.Stderr, "Start it with:")
		fmt.Fprintln(os.Stderr, "  ollama serve")
		fmt.Fprintln(os.Stderr, "Then run filemaid setup again.")
		return fmt.Errorf("ollama not running")
	}
	if !ok {
		return fmt.Errorf("not enough disk space to download models")
	}

	binDir := opts.BinDir
	if binDir == "" {
		binDir = filepath.Join(i.Home, ".local", "bin")
	}
	configDir := opts.ConfigDir
	if configDir == "" {
		configDir = filepath.Join(i.Home, ".config", "filemaid")
	}
	dataDir := opts.DataDir
	if dataDir == "" {
		dataDir = filepath.Join(i.Home, ".local", "share", "filemaid")
	}
	launchdDir := filepath.Join(i.Home, "Library", "LaunchAgents")

	dirs := []string{binDir, configDir, dataDir, launchdDir, filepath.Join(configDir, "modelfiles")}
	for _, d := range dirs {
		if err := i.FS.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	reader := i.Reader
	if reader == nil {
		reader = bufio.NewReader(os.Stdin)
	}
	modelName, err := selectModel(opts, info, reader)
	if err != nil {
		return err
	}

	if modelName == "filemaid-metadata" {
		fmt.Fprintf(os.Stderr, "\nUsing filemaid-metadata (%s) for both text and images.\n\n", baseModelName(modelName))
	} else {
		fmt.Fprintf(os.Stderr, "\nUsing %s (%s) for images.\n", modelName, baseModelName(modelName))
		fmt.Fprintf(os.Stderr, "Using filemaid-metadata (%s) for text and documents.\n\n", baseModelName("filemaid-metadata"))
	}

	exe := i.ExecutablePath
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return fmt.Errorf("executable: %w", err)
		}
	}

	binaryPath := filepath.Join(binDir, "filemaid")
	if err := i.FS.CopyFile(exe, binaryPath); err != nil {
		return fmt.Errorf("copy binary: %w", err)
	}

	dstConfig := filepath.Join(configDir, "config.json")
	overrides := map[string]any{
		"model":       "filemaid-metadata",
		"image_model": modelName,
		"text_model":  "filemaid-metadata",
	}
	if modelName == "filemaid-metadata" {
		overrides["image_model"] = "filemaid-metadata"
	}
	if opts.Interactive {
		overrides, err = interviewConfig(reader, overrides)
		if err != nil {
			return fmt.Errorf("config interview: %w", err)
		}
	}
	if err := i.copyDefaultConfig(dstConfig, overrides); err != nil {
		return fmt.Errorf("copy config: %w", err)
	}

	models := dedupeStrings([]string{modelName, "filemaid-metadata"})

	dstModelfiles := filepath.Join(configDir, "modelfiles")
	if err := i.copyModelfiles(dstModelfiles); err != nil {
		return fmt.Errorf("copy modelfiles: %w", err)
	}

	scanPlistPath := filepath.Join(launchdDir, scanLabel+".plist")
	cleanupPlistPath := filepath.Join(launchdDir, cleanupLabel+".plist")

	if opts.NoScan {
		if err := i.FS.Remove(scanPlistPath); err != nil && !os.IsNotExist(err) {
			slog.Debug("remove stale scan plist", "error", err)
		}
	} else {
		scanPlist, err := RenderScanPlist(binDir, dataDir)
		if err != nil {
			return fmt.Errorf("render scan plist: %w", err)
		}
		if err := i.FS.WriteFile(scanPlistPath, []byte(scanPlist), 0o644); err != nil {
			return fmt.Errorf("write scan plist: %w", err)
		}
	}

	cleanupPlist, err := RenderCleanupPlist(binDir, dataDir)
	if err != nil {
		return fmt.Errorf("render cleanup plist: %w", err)
	}
	if err := i.FS.WriteFile(cleanupPlistPath, []byte(cleanupPlist), 0o644); err != nil {
		return fmt.Errorf("write cleanup plist: %w", err)
	}

	// Boot-out any previous agents. This is expected to fail on first install,
	// so use a quiet runner so the user does not see a benign "No such process"
	// error that looks like a failure.
	quiet := quietRunner{}
	for _, label := range []string{scanLabel, cleanupLabel} {
		target := fmt.Sprintf("gui/%d/%s", i.UID, label)
		if err := quiet.Run("launchctl", "bootout", target); err != nil {
			slog.Debug("bootout existing agent", "target", target, "error", err)
		}
	}

	if err := i.Runner.Run("launchctl", "bootstrap", fmt.Sprintf("gui/%d", i.UID), cleanupPlistPath); err != nil {
		return fmt.Errorf("bootstrap cleanup agent: %w", err)
	}
	if !opts.NoScan {
		if err := i.Runner.Run("launchctl", "bootstrap", fmt.Sprintf("gui/%d", i.UID), scanPlistPath); err != nil {
			return fmt.Errorf("bootstrap scan agent: %w", err)
		}
	}

	if err := i.createOllamaModels(configDir, models); err != nil {
		return fmt.Errorf("create ollama models: %w", err)
	}

	// Build model hash state after modelfiles have been written.
	modelHashes, err := i.hashEmbeddedModelfiles()
	if err != nil {
		return fmt.Errorf("hash modelfiles: %w", err)
	}

	state := SetupState{
		BinaryPath:   binaryPath,
		ConfigDir:    configDir,
		DataDir:      dataDir,
		LaunchdDir:   launchdDir,
		ScanAgent:    scanLabel,
		CleanupAgent: cleanupLabel,
		InstalledAt:  i.Now,
		ModelHashes:  modelHashes,
	}
	stateJSON, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal setup state: %w", err)
	}
	if err := i.FS.WriteFile(filepath.Join(dataDir, "setup.json"), stateJSON, 0o644); err != nil {
		return fmt.Errorf("write setup state: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\nfilemaid setup complete. Binary: %s\n", binaryPath)
	fmt.Fprintf(os.Stderr, "Run `filemaid process <path>` to classify files.\n")
	if opts.NoScan {
		fmt.Fprintf(os.Stderr, "Scan agent disabled; run `filemaid scan` manually.\n")
	} else {
		fmt.Fprintf(os.Stderr, "Scan and cleanup agents are loaded.\n")
	}

	return nil
}

func (i *Installer) copyModelfiles(dstDir string) error {
	if err := i.FS.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	names, err := assets.ListModelfiles()
	if err != nil {
		return err
	}
	for _, name := range names {
		dst := filepath.Join(dstDir, name)
		data, err := assets.ReadModelfile(name)
		if err != nil {
			return fmt.Errorf("read embedded modelfile %s: %w", name, err)
		}
		if err := i.FS.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (i *Installer) copyDefaultConfig(dst string, overrides map[string]any) error {
	if i.FS.Exists(dst) {
		return nil
	}
	data, err := assets.ReadConfig()
	if err != nil {
		return fmt.Errorf("read embedded config: %w", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse embedded config: %w", err)
	}
	for k, v := range overrides {
		cfg[k] = v
	}
	updated, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	updated = append(updated, '\n')
	return i.FS.WriteFile(dst, updated, 0o644)
}

// ollamaFreeSpace returns the free bytes available for Ollama models and the
// approximate bytes required to download and create the named models.
func (i *Installer) ollamaFreeSpace(models []string) (uint64, uint64, error) {
	var required uint64
	for _, m := range models {
		r, ok := modelSpaceRequirements[m]
		if !ok {
			r = modelSpaceRequirements["filemaid-gemma4-26b"]
		}
		required += r
	}

	dir := ollamaModelsDir(i.Home)
	checkPath := dir
	if _, err := os.Stat(checkPath); err != nil {
		checkPath = filepath.Dir(checkPath)
		if _, err := os.Stat(checkPath); err != nil {
			checkPath = "/"
		}
	}

	freeSpace := i.FreeSpace
	if freeSpace == nil {
		freeSpace = defaultFreeSpace
	}
	free, err := freeSpace(checkPath)
	if err != nil {
		return 0, required, fmt.Errorf("check disk space: %w", err)
	}
	return free, required, nil
}

// checkDiskSpace returns an error if there is not enough free disk space to
// download and create the named models. It should only be called when at least
// one model is missing from Ollama; existing models do not need extra space.
func (i *Installer) checkDiskSpace(models []string) error {
	free, required, err := i.ollamaFreeSpace(models)
	if err != nil {
		return err
	}
	if free < required {
		return fmt.Errorf("not enough disk space to download models: %s available, %s required", formatBytes(free), formatBytes(required))
	}
	return nil
}

// printRequirementsCheck prints a checklist of host requirements with emoji
// checkmarks or crosses. It returns true when every hard requirement passes.
func printRequirementsCheck(info *systemInfo, models []string, allExist bool, free, required uint64) bool {
	ok := true
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Requirements check:")

	mark := func(pass bool) string {
		if pass {
			return "✅"
		}
		return "❌"
	}

	primary := ""
	if len(models) > 0 {
		primary = models[0]
	}
	visionModel := primary
	textModel := "filemaid-metadata"
	if len(models) > 1 {
		textModel = models[1]
	}

	fmt.Fprintf(os.Stderr, "  %s Ollama installed\n", mark(info.OllamaInstalled))
	fmt.Fprintf(os.Stderr, "  %s Ollama running\n", mark(info.OllamaRunning))
	fmt.Fprintf(os.Stderr, "  %s Memory: %d GB\n", mark(info.TotalMemoryGB > 0), info.TotalMemoryGB)

	if visionModel == textModel {
		fmt.Fprintf(os.Stderr, "  %s Model for all files: %s (%s)", mark(true), visionModel, baseModelName(visionModel))
		if allExist {
			fmt.Fprintln(os.Stderr, " (already downloaded)")
		} else {
			fmt.Fprintln(os.Stderr)
		}
	} else {
		fmt.Fprintf(os.Stderr, "  %s Vision model for images: %s (%s)", mark(true), visionModel, baseModelName(visionModel))
		if allExist {
			fmt.Fprintln(os.Stderr, " (already downloaded)")
		} else {
			fmt.Fprintln(os.Stderr)
		}
		fmt.Fprintf(os.Stderr, "  %s Text model for documents: %s (%s) (always installed)\n", mark(true), textModel, baseModelName(textModel))
	}

	if allExist {
		fmt.Fprintf(os.Stderr, "  %s Disk space for new models: not needed (models already present)\n", mark(true))
	} else {
		hasSpace := free >= required
		if !hasSpace {
			ok = false
		}
		fmt.Fprintf(os.Stderr, "  %s Disk space for models: %s available, %s required\n", mark(hasSpace), formatBytes(free), formatBytes(required))
	}

	fmt.Fprintln(os.Stderr)
	return ok
}

// createOllamaModels creates or recreates each Ollama model in models when it
// is missing from `ollama list` or when the embedded Modelfile has changed
// since the last setup. Modelfiles for all variants are copied to disk so users
// can switch models by editing config.json and running `ollama create` manually.
func (i *Installer) createOllamaModels(configDir string, models []string) error {
	if _, err := i.Runner.LookPath("ollama"); err != nil {
		return err
	}

	existingModels, err := i.listOllamaModels()
	if err != nil {
		return fmt.Errorf("list ollama models: %w", err)
	}

	var missing []string
	for _, m := range models {
		if !modelExistsInList(existingModels, m) {
			missing = append(missing, m)
		}
	}
	if len(missing) > 0 {
		if err := i.checkDiskSpace(missing); err != nil {
			return err
		}
	}

	for _, model := range models {
		data, err := assets.ReadModelfile("Modelfile." + model)
		if err != nil {
			return fmt.Errorf("read embedded modelfile %s: %w", model, err)
		}
		hash := hashBytes(data)
		exists := modelExistsInList(existingModels, model)

		if previous, ok := i.readStoredModelHash(model); ok && previous == hash && exists {
			slog.Debug("ollama model up to date", "model", model, "hash", hash)
			continue
		}

		path := filepath.Join(configDir, "modelfiles", "Modelfile."+model)

		fmt.Fprintln(os.Stderr)
		if exists {
			fmt.Fprintf(os.Stderr, "Modelfile for %q changed; recreating the Ollama model.\n", model)
		} else {
			fmt.Fprintf(os.Stderr, "Creating Ollama model %q. This may download several gigabytes and take a few minutes.\n", model)
		}
		fmt.Fprintln(os.Stderr, "Do not interrupt the download.")
		fmt.Fprintln(os.Stderr)

		// Create the model with the latest tag so config references without a tag
		// resolve correctly. Ollama overwrites an existing latest tag on changes.
		taggedModel := model + ":latest"

		if err := i.Runner.Run("ollama", "create", taggedModel, "-f", path); err != nil {
			return fmt.Errorf("create model %s: %w", taggedModel, err)
		}

		if err := i.waitForModel(taggedModel); err != nil {
			return fmt.Errorf("model %s did not appear in ollama list after create: %w", taggedModel, err)
		}

		fmt.Fprintf(os.Stderr, "Model %q is ready.\n", taggedModel)
	}

	return nil
}

// waitForModel polls `ollama list` until the named model appears, giving Ollama
// time to finish downloading and registering the model.
func (i *Installer) waitForModel(model string) error {
	const maxAttempts = 60
	const delay = 5 * time.Second
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		output, err := i.runOutput("ollama", "list")
		if err == nil && modelExistsInOutput(output, model) {
			return nil
		}
		if i.Sleep != nil {
			i.Sleep(delay)
		} else {
			time.Sleep(delay)
		}
	}
	return fmt.Errorf("timed out waiting for %q", model)
}

func (i *Installer) modelExists(model string) (bool, error) {
	output, err := i.runOutput("ollama", "list")
	if err != nil {
		return false, err
	}
	return modelExistsInOutput(output, model), nil
}

// missingModels returns the subset of models that are not present in Ollama.
func (i *Installer) missingModels(models []string) ([]string, error) {
	existing, err := i.listOllamaModels()
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, m := range models {
		if !modelExistsInList(existing, m) {
			missing = append(missing, m)
		}
	}
	return missing, nil
}

// modelExistsInList reports whether want appears in models, with or without a
// tag suffix.
func modelExistsInList(models []string, want string) bool {
	for _, name := range models {
		if name == want || strings.HasPrefix(name, want+":") {
			return true
		}
	}
	return false
}

// listOllamaModels returns the parsed model names from `ollama list` output.
func (i *Installer) listOllamaModels() ([]string, error) {
	output, err := i.runOutput("ollama", "list")
	if err != nil {
		return nil, err
	}
	return parseOllamaModelList(output), nil
}

// parseOllamaModelList extracts model names from `ollama list` output.
func parseOllamaModelList(output string) []string {
	var names []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Skip the header line.
		if strings.HasPrefix(line, "NAME") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		names = append(names, fields[0])
	}
	return names
}

// runOutput runs a command quietly and returns its stdout as a string.
func (i *Installer) runOutput(name string, arg ...string) (string, error) {
	if r, ok := i.QuietRunner.(outputRunner); ok {
		return r.RunOutput(name, arg...)
	}
	cmd := exec.Command(name, arg...)
	out, err := cmd.Output()
	return string(out), err
}

func modelExistsInOutput(output, model string) bool {
	for _, line := range strings.Split(output, "\n") {
		if lineHasModel(line, model) {
			return true
		}
	}
	return false
}

func lineHasModel(line, model string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	name := fields[0]
	return name == model || strings.HasPrefix(name, model+":")
}

func (i *Installer) hashEmbeddedModelfiles() (map[string]string, error) {
	names, err := assets.ListModelfiles()
	if err != nil {
		return nil, err
	}
	hashes := make(map[string]string, len(names))
	for _, name := range names {
		data, err := assets.ReadModelfile(name)
		if err != nil {
			return nil, fmt.Errorf("read embedded modelfile %s: %w", name, err)
		}
		hashes[name] = hashBytes(data)
	}
	return hashes, nil
}

func (i *Installer) readStoredModelHash(modelName string) (string, bool) {
	dataDir := i.DataDir
	if dataDir == "" {
		dataDir = filepath.Join(i.Home, ".local", "share", "filemaid")
	}
	data, err := i.FS.ReadFile(filepath.Join(dataDir, "setup.json"))
	if err != nil {
		return "", false
	}
	var state SetupState
	if err := json.Unmarshal(data, &state); err != nil {
		return "", false
	}
	h, ok := state.ModelHashes["Modelfile."+modelName]
	return h, ok
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

var funcMap = template.FuncMap{
	"xmlEscape": xmlEscape,
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

const scanPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + scanLabel + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string>
    <string>-c</string>
    <string>exec >"{{xmlEscape .DataDir}}/scan.log" 2>"{{xmlEscape .DataDir}}/scan.err" "{{xmlEscape .BinDir}}/filemaid" scan</string>
  </array>
  <key>StartInterval</key><integer>900</integer>
  <key>RunAtLoad</key><true/>
</dict>
</plist>
`

const cleanupPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + cleanupLabel + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string>
    <string>-c</string>
    <string>exec >"{{xmlEscape .DataDir}}/cleanup.log" 2>"{{xmlEscape .DataDir}}/cleanup.err" "{{xmlEscape .BinDir}}/filemaid" cleanup</string>
  </array>
  <key>StartCalendarInterval</key>
  <array>
    <dict><key>Hour</key><integer>6</integer><key>Minute</key><integer>0</integer></dict>
    <dict><key>Hour</key><integer>12</integer><key>Minute</key><integer>0</integer></dict>
    <dict><key>Hour</key><integer>18</integer><key>Minute</key><integer>0</integer></dict>
    <dict><key>Hour</key><integer>23</integer><key>Minute</key><integer>0</integer></dict>
  </array>
  <key>RunAtLoad</key><true/>
</dict>
</plist>
`

type plistPaths struct {
	BinDir  string
	DataDir string
}

// RenderScanPlist returns the rendered scan LaunchAgent plist for the given paths.
func RenderScanPlist(binDir, dataDir string) (string, error) {
	var buf bytes.Buffer
	t := template.Must(template.New("scan").Funcs(funcMap).Parse(scanPlistTemplate))
	if err := t.Execute(&buf, plistPaths{BinDir: binDir, DataDir: dataDir}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// RenderCleanupPlist returns the rendered cleanup LaunchAgent plist for the given paths.
func RenderCleanupPlist(binDir, dataDir string) (string, error) {
	var buf bytes.Buffer
	t := template.Must(template.New("cleanup").Funcs(funcMap).Parse(cleanupPlistTemplate))
	if err := t.Execute(&buf, plistPaths{BinDir: binDir, DataDir: dataDir}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func interviewConfig(reader *bufio.Reader, base map[string]any) (map[string]any, error) {
	fmt.Fprintln(os.Stderr, "Let's configure filemaid. Press Enter to accept the defaults.")
	fmt.Fprintln(os.Stderr)

	useDefaults, err := promptYesNo(reader, "Use recommended defaults for everything", true)
	if err != nil {
		return nil, err
	}
	if useDefaults {
		fmt.Fprintln(os.Stderr)
		return base, nil
	}

	out := make(map[string]any, len(base))
	for k, v := range base {
		out[k] = v
	}

	watchDefault := []string{"~/Desktop", "~/Downloads"}
	watchDirs, err := promptList(reader, "Watch directories (comma-separated)", watchDefault)
	if err != nil {
		return nil, err
	}
	out["watch_dirs"] = watchDirs

	archiveBase, err := promptString(reader, "Archive base directory", "~/Documents/Archive")
	if err != nil {
		return nil, err
	}
	out["categories"] = buildCategories(archiveBase)

	allowed := append([]string{}, watchDirs...)
	allowed = append(allowed, archiveBase, "~/.filemaid/review")
	out["allowed_dirs"] = dedupeStrings(allowed)

	tags, err := promptYesNo(reader, "Apply Finder tags to organized files", true)
	if err != nil {
		return nil, err
	}
	out["tags"] = tags

	enableCleaners, err := promptYesNo(reader, "Enable development cache cleanup", true)
	if err != nil {
		return nil, err
	}
	out["dev_cleanup"] = promptDevCleanup(reader, enableCleaners)

	reviewDays, err := promptInt(reader, "Clean up review queue items older than (days, 0 to disable)", 30)
	if err != nil {
		return nil, err
	}
	out["review_cleanup"] = map[string]any{
		"enabled":      reviewDays > 0,
		"mode":         "safe",
		"max_age_days": reviewDays,
	}

	if reviewDays <= 0 {
		allowedCleaners := []string{}
		if ac, ok := out["allowed_cleaners"].([]string); ok {
			for _, c := range ac {
				if c != "review" {
					allowedCleaners = append(allowedCleaners, c)
				}
			}
			out["allowed_cleaners"] = allowedCleaners
		}
	}

	patterns, err := promptString(reader, "Safe delete patterns (comma-separated globs, e.g. ~/Downloads/*.dmg)", "")
	if err != nil {
		return nil, err
	}
	if patterns != "" {
		out["safe_delete_patterns"] = splitTrim(patterns)
	}

	fmt.Fprintln(os.Stderr)
	return out, nil
}

func buildCategories(archiveBase string) map[string]string {
	return map[string]string{
		"Screenshots": archiveBase + "/Screenshots",
		"Documents":   archiveBase + "/Documents",
		"Receipts":    archiveBase + "/Receipts",
		"Images":      archiveBase + "/Images",
		"Installers":  archiveBase + "/Installers",
		"Code":        archiveBase + "/Code",
		"Archives":    archiveBase + "/Archives",
		"Media":       archiveBase + "/Media",
		"Unknown":     "~/.filemaid/review",
	}
}

func promptDevCleanup(reader *bufio.Reader, enableAll bool) map[string]any {
	cleaners := []string{"docker", "npm", "cargo", "pip", "brew", "xcode"}
	out := make(map[string]any, len(cleaners))
	for _, name := range cleaners {
		enabled := enableAll
		if enableAll {
			var err error
			enabled, err = promptYesNo(reader, fmt.Sprintf("  Enable %s cleaner", name), true)
			if err != nil {
				enabled = true
			}
		}
		out[name] = map[string]any{"enabled": enabled, "mode": "safe"}
	}
	return out
}

func promptString(reader *bufio.Reader, question, def string) (string, error) {
	if def == "" {
		fmt.Fprintf(os.Stderr, "%s: ", question)
	} else {
		fmt.Fprintf(os.Stderr, "%s [%s]: ", question, def)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}

func promptInt(reader *bufio.Reader, question string, def int) (int, error) {
	fmt.Fprintf(os.Stderr, "%s [%d]: ", question, def)
	line, err := reader.ReadString('\n')
	if err != nil {
		return 0, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	n, err := strconv.Atoi(line)
	if err != nil {
		return 0, fmt.Errorf("expected a number, got %q", line)
	}
	return n, nil
}

func promptYesNo(reader *bufio.Reader, question string, def bool) (bool, error) {
	marker := "y/N"
	if def {
		marker = "Y/n"
	}
	fmt.Fprintf(os.Stderr, "%s [%s]: ", question, marker)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false, err
	}
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return def, nil
	}
	switch line {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("expected y/n, got %q", line)
	}
}

func promptList(reader *bufio.Reader, question string, def []string) ([]string, error) {
	s := promptStringWithDefault(question, strings.Join(def, ","))
	fmt.Fprint(os.Stderr, s)
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return splitTrim(line), nil
}

func promptStringWithDefault(question, def string) string {
	if def == "" {
		return fmt.Sprintf("%s: ", question)
	}
	return fmt.Sprintf("%s [%s]: ", question, def)
}

func splitTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func dedupeStrings(ss []string) []string {
	seen := make(map[string]struct{}, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
