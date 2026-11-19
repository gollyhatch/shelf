//! Small text input and multiline editor used by the metadata editor.

use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};

/// Index of the start of the word before `from`.
fn word_left(chars: &[char], from: usize) -> usize {
    let mut i = from.min(chars.len());
    while i > 0 && chars[i - 1].is_whitespace() {
        i -= 1;
    }
    while i > 0 && !chars[i - 1].is_whitespace() {
        i -= 1;
    }
    i
}

/// Index just past the whitespace that follows the word at `from`.
fn word_right(chars: &[char], from: usize) -> usize {
    let mut i = from.min(chars.len());
    while i < chars.len() && !chars[i].is_whitespace() {
        i += 1;
    }
    while i < chars.len() && chars[i].is_whitespace() {
        i += 1;
    }
    i
}

/// Editing state for a single line of text.
#[derive(Clone, Debug, Default)]
pub struct Input {
    chars: Vec<char>,
    cursor: usize,
}

impl Input {
    pub fn value(&self) -> String {
        self.chars.iter().collect()
    }

    pub fn set_value(&mut self, value: &str) {
        self.chars = value.chars().collect();
        self.cursor = self.chars.len();
    }

    pub fn cursor_end(&mut self) {
        self.cursor = self.chars.len();
    }

    fn insert_char(&mut self, c: char) {
        self.chars.insert(self.cursor, c);
        self.cursor += 1;
    }

    fn backspace(&mut self) {
        if self.cursor > 0 {
            self.cursor -= 1;
            self.chars.remove(self.cursor);
        }
    }

    fn delete(&mut self) {
        if self.cursor < self.chars.len() {
            self.chars.remove(self.cursor);
        }
    }

    fn delete_word_left(&mut self) {
        let start = word_left(&self.chars, self.cursor);
        self.chars.drain(start..self.cursor);
        self.cursor = start;
    }

    fn delete_to_start(&mut self) {
        self.chars.drain(..self.cursor);
        self.cursor = 0;
    }

    fn delete_to_end(&mut self) {
        self.chars.truncate(self.cursor);
    }

    fn move_word_left(&mut self) {
        self.cursor = word_left(&self.chars, self.cursor);
    }

    fn move_word_right(&mut self) {
        self.cursor = word_right(&self.chars, self.cursor);
    }

    /// Apply a key press. Returns whether the key was consumed.
    pub fn handle_key(&mut self, key: KeyEvent) -> bool {
        let ctrl = key.modifiers.contains(KeyModifiers::CONTROL);
        let alt = key.modifiers.contains(KeyModifiers::ALT);
        match key.code {
            KeyCode::Char(c) if ctrl => match c.to_ascii_lowercase() {
                'w' => self.delete_word_left(),
                'u' => self.delete_to_start(),
                'k' => self.delete_to_end(),
                'a' => self.cursor = 0,
                'e' => self.cursor = self.chars.len(),
                _ => return false,
            },
            KeyCode::Char(c) if !alt => self.insert_char(c),
            KeyCode::Backspace if alt => self.delete_word_left(),
            KeyCode::Backspace => self.backspace(),
            KeyCode::Delete => self.delete(),
            KeyCode::Left if ctrl || alt => self.move_word_left(),
            KeyCode::Right if ctrl || alt => self.move_word_right(),
            KeyCode::Left => self.cursor = self.cursor.saturating_sub(1),
            KeyCode::Right => {
                self.cursor = (self.cursor + 1).min(self.chars.len())
            }
            KeyCode::Home => self.cursor = 0,
            KeyCode::End => self.cursor = self.chars.len(),
            _ => return false,
        }
        true
    }

    /// The visible slice of the input and the cursor's column inside it.
    pub fn window(&self, width: usize) -> (String, usize, bool) {
        if width == 0 {
            return (String::new(), 0, false);
        }
        let start = self.cursor.saturating_sub(width.saturating_sub(1));
        let end = (start + width).min(self.chars.len());
        let text: String = self.chars[start..end].iter().collect();
        let cursor = self.cursor - start;
        let more_right = end < self.chars.len();
        (text, cursor, more_right)
    }
}

