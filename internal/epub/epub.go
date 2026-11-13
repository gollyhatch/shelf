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
	"slices"
	"strings"

	raitepub "github.com/raitucarp/epub"
	pkgmeta "github.com/raitucarp/epub/pkg"
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

// ReadMetadata reads the Dublin Core metadata of an EPUB file without loading
// the rest of the publication. Only the mimetype, the OCF container and the
// package document are decompressed, so the cost does not scale with the book's
// images or chapters.
func ReadMetadata(name string) (meta Metadata, err error) {
	// Treat an unexpected parser panic as a per-file error so one bad EPUB
	// cannot crash a shelf scan.
	defer func() {
		if r := recover(); r != nil {
			meta = Metadata{}
			err = fmt.Errorf("%s: could not read metadata: %v", filepath.Base(name), r)
		}
	}()

	pkg, err := readPackageDocument(name)
	if err != nil {
		return Metadata{}, fmt.Errorf("%s: %w", filepath.Base(name), err)
	}
	// The current editor cannot open a package where a name/content meta is
	// followed by a property meta with the same key. Keep reporting such a
	// book as unreadable so the repair flow is offered, even though its
	// metadata could be parsed.
	if err := checkMetaCollisions(pkg); err != nil {
		return Metadata{}, fmt.Errorf("%s: could not read metadata: %w", filepath.Base(name), err)
	}
	return metadataFromPackage(pkg), nil
}

// checkMetaCollisions reports a meta key that is first written as a
// name/content entry and later as a property entry. The EPUB editor's metadata
// parser panics on that shape, so the read path keeps flagging it as unreadable
// until repair normalizes the keys.
func checkMetaCollisions(pkg *pkgmeta.Package) error {
	if pkg == nil {
		return nil
	}
	names := make(map[string]bool)
	for _, m := range pkg.Metadata.Meta {
		if m.Property == "" {
			names[m.Name] = true
			continue
		}
		if names[m.Property] {
			return fmt.Errorf("meta key %q is defined both as name and property", m.Property)
		}
	}
	return nil
}

// readPackageDocument opens an EPUB and reads only the entries needed to find
// and parse its package document.
func readPackageDocument(name string) (*pkgmeta.Package, error) {
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
	var pkg pkgmeta.Package
	if err := xml.Unmarshal(opfData, &pkg); err != nil {
		return nil, fmt.Errorf("%s: %w", opfName, err)
	}
	return &pkg, nil
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

// metadataFromPackage maps a parsed package document onto our Metadata struct.
func metadataFromPackage(pkg *pkgmeta.Package) Metadata {
	return metadataFromMap(packageMetadataMap(pkg), pkg)
}

// packageMetadataMap mirrors the metadata map built by the EPUB library, so
// the read and edit paths interpret a package document identically.
func packageMetadataMap(pkg *pkgmeta.Package) map[string]any {
	md := make(map[string]any)
	if pkg == nil {
		return md
	}

	identifiers := make([]string, 0, len(pkg.Metadata.Identifiers))
	for _, id := range pkg.Metadata.Identifiers {
		identifiers = append(identifiers, normalizeWhitespace(id.Value))
	}
	md["identifiers"] = identifiers

	titles := make([]string, 0, len(pkg.Metadata.Titles))
	for _, title := range pkg.Metadata.Titles {
		titles = append(titles, normalizeWhitespace(title.Value))
	}
	md["title"] = titles

	languages := make([]string, 0, len(pkg.Metadata.Languages))
	for _, language := range pkg.Metadata.Languages {
		languages = append(languages, normalizeWhitespace(language.Value))
	}
	md["language"] = languages

	for _, optional := range pkg.Metadata.OptionalDC {
		name := optional.XMLName.Local
		values, _ := md[name].([]string)
		md[name] = append(values, normalizeWhitespace(optional.Value))
	}

	meta := make(map[string]any)
	for _, m := range pkg.Metadata.Meta {
		name := m.Property
		if name == "" {
			name = m.Name
			meta[name] = normalizeWhitespace(m.Content)
			continue
		}
		values, _ := meta[name].([]any)
		values = append(values, normalizeWhitespace(m.Value))
		meta[name] = slices.Compact(values)
	}
	md["meta"] = meta
	return md
}

// metadataFromMap maps the library-compatible metadata map onto our Metadata
// struct.
func metadataFromMap(md map[string]any, pkg *pkgmeta.Package) Metadata {
	get := func(key string) []string {
		if v, ok := md[key].([]string); ok {
			return v
		}
		return nil
	}
	first := func(key string) string {
		if s := get(key); len(s) > 0 {
			return strings.TrimSpace(s[0])
		}
		return ""
	}

	var creators []Creator
	for _, name := range get("creator") {
		if name = strings.TrimSpace(name); name != "" {
			creators = append(creators, Creator{Name: name})
		}
	}

	meta, _ := md["meta"].(map[string]any)
	metaValue := func(name string) string {
		switch v := meta[name].(type) {
		case string:
			return strings.TrimSpace(v)
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					return strings.TrimSpace(s)
				}
			}
		}
		return ""
	}

	series, seriesIndex := metaValue("calibre:series"), metaValue("calibre:series_index")
	if name, position, ok := seriesFromCollection(pkg); ok {
		series, seriesIndex = name, position
	}

	subtitle := first("subtitle")
	if subtitle == "" {
		subtitle = metaValue("subtitle")
	}
	if subtitle == "" {
		subtitle = refinedSubtitle(pkg)
	}

	return Metadata{
		Title:       first("title"),
		Subtitle:    subtitle,
		Creators:    creators,
		Subjects:    cleanList(get("subject")),
		Description: first("description"),
		Publisher:   first("publisher"),
		Date:        first("date"),
		Language:    first("language"),
		Rights:      first("rights"),
		Identifier:  first("identifiers"),
		Series:      series,
		SeriesIndex: seriesIndex,
		Read:        ParseReadStatus(metaValue(readMetaName)),
	}
}

