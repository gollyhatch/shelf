package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/gollyhatch/shelf/internal/epub"
	"github.com/gollyhatch/shelf/internal/library"
)

func TestOnlineMetadataLookupAndApply(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)
	found := epub.Metadata{
		Title:       "Cleaner Title",
		Creators:    []epub.Creator{{Name: "Ursula K. Le Guin"}},
		Publisher:   "Ace",
		Description: "A cleaner description.",
	}
	var gotCurrent epub.Metadata
	m.lookupOnline = func(current epub.Metadata) (epub.Metadata, error) {
		gotCurrent = current
		return found, nil
	}

	m, cmd := step(t, m, keyMsg("m"))
	if cmd == nil {
		t.Fatal("m did not schedule an online lookup")
	}
	if !m.lookingUp {
		t.Fatal("lookingUp flag not set")
	}
	m = drain(t, m, cmd)

	if m.lookingUp {
		t.Error("lookingUp flag stuck after lookup")
	}
	if !m.onlineOpen {
		t.Fatal("online comparison dialog did not open")
	}
	if gotCurrent.Title != "The Left Hand of Darkness" {
		t.Errorf("lookup did not receive current metadata: %+v", gotCurrent)
	}
	if len(m.onlineFields) != 4 {
		t.Fatalf("online fields = %d, want 4: %+v", len(m.onlineFields), m.onlineFields)
	}
	view := m.View()
	for _, want := range []string{
		"Online metadata", "Current", "Found",
		"Cleaner Title", "Ursula K. Le Guin", "Ace", "A cleaner description.",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("comparison dialog is missing %q:\n%s", want, view)
		}
	}

	// Deselect the first field (title) and apply the rest.
	if m.onlineFields[m.onlineIdx].field != epub.FieldTitle {
		t.Fatalf("first online field = %v, want title", m.onlineFields[m.onlineIdx].field)
	}
	m = key(t, m, " ")
	if m.onlineFields[0].selected {
		t.Fatal("space did not deselect the title")
	}
	m, cmd = step(t, m, keyMsg("enter"))
	if cmd == nil {
		t.Fatal("enter did not schedule the metadata update")
	}
	m = drain(t, m, cmd)

	if m.onlineOpen {
		t.Error("online dialog stayed open after applying")
	}
	meta, err := epub.ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "The Left Hand of Darkness" {
		t.Errorf("title was changed despite being deselected: %q", meta.Title)
	}
	if meta.AuthorString() != "Ursula K. Le Guin" {
		t.Errorf("author = %q", meta.AuthorString())
	}
	if meta.Publisher != "Ace" {
		t.Errorf("publisher = %q", meta.Publisher)
	}
	if meta.Description != "A cleaner description." {
		t.Errorf("description = %q", meta.Description)
	}
}

// TestOnlineWrapsLongValues checks that a long current or found value is
// wrapped into multiple table rows instead of being truncated with an ellipsis.
func TestOnlineWrapsLongValues(t *testing.T) {
	long := strings.Repeat("a long description with several words ", 8) + "FINALWORD"
	m := newModel("/b", []library.Entry{fakeEntry("/b/a.epub", "Alpha", "Ann", "")})
	m.onlineOpen = true
	m.onlineFound = epub.Metadata{Title: "Found Title"}
	m.onlineFields = []onlineField{{
		field:    epub.FieldDescription,
		current:  "Old description",
		found:    long,
		selected: true,
	}}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = next.(model)

	view := m.View()
	if !strings.Contains(view, "FINALWORD") {
		t.Errorf("wrapped value is missing its end:\n%s", view)
	}
	if strings.Contains(view, "FINALWORD…") {
		t.Errorf("value was truncated instead of wrapped:\n%s", view)
	}
}

func TestOnlineMetadataNoDifferences(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)
	current, err := epub.ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	m.lookupOnline = func(epub.Metadata) (epub.Metadata, error) {
		return current, nil
	}

	m, cmd := step(t, m, keyMsg("m"))
	m = drain(t, m, cmd)
	if m.onlineOpen {
		t.Error("dialog opened even though online metadata matched")
	}
	if !strings.Contains(m.status, "matches") {
		t.Errorf("status = %q", m.status)
	}
}

func TestOnlineMetadataLookupError(t *testing.T) {
	path := makeEPUB(t)
	m := sized(t, path, 110, 34)
	m.lookupOnline = func(epub.Metadata) (epub.Metadata, error) {
		return epub.Metadata{}, errors.New("network down")
	}

	m, cmd := step(t, m, keyMsg("m"))
	m = drain(t, m, cmd)
	if m.onlineOpen {
		t.Error("dialog opened after a lookup error")
	}
	if !strings.Contains(m.status, "online: network down") {
		t.Errorf("status = %q", m.status)
	}
}

func TestOnlineDialogSelectAllAndNone(t *testing.T) {
	m := newModel("/b", []library.Entry{fakeEntry("/b/a.epub", "Alpha", "Ann", "")})
	m.onlineOpen = true
	m.onlineFields = []onlineField{
		{field: epub.FieldTitle, current: "Old", found: "New", selected: true},
		{field: epub.FieldAuthor, current: "A", found: "B", selected: true},
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(model)

	m = key(t, m, "n")
	for _, field := range m.onlineFields {
		if field.selected {
			t.Errorf("field %v still selected after n", field.field)
		}
	}
	m = key(t, m, "a")
	for _, field := range m.onlineFields {
		if !field.selected {
			t.Errorf("field %v still unselected after a", field.field)
		}
	}
}
