package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gollyhatch/shelf/internal/library"
)

// makeUnreadableEPUB writes a book whose package document has the old
// mismatched </dc:meta> closing tag, which the metadata reader rejects.
func makeUnreadableEPUB(t *testing.T, dir, name string) string {
	t.Helper()
	opf := strings.Replace(testOPF, "</metadata>",
		`<meta name="calibre:series" content="Lacroix"></dc:meta></metadata>`, 1)
	return makeEPUBWithOPF(t, dir, name, opf)
}

func TestRepairUnreadableBook(t *testing.T) {
	dir := t.TempDir()
	path := makeUnreadableEPUB(t, dir, "broken.epub")
	e := library.LoadOne(path)
	if e.Err == nil {
		t.Fatal("fixture was expected to be unreadable")
	}

	m := newModel(dir, []library.Entry{e})
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m = next.(model)
	m.pane = paneDetail

	if !m.selectedUnreadable() {
		t.Fatal("selected book should be unreadable")
	}
	if view := m.View(); !strings.Contains(view, "r repairs the EPUB") {
		t.Errorf("error pane does not offer the repair key:\n%s", view)
	}

	m, cmd := step(t, m, keyMsg("r"))
	if cmd == nil {
		t.Fatal("r did not schedule the repair")
	}
	if !m.repairing {
		t.Fatal("repairing flag not set")
	}
	m = drain(t, m, cmd)

	if m.repairing {
		t.Error("repairing flag stuck after the command finished")
	}
	if m.entries[0].Err != nil {
		t.Fatalf("book is still unreadable after repair: %v", m.entries[0].Err)
	}
	if m.entries[0].Metadata.Title != "The Left Hand of Darkness" {
		t.Errorf("title after repair = %q", m.entries[0].Metadata.Title)
	}
	if m.status != "repaired" {
		t.Errorf("status = %q", m.status)
	}
	if view := m.View(); !strings.Contains(view, "The Left Hand of Darkness") {
		t.Errorf("repaired metadata is not rendered:\n%s", view)
	}
}

// makeCollidingEPUB writes a book whose package document has name/content and
// property metas sharing a key, which makes the underlying metadata parser panic.
func makeCollidingEPUB(t *testing.T, dir, name string) string {
	t.Helper()
	opf := strings.Replace(testOPF, "</metadata>",
		`<meta name="schema:accessMode" content="textual"/>`+
			`<meta property="schema:accessMode">visual</meta></metadata>`, 1)
	return makeEPUBWithOPF(t, dir, name, opf)
}

func TestRepairKeyFixesMetaCollisions(t *testing.T) {
	dir := t.TempDir()
	path := makeCollidingEPUB(t, dir, "colliding.epub")
	e := library.LoadOne(path)
	if e.Err == nil {
		t.Fatal("fixture was expected to be unreadable")
	}

	m := newModel(dir, []library.Entry{e})
	m.rebuildRows()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	m = next.(model)
	m.pane = paneDetail

	if !m.selectedUnreadable() {
		t.Fatal("selected book should be unreadable")
	}
	m, cmd := step(t, m, keyMsg("r"))
	if cmd == nil {
		t.Fatal("r did not schedule the repair")
	}
	m = drain(t, m, cmd)

	if m.entries[0].Err != nil {
		t.Fatalf("book is still unreadable after repair: %v", m.entries[0].Err)
	}
	if m.entries[0].Metadata.Title != "The Left Hand of Darkness" {
		t.Errorf("title after repair = %q", m.entries[0].Metadata.Title)
	}
	if m.status != "repaired" {
		t.Errorf("status = %q", m.status)
	}
}

func TestRepairKeyIgnoresReadableBook(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)

	m, cmd := step(t, m, keyMsg("r"))
	if cmd != nil {
		t.Error("r scheduled a repair for a readable book")
	}
	if m.repairing {
		t.Error("repairing flag set for a readable book")
	}
}
