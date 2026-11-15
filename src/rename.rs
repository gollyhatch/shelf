//! File naming for `rename` and `M`: slugification, sanitization and the
//! `--format` placeholder language shared with the TUI.

use anyhow::{Result, bail};
use unicode_normalization::{UnicodeNormalization, char::is_combining_mark};

use crate::epub::{Field, Metadata};
use crate::library::Entry;

/// Build the file name for a book: `author - title.epub` (or `author-title`
/// when slugifying), falling back to the title alone.
pub fn filename(entry: &Entry, slugify_names: bool) -> String {
    let mut title = entry.metadata.title.trim().to_owned();
    if title.is_empty() {
        title = entry.title().trim().to_owned();
    }
    let mut author = short_author(&entry.metadata.author_string());
    let separator = if slugify_names {
        title = slugify(&title);
        author = slugify(&author);
        "-"
    } else {
        title = sanitize(&title);
        author = sanitize(&author);
        " - "
    };

    match (author.is_empty(), title.is_empty()) {
        (false, false) => format!("{author}{separator}{title}.epub"),
        (false, true) => format!("{author}.epub"),
        (true, false) => format!("{title}.epub"),
        (true, true) => entry
            .path
            .file_name()
            .map(|n| n.to_string_lossy().into_owned())
            .unwrap_or_default(),
    }
}

/// Trim an author list down to the first name so file names stay manageable.
fn short_author(s: &str) -> String {
    s.split(',').next().unwrap_or_default().trim().to_owned()
}

/// Explicit ASCII expansions for characters that have no decomposition.
fn expand_special(c: char) -> Option<&'static str> {
    Some(match c {
        'ä' => "ae",
        'ö' => "oe",
        'ü' => "ue",
        'ß' => "ss",
        'æ' => "ae",
        'œ' => "oe",
        'ø' => "o",
        'ł' => "l",
        'đ' => "d",
        'ð' => "d",
        'þ' => "th",
        'ı' => "i",
        'ħ' => "h",
        'ŧ' => "t",
        'ŋ' => "n",
        '&' => "-",
        '@' => " at ",
        '%' => " percent ",
        '$' => " dollar ",
        '€' => " euro ",
        '£' => " pound ",
        '©' => " copyright ",
        '®' => " registered ",
        '™' => " tm ",
        '°' => " deg ",
        '+' => " plus ",
        '×' => "x",
        '÷' => "div",
        '\'' => "",
        '\u{2019}' => "",
        _ => return None,
    })
}

/// Turn a metadata value into a lower-case ASCII slug.
pub fn slugify(s: &str) -> String {
    // Compose first so decomposed umlauts are handled by the explicit map.
    let lowered = s.trim().to_lowercase().nfc().collect::<String>();
    let mut expanded = String::with_capacity(lowered.len());
    for c in lowered.chars() {
        match expand_special(c) {
            Some(replacement) => expanded.push_str(replacement),
            None => expanded.push(c),
        }
    }

    let mut out = String::with_capacity(expanded.len());
    let mut last_dash = false;
    for c in expanded.nfd() {
        if is_combining_mark(c) {
            continue;
        }
        match c {
            'a'..='z' | '0'..='9' => {
                out.push(c);
                last_dash = false;
            }
            c if c.is_whitespace() || c == '-' || c == '_' => {
                if !out.is_empty() && !last_dash {
                    out.push('-');
                    last_dash = true;
                }
            }
            _ => {
                if !out.is_empty() && !last_dash {
                    out.push('-');
                    last_dash = true;
                }
            }
        }
    }
    out.trim_matches('-').to_owned()
}

/// Replace runs of whitespace with a single dash and collapse dashes next to
/// each other. Used for the literal parts of a slugified rename pattern.
pub fn dash_whitespace(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    let mut last_dash = false;
    for c in s.chars() {
        if c.is_whitespace() || c == '-' {
            if !out.is_empty() && !last_dash {
                out.push('-');
                last_dash = true;
            }
            continue;
        }
        out.push(c);
        last_dash = false;
    }
    out.trim_matches('-').to_owned()
}

