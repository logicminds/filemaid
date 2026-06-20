package smartfolder

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Build creates or refreshes a directory of macOS Finder Smart Folders
// (.savedSearch files). It removes any existing .savedSearch files in dir,
// then writes one saved search per non-empty category and per non-empty tag
// (excluding the reserved "filemaid" tag).
//
// The first error encountered is returned, but Build attempts every write so
// that callers receive as complete a result as possible.
func Build(categories, tags, scopes []string, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create smart folder directory %q: %w", dir, err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read smart folder directory %q: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".savedSearch" {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return fmt.Errorf("remove stale saved search %q: %w", entry.Name(), err)
		}
	}

	var firstErr error
	categoryNames := make(map[string]struct{}, len(categories))

	for _, category := range categories {
		category = strings.TrimSpace(category)
		if category == "" {
			continue
		}

		query := categoryPredicate(category)
		if query == "" {
			continue
		}

		name := normalizeFileName(category)
		categoryNames[name] = struct{}{}
		path := filepath.Join(dir, name+".savedSearch")
		if err := writeSavedSearch(path, name, query, scopes); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || strings.EqualFold(tag, "filemaid") {
			continue
		}

		name := normalizeFileName(tag)
		if _, collides := categoryNames[name]; collides {
			continue
		}

		query := tagPredicate(tag)
		if query == "" {
			continue
		}

		path := filepath.Join(dir, name+".savedSearch")
		if err := writeSavedSearch(path, name, query, scopes); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// normalizeFileName sanitizes a name so it can be used as a file name.
// Forward slashes are replaced with hyphens and surrounding whitespace is
// trimmed.
func normalizeFileName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "/", "-")
	return name
}
