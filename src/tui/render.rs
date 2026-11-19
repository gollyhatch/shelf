//! Drawing the browser with ratatui.

use ratatui::Frame;
use ratatui::layout::Rect;
use ratatui::style::{Color, Modifier, Style};
use ratatui::text::{Line, Span, Text};
use ratatui::widgets::{Block, BorderType, Borders, Clear, Padding, Paragraph};
use unicode_width::{UnicodeWidthChar, UnicodeWidthStr};

use super::app::{
    App, Mode, Pane, ReadFilter, Row, RowKind, StatusKind, adjust_list_top,
    clamp, entry_heights, file_count, file_name,
};
use super::help::HELP_ENTRIES;
use super::sort::SORT_MODES;
use super::theme::Styles;
use super::wrap::wrap_text;

impl App {
    /// Draw one frame and update the layout dimensions the key handlers use.
    pub fn view(&mut self, frame: &mut Frame) {
        let area = frame.area();
        self.width = area.width as usize;
        self.height = area.height as usize;

        // The theme's canvas goes down first: every screen below paints only
        // foreground colors, so these backgrounds show through.
        frame.render_widget(Block::default().style(self.styles.base), area);

        if let Some(fatal) = &self.fatal {
            let lines = vec![
                Line::default(),
                Line::from(vec![
                    Span::raw("  "),
                    Span::styled("error: ", self.styles.err),
                    Span::raw(fatal.clone()),
                ]),
                Line::default(),
                Line::from(vec![
                    Span::raw("  "),
                    Span::styled("q to quit", self.styles.help),
                ]),
            ];
            frame.render_widget(Paragraph::new(Text::from(lines)), area);
            return;
        }
        if self.width == 0 || self.height == 0 {
            return;
        }

        let mut y = area.y;
        self.draw_header(frame, Rect::new(area.x, y, area.width, 1));
        y += 1;
        if self.searching || !self.filter.is_empty() {
            self.draw_search(frame, Rect::new(area.x, y, area.width, 1));
            y += 1;
        }
        y += 1; // blank line before the body

        let body_h = self.body_height();
        if body_h > 0 {
            self.draw_body(
                frame,
                Rect::new(area.x, y, area.width, body_h as u16),
            );
            y += body_h as u16;
        }

        if self.mode == Mode::Edit {
            y += 1;
            self.draw_editor(frame, area, y);
            return;
        }
        if !self.status.is_empty() {
            self.draw_status(frame, Rect::new(area.x, y, area.width, 1));
        } else if y < area.bottom() {
            y += 1;
            if y < area.bottom() {
                self.draw_footer(frame, Rect::new(area.x, y, area.width, 1));
            }
        }
    }

    fn draw_header(&self, frame: &mut Frame, area: Rect) {
        let mut count = book_count(self.filtered.len());
        if !self.filter.is_empty() || self.read_filter != ReadFilter::All {
            count = format!(
                "{} of {} books",
                self.filtered.len(),
                self.entries.len()
            );
        }

        let mut spans = vec![
            Span::styled(" shelf ", self.styles.brand),
            Span::raw("  "),
            Span::styled(count, self.styles.header_info),
        ];
        let mut used =
            7 + 2 + UnicodeWidthStr::width(spans[2].content.as_ref());

        if self.read_filter != ReadFilter::All {
            let label = if self.read_filter == ReadFilter::Unread {
                " · unread only"
            } else {
                " · read only"
            };
            let badge_width = UnicodeWidthStr::width(label);
            if used + badge_width <= self.width {
                spans.push(Span::styled(label, self.styles.header_info));
                used += badge_width;
            }
        }
        if !self.sel.is_empty() {
            let label = format!(" · {} selected", self.sel.len());
            let badge_width = UnicodeWidthStr::width(label.as_str());
            if used + badge_width <= self.width {
                spans.push(Span::styled(label, self.styles.header_info));
                used += badge_width;
            }
        }
        if self.loading {
            let mut text = "loading…".to_owned();
            if self.load_total > 0 {
                if self.load_index == 0 {
                    text = format!("scanning… ({})", self.load_total);
                    if let Some(path) = &self.load_path {
                        text = format!(
                            "scanning… {} ({})",
                            ellipsis(&file_name(path), (self.width / 6).max(8)),
                            self.load_total
                        );
                    }
                } else {
                    text = format!(
                        "loading {}/{}",
                        self.load_index, self.load_total
                    );
                }
            }
            if let Some(path) = &self.load_path {
                text.push(' ');
                text.push_str(&ellipsis(
                    &file_name(path),
                    (self.width / 4).max(10),
                ));
            }
            let width = UnicodeWidthStr::width(text.as_str()) + 2;
            if used + width <= self.width {
                spans.push(Span::raw("  "));
                spans.push(Span::styled(text, self.styles.header_info));
                used += width;
            }
        }

        let right =
            ellipsis(&self.dir.to_string_lossy(), (self.width / 2).max(10));
        let right_width = UnicodeWidthStr::width(right.as_str());
        let gap = (self.width as isize - used as isize - right_width as isize)
            .max(0) as usize;
        if gap >= 1 {
            spans.push(Span::raw(" ".repeat(gap)));
            spans.push(Span::styled(right, self.styles.header_info));
        }
        frame.render_widget(Paragraph::new(Line::from(spans)), area);
    }

