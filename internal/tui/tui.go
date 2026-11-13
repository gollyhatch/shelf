package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/gollyhatch/shelf/internal/epub"
	"github.com/gollyhatch/shelf/internal/library"
	"github.com/gollyhatch/shelf/internal/online"
)

// Run starts the interactive browser for a directory.
func Run(dir, viewer, googleBooksAPIKey, libraryPath, sortSpec, searchURL string, renameName, moveName func(library.Entry) (string, error)) error {
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	m := newModel(abs, nil)
	m.viewer = viewer
	m.searchURL = searchURL
	client := online.Client{APIKey: googleBooksAPIKey}
	m.lookupOnline = client.Lookup
	m.renameName = renameName
	m.libraryPath = libraryPath
	m.moveName = moveName
	if err := m.applySortSetting(sortSpec); err != nil {
		return fmt.Errorf("config: sort: %w", err)
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

// RunFile starts the browser on a single file, which is the quickest way to fix
// one book.
func RunFile(path, viewer, googleBooksAPIKey, libraryPath, sortSpec, searchURL string, renameName, moveName func(library.Entry) (string, error)) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	m := newModel(filepath.Dir(abs), []library.Entry{library.LoadOne(abs)})
	m.viewer = viewer
	m.searchURL = searchURL
	client := online.Client{APIKey: googleBooksAPIKey}
	m.lookupOnline = client.Lookup
	m.renameName = renameName
	m.libraryPath = libraryPath
	m.moveName = moveName
	if err := m.applySortSetting(sortSpec); err != nil {
		return fmt.Errorf("config: sort: %w", err)
	}
	m.rebuildRows()
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

type pane int

const (
	paneList pane = iota
	paneDetail
)

type mode int

const (
	modeBrowse mode = iota
	modeEdit
)

type readFilter int

const (
	readFilterAll readFilter = iota
	readFilterUnread
	readFilterRead
)

type rowKind int

const (
	rowField   rowKind = iota // a plain metadata field
	rowCreator                // one author
	rowSubject                // one tag
	rowAddCreator
	rowAddSubject
)

// maxSuggestions is how many completion candidates fit under the editor input.
const maxSuggestions = 5

// row is one editable line in the detail pane.
type row struct {
	kind  rowKind
	field epub.Field
	index int
	label string
	value string
	// mixed marks a bulk row whose selected books do not share one value.
	mixed bool
}

type statusKind int

const (
	statusInfo statusKind = iota
	statusOK
	statusErr
)

type model struct {
	dir    string
	viewer string
	// searchURL is the configured browser search pattern, if any.
	searchURL string
	// openBrowser launches a URL; tests replace it to avoid spawning xdg-open.
	openBrowser func(string) error
	entries     []library.Entry
	listIdx     int

	rows   []row
	rowIdx int

	pane pane
	mode mode

	// editor
	editRow   row
	editAdd   bool
	input     textinput.Model
	area      textarea.Model
	usingArea bool

	width, height int
	loading       bool
	fatal         error
	status        string
	statusKnd     statusKind

	listTop int
	rowTop  int

	// search
	search    textinput.Model
	searching bool
	filter    string
	filtered  []int // indices into entries, after the filter and sort

	// sorting
	sortKeys []sortMode
	sortOpen bool
	sortIdx  int

	// autocomplete
	suggestions      []string
	suggestionIndex  int
	showSuggestions  bool
	authorCandidates []string
	seriesCandidates []string

	// multi-select and bulk editing
	sel      map[int]bool // selected entry indices (into entries, not filtered)
	editBulk bool         // the open editor writes to every selected book
	saving   bool         // a bulk save is in flight; browse keys are ignored

	// read/unread filter
	readFilter readFilter

	// deletion
	confirmDelete bool     // the confirmation dialog is open
	deletePaths   []string // files the dialog will remove
	deleting      bool     // a delete is in flight; browse keys are ignored
	repairing     bool     // an EPUB repair is in flight

	// online metadata lookup
	lookupOnline func(epub.Metadata) (epub.Metadata, error)
	lookingUp    bool
	onlineOpen   bool
	onlinePath   string
	onlineFound  epub.Metadata
	onlineFields []onlineField
	onlineIdx    int

	// rename
	renameName func(library.Entry) (string, error)

	// move to the configured library
	libraryPath string
	moveName    func(library.Entry) (string, error)

	// help
	helpOpen bool
	helpTop  int

	loadTotal int
	loadIndex int
	loadPath  string
}

type loadedMsg struct {
	entries []library.Entry
	err     error
}

type savedMsg struct {
	path string
	meta epub.Metadata
	err  error
}

// deletedMsg reports the outcome of a delete batch.
type deletedMsg struct {
	removed []string
	errs    []string
}

// bulkBatchMsg is the state of an in-flight bulk save: it names the files to
// write and everything needed to write the next one.
type bulkBatchMsg struct {
	paths []string
	idx   int // the file this command writes
	field epub.Field
	value string
	add   bool // append instead of replace (author/tags only)
	ok    int
	errs  []string
}

// bulkSavedMsg reports one finished file of a bulk save. The batch, advanced
// by one, is attached so the model can schedule the next write.
type bulkSavedMsg struct {
	batch bulkBatchMsg
	path  string
	meta  epub.Metadata
	err   error
}

func newModel(dir string, entries []library.Entry) model {
	ti := textinput.New()
	ti.Prompt = "› "
	ti.PromptStyle = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	ti.TextStyle = valueStyle
	ti.PlaceholderStyle = emptyStyle
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(colAccent)

	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.Placeholder = "A short description of the book…"
	ta.FocusedStyle.Base = valueStyle
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Placeholder = emptyStyle
	ta.BlurredStyle.Base = valueStyle

	si := textinput.New()
	si.Prompt = "⌕ "
	si.PromptStyle = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	si.TextStyle = valueStyle
	si.PlaceholderStyle = emptyStyle
	si.Placeholder = "title, author, tag… (field:term)"
	si.Cursor.Style = lipgloss.NewStyle().Foreground(colAccent)

	m := model{
		dir:          dir,
		entries:      entries,
		input:        ti,
		area:         ta,
		search:       si,
		loading:      entries == nil,
		loadTotal:    -1,
		sortKeys:     []sortMode{{field: sortPath}},
		sel:          make(map[int]bool),
		lookupOnline: online.Lookup,
		openBrowser:  openInBrowser,
	}
	if entries == nil {
		m.loadIndex = 0
		m.loadTotal = 0
		m.loadPath = ""
	}
	m.restyleEditors()
	m.applyFilter()
	m.buildCandidates()
	return m
}

func loadCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		paths, err := library.Discover(dir, true)
		if err != nil {
			return loadedMsg{err: err}
		}
		if len(paths) == 0 {
			return loadedMsg{entries: nil}
		}
		return loadBatchMsg{paths: paths}
	}
}

// loadBatchMsg carries the scan state across the per-file load commands; the
// model schedules the next step each time it sees one.
type loadBatchMsg struct {
	paths   []string
	entries []library.Entry
}

// loadOneCmd reads the next path in the batch.
func loadOneCmd(batch loadBatchMsg) tea.Cmd {
	return func() tea.Msg {
		i := len(batch.entries)
		batch.entries = append(batch.entries, library.LoadOne(batch.paths[i]))
		return batch
	}
}

func (m model) Init() tea.Cmd {
	if m.loading {
		return loadCmd(m.dir)
	}
	return nil
}

func (m model) selected() (library.Entry, bool) {
	if m.listIdx >= 0 && m.listIdx < len(m.filtered) {
		return m.entries[m.filtered[m.listIdx]], true
	}
	return library.Entry{}, false
}

// visible returns the entries currently shown in the list pane, after the
// search filter.
func (m model) visible() []library.Entry {
	out := make([]library.Entry, 0, len(m.filtered))
	for _, i := range m.filtered {
		out = append(out, m.entries[i])
	}
	return out
}

// ---------- search ----------

// applyFilter recomputes the visible set from the current query.
func (m *model) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.filter))
	m.filtered = m.filtered[:0]
	for i := range m.entries {
		if q != "" && !entryMatches(m.entries[i], q) {
			continue
		}
		if !m.readFilter.Matches(m.entries[i].Metadata.Read) {
			continue
		}
		m.filtered = append(m.filtered, i)
	}
	sort.SliceStable(m.filtered, func(i, j int) bool {
		return m.lessEntry(m.entries[m.filtered[i]], m.entries[m.filtered[j]])
	})
}

// buildCandidates collects the authors and series of every loaded book, once
// each and in alphabetical order, so the editor can complete against the shelf.
func (m *model) buildCandidates() {
	m.authorCandidates = nil
	m.seriesCandidates = nil
	seenAuthors := make(map[string]struct{})
	seenSeries := make(map[string]struct{})
	for _, e := range m.entries {
		for _, c := range e.Metadata.Creators {
			if name := strings.TrimSpace(c.Name); name != "" {
				key := strings.ToLower(name)
				if _, ok := seenAuthors[key]; !ok {
					seenAuthors[key] = struct{}{}
					m.authorCandidates = append(m.authorCandidates, name)
				}
			}
		}
		if s := strings.TrimSpace(e.Metadata.Series); s != "" {
			key := strings.ToLower(s)
			if _, ok := seenSeries[key]; !ok {
				seenSeries[key] = struct{}{}
				m.seriesCandidates = append(m.seriesCandidates, s)
			}
		}
	}
	sort.Slice(m.authorCandidates, func(i, j int) bool {
		return strings.ToLower(m.authorCandidates[i]) < strings.ToLower(m.authorCandidates[j])
	})
	sort.Slice(m.seriesCandidates, func(i, j int) bool {
		return strings.ToLower(m.seriesCandidates[i]) < strings.ToLower(m.seriesCandidates[j])
	})
}

