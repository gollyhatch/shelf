//! Browser tests that drive the model and assert the rendered frame.

use std::path::PathBuf;
use std::time::{Duration, Instant};

use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};
use ratatui::Terminal;
use ratatui::backend::TestBackend;
use ratatui::buffer::Buffer;
use ratatui::style::Color;

use super::app::{App, Mode, Pane, RowKind, StatusKind};
use super::{RunOptions, theme};
use crate::epub::{self, Creator, Field, Metadata};
use crate::library::{self, Entry};
use crate::rename::NameRule;

fn key(code: KeyCode) -> KeyEvent {
    KeyEvent::new(code, KeyModifiers::NONE)
}

fn ch(c: char) -> KeyEvent {
    KeyEvent::new(KeyCode::Char(c), KeyModifiers::NONE)
}

fn ctrl(c: char) -> KeyEvent {
    KeyEvent::new(KeyCode::Char(c), KeyModifiers::CONTROL)
}

fn options() -> RunOptions {
    RunOptions {
        theme: "gruvbox-dark".into(),
        viewer: String::new(),
        google_books_api_key: String::new(),
        library: String::new(),
        sort: String::new(),
        search_url: String::new(),
        rename: NameRule::new("", false),
        move_rule: None,
    }
}

fn metadata(
    title: &str,
    author: &str,
    series: &str,
    index: &str,
    read: bool,
) -> Metadata {
    Metadata {
        title: title.into(),
        creators: vec![Creator::new(author)],
        series: series.into(),
        series_index: index.into(),
        subjects: vec!["Science Fiction".into()],
        publisher: "Ace Books".into(),
        language: "en".into(),
        description: "A novel about a planet called Winter.".into(),
        read,
        ..Metadata::default()
    }
}

fn entry(
    name: &str,
    title: &str,
    author: &str,
    series: &str,
    index: &str,
    read: bool,
) -> Entry {
    Entry {
        path: PathBuf::from(name),
        metadata: metadata(title, author, series, index, read),
        error: None,
    }
}

fn entries() -> Vec<Entry> {
    vec![
        entry("dune.epub", "Dune", "Frank Herbert", "Dune", "1", true),
        entry(
            "left-hand.epub",
            "The Left Hand of Darkness",
            "Le Guin, Ursula K.",
            "Hainish",
            "4",
            false,
        ),
        entry(
            "hobbit.epub",
            "The Hobbit",
            "Tolkien, J. R. R.",
            "Middle-earth",
            "1",
            false,
        ),
    ]
}

fn app_with(entries: Vec<Entry>, options: RunOptions) -> App {
    App::new(PathBuf::from("."), Some(entries), options).expect("app")
}

fn wait_until(app: &mut App, cond: impl Fn(&App) -> bool) {
    let deadline = Instant::now() + Duration::from_secs(5);
    while Instant::now() < deadline {
        app.drain_messages();
        if cond(app) {
            return;
        }
        std::thread::sleep(Duration::from_millis(5));
    }
    app.drain_messages();
    assert!(cond(app), "condition was not reached");
}

fn render_buffer(
    app: &mut App,
    width: u16,
    height: u16,
) -> (Buffer, ratatui::layout::Rect) {
    let backend = TestBackend::new(width, height);
    let mut terminal = Terminal::new(backend).unwrap();
    terminal.draw(|frame| app.view(frame)).unwrap();
    let buffer = terminal.backend().buffer().clone();
    let area = *buffer.area();
    (buffer, area)
}

fn render(app: &mut App, width: u16, height: u16) -> String {
    let (buffer, area) = render_buffer(app, width, height);
    let mut out = String::new();
    for y in 0..area.height {
        for x in 0..area.width {
            out.push_str(buffer[(x, y)].symbol());
        }
        out.push('\n');
    }
    out
}

