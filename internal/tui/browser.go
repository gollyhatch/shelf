package tui

import (
	"net/url"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gollyhatch/shelf/internal/epub"
	"github.com/gollyhatch/shelf/internal/library"
)

// browserDoneMsg reports that xdg-open finished.
type browserDoneMsg struct {
	err error
}

// openInBrowser launches a URL with the system's default browser using the XDG
// utility.
func openInBrowser(target string) error {
	return exec.Command("xdg-open", target).Run()
}

// openSearchURL expands the configured search URL for the book under the cursor
// and opens it in the browser.
func (m model) openSearchURL() (tea.Model, tea.Cmd) {
	if strings.TrimSpace(m.searchURL) == "" {
		return m, nil
	}
	e, ok := m.selected()
	if !ok {
		return m, nil
	}
	target := expandSearchURL(m.searchURL, e)
	if target == "" {
		return m, nil
	}

	opener := m.openBrowser
	if opener == nil {
		opener = openInBrowser
	}
	m.setStatus("opening in browser…", statusInfo)
	return m, func() tea.Msg {
		return browserDoneMsg{err: opener(target)}
	}
}

// searchURLVerbs maps the single-letter placeholders shared with rename_format
// to metadata fields. Longer field names (%title, %series-index, ...) work too.
var searchURLVerbs = map[byte]epub.Field{
	't': epub.FieldTitle,
	'b': epub.FieldSubtitle,
	'a': epub.FieldAuthor,
	'g': epub.FieldTags,
	's': epub.FieldSeries,
	'e': epub.FieldSeriesIndex,
	'p': epub.FieldPublisher,
	'd': epub.FieldDate,
	'l': epub.FieldLanguage,
	'r': epub.FieldRights,
	'i': epub.FieldIdentifier,
}

// expandSearchURL replaces metadata placeholders in a search URL. Values are
// query-escaped so spaces and punctuation are safe in a URL. Unknown percent
// sequences and %% are left alone, so ordinary percent-encoded URLs work.
func expandSearchURL(pattern string, e library.Entry) string {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '%' {
			b.WriteByte(pattern[i])
			continue
		}
		if i+1 >= len(pattern) {
			b.WriteByte('%')
			break
		}
		if pattern[i+1] == '%' {
			b.WriteByte('%')
			i++
			continue
		}

		field, end, ok := searchURLVerbAt(pattern, i+1)
		if !ok {
			b.WriteByte('%')
			continue
		}
		b.WriteString(url.QueryEscape(e.Metadata.Get(field)))
		i = end - 1
	}
	return b.String()
}

// searchURLVerbAt resolves the placeholder starting immediately after a
// percent sign and returns its exclusive end.
func searchURLVerbAt(pattern string, start int) (epub.Field, int, bool) {
	j := start
	for j < len(pattern) && isSearchURLVerbChar(pattern[j]) {
		j++
	}
	if j == start {
		return "", start, false
	}
	if f, ok := lookupSearchURLVerb(pattern[start:j]); ok {
		return f, j, true
	}
	if pattern[j-1] == '-' {
		if f, ok := lookupSearchURLVerb(pattern[start : j-1]); ok {
			return f, j - 1, true
		}
	}
	return "", start, false
}

// lookupSearchURLVerb resolves a single-letter shorthand or a metadata field
// name.
func lookupSearchURLVerb(word string) (epub.Field, bool) {
	if len(word) == 1 {
		f, ok := searchURLVerbs[word[0]]
		return f, ok
	}
	return epub.ParseField(word)
}

func isSearchURLVerbChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '-'
}
