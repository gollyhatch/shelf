package epub

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// opfNamespace is the namespace of the package document's own elements.
const opfNamespace = "http://www.idpf.org/2007/opf"

// opfChild is one element (or comment) directly inside <metadata>. The byte
// span points into the package document, so an untouched child can be copied
// verbatim instead of being re-serialized.
type opfChild struct {
	local    string // element local name; empty for a comment
	id       string
	refines  string
	property string
	name     string
	content  string
	text     string
	start    int
	end      int
}

// opfDocument is a parsed package document. It keeps the original bytes and
// the spans of the metadata block so an edit can replace exactly the parts that
// change.
type opfDocument struct {
	name             string
	data             []byte
	version          string
	prefix           string
	uniqueIdentifier string
	defaultNS        string
	dcDeclared       bool
	opfDeclared      bool
	packageTagEnd    int
	hasMetadata      bool
	metaStart        int
	metaOpenEnd      int
	metaCloseStart   int
	metaSelfClosing  bool
	children         []opfChild
}

// parseOPF parses the package document and records where the parts an edit
// needs live. Elements are matched by local name, which is how the previous
// reader interpreted them, so namespaced and prefixed documents read alike.
func parseOPF(name string, data []byte) (*opfDocument, error) {
	if err := validateXML(data); err != nil {
		return nil, err
	}
	doc := &opfDocument{name: name, data: data}
	d := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	inMetadata := false
	var child *opfChild

	for {
		off := int(d.InputOffset())
		tok, err := d.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case depth == 0:
				if t.Name.Local != "package" {
					return nil, fmt.Errorf("root element is %q, want package", t.Name.Local)
				}
				doc.packageTagEnd = int(d.InputOffset())
				doc.readAttrs(t.Attr)
			case depth == 1 && !doc.hasMetadata && t.Name.Local == "metadata":
				doc.hasMetadata = true
				doc.metaStart = off
				doc.metaOpenEnd = int(d.InputOffset())
				doc.metaSelfClosing = isSelfClosingTag(data[off:doc.metaOpenEnd])
				doc.readAttrs(t.Attr)
				inMetadata = !doc.metaSelfClosing
			case inMetadata && depth == 2:
				child = &opfChild{local: t.Name.Local, start: off}
				for _, a := range t.Attr {
					if a.Name.Space != "" {
						continue
					}
					switch a.Name.Local {
					case "id":
						child.id = a.Value
					case "refines":
						child.refines = a.Value
					case "property":
						child.property = a.Value
					case "name":
						child.name = a.Value
					case "content":
						child.content = a.Value
					}
				}
			}
			depth++

		case xml.EndElement:
			depth--
			if inMetadata && depth == 1 {
				doc.metaCloseStart = off
				inMetadata = false
			} else if inMetadata && depth == 2 && child != nil {
				child.end = int(d.InputOffset())
				doc.children = append(doc.children, *child)
				child = nil
			}

		case xml.CharData:
			if child != nil && depth == 3 {
				child.text += string(t)
			}

		case xml.Comment:
			if inMetadata && depth == 2 {
				doc.children = append(doc.children, opfChild{start: off, end: int(d.InputOffset())})
			}
		}
	}

	if doc.packageTagEnd == 0 {
		return nil, fmt.Errorf("no package element")
	}
	return doc, nil
}

// validateXML rejects a package document whose elements do not nest correctly.
// RawToken does not check start and end tags against each other, so the scan
// below would happily pair a <meta> with a </dc:meta>; the repair command can
// fix exactly that damage, but only if reading reports it.
func validateXML(data []byte) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		if _, err := d.Token(); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// readAttrs picks the package attributes an edit needs. Attributes declared on
// the metadata element count too, since generated children inherit them.
func (d *opfDocument) readAttrs(attrs []xml.Attr) {
	for _, a := range attrs {
		switch {
		case a.Name.Space == "" && a.Name.Local == "xmlns":
			d.defaultNS = a.Value
		case a.Name.Space == "xmlns" && a.Name.Local == "dc":
			d.dcDeclared = a.Value == dublinCoreNS
		case a.Name.Space == "xmlns" && a.Name.Local == "opf":
			d.opfDeclared = a.Value == opfNamespace
		case a.Name.Space != "":
			continue
		case a.Name.Local == "version":
			d.version = a.Value
		case a.Name.Local == "prefix":
			d.prefix = a.Value
		case a.Name.Local == "unique-identifier":
			d.uniqueIdentifier = a.Value
		}
	}
}

