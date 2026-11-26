//! Page counts for an EPUB.
//!
//! A publisher-provided count wins: the EPUB 3 page-list, the EPUB 2 NCX
//! `pageList`, the Adobe page-map, or `dc:extent`. Reflowable books rarely
//! carry one, so those without fall back to a word-based estimate. Only the
//! publisher sources are exact; the estimate is approximate by definition.

use std::fs::File;
use std::path::Path;

use anyhow::Result;
use quick_xml::Reader;
use quick_xml::events::{BytesStart, Event};
use zip::ZipArchive;

use super::opf::OpfDocument;
use super::{
    MAX_METADATA_ENTRY, clean_path, open_archive, read_archive_file,
    read_package_document,
};

/// Words per page used by the estimate when no publisher count exists.
const WORDS_PER_PAGE: u64 = 300;

/// The page count shown in the detail pane, and whether it is an estimate.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct PageCount {
    pub pages: u32,
    pub estimated: bool,
}

impl PageCount {
    pub fn text(self) -> String {
        if self.estimated {
            format!("{} (est.)", self.pages)
        } else {
            self.pages.to_string()
        }
    }
}

/// Read a book's page count, preferring a publisher source over the estimate.
/// Returns `Ok(None)` when no source yields a positive count.
pub fn read_page_count(name: &Path) -> Result<Option<PageCount>> {
    let mut archive = open_archive(name)?;
    let doc = read_package_document(&mut archive)?;
    let opf_dir = match doc.name.rsplit_once('/') {
        Some((dir, _)) => dir,
        None => "",
    };

    if let Some(count) = nav_count(&mut archive, opf_dir, &doc) {
        return Ok(Some(count));
    }
    if let Some(count) = ncx_count(&mut archive, opf_dir, &doc) {
        return Ok(Some(count));
    }
    if let Some(count) = page_map_count(&mut archive, opf_dir, &doc) {
        return Ok(Some(count));
    }
    if let Some(count) = extent_count(&doc) {
        return Ok(Some(count));
    }
    Ok(estimate(&mut archive, opf_dir, &doc))
}

/// Build an exact count, rejecting zero.
fn exact(pages: usize) -> Option<PageCount> {
    if pages == 0 {
        return None;
    }
    Some(PageCount {
        pages: pages.min(u32::MAX as usize) as u32,
        estimated: false,
    })
}

/// Whether a whitespace-separated attribute value contains `token`.
fn has_token(value: &str, token: &str) -> bool {
    value.split_whitespace().any(|part| part == token)
}

/// Local name of an element, dropping any namespace prefix.
fn local_name(name: &str) -> &str {
    match name.rfind(':') {
        Some(i) => &name[i + 1..],
        None => name,
    }
}

/// Attribute value by local name, ignoring any prefix.
fn attr_local(start: &BytesStart<'_>, local: &str) -> Option<String> {
    for attr in start.attributes().with_checks(false).flatten() {
        if local_name(attr.key.as_ref()) != local {
            continue;
        }
        return attr
            .normalized_value(quick_xml::XmlVersion::Implicit1_0)
            .ok()
            .map(|value| value.into_owned());
    }
    None
}

/// EPUB 3: anchors inside a `nav` whose `type` names `page-list`.
fn nav_count(
    archive: &mut ZipArchive<File>,
    opf_dir: &str,
    doc: &OpfDocument,
) -> Option<PageCount> {
    let item = doc
        .manifest
        .iter()
        .find(|item| has_token(&item.properties, "nav"))?;
    let data = read_entry_opt(archive, opf_dir, &item.href)?;
    exact(count_nav_pages(&data))
}

fn count_nav_pages(data: &[u8]) -> usize {
    let mut reader = Reader::from_reader(data);
    let mut depth = 0usize;
    let mut nav_depth: Option<usize> = None;
    let mut count = 0usize;

    loop {
        match reader.read_event() {
            Ok(Event::Start(start)) => {
                let name = start.name();
                let local = local_name(name.as_ref());
                if local == "nav"
                    && nav_depth.is_none()
                    && attr_local(&start, "type")
                        .is_some_and(|value| has_token(&value, "page-list"))
                {
                    nav_depth = Some(depth);
                } else if nav_depth.is_some() && local == "a" {
                    count += 1;
                }
                depth += 1;
            }
            Ok(Event::Empty(start)) => {
                if nav_depth.is_some()
                    && local_name(start.name().as_ref()) == "a"
                {
                    count += 1;
                }
            }
            Ok(Event::End(_)) => {
                depth = depth.saturating_sub(1);
                if nav_depth == Some(depth) {
                    nav_depth = None;
                }
            }
            Ok(Event::Eof) => break,
            Err(_) => return 0,
            _ => {}
        }
    }
    count
}