#[test]
fn view_shows_metadata() {
    let mut app = app_with(entries(), options());
    let view = render(&mut app, 120, 30);
    assert!(view.contains("Books · File path A–Z"), "{view}");
    assert!(view.contains("Dune"), "{view}");
    assert!(view.contains("Frank Herbert"), "{view}");
    assert!(view.contains("Title:"), "{view}");
    assert!(
        view.contains("The Left Hand of Darkness") || view.contains("Dune")
    );
    assert!(view.contains("q quit"), "{view}");
    assert!(app.width == 120 && app.height == 30);
}

#[test]
fn view_fits_small_terminals() {
    for width in [16u16, 24, 40, 60, 83, 84, 100, 160] {
        for height in [4u16, 6, 8, 12, 20, 40] {
            let mut app = app_with(entries(), options());
            let view = render(&mut app, width, height);
            assert_eq!(
                view.lines().count(),
                height as usize,
                "{width}x{height}"
            );
            for line in view.lines() {
                assert!(
                    unicode_width::UnicodeWidthStr::width(line)
                        <= width as usize,
                    "{width}x{height}: {line:?}"
                );
            }
        }
    }
}

#[test]
fn theme_canvas_is_painted() {
    let mut app = app_with(entries(), options());
    let base = app.styles.base.bg.expect("canvas background");
    let accent = Color::from(app.styles.theme.color("accent.primary"));
    let selection = Color::from(app.styles.theme.color("bg.selection"));
    // Only the accent chips and the selection fill may differ from the canvas;
    // nothing may fall back to the terminal's own background.
    let allowed = [base, accent, selection];
    let check = |buffer: &Buffer| {
        let area = *buffer.area();
        for y in 0..area.height {
            for x in 0..area.width {
                let bg = buffer[(x, y)].bg;
                assert!(
                    allowed.contains(&bg),
                    "cell ({x}, {y}) has background {bg:?}"
                );
            }
        }
    };

    let (buffer, area) = render_buffer(&mut app, 120, 30);
    check(&buffer);

    // Dialogs keep the canvas instead of raising a fill of their own.
    app.help_open = true;
    let (buffer, _) = render_buffer(&mut app, 120, 30);
    check(&buffer);
    assert_eq!(buffer[(area.width / 2, area.height / 2)].bg, base);

    // So does the inline editor, which redraws its box over the body.
    app.help_open = false;
    app.handle_key(key(KeyCode::Enter));
    app.handle_key(key(KeyCode::Enter));
    assert_eq!(app.mode, Mode::Edit);
    let (buffer, _) = render_buffer(&mut app, 120, 30);
    check(&buffer);
}

#[test]
fn navigation_moves_between_books_and_panes() {
    let mut app = app_with(entries(), options());
    app.handle_key(key(KeyCode::Down));
    assert_eq!(app.selected().unwrap().metadata.title, "The Hobbit");
    app.handle_key(key(KeyCode::Char('j')));
    assert_eq!(
        app.selected().unwrap().metadata.title,
        "The Left Hand of Darkness"
    );
    app.handle_key(key(KeyCode::Tab));
    assert_eq!(app.pane, Pane::Detail);
    assert!(app.row_idx == 0);
    app.handle_key(key(KeyCode::Down));
    assert!(app.row_idx > 0);
    app.handle_key(key(KeyCode::Esc));
    assert_eq!(app.pane, Pane::List);
}

#[test]
fn group_navigation_skips_runs() {
    let mut app = app_with(entries(), options());
    app.handle_key(key(KeyCode::Char('J')));
    assert_eq!(app.selected().unwrap().metadata.title, "The Hobbit");
    app.handle_key(key(KeyCode::Char('K')));
    assert_eq!(app.selected().unwrap().metadata.title, "Dune");
}

#[test]
fn search_filters_and_clears() {
    let mut app = app_with(entries(), options());
    app.handle_key(key(KeyCode::Char('/')));
    assert!(app.searching);
    for c in "author:le guin".chars() {
        app.handle_key(ch(c));
    }
    assert_eq!(app.filtered.len(), 1);
    app.handle_key(key(KeyCode::Enter));
    assert!(!app.searching);
    assert_eq!(app.filtered.len(), 1);
    app.handle_key(key(KeyCode::Esc));
    assert_eq!(app.filtered.len(), 3);
    assert!(app.filter.is_empty());
}

