package directory

// Context carries the directory ancestry information for a file that was
// discovered while scanning with --include-dirs. It is passed to the file
// classifier so the prompt can include information about where the file lives.
type Context struct {
	// Ancestor is the directory path that contained this file during scanning.
	Ancestor string `json:"ancestor"`
	// Depth is the number of directory levels from the scan root to Ancestor.
	Depth int `json:"depth"`
	// Marker is the project marker detected in Ancestor, if any.
	Marker string `json:"marker,omitempty"`
}

// None reports whether no directory context is present. It is a convenience
// helper for callers that want to skip context-aware prompt augmentation.
func (c *Context) None() bool {
	return c == nil || c.Ancestor == ""
}
