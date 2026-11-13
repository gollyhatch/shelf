package epub

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	raitepub "github.com/raitucarp/epub"
	pkgmeta "github.com/raitucarp/epub/pkg"
)

// makeEPUB writes a small but valid EPUB and returns its path.
func makeEPUB(t *testing.T) string {
	t.Helper()
	w := raitepub.New("urn:isbn:1")
	w.Title("The Left Hand of Darkness")
	w.Author("Le Guin, Ursula K.")
	w.Languages("en")
	w.Subject("sf", "Science Fiction")
	w.Publisher("Ace Books")
	w.Description("A novel about a planet called Winter.")
	w.AddContent("chapter-1.xhtml", []byte(
		`<html xmlns="http://www.w3.org/1999/xhtml"><body><h1>One</h1></body></html>`))
	if err := w.TableOfContents("toc", raitepub.TOC{
		Title: "Contents",
		Items: []raitepub.TOC{{Title: "One", Href: "chapter-1.xhtml"}},
	}); err != nil {
		t.Fatalf("toc: %v", err)
	}

	path := filepath.Join(t.TempDir(), "book.epub")
	if err := w.Write(path); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// makeEPUB2 downgrades the fixture's package document to EPUB 2 and gives the
// NCX a dtb:uid, as legacy publications have.
func makeEPUB2(t *testing.T) string {
	t.Helper()
	path := makeEPUB(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		content, err := readZipFile(f)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case strings.HasSuffix(f.Name, ".opf"):
			content = bytes.Replace(content, []byte(`version="3.0"`), []byte(`version="2.0"`), 1)
		case strings.HasSuffix(f.Name, ".ncx"):
			content = bytes.Replace(content, []byte(`<head></head>`),
				[]byte(`<head><meta name="dtb:uid" content="urn:isbn:1"/></head>`), 1)
		}
		hdr := &zip.FileHeader{Name: f.Name, Method: f.Method, Modified: f.Modified}
		hdr.SetMode(f.Mode())
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// zipEntry returns the content of the first archive entry ending in suffix.
func zipEntry(t *testing.T, path, suffix string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, suffix) {
			content, err := readZipFile(f)
			if err != nil {
				t.Fatal(err)
			}
			return content
		}
	}
	t.Fatalf("no %s entry in %s", suffix, path)
	return nil
}

func TestReadMetadata(t *testing.T) {
	path := makeEPUB(t)
	m, err := ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "The Left Hand of Darkness" {
		t.Errorf("Title = %q", m.Title)
	}
	if got := m.AuthorString(); got != "Le Guin, Ursula K." {
		t.Errorf("AuthorString = %q", got)
	}
	if m.Language != "en" {
		t.Errorf("Language = %q", m.Language)
	}
	if m.Publisher != "Ace Books" {
		t.Errorf("Publisher = %q", m.Publisher)
	}
	if m.Description != "A novel about a planet called Winter." {
		t.Errorf("Description = %q", m.Description)
	}
	if len(m.Subjects) != 1 || m.Subjects[0] != "Science Fiction" {
		t.Errorf("Subjects = %+v", m.Subjects)
	}
	if m.Identifier != "urn:isbn:1" {
		t.Errorf("Identifier = %q", m.Identifier)
	}
}

// TestReadMetadataIgnoresUnrelatedArchiveDamage checks that the lightweight
// reader only touches the entries it needs. Harm to another entry, which stops
// the dependency's full-container reader, must not affect metadata loading.
func TestReadMetadataIgnoresUnrelatedArchiveDamage(t *testing.T) {
	path := makeEPUB(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}

	var target *zip.File
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "chapter-1.xhtml") {
			target = f
			break
		}
	}
	if target == nil {
		t.Fatal("fixture has no chapter entry")
	}
	offset, err := target.DataOffset()
	if err != nil {
		t.Fatal(err)
	}
	if target.CompressedSize64 < 4 {
		t.Fatalf("chapter entry is too small to corrupt: %d bytes", target.CompressedSize64)
	}

	corrupted := append([]byte(nil), data...)
	pos := int(offset) + int(target.CompressedSize64)/2
	for i := 0; i < 4; i++ {
		corrupted[pos+i] ^= 0xff
	}
	if err := os.WriteFile(path, corrupted, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := raitepub.OpenReader(path); err == nil {
		t.Fatal("damage did not break the full-container reader")
	}
	m, err := ReadMetadata(path)
	if err != nil {
		t.Fatalf("metadata read touched the damaged entry: %v", err)
	}
	if m.Title != "The Left Hand of Darkness" {
		t.Errorf("Title = %q", m.Title)
	}
}

