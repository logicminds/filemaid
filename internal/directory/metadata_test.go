package directory

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/logicminds/filemaid/internal/config"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write file %q: %v", name, err)
	}
}

func mkDir(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", name, err)
	}
}

func TestGather(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(t *testing.T) string
		cfg    *config.Config
		assert func(t *testing.T, meta *Metadata)
	}{
		{
			name: "mixed files and subdirectory",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				writeFile(t, dir, "alpha.txt", "alpha content")
				writeFile(t, dir, "beta.txt", "beta content")
				writeFile(t, dir, "gamma.go", "package main")
				mkDir(t, dir, "subdir")
				return dir
			},
			cfg: &config.Config{
				MaxDirSampleEntries: 50,
				MaxDirSampleBytes:   2048,
				ProjectMarkers:      []string{".git"},
			},
			assert: func(t *testing.T, meta *Metadata) {
				if meta.Base != filepath.Base(meta.Path) {
					t.Errorf("Base = %q, want %q", meta.Base, filepath.Base(meta.Path))
				}
				if meta.IsAppBundle {
					t.Error("IsAppBundle = true, want false")
				}
				if meta.ChildCount != 4 {
					t.Errorf("ChildCount = %d, want 4", meta.ChildCount)
				}
				wantSize := int64(len("alpha content") + len("beta content") + len("package main"))
				if meta.Size != wantSize {
					t.Errorf("Size = %d, want %d", meta.Size, wantSize)
				}
				wantExt := map[string]int{".txt": 2, ".go": 1}
				if !reflect.DeepEqual(meta.Extensions, wantExt) {
					t.Errorf("Extensions = %v, want %v", meta.Extensions, wantExt)
				}
				if len(meta.Markers) != 0 {
					t.Errorf("Markers = %v, want empty", meta.Markers)
				}
				if meta.Truncated != "" {
					t.Errorf("Truncated = %q, want empty", meta.Truncated)
				}
				if meta.Mtime.IsZero() {
					t.Error("Mtime is zero")
				}
			},
		},
		{
			name: "project markers detected as direct children",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				mkDir(t, dir, ".git")
				mkDir(t, dir, "node_modules")
				writeFile(t, dir, "main.go", "package main")
				return dir
			},
			cfg: &config.Config{
				MaxDirSampleEntries: 50,
				MaxDirSampleBytes:   2048,
				ProjectMarkers:      []string{".git", "node_modules", ".venv", "vendor"},
			},
			assert: func(t *testing.T, meta *Metadata) {
				if meta.ChildCount != 3 {
					t.Errorf("ChildCount = %d, want 3", meta.ChildCount)
				}
				wantMarkers := []string{".git", "node_modules"}
				if !reflect.DeepEqual(meta.Markers, wantMarkers) {
					t.Errorf("Markers = %v, want %v", meta.Markers, wantMarkers)
				}
			},
		},
		{
			name: "markers beyond sample cap are still detected",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
					writeFile(t, dir, name, "x")
				}
				mkDir(t, dir, "marker")
				return dir
			},
			cfg: &config.Config{
				MaxDirSampleEntries: 2,
				MaxDirSampleBytes:   2048,
				ProjectMarkers:      []string{"marker"},
			},
			assert: func(t *testing.T, meta *Metadata) {
				if meta.ChildCount != 5 {
					t.Errorf("ChildCount = %d, want 5", meta.ChildCount)
				}
				if !reflect.DeepEqual(meta.Markers, []string{"marker"}) {
					t.Errorf("Markers = %v, want [marker]", meta.Markers)
				}
				if meta.Truncated != "entries" {
					t.Errorf("Truncated = %q, want entries", meta.Truncated)
				}
			},
		},
		{
			name: ".app bundle is opaque",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				app := filepath.Join(dir, "MyApp.app")
				if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
					t.Fatalf("mkdir app bundle: %v", err)
				}
				if err := os.WriteFile(filepath.Join(app, "Contents", "MacOS", "myapp"), []byte("binary"), 0o755); err != nil {
					t.Fatalf("write binary: %v", err)
				}
				return app
			},
			cfg: &config.Config{
				MaxDirSampleEntries: 50,
				MaxDirSampleBytes:   2048,
				ProjectMarkers:      []string{".git"},
			},
			assert: func(t *testing.T, meta *Metadata) {
				if !meta.IsAppBundle {
					t.Error("IsAppBundle = false, want true")
				}
				if meta.ChildCount != 0 {
					t.Errorf("ChildCount = %d, want 0", meta.ChildCount)
				}
				if meta.Size != 0 {
					t.Errorf("Size = %d, want 0", meta.Size)
				}
				if len(meta.Extensions) != 0 {
					t.Errorf("Extensions = %v, want empty", meta.Extensions)
				}
			},
		},
		{
			name: ".app bundle child is not enumerated",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				app := filepath.Join(dir, "MyApp.app")
				if err := os.MkdirAll(filepath.Join(app, "Contents"), 0o755); err != nil {
					t.Fatalf("mkdir app bundle: %v", err)
				}
				if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte("plist"), 0o644); err != nil {
					t.Fatalf("write plist: %v", err)
				}
				writeFile(t, dir, "readme.txt", "hello")
				return dir
			},
			cfg: &config.Config{
				MaxDirSampleEntries: 50,
				MaxDirSampleBytes:   2048,
				ProjectMarkers:      []string{".git"},
			},
			assert: func(t *testing.T, meta *Metadata) {
				if meta.IsAppBundle {
					t.Error("IsAppBundle = true for parent, want false")
				}
				if meta.ChildCount != 2 {
					t.Errorf("ChildCount = %d, want 2", meta.ChildCount)
				}
				if meta.Extensions[".txt"] != 1 {
					t.Errorf("Extensions[.txt] = %d, want 1", meta.Extensions[".txt"])
				}
				if meta.Extensions[".plist"] != 0 {
					t.Errorf("Extensions[.plist] = %d, want 0 (bundle not enumerated)", meta.Extensions[".plist"])
				}
			},
		},
		{
			name: "entry cap truncates sample",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"} {
					writeFile(t, dir, name, "1234567890")
				}
				return dir
			},
			cfg: &config.Config{
				MaxDirSampleEntries: 2,
				MaxDirSampleBytes:   2048,
				ProjectMarkers:      []string{".git"},
			},
			assert: func(t *testing.T, meta *Metadata) {
				if meta.ChildCount != 5 {
					t.Errorf("ChildCount = %d, want 5", meta.ChildCount)
				}
				if meta.Size != 20 {
					t.Errorf("Size = %d, want 20", meta.Size)
				}
				if meta.Extensions[".txt"] != 2 {
					t.Errorf("Extensions[.txt] = %d, want 2", meta.Extensions[".txt"])
				}
				if meta.Truncated != "entries" {
					t.Errorf("Truncated = %q, want entries", meta.Truncated)
				}
			},
		},
		{
			name: "byte cap truncates sample",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				for _, name := range []string{"big1.bin", "big2.bin", "big3.bin"} {
					writeFile(t, dir, name, strings.Repeat("x", 1500))
				}
				return dir
			},
			cfg: &config.Config{
				MaxDirSampleEntries: 50,
				MaxDirSampleBytes:   2048,
				ProjectMarkers:      []string{".git"},
			},
			assert: func(t *testing.T, meta *Metadata) {
				if meta.ChildCount != 3 {
					t.Errorf("ChildCount = %d, want 3", meta.ChildCount)
				}
				if meta.Size != 1500 {
					t.Errorf("Size = %d, want 1500", meta.Size)
				}
				if meta.Extensions[".bin"] != 1 {
					t.Errorf("Extensions[.bin] = %d, want 1", meta.Extensions[".bin"])
				}
				if meta.Truncated != "bytes" {
					t.Errorf("Truncated = %q, want bytes", meta.Truncated)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := tt.setup(t)
			meta, err := Gather(dir, tt.cfg)
			if err != nil {
				t.Fatalf("Gather error: %v", err)
			}
			tt.assert(t, meta)
		})
	}
}

