package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
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
	oldApplyDir := applyDirectoryFunc
	oldFS := processFS
	oldUndoFS := undoFS
	oldScanGetCandidates := scanGetCandidates
	oldRegistry := cleanerRegistry
	oldNow := nowFunc
	oldMoveProjects := moveProjects

	t.Cleanup(func() {
		classifier = oldClassifier
		applyDecision = oldApply
		applyDirectoryFunc = oldApplyDir
		processFS = oldFS
		undoFS = oldUndoFS
		scanGetCandidates = oldScanGetCandidates
		cleanerRegistry = oldRegistry
		nowFunc = oldNow
		moveProjects = oldMoveProjects
		undoLast = false
		undoRun = ""
		undoForce = false
		undoDryRun = false
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

	if _, err := processPaths(context.Background(), inputs(src), "run-test", io.Discard, ""); err != nil {
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

	scanGetCandidates = func(ctx context.Context, dir string, depth int) ([]processInput, []processInput, error) { return []processInput{{path: src}}, nil, nil
 }
	scanDir = filepath.Join(tmp, "Desktop")

	if err := scanCmd.RunE(scanCmd, nil); err != nil {
		t.Fatalf("scan command failed: %v", err)
	}

	if len(db.(*state.FakeRepo).Records()) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(db.(*state.FakeRepo).Records()))
	}
}

func TestSmokeDefaultScanGetCandidates(t *testing.T) {
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

	files, dirs, err := defaultScanGetCandidates(context.Background(), tmp, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if len(dirs) != 1 {
		t.Fatalf("expected 1 directory, got %d", len(dirs))
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

	scanGetCandidates = func(ctx context.Context, dir string, depth int) ([]processInput, []processInput, error) { return []processInput{{path: src}}, nil, nil
 }

	if _, err := runScanDir(context.Background(), filepath.Join(tmp, "Desktop"), "run-test", io.Discard, ""); err != nil {
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

	if _, err := processPaths(context.Background(), inputs(src), "run-test", io.Discard, ""); err != nil {
		t.Fatalf("process failed: %v", err)
	}

	// Scan the same directory; nothing should be re-processed because the file
	// is already gone.
	nowFunc = func() time.Time { return time.Now().Add(2 * time.Hour) }
	scanGetCandidates = func(ctx context.Context, dir string, depth int) ([]processInput, []processInput, error) { return nil, nil, nil
 }
	if _, err := runScanDir(context.Background(), filepath.Join(tmp, "Desktop"), "run-test", io.Discard, ""); err != nil {
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

func TestSmokeSmartFoldersRegeneration(t *testing.T) {
	resetGlobals(t)

	setHubBuilder(t, &fakeHubBuilder{})

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.Tags = true
	cfg.SmartFolders = true
	cfg.SmartFoldersDir = filepath.Join(tmp, "SmartFolders")
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	applyDecision = actions.Apply
	classifier = &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Tags:     []string{"work", "receipt"},
		Action:   "move",
		Reason:   "text file",
	}}

	src := filepath.Join(tmp, "Desktop", "note.txt")
	if err := os.WriteFile(src, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := processCmd.RunE(processCmd, []string{src}); err != nil {
		t.Fatalf("process command failed: %v", err)
	}

	entries, err := os.ReadDir(cfg.SmartFoldersDir)
	if err != nil {
		t.Fatalf("read smart folder dir: %v", err)
	}

	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{"Documents.savedSearch", "Images.savedSearch", "Unknown.savedSearch", "receipt.savedSearch", "work.savedSearch"}
	if !slices.Equal(names, want) {
		t.Fatalf("saved searches = %v, want %v", names, want)
	}
}

// TestAcceptanceProcessDepth verifies that `filemaid process --depth`
// accepts a directory argument, treats it as a read-only candidate, and does not
// move or modify the directory or its contents.
func TestAcceptanceProcessDepth(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	classifier = &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Tags:     []string{},
		Action:   "move",
		Reason:   "text",
	}}

	dirPath := filepath.Join(tmp, "Desktop", "project-folder")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dirPath, "child.txt")
	if err := os.WriteFile(child, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	processDepth = 1
	t.Cleanup(func() { processDepth = 0 })

	results, err := processPaths(context.Background(), inputs(dirPath), "run-test", io.Discard, "")
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Kind != "directory" {
		t.Errorf("kind = %q, want directory", r.Kind)
	}
	if r.Action != "review" {
		t.Errorf("action = %q, want review", r.Action)
	}
	if _, err := os.Stat(dirPath); err != nil {
		t.Fatalf("directory was modified or removed: %v", err)
	}
	if _, err := os.Stat(child); err != nil {
		t.Fatalf("directory contents were modified or removed: %v", err)
	}
	records := db.(*state.FakeRepo).Records()
	if len(records) != 0 {
		t.Errorf("expected no history records for directory candidates, got %d", len(records))
	}
}

// TestAcceptanceScanDepth verifies that `filemaid scan --depth`
// enumerates immediate subdirectories and surfaces them as directory candidates
// without modifying their contents.
func TestAcceptanceScanDepth(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	classifier = &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Tags:     []string{},
		Action:   "move",
		Reason:   "text",
	}}

	desktop := filepath.Join(tmp, "Desktop")
	dirPath := filepath.Join(desktop, "project-folder")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dirPath, "child.txt")
	if err := os.WriteFile(child, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	scanDepth = 1
	t.Cleanup(func() { scanDepth = 0 })
	scanDir = desktop
	t.Cleanup(func() { scanDir = "" })

	if err := scanCmd.RunE(nil, []string{}); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	if _, err := os.Stat(dirPath); err != nil {
		t.Fatalf("directory was modified or removed: %v", err)
	}
	if _, err := os.Stat(child); err != nil {
		t.Fatalf("directory contents were modified or removed: %v", err)
	}
	records := db.(*state.FakeRepo).Records()
	if len(records) != 0 {
		t.Errorf("expected no history records for directory candidates, got %d", len(records))
	}
}