// normalizeWhitespace collapses leading, trailing, and repeated internal
// whitespace in a metadata value to a single space, as required by the EPUB
// specification for textual metadata elements.
func normalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

// cleanList trims each element and drops the empty ones.
func cleanList(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// refinedSubtitle returns the EPUB 3 subtitle: a title refined with the
// title-type property set to "subtitle".
func refinedSubtitle(pkg *pkgmeta.Package) string {
	if pkg == nil {
		return ""
	}
	for _, title := range pkg.Metadata.Titles {
		if title.ID == "" {
			continue
		}
		for _, meta := range pkg.Metadata.Meta {
			if strings.TrimPrefix(meta.Refines, "#") != title.ID {
				continue
			}
			if meta.Property == "title-type" && strings.EqualFold(strings.TrimSpace(meta.Value), "subtitle") {
				return strings.TrimSpace(title.Value)
			}
		}
	}
	return ""
}

// seriesFromCollection returns the standard EPUB 3.3 series metadata: the name
// of a belongs-to-collection meta typed as a series and its group-position.
// Untyped collections are left alone because they often describe reading
// lists or other groupings rather than a series.
func seriesFromCollection(pkg *pkgmeta.Package) (name, position string, ok bool) {
	if pkg == nil {
		return "", "", false
	}
	for _, collection := range pkg.Metadata.Meta {
		if collection.Property != "belongs-to-collection" {
			continue
		}
		name = strings.TrimSpace(collection.Value)
		if name == "" {
			continue
		}
		kind := ""
		position = ""
		for _, meta := range pkg.Metadata.Meta {
			if collection.ID == "" || strings.TrimPrefix(meta.Refines, "#") != collection.ID {
				continue
			}
			switch meta.Property {
			case "collection-type":
				kind = strings.TrimSpace(meta.Value)
			case "group-position":
				position = strings.TrimSpace(meta.Value)
			}
		}
		if !strings.EqualFold(kind, "series") {
			continue
		}
		return name, position, true
	}
	return "", "", false
}

// Package is a handle on an EPUB being edited. Field updates are collected in
// memory and flushed to the underlying publication when Update saves it.
type Package struct {
	path  string
	meta  Metadata
	ed    *raitepub.Editor
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
// atomically, preserving the original file permissions.
func Update(name string, fn func(*Package) error) (err error) {
	// Keep a library panic from taking down a batch operation or the TUI.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s: cannot edit this EPUB: %v", filepath.Base(name), r)
		}
	}()

	if _, err := os.Stat(name); err != nil {
		return err
	}
	r, err := raitepub.OpenReader(name)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(name), err)
	}
	ed, err := r.Edit()
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(name), err)
	}

	p := &Package{path: name, meta: metadataFromPackage(r.CurrentSelectedPackage()), ed: ed}
	if err := fn(p); err != nil {
		return err
	}
	if !p.dirty {
		return nil
	}
	return p.save()
}

