//! Books pane ordering: configurable keys, directions and comparisons.

use std::cmp::Ordering;
use std::collections::HashMap;
use std::fmt;
use std::path::PathBuf;

use crate::epub::PageCount;
use crate::library::Entry;

/// The value the Books pane is ordered by.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum SortField {
    Path,
    Title,
    Author,
    Series,
    Date,
    Language,
    Pages,
}

impl SortField {
    pub fn label(self) -> &'static str {
        match self {
            SortField::Path => "File path",
            SortField::Title => "Title",
            SortField::Author => "Author",
            SortField::Series => "Series",
            SortField::Date => "Date",
            SortField::Language => "Language",
            SortField::Pages => "Pages",
        }
    }
}

/// One selectable field/direction combination.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct SortMode {
    pub field: SortField,
    pub desc: bool,
}

impl SortMode {
    pub fn label(self) -> String {
        match self.field {
            SortField::Date if self.desc => "Date newest first".into(),
            SortField::Date => "Date oldest first".into(),
            SortField::Series if self.desc => "Series, episode Z–A".into(),
            SortField::Series => "Series, episode A–Z".into(),
            SortField::Pages if self.desc => "Pages most first".into(),
            SortField::Pages => "Pages least first".into(),
            field if self.desc => format!("{} Z–A", field.label()),
            field => format!("{} A–Z", field.label()),
        }
    }
}

/// The ordered list shown by the sort selector. File path ascending is the
/// default and preserves the original discovery order.
pub const SORT_MODES: [SortMode; 14] = [
    SortMode {
        field: SortField::Path,
        desc: false,
    },
    SortMode {
        field: SortField::Path,
        desc: true,
    },
    SortMode {
        field: SortField::Title,
        desc: false,
    },
    SortMode {
        field: SortField::Title,
        desc: true,
    },
    SortMode {
        field: SortField::Author,
        desc: false,
    },
    SortMode {
        field: SortField::Author,
        desc: true,
    },
    SortMode {
        field: SortField::Series,
        desc: false,
    },
    SortMode {
        field: SortField::Series,
        desc: true,
    },
    SortMode {
        field: SortField::Date,
        desc: false,
    },
    SortMode {
        field: SortField::Date,
        desc: true,
    },
    SortMode {
        field: SortField::Language,
        desc: false,
    },
    SortMode {
        field: SortField::Language,
        desc: true,
    },
    SortMode {
        field: SortField::Pages,
        desc: false,
    },
    SortMode {
        field: SortField::Pages,
        desc: true,
    },
];

/// Parse the configured sort value into an ordered list of keys. Keys are
/// separated by commas; each key may carry a direction after a colon, a hyphen,
/// or whitespace, e.g. "author,series:desc" or "date newest first,title".
/// Without a direction a key ascends.
pub fn parse_sort_spec(value: &str) -> Result<Vec<SortMode>, String> {
    let value = value.trim().to_lowercase();
    if value.is_empty() {
        return Ok(vec![SortMode {
            field: SortField::Path,
            desc: false,
        }]);
    }
    value
        .split(',')
        .map(|part| parse_sort_key(part).map_err(|err| err.to_string()))
        .collect()
}

#[derive(Debug)]
pub struct SortError(pub String);

impl fmt::Display for SortError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for SortError {}

/// Parse one field and optional direction.
fn parse_sort_key(value: &str) -> Result<SortMode, SortError> {
    let value = value.trim().to_lowercase();
    if value.is_empty() {
        return Err(SortError("empty sort key".into()));
    }
    if let Some(field) = parse_sort_field(&value) {
        return Ok(SortMode { field, desc: false });
    }

    let (field_part, direction_part) =
        if let Some((before, after)) = value.split_once(':') {
            (before.to_owned(), after.to_owned())
        } else if value.split_whitespace().count() > 1 {
            let mut parts = value.split_whitespace();
            let field = parts.next().unwrap_or_default().to_owned();
            let direction = parts.collect::<Vec<_>>().join(" ");
            (field, direction)
        } else if let Some((before, after)) = value.split_once('-') {
            (before.to_owned(), after.to_owned())
        } else {
            (value.clone(), String::new())
        };

    let field = parse_sort_field(field_part.trim()).ok_or_else(|| {
        SortError(format!(
            "unknown sort field {field_part:?} (want path, title, author, series, date, language or pages)"
        ))
    })?;
    let desc =
        parse_sort_direction(direction_part.trim()).ok_or_else(|| {
            SortError(format!(
                "unknown sort direction {direction_part:?} (want asc or desc)"
            ))
        })?;
    Ok(SortMode { field, desc })
}

