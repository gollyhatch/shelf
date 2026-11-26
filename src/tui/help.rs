//! The keyboard help dialog's content.

/// One line of the keyboard help. An entry with a group is a section heading;
/// otherwise `keys` and `desc` form a binding row.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct HelpEntry {
    pub group: Option<&'static str>,
    pub keys: &'static str,
    pub desc: &'static str,
}

const fn group(name: &'static str) -> HelpEntry {
    HelpEntry {
        group: Some(name),
        keys: "",
        desc: "",
    }
}

const fn bind(keys: &'static str, desc: &'static str) -> HelpEntry {
    HelpEntry {
        group: None,
        keys,
        desc,
    }
}

/// The complete key map, grouped by context.
pub const HELP_ENTRIES: &[HelpEntry] = &[
    group("Browse (both panes)"),
    bind("↑/↓, j/k", "move"),
    bind("J/K", "next/previous series or author"),
    bind("pgup/pgdn", "page up / down"),
    bind("ctrl+u/d", "page up / down"),
    bind("home/end", "first / last"),
    bind("g/G", "first / last"),
    bind("tab", "switch pane (shift+tab reverses)"),
    bind("/", "search"),
    bind("u/U", "show unread / read books only"),
    bind("R", "rescan the directory"),
    bind("r", "repair an unreadable EPUB"),
    bind("m", "look up metadata online"),
    bind("y", "copy metadata to the clipboard"),
    bind("x", "mark read / unread"),
    bind("N", "rename selected book(s)"),
    bind("M", "rename and move selected book(s) into the library"),
    bind("D", "delete selected file(s)"),
    bind("S", "change the sort order"),
    bind("C", "show / hide the cover pane"),
    bind("s", "choose a shelf"),
    bind("A", "add selected book(s) to a shelf"),
    bind("ctrl+o", "open the book in the viewer"),
    bind("ctrl+k", "open the configured search URL"),
    bind("ctrl+t", "choose a theme"),
    bind("?", "open this help"),
    bind("q/ctrl+c", "quit"),
    group("Books pane"),
    bind("space", "select / deselect, then move down"),
    bind("ctrl+a", "select all visible books"),
    bind("→/l", "open metadata (enter/e too)"),
    bind("esc", "clear selection / filter"),
    group("Metadata pane"),
    bind("←/h", "back to books (esc too)"),
    bind("enter/e", "edit field"),
    bind("a/n", "add author / tag"),
    bind("d/delete", "delete author/tag or clear field"),
    bind("backspace", "same as d"),
    group("Bulk selection"),
    bind("enter/e", "edit field on every book"),
    bind("a/n", "append author/tag to every book"),
    bind("d/delete", "clear field on every book"),
    bind("x", "mark every selected book read / unread"),
    bind("esc", "clear the selection (books pane)"),
    group("Search"),
    bind("type", "filter the shelf"),
    bind("term term", "combine filters; all must match"),
    bind("-term", "negate a filter"),
    bind("a OR b", "match either side"),
    bind("field:term", "search one field, e.g. title: or author:"),
    bind("field:", "find books where that field is set"),
    bind("enter", "keep the filter"),
    bind("esc/ctrl+c", "clear the filter"),
    group("Editor"),
    bind("enter", "save; new line in description"),
    bind("ctrl+s", "save"),
    bind("alt+enter", "save"),
    bind("esc/ctrl+c", "cancel"),
    bind("ctrl+w", "delete the word before"),
    bind("alt+backspace", "delete the word before"),
    bind("ctrl+u, ctrl+k", "delete to line start / end"),
    bind("ctrl/alt+←/→", "move by word"),
    bind("tab", "accept suggestion"),
    bind("↑/↓", "choose suggestion (ctrl+p/n too)"),
    group("Delete confirmation"),
    bind("y/enter", "delete"),
    bind("n/esc/q", "cancel"),
    bind("ctrl+c", "cancel"),
    group("Sort selector"),
    bind("↑/↓, j/k", "choose"),
    bind("enter", "apply the sort"),
    bind("esc/q", "cancel"),
    bind("pgup/pgdn", "page up / down"),
    bind("ctrl+u/d", "page up / down"),
    bind("home/end, g/G", "first / last"),
    group("Theme selector"),
    bind("↑/↓, j/k", "preview"),
    bind("enter", "apply the theme"),
    bind("esc/q", "cancel"),
    bind("pgup/pgdn", "page up / down"),
    bind("ctrl+u/d", "page up / down"),
    bind("home/end, g/G", "first / last"),
    group("Shelf selector"),
    bind("↑/↓, j/k", "choose"),
    bind("enter", "apply the shelf"),
    bind("n", "create a shelf from the current filter"),
    bind("e", "edit the selected shelf"),
    bind("tab", "switch between name and query"),
    bind("esc/q", "cancel"),
    bind("pgup/pgdn", "page up / down"),
    bind("ctrl+u/d", "page up / down"),
    bind("home/end, g/G", "first / last"),
    group("Online metadata"),
    bind("m", "search online from the selected book"),
    bind("↑/↓, j/k", "choose field"),
    bind("space", "toggle field"),
    bind("a/n", "select all / none"),
    bind("enter", "apply selected fields"),
    bind("esc/q", "cancel"),
    bind("pgup/pgdn", "page up / down"),
    bind("ctrl+u/d", "page up / down"),
    bind("home/end, g/G", "first / last"),
    group("Help"),
    bind("?/esc/q", "close"),
    bind("↑/↓, j/k", "scroll"),
    bind("pgup/pgdn", "page up / down"),
    bind("ctrl+u/d", "page up / down"),
    bind("home/end", "first / last"),
    bind("g/G", "first / last"),
    bind("ctrl+c", "quit"),
];

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn starts_with_a_group() {
        assert!(HELP_ENTRIES[0].group.is_some());
        assert!(HELP_ENTRIES.iter().any(|entry| entry.keys == "ctrl+t"));
        assert!(
            HELP_ENTRIES
                .iter()
                .all(|entry| entry.group.is_some() || !entry.keys.is_empty())
        );
    }
}
