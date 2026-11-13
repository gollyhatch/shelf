package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/gollyhatch/shelf/internal/epub"
	"github.com/gollyhatch/shelf/internal/library"
)

func TestWrapText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  []string
	}{
		{"words", "one two three four", 9, []string{"one two", "three", "four"}},
		{"paragraphs", "first line\n\nsecond line", 20, []string{"first line", "", "second line"}},
		{"long word", "abcdefghij", 4, []string{"abcd", "efgh", "ij"}},
		{"fits", "short", 20, []string{"short"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := wrapText(tt.input, tt.width); !slices.Equal(got, tt.want) {
				t.Errorf("wrapText(%q, %d) = %q, want %q", tt.input, tt.width, got, tt.want)
			}
		})
	}
}

func TestTitleWrapsInsteadOfTruncating(t *testing.T) {
	entry := fakeEntry("/b/book.epub", "Book", "Author", "")
	entry.Metadata.Title = "The Long Way to a Small, Angry Planet and the Even Longer Journey Home"
	m := newModel("/b", []library.Entry{entry})
	m.rebuildRows()

	row := m.rows[0]
	if row.field != epub.FieldTitle {
		t.Fatalf("first row is %+v, want the title", row)
	}

	lines := m.renderRow(row, 50, 12, false)
	if len(lines) < 2 {
		t.Fatalf("title was not wrapped: %q", lines)
	}
	for i, line := range lines {
		if lipgloss.Width(line) > 50 {
			t.Errorf("line %d is %d columns wide: %q", i, lipgloss.Width(line), line)
		}
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Home") {
		t.Errorf("wrapped title lost the end: %q", joined)
	}
	if strings.Contains(joined, "…") {
		t.Errorf("wrapped title was truncated: %q", joined)
	}

	// The selected row keeps a full-width highlight on every wrapped line.
	selected := m.renderRow(row, 50, 12, true)
	for i, line := range selected {
		if got := lipgloss.Width(line); got != 50 {
			t.Errorf("selected line %d is %d columns wide, want 50: %q", i, got, line)
		}
	}
}

func TestDescriptionWrapsInsteadOfTruncating(t *testing.T) {
	entry := fakeEntry("/b/book.epub", "Book", "Author", "")
	entry.Metadata.Description = "The quick brown fox jumps over the lazy dog and keeps running into the woods."
	m := newModel("/b", []library.Entry{entry})
	m.rebuildRows()

	row := m.rows[len(m.rows)-1]
	if row.field != epub.FieldDescription {
		t.Fatalf("last row is %+v, want the description", row)
	}

	lines := m.renderRow(row, 50, 12, false)
	if len(lines) < 2 {
		t.Fatalf("description was not wrapped: %q", lines)
	}
	for i, line := range lines {
		if lipgloss.Width(line) > 50 {
			t.Errorf("line %d is %d columns wide: %q", i, lipgloss.Width(line), line)
		}
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "woods") {
		t.Errorf("wrapped description lost the end: %q", joined)
	}
	if strings.Contains(joined, "…") {
		t.Errorf("wrapped description was truncated: %q", joined)
	}

	// The selected row keeps a full-width highlight on every wrapped line.
	selected := m.renderRow(row, 50, 12, true)
	for i, line := range selected {
		if got := lipgloss.Width(line); got != 50 {
			t.Errorf("selected line %d is %d columns wide, want 50: %q", i, got, line)
		}
	}
}

// TestWrappedDescriptionStaysVisible checks that a tall description row is
// brought into view rather than being pushed below the pane by earlier rows.
func TestWrappedDescriptionStaysVisible(t *testing.T) {
	entry := fakeEntry("/b/book.epub", "Book", "Author", "")
	entry.Metadata.Description = strings.Repeat("A wrapping description sentence. ", 20)
	m := newModel("/b", []library.Entry{entry})
	m.rebuildRows()
	m.pane = paneDetail
	m.rowIdx = len(m.rows) - 1
	m.rowTop = 0

	lines := m.detailLines(40, 6, true)
	if len(lines) == 0 || len(lines) > 6 {
		t.Fatalf("detail lines = %d, want 1..6", len(lines))
	}
	if !strings.Contains(strings.Join(lines, "\n"), "Description:") {
		t.Errorf("selected description is not visible:\n%s", strings.Join(lines, "\n"))
	}
}