// TestAcceptanceScanIncludeDirsRespectsGuardrails verifies that hidden
// directories and directories outside allowed_dirs are skipped during scan.
func TestAcceptanceScanDepthRespectsGuardrails(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	classifier = &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Tags:     []string{},
		Action:   "move",
		Reason:   "text",
	}}

	desktop := filepath.Join(tmp, "Desktop")
	hiddenDir := filepath.Join(desktop, ".hidden")
	normalDir := filepath.Join(desktop, "normal")
	if err := os.MkdirAll(hiddenDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(normalDir, 0755); err != nil {
		t.Fatal(err)
	}

	scanDepth = 1
	t.Cleanup(func() { scanDepth = 0 })
	scanDir = desktop
	t.Cleanup(func() { scanDir = "" })

	results, err := runScanDir(context.Background(), desktop, "run-test", io.Discard, "")
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !strings.Contains(results[0].Path, "normal") {
		t.Errorf("expected normal directory, got %s", results[0].Path)
	}
}

// TestAcceptanceAppBundleTreatedAsDirectory verifies that `.app` bundles,
// which are directories on macOS, are treated as directory candidates.
func TestAcceptanceAppBundleTreatedAsDirectory(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	appBundle := filepath.Join(tmp, "Desktop", "MyApp.app")
	if err := os.MkdirAll(appBundle, 0755); err != nil {
		t.Fatal(err)
	}

	processDepth = 1
	t.Cleanup(func() { processDepth = 0 })

	results, err := processPaths(context.Background(), inputs(appBundle), "run-test", io.Discard, "")
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Kind != "directory" {
		t.Errorf("kind = %q, want directory", results[0].Kind)
	}
}
// TestIntegrationProjectDirectoryMoveAndUndo verifies that a directory
// classified as a project is moved whole to the review queue by
// `process --depth --move-projects` and restored to its original path by
// `undo --last`.
func TestIntegrationProjectDirectoryMoveAndUndo(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	applyDecision = actions.Apply
	applyDirectoryFunc = actions.ApplyDirectory
	classifier = &fakeClassifier{
		decision:    llm.Decision{Category: "Documents", Action: "move", Reason: "text"},
		dirDecision: llm.DirectoryDecision{Recommendation: "archive", Action: "move", Reason: "project folder"},
	}

	dirPath := filepath.Join(tmp, "Desktop", "thesis")
	sections := filepath.Join(dirPath, "sections")
	if err := os.MkdirAll(sections, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, "thesis.tex"), []byte("\\documentclass{article}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, "thesis.bib"), []byte("@article{sample}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sections, "chapter1.md"), []byte("# Chapter 1"), 0644); err != nil {
		t.Fatal(err)
	}

	moveProjects = true
	processDepth = 1
	t.Cleanup(func() {
		moveProjects = false
		processDepth = 0
	})

	if err := processCmd.RunE(processCmd, []string{dirPath}); err != nil {
		t.Fatalf("process failed: %v", err)
	}

	if _, err := os.Stat(dirPath); !os.IsNotExist(err) {
		t.Fatalf("expected original directory to be moved, still exists: %v", err)
	}

	records := db.(*state.FakeRepo).Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(records))
	}
	if records[0].Action != "move" {
		t.Errorf("action = %q, want move", records[0].Action)
	}
	if records[0].OriginalPath != dirPath {
		t.Errorf("original path = %q, want %q", records[0].OriginalPath, dirPath)
	}

	movedDir := records[0].FinalPath
	for _, rel := range []string{"thesis.tex", "thesis.bib", "sections/chapter1.md"} {
		if _, err := os.Stat(filepath.Join(movedDir, rel)); err != nil {
			t.Fatalf("expected %s to be moved whole: %v", rel, err)
		}
	}

	undoLast = true
	t.Cleanup(func() { undoLast = false })
	if err := undoCmd.RunE(undoCmd, nil); err != nil {
		t.Fatalf("undo failed: %v", err)
	}

	if _, err := os.Stat(dirPath); err != nil {
		t.Fatalf("directory not restored to original path: %v", err)
	}
	for _, rel := range []string{"thesis.tex", "thesis.bib", "sections/chapter1.md"} {
		if _, err := os.Stat(filepath.Join(dirPath, rel)); err != nil {
			t.Fatalf("expected %s to be restored: %v", rel, err)
		}
	}
	if _, err := os.Stat(movedDir); !os.IsNotExist(err) {
		t.Fatalf("expected moved directory to be gone after undo: %v", err)
	}

	records = db.(*state.FakeRepo).Records()
	if len(records) != 2 {
		t.Fatalf("expected 2 history records after undo, got %d", len(records))
	}
	if records[1].Action != "undo" {
		t.Errorf("second record action = %q, want undo", records[1].Action)
	}
	if records[1].OriginalPath != movedDir {
		t.Errorf("undo original path = %q, want %q", records[1].OriginalPath, movedDir)
	}
	if records[1].FinalPath != dirPath {
		t.Errorf("undo final path = %q, want %q", records[1].FinalPath, dirPath)
	}
}

