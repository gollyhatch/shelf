package library

import (
	"testing"

	"github.com/gollyhatch/shelf/internal/epub"
)

func TestEntrySeriesLine(t *testing.T) {
	tests := []struct {
		name string
		meta epub.Metadata
		want string
	}{
		{"series and episode", epub.Metadata{Series: "Dune", SeriesIndex: "2"}, "(Dune #2)"},
		{"series only", epub.Metadata{Series: "Dune"}, "(Dune)"},
		{"episode only", epub.Metadata{SeriesIndex: "3"}, "(Episode 3)"},
		{"subtitle and series", epub.Metadata{Subtitle: "A Novel", Series: "Dune", SeriesIndex: "2"}, "A Novel (Dune #2)"},
		{"subtitle only", epub.Metadata{Subtitle: "A Novel"}, ""},
		{"neither", epub.Metadata{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := Entry{Metadata: tt.meta}
			if got := e.SeriesLine(); got != tt.want {
				t.Errorf("SeriesLine = %q, want %q", got, tt.want)
			}
		})
	}
}