#[test]
fn read_filter_toggles() {
    let mut app = app_with(entries(), options());
    app.handle_key(key(KeyCode::Char('u')));
    assert_eq!(app.filtered.len(), 2);
    app.handle_key(key(KeyCode::Char('u')));
    assert_eq!(app.filtered.len(), 3);
    app.handle_key(key(KeyCode::Char('U')));
    assert_eq!(app.filtered.len(), 1);
}

#[test]
fn space_selects_and_bulk_rows_appear() {
    let mut app = app_with(entries(), options());
    app.handle_key(key(KeyCode::Char(' ')));
    app.handle_key(key(KeyCode::Char(' ')));
    assert_eq!(app.sel.len(), 2);
    assert!(app.bulk_active());
    assert_eq!(app.rows.len(), super::app::BULK_FIELDS.len());
    let view = render(&mut app, 120, 30);
    assert!(view.contains("Bulk · 2 books"), "{view}");
}

#[test]
fn add_author_end_to_end() {
    let (dir, path) = crate::epub::test_support::make_epub();
    let mut app = App::new(
        dir.path().to_path_buf(),
        Some(vec![library::load_one(&path)]),
        options(),
    )
    .unwrap();

    app.handle_key(key(KeyCode::Tab));
    let add = app
        .rows
        .iter()
        .position(|row| row.kind == RowKind::AddCreator)
        .unwrap();
    app.row_idx = add;
    app.handle_key(key(KeyCode::Char('a')));
    assert_eq!(app.mode, Mode::Edit);
    for c in "Jane Doe".chars() {
        app.handle_key(ch(c));
    }
    app.handle_key(key(KeyCode::Enter));
    assert_eq!(app.mode, Mode::Browse);
    wait_until(&mut app, |app| app.status == "saved");

    let meta = epub::read_metadata(&path).unwrap();
    assert_eq!(meta.creator_names(), ["Le Guin, Ursula K.", "Jane Doe"]);
}

#[test]
fn edit_title_writes_file_and_updates_list() {
    let (dir, path) = crate::epub::test_support::make_epub();
    let mut app = App::new(
        dir.path().to_path_buf(),
        Some(vec![library::load_one(&path)]),
        options(),
    )
    .unwrap();
    app.pane = Pane::Detail;
    app.row_idx = 0;
    app.handle_key(key(KeyCode::Enter));
    assert_eq!(app.mode, Mode::Edit);
    app.input.set_value("Renamed");
    app.handle_key(key(KeyCode::Enter));
    wait_until(&mut app, |app| app.status == "saved");
    assert_eq!(app.entries[0].metadata.title, "Renamed");
    assert_eq!(epub::read_metadata(&path).unwrap().title, "Renamed");
}

#[test]
fn escape_cancels_edit() {
    let (dir, path) = crate::epub::test_support::make_epub();
    let mut app = App::new(
        dir.path().to_path_buf(),
        Some(vec![library::load_one(&path)]),
        options(),
    )
    .unwrap();
    app.pane = Pane::Detail;
    app.row_idx = 0;
    app.handle_key(key(KeyCode::Enter));
    for c in "Discarded".chars() {
        app.handle_key(ch(c));
    }
    app.handle_key(key(KeyCode::Esc));
    assert_eq!(app.mode, Mode::Browse);
    assert_eq!(
        epub::read_metadata(&path).unwrap().title,
        "The Left Hand of Darkness"
    );
}

