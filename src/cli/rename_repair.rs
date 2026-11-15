//! `rename` and `repair`: file-level operations.

use std::io::Write;
use std::path::Path;

use anyhow::{Result, bail};
use clap::Args;

use super::TargetArgs;
use crate::config::Config;
use crate::epub;
use crate::library;
use crate::rename::{expand_format, filename, validate_format};

#[derive(Args)]
pub struct RenameArgs {
    /// show the new names without renaming
    #[arg(short = 'n', long = "dry-run")]
    pub dry_run: bool,

    /// file name pattern, e.g. '%a - %t.epub'
    #[arg(short = 'f', long = "format", value_name = "FORMAT")]
    pub format: Option<String>,

    /// force slugified metadata values (overrides the config)
    #[arg(short = 's', long = "slugify")]
    pub slugify: bool,

    /// alias for --slugify
    #[arg(long = "sluggify", hide = true)]
    pub sluggify: bool,

    /// disable slugify from the config for this run
    #[arg(long = "no-slugify")]
    pub no_slugify: bool,

    /// alias for --no-slugify
    #[arg(long = "no-sluggify", hide = true)]
    pub no_sluggify: bool,

    #[command(flatten)]
    pub target: TargetArgs,
}

#[derive(Args)]
pub struct RepairArgs {
    /// show what would be rewritten without writing
    #[arg(short = 'n', long = "dry-run")]
    pub dry_run: bool,

    #[command(flatten)]
    pub target: TargetArgs,
}

pub fn rename(
    args: RenameArgs,
    cfg: &Config,
    default_dir: &str,
) -> Result<i32> {
    let format = match &args.format {
        Some(format) => format.clone(),
        None => cfg.rename_format.clone(),
    };
    validate_format(&format)?;

    let slugify = if args.no_slugify || args.no_sluggify {
        false
    } else if args.slugify || args.sluggify {
        true
    } else {
        cfg.slugify_rename()
    };

    let paths = args.target.expand(default_dir)?;
    let mut stdout = std::io::stdout();
    let mut stderr = std::io::stderr();
    for path in &paths {
        let entry = library::load_one(path);
        if let Some(err) = &entry.error {
            writeln!(stderr, "{}: {err}", library::rel_path(path))?;
            continue;
        }
        let name = if format.is_empty() {
            filename(&entry, slugify)
        } else {
            expand_format(&format, &entry, slugify)
        };
        let target = with_name(path, &name);
        if target == *path {
            writeln!(
                stdout,
                "{} (already named correctly)",
                library::rel_path(path)
            )?;
            continue;
        }
        if args.dry_run {
            writeln!(
                stdout,
                "{} → {}",
                library::rel_path(path),
                display_name(&target)
            )?;
            continue;
        }
        if std::fs::metadata(&target).is_ok() {
            writeln!(
                stderr,
                "{}: {} already exists",
                library::rel_path(path),
                display_name(&target)
            )?;
            continue;
        }
        if let Err(err) = std::fs::rename(path, &target) {
            writeln!(stderr, "{}: {err}", library::rel_path(path))?;
            continue;
        }
        writeln!(
            stdout,
            "{} → {}",
            library::rel_path(path),
            display_name(&target)
        )?;
    }
    Ok(0)
}

fn with_name(path: &Path, name: &str) -> std::path::PathBuf {
    path.with_file_name(name)
}

fn display_name(path: &Path) -> String {
    path.file_name()
        .map(|name| name.to_string_lossy().into_owned())
        .unwrap_or_else(|| path.to_string_lossy().into_owned())
}

pub fn repair(args: RepairArgs, default_dir: &str) -> Result<i32> {
    let paths = args.target.expand(default_dir)?;
    let mut stdout = std::io::stdout();
    let mut stderr = std::io::stderr();
    let mut failed = false;
    for path in &paths {
        match epub::repair(path, args.dry_run) {
            Ok(false) => {
                writeln!(stdout, "{} (unchanged)", library::rel_path(path))?
            }
            Ok(true) if args.dry_run => {
                writeln!(stdout, "would repair {}", library::rel_path(path))?
            }
            Ok(true) => {
                writeln!(stdout, "{} (repaired)", library::rel_path(path))?
            }
            Err(err) => {
                failed = true;
                writeln!(stderr, "{}: {err}", library::rel_path(path))?;
            }
        }
    }
    if failed {
        bail!("some files could not be repaired");
    }
    Ok(0)
}
