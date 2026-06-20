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
