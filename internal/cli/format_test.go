package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCollapseHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cases := []struct {
		path string
		want string
	}{
		{filepath.Join(home, "Downloads", "file.txt"), "~/Downloads/file.txt"},
		{home, "~"},
		{"/tmp/note.txt", "/tmp/note.txt"},
		{"", ""},
	}

	for _, c := range cases {
		got := collapseHome(c.path)
		if got != c.want {
			t.Errorf("collapseHome(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestRenderTable(t *testing.T) {
	out := renderTable(
		[]string{"A", "B"},
		[][]string{{"one", "two"}, {"three", "four"}},
	)
	if !strings.Contains(out, "one") || !strings.Contains(out, "B") {
		t.Errorf("table output missing expected content:\n%s", out)
	}
}
func TestTruncatePath(t *testing.T) {
	cases := []struct {
		path string
		max  int
		want string
	}{
		{"/tmp/note.txt", 20, "/tmp/note.txt"},
		{"/very/long/path/to/file.txt", 20, "/very/l ... file.txt"},
		{"/very/long/path/to/averylongfilename.txt", 20, "...ylongfilename.txt"},
	}
	for _, c := range cases {
		got := truncatePath(c.path, c.max)
		if got != c.want {
			t.Errorf("truncatePath(%q, %d) = %q, want %q", c.path, c.max, got, c.want)
		}
	}
}

func TestTruncateTags(t *testing.T) {
	cases := []struct {
		tags []string
		max  int
		want string
	}{
		{[]string{"Screenshots", "email", "inbox", "browser"}, 20, "Screenshots, emai..."},
	}
	for _, c := range cases {
		got := truncateTags(c.tags, c.max)
		if got != c.want {
			t.Errorf("truncateTags(%v, %d) = %q, want %q", c.tags, c.max, got, c.want)
		}
	}
}