// save pushes the in-memory metadata onto the editor and rewrites the file.
func (p *Package) save() error {
	ed := p.ed
	pkg := ed.CurrentPackage()
	if pkg == nil {
		return fmt.Errorf("epub: no package document")
	}
	epub2 := strings.HasPrefix(pkg.Version, "2")

	if p.meta.Title != "" {
		setTitle(ed, pkg, p.meta.Title)
	} else {
		dropTitleRefinements(pkg)
		pkg.Metadata.Titles = nil
	}

	setSubtitle(ed, pkg, p.meta.Subtitle, epub2)

	if names := p.meta.CreatorNames(); len(names) > 0 {
		setCreators(ed, pkg, names)
	} else {
		dropCreatorRefinements(pkg)
		ed.RemoveMetadata("creator")
	}

	if len(p.meta.Subjects) > 0 {
		setSubjects(ed, pkg, p.meta.Subjects)
	} else {
		dropSubjectRefinements(pkg)
		ed.RemoveMetadata("subject")
	}

	setOptional(ed, "description", p.meta.Description)
	setOptional(ed, "publisher", p.meta.Publisher)
	setOptional(ed, "rights", p.meta.Rights)
	setOptional(ed, "date", p.meta.Date)

	if p.meta.Language != "" {
		ed.Language(p.meta.Language)
	} else {
		pkg.Metadata.Languages = nil
	}
	if p.meta.Identifier != "" {
		ed.Identifier(p.meta.Identifier)
	} else {
		pkg.Metadata.Identifiers = nil
	}

	setMetaName(ed, "calibre:series", p.meta.Series)
	setMetaName(ed, "calibre:series_index", p.meta.SeriesIndex)
	setSeriesCollection(pkg, p.meta.Series, p.meta.SeriesIndex, epub2)
	setReadMeta(ed, pkg, p.meta.Read, epub2)

	data, err := ed.Build()
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(p.path), err)
	}
	if data, err = finalizeArchive(data, epub2, p.meta.Identifier); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(p.path), err)
	}
	return writeFileAtomic(p.path, data)
}

// subtitleID is the id of the title element that holds an EPUB 3 subtitle.
const subtitleID = "shelf-subtitle"

// setSubtitle stores the subtitle in the representation the package version
// allows. EPUB 3 uses a second title refined with title-type "subtitle";
// EPUB 2 has no subtitle element, so a name/content meta is used. The
// nonstandard dc:subtitle element is never written.
func setSubtitle(ed *raitepub.Editor, pkg *pkgmeta.Package, value string, epub2 bool) {
	ed.RemoveMetadata("subtitle")
	ed.RemoveMeta("subtitle")
	clearSubtitleTitles(pkg)
	if value == "" {
		return
	}
	if epub2 {
		ed.Meta(pkgmeta.Meta{Name: "subtitle", Content: value})
		return
	}
	pkg.Metadata.Titles = append(pkg.Metadata.Titles, pkgmeta.DCTitle{ID: subtitleID, Value: value})
	pkg.Metadata.Meta = append(pkg.Metadata.Meta, pkgmeta.Meta{
		Refines:  "#" + subtitleID,
		Property: "title-type",
		Value:    "subtitle",
	})
}

// clearSubtitleTitles drops title elements refined as subtitles, along with
// their refinements, so repeated saves do not accumulate them.
func clearSubtitleTitles(pkg *pkgmeta.Package) {
	kinds := make(map[string]string)
	for _, meta := range pkg.Metadata.Meta {
		if meta.Property == "title-type" && meta.Refines != "" {
			kinds[strings.TrimPrefix(meta.Refines, "#")] = meta.Value
		}
	}
	removed := make(map[string]bool)
	var titles []pkgmeta.DCTitle
	for _, title := range pkg.Metadata.Titles {
		if title.ID == subtitleID || strings.EqualFold(kinds[title.ID], "subtitle") {
			removed[title.ID] = true
			continue
		}
		titles = append(titles, title)
	}
	if len(removed) == 0 {
		return
	}
	pkg.Metadata.Titles = titles
	dropRefinements(pkg, removed)
}

// setOptional replaces a name-based Dublin Core element, removing it when the
// value is empty.
func setOptional(ed *raitepub.Editor, local, value string) {
	ed.RemoveMetadata(local)
	if value != "" {
		ed.DublinCore(local, value)
	}
}

// setTitle replaces the main title, but leaves an existing title element
// untouched when the value did not change so its refinements stay attached.
func setTitle(ed *raitepub.Editor, pkg *pkgmeta.Package, title string) {
	if len(pkg.Metadata.Titles) > 0 && strings.TrimSpace(pkg.Metadata.Titles[0].Value) == title {
		return
	}
	dropTitleRefinements(pkg)
	ed.Title(title)
}

