package hub

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestArchiveRoot_DefaultCategories(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	categories := map[string]string{
		"Screenshots": filepath.Join(home, "Documents", "Archive", "Screenshots"),
		"Documents":   filepath.Join(home, "Documents", "Archive", "Documents"),
		"Images":      filepath.Join(home, "Documents", "Archive", "Images"),
		"Unknown":     filepath.Join(home, ".filemaid", "review"),
	}

	got := ArchiveRoot(categories)
	want := filepath.Join(home, "Documents", "Archive")
	if got != want {
		t.Fatalf("ArchiveRoot = %q, want %q", got, want)
	}
}

func TestArchiveRoot_ExcludesUnknown(t *testing.T) {
	categories := map[string]string{
		"Unknown": "~/.filemaid/review",
	}
	if got := ArchiveRoot(categories); got != "" {
		t.Fatalf("ArchiveRoot = %q, want empty", got)
	}
}

func TestArchiveRoot_ScatteredCategories(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	categories := map[string]string{
		"A": filepath.Join(home, "Desktop"),
		"B": filepath.Join(home, "Downloads"),
	}
	got := ArchiveRoot(categories)
	want := home
	if got != want {
		t.Fatalf("ArchiveRoot = %q, want %q", got, want)
	}
}

func TestArchiveRoot_Empty(t *testing.T) {
	if got := ArchiveRoot(nil); got != "" {
		t.Fatalf("ArchiveRoot(nil) = %q, want empty", got)
	}
	if got := ArchiveRoot(map[string]string{}); got != "" {
		t.Fatalf("ArchiveRoot({}) = %q, want empty", got)
	}
}

func TestBuild_CreatesSmartFoldersAndAliasesAndAddsToSidebar(t *testing.T) {
	tmp := t.TempDir()
	hubDir := filepath.Join(tmp, "Filemaid")
	archiveDir := filepath.Join(tmp, "Archive")
	reviewDir := filepath.Join(tmp, "review")

	var smartDir string
	var smartCategories, smartTags, smartScopes []string
	var aliasCalls []string
	var sidebarPath string

	b := &Builder{
		SmartFolderBuilder: func(categories, tags, scopes []string, dir string) error {
			smartDir = dir
			smartCategories = categories
			smartTags = tags
			smartScopes = scopes
			return nil
		},
		AliasWriter: func(dir, name, target string) error {
			aliasCalls = append(aliasCalls, name+"="+target)
			return nil
		},
		SidebarAdder: func(path string) error {
			sidebarPath = path
			return nil
		},
	}

	opts := Options{
		HubDir:     hubDir,
		ArchiveDir: archiveDir,
		ReviewDir:  reviewDir,
		Categories: []string{"Images", "Documents"},
		Tags:       []string{"work"},
		Scopes:     []string{"/tmp"},
	}
	if err := b.Build(opts); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if smartDir != hubDir {
		t.Fatalf("smart folder dir = %q, want %q", smartDir, hubDir)
	}
	if !slices.Equal(smartCategories, []string{"Images", "Documents"}) {
		t.Fatalf("categories = %v, want [Images Documents]", smartCategories)
	}
	if !slices.Equal(smartTags, []string{"work"}) {
		t.Fatalf("tags = %v, want [work]", smartTags)
	}
	if !slices.Equal(smartScopes, []string{"/tmp"}) {
		t.Fatalf("scopes = %v, want [/tmp]", smartScopes)
	}

	slices.Sort(aliasCalls)
	wantAliases := []string{"Archive=" + archiveDir, "Review=" + reviewDir}
	if !slices.Equal(aliasCalls, wantAliases) {
		t.Fatalf("aliases = %v, want %v", aliasCalls, wantAliases)
	}

	if sidebarPath != hubDir {
		t.Fatalf("sidebar path = %q, want %q", sidebarPath, hubDir)
	}

	if _, err := os.Stat(hubDir); err != nil {
		t.Fatalf("hub dir not created: %v", err)
	}
	if _, err := os.Stat(archiveDir); err != nil {
		t.Fatalf("archive dir not created: %v", err)
	}
	if _, err := os.Stat(reviewDir); err != nil {
		t.Fatalf("review dir not created: %v", err)
	}
}

func TestBuild_SkipsEmptyDirs(t *testing.T) {
	tmp := t.TempDir()
	hubDir := filepath.Join(tmp, "Filemaid")

	var aliasCalls []string
	b := &Builder{
		SmartFolderBuilder: func(_, _, _ []string, _ string) error { return nil },
		AliasWriter: func(_, name, _ string) error {
			aliasCalls = append(aliasCalls, name)
			return nil
		},
		SidebarAdder: func(_ string) error { return nil },
	}

	if err := b.Build(Options{HubDir: hubDir}); err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if len(aliasCalls) != 0 {
		t.Fatalf("expected no aliases, got %v", aliasCalls)
	}
}

func TestBuild_SmartFolderError(t *testing.T) {
	tmp := t.TempDir()
	wantErr := errors.New("boom")
	b := &Builder{
		SmartFolderBuilder: func(_, _, _ []string, _ string) error { return wantErr },
		AliasWriter:        func(_, _, _ string) error { return nil },
		SidebarAdder:       func(_ string) error { return nil },
	}
	err := b.Build(Options{HubDir: tmp})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuild_AliasError(t *testing.T) {
	tmp := t.TempDir()
	wantErr := errors.New("alias fail")
	b := &Builder{
		SmartFolderBuilder: func(_, _, _ []string, _ string) error { return nil },
		AliasWriter:        func(_, _, _ string) error { return wantErr },
		SidebarAdder:       func(_ string) error { return nil },
	}
	err := b.Build(Options{HubDir: tmp, ArchiveDir: "/tmp/archive"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuild_SidebarError(t *testing.T) {
	tmp := t.TempDir()
	wantErr := errors.New("sidebar fail")
	b := &Builder{
		SmartFolderBuilder: func(_, _, _ []string, _ string) error { return nil },
		AliasWriter:        func(_, _, _ string) error { return nil },
		SidebarAdder:       func(_ string) error { return wantErr },
	}
	err := b.Build(Options{HubDir: tmp})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuild_MissingHubDir(t *testing.T) {
	b := &Builder{
		SmartFolderBuilder: func(_, _, _ []string, _ string) error { return nil },
		AliasWriter:        func(_, _, _ string) error { return nil },
		SidebarAdder:       func(_ string) error { return nil },
	}
	if err := b.Build(Options{}); err == nil {
		t.Fatal("expected error for empty hub dir")
	}
}

func TestCommonAncestor(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		want  string
	}{
		{
			name:  "siblings",
			paths: []string{"/a/b/c", "/a/b/d"},
			want:  filepath.Join("/a", "b"),
		},
		{
			name:  "single",
			paths: []string{"/a/b/c"},
			want:  "/a/b/c",
		},
		{
			name:  "no common",
			paths: []string{"/a/b", "/c/d"},
			want:  string(filepath.Separator),
		},
		{
			name:  "nested",
			paths: []string{"/a/b/c/d", "/a/b/c/e/f"},
			want:  filepath.Join("/a", "b", "c"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := commonAncestor(tt.paths)
			if got != tt.want {
				t.Fatalf("commonAncestor = %q, want %q", got, tt.want)
			}
		})
	}
}
