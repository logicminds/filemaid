//go:build !darwin

package hub

import "fmt"

// osSidebarAdder is a no-op on non-macOS builds because Finder sidebar
// integration requires the LSSharedFileList API.
type osSidebarAdder struct{}

func (osSidebarAdder) Add(path string) error {
	return fmt.Errorf("Finder sidebar integration is only available on macOS: %s", path)
}
