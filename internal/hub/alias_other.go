//go:build !darwin

package hub

import (
	"fmt"
	"os"
	"path/filepath"
)

// osAliasWriter falls back to symlinks on non-macOS builds so the package
// remains compilable and testable on other platforms.
type osAliasWriter struct{}

func (osAliasWriter) CreateAlias(dir, name, target string) error {
	aliasPath := filepath.Join(dir, name)
	if info, err := os.Lstat(aliasPath); err == nil {
		if info.IsDir() {
			return fmt.Errorf("%q already exists as a directory", aliasPath)
		}
		if err := os.Remove(aliasPath); err != nil {
			return fmt.Errorf("remove existing alias %q: %w", aliasPath, err)
		}
	}
	return os.Symlink(target, aliasPath)
}
