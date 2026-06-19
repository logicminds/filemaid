package cleaners

import (
	"path/filepath"
	"strings"

	"github.com/logicminds/filemaid/internal/config"
)

var npmCleaner = Cleaner{
	Name:   "npm",
	CanRun: npmCanRun,
	Run:    npmRun,
}

func npmCanRun() bool {
	_, err := lookPath("npm")
	return err == nil
}

func npmCacheDir() string {
	out, err := runner.Run("npm", "config", "get", "cache")
	if err == nil {
		path := strings.TrimSpace(string(out))
		if path != "" && path != "undefined" {
			return expandPath(path)
		}
	}
	return filepath.Join(home(), ".npm")
}

func npmRun(dryRun bool, cfg *config.Config) CleanupResult {
	cacheDir := npmCacheDir()
	contentDir := filepath.Join(cacheDir, "_cacache")
	before := dirSizeIfExists(contentDir)

	if dryRun {
		saved := before
		return CleanupResult{
			Name:       "npm",
			Status:     "dry-run",
			Saved:      &saved,
			SavedHuman: Humanize(before),
			Detail:     "would clean cache",
		}
	}

	out, err := runner.Run("npm", "cache", "clean", "--force")
	if err != nil {
		return CleanupResult{
			Name:   "npm",
			Status: "failed",
			Detail: err.Error(),
		}
	}

	after := dirSizeIfExists(contentDir)
	saved := before - after
	if saved < 0 {
		saved = 0
	}
	output := strings.TrimSpace(string(out))
	detail := "cache cleaned"
	if output != "" {
		detail = "cache cleaned\n" + output
	}

	return CleanupResult{
		Name:       "npm",
		Status:     "ok",
		Saved:      &saved,
		SavedHuman: Humanize(saved),
		Detail:     detail,
	}
}