// candidatesFor returns the completion candidates for the field being edited.
// Authors and series complete against the whole shelf; every other field is
// edited as plain text.
func (m model) candidatesFor(r row) []string {
	switch r.field {
	case epub.FieldAuthor:
		return m.authorCandidates
	case epub.FieldSeries:
		return m.seriesCandidates
	}
	return nil
}

// refreshSuggestions recomputes the completion list for the editor from the
// candidates of every loaded book, filtered by what has been typed so far.
// Prefix matches come first, then substring matches. A candidate equal to the
// input is dropped: the input already is that value, so there is nothing to
// complete (this also closes the list after accepting a suggestion).
func (m *model) refreshSuggestions() {
	m.closeSuggestions()
	if m.usingArea {
		return
	}
	q := strings.ToLower(strings.TrimSpace(m.input.Value()))
	var prefix, rest []string
	for _, c := range m.candidatesFor(m.editRow) {
		lc := strings.ToLower(c)
		switch {
		case lc == q:
			// the input already is this candidate; nothing to complete
		case q == "" || strings.HasPrefix(lc, q):
			prefix = append(prefix, c)
		case strings.Contains(lc, q):
			rest = append(rest, c)
		}
	}
	m.suggestions = append(prefix, rest...)
	if len(m.suggestions) == 0 {
		return
	}
	m.suggestionIndex = clamp(m.suggestionIndex, 0, len(m.suggestions)-1)
	m.showSuggestions = true
}

// closeSuggestions dismisses the completion list.
func (m *model) closeSuggestions() {
	m.suggestions = nil
	m.showSuggestions = false
	m.suggestionIndex = 0
}

// moveSuggestion moves the completion highlight, wrapping around.
func (m *model) moveSuggestion(delta int) {
	if n := len(m.suggestions); n > 0 {
		m.suggestionIndex = (m.suggestionIndex + delta + n) % n
	}
}

// acceptSuggestion fills the input with the highlighted suggestion.
func (m *model) acceptSuggestion() {
	if !m.showSuggestions {
		return
	}
	m.input.SetValue(m.suggestions[m.suggestionIndex])
	m.input.CursorEnd()
	m.refreshSuggestions()
}

// ---------- multi-select ----------

// toggleSelect flips the multi-select state of the book under the cursor.
func (m *model) toggleSelect() {
	if m.listIdx < 0 || m.listIdx >= len(m.filtered) {
		return
	}
	idx := m.filtered[m.listIdx]
	if m.sel[idx] {
		delete(m.sel, idx)
	} else {
		m.sel[idx] = true
	}
}

// clearSelection drops every selected book.
func (m *model) clearSelection() {
	m.sel = make(map[int]bool)
}

// selectAllVisible selects every book currently visible in the list, i.e. the
// books that pass the active search filter and sort. Books hidden by the
// filter are dropped from the selection.
func (m *model) selectAllVisible() {
	m.sel = make(map[int]bool, len(m.filtered))
	for _, i := range m.filtered {
		m.sel[i] = true
	}
}

// selectedEntries returns the selected books in list order. Selection is kept
// across filtering: the set holds entry indices, not list positions, so a
// selected book stays selected — and stays part of a bulk edit — even while
// the search hides it.
func (m model) selectedEntries() []library.Entry {
	out := make([]library.Entry, 0, len(m.sel))
	for i := range m.entries {
		if m.sel[i] {
			out = append(out, m.entries[i])
		}
	}
	return out
}

// bulkFields are the only fields editable across a multi-selection: author,
// tags, series, publisher, language and rights.
var bulkFields = []epub.Field{
	epub.FieldAuthor, epub.FieldTags, epub.FieldSeries,
	epub.FieldPublisher, epub.FieldLanguage, epub.FieldRights, epub.FieldRead,
}

// bulkActive reports whether a bulk edit is in progress, i.e. enough books are
// selected for it.
func (m model) bulkActive() bool { return len(m.sel) >= 2 }

// buildBulkRows lays out the bulk editor: one aggregated row per bulk field.
// A row carries the shared value when every selected book agrees, otherwise it
// is marked mixed and starts editing from an empty input.
func buildBulkRows(sel []library.Entry) []row {
	rows := make([]row, 0, len(bulkFields))
	for _, f := range bulkFields {
		r := row{kind: rowField, field: f, label: f.Label()}
		if len(sel) > 0 {
			v := sel[0].Metadata.Get(f)
			same := true
			for _, e := range sel[1:] {
				if e.Metadata.Get(f) != v {
					same = false
					break
				}
			}
			if same {
				r.value = v
			} else {
				r.mixed = true
			}
		}
		rows = append(rows, r)
	}
	return rows
}

// setFilter changes the query and resets the list cursor to the top.
func (m *model) setFilter(q string) {
	m.filter = q
	m.applyFilter()
	m.listIdx = 0
	m.listTop = 0
	m.rowIdx = 0
	m.rowTop = 0
	m.rebuildRows()
}

// clearFilter drops the active query and empties the search box, so the next
// search starts with a blank input. It also clears the read-status filter.
func (m *model) clearFilter() {
	m.search.SetValue("")
	m.readFilter = readFilterAll
	m.setFilter("")
}

// Matches reports whether a book with the given read status passes the filter.
func (f readFilter) Matches(read bool) bool {
	switch f {
	case readFilterRead:
		return read
	case readFilterUnread:
		return !read
	}
	return true
}

// toggleReadFilter cycles the dedicated read filter: pressing the active key
// again returns to all books.
func (m *model) toggleReadFilter(f readFilter) {
	if m.readFilter == f {
		m.readFilter = readFilterAll
	} else {
		m.readFilter = f
	}
	m.applyFilter()
	m.listIdx = 0
	m.listTop = 0
	m.rowIdx = 0
	m.rowTop = 0
	m.rebuildRows()
}

// searchTerm is one whitespace-separated token. Quoted reports whether any
// part of it was quoted, so a literal "or" is not treated as an operator.
type searchTerm struct {
	text   string
	quoted bool
}

// searchClause is one term of a query. An empty field searches every field.
type searchClause struct {
	field  string
	value  string
	op     string
	negate bool
}

// entryMatches reports whether an entry matches a lowercased query. Terms
// within a group are ANDed; groups separated by OR are ORed.
func entryMatches(e library.Entry, q string) bool {
	groups := parseSearchQuery(q)
	if len(groups) == 0 {
		return true
	}
	for _, group := range groups {
		if entryMatchesGroup(e, group) {
			return true
		}
	}
	return false
}

func entryMatchesGroup(e library.Entry, group []searchClause) bool {
	for _, clause := range group {
		matched := entryMatchesClause(e, clause)
		if clause.negate {
			matched = !matched
		}
		if !matched {
			return false
		}
	}
	return true
}

// parseSearchQuery splits a query into OR-separated groups of ANDed clauses.
// Double quotes group a value containing spaces, e.g. author:"le guin". A
// leading "-" negates a term. A colon only becomes a field prefix when the
// name is known.
func parseSearchQuery(q string) [][]searchClause {
	terms := splitSearchTerms(strings.ToLower(strings.TrimSpace(q)))
	var groups [][]searchClause
	var group []searchClause
	for _, term := range terms {
		if !term.quoted && term.text == "or" {
			if len(group) > 0 {
				groups = append(groups, group)
				group = nil
			}
			continue
		}
		group = append(group, parseSearchClause(term.text))
	}
	if len(group) > 0 {
		groups = append(groups, group)
	}
	return groups
}

func parseSearchClause(term string) searchClause {
	clause := searchClause{}
	if strings.HasPrefix(term, "-") && len(term) > 1 {
		clause.negate = true
		term = term[1:]
	}

	prefix, value, ok := strings.Cut(term, ":")
	if ok {
		prefix = strings.TrimSpace(prefix)
		value = strings.TrimSpace(value)
		if knownSearchField(prefix) {
			clause.field = prefix
			clause.op, clause.value = splitComparison(value)
			return clause
		}
	}
	clause.value = term
	return clause
}

// splitComparison peels a comparison operator from a field value.
func splitComparison(value string) (op, rest string) {
	for _, candidate := range []string{">=", "<=", "!=", "=", ">", "<"} {
		if strings.HasPrefix(value, candidate) {
			return candidate, strings.TrimSpace(value[len(candidate):])
		}
	}
	return "", value
}

// splitSearchTerms splits on whitespace outside double quotes and removes the
// quotes, so quoted phrases stay one term. A backslash escapes a quote or
// another backslash.
func splitSearchTerms(q string) []searchTerm {
	var terms []searchTerm
	var b strings.Builder
	inQuote, quoted := false, false
	flush := func() {
		if b.Len() > 0 {
			terms = append(terms, searchTerm{text: b.String(), quoted: quoted})
			b.Reset()
			quoted = false
		}
	}
	runes := []rune(q)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\\' && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\'):
			i++
			b.WriteRune(runes[i])
		case r == '"':
			inQuote = !inQuote
			quoted = true
		case unicode.IsSpace(r) && !inQuote:
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return terms
}

// knownSearchField reports whether name is a field prefix the search supports.
func knownSearchField(name string) bool {
	switch name {
	case "file", "filename", "path":
		return true
	}
	_, ok := epub.ParseField(name)
	return ok
}

