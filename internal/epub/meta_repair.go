package epub

import (
	"bytes"
	"encoding/xml"
	"io"
	"regexp"
	"strings"
)

var metaAttrPattern = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.:-]*)\s*=\s*(?:"([^"]*)"|'([^']*)')`)

type metaAttr struct {
	name  string
	raw   string
	value string
}

// repairMetaCollisions fixes packages where the same meta key appears in both
// name/content and property forms. The EPUB library stores the name form as a
// string and then panics when it tries to append the property form as a slice.
//
// In EPUB 3 the name forms are converted to property forms so all values are
// preserved. Calibre name metas are left as they are and the property duplicate
// is removed. In EPUB 2 property metas are invalid, so those duplicates are
// removed and the name form is kept.
func repairMetaCollisions(opf []byte) []byte {
	version, collisions, err := metaCollisions(opf)
	if err != nil || len(collisions) == 0 {
		return opf
	}
	epub2 := strings.HasPrefix(version, "2")
	s := string(opf)
	var out strings.Builder
	out.Grow(len(s))
	skipMeta := false

	for i := 0; i < len(s); {
		if s[i] != '<' {
			j := strings.IndexByte(s[i:], '<')
			if j < 0 {
				if !skipMeta {
					out.WriteString(s[i:])
				}
				break
			}
			if !skipMeta {
				out.WriteString(s[i : i+j])
			}
			i += j
			continue
		}

		switch {
		case strings.HasPrefix(s[i:], "<!--"):
			end := strings.Index(s[i+4:], "-->")
			if end < 0 {
				if !skipMeta {
					out.WriteString(s[i:])
				}
				i = len(s)
				continue
			}
			end = i + 4 + end + 3
			if !skipMeta {
				out.WriteString(s[i:end])
			}
			i = end

		case strings.HasPrefix(s[i:], "<![CDATA["):
			end := strings.Index(s[i+9:], "]]>")
			if end < 0 {
				if !skipMeta {
					out.WriteString(s[i:])
				}
				i = len(s)
				continue
			}
			end = i + 9 + end + 3
			if !skipMeta {
				out.WriteString(s[i:end])
			}
			i = end

		case strings.HasPrefix(s[i:], "<?"):
			end := strings.Index(s[i+2:], "?>")
			if end < 0 {
				if !skipMeta {
					out.WriteString(s[i:])
				}
				i = len(s)
				continue
			}
			end = i + 2 + end + 2
			if !skipMeta {
				out.WriteString(s[i:end])
			}
			i = end

		case strings.HasPrefix(s[i:], "</"):
			end := findTagEnd(s, i)
			if end < 0 {
				if !skipMeta {
					out.WriteString(s[i:])
				}
				i = len(s)
				continue
			}
			tag := s[i:end]
			if skipMeta {
				if m := endTag.FindStringSubmatch(tag); m != nil && localName(m[1]) == "meta" {
					skipMeta = false
				}
				i = end
				continue
			}
			out.WriteString(tag)
			i = end

		case strings.HasPrefix(s[i:], "<!"):
			end := findTagEnd(s, i)
			if end < 0 {
				if !skipMeta {
					out.WriteString(s[i:])
				}
				i = len(s)
				continue
			}
			if !skipMeta {
				out.WriteString(s[i:end])
			}
			i = end

		default:
			end := findTagEnd(s, i)
			if end < 0 {
				if !skipMeta {
					out.WriteString(s[i:])
				}
				i = len(s)
				continue
			}
			tag := s[i:end]
			m := startTag.FindStringSubmatch(tag)
			if m == nil || localName(m[1]) != "meta" {
				if !skipMeta {
					out.WriteString(tag)
				}
				i = end
				continue
			}

			name, attrsText := m[1], m[2]
			attrs := parseMetaAttrs(attrsText)
			nameValue, hasName := metaAttrValue(attrs, "name")
			propValue, hasProp := metaAttrValue(attrs, "property")
			key := nameValue
			if key == "" {
				key = propValue
			}
			if key == "" || !collisions[key] || skipMeta {
				out.WriteString(tag)
				i = end
				continue
			}

			selfClosing := strings.HasSuffix(strings.TrimSpace(attrsText), "/")
			calibre := strings.HasPrefix(strings.ToLower(key), "calibre:")

			if hasName && nameValue != "" {
				if epub2 || calibre {
					out.WriteString(tag)
					i = end
					continue
				}
				// EPUB 3: convert the legacy name/content form into a
				// property form, preserving the value and any other attrs.
				var rebuilt strings.Builder
				for _, attr := range attrs {
					switch attr.name {
					case "name", "content", "property":
						continue
					}
					rebuilt.WriteByte(' ')
					rebuilt.WriteString(attr.raw)
				}
				rebuilt.WriteString(` property="`)
				rebuilt.WriteString(nameValue)
				rebuilt.WriteString(`"`)
				out.WriteString("<" + name + rebuilt.String() + ">")
				out.WriteString(attrsValue(attrs, "content"))
				out.WriteString("</" + name + ">")
				if !selfClosing {
					skipMeta = true
				}
				i = end
				continue
			}

			// In EPUB 2, and for Calibre name keys that are kept, the
			// property duplicate is removed.
			if hasProp && (epub2 || calibre) {
				if !selfClosing {
					skipMeta = true
				}
				i = end
				continue
			}
			out.WriteString(tag)
			i = end
		}
	}
	return []byte(out.String())
}

func metaCollisions(opf []byte) (version string, collisions map[string]bool, err error) {
	names := make(map[string]bool)
	props := make(map[string]bool)
	dec := xml.NewDecoder(bytes.NewReader(opf))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "package":
			for _, attr := range start.Attr {
				if attr.Name.Local == "version" {
					version = strings.TrimSpace(attr.Value)
				}
			}
		case "meta":
			var name, property string
			for _, attr := range start.Attr {
				switch attr.Name.Local {
				case "name":
					name = strings.TrimSpace(attr.Value)
				case "property":
					property = strings.TrimSpace(attr.Value)
				}
			}
			if name != "" {
				names[name] = true
			}
			if property != "" {
				props[property] = true
			}
		}
	}
	collisions = make(map[string]bool)
	for key := range names {
		if props[key] {
			collisions[key] = true
		}
	}
	return version, collisions, nil
}

func localName(name string) string {
	if i := strings.LastIndexByte(name, ':'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func parseMetaAttrs(s string) []metaAttr {
	matches := metaAttrPattern.FindAllStringSubmatch(s, -1)
	attrs := make([]metaAttr, 0, len(matches))
	for _, m := range matches {
		value := m[2]
		if value == "" && strings.Contains(m[0], "'") && !strings.Contains(m[0], `"`) {
			value = m[3]
		}
		attrs = append(attrs, metaAttr{name: m[1], raw: m[0], value: value})
	}
	return attrs
}

func metaAttrValue(attrs []metaAttr, name string) (string, bool) {
	for _, attr := range attrs {
		if attr.name == name {
			return attr.value, true
		}
	}
	return "", false
}

func attrsValue(attrs []metaAttr, name string) string {
	value, _ := metaAttrValue(attrs, name)
	return value
}