#[test]
fn toggle_read_and_checkbox() {
    let (dir, path) = crate::epub::test_support::make_epub();
    let mut app = App::new(
        dir.path().to_path_buf(),
        Some(vec![library::load_one(&path)]),
        options(),
    )
    .unwrap();
    app.handle_key(key(KeyCode::Char('x')));
    wait_until(&mut app, |app| app.status == "saved");
    assert!(epub::read_metadata(&path).unwrap().read);
    assert!(app.entries[0].metadata.read);

    app.pane = Pane::Detail;
    app.row_idx = app
        .rows
        .iter()
        .position(|row| row.field == Field::Read)
        .unwrap();
    let view = render(&mut app, 100, 24);
    assert!(view.contains("[x]"), "{view}");
    app.handle_key(key(KeyCode::Enter));
    wait_until(&mut app, |app| app.status == "saved");
    assert!(!epub::read_metadata(&path).unwrap().read);
}

#[test]
fn delete_requires_confirmation() {
    let (dir, path) = crate::epub::test_support::make_epub();
    let mut app = App::new(
        dir.path().to_path_buf(),
        Some(vec![library::load_one(&path)]),
        options(),
    )
    .unwrap();
    app.handle_key(key(KeyCode::Char('D')));
    assert!(app.confirm_delete);
    assert!(path.exists());
    app.handle_key(ch('n'));
    assert!(!app.confirm_delete);
    assert!(path.exists());

    app.handle_key(key(KeyCode::Char('D')));
    app.handle_key(ch('y'));
    wait_until(&mut app, |app| !app.deleting);
    assert!(!path.exists());
    assert!(app.entries.is_empty());
}

#[test]
fn help_dialog_opens_and_closes() {
    let mut app = app_with(entries(), options());
    app.handle_key(key(KeyCode::Char('?')));
    assert!(app.help_open);
    let view = render(&mut app, 100, 30);
    assert!(view.contains("Keys"), "{view}");
    app.handle_key(key(KeyCode::Esc));
    assert!(!app.help_open);
}

#[test]
fn sort_selector_applies_order() {
    let mut app = app_with(entries(), options());
    app.handle_key(key(KeyCode::Char('S')));
    assert!(app.sort_open);
    app.handle_key(key(KeyCode::Down));
    app.handle_key(key(KeyCode::Enter));
    assert!(!app.sort_open);
    assert_eq!(app.filtered.len(), 3);
    assert!(app.status.contains("sorted by"));
    let titles: Vec<String> = app
        .visible()
        .iter()
        .map(|entry| entry.metadata.title.clone())
        .collect();
    assert_eq!(titles[0], "The Left Hand of Darkness");
}

#[test]
fn theme_selector_previews_and_applies_opaline_themes() {
    let mut app = app_with(entries(), options());
    assert_eq!(app.theme_name, "gruvbox-dark");
    let original_accent = app.styles.theme.color("accent.primary");

    app.handle_key(ctrl('t'));
    assert!(app.theme_open);
    assert_eq!(app.themes.len(), opaline::builtins::BUILTIN_COUNT);
    assert!(app.themes.iter().any(|theme| theme.id == "gruvbox-dark"));

    let start = app.theme_idx;
    app.handle_key(key(KeyCode::Down));
    assert_ne!(app.theme_idx, start);
    let previewed = app.theme_name.clone();
    assert_ne!(previewed, "gruvbox-dark");
    assert_ne!(app.styles.theme.color("accent.primary"), original_accent);

    app.handle_key(key(KeyCode::Esc));
    assert!(!app.theme_open);
    assert_eq!(app.theme_name, "gruvbox-dark");
    assert_eq!(app.styles.theme.color("accent.primary"), original_accent);

    app.handle_key(ctrl('t'));
    app.handle_key(key(KeyCode::Down));
    let chosen = app.theme_name.clone();
    app.handle_key(key(KeyCode::Enter));
    assert!(!app.theme_open);
    assert_eq!(app.theme_name, chosen);
    assert_eq!(app.status, format!("theme: {chosen}"));
}

#[test]
fn theme_selector_renders() {
    let mut app = app_with(entries(), options());
    app.handle_key(ctrl('t'));
    let view = render(&mut app, 120, 40);
    assert!(view.contains("Theme"), "{view}");
    assert!(view.contains("Gruvbox Dark (gruvbox-dark)"), "{view}");
    app.handle_key(key(KeyCode::Esc));
}

