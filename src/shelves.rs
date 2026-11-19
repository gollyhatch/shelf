//! Named shelves: saved search queries over the library.

use std::fs;
use std::io::ErrorKind;
use std::path::Path;

use anyhow::{Context, Result, bail};

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

/// Add one shelf to the configuration file, keeping every other line as it is.
/// The name and query are trimmed, like the ones the loader reads. The file is
/// only written when the result still parses and holds the shelf.
pub fn save(path: &Path, name: &str, query: &str) -> Result<()> {
    let name = name.trim();
    let query = query.trim();
    let entry = entry_line(name, query);
    let text = insert_entry(&read_config(path)?, &entry);
    let shelves = parsed_shelves(&text, path)?;
    if shelves.get(name).and_then(|value| value.as_str()) != Some(query) {
        bail!("config {}: cannot add shelf {name:?}", path.display());
    }
    write_config(path, &text)
}

/// Change the name or the query of one shelf, keeping every other line as it
/// is. The line that defines `old_name` is replaced; when the file has no such
/// line the shelf is added. The name and query are trimmed.
pub fn edit(
    path: &Path,
    old_name: &str,
    name: &str,
    query: &str,
) -> Result<()> {
    let old_name = old_name.trim();
    let name = name.trim();
    let query = query.trim();
    let entry = entry_line(name, query);
    let original = read_config(path)?;
    let text = match shelf_line(&original, old_name) {
        Some((start, end)) => {
            let mut text = original;
            text.replace_range(start..end, &entry);
            text
        }
        None => insert_entry(&original, &entry),
    };
    let shelves = parsed_shelves(&text, path)?;
    let renamed = old_name != name
        && shelves.get(old_name).is_some_and(|value| value.is_str());
    if shelves.get(name).and_then(|value| value.as_str()) != Some(query)
        || renamed
    {
        bail!("config {}: cannot edit shelf {old_name:?}", path.display());
    }
    write_config(path, &text)
}

fn entry_line(name: &str, query: &str) -> String {
    format!("{} = {}", toml_key(name), toml_string(query))
}

fn read_config(path: &Path) -> Result<String> {
    match fs::read_to_string(path) {
        Ok(text) => Ok(text),
        Err(err) if err.kind() == ErrorKind::NotFound => Ok(String::new()),
        Err(err) => bail!("config {}: {err}", path.display()),
    }
}

/// The `[shelves]` table of the candidate text. A repeated key or a second
/// header makes the whole configuration unreadable, so every caller parses
/// before it writes.
fn parsed_shelves(text: &str, path: &Path) -> Result<toml::Table> {
    let root: toml::Value = toml::from_str(text)
        .with_context(|| format!("update config {}", path.display()))?;
    match root.get("shelves") {
        Some(toml::Value::Table(table)) => Ok(table.clone()),
        _ => Ok(toml::Table::new()),
    }
}

fn write_config(path: &Path, text: &str) -> Result<()> {
    if let Some(dir) = path.parent().filter(|dir| !dir.as_os_str().is_empty()) {
        fs::create_dir_all(dir)
            .with_context(|| format!("create {}", dir.display()))?;
    }
    fs::write(path, text)
        .with_context(|| format!("write {}", path.display()))?;
    Ok(())
}

/// Insert `entry` after the `[shelves]` header, or append a table for it.
fn insert_entry(original: &str, entry: &str) -> String {
    match shelves_table_end(original) {
        Some(at) => {
            let block = if original[..at].ends_with('\n') {
                format!("{entry}\n")
            } else {
                format!("\n{entry}\n")
            };
            let mut text = original.to_owned();
            text.insert_str(at, &block);
            text
        }
        None => {
            let mut text = original.to_owned();
            if !text.is_empty() && !text.ends_with('\n') {
                text.push('\n');
            }
            if !text.is_empty() {
                text.push('\n');
            }
            text.push_str("[shelves]\n");
            text.push_str(entry);
            text.push('\n');
            text
        }
    }
}

/// The byte range of the line in `[shelves]` that sets `name` to a string.
fn shelf_line(text: &str, name: &str) -> Option<(usize, usize)> {
    let table = shelves_table_end(text)?;
    let mut start = table;
    for line in text[table..].split_inclusive('\n') {
        let bare = line.trim();
        if bare.starts_with('[') {
            return None;
        }
        if !bare.is_empty() && !bare.starts_with('#') && line_sets(line, name) {
            let end = start + line.trim_end().len();
            return Some((start, end));
        }
        start += line.len();
    }
    None
}

/// Whether one line of `[shelves]` sets `name` to a string value.
fn line_sets(line: &str, name: &str) -> bool {
    let probe = format!("[shelves]\n{line}\n");
    let Ok(root) = toml::from_str::<toml::Value>(&probe) else {
        return false;
    };
    let Some(toml::Value::Table(table)) = root.get("shelves") else {
        return false;
    };
    table.len() == 1 && table.get(name).is_some_and(|value| value.is_str())
}

/// The byte offset just after a `[shelves]` table header, when the file has
/// one. A trailing comment on the header line is allowed.
fn shelves_table_end(text: &str) -> Option<usize> {
    let mut offset = 0;
    for line in text.split_inclusive('\n') {
        let header = line.split('#').next().unwrap_or_default().trim();
        if header == "[shelves]" {
            return Some(offset + line.len());
        }
        offset += line.len();
    }
    None
}

/// A TOML key: bare when the format allows it, quoted otherwise.
fn toml_key(name: &str) -> String {
    let bare = name
        .chars()
        .all(|c| c.is_ascii_alphanumeric() || c == '_' || c == '-');
    if bare {
        name.to_owned()
    } else {
        toml_string(name)
    }
}