// entryMatchesClause checks one parsed term.
func entryMatchesClause(e library.Entry, clause searchClause) bool {
	if clause.field == "" {
		return entryMatchesAny(e, clause.value)
	}
	matched, known := entryFieldMatches(e, clause.field, clause.value, clause.op)
	if !known {
		return entryMatchesAny(e, clause.value)
	}
	return matched
}

// entryFieldMatches restricts a query to one field. The metadata field names
// accept the same aliases as the CLI, plus file/file name and path. An empty
// query matches every book where the field has a non-empty value.
func entryFieldMatches(e library.Entry, prefix, q, op string) (matched, known bool) {
	switch prefix {
	case "file", "filename":
		return matchField(filepath.Base(e.Path), q, op), true
	case "path":
		return matchField(e.Path, q, op), true
	}

	field, ok := epub.ParseField(prefix)
	if !ok {
		return false, false
	}
	if field == epub.FieldRead {
		if op != "" {
			return compareValues(e.Metadata.Get(epub.FieldRead), q, op), true
		}
		return matchReadStatus(e.Metadata.Read, q), true
	}
	value := e.Metadata.Get(field)
	if field == epub.FieldTitle {
		// The list falls back to the file name when a title is missing.
		value = e.Title()
	}
	return matchField(value, q, op), true
}

// matchReadStatus handles read: queries. An empty query lists read books,
// mirroring the way field: lists books where the field is set.
func matchReadStatus(read bool, q string) bool {
	switch strings.ToLower(strings.TrimSpace(q)) {
	case "", "yes", "true", "1", "y", "read", "finished", "done":
		return read
	case "no", "false", "0", "n", "unread", "not-read", "not read":
		return !read
	}
	return false
}

// matchField reports whether value matches the lowercased query. With an empty
// query, it reports whether the field is non-empty. With a comparison operator,
// the values are compared numerically when possible and as text otherwise.
func matchField(value, q, op string) bool {
	if op != "" {
		return compareValues(value, q, op)
	}
	if q == "" {
		return strings.TrimSpace(value) != ""
	}
	return containsFold(value, q)
}

// compareValues applies a comparison operator to two field values.
func compareValues(value, target, op string) bool {
	a, b := strings.TrimSpace(value), strings.TrimSpace(target)
	if af, aerr := strconv.ParseFloat(a, 64); aerr == nil {
		if bf, berr := strconv.ParseFloat(b, 64); berr == nil {
			switch {
			case af < bf:
				return op == "<" || op == "<=" || op == "!="
			case af > bf:
				return op == ">" || op == ">=" || op == "!="
			}
			return op == "=" || op == "<=" || op == ">="
		}
	}

	cmp := strings.Compare(strings.ToLower(a), strings.ToLower(b))
	switch op {
	case "=":
		return cmp == 0
	case "!=":
		return cmp != 0
	case "<":
		return cmp < 0
	case "<=":
		return cmp <= 0
	case ">":
		return cmp > 0
	case ">=":
		return cmp >= 0
	}
	return false
}

// entryMatchesAny is the unprefixed search across the fields shown in the list.
func entryMatchesAny(e library.Entry, q string) bool {
	m := e.Metadata
	fields := []string{
		e.Title(),
		m.AuthorString(),
		m.Series,
		strings.Join(m.Subjects, " "),
		m.Description,
		filepath.Base(e.Path),
	}
	for _, f := range fields {
		if containsFold(f, q) {
			return true
		}
	}
	return false
}

// containsFold reports whether value contains the already lowercased needle.
func containsFold(value, needle string) bool {
	return strings.Contains(strings.ToLower(value), needle)
}

// startSearch focuses the search box.
func (m model) startSearch() (tea.Model, tea.Cmd) {
	m.pane = paneList
	m.searching = true
	m.search.CursorEnd()
	return m, m.search.Focus()
}

// updateSearch handles keys while the search box has focus.
func (m model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.searching = false
		m.search.Blur()
		m.clearFilter()
		return m, nil
	case "enter":
		m.searching = false
		m.search.Blur()
		return m, nil // keep the filter, hand focus back to the list
	}
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(msg)
	m.setFilter(m.search.Value())
	return m, cmd
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resizeEditors()
		m.scrollList()
		return m, nil

	case loadedMsg:
		m.loading = false
		if msg.err != nil {
			m.fatal = msg.err
			return m, nil
		}
		m.entries = msg.entries
		m.listIdx = 0
		m.listTop = 0
		m.clearSelection()
		m.applyFilter()
		m.buildCandidates()
		m.rebuildRows()
		m.refreshSuggestions()
		m.loadPath = ""
		m.loadTotal = len(m.entries)
		m.loadIndex = len(m.entries)
		return m, nil

	case loadBatchMsg:
		if !m.loading {
			return m, nil
		}
		m.loadTotal = len(msg.paths)
		m.loadIndex = len(msg.entries)
		// Publish each book as soon as it is read so the list fills while the
		// rest of the directory is still being scanned. Entries already known
		// are kept as they are, so an edit made during the scan is not
		// overwritten by the loader's older copy. A shorter batch means a new
		// scan started, so adopt it wholesale.
		if len(msg.entries) < len(m.entries) {
			m.entries = msg.entries
		} else {
			m.entries = append(m.entries, msg.entries[len(m.entries):]...)
		}
		m.applyFilter()
		m.buildCandidates()
		m.rebuildRows()
		if m.loadIndex < m.loadTotal {
			m.loadPath = msg.paths[m.loadIndex]
			return m, loadOneCmd(msg)
		}
		m.loading = false
		m.loadPath = ""
		return m, nil

	case savedMsg:
		if msg.err != nil {
			m.setStatus(msg.err.Error(), statusErr)
			return m, nil
		}
		// Update the saved book even if the cursor moved while the save was in
		// flight, then re-apply the filter and the active sort.
		for i := range m.entries {
			if m.entries[i].Path == msg.path {
				m.entries[i].Metadata = msg.meta
				m.entries[i].Err = nil
				break
			}
		}
		m.applyFilter()
		m.focusPath(msg.path)
		m.buildCandidates()
		m.rebuildRows()
		m.refreshSuggestions()
		m.setStatus("saved", statusOK)
		return m, nil

	case bulkSavedMsg:
		b := msg.batch
		// Failures are already collected in the batch; the loop keeps going so
		// the remaining books still get the edit.
		if msg.err == nil {
			for i := range m.entries {
				if m.entries[i].Path == msg.path {
					m.entries[i].Metadata = msg.meta
					m.entries[i].Err = nil
					break
				}
			}
		}
		if b.idx < len(b.paths) {
			m.setStatus(fmt.Sprintf("saving %d/%d", b.idx+1, len(b.paths)), statusInfo)
			return m, bulkSaveCmd(b)
		}
		m.saving = false
		m.applyFilter()
		// The edit can drop a book out of the active search: keep the cursor
		// inside the list.
		m.listIdx = clamp(m.listIdx, 0, max(0, len(m.filtered)-1))
		m.scrollList()
		m.buildCandidates()
		m.rebuildRows()
		m.refreshSuggestions()
		if n := len(b.errs); n > 0 {
			m.setStatus(fmt.Sprintf("saved %d of %d books (%d failed)",
				b.ok, len(b.paths), n), statusErr)
		} else {
			m.setStatus(fmt.Sprintf("saved %d books", b.ok), statusOK)
		}
		return m, nil

	case deletedMsg:
		m.deleting = false
		if len(msg.removed) > 0 {
			m.removeEntries(msg.removed)
		}
		total := len(msg.removed) + len(msg.errs)
		switch {
		case total == 0:
			m.setStatus("nothing to delete", statusInfo)
		case len(msg.removed) == 0:
			m.setStatus("could not delete "+fileCount(total), statusErr)
		case len(msg.errs) > 0:
			m.setStatus(fmt.Sprintf("deleted %d of %s", len(msg.removed), fileCount(total)), statusErr)
		default:
			m.setStatus("deleted "+fileCount(len(msg.removed)), statusOK)
		}
		return m, nil

	case viewerDoneMsg:
		if msg.err != nil {
			m.setStatus("viewer: "+msg.err.Error(), statusErr)
		} else {
			m.status = ""
		}
		return m, nil

	case browserDoneMsg:
		if msg.err != nil {
			m.setStatus("browser: "+msg.err.Error(), statusErr)
		} else {
			m.status = ""
		}
		return m, nil

	case repairedMsg:
		m.repairing = false
		if msg.err != nil {
			m.setStatus("repair: "+msg.err.Error(), statusErr)
			return m, nil
		}
		for i := range m.entries {
			if m.entries[i].Path == msg.path {
				m.entries[i].Metadata = msg.meta
				m.entries[i].Err = msg.loadErr
				break
			}
		}
		m.applyFilter()
		m.focusPath(msg.path)
		m.buildCandidates()
		m.rebuildRows()
		m.refreshSuggestions()
		switch {
		case msg.loadErr != nil:
			m.setStatus("still unreadable after repair", statusErr)
		case msg.changed:
			m.setStatus("repaired", statusOK)
		default:
			m.setStatus("metadata reloaded", statusOK)
		}
		return m, nil

	case onlineResultMsg:
		m.lookingUp = false
		if msg.err != nil {
			m.setStatus("online: "+msg.err.Error(), statusErr)
			return m, nil
		}
		fields := compareOnlineMetadata(msg.current, msg.found)
		if len(fields) == 0 {
			m.setStatus("online metadata matches this book", statusOK)
			return m, nil
		}
		m.onlineOpen = true
		m.onlinePath = msg.path
		m.onlineFound = msg.found
		m.onlineFields = fields
		m.onlineIdx = 0
		m.status = ""
		return m, nil

	case tea.KeyMsg:
		if m.mode == modeEdit {
			return m.updateEdit(msg)
		}
		return m.updateBrowse(msg)
	}
	return m, nil
}

