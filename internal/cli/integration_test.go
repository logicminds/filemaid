package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/logicminds/filemaid/internal/actions"
	"github.com/logicminds/filemaid/internal/cleaners"
	"github.com/logicminds/filemaid/internal/config"
	"github.com/logicminds/filemaid/internal/llm"
	"github.com/logicminds/filemaid/internal/state"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
		_ = w.Close()
		_ = r.Close()
	}()
	fn()
	_ = w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func resetGlobals(t *testing.T) {
	t.Helper()
	oldClassifier := classifier
	oldApply := applyDecision
	oldFS := processFS
	oldScanGetFiles := scanGetFiles
	oldRegistry := cleanerRegistry
	oldNow := nowFunc

	t.Cleanup(func() {
		classifier = oldClassifier
		applyDecision = oldApply
		processFS = oldFS
		scanGetFiles = oldScanGetFiles
		cleanerRegistry = oldRegistry
		nowFunc = oldNow
	})
}

func TestSmokeProcess(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	applyDecision = actions.Apply
	classifier = &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Tags:     []string{"txt"},
		Action:   "move",
		Reason:   "text file",
	}}

	src := filepath.Join(tmp, "Desktop", "note.txt")
	if err := os.WriteFile(src, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := processPaths([]string{src}); err != nil {
		t.Fatalf("processPaths failed: %v", err)
	}

	records := db.(*state.FakeRepo).Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(records))
	}
	if records[0].Action != "move" {
		t.Errorf("action = %q, want move", records[0].Action)
	}
	if _, err := os.Stat(filepath.Join(tmp, "Documents", "note.txt")); err != nil {
		t.Fatalf("file was not moved to Documents: %v", err)
	}
}

func TestSmokeProcessCommand(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	applyDecision = actions.Apply
	classifier = &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Tags:     []string{},
		Action:   "move",
		Reason:   "text",
	}}

	src := filepath.Join(tmp, "Desktop", "note.txt")
	if err := os.WriteFile(src, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := processCmd.RunE(processCmd, []string{src}); err != nil {
		t.Fatalf("process command failed: %v", err)
	}

	if len(db.(*state.FakeRepo).Records()) != 1 {
		t.Fatalf("expected 1 history record")
	}
}

func TestSmokeScanCommand(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	applyDecision = actions.Apply
	classifier = &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Tags:     []string{},
		Action:   "move",
		Reason:   "text",
	}}

	src := filepath.Join(tmp, "Desktop", "note.txt")
	if err := os.WriteFile(src, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	scanGetFiles = func(dir string) ([]string, error) {
		return []string{src}, nil
	}
	scanDir = filepath.Join(tmp, "Desktop")

	if err := scanCmd.RunE(scanCmd, nil); err != nil {
		t.Fatalf("scan command failed: %v", err)
	}

	if len(db.(*state.FakeRepo).Records()) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(db.(*state.FakeRepo).Records()))
	}
}

func TestSmokeDefaultScanGetFiles(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "b.txt"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "sub"), 0755); err != nil {
		t.Fatal(err)
	}

	files, err := defaultScanGetFiles(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
}

func TestSmokeScanRespectsMinAge(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.MinAgeHours = 1
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	classifier = &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Action:   "move",
		Reason:   "text",
	}}

	src := filepath.Join(tmp, "Desktop", "recent.txt")
	if err := os.WriteFile(src, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	scanGetFiles = func(dir string) ([]string, error) {
		return []string{src}, nil
	}

	if err := runScanDir(filepath.Join(tmp, "Desktop")); err != nil {
		t.Fatal(err)
	}
	if len(db.(*state.FakeRepo).Records()) != 0 {
		t.Fatalf("expected recent file to be skipped")
	}
}

func TestSmokeCleanupCommand(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.AllowedCleaners = []string{"pip"}
	cfg.DevCleanup = map[string]config.CleanerConfig{
		"pip": {Enabled: true, Mode: "safe"},
	}

	cleanerRegistry = func() []cleaners.Cleaner {
		return []cleaners.Cleaner{
			{
				Name:   "pip",
				CanRun: func() bool { return true },
				Run: func(dryRun bool, cfg *config.Config) cleaners.CleanupResult {
					return cleaners.CleanupResult{Name: "pip", Status: "ok", Detail: "pip: cleaned"}
				},
			},
		}
	}

	cleanupDryRun = false
	cleanupFormat = "table"
	out := captureStdout(t, func() {
		if err := cleanupCmd.RunE(cleanupCmd, nil); err != nil {
			t.Fatalf("cleanup command failed: %v", err)
		}
	})

	if !strings.Contains(out, "pip") {
		t.Errorf("expected pip in output, got %q", out)
	}
}

