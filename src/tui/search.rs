//! Search queries: unprefixed terms, field prefixes, negation, OR and
//! comparison operators.

use crate::epub::Field;
use crate::library::Entry;

/// One whitespace-separated token. `quoted` records whether any part of it was
/// quoted, so a literal "or" is not treated as an operator.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SearchTerm {
    pub text: String,
    pub quoted: bool,
}

/// One term of a query. An empty field searches every field.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SearchClause {
    pub field: String,
    pub value: String,
    pub op: String,
    pub negate: bool,
}

/// Whether an entry matches a lowercased query. Terms within a group are ANDed;
/// groups separated by OR are ORed.
pub fn entry_matches(entry: &Entry, query: &str) -> bool {
    let groups = parse_search_query(query);
    if groups.is_empty() {
        return true;
    }
    groups
        .iter()
        .any(|group| group.iter().all(|clause| matches_clause(entry, clause)))
}

fn matches_clause(entry: &Entry, clause: &SearchClause) -> bool {
    let mut matched = if clause.field.is_empty() {
        matches_any(entry, &clause.value)
    } else {
        field_matches(entry, &clause.field, &clause.value, &clause.op)
            .unwrap_or_else(|| matches_any(entry, &clause.value))
    };
    if clause.negate {
        matched = !matched;
    }
    matched
}

/// Split a query into OR-separated groups of ANDed clauses.
pub fn parse_search_query(query: &str) -> Vec<Vec<SearchClause>> {
    let terms = split_search_terms(query.trim().to_lowercase().as_str());
    let mut groups = Vec::new();
    let mut group: Vec<SearchClause> = Vec::new();
    for term in terms {
        if !term.quoted && term.text == "or" {
            if !group.is_empty() {
                groups.push(std::mem::take(&mut group));
            }
            continue;
        }
        group.push(parse_search_clause(&term.text));
    }
    if !group.is_empty() {
        groups.push(group);
    }
    groups
}

fn parse_search_clause(term: &str) -> SearchClause {
    let mut clause = SearchClause {
        field: String::new(),
        value: String::new(),
        op: String::new(),
        negate: false,
    };
    let mut term = term;
    if let Some(stripped) = term.strip_prefix('-')
        && !stripped.is_empty()
    {
        clause.negate = true;
        term = stripped;
    }

    if let Some((prefix, value)) = term.split_once(':') {
        let prefix = prefix.trim();
        let value = value.trim();
        if known_search_field(prefix) {
            clause.field = prefix.to_owned();
            let (op, rest) = split_comparison(value);
            clause.op = op.to_owned();
            clause.value = rest.to_owned();
            return clause;
        }
    }
    clause.value = term.to_owned();
    clause
}

/// Peel a comparison operator from a field value.
fn split_comparison(value: &str) -> (&str, &str) {
    for candidate in [">=", "<=", "!=", "=", ">", "<"] {
        if let Some(rest) = value.strip_prefix(candidate) {
            return (candidate, rest.trim());
        }
    }
    ("", value)
}

/// Split on whitespace outside double quotes and remove the quotes, so quoted
/// phrases stay one term. A backslash escapes a quote or another backslash.
pub fn split_search_terms(query: &str) -> Vec<SearchTerm> {
    let mut terms = Vec::new();
    let mut current = String::new();
    let mut in_quote = false;
    let mut quoted = false;
    let chars: Vec<char> = query.chars().collect();
    let mut i = 0;
    while i < chars.len() {
        let c = chars[i];
        match c {
            '\\' if i + 1 < chars.len()
                && (chars[i + 1] == '"' || chars[i + 1] == '\\') =>
            {
                i += 1;
                current.push(chars[i]);
            }
            '"' => {
                in_quote = !in_quote;
                quoted = true;
            }
            c if c.is_whitespace() && !in_quote => {
                if !current.is_empty() {
                    terms.push(SearchTerm {
                        text: std::mem::take(&mut current),
                        quoted,
                    });
                    quoted = false;
                }
            }
            c => current.push(c),
        }
        i += 1;
    }
    if !current.is_empty() {
        terms.push(SearchTerm {
            text: current,
            quoted,
        });
    }
    terms
}

/// Whether `name` is a field prefix the search supports.
pub fn known_search_field(name: &str) -> bool {
    matches!(name, "file" | "filename" | "path") || Field::parse(name).is_some()
}

/// Restrict a query to one field. The metadata field names accept the same
/// aliases as the CLI, plus file/file name and path. An empty query matches
/// every book where the field has a non-empty value.
fn field_matches(
    entry: &Entry,
    prefix: &str,
    query: &str,
    op: &str,
) -> Option<bool> {
    match prefix {
        "file" | "filename" => {
            let name = entry
                .path
                .file_name()
                .map(|n| n.to_string_lossy().into_owned())
                .unwrap_or_default();
            return Some(match_field(&name, query, op));
        }
        "path" => {
            return Some(match_field(&entry.path.to_string_lossy(), query, op));
        }
        _ => {}
    }

    let field = Field::parse(prefix)?;
    if field == Field::Read {
        if !op.is_empty() {
            return Some(compare_values(&entry.metadata.get(field), query, op));
        }
        return Some(match_read_status(entry.metadata.read, query));
    }
    let mut value = entry.metadata.get(field);
    if field == Field::Title {
        // The list falls back to the file name when a title is missing.
        value = entry.title();
    }
    Some(match_field(&value, query, op))
}

