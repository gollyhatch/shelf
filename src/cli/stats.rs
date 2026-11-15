//! `stats`: counts and totals for a shelf.

use std::collections::{BTreeMap, HashMap, HashSet};
use std::io::Write;

use anyhow::Result;
use clap::Args;
use serde::Serialize;

use super::TargetArgs;
use crate::library::{self, Entry};

#[derive(Args)]
pub struct StatsArgs {
    /// output machine readable JSON
    #[arg(long)]
    pub json: bool,

    /// number of authors and series to list
    #[arg(long, default_value_t = 10, value_name = "N")]
    pub top: i64,

    #[command(flatten)]
    pub target: TargetArgs,
}

/// One named count in the human and JSON reports.
#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct StatCount {
    pub name: String,
    pub count: usize,
}

/// The stable JSON shape of the stats command.
#[derive(Debug, Serialize)]
pub struct Report {
    pub books: usize,
    pub readable: usize,
    pub unreadable: usize,
    pub read: usize,
    pub unread: usize,
    pub authors: usize,
    pub series: usize,
    pub tags: usize,
    pub languages: usize,
    pub size: u64,
    #[serde(
        rename = "languagesByName",
        skip_serializing_if = "BTreeMap::is_empty"
    )]
    pub languages_by: BTreeMap<String, usize>,
    #[serde(rename = "topAuthors", skip_serializing_if = "Vec::is_empty")]
    pub top_authors: Vec<StatCount>,
    #[serde(rename = "topSeries", skip_serializing_if = "Vec::is_empty")]
    pub top_series: Vec<StatCount>,
}

pub fn stats(args: StatsArgs, default_dir: &str) -> Result<i32> {
    let paths = args.target.expand(default_dir)?;
    let entries = library::load(&paths);
    let report = build_stats(&entries, args.top.max(0) as usize);
    if args.json {
        super::write_json(&report)?;
    } else {
        write_stats(&report)?;
    }
    Ok(0)
}

fn build_stats(entries: &[Entry], top: usize) -> Report {
    let mut report = Report {
        books: 0,
        readable: 0,
        unreadable: 0,
        read: 0,
        unread: 0,
        authors: 0,
        series: 0,
        tags: 0,
        languages: 0,
        size: 0,
        languages_by: BTreeMap::new(),
        top_authors: Vec::new(),
        top_series: Vec::new(),
    };

    let mut authors: HashMap<String, StatCount> = HashMap::new();
    let mut series: HashMap<String, StatCount> = HashMap::new();
    let mut tags: HashSet<String> = HashSet::new();
    let mut languages: HashSet<String> = HashSet::new();

    for entry in entries {
        report.books += 1;
        if entry.error.is_some() {
            report.unreadable += 1;
        } else {
            report.readable += 1;
        }
        if entry.metadata.read {
            report.read += 1;
        } else {
            report.unread += 1;
        }
        if let Ok(stat) = std::fs::metadata(&entry.path) {
            report.size += stat.len();
        }

        for name in entry.metadata.creator_names() {
            add_stat(&mut authors, &name);
        }
        if !entry.metadata.series.trim().is_empty() {
            add_stat(&mut series, entry.metadata.series.trim());
        }
        for subject in &entry.metadata.subjects {
            let subject = subject.trim();
            if !subject.is_empty() {
                tags.insert(subject.to_lowercase());
            }
        }
        let language = entry.metadata.language.trim();
        if !language.is_empty() {
            *report.languages_by.entry(language.to_owned()).or_insert(0) += 1;
            languages.insert(language.to_lowercase());
        }
    }

    report.authors = authors.len();
    report.series = series.len();
    report.tags = tags.len();
    report.languages = languages.len();
    report.top_authors = top_stats(authors, top);
    report.top_series = top_stats(series, top);
    report
}

/// Increment a case-insensitive named counter while keeping the first spelling
/// that was seen.
fn add_stat(counts: &mut HashMap<String, StatCount>, name: &str) {
    let name = name.trim();
    if name.is_empty() {
        return;
    }
    counts
        .entry(name.to_lowercase())
        .and_modify(|item| item.count += 1)
        .or_insert_with(|| StatCount {
            name: name.to_owned(),
            count: 1,
        });
}