func TestSmokeRunCleanupStatuses(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.AllowedCleaners = []string{"docker", "pip", "npm"}
	cfg.DevCleanup = map[string]config.CleanerConfig{
		"npm": {Enabled: false, Mode: "safe"},
	}

	cleanerRegistry = func() []cleaners.Cleaner {
		return []cleaners.Cleaner{
			{Name: "docker", CanRun: func() bool { return false }, Run: nil},
			{
				Name:   "pip",
				CanRun: func() bool { return true },
				Run: func(dryRun bool, cfg *config.Config) cleaners.CleanupResult {
					return cleaners.CleanupResult{Name: "pip", Status: "ok", Detail: "ok"}
				},
			},
			{Name: "npm", CanRun: func() bool { return true }, Run: nil},
		}
	}

	results := runCleanup(true)
	want := map[string]string{
		"docker": "not_installed",
		"pip":    "ok",
		"npm":    "disabled",
	}
	got := make(map[string]string)
	for _, r := range results {
		got[r.Name] = r.Status
	}
	for name, wantStatus := range want {
		if got[name] != wantStatus {
			t.Errorf("%s status = %q, want %q", name, got[name], wantStatus)
		}
	}
}

func TestSmokeFormatCleanupResultsFallback(t *testing.T) {
	out, err := formatCleanupResults(nil, "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Cleaner") {
		t.Errorf("expected table fallback for unknown format, got %q", out)
	}
}

func TestSmokeConfigCommand(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.Model = "dummy"
	cfg.OllamaURL = "http://localhost:11434"

	out := captureStdout(t, func() {
		if err := configCmd.RunE(configCmd, nil); err != nil {
			t.Fatalf("config command failed: %v", err)
		}
	})

	if !strings.Contains(out, `"model": "dummy"`) {
		t.Errorf("expected dummy model in output, got %q", out)
	}
	if !strings.Contains(out, `"ollama_url": "http://localhost:11434"`) {
		t.Errorf("expected ollama_url in output, got %q", out)
	}
}

func TestSmokeLogsCommand(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "filemaid.log")
	if err := os.WriteFile(logPath, []byte("line1\nline2\nline3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg = &config.Config{LogPath: logPath}
	logsTail = 2

	out := captureStdout(t, func() {
		if err := logsCmd.RunE(logsCmd, nil); err != nil {
			t.Fatalf("logs command failed: %v", err)
		}
	})

	if !strings.Contains(out, "line2") || !strings.Contains(out, "line3") {
		t.Errorf("expected last two lines, got %q", out)
	}
	if strings.Contains(out, "line1") {
		t.Errorf("did not expect line1, got %q", out)
	}
}

func TestSmokeReviewCommand(t *testing.T) {
	tmp := t.TempDir()
	reviewDir := filepath.Join(tmp, "review")
	sub := filepath.Join(reviewDir, "2026-01-01")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg = &config.Config{ReviewDir: reviewDir}
	reviewOpen = false

	out := captureStdout(t, func() {
		if err := reviewCmd.RunE(reviewCmd, nil); err != nil {
			t.Fatalf("review command failed: %v", err)
		}
	})

	if !strings.Contains(out, "file.txt") {
		t.Errorf("expected file.txt in output, got %q", out)
	}
}

func TestSmokeEndToEnd(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	applyDecision = actions.Apply
	classifier = &fakeClassifier{decision: llm.Decision{
		Category: "Images",
		Tags:     []string{"image"},
		Action:   "move",
		Reason:   "image",
	}}

	src := filepath.Join(tmp, "Desktop", "pic.png")
	if err := os.WriteFile(src, []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := processPaths([]string{src}); err != nil {
		t.Fatalf("process failed: %v", err)
	}

	// Scan the same directory; nothing should be re-processed because the file
	// is already gone.
	nowFunc = func() time.Time { return time.Now().Add(2 * time.Hour) }
	scanGetFiles = func(dir string) ([]string, error) {
		return []string{}, nil
	}
	if err := runScanDir(filepath.Join(tmp, "Desktop")); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	records := db.(*state.FakeRepo).Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Category != "Images" {
		t.Errorf("category = %q, want Images", records[0].Category)
	}
}