/// EPUB 2: `pageTarget` elements inside the NCX `pageList`.
fn ncx_count(
    archive: &mut ZipArchive<File>,
    opf_dir: &str,
    doc: &OpfDocument,
) -> Option<PageCount> {
    let item = if doc.spine.toc.is_empty() {
        None
    } else {
        doc.manifest.iter().find(|item| item.id == doc.spine.toc)
    }
    .or_else(|| {
        doc.manifest.iter().find(|item| {
            item.media_type
                .eq_ignore_ascii_case("application/x-dtbncx+xml")
        })
    })?;
    let data = read_entry_opt(archive, opf_dir, &item.href)?;
    exact(count_in_container(&data, "pageList", "pageTarget"))
}

/// Adobe page-map: `page` elements.
fn page_map_count(
    archive: &mut ZipArchive<File>,
    opf_dir: &str,
    doc: &OpfDocument,
) -> Option<PageCount> {
    let item = if doc.spine.page_map.is_empty() {
        None
    } else {
        doc.manifest
            .iter()
            .find(|item| item.id == doc.spine.page_map)
    }
    .or_else(|| {
        doc.manifest.iter().find(|item| {
            item.media_type
                .eq_ignore_ascii_case("application/oebps-page-map+xml")
        })
    })?;
    let data = read_entry_opt(archive, opf_dir, &item.href)?;
    exact(count_elements(&data, "page"))
}

/// Count `target` elements nested somewhere inside `container`.
fn count_in_container(data: &[u8], container: &str, target: &str) -> usize {
    let mut reader = Reader::from_reader(data);
    let mut depth = 0usize;
    let mut container_depth: Option<usize> = None;
    let mut count = 0usize;

    loop {
        match reader.read_event() {
            Ok(Event::Start(start)) => {
                let name = start.name();
                let local = local_name(name.as_ref());
                if local == container && container_depth.is_none() {
                    container_depth = Some(depth);
                }
                if container_depth.is_some() && local == target {
                    count += 1;
                }
                depth += 1;
            }
            Ok(Event::Empty(start)) => {
                if container_depth.is_some()
                    && local_name(start.name().as_ref()) == target
                {
                    count += 1;
                }
            }
            Ok(Event::End(_)) => {
                depth = depth.saturating_sub(1);
                if container_depth == Some(depth) {
                    container_depth = None;
                }
            }
            Ok(Event::Eof) => break,
            Err(_) => return 0,
            _ => {}
        }
    }
    count
}

/// Count elements with the given local name anywhere in the document.
fn count_elements(data: &[u8], target: &str) -> usize {
    let mut reader = Reader::from_reader(data);
    let mut count = 0usize;

    loop {
        match reader.read_event() {
            Ok(Event::Start(start)) | Ok(Event::Empty(start)) => {
                if local_name(start.name().as_ref()) == target {
                    count += 1;
                }
            }
            Ok(Event::Eof) => break,
            Err(_) => return 0,
            _ => {}
        }
    }
    count
}

/// Dublin Core `extent`, e.g. `384 pages`.
fn extent_count(doc: &OpfDocument) -> Option<PageCount> {
    doc.children
        .iter()
        .filter(|child| child.local == "extent")
        .find_map(|child| parse_extent(&child.text))
        .and_then(|pages| exact(pages as usize))
}

fn parse_extent(text: &str) -> Option<u32> {
    let text = text.trim();
    let digits: String = text
        .chars()
        .take_while(char::is_ascii_digit)
        .collect();
    if digits.is_empty() {
        return None;
    }
    let rest = &text[digits.len()..];
    let rest = rest.trim_start_matches(|c: char| {
        c.is_whitespace() || c == ',' || c == ';'
    });
    let unit: String = rest
        .chars()
        .take_while(|c| !c.is_whitespace() && *c != ',' && *c != ';')
        .collect();
    let unit = unit.trim_end_matches('.').to_ascii_lowercase();
    if !matches!(unit.as_str(), "p" | "pp" | "page" | "pages") {
        return None;
    }
    let pages = digits.parse::<u32>().ok()?;
    (pages > 0).then_some(pages)
}

/// Estimate from the words in the spine's readable documents.
fn estimate(
    archive: &mut ZipArchive<File>,
    opf_dir: &str,
    doc: &OpfDocument,
) -> Option<PageCount> {
    let mut words = 0u64;
    for idref in &doc.spine.itemrefs {
        let Some(item) = doc.manifest.iter().find(|item| &item.id == idref)
        else {
            continue;
        };
        if has_token(&item.properties, "nav") {
            continue;
        }
        if !item.media_type.eq_ignore_ascii_case("application/xhtml+xml")
            && !item.media_type.eq_ignore_ascii_case("text/html")
        {
            continue;
        }
        let Some(data) = read_entry_opt(archive, opf_dir, &item.href) else {
            continue;
        };
        if let Some(count) = count_words(&data) {
            words += count;
        }
    }
    if words == 0 {
        return None;
    }
    let pages = words.div_ceil(WORDS_PER_PAGE);
    Some(PageCount {
        pages: pages.min(u32::MAX as u64) as u32,
        estimated: true,
    })
}

