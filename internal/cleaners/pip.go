package cleaners

import (
	"path/filepath"
	"strings"

	"github.com/logicminds/filemaid/internal/config"
)

var pipCleaner = Cleaner{
	Name:   "pip",
	CanRun: pipCanRun,
	Run:    pipRun,
}

func pipCanRun() bool {
	return true
}

func pipCacheDir() string {
	out, err := runner.Run("python3", "-m", "pip", "cache", "dir")
	if err == nil {
		path := strings.TrimSpace(string(out))
		if path != "" {
			return expandPath(path)
		}
	}
	return filepath.Join(home(), "Library", "Caches", "pip")
}

func pipRun(dryRun bool, cfg *config.Config) CleanupResult {
	cacheDir := pipCacheDir()
	before := dirSizeIfExists(cacheDir)

	if dryRun {
		saved := before
		return CleanupResult{
			Name:       "pip",
			Status:     "dry-run",
			Saved:      &saved,
			SavedHuman: Humanize(before),
			Detail:     "would purge cache",
		}
	}

	out, err := runner.Run("python3", "-m", "pip", "cache", "purge")
	if err != nil {
		return CleanupResult{
			Name:   "pip",
			Status: "failed",
			Detail: err.Error(),
		}
	}

	after := dirSizeIfExists(cacheDir)
	saved := before - after
	if saved < 0 {
		saved = 0
	}
	output := strings.TrimSpace(string(out))
	detail := "cache purged"
	if output != "" {
		detail = "cache purged\n" + output
	}

	return CleanupResult{
		Name:       "pip",
		Status:     "ok",
		Saved:      &saved,
		SavedHuman: Humanize(saved),
		Detail:     detail,
	}
}
