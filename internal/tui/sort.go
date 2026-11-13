package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gollyhatch/shelf/internal/library"
)

// sortField identifies the value the Books pane is ordered by.
type sortField int

const (
	sortPath sortField = iota
	sortTitle
	sortAuthor
	sortSeries
	sortDate
	sortLanguage
)

func (f sortField) label() string {
	switch f {
	case sortTitle:
		return "Title"
	case sortAuthor:
		return "Author"
	case sortSeries:
		return "Series"
	case sortDate:
		return "Date"
	case sortLanguage:
		return "Language"
	}
	return "File path"
}

// sortMode is one selectable field/direction combination.
type sortMode struct {
	field sortField
	desc  bool
}

func (s sortMode) label() string {
	if s.field == sortDate {
		if s.desc {
			return "Date newest first"
		}
		return "Date oldest first"
	}
	if s.field == sortSeries {
		if s.desc {
			return "Series, episode Z–A"
		}
		return "Series, episode A–Z"
	}
	if s.desc {
		return s.field.label() + " Z–A"
	}
	return s.field.label() + " A–Z"
}

// sortModes is the ordered list shown by the sort selector. File path
// ascending is the default and preserves the original discovery order.
var sortModes = []sortMode{
	{sortPath, false}, {sortPath, true},
	{sortTitle, false}, {sortTitle, true},
	{sortAuthor, false}, {sortAuthor, true},
	{sortSeries, false}, {sortSeries, true},
	{sortDate, false}, {sortDate, true},
	{sortLanguage, false}, {sortLanguage, true},
}