func (m *model) setStatus(text string, kind statusKind) {
	m.status = text
	m.statusKnd = kind
}

// ---------- browsing ----------

func (m model) updateBrowse(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searching {
		return m.updateSearch(msg)
	}
	if m.helpOpen {
		return m.updateHelp(msg)
	}
	if m.confirmDelete {
		return m.updateConfirmDelete(msg)
	}
	if m.sortOpen {
		return m.updateSort(msg)
	}
	if m.onlineOpen {
		return m.updateOnline(msg)
	}
	// While a bulk save, delete, repair, or online lookup is in flight the
	// status line reports progress; only quitting is accepted until it ends.
	if m.saving || m.deleting || m.repairing || m.lookingUp {
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		return m, nil
	}
	m.status = ""
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "r":
		return m.repairSelected()
	case "m":
		return m.lookupSelectedMetadata()
	case "x":
		return m.toggleRead()
	case "N":
		return m.renameSelected()
	case "M":
		return m.moveSelectedToLibrary()
	case "D":
		return m.handleDeleteFiles()
	case "S":
		return m.openSort()
	case "?":
		return m.openHelp()
	case "ctrl+t":
		m.cycleTheme()
		return m, nil
	case "ctrl+o":
		return m.openViewer()
	case "ctrl+k":
		return m.openSearchURL()
	case "u":
		m.toggleReadFilter(readFilterUnread)
		return m, nil
	case "U":
		m.toggleReadFilter(readFilterRead)
		return m, nil
	case "/":
		return m.startSearch()
	case "ctrl+a":
		m.selectAllVisible()
		m.rebuildRows()
		return m, nil
	case "R":
		if m.loading {
			return m, nil // a scan is already in flight
		}
		m.loading = true
		m.closeSuggestions()
		return m, loadCmd(m.dir)
	case "tab", "shift+tab":
		if m.pane == paneList {
			m.pane = paneDetail
		} else {
			m.pane = paneList
		}
		m.closeSuggestions()
		return m, nil
	}

	if m.pane == paneList {
		switch msg.String() {
		case "up", "k":
			m.moveList(-1)
		case "down", "j":
			m.moveList(1)
		case "K":
			m.moveListGroup(-1)
		case "J":
			m.moveListGroup(1)
		case "pgup", "ctrl+u":
			m.moveList(-m.listPageSize())
		case "pgdown", "ctrl+d":
			m.moveList(m.listPageSize())
		case "home", "g":
			m.moveList(-len(m.filtered))
		case "end", "G":
			m.moveList(len(m.filtered))
		case " ":
			// Toggle the multi-select state of the book under the cursor and
			// move to the next book, so a run of spaces selects consecutive
			// books.
			m.toggleSelect()
			m.rebuildRows()
			m.moveList(1)
		case "esc":
			// Leave bulk mode before dropping the search.
			if len(m.sel) > 0 {
				m.clearSelection()
				m.rebuildRows()
			} else if m.filter != "" || m.readFilter != readFilterAll {
				m.clearFilter()
			}
		case "right", "l", "enter", "e":
			m.pane = paneDetail
		}
		return m, nil
	}

	switch msg.String() {
	case "up", "k":
		m.moveRow(-1)
	case "down", "j":
		m.moveRow(1)
	case "pgup", "ctrl+u":
		m.moveRow(-m.rowPageSize())
	case "pgdown", "ctrl+d":
		m.moveRow(m.rowPageSize())
	case "home", "g":
		m.moveRow(-len(m.rows))
	case "end", "G":
		m.moveRow(len(m.rows))
	case "left", "h", "esc":
		m.pane = paneList
	case "enter", "e":
		return m.startEdit(false)
	case " ":
		if r, ok := m.currentRow(); ok && r.field == epub.FieldRead {
			return m.toggleRead()
		}
	case "a", "n":
		return m.startEdit(true)
	case "d", "delete", "backspace":
		return m.deleteRow()
	}
	return m, nil
}

// updateConfirmDelete handles keys while the delete confirmation dialog is
// open. Only an explicit confirmation removes anything.
func (m model) updateConfirmDelete(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		return m.confirmDeleteNow()
	case "n", "N", "esc", "q", "ctrl+c":
		m.confirmDelete = false
		m.deletePaths = nil
	}
	return m, nil
}

// handleDeleteFiles asks for confirmation before deleting the selected books,
// or the book under the cursor when nothing is multi-selected.
func (m model) handleDeleteFiles() (tea.Model, tea.Cmd) {
	if m.confirmDelete || m.mode != modeBrowse || m.searching || m.saving || m.deleting || m.loading {
		return m, nil
	}
	targets := m.deleteTargets()
	if len(targets) == 0 {
		return m, nil
	}
	paths := make([]string, 0, len(targets))
	for _, e := range targets {
		paths = append(paths, e.Path)
	}
	m.confirmDelete = true
	m.deletePaths = paths
	m.status = ""
	return m, nil
}

// deleteTargets returns the files a delete would remove: every multi-selected
// book when there is a selection, otherwise the book under the cursor.
func (m model) deleteTargets() []library.Entry {
	if len(m.sel) > 0 {
		return m.selectedEntries()
	}
	if e, ok := m.selected(); ok {
		return []library.Entry{e}
	}
	return nil
}

// toggleRead marks the cursor book, or the whole multi-selection, read when
// any of the targets is unread and unread when all are read.
func (m model) toggleRead() (tea.Model, tea.Cmd) {
	targets := m.deleteTargets()
	if len(targets) == 0 {
		return m, nil
	}
	markRead := false
	for _, e := range targets {
		if !e.Metadata.Read {
			markRead = true
			break
		}
	}
	value := "no"
	if markRead {
		value = "yes"
	}

	if len(m.sel) > 0 {
		return m, m.startBulkSave(epub.FieldRead, value, false)
	}
	e, ok := m.selected()
	if !ok {
		return m, nil
	}
	m.setStatus("updating read status…", statusInfo)
	return m, saveCmd(e.Path, func(p *epub.Package) error {
		return p.Set(epub.FieldRead, value)
	})
}

// confirmDeleteNow closes the dialog and starts removing the files in the
// background, so the interface stays responsive for large selections.
func (m model) confirmDeleteNow() (tea.Model, tea.Cmd) {
	paths := m.deletePaths
	m.confirmDelete = false
	m.deletePaths = nil
	if len(paths) == 0 {
		return m, nil
	}
	m.deleting = true
	m.setStatus("deleting "+fileCount(len(paths))+"…", statusInfo)
	return m, deleteFilesCmd(paths)
}

// removeEntries drops the successfully deleted files from the list. The
// selection is cleared because removing entries shifts its indices.
func (m *model) removeEntries(paths []string) {
	gone := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		gone[p] = struct{}{}
	}
	kept := m.entries[:0]
	for _, e := range m.entries {
		if _, ok := gone[e.Path]; ok {
			continue
		}
		kept = append(kept, e)
	}
	m.entries = kept
	m.clearSelection()
	m.applyFilter()
	m.listIdx = clamp(m.listIdx, 0, max(0, len(m.filtered)-1))
	m.rowIdx = 0
	m.rowTop = 0
	m.buildCandidates()
	m.rebuildRows()
	m.refreshSuggestions()
	m.scrollList()
}

func (m *model) moveList(delta int) {
	if len(m.filtered) == 0 {
		return
	}
	next := clamp(m.listIdx+delta, 0, len(m.filtered)-1)
	if next == m.listIdx {
		return
	}
	m.listIdx = next
	// In bulk mode the detail pane shows the shared bulk rows, which do not
	// follow the list cursor, so the row cursor stays where it is.
	if !m.bulkActive() {
		m.rowIdx = 0
		m.rowTop = 0
	}
	m.rebuildRows()
	m.scrollList()
}

// moveListGroup jumps in delta direction to the next book whose series or
// author differs from the current book. It stays put when no such book exists.
func (m *model) moveListGroup(delta int) {
	if delta == 0 || len(m.filtered) == 0 {
		return
	}
	if m.listIdx < 0 || m.listIdx >= len(m.filtered) {
		return
	}
	current := m.entries[m.filtered[m.listIdx]]
	for i := m.listIdx + delta; i >= 0 && i < len(m.filtered); i += delta {
		if !sameBookGroup(current, m.entries[m.filtered[i]]) {
			m.listIdx = i
			if !m.bulkActive() {
				m.rowIdx = 0
				m.rowTop = 0
			}
			m.rebuildRows()
			m.scrollList()
			return
		}
	}
}

// sameBookGroup reports whether two books share both their series and author,
// so group navigation skips from one run of them to the next.
func sameBookGroup(a, b library.Entry) bool {
	return strings.EqualFold(strings.TrimSpace(a.Metadata.Series), strings.TrimSpace(b.Metadata.Series)) &&
		strings.EqualFold(strings.TrimSpace(a.Metadata.AuthorString()), strings.TrimSpace(b.Metadata.AuthorString()))
}

