package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/logicminds/filemaid/internal/actions"
	"github.com/logicminds/filemaid/internal/llm"
	"github.com/logicminds/filemaid/internal/state"

	"github.com/spf13/pflag"
)

func TestScanDirProcessesFiles(t *testing.T) {
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

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("hello"), 0644)

	scanGetCandidates = func(dir string, depth int) ([]processInput, []string, error) {
		return []processInput{{path: src}}, nil, nil
	}
	t.Cleanup(func() { scanGetCandidates = defaultScanGetCandidates })

	if _, err := runScanDir(context.Background(), filepath.Join(tmp, "Desktop"), "run-test", io.Discard, ""); err != nil {
		t.Fatal(err)
	}

	records := db.(*state.FakeRepo).Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(records))
	}
}

func TestScanDirRejectsNotAllowed(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()

	buf := captureSlog(t)
	_, _ = runScanDir(context.Background(), filepath.Join(tmp, "NotAllowed"), "run-test", io.Discard, "")

	if !bytes.Contains(buf.Bytes(), []byte("scan directory not allowed")) {
		t.Errorf("expected 'scan directory not allowed' log, got %q", buf.String())
	}
}

func TestScanDirPermissionError(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()

	scanGetCandidates = func(dir string, depth int) ([]processInput, []string, error) {
		return nil, nil, os.ErrPermission
	}
	t.Cleanup(func() { scanGetCandidates = defaultScanGetCandidates })

	buf := captureSlog(t)
	_, _ = runScanDir(context.Background(), filepath.Join(tmp, "Desktop"), "run-test", io.Discard, "")

	if !bytes.Contains(buf.Bytes(), []byte("permission denied")) {
		t.Errorf("expected 'permission denied' log, got %q", buf.String())
	}
}

func TestScanCommandFailsValidationBeforeScanning(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	fake := &fakeClassifier{validate: errors.New("model not ready")}
	classifier = fake

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("hello"), 0644)

	scanGetCandidates = func(dir string, depth int) ([]processInput, []string, error) {
		t.Fatal("scanGetCandidates should not be called when validation fails")
		return nil, nil, nil
	}
	t.Cleanup(func() { scanGetCandidates = defaultScanGetCandidates })

	err := scanCmd.RunE(nil, []string{})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "model validation failed") {
		t.Errorf("expected model validation failed error, got %v", err)
	}
	if !fake.validated {
		t.Error("expected Validate to be called")
	}

	records := db.(*state.FakeRepo).Records()
	if len(records) != 0 {
		t.Errorf("expected no history records, got %d", len(records))
	}
}

func TestScanCommandSucceedsWhenValidationPasses(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	fake := &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Tags:     []string{},
		Action:   "move",
		Reason:   "text",
	}}
	classifier = fake

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("hello"), 0644)

	scanGetCandidates = func(dir string, depth int) ([]processInput, []string, error) {
		return []processInput{{path: src}}, nil, nil
	}
	t.Cleanup(func() { scanGetCandidates = defaultScanGetCandidates })

	scanDir = filepath.Join(tmp, "Desktop")
	t.Cleanup(func() { scanDir = "" })

	if err := scanCmd.RunE(nil, []string{}); err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if !fake.validated {
		t.Error("expected Validate to be called")
	}

	records := db.(*state.FakeRepo).Records()
	if len(records) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(records))
	}
}

func TestScanDirIncludesDirectoriesWhenFlagSet(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	dirPath := filepath.Join(tmp, "Desktop", "project-folder")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}

	scanDepth = 1
	t.Cleanup(func() { scanDepth = 0 })
	scanGetCandidates = func(dir string, depth int) ([]processInput, []string, error) {
		return nil, []string{dirPath}, nil
	}
	t.Cleanup(func() { scanGetCandidates = defaultScanGetCandidates })

	results, err := runScanDir(context.Background(), filepath.Join(tmp, "Desktop"), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Kind != "directory" {
		t.Errorf("kind = %q, want directory", results[0].Kind)
	}
	if results[0].Action != "review" {
		t.Errorf("action = %q, want review", results[0].Action)
	}
	records := db.(*state.FakeRepo).Records()
	if len(records) != 0 {
		t.Errorf("expected no history records for directory candidates, got %d", len(records))
	}
}