    fn draw_search(&self, frame: &mut Frame, area: Rect) {
        if self.searching {
            let (text, cursor, _) =
                self.search.window(area.width.saturating_sub(4) as usize);
            let mut spans = vec![Span::styled(
                "⌕ ",
                Style::default()
                    .fg(self.styles.brand.bg.unwrap_or(Color::Reset))
                    .add_modifier(Modifier::BOLD),
            )];
            let chars: Vec<char> = text.chars().collect();
            if chars.is_empty() {
                spans.push(Span::styled(
                    " ",
                    Style::default().add_modifier(Modifier::REVERSED),
                ));
            } else {
                for (i, c) in chars.iter().enumerate() {
                    let mut s = c.to_string();
                    if i == cursor {
                        spans.push(Span::styled(
                            std::mem::take(&mut s),
                            self.styles.value.add_modifier(Modifier::REVERSED),
                        ));
                    } else {
                        spans.push(Span::styled(s, self.styles.value));
                    }
                }
                if cursor >= chars.len() {
                    spans.push(Span::styled(
                        " ",
                        self.styles.value.add_modifier(Modifier::REVERSED),
                    ));
                }
            }
            frame.render_widget(Paragraph::new(Line::from(spans)), area);
            return;
        }
        let line = Line::from(vec![
            Span::styled(" search ", self.styles.search_label),
            Span::styled(
                ellipsis(&self.filter, (self.width.saturating_sub(14)).max(8)),
                self.styles.value,
            ),
        ]);
        frame.render_widget(Paragraph::new(line), area);
    }

    fn draw_status(&self, frame: &mut Frame, area: Rect) {
        let (prefix, style) = match self.status_kind {
            StatusKind::Err => ("✗ ", self.styles.err),
            StatusKind::Ok => ("✓ ", self.styles.ok),
            StatusKind::Info => ("", self.styles.help),
        };
        frame.render_widget(
            Paragraph::new(Line::from(Span::styled(
                format!("{prefix}{}", self.status),
                style,
            ))),
            area,
        );
    }

    fn draw_body(&self, frame: &mut Frame, area: Rect) {
        if self.help_open {
            self.draw_help(frame, area);
            return;
        }
        if self.confirm_delete {
            self.draw_confirm(frame, area);
            return;
        }
        if self.sort_open {
            self.draw_sort(frame, area);
            return;
        }
        if self.online_open {
            self.draw_online(frame, area);
            return;
        }
        if area.height == 0 {
            return;
        }
        if self.width >= 84 {
            let list_w = (self.width / 3) as u16;
            let detail_w = area.width.saturating_sub(list_w + 1);
            let list_rect = Rect::new(area.x, area.y, list_w, area.height);
            let detail_rect =
                Rect::new(area.x + list_w + 1, area.y, detail_w, area.height);
            self.draw_pane(frame, Pane::List, list_rect);
            self.draw_pane(frame, Pane::Detail, detail_rect);
        } else {
            let (list_h, detail_h, _) = self.pane_heights();
            let list_rect =
                Rect::new(area.x, area.y, area.width, list_h as u16);
            let detail_rect = Rect::new(
                area.x,
                area.y + list_h as u16,
                area.width,
                detail_h as u16,
            );
            self.draw_pane(frame, Pane::List, list_rect);
            self.draw_pane(frame, Pane::Detail, detail_rect);
        }
        // The theme selector floats over the shelf, so its preview is visible
        // on the panes behind it.
        if self.theme_open {
            self.draw_theme(frame, area);
        }
    }

    fn draw_pane(&self, frame: &mut Frame, pane: Pane, rect: Rect) {
        if rect.width < 4 || rect.height < 2 {
            return;
        }
        let active = self.pane == pane && self.mode != Mode::Edit;
        let border = if active {
            self.styles.border_focused
        } else {
            self.styles.border_unfocused
        };
        let block = Block::default()
            .borders(Borders::ALL)
            .border_type(BorderType::Rounded)
            .border_style(Style::default().fg(border))
            .padding(Padding::horizontal(1));
        frame.render_widget(block, rect);

        let inner = Rect::new(
            rect.x + 2,
            rect.y + 1,
            rect.width.saturating_sub(4),
            rect.height.saturating_sub(2),
        );
        if inner.width == 0 || inner.height == 0 {
            return;
        }
        let width = inner.width as usize;
        let height = inner.height as usize;

        let (title, mut lines) = if pane == Pane::List {
            (
                format!("Books · {}", self.sort_label()),
                self.list_lines(width, height.saturating_sub(1)),
            )
        } else {
            let title = if self.bulk_active() {
                format!("Bulk · {} books", self.sel.len())
            } else if let Some(entry) = self.selected() {
                file_name(&entry.path)
            } else {
                String::new()
            };
            let content_height = if title.is_empty() {
                height
            } else {
                height.saturating_sub(1)
            };
            (title, self.detail_lines(width, content_height, active))
        };

        if !title.is_empty() {
            let title_style = if active {
                self.styles.pane_title_active
            } else {
                self.styles.pane_title
            };
            lines.insert(
                0,
                Line::from(Span::styled(ellipsis(&title, width), title_style)),
            );
        }
        frame.render_widget(Paragraph::new(Text::from(lines)), inner);
    }

    fn list_lines(&self, width: usize, height: usize) -> Vec<Line<'static>> {
        let items = self.visible();
        if items.is_empty() {
            if !self.filter.is_empty() {
                return vec![Line::from(Span::styled(
                    "no matches",
                    self.styles.empty,
                ))];
            }
            if self.loading {
                return Vec::new();
            }
            return vec![Line::from(Span::styled(
                "no EPUB files here",
                self.styles.empty,
            ))];
        }

        let heights = entry_heights(&items);
        let top =
            adjust_list_top(self.list_top, self.list_idx, &heights, height);
        let checkboxes = !self.sel.is_empty();
        let indent = if checkboxes { 4 } else { 2 };