fn top_stats(
    counts: HashMap<String, StatCount>,
    limit: usize,
) -> Vec<StatCount> {
    let mut out: Vec<StatCount> = counts.into_values().collect();
    out.sort_by(|a, b| {
        b.count
            .cmp(&a.count)
            .then_with(|| a.name.to_lowercase().cmp(&b.name.to_lowercase()))
    });
    out.truncate(limit);
    out
}

fn write_stats(report: &Report) -> Result<()> {
    let mut out = std::io::stdout();
    writeln!(
        out,
        "Books:        {} ({} readable, {} unreadable)",
        report.books, report.readable, report.unreadable
    )?;
    writeln!(
        out,
        "Read:         {} · Unread: {}",
        report.read, report.unread
    )?;
    writeln!(out, "Authors:      {}", report.authors)?;
    writeln!(out, "Series:       {}", report.series)?;
    writeln!(out, "Tags:         {}", report.tags)?;
    writeln!(out, "Languages:    {}", report.languages)?;
    writeln!(out, "Library size: {}", human_bytes(report.size))?;

    if !report.languages_by.is_empty() {
        writeln!(out, "\nLanguages:")?;
        let mut languages: Vec<(&String, &usize)> =
            report.languages_by.iter().collect();
        languages.sort_by(|a, b| {
            b.1.cmp(a.1)
                .then_with(|| a.0.to_lowercase().cmp(&b.0.to_lowercase()))
        });
        for (language, count) in languages {
            writeln!(out, "  {language:<12} {count}")?;
        }
    }
    write_top_stats(&mut out, "Top authors", &report.top_authors)?;
    write_top_stats(&mut out, "Top series", &report.top_series)?;
    Ok(())
}

fn write_top_stats(
    out: &mut impl Write,
    title: &str,
    counts: &[StatCount],
) -> std::io::Result<()> {
    if counts.is_empty() {
        return Ok(());
    }
    writeln!(out, "\n{title}:")?;
    for item in counts {
        writeln!(out, "  {:>4}  {}", item.count, item.name)?;
    }
    Ok(())
}

fn human_bytes(size: u64) -> String {
    const UNIT: u64 = 1024;
    if size < UNIT {
        return format!("{size} B");
    }
    let mut div = UNIT;
    let mut exp = 0;
    let mut n = size / UNIT;
    while n >= UNIT {
        n /= UNIT;
        div *= UNIT;
        exp += 1;
    }
    const SUFFIXES: [&str; 6] = ["K", "M", "G", "T", "P", "E"];
    format!(
        "{:.1} {}iB",
        size as f64 / div as f64,
        SUFFIXES[exp.min(SUFFIXES.len() - 1)]
    )
}

#[cfg(test)]
mod tests {
    use std::path::PathBuf;

    use super::*;
    use crate::epub::{Creator, Metadata};

    fn entry(title: &str, author: &str) -> Entry {
        Entry {
            path: PathBuf::from(format!("{title}.epub")),
            metadata: Metadata {
                title: title.into(),
                creators: vec![Creator::new(author)],
                ..Metadata::default()
            },
            error: None,
        }
    }

    #[test]
    fn counts_books_and_names() {
        let mut a = entry("a", "Frank Herbert");
        a.metadata.read = true;
        a.metadata.series = "Dune".into();
        a.metadata.subjects = vec!["SF".into()];
        a.metadata.language = "en".into();
        let mut b = entry("b", "frank herbert");
        b.metadata.series = "Dune".into();
        b.metadata.language = "EN".into();
        b.error = Some("bad".into());
        let report = build_stats(&[a, b], 10);
        assert_eq!(report.books, 2);
        assert_eq!(report.readable, 1);
        assert_eq!(report.unreadable, 1);
        assert_eq!(report.read, 1);
        assert_eq!(report.authors, 1);
        assert_eq!(report.series, 1);
        assert_eq!(report.tags, 1);
        assert_eq!(report.languages, 1);
        assert_eq!(report.top_authors[0].name, "Frank Herbert");
        assert_eq!(report.top_authors[0].count, 2);
    }

    #[test]
    fn human_bytes_formats() {
        assert_eq!(human_bytes(512), "512 B");
        assert_eq!(human_bytes(2048), "2.0 KiB");
        assert_eq!(human_bytes(5 * 1024 * 1024), "5.0 MiB");
    }
}
