package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/gollyhatch/shelf/internal/epub"
)

type onlineField struct {
	field    epub.Field
	current  string
	found    string
	selected bool
}

type onlineResultMsg struct {
	path    string
	current epub.Metadata
	found   epub.Metadata
	err     error
}

var onlineFieldsOrder = []epub.Field{
	epub.FieldTitle,
	epub.FieldSubtitle,
	epub.FieldAuthor,
	epub.FieldTags,
	epub.FieldDescription,
	epub.FieldPublisher,
	epub.FieldDate,
	epub.FieldLanguage,
	epub.FieldIdentifier,
}

// lookupSelectedMetadata starts an asynchronous online lookup for the book
// under the cursor.
func (m model) lookupSelectedMetadata() (tea.Model, tea.Cmd) {
	if m.lookingUp || m.onlineOpen {
		return m, nil
	}
	e, ok := m.selected()
	if !ok {
		return m, nil
	}
	if e.Err != nil {
		m.setStatus("cannot search online: metadata is unreadable", statusErr)
		return m, nil
	}
	if m.lookupOnline == nil {
		m.setStatus("online lookup is unavailable", statusErr)
		return m, nil
	}
	m.lookingUp = true
	m.setStatus("searching online for "+filepathBase(e.Path)+"…", statusInfo)
	return m, onlineLookupCmd(e.Path, e.Metadata, m.lookupOnline)
}

func onlineLookupCmd(path string, current epub.Metadata, lookup func(epub.Metadata) (epub.Metadata, error)) tea.Cmd {
	return func() tea.Msg {
		found, err := lookup(current)
		return onlineResultMsg{path: path, current: current, found: found, err: err}
	}
}

// filepathBase keeps this file independent of path/filepath's error behavior.
func filepathBase(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// compareOnlineMetadata returns the fields where the online result differs
// from the current metadata.
func compareOnlineMetadata(current, found epub.Metadata) []onlineField {
	var fields []onlineField
	for _, field := range onlineFieldsOrder {
		newValue := strings.TrimSpace(found.Get(field))
		if newValue == "" {
			continue
		}
		oldValue := strings.TrimSpace(current.Get(field))
		if oldValue == newValue {
			continue
		}
		fields = append(fields, onlineField{
			field:    field,
			current:  oldValue,
			found:    newValue,
			selected: true,
		})
	}
	return fields
}

func (m model) updateOnline(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	page := max(1, m.onlineListHeight())
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.onlineOpen = false
		m.onlineFields = nil
		return m, nil
	case "enter":
		return m.applyOnline()
	case "up", "k":
		m.onlineIdx = clamp(m.onlineIdx-1, 0, len(m.onlineFields)-1)
	case "down", "j":
		m.onlineIdx = clamp(m.onlineIdx+1, 0, len(m.onlineFields)-1)
	case "pgup", "ctrl+u":
		m.onlineIdx = clamp(m.onlineIdx-page, 0, len(m.onlineFields)-1)
	case "pgdown", "ctrl+d":
		m.onlineIdx = clamp(m.onlineIdx+page, 0, len(m.onlineFields)-1)
	case "home", "g":
		m.onlineIdx = 0
	case "end", "G":
		m.onlineIdx = max(0, len(m.onlineFields)-1)
	case " ":
		if m.onlineIdx >= 0 && m.onlineIdx < len(m.onlineFields) {
			m.onlineFields[m.onlineIdx].selected = !m.onlineFields[m.onlineIdx].selected
		}
	case "a":
		for i := range m.onlineFields {
			m.onlineFields[i].selected = true
		}
	case "n":
		for i := range m.onlineFields {
			m.onlineFields[i].selected = false
		}
	}
	return m, nil
}

