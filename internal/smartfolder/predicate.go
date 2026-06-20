package smartfolder

import "strings"

// categoryPredicate returns a Spotlight query that requires the 'filemaid' tag
// and matches the category keyword against user tags, file name, Finder comment,
// or text content. Comparisons are case/diacritic insensitive. Empty categories
// yield an empty string so callers can skip them.
func categoryPredicate(category string) string {
	category = strings.TrimSpace(category)
	if category == "" {
		return ""
	}

	keyword := escapeQuotes(category)

	var b strings.Builder
	b.WriteString(`(kMDItemUserTags == "filemaid"cd) && (`)
	b.WriteString(`kMDItemUserTags == "`)
	b.WriteString(keyword)
	b.WriteString(`"cd || `)
	b.WriteString(`kMDItemFSName LIKE "*`)
	b.WriteString(keyword)
	b.WriteString(`*"cd || `)
	b.WriteString(`kMDItemFinderComment LIKE "*`)
	b.WriteString(keyword)
	b.WriteString(`*"cd || `)
	b.WriteString(`kMDItemTextContent LIKE "*`)
	b.WriteString(keyword)
	b.WriteString(`*"cd`)
	b.WriteString(`)`)

	return b.String()
}

// tagPredicate returns a Spotlight query that requires both the 'filemaid' tag
// and the exact supplied tag. Comparisons are case/diacritic insensitive. Empty
// tags yield an empty string so callers can skip them.
func tagPredicate(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return ""
	}

	escaped := escapeQuotes(tag)

	var b strings.Builder
	b.WriteString(`(kMDItemUserTags == "filemaid"cd) && (kMDItemUserTags == "`)
	b.WriteString(escaped)
	b.WriteString(`"cd)`)

	return b.String()
}

// escapeQuotes escapes double quotes in Spotlight string literals by prefixing
// them with a backslash.
func escapeQuotes(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}
