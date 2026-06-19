package cli

import (
	"strings"
)

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