        let mut out: Vec<Line<'static>> = Vec::new();
        for i in top..items.len() {
            if out.len() >= height {
                break;
            }
            let entry = &items[i];
            let selected = i == self.list_idx;
            let checked = checkboxes && self.sel.contains(&self.filtered[i]);
            for line in self.list_entry_lines(
                entry, selected, checked, checkboxes, width, indent,
            ) {
                if out.len() >= height {
                    break;
                }
                out.push(line);
            }
        }
        out
    }

    fn list_entry_lines(
        &self,
        entry: &crate::library::Entry,
        selected: bool,
        checked: bool,
        checkboxes: bool,
        width: usize,
        indent: usize,
    ) -> Vec<Line<'static>> {
        let mut spans: Vec<Span<'static>> = Vec::new();
        let check = if checkboxes {
            if checked {
                vec![Span::styled("✓", self.styles.check), Span::raw(" ")]
            } else {
                vec![Span::raw("  ")]
            }
        } else {
            Vec::new()
        };

        let mut title = entry.title();
        let mut sub = entry.author();
        if sub.is_empty() {
            sub = file_name(&entry.path);
        }
        let mut series = String::new();
        if entry.error.is_none() {
            series = entry.series_line();
        } else {
            title = format!("! {title}");
            sub = "unreadable metadata".into();
        }

        let mut read_mark: Vec<Span<'static>> = Vec::new();
        let mut title_width = width.saturating_sub(indent);
        if entry.error.is_none() && entry.metadata.read && title_width > 2 {
            title_width -= 2;
            read_mark.push(Span::raw(" "));
            read_mark.push(Span::styled("✓", self.styles.check));
        }

        if selected {
            let readable = entry.error.is_none();
            let bar = Span::styled("▌ ", self.styles.item_sel_bar);
            let title_style = if readable {
                self.styles.item_sel
            } else {
                self.styles.bad
            };
            spans.push(bar.clone());
            spans.extend(check.clone());
            spans
                .push(Span::styled(ellipsis(&title, title_width), title_style));
            spans.extend(read_mark.clone());
            let mut lines = vec![Line::from(spans)];
            if !series.is_empty() {
                lines.push(Line::from(vec![
                    bar.clone(),
                    Span::raw(" ".repeat(indent.saturating_sub(2))),
                    Span::styled(
                        ellipsis(&series, width.saturating_sub(indent)),
                        self.styles.item_sel_series,
                    ),
                ]));
            }
            let author_style = if readable {
                self.styles.item_sel_author
            } else {
                self.styles.item_author
            };
            lines.push(Line::from(vec![
                bar,
                Span::raw(" ".repeat(indent.saturating_sub(2))),
                Span::styled(
                    ellipsis(&sub, width.saturating_sub(indent)),
                    author_style,
                ),
            ]));
            return lines;
        }

        let title_style = if entry.error.is_some() {
            self.styles.bad
        } else {
            self.styles.item
        };
        let mut first = vec![Span::raw("  ")];
        first.extend(check);
        first.push(Span::styled(ellipsis(&title, title_width), title_style));
        first.extend(read_mark);
        let mut lines = vec![Line::from(first)];
        if !series.is_empty() {
            lines.push(Line::from(vec![
                Span::raw(" ".repeat(indent)),
                Span::styled(
                    ellipsis(&series, width.saturating_sub(indent)),
                    self.styles.item_series,
                ),
            ]));
        }
        lines.push(Line::from(vec![
            Span::raw(" ".repeat(indent)),
            Span::styled(
                ellipsis(&sub, width.saturating_sub(indent)),
                self.styles.item_author,
            ),
        ]));
        lines
    }

    fn detail_lines(
        &self,
        width: usize,
        height: usize,
        active: bool,
    ) -> Vec<Line<'static>> {
        if !self.bulk_active() {
            let Some(entry) = self.selected() else {
                return vec![Line::from(Span::styled(
                    "no book selected",
                    self.styles.empty,
                ))];
            };
            if let Some(err) = &entry.error {
                return vec![
                    Line::from(Span::styled(
                        "could not read the metadata of this file",
                        self.styles.bad,
                    )),
                    Line::default(),
                    Line::from(Span::styled(
                        ellipsis(err, width),
                        self.styles.err,
                    )),
                    Line::default(),
                    Line::from(Span::styled(
                        ellipsis(
                            "r repairs the EPUB · R rescans the directory",
                            width,
                        ),
                        self.styles.help,
                    )),
                ];
            }
        }
        if self.rows.is_empty() {
            return vec![Line::from(Span::styled(
                "no metadata",
                self.styles.empty,
            ))];
        }

        let label_w = 12;
        let heights: Vec<usize> = self
            .rows
            .iter()
            .map(|row| self.render_row(row, width, label_w, false).len())
            .collect();

        let mut top = clamp(self.row_top, 0, self.rows.len() - 1);
        if self.row_idx < top {
            top = self.row_idx;
        }
        if self.row_idx < self.rows.len() {
            while top < self.row_idx {
                let used: usize = heights[top..=self.row_idx].iter().sum();
                if used <= height {
                    break;
                }
                top += 1;
            }
        }

        let mut out = Vec::new();
        for i in top..self.rows.len() {
            if out.len() >= height {
                break;
            }
            let lines = self.render_row(
                &self.rows[i],
                width,
                label_w,
                i == self.row_idx && active,
            );
            for line in lines {
                if out.len() >= height {
                    break;
                }
                out.push(line);
            }
        }
        out
    }

    fn render_row(
        &self,
        row: &Row,
        width: usize,
        label_w: usize,
        selected: bool,
    ) -> Vec<Line<'static>> {
        if row.kind == RowKind::Field && row.field == crate::epub::Field::Read {
            return self.render_read_checkbox(row, width, label_w, selected);
        }
        match row.kind {
            RowKind::Creator | RowKind::Subject => {
                let plain = format!("  ↳ {}", row.label);
                if selected {
                    return vec![Line::from(Span::styled(
                        pad_right(&ellipsis(&plain, width), width),
                        self.styles.row_sel,
                    ))];
                }
                return vec![Line::from(vec![
                    Span::raw("  "),
                    Span::styled("↳", self.styles.item_sub),
                    Span::raw(" "),
                    Span::styled(
                        ellipsis(&row.label, width.saturating_sub(4)),
                        self.styles.value,
                    ),
                ])];
            }
            RowKind::AddCreator | RowKind::AddSubject => {
                let plain = format!("  + {}", row.label);
                if selected {
                    return vec![Line::from(Span::styled(
                        pad_right(&ellipsis(&plain, width), width),
                        self.styles.row_sel,
                    ))];
                }
                return vec![Line::from(Span::styled(plain, self.styles.add))];
            }
            RowKind::Field => {}
        }

        let label = format!("{}:", row.label);
        if matches!(
            row.field,
            crate::epub::Field::Title | crate::epub::Field::Description
        ) && !row.value.is_empty()
        {
            return self.render_wrapped_field(
                &label, &row.value, width, label_w, selected,
            );
        }

        let value_w = width.saturating_sub(label_w + 3).max(4);
        let mut spans = vec![
            Span::raw(" "),
            Span::styled(
                pad_right(&ellipsis(&label, label_w), label_w),
                self.styles.label,
            ),
            Span::raw(" "),
        ];
        if row.mixed {
            spans.push(Span::styled("(mixed)", self.styles.empty));
        } else if row.value.is_empty() {
            spans.push(Span::styled("(none)", self.styles.empty));
        } else {
            spans.push(Span::styled(
                ellipsis(&row.value, value_w),
                self.styles.value,
            ));
        }
        if selected {
            let plain: String =
                spans.iter().map(|span| span.content.as_ref()).collect();
            return vec![Line::from(Span::styled(
                pad_right(&plain, width),
                self.styles.row_sel,
            ))];
        }
        vec![Line::from(spans)]
    }

    fn render_read_checkbox(
        &self,
        row: &Row,
        width: usize,
        label_w: usize,
        selected: bool,
    ) -> Vec<Line<'static>> {
        let label = format!("{}:", row.label);
        let (mark, mark_style) = if row.mixed {
            ("[-]", self.styles.empty)
        } else if row.value.trim().eq_ignore_ascii_case("yes") {
            ("[x]", self.styles.check)
        } else {
            ("[ ]", self.styles.value)
        };
        let plain = format!(
            " {} {}",
            pad_right(&ellipsis(&label, label_w), label_w),
            mark
        );
        if selected {
            return vec![Line::from(Span::styled(
                pad_right(&plain, width),
                self.styles.row_sel,
            ))];
        }
        vec![Line::from(vec![
            Span::raw(" "),
            Span::styled(
                pad_right(&ellipsis(&label, label_w), label_w),
                self.styles.label,
            ),
            Span::raw(" "),
            Span::styled(mark, mark_style),
        ])]
    }

    fn render_wrapped_field(
        &self,
        label: &str,
        value: &str,
        width: usize,
        label_w: usize,
        selected: bool,
    ) -> Vec<Line<'static>> {
        let value_w = width.saturating_sub(label_w + 3).max(4);
        let wrapped = wrap_text(value, value_w);
        let indent = " ".repeat(label_w + 2);
        let mut lines = Vec::with_capacity(wrapped.len());
        for (i, text) in wrapped.iter().enumerate() {
            if selected {
                let plain = if i == 0 {
                    format!(
                        " {} {} ",
                        pad_right(&ellipsis(label, label_w), label_w),
                        text
                    )
                } else {
                    format!("{indent}{text}")
                };
                lines.push(Line::from(Span::styled(
                    pad_right(&plain, width),
                    self.styles.row_sel,
                )));
                continue;
            }
            if i == 0 {
                lines.push(Line::from(vec![
                    Span::raw(" "),
                    Span::styled(
                        pad_right(&ellipsis(label, label_w), label_w),
                        self.styles.label,
                    ),
                    Span::raw(" "),
                    Span::styled(text.clone(), self.styles.value),
                ]));
            } else {
                lines.push(Line::from(vec![
                    Span::raw(indent.clone()),
                    Span::styled(text.clone(), self.styles.value),
                ]));
            }
        }
        lines
    }

    fn draw_editor(&self, frame: &mut Frame, area: Rect, y: u16) {
        if y >= area.bottom() {
            return;
        }
        let width = clamp(self.width.saturating_sub(4), 24, 96);
        let content_w = width.saturating_sub(4);
        let label = if self.editing_new_creator() {
            "Add author".to_owned()
        } else if self.editing_new_subject() {
            "Add tag".to_owned()
        } else if self.edit_row.kind == RowKind::Creator {
            "Edit author".to_owned()
        } else if self.edit_row.kind == RowKind::Subject {
            "Edit tag".to_owned()
        } else {
            format!("Edit {}", self.edit_row.label.to_lowercase())
        };
        let label = if self.edit_bulk {
            format!("{label} · {} books", self.sel.len())
        } else {
            label
        };

        let mut hint = if self.using_area {
            "ctrl+s save · esc cancel · enter new line".to_owned()
        } else {
            "enter save · esc cancel".to_owned()
        };
        if self.show_suggestions {
            hint.push_str(" · tab complete · ↑↓ choose");
        }

        let mut lines: Vec<Line<'static>> = Vec::new();
        lines.push(Line::from(Span::styled(label, self.styles.editor_label)));
        if self.using_area {
            lines.extend(self.area_lines(content_w));
        } else {
            lines.push(self.input_line(content_w));
        }
        if self.show_suggestions {
            lines.extend(self.suggestion_lines(content_w));
        }
        if self.editing_new_creator() {
            lines.push(Line::from(Span::styled(
                "separate several authors with \";\"",
                self.styles.editor_hint,
            )));
        } else if self.editing_new_subject() {
            lines.push(Line::from(Span::styled(
                "separate several tags with a comma",
                self.styles.editor_hint,
            )));
        }

        let outer_w = (width + 4).min(self.width) as u16;
        let outer_h = (lines.len() + 2)
            .min(area.bottom().saturating_sub(y) as usize)
            as u16;
        let rect = Rect::new(area.x, y, outer_w, outer_h);
        let block = Block::default()
            .borders(Borders::ALL)
            .border_type(BorderType::Rounded)
            .border_style(Style::default().fg(self.styles.border_focused))
            .padding(Padding::horizontal(1));
        frame.render_widget(block, rect);
        let inner = Rect::new(
            rect.x + 2,
            rect.y + 1,
            rect.width.saturating_sub(4),
            rect.height.saturating_sub(2),
        );
        frame.render_widget(Paragraph::new(Text::from(lines)), inner);

        let hint_y = y + outer_h;
        if hint_y < area.bottom() {
            frame.render_widget(
                Paragraph::new(Line::from(vec![
                    Span::raw("  "),
                    Span::styled(
                        ellipsis(&hint, self.width.saturating_sub(2)),
                        self.styles.editor_hint,
                    ),
                ])),
                Rect::new(area.x, hint_y, area.width, 1),
            );
        }
    }

    fn input_line(&self, width: usize) -> Line<'static> {
        let (text, cursor, _) = self.input.window(width.saturating_sub(2));
        let mut spans = vec![Span::styled("› ", self.styles.editor_label)];
        let chars: Vec<char> = text.chars().collect();
        let mut placed = false;
        for (i, c) in chars.iter().enumerate() {
            let style = if i == cursor {
                placed = true;
                self.styles.value.add_modifier(Modifier::REVERSED)
            } else {
                self.styles.value
            };
            spans.push(Span::styled(c.to_string(), style));
        }
        if !placed {
            spans.push(Span::styled(
                " ",
                self.styles.value.add_modifier(Modifier::REVERSED),
            ));
        }
        Line::from(spans)
    }

    fn area_lines(&self, width: usize) -> Vec<Line<'static>> {
        let visible = clamp(self.height / 3, 3, 8);
        let cursor = self.area.cursor();
        let rows = area_rows(self.area.lines(), cursor, width);
        let at = area_cursor_row(&rows, cursor);
        let top = at
            .saturating_sub(visible.saturating_sub(1))
            .min(rows.len().saturating_sub(visible));
        let mut out = Vec::new();
        for (i, row) in rows.iter().enumerate().skip(top).take(visible) {
            let mut spans = Vec::new();
            for (n, ch) in row.chars.iter().enumerate() {
                let style = if i == at && row.start + n == cursor.1 {
                    self.styles.value.add_modifier(Modifier::REVERSED)
                } else {
                    self.styles.value
                };
                spans.push(Span::styled(ch.to_string(), style));
            }
            if i == at && cursor.1 == row.start + row.chars.len() {
                spans.push(Span::styled(
                    " ",
                    self.styles.value.add_modifier(Modifier::REVERSED),
                ));
            }
            out.push(Line::from(spans));
        }
        while out.len() < visible {
            out.push(Line::default());
        }
        out
    }

    fn suggestion_lines(&self, width: usize) -> Vec<Line<'static>> {
        let start = self
            .suggestion_index
            .saturating_sub(super::app::MAX_SUGGESTIONS - 1);
        let end =
            (start + super::app::MAX_SUGGESTIONS).min(self.suggestions.len());
        let mut lines = Vec::new();
        for (i, suggestion) in
            self.suggestions.iter().enumerate().take(end).skip(start)
        {
            if i == self.suggestion_index {
                let plain = format!("  ↳ {suggestion}");
                lines.push(Line::from(Span::styled(
                    pad_right(&ellipsis(&plain, width), width),
                    self.styles.row_sel,
                )));
            } else {
                lines.push(Line::from(vec![
                    Span::raw("  "),
                    Span::styled("↳", self.styles.item_sub),
                    Span::raw(" "),
                    Span::styled(
                        ellipsis(suggestion, width.saturating_sub(4)),
                        self.styles.value,
                    ),
                ]));
            }
        }
        lines
    }

    // ---------- dialogs ----------

    fn draw_help(&self, frame: &mut Frame, area: Rect) {
        let height = self.body_height();
        if height < 4 || self.width < 16 {
            frame.render_widget(
                Paragraph::new(ellipsis("Keys · ?/esc close", self.width)),
                Rect::new(area.x, area.y, area.width, 1),
            );
            return;
        }
        let content_w = clamp(self.width.saturating_sub(6), 10, 72);
        let page = self.help_page_size();
        if page == 0 {
            return;
        }
        let top = clamp(self.help_top, 0, self.help_max_top());
        let mut title = "Keys".to_owned();
        if HELP_ENTRIES.len() > page {
            let end = (top + page).min(HELP_ENTRIES.len());
            title =
                format!("Keys  {}–{} of {}", top + 1, end, HELP_ENTRIES.len());
        }
        let key_w =
            clamp(content_w / 2, 5, 14).min(content_w.saturating_sub(4).max(1));
        let desc_w = content_w.saturating_sub(3 + key_w).max(1);

        let mut lines = vec![Line::from(Span::styled(
            pad_right(&ellipsis(&title, content_w), content_w),
            self.styles.help_title,
        ))];
        for entry in HELP_ENTRIES.iter().skip(top).take(page) {
            if let Some(group) = entry.group {
                lines.push(Line::from(Span::styled(
                    pad_right(&ellipsis(group, content_w), content_w),
                    self.styles.help_group,
                )));
                continue;
            }
            lines.push(Line::from(vec![
                Span::raw("  "),
                Span::styled(
                    pad_right(&ellipsis(entry.keys, key_w), key_w),
                    self.styles.key,
                ),
                Span::raw(" "),
                Span::styled(
                    pad_right(&ellipsis(entry.desc, desc_w), desc_w),
                    self.styles.help,
                ),
            ]));
        }
        self.draw_dialog(
            frame,
            area,
            content_w,
            lines,
            self.styles.border_focused,
        );
    }

    fn draw_sort(&self, frame: &mut Frame, area: Rect) {
        let height = self.body_height();
        if height < 4 || self.width < 16 {
            return;
        }
        let page = self.sort_page_size();
        if page == 0 {
            return;
        }
        let content_w = clamp(self.width.saturating_sub(6), 10, 60);
        let top = clamp(
            self.sort_idx.saturating_sub(page.saturating_sub(1)),
            0,
            SORT_MODES.len().saturating_sub(page),
        );
        let mut title = "Sort by".to_owned();
        if SORT_MODES.len() > page {
            let end = (top + page).min(SORT_MODES.len());
            title =
                format!("Sort by  {}–{} of {}", top + 1, end, SORT_MODES.len());
        }
        let current = self.current_sort_index();
        let mut lines = vec![Line::from(Span::styled(
            pad_right(&ellipsis(&title, content_w), content_w),
            self.styles.help_title,
        ))];
        for (i, mode) in SORT_MODES.iter().enumerate().skip(top).take(page) {
            let mut label = format!("  {}", mode.label());
            if Some(i) == current {
                label.push_str("  ✓");
            }
            let plain = pad_right(&ellipsis(&label, content_w), content_w);
            let style = if i == self.sort_idx {
                self.styles.row_sel
            } else {
                self.styles.value
            };
            lines.push(Line::from(Span::styled(plain, style)));
        }
        self.draw_dialog(
            frame,
            area,
            content_w,
            lines,
            self.styles.border_focused,
        );
    }

    fn draw_theme(&self, frame: &mut Frame, area: Rect) {
        let height = self.body_height();
        if height < 4 || self.width < 16 || self.themes.is_empty() {
            return;
        }
        let page = self.theme_page_size();
        if page == 0 {
            return;
        }
        let content_w = clamp(self.width.saturating_sub(6), 20, 72);
        let top = clamp(
            self.theme_idx.saturating_sub(page.saturating_sub(1)),
            0,
            self.themes.len().saturating_sub(page),
        );
        let mut title = "Theme".to_owned();
        if self.themes.len() > page {
            let end = (top + page).min(self.themes.len());
            title =
                format!("Theme  {}–{} of {}", top + 1, end, self.themes.len());
        }
        let current = self
            .themes
            .iter()
            .position(|choice| choice.id == self.theme_name);
        let mut lines = vec![Line::from(Span::styled(
            pad_right(&ellipsis(&title, content_w), content_w),
            self.styles.help_title,
        ))];
        for (i, choice) in self.themes.iter().enumerate().skip(top).take(page) {
            let mut label = format!("  {}", choice.label);
            if Some(i) == current {
                label.push_str("  ✓");
            }
            let plain = pad_right(&ellipsis(&label, content_w), content_w);
            let style = if i == self.theme_idx {
                self.styles.row_sel
            } else {
                self.styles.value
            };
            lines.push(Line::from(Span::styled(plain, style)));
        }
        self.draw_dialog(
            frame,
            area,
            content_w,
            lines,
            self.styles.border_focused,
        );
    }

    fn draw_confirm(&self, frame: &mut Frame, area: Rect) {
        let height = self.body_height();
        if height == 0 {
            return;
        }
        let count = self.delete_paths.len();
        let title = format!("Delete {}?", file_count(count));
        if height < 3 {
            frame.render_widget(
                Paragraph::new(ellipsis(
                    &format!("{title} · y confirm · n cancel"),
                    self.width,
                )),
                Rect::new(area.x, area.y, area.width, 1),
            );
            return;
        }
        let content_w = clamp(self.width.saturating_sub(6), 4, 60);
        let max_lines = height - 2;
        let mut lines = vec![Line::from(Span::styled(
            pad_right(&ellipsis(&title, content_w), content_w),
            self.styles.confirm_title,
        ))];
        if max_lines >= 2 {
            let slots = (max_lines - 2).min(3);
            if slots > 0 {
                if count <= slots {
                    for path in &self.delete_paths {
                        lines.push(Line::from(Span::styled(
                            pad_right(
                                &ellipsis(&file_name(path), content_w),
                                content_w,
                            ),
                            self.styles.value,
                        )));
                    }
                } else if slots == 1 {
                    let line = format!(
                        "{} + {} more",
                        file_name(&self.delete_paths[0]),
                        count - 1
                    );
                    lines.push(Line::from(Span::styled(
                        pad_right(&ellipsis(&line, content_w), content_w),
                        self.styles.value,
                    )));
                } else {
                    for path in self.delete_paths.iter().take(slots - 1) {
                        lines.push(Line::from(Span::styled(
                            pad_right(
                                &ellipsis(&file_name(path), content_w),
                                content_w,
                            ),
                            self.styles.value,
                        )));
                    }
                    let more = format!("and {} more", count - (slots - 1));
                    lines.push(Line::from(Span::styled(
                        pad_right(&ellipsis(&more, content_w), content_w),
                        self.styles.empty,
                    )));
                }
            }
            let hint = if content_w >= 32 {
                "y/enter confirm · n/esc cancel"
            } else {
                "y confirm · n cancel"
            };
            lines.push(Line::from(Span::styled(
                pad_right(&ellipsis(hint, content_w), content_w),
                self.styles.help,
            )));
        }
        self.draw_dialog(
            frame,
            area,
            content_w,
            lines,
            self.styles.err.fg.unwrap_or(Color::Red),
        );
    }

    fn draw_online(&self, frame: &mut Frame, area: Rect) {
        let height = self.body_height();
        if height < 6 || self.width < 20 || self.online_fields.is_empty() {
            frame.render_widget(
                Paragraph::new(ellipsis(
                    "Online metadata · space toggle · enter apply · esc cancel",
                    self.width,
                )),
                Rect::new(area.x, area.y, area.width, 1),
            );
            return;
        }
        let content_w = clamp(self.width.saturating_sub(6), 14, 76)
            .max(self.width * 4 / 5 - 4);
        let list_h = self.online_list_height();
        if list_h < 1 {
            return;
        }
        let (label_w, value_w) = online_column_widths(content_w);
        if value_w < 1 {
            return;
        }

        let mut title = "Online metadata".to_owned();
        if !self.online_found.title.trim().is_empty() {
            title.push_str(" · ");
            title.push_str(self.online_found.title.trim());
        }
        let mut lines = vec![Line::from(Span::styled(
            pad_right(&ellipsis(&title, content_w), content_w),
            self.styles.help_title,
        ))];
        let header = format!(
            "    {}{}  {} │ {}",
            pad_right("Field", label_w),
            "",
            pad_right("Current", value_w),
            pad_right("Found", value_w)
        );
        lines.push(Line::from(Span::styled(
            pad_right(&ellipsis(&header, content_w), content_w),
            self.styles.help,
        )));

        let heights: Vec<usize> = self
            .online_fields
            .iter()
            .map(|field| online_row_lines(field, label_w, value_w).len())
            .collect();
        let mut top = 0;
        while top < self.online_idx {
            let used: usize = heights[top..=self.online_idx].iter().sum();
            if used <= list_h {
                break;
            }
            top += 1;
        }
        top = clamp(top, 0, self.online_fields.len().saturating_sub(1));

        let mut used = 0;
        for (i, field) in self.online_fields.iter().enumerate().skip(top) {
            if used >= list_h {
                break;
            }
            for row in online_row_lines(field, label_w, value_w) {
                if used >= list_h {
                    break;
                }
                let plain = pad_right(&row, content_w);
                let style = if i == self.online_idx {
                    self.styles.row_sel
                } else {
                    self.styles.value
                };
                lines.push(Line::from(Span::styled(plain, style)));
                used += 1;
            }
        }
        while used < list_h {
            lines.push(Line::from(" ".repeat(content_w)));
            used += 1;
        }
        self.draw_dialog(
            frame,
            area,
            content_w,
            lines,
            self.styles.border_focused,
        );
    }

    fn draw_dialog(
        &self,
        frame: &mut Frame,
        area: Rect,
        content_w: usize,
        lines: Vec<Line<'static>>,
        border: Color,
    ) {
        let outer_w = (content_w + 4).min(area.width as usize) as u16;
        let outer_h = (lines.len() + 2).min(area.height as usize) as u16;
        if outer_w < 4 || outer_h < 3 {
            return;
        }
        let x = area.x + area.width.saturating_sub(outer_w) / 2;
        let y = area.y + area.height.saturating_sub(outer_h) / 2;
        let rect = Rect::new(x, y, outer_w, outer_h);
        frame.render_widget(Clear, rect);
        // `Clear` drops every cell back to the terminal's own colors, so put
        // the theme canvas back before drawing the dialog over it.
        frame.render_widget(Block::default().style(self.styles.base), rect);
        let block = Block::default()
            .borders(Borders::ALL)
            .border_type(BorderType::Rounded)
            .border_style(Style::default().fg(border))
            .padding(Padding::horizontal(1));
        frame.render_widget(block, rect);
        let inner = Rect::new(
            rect.x + 2,
            rect.y + 1,
            rect.width.saturating_sub(4),
            rect.height.saturating_sub(2),
        );
        frame.render_widget(Paragraph::new(Text::from(lines)), inner);
    }

    fn draw_footer(&self, frame: &mut Frame, area: Rect) {
        let bindings: Vec<(&str, &str)> = if self.help_open {
            vec![("?/esc", "close"), ("↑↓", "scroll"), ("pgup/pgdn", "page")]
        } else if self.confirm_delete {
            vec![("y/enter", "delete"), ("n/esc", "cancel")]
        } else if self.sort_open {
            vec![("↑↓", "choose"), ("enter", "apply"), ("esc", "cancel")]
        } else if self.theme_open {
            vec![("↑↓", "preview"), ("enter", "apply"), ("esc", "cancel")]
        } else if self.online_open {
            vec![
                ("space", "toggle"),
                ("a", "all"),
                ("n", "none"),
                ("enter", "apply"),
                ("esc", "cancel"),
            ]
        } else if self.selected_unreadable() && !self.bulk_active() {
            vec![
                ("r", "repair"),
                ("↑↓", "browse"),
                ("tab", "pane"),
                ("q", "quit"),
            ]
        } else if self.bulk_active() && self.pane == Pane::Detail {
            vec![
                ("↑↓", "field"),
                ("e", "edit"),
                ("a", "add"),
                ("d", "clear"),
                ("?", "help"),
                ("esc", "back"),
                ("q", "quit"),
            ]
        } else if self.pane == Pane::List {
            vec![
                ("↑↓", "browse"),
                ("space", "select"),
                ("/", "search"),
                ("?", "help"),
                ("enter", "metadata"),
                ("tab", "pane"),
                ("q", "quit"),
            ]
        } else {
            vec![
                ("↑↓", "field"),
                ("e", "edit"),
                ("a", "add"),
                ("d", "delete"),
                ("?", "help"),
                ("/", "search"),
                ("tab", "pane"),
                ("q", "quit"),
            ]
        };
    frame.render_widget(
        Paragraph::new(fit_bindings(&self.styles, self.width, &bindings)),
        area,
    );
}
}

