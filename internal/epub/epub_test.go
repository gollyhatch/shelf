package epub

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureOPF is a package document with the structure shelf manages: prefixed
// Dublin Core elements, a unique identifier, Calibre-style author/title ids and
// an EPUB 3 dcterms:modified entry. The comment is a placeholder that tests
// replace with extra metadata.
const fixtureOPF = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0" unique-identifier="pub-id">
  <metadata>
    <!-- extra -->
    <dc:identifier id="pub-id">urn:isbn:1</dc:identifier>
    <dc:title id="title">The Left Hand of Darkness</dc:title>
    <dc:language>en</dc:language>
    <dc:creator id="author">Le Guin, Ursula K.</dc:creator>
    <dc:subject id="sf">Science Fiction</dc:subject>
    <dc:publisher id="publisher">Ace Books</dc:publisher>
    <dc:description>A novel about a planet called Winter.</dc:description>
    <meta property="dcterms:modified">2026-01-01T00:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="chapter-1" href="chapter-1.xhtml" media-type="application/xhtml+xml"/>
    <item id="toc" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
  </manifest>
  <spine toc="toc">
    <itemref idref="chapter-1"/>
  </spine>
</package>`

const fixtureContainer = `<?xml version="1.0" encoding="UTF-8"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`

const fixtureChapter = `<html xmlns="http://www.w3.org/1999/xhtml"><body><h1>One</h1></body></html>`

// writeEPUB builds an EPUB around the given package document and returns its
// path. The mimetype entry is written first and stored, as OCF requires.
func writeEPUB(t *testing.T, opf string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, content string) {
		t.Helper()
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}

	w, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(Mimetype)); err != nil {
		t.Fatal(err)
	}
	write("META-INF/container.xml", fixtureContainer)
	write("OEBPS/content.opf", opf)
	write("OEBPS/chapter-1.xhtml", fixtureChapter)
	write("OEBPS/toc.ncx", `<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><head></head><docTitle><text>Book</text></docTitle></ncx>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// makeEPUB writes a small but valid EPUB and returns its path.
func makeEPUB(t *testing.T) string {
	t.Helper()
	return writeEPUB(t, strings.Replace(fixtureOPF, "<!-- extra -->", "", 1))
}

// makeEPUBWith writes the fixture with extra metadata entries in place of the
// fixture's placeholder comment.
func makeEPUBWith(t *testing.T, extra string) string {
	t.Helper()
	return writeEPUB(t, strings.Replace(fixtureOPF, "<!-- extra -->", extra, 1))
}

