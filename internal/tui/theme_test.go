package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestSeriesLineStyle(t *testing.T) {
	for name, style := range map[string]lipgloss.Style{
		"unselected": itemSeriesStyle,
		"selected":   itemSelSeriesStyle,
	} {
		if _, ok := style.GetBackground().(lipgloss.NoColor); !ok {
			t.Errorf("%s series style has a background: %v", name, style.GetBackground())
		}
		if style.GetItalic() {
			t.Errorf("%s series style is italic; some terminals invert italic text", name)
		}
	}

	if itemSeriesStyle.GetForeground() == itemStyle.GetForeground() {
		t.Error("series and title share a foreground colour")
	}
	if itemSeriesStyle.GetForeground() == itemAuthorStyle.GetForeground() {
		t.Error("series and author share a foreground colour")
	}
	if itemSelSeriesStyle.GetForeground() == itemSelStyle.GetForeground() {
		t.Error("selected series and title share a foreground colour")
	}
	if itemSelSeriesStyle.GetForeground() == itemSelAuthorStyle.GetForeground() {
		t.Error("selected series and author share a foreground colour")
	}
}

func TestApplyTheme(t *testing.T) {
	t.Cleanup(func() {
		if err := ApplyTheme(""); err != nil {
			t.Fatal(err)
		}
	})

	for name, want := range themes {
		if err := ApplyTheme(name); err != nil {
			t.Fatalf("ApplyTheme(%q): %v", name, err)
		}
		if got := ActiveTheme(); got != name {
			t.Errorf("ActiveTheme = %q, want %q", got, name)
		}
		if colAccent != want.accent || colText != want.text || colErr != want.err {
			t.Errorf("theme %q did not apply its palette", name)
		}
	}

	if err := ApplyTheme("DRACULA"); err != nil {
		t.Errorf("theme names should be case-insensitive: %v", err)
	}
	if err := ApplyTheme(""); err != nil {
		t.Fatal(err)
	}
	if got := ActiveTheme(); got != "gruvbox" {
		t.Errorf("default theme = %q, want gruvbox", got)
	}
}

func TestApplyThemeUnknown(t *testing.T) {
	if err := ApplyTheme(""); err != nil {
		t.Fatal(err)
	}
	if err := ApplyTheme("solarized"); err == nil {
		t.Fatal("unknown theme was accepted")
	}
	if got := ActiveTheme(); got != "gruvbox" {
		t.Errorf("invalid theme changed the active theme to %q", got)
	}
}

func TestCycleTheme(t *testing.T) {
	if err := ApplyTheme(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ApplyTheme(""); err != nil {
			t.Fatal(err)
		}
	})

	m := newModel("/b", nil)
	m = key(t, m, "ctrl+t")
	if got := ActiveTheme(); got != "ayu dark" {
		t.Errorf("first switch selected %q, want ayu dark", got)
	}
	if !strings.Contains(m.status, "theme: ayu dark") {
		t.Errorf("status = %q", m.status)
	}
	m = key(t, m, "ctrl+t")
	if got := ActiveTheme(); got != "dracula" {
		t.Errorf("second switch selected %q, want dracula", got)
	}
	m = key(t, m, "ctrl+t")
	if got := ActiveTheme(); got != "catppuccin" {
		t.Errorf("third switch selected %q, want catppuccin", got)
	}
	m = key(t, m, "ctrl+t")
	if got := ActiveTheme(); got != "gruvbox" {
		t.Errorf("fourth switch selected %q, want gruvbox", got)
	}
}
