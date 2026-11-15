//! The command line interface: a batch metadata editor for EPUB files plus the
//! entry point into the TUI.

mod check;
mod docs;
mod list_get;
mod rename_repair;
mod set_modify;
mod stats;

use std::io::Write;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result, anyhow, bail};
use clap::{ArgAction, Args, Parser, Subcommand};

use crate::config::{self, Config};
use crate::epub::{self, Field, Metadata, Package};
use crate::library;
use crate::rename::{self, NameRule};
use crate::tui;

const LONG_ABOUT: &str = r#"shelf manages the metadata of EPUB files from the terminal.

Run it with no arguments to browse the configured library (or the current
directory when no library is configured), or point it at a shelf of books. The
browser shows the title and author of every EPUB it finds and lets you edit
each Dublin Core field in place. The library is read from
$XDG_CONFIG_HOME/shelf/config.toml (~/.config/shelf/config.toml by default).

Batch edits are available as subcommands and only change the fields you name;
everything else in the package document, including Calibre series information
and cover metadata, is preserved.

  shelf                          browse the library
  shelf ~/books                  browse a shelf
  shelf edit dune.epub           edit a single book
  shelf list --json ~/books      inspect a shelf
  shelf set -a 'Frank Herbert' dune.epub
  shelf set --series Dune --series-index 1 dune.epub
  shelf modify --title -e 'Dune' --replacement 'DUNE' dune.epub
  shelf rename --dry-run -r ~/books
  shelf rename -f '%a - %t.epub' ~/books
  shelf repair -n -r ~/books
  shelf stats ~/books
  shelf check -r ~/books
  shelf completion bash"#;

#[derive(Parser)]
#[command(
    name = "shelf",
    version,
    about = "Browse and edit EPUB metadata, in your terminal",
    long_about = LONG_ABOUT,
    args_conflicts_with_subcommands = true
)]
pub struct Cli {
    /// directory of EPUB files (overrides the library)
    #[arg(short = 'C', long = "dir", value_name = "DIR")]
    dir: Option<PathBuf>,

    /// directory or EPUB file to browse
    #[arg(value_name = "DIR|FILE")]
    path: Option<PathBuf>,

    #[command(subcommand)]
    command: Option<Command>,
}

#[derive(Subcommand)]
#[allow(clippy::large_enum_variant)]
enum Command {
    /// List EPUB files with their metadata
    List(list_get::ListArgs),
    /// Print a single metadata field, for use in scripts
    Get(list_get::GetArgs),
    /// Set metadata fields on one or many EPUB files
    Set(set_modify::SetArgs),
    /// Modify metadata values with a regular expression
    Modify(set_modify::ModifyArgs),
    /// Open the interactive editor for a single EPUB
    Edit(EditArgs),
    /// Rename files to match their title (author - title.epub)
    Rename(rename_repair::RenameArgs),
    /// Rewrite EPUB package XML to fix metadata problems
    Repair(rename_repair::RepairArgs),
    /// Show library statistics
    Stats(stats::StatsArgs),
    /// Check EPUB metadata and archive structure
    #[command(alias = "doctor")]
    Check(check::CheckArgs),
    /// Print a shell completion script
    Completion(docs::CompletionArgs),
    /// Print or generate man pages
    Man(docs::ManArgs),
}

#[derive(Args)]
struct EditArgs {
    /// EPUB file to edit
    #[arg(value_name = "FILE")]
    file: PathBuf,
}

impl EditArgs {
    fn file(&self) -> &Path {
        &self.file
    }
}

/// Options shared by the batch commands.
#[derive(Args)]
pub(crate) struct TargetArgs {
    /// recurse into directories
    #[arg(short = 'r', long = "recursive")]
    pub recursive: bool,

    /// directory of EPUB files (overrides the library)
    #[arg(short = 'C', long = "dir", value_name = "DIR")]
    pub dir: Option<PathBuf>,

    /// EPUB files or directories
    #[arg(value_name = "FILE|DIR")]
    pub paths: Vec<PathBuf>,
}

impl TargetArgs {
    pub fn targets(&self, default_dir: &str) -> Vec<PathBuf> {
        let mut args = self.paths.clone();
        if let Some(dir) =
            self.dir.as_ref().filter(|dir| !dir.as_os_str().is_empty())
        {
            args.push(dir.clone());
        }
        if args.is_empty() {
            return vec![PathBuf::from(default_dir)];
        }
        args
    }

    pub fn expand(&self, default_dir: &str) -> Result<Vec<PathBuf>> {
        library::expand(&self.targets(default_dir), self.recursive)
            .map_err(Into::into)
    }
}

