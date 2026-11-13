// Package check validates EPUB metadata and archive structure for the
// check/doctor command.
package check

import (
	"archive/zip"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gollyhatch/shelf/internal/epub"
	"github.com/gollyhatch/shelf/internal/library"
)

// Issue is one validation problem found in a book.
type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

var (
	languagePattern = regexp.MustCompile(`^[A-Za-z]{2,3}([_-][A-Za-z0-9]{2,8})*$`)
	dateLayouts     = []string{
		"2006",
		"2006-01",
		"2006-1",
		"2006-01-02",
		"2006-1-2",
		time.RFC3339,
	}
)

// Entries validates a set of loaded books and returns every issue found.
func Entries(entries []library.Entry) []Issue {
	var issues []Issue
	isbnOwners := make(map[string]string)
	for _, e := range entries {
		issues = append(issues, validateEntry(e)...)

		if isbn := normalizeISBN(e.Metadata.Identifier); isbn != "" {
			if _, ok := isbnOwners[isbn]; ok {
				issues = append(issues, Issue{Path: e.Path, Message: "duplicate ISBN " + isbn})
			} else {
				isbnOwners[isbn] = e.Path
			}
		}
	}
	return issues
}

func validateEntry(e library.Entry) []Issue {
	var issues []Issue
	add := func(format string, args ...any) {
		issues = append(issues, Issue{Path: e.Path, Message: fmt.Sprintf(format, args...)})
	}

	if e.Err != nil {
		add("unreadable metadata: %v", e.Err)
		return append(issues, validateArchive(e.Path)...)
	}

	m := e.Metadata
	if strings.TrimSpace(m.Title) == "" {
		add("missing title")
	}
	if strings.TrimSpace(m.AuthorString()) == "" {
		add("missing author")
	}

	switch language := strings.TrimSpace(m.Language); {
	case language == "":
		add("missing language")
	case !languagePattern.MatchString(language):
		add("invalid language %q", language)
	}

	if date := strings.TrimSpace(m.Date); date != "" && !validDate(date) {
		add("invalid date %q", date)
	}

	series := strings.TrimSpace(m.Series)
	index := strings.TrimSpace(m.SeriesIndex)
	switch {
	case series != "" && index == "":
		add("series %q has no episode number", series)
	case series == "" && index != "":
		add("episode number %q has no series", index)
	case index != "":
		if _, err := strconv.ParseFloat(index, 64); err != nil {
			add("invalid episode number %q", index)
		}
	}

	issues = append(issues, validateArchive(e.Path)...)
	return issues
}

func validDate(value string) bool {
	value = strings.TrimSpace(value)
	for _, layout := range dateLayouts {
		if _, err := time.Parse(layout, value); err == nil {
			return true
		}
	}
	return false
}

func normalizeISBN(value string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(value) {
		if (r >= '0' && r <= '9') || r == 'X' {
			b.WriteRune(r)
		}
	}
	isbn := b.String()
	if len(isbn) == 10 || len(isbn) == 13 {
		return isbn
	}
	return ""
}

func validateArchive(name string) []Issue {
	var issues []Issue
	add := func(format string, args ...any) {
		issues = append(issues, Issue{Path: name, Message: fmt.Sprintf(format, args...)})
	}

	zr, err := zip.OpenReader(name)
	if err != nil {
		add("unreadable archive: %v", err)
		return issues
	}
	defer zr.Close()

	names := make(map[string]bool)
	var mimetype *zip.File
	for i, f := range zr.File {
		clean := path.Clean(f.Name)
		if strings.Contains(f.Name, `\`) || !filepath.IsLocal(f.Name) ||
			clean == ".." || strings.HasPrefix(clean, "../") {
			add("unsafe archive path %q", f.Name)
		}
		if names[f.Name] {
			add("duplicate archive entry %q", f.Name)
		}
		names[f.Name] = true
		if f.Name == "mimetype" {
			mimetype = f
			if i != 0 {
				add("mimetype is not the first archive entry")
			}
			if f.Method != zip.Store {
				add("mimetype is compressed")
			}
		}
	}

	if mimetype == nil {
		add("missing mimetype entry")
	} else if data, err := readEntry(mimetype, 1024); err != nil {
		add("unreadable mimetype: %v", err)
	} else if strings.TrimSpace(string(data)) != epub.Mimetype {
		add("wrong mimetype %q", strings.TrimSpace(string(data)))
	}
	if !names["META-INF/container.xml"] {
		add("missing META-INF/container.xml")
	}

	hasPackage := false
	for entry := range names {
		if strings.EqualFold(path.Ext(entry), ".opf") {
			hasPackage = true
			break
		}
	}
	if !hasPackage {
		add("no package document")
	}
	return issues
}

func readEntry(f *zip.File, limit int64) ([]byte, error) {
	if f.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("entry is too large")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, limit+1))
}