func (m *model) scrollList() {
	items := m.visible()
	if len(items) == 0 {
		m.listTop = 0
		return
	}
	m.listTop = adjustListTop(m.listTop, m.listIdx, entryHeights(items), m.listContentHeight())
}

// entryHeights returns the number of lines each list entry occupies.
func entryHeights(items []library.Entry) []int {
	heights := make([]int, len(items))
	for i, e := range items {
		heights[i] = 2
		if e.Err == nil && e.SeriesLine() != "" {
			heights[i] = 3
		}
	}
	return heights
}

// adjustListTop returns the first visible entry so that the selected entry is
// visible within height lines.
func adjustListTop(top, selected int, heights []int, height int) int {
	if len(heights) == 0 {
		return 0
	}
	top = clamp(top, 0, len(heights)-1)
	selected = clamp(selected, 0, len(heights)-1)
	if selected < top {
		top = selected
	}
	height = max(1, height)
	for top < selected {
		used := 0
		for i := top; i <= selected; i++ {
			used += heights[i]
		}
		if used <= height {
			break
		}
		top++
	}
	return top
}

func (m *model) moveRow(delta int) {
	if len(m.rows) == 0 {
		return
	}
	m.rowIdx = clamp(m.rowIdx+delta, 0, len(m.rows)-1)
	page := m.rowPageSize()
	m.rowTop = clamp(m.rowTop, 0, max(0, len(m.rows)-page))
	if m.rowIdx < m.rowTop {
		m.rowTop = m.rowIdx
	}
	if m.rowIdx >= m.rowTop+page {
		m.rowTop = m.rowIdx - page + 1
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// rebuildRows reconstructs the detail pane and tries to keep the cursor on the
// same logical row. With a bulk selection active the pane holds the shared
// bulk rows instead of the cursor book's rows.
func (m *model) rebuildRows() {
	var keep row
	tracked := m.rowIdx >= 0 && m.rowIdx < len(m.rows)
	if tracked {
		keep = m.rows[m.rowIdx]
	}

	if m.bulkActive() {
		m.rows = buildBulkRows(m.selectedEntries())
	} else {
		e, ok := m.selected()
		if !ok {
			m.rows = nil
			m.rowIdx = 0
			m.rowTop = 0
			return
		}
		m.rows = buildRows(e.Metadata)
	}

	if tracked {
		for i, r := range m.rows {
			if r.kind == keep.kind && r.field == keep.field && r.index == keep.index {
				m.rowIdx = i
				return
			}
		}
	}
	m.rowIdx = clamp(m.rowIdx, 0, max(0, len(m.rows)-1))
}

// buildRows lays out the detail pane: plain fields, one row per author and tag,
// each group followed by an "add" row.
func buildRows(m epub.Metadata) []row {
	var rows []row
	rows = append(rows,
		row{kind: rowField, field: epub.FieldTitle, label: epub.FieldTitle.Label(), value: m.Title},
		row{kind: rowField, field: epub.FieldSubtitle, label: epub.FieldSubtitle.Label(), value: m.Subtitle},
	)

	rows = append(rows, row{kind: rowField, field: epub.FieldAuthor, label: epub.FieldAuthor.Label(),
		value: strings.Join(m.CreatorNames(), "; ")})
	for i, c := range m.Creators {
		rows = append(rows, row{kind: rowCreator, field: epub.FieldAuthor, index: i, label: c.Display()})
	}
	rows = append(rows, row{kind: rowAddCreator, field: epub.FieldAuthor, label: "author", index: len(m.Creators)})

	rows = append(rows, row{kind: rowField, field: epub.FieldTags, label: epub.FieldTags.Label(),
		value: strings.Join(m.Subjects, ", ")})
	for i, s := range m.Subjects {
		rows = append(rows, row{kind: rowSubject, field: epub.FieldTags, index: i, label: s})
	}
	rows = append(rows, row{kind: rowAddSubject, field: epub.FieldTags, label: "tag", index: len(m.Subjects)})

	rows = append(rows,
		row{kind: rowField, field: epub.FieldSeries, label: epub.FieldSeries.Label(), value: m.Series},
		row{kind: rowField, field: epub.FieldSeriesIndex, label: epub.FieldSeriesIndex.Label(), value: m.SeriesIndex},
	)

	for _, f := range []epub.Field{
		epub.FieldPublisher, epub.FieldDate, epub.FieldLanguage,
		epub.FieldIdentifier, epub.FieldRights, epub.FieldRead, epub.FieldDescription,
	} {
		rows = append(rows, row{kind: rowField, field: f, label: f.Label(), value: m.Get(f)})
	}
	return rows
}

// ---------- editing ----------

func (m model) currentRow() (row, bool) {
	if m.rowIdx >= 0 && m.rowIdx < len(m.rows) {
		return m.rows[m.rowIdx], true
	}
	return row{}, false
}

// editingNewCreator reports whether the editor is adding an author instead of
// editing the one under the cursor.
func (m model) editingNewCreator() bool {
	return m.editRow.kind == rowAddCreator ||
		(m.editAdd && m.editRow.field == epub.FieldAuthor)
}

// editingNewSubject reports whether the editor is adding a tag instead of
// editing the one under the cursor.
func (m model) editingNewSubject() bool {
	return m.editRow.kind == rowAddSubject ||
		(m.editAdd && m.editRow.field == epub.FieldTags)
}

func (m model) startEdit(add bool) (tea.Model, tea.Cmd) {
	r, ok := m.currentRow()
	if !ok {
		return m, nil
	}
	// Read status is a checkbox, not a text value: toggling it edits the field
	// directly on this book or on the whole bulk selection.
	if r.field == epub.FieldRead {
		if add {
			return m, nil
		}
		return m.toggleRead()
	}
	// "add" only applies to the multi-valued groups.
	add = add && (r.field == epub.FieldAuthor || r.field == epub.FieldTags)

	m.editRow = r
	m.editAdd = add
	// A bulk selection makes the editor write to every selected book.
	m.editBulk = m.bulkActive()

	initial := r.value
	switch {
	case add:
		initial = ""
	case r.kind == rowCreator:
		initial = r.label // the plain author name
	}

	m.mode = modeEdit
	m.usingArea = !add && r.kind == rowField && r.field == epub.FieldDescription
	m.resizeEditors()

	if m.usingArea {
		m.area.SetValue(initial)
		m.area.CursorEnd()
		m.refreshSuggestions()
		return m, m.area.Focus()
	}
	m.input.SetValue(initial)
	m.input.CursorEnd()
	m.refreshSuggestions()
	return m, m.input.Focus()
}

func (m *model) resizeEditors() {
	w := m.width - 8
	w = clamp(w, 20, 160)
	m.input.Width = w
	m.area.SetWidth(w)
	m.area.SetHeight(clamp(m.height/3, 3, 8))
	m.search.Width = clamp(m.width-14, 8, 120)
}

// restyleEditors re-applies the active theme to the text inputs and textarea,
// which store their styles inside the model.
func (m *model) restyleEditors() {
	m.input.PromptStyle = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	m.input.TextStyle = valueStyle
	m.input.PlaceholderStyle = emptyStyle
	m.input.Cursor.Style = lipgloss.NewStyle().Foreground(colAccent)

	m.area.FocusedStyle.Base = valueStyle
	m.area.FocusedStyle.CursorLine = lipgloss.NewStyle()
	m.area.FocusedStyle.Placeholder = emptyStyle
	m.area.BlurredStyle.Base = valueStyle

	m.search.PromptStyle = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	m.search.TextStyle = valueStyle
	m.search.PlaceholderStyle = emptyStyle
	m.search.Cursor.Style = lipgloss.NewStyle().Foreground(colAccent)
}

func (m model) updateEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeBrowse
		m.editBulk = false
		m.input.Blur()
		m.area.Blur()
		m.closeSuggestions()
		return m, nil
	case "ctrl+s", "alt+enter":
		return m.commitEdit()
	case "enter":
		if !m.usingArea {
			return m.commitEdit()
		}
		// In the description editor enter inserts a newline.
	case "tab":
		if m.showSuggestions {
			m.acceptSuggestion()
			return m, nil
		}
	case "down", "ctrl+n":
		if m.showSuggestions {
			m.moveSuggestion(1)
			return m, nil
		}
	case "up", "ctrl+p":
		if m.showSuggestions {
			m.moveSuggestion(-1)
			return m, nil
		}
	}

	var cmd tea.Cmd
	if m.usingArea {
		m.area, cmd = m.area.Update(msg)
	} else {
		m.input, cmd = m.input.Update(msg)
		// A keystroke restarts the completion list at the best match.
		m.refreshSuggestions()
	}
	return m, cmd
}

func (m model) editValue() string {
	if m.usingArea {
		return m.area.Value()
	}
	return m.input.Value()
}

