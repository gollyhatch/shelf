//! The editable subset of an EPUB package's metadata.

/// A Dublin Core creator (author, editor, illustrator, ...). Only the display
/// name is modeled. Elements of creators that are not edited keep their
/// `opf:role` / `opf:file-as` attributes; replacing a creator writes a bare name
/// and drops that element's refinements.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Creator {
    pub name: String,
}

impl Creator {
    pub fn new(name: impl Into<String>) -> Self {
        Self { name: name.into() }
    }

    /// Render a creator the way the UI shows it.
    pub fn display(&self) -> &str {
        &self.name
    }
}

/// Metadata is the editable subset of an EPUB package's metadata.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Metadata {
    pub title: String,
    pub subtitle: String,
    pub creators: Vec<Creator>,
    pub subjects: Vec<String>,
    pub description: String,
    /// Dublin Core extent, e.g. "384 pages".
    pub extent: String,
    pub publisher: String,
    pub date: String,
    pub language: String,
    pub rights: String,
    pub identifier: String,
    /// The collection name a book belongs to (Calibre's `calibre:series`).
    pub series: String,
    /// The book's position in its series, a.k.a. the episode number.
    pub series_index: String,
    /// Marked as read, stored in shelf's own namespaced meta entry.
    pub read: bool,
}

/// Parse a read status value. Unrecognized or empty values mean unread.
pub fn parse_read_status(value: &str) -> bool {
    matches!(
        value.trim().to_ascii_lowercase().as_str(),
        "true" | "yes" | "1" | "read" | "finished"
    )
}

/// Parse an author into a [`Creator`], keeping only the display name. A
/// parenthesized suffix (the legacy "Name (role; file-as)" form) is dropped so
/// role/file-as input degrades gracefully to a bare name.
pub fn parse_creator(s: &str) -> Creator {
    let s = s.trim();
    if s.is_empty() {
        return Creator::default();
    }
    if s.ends_with(')')
        && let Some(i) = s.rfind(" (")
    {
        return Creator::new(s[..i].trim());
    }
    Creator::new(s)
}

/// Split a `;`-separated list of creators.
pub fn parse_creator_list(s: &str) -> Vec<Creator> {
    split_list(s, ';')
        .into_iter()
        .map(|part| parse_creator(&part))
        .filter(|c| !c.name.is_empty())
        .collect()
}

/// Split a delimiter-separated list, trimming parts and dropping empties.
pub fn split_list(s: &str, sep: char) -> Vec<String> {
    s.split(sep)
        .map(|p| p.trim())
        .filter(|p| !p.is_empty())
        .map(str::to_owned)
        .collect()
}

impl Metadata {
    /// Plain names of all creators, skipping blanks.
    pub fn creator_names(&self) -> Vec<String> {
        self.creators
            .iter()
            .map(|c| c.name.trim())
            .filter(|n| !n.is_empty())
            .map(str::to_owned)
            .collect()
    }

    /// Creators rendered as one human friendly string.
    pub fn author_string(&self) -> String {
        self.creator_names().join(", ")
    }

    /// Current value of a field. Multi-valued fields are joined with a
    /// separator suitable for round tripping through [`apply`](Self::apply).
    pub fn get(&self, field: Field) -> String {
        match field {
            Field::Title => self.title.clone(),
            Field::Subtitle => self.subtitle.clone(),
            Field::Author => self.creator_names().join("; "),
            Field::Tags => self.subjects.join(", "),
            Field::Series => self.series.clone(),
            Field::SeriesIndex => self.series_index.clone(),
            Field::Description => self.description.clone(),
            Field::Publisher => self.publisher.clone(),
            Field::Date => self.date.clone(),
            Field::Language => self.language.clone(),
            Field::Rights => self.rights.clone(),
            Field::Identifier => self.identifier.clone(),
            Field::Read => if self.read { "yes" } else { "no" }.to_owned(),
        }
    }

    /// Return a copy with one field updated the way `Package::set` updates a
    /// package, so callers can preview an edit without touching a file.
    pub fn apply(&self, field: Field, value: &str, append: bool) -> Metadata {
        let value = value.trim();
        let mut meta = self.clone();
        match field {
            Field::Title => {
                meta.title = append_text(&self.title, value, append)
            }
            Field::Subtitle => {
                meta.subtitle = append_text(&self.subtitle, value, append)
            }
            Field::Author => {
                let creators = parse_creator_list(value);
                if append {
                    meta.creators.extend(creators);
                } else {
                    meta.creators = creators;
                }
            }
            Field::Tags => {
                let subjects = split_list(value, ',');
                if append {
                    meta.subjects.extend(subjects);
                } else {
                    meta.subjects = subjects;
                }
            }
            Field::Series => {
                meta.series = append_text(&self.series, value, append)
            }
            Field::SeriesIndex => meta.series_index = value.to_owned(),
            Field::Description => {
                meta.description = append_text(&self.description, value, append)
            }
            Field::Publisher => {
                meta.publisher = append_text(&self.publisher, value, append)
            }
            Field::Date => meta.date = append_text(&self.date, value, append),
            Field::Language => {
                meta.language = append_text(&self.language, value, append)
            }
            Field::Rights => {
                meta.rights = append_text(&self.rights, value, append)
            }
            Field::Identifier => {
                meta.identifier = append_text(&self.identifier, value, append)
            }
            Field::Read => meta.read = parse_read_status(value),
        }
        meta
    }
}

