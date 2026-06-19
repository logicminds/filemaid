package cleaners

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/logicminds/filemaid/internal/config"
)

var cargoCleaner = Cleaner{
	Name:   "cargo",
	CanRun: cargoCanRun,
	Run:    cargoRun,
}

func cargoCanRun() bool {
	_, err := lookPath("cargo")
	if err != nil {
		return false
	}
	_, err = lookPath("cargo-cache")
	return err == nil
}

func cargoCacheDir() string {
	cargoHome := os.Getenv("CARGO_HOME")
	if cargoHome == "" {
		cargoHome = filepath.Join(home(), ".cargo")
	}
	return filepath.Join(cargoHome, "registry", "cache")
}

func cargoRun(dryRun bool, cfg *config.Config) CleanupResult {
	cacheDir := cargoCacheDir()
	before := dirSizeIfExists(cacheDir)

	if dryRun {
		saved := before
		return CleanupResult{
			Name:       "cargo",
			Status:     "dry-run",
			Saved:      &saved,
			SavedHuman: Humanize(before),
			Detail:     "would autoclean cache",
		}
	}

	out, err := runner.Run("cargo", "cache", "--autoclean")
	if err != nil {
		return CleanupResult{
			Name:   "cargo",
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
	detail := "cache autocleaned"
	if output != "" {
		detail = "cache autocleaned\n" + output
	}

	return CleanupResult{
		Name:       "cargo",
		Status:     "ok",
		Saved:      &saved,
		SavedHuman: Humanize(saved),
		Detail:     detail,
	}
}
