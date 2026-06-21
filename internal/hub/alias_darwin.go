//go:build darwin

package hub

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// osAliasWriter creates real Finder aliases via AppleScript.
type osAliasWriter struct{}

// CreateAlias writes a Finder alias named name inside dir that points to target.
// An existing item with the same name is replaced.
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

	script := fmt.Sprintf(`tell application "Finder"
	set targetFolder to POSIX file %q as alias
	set destFolder to POSIX file %q as alias
	make new alias file at destFolder to targetFolder with properties {name:%q}
end tell`, target, dir, name)

	if err := exec.Command("osascript", "-e", script).Run(); err != nil {
		return fmt.Errorf("osascript alias %q -> %q: %w", aliasPath, target, err)
	}
	return nil
}