/// `--append` for single valued fields joins with ", ".
fn append_text(current: &str, value: &str, append: bool) -> String {
    if append && !current.is_empty() && !value.is_empty() {
        format!("{current}, {value}")
    } else {
        value.to_owned()
    }
}

/// The metadata fields that can be edited by name.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash, PartialOrd, Ord)]
pub enum Field {
    Title,
    Subtitle,
    Author,
    Tags,
    Series,
    SeriesIndex,
    Description,
    Publisher,
    Date,
    Language,
    Rights,
    Identifier,
    Read,
}

impl Field {
    /// Every editable field in display order.
    pub const ALL: [Field; 13] = [
        Field::Title,
        Field::Subtitle,
        Field::Author,
        Field::Tags,
        Field::Series,
        Field::SeriesIndex,
        Field::Description,
        Field::Publisher,
        Field::Date,
        Field::Language,
        Field::Rights,
        Field::Identifier,
        Field::Read,
    ];

    /// Human readable label used in the UI.
    pub fn label(self) -> &'static str {
        match self {
            Field::Title => "Title",
            Field::Subtitle => "Subtitle",
            Field::Author => "Author",
            Field::Tags => "Tags",
            Field::Series => "Series",
            Field::SeriesIndex => "Episode",
            Field::Description => "Description",
            Field::Publisher => "Publisher",
            Field::Date => "Date",
            Field::Language => "Language",
            Field::Rights => "Rights",
            Field::Identifier => "Identifier",
            Field::Read => "Read",
        }
    }

    /// Stable field identifier, as accepted by the CLI.
    pub fn name(self) -> &'static str {
        match self {
            Field::Title => "title",
            Field::Subtitle => "subtitle",
            Field::Author => "author",
            Field::Tags => "tags",
            Field::Series => "series",
            Field::SeriesIndex => "series-index",
            Field::Description => "description",
            Field::Publisher => "publisher",
            Field::Date => "date",
            Field::Language => "language",
            Field::Rights => "rights",
            Field::Identifier => "identifier",
            Field::Read => "read",
        }
    }

    /// The command line flag that carries this field's value. The identifier
    /// is exposed as `--isbn` to match its most common meaning.
    pub fn flag_name(self) -> &'static str {
        match self {
            Field::Identifier => "isbn",
            other => other.name(),
        }
    }

    /// Resolve a field name (case insensitive) to a [`Field`].
    pub fn parse(s: &str) -> Option<Field> {
        match s.trim().to_ascii_lowercase().as_str() {
            "title" => Some(Field::Title),
            "subtitle" => Some(Field::Subtitle),
            "author" | "creator" | "authors" => Some(Field::Author),
            "tag" | "tags" | "subject" | "subjects" | "genre" => {
                Some(Field::Tags)
            }
            "series" => Some(Field::Series),
            "series-index" | "seriesindex" | "series-number" | "episode"
            | "index" => Some(Field::SeriesIndex),
            "description" | "summary" => Some(Field::Description),
            "publisher" => Some(Field::Publisher),
            "date" | "published" => Some(Field::Date),
            "language" | "lang" => Some(Field::Language),
            "rights" | "copyright" => Some(Field::Rights),
            "identifier" | "isbn" | "id" => Some(Field::Identifier),
            "read" | "read-status" | "finished" => Some(Field::Read),
            _ => None,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_field_aliases() {
        assert_eq!(Field::parse("CREATOR"), Some(Field::Author));
        assert_eq!(Field::parse(" genre "), Some(Field::Tags));
        assert_eq!(Field::parse("isbn"), Some(Field::Identifier));
        assert_eq!(Field::parse("episode"), Some(Field::SeriesIndex));
        assert_eq!(Field::parse("nope"), None);
    }

    #[test]
    fn creator_suffix_is_dropped() {
        assert_eq!(parse_creator("Jane Doe (aut; Doe, Jane)").name, "Jane Doe");
        assert_eq!(parse_creator("  Jane Doe ").name, "Jane Doe");
    }

    #[test]
    fn apply_appends_and_replaces() {
        let meta = Metadata {
            title: "Dune".into(),
            creators: vec![Creator::new("Frank Herbert")],
            ..Metadata::default()
        };
        let appended = meta.apply(Field::Title, "Messiah", true);
        assert_eq!(appended.title, "Dune, Messiah");
        let replaced = meta.apply(Field::Tags, "sf, classic", false);
        assert_eq!(replaced.subjects, ["sf", "classic"]);
        let authors = meta.apply(Field::Author, "A; B", false);
        assert_eq!(authors.creator_names(), ["A", "B"]);
        assert!(meta.apply(Field::Read, "yes", false).read);
        assert!(!meta.apply(Field::Read, "maybe", false).read);
    }
}
