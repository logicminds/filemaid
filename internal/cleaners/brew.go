package cleaners

import (
	"path/filepath"
	"strings"

	"github.com/logicminds/filemaid/internal/config"
)

var brewCleaner = Cleaner{
	Name:   "brew",
	CanRun: brewCanRun,
	Run:    brewRun,
}

func brewCanRun() bool {
	_, err := lookPath("brew")
	return err == nil
}

func brewCacheDir() string {
	out, err := runner.Run("brew", "--cache")
	if err == nil {
		path := strings.TrimSpace(string(out))
		if path != "" {
			return expandPath(path)
		}
	}
	return filepath.Join(home(), "Library", "Caches", "Homebrew")
}

func brewRun(dryRun bool, cfg *config.Config) CleanupResult {
	mode := "safe"
	if c, ok := cfg.DevCleanup["brew"]; ok && c.Mode != "" {
		mode = c.Mode
	}

	prune := "7"
	if mode == "aggressive" {
		prune = "all"
	}
	command := "brew cleanup --prune=" + prune
	cacheDir := brewCacheDir()
	before := dirSizeIfExists(cacheDir)

	if dryRun {
		saved := before
		return CleanupResult{
			Name:       "brew",
			Status:     "dry-run",
			Saved:      &saved,
			SavedHuman: Humanize(before),
			Detail:     "would " + command,
			Command:    command,
		}
	}

	out, err := runner.Run("brew", "cleanup", "--prune="+prune)
	if err != nil {
		return CleanupResult{
			Name:    "brew",
			Status:  "failed",
			Detail:  err.Error(),
			Command: command,
		}
	}

	after := dirSizeIfExists(cacheDir)
	saved := before - after
	if saved < 0 {
		saved = 0
	}
	output := strings.TrimSpace(string(out))
	detail := command
	if output != "" {
		detail = command + "\n" + output
	}

	return CleanupResult{
		Name:       "brew",
		Status:     "ok",
		Saved:      &saved,
		SavedHuman: Humanize(saved),
		Detail:     detail,
		Command:    command,
	}
}
