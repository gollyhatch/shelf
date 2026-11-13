package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// helpEntry is one line of the keyboard help. An entry with a group is a
// section heading; otherwise keys and desc form a binding row.
type helpEntry struct {
	group string
	keys  string
	desc  string
}

// helpEntries is the complete key map, grouped by context.
var helpEntries = []helpEntry{
	{group: "Browse (both panes)"},
	{keys: "↑/↓, j/k", desc: "move"},
	{keys: "J/K", desc: "next/previous series or author"},
	{keys: "pgup/pgdn", desc: "page up / down"},
	{keys: "ctrl+u/d", desc: "page up / down"},
	{keys: "home/end", desc: "first / last"},
	{keys: "g/G", desc: "first / last"},
	{keys: "tab", desc: "switch pane (shift+tab reverses)"},
	{keys: "/", desc: "search"},
	{keys: "u/U", desc: "show unread / read books only"},
	{keys: "R", desc: "rescan the directory"},
	{keys: "r", desc: "repair an unreadable EPUB"},
	{keys: "m", desc: "look up metadata online"},
	{keys: "x", desc: "mark read / unread"},
	{keys: "N", desc: "rename selected book(s)"},
	{keys: "M", desc: "rename and move selected book(s) into the library"},
	{keys: "D", desc: "delete selected file(s)"},
	{keys: "S", desc: "change the sort order"},
	{keys: "ctrl+o", desc: "open the book in the viewer"},
	{keys: "ctrl+k", desc: "open the configured search URL"},
	{keys: "ctrl+t", desc: "switch theme"},
	{keys: "?", desc: "open this help"},
	{keys: "q/ctrl+c", desc: "quit"},

	{group: "Books pane"},
	{keys: "space", desc: "select / deselect, then move down"},
	{keys: "ctrl+a", desc: "select all visible books"},
	{keys: "→/l", desc: "open metadata (enter/e too)"},
	{keys: "esc", desc: "clear selection / filter"},

	{group: "Metadata pane"},
	{keys: "←/h", desc: "back to books (esc too)"},
	{keys: "enter/e", desc: "edit field"},
	{keys: "a/n", desc: "add author / tag"},
	{keys: "d/delete", desc: "delete author/tag or clear field"},
	{keys: "backspace", desc: "same as d"},

	{group: "Bulk selection"},
	{keys: "enter/e", desc: "edit field on every book"},
	{keys: "a/n", desc: "append author/tag to every book"},
	{keys: "d/delete", desc: "clear field on every book"},
	{keys: "x", desc: "mark every selected book read / unread"},
	{keys: "esc", desc: "clear the selection (books pane)"},

	{group: "Search"},
	{keys: "type", desc: "filter the shelf"},
	{keys: "term term", desc: "combine filters; all must match"},
	{keys: "-term", desc: "negate a filter"},
	{keys: "a OR b", desc: "match either side"},
	{keys: "field:term", desc: "search one field, e.g. title: or author:"},
	{keys: "field:", desc: "find books where that field is set"},
	{keys: "enter", desc: "keep the filter"},
	{keys: "esc/ctrl+c", desc: "clear the filter"},

	{group: "Editor"},
	{keys: "enter", desc: "save; new line in description"},
	{keys: "ctrl+s", desc: "save"},
	{keys: "alt+enter", desc: "save"},
	{keys: "esc/ctrl+c", desc: "cancel"},
	{keys: "tab", desc: "accept suggestion"},
	{keys: "↑/↓", desc: "choose suggestion (ctrl+p/n too)"},

	{group: "Delete confirmation"},
	{keys: "y/enter", desc: "delete"},
	{keys: "n/esc/q", desc: "cancel"},
	{keys: "ctrl+c", desc: "cancel"},

	{group: "Sort selector"},
	{keys: "↑/↓, j/k", desc: "choose"},
	{keys: "enter", desc: "apply the sort"},
	{keys: "esc/q", desc: "cancel"},
	{keys: "pgup/pgdn", desc: "page up / down"},
	{keys: "ctrl+u/d", desc: "page up / down"},
	{keys: "home/end, g/G", desc: "first / last"},

	{group: "Online metadata"},
	{keys: "m", desc: "search online from the selected book"},
	{keys: "↑/↓, j/k", desc: "choose field"},
	{keys: "space", desc: "toggle field"},
	{keys: "a/n", desc: "select all / none"},
	{keys: "enter", desc: "apply selected fields"},
	{keys: "esc/q", desc: "cancel"},
	{keys: "pgup/pgdn", desc: "page up / down"},
	{keys: "ctrl+u/d", desc: "page up / down"},
	{keys: "home/end, g/G", desc: "first / last"},

	{group: "Help"},
	{keys: "?/esc/q", desc: "close"},
	{keys: "↑/↓, j/k", desc: "scroll"},
	{keys: "pgup/pgdn", desc: "page up / down"},
	{keys: "ctrl+u/d", desc: "page up / down"},
	{keys: "home/end", desc: "first / last"},
	{keys: "g/G", desc: "first / last"},
	{keys: "ctrl+c", desc: "quit"},
}