/// Editing state for the multi-line description.
#[derive(Clone, Debug, Default)]
pub struct TextArea {
    lines: Vec<Vec<char>>,
    row: usize,
    col: usize,
}

impl TextArea {
    pub fn from_value(value: &str) -> Self {
        let mut lines: Vec<Vec<char>> =
            value.split('\n').map(|l| l.chars().collect()).collect();
        if lines.is_empty() {
            lines.push(Vec::new());
        }
        let row = lines.len().saturating_sub(1);
        let col = lines[row].len();
        Self { lines, row, col }
    }

    pub fn value(&self) -> String {
        self.lines
            .iter()
            .map(|line| line.iter().collect::<String>())
            .collect::<Vec<_>>()
            .join("\n")
    }

    pub fn lines(&self) -> &[Vec<char>] {
        &self.lines
    }

    pub fn cursor(&self) -> (usize, usize) {
        (self.row, self.col)
    }

    fn insert_char(&mut self, c: char) {
        self.lines[self.row].insert(self.col, c);
        self.col += 1;
    }

    fn insert_newline(&mut self) {
        let rest: Vec<char> = self.lines[self.row].split_off(self.col);
        self.lines.insert(self.row + 1, rest);
        self.row += 1;
        self.col = 0;
    }

    fn backspace(&mut self) {
        if self.col > 0 {
            self.col -= 1;
            self.lines[self.row].remove(self.col);
        } else if self.row > 0 {
            let line = self.lines.remove(self.row);
            self.row -= 1;
            self.col = self.lines[self.row].len();
            self.lines[self.row].extend(line);
        }
    }

    fn delete(&mut self) {
        if self.col < self.lines[self.row].len() {
            self.lines[self.row].remove(self.col);
        } else if self.row + 1 < self.lines.len() {
            let next = self.lines.remove(self.row + 1);
            self.lines[self.row].extend(next);
        }
    }

    fn move_up(&mut self) {
        if self.row > 0 {
            self.row -= 1;
            self.col = self.col.min(self.lines[self.row].len());
        }
    }

    fn move_down(&mut self) {
        if self.row + 1 < self.lines.len() {
            self.row += 1;
            self.col = self.col.min(self.lines[self.row].len());
        }
    }

    fn delete_word_left(&mut self) {
        let line = &mut self.lines[self.row];
        let start = word_left(line, self.col);
        line.drain(start..self.col);
        self.col = start;
    }

    fn delete_to_line_start(&mut self) {
        self.lines[self.row].drain(..self.col);
        self.col = 0;
    }

    fn delete_to_line_end(&mut self) {
        self.lines[self.row].truncate(self.col);
    }

    fn move_word_left(&mut self) {
        self.col = word_left(&self.lines[self.row], self.col);
    }

    fn move_word_right(&mut self) {
        self.col = word_right(&self.lines[self.row], self.col);
    }

