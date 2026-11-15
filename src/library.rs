//! Discovery of EPUB files on disk and loading of their metadata.

use std::fs;
use std::path::{Path, PathBuf};

use crate::epub::{self, Metadata};

/// One EPUB file and its metadata.
#[derive(Clone, Debug)]
pub struct Entry {
    pub path: PathBuf,
    pub metadata: Metadata,
    /// Set when the metadata could not be read; the file is still listed so
    /// that the user can see what is wrong with it.
    pub error: Option<String>,
}

/// Return the EPUB files in a directory (and, when `recursive` is set, its
/// subdirectories), sorted by path. The `.epub` suffix is the only filter
/// applied, mirroring how readers index a shelf.
pub fn discover(dir: &Path, recursive: bool) -> std::io::Result<Vec<PathBuf>> {
    let mut out = Vec::new();
    if recursive {
        walk(dir, dir, &mut out)?;
    } else {
        for item in fs::read_dir(dir)? {
            let item = item?;
            if item.file_type()?.is_dir() {
                continue;
            }
            let path = item.path();
            if is_epub(&path) {
                out.push(path);
            }
        }
    }
    sort_paths(&mut out);
    Ok(out)
}

fn walk(
    root: &Path,
    dir: &Path,
    out: &mut Vec<PathBuf>,
) -> std::io::Result<()> {
    for item in fs::read_dir(dir)? {
        let item = item?;
        let path = item.path();
        let file_type = item.file_type()?;
        if file_type.is_dir() {
            let hidden = path != root
                && item
                    .file_name()
                    .to_str()
                    .is_some_and(|name| name.starts_with('.'));
            if !hidden {
                walk(root, &path, out)?;
            }
            continue;
        }
        if is_epub(&path) {
            out.push(path);
        }
    }
    Ok(())
}

fn sort_paths(paths: &mut [PathBuf]) {
    paths.sort_by(|a, b| a.to_string_lossy().cmp(&b.to_string_lossy()));
}

fn is_epub(path: &Path) -> bool {
    path.extension()
        .and_then(|ext| ext.to_str())
        .is_some_and(|ext| ext.eq_ignore_ascii_case("epub"))
}

/// Turn a mix of files and directories into a list of EPUB paths.
pub fn expand(
    args: &[PathBuf],
    recursive: bool,
) -> std::io::Result<Vec<PathBuf>> {
    let mut out = Vec::new();
    for arg in args {
        let stat = fs::metadata(arg)?;
        if stat.is_dir() {
            out.extend(discover(arg, recursive)?);
        } else {
            out.push(arg.clone());
        }
    }
    Ok(out)
}

/// Read the metadata of every path.
pub fn load(paths: &[PathBuf]) -> Vec<Entry> {
    paths.iter().map(|p| load_one(p)).collect()
}

/// Read the metadata of a single EPUB. Failures are reported on the entry
/// rather than aborting a whole batch.
pub fn load_one(path: &Path) -> Entry {
    match epub::read_metadata(path) {
        Ok(metadata) => Entry {
            path: path.to_path_buf(),
            metadata,
            error: None,
        },
        Err(err) => Entry {
            path: path.to_path_buf(),
            metadata: Metadata::default(),
            error: Some(err.to_string()),
        },
    }
}

impl Entry {
    /// Display title, falling back to the file name.
    pub fn title(&self) -> String {
        let title = self.metadata.title.trim();
        if !title.is_empty() {
            return title.to_owned();
        }
        self.path
            .file_stem()
            .map(|s| s.to_string_lossy().into_owned())
            .unwrap_or_default()
    }

    /// Short author string for list rendering.
    pub fn author(&self) -> String {
        self.metadata.author_string()
    }

    /// Series name and episode number for the list view, with the subtitle in
    /// front when the book has one. Empty when there is neither series nor
    /// episode information.
    pub fn series_line(&self) -> String {
        let series = self.metadata.series.trim();
        let index = self.metadata.series_index.trim();
        let subtitle = self.metadata.subtitle.trim();

        let detail = match (series.is_empty(), index.is_empty()) {
            (false, false) => format!("{series} #{index}"),
            (false, true) => series.to_owned(),
            (true, false) => format!("Episode {index}"),
            (true, true) => return String::new(),
        };
        if subtitle.is_empty() {
            format!("({detail})")
        } else {
            format!("{subtitle} ({detail})")
        }
    }

    /// Render the path relative to the working directory when that is shorter.
    pub fn rel(&self) -> String {
        rel_path(&self.path)
    }
}

pub fn rel_path(path: &Path) -> String {
    let Ok(cwd) = std::env::current_dir() else {
        return path.to_string_lossy().into_owned();
    };
    match path.strip_prefix(&cwd) {
        Ok(rel)
            if !rel.as_os_str().is_empty()
                && rel.to_string_lossy().len()
                    < path.to_string_lossy().len() =>
        {
            rel.to_string_lossy().into_owned()
        }
        _ => path.to_string_lossy().into_owned(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn series_line_formats() {
        let mut entry = Entry {
            path: PathBuf::from("dune.epub"),
            metadata: Metadata::default(),
            error: None,
        };
        assert_eq!(entry.series_line(), "");
        entry.metadata.series = "Dune".into();
        entry.metadata.series_index = "1".into();
        assert_eq!(entry.series_line(), "(Dune #1)");
        entry.metadata.subtitle = "A Novel".into();
        assert_eq!(entry.series_line(), "A Novel (Dune #1)");
        entry.metadata.series.clear();
        assert_eq!(entry.series_line(), "A Novel (Episode 1)");
    }

    #[test]
    fn title_falls_back_to_file_name() {
        let entry = Entry {
            path: PathBuf::from("/books/dune.epub"),
            metadata: Metadata::default(),
            error: None,
        };
        assert_eq!(entry.title(), "dune");
    }

    #[test]
    fn discover_sorts_and_skips_hidden_dirs() {
        let dir = tempfile::tempdir().unwrap();
        fs::write(dir.path().join("b.epub"), b"x").unwrap();
        fs::write(dir.path().join("a.EPUB"), b"x").unwrap();
        fs::write(dir.path().join("notes.txt"), b"x").unwrap();
        let hidden = dir.path().join(".cache");
        fs::create_dir(&hidden).unwrap();
        fs::write(hidden.join("c.epub"), b"x").unwrap();

        let names: Vec<String> = discover(dir.path(), true)
            .unwrap()
            .iter()
            .map(|p| p.file_name().unwrap().to_string_lossy().into_owned())
            .collect();
        assert_eq!(names, ["a.EPUB", "b.epub"]);
    }
}
