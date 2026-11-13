package tui

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"

	"github.com/gollyhatch/shelf/internal/epub"
	"github.com/gollyhatch/shelf/internal/library"
)

const testOPF = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:identifier id="id">urn:isbn:1</dc:identifier>
    <dc:title>The Left Hand of Darkness</dc:title>
    <dc:creator opf:role="aut" opf:file-as="Le Guin, Ursula K.">Le Guin, Ursula K.</dc:creator>
    <dc:language>en</dc:language>
    <dc:subject>Science Fiction</dc:subject>
    <dc:subject>Winter</dc:subject>
    <dc:publisher>Ace Books</dc:publisher>
    <dc:date>1969-03-01</dc:date>
    <dc:description>A novel about a planet called Winter.</dc:description>
  </metadata>
</package>`

// makeEPUB writes a test book into its own temp directory.
func makeEPUB(t *testing.T) string {
	t.Helper()
	return makeEPUBIn(t, t.TempDir(), "book.epub")
}

// makeEPUBIn writes a test book with the given name into dir, so a test can
// build a small library of real files.
func makeEPUBIn(t *testing.T, dir, name string) string {
	t.Helper()
	return makeEPUBWithOPF(t, dir, name, testOPF)
}

// makeEPUBWithOPF writes a test EPUB using the supplied package document.
func makeEPUBWithOPF(t *testing.T, dir, name, opf string) string {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	mw, _ := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	mw.Write([]byte(epub.Mimetype))
	cw, _ := zw.Create("META-INF/container.xml")
	cw.Write([]byte(
		`<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles>` +
			`<rootfile full-path="content.opf" media-type="application/oebps-package+xml"/>` +
			`</rootfiles></container>`))
	ow, _ := zw.Create("content.opf")
	ow.Write([]byte(opf))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// libraryOf loads real entries from dir, the way the TUI would at startup.
func libraryOf(t *testing.T, dir string, paths ...string) []library.Entry {
	t.Helper()
	entries := make([]library.Entry, 0, len(paths))
	for _, p := range paths {
		e := library.LoadOne(p)
		if e.Err != nil {
			t.Fatal(e.Err)
		}
		entries = append(entries, e)
	}
	return entries
}

func sized(t *testing.T, path string, w, h int) model {
	t.Helper()
	e := library.LoadOne(path)
	if e.Err != nil {
		t.Fatal(e.Err)
	}
	m := newModel(filepath.Dir(path), []library.Entry{e})
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(model)
}

// TestViewFitsTerminal is the single most valuable TUI test: a line that is
// wider or taller than the terminal wraps and destroys the layout.
func TestViewFitsTerminal(t *testing.T) {
	path := makeEPUB(t)
	modes := map[string]func(model) model{
		"browse-list": func(m model) model {
			m.pane = paneList
			return m
		},
		"browse-detail": func(m model) model {
			m.pane = paneDetail
			m.rowIdx = 3
			return m
		},
		"edit-scalar": func(m model) model {
			m.pane = paneDetail
			m.rowIdx = 0
			m.mode = modeEdit
			m.editRow = m.rows[0]
			m.usingArea = false
			return m
		},
		"edit-description": func(m model) model {
			m.pane = paneDetail
			m.mode = modeEdit
			m.editRow = row{kind: rowField, field: epub.FieldDescription, label: "Description"}
			m.usingArea = true
			return m
		},
		"status": func(m model) model {
			m.setStatus("saved", statusOK)
			return m
		},
		"loading": func(m model) model {
			m.loading = true
			return m
		},
		"search": func(m model) model {
			m.searching = true
			m.search.Focus()
			m.search.SetValue("dark")
			m.setFilter("dark")
			return m
		},
		"search-empty": func(m model) model {
			m.setFilter("no-such-book")
			return m
		},
		"read-filter": func(m model) model {
			m.readFilter = readFilterUnread
			m.applyFilter()
			m.pane = paneList
			return m
		},
		"selected-list": func(m model) model {
			m.pane = paneList
			m.sel = map[int]bool{0: true}
			m.rebuildRows()
			return m
		},
		"bulk": func(m model) model {
			m.pane = paneDetail
			m.entries = append(m.entries, fakeEntry("/b/second.epub", "Second Book", "Somebody", ""))
			m.applyFilter()
			m.sel = map[int]bool{0: true, 1: true}
			m.rebuildRows()
			return m
		},
		"bulk-edit": func(m model) model {
			m.pane = paneDetail
			m.entries = append(m.entries, fakeEntry("/b/second.epub", "Second Book", "Somebody", ""))
			m.applyFilter()
			m.sel = map[int]bool{0: true, 1: true}
			m.rebuildRows()
			m.mode = modeEdit
			m.editBulk = true
			m.editRow = m.rows[0]
			m.usingArea = false
			return m
		},
		"confirm-delete": func(m model) model {
			m.confirmDelete = true
			m.deletePaths = []string{
				"/b/a-very-long-book-name-that-needs-truncating.epub",
				"/b/two.epub",
				"/b/three.epub",
			}
			return m
		},
		"help": func(m model) model {
			m.helpOpen = true
			return m
		},
		"help-scrolled": func(m model) model {
			m.helpOpen = true
			m.helpTop = len(helpEntries)
			return m
		},
		"sort": func(m model) model {
			m.sortOpen = true
			return m
		},
		"sort-end": func(m model) model {
			m.sortOpen = true
			m.sortIdx = len(sortModes) - 1
			return m
		},
		"long-description": func(m model) model {
			if len(m.entries) > 0 {
				m.entries[0].Metadata.Description = strings.Repeat(
					"A long description that should wrap across several lines instead of being truncated. ", 6)
			}
			m.rebuildRows()
			m.pane = paneDetail
			m.rowIdx = len(m.rows) - 1
			return m
		},
		"unreadable": func(m model) model {
			m.entries = []library.Entry{fakeEntry("/b/broken.epub", "Broken", "", "")}
			m.entries[0].Err = errors.New("xml: unexpected EOF")
			m.applyFilter()
			m.rebuildRows()
			m.pane = paneDetail
			m.rowIdx = 0
			return m
		},
		"series-list": func(m model) model {
			m.entries = append(m.entries,
				fakeEntry("/b/second.epub", "Second Book", "Somebody", "A Very Long Series Name"))
			m.entries[1].Metadata.SeriesIndex = "10"
			m.applyFilter()
			m.rebuildRows()
			m.pane = paneList
			return m
		},
		"read-list": func(m model) model {
			if len(m.entries) > 0 {
				m.entries[0].Metadata.Read = true
			}
			m.pane = paneList
			return m
		},
		"online": func(m model) model {
			m.onlineOpen = true
			m.onlineFound = epub.Metadata{Title: "Found Title"}
			m.onlineFields = []onlineField{
				{field: epub.FieldTitle, current: "Current Title", found: "Found Title", selected: true},
				{field: epub.FieldDescription,
					current:  strings.Repeat("A long current description. ", 8),
					found:    strings.Repeat("A long online description. ", 8),
					selected: false},
			}
			m.onlineIdx = 1
			return m
		},
		"online-scrolled": func(m model) model {
			m.onlineOpen = true
			m.onlineFound = epub.Metadata{Title: "Found Title"}
			for _, f := range onlineFieldsOrder {
				m.onlineFields = append(m.onlineFields, onlineField{
					field: f, current: "Current value", found: "Found value", selected: true,
				})
			}
			m.onlineIdx = len(m.onlineFields) - 1
			return m
		},
	}
	for _, size := range []struct{ w, h int }{
		{80, 24}, {100, 40}, {120, 30}, {84, 24}, {60, 20}, {40, 15}, {200, 50}, {30, 10},
	} {
		m := sized(t, path, size.w, size.h)
		for name, mutate := range modes {
			v := mutate(m).View()
			lines := strings.Split(v, "\n")
			if len(lines) > size.h {
				t.Errorf("%dx%d %s: view is %d lines tall (> %d):\n%s",
					size.w, size.h, name, len(lines), size.h, v)
			}
			for i, line := range lines {
				if got := runewidth.StringWidth(line); got > size.w {
					t.Errorf("%dx%d %s: line %d is %d columns wide (> %d):\n%q",
						size.w, size.h, name, i, got, size.w, line)
				}
			}
		}
	}
}

func TestViewShowsMetadata(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)
	view := m.View()
	for _, want := range []string{
		"shelf", "1 book", "book.epub", "The Left Hand of Darkness", "Le Guin, Ursula K.",
		"Title:", "Author:", "Tags:", "Science Fiction",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view is missing %q:\n%s", want, view)
		}
	}
}

// TestLoadingStreamsIntoList checks that books appear in the list as they are
// read, and that the progress is only repeated in the header.
func TestLoadingStreamsIntoList(t *testing.T) {
	m := newModel("/b", nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = next.(model)

	batch := loadBatchMsg{
		paths:   []string{"/b/a.epub", "/b/b.epub"},
		entries: []library.Entry{fakeEntry("/b/a.epub", "Alpha", "Ann", "")},
	}
	m, cmd := step(t, m, batch)
	if cmd == nil {
		t.Fatal("loading did not schedule the next file")
	}
	if len(m.entries) != 1 || len(m.filtered) != 1 {
		t.Fatalf("entries = %d, filtered = %d", len(m.entries), len(m.filtered))
	}
	view := m.View()
	if !strings.Contains(view, "Alpha") {
		t.Errorf("loaded book is not listed yet:\n%s", view)
	}
	lines := strings.Split(view, "\n")
	if !strings.Contains(lines[0], "loading 1/2") {
		t.Errorf("header lacks progress:\n%s", view)
	}
	for i, line := range lines[1:] {
		if strings.Contains(line, "loading") {
			t.Errorf("progress repeated in body line %d: %q", i+1, line)
		}
	}

	// The last book completes the load.
	m, cmd = step(t, m, loadBatchMsg{
		paths:   batch.paths,
		entries: append(batch.entries, fakeEntry("/b/b.epub", "Beta", "Bob", "")),
	})
	if cmd != nil {
		t.Error("loading scheduled another file after the batch was done")
	}
	if m.loading || len(m.entries) != 2 {
		t.Errorf("loading = %v, entries = %d", m.loading, len(m.entries))
	}
	if strings.Contains(m.View(), "loading") {
		t.Errorf("progress remains after loading:\n%s", m.View())
	}
}

// TestLoadingKeepsEdits checks that a book edited while the scan is still
// running is not replaced by the loader's older copy.
func TestLoadingKeepsEdits(t *testing.T) {
	m := newModel("/b", nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = next.(model)

	paths := []string{"/b/a.epub", "/b/b.epub"}
	first := fakeEntry("/b/a.epub", "Alpha", "Ann", "")
	m, _ = step(t, m, loadBatchMsg{paths: paths, entries: []library.Entry{first}})
	m.entries[0].Metadata.Title = "Alpha Edited"

	m, _ = step(t, m, loadBatchMsg{paths: paths, entries: []library.Entry{
		first, fakeEntry("/b/b.epub", "Beta", "Bob", ""),
	}})
	if got := m.entries[0].Metadata.Title; got != "Alpha Edited" {
		t.Errorf("edit during loading was overwritten: %q", got)
	}
	if len(m.entries) != 2 {
		t.Errorf("entries = %d, want 2", len(m.entries))
	}
}

// TestRescanReplacesList checks that a rescan builds a fresh list instead of
// appending the new results to the old ones.
func TestRescanReplacesList(t *testing.T) {
	m := newModel("/b", []library.Entry{
		fakeEntry("/b/a.epub", "Alpha", "Ann", ""),
		fakeEntry("/b/b.epub", "Beta", "Bob", ""),
	})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = next.(model)

	m = key(t, m, "R")
	if !m.loading {
		t.Fatal("R did not start a scan")
	}
	paths := []string{"/b/c.epub"}
	m, _ = step(t, m, loadBatchMsg{paths: paths})
	m, _ = step(t, m, loadBatchMsg{paths: paths, entries: []library.Entry{
		fakeEntry("/b/c.epub", "Gamma", "Gil", ""),
	}})
	if len(m.entries) != 1 || m.entries[0].Title() != "Gamma" {
		t.Errorf("entries after rescan = %v", m.entries)
	}
}

// TestNoMetadataHeader checks that the detail pane has no fallback title.
func TestNoMetadataHeader(t *testing.T) {
	m := newModel("/b", nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = next.(model)
	if strings.Contains(m.View(), "Metadata") {
		t.Errorf("detail pane still has a Metadata header:\n%s", m.View())
	}
}

// TestSelectedAuthorIsHighlighted checks that the author line uses its own
// secondary highlight colour, distinct from the title, and that authors are
// fainter than titles when browsing.
func TestSelectedAuthorIsHighlighted(t *testing.T) {
	cases := []struct {
		name     string
		selected bool
		readable bool
		want     lipgloss.TerminalColor
	}{
		{"selected", true, true, colSelAuthor},
		{"browsing", false, true, colFaint},
		{"unreadable", true, false, colFaint},
	}
	for _, c := range cases {
		if got := entrySubStyle(c.selected, c.readable).GetForeground(); got != c.want {
			t.Errorf("%s: author colour = %v, want %v", c.name, got, c.want)
		}
	}
	if itemSelAuthorStyle.GetForeground() == itemSelStyle.GetForeground() {
		t.Error("selected author uses the same colour as the selected title")
	}
}

// TestSelectedListCursorBar checks that the cursor bar prefixes both the title
// and author lines of the selected book, and neither line otherwise.
func TestListShowsSeriesLine(t *testing.T) {
	entries := []library.Entry{
		fakeEntry("/b/a.epub", "Alpha", "Ann", "Saga"),
		fakeEntry("/b/b.epub", "Beta", "Bob", ""),
	}
	entries[0].Metadata.SeriesIndex = "2"
	m := newModel("/b", entries)
	m.rebuildRows()

	lines := m.listLines(80, 10)
	if len(lines) != 5 {
		t.Fatalf("lines = %d, want 5: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "Alpha") || !strings.Contains(lines[1], "(Saga #2)") || !strings.Contains(lines[2], "Ann") {
		t.Errorf("series entry lines are wrong: %q", lines[:3])
	}
	for i := 0; i < 3; i++ {
		if !strings.HasPrefix(lines[i], "▌ ") {
			t.Errorf("selected line %d has no cursor bar: %q", i, lines[i])
		}
	}
	if !strings.Contains(lines[3], "Beta") || !strings.Contains(lines[4], "Bob") {
		t.Errorf("entry without a series should have two lines: %q", lines[3:])
	}
}

func TestSelectedListCursorBar(t *testing.T) {
	m := newModel("/b", []library.Entry{
		fakeEntry("/b/a.epub", "Alpha", "Ann", ""),
		fakeEntry("/b/b.epub", "Beta", "Bob", ""),
	})
	m.rebuildRows()
	lines := m.listLines(80, 10)
	if len(lines) != 4 {
		t.Fatalf("lines = %d, want 4: %q", len(lines), lines)
	}
	for i := 0; i < 2; i++ {
		if !strings.HasPrefix(lines[i], "▌ ") {
			t.Errorf("selected line %d has no cursor bar: %q", i, lines[i])
		}
	}
	for i := 2; i < 4; i++ {
		if strings.HasPrefix(lines[i], "▌") {
			t.Errorf("unselected line %d has a cursor bar: %q", i, lines[i])
		}
	}

	// In bulk mode the author stays aligned with the title text.
	m.sel[0] = true
	m.rebuildRows()
	lines = m.listLines(80, 10)
	if !strings.HasPrefix(lines[0], "▌ ") || !strings.HasPrefix(lines[1], "▌ ") {
		t.Errorf("cursor bar missing with checkboxes: %q %q", lines[0], lines[1])
	}
	if !strings.HasPrefix(lines[0], "▌ ✓ Alpha") {
		t.Errorf("checked title is misaligned: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "▌   Ann") {
		t.Errorf("author is misaligned in bulk mode: %q", lines[1])
	}
}

// TestCursorBarUsesAccent checks that both selected lines start with the same
// accent-green cursor bar, even though the author text has its own tint.
func TestCursorBarUsesAccent(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(old)

	m := newModel("/b", []library.Entry{fakeEntry("/b/a.epub", "Alpha", "Ann", "")})
	m.rebuildRows()
	lines := m.listLines(80, 10)
	bar := itemSelBarStyle.Render("▌ ")
	if bar == itemSelAuthorStyle.Render("▌ ") {
		t.Fatal("the bar and the author tint are indistinguishable")
	}
	for i := 0; i < 2; i++ {
		if !strings.HasPrefix(lines[i], bar) {
			t.Errorf("line %d does not start with the accent bar: %q", i, lines[i])
		}
	}
}

func TestNavigation(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)

	if m.pane != paneList {
		t.Fatalf("expected to start in the list pane")
	}
	m = key(t, m, "tab")
	if m.pane != paneDetail {
		t.Fatalf("tab did not focus the detail pane")
	}
	start := m.rowIdx
	m = key(t, m, "j")
	if m.rowIdx != start+1 {
		t.Errorf("j moved to %d, want %d", m.rowIdx, start+1)
	}
	m = key(t, m, "k")
	if m.rowIdx != start {
		t.Errorf("k moved to %d, want %d", m.rowIdx, start)
	}
	m = key(t, m, "esc")
	if m.pane != paneList {
		t.Errorf("esc did not return to the list pane")
	}
}

// TestAddAuthorEndToEnd drives the add-author flow and checks the file on disk.
func TestGroupNavigation(t *testing.T) {
	entries := []library.Entry{
		fakeEntry("/b/a1.epub", "A1", "Author A", "Saga"),
		fakeEntry("/b/a2.epub", "A2", "Author A", "Saga"),
		fakeEntry("/b/a3.epub", "A3", "Author A", "Other"),
		fakeEntry("/b/b1.epub", "B1", "Author B", "Other"),
	}
	m := newModel("/b", entries)
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m = next.(model)

	if m.listIdx != 0 {
		t.Fatalf("start index = %d, want 0", m.listIdx)
	}
	m = key(t, m, "J")
	if m.listIdx != 2 {
		t.Errorf("J from the first group went to %d, want 2", m.listIdx)
	}
	m = key(t, m, "J")
	if m.listIdx != 3 {
		t.Errorf("J from a new author went to %d, want 3", m.listIdx)
	}
	m = key(t, m, "J")
	if m.listIdx != 3 {
		t.Errorf("J past the last group moved to %d, want 3", m.listIdx)
	}
	m = key(t, m, "K")
	if m.listIdx != 2 {
		t.Errorf("K went to %d, want 2", m.listIdx)
	}
	m = key(t, m, "K")
	if m.listIdx != 1 {
		t.Errorf("K across a series change went to %d, want 1", m.listIdx)
	}
	m = key(t, m, "K")
	if m.listIdx != 1 {
		t.Errorf("K within the same group moved to %d, want 1", m.listIdx)
	}
}

// TestGroupNavigationByAuthor checks that books without series still group by
// author.
func TestGroupNavigationByAuthor(t *testing.T) {
	entries := []library.Entry{
		fakeEntry("/b/a1.epub", "A1", "Author A", ""),
		fakeEntry("/b/a2.epub", "A2", "Author A", ""),
		fakeEntry("/b/b1.epub", "B1", "Author B", ""),
	}
	m := newModel("/b", entries)
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m = next.(model)

	m = key(t, m, "J")
	if m.listIdx != 2 {
		t.Errorf("J by author went to %d, want 2", m.listIdx)
	}
}

func TestAddAuthorEndToEnd(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)

	m = key(t, m, "tab")
	// Move to the "add author" row.
	for i := 0; i < len(m.rows); i++ {
		if m.rows[m.rowIdx].kind == rowAddCreator {
			break
		}
		m = key(t, m, "j")
	}
	if m.rows[m.rowIdx].kind != rowAddCreator {
		t.Fatalf("could not reach the add-author row: %+v", m.rows[m.rowIdx])
	}

	m = key(t, m, "a")
	if m.mode != modeEdit {
		t.Fatalf("expected edit mode, got %v", m.mode)
	}
	m = typeText(m, "Ursula K. Le Guin (aut)")

	m, cmd := step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeBrowse {
		t.Fatalf("enter did not leave edit mode")
	}
	if cmd == nil {
		t.Fatal("enter did not schedule a save")
	}

	msg, ok := cmd().(savedMsg)
	if !ok || msg.err != nil {
		t.Fatalf("save reported %+v", msg)
	}

	meta, err := epub.ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Creators) != 2 {
		t.Fatalf("creators = %+v", meta.Creators)
	}
	if meta.Creators[1].Name != "Ursula K. Le Guin" {
		t.Errorf("new creator = %+v", meta.Creators[1])
	}
	if meta.Creators[0].Name != "Le Guin, Ursula K." {
		t.Errorf("existing creator was damaged: %+v", meta.Creators[0])
	}
	// The subjects and description must be untouched.
	if len(meta.Subjects) != 2 || meta.Description == "" {
		t.Errorf("unrelated metadata was lost: %+v", meta)
	}
}

func TestDeleteAuthor(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)
	m = key(t, m, "tab")
	for i := 0; i < len(m.rows); i++ {
		if m.rows[m.rowIdx].kind == rowCreator {
			break
		}
		m = key(t, m, "j")
	}
	if m.rows[m.rowIdx].kind != rowCreator {
		t.Fatalf("could not reach the creator row")
	}
	m, cmd := step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if cmd == nil {
		t.Fatal("delete did not schedule a save")
	}
	if msg := cmd().(savedMsg); msg.err != nil {
		t.Fatal(msg.err)
	}
	meta, err := epub.ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Creators) != 0 {
		t.Errorf("creators = %+v", meta.Creators)
	}
}

func TestEditField(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)
	m = key(t, m, "tab")
	// Row 0 is the title.
	if m.rows[0].field != epub.FieldTitle {
		t.Fatalf("first row is %+v, want the title", m.rows[0])
	}
	m = key(t, m, "e")
	if m.mode != modeEdit {
		t.Fatalf("e did not open the editor")
	}
	if m.input.Value() != "The Left Hand of Darkness" {
		t.Errorf("editor was prefilled with %q", m.input.Value())
	}
	m = typeText(m, " Renamed")
	m, cmd := step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter did not schedule a save")
	}
	if msg := cmd().(savedMsg); msg.err != nil {
		t.Fatal(msg.err)
	}
	meta, _ := epub.ReadMetadata(path)
	if meta.Title != "The Left Hand of Darkness Renamed" {
		t.Errorf("title = %q", meta.Title)
	}
}

// TestEditCancelLeavesFileAlone checks that escaping the editor writes nothing.
func TestEditCancelLeavesFileAlone(t *testing.T) {
	path := makeEPUB(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := sized(t, path, 110, 34)
	m = key(t, m, "tab")
	m = key(t, m, "e")
	m = typeText(m, "garbage")
	m = key(t, m, "esc")
	if m.mode != modeBrowse {
		t.Fatalf("esc did not close the editor")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("cancelling an edit modified the file")
	}
}

func TestStatusMessage(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)
	m.setStatus("saved", statusOK)
	if !strings.Contains(m.View(), "saved") {
		t.Errorf("status is not rendered:\n%s", m.View())
	}
	// The next key press clears it.
	m = key(t, m, "j")
	if strings.Contains(m.View(), "saved") {
		t.Error("status survived a key press")
	}
}

// fakeEntry builds an in-memory entry, for tests that do not need a real file.
func fakeEntry(path, title, author, series string) library.Entry {
	return library.Entry{
		Path: path,
		Metadata: epub.Metadata{
			Title:    title,
			Creators: []epub.Creator{{Name: author}},
			Series:   series,
		},
	}
}

// TestToggleRead checks that x marks the selected book read and toggles it back
// to unread.
func TestToggleRead(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)

	m, cmd := step(t, m, keyMsg("x"))
	if cmd == nil {
		t.Fatal("x did not schedule a save")
	}
	m = drain(t, m, cmd)
	if !m.entries[0].Metadata.Read {
		t.Error("entry is not read after toggling")
	}
	meta, err := epub.ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if !meta.Read {
		t.Error("file is not read after toggling")
	}

	m, cmd = step(t, m, keyMsg("x"))
	if cmd == nil {
		t.Fatal("second x did not schedule a save")
	}
	m = drain(t, m, cmd)
	if m.entries[0].Metadata.Read {
		t.Error("entry is still read after toggling back")
	}
	meta, err = epub.ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Read {
		t.Error("file is still read after toggling back")
	}
}

// TestReadIndicatorInList checks that a read book has a check mark in the book
// list and an unread book does not.
func TestReadIndicatorInList(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)

	if strings.Contains(m.View(), "✓") {
		t.Errorf("unread book shows a read indicator:\n%s", m.View())
	}

	m.entries[0].Metadata.Read = true
	m.rebuildRows()
	if view := m.View(); !strings.Contains(view, "✓") {
		t.Errorf("read book has no indicator in the list:\n%s", view)
	}

	m.entries[0].Metadata.Read = false
	m.rebuildRows()
	if view := m.View(); strings.Contains(view, "✓") {
		t.Errorf("unread book still shows an indicator:\n%s", view)
	}
}

// TestReadCheckbox checks that the read field is rendered and toggled as a
// checkbox rather than opening the text editor.
func TestReadCheckbox(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)
	m.pane = paneDetail

	readRow := -1
	for i, r := range m.rows {
		if r.kind == rowField && r.field == epub.FieldRead {
			readRow = i
			break
		}
	}
	if readRow < 0 {
		t.Fatal("no read row in the metadata pane")
	}
	m.rowIdx = readRow

	if view := m.View(); !strings.Contains(view, "[ ]") {
		t.Errorf("unread checkbox is not rendered:\n%s", view)
	}

	m, cmd := step(t, m, keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter did not schedule the read update")
	}
	if m.mode == modeEdit {
		t.Fatal("read opened the text editor")
	}
	m = drain(t, m, cmd)
	if !m.entries[0].Metadata.Read {
		t.Error("enter did not mark the book read")
	}
	if view := m.View(); !strings.Contains(view, "[x]") {
		t.Errorf("read checkbox is not checked:\n%s", view)
	}

	m, cmd = step(t, m, keyMsg(" "))
	if cmd == nil {
		t.Fatal("space did not schedule the read update")
	}
	m = drain(t, m, cmd)
	if m.entries[0].Metadata.Read {
		t.Error("space did not mark the book unread")
	}
	if view := m.View(); !strings.Contains(view, "[ ]") {
		t.Errorf("read checkbox is still checked:\n%s", view)
	}
}

// TestReadFilter checks the dedicated unread/read filter and its combination
// with the search query.
func TestReadFilter(t *testing.T) {
	entries := []library.Entry{
		fakeEntry("/b/a.epub", "Read A", "Ann", ""),
		fakeEntry("/b/b.epub", "Unread B", "Ann", ""),
		fakeEntry("/b/c.epub", "Read C", "Bob", ""),
	}
	entries[0].Metadata.Read = true
	entries[2].Metadata.Read = true

	m := newModel("/b", entries)
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m = next.(model)

	titles := func() []string {
		var out []string
		for _, i := range m.filtered {
			out = append(out, m.entries[i].Metadata.Title)
		}
		return out
	}

	m = key(t, m, "u")
	if got, want := titles(), []string{"Unread B"}; !slices.Equal(got, want) {
		t.Errorf("unread filter = %v, want %v", got, want)
	}
	if !strings.Contains(m.View(), "unread only") {
		t.Errorf("header does not show the unread filter:\n%s", m.View())
	}

	m = key(t, m, "u")
	if got, want := titles(), []string{"Read A", "Unread B", "Read C"}; !slices.Equal(got, want) {
		t.Errorf("clearing the unread filter = %v, want %v", got, want)
	}

	m = key(t, m, "U")
	if got, want := titles(), []string{"Read A", "Read C"}; !slices.Equal(got, want) {
		t.Errorf("read filter = %v, want %v", got, want)
	}

	m.setFilter("author:bob")
	if got, want := titles(), []string{"Read C"}; !slices.Equal(got, want) {
		t.Errorf("combined read and search filter = %v, want %v", got, want)
	}

	m = key(t, m, "esc")
	if m.readFilter != readFilterAll || m.filter != "" {
		t.Errorf("esc left filter=%v query=%q", m.readFilter, m.filter)
	}
}

func TestSearchFilters(t *testing.T) {
	entries := []library.Entry{
		fakeEntry("/b/dune.epub", "Dune", "Frank Herbert", "Dune"),
		fakeEntry("/b/hobbit.epub", "The Hobbit", "J. R. R. Tolkien", ""),
		fakeEntry("/b/left-hand.epub", "The Left Hand of Darkness", "Ursula K. Le Guin", "Hainish"),
	}
	m := newModel("/b", entries)
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(model)
	if len(m.filtered) != 3 {
		t.Fatalf("filtered = %d, want 3", len(m.filtered))
	}

	m = key(t, m, "/")
	if !m.searching {
		t.Fatal("/ did not start the search box")
	}
	m = typeText(m, "hobbit")
	if len(m.filtered) != 1 {
		t.Fatalf("filtered = %v, want just the hobbit", m.filtered)
	}
	if e, _ := m.selected(); e.Metadata.Title != "The Hobbit" {
		t.Errorf("selection = %q", e.Metadata.Title)
	}
	if !strings.Contains(m.View(), "1 of 3 books") {
		t.Errorf("header does not show the filter count:\n%s", m.View())
	}

	// Author matches too.
	m = typeText(m, "zzz") // becomes "hobbitzzz"
	if len(m.filtered) != 0 {
		t.Errorf("filtered = %v, want none", m.filtered)
	}
	if !strings.Contains(m.View(), "no matches") {
		t.Errorf("empty filter does not explain itself:\n%s", m.View())
	}
}

func TestSearchMatchesSeriesAndDescription(t *testing.T) {
	entries := []library.Entry{
		fakeEntry("/b/a.epub", "Alpha", "Ann", "The Culture"),
		fakeEntry("/b/b.epub", "Beta", "Bob", ""),
	}
	entries[1].Metadata.Description = "a story about a ring"
	m := newModel("/b", entries)
	m.rebuildRows()

	m.setFilter("culture")
	if len(m.filtered) != 1 || m.filtered[0] != 0 {
		t.Errorf("series match failed: %v", m.filtered)
	}
	m.setFilter("ring")
	if len(m.filtered) != 1 || m.filtered[0] != 1 {
		t.Errorf("description match failed: %v", m.filtered)
	}
}

func TestSearchFieldPrefixes(t *testing.T) {
	entries := []library.Entry{
		fakeEntry("/b/dune.epub", "Dune", "Frank Herbert", "Dune"),
		fakeEntry("/b/emperor.epub", "Emperor of Dune", "Frank Herbert", "Dune"),
		fakeEntry("/b/other.epub", "Other Book", "Ursula K. Le Guin", "Hainish"),
	}
	other := &entries[2].Metadata
	other.Subtitle = "A Novel"
	other.Subjects = []string{"Science Fiction"}
	other.Description = "Winter world"
	other.Publisher = "Ace Books"
	other.Date = "2020-01-01"
	other.Language = "fr"
	other.Rights = "Public Domain"
	other.Identifier = "urn:isbn:42"
	other.SeriesIndex = "7"
	other.Read = true

	m := newModel("/b", entries)
	m.rebuildRows()

	titles := func() []string {
		var out []string
		for _, i := range m.filtered {
			out = append(out, m.entries[i].Metadata.Title)
		}
		return out
	}

	tests := []struct {
		query string
		want  []string
	}{
		{"title:dune", []string{"Dune", "Emperor of Dune"}},
		{"TITLE:DUNE", []string{"Dune", "Emperor of Dune"}},
		{"author:herbert", []string{"Dune", "Emperor of Dune"}},
		{"creator:herbert", []string{"Dune", "Emperor of Dune"}},
		{"author:le guin", []string{"Other Book"}},
		{"subtitle:novel", []string{"Other Book"}},
		{"tags:science", []string{"Other Book"}},
		{"genre:science", []string{"Other Book"}},
		{"series:hainish", []string{"Other Book"}},
		{"series-index:7", []string{"Other Book"}},
		{"description:winter", []string{"Other Book"}},
		{"publisher:ace", []string{"Other Book"}},
		{"date:2020", []string{"Other Book"}},
		{"language:fr", []string{"Other Book"}},
		{"rights:public", []string{"Other Book"}},
		{"identifier:42", []string{"Other Book"}},
		{"isbn:42", []string{"Other Book"}},
		{"file:emperor", []string{"Emperor of Dune"}},
		{"path:/b/other", []string{"Other Book"}},
		{"title:", []string{"Dune", "Emperor of Dune", "Other Book"}},
		{"author:", []string{"Dune", "Emperor of Dune", "Other Book"}},
		{"tags:", []string{"Other Book"}},
		{"subtitle:", []string{"Other Book"}},
		{"series:", []string{"Dune", "Emperor of Dune", "Other Book"}},
		{"series-index:", []string{"Other Book"}},
		{"description:", []string{"Other Book"}},
		{"publisher:", []string{"Other Book"}},
		{"date:", []string{"Other Book"}},
		{"language:", []string{"Other Book"}},
		{"rights:", []string{"Other Book"}},
		{"identifier:", []string{"Other Book"}},
		{"read:", []string{"Other Book"}},
		{"read:yes", []string{"Other Book"}},
		{"read:true", []string{"Other Book"}},
		{"read:finished", []string{"Other Book"}},
		{"read:no", []string{"Dune", "Emperor of Dune"}},
		{"read:false", []string{"Dune", "Emperor of Dune"}},
		{"read:unread", []string{"Dune", "Emperor of Dune"}},
		{"read:blah", nil},
		{"file:", []string{"Dune", "Emperor of Dune", "Other Book"}},
		{"path:", []string{"Dune", "Emperor of Dune", "Other Book"}},
		{"title:herbert", nil},
		{"author:dune", nil},
		{"unknown:dune", nil},
		{"unknown:", nil},
	}
	for _, tt := range tests {
		m.setFilter(tt.query)
		if got := titles(); !slices.Equal(got, tt.want) {
			t.Errorf("%q matched %v, want %v", tt.query, got, tt.want)
		}
	}
}

func TestSearchCombinedFilters(t *testing.T) {
	entries := []library.Entry{
		fakeEntry("/b/a.epub", "Test Match", "lol", ""),
		fakeEntry("/b/b.epub", "Test Read", "lol", ""),
		fakeEntry("/b/c.epub", "Test Other Author", "Other", ""),
		fakeEntry("/b/d.epub", "Different Title", "lol", ""),
		fakeEntry("/b/e.epub", "Another Match", "lol", ""),
	}
	entries[1].Metadata.Read = true
	entries[4].Metadata.Description = "contains test in the description"

	m := newModel("/b", entries)
	m.rebuildRows()

	titles := func() []string {
		var out []string
		for _, i := range m.filtered {
			out = append(out, m.entries[i].Metadata.Title)
		}
		return out
	}

	// A general term, a field filter and a read filter must all match.
	m.setFilter("test read:no author:lol")
	if got, want := titles(), []string{"Test Match", "Another Match"}; !slices.Equal(got, want) {
		t.Errorf("combined filter matched %v, want %v", got, want)
	}

	// Terms are order independent.
	m.setFilter("author:lol read:no test")
	if got, want := titles(), []string{"Test Match", "Another Match"}; !slices.Equal(got, want) {
		t.Errorf("reordered combined filter matched %v, want %v", got, want)
	}

	// An empty field filter can be combined too.
	m.setFilter("read: author:lol")
	if got, want := titles(), []string{"Test Read"}; !slices.Equal(got, want) {
		t.Errorf("read field filter matched %v, want %v", got, want)
	}
}

func TestSearchQuotedPhrase(t *testing.T) {
	entries := []library.Entry{
		fakeEntry("/b/a.epub", "Phrase Match", "Le Guin", ""),
		fakeEntry("/b/b.epub", "Tokens Only", "Le Something Guin", ""),
	}
	m := newModel("/b", entries)
	m.rebuildRows()

	titles := func() []string {
		var out []string
		for _, i := range m.filtered {
			out = append(out, m.entries[i].Metadata.Title)
		}
		return out
	}

	m.setFilter(`author:"le guin"`)
	if got, want := titles(), []string{"Phrase Match"}; !slices.Equal(got, want) {
		t.Errorf("quoted author filter matched %v, want %v", got, want)
	}

	m.setFilter(`"le guin"`)
	if got, want := titles(), []string{"Phrase Match"}; !slices.Equal(got, want) {
		t.Errorf("quoted general filter matched %v, want %v", got, want)
	}

	// Without quotes the words are independent terms.
	m.setFilter(`author:le guin`)
	if got, want := titles(), []string{"Phrase Match", "Tokens Only"}; !slices.Equal(got, want) {
		t.Errorf("unquoted terms matched %v, want %v", got, want)
	}
}

func TestSearchOperators(t *testing.T) {
	entries := []library.Entry{
		fakeEntry("/b/a.epub", "Dune", "Frank Herbert", "Dune"),
		fakeEntry("/b/b.epub", "Dragonflight", "Anne McCaffrey", "Pern"),
		fakeEntry("/b/c.epub", "Neuromancer", "William Gibson", ""),
		fakeEntry("/b/d.epub", "No Tags", "Frank Herbert", ""),
	}
	entries[0].Metadata.Subjects = []string{"scifi"}
	entries[1].Metadata.Subjects = []string{"fantasy"}
	entries[2].Metadata.Subjects = []string{"scifi"}
	entries[0].Metadata.Date = "1965"
	entries[1].Metadata.Date = "1968"
	entries[2].Metadata.Date = "1984"
	entries[3].Metadata.Date = "1990"
	entries[0].Metadata.SeriesIndex = "1"
	entries[1].Metadata.SeriesIndex = "2"
	entries[2].Metadata.SeriesIndex = "3"
	entries[0].Metadata.Language = "en"
	entries[1].Metadata.Language = "fr"
	entries[2].Metadata.Language = "en"
	entries[3].Metadata.Language = "en"
	entries[0].Metadata.Read = true

	m := newModel("/b", entries)
	m.rebuildRows()

	titles := func() []string {
		var out []string
		for _, i := range m.filtered {
			out = append(out, m.entries[i].Metadata.Title)
		}
		return out
	}

	tests := []struct {
		query string
		want  []string
	}{
		{"-read:yes", []string{"Dragonflight", "Neuromancer", "No Tags"}},
		{"tag:scifi OR tag:fantasy", []string{"Dune", "Dragonflight", "Neuromancer"}},
		{"author:herbert tag:scifi", []string{"Dune"}},
		{"author:herbert OR author:gibson", []string{"Dune", "Neuromancer", "No Tags"}},
		{"date:>=1980", []string{"Neuromancer", "No Tags"}},
		{"date:<1970", []string{"Dune", "Dragonflight"}},
		{"series-index:>=2", []string{"Dragonflight", "Neuromancer"}},
		{"-tag:", []string{"No Tags"}},
		{"-author:herbert", []string{"Dragonflight", "Neuromancer"}},
		{"title:=dune", []string{"Dune"}},
		{"title:!=dune", []string{"Dragonflight", "Neuromancer", "No Tags"}},
		{"language:=en", []string{"Dune", "Neuromancer", "No Tags"}},
		{"author:herbert read:no OR title:neuromancer", []string{"Neuromancer", "No Tags"}},
	}
	for _, tt := range tests {
		m.setFilter(tt.query)
		if got := titles(); !slices.Equal(got, tt.want) {
			t.Errorf("%q matched %v, want %v", tt.query, got, tt.want)
		}
	}
}

func TestSearchEscapeClears(t *testing.T) {
	entries := []library.Entry{
		fakeEntry("/b/a.epub", "Alpha", "Ann", ""),
		fakeEntry("/b/b.epub", "Beta", "Bob", ""),
	}
	m := newModel("/b", entries)
	m.rebuildRows()

	m = key(t, m, "/")
	m = typeText(m, "beta")
	if len(m.filtered) != 1 {
		t.Fatalf("filtered = %v", m.filtered)
	}
	m = key(t, m, "esc")
	if m.searching {
		t.Error("esc left the search box focused")
	}
	if m.filter != "" {
		t.Errorf("esc left the filter as %q", m.filter)
	}
	if got := m.search.Value(); got != "" {
		t.Errorf("esc left the search box as %q", got)
	}
	if len(m.filtered) != 2 {
		t.Errorf("esc did not restore the full list: %v", m.filtered)
	}

	// Accepting a query with enter keeps the filter, and esc while browsing
	// clears it again.
	m = key(t, m, "/")
	m = typeText(m, "alpha")
	m = key(t, m, "enter")
	if m.searching {
		t.Error("enter left the search box focused")
	}
	if m.filter != "alpha" || len(m.filtered) != 1 {
		t.Fatalf("enter did not keep the filter: %q %v", m.filter, m.filtered)
	}
	m = key(t, m, "esc")
	if m.filter != "" || len(m.filtered) != 2 {
		t.Errorf("esc did not clear the accepted filter: %q %v", m.filter, m.filtered)
	}
	if got := m.search.Value(); got != "" {
		t.Errorf("esc left the accepted query in the search box as %q", got)
	}

	// The next search starts with an empty input.
	m = key(t, m, "/")
	if got := m.search.Value(); got != "" {
		t.Errorf("new search started with %q", got)
	}
}

// TestScrollCtrlDU checks that ctrl+d / ctrl+u page the list like pgdown/pgup.
// TestScrollListUsesEntryHeights checks that the stored viewport top matches the
// line heights used to render the list. Previously it was computed from a
// two-lines-per-book assumption, so mixed series entries made the view jump at
// the bottom.
func TestScrollListUsesEntryHeights(t *testing.T) {
	var entries []library.Entry
	for i := 0; i < 8; i++ {
		series := ""
		if i%2 == 0 {
			series = fmt.Sprintf("Series %d", i)
		}
		e := fakeEntry(fmt.Sprintf("/b/%02d.epub", i), fmt.Sprintf("Book %02d", i), "Author", series)
		if series != "" {
			e.Metadata.SeriesIndex = "1"
		}
		entries = append(entries, e)
	}

	m := newModel("/b", entries)
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	m = next.(model)

	m.listIdx = 7
	m.listTop = 4
	m.scrollList()
	if m.listTop != 5 {
		t.Fatalf("top at the last entry = %d, want 5", m.listTop)
	}
	lines := m.listLines(60, m.listContentHeight())
	if len(lines) == 0 || !strings.Contains(lines[0], "Book 05") {
		t.Errorf("rendered list does not start at entry 5: %q", lines)
	}

	m.listIdx = 6
	m.scrollList()
	if m.listTop != 5 {
		t.Errorf("moving up one entry changed the top to %d, want 5", m.listTop)
	}
}

func TestScrollCtrlDU(t *testing.T) {
	var entries []library.Entry
	for i := 0; i < 60; i++ {
		entries = append(entries, fakeEntry(
			filepath.Join("/b", fmt.Sprintf("%02d.epub", i)),
			fmt.Sprintf("Book %02d", i), "Author", ""))
	}
	m := newModel("/b", entries)
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(model)
	m.pane = paneList

	page := m.listPageSize()
	if page < 2 {
		t.Fatalf("page size = %d, too small for the test", page)
	}
	m = key(t, m, "ctrl+d")
	if m.listIdx != page {
		t.Errorf("ctrl+d moved to %d, want %d", m.listIdx, page)
	}
	m = key(t, m, "ctrl+u")
	if m.listIdx != 0 {
		t.Errorf("ctrl+u moved to %d, want 0", m.listIdx)
	}

	// Paging past the end clamps, and never overflows the view.
	for i := 0; i < 20; i++ {
		m = key(t, m, "ctrl+d")
	}
	if m.listIdx != len(entries)-1 {
		t.Errorf("ctrl+d stopped at %d, want %d", m.listIdx, len(entries)-1)
	}
}

func TestEditSeriesEndToEnd(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)
	m = key(t, m, "tab")

	for i := 0; i < len(m.rows); i++ {
		if m.rows[m.rowIdx].field == epub.FieldSeries {
			break
		}
		m = key(t, m, "j")
	}
	if m.rows[m.rowIdx].field != epub.FieldSeries {
		t.Fatalf("could not reach the series row")
	}
	m = key(t, m, "e")
	m = typeText(m, "The Culture")
	m, cmd := step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter did not schedule a save")
	}
	if msg := cmd().(savedMsg); msg.err != nil {
		t.Fatal(msg.err)
	}
	meta, err := epub.ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Series != "The Culture" {
		t.Errorf("series = %q", meta.Series)
	}
	// An unrelated field is untouched.
	if meta.Title != "The Left Hand of Darkness" {
		t.Errorf("title = %q", meta.Title)
	}
}

// TestSpaceTogglesSelection checks the multi-select state machine: one book
// is not enough for bulk mode, two are, and esc leaves it.
func TestSpaceTogglesSelection(t *testing.T) {
	entries := []library.Entry{
		fakeEntry("/b/dune.epub", "Dune", "Frank Herbert", "Dune"),
		fakeEntry("/b/hobbit.epub", "The Hobbit", "J. R. R. Tolkien", ""),
		fakeEntry("/b/left-hand.epub", "The Left Hand of Darkness", "Ursula K. Le Guin", "Hainish"),
	}
	m := newModel("/b", entries)
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m = next.(model)

	m = key(t, m, " ")
	if !m.sel[0] {
		t.Fatalf("space did not select the book under the cursor: %v", m.sel)
	}
	if m.listIdx != 1 {
		t.Errorf("space did not advance the cursor: listIdx = %d, want 1", m.listIdx)
	}
	if m.bulkActive() {
		t.Fatal("one selected book must not start a bulk edit")
	}
	m = key(t, m, " ")
	if !m.sel[1] {
		t.Fatalf("space did not select the next book: %v", m.sel)
	}
	if m.listIdx != 2 {
		t.Errorf("space did not advance the cursor again: listIdx = %d, want 2", m.listIdx)
	}
	if !m.bulkActive() {
		t.Fatalf("two selected books should start a bulk edit: %v", m.sel)
	}
	// The detail pane now holds the six bulk fields only.
	if len(m.rows) != len(bulkFields) {
		t.Fatalf("bulk rows = %d, want %d: %+v", len(m.rows), len(bulkFields), m.rows)
	}
	for i, r := range m.rows {
		if r.field != bulkFields[i] {
			t.Errorf("row %d = %s, want %s", i, r.field, bulkFields[i])
		}
		if r.kind != rowField {
			t.Errorf("row %d kind = %v, want a plain field", i, r.kind)
		}
	}
	if !strings.Contains(m.View(), "2 selected") {
		t.Errorf("header does not show the selection:\n%s", m.View())
	}
	if !strings.Contains(m.View(), "Bulk · 2 books") {
		t.Errorf("detail pane has no bulk title:\n%s", m.View())
	}
	m.pane = paneList
	if v := m.View(); !strings.Contains(v, "✓") {
		t.Errorf("list shows no selection checkbox:\n%s", v)
	}

	// Move back to the second book and deselect it. Deselecting advances the
	// cursor to the next book as well.
	m = key(t, m, "k")
	m = key(t, m, " ")
	if m.sel[1] {
		t.Fatalf("space did not deselect the second book: %v", m.sel)
	}
	if m.listIdx != 2 {
		t.Errorf("deselecting did not advance the cursor: listIdx = %d, want 2", m.listIdx)
	}
	if m.bulkActive() {
		t.Fatal("deselecting should leave bulk mode")
	}
	// esc clears the remaining selection.
	m = key(t, m, "esc")
	if len(m.sel) != 0 {
		t.Fatalf("esc did not clear the selection: %v", m.sel)
	}
}

// TestCtrlASelectsVisibleBooks checks that ctrl+a selects exactly the books
// shown by the active filter, not the hidden ones.
func TestCtrlASelectsVisibleBooks(t *testing.T) {
	entries := []library.Entry{
		fakeEntry("/b/dune.epub", "Dune", "Frank Herbert", "Dune"),
		fakeEntry("/b/hobbit.epub", "The Hobbit", "J. R. R. Tolkien", ""),
		fakeEntry("/b/left-hand.epub", "The Left Hand of Darkness", "Ursula K. Le Guin", "Hainish"),
	}
	m := newModel("/b", entries)
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m = next.(model)

	// Select everything when no filter is active.
	m = key(t, m, "ctrl+a")
	if len(m.sel) != len(entries) {
		t.Fatalf("ctrl+a selected %d books, want %d: %v", len(m.sel), len(entries), m.sel)
	}
	if !m.bulkActive() {
		t.Fatal("selecting every book should start a bulk edit")
	}

	// A filter narrows the selection to the visible matches.
	m.setFilter("hobbit")
	m = key(t, m, "ctrl+a")
	if len(m.sel) != 1 {
		t.Fatalf("ctrl+a with a filter selected %d books, want 1: %v", len(m.sel), m.sel)
	}
	if !m.sel[m.filtered[0]] {
		t.Fatalf("ctrl+a did not select the visible book: %v", m.sel)
	}
	for i := range entries {
		if i != m.filtered[0] && m.sel[i] {
			t.Errorf("ctrl+a kept hidden book %d selected: %v", i, m.sel)
		}
	}
}

// TestBulkEditEndToEnd drives a bulk publisher edit over two real files and
// verifies both on disk.
func TestBulkEditEndToEnd(t *testing.T) {
	dir := t.TempDir()
	p1 := makeEPUBIn(t, dir, "one.epub")
	p2 := makeEPUBIn(t, dir, "two.epub")
	m := newModel(dir, libraryOf(t, dir, p1, p2))
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m = next.(model)

	m = key(t, m, " ")
	m = key(t, m, "j")
	m = key(t, m, " ")
	if !m.bulkActive() {
		t.Fatal("selection did not start a bulk edit")
	}
	m = key(t, m, "enter") // to the detail pane
	for i := 0; i < len(m.rows) && m.rows[m.rowIdx].field != epub.FieldPublisher; i++ {
		m = key(t, m, "j")
	}
	if m.rows[m.rowIdx].field != epub.FieldPublisher {
		t.Fatalf("could not reach the publisher row: %+v", m.rows[m.rowIdx])
	}
	m = key(t, m, "e")
	if m.mode != modeEdit || !m.editBulk {
		t.Fatalf("expected a bulk editor, got mode=%v editBulk=%v", m.mode, m.editBulk)
	}
	if !strings.Contains(m.viewEditor(), "2 books") {
		t.Errorf("editor label does not mention the selection:\n%s", m.viewEditor())
	}
	// The editor starts prefilled with the shared value; erase it.
	for m.input.Value() != "" {
		m, _ = step(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	m = typeText(m, "Bulk House")
	m, cmd := step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter did not schedule the bulk save")
	}
	if !m.saving {
		t.Fatal("saving flag not set")
	}
	m = drain(t, m, cmd)

	if m.saving {
		t.Fatal("saving flag stuck after the chain finished")
	}
	if m.status != "saved 2 books" {
		t.Errorf("status = %q", m.status)
	}
	for _, p := range []string{p1, p2} {
		meta, err := epub.ReadMetadata(p)
		if err != nil {
			t.Fatal(err)
		}
		if meta.Publisher != "Bulk House" {
			t.Errorf("%s publisher = %q", filepath.Base(p), meta.Publisher)
		}
		// Unrelated metadata survives.
		if meta.Title != "The Left Hand of Darkness" || len(meta.Creators) != 1 {
			t.Errorf("%s was damaged: %+v", filepath.Base(p), meta)
		}
	}
}

// TestBulkEditSkipsEmptyCommit checks that leaving a mixed row's empty input
// untouched does not clear the field on every book.
func TestBulkEditSkipsEmptyCommit(t *testing.T) {
	dir := t.TempDir()
	p1 := makeEPUBIn(t, dir, "one.epub")
	p2 := makeEPUBIn(t, dir, "two.epub")
	m := newModel(dir, libraryOf(t, dir, p1, p2))
	// The books disagree on the series, so that row starts out mixed.
	m.entries[1].Metadata.Series = "Something Else"
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m = next.(model)

	m = key(t, m, " ")
	m = key(t, m, "j")
	m = key(t, m, " ")
	m = key(t, m, "enter")
	for i := 0; i < len(m.rows) && m.rows[m.rowIdx].field != epub.FieldSeries; i++ {
		m = key(t, m, "j")
	}
	if !m.rows[m.rowIdx].mixed {
		t.Fatalf("series row should be mixed: %+v", m.rows[m.rowIdx])
	}
	if !strings.Contains(m.View(), "(mixed)") {
		t.Errorf("view does not mark the mixed row:\n%s", m.View())
	}

	m = key(t, m, "e")
	m, cmd := step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("committing an untouched mixed row must not write files")
	}

	// Pressing d, in contrast, clears the field everywhere.
	m, cmd = step(t, m, keyMsg("d"))
	if cmd == nil {
		t.Fatal("d did not schedule the bulk clear")
	}
	m = drain(t, m, cmd)
	if m.status != "saved 2 books" {
		t.Errorf("status = %q", m.status)
	}
	for _, p := range []string{p1, p2} {
		meta, err := epub.ReadMetadata(p)
		if err != nil {
			t.Fatal(err)
		}
		if meta.Series != "" {
			t.Errorf("%s series = %q, want cleared", filepath.Base(p), meta.Series)
		}
		if meta.Title != "The Left Hand of Darkness" {
			t.Errorf("%s title was damaged", filepath.Base(p))
		}
	}
}

// TestDeleteRequiresConfirmation deletes a multi-selection only after the
// dialog is confirmed, and verifies that cancelling is safe.
func TestDeleteRequiresConfirmation(t *testing.T) {
	dir := t.TempDir()
	p1 := makeEPUBIn(t, dir, "one.epub")
	p2 := makeEPUBIn(t, dir, "two.epub")
	m := newModel(dir, libraryOf(t, dir, p1, p2))
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m = next.(model)

	// Select both books.
	m = key(t, m, " ")
	m = key(t, m, "j")
	m = key(t, m, " ")
	if !m.bulkActive() {
		t.Fatal("selection did not start a bulk edit")
	}

	// D opens the dialog without touching the files.
	m = key(t, m, "D")
	if !m.confirmDelete {
		t.Fatal("D did not open the confirmation dialog")
	}
	if len(m.deletePaths) != 2 {
		t.Fatalf("deletePaths = %v, want both books", m.deletePaths)
	}
	view := m.View()
	for _, want := range []string{"Delete 2 files?", "one.epub", "two.epub"} {
		if !strings.Contains(view, want) {
			t.Errorf("confirmation dialog is missing %q:\n%s", want, view)
		}
	}
	for _, p := range []string{p1, p2} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s was deleted before confirmation: %v", p, err)
		}
	}

	// Cancelling leaves everything alone.
	m = key(t, m, "esc")
	if m.confirmDelete || len(m.deletePaths) != 0 {
		t.Fatalf("esc did not close the dialog: confirm=%v paths=%v", m.confirmDelete, m.deletePaths)
	}
	for _, p := range []string{p1, p2} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s was deleted after cancelling: %v", p, err)
		}
	}

	// Confirming schedules the deletion.
	m = key(t, m, "D")
	m, cmd := step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter did not schedule the deletion")
	}
	if !m.deleting || m.confirmDelete {
		t.Fatalf("deleting = %v, confirmDelete = %v", m.deleting, m.confirmDelete)
	}
	msg, ok := cmd().(deletedMsg)
	if !ok {
		t.Fatalf("delete command returned %T", cmd())
	}
	m, _ = step(t, m, msg)

	if m.deleting {
		t.Error("deleting flag stuck after the batch finished")
	}
	if len(m.entries) != 0 || len(m.filtered) != 0 {
		t.Errorf("entries = %d, filtered = %d, want both empty", len(m.entries), len(m.filtered))
	}
	if len(m.sel) != 0 {
		t.Errorf("selection survived the deletion: %v", m.sel)
	}
	if m.status != "deleted 2 files" {
		t.Errorf("status = %q", m.status)
	}
	for _, p := range []string{p1, p2} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists: %v", filepath.Base(p), err)
		}
	}
}

// TestDeleteCurrentBook checks that D falls back to the book under the cursor
// when no multi-selection is active.
func TestDeleteCurrentBook(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)

	m = key(t, m, "D")
	if !m.confirmDelete {
		t.Fatal("D did not open the confirmation dialog")
	}
	if len(m.deletePaths) != 1 || m.deletePaths[0] != path {
		t.Fatalf("deletePaths = %v, want [%s]", m.deletePaths, path)
	}

	m, cmd := step(t, m, keyMsg("y"))
	if cmd == nil {
		t.Fatal("y did not schedule the deletion")
	}
	msg := cmd()
	m, _ = step(t, m, msg)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still exists after confirmation: %v", err)
	}
	if len(m.entries) != 0 {
		t.Errorf("entries = %d, want 0", len(m.entries))
	}
	if m.status != "deleted 1 file" {
		t.Errorf("status = %q", m.status)
	}
}

// TestHelpDialog opens the key map, scrolls to the end and closes it again.
func TestHelpDialog(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 100, 30)

	m = key(t, m, "?")
	if !m.helpOpen {
		t.Fatal("? did not open the help dialog")
	}
	view := m.View()
	for _, want := range []string{
		"Keys", "Browse (both panes)", "open this help", "delete selected file(s)",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("help dialog is missing %q:\n%s", want, view)
		}
	}

	// Scrolling reaches the last page of bindings.
	if m.helpMaxTop() == 0 {
		t.Fatalf("help fits without scrolling; the test needs a longer list")
	}
	m = key(t, m, "G")
	if m.helpTop != m.helpMaxTop() {
		t.Errorf("G moved to %d, want %d", m.helpTop, m.helpMaxTop())
	}
	view = m.View()
	for _, want := range []string{"Help", "close", "scroll"} {
		if !strings.Contains(view, want) {
			t.Errorf("scrolled help is missing %q:\n%s", want, view)
		}
	}

	// ? closes the dialog; q closes it too instead of quitting.
	m = key(t, m, "?")
	if m.helpOpen {
		t.Fatal("? did not close the help dialog")
	}
	m = key(t, m, "?")
	m = key(t, m, "q")
	if m.helpOpen {
		t.Fatal("q did not close the help dialog")
	}

	// The footer advertises the shortcut.
	if !strings.Contains(m.View(), "? help") {
		t.Errorf("footer does not mention the help key:\n%s", m.View())
	}
}

// drain runs a chained command sequence to completion.
func drain(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	for i := 0; cmd != nil; i++ {
		if i > 1000 {
			t.Fatal("command chain did not finish")
		}
		msg := cmd()
		if msg == nil {
			break
		}
		m, cmd = step(t, m, msg)
	}
	return m
}

func key(t *testing.T, m model, k string) model {
	t.Helper()
	got, _ := step(t, m, keyMsg(k))
	return got
}

// step feeds one message to the model and unwraps the tea.Model result.
func step(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	got, ok := next.(model)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return got, cmd
}

func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+a":
		return tea.KeyMsg{Type: tea.KeyCtrlA}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "ctrl+o":
		return tea.KeyMsg{Type: tea.KeyCtrlO}
	case "ctrl+k":
		return tea.KeyMsg{Type: tea.KeyCtrlK}
	case "ctrl+t":
		return tea.KeyMsg{Type: tea.KeyCtrlT}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
}

func typeText(to model, s string) model {
	next, _ := to.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	got, ok := next.(model)
	if !ok {
		panic("Update returned a non-model")
	}
	return got
}