func TestReadStatusRoundTrip(t *testing.T) {
	path := makeEPUB(t)
	if err := Update(path, func(p *Package) error {
		return p.Set(FieldRead, "yes")
	}); err != nil {
		t.Fatal(err)
	}

	m, err := ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Read {
		t.Error("book was not read back as read")
	}
	opf := zipEntry(t, path, ".opf")
	if !bytes.Contains(opf, []byte(`property="shelf:read"`)) {
		t.Errorf("package has no shelf:read property:\n%s", opf)
	}
	if !bytes.Contains(opf, []byte("shelf: https://github.com/gollyhatch/shelf#")) {
		t.Errorf("package does not declare the shelf prefix:\n%s", opf)
	}

	if err := Update(path, func(p *Package) error {
		return p.Set(FieldRead, "no")
	}); err != nil {
		t.Fatal(err)
	}
	m, err = ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Read {
		t.Error("book was still read after clearing the marker")
	}
	if bytes.Contains(zipEntry(t, path, ".opf"), []byte("shelf:read")) {
		t.Error("cleared read marker remains in the package")
	}
}

// TestReadStatusEPUB2 checks that EPUB 2 packages use the legacy name/content
// meta form for the custom read marker.
func TestReadStatusEPUB2(t *testing.T) {
	path := makeEPUB2(t)
	if err := Update(path, func(p *Package) error {
		return p.Set(FieldRead, "yes")
	}); err != nil {
		t.Fatal(err)
	}

	m, err := ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Read {
		t.Error("EPUB 2 book was not read back as read")
	}
	opf := zipEntry(t, path, ".opf")
	if !bytes.Contains(opf, []byte(`name="shelf:read"`)) ||
		!bytes.Contains(opf, []byte(`content="true"`)) {
		t.Errorf("EPUB 2 package has no name/content read marker:\n%s", opf)
	}
	if bytes.Contains(opf, []byte(`property="shelf:read"`)) {
		t.Errorf("EPUB 2 package used the EPUB 3 property form:\n%s", opf)
	}
}