/// `read:` queries. An empty query lists read books, mirroring the way
/// `field:` lists books where the field is set.
fn match_read_status(read: bool, query: &str) -> bool {
    match query.trim().to_lowercase().as_str() {
        "" | "yes" | "true" | "1" | "y" | "read" | "finished" | "done" => read,
        "no" | "false" | "0" | "n" | "unread" | "not-read" | "not read" => {
            !read
        }
        _ => false,
    }
}

/// With an empty query, report whether the field is non-empty. With a
/// comparison operator, compare numerically when possible and as text
/// otherwise.
fn match_field(value: &str, query: &str, op: &str) -> bool {
    if !op.is_empty() {
        return compare_values(value, query, op);
    }
    if query.is_empty() {
        return !value.trim().is_empty();
    }
    value.to_lowercase().contains(query)
}

fn compare_values(value: &str, target: &str, op: &str) -> bool {
    let a = value.trim();
    let b = target.trim();
    if let (Ok(af), Ok(bf)) = (a.parse::<f64>(), b.parse::<f64>()) {
        let ordering = af.partial_cmp(&bf);
        return match ordering {
            Some(std::cmp::Ordering::Less) => matches!(op, "<" | "<=" | "!="),
            Some(std::cmp::Ordering::Greater) => {
                matches!(op, ">" | ">=" | "!=")
            }
            Some(std::cmp::Ordering::Equal) => matches!(op, "=" | "<=" | ">="),
            None => false,
        };
    }

    let ord = a.to_lowercase().cmp(&b.to_lowercase());
    match op {
        "=" => ord.is_eq(),
        "!=" => ord.is_ne(),
        "<" => ord.is_lt(),
        "<=" => ord.is_le(),
        ">" => ord.is_gt(),
        ">=" => ord.is_ge(),
        _ => false,
    }
}

/// Unprefixed search across the fields shown in the list.
fn matches_any(entry: &Entry, query: &str) -> bool {
    let meta = &entry.metadata;
    let file_name = entry
        .path
        .file_name()
        .map(|n| n.to_string_lossy().into_owned())
        .unwrap_or_default();
    let subjects = meta.subjects.join(" ");
    [
        entry.title(),
        meta.author_string(),
        meta.series.clone(),
        subjects,
        meta.description.clone(),
        file_name,
    ]
    .iter()
    .any(|value| value.to_lowercase().contains(query))
}

#[cfg(test)]
mod tests {
    use std::path::PathBuf;

    use super::*;
    use crate::epub::{Creator, Metadata};

    fn entry(
        title: &str,
        author: &str,
        series: &str,
        index: &str,
        tags: &[&str],
    ) -> Entry {
        Entry {
            path: PathBuf::from(format!(
                "/books/{}.epub",
                title.replace(' ', "-")
            )),
            metadata: Metadata {
                title: title.into(),
                creators: vec![Creator::new(author)],
                series: series.into(),
                series_index: index.into(),
                subjects: tags.iter().map(|t| (*t).to_owned()).collect(),
                description: "A novel about Winter".into(),
                language: "en".into(),
                date: "1980".into(),
                ..Metadata::default()
            },
            error: None,
        }
    }

    #[test]
    fn splits_quoted_terms() {
        let terms = split_search_terms(r#"author:"le guin" test"#);
        assert_eq!(terms.len(), 2);
        assert_eq!(terms[0].text, "author:le guin");
        assert!(terms[0].quoted);
        assert_eq!(terms[1].text, "test");
    }

    #[test]
    fn and_or_and_negation() {
        let e = entry("Dune", "Frank Herbert", "Dune", "1", &["scifi"]);
        assert!(entry_matches(&e, "dune"));
        assert!(entry_matches(&e, "author:herbert read:no"));
        assert!(!entry_matches(&e, "author:herbert read:yes"));
        assert!(!entry_matches(&e, "-author:herbert"));
        assert!(entry_matches(&e, "tag:fantasy OR tag:scifi"));
        assert!(!entry_matches(&e, "tag:fantasy tag:scifi"));
    }

    #[test]
    fn field_prefixes_and_empty_values() {
        let e = entry("Dune", "Frank Herbert", "Dune", "1", &["scifi"]);
        assert!(entry_matches(&e, "tags:"));
        assert!(!entry_matches(&e, "rights:"));
        assert!(entry_matches(&e, "read:no"));
        assert!(!entry_matches(&e, "read:yes"));
        assert!(entry_matches(&e, "path:books"));
        assert!(entry_matches(&e, "file:dune"));
        assert!(
            !entry_matches(&e, "unknown:dune"),
            "an unknown prefix is searched as literal free text"
        );
    }

    #[test]
    fn comparisons() {
        let e = entry("Dune", "Frank Herbert", "Dune", "2", &[]);
        assert!(entry_matches(&e, "date:>=1980"));
        assert!(entry_matches(&e, "series-index:<=3"));
        assert!(!entry_matches(&e, "series-index:<2"));
        assert!(entry_matches(&e, "language:=en"));
        assert!(entry_matches(&e, "language:!=de"));
    }

    #[test]
    fn quoted_phrases() {
        let e = entry("The Left Hand", "Le Guin, Ursula K.", "", "", &[]);
        assert!(entry_matches(&e, r#"author:"le guin""#));
        assert!(!entry_matches(&e, r#"author:"guin le""#));
    }
}