func TestGatherDefaults(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		writeFile(t, dir, string(rune('a'+i))+".txt", "x")
	}

	meta, err := Gather(dir, config.Defaults())
	if err != nil {
		t.Fatalf("Gather error: %v", err)
	}
	if meta.ChildCount != 3 {
		t.Errorf("ChildCount = %d, want 3", meta.ChildCount)
	}
	if meta.Truncated != "" {
		t.Errorf("Truncated = %q, want empty", meta.Truncated)
	}
	// Default markers from config.Defaults() should be present even if empty dir.
	if !sort.StringsAreSorted(meta.Markers) {
		t.Errorf("Markers not sorted: %v", meta.Markers)
	}
}

func TestGatherSnippets(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "readme.md", "# Project\n\ncross-ref: see also notes.txt")
	writeFile(t, dir, "notes.txt", "Details about the project.")
	writeFile(t, dir, "image.png", string([]byte{0x89, 0x50, 0x4E, 0x47})) // binary

	meta, err := Gather(dir, config.Defaults())
	if err != nil {
		t.Fatalf("Gather error: %v", err)
	}
	if len(meta.Snippets) != 2 {
		t.Errorf("Snippets = %v, want 2 entries", meta.Snippets)
	}
	if _, ok := meta.Snippets["readme.md"]; !ok {
		t.Errorf("missing snippet for readme.md")
	}
	if _, ok := meta.Snippets["notes.txt"]; !ok {
		t.Errorf("missing snippet for notes.txt")
	}
	if _, ok := meta.Snippets["image.png"]; ok {
		t.Errorf("binary image.png should not have a snippet")
	}
	if meta.Snippets["readme.md"] != "# Project\n\ncross-ref: see also notes.txt" {
		t.Errorf("readme.md snippet = %q, want full content", meta.Snippets["readme.md"])
	}
}

func TestGatherSnippetBudget(t *testing.T) {
	dir := t.TempDir()
	// Two text files whose combined snippets exceed the default byte budget.
	writeFile(t, dir, "a.txt", strings.Repeat("a", 1500))
	writeFile(t, dir, "b.txt", strings.Repeat("b", 1500))

	meta, err := Gather(dir, config.Defaults())
	if err != nil {
		t.Fatalf("Gather error: %v", err)
	}
	if len(meta.Snippets) == 0 {
		t.Fatal("expected at least one snippet")
	}
	var total int
	for _, s := range meta.Snippets {
		total += len(s)
	}
	if total > defaultSnippetBytes {
		t.Errorf("total snippet bytes = %d, want \u003c= %d", total, defaultSnippetBytes)
	}
}

func TestGatherNotADirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := Gather(f, config.Defaults()); err == nil {
		t.Error("Gather(file) expected error, got nil")
	}
}
