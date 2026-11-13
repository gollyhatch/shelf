package epub

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"

	pkgmeta "github.com/raitucarp/epub/pkg"
)

const generatedOPF = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="book-id">
  <metadata xmlns="http://www.idpf.org/2007/opf">
    <identifier xmlns="http://purl.org/dc/elements/1.1/" id="book-id">urn:uuid:test</identifier>
    <title xmlns="http://purl.org/dc/elements/1.1/">The Book</title>
    <creator xmlns='http://purl.org/dc/elements/1.1/' id="author">The Author</creator>
    <meta property="dcterms:modified">2026-01-01T00:00:00Z</meta>
    <subject xmlns="http://purl.org/dc/elements/1.1/">Fiction</subject>
    <description xmlns="http://purl.org/dc/elements/1.1/"></description>
  </metadata>
</package>`

func TestPrefixDublinCore(t *testing.T) {
	got := string(prefixDublinCore([]byte(generatedOPF)))

	for _, want := range []string{
		`xmlns:dc="` + dublinCoreNS + `"`,
		`<dc:identifier id="book-id">urn:uuid:test</dc:identifier>`,
		`<dc:title>The Book</dc:title>`,
		`<dc:creator id="author">The Author</dc:creator>`,
		`<dc:subject>Fiction</dc:subject>`,
		`<dc:description></dc:description>`,
		`<meta property="dcterms:modified">`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rewritten OPF is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `xmlns="`+dublinCoreNS+`"`) {
		t.Errorf("rewritten OPF still uses a default DC namespace:\n%s", got)
	}

	// The namespace-aware representation is unchanged.
	var pkg pkgmeta.Package
	if err := xml.Unmarshal([]byte(got), &pkg); err != nil {
		t.Fatalf("rewritten OPF does not parse: %v\n%s", err, got)
	}
	if len(pkg.Metadata.Titles) != 1 || pkg.Metadata.Titles[0].Value != "The Book" {
		t.Errorf("titles after rewrite = %+v", pkg.Metadata.Titles)
	}
	if len(pkg.Metadata.OptionalDC) < 1 {
		t.Errorf("optional DC metadata was lost: %+v", pkg.Metadata.OptionalDC)
	}
}

func TestPrefixDublinCoreIsIdempotent(t *testing.T) {
	once := prefixDublinCore([]byte(generatedOPF))
	twice := prefixDublinCore(once)
	if !bytes.Equal(once, twice) {
		t.Errorf("a second rewrite changed the OPF:\nfirst:\n%s\nsecond:\n%s", once, twice)
	}
}

func TestPrefixDublinCoreAlreadyPrefixed(t *testing.T) {
	in := `<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="` + dublinCoreNS + `">
  <metadata><dc:title>Book</dc:title></metadata>
</package>`
	if got := string(prefixDublinCore([]byte(in))); got != in {
		t.Errorf("already-prefixed OPF was changed:\n%s", got)
	}
}

// TestPrefixDublinCorePairsMetaEndTags reproduces a bug where a DC element
// named "meta" caused every ordinary OPF </meta> to be rewritten as
// </dc:meta>, producing invalid XML.
func TestPrefixDublinCorePairsMetaEndTags(t *testing.T) {
	in := `<package xmlns="http://www.idpf.org/2007/opf">
  <metadata xmlns="http://www.idpf.org/2007/opf">
    <meta xmlns="` + dublinCoreNS + `" property="example">dc value</meta>
    <meta xmlns="http://www.idpf.org/2007/opf" name="calibre:series" content="Lacroix"></meta>
  </metadata>
</package>`
	got := string(prefixDublinCore([]byte(in)))
	if !strings.Contains(got, `<dc:meta property="example">dc value</dc:meta>`) {
		t.Errorf("DC meta was not rewritten correctly:\n%s", got)
	}
	if !strings.Contains(got, `<meta xmlns="http://www.idpf.org/2007/opf" name="calibre:series" content="Lacroix"></meta>`) {
		t.Errorf("ordinary meta was corrupted:\n%s", got)
	}
	if strings.Contains(got, `content="Lacroix"></dc:meta>`) {
		t.Errorf("ordinary meta got a dc: closing tag:\n%s", got)
	}
}

// TestPrefixDublinCoreRepairsMalformedMetaClose checks that the old broken
// output can be corrected by repair.
func TestPrefixDublinCoreRepairsMalformedMetaClose(t *testing.T) {
	in := `<package xmlns="http://www.idpf.org/2007/opf">
  <metadata xmlns:dc="` + dublinCoreNS + `" xmlns="http://www.idpf.org/2007/opf">
    <dc:title>Book</dc:title>
    <meta xmlns="http://www.idpf.org/2007/opf" name="calibre:series" content="Lacroix"></dc:meta>
  </metadata>
</package>`
	got := string(prefixDublinCore([]byte(in)))
	if !strings.Contains(got, `content="Lacroix"></meta>`) {
		t.Errorf("malformed meta close was not repaired:\n%s", got)
	}
	if strings.Contains(got, `content="Lacroix"></dc:meta>`) {
		t.Errorf("malformed meta close remains:\n%s", got)
	}
}

// TestOPFUsesDublinCorePrefix is the regression test for readers that look up
// metadata by qualified name: a save must keep writing dc:title and friends.
func TestOPFUsesDublinCorePrefix(t *testing.T) {
	path := makeEPUB(t)
	if err := Update(path, func(p *Package) error {
		if err := p.Set(FieldTitle, "Renamed"); err != nil {
			return err
		}
		return p.Set(FieldPublisher, "Orbit")
	}); err != nil {
		t.Fatal(err)
	}

	opf := zipEntry(t, path, ".opf")
	for _, want := range []string{
		`xmlns:dc="` + dublinCoreNS + `"`,
		`<dc:title id="title">Renamed</dc:title>`,
		`<dc:publisher id="publisher">Orbit</dc:publisher>`,
		`<dc:creator`,
		`<dc:identifier`,
	} {
		if !bytes.Contains(opf, []byte(want)) {
			t.Errorf("saved OPF is missing %q:\n%s", want, opf)
		}
	}
	if bytes.Contains(opf, []byte(`xmlns="`+dublinCoreNS+`"`)) {
		t.Errorf("saved OPF uses a default DC namespace:\n%s", opf)
	}

	// The namespace-aware package is still intact.
	var pkg pkgmeta.Package
	if err := xml.Unmarshal(opf, &pkg); err != nil {
		t.Fatalf("saved OPF does not parse: %v\n%s", err, opf)
	}
	if len(pkg.Metadata.Titles) != 1 || pkg.Metadata.Titles[0].Value != "Renamed" {
		t.Errorf("titles after save = %+v", pkg.Metadata.Titles)
	}

	m, err := ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "Renamed" || m.Publisher != "Orbit" {
		t.Errorf("metadata after save = %+v", m)
	}
}