func (m model) commitEdit() (tea.Model, tea.Cmd) {
	if m.editBulk {
		return m.commitBulkEdit()
	}
	e, ok := m.selected()
	if !ok {
		m.mode = modeBrowse
		return m, nil
	}
	value := m.editValue()
	r := m.editRow

	apply := func(p *epub.Package) error {
		meta := p.Metadata()
		switch {
		// "add" wins over the row kinds: pressing a/n on an existing author or
		// tag appends to the group instead of replacing the row under the
		// cursor.
		case m.editingNewCreator():
			creators := append([]epub.Creator{}, meta.Creators...)
			return p.SetCreators(append(creators, epub.ParseCreatorList(value)...))

		case m.editingNewSubject():
			subjects := append([]string{}, meta.Subjects...)
			return p.SetSubjects(append(subjects, epub.SplitList(value, ",")...))

		case r.kind == rowCreator:
			creators := append([]epub.Creator{}, meta.Creators...)
			if r.index < len(creators) {
				creators[r.index] = epub.ParseCreator(value)
			}
			return p.SetCreators(creators)

		case r.kind == rowSubject:
			subjects := append([]string{}, meta.Subjects...)
			if r.index < len(subjects) {
				subjects[r.index] = strings.TrimSpace(value)
			}
			return p.SetSubjects(subjects)

		default:
			return p.Set(r.field, value)
		}
	}

	m.mode = modeBrowse
	m.input.Blur()
	m.area.Blur()
	m.closeSuggestions()
	return m, saveCmd(e.Path, apply)
}

func saveCmd(path string, apply func(*epub.Package) error) tea.Cmd {
	return func() tea.Msg {
		var updated epub.Metadata
		err := epub.Update(path, func(p *epub.Package) error {
			if err := apply(p); err != nil {
				return err
			}
			updated = p.Metadata()
			return nil
		})
		return savedMsg{path: path, meta: updated, err: err}
	}
}

// deleteFilesCmd removes the given files, reporting which ones are gone and
// which ones could not be deleted.
func deleteFilesCmd(paths []string) tea.Cmd {
	return func() tea.Msg {
		msg := deletedMsg{}
		for _, p := range paths {
			if err := os.Remove(p); err != nil {
				msg.errs = append(msg.errs, fmt.Sprintf("%s: %v", p, err))
				continue
			}
			msg.removed = append(msg.removed, p)
		}
		return msg
	}
}

