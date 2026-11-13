package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gollyhatch/shelf/internal/library"
)

func TestRenameSelectedBook(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)
	m.renameName = func(e library.Entry) (string, error) {
		return "renamed-" + filepath.Base(e.Path), nil
	}

	m, cmd := step(t, m, keyMsg("N"))
	if cmd != nil {
		t.Fatal("rename scheduled an unexpected command")
	}
	want := filepath.Join(filepath.Dir(path), "renamed-book.epub")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("renamed file missing: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("original file still exists")
	}
	if got := m.entries[0].Path; got != want {
		t.Errorf("entry path = %q, want %q", got, want)
	}
	if m.status != "renamed 1 book" {
		t.Errorf("status = %q", m.status)
	}
}

func TestRenameSelectedBooks(t *testing.T) {
	dir := t.TempDir()
	p1 := makeEPUBIn(t, dir, "one.epub")
	p2 := makeEPUBIn(t, dir, "two.epub")
	m := newModel(dir, libraryOf(t, dir, p1, p2))
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m = next.(model)
	m.renameName = func(e library.Entry) (string, error) {
		return "renamed-" + filepath.Base(e.Path), nil
	}

	m = key(t, m, " ")
	m = key(t, m, " ")
	if len(m.sel) != 2 {
		t.Fatalf("selected = %v", m.sel)
	}
	m, _ = step(t, m, keyMsg("N"))
	for _, name := range []string{"renamed-one.epub", "renamed-two.epub"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s missing: %v", name, err)
		}
	}
	if m.status != "renamed 2 books" {
		t.Errorf("status = %q", m.status)
	}
	if len(m.sel) != 2 {
		t.Errorf("selection was lost: %v", m.sel)
	}
}

func TestRenameSkipsCollision(t *testing.T) {
	dir := t.TempDir()
	src := makeEPUBIn(t, dir, "source.epub")
	if err := os.WriteFile(filepath.Join(dir, "taken.epub"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newModel(dir, libraryOf(t, dir, src))
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m = next.(model)
	m.renameName = func(library.Entry) (string, error) { return "taken.epub", nil }

	m, _ = step(t, m, keyMsg("N"))
	if _, err := os.Stat(src); err != nil {
		t.Errorf("source file was renamed despite the collision: %v", err)
	}
	if !strings.Contains(m.status, "1 failed") {
		t.Errorf("status = %q", m.status)
	}
}

func TestRenameErrorStatus(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)
	m.renameName = func(library.Entry) (string, error) {
		return "", errors.New("bad format")
	}
	m, _ = step(t, m, keyMsg("N"))
	if !strings.Contains(m.status, "1 failed") {
		t.Errorf("status = %q", m.status)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file was removed after a rename error: %v", err)
	}
}
