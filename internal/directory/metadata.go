package directory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/logicminds/filemaid/internal/config"
)

// Metadata is a bounded snapshot of a single directory.
type Metadata struct {
	Path        string
	Base        string
	Size        int64
	ChildCount  int
	Mtime       time.Time
	Extensions  map[string]int
	Markers     []string
	IsAppBundle bool
	Truncated   string   // "entries", "bytes", or "entries, bytes" when capped
	ReadErrors  []string // individual child permission/stat failures
}

// Gather builds a bounded snapshot of dir. It walks only the immediate
// children. It returns an error only for fundamental failures; permission
// errors on individual children are recorded in Metadata.ReadErrors.
func Gather(dir string, cfg *config.Config) (*Metadata, error) {
	if cfg == nil {
		cfg = &config.Config{}
	}

	maxEntries := cfg.MaxDirSampleEntries
	if maxEntries <= 0 {
		maxEntries = 50
	}
	maxBytes := cfg.MaxDirSampleBytes
	if maxBytes <= 0 {
		maxBytes = 2048
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(absDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", absDir)
	}

	base := filepath.Base(absDir)
	meta := &Metadata{
		Path:        absDir,
		Base:        base,
		Mtime:       info.ModTime(),
		Extensions:  make(map[string]int),
		IsAppBundle: strings.HasSuffix(base, ".app"),
	}

	// .app bundles are opaque directory candidates; do not enumerate inside.
	if meta.IsAppBundle {
		return meta, nil
	}

	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, err
	}

	meta.ChildCount = len(entries)

	// Detect markers across all direct children before applying caps.
	markerSet := make(map[string]struct{}, len(cfg.ProjectMarkers))
	for _, m := range cfg.ProjectMarkers {
		markerSet[m] = struct{}{}
	}
	foundMarkers := make(map[string]struct{})
	for _, entry := range entries {
		if _, ok := markerSet[entry.Name()]; ok {
			foundMarkers[entry.Name()] = struct{}{}
		}
	}
	for m := range foundMarkers {
		meta.Markers = append(meta.Markers, m)
	}
	sort.Strings(meta.Markers)

	var sampledBytes int64
	var truncated bool
	var reasons []string

	for i, entry := range entries {
		if i >= maxEntries {
			truncated = true
			reasons = append(reasons, "entries")
			break
		}

		name := entry.Name()
		childInfo, err := entry.Info()
		if err != nil {
			meta.ReadErrors = append(meta.ReadErrors, fmt.Sprintf("%s: %v", name, err))
			continue
		}

		if childInfo.IsDir() {
			// Directories are counted in ChildCount but do not contribute to size;
			// we never recurse, and .app bundles are handled at the top level.
			continue
		}

		if sampledBytes+childInfo.Size() > int64(maxBytes) {
			truncated = true
			reasons = append(reasons, "bytes")
			break
		}
		sampledBytes += childInfo.Size()
		meta.Size += childInfo.Size()

		ext := strings.ToLower(filepath.Ext(name))
		meta.Extensions[ext]++
	}

	if truncated {
		meta.Truncated = strings.Join(reasons, ", ")
	}

	return meta, nil
}