// fileCount renders a file count with the right plural.
func fileCount(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

// commitBulkEdit closes the editor and starts writing the edited field to
// every selected book, one file at a time.
func (m model) commitBulkEdit() (tea.Model, tea.Cmd) {
	value := m.editValue()
	r := m.editRow
	add := m.editAdd

	m.mode = modeBrowse
	m.editBulk = false
	m.input.Blur()
	m.area.Blur()
	m.closeSuggestions()

	empty := strings.TrimSpace(value) == ""
	// A mixed row starts editing from an empty input: leaving it empty means
	// nothing was typed, not "clear every book" (that is what d is for). The
	// same applies to an add of nothing.
	if empty && (add || r.mixed || r.value == "") {
		return m, nil
	}
	cmd := m.startBulkSave(r.field, value, add)
	return m, cmd
}

// startBulkSave queues the chained writes of one field across the selection.
// Unreadable files are skipped; the reader is told when nothing could be
// written.
func (m *model) startBulkSave(field epub.Field, value string, add bool) tea.Cmd {
	var paths []string
	for _, e := range m.selectedEntries() {
		if e.Err != nil {
			continue
		}
		paths = append(paths, e.Path)
	}
	if len(paths) == 0 {
		m.setStatus("no readable books selected", statusErr)
		return nil
	}
	m.saving = true
	m.setStatus(fmt.Sprintf("saving 1/%d", len(paths)), statusInfo)
	return bulkSaveCmd(bulkBatchMsg{paths: paths, field: field, value: value, add: add})
}

// bulkSaveCmd writes the next file of a bulk batch and reports the result.
func bulkSaveCmd(b bulkBatchMsg) tea.Cmd {
	return func() tea.Msg {
		path := b.paths[b.idx]
		var updated epub.Metadata
		err := epub.Update(path, func(p *epub.Package) error {
			if err := p.SetWith(b.field, b.value, b.add); err != nil {
				return err
			}
			updated = p.Metadata()
			return nil
		})
		if err != nil {
			b.errs = append(b.errs, path+": "+err.Error())
		} else {
			b.ok++
		}
		b.idx++
		return bulkSavedMsg{batch: b, path: path, meta: updated, err: err}
	}
}

func (m model) deleteRow() (tea.Model, tea.Cmd) {
	r, rok := m.currentRow()
	if !rok {
		return m, nil
	}
	if r.field == epub.FieldRead {
		return m, nil
	}

	// In bulk mode every row is a plain field: clearing it clears the field on
	// all selected books.
	if m.bulkActive() {
		if r.value == "" && !r.mixed {
			return m, nil
		}
		cmd := m.startBulkSave(r.field, "", false)
		return m, cmd
	}

	e, ok := m.selected()
	if !ok {
		return m, nil
	}

	switch r.kind {
	case rowAddCreator, rowAddSubject:
		return m, nil

	case rowCreator:
		return m, saveCmd(e.Path, func(p *epub.Package) error {
			creators := append([]epub.Creator{}, p.Metadata().Creators...)
			if r.index < len(creators) {
				creators = append(creators[:r.index], creators[r.index+1:]...)
			}
			return p.SetCreators(creators)
		})

	case rowSubject:
		return m, saveCmd(e.Path, func(p *epub.Package) error {
			subjects := append([]string{}, p.Metadata().Subjects...)
			if r.index < len(subjects) {
				subjects = append(subjects[:r.index], subjects[r.index+1:]...)
			}
			return p.SetSubjects(subjects)
		})

	default:
		if r.value == "" {
			return m, nil
		}
		return m, saveCmd(e.Path, func(p *epub.Package) error { return p.Set(r.field, "") })
	}
}

// ---------- rendering ----------

func (m model) View() string {
	if m.fatal != nil {
		return "\n  " + errStyle.Render("error: ") + m.fatal.Error() +
			"\n\n  " + helpStyle.Render("q to quit") + "\n"
	}
	if m.width == 0 {
		return "loading…"
	}

	parts := []string{m.viewHeader()}
	if s := m.viewSearch(); s != "" {
		parts = append(parts, s)
	}
	if m.mode == modeEdit {
		if b := m.viewBody(); b != "" {
			parts = append(parts, "", b)
		}
		parts = append(parts, "", m.viewEditor())
	} else {
		parts = append(parts, "", m.viewBody())
		if m.status != "" {
			parts = append(parts, m.viewStatus())
		} else {
			parts = append(parts, "", m.viewFooter())
		}
	}
	return clipLines(strings.Join(parts, "\n"), m.height)
}

// clipLines is a last-resort guard so a view can never exceed the terminal.
func clipLines(s string, height int) string {
	if height <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= height {
		return s
	}
	return strings.Join(lines[:height], "\n")
}

func (m model) viewHeader() string {
	brand := brandStyle.Render("shelf")
	count := "0 books"
	shown := len(m.filtered)
	switch n := shown; {
	case n == 1:
		count = "1 book"
	case n > 1:
		count = fmt.Sprintf("%d books", n)
	}
	if m.filter != "" || m.readFilter != readFilterAll {
		count = fmt.Sprintf("%d of %d books", shown, len(m.entries))
	}
	left := brand + "  " + headerInfoStyle.Render(count)
	if m.readFilter != readFilterAll {
		label := " · read only"
		if m.readFilter == readFilterUnread {
			label = " · unread only"
		}
		badge := headerInfoStyle.Render(label)
		if lipgloss.Width(left+badge) <= m.width {
			left += badge
		}
	}
	// The selection count only joins the header when it fits; a wrapped header
	// line would break the layout.
	if n := len(m.sel); n > 0 {
		sel := headerInfoStyle.Render(fmt.Sprintf(" · %d selected", n))
		if lipgloss.Width(left+sel) <= m.width {
			left += sel
		}
	}
	if m.loading {
		loadingText := "loading…"
		if m.loadTotal > 0 {
			if m.loadIndex == 0 {
				loadingText = fmt.Sprintf("scanning… (%d)", m.loadTotal)
				if m.loadPath != "" {
					loadingText = fmt.Sprintf("scanning… %s (%d)", ellipsis(filepath.Base(m.loadPath), max(8, m.width/6)), m.loadTotal)
				}
			} else {
				loadingText = fmt.Sprintf("loading %d/%d", m.loadIndex, m.loadTotal)
			}
		}
		if m.loadPath != "" {
			loadingText += " " + ellipsis(filepath.Base(m.loadPath), max(10, m.width/4))
		}
		left += "  " + headerInfoStyle.Render(loadingText)
	}
	right := headerInfoStyle.Render(ellipsis(m.dir, max(10, m.width/2)))
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

// viewSearch renders the search bar. While typing it is the live input; once a
// filter is applied it shows the query with a hint.
func (m model) viewSearch() string {
	if m.searching {
		return m.search.View()
	}
	if m.filter == "" {
		return ""
	}
	return searchLabelStyle.Render(" search ") +
		valueStyle.Render(ellipsis(m.filter, max(8, m.width-14)))
}

func (m model) viewStatus() string {
	switch m.statusKnd {
	case statusErr:
		return errStyle.Render("✗ " + m.status)
	case statusOK:
		return okStyle.Render("✓ " + m.status)
	default:
		return helpStyle.Render(m.status)
	}
}

// bodyHeight derives the height of the panes from the space the fixed parts of
// the view actually occupy, so the layout can never overflow the terminal. It
// returns 0 when there is no room for the panes at all (which only happens in
// the editor on very short terminals).
func (m model) bodyHeight() int {
	fixed := 1 + 1 // header + blank line before the body
	if m.viewSearch() != "" {
		fixed++
	}
	switch {
	case m.mode == modeEdit:
		fixed += 1 + countLines(m.viewEditor()) // blank + editor
		avail := m.height - fixed
		if avail < 6 { // a bordered pane needs at least three lines
			return 0
		}
		return avail
	case m.status != "":
		fixed++ // status line
	default:
		fixed += 2 // blank + footer
	}
	return max(m.height-fixed, 0)
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// paneHeights returns the outer height of each pane. Below 84 columns the panes
// are stacked vertically.
func (m model) paneHeights() (listH, detailH int, stacked bool) {
	avail := m.bodyHeight()
	if m.width >= 84 {
		return avail, avail, false
	}
	if avail <= 0 {
		return 0, 0, true
	}
	list := clamp(avail/3, 4, 12)
	if list > avail-3 {
		list = max(3, avail-3)
	}
	return list, avail - list, true
}

func (m model) listPageSize() int {
	listH, _, _ := m.paneHeights()
	return max(1, (listH-3)/2) // minus border and title, two lines per book
}

// listContentHeight is the number of lines available inside the Books pane.
func (m model) listContentHeight() int {
	listH, _, _ := m.paneHeights()
	return max(1, listH-3)
}

func (m model) rowPageSize() int {
	_, detailH, _ := m.paneHeights()
	return max(1, detailH-3)
}

func (m model) viewBody() string {
	if m.helpOpen {
		return m.viewHelp()
	}
	if m.confirmDelete {
		return m.viewConfirm()
	}
	if m.sortOpen {
		return m.viewSort()
	}
	if m.onlineOpen {
		return m.viewOnline()
	}
	if m.bodyHeight() <= 0 {
		return ""
	}
	if m.width >= 84 {
		listH, _, _ := m.paneHeights()
		listW := m.width / 3
		detailW := m.width - listW - 1
		return lipgloss.JoinHorizontal(lipgloss.Top,
			m.renderPane(paneList, listW, listH), " ",
			m.renderPane(paneDetail, detailW, listH))
	}
	listH, detailH, _ := m.paneHeights()
	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderPane(paneList, m.width, listH),
		m.renderPane(paneDetail, m.width, detailH))
}

func (m model) viewConfirm() string {
	height := m.bodyHeight()
	if height <= 0 {
		return ""
	}
	count := len(m.deletePaths)
	title := fmt.Sprintf("Delete %s?", fileCount(count))
	if height < 3 {
		return ellipsis(title+" · y confirm · n cancel", m.width)
	}

	contentW := clamp(m.width-6, 4, 60)
	maxLines := height - 2 // the box border
	lines := []string{confirmTitleStyle.Render(padRight(ellipsis(title, contentW), contentW))}
	if maxLines >= 2 {
		// Reserve the last line for the key hint and show as many names as fit.
		slots := min(maxLines-2, 3)
		switch {
		case slots <= 0:
		case count <= slots:
			for _, p := range m.deletePaths {
				lines = append(lines, valueStyle.Render(padRight(ellipsis(filepath.Base(p), contentW), contentW)))
			}
		case slots == 1:
			line := filepath.Base(m.deletePaths[0]) + fmt.Sprintf(" + %d more", count-1)
			lines = append(lines, valueStyle.Render(padRight(ellipsis(line, contentW), contentW)))
		default:
			for _, p := range m.deletePaths[:slots-1] {
				lines = append(lines, valueStyle.Render(padRight(ellipsis(filepath.Base(p), contentW), contentW)))
			}
			more := fmt.Sprintf("and %d more", count-(slots-1))
			lines = append(lines, emptyStyle.Render(padRight(ellipsis(more, contentW), contentW)))
		}
		hint := "y confirm · n cancel"
		if contentW >= 32 {
			hint = "y/enter confirm · n/esc cancel"
		}
		lines = append(lines, helpStyle.Render(padRight(ellipsis(hint, contentW), contentW)))
	}

	box := confirmBoxStyle.Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, box)
}

func (m model) renderPane(p pane, width, height int) string {
	active := m.pane == p && m.mode != modeEdit
	style := paneStyle
	titleStyle := paneTitleStyle
	if active {
		style = paneActiveStyle
		titleStyle = paneTitleActiveStyle
	}
	contentW := max(4, width-4) // the text area inside the border and padding
	frameW := max(2, width-2)   // the area inside the border (lipgloss Width includes padding)
	contentH := max(1, height-2)

	var title string
	var lines []string
	if p == paneList {
		title = "Books · " + m.sortLabel()
		lines = m.listLines(contentW, contentH-1)
	} else {
		if m.bulkActive() {
			title = fmt.Sprintf("Bulk · %d books", len(m.sel))
		} else if e, ok := m.selected(); ok {
			title = filepath.Base(e.Path)
		}
		// Without a selection there is no header; the pane starts with its
		// own "no book selected" line.
		if title == "" {
			lines = m.detailLines(contentW, contentH, active)
		} else {
			lines = m.detailLines(contentW, contentH-1, active)
		}
	}

	parts := lines
	if title != "" {
		parts = append([]string{titleStyle.Render(ellipsis(title, contentW))}, lines...)
	}
	return style.Width(frameW).Height(contentH).Render(strings.Join(parts, "\n"))
}

func (m model) listLines(width, height int) []string {
	items := m.visible()
	if len(items) == 0 {
		if m.filter != "" {
			return []string{emptyStyle.Render("no matches")}
		}
		// While scanning, the header carries the progress; the list stays
		// blank until the first book is ready.
		if m.loading {
			return nil
		}
		return []string{emptyStyle.Render("no EPUB files here")}
	}

	// Entries take two lines, or three when they carry series information.
	heights := entryHeights(items)
	top := adjustListTop(m.listTop, m.listIdx, heights, height)

	// The checkbox column appears as soon as anything is selected, so the
	// plain list keeps its full width when no bulk edit is pending.
	checkboxes := len(m.sel) > 0
	indent := 2
	if checkboxes {
		indent = 4 // "▌ " + "✓ "
	}

	var out []string
	for i := top; i < len(items) && len(out) < height; i++ {
		e := items[i]
		selected := i == m.listIdx
		checked := checkboxes && m.sel[m.filtered[i]]
		lines := m.listEntryLines(e, selected, checked, checkboxes, width, indent)
		for _, line := range lines {
			if len(out) >= height {
				break
			}
			out = append(out, line)
		}
	}
	return out
}

// listEntryLines renders one book as a title line, an optional series line,
// and an author line.
func (m model) listEntryLines(e library.Entry, selected, checked, checkboxes bool, width, indent int) []string {
	check := ""
	if checkboxes {
		check = "  "
		if checked {
			check = checkStyle.Render("✓") + " "
		}
	}

	title := e.Title()
	sub := e.Author()
	if sub == "" {
		sub = filepath.Base(e.Path)
	}
	series := ""
	if e.Err == nil {
		series = e.SeriesLine()
	} else {
		title = "! " + title
		sub = "unreadable metadata"
	}

	// A read book gets a small check mark at the end of its title line. Reserve
	// room for it before truncating the title.
	readMark := ""
	titleWidth := width - indent
	if e.Err == nil && e.Metadata.Read && titleWidth > 2 {
		titleWidth -= 2
		readMark = " " + checkStyle.Render("✓")
	}

	if selected {
		readable := e.Err == nil
		bar := itemSelBarStyle.Render("▌ ")
		titleStyle := itemSelStyle
		if !readable {
			titleStyle = badStyle
		}
		lines := []string{bar + check + titleStyle.Render(ellipsis(title, titleWidth)) + readMark}
		if series != "" {
			lines = append(lines, bar+strings.Repeat(" ", indent-2)+itemSelSeriesStyle.Render(ellipsis(series, width-indent)))
		}
		// The cursor bar runs on in front of the author, which stays aligned
		// with the title text.
		lines = append(lines, bar+strings.Repeat(" ", indent-2)+entrySubStyle(true, readable).Render(ellipsis(sub, width-indent)))
		return lines
	}

	titleStyle := itemStyle
	if e.Err != nil {
		titleStyle = badStyle
	}
	lines := []string{strings.Repeat(" ", 2) + check + titleStyle.Render(ellipsis(title, titleWidth)) + readMark}
	if series != "" {
		lines = append(lines, strings.Repeat(" ", indent)+itemSeriesStyle.Render(ellipsis(series, width-indent)))
	}
	lines = append(lines, strings.Repeat(" ", indent)+entrySubStyle(false, e.Err == nil).Render(ellipsis(sub, width-indent)))
	return lines
}

// entrySubStyle returns the style for the author line of a list entry. A
// selected, readable entry gets the secondary highlight colour so its author
// stays distinct from the accent-coloured title.
func entrySubStyle(selected, readable bool) lipgloss.Style {
	if selected && readable {
		return itemSelAuthorStyle
	}
	return itemAuthorStyle
}

func (m model) detailLines(width, height int, active bool) []string {
	// Bulk rows describe the whole selection, so the cursor book's own state
	// (unreadable, nothing selected) does not gate them.
	if !m.bulkActive() {
		e, ok := m.selected()
		if !ok {
			return []string{emptyStyle.Render("no book selected")}
		}
		if e.Err != nil {
			return []string{
				badStyle.Render("could not read the metadata of this file"),
				"",
				errStyle.Render(ellipsis(e.Err.Error(), width)),
				"",
				helpStyle.Render(ellipsis("r repairs the EPUB · R rescans the directory", width)),
			}
		}
	}
	if len(m.rows) == 0 {
		return []string{emptyStyle.Render("no metadata")}
	}

	labelW := 12 // wide enough for "Description:"
	heights := make([]int, len(m.rows))
	for i, r := range m.rows {
		heights[i] = len(m.renderRow(r, width, labelW, false))
	}

	// Keep the selected row visible even when earlier rows (notably a wrapped
	// description) occupy several lines.
	top := clamp(m.rowTop, 0, len(m.rows)-1)
	if m.rowIdx < top {
		top = m.rowIdx
	}
	if m.rowIdx >= 0 && m.rowIdx < len(m.rows) {
		for top < m.rowIdx {
			used := 0
			for i := top; i <= m.rowIdx; i++ {
				used += heights[i]
			}
			if used <= height {
				break
			}
			top++
		}
	}

	var out []string
	for i := top; i < len(m.rows) && len(out) < height; i++ {
		lines := m.renderRow(m.rows[i], width, labelW, i == m.rowIdx && active)
		for _, line := range lines {
			if len(out) >= height {
				break
			}
			out = append(out, line)
		}
	}
	return out
}

// renderReadCheckbox renders the read row as a checkbox. A mixed bulk row is
// shown with a dash and becomes checked when toggled.
func renderReadCheckbox(r row, width, labelW int, selected bool) []string {
	label := r.label + ":"
	mark := "[ ]"
	markStyle := valueStyle
	switch {
	case r.mixed:
		mark = "[-]"
		markStyle = emptyStyle
	case strings.EqualFold(strings.TrimSpace(r.value), "yes"):
		mark = "[x]"
		markStyle = checkStyle
	}

	plain := " " + padRight(ellipsis(label, labelW), labelW) + " " + mark
	if selected {
		return []string{rowSelStyle.Render(padRight(plain, width))}
	}
	left := " " + labelStyle.Render(padRight(ellipsis(label, labelW), labelW))
	return []string{left + " " + markStyle.Render(mark)}
}

func (m model) renderRow(r row, width, labelW int, selected bool) []string {
	if r.kind == rowField && r.field == epub.FieldRead {
		return renderReadCheckbox(r, width, labelW, selected)
	}
	switch r.kind {
	// An author or tag row is content, not a label/value pair: it has no label
	// column and must never be truncated to one.
	case rowCreator, rowSubject:
		plain := "  ↳ " + r.label
		if selected {
			return []string{rowSelStyle.Render(padRight(ellipsis(plain, width), width))}
		}
		return []string{"  " + itemSubStyle.Render("↳") + " " + valueStyle.Render(ellipsis(r.label, width-4))}

	case rowAddCreator, rowAddSubject:
		plain := "  + " + r.label
		if selected {
			return []string{rowSelStyle.Render(padRight(ellipsis(plain, width), width))}
		}
		return []string{addStyle.Render(plain)}
	}

	// A plain field: dimmed label column, then the value. A mixed bulk row has
	// no shared value to show; it states that instead.
	label := r.label + ":"
	if r.kind == rowField && (r.field == epub.FieldTitle || r.field == epub.FieldDescription) && r.value != "" {
		return renderWrappedField(label, r.value, width, labelW, selected)
	}

	plain := " " + padRight(ellipsis(label, labelW), labelW)
	valueW := max(4, width-labelW-3)
	switch {
	case r.mixed:
		plain += " (mixed)"
	case r.value == "":
		plain += " (none)"
	default:
		plain += " " + ellipsis(r.value, valueW)
	}
	if selected {
		return []string{rowSelStyle.Render(padRight(plain, width))}
	}
	left := " " + labelStyle.Render(padRight(ellipsis(label, labelW), labelW))
	if r.mixed {
		return []string{left + " " + emptyStyle.Render("(mixed)")}
	}
	if r.value == "" {
		return []string{left + " " + emptyStyle.Render("(none)")}
	}
	return []string{left + " " + valueStyle.Render(ellipsis(r.value, valueW))}
}

// renderWrappedField lays a multi-line value below its label, indenting the
// continuation lines to the value column.
func renderWrappedField(label, value string, width, labelW int, selected bool) []string {
	valueW := max(4, width-labelW-3)
	wrapped := wrapText(value, valueW)
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}

	prefix := " " + padRight(ellipsis(label, labelW), labelW) + " "
	indent := strings.Repeat(" ", labelW+2)
	lines := make([]string, 0, len(wrapped))
	for i, text := range wrapped {
		plain := indent + text
		if i == 0 {
			plain = prefix + text
		}
		if selected {
			lines = append(lines, rowSelStyle.Render(padRight(plain, width)))
			continue
		}
		if i == 0 {
			left := " " + labelStyle.Render(padRight(ellipsis(label, labelW), labelW)) + " "
			lines = append(lines, left+valueStyle.Render(text))
			continue
		}
		lines = append(lines, indent+valueStyle.Render(text))
	}
	return lines
}

