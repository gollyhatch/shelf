//! The browser's state and key/message handling.

use std::collections::{HashMap, HashSet};
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::thread;

use anyhow::{Result, anyhow, bail};
use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};

use super::RunOptions;
use super::editor::{Input, TextArea};
use super::help::HELP_ENTRIES;
use super::sort::{
    SORT_MODES, SortField, SortMode, less_entry, parse_sort_spec,
};
use super::theme::{
    DEFAULT_THEME, Styles, ThemeChoice, catalog, load as load_theme,
};
use crate::epub::{self, Field, Metadata, Package};
use crate::library::{self, Entry};
use crate::online;
use crate::rename::expand_search_url;
use crate::search::entry_matches;
use crate::shelves::{self, Shelf};

/// How many completion candidates fit under the editor input.
pub(crate) const MAX_SUGGESTIONS: usize = 5;

/// How many worker messages one loop iteration may consume before the UI gets
/// a chance to draw and read keys. A large shelf produces thousands of load
/// messages; draining them all at once starves input handling.
const MAX_MESSAGES_PER_TICK: usize = 128;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Pane {
    List,
    Detail,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Mode {
    Browse,
    Edit,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum ReadFilter {
    All,
    Unread,
    Read,
}

impl ReadFilter {
    /// Whether a book with the given read status passes the filter.
    pub fn matches(self, read: bool) -> bool {
        match self {
            ReadFilter::All => true,
            ReadFilter::Read => read,
            ReadFilter::Unread => !read,
        }
    }
}

/// The field of the shelf form that takes keys.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum ShelfField {
    Name,
    Query,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum RowKind {
    /// A plain metadata field.
    Field,
    /// One author.
    Creator,
    /// One tag.
    Subject,
    AddCreator,
    AddSubject,
    /// A read-only value, such as the page count.
    Info,
}

/// One editable line in the detail pane.
#[derive(Clone, Debug)]
pub(crate) struct Row {
    pub kind: RowKind,
    pub field: Field,
    pub index: usize,
    pub label: String,
    pub value: String,
    /// Marks a bulk row whose selected books do not share one value.
    pub mixed: bool,
}

impl Default for Row {
    fn default() -> Self {
        Self {
            kind: RowKind::Field,
            field: Field::Title,
            index: 0,
            label: String::new(),
            value: String::new(),
            mixed: false,
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum StatusKind {
    Info,
    Ok,
    Err,
}

/// One line of the online metadata comparison.
#[derive(Clone, Debug)]
pub(crate) struct OnlineField {
    pub field: Field,
    pub current: String,
    pub found: String,
    pub selected: bool,
}

/// The fields offered by the online lookup, in display order.
pub(crate) const ONLINE_FIELDS_ORDER: [Field; 9] = [
    Field::Title,
    Field::Subtitle,
    Field::Author,
    Field::Tags,
    Field::Description,
    Field::Publisher,
    Field::Date,
    Field::Language,
    Field::Identifier,
];

/// Fields editable across a multi-selection.
pub(crate) const BULK_FIELDS: [Field; 7] = [
    Field::Author,
    Field::Tags,
    Field::Series,
    Field::Publisher,
    Field::Language,
    Field::Rights,
    Field::Read,
];

/// A pluggable online lookup, so tests can avoid the network.
pub(crate) type LookupFn =
    std::sync::Arc<dyn Fn(&Metadata) -> Result<Metadata> + Send + Sync>;

/// A pluggable URL opener, so tests can avoid spawning a browser.
pub(crate) type BrowserFn =
    std::sync::Arc<dyn Fn(&str) -> Result<()> + Send + Sync>;

/// Messages from worker threads.
#[allow(clippy::large_enum_variant)]
pub(crate) enum Msg {
    Scan(Vec<PathBuf>),
    ScanError(String),
    Loaded(Entry),
    LoadDone,
    Pages {
        path: PathBuf,
        count: Option<epub::PageCount>,
    },
    Saved {
        path: PathBuf,
        meta: Metadata,
        error: Option<String>,
    },
    BulkProgress {
        path: PathBuf,
        meta: Option<Metadata>,
        index: usize,
        total: usize,
    },
    BulkDone {
        ok: usize,
        total: usize,
        errs: Vec<String>,
    },
    Deleted {
        removed: Vec<PathBuf>,
        errs: Vec<String>,
    },
    ViewerDone(Option<String>),
    BrowserDone(Option<String>),
    Repaired {
        path: PathBuf,
        changed: bool,
        meta: Metadata,
        load_error: Option<String>,
        error: Option<String>,
    },
    OnlineResult {
        path: PathBuf,
        current: Metadata,
        found: Metadata,
        error: Option<String>,
    },
}

pub(crate) struct App {
    pub dir: PathBuf,
    pub options: RunOptions,
    pub entries: Vec<Entry>,
    pub list_idx: usize,
    pub rows: Vec<Row>,
    pub row_idx: usize,
    pub pages: HashMap<PathBuf, Option<epub::PageCount>>,
    pages_tx: Sender<PathBuf>,
    pages_pending: HashSet<PathBuf>,
    pages_dirty: bool,
    pub pane: Pane,
    pub mode: Mode,

    pub edit_row: Row,
    pub edit_add: bool,
    pub input: Input,
    pub area: TextArea,
    pub using_area: bool,

    pub width: usize,
    pub height: usize,
    pub loading: bool,
    pub fatal: Option<String>,
    pub status: String,
    pub status_kind: StatusKind,
    pub list_top: usize,
    pub row_top: usize,

    pub search: Input,
    pub searching: bool,
    pub filter: String,
    pub filtered: Vec<usize>,

    pub sort_keys: Vec<SortMode>,
    pub sort_open: bool,
    pub sort_idx: usize,

    pub theme_open: bool,
    pub theme_idx: usize,
    pub themes: Vec<ThemeChoice>,
    theme_before: Option<(String, Styles)>,

    /// The configured shelves, in name order.
    pub shelves: Vec<Shelf>,
    pub shelf_open: bool,
    pub shelf_add: bool,
    /// The name-and-query form is open over the picker.
    pub shelf_prompt: bool,
    /// The shelf the form edits; `None` creates a new shelf.
    pub shelf_edit: Option<usize>,
    pub shelf_focus: ShelfField,
    pub shelf_idx: usize,
    pub shelf_counts: Vec<usize>,
    pub shelf_name: Input,
    pub shelf_query: Input,

    pub suggestions: Vec<String>,
    pub suggestion_index: usize,
    pub show_suggestions: bool,
    pub author_candidates: Vec<String>,
    pub series_candidates: Vec<String>,

    pub sel: HashSet<usize>,
    pub edit_bulk: bool,
    pub saving: bool,

    pub read_filter: ReadFilter,

    pub confirm_delete: bool,
    pub delete_paths: Vec<PathBuf>,
    pub deleting: bool,
    pub repairing: bool,

    pub lookup: LookupFn,
    pub looking_up: bool,
    pub online_open: bool,
    pub online_path: Option<PathBuf>,
    pub online_found: Metadata,
    pub online_fields: Vec<OnlineField>,
    pub online_idx: usize,

    pub library_path: String,

    pub help_open: bool,
    pub help_top: usize,

    pub load_total: usize,
    pub load_index: usize,
    pub load_path: Option<PathBuf>,
    load_dirty: bool,

    pub styles: Styles,
    pub theme_name: String,
    pub quit: bool,
    pub pending_viewer: Option<Command>,

    pub open_browser: BrowserFn,

    pub(crate) tx: Sender<Msg>,
    pub(crate) rx: Receiver<Msg>,
}

impl App {
    pub fn new(
        dir: PathBuf,
        entries: Option<Vec<Entry>>,
        options: RunOptions,
    ) -> Result<Self> {
        let theme_name = if options.theme.trim().is_empty() {
            DEFAULT_THEME.to_owned()
        } else {
            options.theme.trim().to_ascii_lowercase()
        };
        let theme = load_theme(&theme_name)?;
        let (tx, rx) = channel();
        let (pages_tx, pages_rx) = channel::<PathBuf>();
        let pages_done = tx.clone();
        let write_page_estimates = options.write_page_estimates;
        thread::spawn(move || {
            while let Ok(path) = pages_rx.recv() {
                let mut count = epub::read_page_count(&path).ok().flatten();
                if write_page_estimates
                    && let Some(pages) = count
                    && pages.estimated
                    && epub::write_page_estimate(&path, pages.pages).is_ok()
                {
                    count = Some(epub::PageCount {
                        pages: pages.pages,
                        estimated: false,
                    });
                }
                let _ = pages_done.send(Msg::Pages { path, count });
            }
        });
        let client = online::Client::new(options.google_books_api_key.clone());
        let loaded_count = entries.as_ref().map_or(0, Vec::len);
        let library_path = options.library.clone();
        let shelves = options.shelves.clone();
        let open_browser = browser_opener(&options.browser);
        let mut app = Self {
            dir,
            options,
            entries: entries.clone().unwrap_or_default(),
            list_idx: 0,
            rows: Vec::new(),
            row_idx: 0,
            pages: HashMap::new(),
            pages_tx,
            pages_pending: HashSet::new(),
            pages_dirty: false,
            pane: Pane::List,
            mode: Mode::Browse,
            edit_row: Row::default(),
            edit_add: false,
            input: Input::default(),
            area: TextArea::default(),
            using_area: false,
            width: 0,
            height: 0,
            loading: entries.is_none(),
            fatal: None,
            status: String::new(),
            status_kind: StatusKind::Info,
            list_top: 0,
            row_top: 0,
            search: Input::default(),
            searching: false,
            filter: String::new(),
            filtered: Vec::new(),
            sort_keys: vec![SortMode {
                field: super::sort::SortField::Path,
                desc: false,
            }],
            sort_open: false,
            sort_idx: 0,
            theme_open: false,
            theme_idx: 0,
            themes: Vec::new(),
            theme_before: None,
            shelves,
            shelf_open: false,
            shelf_add: false,
            shelf_prompt: false,
            shelf_edit: None,
            shelf_focus: ShelfField::Name,
            shelf_idx: 0,
            shelf_counts: Vec::new(),
            shelf_name: Input::default(),
            shelf_query: Input::default(),
            suggestions: Vec::new(),
            suggestion_index: 0,
            show_suggestions: false,
            author_candidates: Vec::new(),
            series_candidates: Vec::new(),
            sel: HashSet::new(),
            edit_bulk: false,
            saving: false,
            read_filter: ReadFilter::All,
            confirm_delete: false,
            delete_paths: Vec::new(),
            deleting: false,
            repairing: false,
            lookup: std::sync::Arc::new(move |meta| client.lookup(meta)),
            looking_up: false,
            online_open: false,
            online_path: None,
            online_found: Metadata::default(),
            online_fields: Vec::new(),
            online_idx: 0,
            library_path,
            help_open: false,
            help_top: 0,
            load_total: loaded_count,
            load_index: loaded_count,
            load_path: None,
            load_dirty: false,
            styles: Styles::new(theme),
            theme_name,
            quit: false,
            pending_viewer: None,
            open_browser,
            tx,
            rx,
        };
        let sort = app.options.sort.clone();
        app.apply_sort_setting(&sort)?;
        app.build_candidates();
        app.apply_filter();
        app.rebuild_rows();
        Ok(app)
    }

    // ---------- workers ----------

    pub fn start_scan(&mut self) {
        if self.loading && !self.entries.is_empty() {
            return;
        }
        self.loading = true;
        let dir = self.dir.clone();
        let tx = self.tx.clone();
        thread::spawn(move || match library::discover(&dir, true) {
            Ok(paths) => {
                let _ = tx.send(Msg::Scan(paths.clone()));
                for path in &paths {
                    let _ = tx.send(Msg::Loaded(library::load_one(path)));
                }
                let _ = tx.send(Msg::LoadDone);
            }
            Err(err) => {
                let _ = tx.send(Msg::ScanError(err.to_string()));
            }
        });
    }

    fn save(
        &self,
        path: PathBuf,
        apply: impl Fn(&mut Package) -> Result<()> + Send + 'static,
    ) {
        let tx = self.tx.clone();
        thread::spawn(move || {
            let mut updated = Metadata::default();
            let result = epub::update(&path, |package| {
                apply(package)?;
                updated = package.metadata();
                Ok(())
            });
            let (meta, error) = match result {
                Ok(()) => (updated, None),
                Err(err) => (Metadata::default(), Some(err.to_string())),
            };
            let _ = tx.send(Msg::Saved { path, meta, error });
        });
    }

    fn start_bulk_save(&mut self, field: Field, value: String, add: bool) {
        let paths: Vec<PathBuf> = self
            .selected_entries()
            .into_iter()
            .filter(|entry| entry.error.is_none())
            .map(|entry| entry.path)
            .collect();
        if paths.is_empty() {
            self.set_status("no readable books selected", StatusKind::Err);
            return;
        }
        self.start_bulk_paths(paths, field, value, add);
    }

    /// Save one field on an explicit set of books, reporting bulk progress.
    fn start_bulk_paths(
        &mut self,
        paths: Vec<PathBuf>,
        field: Field,
        value: String,
        add: bool,
    ) {
        self.saving = true;
        self.set_status(&format!("saving 1/{}", paths.len()), StatusKind::Info);
        let tx = self.tx.clone();
        thread::spawn(move || {
            let total = paths.len();
            let mut ok = 0usize;
            let mut errs = Vec::new();
            for (i, path) in paths.into_iter().enumerate() {
                let mut updated = None;
                let result = epub::update(&path, |package| {
                    package.set_with(field, &value, add)?;
                    updated = Some(package.metadata());
                    Ok(())
                });
                match result {
                    Ok(()) => ok += 1,
                    Err(err) => errs.push(format!("{}: {err}", path.display())),
                }
                let _ = tx.send(Msg::BulkProgress {
                    path,
                    meta: updated,
                    index: i + 1,
                    total,
                });
            }
            let _ = tx.send(Msg::BulkDone { ok, total, errs });
        });
    }

    fn delete_files(&mut self, paths: Vec<PathBuf>) {
        self.deleting = true;
        self.set_status(
            &format!("deleting {}…", file_count(paths.len())),
            StatusKind::Info,
        );
        let tx = self.tx.clone();
        thread::spawn(move || {
            let mut removed = Vec::new();
            let mut errs = Vec::new();
            for path in paths {
                match std::fs::remove_file(&path) {
                    Ok(()) => removed.push(path),
                    Err(err) => errs.push(format!("{}: {err}", path.display())),
                }
            }
            let _ = tx.send(Msg::Deleted { removed, errs });
        });
    }

    fn repair_file(&mut self, path: PathBuf) {
        self.repairing = true;
        self.set_status(
            &format!("repairing {}…", file_name(&path)),
            StatusKind::Info,
        );
        let tx = self.tx.clone();
        thread::spawn(move || {
            let mut msg = Msg::Repaired {
                path: path.clone(),
                changed: false,
                meta: Metadata::default(),
                load_error: None,
                error: None,
            };
            match epub::repair(&path, false) {
                Ok(changed) => {
                    let entry = library::load_one(&path);
                    msg = Msg::Repaired {
                        path,
                        changed,
                        meta: entry.metadata,
                        load_error: entry.error,
                        error: None,
                    };
                }
                Err(err) => {
                    if let Msg::Repaired { error, .. } = &mut msg {
                        *error = Some(err.to_string());
                    }
                }
            }
            let _ = tx.send(msg);
        });
    }

    fn lookup_online(&mut self, entry: Entry) {
        self.looking_up = true;
        self.set_status(
            &format!("searching online for {}…", file_name(&entry.path)),
            StatusKind::Info,
        );
        let tx = self.tx.clone();
        let lookup = self.lookup.clone();
        thread::spawn(move || {
            let found = lookup(&entry.metadata);
            let _ = tx.send(match found {
                Ok(found) => Msg::OnlineResult {
                    path: entry.path,
                    current: entry.metadata,
                    found,
                    error: None,
                },
                Err(err) => Msg::OnlineResult {
                    path: entry.path,
                    current: entry.metadata,
                    found: Metadata::default(),
                    error: Some(err.to_string()),
                },
            });
        });
    }

    // ---------- polling ----------

    pub fn drain_messages(&mut self) {
        for _ in 0..MAX_MESSAGES_PER_TICK {
            let Ok(msg) = self.rx.try_recv() else {
                break;
            };
            self.handle_msg(msg);
        }
        let loaded = std::mem::take(&mut self.load_dirty);
        let pages = std::mem::take(&mut self.pages_dirty);
        if loaded {
            self.refresh_after_load();
        } else if pages
            && self
                .sort_keys
                .iter()
                .any(|key| key.field == SortField::Pages)
        {
            let focus = self.selected().map(|entry| entry.path);
            self.apply_filter();
            self.focus_path(focus.as_deref());
            self.rebuild_rows();
        }
    }

    /// Recompute the filtered list, completions and detail rows once for a
    /// batch of loaded books instead of once per book.
    fn refresh_after_load(&mut self) {
        self.apply_filter();
        self.build_candidates();
        self.rebuild_rows();
    }

    pub fn set_status(&mut self, text: &str, kind: StatusKind) {
        self.status = text.to_owned();
        self.status_kind = kind;
    }

    // ---------- selection ----------

    pub fn selected(&self) -> Option<Entry> {
        self.filtered
            .get(self.list_idx)
            .and_then(|&i| self.entries.get(i))
            .cloned()
    }

    pub fn current_row(&self) -> Option<&Row> {
        self.rows.get(self.row_idx)
    }

    fn selected_entries(&self) -> Vec<Entry> {
        self.entries
            .iter()
            .enumerate()
            .filter(|(i, _)| self.sel.contains(i))
            .map(|(_, entry)| entry.clone())
            .collect()
    }

    fn delete_targets(&self) -> Vec<Entry> {
        if !self.sel.is_empty() {
            return self.selected_entries();
        }
        self.selected().into_iter().collect()
    }

    pub fn bulk_active(&self) -> bool {
        self.sel.len() >= 2
    }

    // ---------- filtering ----------

    pub fn apply_filter(&mut self) {
        let query = self.filter.trim().to_lowercase();
        self.filtered.clear();
        for (i, entry) in self.entries.iter().enumerate() {
            if !query.is_empty() && !entry_matches(entry, &query) {
                continue;
            }
            if !self.read_filter.matches(entry.metadata.read) {
                continue;
            }
            self.filtered.push(i);
        }
        let entries = &self.entries;
        let keys = &self.sort_keys;
        let pages = &self.pages;
        self.filtered.sort_by(|&a, &b| {
            match (
                less_entry(&entries[a], &entries[b], keys, pages),
                less_entry(&entries[b], &entries[a], keys, pages),
            ) {
                (true, _) => std::cmp::Ordering::Less,
                (false, true) => std::cmp::Ordering::Greater,
                _ => std::cmp::Ordering::Equal,
            }
        });
        self.request_all_pages();
    }

    pub fn set_filter(&mut self, query: &str) {
        self.filter = query.to_owned();
        self.apply_filter();
        self.list_idx = 0;
        self.list_top = 0;
        self.row_idx = 0;
        self.row_top = 0;
        self.rebuild_rows();
    }

    pub fn clear_filter(&mut self) {
        self.search.set_value("");
        self.read_filter = ReadFilter::All;
        self.set_filter("");
    }

    fn toggle_read_filter(&mut self, filter: ReadFilter) {
        self.read_filter = if self.read_filter == filter {
            ReadFilter::All
        } else {
            filter
        };
        self.apply_filter();
        self.list_idx = 0;
        self.list_top = 0;
        self.row_idx = 0;
        self.row_top = 0;
        self.rebuild_rows();
    }

    pub fn visible(&self) -> Vec<Entry> {
        self.filtered
            .iter()
            .filter_map(|&i| self.entries.get(i).cloned())
            .collect()
    }

    // ---------- candidates ----------

    pub fn build_candidates(&mut self) {
        self.author_candidates.clear();
        self.series_candidates.clear();
        let mut seen_authors = HashSet::new();
        let mut seen_series = HashSet::new();
        for entry in &self.entries {
            for creator in &entry.metadata.creators {
                let name = creator.name.trim();
                if !name.is_empty() && seen_authors.insert(name.to_lowercase())
                {
                    self.author_candidates.push(name.to_owned());
                }
            }
            let series = entry.metadata.series.trim();
            if !series.is_empty() && seen_series.insert(series.to_lowercase()) {
                self.series_candidates.push(series.to_owned());
            }
        }
        self.author_candidates.sort_by_key(|a| a.to_lowercase());
        self.series_candidates.sort_by_key(|a| a.to_lowercase());
    }

    fn candidates_for(&self, row: &Row) -> &[String] {
        match row.field {
            Field::Author => &self.author_candidates,
            Field::Series => &self.series_candidates,
            _ => &[],
        }
    }

    pub fn refresh_suggestions(&mut self) {
        self.close_suggestions();
        if self.using_area {
            return;
        }
        let query = self.input.value().trim().to_lowercase();
        let mut prefix = Vec::new();
        let mut rest = Vec::new();
        let row = self.edit_row.clone();
        for candidate in self.candidates_for(&row) {
            let lower = candidate.to_lowercase();
            if lower == query {
                continue;
            }
            if query.is_empty() || lower.starts_with(&query) {
                prefix.push(candidate.clone());
            } else if lower.contains(&query) {
                rest.push(candidate.clone());
            }
        }
        self.suggestions = prefix;
        self.suggestions.extend(rest);
        if self.suggestions.is_empty() {
            return;
        }
        self.suggestion_index =
            clamp(self.suggestion_index, 0, self.suggestions.len() - 1);
        self.show_suggestions = true;
    }

    fn close_suggestions(&mut self) {
        self.suggestions.clear();
        self.show_suggestions = false;
        self.suggestion_index = 0;
    }

    fn move_suggestion(&mut self, delta: isize) {
        let n = self.suggestions.len();
        if n == 0 {
            return;
        }
        let index =
            (self.suggestion_index as isize + delta).rem_euclid(n as isize);
        self.suggestion_index = index as usize;
    }

    fn accept_suggestion(&mut self) {
        if !self.show_suggestions {
            return;
        }
        let value = self.suggestions[self.suggestion_index].clone();
        self.input.set_value(&value);
        self.input.cursor_end();
        self.refresh_suggestions();
    }

    // ---------- rows ----------

    pub fn rebuild_rows(&mut self) {
        let keep = self.rows.get(self.row_idx).cloned();
        if self.bulk_active() {
            self.rows = build_bulk_rows(&self.selected_entries());
        } else {
            let Some(entry) = self.selected() else {
                self.rows.clear();
                self.row_idx = 0;
                self.row_top = 0;
                return;
            };
            let rows = {
                let pages =
                    self.pages.get(&entry.path).and_then(Option::as_ref);
                build_rows(&entry.metadata, pages)
            };
            self.rows = rows;
            if entry.error.is_none() {
                self.request_pages(&entry.path);
            }
        }

        if let Some(keep) = keep
            && let Some(i) = self.rows.iter().position(|row| {
                row.kind == keep.kind
                    && row.field == keep.field
                    && row.index == keep.index
            })
        {
            self.row_idx = i;
            return;
        }
        self.row_idx =
            clamp(self.row_idx, 0, self.rows.len().saturating_sub(1));
    }

    fn request_pages(&mut self, path: &Path) {
        if self.pages.contains_key(path)
            || !self.pages_pending.insert(path.to_path_buf())
        {
            return;
        }
        let _ = self.pages_tx.send(path.to_path_buf());
    }

    /// A pages sort needs every book's count, not just the selected one.
    fn request_all_pages(&mut self) {
        if !self.sort_keys.iter().any(|key| key.field == SortField::Pages) {
            return;
        }
        let missing: Vec<PathBuf> = self
            .entries
            .iter()
            .filter(|entry| {
                entry.error.is_none()
                    && !self.pages.contains_key(&entry.path)
                    && !self.pages_pending.contains(&entry.path)
            })
            .map(|entry| entry.path.clone())
            .collect();
        for path in missing {
            self.request_pages(&path);
        }
    }

    fn move_list(&mut self, delta: isize) {
        if self.filtered.is_empty() {
            return;
        }
        let next = clamp(
            (self.list_idx as isize + delta).max(0) as usize,
            0,
            self.filtered.len() - 1,
        );
        if next == self.list_idx {
            return;
        }
        self.list_idx = next;
        if !self.bulk_active() {
            self.row_idx = 0;
            self.row_top = 0;
        }
        self.rebuild_rows();
        self.scroll_list();
    }

    fn move_list_group(&mut self, delta: isize) {
        if delta == 0 || self.filtered.is_empty() {
            return;
        }
        if self.list_idx >= self.filtered.len() {
            return;
        }
        let current = self.entries[self.filtered[self.list_idx]].clone();
        let mut i = self.list_idx as isize + delta;
        while i >= 0 && (i as usize) < self.filtered.len() {
            if !same_book_group(
                &current,
                &self.entries[self.filtered[i as usize]],
            ) {
                self.list_idx = i as usize;
                if !self.bulk_active() {
                    self.row_idx = 0;
                    self.row_top = 0;
                }
                self.rebuild_rows();
                self.scroll_list();
                return;
            }
            i += delta;
        }
    }

    pub fn scroll_list(&mut self) {
        let items = self.visible();
        if items.is_empty() {
            self.list_top = 0;
            return;
        }
        self.list_top = adjust_list_top(
            self.list_top,
            self.list_idx,
            &entry_heights(&items),
            self.list_content_height(),
        );
    }

    fn move_row(&mut self, delta: isize) {
        if self.rows.is_empty() {
            return;
        }
        self.row_idx = clamp(
            (self.row_idx as isize + delta).max(0) as usize,
            0,
            self.rows.len() - 1,
        );
        let page = self.row_page_size();
        self.row_top =
            clamp(self.row_top, 0, self.rows.len().saturating_sub(page));
        if self.row_idx < self.row_top {
            self.row_top = self.row_idx;
        }
        if self.row_idx >= self.row_top + page {
            self.row_top = self.row_idx + 1 - page;
        }
    }

    pub fn focus_path(&mut self, path: Option<&Path>) {
        self.list_idx = 0;
        if let Some(path) = path
            && let Some(i) = self
                .filtered
                .iter()
                .position(|&idx| self.entries[idx].path == path)
        {
            self.list_idx = i;
        }
        self.list_idx =
            clamp(self.list_idx, 0, self.filtered.len().saturating_sub(1));
        self.scroll_list();
    }

    fn remove_entries(&mut self, paths: &[PathBuf]) {
        let gone: HashSet<&Path> = paths.iter().map(PathBuf::as_path).collect();
        self.entries
            .retain(|entry| !gone.contains(entry.path.as_path()));
        self.sel.clear();
        self.apply_filter();
        self.list_idx =
            clamp(self.list_idx, 0, self.filtered.len().saturating_sub(1));
        self.row_idx = 0;
        self.row_top = 0;
        self.build_candidates();
        self.rebuild_rows();
        self.refresh_suggestions();
        self.scroll_list();
    }

    // ---------- layout ----------

    pub fn view_search(&self) -> Option<String> {
        if self.searching {
            return Some(self.search.value());
        }
        if self.filter.is_empty() {
            return None;
        }
        Some(self.filter.clone())
    }

    pub fn editor_height(&self) -> usize {
        if self.mode != Mode::Edit {
            return 0;
        }
        let lines = self.edit_editor_line_count();
        lines + 1
    }

    fn edit_editor_line_count(&self) -> usize {
        let mut inner = 1; // label
        inner += if self.using_area {
            clamp(self.height / 3, 3, 8)
        } else {
            1
        };
        if self.show_suggestions {
            inner += self.suggestions.len().min(MAX_SUGGESTIONS);
        }
        if self.editing_new_creator() || self.editing_new_subject() {
            inner += 1;
        }
        inner + 2 // border
    }

    pub fn body_height(&self) -> usize {
        let mut fixed = 2; // header + blank line before the body
        if self.view_search().is_some() {
            fixed += 1;
        }
        if self.mode == Mode::Edit {
            fixed += self.editor_height();
            let avail = self.height.saturating_sub(fixed);
            if avail < 6 {
                return 0;
            }
            return avail;
        }
        if self.status.is_empty() {
            fixed += 2; // blank + footer
        } else {
            fixed += 1;
        }
        self.height.saturating_sub(fixed)
    }

    pub fn pane_heights(&self) -> (usize, usize, bool) {
        let avail = self.body_height();
        if self.width >= 84 {
            return (avail, avail, false);
        }
        if avail == 0 {
            return (0, 0, true);
        }
        let mut list = clamp(avail / 3, 4, 12);
        if list > avail.saturating_sub(3) {
            list = avail.saturating_sub(3).max(3);
        }
        list = list.min(avail);
        (list, avail - list, true)
    }

    pub fn list_content_height(&self) -> usize {
        let (list_h, _, _) = self.pane_heights();
        list_h.saturating_sub(3).max(1)
    }

    pub fn list_page_size(&self) -> usize {
        let (list_h, _, _) = self.pane_heights();
        ((list_h.saturating_sub(3)) / 2).max(1)
    }

    pub fn row_page_size(&self) -> usize {
        let (_, detail_h, _) = self.pane_heights();
        detail_h.saturating_sub(3).max(1)
    }

    pub fn help_page_size(&self) -> usize {
        let height = self.body_height();
        if height < 4 {
            return 0;
        }
        height - 3
    }

    pub fn help_max_top(&self) -> usize {
        HELP_ENTRIES.len().saturating_sub(self.help_page_size())
    }

    pub fn sort_page_size(&self) -> usize {
        let height = self.body_height();
        if height < 4 {
            return 0;
        }
        height - 3
    }

    pub fn theme_page_size(&self) -> usize {
        let height = self.body_height();
        if height < 4 {
            return 0;
        }
        // Keep the pop-over to roughly half the body so the shelf behind it
        // stays visible while previewing.
        let available = height - 3;
        available.min((height / 2).max(4)).min(20)
    }

    pub fn online_list_height(&self) -> usize {
        let height = self.body_height();
        if height < 6 {
            return 0;
        }
        let content = height - 2;
        content.saturating_sub(2)
    }

    pub fn sort_label(&self) -> String {
        if self.sort_keys.is_empty() {
            return SORT_MODES[0].label();
        }
        self.sort_keys
            .iter()
            .map(|key| key.label())
            .collect::<Vec<_>>()
            .join(" → ")
    }

    pub(crate) fn current_sort_index(&self) -> Option<usize> {
        if self.sort_keys.len() != 1 {
            return None;
        }
        SORT_MODES
            .iter()
            .position(|mode| *mode == self.sort_keys[0])
    }

    pub fn selected_unreadable(&self) -> bool {
        self.selected().is_some_and(|entry| entry.error.is_some())
    }

    // ---------- editing ----------

    pub(crate) fn editing_new_creator(&self) -> bool {
        self.edit_row.kind == RowKind::AddCreator
            || (self.edit_add && self.edit_row.field == Field::Author)
    }

    pub(crate) fn editing_new_subject(&self) -> bool {
        self.edit_row.kind == RowKind::AddSubject
            || (self.edit_add && self.edit_row.field == Field::Tags)
    }

    pub fn edit_value(&self) -> String {
        if self.using_area {
            self.area.value()
        } else {
            self.input.value()
        }
    }

    fn start_edit(&mut self, add: bool) {
        let Some(row) = self.current_row().cloned() else {
            return;
        };
        if row.kind == RowKind::Info {
            return;
        }
        if row.field == Field::Read {
            if !add {
                self.toggle_read();
            }
            return;
        }
        let add = add && matches!(row.field, Field::Author | Field::Tags);

        self.edit_row = row.clone();
        self.edit_add = add;
        self.edit_bulk = self.bulk_active();

        let initial = if add {
            String::new()
        } else if row.kind == RowKind::Creator {
            row.label.clone()
        } else {
            row.value.clone()
        };

        self.mode = Mode::Edit;
        self.using_area = !add
            && row.kind == RowKind::Field
            && row.field == Field::Description;
        if self.using_area {
            self.area = TextArea::from_value(&initial);
        } else {
            self.input.set_value(&initial);
            self.input.cursor_end();
        }
        self.refresh_suggestions();
    }

    fn cancel_edit(&mut self) {
        self.mode = Mode::Browse;
        self.edit_bulk = false;
        self.close_suggestions();
    }

    fn commit_edit(&mut self) {
        if self.edit_bulk {
            self.commit_bulk_edit();
            return;
        }
        let Some(entry) = self.selected() else {
            self.mode = Mode::Browse;
            return;
        };
        let value = self.edit_value();
        let row = self.edit_row.clone();
        let new_creator = self.editing_new_creator();
        let new_subject = self.editing_new_subject();

        self.mode = Mode::Browse;
        self.close_suggestions();
        self.save(entry.path, move |package| {
            let meta = package.metadata();
            match row.kind {
                _ if new_creator => {
                    let mut creators = meta.creators.clone();
                    creators.extend(epub::parse_creator_list(&value));
                    package.set_creators(creators)
                }
                _ if new_subject => {
                    let mut subjects = meta.subjects.clone();
                    subjects.extend(epub::split_list(&value, ','));
                    package.set_subjects(subjects)
                }
                RowKind::Creator => {
                    let mut creators = meta.creators.clone();
                    if let Some(slot) = creators.get_mut(row.index) {
                        *slot = epub::parse_creator(&value);
                    }
                    package.set_creators(creators)
                }
                RowKind::Subject => {
                    let mut subjects = meta.subjects.clone();
                    if let Some(slot) = subjects.get_mut(row.index) {
                        *slot = value.trim().to_owned();
                    }
                    package.set_subjects(subjects)
                }
                RowKind::Info => Ok(()),
                _ => package.set(row.field, &value),
            }
        });
    }

    fn commit_bulk_edit(&mut self) {
        let value = self.edit_value();
        let row = self.edit_row.clone();
        let add = self.edit_add;

        self.mode = Mode::Browse;
        self.edit_bulk = false;
        self.close_suggestions();

        let empty = value.trim().is_empty();
        if empty && (add || row.mixed || row.value.is_empty()) {
            return;
        }
        self.start_bulk_save(row.field, value, add);
    }

    fn delete_row(&mut self) {
        let Some(row) = self.current_row().cloned() else {
            return;
        };
        if row.field == Field::Read {
            return;
        }
        if self.bulk_active() {
            if row.value.is_empty() && !row.mixed {
                return;
            }
            self.start_bulk_save(row.field, String::new(), false);
            return;
        }
        let Some(entry) = self.selected() else {
            return;
        };
        match row.kind {
            RowKind::AddCreator | RowKind::AddSubject | RowKind::Info => {}
            RowKind::Creator => {
                self.save(entry.path, move |package| {
                    let mut creators = package.metadata().creators;
                    if row.index < creators.len() {
                        creators.remove(row.index);
                    }
                    package.set_creators(creators)
                });
            }
            RowKind::Subject => {
                self.save(entry.path, move |package| {
                    let mut subjects = package.metadata().subjects;
                    if row.index < subjects.len() {
                        subjects.remove(row.index);
                    }
                    package.set_subjects(subjects)
                });
            }
            RowKind::Field => {
                if row.value.is_empty() {
                    return;
                }
                let field = row.field;
                self.save(entry.path, move |package| package.set(field, ""));
            }
        }
    }

    // ---------- actions ----------

    fn toggle_read(&mut self) {
        let targets = self.delete_targets();
        if targets.is_empty() {
            return;
        }
        let mark_read = targets.iter().any(|entry| !entry.metadata.read);
        let value = if mark_read { "yes" } else { "no" };

        if !self.sel.is_empty() {
            self.start_bulk_save(Field::Read, value.to_owned(), false);
            return;
        }
        let Some(entry) = self.selected() else {
            return;
        };
        self.set_status("updating read status…", StatusKind::Info);
        self.save(entry.path, move |package| package.set(Field::Read, value));
    }

    fn repair_selected(&mut self) {
        if self.repairing {
            return;
        }
        let Some(entry) = self.selected() else {
            return;
        };
        if entry.error.is_none() {
            return;
        }
        self.repair_file(entry.path);
    }

    fn lookup_selected(&mut self) {
        if self.looking_up || self.online_open {
            return;
        }
        let Some(entry) = self.selected() else {
            return;
        };
        if entry.error.is_some() {
            self.set_status(
                "cannot search online: metadata is unreadable",
                StatusKind::Err,
            );
            return;
        }
        self.lookup_online(entry);
    }

    fn apply_online(&mut self) {
        if !self.online_open {
            return;
        }
        let selected: Vec<OnlineField> = self
            .online_fields
            .iter()
            .filter(|field| field.selected)
            .cloned()
            .collect();
        let path = self.online_path.clone();
        let found = self.online_found.clone();
        self.online_open = false;
        self.online_fields.clear();
        if selected.is_empty() {
            self.set_status("no metadata fields selected", StatusKind::Info);
            return;
        }
        let Some(path) = path else {
            return;
        };
        self.set_status("updating metadata…", StatusKind::Info);
        self.save(path, move |package| {
            for field in &selected {
                match field.field {
                    Field::Author => {
                        package.set_creators(found.creators.clone())?
                    }
                    Field::Tags => {
                        package.set_subjects(found.subjects.clone())?
                    }
                    other => package.set(other, &field.found)?,
                }
            }
            Ok(())
        });
    }

    fn rename_selected(&mut self) {
        let targets = self.delete_targets();
        if targets.is_empty() {
            return;
        }
        let mut current_path = self.selected().map(|entry| entry.path);
        let mut renamed = 0usize;
        let mut unchanged = 0usize;
        let mut failures = 0usize;
        for entry in &targets {
            let name = match self.options.rename.name(entry) {
                Ok(name) if !name.is_empty() => name,
                _ => {
                    failures += 1;
                    continue;
                }
            };
            let target = entry.path.with_file_name(&name);
            if target == entry.path {
                unchanged += 1;
                continue;
            }
            if std::fs::metadata(&target).is_ok() {
                failures += 1;
                continue;
            }
            if std::fs::rename(&entry.path, &target).is_err() {
                failures += 1;
                continue;
            }
            for existing in &mut self.entries {
                if existing.path == entry.path {
                    existing.path = target.clone();
                    break;
                }
            }
            if current_path.as_deref() == Some(entry.path.as_path()) {
                current_path = Some(target.clone());
            }
            renamed += 1;
        }
        self.apply_filter();
        self.focus_path(current_path.as_deref());
        self.rebuild_rows();
        match (failures, renamed, unchanged) {
            (f, _, _) if f > 0 => self.set_status(
                &format!(
                    "renamed {renamed} of {} books ({f} failed)",
                    targets.len()
                ),
                StatusKind::Err,
            ),
            (_, r, _) if r > 0 => self.set_status(
                &format!("renamed {}", book_count(r)),
                StatusKind::Ok,
            ),
            (_, _, u) if u > 0 => {
                self.set_status("already named correctly", StatusKind::Info)
            }
            _ => {}
        }
    }

    fn move_selected_to_library(&mut self) {
        if self.library_path.is_empty() {
            self.set_status("no library configured", StatusKind::Err);
            return;
        }
        let Some(rule) = self.options.move_rule.clone() else {
            self.set_status("rename_format is not configured", StatusKind::Err);
            return;
        };
        if self.loading {
            return;
        }
        let targets = self.delete_targets();
        if targets.is_empty() {
            return;
        }
        let library_dir = match std::path::absolute(&self.library_path) {
            Ok(path) => path,
            Err(err) => {
                self.set_status(
                    &format!("cannot use the configured library: {err}"),
                    StatusKind::Err,
                );
                return;
            }
        };
        if let Err(err) = std::fs::create_dir_all(&library_dir) {
            self.set_status(
                &format!("cannot create the library: {err}"),
                StatusKind::Err,
            );
            return;
        }

        let mut moved = Vec::new();
        let mut failures = 0usize;
        let mut already = 0usize;
        for entry in &targets {
            if path_within(&library_dir, &entry.path) {
                already += 1;
                continue;
            }
            let name = match rule.name(entry) {
                Ok(name) if !name.is_empty() => name,
                _ => {
                    failures += 1;
                    continue;
                }
            };
            let target = library_dir.join(&name);
            if target == entry.path || !path_within(&library_dir, &target) {
                failures += 1;
                continue;
            }
            if std::fs::metadata(&target).is_ok() {
                failures += 1;
                continue;
            }
            if std::fs::rename(&entry.path, &target).is_err() {
                failures += 1;
                continue;
            }
            moved.push(entry.path.clone());
        }

        if !moved.is_empty() {
            self.remove_entries(&moved);
        }
        let eligible = targets.len() - already;
        match (failures, moved.len(), already) {
            (f, _, _) if f > 0 => self.set_status(
                &format!(
                    "moved {} of {eligible} books to the library ({f} failed)",
                    moved.len()
                ),
                StatusKind::Err,
            ),
            (_, m, a) if m > 0 && a > 0 => self.set_status(
                &format!(
                    "moved {} to the library ({a} already there)",
                    book_count(m)
                ),
                StatusKind::Ok,
            ),
            (_, m, _) if m > 0 => self.set_status(
                &format!("moved {} to the library", book_count(m)),
                StatusKind::Ok,
            ),
            (_, _, a) if a > 0 => {
                self.set_status("already in the library", StatusKind::Info)
            }
            _ => {}
        }
    }

    fn open_viewer(&mut self) {
        if self.options.viewer.trim().is_empty() {
            return;
        }
        let Some(entry) = self.selected() else {
            return;
        };
        let args = match shlex::split(&self.options.viewer) {
            Some(args) if !args.is_empty() && !args[0].trim().is_empty() => {
                args
            }
            Some(_) => {
                self.set_status("viewer: viewer is empty", StatusKind::Err);
                return;
            }
            None => {
                self.set_status(
                    "viewer: parse viewer: unmatched quote",
                    StatusKind::Err,
                );
                return;
            }
        };
        let mut command = Command::new(&args[0]);
        command.args(&args[1..]).arg(&entry.path);
        self.set_status(
            &format!("opening {}…", file_name(&entry.path)),
            StatusKind::Info,
        );
        self.pending_viewer = Some(command);
    }

    fn open_search_url(&mut self) {
        if self.options.search_url.trim().is_empty() {
            return;
        }
        let Some(entry) = self.selected() else {
            return;
        };
        let target =
            expand_search_url(&self.options.search_url, &entry.metadata);
        if target.is_empty() {
            return;
        }
        let opener = self.open_browser.clone();
        self.set_status("opening in browser…", StatusKind::Info);
        let tx = self.tx.clone();
        thread::spawn(move || {
            let result = opener(&target);
            let _ = tx.send(Msg::BrowserDone(
                result.err().map(|err| err.to_string()),
            ));
        });
    }

    fn open_theme(&mut self) {
        if self.theme_open
            || self.sort_open
            || self.help_open
            || self.confirm_delete
            || self.online_open
            || self.mode != Mode::Browse
            || self.searching
            || self.saving
            || self.deleting
        {
            return;
        }
        self.themes = catalog();
        if self.themes.is_empty() {
            self.set_status("no themes available", StatusKind::Err);
            return;
        }
        self.theme_idx = self
            .themes
            .iter()
            .position(|theme| theme.id == self.theme_name)
            .unwrap_or(0);
        self.theme_before =
            Some((self.theme_name.clone(), self.styles.clone()));
        self.theme_open = true;
        self.status.clear();
    }

    fn preview_theme(&mut self) {
        let Some(choice) = self.themes.get(self.theme_idx) else {
            return;
        };
        match load_theme(&choice.id) {
            Ok(theme) => {
                self.styles = Styles::new(theme);
                self.theme_name = choice.id.clone();
            }
            Err(err) => {
                self.set_status(&format!("theme: {err}"), StatusKind::Err)
            }
        }
    }

    fn apply_theme_choice(&mut self) {
        self.theme_open = false;
        self.theme_before = None;
        let label = self
            .themes
            .get(self.theme_idx)
            .map(|choice| choice.id.clone())
            .unwrap_or_else(|| self.theme_name.clone());
        self.set_status(&format!("theme: {label}"), StatusKind::Info);
    }

    fn cancel_theme_choice(&mut self) {
        if let Some((name, styles)) = self.theme_before.take() {
            self.theme_name = name;
            self.styles = styles;
        }
        self.theme_open = false;
        self.status.clear();
    }

    fn update_theme(&mut self, key: KeyEvent) {
        let page = self.theme_page_size().max(1) as isize;
        let last = self.themes.len().saturating_sub(1);
        match key_name(&key).as_str() {
            "esc" | "q" | "ctrl+c" => {
                self.cancel_theme_choice();
                return;
            }
            "enter" => {
                self.apply_theme_choice();
                return;
            }
            "up" | "k" => {
                self.theme_idx = clamp(
                    (self.theme_idx as isize - 1).max(0) as usize,
                    0,
                    last,
                )
            }
            "down" | "j" => {
                self.theme_idx = clamp((self.theme_idx + 1).min(last), 0, last)
            }
            "pgup" | "ctrl+u" => {
                self.theme_idx = clamp(
                    (self.theme_idx as isize - page).max(0) as usize,
                    0,
                    last,
                )
            }
            "pgdown" | "ctrl+d" => {
                self.theme_idx =
                    clamp((self.theme_idx as isize + page) as usize, 0, last)
            }
            "home" | "g" => self.theme_idx = 0,
            "end" | "G" => self.theme_idx = last,
            _ => return,
        }
        self.preview_theme();
    }

    // ---------- shelves ----------

    /// The configured shelf whose query equals the current filter.
    pub(crate) fn active_shelf_idx(&self) -> Option<usize> {
        self.shelves.iter().position(|shelf| {
            !shelf.query.is_empty()
                && shelf.query.trim().eq_ignore_ascii_case(self.filter.trim())
        })
    }

    /// The shelf indices the open picker shows. The add picker lists only
    /// shelves whose query is one tag clause, since those are the only ones it
    /// can write.
    pub(crate) fn shelf_rows(&self) -> Vec<usize> {
        if self.shelf_add {
            (0..self.shelves.len())
                .filter(|&i| {
                    shelves::membership_tag(&self.shelves[i].query).is_some()
                })
                .collect()
        } else {
            (0..self.shelves.len()).collect()
        }
    }

    fn select_shelf(&mut self, idx: usize) {
        let Some(shelf) = self.shelves.get(idx).cloned() else {
            return;
        };
        self.read_filter = ReadFilter::All;
        self.search.set_value(&shelf.query);
        self.set_filter(&shelf.query);
    }

    fn refresh_shelf_counts(&mut self) {
        self.shelf_counts = self
            .shelves
            .iter()
            .map(|shelf| shelves::count(&self.entries, &shelf.query))
            .collect();
    }

    fn open_shelf(&mut self, add: bool) {
        if self.shelf_open
            || self.theme_open
            || self.sort_open
            || self.help_open
            || self.confirm_delete
            || self.online_open
            || self.mode != Mode::Browse
            || self.searching
            || self.saving
            || self.deleting
        {
            return;
        }
        if add {
            let writable = self
                .shelves
                .iter()
                .any(|shelf| shelves::membership_tag(&shelf.query).is_some());
            if !writable {
                self.set_status(
                    "no tag shelves configured (add e.g. fantasy = \"tag:fantasy\")",
                    StatusKind::Err,
                );
                return;
            }
            if self.delete_targets().is_empty() {
                self.set_status("no books selected", StatusKind::Err);
                return;
            }
            self.shelf_idx = 0;
        } else {
            self.shelf_idx = self.active_shelf_idx().unwrap_or(0);
        }
        self.shelf_add = add;
        self.shelf_prompt = false;
        self.shelf_edit = None;
        self.refresh_shelf_counts();
        self.shelf_open = true;
        self.status.clear();
    }

    /// Rows the picker can show: the body minus the borders, the title and
    /// the key hints.
    pub(crate) fn shelf_page_size(&self) -> usize {
        let height = self.body_height();
        if height < 5 {
            return 0;
        }
        height - 4
    }

    fn update_shelf(&mut self, key: KeyEvent) {
        if self.shelf_prompt {
            self.update_shelf_form(key);
            return;
        }
        let page = self.shelf_page_size().max(1) as isize;
        let rows = self.shelf_rows();
        let last = rows.len().saturating_sub(1);
        match key_name(&key).as_str() {
            "esc" | "q" | "ctrl+c" => self.shelf_open = false,
            "n" => self.start_shelf_form(None),
            "e" => self.edit_shelf_row(),
            "enter" => {
                self.shelf_open = false;
                if let Some(&target) = rows.get(self.shelf_idx) {
                    if self.shelf_add {
                        self.add_to_shelf(target);
                    } else {
                        self.select_shelf(target);
                    }
                }
            }
            "up" | "k" => {
                self.shelf_idx = clamp(
                    (self.shelf_idx as isize - 1).max(0) as usize,
                    0,
                    last,
                )
            }
            "down" | "j" => {
                self.shelf_idx =
                    clamp((self.shelf_idx as isize + 1) as usize, 0, last)
            }
            "pgup" | "ctrl+u" => {
                self.shelf_idx = clamp(
                    (self.shelf_idx as isize - page).max(0) as usize,
                    0,
                    last,
                )
            }
            "pgdown" | "ctrl+d" => {
                self.shelf_idx =
                    clamp((self.shelf_idx as isize + page) as usize, 0, last)
            }
            "home" | "g" => self.shelf_idx = 0,
            "end" | "G" => self.shelf_idx = last,
            _ => {}
        }
    }

    /// Start the name-and-query form, for a new shelf or for `edit`.
    fn start_shelf_form(&mut self, edit: Option<usize>) {
        let (name, query) = match edit {
            Some(idx) => {
                let Some(shelf) = self.shelves.get(idx) else {
                    return;
                };
                (shelf.name.clone(), shelf.query.clone())
            }
            None => {
                let query = self.filter.trim().to_owned();
                if query.is_empty() {
                    self.set_status(
                        "filter the books first: a new shelf saves the current filter",
                        StatusKind::Err,
                    );
                    return;
                }
                (String::new(), query)
            }
        };
        self.shelf_name.set_value(&name);
        self.shelf_query.set_value(&query);
        self.shelf_focus = ShelfField::Name;
        self.shelf_edit = edit;
        self.shelf_prompt = true;
        self.status.clear();
    }

    /// Open the form on the highlighted shelf of the picker.
    fn edit_shelf_row(&mut self) {
        let rows = self.shelf_rows();
        let Some(&idx) = rows.get(self.shelf_idx) else {
            return;
        };
        self.start_shelf_form(Some(idx));
    }

    fn update_shelf_form(&mut self, key: KeyEvent) {
        match key_name(&key).as_str() {
            "esc" | "ctrl+c" => {
                self.shelf_prompt = false;
                self.shelf_edit = None;
                self.status.clear();
            }
            "tab" | "shift+tab" => {
                self.shelf_focus = match self.shelf_focus {
                    ShelfField::Name => ShelfField::Query,
                    ShelfField::Query => ShelfField::Name,
                };
            }
            "enter" => self.save_shelf_form(),
            _ => match self.shelf_focus {
                ShelfField::Name => {
                    self.shelf_name.handle_key(key);
                }
                ShelfField::Query => {
                    self.shelf_query.handle_key(key);
                }
            },
        }
    }

    /// Write the shelf to the config file, then update the picker.
    fn save_shelf_form(&mut self) {
        let name = self.shelf_name.value().trim().to_owned();
        if name.is_empty() {
            self.set_status("the shelf needs a name", StatusKind::Err);
            return;
        }
        let editing = self.shelf_edit;
        let clash = self.shelves.iter().enumerate().any(|(idx, shelf)| {
            Some(idx) != editing && shelf.name.eq_ignore_ascii_case(&name)
        });
        if clash {
            self.set_status(
                &format!("a shelf named {name:?} already exists"),
                StatusKind::Err,
            );
            return;
        }
        let query = self.shelf_query.value().trim().to_owned();
        let Some(path) = self.options.config_path.clone() else {
            self.set_status("cannot find the config file", StatusKind::Err);
            return;
        };
        let result = match editing.and_then(|idx| self.shelves.get(idx)) {
            Some(shelf) => shelves::edit(&path, &shelf.name, &name, &query),
            None => shelves::save(&path, &name, &query),
        };
        if let Err(err) = result {
            self.set_status(&format!("shelf: {err}"), StatusKind::Err);
            return;
        }
        match editing {
            Some(idx) if idx < self.shelves.len() => {
                self.shelves[idx] = Shelf {
                    name: name.clone(),
                    query,
                };
                self.reorder_shelves();
                self.set_status(
                    &format!("updated shelf {name:?}"),
                    StatusKind::Ok,
                );
            }
            _ => {
                self.push_shelf(Shelf {
                    name: name.clone(),
                    query,
                });
                self.set_status(
                    &format!("saved shelf {name:?}"),
                    StatusKind::Ok,
                );
            }
        }
        self.shelf_prompt = false;
        self.shelf_edit = None;
        self.shelf_open = false;
    }

    /// Sort the shelves by name and count their books again.
    fn reorder_shelves(&mut self) {
        self.shelves
            .sort_by(|left, right| left.name.cmp(&right.name));
        self.refresh_shelf_counts();
    }

    /// Add a shelf in name order.
    fn push_shelf(&mut self, shelf: Shelf) {
        self.shelves.push(shelf);
        self.reorder_shelves();
    }

    fn add_to_shelf(&mut self, idx: usize) {
        let Some(shelf) = self.shelves.get(idx).cloned() else {
            return;
        };
        let Some(tag) = shelves::membership_tag(&shelf.query) else {
            self.set_status(
                &format!("{:?} is not a tag shelf", shelf.name),
                StatusKind::Err,
            );
            return;
        };
        let pending: Vec<PathBuf> = self
            .delete_targets()
            .into_iter()
            .filter(|entry| entry.error.is_none())
            .filter(|entry| {
                !entry
                    .metadata
                    .subjects
                    .iter()
                    .any(|subject| subject.eq_ignore_ascii_case(&tag))
            })
            .map(|entry| entry.path)
            .collect();
        if pending.is_empty() {
            self.set_status(
                &format!("already on {:?}", shelf.name),
                StatusKind::Info,
            );
            return;
        }
        self.start_bulk_paths(pending, Field::Tags, tag, true);
    }

    fn apply_sort_setting(&mut self, value: &str) -> Result<()> {
        if value.trim().is_empty() {
            return Ok(());
        }
        let keys = parse_sort_spec(value)
            .map_err(|err| anyhow!("config: sort: {err}"))?;
        self.sort_keys = keys;
        self.apply_filter();
        self.list_idx =
            clamp(self.list_idx, 0, self.filtered.len().saturating_sub(1));
        self.scroll_list();
        self.rebuild_rows();
        Ok(())
    }

    fn open_sort(&mut self) {
        if self.sort_open
            || self.theme_open
            || self.help_open
            || self.confirm_delete
            || self.mode != Mode::Browse
            || self.searching
            || self.saving
            || self.deleting
        {
            return;
        }
        self.sort_open = true;
        self.sort_idx = self.current_sort_index().unwrap_or(0);
        self.status.clear();
    }

    fn apply_sort(&mut self) {
        if self.sort_idx >= SORT_MODES.len() {
            self.sort_open = false;
            return;
        }
        let mode = SORT_MODES[self.sort_idx];
        let focus = self.selected().map(|entry| entry.path);
        self.sort_open = false;
        self.sort_keys = vec![mode];
        self.apply_filter();
        self.focus_path(focus.as_deref());
        self.rebuild_rows();
        self.set_status(
            &format!("sorted by {}", mode.label()),
            StatusKind::Info,
        );
    }

    fn open_help(&mut self) {
        self.help_open = true;
        self.help_top = 0;
        self.status.clear();
        self.close_suggestions();
    }

    fn scroll_help(&mut self, delta: isize) {
        self.help_top = clamp(
            (self.help_top as isize + delta).max(0) as usize,
            0,
            self.help_max_top(),
        );
    }

    fn handle_delete_files(&mut self) {
        if self.confirm_delete
            || self.mode != Mode::Browse
            || self.searching
            || self.saving
            || self.deleting
            || self.loading
        {
            return;
        }
        let targets = self.delete_targets();
        if targets.is_empty() {
            return;
        }
        self.delete_paths =
            targets.into_iter().map(|entry| entry.path).collect();
        self.confirm_delete = true;
        self.status.clear();
    }

    fn confirm_delete_now(&mut self) {
        let paths = std::mem::take(&mut self.delete_paths);
        self.confirm_delete = false;
        if paths.is_empty() {
            return;
        }
        self.delete_files(paths);
    }

    // ---------- input ----------

    pub fn handle_key(&mut self, key: KeyEvent) {
        if self.mode == Mode::Edit {
            self.update_edit(key);
            return;
        }
        self.update_browse(key);
    }

    fn update_browse(&mut self, key: KeyEvent) {
        if self.searching {
            self.update_search(key);
            return;
        }
        if self.help_open {
            self.update_help(key);
            return;
        }
        if self.confirm_delete {
            self.update_confirm_delete(key);
            return;
        }
        if self.sort_open {
            self.update_sort(key);
            return;
        }
        if self.theme_open {
            self.update_theme(key);
            return;
        }
        if self.shelf_open {
            self.update_shelf(key);
            return;
        }
        if self.online_open {
            self.update_online(key);
            return;
        }
        if self.saving || self.deleting || self.repairing || self.looking_up {
            let name = key_name(&key);
            if name == "q" || name == "ctrl+c" {
                self.quit = true;
            }
            return;
        }
        self.status.clear();
        match key_name(&key).as_str() {
            "q" | "ctrl+c" => {
                self.quit = true;
                return;
            }
            "r" => {
                self.repair_selected();
                return;
            }
            "m" => {
                self.lookup_selected();
                return;
            }
            "x" => {
                self.toggle_read();
                return;
            }
            "N" => {
                self.rename_selected();
                return;
            }
            "M" => {
                self.move_selected_to_library();
                return;
            }
            "D" => {
                self.handle_delete_files();
                return;
            }
            "S" => {
                self.open_sort();
                return;
            }
            "s" => {
                self.open_shelf(false);
                return;
            }
            "A" => {
                self.open_shelf(true);
                return;
            }
            "?" => {
                self.open_help();
                return;
            }
            "ctrl+t" => {
                self.open_theme();
                return;
            }
            "ctrl+o" => {
                self.open_viewer();
                return;
            }
            "ctrl+k" => {
                self.open_search_url();
                return;
            }
            "u" => {
                self.toggle_read_filter(ReadFilter::Unread);
                return;
            }
            "U" => {
                self.toggle_read_filter(ReadFilter::Read);
                return;
            }
            "/" => {
                self.start_search();
                return;
            }
            "ctrl+a" => {
                self.select_all_visible();
                self.rebuild_rows();
                return;
            }
            "R" => {
                if self.loading {
                    return;
                }
                self.start_scan();
                return;
            }
            "tab" | "shift+tab" => {
                self.pane = if self.pane == Pane::List {
                    Pane::Detail
                } else {
                    Pane::List
                };
                self.close_suggestions();
                return;
            }
            _ => {}
        }

        if self.pane == Pane::List {
            match key_name(&key).as_str() {
                "up" | "k" => self.move_list(-1),
                "down" | "j" => self.move_list(1),
                "K" => self.move_list_group(-1),
                "J" => self.move_list_group(1),
                "pgup" | "ctrl+u" => {
                    let page = self.list_page_size() as isize;
                    self.move_list(-page);
                }
                "pgdown" | "ctrl+d" => {
                    let page = self.list_page_size() as isize;
                    self.move_list(page);
                }
                "home" | "g" => {
                    let n = self.filtered.len() as isize;
                    self.move_list(-n);
                }
                "end" | "G" => {
                    let n = self.filtered.len() as isize;
                    self.move_list(n);
                }
                " " => {
                    self.toggle_select();
                    self.rebuild_rows();
                    self.move_list(1);
                }
                "esc" => {
                    if !self.sel.is_empty() {
                        self.sel.clear();
                        self.rebuild_rows();
                    } else if !self.filter.is_empty()
                        || self.read_filter != ReadFilter::All
                    {
                        self.clear_filter();
                    }
                }
                "right" | "l" | "enter" | "e" => self.pane = Pane::Detail,
                _ => {}
            }
            return;
        }

        match key_name(&key).as_str() {
            "up" | "k" => self.move_row(-1),
            "down" | "j" => self.move_row(1),
            "pgup" | "ctrl+u" => {
                let page = self.row_page_size() as isize;
                self.move_row(-page);
            }
            "pgdown" | "ctrl+d" => {
                let page = self.row_page_size() as isize;
                self.move_row(page);
            }
            "home" | "g" => {
                let n = self.rows.len() as isize;
                self.move_row(-n);
            }
            "end" | "G" => {
                let n = self.rows.len() as isize;
                self.move_row(n);
            }
            "left" | "h" | "esc" => self.pane = Pane::List,
            "enter" | "e" => self.start_edit(false),
            " " => {
                if self
                    .current_row()
                    .is_some_and(|row| row.field == Field::Read)
                {
                    self.toggle_read();
                }
            }
            "a" | "n" => self.start_edit(true),
            "d" | "delete" | "backspace" => self.delete_row(),
            _ => {}
        }
    }

    fn start_search(&mut self) {
        self.pane = Pane::List;
        self.searching = true;
    }

    fn update_search(&mut self, key: KeyEvent) {
        match key_name(&key).as_str() {
            "esc" | "ctrl+c" => {
                self.searching = false;
                self.clear_filter();
            }
            "enter" => {
                self.searching = false;
            }
            _ => {
                if self.search.handle_key(key) {
                    let value = self.search.value();
                    self.set_filter(&value);
                }
            }
        }
    }

    fn update_help(&mut self, key: KeyEvent) {
        match key_name(&key).as_str() {
            "?" | "esc" | "q" => self.help_open = false,
            "ctrl+c" => self.quit = true,
            "up" | "k" => self.scroll_help(-1),
            "down" | "j" => self.scroll_help(1),
            "pgup" | "ctrl+u" => {
                let page = self.help_page_size().max(1) as isize;
                self.scroll_help(-page);
            }
            "pgdown" | "ctrl+d" => {
                let page = self.help_page_size().max(1) as isize;
                self.scroll_help(page);
            }
            "home" | "g" => self.help_top = 0,
            "end" | "G" => self.help_top = self.help_max_top(),
            _ => {}
        }
    }

    fn update_confirm_delete(&mut self, key: KeyEvent) {
        match key_name(&key).as_str() {
            "y" | "Y" | "enter" => self.confirm_delete_now(),
            "n" | "N" | "esc" | "q" | "ctrl+c" => {
                self.confirm_delete = false;
                self.delete_paths.clear();
            }
            _ => {}
        }
    }

    fn update_sort(&mut self, key: KeyEvent) {
        let page = self.sort_page_size().max(1) as isize;
        match key_name(&key).as_str() {
            "esc" | "q" | "ctrl+c" => self.sort_open = false,
            "enter" => self.apply_sort(),
            "up" | "k" => {
                self.sort_idx = clamp(
                    (self.sort_idx as isize - 1).max(0) as usize,
                    0,
                    SORT_MODES.len() - 1,
                )
            }
            "down" | "j" => {
                self.sort_idx = clamp(
                    (self.sort_idx as isize + 1) as usize,
                    0,
                    SORT_MODES.len() - 1,
                )
            }
            "pgup" | "ctrl+u" => {
                self.sort_idx = clamp(
                    (self.sort_idx as isize - page).max(0) as usize,
                    0,
                    SORT_MODES.len() - 1,
                )
            }
            "pgdown" | "ctrl+d" => {
                self.sort_idx = clamp(
                    (self.sort_idx as isize + page) as usize,
                    0,
                    SORT_MODES.len() - 1,
                )
            }
            "home" | "g" => self.sort_idx = 0,
            "end" | "G" => self.sort_idx = SORT_MODES.len() - 1,
            _ => {}
        }
    }

    fn update_online(&mut self, key: KeyEvent) {
        let page = self.online_list_height().max(1) as isize;
        let last = self.online_fields.len().saturating_sub(1);
        match key_name(&key).as_str() {
            "esc" | "q" | "ctrl+c" => {
                self.online_open = false;
                self.online_fields.clear();
            }
            "enter" => self.apply_online(),
            "up" | "k" => {
                self.online_idx = clamp(
                    (self.online_idx as isize - 1).max(0) as usize,
                    0,
                    last,
                )
            }
            "down" | "j" => {
                self.online_idx =
                    clamp((self.online_idx as isize + 1) as usize, 0, last)
            }
            "pgup" | "ctrl+u" => {
                self.online_idx = clamp(
                    (self.online_idx as isize - page).max(0) as usize,
                    0,
                    last,
                )
            }
            "pgdown" | "ctrl+d" => {
                self.online_idx =
                    clamp((self.online_idx as isize + page) as usize, 0, last)
            }
            "home" | "g" => self.online_idx = 0,
            "end" | "G" => self.online_idx = last,
            " " => {
                if let Some(field) = self.online_fields.get_mut(self.online_idx)
                {
                    field.selected = !field.selected;
                }
            }
            "a" => {
                for field in &mut self.online_fields {
                    field.selected = true;
                }
            }
            "n" => {
                for field in &mut self.online_fields {
                    field.selected = false;
                }
            }
            _ => {}
        }
    }

    fn update_edit(&mut self, key: KeyEvent) {
        match key_name(&key).as_str() {
            "esc" | "ctrl+c" => {
                self.cancel_edit();
                return;
            }
            "ctrl+s" | "alt+enter" => {
                self.commit_edit();
                return;
            }
            "enter" if !self.using_area => {
                self.commit_edit();
                return;
            }
            "tab" if self.show_suggestions => {
                self.accept_suggestion();
                return;
            }
            "down" | "ctrl+n" if self.show_suggestions => {
                self.move_suggestion(1);
                return;
            }
            "up" | "ctrl+p" if self.show_suggestions => {
                self.move_suggestion(-1);
                return;
            }
            _ => {}
        }

        if self.using_area {
            self.area.handle_key(key);
        } else if self.input.handle_key(key) {
            self.refresh_suggestions();
        }
    }

    fn toggle_select(&mut self) {
        let Some(&idx) = self.filtered.get(self.list_idx) else {
            return;
        };
        if !self.sel.remove(&idx) {
            self.sel.insert(idx);
        }
    }

    fn select_all_visible(&mut self) {
        self.sel = self.filtered.iter().copied().collect();
    }

    // ---------- messages ----------

    pub fn handle_msg(&mut self, msg: Msg) {
        match msg {
            Msg::Scan(paths) => {
                self.entries.clear();
                self.sel.clear();
                self.load_total = paths.len();
                self.load_index = 0;
                self.loading = true;
                self.apply_filter();
                self.build_candidates();
                self.rebuild_rows();
            }
            Msg::ScanError(err) => {
                self.loading = false;
                self.fatal = Some(err);
            }
            Msg::Loaded(entry) => {
                if !self.loading {
                    return;
                }
                self.load_index += 1;
                self.load_path = Some(entry.path.clone());
                if self.load_index > self.entries.len() {
                    self.entries.push(entry);
                    self.load_dirty = true;
                }
            }
            Msg::LoadDone => {
                self.loading = false;
                self.load_path = None;
                self.load_total = self.entries.len();
                self.load_index = self.entries.len();
            }
            Msg::Pages { path, count } => {
                self.pages.insert(path.clone(), count);
                self.pages_pending.remove(&path);
                if self
                    .sort_keys
                    .iter()
                    .any(|key| key.field == SortField::Pages)
                {
                    self.pages_dirty = true;
                }
                if self.selected().is_some_and(|entry| entry.path == path) {
                    self.rebuild_rows();
                }
            }
            Msg::Saved { path, meta, error } => {
                if let Some(err) = error {
                    self.set_status(&err, StatusKind::Err);
                    return;
                }
                if let Some(entry) =
                    self.entries.iter_mut().find(|e| e.path == path)
                {
                    entry.metadata = meta;
                    entry.error = None;
                }
                self.apply_filter();
                self.focus_path(Some(&path));
                self.build_candidates();
                self.rebuild_rows();
                self.refresh_suggestions();
                self.set_status("saved", StatusKind::Ok);
            }
            Msg::BulkProgress {
                path,
                meta,
                index,
                total,
            } => {
                if let Some(meta) = meta
                    && let Some(entry) =
                        self.entries.iter_mut().find(|e| e.path == path)
                {
                    entry.metadata = meta;
                    entry.error = None;
                }
                self.set_status(
                    &format!("saving {index}/{total}"),
                    StatusKind::Info,
                );
            }
            Msg::BulkDone { ok, total, errs } => {
                self.saving = false;
                self.apply_filter();
                self.list_idx = clamp(
                    self.list_idx,
                    0,
                    self.filtered.len().saturating_sub(1),
                );
                self.scroll_list();
                self.build_candidates();
                self.rebuild_rows();
                self.refresh_suggestions();
                if errs.is_empty() {
                    self.set_status(
                        &format!("saved {ok} books"),
                        StatusKind::Ok,
                    );
                } else {
                    self.set_status(
                        &format!(
                            "saved {ok} of {total} books ({} failed)",
                            errs.len()
                        ),
                        StatusKind::Err,
                    );
                }
            }
            Msg::Deleted { removed, errs } => {
                self.deleting = false;
                if !removed.is_empty() {
                    self.remove_entries(&removed);
                }
                let total = removed.len() + errs.len();
                match (total, removed.len(), errs.len()) {
                    (0, _, _) => {
                        self.set_status("nothing to delete", StatusKind::Info)
                    }
                    (_, 0, _) => self.set_status(
                        &format!("could not delete {}", file_count(total)),
                        StatusKind::Err,
                    ),
                    (_, _, e) if e > 0 => self.set_status(
                        &format!(
                            "deleted {} of {}",
                            removed.len(),
                            file_count(total)
                        ),
                        StatusKind::Err,
                    ),
                    _ => self.set_status(
                        &format!("deleted {}", file_count(removed.len())),
                        StatusKind::Ok,
                    ),
                }
            }
            Msg::ViewerDone(err) | Msg::BrowserDone(err) => {
                if let Some(err) = err {
                    self.set_status(&err, StatusKind::Err);
                } else {
                    self.status.clear();
                }
            }
            Msg::Repaired {
                path,
                changed,
                meta,
                load_error,
                error,
            } => {
                self.repairing = false;
                if let Some(err) = error {
                    self.set_status(&format!("repair: {err}"), StatusKind::Err);
                    return;
                }
                if let Some(entry) =
                    self.entries.iter_mut().find(|e| e.path == path)
                {
                    entry.metadata = meta;
                    entry.error = load_error.clone();
                }
                self.apply_filter();
                self.focus_path(Some(&path));
                self.build_candidates();
                self.rebuild_rows();
                self.refresh_suggestions();
                if load_error.is_some() {
                    self.set_status(
                        "still unreadable after repair",
                        StatusKind::Err,
                    );
                } else if changed {
                    self.set_status("repaired", StatusKind::Ok);
                } else {
                    self.set_status("metadata reloaded", StatusKind::Ok);
                }
            }
            Msg::OnlineResult {
                path,
                current,
                found,
                error,
            } => {
                self.looking_up = false;
                if let Some(err) = error {
                    self.set_status(&format!("online: {err}"), StatusKind::Err);
                    return;
                }
                let fields = compare_online_metadata(&current, &found);
                if fields.is_empty() {
                    self.set_status(
                        "online metadata matches this book",
                        StatusKind::Ok,
                    );
                    return;
                }
                self.online_open = true;
                self.online_path = Some(path);
                self.online_found = found;
                self.online_fields = fields;
                self.online_idx = 0;
                self.status.clear();
            }
        }
    }

    /// Run pending external commands after a key press. Returns `true` when the
    /// terminal must be suspended around a viewer run.
    pub fn take_viewer(&mut self) -> Option<Command> {
        self.pending_viewer.take()
    }
}

fn default_open_browser(target: &str) -> Result<()> {
    let mut command = Command::new("xdg-open");
    command.arg(target);
    detach_stdio(&mut command);
    let status = command
        .status()
        .map_err(|err| anyhow!("browser: {err}"))?;
    if status.success() {
        Ok(())
    } else {
        bail!("browser: xdg-open exited with {status}")
    }
}

/// Keep a launched GUI program off the TUI's terminal. Its diagnostics would
/// otherwise scribble over the alternate screen, and it has no use for the
/// keyboard.
fn detach_stdio(command: &mut Command) {
    command
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null());
}

/// The URL opener: the configured `browser` command when set, otherwise
/// `xdg-open`.
fn browser_opener(configured: &str) -> BrowserFn {
    if configured.trim().is_empty() {
        return std::sync::Arc::new(default_open_browser);
    }
    let command = configured.to_owned();
    std::sync::Arc::new(move |target| run_browser(&command, target))
}

pub(super) fn run_browser(command: &str, target: &str) -> Result<()> {
    let mut command = browser_command(command, target)?;
    detach_stdio(&mut command);
    let mut child = command
        .spawn()
        .map_err(|err| anyhow!("browser: {err}"))?;
    // Do not wait for the browser: it may become the primary instance and run
    // until the user closes it. Reap the launcher in the background so a
    // short-lived forwarder does not linger as a zombie.
    thread::spawn(move || {
        let _ = child.wait();
    });
    Ok(())
}

/// Build the configured browser command with `target` appended last.
pub(super) fn browser_command(command: &str, target: &str) -> Result<Command> {
    let args = shlex::split(command)
        .ok_or_else(|| anyhow!("browser: parse browser: unmatched quote"))?;
    let Some((program, rest)) = args.split_first() else {
        bail!("browser: browser is empty");
    };
    let mut command = Command::new(program);
    command.args(rest).arg(target);
    Ok(command)
}

/// Compare the online result with the current metadata.
pub(crate) fn compare_online_metadata(
    current: &Metadata,
    found: &Metadata,
) -> Vec<OnlineField> {
    let mut fields = Vec::new();
    for field in ONLINE_FIELDS_ORDER {
        let new_value = found.get(field).trim().to_owned();
        if new_value.is_empty() {
            continue;
        }
        let old_value = current.get(field).trim().to_owned();
        if old_value == new_value {
            continue;
        }
        fields.push(OnlineField {
            field,
            current: old_value,
            found: new_value,
            selected: true,
        });
    }
    fields
}

/// Build the detail pane rows: plain fields, one row per author and tag, each
/// group followed by an "add" row.
pub(crate) fn build_rows(
    meta: &Metadata,
    pages: Option<&epub::PageCount>,
) -> Vec<Row> {
    let mut rows = vec![
        Row {
            kind: RowKind::Field,
            field: Field::Title,
            label: Field::Title.label().into(),
            value: meta.title.clone(),
            ..Row::default()
        },
        Row {
            kind: RowKind::Field,
            field: Field::Subtitle,
            label: Field::Subtitle.label().into(),
            value: meta.subtitle.clone(),
            ..Row::default()
        },
    ];

    rows.push(Row {
        kind: RowKind::Field,
        field: Field::Author,
        label: Field::Author.label().into(),
        value: meta.creator_names().join("; "),
        ..Row::default()
    });
    for (i, creator) in meta.creators.iter().enumerate() {
        rows.push(Row {
            kind: RowKind::Creator,
            field: Field::Author,
            index: i,
            label: creator.display().to_owned(),
            ..Row::default()
        });
    }
    rows.push(Row {
        kind: RowKind::AddCreator,
        field: Field::Author,
        index: meta.creators.len(),
        label: "author".into(),
        ..Row::default()
    });

    rows.push(Row {
        kind: RowKind::Field,
        field: Field::Tags,
        label: Field::Tags.label().into(),
        value: meta.subjects.join(", "),
        ..Row::default()
    });
    for (i, subject) in meta.subjects.iter().enumerate() {
        rows.push(Row {
            kind: RowKind::Subject,
            field: Field::Tags,
            index: i,
            label: subject.clone(),
            ..Row::default()
        });
    }
    rows.push(Row {
        kind: RowKind::AddSubject,
        field: Field::Tags,
        index: meta.subjects.len(),
        label: "tag".into(),
        ..Row::default()
    });

    rows.push(Row {
        kind: RowKind::Field,
        field: Field::Series,
        label: Field::Series.label().into(),
        value: meta.series.clone(),
        ..Row::default()
    });
    rows.push(Row {
        kind: RowKind::Field,
        field: Field::SeriesIndex,
        label: Field::SeriesIndex.label().into(),
        value: meta.series_index.clone(),
        ..Row::default()
    });

    for field in [
        Field::Publisher,
        Field::Date,
        Field::Language,
        Field::Identifier,
        Field::Rights,
        Field::Read,
        Field::Description,
    ] {
        rows.push(Row {
            kind: RowKind::Field,
            field,
            label: field.label().into(),
            value: meta.get(field),
            ..Row::default()
        });
    }
    if let Some(pages) = pages {
        rows.push(Row {
            kind: RowKind::Info,
            label: "Pages".into(),
            value: pages.text(),
            ..Row::default()
        });
    }
    rows
}

/// Lay out the bulk editor: one aggregated row per bulk field.
fn build_bulk_rows(selected: &[Entry]) -> Vec<Row> {
    let mut rows = Vec::with_capacity(BULK_FIELDS.len());
    for field in BULK_FIELDS {
        let mut row = Row {
            kind: RowKind::Field,
            field,
            label: field.label().into(),
            ..Row::default()
        };
        if let Some(first) = selected.first() {
            let value = first.metadata.get(field);
            if selected
                .iter()
                .all(|entry| entry.metadata.get(field) == value)
            {
                row.value = value;
            } else {
                row.mixed = true;
            }
        }
        rows.push(row);
    }
    rows
}

/// Whether two books share both their series and author, so group navigation
/// skips from one run of them to the next.
fn same_book_group(a: &Entry, b: &Entry) -> bool {
    a.metadata
        .series
        .trim()
        .eq_ignore_ascii_case(b.metadata.series.trim())
        && a.metadata
            .author_string()
            .trim()
            .eq_ignore_ascii_case(b.metadata.author_string().trim())
}

/// The number of lines each list entry occupies.
pub(crate) fn entry_heights(items: &[Entry]) -> Vec<usize> {
    items
        .iter()
        .map(|entry| {
            if entry.error.is_none() && !entry.series_line().is_empty() {
                3
            } else {
                2
            }
        })
        .collect()
}

/// Return the first visible entry so that the selected entry is visible within
/// `height` lines.
pub(crate) fn adjust_list_top(
    top: usize,
    selected: usize,
    heights: &[usize],
    height: usize,
) -> usize {
    if heights.is_empty() {
        return 0;
    }
    let mut top = clamp(top, 0, heights.len() - 1);
    let selected = clamp(selected, 0, heights.len() - 1);
    if selected < top {
        top = selected;
    }
    let height = height.max(1);
    while top < selected {
        let used: usize = heights[top..=selected].iter().sum();
        if used <= height {
            break;
        }
        top += 1;
    }
    top
}

pub(crate) fn clamp(value: usize, low: usize, high: usize) -> usize {
    value.max(low).min(high)
}

pub(crate) fn file_count(n: usize) -> String {
    if n == 1 {
        "1 file".into()
    } else {
        format!("{n} files")
    }
}

pub(crate) fn book_count(n: usize) -> String {
    if n == 1 {
        "1 book".into()
    } else {
        format!("{n} books")
    }
}

pub(crate) fn file_name(path: &Path) -> String {
    path.file_name()
        .map(|name| name.to_string_lossy().into_owned())
        .unwrap_or_else(|| path.to_string_lossy().into_owned())
}

fn path_within(dir: &Path, path: &Path) -> bool {
    let Ok(dir) = std::path::absolute(dir) else {
        return false;
    };
    let Ok(path) = std::path::absolute(path) else {
        return false;
    };
    match path.strip_prefix(&dir) {
        Ok(rel) => rel.as_os_str().is_empty() || !rel.starts_with(".."),
        Err(_) => false,
    }
}

/// Translate a crossterm key event into the names used by the key map.
pub fn key_name(key: &KeyEvent) -> String {
    let ctrl = key.modifiers.contains(KeyModifiers::CONTROL);
    let alt = key.modifiers.contains(KeyModifiers::ALT);
    let shift = key.modifiers.contains(KeyModifiers::SHIFT);
    match key.code {
        KeyCode::Char(c) => {
            if ctrl {
                format!("ctrl+{}", c.to_ascii_lowercase())
            } else if alt {
                format!("alt+{}", c.to_ascii_lowercase())
            } else {
                c.to_string()
            }
        }
        KeyCode::Tab if shift => "shift+tab".into(),
        KeyCode::BackTab => "shift+tab".into(),
        KeyCode::Tab => "tab".into(),
        KeyCode::Enter if alt => "alt+enter".into(),
        KeyCode::Enter => "enter".into(),
        KeyCode::Esc => "esc".into(),
        KeyCode::Up => "up".into(),
        KeyCode::Down => "down".into(),
        KeyCode::Left => "left".into(),
        KeyCode::Right => "right".into(),
        KeyCode::PageUp => "pgup".into(),
        KeyCode::PageDown => "pgdown".into(),
        KeyCode::Home => "home".into(),
        KeyCode::End => "end".into(),
        KeyCode::Delete => "delete".into(),
        KeyCode::Backspace => "backspace".into(),
        KeyCode::Insert => "insert".into(),
        other => format!("{other:?}").to_lowercase(),
    }
}
