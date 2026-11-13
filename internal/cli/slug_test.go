package cli

import "testing"

func TestSlugify(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"lowercase and spaces", "The Left Hand of Darkness", "the-left-hand-of-darkness"},
		{"german", "Über ß", "ueber-ss"},
		{"accented", "Straße & Café", "strasse-cafe"},
		{"decomposed umlaut", "u\u0308ber", "ueber"},
		{"ampersand", "A&B", "a-b"},
		{"ampersand with spaces", "A & B", "a-b"},
		{"apostrophe", "Frank Herbert's Dune", "frank-herberts-dune"},
		{"surrounding spaces", "  spaced   out  ", "spaced-out"},
		{"punctuation", "Dune: Messiah / Book 1", "dune-messiah-book-1"},
		{"ligatures", "Æon Øresund", "aeon-oresund"},
		{"symbols", "100% & More", "100-percent-more"},
		{"plus signs", "C++", "c-plus-plus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := slugify(tt.in); got != tt.want {
				t.Errorf("slugify(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
