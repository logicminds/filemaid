package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/logicminds/filemaid/internal/actions"
	"github.com/logicminds/filemaid/internal/config"
	"github.com/logicminds/filemaid/internal/llm"
	"github.com/logicminds/filemaid/internal/state"
)

func TestRegenerateSmartFolders_Disabled(t *testing.T) {
	cfg = &config.Config{SmartFolders: false}
	if err := regenerateSmartFolders(); err != nil {
		t.Fatalf("expected nil when SmartFolders=false, got %v", err)
	}
}

func TestSmartFoldersCmd_ErrorsWhenDisabled(t *testing.T) {
	cfg = &config.Config{SmartFolders: false}
	err := smartFoldersCmd.RunE(nil, []string{})
	if err == nil {
		t.Fatal("expected error when SmartFolders=false")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("expected disabled error, got %v", err)
	}
}

func TestRegenerateSmartFolders_BuildsSavedSearches(t *testing.T) {
	tmp := t.TempDir()
	smartDir := filepath.Join(tmp, "SmartFolders")
	desktop := filepath.Join(tmp, "Desktop")
	os.MkdirAll(desktop, 0755)

	cfg = &config.Config{
		SmartFolders:    true,
		SmartFoldersDir: smartDir,
		AllowedDirs:     []string{desktop},
		Categories: map[string]string{
			"Images":    filepath.Join(tmp, "Images"),
			"Documents": filepath.Join(tmp, "Documents"),
		},
	}
	fake := state.NewFake()
	if err := fake.Record("/a", "/b", "h1", "Images", []string{"work", "personal"}, "move", "r", "", llm.Metrics{}); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	db = fake

	if err := regenerateSmartFolders(); err != nil {
		t.Fatalf("regenerateSmartFolders failed: %v", err)
	}

	entries, err := os.ReadDir(smartDir)
	if err != nil {
		t.Fatalf("read smart folder dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	want := []string{"Documents.savedSearch", "Images.savedSearch", "personal.savedSearch", "work.savedSearch"}
	if !slices.Equal(names, want) {
		t.Fatalf("got saved searches %v, want %v", names, want)
	}
}

func TestProcessCommand_RegeneratesSmartFoldersAndWarnsOnFailure(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.SmartFolders = true
	cfg.SmartFoldersDir = "/dev/null/invalid-smart-folders"
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	buf := captureSlog(t)

	fake := &fakeClassifier{decision: llm.Decision{
		Category: "Documents",
		Tags:     []string{"txt"},
		Action:   "move",
		Reason:   "text",
	}}
	classifier = fake

	src := filepath.Join(tmp, "Desktop", "note.txt")
	os.MkdirAll(filepath.Dir(src), 0755)
	os.WriteFile(src, []byte("hello"), 0644)

	if err := processCmd.RunE(nil, []string{src}); err != nil {
		t.Fatalf("process failed: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("smart folder regeneration failed")) {
		t.Fatalf("expected warning in logs, got %q", buf.String())
	}
}

func TestScanCommand_RegeneratesSmartFoldersAndWarnsOnFailure(t *testing.T) {
	tmp := t.TempDir()
	cfg = testConfig(tmp)
	cfg.SmartFolders = true
	cfg.SmartFoldersDir = "/dev/null/invalid-smart-folders"
	db = state.NewFake()
	processFS = actions.NewRecordingFS()

	buf := captureSlog(t)

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

	scanGetFiles = func(dir string) ([]string, error) {
		return []string{src}, nil
	}
	t.Cleanup(func() { scanGetFiles = defaultScanGetFiles })

	scanDir = filepath.Join(tmp, "Desktop")
	t.Cleanup(func() { scanDir = "" })

	if err := scanCmd.RunE(nil, []string{}); err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("smart folder regeneration failed")) {
		t.Fatalf("expected warning in logs, got %q", buf.String())
	}
}