// TestIntegrationProcessMoveProjectsDryRun verifies that
// `process --depth --move-projects --dry-run` previews a directory move without
// touching the filesystem or recording history.
func TestIntegrationProcessMoveProjectsDryRun(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	applyDirectoryFunc = actions.ApplyDirectory
	classifier = &fakeClassifier{
		dirDecision: llm.DirectoryDecision{Recommendation: "archive", Action: "move", Reason: "project folder"},
	}

	dirPath := filepath.Join(tmp, "Desktop", "thesis")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, "thesis.tex"), []byte("\\documentclass{article}"), 0644); err != nil {
		t.Fatal(err)
	}

	moveProjects = true
	processDepth = 1
	processDryRun = true
	t.Cleanup(func() {
		moveProjects = false
		processDepth = 0
		processDryRun = false
	})

	if err := processCmd.RunE(processCmd, []string{dirPath}); err != nil {
		t.Fatalf("process dry-run failed: %v", err)
	}

	if _, err := os.Stat(dirPath); err != nil {
		t.Fatalf("dry-run should not move directory: %v", err)
	}
	if len(db.(*state.FakeRepo).Records()) != 0 {
		t.Fatalf("expected no history records in dry-run, got %d", len(db.(*state.FakeRepo).Records()))
	}
}

