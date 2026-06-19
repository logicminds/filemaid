package cleaners

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/logicminds/filemaid/internal/config"
)

var reviewCleaner = Cleaner{
	Name:   "review",
	CanRun: reviewCanRun,
	Run:    reviewRun,
}

func reviewCanRun() bool {
	return true
}

func reviewRun(dryRun bool, cfg *config.Config) CleanupResult {
	reviewDir := cfg.ReviewDir
	if reviewDir == "" {
		reviewDir = filepath.Join(home(), ".filemaid", "review")
	}

	if _, err := os.Stat(reviewDir); err != nil {
		return CleanupResult{
			Name:   "review",
			Status: "skipped",
			Detail: "review queue does not exist",
		}
	}

	if !cfg.ReviewCleanup.Enabled {
		return CleanupResult{
			Name:   "review",
			Status: "disabled",
			Detail: "disabled in config",
		}
	}

	mode := cfg.ReviewCleanup.Mode
	maxAgeDays := cfg.ReviewCleanup.MaxAgeDays
	if maxAgeDays == 0 {
		maxAgeDays = 30
	}

	var cutoff int64
	if mode == "aggressive" {
		cutoff = sysClock.Now()
	} else {
		cutoff = sysClock.Now() - int64(maxAgeDays)*24*60*60
	}

	var removedBytes int64
	removedFiles := 0
	skipped := 0

	_ = filepath.WalkDir(reviewDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			skipped++
			return nil
		}
		if info.ModTime().Unix() < cutoff {
			if dryRun {
				removedBytes += info.Size()
				removedFiles++
			} else {
				if err := os.Remove(path); err == nil {
					removedBytes += info.Size()
					removedFiles++
				} else {
					skipped++
				}
			}
		}
		return nil
	})

	if !dryRun {
		removeEmptyDirs(reviewDir)
	}

	status := "ok"
	if dryRun {
		status = "dry-run"
	}

	if removedFiles == 0 {
		return CleanupResult{
			Name:   "review",
			Status: status,
			Detail: "no stale review items",
		}
	}

	modeDetail := reviewModeDetail(mode, maxAgeDays)
	return CleanupResult{
		Name:       "review",
		Status:     status,
		Saved:      &removedBytes,
		SavedHuman: Humanize(removedBytes),
		Detail:     "removed " + strconv.Itoa(removedFiles) + " review item(s) (" + modeDetail + ")",
	}
}

func reviewModeDetail(mode string, maxAgeDays int) string {
	if mode == "aggressive" {
		return "aggressive mode"
	}
	return "older than " + strconv.Itoa(maxAgeDays) + " days"
}

func removeEmptyDirs(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	})
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, dir := range dirs {
		entries, _ := os.ReadDir(dir)
		if len(entries) == 0 {
			_ = os.Remove(dir)
		}
	}
}