// parseSortSpec parses the configured sort value into an ordered list of keys.
// Keys are separated by commas; each key may carry a direction after a colon,
// a hyphen, or whitespace, e.g. "author,series:desc" or
// "date newest first,title". Without a direction a key ascends.
func parseSortSpec(value string) ([]sortMode, error) {
	s := strings.ToLower(strings.TrimSpace(value))
	if s == "" {
		return []sortMode{{field: sortPath}}, nil
	}

	parts := strings.Split(s, ",")
	keys := make([]sortMode, 0, len(parts))
	for _, part := range parts {
		key, err := parseSortKey(part)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// parseSortKey parses one field and optional direction.
func parseSortKey(value string) (sortMode, error) {
	s := strings.ToLower(strings.TrimSpace(value))
	if s == "" {
		return sortMode{}, fmt.Errorf("empty sort key")
	}
	if field, ok := parseSortField(s); ok {
		return sortMode{field: field}, nil
	}

	fieldPart, directionPart := s, ""
	switch {
	case strings.Contains(s, ":"):
		fieldPart, directionPart, _ = strings.Cut(s, ":")
	case len(strings.Fields(s)) > 1:
		parts := strings.Fields(s)
		fieldPart = parts[0]
		directionPart = strings.Join(parts[1:], " ")
	default:
		if before, after, ok := strings.Cut(s, "-"); ok {
			fieldPart, directionPart = before, after
		}
	}

	field, ok := parseSortField(strings.TrimSpace(fieldPart))
	if !ok {
		return sortMode{}, fmt.Errorf("unknown sort field %q (want path, title, author, series, date or language)", fieldPart)
	}
	desc, ok := parseSortDirection(strings.TrimSpace(directionPart))
	if !ok {
		return sortMode{}, fmt.Errorf("unknown sort direction %q (want asc or desc)", directionPart)
	}
	return sortMode{field: field, desc: desc}, nil
}

// parseSortField resolves the configurable name of a sort field.
func parseSortField(s string) (sortField, bool) {
	switch strings.TrimSpace(s) {
	case "path", "file", "filename", "file path", "file-path":
		return sortPath, true
	case "title":
		return sortTitle, true
	case "author", "creator":
		return sortAuthor, true
	case "series":
		return sortSeries, true
	case "date", "published":
		return sortDate, true
	case "language", "lang":
		return sortLanguage, true
	}
	return sortPath, false
}

// parseSortDirection resolves the optional sorting direction.
func parseSortDirection(s string) (desc bool, ok bool) {
	switch strings.TrimSpace(s) {
	case "", "asc", "ascending", "a-z", "az", "a to z", "oldest", "oldest first", "oldest-first", "up":
		return false, true
	case "desc", "descending", "z-a", "za", "z to a", "newest", "newest first", "newest-first", "down":
		return true, true
	}
	return false, false
}

// applySortSetting applies the configured startup sort. An empty value keeps
// the default order.
func (m *model) applySortSetting(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	keys, err := parseSortSpec(value)
	if err != nil {
		return err
	}
	m.sortKeys = keys
	m.applyFilter()
	m.listIdx = clamp(m.listIdx, 0, max(0, len(m.filtered)-1))
	m.scrollList()
	m.rebuildRows()
	return nil
}

// currentSortIndex returns the selector entry matching the active sort, or -1
// when the active sort is a composite.
func (m model) currentSortIndex() int {
	if len(m.sortKeys) != 1 {
		return -1
	}
	for i, mode := range sortModes {
		if mode == m.sortKeys[0] {
			return i
		}
	}
	return -1
}

// sortLabel describes the active sort, including every key of a composite.
func (m model) sortLabel() string {
	if len(m.sortKeys) == 0 {
		return sortModes[0].label()
	}
	labels := make([]string, len(m.sortKeys))
	for i, key := range m.sortKeys {
		labels[i] = key.label()
	}
	return strings.Join(labels, " → ")
}

// openSort shows the sort selector at the current mode.
func (m model) openSort() (tea.Model, tea.Cmd) {
	if m.sortOpen || m.helpOpen || m.confirmDelete || m.mode != modeBrowse ||
		m.searching || m.saving || m.deleting {
		return m, nil
	}
	m.sortOpen = true
	m.sortIdx = m.currentSortIndex()
	if m.sortIdx < 0 {
		m.sortIdx = 0
	}
	m.status = ""
	return m, nil
}

// updateSort handles keys while the sort selector is open.
func (m model) updateSort(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	page := max(1, m.sortPageSize())
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.sortOpen = false
		return m, nil
	case "enter":
		return m.applySort()
	case "up", "k":
		m.sortIdx = clamp(m.sortIdx-1, 0, len(sortModes)-1)
	case "down", "j":
		m.sortIdx = clamp(m.sortIdx+1, 0, len(sortModes)-1)
	case "pgup", "ctrl+u":
		m.sortIdx = clamp(m.sortIdx-page, 0, len(sortModes)-1)
	case "pgdown", "ctrl+d":
		m.sortIdx = clamp(m.sortIdx+page, 0, len(sortModes)-1)
	case "home", "g":
		m.sortIdx = 0
	case "end", "G":
		m.sortIdx = len(sortModes) - 1
	}
	return m, nil
}

// applySort switches to the highlighted mode, reorders the visible books and
// keeps the cursor on the same publication.
func (m model) applySort() (tea.Model, tea.Cmd) {
	if m.sortIdx < 0 || m.sortIdx >= len(sortModes) {
		m.sortOpen = false
		return m, nil
	}
	mode := sortModes[m.sortIdx]
	var focus string
	if e, ok := m.selected(); ok {
		focus = e.Path
	}

	m.sortOpen = false
	m.sortKeys = []sortMode{mode}
	m.applyFilter()
	m.focusPath(focus)
	m.rebuildRows()
	m.setStatus("sorted by "+mode.label(), statusInfo)
	return m, nil
}

// focusPath moves the list cursor to path, or clamps it when the path is no
// longer visible under the current filter.
func (m *model) focusPath(path string) {
	m.listIdx = 0
	if path != "" {
		for i, idx := range m.filtered {
			if m.entries[idx].Path == path {
				m.listIdx = i
				break
			}
		}
	}
	m.listIdx = clamp(m.listIdx, 0, max(0, len(m.filtered)-1))
	m.scrollList()
}

func (m model) sortPageSize() int {
	height := m.bodyHeight()
	if height < 4 {
		return 0
	}
	return height - 3 // border and title
}

// viewSort renders the sort selector as a centered dialog.
func (m model) viewSort() string {
	height := m.bodyHeight()
	if height < 4 || m.width < 16 {
		return ellipsis("Sort by · ↑↓ enter esc", m.width)
	}
	page := m.sortPageSize()
	if page <= 0 {
		return ellipsis("Sort by · ↑↓ enter esc", m.width)
	}

	contentW := clamp(m.width-6, 10, 60)
	top := clamp(m.sortIdx-page+1, 0, max(0, len(sortModes)-page))
	title := "Sort by"
	if len(sortModes) > page {
		end := min(top+page, len(sortModes))
		title = fmt.Sprintf("Sort by  %d–%d of %d", top+1, end, len(sortModes))
	}
	lines := []string{helpTitleStyle.Render(padRight(ellipsis(title, contentW), contentW))}

	current := m.currentSortIndex()
	for i := top; i < min(top+page, len(sortModes)); i++ {
		label := "  " + sortModes[i].label()
		if i == current {
			label += "  ✓"
		}
		plain := padRight(ellipsis(label, contentW), contentW)
		if i == m.sortIdx {
			lines = append(lines, rowSelStyle.Render(plain))
		} else {
			lines = append(lines, valueStyle.Render(plain))
		}
	}

	box := helpBoxStyle.Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, box)
}

