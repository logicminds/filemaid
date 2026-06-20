package smartfolder

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCategoryPredicate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "documents",
			input: "Documents",
			want:  `kMDItemUserTags == "filemaid"cd`,
		},
		{
			name:  "empty",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace only",
			input: "   ",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := categoryPredicate(tt.input)
			if tt.want == "" {
				if got != "" {
					t.Fatalf("categoryPredicate(%q) = %q, want empty", tt.input, got)
				}
				return
			}

			if !strings.Contains(got, tt.want) {
				t.Errorf("categoryPredicate(%q) missing %q: got %q", tt.input, tt.want, got)
			}

			requiredKeys := []string{
				"kMDItemUserTags",
				"kMDItemFSName",
				"kMDItemFinderComment",
				"kMDItemTextContent",
			}
			for _, key := range requiredKeys {
				if !strings.Contains(got, key) {
					t.Errorf("categoryPredicate(%q) missing key %q: got %q", tt.input, key, got)
				}
			}

			if !strings.Contains(got, "cd") {
				t.Errorf("categoryPredicate(%q) missing case/diacritic flag: got %q", tt.input, got)
			}
		})
	}
}

func TestCategoryPredicateEscapesQuotes(t *testing.T) {
	got := categoryPredicate(`My "Documents"`)
	want := `My \"Documents\"`
	if !strings.Contains(got, want) {
		t.Fatalf("categoryPredicate did not escape quotes; want substring %q, got %q", want, got)
	}
}

func TestTagPredicate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		empty bool
	}{
		{name: "work", input: "work", empty: false},
		{name: "empty", input: "", empty: true},
		{name: "whitespace only", input: "   ", empty: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tagPredicate(tt.input)
			if tt.empty {
				if got != "" {
					t.Fatalf("tagPredicate(%q) = %q, want empty", tt.input, got)
				}
				return
			}

			for _, want := range []string{`kMDItemUserTags == "filemaid"cd`, `kMDItemUserTags == "` + tt.input + `"cd`} {
				if !strings.Contains(got, want) {
					t.Errorf("tagPredicate(%q) missing %q: got %q", tt.input, want, got)
				}
			}
		})
	}
}

func TestTagPredicateEscapesQuotes(t *testing.T) {
	got := tagPredicate(`"urgent"`)
	want := `\"urgent\"`
	if !strings.Contains(got, want) {
		t.Fatalf("tagPredicate did not escape quotes; want substring %q, got %q", want, got)
	}
}

func TestBuildCreatesSavedSearches(t *testing.T) {
	dir := t.TempDir()

	categories := []string{"Documents", "Images"}
	tags := []string{"work", "personal"}
	scopes := []string{"/Users/test/Documents", "/Users/test/Desktop"}

	if err := Build(categories, tags, scopes, dir); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	for _, name := range []string{"Documents", "Images", "work", "personal"} {
		path := filepath.Join(dir, name+".savedSearch")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected file %q to exist: %v", path, err)
			continue
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %q: %v", path, err)
		}

		var plist struct {
			XMLName xml.Name `xml:"plist"`
		}
		if err := xml.Unmarshal(data, &plist); err != nil {
			t.Errorf("%q is not valid XML plist: %v", path, err)
		}
	}
}

func TestBuildRemovesStaleSavedSearches(t *testing.T) {
	dir := t.TempDir()

	stale := filepath.Join(dir, "Old.savedSearch")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}

	if err := Build([]string{"New"}, nil, []string{"/tmp"}, dir); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale saved search %q should have been removed", stale)
	}

	if _, err := os.Stat(filepath.Join(dir, "New.savedSearch")); err != nil {
		t.Errorf("new saved search should exist: %v", err)
	}
}

func TestBuildSkipsEmptyAndFilemaidTags(t *testing.T) {
	dir := t.TempDir()

	if err := Build(nil, []string{"", "filemaid", "  "}, nil, dir); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasSuffix(name, ".savedSearch") {
			t.Errorf("unexpected saved search %q", name)
		}
	}
}

func TestBuildSkipsEmptyCategories(t *testing.T) {
	dir := t.TempDir()

	if err := Build([]string{"", "   "}, nil, nil, dir); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".savedSearch") {
			t.Errorf("unexpected saved search %q", entry.Name())
		}
	}
}

func TestBuildNilSlices(t *testing.T) {
	dir := t.TempDir()

	if err := Build(nil, nil, nil, dir); err != nil {
		t.Fatalf("Build with nil slices failed: %v", err)
	}
}

func TestBuildNormalizesFileNames(t *testing.T) {
	dir := t.TempDir()

	if err := Build([]string{"foo/bar "}, nil, nil, dir); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	path := filepath.Join(dir, "foo-bar.savedSearch")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected normalized file %q to exist: %v", path, err)
	}
}

func TestWriteSavedSearch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.savedSearch")

	if err := writeSavedSearch(path, "test", `(kMDItemUserTags == "filemaid"cd)`, []string{"/Users/test"}); err != nil {
		t.Fatalf("writeSavedSearch failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved search: %v", err)
	}

	content := string(data)
	for _, want := range []string{
		"CompatibleVersion",
		"RawQuery",
		"RawQueryDict",
		"FinderFilesOnly",
		"UserFilesOnly",
		"SearchScopes",
		"SearchCriteria",
		"CurrentFolderPath",
		"FXScopeArrayOfPaths",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("saved search missing %q", want)
		}
	}

	var plist struct {
		XMLName xml.Name `xml:"plist"`
	}
	if err := xml.Unmarshal(data, &plist); err != nil {
		t.Errorf("saved search is not valid XML plist: %v", err)
	}
}

func TestBuildSkipsTagWhenCategoryCollides(t *testing.T) {
	tmp := t.TempDir()
	categories := []string{"Documents"}
	tags := []string{"Documents", "work"}

	if err := Build(categories, tags, []string{"/tmp"}, tmp); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}

	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}

	// Documents.savedSearch should come from categoryPredicate, not tagPredicate.
	if len(names) != 2 {
		t.Fatalf("got %d saved searches, want 2: %v", len(names), names)
	}

	data, err := os.ReadFile(filepath.Join(tmp, "Documents.savedSearch"))
	if err != nil {
		t.Fatalf("read Documents.savedSearch: %v", err)
	}
	if !strings.Contains(string(data), "kMDItemTextContent") {
		t.Errorf("Documents.savedSearch missing category predicate content clause")
	}
}