/// One display row of the multi-line editor: the logical line it comes from,
/// the column it starts at, and the characters that fit the box width.
struct AreaRow {
    row: usize,
    start: usize,
    chars: Vec<char>,
}

/// Soft-wrap the editor's logical lines so a long paragraph stays visible.
/// Rows break after the last space that fits, or mid-word when a word is wider
/// than the box. Every character is kept, so the editor shows exactly what it
/// will save.
fn area_rows(
    lines: &[Vec<char>],
    cursor: (usize, usize),
    width: usize,
) -> Vec<AreaRow> {
    let width = width.max(1);
    let width_of = |chars: &[char]| {
        chars
            .iter()
            .map(|c| UnicodeWidthChar::width(*c).unwrap_or(0))
            .sum::<usize>()
    };
    let mut rows = Vec::new();
    for (r, line) in lines.iter().enumerate() {
        let mut start = 0;
        loop {
            let mut used = 0;
            let mut end = start;
            while end < line.len() {
                let w = UnicodeWidthChar::width(line[end]).unwrap_or(0);
                if used + w > width {
                    break;
                }
                used += w;
                end += 1;
            }
            if end == start && start < line.len() {
                // A character wider than the box still needs a row of its own.
                end = start + 1;
            }
            if end < line.len() {
                if let Some(space) = (start..end).rev().find(|&i| line[i] == ' ')
                {
                    end = space + 1;
                }
            }
            rows.push(AreaRow {
                row: r,
                start,
                chars: line[start..end].to_vec(),
            });
            start = end;
            if start >= line.len() {
                break;
            }
        }
        // A cursor that sits past a full row needs a cell of its own.
        if r == cursor.0
            && cursor.1 == line.len()
            && rows
                .last()
                .is_some_and(|row| width_of(&row.chars) >= width)
        {
            rows.push(AreaRow {
                row: r,
                start: line.len(),
                chars: Vec::new(),
            });
        }
    }
    rows
}