/// Count whitespace-separated words, ignoring scripts, styles and the head.
fn count_words(data: &[u8]) -> Option<u64> {
    let mut reader = Reader::from_reader(data);
    let mut skip = 0usize;
    let mut words = 0u64;

    loop {
        match reader.read_event() {
            Ok(Event::Start(start)) => {
                if skip > 0 {
                    skip += 1;
                } else if matches!(
                    local_name(start.name().as_ref()),
                    "script" | "style" | "head"
                ) {
                    skip = 1;
                }
            }
            Ok(Event::End(_)) => {
                skip = skip.saturating_sub(1);
            }
            Ok(Event::Text(text)) => {
                if skip == 0 {
                    words += run_count(text.as_ref());
                }
            }
            Ok(Event::CData(cdata)) => {
                if skip == 0 {
                    words += run_count(cdata.as_ref());
                }
            }
            Ok(Event::Eof) => break,
            Err(_) => return None,
            _ => {}
        }
    }
    Some(words)
}

/// Count runs of non-whitespace characters.
fn run_count(text: &str) -> u64 {
    let mut words = 0u64;
    let mut in_word = false;
    for ch in text.chars() {
        if ch.is_whitespace() {
            in_word = false;
        } else if !in_word {
            in_word = true;
            words += 1;
        }
    }
    words
}

/// Read a manifest entry, trying the percent-decoded name first.
fn read_entry_opt(
    archive: &mut ZipArchive<File>,
    opf_dir: &str,
    href: &str,
) -> Option<Vec<u8>> {
    let decoded = resolve_href(opf_dir, href, true);
    if let Ok(data) =
        read_archive_file(archive, &decoded, MAX_METADATA_ENTRY)
    {
        return Some(data);
    }
    let raw = resolve_href(opf_dir, href, false);
    if raw != decoded
        && let Ok(data) =
            read_archive_file(archive, &raw, MAX_METADATA_ENTRY)
    {
        return Some(data);
    }
    None
}

/// Resolve a manifest href against the package directory.
fn resolve_href(opf_dir: &str, href: &str, decode: bool) -> String {
    let path = href.split('#').next().unwrap_or("");
    let path = if decode {
        percent_decode(path)
    } else {
        path.to_owned()
    };
    let absolute = path.starts_with('/');
    let path = path.trim_start_matches('/');
    let full = if absolute || opf_dir.is_empty() {
        path.to_owned()
    } else {
        format!("{opf_dir}/{path}")
    };
    clean_path(&full)
}

