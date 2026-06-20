package cli

import (
	"os"
	"path/filepath"
	"strings"
)

// collapseHome replaces the user's home directory prefix with "~" for display.
// It returns the original path unchanged if the prefix does not match or home
// cannot be determined. The result uses "/" separators so it matches the
// conventional "~/..." notation regardless of OS.
func collapseHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	home = filepath.Clean(home)
	rel, err := filepath.Rel(home, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "..") {
		return p
	}
	return filepath.ToSlash(filepath.Join("~", rel))
}

// renderTable renders an ASCII table from headers and rows.
func renderTable(headers []string, rows [][]string) string {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	sep := "+" + strings.Join(mapSlice(widths, func(w int) string { return strings.Repeat("-", w+2) }), "+") + "+"

	var lines []string
	lines = append(lines, sep)
	lines = append(lines, "| "+strings.Join(mapSliceIndex(headers, widths, func(h string, w int) string { return padRight(h, w) }), " | ")+" |")
	lines = append(lines, sep)
	for _, row := range rows {
		cells := mapSliceIndex(row, widths, func(cell string, w int) string { return padRight(cell, w) })
		lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
	}
	lines = append(lines, sep)
	return strings.Join(lines, "\n")
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func mapSlice[T any, U any](in []T, fn func(T) U) []U {
	out := make([]U, len(in))
	for i, v := range in {
		out[i] = fn(v)
	}
	return out
}

func mapSliceIndex[T any, U any](in []T, widths []int, fn func(T, int) U) []U {
	out := make([]U, len(in))
	for i, v := range in {
		out[i] = fn(v, widths[i])
	}
	return out
}

// isTerminal reports whether f is connected to an interactive terminal.
// It uses the character-device check, which is sufficient on macOS and Unix.
func isTerminal(f *os.File) bool {
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice != 0
}

// ANSI color/formatting codes. These are only applied when stdout is a terminal.
const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
)

// colorize wraps s with code and reset when useColor is true.
func colorize(s, code string, useColor bool) string {
	if !useColor {
		return s
	}
	return code + s + colorReset
}

// truncatePath shortens p to at most maxLen runes, preserving the basename.
func truncatePath(p string, maxLen int) string {
	if maxLen <= 0 {
		return p
	}
	r := []rune(p)
	if len(r) <= maxLen {
		return p
	}
	base := []rune(filepath.Base(p))
	if len(base) >= maxLen {
		if maxLen <= 3 {
			return string(base[len(base)-maxLen:])
		}
		return "..." + string(base[len(base)-maxLen+3:])
	}
	// "prefix ... basename"
	reserve := len(base) + 5 // " ... "
	if reserve > maxLen {
		return "..." + string(base[len(base)-maxLen+3:])
	}
	prefixLen := maxLen - reserve
	return string(r[:prefixLen]) + " ... " + string(base)
}

// truncateTags joins tags and truncates the result to maxLen runes.
func truncateTags(tags []string, maxLen int) string {
	if maxLen <= 0 {
		return strings.Join(tags, ", ")
	}
	s := strings.Join(tags, ", ")
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return "..."
	}
	return string(r[:maxLen-3]) + "..."
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