// dropTitleRefinements removes metas that refine title elements, as those
// refinements would dangle once the titles are replaced.
func dropTitleRefinements(pkg *pkgmeta.Package) {
	ids := make(map[string]bool)
	for _, title := range pkg.Metadata.Titles {
		if title.ID != "" {
			ids[title.ID] = true
		}
	}
	dropRefinements(pkg, ids)
}

// setCreators replaces the creator elements, but keeps them untouched when the
// names did not change so that refinements such as file-as and role stay
// attached.
func setCreators(ed *raitepub.Editor, pkg *pkgmeta.Package, names []string) {
	if slices.Equal(packageCreators(pkg), names) {
		return
	}
	dropCreatorRefinements(pkg)
	ed.Author(names...)
}

// packageCreators returns the creator names in package order.
func packageCreators(pkg *pkgmeta.Package) []string {
	var names []string
	for _, dc := range pkg.Metadata.OptionalDC {
		if dc.XMLName.Local == "creator" {
			names = append(names, strings.TrimSpace(dc.Value))
		}
	}
	return names
}

// dropCreatorRefinements removes metas that refine creator elements.
func dropCreatorRefinements(pkg *pkgmeta.Package) {
	ids := make(map[string]bool)
	for _, dc := range pkg.Metadata.OptionalDC {
		if dc.XMLName.Local == "creator" && dc.ID != "" {
			ids[dc.ID] = true
		}
	}
	dropRefinements(pkg, ids)
}

// dropRefinements removes metas whose refines attribute points at one of ids.
func dropRefinements(pkg *pkgmeta.Package, ids map[string]bool) {
	if len(ids) == 0 {
		return
	}
	var kept []pkgmeta.Meta
	for _, meta := range pkg.Metadata.Meta {
		if meta.Refines != "" && ids[strings.TrimPrefix(meta.Refines, "#")] {
			continue
		}
		kept = append(kept, meta)
	}
	pkg.Metadata.Meta = kept
}

// setSubjects replaces the subject list, but keeps the existing elements
// untouched when the values did not change so refinements such as authority
// and term stay attached.
func setSubjects(ed *raitepub.Editor, pkg *pkgmeta.Package, subjects []string) {
	if slices.Equal(packageSubjects(pkg), subjects) {
		return
	}
	dropSubjectRefinements(pkg)
	ed.Subject(subjects...)
}

// packageSubjects returns the subject values in package order.
func packageSubjects(pkg *pkgmeta.Package) []string {
	var out []string
	for _, dc := range pkg.Metadata.OptionalDC {
		if dc.XMLName.Local == "subject" {
			out = append(out, strings.TrimSpace(dc.Value))
		}
	}
	return out
}

// dropSubjectRefinements removes metas that refine subject elements.
func dropSubjectRefinements(pkg *pkgmeta.Package) {
	ids := make(map[string]bool)
	for _, dc := range pkg.Metadata.OptionalDC {
		if dc.XMLName.Local == "subject" && dc.ID != "" {
			ids[dc.ID] = true
		}
	}
	dropRefinements(pkg, ids)
}

// setMetaName replaces a name/content <meta> entry, removing it when empty.
func setMetaName(ed *raitepub.Editor, name, value string) {
	ed.RemoveMeta(name)
	if value != "" {
		ed.Meta(pkgmeta.Meta{Name: name, Content: value})
	}
}

// setReadMeta stores shelf's read marker. EPUB 3 gets a namespaced property
// meta with a declared prefix; EPUB 2 uses the legacy name/content form.
func setReadMeta(ed *raitepub.Editor, pkg *pkgmeta.Package, read, epub2 bool) {
	ed.RemoveMeta(readMetaName)
	if !read {
		return
	}
	if epub2 {
		ed.Meta(pkgmeta.Meta{Name: readMetaName, Content: "true"})
		return
	}
	ensureMetaPrefix(pkg, readPrefix, readNamespace)
	ed.Meta(pkgmeta.Meta{Property: readMetaName, Value: "true"})
}

// ensureMetaPrefix adds a property prefix declaration when the package does
// not already declare it.
func ensureMetaPrefix(pkg *pkgmeta.Package, prefix, namespace string) {
	if pkg == nil {
		return
	}
	token := prefix + ":"
	for _, field := range strings.Fields(pkg.Prefix) {
		if field == token {
			return
		}
	}
	if pkg.Prefix != "" {
		pkg.Prefix += " "
	}
	pkg.Prefix += token + " " + namespace
}

