package epub

import "strings"

// ParseCreator parses an author into a Creator, keeping only the display name.
// A parenthesised suffix (the legacy "Name (role; file-as)" form) is dropped so
// that role/file-as input degrades gracefully to a bare name.
func ParseCreator(s string) Creator {
	s = strings.TrimSpace(s)
	if s == "" {
		return Creator{}
	}
	if strings.HasSuffix(s, ")") {
		if i := strings.LastIndex(s, " ("); i >= 0 {
			s = strings.TrimSpace(s[:i])
		}
	}
	return Creator{Name: s}
}

// ParseCreatorList splits a ;-separated list of creators.
func ParseCreatorList(s string) []Creator {
	var out []Creator
	for _, part := range SplitList(s, ";") {
		if c := ParseCreator(part); c.Name != "" {
			out = append(out, c)
		}
	}
	return out
}

// SplitList splits a separator delimited list, trimming parts and dropping
// empties.
func SplitList(s string, sep string) []string {
	var out []string
	for _, part := range strings.Split(s, sep) {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Display renders a creator the way the UI shows it.
func (c Creator) Display() string { return c.Name }
