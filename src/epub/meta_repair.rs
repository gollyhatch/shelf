//! Normalization of packages where the same meta key appears in both
//! `name`/`content` and `property` forms.

use std::collections::HashSet;
use std::sync::LazyLock;

use anyhow::Result;
use regex::Regex;

use super::namespace::{END_TAG, START_TAG};
use super::opf::find_tag_end;

static META_ATTR_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r#"([A-Za-z_][A-Za-z0-9_.:-]*)\s*=\s*(?:"([^"]*)"|'([^']*)')"#)
        .expect("valid meta attribute pattern")
});

struct MetaAttr {
    name: String,
    raw: String,
    value: String,
}

/// Fix packages where the same meta key appears in both `name`/`content` and
/// `property` forms.
///
/// In EPUB 3 the name forms are converted to property forms so all values are
/// preserved. Calibre name metas are left as they are and the property
/// duplicate is removed. In EPUB 2 property metas are invalid, so those
/// duplicates are removed and the name form is kept.
pub fn repair_meta_collisions(opf: &[u8]) -> Vec<u8> {
    let (version, collisions) = match meta_collisions(opf) {
        Ok(result) => result,
        Err(_) => return opf.to_vec(),
    };
    if collisions.is_empty() {
        return opf.to_vec();
    }
    let Ok(s) = std::str::from_utf8(opf) else {
        return opf.to_vec();
    };
    let epub2 = version.starts_with('2');
    let mut out = String::with_capacity(s.len());
    let mut skip_meta = false;
    let mut i = 0;

    while i < s.len() {
        if s.as_bytes()[i] != b'<' {
            match s[i..].find('<') {
                Some(j) => {
                    if !skip_meta {
                        out.push_str(&s[i..i + j]);
                    }
                    i += j;
                }
                None => {
                    if !skip_meta {
                        out.push_str(&s[i..]);
                    }
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
                    if !skip_meta {
                        out.push_str(&s[i..end]);
                    }
                    i = end;
                }
                None => {
                    if !skip_meta {
                        out.push_str(rest);
                    }
                    break;
                }
            }
            continue;
        }
        if rest.starts_with("<![CDATA[") {
            match rest.strip_prefix("<![CDATA[").and_then(|r| r.find("]]>")) {
                Some(end) => {
                    let end = i + 9 + end + 3;
                    if !skip_meta {
                        out.push_str(&s[i..end]);
                    }
                    i = end;
                }
                None => {
                    if !skip_meta {
                        out.push_str(rest);
                    }
                    break;
                }
            }
            continue;
        }
        if rest.starts_with("<?") {
            match rest.strip_prefix("<?").and_then(|r| r.find("?>")) {
                Some(end) => {
                    let end = i + 2 + end + 2;
                    if !skip_meta {
                        out.push_str(&s[i..end]);
                    }
                    i = end;
                }
                None => {
                    if !skip_meta {
                        out.push_str(rest);
                    }
                    break;
                }
            }
            continue;
        }
        if rest.starts_with("</") {
            let Some(end) = find_tag_end(s, i) else {
                if !skip_meta {
                    out.push_str(rest);
                }
                break;
            };
            let tag = &s[i..end];
            if skip_meta {
                if END_TAG
                    .captures(tag)
                    .is_some_and(|caps| local_name(&caps[1]) == "meta")
                {
                    skip_meta = false;
                }
                i = end;
                continue;
            }
            out.push_str(tag);
            i = end;
            continue;
        }
        if rest.starts_with("<!") {
            let Some(end) = find_tag_end(s, i) else {
                if !skip_meta {
                    out.push_str(rest);
                }
                break;
            };
            if !skip_meta {
                out.push_str(&s[i..end]);
            }
            i = end;
            continue;
        }

        let Some(end) = find_tag_end(s, i) else {
            if !skip_meta {
                out.push_str(rest);
            }
            break;
        };
        let tag = &s[i..end];
        let Some(caps) = START_TAG.captures(tag) else {
            if !skip_meta {
                out.push_str(tag);
            }
            i = end;
            continue;
        };
        let qualified_name = caps[1].to_owned();
        if local_name(&qualified_name) != "meta" {
            if !skip_meta {
                out.push_str(tag);
            }
            i = end;
            continue;
        }

        let attrs_text = caps[2].to_owned();
        let attrs = parse_meta_attrs(&attrs_text);
        let (name_value, has_name) = meta_attr_value(&attrs, "name");
        let (prop_value, has_prop) = meta_attr_value(&attrs, "property");
        let key = if name_value.is_empty() {
            prop_value
        } else {
            name_value.clone()
        };
        if key.is_empty() || !collisions.contains(&key) || skip_meta {
            out.push_str(tag);
            i = end;
            continue;
        }

        let self_closing = attrs_text.trim_end().ends_with('/');
        let calibre = key.to_ascii_lowercase().starts_with("calibre:");

        if has_name && !name_value.is_empty() {
            if epub2 || calibre {
                out.push_str(tag);
                i = end;
                continue;
            }
            // EPUB 3: convert the legacy name/content form into a property
            // form, preserving the value and any other attributes.
            let mut rebuilt = String::new();
            for attr in &attrs {
                if matches!(attr.name.as_str(), "name" | "content" | "property")
                {
                    continue;
                }
                rebuilt.push(' ');
                rebuilt.push_str(&attr.raw);
            }
            rebuilt.push_str(" property=\"");
            rebuilt.push_str(&name_value);
            rebuilt.push('"');
            out.push('<');
            out.push_str(&qualified_name);
            out.push_str(&rebuilt);
            out.push('>');
            out.push_str(&attrs_value(&attrs, "content"));
            out.push_str("</");
            out.push_str(&qualified_name);
            out.push('>');
            if !self_closing {
                skip_meta = true;
            }
            i = end;
            continue;
        }

        // In EPUB 2, and for Calibre name keys that are kept, the property
        // duplicate is removed.
        if has_prop && (epub2 || calibre) {
            if !self_closing {
                skip_meta = true;
            }
            i = end;
            continue;
        }
        out.push_str(tag);
        i = end;
    }

    out.into_bytes()
}

