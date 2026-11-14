package cli

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gollyhatch/shelf/internal/epub"
	"github.com/gollyhatch/shelf/internal/library"
)

// cliFixtureOPF is a legacy package document: Dublin Core elements carry a
// default xmlns instead of the dc: prefix, so the repair command has something
// to fix.
const cliFixtureOPF = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="pub-id">
  <metadata xmlns="http://www.idpf.org/2007/opf">
    <identifier xmlns="http://purl.org/dc/elements/1.1/" id="pub-id">urn:isbn:1</identifier>
    <title xmlns="http://purl.org/dc/elements/1.1/" id="title">The Left Hand of Darkness</title>
    <creator xmlns="http://purl.org/dc/elements/1.1/" id="author">Le Guin, Ursula K.</creator>
    <language xmlns="http://purl.org/dc/elements/1.1/">en</language>
    <publisher xmlns="http://purl.org/dc/elements/1.1/">Ace Books</publisher>
    <meta property="dcterms:modified">2026-01-01T00:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="chapter-1" href="chapter-1.xhtml" media-type="application/xhtml+xml"/>
    <item id="toc" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
  </manifest>
  <spine toc="toc"/>
</package>`

// makeEPUB writes a small but valid EPUB with a known title and author into
// dir and returns its path.
func makeEPUB(t *testing.T, dir string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	w, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(epub.Mimetype)); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	write("META-INF/container.xml", `<?xml version="1.0" encoding="UTF-8"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`)
	write("OEBPS/content.opf", cliFixtureOPF)
	write("OEBPS/chapter-1.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><body><h1>One</h1></body></html>`)
	write("OEBPS/toc.ncx", `<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><head></head></ncx>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "original.epub")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// runRename executes the rename command with args and returns both output
// streams together with the command error.
func runRename(args ...string) (stdout, stderr string, err error) {
	return runRenameWithDefault("", args...)
}

func runRenameWithDefault(defaultFormat string, args ...string) (stdout, stderr string, err error) {
	return runRenameWithConfig(defaultFormat, false, args...)
}

func runRenameWithConfig(defaultFormat string, defaultSlugify bool, args ...string) (stdout, stderr string, err error) {
	cmd := renameCmd(".", defaultFormat, defaultSlugify)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestValidateFormat(t *testing.T) {
	valid := []string{
		"",
		"no placeholders",
		"%a - %t.epub",
		"%a-%t.epub",
		"%s-%e-%t.epub",
		"%t%%",
		"100%%.epub",
		"%series-index - %title.epub",
		"%author (%s, book %e).epub",
	}
	for _, format := range valid {
		if err := validateFormat(format); err != nil {
			t.Errorf("validateFormat(%q) = %v, want nil", format, err)
		}
	}
	invalid := []string{
		"%x",               // no such shorthand
		"%foo",             // no such field
		"%series-indexx",   // near miss of a field name
		"%",                // dangling sign
		"% (percent sign)", // unescaped sign
	}
	for _, format := range invalid {
		if err := validateFormat(format); err == nil {
			t.Errorf("validateFormat(%q) = nil, want an error", format)
		}
	}
}

func TestExpandFormat(t *testing.T) {
	entry := library.Entry{
		Path: "/shelf/original.epub",
		Metadata: epub.Metadata{
			Title:       "The Left Hand of Darkness",
			Creators:    []epub.Creator{{Name: "Le Guin, Ursula K."}},
			Subjects:    []string{"Science Fiction"},
			Publisher:   "Ace Books",
			Date:        "1969",
			Language:    "en",
			Rights:      "Public Domain",
			Identifier:  "9780441478125",
			Series:      "Hainish Cycle",
			SeriesIndex: "4",
		},
	}
	tests := []struct {
		format string
		want   string
	}{
		{"%a - %t.epub", "Le Guin, Ursula K. - The Left Hand of Darkness.epub"},
		{"%a-%t.epub", "Le Guin, Ursula K.-The Left Hand of Darkness.epub"},
		{"%s %e - %t.epub", "Hainish Cycle 4 - The Left Hand of Darkness.epub"},
		{"%title.epub", "The Left Hand of Darkness.epub"},
		{"%series-index/%t.epub", "4-The Left Hand of Darkness.epub"},
		{"%p, %d, %l.epub", "Ace Books, 1969, en.epub"},
		{"%g.epub", "Science Fiction.epub"},
		{"%i.epub", "9780441478125.epub"},
		{"%t (%%).epub", "The Left Hand of Darkness (%).epub"},
		{"%t", "The Left Hand of Darkness.epub"},      // extension is added
		{"%t.EPUB", "The Left Hand of Darkness.EPUB"}, // already an epub extension
	}
	for _, tt := range tests {
		if got := expandFormat(tt.format, entry, false); got != tt.want {
			t.Errorf("expandFormat(%q) = %q, want %q", tt.format, got, tt.want)
		}
	}
}

// TestExpandFormatSanitizes checks that neither the metadata nor the pattern
// can smuggle a path separator into the new name.
func TestExpandFormatSanitizes(t *testing.T) {
	entry := library.Entry{
		Path:     "/shelf/original.epub",
		Metadata: epub.Metadata{Title: "What/If", Creators: []epub.Creator{{Name: "Smith; John"}}},
	}
	got := expandFormat("%a/%t.epub", entry, false)
	if got != "Smith; John-What-If.epub" {
		t.Errorf("expandFormat = %q, want %q", got, "Smith; John-What-If.epub")
	}
	if strings.ContainsAny(got, `/\`) {
		t.Errorf("expandFormat = %q, want no path separator", got)
	}
}

// TestExpandFormatFallsBack checks the degenerate expansions: an empty
// result keeps the default name, and an empty field does not leave stray
// separators behind.
func TestExpandFormatFallsBack(t *testing.T) {
	empty := library.Entry{Path: "/shelf/original.epub"}
	if got := expandFormat("%t", empty, false); got != "original.epub" {
		t.Errorf("expandFormat on empty metadata = %q, want %q", got, "original.epub")
	}
	noAuthor := library.Entry{
		Path:     "/shelf/original.epub",
		Metadata: epub.Metadata{Title: "Dune"},
	}
	if got := expandFormat("%a - %t.epub", noAuthor, false); got != "Dune.epub" {
		t.Errorf("expandFormat with empty author = %q, want %q", got, "Dune.epub")
	}
}

// runRepair executes the repair command with args and returns its output streams.
func runRepair(args ...string) (stdout, stderr string, err error) {
	cmd := repairCmd(".")
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// TestExpandFormatSlugify checks that slugified values are inserted into the
// pattern without changing the pattern's literal text.
func TestExpandFormatSlugify(t *testing.T) {
	entry := library.Entry{
		Metadata: epub.Metadata{
			Title:    "Straße & Café",
			Creators: []epub.Creator{{Name: "Jürgen Müller"}},
		},
	}
	if got := expandFormat("%a-%t.epub", entry, true); got != "juergen-mueller-strasse-cafe.epub" {
		t.Errorf("expandFormat = %q", got)
	}
	if got := expandFormat("%a - %t.epub", entry, true); got != "juergen-mueller-strasse-cafe.epub" {
		t.Errorf("expandFormat with spaced separator = %q", got)
	}
	if got := expandFormat("%a by %t.epub", entry, true); got != "juergen-mueller-by-strasse-cafe.epub" {
		t.Errorf("expandFormat with literal words = %q", got)
	}
}

func TestExpandFormatEpisodeNumber(t *testing.T) {
	entry := library.Entry{Metadata: epub.Metadata{SeriesIndex: "2.0"}}
	if got := expandFormat("%e.epub", entry, false); got != "2.epub" {
		t.Errorf("episode 2.0 = %q, want 2.epub", got)
	}
	if got := expandFormat("%e.epub", entry, true); got != "2.epub" {
		t.Errorf("slugified episode 2.0 = %q, want 2.epub", got)
	}

	entry.Metadata.SeriesIndex = "2.5"
	if got := expandFormat("%e.epub", entry, false); got != "2.5.epub" {
		t.Errorf("episode 2.5 = %q, want 2.5.epub", got)
	}
	if got := expandFormat("%e.epub", entry, true); got != "2-5.epub" {
		t.Errorf("slugified episode 2.5 = %q, want 2-5.epub", got)
	}
}

// TestRenameNameFor checks that the TUI name function uses the CLI's format and
// slugify rules.
func TestRenameNameFor(t *testing.T) {
	entry := library.Entry{
		Path: "/shelf/original.epub",
		Metadata: epub.Metadata{
			Title:    "Straße",
			Creators: []epub.Creator{{Name: "Jürgen Müller"}},
		},
	}
	name, err := renameNameFor("%a-%t.epub", true)(entry)
	if err != nil {
		t.Fatal(err)
	}
	if name != "juergen-mueller-strasse.epub" {
		t.Errorf("formatted name = %q", name)
	}
	name, err = renameNameFor("", true)(entry)
	if err != nil {
		t.Fatal(err)
	}
	if name != "juergen-mueller-strasse.epub" {
		t.Errorf("default name = %q", name)
	}
}

// TestMoveNameForRequiresFormat checks that moving into the library has no
// fallback name when rename_format is not configured.
func TestMoveNameForRequiresFormat(t *testing.T) {
	if moveNameFor("", false) != nil {
		t.Error("moveNameFor returned a generator without a configured format")
	}
	if moveNameFor("   ", false) != nil {
		t.Error("moveNameFor returned a generator for a blank format")
	}

	entry := library.Entry{
		Metadata: epub.Metadata{
			Title:    "The Left Hand of Darkness",
			Creators: []epub.Creator{{Name: "Le Guin, Ursula K."}},
		},
	}
	name, err := moveNameFor("%a - %t.epub", false)(entry)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Le Guin, Ursula K. - The Left Hand of Darkness.epub" {
		t.Errorf("name = %q", name)
	}
}

// runSet executes the set command with args and returns its output streams.
func runSet(args ...string) (stdout, stderr string, err error) {
	return runSetIn(".", args...)
}

// runSetIn executes set with a default library directory.
func runSetIn(defaultDir string, args ...string) (stdout, stderr string, err error) {
	cmd := setCmd(defaultDir)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// TestSetIdentifier checks that --isbn, the flag name for the identifier
// field, is applied: the flag used to be looked up under "identifier".
func TestSetIdentifier(t *testing.T) {
	dir := t.TempDir()
	path := makeEPUB(t, dir)
	if _, stderr, err := runSet("--isbn", "9780141439518", path); err != nil {
		t.Fatalf("set --isbn: %v (stderr: %s)", err, stderr)
	}
	m, err := epub.ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Identifier != "9780141439518" {
		t.Errorf("Identifier = %q, want %q", m.Identifier, "9780141439518")
	}
}

// TestSetReadStatus checks the custom read marker through the set command.
func TestSetReadStatus(t *testing.T) {
	dir := t.TempDir()
	path := makeEPUB(t, dir)
	if _, stderr, err := runSet("--read", "yes", path); err != nil {
		t.Fatalf("set --read yes: %v (stderr: %s)", err, stderr)
	}
	m, err := epub.ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Read {
		t.Error("Read = false after setting --read yes")
	}

	if _, stderr, err := runSet("--read", "no", path); err != nil {
		t.Fatalf("set --read no: %v (stderr: %s)", err, stderr)
	}
	m, err = epub.ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Read {
		t.Error("Read = true after setting --read no")
	}
	if got := m.Get(epub.FieldRead); got != "no" {
		t.Errorf("Get(read) = %q, want no", got)
	}
}

// TestSetUsesDefaultLibrary checks that set with no file arguments operates on
// the configured library directory.
func TestSetUsesDefaultLibrary(t *testing.T) {
	dir := t.TempDir()
	path := makeEPUB(t, dir)

	if _, stderr, err := runSetIn(dir, "--title", "From the library"); err != nil {
		t.Fatalf("set with default library: %v (stderr: %s)", err, stderr)
	}
	m, err := epub.ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "From the library" {
		t.Errorf("Title = %q", m.Title)
	}
}

// TestRepairCommand checks the CLI reporting, dry-run safety and idempotence.
func TestRepairCommand(t *testing.T) {
	dir := t.TempDir()
	path := makeEPUB(t, dir)

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runRepair("-n", path)
	if err != nil {
		t.Fatalf("repair dry run: %v (stderr: %s)", err, stderr)
	}
	if !strings.Contains(stdout, "would repair") {
		t.Errorf("dry run output = %q", stdout)
	}
	afterDryRun, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, afterDryRun) {
		t.Error("the dry run rewrote the file")
	}

	stdout, stderr, err = runRepair(path)
	if err != nil {
		t.Fatalf("repair: %v (stderr: %s)", err, stderr)
	}
	if !strings.Contains(stdout, "(repaired)") {
		t.Errorf("repair output = %q", stdout)
	}
	if changed, err := epub.Repair(path, true); err != nil {
		t.Fatal(err)
	} else if changed {
		t.Error("the file still needs repair")
	}

	stdout, stderr, err = runRepair(path)
	if err != nil {
		t.Fatalf("second repair: %v (stderr: %s)", err, stderr)
	}
	if !strings.Contains(stdout, "(unchanged)") {
		t.Errorf("second repair output = %q", stdout)
	}
}

func TestRenameWithFormat(t *testing.T) {
	dir := t.TempDir()
	src := makeEPUB(t, dir)
	want := "Le Guin, Ursula K. - The Left Hand of Darkness.epub"

	// A dry run reports the new name without touching the file.
	stdout, _, err := runRename("-f", "%a - %t.epub", "--dry-run", src)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !strings.Contains(stdout, want) {
		t.Errorf("dry run output %q does not mention %q", stdout, want)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("the dry run renamed the file: %v", err)
	}

	// The real run renames it.
	if _, stderr, err := runRename("-f", "%a - %t.epub", src); err != nil {
		t.Fatalf("rename: %v (stderr: %s)", err, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
		t.Errorf("renamed file missing: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("the original file still exists")
	}
}

func TestRenameUsesConfiguredFormat(t *testing.T) {
	dir := t.TempDir()
	src := makeEPUB(t, dir)
	if _, stderr, err := runRenameWithDefault("%t.epub", src); err != nil {
		t.Fatalf("rename with configured format: %v (stderr: %s)", err, stderr)
	}
	want := filepath.Join(dir, "The Left Hand of Darkness.epub")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("renamed file missing: %v", err)
	}
}

func TestRenameFlagOverridesConfiguredFormat(t *testing.T) {
	dir := t.TempDir()
	src := makeEPUB(t, dir)
	if _, stderr, err := runRenameWithDefault("%a.epub", "-f", "%t.epub", src); err != nil {
		t.Fatalf("rename with explicit format: %v (stderr: %s)", err, stderr)
	}
	want := filepath.Join(dir, "The Left Hand of Darkness.epub")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("explicit -f did not override the configured format: %v", err)
	}
}

func TestRenameConfigSlugifyDefault(t *testing.T) {
	dir := t.TempDir()
	src := makeEPUB(t, dir)
	if _, stderr, err := runRenameWithConfig("", true, src); err != nil {
		t.Fatalf("rename with config slugify: %v (stderr: %s)", err, stderr)
	}
	want := filepath.Join(dir, "le-guin-the-left-hand-of-darkness.epub")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("slugified file missing: %v", err)
	}
}

func TestRenameNoSlugifyOverridesConfig(t *testing.T) {
	dir := t.TempDir()
	src := makeEPUB(t, dir)
	if _, stderr, err := runRenameWithConfig("", true, "--no-slugify", src); err != nil {
		t.Fatalf("rename with --no-slugify: %v (stderr: %s)", err, stderr)
	}
	want := filepath.Join(dir, "Le Guin - The Left Hand of Darkness.epub")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("--no-slugify did not override the config: %v", err)
	}
}

func TestRenameNoSluggifyAlias(t *testing.T) {
	dir := t.TempDir()
	src := makeEPUB(t, dir)
	if _, stderr, err := runRenameWithConfig("", true, "--no-sluggify", src); err != nil {
		t.Fatalf("rename with --no-sluggify: %v (stderr: %s)", err, stderr)
	}
	want := filepath.Join(dir, "Le Guin - The Left Hand of Darkness.epub")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("--no-sluggify did not override the config: %v", err)
	}
}

func TestRenameDefaultName(t *testing.T) {
	dir := t.TempDir()
	src := makeEPUB(t, dir)
	if _, stderr, err := runRename(src); err != nil {
		t.Fatalf("rename: %v (stderr: %s)", err, stderr)
	}
	want := filepath.Join(dir, "Le Guin - The Left Hand of Darkness.epub")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("renamed file missing: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("the original file still exists")
	}
}

func TestRenameSlugify(t *testing.T) {
	dir := t.TempDir()
	src := makeEPUB(t, dir)
	if _, stderr, err := runRename("--slugify", src); err != nil {
		t.Fatalf("rename --slugify: %v (stderr: %s)", err, stderr)
	}
	want := filepath.Join(dir, "le-guin-the-left-hand-of-darkness.epub")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("slugified file missing: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("the original file still exists")
	}
}

func TestRenameSluggifyAlias(t *testing.T) {
	dir := t.TempDir()
	src := makeEPUB(t, dir)
	stdout, stderr, err := runRename("--sluggify", "--dry-run", src)
	if err != nil {
		t.Fatalf("rename --sluggify: %v (stderr: %s)", err, stderr)
	}
	if !strings.Contains(stdout, "le-guin-the-left-hand-of-darkness.epub") {
		t.Errorf("slugified preview = %q", stdout)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("dry run renamed the file: %v", err)
	}
}

// TestRenameRejectsBadFormat checks that a pattern typo fails before any
// file is renamed.
func TestRenameRejectsBadFormat(t *testing.T) {
	dir := t.TempDir()
	src := makeEPUB(t, dir)
	if _, _, err := runRename("-f", "%x", src); err == nil {
		t.Fatal("expected an error for an unknown placeholder")
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("the file was renamed despite the bad format: %v", err)
	}
}