/// Make a string safe for a file name.
pub fn sanitize(s: &str) -> String {
    let mut replaced = String::with_capacity(s.len());
    for c in s.trim().chars() {
        match c {
            '/' | '\\' => replaced.push('-'),
            ':' => replaced.push_str(" -"),
            '*' | '?' => {}
            '"' => replaced.push('\''),
            '<' => replaced.push('('),
            '>' => replaced.push(')'),
            '|' => replaced.push('-'),
            '\n' | '\r' | '\t' => replaced.push(' '),
            other => replaced.push(other),
        }
    }
    let collapsed = replaced.split_whitespace().collect::<Vec<_>>().join(" ");
    let trimmed = collapsed.trim_matches(|c| c == ' ' || c == '.' || c == '-');
    if trimmed.chars().count() <= 120 {
        return trimmed.to_owned();
    }
    trimmed
        .chars()
        .take(120)
        .collect::<String>()
        .trim_end()
        .to_owned()
}

/// Single-letter placeholders of `--format` and `search_url`.
pub fn format_verb(c: u8) -> Option<Field> {
    Some(match c {
        b't' => Field::Title,
        b'b' => Field::Subtitle,
        b'a' => Field::Author,
        b'g' => Field::Tags,
        b's' => Field::Series,
        b'e' => Field::SeriesIndex,
        b'p' => Field::Publisher,
        b'd' => Field::Date,
        b'l' => Field::Language,
        b'r' => Field::Rights,
        b'i' => Field::Identifier,
        _ => return None,
    })
}

const FORMAT_VERB_LIST: &str =
    "%t %b %a %g %s %e %p %d %l %r %i, or % for a literal percent";

fn is_format_verb_char(c: u8) -> bool {
    c.is_ascii_alphanumeric() || c == b'-'
}

/// Resolve one placeholder (the text after a `%`) to the field it expands to.
fn lookup_format_verb(word: &str) -> Option<Field> {
    if word.len() == 1 {
        return format_verb(word.as_bytes()[0]);
    }
    Field::parse(word)
}

/// Resolve the placeholder starting immediately after a percent sign. Returns
/// the field and the exclusive end of the placeholder. A trailing dash that is
/// not part of a known field name is left for the literal pattern, so
/// `%a-%t` works as an author-title separator.
fn format_verb_at(format: &str, start: usize) -> Option<(Field, usize)> {
    let bytes = format.as_bytes();
    let mut j = start;
    while j < bytes.len() && is_format_verb_char(bytes[j]) {
        j += 1;
    }
    if j == start {
        return None;
    }
    if let Some(field) = lookup_format_verb(&format[start..j]) {
        return Some((field, j));
    }
    if bytes[j - 1] == b'-'
        && let Some(field) = lookup_format_verb(&format[start..j - 1])
    {
        return Some((field, j - 1));
    }
    None
}

/// Report the first problem with a `--format` pattern so a typo is caught
/// before any file is renamed.
pub fn validate_format(format: &str) -> Result<()> {
    let bytes = format.as_bytes();
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] != b'%' {
            i += 1;
            continue;
        }
        i += 1;
        if i >= bytes.len() {
            bail!("format ends with a lone %");
        }
        if bytes[i] == b'%' {
            i += 1;
            continue;
        }
        match format_verb_at(format, i) {
            Some((_, end)) => i = end,
            None => {
                let mut j = i;
                while j < bytes.len() && is_format_verb_char(bytes[j]) {
                    j += 1;
                }
                if j == i {
                    bail!(
                        "% must be followed by a placeholder ({FORMAT_VERB_LIST})"
                    );
                }
                bail!(
                    "unknown placeholder %{} in format; valid: {FORMAT_VERB_LIST}",
                    &format[i..j]
                );
            }
        }
    }
    Ok(())
}

