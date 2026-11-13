package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gollyhatch/shelf/internal/library"
)

func TestMoveSelectedBookToLibrary(t *testing.T) {
	root := t.TempDir()
	incoming := filepath.Join(root, "incoming")
	libraryDir := filepath.Join(root, "library")
	if err := os.MkdirAll(incoming, 0o755); err != nil {
		t.Fatal(err)
	}
	src := makeEPUBIn(t, incoming, "source.epub")

	m := newModel(incoming, libraryOf(t, incoming, src))
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m = next.(model)
	m.libraryPath = libraryDir
	m.moveName = func(library.Entry) (string, error) { return "renamed.epub", nil }

	m, cmd := step(t, m, keyMsg("M"))
	if cmd != nil {
		t.Fatal("move scheduled an unexpected command")
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source file still exists: %v", err)
	}
	target := filepath.Join(libraryDir, "renamed.epub")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("moved file missing: %v", err)
	}
	if len(m.entries) != 0 {
		t.Errorf("moved entry is still listed: %+v", m.entries)
	}
	if m.status != "moved 1 book to the library" {
		t.Errorf("status = %q", m.status)
	}
}

func TestMoveSelectedBooksToLibrary(t *testing.T) {
	root := t.TempDir()
	incoming := filepath.Join(root, "incoming")
	libraryDir := filepath.Join(root, "library")
	if err := os.MkdirAll(incoming, 0o755); err != nil {
		t.Fatal(err)
	}
	p1 := makeEPUBIn(t, incoming, "one.epub")
	p2 := makeEPUBIn(t, incoming, "two.epub")

	m := newModel(incoming, libraryOf(t, incoming, p1, p2))
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m = next.(model)
	m.libraryPath = libraryDir
	m.moveName = func(e library.Entry) (string, error) {
		return "lib-" + filepath.Base(e.Path), nil
	}

	m = key(t, m, " ")
	m = key(t, m, " ")
	if len(m.sel) != 2 {
		t.Fatalf("selected = %v", m.sel)
	}
	m, _ = step(t, m, keyMsg("M"))
	for _, name := range []string{"lib-one.epub", "lib-two.epub"} {
		if _, err := os.Stat(filepath.Join(libraryDir, name)); err != nil {
			t.Errorf("%s missing: %v", name, err)
		}
	}
	for _, path := range []string{p1, p2} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("source file still exists: %s", path)
		}
	}
	if len(m.entries) != 0 {
		t.Errorf("moved entries are still listed: %+v", m.entries)
	}
	if len(m.sel) != 0 {
		t.Errorf("selection was not cleared: %v", m.sel)
	}
	if m.status != "moved 2 books to the library" {
		t.Errorf("status = %q", m.status)
	}
}

func TestMoveKeyRequiresLibrary(t *testing.T) {
	dir := t.TempDir()
	src := makeEPUBIn(t, dir, "source.epub")
	m := sized(t, src, 110, 34)
	m.moveName = func(library.Entry) (string, error) { return "renamed.epub", nil }

	m, cmd := step(t, m, keyMsg("M"))
	if cmd != nil {
		t.Fatal("move scheduled a command without a library")
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("source file was moved without a library: %v", err)
	}
	if m.status != "no library configured" {
		t.Errorf("status = %q", m.status)
	}
}

func TestMoveKeyRequiresRenameFormat(t *testing.T) {
	root := t.TempDir()
	incoming := filepath.Join(root, "incoming")
	libraryDir := filepath.Join(root, "library")
	if err := os.MkdirAll(incoming, 0o755); err != nil {
		t.Fatal(err)
	}
	src := makeEPUBIn(t, incoming, "source.epub")
	m := newModel(incoming, libraryOf(t, incoming, src))
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m = next.(model)
	m.libraryPath = libraryDir

	m, cmd := step(t, m, keyMsg("M"))
	if cmd != nil {
		t.Fatal("move scheduled a command without a rename format")
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("source file was moved without a rename format: %v", err)
	}
	if m.status != "rename_format is not configured" {
		t.Errorf("status = %q", m.status)
	}
}

func TestMoveSkipsBooksAlreadyInLibrary(t *testing.T) {
	libraryDir := t.TempDir()
	src := makeEPUBIn(t, libraryDir, "inside.epub")
	m := newModel(libraryDir, libraryOf(t, libraryDir, src))
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m = next.(model)
	m.libraryPath = libraryDir
	m.moveName = func(library.Entry) (string, error) { return "renamed.epub", nil }

	m, cmd := step(t, m, keyMsg("M"))
	if cmd != nil {
		t.Fatal("move scheduled an unexpected command")
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("book already in the library was moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(libraryDir, "renamed.epub")); !os.IsNotExist(err) {
		t.Error("a copy was created in the library")
	}
	if m.status != "already in the library" {
		t.Errorf("status = %q", m.status)
	}
}

func TestMoveSkipsExistingTarget(t *testing.T) {
	root := t.TempDir()
	incoming := filepath.Join(root, "incoming")
	libraryDir := filepath.Join(root, "library")
	if err := os.MkdirAll(incoming, 0o755); err != nil {
		t.Fatal(err)
	}
	src := makeEPUBIn(t, incoming, "source.epub")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libraryDir, "taken.epub"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newModel(incoming, libraryOf(t, incoming, src))
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m = next.(model)
	m.libraryPath = libraryDir
	m.moveName = func(library.Entry) (string, error) { return "taken.epub", nil }

	m, _ = step(t, m, keyMsg("M"))
	if _, err := os.Stat(src); err != nil {
		t.Errorf("source file was moved despite the collision: %v", err)
	}
	if !strings.Contains(m.status, "1 failed") {
		t.Errorf("status = %q", m.status)
	}
}