/// The metadata flag set shared by `set` and `modify`.
#[derive(Args, Default)]
pub(crate) struct FieldArgs {
    /// set the title
    #[arg(long, action = ArgAction::Append, value_name = "TITLE")]
    pub title: Option<Vec<String>>,
    /// set the subtitle
    #[arg(long, action = ArgAction::Append, value_name = "SUBTITLE")]
    pub subtitle: Option<Vec<String>>,
    /// set the author(s), separate several with ';'
    #[arg(short = 'a', long, action = ArgAction::Append, value_name = "AUTHOR")]
    pub author: Option<Vec<String>>,
    /// set the tags/subjects, comma separated
    #[arg(long, action = ArgAction::Append, value_name = "TAGS")]
    pub tags: Option<Vec<String>>,
    /// set the series name
    #[arg(long, action = ArgAction::Append, value_name = "SERIES")]
    pub series: Option<Vec<String>>,
    /// set the book's number within its series
    #[arg(long, action = ArgAction::Append, value_name = "N")]
    pub series_index: Option<Vec<String>>,
    /// set the description
    #[arg(short = 'd', long, action = ArgAction::Append, value_name = "TEXT")]
    pub description: Option<Vec<String>>,
    /// set the publisher
    #[arg(long, action = ArgAction::Append, value_name = "NAME")]
    pub publisher: Option<Vec<String>>,
    /// set the publication date
    #[arg(long, action = ArgAction::Append, value_name = "DATE")]
    pub date: Option<Vec<String>>,
    /// set the language, e.g. en
    #[arg(short = 'l', long, action = ArgAction::Append, value_name = "LANG")]
    pub language: Option<Vec<String>>,
    /// set the rights/copyright statement
    #[arg(long, action = ArgAction::Append, value_name = "TEXT")]
    pub rights: Option<Vec<String>>,
    /// set the identifier/ISBN
    #[arg(long, action = ArgAction::Append, value_name = "ISBN")]
    pub isbn: Option<Vec<String>>,
    /// mark the book as read (yes/no)
    #[arg(long, action = ArgAction::Append, value_name = "YES|NO")]
    pub read: Option<Vec<String>>,
}

impl FieldArgs {
    fn values(&self, field: Field) -> Option<&Vec<String>> {
        match field {
            Field::Title => self.title.as_ref(),
            Field::Subtitle => self.subtitle.as_ref(),
            Field::Author => self.author.as_ref(),
            Field::Tags => self.tags.as_ref(),
            Field::Series => self.series.as_ref(),
            Field::SeriesIndex => self.series_index.as_ref(),
            Field::Description => self.description.as_ref(),
            Field::Publisher => self.publisher.as_ref(),
            Field::Date => self.date.as_ref(),
            Field::Language => self.language.as_ref(),
            Field::Rights => self.rights.as_ref(),
            Field::Identifier => self.isbn.as_ref(),
            Field::Read => self.read.as_ref(),
        }
    }

    /// The requested field updates in a deterministic order.
    pub fn changes(&self) -> Vec<Field> {
        Field::ALL
            .into_iter()
            .filter(|field| {
                self.values(*field).is_some_and(|values| !values.is_empty())
            })
            .collect()
    }

    /// Collapse repeated flags: multi-valued fields are joined, singular fields
    /// take the last value given.
    pub fn value(&self, field: Field) -> String {
        let values = self.values(field).cloned().unwrap_or_default();
        match field {
            Field::Author => values.join("; "),
            Field::Tags => values.join(","),
            _ => values.last().cloned().unwrap_or_default(),
        }
    }
}

/// Run the command line interface and return the process exit code.
pub fn run() -> i32 {
    match execute(Cli::parse()) {
        Ok(code) => code,
        Err(err) => {
            eprintln!("error: {err}");
            1
        }
    }
}

fn execute(cli: Cli) -> Result<i32> {
    let cfg = config::load()?;
    crate::tui::theme::validate(&cfg.theme)?;
    let default_dir = cfg.library_dir(".").to_owned();

    match cli.command {
        Some(Command::List(args)) => list_get::list(args, &default_dir),
        Some(Command::Get(args)) => list_get::get(args, &default_dir),
        Some(Command::Set(args)) => set_modify::set(args, &default_dir),
        Some(Command::Modify(args)) => set_modify::modify(args, &default_dir),
        Some(Command::Edit(args)) => {
            if !is_epub_file(args.file()) {
                bail!(
                    "{} does not look like an EPUB file",
                    args.file().display()
                );
            }
            run_tui_file(args.file(), &cfg)
        }
        Some(Command::Rename(args)) => {
            rename_repair::rename(args, &cfg, &default_dir)
        }
        Some(Command::Repair(args)) => {
            rename_repair::repair(args, &default_dir)
        }
        Some(Command::Stats(args)) => stats::stats(args, &default_dir),
        Some(Command::Check(args)) => check::check(args, &default_dir),
        Some(Command::Completion(args)) => docs::completion(args),
        Some(Command::Man(args)) => docs::man(args),
        None => {
            let target = cli
                .path
                .or(cli.dir)
                .unwrap_or_else(|| PathBuf::from(&default_dir));
            if is_epub_file(&target) {
                return run_tui_file(&target, &cfg);
            }
            if !target.is_dir() {
                bail!(
                    "{} is neither a directory nor an EPUB file",
                    target.display()
                );
            }
            run_tui(&target, &cfg)
        }
    }
}

