package smartfolder

import (
	"fmt"
	"os"
	"strings"
	"text/template"
)

const savedSearchTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CompatibleVersion</key>
	<integer>1</integer>
	<key>RawQuery</key>
	<string>{{.RawQuery}}</string>
	<key>RawQueryDict</key>
	<dict>
		<key>FinderFilesOnly</key>
		<true/>
		<key>UserFilesOnly</key>
		<true/>
		<key>SearchScopes</key>
		<array>
{{range .Scopes}}			<string>{{.}}</string>
{{end}}		</array>
	</dict>
	<key>SearchCriteria</key>
	<dict>
		<key>CurrentFolderPath</key>
		<array>
{{range .Scopes}}			<string>{{.}}</string>
{{end}}		</array>
		<key>FXScopeArrayOfPaths</key>
		<array>
{{range .Scopes}}			<string>{{.}}</string>
{{end}}		</array>
	</dict>
</dict>
</plist>
`

// writeSavedSearch writes a macOS Finder Smart Folder .savedSearch plist to path.
// The file contains the supplied display name, raw Spotlight query, and search
// scopes. No third-party plist libraries are used; output is generated with
// text/template.
func writeSavedSearch(path, name, rawQuery string, scopes []string) error {
	type tmplData struct {
		Name     string
		RawQuery string
		Scopes   []string
	}

	tmpl, err := template.New("savedSearch").Parse(savedSearchTemplate)
	if err != nil {
		return fmt.Errorf("parse saved search template: %w", err)
	}

	data := tmplData{
		Name:     name,
		RawQuery: xmlEscape(rawQuery),
		Scopes:   xmlEscapeSlice(scopes),
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create saved search %q: %w", path, err)
	}
	defer f.Close()

	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("write saved search %q: %w", path, err)
	}

	return f.Close()
}

// xmlEscape replaces XML special characters with their entities so that
// generated plists are well-formed even when queries or paths contain them.
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

func xmlEscapeSlice(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = xmlEscape(s)
	}
	return out
}