#[test]
fn online_dialog_lists_differences() {
    let mut app = app_with(entries(), options());
    app.lookup = std::sync::Arc::new(|_| {
        Ok(Metadata {
            title: "Dune".into(),
            creators: vec![Creator::new("Frank Herbert")],
            publisher: "Chilton".into(),
            ..Metadata::default()
        })
    });
    app.handle_key(ch('m'));
    wait_until(&mut app, |app| app.online_open);
    assert!(
        app.online_fields
            .iter()
            .any(|f| f.field == Field::Publisher)
    );
    let view = render(&mut app, 120, 30);
    assert!(view.contains("Online metadata"), "{view}");
    assert!(view.contains("Current"), "{view}");
    assert!(view.contains("Found"), "{view}");
    app.handle_key(key(KeyCode::Esc));
    assert!(!app.online_open);
}

#[test]
fn search_url_opens_in_browser() {
    let seen = std::sync::Arc::new(std::sync::Mutex::new(String::new()));
    let recorded = seen.clone();
    let mut options = options();
    options.search_url = "https://example.com/?q=%a+%t".into();
    let mut app = app_with(entries(), options);
    app.open_browser = std::sync::Arc::new(move |target| {
        *recorded.lock().unwrap() = target.to_owned();
        Ok(())
    });
    app.handle_key(ctrl('k'));
    wait_until(&mut app, |_| false_or_status_cleared(&seen));
    let target = seen.lock().unwrap().clone();
    assert_eq!(target, "https://example.com/?q=Frank+Herbert+Dune");
}

fn false_or_status_cleared(seen: &std::sync::Mutex<String>) -> bool {
    !seen.lock().unwrap().is_empty()
}

#[test]
fn viewer_command_is_built_with_path_last() {
    let mut options = options();
    options.viewer = "cat --number".into();
    let mut app = app_with(entries(), options);
    app.handle_key(ctrl('o'));
    let command = app.take_viewer().expect("viewer command");
    assert_eq!(command.get_program(), "cat");
    let args: Vec<String> = command
        .get_args()
        .map(|arg| arg.to_string_lossy().into_owned())
        .collect();
    assert_eq!(args.len(), 2);
    assert_eq!(args[0], "--number");
    assert!(args[1].ends_with("dune.epub"));
}

#[test]
fn scan_streams_books_into_the_list() {
    let dir = tempfile::tempdir().unwrap();
    for i in 0..3 {
        let path = dir.path().join(format!("book{i}.epub"));
        std::fs::write(&path, b"not an epub").unwrap();
    }
    let mut app = App::new(dir.path().to_path_buf(), None, options()).unwrap();
    app.start_scan();
    wait_until(&mut app, |app| !app.loading);
    assert_eq!(app.entries.len(), 3);
    assert!(app.entries.iter().all(|entry| entry.error.is_some()));
    let view = render(&mut app, 100, 24);
    assert!(view.contains("unreadable metadata"), "{view}");
}

#[test]
fn metadata_pane_wraps_long_description() {
    let mut entries = entries();
    entries[0].metadata.description = "word ".repeat(60);
    let mut app = app_with(entries, options());
    app.pane = Pane::Detail;
    app.row_idx = app
        .rows
        .iter()
        .position(|row| row.field == Field::Description)
        .unwrap();
    app.row_top = app.row_idx;
    let view = render(&mut app, 100, 44);
    assert!(view.contains("Description:"), "{view}");
    // The wrapped value occupies more than one line below the label.
    let wrapped_lines = view
        .lines()
        .filter(|line| line.contains("word") && !line.contains("Description:"))
        .count();
    assert!(wrapped_lines >= 2, "description did not wrap: {view}");
}

