//! `check` (alias `doctor`): validate a shelf.

use std::io::Write;

use anyhow::{Result, bail};
use clap::Args;

use super::TargetArgs;
use crate::check;
use crate::library;

#[derive(Args)]
pub struct CheckArgs {
    /// output machine readable JSON
    #[arg(long)]
    pub json: bool,

    #[command(flatten)]
    pub target: TargetArgs,
}

pub fn check(args: CheckArgs, default_dir: &str) -> Result<i32> {
    let paths = args.target.expand(default_dir)?;
    let entries = library::load(&paths);
    let mut issues = check::entries(&entries);
    issues.sort_by(|a, b| {
        a.path.cmp(&b.path).then_with(|| a.message.cmp(&b.message))
    });

    let mut stdout = std::io::stdout();
    if args.json {
        serde_json::to_writer_pretty(&mut stdout, &issues)?;
        writeln!(stdout)?;
    } else {
        for issue in &issues {
            let path = library::rel_path(std::path::Path::new(&issue.path));
            writeln!(stdout, "{path}: {}", issue.message)?;
        }
    }

    match issues.len() {
        0 => Ok(0),
        1 => bail!("1 issue found"),
        n => bail!("{n} issues found"),
    }
}