/// Replace the placeholders of a rename pattern with the book's metadata and
/// make the result safe to use as a file name. An empty expansion falls back to
/// the default name. With `slugify_names`, each metadata value is slugified
/// before it is inserted; literal pattern text is left as written.
pub fn expand_format(
    format: &str,
    entry: &Entry,
    slugify_names: bool,
) -> String {
    let bytes = format.as_bytes();
    let mut out = String::with_capacity(format.len());
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] != b'%' {
            match format[i..].find('%') {
                Some(next) => {
                    out.push_str(&format[i..i + next]);
                    i += next;
                }
                None => {
                    out.push_str(&format[i..]);
                    break;
                }
            }
            continue;
        }
        i += 1;
        if i >= bytes.len() {
            break;
        }
        if bytes[i] == b'%' {
            out.push('%');
            i += 1;
            continue;
        }
        if let Some((field, end)) = format_verb_at(format, i) {
            let mut value = entry.metadata.get(field);
            if field == Field::SeriesIndex {
                value = normalize_episode(&value);
            }
            if slugify_names {
                value = slugify(&value);
            }
            out.push_str(&value);
            i = end;
            continue;
        }
        let mut j = i;
        while j < bytes.len() && is_format_verb_char(bytes[j]) {
            j += 1;
        }
        out.push_str(&format[i..j]);
        i = j;
    }

    let mut name = sanitize(&out);
    if slugify_names {
        name = dash_whitespace(&name);
    }
    if name.is_empty() {
        return filename(entry, slugify_names);
    }
    if !name.to_ascii_lowercase().ends_with(".epub") {
        name.push_str(".epub");
    }
    name
}

/// Drop a trailing `.0` from an episode number (`2.0` becomes `2`).
fn normalize_episode(s: &str) -> String {
    let s = s.trim();
    match s.strip_suffix(".0") {
        Some(value) => value.to_owned(),
        None => s.to_owned(),
    }
}

/// A configured rename rule for the TUI.
#[derive(Clone, Debug)]
pub struct NameRule {
    pub format: String,
    pub slugify: bool,
}

impl NameRule {
    pub fn new(format: impl Into<String>, slugify: bool) -> Self {
        Self {
            format: format.into(),
            slugify,
        }
    }

    pub fn is_empty(&self) -> bool {
        self.format.trim().is_empty()
    }

    pub fn name(&self, entry: &Entry) -> Result<String> {
        if self.is_empty() {
            return Ok(filename(entry, self.slugify));
        }
        validate_format(&self.format)?;
        Ok(expand_format(&self.format, entry, self.slugify))
    }
}

/// Rule for `M`: only available when `rename_format` is configured.
pub fn move_rule(format: &str, slugify: bool) -> Option<NameRule> {
    if format.trim().is_empty() {
        return None;
    }
    Some(NameRule::new(format, slugify))
}

/// Expand metadata placeholders in a search URL. Values are query-escaped so
/// spaces and punctuation are safe in a URL. Unknown percent sequences are left
/// alone, so ordinary percent-encoded URLs work.
pub fn expand_search_url(pattern: &str, meta: &Metadata) -> String {
    let bytes = pattern.as_bytes();
    let mut out = String::with_capacity(pattern.len());
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] != b'%' {
            match pattern[i..].find('%') {
                Some(next) => {
                    out.push_str(&pattern[i..i + next]);
                    i += next;
                }
                None => {
                    out.push_str(&pattern[i..]);
                    break;
                }
            }
            continue;
        }
        if i + 1 >= bytes.len() {
            out.push('%');
            break;
        }
        if bytes[i + 1] == b'%' {
            out.push('%');
            i += 2;
            continue;
        }
        match format_verb_at(pattern, i + 1) {
            Some((field, end)) => {
                out.push_str(&query_escape(&meta.get(field)));
                i = end;
            }
            None => {
                out.push('%');
                i += 1;
            }
        }
    }
    out
}

