package epub

import (
	"regexp"
	"strings"
)

const dublinCoreNS = "http://purl.org/dc/elements/1.1/"

var (
	// startTag matches an element start tag, including self-closing tags. The
	// package documents older shelf releases wrote are generated XML, so '>'
	// only terminates a tag and never appears inside an attribute value.
	// Prefixed names are allowed so already-prefixed documents can be inspected
	// without losing track of their element stack.
	startTag = regexp.MustCompile(`<([A-Za-z_][A-Za-z0-9_.:-]*)([^>]*)>`)
	// endTag matches an element end tag.
	endTag = regexp.MustCompile(`</([A-Za-z_][A-Za-z0-9_.:-]*)\s*>`)
	// dcNamespaceAttr matches a default namespace declaration for Dublin Core,
	// with either quote style and optional whitespace.
	dcNamespaceAttr = regexp.MustCompile(`\s+xmlns\s*=\s*(?:"` + regexp.QuoteMeta(dublinCoreNS) + `"|'` + regexp.QuoteMeta(dublinCoreNS) + `')`)
)

type tagFrame struct {
	name string
	dc   bool
}

// prefixDublinCore restores the conventional dc: prefix on Dublin Core
// elements. Older shelf releases re-marshalled the package document with
// encoding/xml, which does not preserve namespace prefixes: it writes each
// element in the DC namespace with a default xmlns declaration. The resulting
// XML is namespace-correct, but readers that look elements up by qualified name
// (for example getElementsByTagName("dc:title")) then find no metadata.
//
// Tags are processed in document order with a stack so that only the closing
// tag belonging to a rewritten element is prefixed. A previous version keyed
// closing tags by local name alone: a DC element named "meta" then turned every
// ordinary </meta> into </dc:meta>, producing invalid package documents.
func prefixDublinCore(opf []byte) []byte {
	s := string(opf)
	var out strings.Builder
	out.Grow(len(s))
	var stack []tagFrame
	prefixed := false

	for i := 0; i < len(s); {
		if s[i] != '<' {
			j := strings.IndexByte(s[i:], '<')
			if j < 0 {
				out.WriteString(s[i:])
				break
			}
			out.WriteString(s[i : i+j])
			i += j
			continue
		}

		switch {
		case strings.HasPrefix(s[i:], "<!--"):
			end := strings.Index(s[i+4:], "-->")
			if end < 0 {
				out.WriteString(s[i:])
				i = len(s)
				continue
			}
			end = i + 4 + end + 3
			out.WriteString(s[i:end])
			i = end

		case strings.HasPrefix(s[i:], "<![CDATA["):
			end := strings.Index(s[i+9:], "]]>")
			if end < 0 {
				out.WriteString(s[i:])
				i = len(s)
				continue
			}
			end = i + 9 + end + 3
			out.WriteString(s[i:end])
			i = end

		case strings.HasPrefix(s[i:], "<?"):
			end := strings.Index(s[i+2:], "?>")
			if end < 0 {
				out.WriteString(s[i:])
				i = len(s)
				continue
			}
			end = i + 2 + end + 2
			out.WriteString(s[i:end])
			i = end

		case strings.HasPrefix(s[i:], "</"):
			end := findTagEnd(s, i)
			if end < 0 {
				out.WriteString(s[i:])
				i = len(s)
				continue
			}
			tag := s[i:end]
			if m := endTag.FindStringSubmatch(tag); m != nil && len(stack) > 0 {
				frame := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if frame.dc {
					out.WriteString("</dc:" + frame.name + ">")
					i = end
					continue
				}
				// Correct the malformed </dc:meta> closing tags written by
				// the old name-based rewrite.
				if strings.HasPrefix(m[1], "dc:") && frame.name == strings.TrimPrefix(m[1], "dc:") {
					out.WriteString("</" + frame.name + ">")
					i = end
					continue
				}
			}
			out.WriteString(tag)
			i = end

		case strings.HasPrefix(s[i:], "<!"):
			end := findTagEnd(s, i)
			if end < 0 {
				out.WriteString(s[i:])
				i = len(s)
				continue
			}
			out.WriteString(s[i:end])
			i = end

		default:
			end := findTagEnd(s, i)
			if end < 0 {
				out.WriteString(s[i:])
				i = len(s)
				continue
			}
			tag := s[i:end]
			m := startTag.FindStringSubmatch(tag)
			if m == nil {
				out.WriteString(tag)
				i = end
				continue
			}
			name, attrs := m[1], m[2]
			isDC := !strings.Contains(name, ":") && dcNamespaceAttr.MatchString(attrs)
			selfClosing := strings.HasSuffix(strings.TrimSpace(attrs), "/")
			if isDC {
				attrs = dcNamespaceAttr.ReplaceAllString(attrs, "")
				out.WriteString("<dc:" + name + attrs + ">")
				prefixed = true
			} else {
				out.WriteString(tag)
			}
			if !selfClosing {
				stack = append(stack, tagFrame{name: name, dc: isDC})
			}
			i = end
		}
	}

	result := out.String()
	if result == s {
		return opf
	}
	if prefixed {
		result = addDCDeclaration(result)
	}
	return []byte(result)
}

// findTagEnd returns the index just after the closing '>' of a tag, ignoring
// '>' characters inside quoted attribute values. It returns -1 when the tag is
// unterminated.
func findTagEnd(s string, start int) int {
	var quote byte
	for i := start + 1; i < len(s); i++ {
		switch c := s[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return i + 1
		}
	}
	return -1
}

// addDCDeclaration adds xmlns:dc to the metadata element (or the package
// element if there is no metadata, which would be an unusual package).
func addDCDeclaration(s string) string {
	if strings.Contains(s, "xmlns:dc=") {
		return s
	}
	for _, element := range []string{"<metadata", "<package"} {
		i := strings.Index(s, element)
		if i < 0 {
			continue
		}
		j := i + len(element)
		if j < len(s) && s[j] != ' ' && s[j] != '>' && s[j] != '/' {
			continue
		}
		return s[:j] + ` xmlns:dc="` + dublinCoreNS + `"` + s[j:]
	}
	return s
}
