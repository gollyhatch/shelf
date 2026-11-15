//! Validation of EPUB metadata and archive structure for `check`.

use std::io::{Read, Seek};
use std::path::Path;
use std::sync::LazyLock;

use regex::Regex;
use serde::Serialize;
use zip::ZipArchive;

use crate::epub::{MIMETYPE, Metadata};
use crate::library::Entry;

/// One validation problem found in a book.
#[derive(Clone, Debug, PartialEq, Eq, PartialOrd, Ord, Serialize)]
pub struct Issue {
    pub path: String,
    pub message: String,
}

static LANGUAGE_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"^[A-Za-z]{2,3}([_-][A-Za-z0-9]{2,8})*$")
        .expect("valid language pattern")
});

/// Validate a set of loaded books and return every issue found.
pub fn entries(entries: &[Entry]) -> Vec<Issue> {
    let mut issues = Vec::new();
    let mut isbn_owners: std::collections::HashMap<String, String> =
        std::collections::HashMap::new();
    for entry in entries {
        let path = entry.path.to_string_lossy().into_owned();
        issues.extend(validate_entry(entry));

        let isbn = normalize_isbn(&entry.metadata.identifier);
        if !isbn.is_empty() {
            if let std::collections::hash_map::Entry::Vacant(slot) =
                isbn_owners.entry(isbn.clone())
            {
                slot.insert(path);
            } else {
                issues.push(Issue {
                    path,
                    message: format!("duplicate ISBN {isbn}"),
                });
            }
        }
    }
    issues
}

fn validate_entry(entry: &Entry) -> Vec<Issue> {
    let path = entry.path.to_string_lossy().into_owned();
    let mut issues = Vec::new();
    let mut add = |message: String| {
        issues.push(Issue {
            path: path.clone(),
            message,
        });
    };

    if let Some(err) = &entry.error {
        add(format!("unreadable metadata: {err}"));
        issues.extend(validate_archive(&entry.path));
        return issues;
    }

    let meta = &entry.metadata;
    if meta.title.trim().is_empty() {
        add("missing title".into());
    }
    if meta.author_string().trim().is_empty() {
        add("missing author".into());
    }

    let language = meta.language.trim();
    if language.is_empty() {
        add("missing language".into());
    } else if !LANGUAGE_PATTERN.is_match(language) {
        add(format!("invalid language {language:?}"));
    }

    let date = meta.date.trim();
    if !date.is_empty() && !valid_date(date) {
        add(format!("invalid date {date:?}"));
    }

    let series = meta.series.trim();
    let index = meta.series_index.trim();
    match (series.is_empty(), index.is_empty()) {
        (false, true) => {
            add(format!("series {series:?} has no episode number"))
        }
        (true, false) => add(format!("episode number {index:?} has no series")),
        (false, false) => {
            if index.parse::<f64>().is_err() {
                add(format!("invalid episode number {index:?}"));
            }
        }
        (true, true) => {}
    }

    issues.extend(validate_archive(&entry.path));
    issues
}

/// Accept the four basic ISO-8601 shapes plus RFC 3339 timestamps.
fn valid_date(value: &str) -> bool {
    if chrono::DateTime::parse_from_rfc3339(value).is_ok() {
        return true;
    }
    let mut parts = value.split('-');
    let Some(year) = parts.next() else {
        return false;
    };
    if year.len() != 4 || !year.bytes().all(|b| b.is_ascii_digit()) {
        return false;
    }
    let year: i32 = year.parse().unwrap_or(0);
    let month = match parts.next() {
        Some(part) => {
            if (part.len() != 1 && part.len() != 2)
                || !part.bytes().all(|b| b.is_ascii_digit())
            {
                return false;
            }
            part.parse::<u32>().unwrap_or(0)
        }
        None => return true,
    };
    let day = match parts.next() {
        Some(part) => {
            if (part.len() != 1 && part.len() != 2)
                || !part.bytes().all(|b| b.is_ascii_digit())
            {
                return false;
            }
            Some(part.parse::<u32>().unwrap_or(0))
        }
        None => None,
    };
    if parts.next().is_some() {
        return false;
    }
    if (1..=12).contains(&month) {
        match day {
            Some(day) => {
                chrono::NaiveDate::from_ymd_opt(year, month, day).is_some()
            }
            None => true,
        }
    } else {
        false
    }
}

fn normalize_isbn(value: &str) -> String {
    let isbn: String = value
        .to_ascii_uppercase()
        .chars()
        .filter(|c| c.is_ascii_digit() || *c == 'X')
        .collect();
    if isbn.len() == 10 || isbn.len() == 13 {
        isbn
    } else {
        String::new()
    }
}

