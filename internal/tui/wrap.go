package tui

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// wrapText hard-wraps plain text to width columns. Explicit newlines are kept
// as paragraph breaks, and words longer than the line are split so no text is
// silently dropped.
func wrapText(s string, width int) []string {
	if width < 1 {
		return []string{""}
	}
	var out []string
	for _, paragraph := range strings.Split(s, "\n") {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}

		line, lineWidth := "", 0
		for _, word := range words {
			wordWidth := runewidth.StringWidth(word)
			if line == "" {
				if wordWidth <= width {
					line, lineWidth = word, wordWidth
					continue
				}
				parts := breakWord(word, width)
				out = append(out, parts[:len(parts)-1]...)
				line = parts[len(parts)-1]
				lineWidth = runewidth.StringWidth(line)
				continue
			}

			if lineWidth+1+wordWidth <= width {
				line += " " + word
				lineWidth += 1 + wordWidth
				continue
			}

			out = append(out, line)
			if wordWidth <= width {
				line, lineWidth = word, wordWidth
				continue
			}
			parts := breakWord(word, width)
			out = append(out, parts[:len(parts)-1]...)
			line = parts[len(parts)-1]
			lineWidth = runewidth.StringWidth(line)
		}
		out = append(out, line)
	}
	return out
}

// breakWord splits a word into chunks of at most width columns.
func breakWord(word string, width int) []string {
	var parts []string
	var b strings.Builder
	used := 0
	for _, r := range word {
		w := runewidth.RuneWidth(r)
		if b.Len() > 0 && used+w > width {
			parts = append(parts, b.String())
			b.Reset()
			used = 0
		}
		b.WriteRune(r)
		used += w
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	if len(parts) == 0 {
		return []string{""}
	}
	return parts
}