fn run_options(cfg: &Config) -> tui::RunOptions {
    let slugify = cfg.slugify_rename();
    tui::RunOptions {
        theme: cfg.theme.clone(),
        viewer: cfg.viewer.clone(),
        google_books_api_key: cfg.google_books_api_key.clone(),
        library: cfg.library.clone(),
        sort: cfg.sort.clone(),
        search_url: cfg.search_url.clone(),
        rename: NameRule::new(cfg.rename_format.clone(), slugify),
        move_rule: rename::move_rule(&cfg.rename_format, slugify),
    }
}

fn run_tui(dir: &Path, cfg: &Config) -> Result<i32> {
    tui::run(dir, run_options(cfg))?;
    Ok(0)
}

fn run_tui_file(path: &Path, cfg: &Config) -> Result<i32> {
    tui::run_file(path, run_options(cfg))?;
    Ok(0)
}

fn is_dir(path: &Path) -> bool {
    path.is_dir()
}

fn is_epub_file(path: &Path) -> bool {
    !is_dir(path)
        && path
            .extension()
            .and_then(|ext| ext.to_str())
            .is_some_and(|ext| ext.eq_ignore_ascii_case("epub"))
}

/// Apply one transformation to every file, report the metadata diff, and
/// continue past per-file failures.
pub(crate) fn apply_files<A, P>(
    paths: &[PathBuf],
    dry_run: bool,
    action: &str,
    apply: A,
    preview: P,
) -> Result<i32>
where
    A: Fn(&mut Package) -> Result<()>,
    P: Fn(&Metadata) -> Metadata,
{
    let mut failed = false;
    let mut stdout = std::io::stdout();
    let mut stderr = std::io::stderr();
    for path in paths {
        let before = library::load_one(path);
        if let Some(err) = &before.error {
            failed = true;
            let _ = writeln!(stderr, "{}: {err}", library::rel_path(path));
            continue;
        }
        let after = if dry_run {
            preview(&before.metadata)
        } else {
            if let Err(err) = epub::update(path, |p| apply(p)) {
                failed = true;
                let _ = writeln!(stderr, "{}: {err}", library::rel_path(path));
                continue;
            }
            match library::load_one(path) {
                entry if entry.error.is_none() => entry.metadata,
                _ => before.metadata.clone(),
            }
        };
        report(
            &mut stdout,
            &library::rel_path(path),
            &before.metadata,
            &after,
            dry_run,
        )?;
    }
    if failed {
        bail!("some files could not be {action}");
    }
    Ok(0)
}

fn report(
    out: &mut impl Write,
    path: &str,
    before: &Metadata,
    after: &Metadata,
    dry: bool,
) -> std::io::Result<()> {
    let lines = diff_fields(before, after);
    if lines.is_empty() {
        writeln!(out, "{path} (unchanged)")?;
        return Ok(());
    }
    if dry {
        writeln!(out, "would update {path}")?;
    } else {
        writeln!(out, "{path}")?;
    }
    for line in lines {
        writeln!(out, "  {line}")?;
    }
    Ok(())
}

pub(crate) fn diff_fields(before: &Metadata, after: &Metadata) -> Vec<String> {
    Field::ALL
        .into_iter()
        .filter_map(|field| {
            let b = before.get(field);
            let a = after.get(field);
            (b != a).then(|| {
                format!(
                    "{:<11} {}",
                    format!("{}:", field.label()),
                    change(&b, &a)
                )
            })
        })
        .collect()
}

fn change(before: &str, after: &str) -> String {
    match (before, after) {
        ("", a) => format!("+ {}", quote(a)),
        (b, "") => format!("- {}", quote(b)),
        (b, a) => format!("{} → {}", quote(b), quote(a)),
    }
}

fn quote(s: &str) -> String {
    if s.is_empty() {
        "\"\"".to_owned()
    } else if s.contains(' ') {
        format!("\"{s}\"")
    } else {
        s.to_owned()
    }
}

pub(crate) fn field_names() -> String {
    Field::ALL
        .iter()
        .map(|f| f.name())
        .collect::<Vec<_>>()
        .join(", ")
}

pub(crate) fn write_json<T: serde::Serialize>(value: &T) -> Result<()> {
    let mut stdout = std::io::stdout();
    serde_json::to_writer_pretty(&mut stdout, value).context("write JSON")?;
    writeln!(stdout)?;
    Ok(())
}

/// Check that at least one field was selected, with the standard error text.
pub(crate) fn require_fields(fields: &[Field], verb: &str) -> Result<()> {
    if fields.is_empty() {
        return Err(anyhow!("nothing to do: {verb} (see --help)"));
    }
    Ok(())
}
