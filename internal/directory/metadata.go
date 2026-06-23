package directory

import (
	"errors"
	"fmt"
	"io"
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
	Truncated   string            // "entries", "bytes", or "entries, bytes" when capped
	ReadErrors  []string          // individual child permission/stat failures
	Snippets    map[string]string // representative text snippets keyed by filename
}

// textExtensions is the set of file extensions treated as text for snippet
// gathering. It is intentionally conservative to avoid reading binaries.
var textExtensions = map[string]bool{
	".txt": true, ".md": true, ".markdown": true,
	".go": true, ".js": true, ".ts": true, ".jsx": true, ".tsx": true,
	".json": true, ".yaml": true, ".yml": true, ".toml": true,
	".xml": true, ".html": true, ".htm": true, ".css": true,
	".py": true, ".rb": true, ".rs": true, ".java": true,
	".c": true, ".cpp": true, ".cc": true, ".h": true, ".hpp": true,
	".sh": true, ".bash": true, ".zsh": true,
	".log": true, ".csv": true, ".tex": true, ".bib": true,
	".rst": true, ".org": true,
}

func isTextFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return textExtensions[ext]
}

// readSnippet reads up to limit bytes from path and returns them as a string.
func readSnippet(path string, limit int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	buf := make([]byte, limit)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	return string(buf[:n]), nil
}

const (
	defaultSnippetCount = 10
	defaultSnippetBytes = 2048
)

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
		Snippets:    make(map[string]string),
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
	maxSnippetCount := defaultSnippetCount
	maxSnippetBytes := defaultSnippetBytes
	var snippetCount, snippetBytes int

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

		if isTextFile(name) && snippetCount < maxSnippetCount && snippetBytes < maxSnippetBytes {
			limit := maxSnippetBytes - snippetBytes
			if limit > 512 {
				limit = 512
			}
			if s, err := readSnippet(filepath.Join(absDir, name), limit); err == nil && s != "" {
				meta.Snippets[name] = s
				snippetCount++
				snippetBytes += len(s)
			}
		}
	}

	if truncated {
		meta.Truncated = strings.Join(reasons, ", ")
	}

	return meta, nil
}
