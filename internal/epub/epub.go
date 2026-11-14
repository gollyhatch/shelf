package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// maxMetadataEntry bounds the container and package documents we decompress.
// Real-world OPF files are far smaller; the limit only protects a scan from a
// malicious or corrupt archive.
const maxMetadataEntry = 8 << 20

// Read status is stored in shelf's own namespaced meta entry so it cannot
// collide with Dublin Core or other official EPUB metadata.
const (
	readMetaName  = "shelf:read"
	readPrefix    = "shelf"
	readNamespace = "https://github.com/gollyhatch/shelf#"
)

// subtitleID is the id of the title element that holds an EPUB 3 subtitle.
const subtitleID = "shelf-subtitle"

// seriesCollectionID is the id of the EPUB 3.3 belongs-to-collection meta.
const seriesCollectionID = "shelf-series"

// ReadMetadata reads the Dublin Core metadata of an EPUB file without loading
// the rest of the publication. Only the mimetype, the OCF container and the
// package document are decompressed, so the cost does not scale with the book's
// images or chapters.
func ReadMetadata(name string) (meta Metadata, err error) {
	doc, err := readPackageDocument(name)
	if err != nil {
		return Metadata{}, fmt.Errorf("%s: %w", filepath.Base(name), err)
	}
	return doc.metadata(), nil
}

// readPackageDocument opens an EPUB and reads only the entries needed to find
// and parse its package document.
func readPackageDocument(name string) (*opfDocument, error) {
	zr, err := zip.OpenReader(name)
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	mimetype, err := readArchiveFile(zr.File, "mimetype", 1024)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(mimetype)) != Mimetype {
		return nil, fmt.Errorf("mimetype mismatch")
	}

	containerData, err := readArchiveFile(zr.File, "META-INF/container.xml", maxMetadataEntry)
	if err != nil {
		return nil, err
	}
	var container struct {
		RootFiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(containerData, &container); err != nil {
		return nil, fmt.Errorf("META-INF/container.xml: %w", err)
	}
	if len(container.RootFiles) == 0 || strings.TrimSpace(container.RootFiles[0].FullPath) == "" {
		return nil, fmt.Errorf("META-INF/container.xml has no rootfile")
	}

	opfName := path.Clean(strings.TrimPrefix(strings.TrimSpace(container.RootFiles[0].FullPath), "/"))
	opfData, err := readArchiveFile(zr.File, opfName, maxMetadataEntry)
	if err != nil {
		return nil, err
	}
	doc, err := parseOPF(opfName, opfData)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", opfName, err)
	}
	return doc, nil
}

// readArchiveFile returns the decompressed contents of one ZIP entry, refusing
// entries that exceed limit bytes.
func readArchiveFile(files []*zip.File, name string, limit int64) ([]byte, error) {
	var file *zip.File
	for _, f := range files {
		if f.Name == name {
			file = f
			break
		}
	}
	if file == nil {
		return nil, fmt.Errorf("%s: no such file in the EPUB", name)
	}
	if file.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("%s: too large (%d bytes)", name, file.UncompressedSize64)
	}

	rc, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: too large", name)
	}
	return data, nil
}

// Package is a handle on an EPUB being edited. Field updates are collected in
// memory; Update writes the changed metadata back into the package document.
type Package struct {
	path  string
	doc   *opfDocument
	meta  Metadata
	orig  Metadata
	dirty bool
}

// Metadata returns the current (possibly edited) metadata.
func (p *Package) Metadata() Metadata { return p.meta }

// Set replaces the value of a field. An empty value removes the element(s).
// Multi valued fields accept "a; b" for authors and "a, b" for tags.
func (p *Package) Set(f Field, value string) error {
	return p.set(f, value, false)
}

// SetWith appends to the existing value of a field instead of replacing it.
func (p *Package) SetWith(f Field, value string, appendMode bool) error {
	return p.set(f, value, appendMode)
}

