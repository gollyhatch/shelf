package tui

import (
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gollyhatch/shelf/internal/epub"
	"github.com/gollyhatch/shelf/internal/library"
)

// repairedMsg reports the outcome of repairing and re-reading an EPUB.
type repairedMsg struct {
	path    string
	changed bool
	meta    epub.Metadata
	loadErr error
	err     error
}

// repairFileCmd runs the same package-XML repair as "shelf repair" and then
// reloads the book's metadata.
func repairFileCmd(path string) tea.Cmd {
	return func() tea.Msg {
		changed, err := epub.Repair(path, false)
		msg := repairedMsg{path: path, changed: changed, err: err}
		if err != nil {
			return msg
		}
		e := library.LoadOne(path)
		msg.meta = e.Metadata
		msg.loadErr = e.Err
		return msg
	}
}

// repairSelected repairs the book under the cursor when its metadata could not
// be read.
func (m model) repairSelected() (tea.Model, tea.Cmd) {
	if m.repairing {
		return m, nil
	}
	e, ok := m.selected()
	if !ok || e.Err == nil {
		return m, nil
	}
	m.repairing = true
	m.setStatus("repairing "+filepath.Base(e.Path)+"…", statusInfo)
	return m, repairFileCmd(e.Path)
}

// selectedUnreadable reports whether the book under the cursor has unreadable
// metadata, so the footer can offer the repair key.
func (m model) selectedUnreadable() bool {
	e, ok := m.selected()
	return ok && e.Err != nil
}
