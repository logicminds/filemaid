package cleaners

import (
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// DirSize returns the total byte size of a directory tree. Symlinks to files
// are counted as the size of their target; symlinked directories are not
// traversed. Files that cannot be stated are skipped.
func DirSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	if !info.IsDir() {
		return info.Size()
	}

	var total int64
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			fi, err := os.Stat(p)
			if err != nil {
				return nil
			}
			if fi.IsDir() {
				return fs.SkipDir
			}
			total += fi.Size()
			return nil
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		total += fi.Size()
		return nil
	})
	return total
}

// Humanize formats bytes as human-readable units.
func Humanize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	f := float64(size)
	for _, unit := range []string{"KB", "MB", "GB", "TB"} {
		f /= 1024
		if f < 1024 {
			return fmt.Sprintf("%.1f %s", f, unit)
		}
	}
	return fmt.Sprintf("%.1f PB", f)
}

var sizeRe = regexp.MustCompile(`^([0-9]*\.?[0-9]+)\s*([A-Z]*)$`)

// ParseSize parses a human-readable size string (e.g. "1.2 GB", "0B") into bytes.
// It returns nil if the string cannot be parsed.
func ParseSize(text string) *int64 {
	text = strings.TrimSpace(strings.ToUpper(strings.ReplaceAll(text, " ", "")))
	if text == "" {
		return nil
	}
	m := sizeRe.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	value, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return nil
	}
	unit := m[2]
	if unit == "" {
		unit = "B"
	}
	units := map[string]int64{
		"B":  1,
		"KB": 1024,
		"MB": 1024 * 1024,
		"GB": 1024 * 1024 * 1024,
		"TB": 1024 * 1024 * 1024 * 1024,
		"PB": 1024 * 1024 * 1024 * 1024 * 1024,
	}
	factor, ok := units[unit]
	if !ok {
		return nil
	}
	out := int64(value * float64(factor))
	return &out
}

func dirSizeIfExists(path string) int64 {
	if _, err := os.Stat(path); err != nil {
		return 0
	}
	return DirSize(path)
}

func expandPath(path string) string {
	if path == "" {
		return path
	}
	if strings.HasPrefix(path, "~/") {
		h := home()
		if h == "" {
			return path
		}
		return filepath.Join(h, path[2:])
	}
	return path
}

func home() string {
	h, _ := os.UserHomeDir()
	if h != "" {
		return h
	}
	u, _ := user.Current()
	if u != nil {
		return u.HomeDir
	}
	return ""
}