/// Decode `%XX` sequences, leaving invalid ones literal.
fn percent_decode(value: &str) -> String {
    let bytes = value.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%' && i + 2 < bytes.len() {
            let hex = std::str::from_utf8(&bytes[i + 1..i + 3]).ok();
            if let Some(byte) =
                hex.and_then(|hex| u8::from_str_radix(hex, 16).ok())
            {
                out.push(byte);
                i += 3;
                continue;
            }
        }
        out.push(bytes[i]);
        i += 1;
    }
    String::from_utf8_lossy(&out).into_owned()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::epub::test_support::{FIXTURE_CONTAINER, write_entries};

    fn opf(metadata: &str, manifest: &str, spine_attrs: &str, spine: &str) -> String {
        format!(
            r#"<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0" unique-identifier="pub-id">
  <metadata>
    <dc:identifier id="pub-id">urn:isbn:1</dc:identifier>
    <dc:title>Book</dc:title>
    <dc:language>en</dc:language>
    {metadata}
  </metadata>
  <manifest>
    {manifest}
  </manifest>
  <spine{spine_attrs}>
    {spine}
  </spine>
</package>"#
        )
    }

    fn read(opf: &str, extra: &[(&str, &str)]) -> Option<PageCount> {
        let mut entries: Vec<(&str, &str)> = vec![
            ("META-INF/container.xml", FIXTURE_CONTAINER),
            ("OEBPS/content.opf", opf),
        ];
        entries.extend_from_slice(extra);
        let (_dir, path) = write_entries(&entries);
        read_page_count(&path).unwrap()
    }

    #[test]
    fn epub3_page_list() {
        let nav = r#"<html xmlns="http://www.w3.org/1999/xhtml"><body>
<nav epub:type="toc"><a href="chapter.xhtml">One</a></nav>
<nav epub:type="page-list">
  <a href="chapter.xhtml#p1">1</a>
  <a href="chapter.xhtml#p2">2</a>
  <a href="chapter.xhtml#p3">3</a>
</nav>
</body></html>"#;
        let opf = opf(
            "",
            r#"<item id="nav" href="nav%20pages.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>"#,
            "",
            r#"<itemref idref="chapter"/>"#,
        );
        let count = read(
            &opf,
            &[
                ("OEBPS/nav pages.xhtml", nav),
                (
                    "OEBPS/chapter.xhtml",
                    r#"<html><body><p>one two three</p></body></html>"#,
                ),
            ],
        );
        assert_eq!(
            count,
            Some(PageCount {
                pages: 3,
                estimated: false
            })
        );
    }

    #[test]
    fn epub2_page_list() {
        let ncx = r#"<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
<navMap><navPoint><content src="chapter.xhtml"/></navPoint></navMap>
<pageList>
  <pageTarget id="p1"><content src="chapter.xhtml#p1"/></pageTarget>
  <pageTarget id="p2"><content src="chapter.xhtml#p2"/></pageTarget>
  <pageTarget id="p3"><content src="chapter.xhtml#p3"/></pageTarget>
</pageList>
</ncx>"#;
        let opf = opf(
            "",
            r#"<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>"#,
            r#" toc="ncx""#,
            r#"<itemref idref="chapter"/>"#,
        );
        let count = read(
            &opf,
            &[
                ("OEBPS/toc.ncx", ncx),
                (
                    "OEBPS/chapter.xhtml",
                    r#"<html><body><p>one two three</p></body></html>"#,
                ),
            ],
        );
        assert_eq!(
            count,
            Some(PageCount {
                pages: 3,
                estimated: false
            })
        );
    }

    #[test]
    fn adobe_page_map() {
        let map = r#"<?xml version="1.0" encoding="UTF-8"?>
<page-map xmlns="http://www.idpf.org/2007/opf">
  <page name="1" href="chapter.xhtml#p1"/>
  <page name="2" href="chapter.xhtml#p2"/>
</page-map>"#;
        let opf = opf(
            "",
            r#"<item id="map" href="page-map.xml" media-type="application/oebps-page-map+xml"/>
    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>"#,
            r#" page-map="map""#,
            r#"<itemref idref="chapter"/>"#,
        );
        let count = read(
            &opf,
            &[
                ("OEBPS/page-map.xml", map),
                (
                    "OEBPS/chapter.xhtml",
                    r#"<html><body><p>one two three</p></body></html>"#,
                ),
            ],
        );
        assert_eq!(
            count,
            Some(PageCount {
                pages: 2,
                estimated: false
            })
        );
    }

    #[test]
    fn dc_extent() {
        let opf = opf(
            "<dc:extent>384 pages</dc:extent>",
            r#"<item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>"#,
            "",
            r#"<itemref idref="chapter"/>"#,
        );
        let count = read(
            &opf,
            &[(
                "OEBPS/chapter.xhtml",
                r#"<html><body><p>one two three</p></body></html>"#,
            )],
        );
        assert_eq!(
            count,
            Some(PageCount {
                pages: 384,
                estimated: false
            })
        );
    }

    #[test]
    fn estimate_from_words() {
        let chapter = format!(
            r#"<html><head><title>skip these words</title><style>a {{ color: red }}</style></head><body><p>{}</p></body></html>"#,
            "word ".repeat(600)
        );
        let opf = opf(
            "",
            r#"<item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>"#,
            "",
            r#"<itemref idref="chapter"/>"#,
        );
        let count = read(&opf, &[("OEBPS/chapter.xhtml", &chapter)]);
        assert_eq!(
            count,
            Some(PageCount {
                pages: 2,
                estimated: true
            })
        );
    }

    #[test]
    fn page_list_wins_over_extent() {
        let nav = r#"<html xmlns="http://www.w3.org/1999/xhtml"><body>
<nav epub:type="page-list">
  <a href="chapter.xhtml#p1">1</a>
  <a href="chapter.xhtml#p2">2</a>
</nav>
</body></html>"#;
        let opf = opf(
            "<dc:extent>999 pages</dc:extent>",
            r#"<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>"#,
            "",
            r#"<itemref idref="chapter"/>"#,
        );
        let count = read(
            &opf,
            &[
                ("OEBPS/nav.xhtml", nav),
                (
                    "OEBPS/chapter.xhtml",
                    r#"<html><body><p>one two three</p></body></html>"#,
                ),
            ],
        );
        assert_eq!(
            count,
            Some(PageCount {
                pages: 2,
                estimated: false
            })
        );
    }
}