// makeEPUB2 downgrades the fixture's package document to EPUB 2 and gives the
// NCX a dtb:uid, as legacy publications have.
func makeEPUB2(t *testing.T) string {
	t.Helper()
	opf := strings.Replace(fixtureOPF, `version="3.0"`, `version="2.0"`, 1)
	opf = strings.Replace(opf, "<!-- extra -->", "", 1)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
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

	w, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(Mimetype)); err != nil {
		t.Fatal(err)
	}
	write("META-INF/container.xml", fixtureContainer)
	write("OEBPS/content.opf", opf)
	write("OEBPS/chapter-1.xhtml", fixtureChapter)
	write("OEBPS/toc.ncx", `<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><head><meta name="dtb:uid" content="urn:isbn:1"/></head><docTitle><text>Book</text></docTitle></ncx>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "book.epub")
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

// TestReadMetadataIgnoresUnrelatedArchiveDamage checks that the reader only
// touches the entries it needs. Harm to another entry must not affect metadata
// loading.
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
	for i := range 4 {
		corrupted[pos+i] ^= 0xff
	}
	if err := os.WriteFile(path, corrupted, 0o644); err != nil {
		t.Fatal(err)
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

// TestSaveKeepsOtherEntriesVerbatim checks that a save only rewrites the
// package document: every other entry keeps its compressed bytes, and the
// package document keeps everything outside the metadata block.
func TestSaveKeepsOtherEntriesVerbatim(t *testing.T) {
	path := makeEPUB(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	compressed := make(map[string][]byte)
	zr, err := zip.NewReader(bytes.NewReader(before), int64(len(before)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		rc, err := f.OpenRaw()
		if err != nil {
			t.Fatal(err)
		}
		if compressed[f.Name], err = io.ReadAll(rc); err != nil {
			t.Fatal(err)
		}
	}

	beforeOPF := zipEntry(t, path, ".opf")

	if err := Update(path, func(p *Package) error {
		return p.Set(FieldPublisher, "Orbit")
	}); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err = zip.NewReader(bytes.NewReader(after), int64(len(after)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		want, ok := compressed[f.Name]
		if !ok {
			t.Fatalf("%s appeared after the save", f.Name)
		}
		rc, err := f.OpenRaw()
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		if f.Name != "OEBPS/content.opf" && !bytes.Equal(got, want) {
			t.Errorf("%s was rewritten", f.Name)
		}
	}

	afterOPF := zipEntry(t, path, ".opf")
	head := func(b []byte) []byte {
		i := bytes.Index(b, []byte("<metadata"))
		if i < 0 {
			t.Fatal("no <metadata>")
		}
		return b[:i]
	}
	if !bytes.Equal(head(beforeOPF), head(afterOPF)) {
		t.Error("the package document before <metadata> changed")
	}
	tail := func(b []byte) []byte {
		i := bytes.Index(b, []byte("</metadata>"))
		if i < 0 {
			t.Fatal("no </metadata>")
		}
		return b[i:]
	}
	if !bytes.Equal(tail(beforeOPF), tail(afterOPF)) {
		t.Error("the package document after </metadata> changed")
	}
	if !bytes.Contains(afterOPF, []byte("<dc:publisher>Orbit</dc:publisher>")) {
		t.Errorf("publisher was not written:\n%s", afterOPF)
	}
}

// TestCreatorAttributesPreserved checks that creator attributes survive edits
// that do not touch the creator, and that replacing the creator drops its
// refinements instead of leaving them dangling.
func TestCreatorAttributesPreserved(t *testing.T) {
	opf := strings.Replace(fixtureOPF, `xmlns:dc="http://purl.org/dc/elements/1.1/"`,
		`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf"`, 1)
	opf = strings.Replace(opf, `<dc:creator id="author">`,
		`<dc:creator id="author" opf:role="aut" opf:file-as="Le Guin, Ursula K.">`, 1)
	opf = strings.Replace(opf, "<!-- extra -->", `<meta property="file-as" refines="#author">Le Guin, Ursula K.</meta>`, 1)
	path := writeEPUB(t, opf)

	if err := Update(path, func(p *Package) error {
		return p.Set(FieldPublisher, "Orbit")
	}); err != nil {
		t.Fatal(err)
	}
	saved := zipEntry(t, path, ".opf")
	if !bytes.Contains(saved, []byte(`opf:role="aut" opf:file-as="Le Guin, Ursula K."`)) {
		t.Errorf("creator attributes were lost:\n%s", saved)
	}
	if !bytes.Contains(saved, []byte(`refines="#author"`)) {
		t.Errorf("creator refinement was lost:\n%s", saved)
	}

	if err := Update(path, func(p *Package) error {
		return p.Set(FieldAuthor, "Jane Doe")
	}); err != nil {
		t.Fatal(err)
	}
	saved = zipEntry(t, path, ".opf")
	if bytes.Contains(saved, []byte(`opf:role="aut"`)) {
		t.Errorf("stale creator attributes remain:\n%s", saved)
	}
	if bytes.Contains(saved, []byte(`refines="#author"`)) {
		t.Errorf("stale creator refinement remains:\n%s", saved)
	}
	if !bytes.Contains(saved, []byte(">Jane Doe<")) {
		t.Errorf("new creator missing:\n%s", saved)
	}
}