fn meta_collisions(opf: &[u8]) -> Result<(String, HashSet<String>)> {
    let mut names: HashSet<String> = HashSet::new();
    let mut props: HashSet<String> = HashSet::new();
    let mut version = String::new();

    let mut reader = quick_xml::Reader::from_reader(opf);
    loop {
        let event = reader.read_event()?;
        let start = match &event {
            quick_xml::events::Event::Start(start)
            | quick_xml::events::Event::Empty(start) => start,
            quick_xml::events::Event::Eof => break,
            _ => continue,
        };
        match local_name(start.name().as_ref()) {
            "package" => {
                for attr in start.attributes().with_checks(false) {
                    let attr = attr?;
                    if local_name(attr.key.as_ref()) == "version" {
                        version = normalized(&attr)?.trim().to_owned();
                    }
                }
            }
            "meta" => {
                let mut name = String::new();
                let mut property = String::new();
                for attr in start.attributes().with_checks(false) {
                    let attr = attr?;
                    let value = normalized(&attr)?.trim().to_owned();
                    match local_name(attr.key.as_ref()) {
                        "name" => name = value,
                        "property" => property = value,
                        _ => {}
                    }
                }
                if !name.is_empty() {
                    names.insert(name);
                }
                if !property.is_empty() {
                    props.insert(property);
                }
            }
            _ => {}
        }
    }

    Ok((version, names.intersection(&props).cloned().collect()))
}

fn normalized(
    attr: &quick_xml::events::attributes::Attribute<'_>,
) -> Result<String> {
    Ok(attr
        .normalized_value(quick_xml::XmlVersion::Implicit1_0)?
        .into_owned())
}

pub(crate) fn local_name(name: &str) -> &str {
    match name.rfind(':') {
        Some(i) => &name[i + 1..],
        None => name,
    }
}

fn parse_meta_attrs(s: &str) -> Vec<MetaAttr> {
    META_ATTR_PATTERN
        .captures_iter(s)
        .map(|caps| {
            let raw = caps.get(0).map(|m| m.as_str()).unwrap_or_default();
            let mut value = caps
                .get(2)
                .map(|m| m.as_str())
                .unwrap_or_default()
                .to_owned();
            if value.is_empty() && raw.contains('\'') && !raw.contains('"') {
                value = caps
                    .get(3)
                    .map(|m| m.as_str())
                    .unwrap_or_default()
                    .to_owned();
            }
            MetaAttr {
                name: caps[1].to_owned(),
                raw: raw.to_owned(),
                value,
            }
        })
        .collect()
}

fn meta_attr_value(attrs: &[MetaAttr], name: &str) -> (String, bool) {
    for attr in attrs {
        if attr.name == name {
            return (attr.value.clone(), true);
        }
    }
    (String::new(), false)
}

fn attrs_value(attrs: &[MetaAttr], name: &str) -> String {
    meta_attr_value(attrs, name).0
}
