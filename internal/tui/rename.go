package tui

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

// renameSelected renames every multi-selected book, or the book under the
// cursor when nothing is selected, using the configured rename rules.
func (m model) renameSelected() (tea.Model, tea.Cmd) {
	if m.renameName == nil {
		m.setStatus("renaming is unavailable", statusErr)
		return m, nil
	}
	targets := m.deleteTargets()
	if len(targets) == 0 {
		return m, nil
	}

	var currentPath string
	if e, ok := m.selected(); ok {
		currentPath = e.Path
	}

	renamed, unchanged := 0, 0
	var failures int
	for _, e := range targets {
		name, err := m.renameName(e)
		if err != nil || name == "" {
			failures++
			continue
		}
		target := filepath.Join(filepath.Dir(e.Path), name)
		if target == e.Path {
			unchanged++
			continue
		}
		if _, err := os.Stat(target); err == nil {
			failures++
			continue
		}
		if err := os.Rename(e.Path, target); err != nil {
			failures++
			continue
		}
		for i := range m.entries {
			if m.entries[i].Path == e.Path {
				m.entries[i].Path = target
				break
			}
		}
		if currentPath == e.Path {
			currentPath = target
		}
		renamed++
	}

	m.applyFilter()
	m.focusPath(currentPath)
	m.rebuildRows()
	switch {
	case failures > 0:
		m.setStatus(fmt.Sprintf("renamed %d of %d books (%d failed)",
			renamed, len(targets), failures), statusErr)
	case renamed > 0:
		m.setStatus("renamed "+bookCount(renamed), statusOK)
	case unchanged > 0:
		m.setStatus("already named correctly", statusInfo)
	}
	return m, nil
}

func bookCount(n int) string {
	if n == 1 {
		return "1 book"
	}
	return fmt.Sprintf("%d books", n)
}
