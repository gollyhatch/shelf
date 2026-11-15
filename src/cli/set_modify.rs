//! `set` and `modify`: batch metadata edits.

use std::collections::HashMap;

use anyhow::{Result, bail};
use clap::Args;
use regex::Regex;

use super::{FieldArgs, TargetArgs, apply_files, require_fields};
use crate::epub::{self, Field, Metadata, Package};

#[derive(Args)]
pub struct SetArgs {
    #[command(flatten)]
    pub fields: FieldArgs,

    /// append to multi-valued fields instead of replacing them
    #[arg(long)]
    pub append: bool,

    /// show what would change without writing
    #[arg(short = 'n', long = "dry-run")]
    pub dry_run: bool,

    #[command(flatten)]
    pub target: TargetArgs,
}

#[derive(Args)]
pub struct ModifyArgs {
    /// modify the title
    #[arg(long)]
    pub title: bool,
    /// modify the subtitle
    #[arg(long)]
    pub subtitle: bool,
    /// modify the author(s)
    #[arg(long)]
    pub author: bool,
    /// modify the tags
    #[arg(long)]
    pub tags: bool,
    /// modify the series
    #[arg(long)]
    pub series: bool,
    /// modify the episode number
    #[arg(long = "series-index")]
    pub series_index: bool,
    /// modify the description
    #[arg(long)]
    pub description: bool,
    /// modify the publisher
    #[arg(long)]
    pub publisher: bool,
    /// modify the date
    #[arg(long)]
    pub date: bool,
    /// modify the language
    #[arg(long)]
    pub language: bool,
    /// modify the rights
    #[arg(long)]
    pub rights: bool,
    /// modify the identifier
    #[arg(long = "isbn")]
    pub identifier: bool,
    /// modify the read status
    #[arg(long)]
    pub read: bool,

    /// regular expression to apply
    #[arg(short = 'e', long = "regex", value_name = "REGEX")]
    pub regex: Option<String>,

    /// replacement text (supports $1, ${name})
    #[arg(long = "replacement", value_name = "TEXT", default_value = "")]
    pub replacement: String,

    /// show what would change without writing
    #[arg(short = 'n', long = "dry-run")]
    pub dry_run: bool,

    #[command(flatten)]
    pub target: TargetArgs,
}

impl ModifyArgs {
    fn selected(&self) -> Vec<Field> {
        let selected = [
            (Field::Title, self.title),
            (Field::Subtitle, self.subtitle),
            (Field::Author, self.author),
            (Field::Tags, self.tags),
            (Field::Series, self.series),
            (Field::SeriesIndex, self.series_index),
            (Field::Description, self.description),
            (Field::Publisher, self.publisher),
            (Field::Date, self.date),
            (Field::Language, self.language),
            (Field::Rights, self.rights),
            (Field::Identifier, self.identifier),
            (Field::Read, self.read),
        ];
        Field::ALL
            .into_iter()
            .filter(|field| {
                selected
                    .iter()
                    .any(|(candidate, on)| candidate == field && *on)
            })
            .collect()
    }
}

pub fn set(args: SetArgs, default_dir: &str) -> Result<i32> {
    let fields = args.fields.changes();
    require_fields(&fields, "pass at least one metadata flag")?;

    let mut updates: HashMap<Field, String> = HashMap::new();
    for field in &fields {
        updates.insert(*field, args.fields.value(*field));
    }
    let paths = args.target.expand(default_dir)?;

    let append = args.append;
    let apply = |p: &mut Package| -> Result<()> {
        for field in &fields {
            p.set_with(
                *field,
                updates.get(field).map(String::as_str).unwrap_or_default(),
                append,
            )?;
        }
        Ok(())
    };
    let preview = |meta: &Metadata| -> Metadata {
        let mut meta = meta.clone();
        for field in &fields {
            meta = meta.apply(
                *field,
                updates.get(field).map(String::as_str).unwrap_or_default(),
                append,
            );
        }
        meta
    };
    apply_files(&paths, args.dry_run, "updated", apply, preview)
}

pub fn modify(args: ModifyArgs, default_dir: &str) -> Result<i32> {
    let fields = args.selected();
    require_fields(&fields, "select at least one metadata field")?;
    let Some(source) = &args.regex else {
        bail!("pass --regex/-e with the regular expression to apply");
    };
    if source.is_empty() {
        bail!("--regex must not be empty");
    }
    let regex = Regex::new(source)
        .map_err(|err| anyhow::anyhow!("invalid --regex: {err}"))?;

    let paths = args.target.expand(default_dir)?;
    let replacement = args.replacement.clone();
    let preview_regex = regex.clone();
    let preview_replacement = replacement.clone();
    let preview_fields = fields.clone();

    let apply = move |p: &mut Package| -> Result<()> {
        modify_package(p, &fields, &regex, &replacement)
    };
    let preview = move |meta: &Metadata| -> Metadata {
        modify_metadata(
            meta,
            &preview_fields,
            &preview_regex,
            &preview_replacement,
        )
    };
    apply_files(&paths, args.dry_run, "modified", apply, preview)
}

/// Apply the expression to the selected fields of an open EPUB. It reuses the
/// dry-run transformation so both paths interpret the expression identically,
/// and only writes fields that actually changed.
fn modify_package(
    package: &mut Package,
    fields: &[Field],
    regex: &Regex,
    replacement: &str,
) -> Result<()> {
    let before = package.metadata();
    let after = modify_metadata(&before, fields, regex, replacement);

    if before.creators != after.creators {
        package.set_creators(after.creators.clone())?;
    }
    if before.subjects != after.subjects {
        package.set_subjects(after.subjects.clone())?;
    }
    for field in fields {
        if matches!(field, Field::Author | Field::Tags) {
            continue;
        }
        let value = after.get(*field);
        if value != before.get(*field) {
            package.set(*field, &value)?;
        }
    }
    Ok(())
}

/// The dry-run equivalent of [`modify_package`].
fn modify_metadata(
    meta: &Metadata,
    fields: &[Field],
    regex: &Regex,
    replacement: &str,
) -> Metadata {
    let mut meta = meta.clone();
    for field in fields {
        match field {
            Field::Author => {
                meta.creators = meta
                    .creators
                    .iter()
                    .map(|creator| {
                        regex
                            .replace_all(&creator.name, replacement)
                            .trim()
                            .to_owned()
                    })
                    .filter(|name| !name.is_empty())
                    .map(epub::Creator::new)
                    .collect();
            }
            Field::Tags => {
                meta.subjects = meta
                    .subjects
                    .iter()
                    .map(|subject| {
                        regex
                            .replace_all(subject, replacement)
                            .trim()
                            .to_owned()
                    })
                    .filter(|value| !value.is_empty())
                    .collect();
            }
            other => {
                let current = meta.get(*other);
                let current = current.trim();
                if current.is_empty() {
                    continue;
                }
                meta = meta.apply(
                    *other,
                    &regex.replace_all(current, replacement),
                    false,
                );
            }
        }
    }
    meta
}