/// Resolve the configurable name of a sort field.
pub fn parse_sort_field(s: &str) -> Option<SortField> {
    Some(match s.trim() {
        "path" | "file" | "filename" | "file path" | "file-path" => {
            SortField::Path
        }
        "title" => SortField::Title,
        "author" | "creator" => SortField::Author,
        "series" => SortField::Series,
        "date" | "published" => SortField::Date,
        "language" | "lang" => SortField::Language,
        "pages" | "page" | "page count" | "page-count" => SortField::Pages,
        _ => return None,
    })
}

/// Resolve the optional sorting direction.
fn parse_sort_direction(s: &str) -> Option<bool> {
    Some(match s.trim() {
        "" | "asc" | "ascending" | "a-z" | "az" | "a to z" | "oldest"
        | "oldest first" | "oldest-first" | "least" | "least first"
        | "least-first" | "up" => false,
        "desc" | "descending" | "z-a" | "za" | "z to a" | "newest"
        | "newest first" | "newest-first" | "most" | "most first"
        | "most-first" | "down" => true,
        _ => return None,
    })
}

/// The ordering used by the Books pane. It compares each sort key in turn,
/// then falls back to the file path for a deterministic order.
pub fn less_entry(
    a: &Entry,
    b: &Entry,
    keys: &[SortMode],
    pages: &HashMap<PathBuf, Option<PageCount>>,
) -> bool {
    for key in keys {
        match compare_entry_key(a, b, *key, pages) {
            Some(ordering) => return ordering == Ordering::Less,
            None => continue,
        }
    }
    compare_text(&a.path.to_string_lossy(), &b.path.to_string_lossy())
        == Ordering::Less
}

/// Compare one sort key. Missing values always sort last, in either direction.
/// `None` means this key did not decide the order.
fn compare_entry_key(
    a: &Entry,
    b: &Entry,
    key: SortMode,
    pages: &HashMap<PathBuf, Option<PageCount>>,
) -> Option<Ordering> {
    let (a_missing, b_missing) = if key.field == SortField::Pages {
        (page_number(a, pages).is_none(), page_number(b, pages).is_none())
    } else {
        (sort_value(a, key.field).is_empty(), sort_value(b, key.field).is_empty())
    };
    if a_missing || b_missing {
        return match (a_missing, b_missing) {
            (true, true) => None,
            (false, true) => Some(Ordering::Less),
            (true, false) => Some(Ordering::Greater),
            (false, false) => unreachable!(),
        };
    }

    let mut ordering = compare_entries(a, b, key.field, pages);
    if key.desc {
        ordering = ordering.reverse();
    }
    (ordering != Ordering::Equal).then_some(ordering)
}

fn compare_entries(
    a: &Entry,
    b: &Entry,
    field: SortField,
    pages: &HashMap<PathBuf, Option<PageCount>>,
) -> Ordering {
    if field == SortField::Series {
        // Series name is primary, episode number is secondary, and title is the
        // final tie-breaker.
        let series = compare_text(&a.metadata.series, &b.metadata.series);
        if series != Ordering::Equal {
            return series;
        }
        let index = compare_series_index(
            &a.metadata.series_index,
            &b.metadata.series_index,
        );
        if index != Ordering::Equal {
            return index;
        }
        return compare_text(&a.title(), &b.title());
    }
    if field == SortField::Pages {
        return match (page_number(a, pages), page_number(b, pages)) {
            (Some(ap), Some(bp)) => ap.cmp(&bp),
            _ => Ordering::Equal,
        };
    }
    compare_text(&sort_value(a, field), &sort_value(b, field))
}

/// Page count for sorting; unknown and unreadable books are missing.
fn page_number(
    entry: &Entry,
    pages: &HashMap<PathBuf, Option<PageCount>>,
) -> Option<u32> {
    pages.get(&entry.path).copied().flatten().map(|count| count.pages)
}

pub fn sort_value(entry: &Entry, field: SortField) -> String {
    match field {
        SortField::Title => entry.title(),
        SortField::Author => entry.metadata.author_string(),
        SortField::Series => entry.metadata.series.clone(),
        SortField::Date => entry.metadata.date.clone(),
        SortField::Language => entry.metadata.language.clone(),
        SortField::Path => entry.path.to_string_lossy().into_owned(),
        // Pages are numeric and live in the browser's cache, not in Entry.
        SortField::Pages => String::new(),
    }
}