fn validate_archive(name: &Path) -> Vec<Issue> {
    let display: String = name.to_string_lossy().into_owned();
    let mut issues = Vec::new();
    let mut add = |message: String| {
        issues.push(Issue {
            path: display.clone(),
            message,
        });
    };

    let file = match std::fs::File::open(name) {
        Ok(file) => file,
        Err(err) => {
            add(format!("unreadable archive: {err}"));
            return issues;
        }
    };
    let mut archive = match ZipArchive::new(file) {
        Ok(archive) => archive,
        Err(err) => {
            add(format!("unreadable archive: {err}"));
            return issues;
        }
    };

    let mut names = std::collections::HashSet::new();
    let mut mimetype_seen = false;
    for i in 0..archive.len() {
        let entry = match archive.by_index_raw(i) {
            Ok(entry) => entry,
            Err(err) => {
                add(format!("unreadable archive: {err}"));
                continue;
            }
        };
        let Ok(entry_name) = entry.name() else {
            continue;
        };
        let entry_name = entry_name.into_owned();
        if unsafe_path(&entry_name) {
            add(format!("unsafe archive path {entry_name:?}"));
        }
        if !names.insert(entry_name.clone()) {
            add(format!("duplicate archive entry {entry_name:?}"));
        }
        if entry_name == "mimetype" {
            mimetype_seen = true;
            if i != 0 {
                add("mimetype is not the first archive entry".into());
            }
            if entry.compression() != zip::CompressionMethod::Stored {
                add("mimetype is compressed".into());
            }
        }
    }

    if !mimetype_seen {
        add("missing mimetype entry".into());
    } else {
        match read_entry(&mut archive, "mimetype", 1024) {
            Err(err) => add(format!("unreadable mimetype: {err}")),
            Ok(data) => {
                let value = String::from_utf8_lossy(&data);
                if value.trim() != MIMETYPE {
                    add(format!("wrong mimetype {:?}", value.trim()));
                }
            }
        }
    }
    if !names.contains("META-INF/container.xml") {
        add("missing META-INF/container.xml".into());
    }
    if !names.iter().any(|name| {
        Path::new(name)
            .extension()
            .and_then(|ext| ext.to_str())
            .is_some_and(|ext| ext.eq_ignore_ascii_case("opf"))
    }) {
        add("no package document".into());
    }
    issues
}

/// A relative archive path that stays inside the archive root.
fn unsafe_path(name: &str) -> bool {
    if name.contains('\\') || name.is_empty() || name.starts_with('/') {
        return true;
    }
    // Windows drive letters (`C:`) are not local paths either.
    let bytes = name.as_bytes();
    if bytes.len() >= 2 && bytes[0].is_ascii_alphabetic() && bytes[1] == b':' {
        return true;
    }
    name.split('/').any(|part| part == "..")
}

fn read_entry<R: Read + Seek>(
    archive: &mut ZipArchive<R>,
    name: &str,
    limit: u64,
) -> Result<Vec<u8>, String> {
    let mut entry = archive.by_name(name).map_err(|err| err.to_string())?;
    if entry.size() > limit {
        return Err("entry is too large".into());
    }
    let mut data = Vec::new();
    entry
        .by_ref()
        .take(limit + 1)
        .read_to_end(&mut data)
        .map_err(|err| err.to_string())?;
    Ok(data)
}

/// Validate a bare metadata value, used by the check tests.
pub fn metadata_issues(path: &Path, meta: &Metadata) -> Vec<Issue> {
    let entry = Entry {
        path: path.to_path_buf(),
        metadata: meta.clone(),
        error: None,
    };
    validate_entry(&entry)
        .into_iter()
        .filter(|issue| !issue.message.starts_with("unreadable archive"))
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn dates() {
        for good in [
            "2001",
            "2001-02",
            "2001-2",
            "2001-02-03",
            "2001-2-3",
            "2001-02-03T04:05:06Z",
        ] {
            assert!(valid_date(good), "{good} should be valid");
        }
        for bad in ["20", "2001-13", "2001-02-30", "2001-02-03-04", "yesterday"]
        {
            assert!(!valid_date(bad), "{bad} should be invalid");
        }
    }

    #[test]
    fn isbn_normalisation() {
        assert_eq!(
            normalize_isbn("urn:isbn:978-0-441-01359-3"),
            "9780441013593"
        );
        assert_eq!(normalize_isbn("0306406152"), "0306406152");
        assert_eq!(normalize_isbn("123"), "");
    }

    #[test]
    fn language_pattern() {
        assert!(LANGUAGE_PATTERN.is_match("en"));
        assert!(LANGUAGE_PATTERN.is_match("en-US"));
        assert!(!LANGUAGE_PATTERN.is_match("english"));
    }

    #[test]
    fn unsafe_archive_paths() {
        assert!(unsafe_path("../x"));
        assert!(unsafe_path("a/../../x"));
        assert!(unsafe_path("/etc/passwd"));
        assert!(unsafe_path("a\\b"));
        assert!(!unsafe_path("OEBPS/content.opf"));
    }
}