func (p *Package) set(f Field, value string, appendMode bool) error {
	value = strings.TrimSpace(value)
	switch f {
	case FieldTitle:
		p.meta.Title = appendText(p.meta.Title, value, appendMode)
	case FieldSubtitle:
		p.meta.Subtitle = appendText(p.meta.Subtitle, value, appendMode)
	case FieldAuthor:
		cs := ParseCreatorList(value)
		if appendMode {
			p.meta.Creators = append(p.meta.Creators, cs...)
		} else {
			p.meta.Creators = cs
		}
	case FieldTags:
		ss := SplitList(value, ",")
		if appendMode {
			p.meta.Subjects = append(p.meta.Subjects, ss...)
		} else {
			p.meta.Subjects = ss
		}
	case FieldSeries:
		p.meta.Series = appendText(p.meta.Series, value, appendMode)
	case FieldSeriesIndex:
		p.meta.SeriesIndex = value
	case FieldDescription:
		p.meta.Description = appendText(p.meta.Description, value, appendMode)
	case FieldPublisher:
		p.meta.Publisher = appendText(p.meta.Publisher, value, appendMode)
	case FieldDate:
		p.meta.Date = appendText(p.meta.Date, value, appendMode)
	case FieldLanguage:
		p.meta.Language = appendText(p.meta.Language, value, appendMode)
	case FieldRights:
		p.meta.Rights = appendText(p.meta.Rights, value, appendMode)
	case FieldIdentifier:
		p.meta.Identifier = appendText(p.meta.Identifier, value, appendMode)
	case FieldRead:
		p.meta.Read = ParseReadStatus(value)
	default:
		return fmt.Errorf("epub: unknown field %q", f)
	}
	p.dirty = true
	return nil
}

// appendText implements --append for single valued fields by joining with ", ".
func appendText(current, value string, appendMode bool) string {
	if appendMode && current != "" && value != "" {
		return current + ", " + value
	}
	return value
}

// SetCreators replaces the whole list of authors. An empty list removes them.
func (p *Package) SetCreators(cs []Creator) error {
	p.meta.Creators = cs
	p.dirty = true
	return nil
}

// SetSubjects replaces the whole list of tags. An empty list removes them.
func (p *Package) SetSubjects(ss []string) error {
	p.meta.Subjects = ss
	p.dirty = true
	return nil
}

// Update reads an EPUB, applies fn to its metadata and writes the result back
// atomically, preserving the original file permissions. Entries other than the
// package document are copied in compressed form, so they stay byte-identical.
func Update(name string, fn func(*Package) error) error {
	if _, err := os.Stat(name); err != nil {
		return err
	}
	doc, err := readPackageDocument(name)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(name), err)
	}

	p := &Package{path: name, doc: doc, meta: doc.metadata()}
	p.orig = cloneMetadata(p.meta)
	if err := fn(p); err != nil {
		return err
	}
	if !p.dirty {
		return nil
	}
	return p.save()
}

// cloneMetadata copies a Metadata value, including its slices, so later edits
// cannot alias the original.
func cloneMetadata(m Metadata) Metadata {
	m.Creators = append([]Creator(nil), m.Creators...)
	m.Subjects = append([]string(nil), m.Subjects...)
	return m
}

// save rewrites the package document and the archive around it.
func (p *Package) save() error {
	opf, err := editOPF(p.doc, p.orig, p.meta)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(p.path), err)
	}

	zr, err := zip.OpenReader(p.path)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(p.path), err)
	}
	defer zr.Close()

	replace := map[string][]byte{p.doc.name: opf}
	if p.meta.Identifier != p.orig.Identifier && p.meta.Identifier != "" {
		for _, f := range zr.File {
			if !isNCX(f.Name) {
				continue
			}
			content, err := readZipFile(f)
			if err != nil {
				return fmt.Errorf("%s: %w", filepath.Base(p.path), err)
			}
			replace[f.Name] = syncNCXIdentifier(content, p.meta.Identifier)
		}
	}

	data, err := rewriteArchive(&zr.Reader, replace)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(p.path), err)
	}
	return writeFileAtomic(p.path, data)
}

var (
	ncxUIDTag   = regexp.MustCompile(`<meta\b[^>]*\bname=["']dtb:uid["'][^>]*>`)
	contentAttr = regexp.MustCompile(`(\bcontent=)(["'])[^"']*(["'])`)
)

// syncNCXIdentifier points the NCX dtb:uid at the package identifier.
func syncNCXIdentifier(content []byte, identifier string) []byte {
	quoted := strings.ReplaceAll(escapeXML(identifier), "$", "$$")
	return ncxUIDTag.ReplaceAllFunc(content, func(tag []byte) []byte {
		return []byte(contentAttr.ReplaceAllString(string(tag), "${1}${2}"+quoted+"${3}"))
	})
}

// readZipFile reads one archive entry.
func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// escapeXML escapes a value for use in XML text or an attribute.
func escapeXML(s string) string {
	var buf bytes.Buffer
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		return s
	}
	return buf.String()
}

// writeFileAtomic writes data to path via a temporary file and rename so a
// failed write can never truncate the original.
func writeFileAtomic(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".shelf-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
