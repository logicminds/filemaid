// Package assets bundles embedded assets (config and Ollama Modelfiles)
// so the filemaid binary is self-contained and portable.
package assets

import (
	"embed"
	"fmt"
	"path"
)

//go:embed config.json
//go:embed modelfiles/*
var fs embed.FS

// ListModelfiles returns the names of all embedded Modelfiles
// (entries under modelfiles/ whose names start with "Modelfile.").
func ListModelfiles() ([]string, error) {
	entries, err := fs.ReadDir("modelfiles")
	if err != nil {
		return nil, fmt.Errorf("read embedded modelfiles: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) <= len("Modelfile.") || name[:len("Modelfile.")] != "Modelfile." {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

// ReadModelfile returns the contents of an embedded Modelfile.
func ReadModelfile(name string) ([]byte, error) {
	return fs.ReadFile(path.Join("modelfiles", name))
}

// ReadConfig returns the contents of the embedded default config.
func ReadConfig() ([]byte, error) {
	return fs.ReadFile("config.json")
}
