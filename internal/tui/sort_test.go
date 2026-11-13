package tui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gollyhatch/shelf/internal/library"
)

func sortTestEntries() []library.Entry {
	alpha := fakeEntry("/b/a.epub", "Alpha", "Ann", "Saga")
	alpha.Metadata.SeriesIndex = "2"
	alpha.Metadata.Date = "2001"
	alpha.Metadata.Language = "en"

	mike := fakeEntry("/b/m.epub", "Mike", "Bob", "")
	mike.Metadata.Date = "2010"
	mike.Metadata.Language = "fr"

	zeta := fakeEntry("/b/z.epub", "Zeta", "Zed", "Saga")
	zeta.Metadata.SeriesIndex = "10"
	zeta.Metadata.Date = "2020"
	zeta.Metadata.Language = "de"

	return []library.Entry{zeta, alpha, mike}
}

func visibleTitles(m model) []string {
	entries := m.visible()
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Title()
	}
	return out
}

func TestSortModes(t *testing.T) {
	tests := []struct {
		name  string
		field sortField
		desc  bool
		want  []string
	}{
		{"path ascending", sortPath, false, []string{"Alpha", "Mike", "Zeta"}},
		{"path descending", sortPath, true, []string{"Zeta", "Mike", "Alpha"}},
		{"title ascending", sortTitle, false, []string{"Alpha", "Mike", "Zeta"}},
		{"title descending", sortTitle, true, []string{"Zeta", "Mike", "Alpha"}},
		{"author ascending", sortAuthor, false, []string{"Alpha", "Mike", "Zeta"}},
		{"author descending", sortAuthor, true, []string{"Zeta", "Mike", "Alpha"}},
		{"series ascending", sortSeries, false, []string{"Alpha", "Zeta", "Mike"}},
		{"series descending", sortSeries, true, []string{"Zeta", "Alpha", "Mike"}},
		{"date ascending", sortDate, false, []string{"Alpha", "Mike", "Zeta"}},
		{"date descending", sortDate, true, []string{"Zeta", "Mike", "Alpha"}},
		{"language ascending", sortLanguage, false, []string{"Zeta", "Alpha", "Mike"}},
		{"language descending", sortLanguage, true, []string{"Mike", "Alpha", "Zeta"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel("/b", sortTestEntries())
			m.sortKeys = []sortMode{{field: tt.field, desc: tt.desc}}
			m.applyFilter()
			if got := visibleTitles(m); !slices.Equal(got, tt.want) {
				t.Errorf("order = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseSortSpec(t *testing.T) {
	tests := []struct {
		value   string
		want    []sortMode
		wantErr bool
	}{
		{"", []sortMode{{field: sortPath}}, false},
		{"path", []sortMode{{field: sortPath}}, false},
		{"file path", []sortMode{{field: sortPath}}, false},
		{"title", []sortMode{{field: sortTitle}}, false},
		{"TITLE:DESC", []sortMode{{field: sortTitle, desc: true}}, false},
		{"title-desc", []sortMode{{field: sortTitle, desc: true}}, false},
		{"author z-a", []sortMode{{field: sortAuthor, desc: true}}, false},
		{"creator ascending", []sortMode{{field: sortAuthor}}, false},
		{"series:descending", []sortMode{{field: sortSeries, desc: true}}, false},
		{"date newest first", []sortMode{{field: sortDate, desc: true}}, false},
		{"date-oldest-first", []sortMode{{field: sortDate}}, false},
		{"language:desc", []sortMode{{field: sortLanguage, desc: true}}, false},
		{"author,series", []sortMode{{field: sortAuthor}, {field: sortSeries}}, false},
		{"author:desc,series", []sortMode{{field: sortAuthor, desc: true}, {field: sortSeries}}, false},
		{"date newest first,title", []sortMode{{field: sortDate, desc: true}, {field: sortTitle}}, false},
		{"nonsense", nil, true},
		{"author,", nil, true},
		{",series", nil, true},
		{"title sideways", nil, true},
		{"title:desc:extra", nil, true},
	}
	for _, tt := range tests {
		got, err := parseSortSpec(tt.value)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseSortSpec(%q) returned no error", tt.value)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSortSpec(%q): %v", tt.value, err)
			continue
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("parseSortSpec(%q) = %+v, want %+v", tt.value, got, tt.want)
		}
	}
}

func TestApplySortSetting(t *testing.T) {
	m := newModel("/b", sortTestEntries())
	if err := m.applySortSetting("title:desc"); err != nil {
		t.Fatal(err)
	}
	if want := []sortMode{{field: sortTitle, desc: true}}; !slices.Equal(m.sortKeys, want) {
		t.Errorf("sort keys = %+v, want %+v", m.sortKeys, want)
	}
	if got := visibleTitles(m); !slices.Equal(got, []string{"Zeta", "Mike", "Alpha"}) {
		t.Errorf("order = %v", got)
	}

	// An invalid setting is reported and leaves the active sort untouched.
	if err := m.applySortSetting("sideways"); err == nil {
		t.Fatal("invalid setting was accepted")
	}
	if want := []sortMode{{field: sortTitle, desc: true}}; !slices.Equal(m.sortKeys, want) {
		t.Errorf("invalid setting changed the sort keys: %+v", m.sortKeys)
	}
}

func TestApplyCompositeSortSetting(t *testing.T) {
	entries := []library.Entry{
		fakeEntry("/b/zeta.epub", "Zeta", "Ann", "Beta"),
		fakeEntry("/b/alpha.epub", "Alpha", "Ann", "Alpha"),
		fakeEntry("/b/beta.epub", "Beta", "Bob", "Alpha"),
	}
	entries[0].Metadata.SeriesIndex = "1"
	entries[1].Metadata.SeriesIndex = "2"
	entries[2].Metadata.SeriesIndex = "1"

	m := newModel("/b", entries)
	if err := m.applySortSetting("author,series"); err != nil {
		t.Fatal(err)
	}
	if want := []sortMode{{field: sortAuthor}, {field: sortSeries}}; !slices.Equal(m.sortKeys, want) {
		t.Errorf("sort keys = %+v, want %+v", m.sortKeys, want)
	}
	if got := visibleTitles(m); !slices.Equal(got, []string{"Alpha", "Zeta", "Beta"}) {
		t.Errorf("author then series order = %v", got)
	}

	// The direction is per key: the author stays ascending, series descends.
	if err := m.applySortSetting("author,series:desc"); err != nil {
		t.Fatal(err)
	}
	if got := visibleTitles(m); !slices.Equal(got, []string{"Zeta", "Alpha", "Beta"}) {
		t.Errorf("author then descending series order = %v", got)
	}
}

func TestSeriesSortUsesNumericIndex(t *testing.T) {
	m := newModel("/b", sortTestEntries())
	m.sortKeys = []sortMode{{field: sortSeries}}
	m.applyFilter()
	// Both in Saga: episode 2 must come before episode 10.
	if got := visibleTitles(m); !slices.Equal(got, []string{"Alpha", "Zeta", "Mike"}) {
		t.Errorf("order = %v", got)
	}
}

// TestSeriesSortUsesEpisodeAsSecondaryKey checks that the series name is the
// primary key and the episode number is the secondary key.
func TestSeriesSortUsesEpisodeAsSecondaryKey(t *testing.T) {
	entries := []library.Entry{
		fakeEntry("/b/beta.epub", "Beta Book", "Author", "Beta"),
		fakeEntry("/b/alpha-ten.epub", "Alpha Ten", "Author", "Alpha"),
		fakeEntry("/b/alpha-two.epub", "Alpha Two", "Author", "Alpha"),
	}
	entries[0].Metadata.SeriesIndex = "1"
	entries[1].Metadata.SeriesIndex = "10"
	entries[2].Metadata.SeriesIndex = "2"

	m := newModel("/b", entries)
	m.sortKeys = []sortMode{{field: sortSeries}}
	m.applyFilter()
	if got := visibleTitles(m); !slices.Equal(got, []string{"Alpha Two", "Alpha Ten", "Beta Book"}) {
		t.Errorf("ascending series order = %v", got)
	}

	m.sortKeys = []sortMode{{field: sortSeries, desc: true}}
	m.applyFilter()
	if got := visibleTitles(m); !slices.Equal(got, []string{"Beta Book", "Alpha Ten", "Alpha Two"}) {
		t.Errorf("descending series order = %v", got)
	}
}

func TestSortSelector(t *testing.T) {
	m := newModel("/b", sortTestEntries())
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(model)

	// Start on Zeta and sort by title ascending.
	for {
		e, ok := m.selected()
		if ok && e.Path == "/b/z.epub" {
			break
		}
		m = key(t, m, "j")
	}
	m = key(t, m, "S")
	if !m.sortOpen {
		t.Fatal("S did not open the sort selector")
	}
	view := m.View()
	for _, want := range []string{"Sort by", "Title A–Z", "Series, episode Z–A", "Date oldest first"} {
		if !strings.Contains(view, want) {
			t.Errorf("sort selector is missing %q:\n%s", want, view)
		}
	}

	// Sort modes are ordered as path, title, author, ...; title A-Z is third.
	if sortModes[2].field != sortTitle || sortModes[2].desc {
		t.Fatalf("unexpected sortModes[2]: %+v", sortModes[2])
	}
	m = key(t, m, "j")
	m = key(t, m, "j")
	m, cmd := step(t, m, keyMsg("enter"))
	if cmd != nil {
		t.Fatal("applying a sort scheduled a command")
	}
	if m.sortOpen || !slices.Equal(m.sortKeys, []sortMode{{field: sortTitle}}) {
		t.Fatalf("sort state = open:%v keys:%+v", m.sortOpen, m.sortKeys)
	}
	if got := visibleTitles(m); !slices.Equal(got, []string{"Alpha", "Mike", "Zeta"}) {
		t.Errorf("order after sort = %v", got)
	}
	if e, ok := m.selected(); !ok || e.Path != "/b/z.epub" {
		t.Errorf("cursor moved away from Zeta: %+v", e)
	}

	// Title descending is one row below the current mode.
	m = key(t, m, "S")
	m = key(t, m, "j")
	m, _ = step(t, m, keyMsg("enter"))
	if got := visibleTitles(m); !slices.Equal(got, []string{"Zeta", "Mike", "Alpha"}) {
		t.Errorf("descending order = %v", got)
	}
	if e, ok := m.selected(); !ok || e.Path != "/b/z.epub" {
		t.Errorf("cursor did not stay on Zeta: %+v", e)
	}

	// Escape cancels without changing the active sort.
	m = key(t, m, "S")
	m = key(t, m, "j")
	m = key(t, m, "esc")
	if m.sortOpen {
		t.Fatal("esc did not close the sort selector")
	}
	if got := visibleTitles(m); !slices.Equal(got, []string{"Zeta", "Mike", "Alpha"}) {
		t.Errorf("cancelling changed the order: %v", got)
	}
}

// TestSavedMetadataResorts checks that editing a sorted field re-orders the
// Books pane while keeping the saved book selected.
func TestSavedMetadataResorts(t *testing.T) {
	a := fakeEntry("/b/a.epub", "Alpha", "Ann", "")
	b := fakeEntry("/b/b.epub", "Beta", "Bob", "")
	m := newModel("/b", []library.Entry{a, b})
	m.sortKeys = []sortMode{{field: sortTitle}}
	m.applyFilter()
	m.rebuildRows()
	m.listIdx = 0

	meta := m.entries[0].Metadata
	meta.Title = "Zulu"
	next, _ := m.Update(savedMsg{path: "/b/a.epub", meta: meta})
	m = next.(model)

	if got := visibleTitles(m); !slices.Equal(got, []string{"Beta", "Zulu"}) {
		t.Errorf("order after save = %v", got)
	}
	if e, ok := m.selected(); !ok || e.Path != "/b/a.epub" {
		t.Errorf("cursor is not on the saved book: %+v", e)
	}
}