/// Index of the display row that holds the cursor.
fn area_cursor_row(rows: &[AreaRow], cursor: (usize, usize)) -> usize {
    rows.iter()
        .rposition(|row| row.row == cursor.0 && row.start <= cursor.1)
        .unwrap_or(0)
}

fn book_count(n: usize) -> String {
    match n {
        1 => "1 book".into(),
        n => format!("{n} books"),
    }
}

/// Truncate plain text to `width` columns and append an ellipsis.
pub(crate) fn ellipsis(s: &str, width: usize) -> String {
    let s = s.replace('\n', " ");
    if width == 0 {
        return String::new();
    }
    if UnicodeWidthStr::width(s.as_str()) <= width {
        return s;
    }
    if width == 1 {
        return "…".into();
    }
    let mut used = 0;
    let mut out = String::new();
    for c in s.chars() {
        let w = UnicodeWidthChar::width(c).unwrap_or(0);
        if used + w > width - 1 {
            break;
        }
        out.push(c);
        used += w;
    }
    out.push('…');
    out
}

pub(crate) fn pad_right(s: &str, width: usize) -> String {
    let w = UnicodeWidthStr::width(s);
    if w < width {
        format!("{s}{}", " ".repeat(width - w))
    } else {
        s.to_owned()
    }
}

