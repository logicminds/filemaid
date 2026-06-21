// Package hub builds and maintains the Filemaid Finder hub folder.
//
// The hub is a single directory (configured by smart_folders_dir) that contains
// dynamically generated Smart Folders, Finder aliases to the archive root and
// review queue, and is pinned to the Finder sidebar for quick access.
package hub

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/logicminds/filemaid/internal/smartfolder"
)

// Options configures a single hub build.
type Options struct {
	// HubDir is the directory that becomes the Filemaid hub. It is created if it
	// does not exist.
	HubDir string

	// ArchiveDir is the root archive directory. An alias named "Archive" is
	// created inside HubDir when this is non-empty and exists (or can be
	// created safely).
	ArchiveDir string

	// ReviewDir is the review queue directory. An alias named "Review" is
	// created inside HubDir when this is non-empty.
	ReviewDir string

	// Categories, Tags, and Scopes are passed through to smartfolder.Build.
	Categories []string
	Tags       []string
	Scopes     []string
}

// Builder builds a Filemaid hub. The zero value is not usable; use New.
type Builder struct {
	AliasWriter        func(dir, name, target string) error
	SidebarAdder       func(path string) error
	SmartFolderBuilder func(categories, tags, scopes []string, dir string) error
}

// New returns a Builder that uses macOS Finder integrations.
func New() *Builder {
	return &Builder{
		AliasWriter:        osAliasWriter{}.CreateAlias,
		SidebarAdder:       osSidebarAdder{}.Add,
		SmartFolderBuilder: smartfolder.Build,
	}
}

// Build creates or refreshes the Filemaid hub.
func (b *Builder) Build(opts Options) error {
	if opts.HubDir == "" {
		return fmt.Errorf("hub directory is required")
	}

	if err := os.MkdirAll(opts.HubDir, 0o755); err != nil {
		return fmt.Errorf("create hub directory %q: %w", opts.HubDir, err)
	}

	if err := b.SmartFolderBuilder(opts.Categories, opts.Tags, opts.Scopes, opts.HubDir); err != nil {
		return fmt.Errorf("build smart folders: %w", err)
	}

	if opts.ArchiveDir != "" {
		if err := os.MkdirAll(opts.ArchiveDir, 0o755); err != nil {
			return fmt.Errorf("create archive directory %q: %w", opts.ArchiveDir, err)
		}
		if err := b.AliasWriter(opts.HubDir, "Archive", opts.ArchiveDir); err != nil {
			return fmt.Errorf("create archive alias: %w", err)
		}
	}

	if opts.ReviewDir != "" {
		if err := os.MkdirAll(opts.ReviewDir, 0o755); err != nil {
			return fmt.Errorf("create review directory %q: %w", opts.ReviewDir, err)
		}
		if err := b.AliasWriter(opts.HubDir, "Review", opts.ReviewDir); err != nil {
			return fmt.Errorf("create review alias: %w", err)
		}
	}

	if err := b.SidebarAdder(opts.HubDir); err != nil {
		return fmt.Errorf("add hub to Finder sidebar: %w", err)
	}

	return nil
}

// ArchiveRoot returns the longest common ancestor of all configured category
// destinations, excluding the reserved "Unknown" category. If categories are
// empty or have no common ancestor, it returns an empty string.
func ArchiveRoot(categories map[string]string) string {
	var paths []string
	for name, p := range categories {
		if strings.EqualFold(name, "Unknown") {
			continue
		}
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		paths = append(paths, p)
	}
	if len(paths) == 0 {
		return ""
	}
	sort.Strings(paths)
	return commonAncestor(paths)
}

func commonAncestor(paths []string) string {
	if len(paths) == 0 {
		return ""
	}

	sep := string(filepath.Separator)
	split := func(p string) []string {
		clean := filepath.Clean(p)
		if clean == sep {
			return []string{""}
		}
		if strings.HasPrefix(clean, sep) {
			return append([]string{""}, strings.Split(strings.TrimPrefix(clean, sep), sep)...)
		}
		return strings.Split(clean, sep)
	}

	parts := split(paths[0])
	for _, p := range paths[1:] {
		cur := split(p)
		n := len(parts)
		if len(cur) < n {
			n = len(cur)
		}
		i := 0
		for i < n && parts[i] == cur[i] {
			i++
		}
		parts = parts[:i]
	}

	if len(parts) == 0 {
		return ""
	}
	if parts[0] == "" {
		return sep + strings.Join(parts[1:], sep)
	}
	return strings.Join(parts, sep)
}
