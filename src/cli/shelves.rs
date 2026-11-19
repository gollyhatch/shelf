//! `shelves`: list the configured shelves and their book counts.

use std::io::Write;

use anyhow::Result;
use clap::Args;
use serde::Serialize;

use super::TargetArgs;
use crate::config::Config;
use crate::library;

#[derive(Args)]
pub struct ShelvesArgs {
    /// output machine readable JSON
    #[arg(long)]
    pub json: bool,

    #[command(flatten)]
    pub target: TargetArgs,
}

/// One shelf in the stable JSON shape emitted by `shelves --json`.
#[derive(Serialize)]
struct JsonShelf {
    name: String,
    query: String,
    books: usize,
}

pub fn shelves(
    args: ShelvesArgs,
    cfg: &Config,
    default_dir: &str,
) -> Result<i32> {
    let catalog = crate::shelves::catalog(cfg);
    if catalog.is_empty() {
        eprintln!(
            "no shelves configured (add a [shelves] table to the config file)"
        );
        return Ok(0);
    }
    let paths = args.target.expand(default_dir)?;
    let entries = library::load(&paths);
    if args.json {
        let rows: Vec<JsonShelf> = catalog
            .iter()
            .map(|shelf| JsonShelf {
                name: shelf.name.clone(),
                query: shelf.query.clone(),
                books: crate::shelves::count(&entries, &shelf.query),
            })
            .collect();
        super::write_json(&rows)?;
        return Ok(0);
    }
    let mut stdout = std::io::stdout();
    for shelf in &catalog {
        let count = crate::shelves::count(&entries, &shelf.query);
        writeln!(
            stdout,
            "{}: {} ({})",
            shelf.name,
            book_word(count),
            shelf.query
        )?;
    }
    Ok(0)
}

fn book_word(n: usize) -> String {
    if n == 1 {
        "1 book".to_owned()
    } else {
        format!("{n} books")
    }
}