func TestUpdateRoundTrip(t *testing.T) {
	path := makeEPUB(t)
	err := Update(path, func(p *Package) error {
		if err := p.Set(FieldTitle, "There and Back Again"); err != nil {
			return err
		}
		if err := p.Set(FieldSubtitle, "A Hobbit's Tale"); err != nil {
			return err
		}
		return p.Set(FieldTags, "fantasy, classic")
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "There and Back Again" {
		t.Errorf("Title = %q", m.Title)
	}
	if m.Subtitle != "A Hobbit's Tale" {
		t.Errorf("Subtitle = %q", m.Subtitle)
	}
	if len(m.Subjects) != 2 || m.Subjects[0] != "fantasy" || m.Subjects[1] != "classic" {
		t.Errorf("Subjects = %+v", m.Subjects)
	}
	// The fields we did not touch survive the rewrite.
	if m.Publisher != "Ace Books" || m.Identifier != "urn:isbn:1" {
		t.Errorf("unchanged fields were lost: %+v", m)
	}
}

func TestSeriesRoundTrip(t *testing.T) {
	path := makeEPUB(t)
	err := Update(path, func(p *Package) error {
		if err := p.Set(FieldSeries, "The Culture"); err != nil {
			return err
		}
		return p.Set(FieldSeriesIndex, "3")
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Series != "The Culture" {
		t.Errorf("Series = %q", m.Series)
	}
	if m.SeriesIndex != "3" {
		t.Errorf("SeriesIndex = %q", m.SeriesIndex)
	}

	// Clearing the series removes the meta entries.
	if err := Update(path, func(p *Package) error {
		return p.Set(FieldSeries, "")
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := ReadMetadata(path)
	if got.Series != "" || got.SeriesIndex != "3" {
		t.Errorf("clearing series changed the index: %+v", got)
	}
}

func TestSetCreatorsReplaces(t *testing.T) {
	path := makeEPUB(t)
	err := Update(path, func(p *Package) error {
		return p.SetCreators([]Creator{{Name: "Jane Doe"}, {Name: "John Roe"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := ReadMetadata(path)
	if len(m.Creators) != 2 || m.Creators[0].Name != "Jane Doe" || m.Creators[1].Name != "John Roe" {
		t.Errorf("Creators = %+v", m.Creators)
	}
}

func TestAppendAuthors(t *testing.T) {
	path := makeEPUB(t)
	err := Update(path, func(p *Package) error {
		return p.SetWith(FieldAuthor, "Christopher Tolkien", true)
	})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := ReadMetadata(path)
	if len(m.Creators) != 2 {
		t.Fatalf("Creators = %+v", m.Creators)
	}
	if m.Creators[0].Name != "Le Guin, Ursula K." || m.Creators[1].Name != "Christopher Tolkien" {
		t.Errorf("Creators = %+v", m.Creators)
	}
}

func TestClearField(t *testing.T) {
	path := makeEPUB(t)
	if err := Update(path, func(p *Package) error {
		return p.Set(FieldPublisher, "")
	}); err != nil {
		t.Fatal(err)
	}
	m, _ := ReadMetadata(path)
	if m.Publisher != "" {
		t.Errorf("Publisher = %q, want empty", m.Publisher)
	}
	if m.Title == "" {
		t.Error("clearing the publisher removed the title")
	}
}

// TestNoChangeDoesNotWrite verifies that an update which touches nothing leaves
// the file byte for byte identical.
func TestNoChangeDoesNotWrite(t *testing.T) {
	path := makeEPUB(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Update(path, func(p *Package) error { return nil }); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("an empty update rewrote the file")
	}
}

func TestPermissionPreserved(t *testing.T) {
	path := makeEPUB(t)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Update(path, func(p *Package) error {
		return p.Set(FieldTitle, "Renamed")
	}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", st.Mode().Perm())
	}
}

// TestSeriesMetaShape checks that series is stored in both the Calibre metas
// other tools expect and the standard EPUB 3.3 collection metas.
func TestSeriesMetaShape(t *testing.T) {
	path := makeEPUB(t)
	for i := 0; i < 2; i++ {
		if err := Update(path, func(p *Package) error {
			p.Set(FieldSeries, "Foundation")
			return p.Set(FieldSeriesIndex, "2")
		}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := raitepub.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	md, _ := r.Metadata()["meta"].(map[string]any)
	if md["calibre:series"] != "Foundation" {
		t.Errorf("calibre:series = %v", md["calibre:series"])
	}
	if md["calibre:series_index"] != "2" {
		t.Errorf("calibre:series_index = %v", md["calibre:series_index"])
	}

	opf := zipEntry(t, path, ".opf")
	if got := bytes.Count(opf, []byte(`property="belongs-to-collection"`)); got != 1 {
		t.Errorf("belongs-to-collection appears %d times, want 1:\n%s", got, opf)
	}
	for _, want := range []string{
		`id="shelf-series"`,
		`>Foundation<`,
		`property="collection-type"`,
		`>series<`,
		`property="group-position"`,
		`>2<`,
	} {
		if !bytes.Contains(opf, []byte(want)) {
			t.Errorf("package is missing %s:\n%s", want, opf)
		}
	}
}

// TestSeriesEPUB2 checks that the EPUB 3.3 collection metas are not written to
// an EPUB 2 package.
func TestSeriesEPUB2(t *testing.T) {
	path := makeEPUB2(t)
	if err := Update(path, func(p *Package) error {
		p.Set(FieldSeries, "Foundation")
		return p.Set(FieldSeriesIndex, "2")
	}); err != nil {
		t.Fatal(err)
	}
	opf := zipEntry(t, path, ".opf")
	if bytes.Contains(opf, []byte("belongs-to-collection")) {
		t.Errorf("EPUB 2 package contains EPUB 3 collection metadata:\n%s", opf)
	}
	m, err := ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Series != "Foundation" || m.SeriesIndex != "2" {
		t.Errorf("Series = %q, SeriesIndex = %q", m.Series, m.SeriesIndex)
	}
}

// TestCreatorRefinements checks that editing other fields keeps creator IDs
// and their refinements intact, while replacing a creator drops its stale
// refinements instead of leaving them dangling.
func TestCreatorRefinements(t *testing.T) {
	path := makeEPUB(t)
	r, err := raitepub.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	ed, err := r.Edit()
	if err != nil {
		t.Fatal(err)
	}
	pkg := ed.CurrentPackage()
	for i := range pkg.Metadata.OptionalDC {
		if pkg.Metadata.OptionalDC[i].XMLName.Local == "creator" {
			pkg.Metadata.OptionalDC[i].ID = "author_0"
		}
	}
	pkg.Metadata.Meta = append(pkg.Metadata.Meta,
		pkgmeta.Meta{Property: "file-as", Refines: "#author_0", Value: "Le Guin, Ursula K."})
	data, err := ed.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	// Editing an unrelated field keeps the creator and its refinement.
	if err := Update(path, func(p *Package) error {
		return p.Set(FieldPublisher, "Orbit")
	}); err != nil {
		t.Fatal(err)
	}
	opf := zipEntry(t, path, ".opf")
	if !bytes.Contains(opf, []byte(`refines="#author_0"`)) {
		t.Errorf("creator refinement was lost:\n%s", opf)
	}
	// Reading such a file used to panic in the library's Refines helper.
	m, err := ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.AuthorString() != "Le Guin, Ursula K." {
		t.Errorf("AuthorString = %q", m.AuthorString())
	}

	// Replacing the creator drops the refinement instead of dangling it.
	if err := Update(path, func(p *Package) error {
		return p.Set(FieldAuthor, "Jane Doe")
	}); err != nil {
		t.Fatal(err)
	}
	opf = zipEntry(t, path, ".opf")
	if bytes.Contains(opf, []byte(`refines="#author_0"`)) {
		t.Errorf("stale creator refinement remains:\n%s", opf)
	}
	if !bytes.Contains(opf, []byte(">Jane Doe<")) {
		t.Errorf("new creator missing:\n%s", opf)
	}
}

// TestTitleRefinements checks that the title element and its refinements are
// kept when the title is unchanged, and dropped cleanly when it is replaced.
func TestTitleRefinements(t *testing.T) {
	path := makeEPUB(t)
	r, err := raitepub.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	ed, err := r.Edit()
	if err != nil {
		t.Fatal(err)
	}
	pkg := ed.CurrentPackage()
	for i := range pkg.Metadata.Titles {
		pkg.Metadata.Titles[i].ID = "title_0"
	}
	pkg.Metadata.Meta = append(pkg.Metadata.Meta,
		pkgmeta.Meta{Property: "file-as", Refines: "#title_0", Value: "Left Hand of Darkness, The"},
		pkgmeta.Meta{Property: "title-type", Refines: "#title_0", Value: "main"})
	data, err := ed.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Update(path, func(p *Package) error {
		return p.Set(FieldPublisher, "Orbit")
	}); err != nil {
		t.Fatal(err)
	}
	opf := zipEntry(t, path, ".opf")
	if !bytes.Contains(opf, []byte(`refines="#title_0"`)) {
		t.Errorf("title refinement was lost:\n%s", opf)
	}

	if err := Update(path, func(p *Package) error {
		return p.Set(FieldTitle, "There and Back Again")
	}); err != nil {
		t.Fatal(err)
	}
	opf = zipEntry(t, path, ".opf")
	if bytes.Contains(opf, []byte(`refines="#title_0"`)) {
		t.Errorf("stale title refinement remains:\n%s", opf)
	}
	if !bytes.Contains(opf, []byte(">There and Back Again<")) {
		t.Errorf("new title missing:\n%s", opf)
	}
}

// TestSubjectRefinements checks that subject elements and their refinements
// stay attached while the tag list is unchanged, and are dropped along with the
// old elements when it changes.
func TestSubjectRefinements(t *testing.T) {
	path := makeEPUB(t)
	r, err := raitepub.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	ed, err := r.Edit()
	if err != nil {
		t.Fatal(err)
	}
	pkg := ed.CurrentPackage()
	pkg.Metadata.Meta = append(pkg.Metadata.Meta,
		pkgmeta.Meta{Property: "authority", Refines: "#sf", Value: "BISAC"})
	data, err := ed.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Update(path, func(p *Package) error {
		return p.Set(FieldPublisher, "Orbit")
	}); err != nil {
		t.Fatal(err)
	}
	opf := zipEntry(t, path, ".opf")
	if !bytes.Contains(opf, []byte(`refines="#sf"`)) {
		t.Errorf("subject refinement was lost:\n%s", opf)
	}

	if err := Update(path, func(p *Package) error {
		return p.Set(FieldTags, "fantasy, classic")
	}); err != nil {
		t.Fatal(err)
	}
	opf = zipEntry(t, path, ".opf")
	if bytes.Contains(opf, []byte(`refines="#sf"`)) {
		t.Errorf("stale subject refinement remains:\n%s", opf)
	}
	if !bytes.Contains(opf, []byte(">fantasy<")) || !bytes.Contains(opf, []byte(">classic<")) {
		t.Errorf("new subjects missing:\n%s", opf)
	}
}

// TestSetCollectionPreserved checks that a non-series collection is left alone
// when the series is written.
func TestSetCollectionPreserved(t *testing.T) {
	path := makeEPUB(t)
	r, err := raitepub.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	ed, err := r.Edit()
	if err != nil {
		t.Fatal(err)
	}
	pkg := ed.CurrentPackage()
	pkg.Metadata.Meta = append(pkg.Metadata.Meta,
		pkgmeta.Meta{ID: "boxed", Property: "belongs-to-collection", Value: "The Boxed Set"},
		pkgmeta.Meta{Refines: "#boxed", Property: "collection-type", Value: "set"},
		pkgmeta.Meta{ID: "list", Property: "belongs-to-collection", Value: "Best Novels of All Time"})
	data, err := ed.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	before, err := ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if before.Series != "" {
		t.Errorf("untyped collection read as series: %q", before.Series)
	}

	if err := Update(path, func(p *Package) error {
		return p.Set(FieldSeries, "Foundation")
	}); err != nil {
		t.Fatal(err)
	}
	opf := zipEntry(t, path, ".opf")
	for _, want := range []string{">The Boxed Set<", ">Best Novels of All Time<"} {
		if !bytes.Contains(opf, []byte(want)) {
			t.Errorf("unrelated collection %s was removed:\n%s", want, opf)
		}
	}
	m, err := ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Series != "Foundation" {
		t.Errorf("Series = %q", m.Series)
	}
}

// TestReadSeriesCollection checks reading a package that carries only the
// standard EPUB 3.3 collection metadata.
func TestReadSeriesCollection(t *testing.T) {
	path := makeEPUB(t)
	r, err := raitepub.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	ed, err := r.Edit()
	if err != nil {
		t.Fatal(err)
	}
	pkg := ed.CurrentPackage()
	pkg.Metadata.Meta = append(pkg.Metadata.Meta,
		pkgmeta.Meta{ID: "c1", Property: "belongs-to-collection", Value: "The Culture"},
		pkgmeta.Meta{Refines: "#c1", Property: "collection-type", Value: "series"},
		pkgmeta.Meta{Refines: "#c1", Property: "group-position", Value: "4"},
	)
	data, err := ed.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Series != "The Culture" || m.SeriesIndex != "4" {
		t.Errorf("Series = %q, SeriesIndex = %q", m.Series, m.SeriesIndex)
	}
}

// TestSubtitleEPUB2 checks that the subtitle is stored as a name/content meta,
// which EPUB 2 allows, rather than the nonstandard dc:subtitle element, and
// that the EPUB 3 dcterms:modified meta is stripped.
func TestSubtitleEPUB2(t *testing.T) {
	path := makeEPUB2(t)
	if err := Update(path, func(p *Package) error {
		return p.Set(FieldSubtitle, "A Novel")
	}); err != nil {
		t.Fatal(err)
	}
	m, err := ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Subtitle != "A Novel" {
		t.Errorf("Subtitle = %q", m.Subtitle)
	}
	opf := zipEntry(t, path, ".opf")
	if bytes.Contains(opf, []byte("<subtitle")) {
		t.Errorf("EPUB 2 package contains a subtitle element:\n%s", opf)
	}
	if !bytes.Contains(opf, []byte(`name="subtitle"`)) {
		t.Errorf("EPUB 2 package lacks the subtitle meta:\n%s", opf)
	}
	if bytes.Contains(opf, []byte(`property="dcterms:modified"`)) {
		t.Errorf("EPUB 2 package contains dcterms:modified:\n%s", opf)
	}
}

// TestIdentifierSyncsNCX checks that changing the package identifier updates
// the legacy NCX dtb:uid, which must match.
func TestIdentifierSyncsNCX(t *testing.T) {
	path := makeEPUB2(t)
	const isbn = "9780141439518"
	if err := Update(path, func(p *Package) error {
		return p.Set(FieldIdentifier, isbn)
	}); err != nil {
		t.Fatal(err)
	}
	ncx := zipEntry(t, path, ".ncx")
	if !bytes.Contains(ncx, []byte(`content="`+isbn+`"`)) {
		t.Errorf("NCX dtb:uid was not updated:\n%s", ncx)
	}
	if bytes.Contains(ncx, []byte(`content="urn:isbn:1"`)) {
		t.Errorf("NCX still contains the old identifier:\n%s", ncx)
	}
}

// TestArchiveMimetype checks that a rewrite keeps the OCF mimetype entry
// first, stored, and without an extra field.
func TestArchiveMimetype(t *testing.T) {
	path := makeEPUB2(t)
	if err := Update(path, func(p *Package) error {
		return p.Set(FieldTitle, "Renamed")
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	first := zr.File[0]
	if first.Name != "mimetype" {
		t.Errorf("first entry = %q, want mimetype", first.Name)
	}
	if first.Method != zip.Store {
		t.Errorf("mimetype method = %d, want store", first.Method)
	}
	if len(first.Extra) != 0 {
		t.Errorf("mimetype has a %d byte extra field", len(first.Extra))
	}
}

// TestReadMetadataSurvivesDependencyPanic checks that a package which trips a
// panic in the underlying editor's metadata parser becomes a normal per-file
// error instead of crashing a shelf scan.
func TestReadMetadataSurvivesDependencyPanic(t *testing.T) {
	path := makeEPUB(t)
	r, err := raitepub.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	ed, err := r.Edit()
	if err != nil {
		t.Fatal(err)
	}
	pkg := ed.CurrentPackage()
	pkg.Metadata.Meta = append(pkg.Metadata.Meta,
		pkgmeta.Meta{Name: "schema:accessMode", Content: "textual"},
		pkgmeta.Meta{Property: "schema:accessMode", Value: "visual"},
	)
	data, err := ed.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadMetadata(path); err == nil {
		t.Fatal("expected the parser panic shape to become an error")
	} else if !strings.Contains(err.Error(), "could not read metadata") {
		t.Errorf("error = %v", err)
	}

	if err := Update(path, func(p *Package) error { return nil }); err == nil {
		t.Fatal("expected Update to report the parser panic as an error")
	} else if !strings.Contains(err.Error(), "cannot edit this EPUB") {
		t.Errorf("error = %v", err)
	}
}

// TestSubtitleEPUB3 checks the EPUB 3 representation: a second title refined
// with title-type "subtitle", without duplicates on repeated saves.
func TestSubtitleEPUB3(t *testing.T) {
	path := makeEPUB(t)
	for _, subtitle := range []string{"A Novel", "A Gothic Novel"} {
		if err := Update(path, func(p *Package) error {
			return p.Set(FieldSubtitle, subtitle)
		}); err != nil {
			t.Fatal(err)
		}
		m, err := ReadMetadata(path)
		if err != nil {
			t.Fatal(err)
		}
		if m.Subtitle != subtitle {
			t.Errorf("Subtitle = %q, want %q", m.Subtitle, subtitle)
		}
	}
	opf := zipEntry(t, path, ".opf")
	if bytes.Contains(opf, []byte("<subtitle")) {
		t.Errorf("EPUB 3 package contains a subtitle element:\n%s", opf)
	}
	if got := bytes.Count(opf, []byte(`id="shelf-subtitle"`)); got != 1 {
		t.Errorf("subtitle title appears %d times, want 1:\n%s", got, opf)
	}
	if !bytes.Contains(opf, []byte(`property="title-type"`)) ||
		!bytes.Contains(opf, []byte(`refines="#shelf-subtitle"`)) {
		t.Errorf("subtitle title is not refined:\n%s", opf)
	}
	if !bytes.Contains(opf, []byte("dcterms:modified")) {
		t.Errorf("EPUB 3 package lost dcterms:modified:\n%s", opf)
	}
}