/// Render as many bindings as fit on one line, so narrow terminals never wrap
/// the footer.
fn fit_bindings(
    styles: &Styles,
    width: usize,
    bindings: &[(&str, &str)],
) -> Line<'static> {
    let mut spans: Vec<Span<'static>> = Vec::new();
    let mut used = 0usize;
    for (key, desc) in bindings {
        let piece_width =
            UnicodeWidthStr::width(*key) + 1 + UnicodeWidthStr::width(*desc);
        let extra = if spans.is_empty() { 0 } else { 3 };
        if !spans.is_empty() && used + piece_width + extra > width {
            break;
        }
        if !spans.is_empty() {
            spans.push(Span::styled(" · ", styles.key_sep));
            used += 3;
        }
        spans.push(Span::styled((*key).to_owned(), styles.key));
        spans.push(Span::raw(" "));
        spans.push(Span::styled((*desc).to_owned(), styles.help));
        used += piece_width;
    }
    Line::from(spans)
}

fn online_column_widths(content_w: usize) -> (usize, usize) {
    const CHECK_W: usize = 4;
    let mut label_w = clamp(content_w / 5, 8, 14);
    let mut value_w = (content_w.saturating_sub(CHECK_W + label_w + 5)) / 2;
    if value_w < 4 {
        label_w = (content_w / 4).max(4);
        value_w = (content_w.saturating_sub(CHECK_W + label_w + 5)) / 2;
    }
    (label_w, value_w)
}

