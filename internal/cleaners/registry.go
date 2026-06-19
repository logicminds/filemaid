package cleaners

import "github.com/logicminds/filemaid/internal/config"

// Cleaner represents a single dev-artifact cleaner.
type Cleaner struct {
	Name   string
	CanRun func() bool
	Run    func(dryRun bool, cfg *config.Config) CleanupResult
}

// Registry returns all registered cleaners in the canonical order.
func Registry() []Cleaner {
	return []Cleaner{
		dockerCleaner,
		npmCleaner,
		cargoCleaner,
		pipCleaner,
		brewCleaner,
		xcodeCleaner,
		reviewCleaner,
	}
}