// lessEntry is the ordering used by the Books pane. It compares each sort key
// in turn, then falls back to the file path for a deterministic order.
func (m model) lessEntry(a, b library.Entry) bool {
	for _, key := range m.sortKeys {
		cmp, decided := compareEntryKey(a, b, key)
		if decided {
			return cmp < 0
		}
	}
	return compareText(a.Path, b.Path) < 0
}

// compareEntryKey compares one sort key. Missing values always sort last, in
// either direction. The bool reports whether this key decided the order.
func compareEntryKey(a, b library.Entry, key sortMode) (int, bool) {
	av, bv := sortValue(a, key.field), sortValue(b, key.field)
	if av == "" || bv == "" {
		if av == bv {
			return 0, false
		}
		if av != "" {
			return -1, true
		}
		return 1, true
	}

	cmp := compareEntries(a, b, key.field)
	if key.desc {
		cmp = -cmp
	}
	if cmp == 0 {
		return 0, false
	}
	return cmp, true
}

func compareEntries(a, b library.Entry, field sortField) int {
	if field == sortSeries {
		// Series name is primary, episode number is secondary, and title is
		// the final tie-breaker.
		if c := compareText(a.Metadata.Series, b.Metadata.Series); c != 0 {
			return c
		}
		if c := compareSeriesIndex(a.Metadata.SeriesIndex, b.Metadata.SeriesIndex); c != 0 {
			return c
		}
		return compareText(a.Title(), b.Title())
	}
	return compareText(sortValue(a, field), sortValue(b, field))
}

func sortValue(e library.Entry, field sortField) string {
	switch field {
	case sortTitle:
		return e.Title()
	case sortAuthor:
		return e.Metadata.AuthorString()
	case sortSeries:
		return e.Metadata.Series
	case sortDate:
		return e.Metadata.Date
	case sortLanguage:
		return e.Metadata.Language
	}
	return e.Path
}

// compareText is a case-insensitive alphabetical comparison with a
// case-sensitive tie-break so ordering is deterministic.
func compareText(a, b string) int {
	la, lb := strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	switch {
	case la < lb:
		return -1
	case la > lb:
		return 1
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// compareSeriesIndex orders numeric series positions correctly ("2" before
// "10"), falling back to text when either value is not a number.
func compareSeriesIndex(a, b string) int {
	af, aerr := strconv.ParseFloat(strings.TrimSpace(a), 64)
	bf, berr := strconv.ParseFloat(strings.TrimSpace(b), 64)
	switch {
	case aerr == nil && berr == nil:
		switch {
		case af < bf:
			return -1
		case af > bf:
			return 1
		}
		return 0
	case aerr == nil:
		return -1
	case berr == nil:
		return 1
	}
	return compareText(a, b)
}