// isSelfClosingTag reports whether a raw start tag ends with "/>".
func isSelfClosingTag(tag []byte) bool {
	return bytes.HasSuffix(bytes.TrimRight(tag, " \t\r\n"), []byte("/>"))
}

// raw returns the original bytes of a metadata child.
func (d *opfDocument) raw(i int) []byte {
	return d.data[d.children[i].start:d.children[i].end]
}

// metadata maps the parsed children onto the editable metadata subset, using
// the same precedence the previous reader had: name/content metas win over
// property metas, and the EPUB 3 series collection wins over Calibre's tags.
func (d *opfDocument) metadata() Metadata {
	var (
		identifiers []string
		titles      []string
		languages   []string
	)
	optional := make(map[string][]string)
	named := make(map[string]string)
	valued := make(map[string][]string)

	for _, c := range d.children {
		switch c.local {
		case "":
			continue
		case "meta":
			if c.property == "" {
				named[c.name] = normalizeWhitespace(c.content)
				continue
			}
			valued[c.property] = append(valued[c.property], normalizeWhitespace(c.text))
		case "identifier":
			identifiers = append(identifiers, normalizeWhitespace(c.text))
		case "title":
			titles = append(titles, normalizeWhitespace(c.text))
		case "language":
			languages = append(languages, normalizeWhitespace(c.text))
		default:
			optional[c.local] = append(optional[c.local], normalizeWhitespace(c.text))
		}
	}

	get := func(key string) []string {
		switch key {
		case "identifiers":
			return identifiers
		case "title":
			return titles
		case "language":
			return languages
		}
		return optional[key]
	}
	first := func(key string) string {
		if values := get(key); len(values) > 0 {
			return strings.TrimSpace(values[0])
		}
		return ""
	}
	metaValue := func(name string) string {
		if value, ok := named[name]; ok {
			return strings.TrimSpace(value)
		}
		if values := valued[name]; len(values) > 0 {
			return strings.TrimSpace(values[0])
		}
		return ""
	}

	var creators []Creator
	for _, name := range get("creator") {
		if name = strings.TrimSpace(name); name != "" {
			creators = append(creators, Creator{Name: name})
		}
	}

	series, seriesIndex := metaValue("calibre:series"), metaValue("calibre:series_index")
	if name, position, ok := d.seriesCollection(); ok {
		series, seriesIndex = name, position
	}

	subtitle := first("subtitle")
	if subtitle == "" {
		subtitle = metaValue("subtitle")
	}
	if subtitle == "" {
		subtitle = d.refinedSubtitle()
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

// refinedSubtitle returns the EPUB 3 subtitle: a title refined with the
// title-type property set to "subtitle".
func (d *opfDocument) refinedSubtitle() string {
	for _, title := range d.children {
		if title.local != "title" || title.id == "" {
			continue
		}
		for _, meta := range d.children {
			if meta.local != "meta" || meta.property != "title-type" {
				continue
			}
			if strings.TrimPrefix(meta.refines, "#") == title.id &&
				strings.EqualFold(strings.TrimSpace(meta.text), "subtitle") {
				return strings.TrimSpace(title.text)
			}
		}
	}
	return ""
}

// seriesCollection returns the standard EPUB 3.3 series metadata: the name of a
// belongs-to-collection meta typed as a series and its group-position. Untyped
// collections are left alone because they often describe reading lists or other
// groupings rather than a series.
func (d *opfDocument) seriesCollection() (name, position string, ok bool) {
	for _, collection := range d.children {
		if collection.local != "meta" || collection.property != "belongs-to-collection" {
			continue
		}
		name = strings.TrimSpace(collection.text)
		if name == "" {
			continue
		}
		kind, groupPosition := "", ""
		for _, meta := range d.children {
			if collection.id == "" || strings.TrimPrefix(meta.refines, "#") != collection.id {
				continue
			}
			switch meta.property {
			case "collection-type":
				kind = strings.TrimSpace(meta.text)
			case "group-position":
				groupPosition = strings.TrimSpace(meta.text)
			}
		}
		if !strings.EqualFold(kind, "series") {
			continue
		}
		return name, groupPosition, true
	}
	return "", "", false
}

// titleIsSubtitle reports whether a title element is refined as a subtitle.
func (d *opfDocument) titleIsSubtitle(title opfChild) bool {
	if title.id == "" {
		return false
	}
	for _, meta := range d.children {
		if meta.local != "meta" || meta.property != "title-type" {
			continue
		}
		if strings.TrimPrefix(meta.refines, "#") == title.id &&
			strings.EqualFold(strings.TrimSpace(meta.text), "subtitle") {
			return true
		}
	}
	return false
}

// isSeriesCollection reports whether a belongs-to-collection meta is explicitly
// typed as a series. Untyped collections are preserved.
func (d *opfDocument) isSeriesCollection(collection opfChild) bool {
	if collection.id == "" {
		return false
	}
	for _, meta := range d.children {
		if meta.local != "meta" || meta.property != "collection-type" {
			continue
		}
		if strings.TrimPrefix(meta.refines, "#") == collection.id {
			return strings.EqualFold(strings.TrimSpace(meta.text), "series")
		}
	}
	return false
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

// opfEdit collects the changes to a package document: children to drop, child
// text to replace, and elements to append to the metadata block.
type opfEdit struct {
	doc         *opfDocument
	remove      map[int]bool
	replace     map[int][]byte
	appendElems []string
	removedIDs  map[string]bool
	needDC      bool
	needOPF     bool
	needPrefix  bool
}

func newOPFEdit(doc *opfDocument) *opfEdit {
	return &opfEdit{
		doc:        doc,
		remove:     make(map[int]bool),
		replace:    make(map[int][]byte),
		removedIDs: make(map[string]bool),
	}
}

// drop removes a metadata child and remembers the ids it defined, so metas that
// refine it can be dropped too instead of dangling.
func (e *opfEdit) drop(i int) {
	e.remove[i] = true
	if id := e.doc.children[i].id; id != "" {
		e.removedIDs[id] = true
	}
}

// add appends a generated element to the metadata block.
func (e *opfEdit) add(raw string, dc bool) {
	e.appendElems = append(e.appendElems, raw)
	if dc {
		e.needDC = true
	} else {
		e.needOPF = true
	}
}

// setMetaName replaces a name/content meta entry, removing it when empty.
func (e *opfEdit) setMetaName(name, value string) {
	for i, c := range e.doc.children {
		if c.local == "meta" && c.name == name {
			e.drop(i)
		}
	}
	if value != "" {
		e.add(e.doc.metaName(name, value), false)
	}
}

// editOPF applies the difference between the original and edited metadata to
// the package document. Only changed fields are touched: untouched elements
// keep their original bytes, including attributes, refinements and comments.
func editOPF(doc *opfDocument, old, new Metadata) ([]byte, error) {
	if !doc.hasMetadata {
		return nil, fmt.Errorf("package document has no metadata element")
	}
	e := newOPFEdit(doc)
	epub2 := strings.HasPrefix(doc.version, "2")
	children := doc.children

	indices := func(local string) []int {
		var out []int
		for i, c := range children {
			if c.local == local {
				out = append(out, i)
			}
		}
		return out
	}

	subtitleDirty := new.Subtitle != old.Subtitle
	if new.Title != old.Title {
		for _, i := range indices("title") {
			e.drop(i)
		}
		subtitleDirty = true
		if new.Title != "" {
			e.add(doc.dcElement("title", "", new.Title), true)
		}
	}
	if subtitleDirty {
		for i, c := range children {
			switch {
			case c.local == "title" && doc.titleIsSubtitle(c):
				e.drop(i)
			case c.local == "subtitle":
				e.drop(i)
			case c.local == "meta" && (c.name == "subtitle" || c.property == "subtitle"):
				e.drop(i)
			}
		}
		if new.Subtitle != "" {
			if epub2 {
				e.add(doc.metaName("subtitle", new.Subtitle), false)
			} else {
				e.add(doc.dcElement("title", subtitleID, new.Subtitle), true)
				e.add(doc.metaProperty("", "#"+subtitleID, "title-type", "subtitle"), false)
			}
		}
	}

	if names := new.CreatorNames(); !slices.Equal(names, old.CreatorNames()) {
		for _, i := range indices("creator") {
			e.drop(i)
		}
		for _, name := range names {
			e.add(doc.dcElement("creator", "", name), true)
		}
	}
	if !slices.Equal(new.Subjects, old.Subjects) {
		for _, i := range indices("subject") {
			e.drop(i)
		}
		for _, subject := range new.Subjects {
			e.add(doc.dcElement("subject", "", subject), true)
		}
	}

	setOptional := func(local, oldValue, newValue string) {
		if oldValue == newValue {
			return
		}
		for _, i := range indices(local) {
			e.drop(i)
		}
		if newValue != "" {
			e.add(doc.dcElement(local, "", newValue), true)
		}
	}
	setOptional("description", old.Description, new.Description)
	setOptional("publisher", old.Publisher, new.Publisher)
	setOptional("rights", old.Rights, new.Rights)
	setOptional("date", old.Date, new.Date)

	if new.Language != old.Language {
		for _, i := range indices("language") {
			e.drop(i)
		}
		if new.Language != "" {
			e.add(doc.dcElement("language", "", new.Language), true)
		}
	}
	if new.Identifier != old.Identifier {
		for _, i := range indices("identifier") {
			e.drop(i)
		}
		if new.Identifier != "" {
			e.add(doc.dcElement("identifier", doc.uniqueIdentifier, new.Identifier), true)
		}
	}

	if new.Series != old.Series || new.SeriesIndex != old.SeriesIndex {
		e.setMetaName("calibre:series", new.Series)
		e.setMetaName("calibre:series_index", new.SeriesIndex)
		for i, c := range children {
			if c.local == "meta" && c.property == "belongs-to-collection" && doc.isSeriesCollection(c) {
				e.drop(i)
			}
		}
		if !epub2 && new.Series != "" {
			e.add(doc.metaProperty(seriesCollectionID, "", "belongs-to-collection", new.Series), false)
			e.add(doc.metaProperty("", "#"+seriesCollectionID, "collection-type", "series"), false)
			if new.SeriesIndex != "" {
				e.add(doc.metaProperty("", "#"+seriesCollectionID, "group-position", new.SeriesIndex), false)
			}
		}
	}

	if new.Read != old.Read {
		for i, c := range children {
			if c.local == "meta" && (c.name == readMetaName || c.property == readMetaName) {
				e.drop(i)
			}
		}
		if new.Read {
			if epub2 {
				e.add(doc.metaName(readMetaName, "true"), false)
			} else {
				e.add(doc.metaProperty("", "", readMetaName, "true"), false)
				e.needPrefix = true
			}
		}
	}

	// Refinements of a removed element have no target left.
	for i, c := range children {
		if e.remove[i] || c.local != "meta" || c.refines == "" {
			continue
		}
		if e.removedIDs[strings.TrimPrefix(c.refines, "#")] {
			e.remove[i] = true
		}
	}

	// EPUB 3 requires dcterms:modified to change whenever the package does.
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	foundModified := false
	for i, c := range children {
		if c.local != "meta" || c.property != "dcterms:modified" {
			continue
		}
		if epub2 {
			e.drop(i)
			continue
		}
		if !foundModified && c.refines == "" {
			e.replace[i] = setElementText(doc.raw(i), now)
			foundModified = true
		}
	}
	if !epub2 && !foundModified {
		e.add(doc.metaProperty("", "", "dcterms:modified", now), false)
	}

	return e.render(), nil
}

// render reassembles the package document from its unchanged bytes plus the
// planned edits.
func (e *opfEdit) render() []byte {
	doc := e.doc

	var items [][]byte
	for i, c := range doc.children {
		switch {
		case e.remove[i]:
			continue
		case e.replace[i] != nil:
			items = append(items, e.replace[i])
		default:
			items = append(items, doc.data[c.start:c.end])
		}
	}
	for _, raw := range e.appendElems {
		items = append(items, []byte(raw))
	}

	indent := doc.indent()
	unit := append(append([]byte{}, indent...), ' ', ' ')

	var content bytes.Buffer
	if len(items) > 0 {
		content.WriteByte('\n')
		for i, item := range items {
			if i > 0 {
				content.WriteByte('\n')
			}
			content.Write(unit)
			content.Write(item)
		}
		content.WriteByte('\n')
		content.Write(indent)
	}

	var out []byte
	if doc.metaSelfClosing {
		if len(items) == 0 {
			out = append([]byte{}, doc.data...)
		} else {
			out = append(out, doc.data[:doc.metaStart]...)
			out = append(out, openMetadataTag(doc.data[doc.metaStart:doc.metaOpenEnd])...)
			out = append(out, content.Bytes()...)
			out = append(out, "</metadata>"...)
			out = append(out, doc.data[doc.metaOpenEnd:]...)
		}
	} else {
		out = append(out, doc.data[:doc.metaOpenEnd]...)
		out = append(out, content.Bytes()...)
		out = append(out, doc.data[doc.metaCloseStart:]...)
	}

	needDC := e.needDC && !doc.dcDeclared
	needOPF := e.needOPF && doc.metaElementName() == "opf:meta" && !doc.opfDeclared
	if !e.needPrefix && !needDC && !needOPF {
		return out
	}

	tag := out[:doc.packageTagEnd]
	if e.needPrefix {
		tag = setTagAttr(tag, "prefix", mergePrefix(doc.prefix, readPrefix, readNamespace))
	}
	if needDC {
		tag = setTagAttr(tag, "xmlns:dc", dublinCoreNS)
	}
	if needOPF {
		tag = setTagAttr(tag, "xmlns:opf", opfNamespace)
	}
	result := make([]byte, 0, len(tag)+len(out)-doc.packageTagEnd)
	result = append(result, tag...)
	result = append(result, out[doc.packageTagEnd:]...)
	return result
}

// indent returns the whitespace that precedes the metadata element, which is
// also the indentation of its closing tag.
func (d *opfDocument) indent() []byte {
	start := bytes.LastIndexByte(d.data[:d.metaStart], '\n') + 1
	prefix := d.data[start:d.metaStart]
	for _, b := range prefix {
		if b != ' ' && b != '\t' {
			return nil
		}
	}
	return prefix
}

// dcElement renders a Dublin Core element. The dc: prefix is always used; the
// declaration is added to the package element when the document lacks one.
func (d *opfDocument) dcElement(local, id, value string) string {
	var b strings.Builder
	b.WriteString("<dc:")
	b.WriteString(local)
	if id != "" {
		b.WriteString(` id="`)
		b.WriteString(escapeAttr(id))
		b.WriteString(`"`)
	}
	b.WriteString(">")
	b.WriteString(escapeXML(value))
	b.WriteString("</dc:")
	b.WriteString(local)
	b.WriteString(">")
	return b.String()
}

// metaElementName returns the qualified name for a generated meta element. It
// follows the document's own namespace declarations instead of assuming the
// OPF namespace is the default.
func (d *opfDocument) metaElementName() string {
	if d.defaultNS == opfNamespace {
		return "meta"
	}
	return "opf:meta"
}

// metaName renders a legacy name/content meta element.
func (d *opfDocument) metaName(name, content string) string {
	return "<" + d.metaElementName() + ` name="` + escapeAttr(name) +
		`" content="` + escapeAttr(content) + `"/>`
}

// metaProperty renders a property meta element with optional id and refines.
func (d *opfDocument) metaProperty(id, refines, property, value string) string {
	name := d.metaElementName()
	var b strings.Builder
	b.WriteString("<")
	b.WriteString(name)
	if id != "" {
		b.WriteString(` id="`)
		b.WriteString(escapeAttr(id))
		b.WriteString(`"`)
	}
	if refines != "" {
		b.WriteString(` refines="`)
		b.WriteString(escapeAttr(refines))
		b.WriteString(`"`)
	}
	b.WriteString(` property="`)
	b.WriteString(escapeAttr(property))
	b.WriteString(`">`)
	b.WriteString(escapeXML(value))
	b.WriteString("</")
	b.WriteString(name)
	b.WriteString(">")
	return b.String()
}

// escapeAttr escapes a value for use inside a double-quoted attribute.
func escapeAttr(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// openMetadataTag turns a self-closing metadata tag into an opening tag so
// children can be inserted.
func openMetadataTag(raw []byte) []byte {
	trimmed := bytes.TrimRight(raw, " \t\r\n")
	if bytes.HasSuffix(trimmed, []byte("/>")) {
		out := append([]byte{}, trimmed[:len(trimmed)-2]...)
		return append(out, '>')
	}
	return append([]byte{}, raw...)
}

// setElementText replaces the character data of an element, keeping its start
// tag attributes.
func setElementText(raw []byte, text string) []byte {
	s := string(raw)
	openEnd := findTagEnd(s, 0)
	if openEnd < 0 {
		return raw
	}

	j := openEnd - 2
	for j > 0 && isXMLSpace(s[j]) {
		j--
	}
	if s[j] == '/' {
		nameEnd := 1
		for nameEnd < len(s) && !isXMLSpace(s[nameEnd]) && s[nameEnd] != '>' && s[nameEnd] != '/' {
			nameEnd++
		}
		return []byte(s[:j] + ">" + escapeXML(text) + "</" + s[1:nameEnd] + ">")
	}

	closeStart := strings.LastIndex(s, "</")
	if closeStart < openEnd {
		return raw
	}
	out := make([]byte, 0, len(s)+len(text))
	out = append(out, s[:openEnd]...)
	out = append(out, escapeXML(text)...)
	out = append(out, s[closeStart:]...)
	return out
}

// mergePrefix adds a prefix/IRI token pair to the package's prefix attribute.
func mergePrefix(value, prefix, namespace string) string {
	token := prefix + ":"
	fields := strings.Fields(value)
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i] == token {
			return value
		}
	}
	if strings.TrimSpace(value) == "" {
		return token + " " + namespace
	}
	return value + " " + token + " " + namespace
}

// findTagAttr locates the quoted value of an unqualified attribute inside a raw
// start tag.
func findTagAttr(tag []byte, name string) (start, end int, ok bool) {
	i := 1 // skip '<'
	for i < len(tag) && !isXMLSpace(tag[i]) && tag[i] != '>' && tag[i] != '/' {
		i++
	}
	for i < len(tag) {
		for i < len(tag) && isXMLSpace(tag[i]) {
			i++
		}
		if i >= len(tag) {
			break
		}
		if tag[i] == '>' || tag[i] == '/' {
			i++
			continue
		}
		nameStart := i
		for i < len(tag) && !isXMLSpace(tag[i]) && tag[i] != '=' && tag[i] != '>' && tag[i] != '/' {
			i++
		}
		nameEnd := i
		for i < len(tag) && isXMLSpace(tag[i]) {
			i++
		}
		if i >= len(tag) || tag[i] != '=' {
			continue
		}
		i++
		for i < len(tag) && isXMLSpace(tag[i]) {
			i++
		}
		if i >= len(tag) || (tag[i] != '"' && tag[i] != '\'') {
			continue
		}
		quote := tag[i]
		i++
		valueStart := i
		for i < len(tag) && tag[i] != quote {
			i++
		}
		valueEnd := i
		if string(tag[nameStart:nameEnd]) == name {
			return valueStart, valueEnd, true
		}
		i++
	}
	return 0, 0, false
}

// setTagAttr replaces an attribute value, inserting the attribute when absent.
func setTagAttr(tag []byte, name, value string) []byte {
	start, end, ok := findTagAttr(tag, name)
	if !ok {
		return insertTagAttr(tag, name+`="`+escapeAttr(value)+`"`)
	}
	out := make([]byte, 0, len(tag)+len(value))
	out = append(out, tag[:start]...)
	out = append(out, escapeAttr(value)...)
	out = append(out, tag[end:]...)
	return out
}

// insertTagAttr adds an attribute just before the tag's closing bracket.
func insertTagAttr(tag []byte, attr string) []byte {
	i := len(tag)
	for i > 0 && tag[i-1] != '>' {
		i--
	}
	if i > 0 {
		i--
	}
	j := i
	if j > 0 && tag[j-1] == '/' {
		j--
	}
	k := j
	for k > 0 && isXMLSpace(tag[k-1]) {
		k--
	}
	out := make([]byte, 0, len(tag)+len(attr)+2)
	out = append(out, tag[:k]...)
	out = append(out, ' ')
	out = append(out, attr...)
	out = append(out, tag[j:]...)
	return out
}

// isXMLSpace reports whether b is XML whitespace.
func isXMLSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
