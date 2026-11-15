//! Color palettes, sourced from opaline themes, and the styles derived from
//! their semantic tokens.

use anyhow::Result;
use opaline::Theme;
use ratatui::style::{Color, Modifier, Style};

/// Theme used when no theme is configured.
pub const DEFAULT_THEME: &str = "gruvbox-dark";

/// One selectable opaline theme.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ThemeChoice {
    /// The kebab-case identifier used in the configuration and `load`.
    pub id: String,
    /// Human readable label shown by the selector.
    pub label: String,
}

/// Every theme opaline ships with, in display order.
pub fn catalog() -> Vec<ThemeChoice> {
    let mut choices: Vec<ThemeChoice> = opaline::list_available_themes()
        .into_iter()
        .filter(|info| info.builtin)
        .map(|info| {
            let display = if info.display_name.trim().is_empty() {
                info.name.clone()
            } else {
                info.display_name.trim().to_owned()
            };
            ThemeChoice {
                label: format!("{display} ({})", info.name),
                id: info.name,
            }
        })
        .collect();
    choices.sort_by(|a, b| {
        a.label
            .to_lowercase()
            .cmp(&b.label.to_lowercase())
            .then_with(|| a.id.cmp(&b.id))
    });
    choices.dedup_by(|a, b| a.id == b.id);
    choices
}

/// Load an opaline theme by its kebab-case identifier. An empty name selects
/// the default theme.
pub fn load(name: &str) -> Result<Theme> {
    let name = name.trim().to_ascii_lowercase();
    let name = if name.is_empty() {
        DEFAULT_THEME
    } else {
        name.as_str()
    };
    opaline::load_by_name(name).ok_or_else(|| {
        anyhow::anyhow!(
            "unknown theme {name:?} (want an opaline theme id such as gruvbox-dark)"
        )
    })
}

/// The styles every screen derives from the active theme.
#[derive(Clone)]
pub struct Styles {
    pub theme: Theme,
    /// Canvas painted behind every screen: `bg.base` with `text.primary`.
    pub base: Style,
    pub border_focused: Color,
    pub border_unfocused: Color,
    pub brand: Style,
    pub header_info: Style,
    pub search_label: Style,
    pub key: Style,
    pub key_sep: Style,
    pub pane_title: Style,
    pub pane_title_active: Style,
    pub item: Style,
    pub item_sub: Style,
    pub item_author: Style,
    pub item_series: Style,
    pub item_sel: Style,
    pub item_sel_bar: Style,
    pub item_sel_author: Style,
    pub item_sel_series: Style,
    pub label: Style,
    pub value: Style,
    pub empty: Style,
    pub row_sel: Style,
    pub add: Style,
    pub check: Style,
    pub ok: Style,
    pub err: Style,
    pub bad: Style,
    pub editor_label: Style,
    pub editor_hint: Style,
    pub confirm_title: Style,
    pub help_title: Style,
    pub help_group: Style,
    pub help: Style,
}

impl Styles {
    pub fn new(theme: Theme) -> Self {
        let color = |token: &str| Color::from(theme.color(token));
        let accent = color("accent.primary");
        let text = color("text.primary");
        let subtle = color("text.secondary");
        let faint = color("text.muted");
        let ok = color("success");
        let err = color("error");
        let sel_bg = color("bg.selection");
        let sel_author = color("accent.secondary");
        let series = color("accent.deep");
        let sel_series = color("accent.tertiary");
        let base = color("bg.base");

        Self {
            base: Style::default().fg(text).bg(base),
            border_focused: color("border.focused"),
            border_unfocused: color("border.unfocused"),
            brand: Style::default()
                .fg(base)
                .bg(accent)
                .add_modifier(Modifier::BOLD),
            header_info: Style::default().fg(subtle),
            search_label: Style::default()
                .fg(base)
                .bg(accent)
                .add_modifier(Modifier::BOLD),
            key: Style::default().fg(text).add_modifier(Modifier::BOLD),
            key_sep: Style::default().fg(faint),
            pane_title: Style::default().fg(subtle),
            pane_title_active: Style::default()
                .fg(accent)
                .add_modifier(Modifier::BOLD),
            item: Style::default().fg(text),
            item_sub: Style::default().fg(subtle),
            item_author: Style::default().fg(faint),
            item_series: Style::default().fg(series),
            item_sel: Style::default().fg(accent).add_modifier(Modifier::BOLD),
            item_sel_bar: Style::default()
                .fg(accent)
                .add_modifier(Modifier::BOLD),
            item_sel_author: Style::default().fg(sel_author),
            item_sel_series: Style::default().fg(sel_series),
            label: Style::default().fg(subtle),
            value: Style::default().fg(text),
            empty: Style::default().fg(faint).add_modifier(Modifier::ITALIC),
            row_sel: Style::default().bg(sel_bg).add_modifier(Modifier::BOLD),
            add: Style::default().fg(faint),
            check: Style::default().fg(ok).add_modifier(Modifier::BOLD),
            ok: Style::default().fg(ok),
            err: Style::default().fg(err),
            bad: Style::default().fg(err).add_modifier(Modifier::BOLD),
            editor_label: Style::default()
                .fg(accent)
                .add_modifier(Modifier::BOLD),
            editor_hint: Style::default().fg(subtle),
            confirm_title: Style::default()
                .fg(err)
                .add_modifier(Modifier::BOLD),
            help_title: Style::default()
                .fg(accent)
                .add_modifier(Modifier::BOLD),
            help_group: Style::default()
                .fg(accent)
                .add_modifier(Modifier::BOLD),
            help: Style::default().fg(subtle),
            theme,
        }
    }
}

/// Validate a configured theme name without building an application.
pub fn validate(name: &str) -> Result<()> {
    load(name).map(|_| ()).map_err(|err| {
        if err.to_string().starts_with("unknown theme") {
            err
        } else {
            anyhow::anyhow!("theme {name:?}: {err}")
        }
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn loads_opaline_themes() {
        assert!(load("gruvbox-dark").is_ok());
        assert!(load("nord").is_ok());
        assert!(load("catppuccin-mocha").is_ok());
        assert!(load("").is_ok());
        assert!(
            load("gruvbox").is_err(),
            "shelf no longer aliases theme names"
        );
        assert!(load("nope").is_err());
    }

    #[test]
    fn catalog_lists_every_builtin() {
        let choices = catalog();
        assert_eq!(choices.len(), opaline::builtins::BUILTIN_COUNT);
        assert!(choices.iter().any(|choice| choice.id == DEFAULT_THEME));
        let mut sorted = choices.clone();
        sorted.sort_by(|a, b| {
            a.label.to_lowercase().cmp(&b.label.to_lowercase())
        });
        assert_eq!(choices, sorted);
    }

    #[test]
    fn styles_carry_the_theme_canvas() {
        let theme = load(DEFAULT_THEME).unwrap();
        let styles = Styles::new(theme.clone());
        assert_eq!(
            styles.base.bg,
            Some(Color::from(theme.color(opaline::names::tokens::BG_BASE)))
        );
        assert_eq!(
            styles.base.fg,
            Some(Color::from(theme.color("text.primary")))
        );
    }
}
