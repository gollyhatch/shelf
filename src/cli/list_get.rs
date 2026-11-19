//! `list` and `get`: read-only inspection of a shelf.

use std::io::Write;
use std::path::PathBuf;

use anyhow::{Result, bail};
use clap::Args;
use serde::Serialize;

use super::{TargetArgs, field_names};
use crate::config::Config;
use crate::epub::Field;
use crate::library;

#[derive(Args)]
pub struct ListArgs {
    /// output machine readable JSON
    #[arg(long)]
    pub json: bool,

    /// only books on this configured shelf
    #[arg(long, value_name = "NAME")]
    pub shelf: Option<String>,

    #[command(flatten)]
    pub target: TargetArgs,
}

#[derive(Args)]
pub struct GetArgs {
    /// metadata field to print
    #[arg(value_name = "FIELD")]
    pub field: String,

    /// directory of EPUB files (overrides the library)
    #[arg(short = 'C', long = "dir", value_name = "DIR")]
    pub dir: Option<PathBuf>,

    /// EPUB files or directories
    #[arg(value_name = "FILE|DIR")]
    pub paths: Vec<PathBuf>,
}

pub fn list(args: ListArgs, cfg: &Config, default_dir: &str) -> Result<i32> {
    let paths = args.target.expand(default_dir)?;
    let mut entries = library::load(&paths);
    if let Some(name) = &args.shelf {
        let query = match cfg.shelves.get(name) {
            Some(query) => query.clone(),
            None if cfg.shelves.is_empty() => {
                bail!("unknown shelf {name:?} (no shelves configured)")
            }
            None => bail!(
                "unknown shelf {name:?} (configured: {})",
                cfg.shelves.keys().cloned().collect::<Vec<_>>().join(", ")
            ),
        };
        entries.retain(|entry| crate::search::entry_matches(entry, &query));
    }
    if args.json {
        super::write_json(&entries.iter().map(json_entry).collect::<Vec<_>>())?;
        return Ok(0);
    }

    let mut failed = false;
    let mut stdout = std::io::stdout();
    let mut stderr = std::io::stderr();
    for (i, entry) in entries.iter().enumerate() {
        if let Some(err) = &entry.error {
            failed = true;
            writeln!(stderr, "{}: {err}", entry.rel())?;
            continue;
        }
        if i > 0 {
            writeln!(stdout)?;
        }
        write_entry(&mut stdout, entry)?;
    }
    Ok(if failed { 2 } else { 0 })
}

fn write_entry(
    out: &mut impl Write,
    entry: &library::Entry,
) -> std::io::Result<()> {
    let meta = &entry.metadata;
    writeln!(out, "{}", entry.rel())?;
    let mut row = |label: &str, value: &str| -> std::io::Result<()> {
        let value = value.trim();
        if value.is_empty() {
            return Ok(());
        }
        writeln!(out, "  {:<13} {}", format!("{label}:"), first_line(value))
    };
    row("Title", &meta.title)?;
    row("Subtitle", &meta.subtitle)?;
    row("Author", &meta.author_string())?;
    row("Tags", &meta.subjects.join(", "))?;
    row("Series", &meta.series)?;
    row("Episode", &meta.series_index)?;
    row("Publisher", &meta.publisher)?;
    row("Date", &meta.date)?;
    row("Language", &meta.language)?;
    row("Rights", &meta.rights)?;
    row("Identifier", &meta.identifier)?;
    row("Description", &meta.description)?;
    if meta.read {
        row("Read", "yes")?;
    }
    Ok(())
}

fn first_line(s: &str) -> String {
    let s = s.trim();
    match s.find('\n') {
        Some(i) => format!("{} …", s[..i].trim_end_matches([' ', '\t'])),
        None => s.to_owned(),
    }
}

/// The stable JSON shape emitted by `list --json`.
#[derive(Serialize)]
struct JsonEntry {
    path: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    title: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    subtitle: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    authors: Vec<String>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    tags: Vec<String>,
    #[serde(skip_serializing_if = "String::is_empty")]
    series: String,
    #[serde(rename = "seriesIndex", skip_serializing_if = "String::is_empty")]
    series_index: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    publisher: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    date: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    language: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    rights: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    identifier: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    description: String,
    #[serde(skip_serializing_if = "is_false")]
    read: bool,
}

fn is_false(value: &bool) -> bool {
    !*value
}

fn json_entry(entry: &library::Entry) -> JsonEntry {
    let meta = &entry.metadata;
    JsonEntry {
        path: entry.path.to_string_lossy().into_owned(),
        title: meta.title.clone(),
        subtitle: meta.subtitle.clone(),
        authors: meta.creator_names(),
        tags: meta.subjects.clone(),
        series: meta.series.clone(),
        series_index: meta.series_index.clone(),
        publisher: meta.publisher.clone(),
        date: meta.date.clone(),
        language: meta.language.clone(),
        rights: meta.rights.clone(),
        identifier: meta.identifier.clone(),
        description: meta.description.clone(),
        read: meta.read,
    }
}

pub fn get(args: GetArgs, default_dir: &str) -> Result<i32> {
    let Some(field) = Field::parse(&args.field) else {
        bail!("unknown field {:?} (try: {})", args.field, field_names());
    };
    let mut targets = args.paths;
    if let Some(dir) = args.dir.filter(|dir| !dir.as_os_str().is_empty()) {
        targets.push(dir);
    }
    if targets.is_empty() {
        targets.push(PathBuf::from(default_dir));
    }
    let paths = library::expand(&targets, false)?;
    let multiple = paths.len() > 1;
    let mut stdout = std::io::stdout();
    let mut stderr = std::io::stderr();
    for path in &paths {
        let entry = library::load_one(path);
        if let Some(err) = &entry.error {
            writeln!(stderr, "{}: {err}", library::rel_path(path))?;
            continue;
        }
        let value = entry.metadata.get(field);
        if multiple {
            writeln!(stdout, "{}\t{value}", library::rel_path(path))?;
        } else {
            writeln!(stdout, "{value}")?;
        }
    }
    Ok(0)
}
