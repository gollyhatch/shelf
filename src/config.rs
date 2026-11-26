//! Loads shelf's optional TOML configuration file.

use std::collections::BTreeMap;
use std::fs;
use std::io::ErrorKind;
use std::path::{Path, PathBuf};

use anyhow::{Context, Result, anyhow};
use serde::{Deserialize, Serialize};

/// The contents of the shelf configuration file.
#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(default)]
pub struct Config {
    /// Default directory that commands search for EPUB files.
    pub library: String,
    /// Named shelves: a shelf name mapped to a search query such as "read:no"
    /// or "tag:fantasy". Order is alphabetical.
    pub shelves: BTreeMap<String, String>,
    /// Command used to open an EPUB from the TUI. The selected file path is
    /// appended as the final argument.
    pub viewer: String,
    /// Default Books pane ordering, e.g. "title" or "date:desc".
    pub sort: String,
    /// Write an estimated page count back into the EPUB as `dc:extent`.
    pub write_page_estimates: bool,
    /// Command used to open a URL. When set, it replaces `xdg-open`; the URL
    /// is appended as the final argument.
    pub browser: String,
    /// Opened in the configured browser for the selected book.
    pub search_url: String,
    /// Selects the TUI color palette: any opaline theme id. The default is
    /// "gruvbox-dark".
    pub theme: String,
    /// Optional Google Books API key. Unauthenticated requests share a small
    /// quota and often return 429.
    pub google_books_api_key: String,
    /// Default file-name pattern for the rename command.
    pub rename_format: String,
    /// Makes the rename command use slugified values by default.
    pub slugify: bool,
    /// Accepted as an alias for `slugify`.
    pub sluggify: bool,
}

impl Config {
    /// Whether rename should slugify by default.
    pub fn slugify_rename(&self) -> bool {
        self.slugify || self.sluggify
    }

    /// The configured library, or `fallback` when none is set.
    pub fn library_dir<'a>(&'a self, fallback: &'a str) -> &'a str {
        if self.library.is_empty() {
            fallback
        } else {
            &self.library
        }
    }
}

/// The XDG configuration file path: `$XDG_CONFIG_HOME/shelf/config.toml`, or
/// `~/.config/shelf/config.toml` when `XDG_CONFIG_HOME` is unset.
pub fn path() -> Result<PathBuf> {
    if let Ok(dir) = std::env::var("XDG_CONFIG_HOME") {
        let dir = dir.trim();
        if !dir.is_empty() {
            return Ok(Path::new(dir).join("shelf").join("config.toml"));
        }
    }
    let home = home_dir()
        .ok_or_else(|| anyhow!("cannot locate the configuration directory"))?;
    Ok(home.join(".config").join("shelf").join("config.toml"))
}

fn home_dir() -> Option<PathBuf> {
    std::env::var_os("HOME")
        .filter(|home| !home.is_empty())
        .map(PathBuf::from)
}

/// Read the configuration from the XDG location. A missing file is not an
/// error and yields the default configuration.
pub fn load() -> Result<Config> {
    load_file(&path()?)
}

/// Read the configuration at `path`. A missing file is not an error.
pub fn load_file(path: &Path) -> Result<Config> {
    let data = match fs::read_to_string(path) {
        Ok(data) => data,
        Err(err) if err.kind() == ErrorKind::NotFound => {
            return Ok(Config::default());
        }
        Err(err) => return Err(anyhow!("config {}: {err}", path.display())),
    };
    let mut cfg: Config = toml::from_str(&data)
        .with_context(|| format!("config {}", path.display()))?;
    cfg.library = expand_home(cfg.library.trim());
    cfg.viewer = cfg.viewer.trim().to_owned();
    cfg.sort = cfg.sort.trim().to_owned();
    cfg.browser = cfg.browser.trim().to_owned();
    cfg.search_url = cfg.search_url.trim().to_owned();
    cfg.theme = cfg.theme.trim().to_owned();
    cfg.google_books_api_key = cfg.google_books_api_key.trim().to_owned();
    cfg.rename_format = cfg.rename_format.trim().to_owned();
    cfg.shelves = cfg
        .shelves
        .into_iter()
        .filter(|(name, _)| !name.trim().is_empty())
        .map(|(name, query)| (name.trim().to_owned(), query.trim().to_owned()))
        .collect();
    Ok(cfg)
}

/// Expand a leading `~` or `~/` to the user's home directory.
fn expand_home(path: &str) -> String {
    if path != "~" && !path.starts_with("~/") && !path.starts_with("~\\") {
        return path.to_owned();
    }
    let Some(home) = home_dir() else {
        return path.to_owned();
    };
    if path == "~" {
        return home.to_string_lossy().into_owned();
    }
    home.join(&path[2..]).to_string_lossy().into_owned()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn missing_file_is_default() {
        let dir = tempfile::tempdir().unwrap();
        let cfg = load_file(&dir.path().join("nope.toml")).unwrap();
        assert!(cfg.library.is_empty());
    }

    #[test]
    fn parses_and_trims() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("config.toml");
        fs::write(
            &path,
            "library = \" /books \"\nviewer = \" zathura \"\nbrowser = \" firefox --new-window \"\nslugify = true\nsluggify = true\n",
        )
        .unwrap();
        let cfg = load_file(&path).unwrap();
        assert_eq!(cfg.library, "/books");
        assert_eq!(cfg.viewer, "zathura");
        assert_eq!(cfg.browser, "firefox --new-window");
        assert!(cfg.slugify_rename());
    }

    #[test]
    fn page_estimates_are_off_by_default() {
        assert!(!Config::default().write_page_estimates);
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("config.toml");
        fs::write(&path, "write_page_estimates = true\n").unwrap();
        assert!(load_file(&path).unwrap().write_page_estimates);
    }

    #[test]
    fn parses_shelves() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("config.toml");
        fs::write(
            &path,
            "[shelves]\nunread = \" read:no \"\nfantasy = \"tag:fantasy\"\n",
        )
        .unwrap();
        let cfg = load_file(&path).unwrap();
        assert_eq!(cfg.shelves.len(), 2);
        assert_eq!(cfg.shelves["unread"], "read:no");
        assert_eq!(cfg.shelves["fantasy"], "tag:fantasy");
    }

    #[test]
    fn invalid_toml_is_an_error() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("config.toml");
        fs::write(&path, "library = [").unwrap();
        assert!(load_file(&path).is_err());
    }
}
