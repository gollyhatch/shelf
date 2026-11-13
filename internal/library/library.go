// Package library discovers EPUB files on disk and loads their metadata.
package library

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gollyhatch/shelf/internal/epub"
)

// Entry is one EPUB file and its metadata.
type Entry struct {
	Path     string
	Metadata epub.Metadata
	// Err is set when the metadata could not be read; the file is still listed
	// so that the user can see what is wrong with it.
	Err error
}

// Discover returns the EPUB files in a directory (and, when recursive is set,
// its subdirectories), sorted by path. The mimetype-independent .epub suffix is
// the only filter applied, mirroring how readers index a shelf.
func Discover(dir string, recursive bool) ([]string, error) {
	var out []string
	if recursive {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if name := d.Name(); path != dir && strings.HasPrefix(name, ".") {
					return fs.SkipDir
				}
				return nil
			}
			if isEPUB(path) {
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		items, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if it.IsDir() {
				continue
			}
			p := filepath.Join(dir, it.Name())
			if isEPUB(p) {
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func isEPUB(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".epub")
}

// Expand turns a mix of files and directories into a list of EPUB paths.
func Expand(args []string, recursive bool) ([]string, error) {
	var out []string
	for _, arg := range args {
		st, err := os.Stat(arg)
		if err != nil {
			return nil, err
		}
		if st.IsDir() {
			paths, err := Discover(arg, recursive)
			if err != nil {
				return nil, err
			}
			out = append(out, paths...)
			continue
		}
		out = append(out, arg)
	}
	return out, nil
}

// Load reads the metadata of every path.
func Load(paths []string) []Entry {
	entries := make([]Entry, 0, len(paths))
	for _, p := range paths {
		entries = append(entries, LoadOne(p))
	}
	return entries
}

// LoadOne reads the metadata of a single EPUB. Failures are reported on the
// entry rather than aborting a whole batch.
func LoadOne(path string) Entry {
	e := Entry{Path: path}
	meta, err := epub.ReadMetadata(path)
	if err != nil {
		e.Err = err
		return e
	}
	e.Metadata = meta
	return e
}

// Title returns a display title, falling back to the file name.
func (e Entry) Title() string {
	if t := strings.TrimSpace(e.Metadata.Title); t != "" {
		return t
	}
	return strings.TrimSuffix(filepath.Base(e.Path), filepath.Ext(e.Path))
}

// Author returns a short author string for list rendering.
func (e Entry) Author() string {
	return e.Metadata.AuthorString()
}

// SeriesLine renders the series name and episode number for the list view,
// with the subtitle in front when the book has one. It returns an empty string
// when the book has neither series nor episode information.
func (e Entry) SeriesLine() string {
	series := strings.TrimSpace(e.Metadata.Series)
	index := strings.TrimSpace(e.Metadata.SeriesIndex)
	subtitle := strings.TrimSpace(e.Metadata.Subtitle)

	var detail string
	switch {
	case series != "" && index != "":
		detail = series + " #" + index
	case series != "":
		detail = series
	case index != "":
		detail = "Episode " + index
	default:
		return ""
	}
	if subtitle != "" {
		return subtitle + " (" + detail + ")"
	}
	return "(" + detail + ")"
}
