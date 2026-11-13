package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// moveSelectedToLibrary renames every multi-selected book, or the book under the
// cursor when nothing is selected, and moves it into the configured library.
// It requires an explicit rename_format and a configured library, and books that
// are already inside the library are left where they are.
func (m model) moveSelectedToLibrary() (tea.Model, tea.Cmd) {
	if m.libraryPath == "" {
		m.setStatus("no library configured", statusErr)
		return m, nil
	}
	if m.moveName == nil {
		m.setStatus("rename_format is not configured", statusErr)
		return m, nil
	}
	if m.loading {
		return m, nil
	}
	targets := m.deleteTargets()
	if len(targets) == 0 {
		return m, nil
	}

	libraryDir, err := filepath.Abs(m.libraryPath)
	if err != nil {
		m.setStatus("cannot use the configured library: "+err.Error(), statusErr)
		return m, nil
	}
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		m.setStatus("cannot create the library: "+err.Error(), statusErr)
		return m, nil
	}

	var moved []string
	var failures, already int
	for _, e := range targets {
		if pathWithin(libraryDir, e.Path) {
			already++
			continue
		}
		name, err := m.moveName(e)
		if err != nil || name == "" {
			failures++
			continue
		}
		target := filepath.Join(libraryDir, name)
		if target == e.Path || !pathWithin(libraryDir, target) {
			failures++
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
		moved = append(moved, e.Path)
	}

	if len(moved) > 0 {
		m.removeEntries(moved)
	}
	eligible := len(targets) - already
	switch {
	case failures > 0:
		m.setStatus(fmt.Sprintf("moved %d of %d books to the library (%d failed)",
			len(moved), eligible, failures), statusErr)
	case len(moved) > 0 && already > 0:
		m.setStatus(fmt.Sprintf("moved %s to the library (%d already there)",
			bookCount(len(moved)), already), statusOK)
	case len(moved) > 0:
		m.setStatus("moved "+bookCount(len(moved))+" to the library", statusOK)
	case already > 0:
		m.setStatus("already in the library", statusInfo)
	}
	return m, nil
}

// pathWithin reports whether path is inside dir. Both are made absolute so a
// relative library setting and absolute book paths compare correctly.
func pathWithin(dir, path string) bool {
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(dirAbs, pathAbs)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