func (m model) viewEditor() string {
	width := clamp(m.width-4, 24, 96)
	contentW := width - 2 // the text area inside the border and padding
	var label string
	switch {
	case m.editingNewCreator():
		label = "Add author"
	case m.editingNewSubject():
		label = "Add tag"
	case m.editRow.kind == rowCreator:
		label = "Edit author"
	case m.editRow.kind == rowSubject:
		label = "Edit tag"
	default:
		label = "Edit " + strings.ToLower(m.editRow.label)
	}
	if m.editBulk {
		label = fmt.Sprintf("%s · %d books", label, len(m.sel))
	}

	hint := "enter save · esc cancel"
	body := m.input.View()
	if m.usingArea {
		hint = "ctrl+s save · esc cancel · enter new line"
		body = m.area.View()
	}
	content := editorLabelStyle.Render(label) + "\n" + body
	if m.showSuggestions {
		hint += " · tab complete · ↑↓ choose"
		content += "\n" + m.viewSuggestions(contentW)
	}
	switch {
	case m.editingNewCreator():
		content += "\n" + editorHintStyle.Render(`separate several authors with ";"`)
	case m.editingNewSubject():
		content += "\n" + editorHintStyle.Render("separate several tags with a comma")
	}
	return editorBoxStyle.Width(width).Render(content) + "\n  " +
		editorHintStyle.Render(ellipsis(hint, max(0, m.width-2)))
}

// viewSuggestions renders the completion list under the editor input, showing
// at most maxSuggestions entries and always keeping the highlighted one in
// view.
func (m model) viewSuggestions(width int) string {
	start := max(0, m.suggestionIndex-maxSuggestions+1)
	end := min(len(m.suggestions), start+maxSuggestions)
	var lines []string
	for i := start; i < end; i++ {
		if i == m.suggestionIndex {
			line := "  ↳ " + m.suggestions[i]
			lines = append(lines, rowSelStyle.Render(padRight(ellipsis(line, width), width)))
			continue
		}
		lines = append(lines, "  "+itemSubStyle.Render("↳")+" "+
			valueStyle.Render(ellipsis(m.suggestions[i], width-4)))
	}
	return strings.Join(lines, "\n")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type binding struct {
	key  string
	desc string
}

func (m model) viewFooter() string {
	var bindings []binding
	switch {
	case m.helpOpen:
		bindings = []binding{
			{"?/esc", "close"}, {"↑↓", "scroll"}, {"pgup/pgdn", "page"},
		}
	case m.confirmDelete:
		bindings = []binding{
			{"y/enter", "delete"}, {"n/esc", "cancel"},
		}
	case m.sortOpen:
		bindings = []binding{
			{"↑↓", "choose"}, {"enter", "apply"}, {"esc", "cancel"},
		}
	case m.onlineOpen:
		bindings = []binding{
			{"space", "toggle"}, {"a", "all"}, {"n", "none"},
			{"enter", "apply"}, {"esc", "cancel"},
		}
	case m.selectedUnreadable() && !m.bulkActive():
		bindings = []binding{
			{"r", "repair"}, {"↑↓", "browse"}, {"tab", "pane"}, {"q", "quit"},
		}
	case m.bulkActive() && m.pane == paneDetail:
		bindings = []binding{
			{"↑↓", "field"}, {"e", "edit"}, {"a", "add"}, {"d", "clear"},
			{"?", "help"}, {"esc", "back"}, {"q", "quit"},
		}
	case m.pane == paneList:
		bindings = []binding{
			{"↑↓", "browse"}, {"space", "select"}, {"/", "search"},
			{"?", "help"}, {"enter", "metadata"}, {"tab", "pane"}, {"q", "quit"},
		}
	default:
		bindings = []binding{
			{"↑↓", "field"}, {"e", "edit"}, {"a", "add"}, {"d", "delete"},
			{"?", "help"}, {"/", "search"}, {"tab", "pane"}, {"q", "quit"},
		}
	}
	return fitBindings(m.width, bindings)
}

// fitBindings renders as many bindings as fit on one line, so that narrow
// terminals never wrap the footer.
func fitBindings(width int, bindings []binding) string {
	sep := keySepStyle.Render(" · ")
	out := ""
	used := 0
	for _, b := range bindings {
		piece := markup(b.key, b.desc)
		add := lipgloss.Width(piece)
		if out != "" {
			add += 3
		}
		if used+add > width && out != "" {
			break
		}
		if out != "" {
			out += sep
			used += 3
		}
		out += piece
		used += lipgloss.Width(piece)
	}
	return out
}

// ---------- text helpers ----------

// ellipsis truncates plain text to width columns and appends an ellipsis.
func ellipsis(s string, width int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	used := 0
	var b strings.Builder
	for _, r := range s {
		w := runewidth.RuneWidth(r)
		if used+w > width-1 {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String() + "…"
}

func padRight(s string, width int) string {
	if w := runewidth.StringWidth(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
