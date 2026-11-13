package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathUsesXDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	got, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "shelf", "config.toml")
	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestPathFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", home)

	got, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "shelf", "config.toml")
	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("library = \"/srv/books\"\nviewer = \"zathura --reuse-instance\"\nsort = \"title:desc\"\nsearch_url = \"https://example.com/?q=%a+%t\"\ntheme = \"dracula\"\ngoogle_books_api_key = \"test-key\"\nrename_format = \"%a - %t.epub\"\nslugify = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Library != "/srv/books" {
		t.Errorf("Library = %q", cfg.Library)
	}
	if cfg.Viewer != "zathura --reuse-instance" {
		t.Errorf("Viewer = %q", cfg.Viewer)
	}
	if cfg.Sort != "title:desc" {
		t.Errorf("Sort = %q", cfg.Sort)
	}
	if cfg.SearchURL != "https://example.com/?q=%a+%t" {
		t.Errorf("SearchURL = %q", cfg.SearchURL)
	}
	if cfg.Theme != "dracula" {
		t.Errorf("Theme = %q", cfg.Theme)
	}
	if cfg.GoogleBooksAPIKey != "test-key" {
		t.Errorf("GoogleBooksAPIKey = %q", cfg.GoogleBooksAPIKey)
	}
	if cfg.RenameFormat != "%a - %t.epub" {
		t.Errorf("RenameFormat = %q", cfg.RenameFormat)
	}
	if !cfg.SlugifyRename() {
		t.Error("SlugifyRename() = false, want true")
	}
}

func TestLoadFileSluggifyAlias(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("sluggify = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SlugifyRename() {
		t.Error("sluggify alias was not honoured")
	}
}

func TestLoadFileMissingIsEmpty(t *testing.T) {
	cfg, err := LoadFile(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("missing config returned an error: %v", err)
	}
	if cfg.Library != "" {
		t.Errorf("Library = %q, want empty", cfg.Library)
	}
	if got := cfg.LibraryDir("/fallback"); got != "/fallback" {
		t.Errorf("LibraryDir = %q, want the fallback", got)
	}
}

func TestLoadFileInvalidTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("library = [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil {
		t.Fatal("expected an error for invalid TOML")
	} else if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not mention the file path", err)
	}
}

func TestLoadFileExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("library = \"~/Books\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "Books")
	if cfg.Library != want {
		t.Errorf("Library = %q, want %q", cfg.Library, want)
	}
}

func TestLoadUsesXDGLocation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "shelf"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "shelf", "config.toml")
	if err := os.WriteFile(path, []byte("library = \"/srv/books\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Library != "/srv/books" {
		t.Errorf("Library = %q", cfg.Library)
	}
}