/// Case-insensitive alphabetical comparison with a case-sensitive tie-break so
/// ordering is deterministic.
pub fn compare_text(a: &str, b: &str) -> Ordering {
    let la = a.trim().to_lowercase();
    let lb = b.trim().to_lowercase();
    la.cmp(&lb).then_with(|| a.cmp(b))
}

/// Order numeric series positions correctly ("2" before "10"), falling back to
/// text when either value is not a number.
fn compare_series_index(a: &str, b: &str) -> Ordering {
    match (a.trim().parse::<f64>(), b.trim().parse::<f64>()) {
        (Ok(af), Ok(bf)) => af.partial_cmp(&bf).unwrap_or(Ordering::Equal),
        (Ok(_), Err(_)) => Ordering::Less,
        (Err(_), Ok(_)) => Ordering::Greater,
        (Err(_), Err(_)) => compare_text(a, b),
    }
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
        date: &str,
    ) -> Entry {
        Entry {
            path: PathBuf::from(format!("{title}.epub")),
            metadata: Metadata {
                title: title.into(),
                creators: vec![Creator::new(author)],
                series: series.into(),
                series_index: index.into(),
                date: date.into(),
                ..Metadata::default()
            },
            error: None,
        }
    }

    #[test]
    fn parses_specs() {
        assert_eq!(
            parse_sort_spec("").unwrap(),
            vec![SortMode {
                field: SortField::Path,
                desc: false
            }]
        );
        assert_eq!(
            parse_sort_spec("title:desc").unwrap(),
            vec![SortMode {
                field: SortField::Title,
                desc: true
            }]
        );
        assert_eq!(
            parse_sort_spec("date newest first,author").unwrap(),
            vec![
                SortMode {
                    field: SortField::Date,
                    desc: true
                },
                SortMode {
                    field: SortField::Author,
                    desc: false
                },
            ]
        );
        assert!(parse_sort_spec("nope").is_err());
        assert!(parse_sort_spec("title:sideways").is_err());
    }

    #[test]
    fn sorts_series_numerically() {
        let a = entry("Two", "A", "S", "2", "");
        let b = entry("Ten", "A", "S", "10", "");
        let keys = [SortMode {
            field: SortField::Series,
            desc: false,
        }];
        assert!(less_entry(&a, &b, &keys, &HashMap::new()));
    }

    #[test]
    fn missing_values_sort_last() {
        let full = entry("A", "A", "S", "1", "2001");
        let empty = entry("B", "A", "", "", "");
        let keys = [SortMode {
            field: SortField::Date,
            desc: false,
        }];
        assert!(less_entry(&full, &empty, &keys, &HashMap::new()));
        let keys = [SortMode {
            field: SortField::Date,
            desc: true,
        }];
        assert!(less_entry(&full, &empty, &keys, &HashMap::new()));
    }

    #[test]
    fn sorts_by_page_count_with_missing_last() {
        let small = entry("Small", "A", "", "", "");
        let big = entry("Big", "A", "", "", "");
        let unknown = entry("Unknown", "A", "", "", "");
        let mut pages = HashMap::new();
        pages.insert(
            small.path.clone(),
            Some(PageCount {
                pages: 20,
                estimated: true,
            }),
        );
        pages.insert(
            big.path.clone(),
            Some(PageCount {
                pages: 500,
                estimated: false,
            }),
        );

        let keys = [SortMode {
            field: SortField::Pages,
            desc: false,
        }];
        assert!(less_entry(&small, &big, &keys, &pages));
        assert!(less_entry(&big, &unknown, &keys, &pages));

        let keys = [SortMode {
            field: SortField::Pages,
            desc: true,
        }];
        assert!(less_entry(&big, &small, &keys, &pages));
        assert!(less_entry(&big, &unknown, &keys, &pages));
    }

    #[test]
    fn labels() {
        assert_eq!(
            SortMode {
                field: SortField::Date,
                desc: true
            }
            .label(),
            "Date newest first"
        );
        assert_eq!(
            SortMode {
                field: SortField::Author,
                desc: false
            }
            .label(),
            "Author A–Z"
        );
        assert_eq!(
            SortMode {
                field: SortField::Pages,
                desc: true
            }
            .label(),
            "Pages most first"
        );
        assert_eq!(
            SortMode {
                field: SortField::Pages,
                desc: false
            }
            .label(),
            "Pages least first"
        );
        assert_eq!(
            parse_sort_spec("pages:most").unwrap(),
            vec![SortMode {
                field: SortField::Pages,
                desc: true
            }]
        );
        assert_eq!(
            parse_sort_spec("pages least first").unwrap(),
            vec![SortMode {
                field: SortField::Pages,
                desc: false
            }]
        );
    }
}