#[test]
fn editor_renders_and_suggestions_complete_authors() {
    let mut app = app_with(entries(), options());
    app.pane = Pane::Detail;
    app.row_idx = 0;
    app.handle_key(ch('e'));
    assert!(app.mode == Mode::Edit);
    for c in "Le".chars() {
        app.handle_key(ch(c));
    }
    assert!(!app.show_suggestions);
    let view = render(&mut app, 100, 30);
    assert!(view.contains("Edit title"), "{view}");
    // Title has no candidates, so this exercises the editor without a list.
    app.handle_key(key(KeyCode::Esc));

    app.row_idx = app
        .rows
        .iter()
        .position(|row| row.kind == RowKind::AddCreator)
        .unwrap();
    app.handle_key(ch('a'));
    for c in "Le".chars() {
        app.handle_key(ch(c));
    }
    assert!(app.show_suggestions);
    app.handle_key(key(KeyCode::Tab));
    assert_eq!(app.input.value(), "Le Guin, Ursula K.");
}

#[test]
fn status_kinds_render_differently() {
    let mut app = app_with(entries(), options());
    app.set_status("boom", StatusKind::Err);
    let view = render(&mut app, 100, 20);
    assert!(view.contains("✗ boom"), "{view}");
    app.set_status("fine", StatusKind::Ok);
    let view = render(&mut app, 100, 20);
    assert!(view.contains("✓ fine"), "{view}");
}

#[test]
fn theme_validation_names_builtins() {
    let err = theme::load("nope").unwrap_err().to_string();
    assert!(err.contains("gruvbox-dark"), "{err}");
}

#[test]
fn entry_from_paths_is_used_for_run_file() {
    // `run_file` builds the model with one entry and no scan.
    let (dir, path) = crate::epub::test_support::make_epub();
    let app = App::new(
        dir.path().to_path_buf(),
        Some(vec![library::load_one(&path)]),
        options(),
    )
    .unwrap();
    assert!(!app.loading);
    assert_eq!(app.entries.len(), 1);
    assert_eq!(app.filtered.len(), 1);
}

#[test]
fn edit_and_dialogs_fit_small_terminals() {
    for (width, height) in [(30u16, 10u16), (60, 18), (84, 24), (140, 40)] {
        let mut app = app_with(entries(), options());
        app.pane = Pane::Detail;
        app.row_idx = 0;
        app.handle_key(key(KeyCode::Enter));
        let view = render(&mut app, width, height);
        assert_eq!(view.lines().count(), height as usize);
        app.handle_key(key(KeyCode::Esc));

        app.handle_key(key(KeyCode::Char('?')));
        let view = render(&mut app, width, height);
        assert_eq!(view.lines().count(), height as usize);
        app.handle_key(key(KeyCode::Esc));

        app.handle_key(key(KeyCode::Char('S')));
        let view = render(&mut app, width, height);
        assert_eq!(view.lines().count(), height as usize);
        app.handle_key(key(KeyCode::Esc));

        app.handle_key(ctrl('t'));
        let view = render(&mut app, width, height);
        assert_eq!(view.lines().count(), height as usize);
        app.handle_key(key(KeyCode::Esc));

        app.handle_key(key(KeyCode::Char('D')));
        let view = render(&mut app, width, height);
        assert_eq!(view.lines().count(), height as usize);
        app.handle_key(ch('n'));
    }
}

