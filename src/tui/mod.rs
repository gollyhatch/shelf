//! The interactive browser.

pub mod app;
mod editor;
mod help;
pub mod render;
mod sort;
pub mod theme;
mod wrap;

#[cfg(test)]
mod tests;

use std::path::{Path, PathBuf};
use std::time::Duration;

use anyhow::{Context, Result};
use crossterm::event::{self, Event, KeyEventKind};
use ratatui::DefaultTerminal;

use crate::library;
use crate::rename::NameRule;
use crate::shelves::Shelf;

use app::{App, Msg};

/// Everything the browser needs from the configuration.
#[derive(Clone)]
pub struct RunOptions {
    pub theme: String,
    pub viewer: String,
    pub google_books_api_key: String,
    pub library: String,
    pub sort: String,
    pub search_url: String,
    pub rename: NameRule,
    pub move_rule: Option<NameRule>,
    /// Named shelves from the configuration; the browser prepends "All books".
    pub shelves: Vec<Shelf>,
    /// Config file that shelves created in the app are written to. `None`
    /// disables saving.
    pub config_path: Option<PathBuf>,
}

/// Start the interactive browser for a directory.
pub fn run(dir: &Path, options: RunOptions) -> Result<()> {
    let dir = std::path::absolute(dir).context("resolve directory")?;
    let mut app = App::new(dir, None, options)?;
    app.start_scan();
    event_loop(app)
}

/// Start the browser on a single file, which is the quickest way to fix one
/// book.
pub fn run_file(path: &Path, options: RunOptions) -> Result<()> {
    let path = std::path::absolute(path).context("resolve file")?;
    let dir = path
        .parent()
        .map(Path::to_path_buf)
        .unwrap_or_else(|| std::path::PathBuf::from("."));
    let app = App::new(dir, Some(vec![library::load_one(&path)]), options)?;
    event_loop(app)
}

fn event_loop(mut app: App) -> Result<()> {
    let mut terminal = ratatui::init();
    terminal.hide_cursor()?;
    let result = run_loop(&mut terminal, &mut app);
    ratatui::restore();
    result
}

fn run_loop(terminal: &mut DefaultTerminal, app: &mut App) -> Result<()> {
    loop {
        app.drain_messages();
        terminal.draw(|frame| app.view(frame))?;
        if app.quit {
            return Ok(());
        }
        if event::poll(Duration::from_millis(50))? {
            match event::read()? {
                Event::Key(key)
                    if matches!(
                        key.kind,
                        KeyEventKind::Press | KeyEventKind::Repeat
                    ) =>
                {
                    app.handle_key(key);
                    if let Some(mut command) = app.take_viewer() {
                        ratatui::restore();
                        let outcome = command.status();
                        *terminal = ratatui::init();
                        terminal.hide_cursor()?;
                        let error = match outcome {
                            Ok(status) if status.success() => None,
                            Ok(status) => Some(format!("viewer: {status}")),
                            Err(err) => Some(format!("viewer: {err}")),
                        };
                        app.handle_msg(Msg::ViewerDone(error));
                    }
                }
                Event::Resize(_, _) => {}
                _ => {}
            }
        }
        app.drain_messages();
    }
}