// TestSeriesMetaShape checks that series is stored in both the Calibre metas
// other tools expect and the standard EPUB 3.3 collection metas.
func TestSeriesMetaShape(t *testing.T) {
	path := makeEPUB(t)
	for range 2 {
		if err := Update(path, func(p *Package) error {
			p.Set(FieldSeries, "Foundation")
			return p.Set(FieldSeriesIndex, "2")
		}); err != nil {
			t.Fatal(err)
		}
	}

	m, err := ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Series != "Foundation" || m.SeriesIndex != "2" {
		t.Errorf("Series = %q, SeriesIndex = %q", m.Series, m.SeriesIndex)
	}

	opf := zipEntry(t, path, ".opf")
	for _, want := range []string{`name="calibre:series"`, `name="calibre:series_index"`} {
		if got := bytes.Count(opf, []byte(want)); got != 1 {
			t.Errorf("%s appears %d times, want 1:\n%s", want, got, opf)
		}
	}
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

// TestTitleRefinements checks that the title element and its refinements are
// kept when the title is unchanged, and dropped cleanly when it is replaced.
func TestTitleRefinements(t *testing.T) {
	path := makeEPUBWith(t,
		`<meta property="file-as" refines="#title">Left Hand of Darkness, The</meta>`+
			`<meta property="title-type" refines="#title">main</meta>`)

	if err := Update(path, func(p *Package) error {
		return p.Set(FieldPublisher, "Orbit")
	}); err != nil {
		t.Fatal(err)
	}
	opf := zipEntry(t, path, ".opf")
	if !bytes.Contains(opf, []byte(`refines="#title"`)) {
		t.Errorf("title refinement was lost:\n%s", opf)
	}

	if err := Update(path, func(p *Package) error {
		return p.Set(FieldTitle, "There and Back Again")
	}); err != nil {
		t.Fatal(err)
	}
	opf = zipEntry(t, path, ".opf")
	if bytes.Contains(opf, []byte(`refines="#title"`)) {
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
	path := makeEPUBWith(t, `<meta property="authority" refines="#sf">BISAC</meta>`)

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
	path := makeEPUBWith(t,
		`<meta id="boxed" property="belongs-to-collection">The Boxed Set</meta>`+
			`<meta refines="#boxed" property="collection-type">set</meta>`+
			`<meta id="list" property="belongs-to-collection">Best Novels of All Time</meta>`)

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
	path := makeEPUBWith(t,
		`<meta id="c1" property="belongs-to-collection">The Culture</meta>`+
			`<meta refines="#c1" property="collection-type">series</meta>`+
			`<meta refines="#c1" property="group-position">4</meta>`)

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

// TestMetaKeyCollisionsAreReadable checks that a package with the same key in
// name/content and property form is read and edited normally, and that the
// untouched entries survive the edit.
func TestMetaKeyCollisionsAreReadable(t *testing.T) {
	path := makeEPUBWith(t,
		`<meta name="schema:accessMode" content="textual"/>`+
			`<meta property="schema:accessMode">visual</meta>`)

	m, err := ReadMetadata(path)
	if err != nil {
		t.Fatalf("package with colliding meta keys is unreadable: %v", err)
	}
	if m.Title != "The Left Hand of Darkness" {
		t.Errorf("Title = %q", m.Title)
	}

	if err := Update(path, func(p *Package) error {
		return p.Set(FieldPublisher, "Orbit")
	}); err != nil {
		t.Fatalf("package with colliding meta keys is not editable: %v", err)
	}
	opf := zipEntry(t, path, ".opf")
	if !bytes.Contains(opf, []byte(`name="schema:accessMode"`)) ||
		!bytes.Contains(opf, []byte(`property="schema:accessMode"`)) {
		t.Errorf("colliding metas were dropped:\n%s", opf)
	}
	if !bytes.Contains(opf, []byte("<dc:publisher>Orbit</dc:publisher>")) {
		t.Errorf("publisher was not written:\n%s", opf)
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
