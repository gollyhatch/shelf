package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/gollyhatch/shelf/internal/library"
)

func TestViewerCommand(t *testing.T) {
	tests := []struct {
		name   string
		viewer string
		path   string
		want   []string
	}{
		{
			name:   "plain command",
			viewer: "zathura",
			path:   "/books/dune.epub",
			want:   []string{"zathura", "/books/dune.epub"},
		},
		{
			name:   "arguments",
			viewer: "calibre-ebook-viewer --detach",
			path:   "/books/dune.epub",
			want:   []string{"calibre-ebook-viewer", "--detach", "/books/dune.epub"},
		},
		{
			name:   "quoted path and argument",
			viewer: `"/opt/My Viewer/viewer" --title "Dune Messiah"`,
			path:   "/books/a book.epub",
			want: []string{
				"/opt/My Viewer/viewer", "--title", "Dune Messiah", "/books/a book.epub",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := viewerCommand(tt.viewer, tt.path)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(cmd.Args, tt.want) {
				t.Errorf("Args = %v, want %v", cmd.Args, tt.want)
			}
		})
	}
}

func TestViewerCommandErrors(t *testing.T) {
	if _, err := viewerCommand("", "/books/dune.epub"); err == nil {
		t.Error("empty viewer should fail")
	}
	if _, err := viewerCommand(`""`, "/books/dune.epub"); err == nil {
		t.Error("a viewer that expands to nothing should fail")
	}
	if _, err := viewerCommand(`"unterminated`, "/books/dune.epub"); err == nil {
		t.Error("unterminated quote should fail")
	}
}

func TestOpenViewerWithoutConfig(t *testing.T) {
	m := newModel("/b", []library.Entry{fakeEntry("/b/book.epub", "Book", "Author", "")})
	m.rebuildRows()

	next, cmd := m.openViewer()
	m = next.(model)
	if cmd != nil {
		t.Error("openViewer scheduled a command without a configured viewer")
	}
	if m.status != "" {
		t.Errorf("openViewer set a status without a configured viewer: %q", m.status)
	}
}

func TestOpenViewerUsesCurrentBook(t *testing.T) {
	m := newModel("/b", []library.Entry{
		fakeEntry("/b/one.epub", "One", "Author", ""),
		fakeEntry("/b/two.epub", "Two", "Author", ""),
	})
	m.rebuildRows()
	m.listIdx = 1
	m.viewer = "zathura"

	m, cmd := step(t, m, keyMsg("ctrl+o"))
	if cmd == nil {
		t.Fatal("ctrl+o did not schedule the viewer")
	}
	if !strings.Contains(m.status, "two.epub") {
		t.Errorf("status = %q, want the selected book", m.status)
	}
}
