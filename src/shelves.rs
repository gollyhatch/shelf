//! Named shelves: saved search queries over the library.

use crate::config::Config;
use crate::epub::Field;
use crate::library::Entry;
use crate::search::{entry_matches, split_comparison, split_search_terms};

/// One named shelf: a display name and the query it filters by.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Shelf {
    pub name: String,
    pub query: String,
}

/// Configured shelves, in name order.
pub fn catalog(config: &Config) -> Vec<Shelf> {
    config
        .shelves
        .iter()
        .map(|(name, query)| Shelf {
            name: name.clone(),
            query: query.clone(),
        })
        .collect()
}

/// Number of loaded books a shelf query matches.
pub fn count(entries: &[Entry], query: &str) -> usize {
    entries
        .iter()
        .filter(|entry| entry_matches(entry, query))
        .count()
}

/// The tag this shelf writes, when its query is exactly one tag clause.
/// `tag:fantasy` → `fantasy`; `tag:>1`, `-tag:x`, `read:no` and multi-term
/// queries return `None`.
pub fn membership_tag(query: &str) -> Option<String> {
    let terms = split_search_terms(query.trim());
    let [term] = terms.as_slice() else {
        return None;
    };
    let (prefix, value) = term.text.split_once(':')?;
    if Field::parse(prefix.trim()) != Some(Field::Tags) {
        return None;
    }
    let (op, rest) = split_comparison(value.trim());
    if !op.is_empty() {
        return None;
    }
    let tag = rest.trim();
    if tag.is_empty() || tag.contains(',') {
        None
    } else {
        Some(tag.to_owned())
    }
}

#[cfg(test)]
mod tests {
    use std::path::PathBuf;

    use super::*;
    use crate::epub::{Creator, Metadata};

    fn entry(title: &str, tags: &[&str], read: bool) -> Entry {
        Entry {
            path: PathBuf::from(format!("/books/{title}.epub")),
            metadata: Metadata {
                title: title.into(),
                creators: vec![Creator::new("Frank Herbert")],
                subjects: tags.iter().map(|tag| (*tag).to_owned()).collect(),
                read,
                ..Metadata::default()
            },
            error: None,
        }
    }

    #[test]
    fn membership_tag_accepts_one_plain_tag_clause() {
        assert_eq!(membership_tag("tag:fantasy").as_deref(), Some("fantasy"));
        assert_eq!(
            membership_tag(r#"tags:"le guin""#).as_deref(),
            Some("le guin")
        );
        assert_eq!(
            membership_tag("subject:fantasy").as_deref(),
            Some("fantasy")
        );
        assert_eq!(membership_tag("genre:fantasy").as_deref(), Some("fantasy"));
        assert_eq!(
            membership_tag("tag:Fantasy").as_deref(),
            Some("Fantasy"),
            "case is kept so the tag is written as configured"
        );
    }

    #[test]
    fn membership_tag_rejects_everything_else() {
        for query in [
            "-tag:x",
            "tag:>1",
            "read:no",
            "tag:a,b",
            "tag:a author:b",
            "tag:fantasy OR tag:scifi",
            "",
            "tag:",
        ] {
            assert_eq!(membership_tag(query), None, "{query:?}");
        }
    }

    #[test]
    fn count_matches_like_the_search() {
        let entries = vec![
            entry("Dune", &["scifi"], false),
            entry("Messiah", &["scifi", "fantasy"], true),
        ];
        assert_eq!(count(&entries, "read:no"), 1);
        assert_eq!(count(&entries, "tag:scifi"), 2);
        assert_eq!(count(&entries, "tag:fantasy"), 1);
        assert_eq!(count(&entries, ""), 2);
        assert_eq!(count(&entries, "author:herbert tag:nope"), 0);
    }
}
