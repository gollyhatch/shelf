package cli

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// slugReplacer handles characters that need an explicit ASCII expansion.
// German umlauts are expanded the conventional way rather than just stripped.
var slugReplacer = strings.NewReplacer(
	"ä", "ae",
	"ö", "oe",
	"ü", "ue",
	"ß", "ss",
	"æ", "ae",
	"œ", "oe",
	"ø", "o",
	"ł", "l",
	"đ", "d",
	"ð", "d",
	"þ", "th",
	"ı", "i",
	"ħ", "h",
	"ŧ", "t",
	"ŋ", "n",
	"&", "-",
	"@", " at ",
	"%", " percent ",
	"$", " dollar ",
	"€", " euro ",
	"£", " pound ",
	"©", " copyright ",
	"®", " registered ",
	"™", " tm ",
	"°", " deg ",
	"+", " plus ",
	"×", "x",
	"÷", "div",
	"'", "",
	"’", "",
)

// slugify turns a metadata value into a lower-case ASCII slug.
func slugify(s string) string {
	// Compose first so decomposed umlauts are handled by the explicit map.
	s = norm.NFC.String(strings.ToLower(strings.TrimSpace(s)))
	s = slugReplacer.Replace(s)
	// Strip accents that decompose into a base letter plus a combining mark.
	s = norm.NFD.String(s)

	var b strings.Builder
	b.Grow(len(s))
	lastDash := false
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case unicode.IsSpace(r), r == '-', r == '_':
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		default:
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// dashWhitespace replaces runs of whitespace with a single dash and collapses
// dashes that end up adjacent to each other. It is used for the literal parts
// of a slugified rename pattern.
func dashWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastDash := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
			continue
		}
		if r == '-' {
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
			continue
		}
		b.WriteRune(r)
		lastDash = false
	}
	return strings.Trim(b.String(), "-")
}