// seriesCollectionID is the id of the EPUB 3.3 belongs-to-collection meta.
const seriesCollectionID = "shelf-series"

// setSeriesCollection writes the standard EPUB 3.3 collection metadata next to
// the proprietary Calibre tags. EPUB 2 has no equivalent, so it is skipped.
func setSeriesCollection(pkg *pkgmeta.Package, series, index string, epub2 bool) {
	clearSeriesCollection(pkg)
	if epub2 || series == "" {
		return
	}
	pkg.Metadata.Meta = append(pkg.Metadata.Meta,
		pkgmeta.Meta{ID: seriesCollectionID, Property: "belongs-to-collection", Value: series},
		pkgmeta.Meta{Refines: "#" + seriesCollectionID, Property: "collection-type", Value: "series"},
	)
	if index != "" {
		pkg.Metadata.Meta = append(pkg.Metadata.Meta,
			pkgmeta.Meta{Refines: "#" + seriesCollectionID, Property: "group-position", Value: index})
	}
}

// clearSeriesCollection removes existing series collections and their
// refinements, leaving explicitly typed sets and collections alone.
func clearSeriesCollection(pkg *pkgmeta.Package) {
	removed := make(map[string]bool)
	var rest []pkgmeta.Meta
	for _, meta := range pkg.Metadata.Meta {
		if meta.Property == "belongs-to-collection" && isSeriesCollection(pkg, meta) {
			if meta.ID != "" {
				removed[meta.ID] = true
			}
			continue
		}
		rest = append(rest, meta)
	}
	if len(removed) == 0 {
		pkg.Metadata.Meta = rest
		return
	}
	var kept []pkgmeta.Meta
	for _, meta := range rest {
		if meta.Refines != "" && removed[strings.TrimPrefix(meta.Refines, "#")] {
			continue
		}
		kept = append(kept, meta)
	}
	pkg.Metadata.Meta = kept
}

// isSeriesCollection reports whether a belongs-to-collection meta is explicitly
// typed as a series. Untyped collections are preserved.
func isSeriesCollection(pkg *pkgmeta.Package, collection pkgmeta.Meta) bool {
	if collection.ID == "" {
		return false
	}
	for _, meta := range pkg.Metadata.Meta {
		if meta.Property != "collection-type" || strings.TrimPrefix(meta.Refines, "#") != collection.ID {
			continue
		}
		return strings.EqualFold(strings.TrimSpace(meta.Value), "series")
	}
	return false
}

var (
	modifiedMeta = regexp.MustCompile(`<meta\b[^>]*\bproperty="dcterms:modified"[^>]*/>|<meta\b[^>]*\bproperty="dcterms:modified"[^>]*>[^<]*</meta>`)
	ncxUIDTag    = regexp.MustCompile(`<meta\b[^>]*\bname=["']dtb:uid["'][^>]*>`)
	contentAttr  = regexp.MustCompile(`(\bcontent=)(["'])[^"']*(["'])`)
)

// finalizeArchive repairs what the underlying builder gets wrong for existing
// packages: it restores the conventional dc: prefixes on Dublin Core elements,
// normalizes colliding meta keys, strips the EPUB 3 dcterms:modified meta from
// EPUB 2 packages, and keeps the NCX identifier in sync with a changed package
// identifier.
func finalizeArchive(data []byte, epub2 bool, identifier string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		content, err := readZipFile(f)
		if err != nil {
			return nil, err
		}
		switch {
		case isOPF(f.Name):
			content = prefixDublinCore(content)
			content = repairMetaCollisions(content)
			if epub2 {
				content = modifiedMeta.ReplaceAll(content, nil)
			}
		case identifier != "" && isNCX(f.Name):
			content = syncNCXIdentifier(content, identifier)
		}
		hdr := &zip.FileHeader{
			Name:         f.Name,
			Method:       f.Method,
			ModifiedDate: f.ModifiedDate,
			ModifiedTime: f.ModifiedTime,
		}
		hdr.SetMode(f.Mode())
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(content); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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

// syncNCXIdentifier points the NCX dtb:uid at the package identifier.
func syncNCXIdentifier(content []byte, identifier string) []byte {
	quoted := strings.ReplaceAll(escapeXML(identifier), "$", "$$")
	return ncxUIDTag.ReplaceAllFunc(content, func(tag []byte) []byte {
		return []byte(contentAttr.ReplaceAllString(string(tag), "${1}${2}"+quoted+"${3}"))
	})
}

// escapeXML escapes a value for use in an XML attribute.
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