func TestScanDirSkipsHiddenDirectories(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	dirPath := filepath.Join(tmp, "Desktop", ".hidden-dir")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}

	scanDepth = 1
	t.Cleanup(func() { scanDepth = 0 })
	scanGetCandidates = func(dir string, depth int) ([]processInput, []string, error) {
		return nil, []string{dirPath}, nil
	}
	t.Cleanup(func() { scanGetCandidates = defaultScanGetCandidates })

	results, err := runScanDir(context.Background(), filepath.Join(tmp, "Desktop"), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestScanDirSkipsDirectoriesOutsideAllowedDirs(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	dirPath := filepath.Join(tmp, "Outside", "project-folder")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}

	scanDepth = 1
	t.Cleanup(func() { scanDepth = 0 })
	scanGetCandidates = func(dir string, depth int) ([]processInput, []string, error) {
		return nil, []string{dirPath}, nil
	}
	t.Cleanup(func() { scanGetCandidates = defaultScanGetCandidates })

	results, err := runScanDir(context.Background(), filepath.Join(tmp, "Outside"), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestScanDirRespectsMinAgeForDirectories(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.MinAgeHours = 1
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	recentDir := filepath.Join(tmp, "Desktop", "recent-project")
	if err := os.MkdirAll(recentDir, 0755); err != nil {
		t.Fatal(err)
	}
	recentTime := time.Now()
	if err := os.Chtimes(recentDir, recentTime, recentTime); err != nil {
		t.Fatal(err)
	}

	scanDepth = 1
	t.Cleanup(func() { scanDepth = 0 })
	scanGetCandidates = func(dir string, depth int) ([]processInput, []string, error) {
		return nil, []string{recentDir}, nil
	}
	t.Cleanup(func() { scanGetCandidates = defaultScanGetCandidates })

	results, err := runScanDir(context.Background(), filepath.Join(tmp, "Desktop"), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results for recent directory, got %d", len(results))
	}
}

func TestScanDirWithoutFlagSkipsDirectories(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	dirPath := filepath.Join(tmp, "Desktop", "project-folder")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatal(err)
	}

	scanDepth = 0
	scanGetCandidates = func(dir string, depth int) ([]processInput, []string, error) {
		return nil, []string{dirPath}, nil
	}
	t.Cleanup(func() { scanGetCandidates = defaultScanGetCandidates })

	results, err := runScanDir(context.Background(), filepath.Join(tmp, "Desktop"), "run-test", io.Discard, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestScanDepthFlagDefaults(t *testing.T) {
	flag := scanCmd.Flags().Lookup("depth")
	if flag == nil {
		t.Fatal("depth flag not registered")
	}
	if flag.NoOptDefVal != "1" {
		t.Errorf("NoOptDefVal = %q, want 1", flag.NoOptDefVal)
	}

	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	var v int
	fs.IntVar(&v, "depth", 0, "descend into directories N levels (0 = file-only)")
	fs.Lookup("depth").NoOptDefVal = "1"

	if err := fs.Parse([]string{"--depth"}); err != nil {
		t.Fatalf("bare flag parse failed: %v", err)
	}
	if v != 1 {
		t.Errorf("bare --depth: got %d, want 1", v)
	}

	v = 0
	if err := fs.Parse([]string{"--depth=3"}); err != nil {
		t.Fatalf("valued flag parse failed: %v", err)
	}
	if v != 3 {
		t.Errorf("--depth=3: got %d, want 3", v)
	}
}

func TestScanDepthFlagRejectsNegative(t *testing.T) {
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

	scanDepth = -1
	scanDir = filepath.Join(tmp, "Desktop")
	t.Cleanup(func() { scanDepth = 0; scanDir = "" })

	err := scanCmd.RunE(nil, []string{})
	if err == nil {
		t.Fatal("expected error for negative --depth")
	}
	if !strings.Contains(err.Error(), "depth must be >= 0") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDefaultScanGetCandidates(t *testing.T) {
	oldCfg := cfg
	t.Cleanup(func() { cfg = oldCfg })

	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.ProjectMarkers = []string{".git"}
	setOldMtime := func(path string) {
		old := time.Now().Add(-24 * time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatalf("Chtimes %s: %v", path, err)
		}
	}

	desktop := filepath.Join(tmp, "Desktop")

	// depth-1 structure
	d1 := filepath.Join(desktop, "subdir")
	mustMkdir(t, d1)
	mustWriteFile(t, filepath.Join(d1, "file.txt"), "hello")
	nested := filepath.Join(d1, "nested")
	mustMkdir(t, nested)
	mustWriteFile(t, filepath.Join(nested, "deep.txt"), "deep")

	// project marker structure
	proj := filepath.Join(desktop, "proj")
	mustMkdir(t, proj)
	mustMkdir(t, filepath.Join(proj, ".git"))
	mustWriteFile(t, filepath.Join(proj, ".git", "HEAD"), "ref")
	mustWriteFile(t, filepath.Join(proj, "readme.txt"), "readme")

	// hidden directory
	hidden := filepath.Join(desktop, ".hidden")
	mustMkdir(t, hidden)
	mustWriteFile(t, filepath.Join(hidden, "secret.txt"), "secret")

	// app bundle
	app := filepath.Join(desktop, "MyApp.app")
	mustMkdir(t, app)
	mustWriteFile(t, filepath.Join(app, "Contents", "Info.plist"), "plist")

	// outside allowed_dirs structure (created under a restricted allowed set)
	allowedRoot := filepath.Join(desktop, "allowed")
	mustMkdir(t, allowedRoot)
	mustWriteFile(t, filepath.Join(allowedRoot, "ok.txt"), "ok")
	rejectedRoot := filepath.Join(desktop, "rejected")
	mustMkdir(t, rejectedRoot)
	mustWriteFile(t, filepath.Join(rejectedRoot, "bad.txt"), "bad")

	setOldMtime(desktop)
	setOldMtime(d1)
	setOldMtime(nested)
	setOldMtime(proj)
	setOldMtime(hidden)
	setOldMtime(app)
	setOldMtime(allowedRoot)
	setOldMtime(rejectedRoot)

	tests := []struct {
		name         string
		depth        int
		allowedDirs  []string
		wantFiles    []string
		wantDirs     []string
		wantFilesNot []string
		wantDirsNot  []string
	}{
		{
			name:      "file_only_returns_immediate_files_and_dirs",
			depth:     0,
			wantFiles: nil,
			wantDirs:  []string{d1, proj, hidden, app, allowedRoot, rejectedRoot},
		},
		{
			name:      "depth_1_enumerates_immediate_directories",
			depth:     1,
			wantFiles: nil,
			wantDirs:  []string{d1, proj, app, allowedRoot, rejectedRoot},
			wantFilesNot: []string{
				filepath.Join(d1, "file.txt"),
				filepath.Join(nested, "deep.txt"),
				filepath.Join(proj, "readme.txt"),
			},
			wantDirsNot: []string{hidden, nested, filepath.Join(proj, ".git"), filepath.Join(app, "Contents")},
		},
		{
			name:      "depth_2_recurses_into_subdirectories",
			depth:     2,
			wantFiles: []string{filepath.Join(d1, "file.txt")},
			wantDirs:  []string{d1, nested, proj, app, allowedRoot, rejectedRoot},
			wantFilesNot: []string{
				filepath.Join(nested, "deep.txt"),
				filepath.Join(proj, "readme.txt"),
				filepath.Join(app, "Contents", "Info.plist"),
			},
			wantDirsNot: []string{hidden, filepath.Join(proj, ".git"), filepath.Join(app, "Contents")},
		},
		{
			name:      "project_marker_stops_recursion",
			depth:     2,
			wantFiles: []string{filepath.Join(d1, "file.txt")},
			wantDirs:  []string{d1, nested, proj, app, allowedRoot, rejectedRoot},
			wantFilesNot: []string{
				filepath.Join(proj, "readme.txt"),
				filepath.Join(nested, "deep.txt"),
			},
			wantDirsNot: []string{filepath.Join(proj, ".git"), hidden},
		},
		{
			name:      "hidden_directory_skipped_at_depth",
			depth:     2,
			wantFiles: []string{filepath.Join(d1, "file.txt")},
			wantDirs:  []string{d1, nested, proj, app, allowedRoot, rejectedRoot},
			wantFilesNot: []string{
				filepath.Join(hidden, "secret.txt"),
				filepath.Join(nested, "deep.txt"),
			},
			wantDirsNot: []string{hidden},
		},
		{
			name:        "allowed_dirs_guardrail_at_depth",
			depth:       2,
			allowedDirs: []string{allowedRoot},
			wantFiles:   []string{filepath.Join(allowedRoot, "ok.txt")},
			wantDirs:    []string{allowedRoot},
			wantFilesNot: []string{
				filepath.Join(d1, "file.txt"),
				filepath.Join(rejectedRoot, "bad.txt"),
				filepath.Join(proj, "readme.txt"),
			},
			wantDirsNot: []string{d1, rejectedRoot, proj, hidden, app, nested},
		},
		{
			name:      "app_bundle_treated_as_opaque_directory",
			depth:     2,
			wantFiles: []string{filepath.Join(d1, "file.txt")},
			wantDirs:  []string{d1, nested, proj, app, allowedRoot, rejectedRoot},
			wantFilesNot: []string{
				filepath.Join(app, "Contents", "Info.plist"),
				filepath.Join(nested, "deep.txt"),
			},
			wantDirsNot: []string{filepath.Join(app, "Contents"), hidden},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.allowedDirs != nil {
				cfg.AllowedDirs = tt.allowedDirs
			} else {
				cfg.AllowedDirs = testConfig(tmp).AllowedDirs
			}

			files, dirs, err := defaultScanGetCandidates(desktop, tt.depth)
			if err != nil {
				t.Fatalf("defaultScanGetCandidates: %v", err)
			}
			fileSet := make(map[string]struct{}, len(files))
			for _, f := range files {
				fileSet[f.path] = struct{}{}
			}
			dirSet := sliceToSet(dirs)
			for _, want := range tt.wantFiles {
				if _, ok := fileSet[want]; !ok {
					t.Errorf("missing file %s\nfiles = %v", want, files)
				}
			}
			for _, want := range tt.wantDirs {
				if _, ok := dirSet[want]; !ok {
					t.Errorf("missing dir %s\ndirs = %v", want, dirs)
				}
			}
			for _, notWant := range tt.wantFilesNot {
				if _, ok := fileSet[notWant]; ok {
					t.Errorf("unexpected file %s", notWant)
				}
			}
			for _, notWant := range tt.wantDirsNot {
				if _, ok := dirSet[notWant]; ok {
					t.Errorf("unexpected dir %s", notWant)
				}
			}
		})
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func sliceToSet(ss []string) map[string]struct{} {
	m := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		m[s] = struct{}{}
	}
	return m
}