// applyOnline saves the selected online fields.
func (m model) applyOnline() (tea.Model, tea.Cmd) {
	if !m.onlineOpen {
		return m, nil
	}
	var selected []onlineField
	for _, field := range m.onlineFields {
		if field.selected {
			selected = append(selected, field)
		}
	}
	path := m.onlinePath
	found := m.onlineFound
	m.onlineOpen = false
	m.onlineFields = nil
	if len(selected) == 0 {
		m.setStatus("no metadata fields selected", statusInfo)
		return m, nil
	}
	m.setStatus("updating metadata…", statusInfo)
	return m, saveCmd(path, func(p *epub.Package) error {
		for _, field := range selected {
			switch field.field {
			case epub.FieldAuthor:
				if err := p.SetCreators(found.Creators); err != nil {
					return err
				}
			case epub.FieldTags:
				if err := p.SetSubjects(found.Subjects); err != nil {
					return err
				}
			default:
				if err := p.Set(field.field, field.found); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (m model) onlineListHeight() int {
	height := m.bodyHeight()
	if height < 6 {
		return 0
	}
	contentH := height - 2
	// The dialog title and the Current/Found column header take one line each.
	return max(0, contentH-2)
}

// onlineColumnWidths splits the dialog into the checkbox/label column and the
// two value columns.
func onlineColumnWidths(contentW int) (labelW, valueW int) {
	const checkW = 4 // "  ✓ "
	labelW = clamp(contentW/5, 8, 14)
	valueW = (contentW - checkW - labelW - 5) / 2
	if valueW < 4 {
		labelW = max(4, contentW/4)
		valueW = (contentW - checkW - labelW - 5) / 2
	}
	return labelW, valueW
}

// viewOnline renders all differing fields at once. The current metadata is in
// the left value column and the online result in the right one; the cursor
// still selects a row for toggling.
func (m model) viewOnline() string {
	height := m.bodyHeight()
	if height < 6 || m.width < 20 || len(m.onlineFields) == 0 {
		return ellipsis("Online metadata · space toggle · enter apply · esc cancel", m.width)
	}
	// Aim for 80% of the terminal width, while never making the dialog
	// narrower than it used to be on smaller screens. The box adds a border
	// and one column of padding on each side.
	contentW := max(clamp(m.width-6, 14, 76), m.width*4/5-4)
	listH := m.onlineListHeight()
	if listH < 1 {
		return ellipsis("Online metadata · space toggle · enter apply · esc cancel", m.width)
	}
	labelW, valueW := onlineColumnWidths(contentW)
	if valueW < 1 {
		return ellipsis("Online metadata · space toggle · enter apply · esc cancel", m.width)
	}

	title := "Online metadata"
	if t := strings.TrimSpace(m.onlineFound.Title); t != "" {
		title += " · " + t
	}
	lines := []string{helpTitleStyle.Render(padRight(ellipsis(title, contentW), contentW))}

	header := strings.Repeat(" ", 4) + padRight("Field", labelW) + "  " +
		padRight("Current", valueW) + " │ " + padRight("Found", valueW)
	lines = append(lines, helpStyle.Render(padRight(ellipsis(header, contentW), contentW)))

	heights := make([]int, len(m.onlineFields))
	for i, field := range m.onlineFields {
		heights[i] = len(onlineRowLines(field, labelW, valueW))
	}
	top := 0
	for top < m.onlineIdx {
		used := 0
		for i := top; i <= m.onlineIdx; i++ {
			used += heights[i]
		}
		if used <= listH {
			break
		}
		top++
	}
	top = clamp(top, 0, max(0, len(m.onlineFields)-1))

	used := 0
	for i := top; i < len(m.onlineFields) && used < listH; i++ {
		field := m.onlineFields[i]
		for _, row := range onlineRowLines(field, labelW, valueW) {
			if used >= listH {
				break
			}
			plain := padRight(row, contentW)
			if i == m.onlineIdx {
				lines = append(lines, rowSelStyle.Render(plain))
			} else {
				lines = append(lines, valueStyle.Render(plain))
			}
			used++
		}
	}
	for used < listH {
		lines = append(lines, strings.Repeat(" ", contentW))
		used++
	}

	box := helpBoxStyle.Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, box)
}

// onlineRowLines lays out one comparison row. Current and found wrap
// independently, so the row grows to the taller of the two columns.
func onlineRowLines(field onlineField, labelW, valueW int) []string {
	current := wrapText(onlineValue(field.current), valueW)
	found := wrapText(onlineValue(field.found), valueW)
	if len(current) == 0 {
		current = []string{""}
	}
	if len(found) == 0 {
		found = []string{""}
	}
	height := max(len(current), len(found))
	lines := make([]string, 0, height)
	for i := 0; i < height; i++ {
		check, label := "    ", strings.Repeat(" ", labelW)
		if i == 0 {
			if field.selected {
				check = "  ✓ "
			}
			label = padRight(ellipsis(field.field.Label(), labelW), labelW)
		}
		cur, fnd := "", ""
		if i < len(current) {
			cur = current[i]
		}
		if i < len(found) {
			fnd = found[i]
		}
		lines = append(lines, check+label+"  "+
			padRight(cur, valueW)+" │ "+padRight(fnd, valueW))
	}
	return lines
}

// onlineValue renders an absent current value for the comparison table.
func onlineValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(none)"
	}
	return value
}
