package smartfolder

import "strings"

// criteriaValue represents a single value in a Finder criteria array. Finder
// expects attribute names as strings and operators as integers.
type criteriaValue struct {
	Int   int
	Str   string
	IsInt bool
}

// XML returns the plist fragment for this value: <integer> for operators and
// <string> for attribute names and display strings.
func (v criteriaValue) XML() string {
	if v.IsInt {
		return "<integer>" + itoa(v.Int) + "</integer>"
	}
	return "<string>" + xmlEscape(v.Str) + "</string>"
}

// criteriaSlice mirrors the FXCriteriaSlices entry Finder uses to populate the
// editable search-criteria UI. A slice with RowType 0 is a single criterion; a
// slice with RowType 1 is an ANY/ALL group and has Subrows.
type criteriaSlice struct {
	Criteria      []criteriaValue
	DisplayValues []string
	RowType       int
	Subrows       []criteriaSlice
}

const (
	rowTypeCriterion = 0
	rowTypeGroup     = 1
	// Finder operator codes used by the criteria array.
	opIs       = 100
	opContains = 104
)

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

// categoryCriteria returns the Finder-editable criteria slice list that
// corresponds to categoryPredicate. The first slice requires the 'filemaid' tag;
// the second is an ANY group matching the category against tags, name, Finder
// comments, or text content. Empty categories yield nil.
func categoryCriteria(category string) []criteriaSlice {
	category = strings.TrimSpace(category)
	if category == "" {
		return nil
	}

	return []criteriaSlice{
		filemaidTagSlice(),
		{
			DisplayValues: []string{"Any of the following are true"},
			RowType:       rowTypeGroup,
			Subrows: []criteriaSlice{
				tagIsSlice(category),
				nameContainsSlice(category),
				commentContainsSlice(category),
				textContainsSlice(category),
			},
		},
	}
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

// tagCriteria returns the Finder-editable criteria slice list that corresponds
// to tagPredicate: two ANDed rows requiring the 'filemaid' tag and the exact
// supplied tag. Empty tags yield nil.
func tagCriteria(tag string) []criteriaSlice {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil
	}

	return []criteriaSlice{
		filemaidTagSlice(),
		tagIsSlice(tag),
	}
}

func filemaidTagSlice() criteriaSlice {
	return criteriaSlice{
		Criteria:      []criteriaValue{{Str: "kMDItemUserTags"}, {Int: opIs, IsInt: true}, {Int: opIs, IsInt: true}},
		DisplayValues: []string{"Tags", "is", "filemaid"},
		RowType:       rowTypeCriterion,
	}
}

func tagIsSlice(tag string) criteriaSlice {
	return criteriaSlice{
		Criteria:      []criteriaValue{{Str: "kMDItemUserTags"}, {Int: opIs, IsInt: true}, {Int: opIs, IsInt: true}},
		DisplayValues: []string{"Tags", "is", tag},
		RowType:       rowTypeCriterion,
	}
}

func nameContainsSlice(keyword string) criteriaSlice {
	return criteriaSlice{
		Criteria:      []criteriaValue{{Str: "kMDItemFSName"}, {Int: opContains, IsInt: true}, {Int: opContains, IsInt: true}},
		DisplayValues: []string{"Name", "contains", keyword},
		RowType:       rowTypeCriterion,
	}
}

func commentContainsSlice(keyword string) criteriaSlice {
	return criteriaSlice{
		Criteria:      []criteriaValue{{Str: "kMDItemFinderComment"}, {Int: opContains, IsInt: true}, {Int: opContains, IsInt: true}},
		DisplayValues: []string{"Comments", "contains", keyword},
		RowType:       rowTypeCriterion,
	}
}

func textContainsSlice(keyword string) criteriaSlice {
	return criteriaSlice{
		Criteria:      []criteriaValue{{Str: "kMDItemTextContent"}, {Int: opContains, IsInt: true}, {Int: opContains, IsInt: true}},
		DisplayValues: []string{"Contents", "contains", keyword},
		RowType:       rowTypeCriterion,
	}
}

// itoa is a tiny int-to-string helper so predicate.go does not depend on
// strconv just for criteria rendering.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	var i = len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// escapeQuotes escapes double quotes in Spotlight string literals by prefixing
// them with a backslash.
func escapeQuotes(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}
