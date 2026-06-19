package cleaners

import (
	"os"
	"path/filepath"

	"github.com/logicminds/filemaid/internal/config"
)

var xcodeCleaner = Cleaner{
	Name:   "xcode",
	CanRun: xcodeCanRun,
	Run:    xcodeRun,
}

func xcodeCanRun() bool {
	if _, err := lookPath("xcodebuild"); err == nil {
		return true
	}
	if _, err := os.Stat(derivedDataPath()); err == nil {
		return true
	}
	return false
}

func derivedDataPath() string {
	return filepath.Join(home(), "Library", "Developer", "Xcode", "DerivedData")
}

func xcodeRun(dryRun bool, cfg *config.Config) CleanupResult {
	derivedData := derivedDataPath()
	if _, err := os.Stat(derivedData); err != nil {
		return CleanupResult{
			Name:   "xcode",
			Status: "skipped",
			Detail: "DerivedData does not exist",
		}
	}

	mode := "safe"
	if c, ok := cfg.DevCleanup["xcode"]; ok && c.Mode != "" {
		mode = c.Mode
	}

	cutoff := sysClock.Now() - 30*24*60*60
	var removed int64
	var action string

	if mode == "aggressive" {
		removed = removeDerivedData(derivedData, dryRun)
		action = "removed all DerivedData"
	} else {
		entries, err := os.ReadDir(derivedData)
		if err == nil {
			for _, entry := range entries {
				info, err := entry.Info()
				if err != nil {
					continue
				}
				if info.ModTime().Unix() < cutoff {
					removed += removeDerivedData(filepath.Join(derivedData, entry.Name()), dryRun)
				}
			}
		}
		action = "removed DerivedData entries older than 30 days"
	}

	status := "ok"
	if dryRun {
		status = "dry-run"
	}

	return CleanupResult{
		Name:       "xcode",
		Status:     status,
		Saved:      &removed,
		SavedHuman: Humanize(removed),
		Detail:     action,
	}
}

func removeDerivedData(path string, dryRun bool) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}

	if dryRun {
		if info.IsDir() {
			return DirSize(path)
		}
		return info.Size()
	}

	if !info.IsDir() {
		if err := os.Remove(path); err == nil {
			return info.Size()
		}
		return 0
	}

	var total int64
	entries, _ := os.ReadDir(path)
	for _, entry := range entries {
		child := filepath.Join(path, entry.Name())
		cinfo, err := entry.Info()
		if err != nil {
			continue
		}
		if cinfo.IsDir() {
			total += DirSize(child)
			_ = os.RemoveAll(child)
		} else {
			total += cinfo.Size()
			_ = os.Remove(child)
		}
	}
	return total
}