/// A TOML basic string with the escapes the format requires.
fn toml_string(value: &str) -> String {
    toml::Value::String(value.to_owned()).to_string()
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

    #[test]
    fn save_creates_the_shelves_table() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("config.toml");
        fs::write(&path, "library = \"/books\"\n").unwrap();
        save(&path, "unread", "read:no").unwrap();
        let text = fs::read_to_string(&path).unwrap();
        assert!(text.starts_with("library = \"/books\"\n"), "{text}");
        assert!(text.contains("[shelves]\nunread = \"read:no\"\n"), "{text}");
        let cfg = crate::config::load_file(&path).unwrap();
        assert_eq!(cfg.shelves["unread"], "read:no");

        save(&path, " spaced ", " read:yes ").unwrap();
        let cfg = crate::config::load_file(&path).unwrap();
        assert_eq!(cfg.shelves["spaced"], "read:yes");
        assert_eq!(cfg.shelves.len(), 2);
    }

    #[test]
    fn save_keeps_an_existing_table_and_its_lines() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("config.toml");
        fs::write(
            &path,
            "theme = \"nord\"\n\n[shelves] # my shelves\nfantasy = \"tag:fantasy\"\n",
        )
        .unwrap();
        save(&path, "le_guin", "tag:\"le guin\"").unwrap();
        let text = fs::read_to_string(&path).unwrap();
        assert_eq!(text.matches("[shelves]").count(), 1, "{text}");
        assert!(text.contains("fantasy = \"tag:fantasy\"\n"), "{text}");
        let cfg = crate::config::load_file(&path).unwrap();
        assert_eq!(cfg.theme, "nord");
        assert_eq!(cfg.shelves.len(), 2);
        assert_eq!(cfg.shelves["le_guin"], "tag:\"le guin\"");
    }

    #[test]
    fn save_appends_a_table_to_an_empty_file() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("config.toml");
        save(&path, "my books", "series:").unwrap();
        let cfg = crate::config::load_file(&path).unwrap();
        assert_eq!(cfg.shelves["my books"], "series:");
    }

    #[test]
    fn save_handles_missing_final_newlines() {
        let dir = tempfile::tempdir().unwrap();
        let bare = dir.path().join("bare.toml");
        fs::write(&bare, "[shelves]").unwrap();
        save(&bare, "unread", "read:no").unwrap();
        let cfg = crate::config::load_file(&bare).unwrap();
        assert_eq!(cfg.shelves["unread"], "read:no");

        let plain = dir.path().join("plain.toml");
        fs::write(&plain, "library = \"/books\"").unwrap();
        save(&plain, "unread", "read:no").unwrap();
        let cfg = crate::config::load_file(&plain).unwrap();
        assert_eq!(cfg.library, "/books");
        assert_eq!(cfg.shelves["unread"], "read:no");
    }

    #[test]
    fn save_refuses_to_break_the_file() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("config.toml");
        fs::write(&path, "[shelves]\nunread = \"read:no\"\n").unwrap();
        assert!(save(&path, "unread", "read:yes").is_err());
        let text = fs::read_to_string(&path).unwrap();
        assert_eq!(text, "[shelves]\nunread = \"read:no\"\n");
    }

    #[test]
    fn edit_changes_the_query_in_place() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("config.toml");
        fs::write(
            &path,
            "# mine\n[shelves]\nfantasy = \"tag:fantasy\"\nunread = \"read:no\"\n",
        )
        .unwrap();
        edit(&path, "unread", "unread", "read:no author:herbert").unwrap();
        let text = fs::read_to_string(&path).unwrap();
        assert!(text.starts_with("# mine\n[shelves]\n"), "{text}");
        assert!(
            text.contains("fantasy = \"tag:fantasy\"\n"),
            "the other shelf is kept: {text}"
        );
        let cfg = crate::config::load_file(&path).unwrap();
        assert_eq!(cfg.shelves["unread"], "read:no author:herbert");
        assert_eq!(cfg.shelves.len(), 2);
    }

    #[test]
    fn edit_renames_a_shelf() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("config.toml");
        fs::write(
            &path,
            "[shelves]\nfantasy = \"tag:fantasy\"\nunread = \"read:no\"\n",
        )
        .unwrap();
        edit(&path, "unread", "to read", "read:no").unwrap();
        let cfg = crate::config::load_file(&path).unwrap();
        assert_eq!(cfg.shelves["to read"], "read:no");
        assert!(!cfg.shelves.contains_key("unread"));
        assert_eq!(cfg.shelves["fantasy"], "tag:fantasy");
        assert_eq!(cfg.shelves.len(), 2);
    }

    #[test]
    fn edit_adds_a_missing_shelf() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("config.toml");
        fs::write(&path, "theme = \"nord\"\n").unwrap();
        edit(&path, "unread", "unread", "read:no").unwrap();
        let cfg = crate::config::load_file(&path).unwrap();
        assert_eq!(cfg.theme, "nord");
        assert_eq!(cfg.shelves["unread"], "read:no");
    }

    #[test]
    fn edit_refuses_to_overwrite_another_shelf() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("config.toml");
        fs::write(
            &path,
            "[shelves]\nfantasy = \"tag:fantasy\"\nunread = \"read:no\"\n",
        )
        .unwrap();
        assert!(edit(&path, "unread", "fantasy", "read:no").is_err());
        let cfg = crate::config::load_file(&path).unwrap();
        assert_eq!(cfg.shelves["fantasy"], "tag:fantasy");
        assert_eq!(cfg.shelves["unread"], "read:no");
    }
}
