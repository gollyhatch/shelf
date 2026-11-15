//! Repair of package documents written by older shelf releases.

use std::sync::LazyLock;

use regex::Regex;

use super::opf::{DUBLIN_CORE_NS, find_tag_end, is_xml_space};

pub(crate) static START_TAG: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"<([A-Za-z_][A-Za-z0-9_.:-]*)([^>]*)>")
        .expect("valid start tag pattern")
});
pub(crate) static END_TAG: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"</([A-Za-z_][A-Za-z0-9_.:-]*)\s*>")
        .expect("valid end tag pattern")
});
static DC_NAMESPACE_ATTR: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(&format!(
        r#"\s+xmlns\s*=\s*(?:"{}"|'{}')"#,
        regex::escape(DUBLIN_CORE_NS),
        regex::escape(DUBLIN_CORE_NS)
    ))
    .expect("valid namespace pattern")
});

struct TagFrame {
    name: String,
    dc: bool,
}

/// Restore the conventional `dc:` prefix on Dublin Core elements.
///
/// Older shelf releases re-marshalled the package document with Go's
/// encoding/xml, which does not preserve namespace prefixes: it writes each
/// element in the DC namespace with a default `xmlns` declaration. The result
/// is namespace-correct, but readers that look elements up by qualified name
/// (for example `getElementsByTagName("dc:title")`) find no metadata.
///
/// Tags are processed in document order with a stack so that only the closing
/// tag belonging to a rewritten element is prefixed. The old name-keyed rewrite
/// turned every `</meta>` into `</dc:meta>` when a DC element was named "meta".
pub fn prefix_dublin_core(opf: &[u8]) -> Vec<u8> {
    let Ok(s) = std::str::from_utf8(opf) else {
        return opf.to_vec();
    };
    let mut out = String::with_capacity(s.len());
    let mut stack: Vec<TagFrame> = Vec::new();
    let mut prefixed = false;
    let mut i = 0;

    while i < s.len() {
        if s.as_bytes()[i] != b'<' {
            match s[i..].find('<') {
                Some(j) => {
                    out.push_str(&s[i..i + j]);
                    i += j;
                }
                None => {
                    out.push_str(&s[i..]);
                    break;
                }
            }
            continue;
        }

        let rest = &s[i..];
        if rest.starts_with("<!--") {
            match rest.strip_prefix("<!--").and_then(|r| r.find("-->")) {
                Some(end) => {
                    let end = i + 4 + end + 3;
                    out.push_str(&s[i..end]);
                    i = end;
                }
                None => {
                    out.push_str(rest);
                    break;
                }
            }
            continue;
        }
        if rest.starts_with("<![CDATA[") {
            match rest.strip_prefix("<![CDATA[").and_then(|r| r.find("]]>")) {
                Some(end) => {
                    let end = i + 9 + end + 3;
                    out.push_str(&s[i..end]);
                    i = end;
                }
                None => {
                    out.push_str(rest);
                    break;
                }
            }
            continue;
        }
        if rest.starts_with("<?") {
            match rest.strip_prefix("<?").and_then(|r| r.find("?>")) {
                Some(end) => {
                    let end = i + 2 + end + 2;
                    out.push_str(&s[i..end]);
                    i = end;
                }
                None => {
                    out.push_str(rest);
                    break;
                }
            }
            continue;
        }
        if rest.starts_with("</") {
            let Some(end) = find_tag_end(s, i) else {
                out.push_str(rest);
                break;
            };
            let tag = &s[i..end];
            if let Some(caps) = END_TAG.captures(tag)
                && let Some(frame) = stack.pop()
            {
                if frame.dc {
                    out.push_str("</dc:");
                    out.push_str(&frame.name);
                    out.push('>');
                    i = end;
                    continue;
                }
                let name = &caps[1];
                if let Some(stripped) = name.strip_prefix("dc:")
                    && frame.name == stripped
                {
                    out.push_str("</");
                    out.push_str(&frame.name);
                    out.push('>');
                    i = end;
                    continue;
                }
            }
            out.push_str(tag);
            i = end;
            continue;
        }
        if rest.starts_with("<!") {
            let Some(end) = find_tag_end(s, i) else {
                out.push_str(rest);
                break;
            };
            out.push_str(&s[i..end]);
            i = end;
            continue;
        }

        let Some(end) = find_tag_end(s, i) else {
            out.push_str(rest);
            break;
        };
        let tag = &s[i..end];
        let Some(caps) = START_TAG.captures(tag) else {
            out.push_str(tag);
            i = end;
            continue;
        };
        let name = caps[1].to_owned();
        let mut attrs = caps[2].to_owned();
        let is_dc = !name.contains(':') && DC_NAMESPACE_ATTR.is_match(&attrs);
        let self_closing = attrs.trim_end().ends_with('/');
        if is_dc {
            attrs = DC_NAMESPACE_ATTR.replace_all(&attrs, "").into_owned();
            out.push_str("<dc:");
            out.push_str(&name);
            out.push_str(&attrs);
            out.push('>');
            prefixed = true;
        } else {
            out.push_str(tag);
        }
        if !self_closing {
            stack.push(TagFrame { name, dc: is_dc });
        }
        i = end;
    }

    let result = out;
    if result == s {
        return opf.to_vec();
    }
    if prefixed {
        return add_dc_declaration(&result).into_bytes();
    }
    result.into_bytes()
}

/// Add `xmlns:dc` to the metadata element (or the package element if there is
/// no metadata, which would be an unusual package).
fn add_dc_declaration(s: &str) -> String {
    if s.contains("xmlns:dc=") {
        return s.to_owned();
    }
    for element in ["<metadata", "<package"] {
        let Some(i) = s.find(element) else {
            continue;
        };
        let j = i + element.len();
        if let Some(c) = s.as_bytes().get(j)
            && !is_xml_space(*c)
            && *c != b'>'
            && *c != b'/'
        {
            continue;
        }
        let mut out = String::with_capacity(s.len() + 48);
        out.push_str(&s[..j]);
        out.push_str(&format!(" xmlns:dc=\"{DUBLIN_CORE_NS}\""));
        out.push_str(&s[j..]);
        return out;
    }
    s.to_owned()
}
