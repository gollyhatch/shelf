package tui

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/shlex"
)

// viewerDoneMsg reports that the external viewer has finished.
type viewerDoneMsg struct {
	err error
}

// openViewer launches the configured viewer for the book under the cursor.
// The TUI is suspended until the viewer exits.
func (m model) openViewer() (tea.Model, tea.Cmd) {
	if strings.TrimSpace(m.viewer) == "" {
		return m, nil
	}
	e, ok := m.selected()
	if !ok {
		return m, nil
	}
	cmd, err := viewerCommand(m.viewer, e.Path)
	if err != nil {
		m.setStatus("viewer: "+err.Error(), statusErr)
		return m, nil
	}
	m.setStatus("opening "+filepath.Base(e.Path)+"…", statusInfo)
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return viewerDoneMsg{err: err}
	})
}

// viewerCommand turns a viewer setting into an exec.Cmd. The setting may carry
// arguments; shell-style quoting is supported, but no shell is involved, and
// the EPUB path is always passed as one final argument.
func viewerCommand(viewer, path string) (*exec.Cmd, error) {
	args, err := shlex.Split(viewer)
	if err != nil {
		return nil, fmt.Errorf("parse viewer: %w", err)
	}
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return nil, errors.New("viewer is empty")
	}
	args = append(args, path)
	return exec.Command(args[0], args[1:]...), nil
}