#[test]
fn bulk_edit_writes_every_selected_book() {
    let dir = tempfile::tempdir().unwrap();
    let mut paths = Vec::new();
    for (title, author) in [("One", "Author A"), ("Two", "Author B")] {
        let opf = crate::epub::test_support::FIXTURE_OPF
            .replace("<!-- extra -->", "")
            .replace("The Left Hand of Darkness", title)
            .replace("Le Guin, Ursula K.", author);
        let (source_dir, path) = crate::epub::test_support::write_epub(&opf);
        // Move the book into the shared temp dir so both share App.dir.
        let target = dir.path().join(format!("{title}.epub"));
        std::fs::copy(&path, &target).unwrap();
        drop(source_dir);
        paths.push(target);
    }
    let mut app = App::new(
        dir.path().to_path_buf(),
        Some(library::load(&paths)),
        options(),
    )
    .unwrap();

    app.handle_key(key(KeyCode::Char(' ')));
    app.handle_key(key(KeyCode::Char(' ')));
    assert_eq!(app.sel.len(), 2);
    app.handle_key(key(KeyCode::Tab));
    assert_eq!(app.pane, Pane::Detail);
    let publisher = app
        .rows
        .iter()
        .position(|row| row.field == Field::Publisher)
        .unwrap();
    app.row_idx = publisher;
    app.handle_key(key(KeyCode::Enter));
    assert!(app.edit_bulk);
    app.input.set_value("Orbit");
    app.handle_key(key(KeyCode::Enter));
    wait_until(&mut app, |app| !app.saving && app.status.contains("saved"));
    for path in &paths {
        assert_eq!(epub::read_metadata(path).unwrap().publisher, "Orbit");
    }
    assert_eq!(app.sel.len(), 2);
}

#[test]
fn rename_action_renames_selected_book() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("messy.epub");
    let (source_dir, source) = crate::epub::test_support::make_epub();
    std::fs::copy(&source, &path).unwrap();
    drop(source_dir);

    let mut options = options();
    options.rename = NameRule::new("%a - %t", false);
    let mut app = App::new(
        dir.path().to_path_buf(),
        Some(vec![library::load_one(&path)]),
        options,
    )
    .unwrap();
    app.handle_key(ch('N'));
    let renamed = dir
        .path()
        .join("Le Guin, Ursula K. - The Left Hand of Darkness.epub");
    assert!(renamed.exists());
    assert_eq!(app.entries[0].path, renamed);
}

#[test]
fn repair_key_fixes_an_unreadable_book() {
    let opf = crate::epub::test_support::FIXTURE_OPF.replace(
        "</metadata>",
        r#"<meta name="calibre:series" content="Lacroix"></dc:meta></metadata>"#,
    );
    let (dir, path) = crate::epub::test_support::write_entries(&[
        (
            "META-INF/container.xml",
            crate::epub::test_support::FIXTURE_CONTAINER,
        ),
        ("OEBPS/content.opf", &opf),
    ]);
    let entry = library::load_one(&path);
    assert!(entry.error.is_some(), "fixture must be unreadable");

    let mut app =
        App::new(dir.path().to_path_buf(), Some(vec![entry]), options())
            .unwrap();
    app.pane = Pane::Detail;
    assert!(app.selected_unreadable());
    let view = render(&mut app, 110, 34);
    assert!(view.contains("r repairs the EPUB"), "{view}");

    app.handle_key(ch('r'));
    assert!(app.repairing);
    wait_until(&mut app, |app| !app.repairing);
    assert!(app.entries[0].error.is_none(), "{:?}", app.entries[0].error);
    assert_eq!(app.entries[0].metadata.title, "The Left Hand of Darkness");
    assert_eq!(app.status, "repaired");
}

#[test]
fn repair_key_ignores_readable_books() {
    let (dir, path) = crate::epub::test_support::make_epub();
    let mut app = App::new(
        dir.path().to_path_buf(),
        Some(vec![library::load_one(&path)]),
        options(),
    )
    .unwrap();
    app.handle_key(ch('r'));
    assert!(!app.repairing);
}

#[test]
fn move_to_library_renames_and_removes_the_book() {
    let dir = tempfile::tempdir().unwrap();
    let books = dir.path().join("books");
    let library = dir.path().join("library");
    std::fs::create_dir(&books).unwrap();
    let (source_dir, source) = crate::epub::test_support::make_epub();
    let path = books.join("messy.epub");
    std::fs::copy(&source, &path).unwrap();
    drop(source_dir);

    let mut options = options();
    options.library = library.to_string_lossy().into_owned();
    options.move_rule = Some(NameRule::new("%a - %t", false));
    let mut app =
        App::new(books.clone(), Some(vec![library::load_one(&path)]), options)
            .unwrap();

    app.handle_key(ch('M'));
    let target =
        library.join("Le Guin, Ursula K. - The Left Hand of Darkness.epub");
    assert!(target.exists(), "book was not moved");
    assert!(!path.exists());
    assert!(app.entries.is_empty());
    assert!(app.status.contains("moved 1 book"), "{}", app.status);
}