    /// Apply a key press. Returns whether the key was consumed.
    pub fn handle_key(&mut self, key: KeyEvent) -> bool {
        let ctrl = key.modifiers.contains(KeyModifiers::CONTROL);
        let alt = key.modifiers.contains(KeyModifiers::ALT);
        match key.code {
            KeyCode::Char(c) if ctrl => match c.to_ascii_lowercase() {
                'w' => self.delete_word_left(),
                'u' => self.delete_to_line_start(),
                'k' => self.delete_to_line_end(),
                'a' => self.col = 0,
                'e' => self.col = self.lines[self.row].len(),
                _ => return false,
            },
            KeyCode::Char(c) if !ctrl => self.insert_char(c),
            KeyCode::Enter => self.insert_newline(),
            KeyCode::Backspace if alt => self.delete_word_left(),
            KeyCode::Backspace => self.backspace(),
            KeyCode::Delete => self.delete(),
            KeyCode::Left if ctrl || alt => self.move_word_left(),
            KeyCode::Right if ctrl || alt => self.move_word_right(),
            KeyCode::Left => self.col = self.col.saturating_sub(1),
            KeyCode::Right => {
                self.col = (self.col + 1).min(self.lines[self.row].len())
            }
            KeyCode::Up => self.move_up(),
            KeyCode::Down => self.move_down(),
            KeyCode::Home => self.col = 0,
            KeyCode::End => self.col = self.lines[self.row].len(),
            _ => return false,
        }
        true
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn key(code: KeyCode) -> KeyEvent {
        KeyEvent::new(code, KeyModifiers::NONE)
    }

    fn ctrl(code: KeyCode) -> KeyEvent {
        KeyEvent::new(code, KeyModifiers::CONTROL)
    }

    fn alt(code: KeyCode) -> KeyEvent {
        KeyEvent::new(code, KeyModifiers::ALT)
    }

    #[test]
    fn input_edits() {
        let mut input = Input::default();
        input.handle_key(key(KeyCode::Char('a')));
        input.handle_key(key(KeyCode::Char('b')));
        input.handle_key(key(KeyCode::Left));
        input.handle_key(key(KeyCode::Char('X')));
        assert_eq!(input.value(), "aXb");
        input.handle_key(key(KeyCode::Backspace));
        assert_eq!(input.value(), "ab");
        input.handle_key(key(KeyCode::Home));
        input.handle_key(key(KeyCode::Delete));
        assert_eq!(input.value(), "b");
    }

    #[test]
    fn input_window_tracks_cursor() {
        let mut input = Input::default();
        input.set_value("abcdefghij");
        let (text, cursor, more) = input.window(4);
        assert_eq!(text, "hij");
        assert_eq!(cursor, 3);
        assert!(!more);
    }

    #[test]
    fn input_word_keys() {
        let mut input = Input::default();
        input.set_value("one two three");
        input.handle_key(alt(KeyCode::Left));
        assert_eq!(input.cursor, 8, "alt+left moves by word");
        input.handle_key(ctrl(KeyCode::Left));
        assert_eq!(input.cursor, 4, "ctrl+left moves by word");
        input.handle_key(alt(KeyCode::Right));
        assert_eq!(input.cursor, 8, "alt+right moves by word");
        input.handle_key(ctrl(KeyCode::Char('w')));
        assert_eq!(input.value(), "one three", "ctrl+w drops the word before");
        assert_eq!(input.cursor, 4);
        input.handle_key(alt(KeyCode::Backspace));
        assert_eq!(input.value(), "three", "alt+backspace does the same");
        assert_eq!(input.cursor, 0);
        input.handle_key(ctrl(KeyCode::Char('u')));
        assert_eq!(input.value(), "three", "nothing left to cut");
        input.handle_key(ctrl(KeyCode::Char('k')));
        assert_eq!(input.value(), "");
    }

    #[test]
    fn textarea_word_keys() {
        let mut area = TextArea::from_value("one two three");
        area.handle_key(alt(KeyCode::Left));
        assert_eq!(area.cursor(), (0, 8));
        area.handle_key(ctrl(KeyCode::Left));
        assert_eq!(area.cursor(), (0, 4));
        area.handle_key(ctrl(KeyCode::Char('w')));
        assert_eq!(area.value(), "two three");
        area.handle_key(KeyEvent::new(KeyCode::End, KeyModifiers::NONE));
        area.handle_key(alt(KeyCode::Backspace));
        assert_eq!(area.value(), "two ");
        area.handle_key(ctrl(KeyCode::Char('u')));
        assert_eq!(area.value(), "");
    }

    #[test]
    fn textarea_word_edits_stay_on_their_line() {
        let mut area = TextArea::from_value("alpha beta\ngamma delta");
        area.handle_key(key(KeyCode::Up));
        assert_eq!(area.cursor(), (0, 10));
        area.handle_key(ctrl(KeyCode::Char('u')));
        assert_eq!(area.value(), "\ngamma delta");
        assert_eq!(area.cursor(), (0, 0));
        area.handle_key(ctrl(KeyCode::Char('u')));
        assert_eq!(area.value(), "\ngamma delta", "nothing left to cut");
    }

    #[test]
    fn textarea_edits_multiline() {
        let mut area = TextArea::from_value("ab\ncd");
        assert_eq!(area.cursor(), (1, 2));
        area.handle_key(key(KeyCode::Backspace));
        area.handle_key(key(KeyCode::Enter));
        assert_eq!(area.value(), "ab\nc\n");
        area.handle_key(key(KeyCode::Up));
        assert_eq!(area.cursor(), (1, 0));
    }
}
