//! Hard wrapping of plain text to a column width.

use unicode_width::{UnicodeWidthChar, UnicodeWidthStr};

/// Hard-wrap plain text to `width` columns. Explicit newlines are kept as
/// paragraph breaks, and words longer than the line are split so no text is
/// silently dropped.
pub fn wrap_text(text: &str, width: usize) -> Vec<String> {
    if width < 1 {
        return vec![String::new()];
    }
    let mut out = Vec::new();
    for paragraph in text.split('\n') {
        let words: Vec<&str> = paragraph.split_whitespace().collect();
        if words.is_empty() {
            out.push(String::new());
            continue;
        }

        let mut line = String::new();
        let mut line_width = 0usize;
        for word in words {
            let word_width = UnicodeWidthStr::width(word);
            if line.is_empty() {
                if word_width <= width {
                    line.push_str(word);
                    line_width = word_width;
                    continue;
                }
                let mut parts = break_word(word, width);
                let last = parts.pop().unwrap_or_default();
                out.extend(parts);
                line_width = UnicodeWidthStr::width(last.as_str());
                line = last;
                continue;
            }

            if line_width + 1 + word_width <= width {
                line.push(' ');
                line.push_str(word);
                line_width += 1 + word_width;
                continue;
            }

            out.push(std::mem::take(&mut line));
            if word_width <= width {
                line.push_str(word);
                line_width = word_width;
                continue;
            }
            let mut parts = break_word(word, width);
            let last = parts.pop().unwrap_or_default();
            out.extend(parts);
            line_width = UnicodeWidthStr::width(last.as_str());
            line = last;
        }
        out.push(line);
    }
    out
}

/// Split a word into chunks of at most `width` columns.
fn break_word(word: &str, width: usize) -> Vec<String> {
    let mut parts = Vec::new();
    let mut current = String::new();
    let mut used = 0usize;
    for c in word.chars() {
        let w = UnicodeWidthChar::width(c).unwrap_or(0);
        if !current.is_empty() && used + w > width {
            parts.push(std::mem::take(&mut current));
            used = 0;
        }
        current.push(c);
        used += w;
    }
    if !current.is_empty() {
        parts.push(current);
    }
    if parts.is_empty() {
        parts.push(String::new());
    }
    parts
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn wraps_words() {
        assert_eq!(
            wrap_text("one two three four", 9),
            ["one two", "three", "four"]
        );
    }

    #[test]
    fn keeps_paragraphs() {
        assert_eq!(wrap_text("a\n\nb", 10), ["a", "", "b"]);
    }

    #[test]
    fn breaks_long_words() {
        assert_eq!(wrap_text("abcdefghij", 4), ["abcd", "efgh", "ij"]);
    }

    #[test]
    fn counts_wide_characters() {
        let lines = wrap_text("日本 語", 5);
        assert!(
            lines
                .iter()
                .all(|line| UnicodeWidthStr::width(line.as_str()) <= 5)
        );
    }
}
