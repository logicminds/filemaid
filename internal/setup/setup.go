// Package setup installs filemaid binaries, configuration, LaunchAgent plists,
// and Ollama models.
package setup

import (
	"bufio"
	"bytes"
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
	Choices         []string
}

// checkRequirements inspects the host and returns a systemInfo summary.
func checkRequirements(runner Runner) (*systemInfo, error) {
	info := &systemInfo{
		Choices: []string{
			"filemaid-gemma4-26b",
			"filemaid-gemma4-12b",
			"filemaid-metadata",
		},
	}

	if _, err := runner.LookPath("ollama"); err == nil {
		info.OllamaInstalled = true
		q := quietRunner{}
		if err := q.Run("ollama", "list"); err == nil {
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

// selectModel picks the model to install.
// If opts.ModelName is set, it is validated and used.
// If opts.Interactive is false and no model is set, the RAM-based
// recommendation is used without prompting.
// Otherwise the user is prompted with the RAM-based recommendation.
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

	fmt.Fprintf(os.Stderr, "\nDetected %d GB of memory.\n", info.TotalMemoryGB)
	fmt.Fprintf(os.Stderr, "Recommended model: %s\n", info.Recommended)
	fmt.Fprintln(os.Stderr, "Available models:")
	for i, c := range info.Choices {
		marker := " "
		if c == info.Recommended {
			marker = "*"
		}
		fmt.Fprintf(os.Stderr, "  %s %d) %s\n", marker, i+1, c)
	}
	fmt.Fprintf(os.Stderr, "Press Enter to use %s, or type 1-%d to choose another: ", info.Recommended, len(info.Choices))

	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read choice: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return info.Recommended, nil
	}

	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(info.Choices) {
		return "", fmt.Errorf("invalid choice %q; expected 1-%d", line, len(info.Choices))
	}
	return info.Choices[n-1], nil
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
	BinaryPath   string    `json:"binary_path"`
	ConfigDir    string    `json:"config_dir"`
	DataDir      string    `json:"data_dir"`
	LaunchdDir   string    `json:"launchd_dir"`
	ScanAgent    string    `json:"scan_agent"`
	CleanupAgent string    `json:"cleanup_agent"`
	InstalledAt  time.Time `json:"installed_at"`
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

// quietRunner captures subprocess output and discards it. Use it for
// idempotent teardown commands that are expected to fail on first install.
type quietRunner struct{}

func (quietRunner) Run(name string, arg ...string) error {
	cmd := exec.Command(name, arg...)
	return cmd.Run()
}

func (quietRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

// Installer performs a filemaid installation. All dependencies are fields so
// tests can inject fakes.
type Installer struct {
	FS             FS
	Runner         Runner
	Home           string
	UID            int
	Now            time.Time
	ExecutablePath string
	// Reader supplies interactive input. Defaults to os.Stdin.
	Reader *bufio.Reader
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
		Home:           home,
		UID:            os.Getuid(),
		Now:            time.Now(),
		ExecutablePath: exe,
		Reader:         bufio.NewReader(os.Stdin),
	}, nil
}

// Install runs the installation with the configured dependencies.
func (i *Installer) Install(opts InstallOptions) error {
	info, err := checkRequirements(i.Runner)
	if err != nil {
		return fmt.Errorf("check requirements: %w", err)
	}

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

	fmt.Fprintf(os.Stderr, "\nUsing model: %s\n\n", modelName)

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
	overrides := map[string]any{"model": modelName}
	if opts.Interactive {
		overrides, err = interviewConfig(reader, overrides)
		if err != nil {
			return fmt.Errorf("config interview: %w", err)
		}
	}
	if err := i.copyDefaultConfig(dstConfig, overrides); err != nil {
		return fmt.Errorf("copy config: %w", err)
	}

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

	if err := i.createOllamaModels(configDir, modelName); err != nil {
		slog.Debug("create ollama models", "error", err)
	}

	state := SetupState{
		BinaryPath:   binaryPath,
		ConfigDir:    configDir,
		DataDir:      dataDir,
		LaunchdDir:   launchdDir,
		ScanAgent:    scanLabel,
		CleanupAgent: cleanupLabel,
		InstalledAt:  i.Now,
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
		if i.FS.Exists(dst) {
			continue
		}
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

// createOllamaModels creates only the selected Ollama model. Modelfiles for
// all variants are still copied to disk so users can switch models by editing
// config.json and running `ollama create` manually.
func (i *Installer) createOllamaModels(configDir, selectedModel string) error {
	if _, err := i.Runner.LookPath("ollama"); err != nil {
		return err
	}
	path := filepath.Join(configDir, "modelfiles", "Modelfile."+selectedModel)
	if err := i.Runner.Run("ollama", "create", selectedModel, "-f", path); err != nil {
		return fmt.Errorf("create model %s: %w", selectedModel, err)
	}
	return nil
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