// helpPageSize is the number of binding rows that fit under the dialog title.
func (m model) helpPageSize() int {
	height := m.bodyHeight()
	if height < 4 {
		return 0
	}
	return height - 3 // the border and the title line
}

func (m model) helpMaxTop() int {
	return max(0, len(helpEntries)-m.helpPageSize())
}

// openHelp shows the key map, starting at the top.
func (m model) openHelp() (tea.Model, tea.Cmd) {
	m.helpOpen = true
	m.helpTop = 0
	m.status = ""
	m.closeSuggestions()
	return m, nil
}

// updateHelp handles keys while the help dialog is open. Quitting is only
// possible after closing the dialog, so q and esc are safe ways out of it.
func (m model) updateHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "?", "esc", "q":
		m.helpOpen = false
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		m = m.scrollHelp(-1)
	case "down", "j":
		m = m.scrollHelp(1)
	case "pgup", "ctrl+u":
		m = m.scrollHelp(-max(1, m.helpPageSize()))
	case "pgdown", "ctrl+d":
		m = m.scrollHelp(max(1, m.helpPageSize()))
	case "home", "g":
		m.helpTop = 0
	case "end", "G":
		m.helpTop = m.helpMaxTop()
	}
	return m, nil
}

func (m model) scrollHelp(delta int) model {
	m.helpTop = clamp(m.helpTop+delta, 0, m.helpMaxTop())
	return m
}

// viewHelp renders the scrollable key map as a dialog. The box fills the body
// height so as many bindings as possible are visible.
func (m model) viewHelp() string {
	height := m.bodyHeight()
	if height < 4 || m.width < 16 {
		return ellipsis("Keys · ?/esc close", m.width)
	}

	contentW := clamp(m.width-6, 10, 72)
	page := m.helpPageSize()
	if page <= 0 {
		return ellipsis("Keys · ?/esc close", m.width)
	}
	top := clamp(m.helpTop, 0, m.helpMaxTop())

	title := "Keys"
	if len(helpEntries) > page {
		end := min(top+page, len(helpEntries))
		title = fmt.Sprintf("Keys  %d–%d of %d", top+1, end, len(helpEntries))
	}
	lines := []string{helpTitleStyle.Render(padRight(ellipsis(title, contentW), contentW))}

	keyW := clamp(contentW/2, 5, 14)
	if keyW > contentW-4 {
		keyW = max(1, contentW-4)
	}
	descW := max(1, contentW-3-keyW)

	for i := top; i < min(top+page, len(helpEntries)); i++ {
		e := helpEntries[i]
		if e.group != "" {
			lines = append(lines, helpGroupStyle.Render(padRight(ellipsis(e.group, contentW), contentW)))
			continue
		}
		key := padRight(ellipsis(e.keys, keyW), keyW)
		desc := padRight(ellipsis(e.desc, descW), descW)
		lines = append(lines, "  "+keyStyle.Render(key)+" "+helpStyle.Render(desc))
	}

	box := helpBoxStyle.Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, height, lipgloss.Center, lipgloss.Center, box)
}