#[test]
fn move_requires_library_and_format() {
    let mut app = app_with(entries(), options());
    app.handle_key(ch('M'));
    assert_eq!(app.status, "no library configured");

    let mut options = options();
    options.library = "/tmp/library".into();
    let mut app = app_with(entries(), options);
    app.handle_key(ch('M'));
    assert_eq!(app.status, "rename_format is not configured");
}

#[test]
fn composite_sort_setting_orders_by_each_key() {
    let mut options = options();
    options.sort = "author,series:desc".into();
    let app = app_with(entries(), options);
    assert_eq!(app.sort_keys.len(), 2);
    let titles: Vec<String> = app
        .visible()
        .iter()
        .map(|entry| entry.metadata.title.clone())
        .collect();
    assert_eq!(titles, ["Dune", "The Left Hand of Darkness", "The Hobbit"]);
}

#[test]
fn invalid_sort_setting_is_a_config_error() {
    let mut options = options();
    options.sort = "sideways".into();
    let err = match App::new(PathBuf::from("."), Some(entries()), options) {
        Ok(_) => panic!("an invalid sort setting must be rejected"),
        Err(err) => err,
    };
    assert!(err.to_string().contains("config: sort:"), "{err}");
}

#[test]
fn large_load_is_drained_in_bounded_batches() {
    let mut app = app_with(Vec::new(), options());
    app.loading = true;
    app.load_total = 400;
    for i in 0..400 {
        let mut book = entry("x.epub", "Dune", "Frank Herbert", "", "", false);
        book.path = PathBuf::from(format!("book{i}.epub"));
        app.tx.send(super::app::Msg::Loaded(book)).unwrap();
    }

    app.drain_messages();
    let after_one_tick = app.entries.len();
    assert!(after_one_tick > 0);
    assert!(
        after_one_tick < 400,
        "the whole queue was drained in one tick"
    );
    assert_eq!(app.filtered.len(), after_one_tick);

    let mut ticks = 0;
    while app.entries.len() < 400 {
        app.drain_messages();
        ticks += 1;
        assert!(ticks < 10, "loading did not make progress");
    }
    assert_eq!(app.filtered.len(), 400);
}

#[test]
fn quit_is_handled_while_load_messages_are_queued() {
    let mut app = app_with(Vec::new(), options());
    app.loading = true;
    for i in 0..400 {
        let book = entry("x.epub", "Dune", "Frank Herbert", "", "", false);
        app.tx.send(super::app::Msg::Loaded(book)).unwrap();
        let _ = i;
    }
    app.handle_key(ctrl('c'));
    assert!(app.quit);
}

#[test]
fn theme_selector_floats_over_the_shelf() {
    let mut app = app_with(entries(), options());
    app.handle_key(ctrl('t'));
    assert!(app.theme_open);
    let view = render(&mut app, 120, 40);
    assert!(view.contains("Theme"), "selector missing: {view}");
    assert!(
        view.contains("Books ·"),
        "shelf disappeared behind the pop-over: {view}"
    );
    assert!(view.contains("Title:"), "metadata pane disappeared: {view}");
    assert!(view.contains("Gruvbox Dark (gruvbox-dark)"), "{view}");

    // Previewing a theme repaints the panes behind the selector.
    let accent_before = app.styles.theme.color("accent.primary");
    app.handle_key(key(KeyCode::Down));
    let previewed = render(&mut app, 120, 40);
    assert!(previewed.contains("Books ·"));
    assert_ne!(accent_before, app.styles.theme.color("accent.primary"));
}