/// `application/x-www-form-urlencoded` escaping, as used by Go's
/// `url.QueryEscape`: spaces become `+`.
pub fn query_escape(value: &str) -> String {
    let mut out = String::with_capacity(value.len());
    for byte in value.bytes() {
        match byte {
            b'A'..=b'Z'
            | b'a'..=b'z'
            | b'0'..=b'9'
            | b'-'
            | b'_'
            | b'.'
            | b'~' => {
                out.push(byte as char);
            }
            b' ' => out.push('+'),
            other => out.push_str(&format!("%{other:02X}")),
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use std::path::PathBuf;

    use super::*;
    use crate::epub::Creator;

    fn entry(title: &str, author: &str) -> Entry {
        Entry {
            path: PathBuf::from("book.epub"),
            metadata: Metadata {
                title: title.into(),
                creators: vec![Creator::new(author)],
                ..Metadata::default()
            },
            error: None,
        }
    }

    #[test]
    fn slugifies_values() {
        assert_eq!(slugify("Jürgen Müller"), "juergen-mueller");
        assert_eq!(slugify("Straße"), "strasse");
        assert_eq!(slugify("Café & Crème"), "cafe-creme");
        assert_eq!(slugify("  --Hello,  World!--  "), "hello-world");
    }

    #[test]
    fn validates_formats() {
        assert!(validate_format("%a - %t.epub").is_ok());
        assert!(validate_format("%series-index %title").is_ok());
        assert!(validate_format("100%% %t").is_ok());
        assert!(
            validate_format("%")
                .unwrap_err()
                .to_string()
                .contains("lone %")
        );
        assert!(
            validate_format("%z")
                .unwrap_err()
                .to_string()
                .contains("unknown placeholder %z")
        );
        assert!(
            validate_format("% title")
                .unwrap_err()
                .to_string()
                .contains("must be followed")
        );
    }

    #[test]
    fn expands_formats() {
        let mut e = entry("The Hobbit", "Tolkien, J. R. R.");
        e.metadata.series = "Middle-earth".into();
        e.metadata.series_index = "2.0".into();
        assert_eq!(
            expand_format("%a - %t", &e, false),
            "Tolkien, J. R. R. - The Hobbit.epub"
        );
        assert_eq!(
            expand_format("%s %e - %t", &e, false),
            "Middle-earth 2 - The Hobbit.epub"
        );
        assert_eq!(expand_format("%title", &e, false), "The Hobbit.epub");
    }

    #[test]
    fn expands_slugified_format() {
        let e = entry("Jürgen's Book", "Müller, Jürgen");
        assert_eq!(
            expand_format("%a-%t", &e, true),
            "mueller-juergen-juergens-book.epub"
        );
        assert_eq!(filename(&e, true), "mueller-juergens-book.epub");
    }

    #[test]
    fn sanitizes_names() {
        assert_eq!(sanitize("a/b:c*d?e\"f<g>h|i"), "a-b -cde'f(g)h-i");
        assert_eq!(
            expand_format("a/b %t", &entry("x", "y"), false),
            "a-b x.epub"
        );
    }

    #[test]
    fn default_name_uses_first_author() {
        let e = entry("Dune", "Frank Herbert, Brian Herbert");
        assert_eq!(filename(&e, false), "Frank Herbert - Dune.epub");
    }

    #[test]
    fn search_url_expansion() {
        let mut e = entry("Dune", "Frank Herbert");
        e.metadata.series = "Dune".into();
        assert_eq!(
            expand_search_url("https://x/?q=%a+%t&s=%s", &e.metadata),
            "https://x/?q=Frank+Herbert+Dune&s=Dune"
        );
        assert_eq!(expand_search_url("100%25 %zz", &e.metadata), "100%25 %zz");
    }
}
