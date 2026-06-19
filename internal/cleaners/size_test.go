package cleaners

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirSize(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a.txt"), []byte("hello"))
	mustWriteFile(t, filepath.Join(dir, "b.txt"), []byte("world!"))
	sub := filepath.Join(dir, "sub")
	mustMkdir(t, sub)
	mustWriteFile(t, filepath.Join(sub, "c.txt"), make([]byte, 100))

	want := int64(5 + 6 + 100)
	if got := DirSize(dir); got != want {
		t.Fatalf("DirSize = %d, want %d", got, want)
	}

	if DirSize(filepath.Join(dir, "missing")) != 0 {
		t.Fatal("DirSize on missing path should be 0")
	}
	linkTarget := filepath.Join(dir, "target.txt")
	mustWriteFile(t, linkTarget, []byte("target"))
	_ = os.Symlink(linkTarget, filepath.Join(dir, "link.txt"))
	// Symlinks to files are followed and counted as their target size.
	if got := DirSize(dir); got != want+12 {
		t.Fatalf("DirSize should count symlink target: got %d, want %d", got, want+12)
	}
}

func TestHumanize(t *testing.T) {
	tests := []struct {
		size int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{1024 * 1024 * 1024 * 1024, "1.0 TB"},
		{1024 * 1024 * 1024 * 1024 * 1024, "1024.0 PB"},
	}
	for _, tc := range tests {
		if got := Humanize(tc.size); got != tc.want {
			t.Errorf("Humanize(%d) = %q, want %q", tc.size, got, tc.want)
		}
	}
}

func TestParseSize(t *testing.T) {
	oneGB := 1024 * 1024 * 1024
	onepointtwoGB := int64(1.2 * float64(oneGB))
	tests := []struct {
		text string
		want *int64
	}{
		{"0B", ptr(int64(0))},
		{"1.2GB", ptr(onepointtwoGB)},
		{"  10 MB ", ptr(10 * 1024 * 1024)},
		{"5 KB", ptr(5 * 1024)},
		{"100", ptr(100)},
		{"", nil},
		{"abc", nil},
		{"1.2 XB", nil},
	}
	for _, tc := range tests {
		got := ParseSize(tc.text)
		if tc.want == nil {
			if got != nil {
				t.Errorf("ParseSize(%q) = %d, want nil", tc.text, *got)
			}
			continue
		}
		if got == nil || *got != *tc.want {
			var gotv int64
			if got != nil {
				gotv = *got
			}
			t.Errorf("ParseSize(%q) = %d, want %d", tc.text, gotv, *tc.want)
		}
	}
}

func ptr(i int64) *int64 { return &i }