// TestIntegrationUndoDryRun verifies that `undo --last --dry-run` previews a
// restore without moving files or recording an undo history row.
func TestIntegrationUndoDryRun(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	applyDirectoryFunc = actions.ApplyDirectory
	classifier = &fakeClassifier{
		dirDecision: llm.DirectoryDecision{Recommendation: "archive", Action: "move", Reason: "project folder"},
	}

	dirPath := filepath.Join(tmp, "Desktop", "thesis")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, "thesis.tex"), []byte("\\documentclass{article}"), 0644); err != nil {
		t.Fatal(err)
	}

	moveProjects = true
	processDepth = 1
	if err := processCmd.RunE(processCmd, []string{dirPath}); err != nil {
		t.Fatalf("process failed: %v", err)
	}
	moveProjects = false
	processDepth = 0

	records := db.(*state.FakeRepo).Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 history record after process, got %d", len(records))
	}
	movedDir := records[0].FinalPath

	undoLast = true
	undoDryRun = true
	t.Cleanup(func() {
		undoLast = false
		undoDryRun = false
	})
	if err := undoCmd.RunE(undoCmd, nil); err != nil {
		t.Fatalf("undo dry-run failed: %v", err)
	}

	if _, err := os.Stat(movedDir); err != nil {
		t.Fatalf("undo dry-run should not restore directory: %v", err)
	}
	if _, err := os.Stat(dirPath); !os.IsNotExist(err) {
		t.Fatalf("undo dry-run should not restore to original path: %v", err)
	}
	if len(db.(*state.FakeRepo).Records()) != 1 {
		t.Fatalf("expected 1 history record after dry-run undo, got %d", len(db.(*state.FakeRepo).Records()))
	}
}

// TestIntegrationNonProjectDirectoryProcessesFiles verifies that a directory
// that is not classified as a project is not moved whole; instead, scan
// descends into it and processes individual files when `--depth` is used.
func TestIntegrationNonProjectDirectoryProcessesFiles(t *testing.T) {
	resetGlobals(t)

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.MinAgeHours = 0
	db = state.NewFake()
	processFS = actions.NewRecordingFS()
	applyDecision = actions.Apply
	applyDirectoryFunc = actions.ApplyDirectory
	classifier = &fakeClassifier{
		decision:    llm.Decision{Category: "Documents", Action: "move", Reason: "text"},
		dirDecision: llm.DirectoryDecision{Recommendation: "review", Action: "", Reason: "not a project"},
	}

	scanRoot := filepath.Join(tmp, "Desktop")
	mixedDir := filepath.Join(scanRoot, "mixed")
	if err := os.MkdirAll(mixedDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mixedDir, "a.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mixedDir, "b.txt"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}

	oldTime := time.Now().Add(-time.Hour)
	for _, p := range []string{mixedDir, filepath.Join(mixedDir, "a.txt"), filepath.Join(mixedDir, "b.txt")} {
		if err := os.Chtimes(p, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}

	scanDir = scanRoot
	scanDepth = 2
	moveProjects = true
	scanGetCandidates = defaultScanGetCandidates
	t.Cleanup(func() {
		scanDir = ""
		scanDepth = 0
		moveProjects = false
	})

	if err := scanCmd.RunE(scanCmd, nil); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	if _, err := os.Stat(mixedDir); err != nil {
		t.Fatalf("non-project directory should remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "Documents", "a.txt")); err != nil {
		t.Fatalf("a.txt should be moved to Documents: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "Documents", "b.txt")); err != nil {
		t.Fatalf("b.txt should be moved to Documents: %v", err)
	}

	records := db.(*state.FakeRepo).Records()
	if len(records) != 2 {
		t.Fatalf("expected 2 history records for files, got %d", len(records))
	}
	for _, r := range records {
		if !strings.HasPrefix(r.OriginalPath, mixedDir) {
			t.Errorf("expected file record inside %s, got %s", mixedDir, r.OriginalPath)
		}
		if r.Action != "move" {
			t.Errorf("expected action move, got %s", r.Action)
		}
	}
}
