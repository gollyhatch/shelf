package epub

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// legacyOPF mirrors the package documents older shelf releases wrote: Dublin
// Core elements carry a default xmlns instead of the conventional dc: prefix.
const legacyOPF = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="pub-id" version="3.0">
  <metadata xmlns="http://www.idpf.org/2007/opf">
    <identifier xmlns="http://purl.org/dc/elements/1.1/" id="pub-id">urn:isbn:1</identifier>
    <title xmlns="http://purl.org/dc/elements/1.1/" id="title">The Left Hand of Darkness</title>
    <creator xmlns="http://purl.org/dc/elements/1.1/" id="author">Le Guin, Ursula K.</creator>
    <language xmlns="http://purl.org/dc/elements/1.1/">en</language>
    <meta property="dcterms:modified">2026-01-01T00:00:00Z</meta>
  </metadata>
  <manifest/>
  <spine/>
</package>`

// makeCollidingEPUB writes a fixture with a name/content meta and a property
// meta that share a key.
func makeCollidingEPUB(t *testing.T) string {
	t.Helper()
	return makeEPUBWith(t,
		`<meta name="schema:accessMode" content="textual"/>`+
			`<meta property="schema:accessMode">visual</meta>`)
}

// TestRepairRestoresDublinCorePrefixes checks that repair rewrites a package
// document written with default DC namespaces back to the dc: form, without
// changing metadata values. It also checks dry runs and idempotence.
func TestRepairRestoresDublinCorePrefixes(t *testing.T) {
	path := writeEPUB(t, legacyOPF)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(zipEntry(t, path, ".opf"), []byte(`xmlns:dc=`)) {
		t.Fatal("fixture already has dc: prefixes; it cannot exercise repair")
	}

	// A dry run reports the need for a rewrite without touching the file.
	changed, err := Repair(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("repair did not detect the missing dc: prefixes")
	}
	afterDryRun, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, afterDryRun) {
		t.Error("the dry run rewrote the file")
	}

	// The real repair rewrites it.
	changed, err = Repair(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("repair reported no change")
	}
	opf := zipEntry(t, path, ".opf")
	for _, want := range []string{
		`xmlns:dc="` + dublinCoreNS + `"`,
		`<dc:title`,
		`<dc:creator`,
		`<dc:identifier`,
	} {
		if !bytes.Contains(opf, []byte(want)) {
			t.Errorf("repaired OPF is missing %q:\n%s", want, opf)
		}
	}
	if bytes.Contains(opf, []byte(`xmlns="`+dublinCoreNS+`"`)) {
		t.Errorf("repaired OPF still uses a default DC namespace:\n%s", opf)
	}

	m, err := ReadMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "The Left Hand of Darkness" {
		t.Errorf("title after repair = %q", m.Title)
	}

	// A second pass leaves the file byte-for-byte alone.
	afterRepair, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed, err = Repair(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("a second repair reported a change")
	}
	afterSecond, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterRepair, afterSecond) {
		t.Error("a second repair rewrote an already repaired file")
	}
}

// TestRepairFixesMetaCollisions checks that repair normalizes the colliding
// name/content and property metas. Reading already tolerates the collisions;
// repair exists to clean up files written by older releases and by other tools.
func TestRepairFixesMetaCollisions(t *testing.T) {
	path := makeCollidingEPUB(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMetadata(path); err != nil {
		t.Fatalf("colliding metas should already be readable: %v", err)
	}

	changed, err := Repair(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("repair did not detect the colliding metas")
	}
	afterDryRun, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, afterDryRun) {
		t.Error("the dry run rewrote the file")
	}

	changed, err = Repair(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("repair reported no change")
	}

	opf := zipEntry(t, path, ".opf")
	if bytes.Contains(opf, []byte(`name="schema:accessMode"`)) {
		t.Errorf("repaired OPF still has the name form:\n%s", opf)
	}
	if got := bytes.Count(opf, []byte(`property="schema:accessMode"`)); got != 2 {
		t.Errorf("property form count = %d, want 2:\n%s", got, opf)
	}
	for _, value := range []string{"textual", "visual"} {
		want := []byte(">" + value + "</meta>")
		if !bytes.Contains(opf, want) {
			t.Errorf("repaired OPF is missing %q:\n%s", want, opf)
		}
	}

	m, err := ReadMetadata(path)
	if err != nil {
		t.Fatalf("reading the repaired book: %v", err)
	}
	if m.Title != "The Left Hand of Darkness" {
		t.Errorf("title after repair = %q", m.Title)
	}
	if err := Update(path, func(*Package) error { return nil }); err != nil {
		t.Fatalf("editing the repaired book: %v", err)
	}

	// A second pass leaves the file byte-for-byte alone.
	afterRepair, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed, err = Repair(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("a second repair reported a change")
	}
	afterSecond, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterRepair, afterSecond) {
		t.Error("a second repair rewrote an already repaired file")
	}
}

func TestRepairMetaCollisionsEPUB2(t *testing.T) {
	opf := []byte(`<?xml version="1.0"?><package version="2.0"><metadata>` +
		`<meta name="schema:accessMode" content="textual"/>` +
		`<meta property="schema:accessMode">visual</meta>` +
		`</metadata></package>`)
	got := string(repairMetaCollisions(opf))
	if !strings.Contains(got, `<meta name="schema:accessMode" content="textual"/>`) {
		t.Errorf("EPUB 2 name form was not kept:\n%s", got)
	}
	if strings.Contains(got, `property="schema:accessMode"`) {
		t.Errorf("EPUB 2 property duplicate was not removed:\n%s", got)
	}
}

func TestRepairMetaCollisionsCalibre(t *testing.T) {
	opf := []byte(`<?xml version="1.0"?><package version="3.0"><metadata>` +
		`<meta name="calibre:series" content="Series"/>` +
		`<meta property="calibre:series">Duplicate</meta>` +
		`</metadata></package>`)
	got := string(repairMetaCollisions(opf))
	if !strings.Contains(got, `<meta name="calibre:series" content="Series"/>`) {
		t.Errorf("Calibre name form was not kept:\n%s", got)
	}
	if strings.Contains(got, `property="calibre:series"`) {
		t.Errorf("Calibre property duplicate was not removed:\n%s", got)
	}
}

func TestRepairMetaCollisionsNoop(t *testing.T) {
	opf := []byte(`<?xml version="1.0"?><package version="3.0"><metadata>` +
		`<meta property="schema:accessMode">visual</meta>` +
		`<meta name="cover" content="cover.jpg"/>` +
		`</metadata></package>`)
	if got := repairMetaCollisions(opf); !bytes.Equal(got, opf) {
		t.Errorf("a package without collisions was changed:\n%s", got)
	}
}

func TestRepairWithoutPackageDocument(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("mimetype")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(Mimetype)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "no-opf.epub")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Repair(path, false); err == nil {
		t.Fatal("repair accepted an EPUB without a package document")
	}
}

func TestRepairRejectsInvalidArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.epub")
	if err := os.WriteFile(path, []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Repair(path, false); err == nil {
		t.Fatal("repair accepted an invalid archive")
	}
}