fn online_row_lines(
    field: &super::app::OnlineField,
    label_w: usize,
    value_w: usize,
) -> Vec<String> {
    let current = wrap_text(&online_value(&field.current), value_w);
    let found = wrap_text(&online_value(&field.found), value_w);
    let height = current.len().max(found.len());
    let mut lines = Vec::with_capacity(height);
    for i in 0..height {
        let (check, label) = if i == 0 {
            (
                if field.selected { "  ✓ " } else { "    " },
                pad_right(&ellipsis(field.field.label(), label_w), label_w),
            )
        } else {
            ("    ", " ".repeat(label_w))
        };
        let cur = current.get(i).cloned().unwrap_or_default();
        let fnd = found.get(i).cloned().unwrap_or_default();
        lines.push(format!(
            "{check}{label}  {} │ {}",
            pad_right(&cur, value_w),
            pad_right(&fnd, value_w)
        ));
    }
    lines
}

fn online_value(value: &str) -> String {
    if value.trim().is_empty() {
        "(none)".into()
    } else {
        value.to_owned()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn area_text(
        lines: &[&str],
        cursor: (usize, usize),
        width: usize,
    ) -> Vec<String> {
        let lines: Vec<Vec<char>> =
            lines.iter().map(|line| line.chars().collect()).collect();
        area_rows(&lines, cursor, width)
            .into_iter()
            .map(|row| row.chars.iter().collect())
            .collect()
    }

    #[test]
    fn wraps_a_long_line_at_spaces() {
        assert_eq!(
            area_text(&["one two three"], (0, 13), 7),
            ["one ", "two ", "three"]
        );
    }

    #[test]
    fn wraps_a_word_wider_than_the_box() {
        assert_eq!(
            area_text(&["abcdefghij"], (0, 10), 4),
            ["abcd", "efgh", "ij"]
        );
    }

    #[test]
    fn keeps_one_row_per_empty_line() {
        assert_eq!(area_text(&["", "x"], (1, 1), 4), ["", "x"]);
    }

    #[test]
    fn cursor_past_a_full_row_gets_its_own_row() {
        let lines = vec!["abcdefgh".chars().collect::<Vec<_>>()];
        let wrapped = area_rows(&lines, (0, 8), 4);
        assert_eq!(wrapped.len(), 3);
        assert_eq!(area_cursor_row(&wrapped, (0, 8)), 2);
        assert_eq!(area_cursor_row(&wrapped, (0, 5)), 1);
        assert_eq!(area_cursor_row(&wrapped, (0, 0)), 0);
    }
}
