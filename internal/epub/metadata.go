// Package epub reads and edits the Dublin Core metadata of an EPUB file.
//
// Reading extracts only the OCF container and the package document from the
// ZIP, so it does not pay for the book's images or chapters. Saving patches the
// metadata block of the package document in place and copies every other
// archive entry in compressed form: fields that were not edited keep their
// original bytes, including author role/file-as attributes, refinements,
// namespace prefixes, comments and ordering.
package epub

import "strings"

// Mimetype is the media type every EPUB container must declare.
const Mimetype = "application/epub+zip"

// Creator is a Dublin Core creator (author, editor, illustrator, ...). Only the
// display name is modelled. Elements of creators that are not edited keep their
// opf:role / opf:file-as attributes; replacing a creator writes a bare name and
// drops that element's refinements.
type Creator struct {
	Name string
}

// Metadata is the editable subset of an EPUB package's metadata.
type Metadata struct {
	Title       string
	Subtitle    string
	Creators    []Creator
	Subjects    []string
	Description string
	Publisher   string
	Date        string
	Language    string
	Rights      string
	Identifier  string
	// Series is the collection name a book belongs to (Calibre's
	// calibre:series meta).
	Series string
	// SeriesIndex is the book's position in its series, a.k.a. the episode
	// number (Calibre's calibre:series_index meta).
	SeriesIndex string
	// Read marks a book as read. It is stored as shelf's own namespaced meta
	// entry, not as an official Dublin Core field.
	Read bool
}

// ParseReadStatus reports whether value marks a book as read. Unrecognised or
// empty values mean unread.
func ParseReadStatus(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "1", "read", "finished":
		return true
	}
	return false
}

// CreatorNames returns the plain names of all creators.
func (m Metadata) CreatorNames() []string {
	out := make([]string, 0, len(m.Creators))
	for _, c := range m.Creators {
		if n := strings.TrimSpace(c.Name); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// AuthorString renders the creators as a single human friendly string.
func (m Metadata) AuthorString() string {
	return strings.Join(m.CreatorNames(), ", ")
}

// Field enumerates the metadata fields that can be edited by name.
type Field string

const (
	FieldTitle       Field = "title"
	FieldSubtitle    Field = "subtitle"
	FieldAuthor      Field = "author"
	FieldTags        Field = "tags"
	FieldSeries      Field = "series"
	FieldSeriesIndex Field = "series-index"
	FieldDescription Field = "description"
	FieldPublisher   Field = "publisher"
	FieldDate        Field = "date"
	FieldLanguage    Field = "language"
	FieldRights      Field = "rights"
	FieldIdentifier  Field = "identifier"
	FieldRead        Field = "read"
)

// AllFields lists every editable field in display order.
var AllFields = []Field{
	FieldTitle, FieldSubtitle, FieldAuthor, FieldTags,
	FieldSeries, FieldSeriesIndex,
	FieldDescription, FieldPublisher, FieldDate, FieldLanguage,
	FieldRights, FieldIdentifier, FieldRead,
}

// Label returns the human readable label used in the UI.
func (f Field) Label() string {
	switch f {
	case FieldTitle:
		return "Title"
	case FieldSubtitle:
		return "Subtitle"
	case FieldAuthor:
		return "Author"
	case FieldTags:
		return "Tags"
	case FieldSeries:
		return "Series"
	case FieldSeriesIndex:
		return "Episode"
	case FieldDescription:
		return "Description"
	case FieldPublisher:
		return "Publisher"
	case FieldDate:
		return "Date"
	case FieldLanguage:
		return "Language"
	case FieldRights:
		return "Rights"
	case FieldIdentifier:
		return "Identifier"
	case FieldRead:
		return "Read"
	}
	return string(f)
}

// ParseField resolves a field name (case insensitive) to a Field.
func ParseField(s string) (Field, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "title":
		return FieldTitle, true
	case "subtitle":
		return FieldSubtitle, true
	case "author", "creator", "authors":
		return FieldAuthor, true
	case "tag", "tags", "subject", "subjects", "genre":
		return FieldTags, true
	case "series":
		return FieldSeries, true
	case "series-index", "seriesindex", "series-number", "episode", "index":
		return FieldSeriesIndex, true
	case "description", "summary":
		return FieldDescription, true
	case "publisher":
		return FieldPublisher, true
	case "date", "published":
		return FieldDate, true
	case "language", "lang":
		return FieldLanguage, true
	case "rights", "copyright":
		return FieldRights, true
	case "identifier", "isbn", "id":
		return FieldIdentifier, true
	case "read", "read-status", "finished":
		return FieldRead, true
	}
	return "", false
}

// Get returns the current value of a field. Multi valued fields are joined with
// a separator suitable for round tripping through Set and the edit commands.
func (m Metadata) Get(f Field) string {
	switch f {
	case FieldTitle:
		return m.Title
	case FieldSubtitle:
		return m.Subtitle
	case FieldAuthor:
		return strings.Join(m.CreatorNames(), "; ")
	case FieldTags:
		return strings.Join(m.Subjects, ", ")
	case FieldSeries:
		return m.Series
	case FieldSeriesIndex:
		return m.SeriesIndex
	case FieldDescription:
		return m.Description
	case FieldPublisher:
		return m.Publisher
	case FieldDate:
		return m.Date
	case FieldLanguage:
		return m.Language
	case FieldRights:
		return m.Rights
	case FieldIdentifier:
		return m.Identifier
	case FieldRead:
		if m.Read {
			return "yes"
		}
		return "no"
	}
	return ""
}
