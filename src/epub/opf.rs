//! Parsing and in-place editing of an EPUB package document.
//!
//! The original bytes are kept and the metadata block's child elements are
//! recorded as byte spans, so an edit replaces exactly the parts that change
//! and everything else survives verbatim.

use std::collections::{HashMap, HashSet};

use anyhow::{Context, Result, bail};
use quick_xml::Reader;
use quick_xml::events::{BytesStart, Event};
use quick_xml::events::attributes::Attribute;

use super::escaping::escape_xml;
use super::metadata::{Creator, Metadata, parse_read_status};
use super::{
    READ_META_NAME, READ_NAMESPACE, READ_PREFIX, SERIES_COLLECTION_ID,
    SUBTITLE_ID,
};

/// Namespace of the package document's own elements.
pub const OPF_NS: &str = "http://www.idpf.org/2007/opf";
pub const DUBLIN_CORE_NS: &str = "http://purl.org/dc/elements/1.1/";

/// One element (or comment) directly inside `<metadata>`. The byte span points
/// into the package document, so an untouched child can be copied verbatim.
#[derive(Clone, Debug, Default)]
pub struct OpfChild {
    pub local: String,
    pub id: String,
    pub refines: String,
    pub property: String,
    pub name: String,
    pub content: String,
    pub text: String,
    pub start: usize,
    pub end: usize,
}

impl OpfChild {
    fn is_meta(&self) -> bool {
        self.local == "meta"
    }
}

/// One manifest item, enough to locate a page-list, NCX, page-map or spine
/// document.
#[derive(Clone, Debug, Default)]
pub struct ManifestItem {
    pub id: String,
    pub href: String,
    pub media_type: String,
    pub properties: String,
}

/// The spine and its `toc`/`page-map` references.
#[derive(Clone, Debug, Default)]
pub struct Spine {
    pub toc: String,
    pub page_map: String,
    pub itemrefs: Vec<String>,
}

/// A parsed package document with the byte spans an edit needs.
#[derive(Clone, Debug)]
pub struct OpfDocument {
    pub name: String,
    pub data: Vec<u8>,
    pub version: String,
    pub prefix: String,
    pub unique_identifier: String,
    pub default_ns: String,
    pub dc_declared: bool,
    pub opf_declared: bool,
    pub package_tag_end: usize,
    pub has_metadata: bool,
    pub meta_start: usize,
    pub meta_open_end: usize,
    pub meta_close_start: usize,
    pub meta_self_closing: bool,
    pub children: Vec<OpfChild>,
    pub manifest: Vec<ManifestItem>,
    pub spine: Spine,
}

fn local_of(raw: &str) -> &str {
    match raw.rfind(':') {
        Some(i) => &raw[i + 1..],
        None => raw,
    }
}

/// Parse the package document and record where the parts an edit needs live.
/// Elements are matched by local name, so namespaced and prefixed documents
/// read alike.
pub fn parse_opf(name: &str, data: &[u8]) -> Result<OpfDocument> {
    let mut doc = OpfDocument {
        name: name.to_owned(),
        data: data.to_vec(),
        version: String::new(),
        prefix: String::new(),
        unique_identifier: String::new(),
        default_ns: String::new(),
        dc_declared: false,
        opf_declared: false,
        package_tag_end: 0,
        has_metadata: false,
        meta_start: 0,
        meta_open_end: 0,
        meta_close_start: 0,
        meta_self_closing: false,
        children: Vec::new(),
        manifest: Vec::new(),
        spine: Spine::default(),
    };

    let mut reader = Reader::from_reader(data);
    reader.config_mut().trim_text(false);
    reader.config_mut().check_end_names = true;

    let mut depth = 0usize;
    let mut in_metadata = false;
    let mut in_manifest = false;
    let mut in_spine = false;
    let mut child: Option<OpfChild> = None;
    let mut saw_package = false;

    loop {
        let off = reader.buffer_position() as usize;
        let event = reader
            .read_event()
            .with_context(|| format!("{name}: invalid XML"))?;
        let after = reader.buffer_position() as usize;
        match event {
            Event::Start(start) => {
                let qname = start.name();
                let local = local_of(qname.as_ref()).to_owned();
                if depth == 0 {
                    if local != "package" {
                        bail!("root element is {local:?}, want package");
                    }
                    saw_package = true;
                    doc.package_tag_end = after;
                    read_attrs(&mut doc, &start)?;
                } else if depth == 1 && !doc.has_metadata && local == "metadata"
                {
                    doc.has_metadata = true;
                    doc.meta_start = off;
                    doc.meta_open_end = after;
                    doc.meta_self_closing =
                        is_self_closing_tag(&data[off..after]);
                    read_attrs(&mut doc, &start)?;
                    in_metadata = !doc.meta_self_closing;
                } else if in_metadata && depth == 2 {
                    child = Some(child_from_start(&local, off, &start)?);
                }
                if depth == 1 {
                    if local == "manifest" {
                        in_manifest = true;
                    } else if local == "spine" {
                        doc.spine.toc =
                            attr_local(&start, "toc")?.unwrap_or_default();
                        doc.spine.page_map =
                            attr_local(&start, "page-map")?.unwrap_or_default();
                        in_spine = true;
                    }
                } else if depth == 2 {
                    if in_manifest && local == "item" {
                        doc.manifest.push(manifest_item(&start)?);
                    } else if in_spine
                        && local == "itemref"
                        && let Some(idref) = attr_local(&start, "idref")?
                        && !idref.is_empty()
                    {
                        doc.spine.itemrefs.push(idref);
                    }
                }
                depth += 1;
            }
            Event::Empty(start) => {
                let qname = start.name();
                let local = local_of(qname.as_ref()).to_owned();
                if depth == 0 {
                    if local != "package" {
                        bail!("root element is {local:?}, want package");
                    }
                    saw_package = true;
                    doc.package_tag_end = after;
                    read_attrs(&mut doc, &start)?;
                } else if depth == 1 && !doc.has_metadata && local == "metadata"
                {
                    doc.has_metadata = true;
                    doc.meta_start = off;
                    doc.meta_open_end = after;
                    doc.meta_self_closing = true;
                    read_attrs(&mut doc, &start)?;
                } else if in_metadata && depth == 2 {
                    let mut c = child_from_start(&local, off, &start)?;
                    c.end = after;
                    doc.children.push(c);
                }
                if depth == 1 && local == "spine" {
                    doc.spine.toc =
                        attr_local(&start, "toc")?.unwrap_or_default();
                    doc.spine.page_map =
                        attr_local(&start, "page-map")?.unwrap_or_default();
                } else if depth == 2 {
                    if in_manifest && local == "item" {
                        doc.manifest.push(manifest_item(&start)?);
                    } else if in_spine
                        && local == "itemref"
                        && let Some(idref) = attr_local(&start, "idref")?
                        && !idref.is_empty()
                    {
                        doc.spine.itemrefs.push(idref);
                    }
                }
            }
            Event::End(_) => {
                depth = depth.saturating_sub(1);
                if in_metadata && depth == 1 {
                    doc.meta_close_start = off;
                    in_metadata = false;
                } else if in_metadata
                    && depth == 2
                    && let Some(mut c) = child.take()
                {
                    c.end = after;
                    doc.children.push(c);
                }
                if depth == 1 {
                    in_manifest = false;
                    in_spine = false;
                }
            }
            Event::Text(text) => {
                if let Some(c) = child.as_mut()
                    && depth == 3
                {
                    c.text.push_str(&unescape_text(text.as_ref())?);
                }
            }
            Event::GeneralRef(reference) => {
                if let Some(c) = child.as_mut()
                    && depth == 3
                {
                    resolve_reference(&mut c.text, reference.as_ref())?;
                }
            }
            Event::CData(cdata) => {
                if let Some(c) = child.as_mut()
                    && depth == 3
                {
                    c.text.push_str(cdata.as_ref());
                }
            }
            Event::Comment(_) => {
                if in_metadata && depth == 2 {
                    doc.children.push(OpfChild {
                        start: off,
                        end: after,
                        ..OpfChild::default()
                    });
                }
            }
            Event::Eof => break,
            _ => {}
        }
    }

    if !saw_package {
        bail!("no package element");
    }
    if depth != 0 {
        bail!("invalid XML: unclosed element");
    }
    Ok(doc)
}

fn resolve_reference(out: &mut String, reference: &str) -> Result<()> {
    if let Some(num) = reference.strip_prefix('#') {
        let code = if let Some(hex) = num.strip_prefix(['x', 'X']) {
            u32::from_str_radix(hex, 16).ok()
        } else {
            num.parse::<u32>().ok()
        };
        let Some(code) = code.and_then(char::from_u32) else {
            bail!("invalid character reference &{reference};");
        };
        out.push(code);
        return Ok(());
    }
    match quick_xml::escape::resolve_predefined_entity(reference) {
        Some(value) => {
            out.push_str(value);
            Ok(())
        }
        None => bail!("unknown entity &{reference};"),
    }
}

fn unescape_text(raw: &str) -> Result<String> {
    Ok(quick_xml::escape::unescape(raw)
        .context("invalid XML entity")?
        .into_owned())
}

fn child_from_start(
    local: &str,
    off: usize,
    start: &quick_xml::events::BytesStart<'_>,
) -> Result<OpfChild> {
    let mut child = OpfChild {
        local: local.to_owned(),
        start: off,
        ..OpfChild::default()
    };
    for attr in start.attributes().with_checks(false) {
        let attr = attr.context("invalid XML attribute")?;
        let key = attr.key.as_ref();
        if key.contains(':') {
            continue;
        }
        let value = attr_value(&attr)?;
        match key {
            "id" => child.id = value,
            "refines" => child.refines = value,
            "property" => child.property = value,
            "name" => child.name = value,
            "content" => child.content = value,
            _ => {}
        }
    }
    Ok(child)
}

fn attr_value(attr: &Attribute<'_>) -> Result<String> {
    Ok(attr
        .normalized_value(quick_xml::XmlVersion::Implicit1_0)
        .context("invalid XML attribute value")?
        .into_owned())
}

/// Pick the package attributes an edit needs. Attributes declared on the
/// metadata element count too, since generated children inherit them.
fn read_attrs(
    doc: &mut OpfDocument,
    start: &quick_xml::events::BytesStart<'_>,
) -> Result<()> {
    for attr in start.attributes().with_checks(false) {
        let attr = attr.context("invalid XML attribute")?;
        let raw = attr.key.as_ref();
        let value = attr_value(&attr)?;
        if raw == "xmlns" {
            doc.default_ns = value;
            continue;
        }
        if let Some(prefix) = raw.strip_prefix("xmlns:") {
            match prefix {
                "dc" => doc.dc_declared = value == DUBLIN_CORE_NS,
                "opf" => doc.opf_declared = value == OPF_NS,
                _ => {}
            }
            continue;
        }
        if raw.contains(':') {
            continue;
        }
        match raw {
            "version" => doc.version = value,
            "prefix" => doc.prefix = value,
            "unique-identifier" => doc.unique_identifier = value,
            _ => {}
        }
    }
    Ok(())
}

fn manifest_item(start: &BytesStart<'_>) -> Result<ManifestItem> {
    Ok(ManifestItem {
        id: attr_local(start, "id")?.unwrap_or_default(),
        href: attr_local(start, "href")?.unwrap_or_default(),
        media_type: attr_local(start, "media-type")?.unwrap_or_default(),
        properties: attr_local(start, "properties")?.unwrap_or_default(),
    })
}

/// Read an attribute by local name, ignoring any namespace prefix.
fn attr_local(
    start: &BytesStart<'_>,
    local: &str,
) -> Result<Option<String>> {
    for attr in start.attributes().with_checks(false) {
        let attr = attr.context("invalid XML attribute")?;
        let key = attr.key.as_ref();
        let name = match key.rfind(':') {
            Some(i) => &key[i + 1..],
            None => key,
        };
        if name == local {
            return Ok(Some(attr_value(&attr)?));
        }
    }
    Ok(None)
}

/// Whether a raw start tag ends with `/>`.
pub fn is_self_closing_tag(tag: &[u8]) -> bool {
    let trimmed = trim_end_xml_space(tag);
    trimmed.ends_with(b"/>")
}

fn trim_end_xml_space(mut s: &[u8]) -> &[u8] {
    while let Some((last, rest)) = s.split_last() {
        if is_xml_space(*last) {
            s = rest;
        } else {
            break;
        }
    }
    s
}

pub fn is_xml_space(b: u8) -> bool {
    matches!(b, b' ' | b'\t' | b'\n' | b'\r')
}

impl OpfDocument {
    /// Original bytes of a metadata child.
    pub fn raw(&self, i: usize) -> &[u8] {
        &self.data[self.children[i].start..self.children[i].end]
    }

    /// Map the parsed children onto the editable metadata subset. `name`/
    /// `content` metas win over property metas, and the EPUB 3 series
    /// collection wins over Calibre's tags.
    pub fn metadata(&self) -> Metadata {
        let mut by_local: HashMap<&str, Vec<String>> = HashMap::new();
        let mut named: HashMap<&str, String> = HashMap::new();
        let mut valued: HashMap<&str, Vec<String>> = HashMap::new();

        for c in &self.children {
            if c.local.is_empty() {
                continue;
            }
            if c.is_meta() {
                if c.property.is_empty() {
                    named.insert(&c.name, normalize_whitespace(&c.content));
                } else {
                    valued
                        .entry(&c.property)
                        .or_default()
                        .push(normalize_whitespace(&c.text));
                }
                continue;
            }
            by_local
                .entry(&c.local)
                .or_default()
                .push(normalize_whitespace(&c.text));
        }

        let first = |local: &str| -> String {
            by_local
                .get(local)
                .and_then(|v| v.first())
                .map(|v| v.trim().to_owned())
                .unwrap_or_default()
        };
        let meta_value = |name: &str| -> String {
            if let Some(value) = named.get(name) {
                return value.trim().to_owned();
            }
            valued
                .get(name)
                .and_then(|v| v.first())
                .map(|v| v.trim().to_owned())
                .unwrap_or_default()
        };

        let creators = by_local
            .get("creator")
            .map(|names| {
                names
                    .iter()
                    .map(|n| n.trim())
                    .filter(|n| !n.is_empty())
                    .map(Creator::new)
                    .collect()
            })
            .unwrap_or_default();

        let mut series = meta_value("calibre:series");
        let mut series_index = meta_value("calibre:series_index");
        if let Some((name, position)) = self.series_collection() {
            series = name;
            series_index = position;
        }

        let mut subtitle = first("subtitle");
        if subtitle.is_empty() {
            subtitle = meta_value("subtitle");
        }
        if subtitle.is_empty() {
            subtitle = self.refined_subtitle();
        }

        Metadata {
            title: first("title"),
            subtitle,
            creators,
            subjects: clean_list(
                by_local.get("subject").cloned().unwrap_or_default(),
            ),
            description: first("description"),
            publisher: first("publisher"),
            date: first("date"),
            language: first("language"),
            rights: first("rights"),
            identifier: first("identifier"),
            series,
            series_index,
            read: parse_read_status(&meta_value(READ_META_NAME)),
        }
    }

    /// Value of the first meta that refines `id` with the given property.
    fn refined_value(&self, id: &str, property: &str) -> String {
        if id.is_empty() {
            return String::new();
        }
        for meta in &self.children {
            if meta.is_meta()
                && meta.property == property
                && meta.refines.trim_start_matches('#') == id
            {
                return meta.text.trim().to_owned();
            }
        }
        String::new()
    }

    /// The EPUB 3 subtitle: a title refined with the title-type property set
    /// to "subtitle".
    fn refined_subtitle(&self) -> String {
        for title in &self.children {
            if title.local == "title" && self.title_is_subtitle(title) {
                return title.text.trim().to_owned();
            }
        }
        String::new()
    }

    /// The standard EPUB 3.3 series metadata: the name of a
    /// belongs-to-collection meta typed as a series and its group-position.
    fn series_collection(&self) -> Option<(String, String)> {
        for collection in &self.children {
            if !collection.is_meta()
                || collection.property != "belongs-to-collection"
            {
                continue;
            }
            let name = collection.text.trim();
            if !name.is_empty() && self.is_series_collection(collection) {
                return Some((
                    name.to_owned(),
                    self.refined_value(&collection.id, "group-position"),
                ));
            }
        }
        None
    }

    fn title_is_subtitle(&self, title: &OpfChild) -> bool {
        self.refined_value(&title.id, "title-type")
            .eq_ignore_ascii_case("subtitle")
    }

    /// A belongs-to-collection meta explicitly typed as a series. Untyped
    /// collections are preserved.
    fn is_series_collection(&self, collection: &OpfChild) -> bool {
        self.refined_value(&collection.id, "collection-type")
            .eq_ignore_ascii_case("series")
    }

    /// Qualified name for a generated meta element. It follows the document's
    /// own namespace declarations instead of assuming the OPF namespace is the
    /// default.
    pub fn meta_element_name(&self) -> &'static str {
        if self.default_ns == OPF_NS {
            "meta"
        } else {
            "opf:meta"
        }
    }

    /// Render a Dublin Core element. The `dc:` prefix is always used; the
    /// declaration is added to the package element when the document lacks one.
    pub fn dc_element(&self, local: &str, id: &str, value: &str) -> String {
        let mut out = String::from("<dc:");
        out.push_str(local);
        if !id.is_empty() {
            out.push_str(" id=\"");
            out.push_str(&escape_xml(id));
            out.push('"');
        }
        out.push('>');
        out.push_str(&escape_xml(value));
        out.push_str("</dc:");
        out.push_str(local);
        out.push('>');
        out
    }

    /// Render a legacy name/content meta element.
    pub fn meta_name(&self, name: &str, content: &str) -> String {
        format!(
            "<{} name=\"{}\" content=\"{}\"/>",
            self.meta_element_name(),
            escape_xml(name),
            escape_xml(content)
        )
    }

    /// Render a property meta element with optional id and refines.
    pub fn meta_property(
        &self,
        id: &str,
        refines: &str,
        property: &str,
        value: &str,
    ) -> String {
        let name = self.meta_element_name();
        let mut out = format!("<{name}");
        if !id.is_empty() {
            out.push_str(" id=\"");
            out.push_str(&escape_xml(id));
            out.push('"');
        }
        if !refines.is_empty() {
            out.push_str(" refines=\"");
            out.push_str(&escape_xml(refines));
            out.push('"');
        }
        out.push_str(" property=\"");
        out.push_str(&escape_xml(property));
        out.push_str("\">");
        out.push_str(&escape_xml(value));
        out.push_str("</");
        out.push_str(name);
        out.push('>');
        out
    }

    /// Whitespace that precedes the metadata element, which is also the
    /// indentation of its closing tag.
    fn indent(&self) -> Vec<u8> {
        let start = self.data[..self.meta_start]
            .iter()
            .rposition(|b| *b == b'\n')
            .map(|i| i + 1)
            .unwrap_or(0);
        let prefix = &self.data[start..self.meta_start];
        if prefix.iter().all(|b| *b == b' ' || *b == b'\t') {
            prefix.to_vec()
        } else {
            Vec::new()
        }
    }
}

fn normalize_whitespace(value: &str) -> String {
    value.split_whitespace().collect::<Vec<_>>().join(" ")
}

fn clean_list(values: Vec<String>) -> Vec<String> {
    values
        .into_iter()
        .map(|s| s.trim().to_owned())
        .filter(|s| !s.is_empty())
        .collect()
}

/// Changes to a package document: children to drop, child text to replace, and
/// elements to append to the metadata block.
struct OpfEdit<'a> {
    doc: &'a OpfDocument,
    remove: Vec<bool>,
    replace: Vec<Option<Vec<u8>>>,
    append: Vec<String>,
    removed_ids: HashSet<String>,
    need_dc: bool,
    need_opf: bool,
    need_prefix: bool,
}

impl<'a> OpfEdit<'a> {
    fn new(doc: &'a OpfDocument) -> Self {
        let n = doc.children.len();
        Self {
            doc,
            remove: vec![false; n],
            replace: vec![None; n],
            append: Vec::new(),
            removed_ids: HashSet::new(),
            need_dc: false,
            need_opf: false,
            need_prefix: false,
        }
    }

    /// Remove a child and remember the id it defined, so metas that refine it
    /// can be dropped too instead of dangling.
    fn drop_child(&mut self, i: usize) {
        self.remove[i] = true;
        let id = &self.doc.children[i].id;
        if !id.is_empty() {
            self.removed_ids.insert(id.clone());
        }
    }

    fn add(&mut self, raw: String, dc: bool) {
        self.append.push(raw);
        if dc {
            self.need_dc = true;
        } else {
            self.need_opf = true;
        }
    }

    fn set_meta_name(&mut self, name: &str, value: &str) {
        for (i, c) in self.doc.children.iter().enumerate() {
            if c.is_meta() && c.name == name {
                self.drop_child(i);
            }
        }
        if !value.is_empty() {
            self.add(self.doc.meta_name(name, value), false);
        }
    }

    fn drop_all(&mut self, local: &str) {
        for i in 0..self.doc.children.len() {
            if self.doc.children[i].local == local {
                self.drop_child(i);
            }
        }
    }

    fn replace_all(&mut self, local: &str, values: &[String]) {
        self.drop_all(local);
        for value in values {
            if !value.is_empty() {
                self.add(self.doc.dc_element(local, "", value), true);
            }
        }
    }

    /// Reassemble the package document from its unchanged bytes plus the
    /// planned edits.
    fn render(&self) -> Vec<u8> {
        let doc = self.doc;
        let mut items: Vec<Vec<u8>> = Vec::new();
        for (i, c) in doc.children.iter().enumerate() {
            if self.remove[i] {
                continue;
            }
            match &self.replace[i] {
                Some(raw) => items.push(raw.clone()),
                None => items.push(doc.data[c.start..c.end].to_vec()),
            }
        }
        for raw in &self.append {
            items.push(raw.as_bytes().to_vec());
        }

        let indent = doc.indent();
        let mut unit = indent.clone();
        unit.extend_from_slice(b"  ");

        let mut content: Vec<u8> = Vec::new();
        if !items.is_empty() {
            content.push(b'\n');
            for (i, item) in items.iter().enumerate() {
                if i > 0 {
                    content.push(b'\n');
                }
                content.extend_from_slice(&unit);
                content.extend_from_slice(item);
            }
            content.push(b'\n');
            content.extend_from_slice(&indent);
        }

        if doc.meta_self_closing && items.is_empty() {
            return doc.data.clone();
        }

        let mut out: Vec<u8> = Vec::new();
        if doc.meta_self_closing {
            out.extend_from_slice(&doc.data[..doc.meta_start]);
            out.extend_from_slice(&open_metadata_tag(
                &doc.data[doc.meta_start..doc.meta_open_end],
            ));
            out.extend_from_slice(&content);
            out.extend_from_slice(b"</metadata>");
            out.extend_from_slice(&doc.data[doc.meta_open_end..]);
        } else {
            out.extend_from_slice(&doc.data[..doc.meta_open_end]);
            out.extend_from_slice(&content);
            out.extend_from_slice(&doc.data[doc.meta_close_start..]);
        }

        let need_dc = self.need_dc && !doc.dc_declared;
        let need_opf = self.need_opf
            && doc.meta_element_name() == "opf:meta"
            && !doc.opf_declared;
        if !self.need_prefix && !need_dc && !need_opf {
            return out;
        }

        let mut tag = out[..doc.package_tag_end].to_vec();
        if self.need_prefix {
            tag = set_tag_attr(
                &tag,
                "prefix",
                &merge_prefix(&doc.prefix, READ_PREFIX, READ_NAMESPACE),
            );
        }
        if need_dc {
            tag = set_tag_attr(&tag, "xmlns:dc", DUBLIN_CORE_NS);
        }
        if need_opf {
            tag = set_tag_attr(&tag, "xmlns:opf", OPF_NS);
        }
        let mut result = tag;
        result.extend_from_slice(&out[doc.package_tag_end..]);
        result
    }
}

/// Apply the difference between the original and edited metadata to the
/// package document. Only changed fields are touched: untouched elements keep
/// their original bytes, including attributes, refinements and comments.
pub fn edit_opf(
    doc: &OpfDocument,
    old: &Metadata,
    new: &Metadata,
) -> Result<Vec<u8>> {
    if !doc.has_metadata {
        bail!("package document has no metadata element");
    }
    let mut e = OpfEdit::new(doc);
    let epub2 = doc.version.starts_with('2');
    let children = &doc.children;

    let mut subtitle_dirty = new.subtitle != old.subtitle;
    if new.title != old.title {
        e.drop_all("title");
        subtitle_dirty = true;
        if !new.title.is_empty() {
            e.add(doc.dc_element("title", "", &new.title), true);
        }
    }
    if subtitle_dirty {
        for (i, c) in children.iter().enumerate() {
            if (c.local == "title" && doc.title_is_subtitle(c))
                || c.local == "subtitle"
                || (c.is_meta()
                    && (c.name == "subtitle" || c.property == "subtitle"))
            {
                e.drop_child(i);
            }
        }
        if !new.subtitle.is_empty() {
            if epub2 {
                e.add(doc.meta_name("subtitle", &new.subtitle), false);
            } else {
                e.add(
                    doc.dc_element("title", SUBTITLE_ID, &new.subtitle),
                    true,
                );
                e.add(
                    doc.meta_property(
                        "",
                        &format!("#{SUBTITLE_ID}"),
                        "title-type",
                        "subtitle",
                    ),
                    false,
                );
            }
        }
    }

    if new.creator_names() != old.creator_names() {
        e.replace_all("creator", &new.creator_names());
    }
    if new.subjects != old.subjects {
        e.replace_all("subject", &new.subjects);
    }

    let mut set_optional = |local: &str, old_value: &str, new_value: &str| {
        if old_value != new_value {
            e.replace_all(local, &[new_value.to_owned()]);
        }
    };
    set_optional("description", &old.description, &new.description);
    set_optional("publisher", &old.publisher, &new.publisher);
    set_optional("rights", &old.rights, &new.rights);
    set_optional("date", &old.date, &new.date);

    if new.language != old.language {
        e.replace_all("language", std::slice::from_ref(&new.language));
    }
    if new.identifier != old.identifier {
        e.drop_all("identifier");
        if !new.identifier.is_empty() {
            e.add(
                doc.dc_element(
                    "identifier",
                    &doc.unique_identifier,
                    &new.identifier,
                ),
                true,
            );
        }
    }

    if new.series != old.series || new.series_index != old.series_index {
        e.set_meta_name("calibre:series", &new.series);
        e.set_meta_name("calibre:series_index", &new.series_index);
        for (i, c) in children.iter().enumerate() {
            if c.is_meta()
                && c.property == "belongs-to-collection"
                && doc.is_series_collection(c)
            {
                e.drop_child(i);
            }
        }
        if !epub2 && !new.series.is_empty() {
            e.add(
                doc.meta_property(
                    SERIES_COLLECTION_ID,
                    "",
                    "belongs-to-collection",
                    &new.series,
                ),
                false,
            );
            e.add(
                doc.meta_property(
                    "",
                    &format!("#{SERIES_COLLECTION_ID}"),
                    "collection-type",
                    "series",
                ),
                false,
            );
            if !new.series_index.is_empty() {
                e.add(
                    doc.meta_property(
                        "",
                        &format!("#{SERIES_COLLECTION_ID}"),
                        "group-position",
                        &new.series_index,
                    ),
                    false,
                );
            }
        }
    }

    if new.read != old.read {
        for (i, c) in children.iter().enumerate() {
            if c.is_meta()
                && (c.name == READ_META_NAME || c.property == READ_META_NAME)
            {
                e.drop_child(i);
            }
        }
        if new.read {
            if epub2 {
                e.add(doc.meta_name(READ_META_NAME, "true"), false);
            } else {
                e.add(doc.meta_property("", "", READ_META_NAME, "true"), false);
                e.need_prefix = true;
            }
        }
    }

    // Refinements of a removed element have no target left.
    for (i, c) in children.iter().enumerate() {
        if e.remove[i] || !c.is_meta() || c.refines.is_empty() {
            continue;
        }
        if e.removed_ids.contains(c.refines.trim_start_matches('#')) {
            e.remove[i] = true;
        }
    }

    // EPUB 3 requires dcterms:modified to change whenever the package does.
    let now = chrono::Utc::now().format("%Y-%m-%dT%H:%M:%SZ").to_string();
    let mut found_modified = false;
    for i in 0..children.len() {
        let c = &children[i];
        if !c.is_meta() || c.property != "dcterms:modified" {
            continue;
        }
        if epub2 {
            e.drop_child(i);
            continue;
        }
        if !found_modified && c.refines.is_empty() {
            e.replace[i] = Some(set_element_text(doc.raw(i), &now));
            found_modified = true;
        }
    }
    if !epub2 && !found_modified {
        e.add(doc.meta_property("", "", "dcterms:modified", &now), false);
    }

    Ok(e.render())
}

fn open_metadata_tag(raw: &[u8]) -> Vec<u8> {
    let trimmed = trim_end_xml_space(raw);
    if trimmed.ends_with(b"/>") {
        let mut out = trimmed[..trimmed.len() - 2].to_vec();
        out.push(b'>');
        out
    } else {
        raw.to_vec()
    }
}

/// Replace the character data of an element, keeping its start tag attributes.
fn set_element_text(raw: &[u8], text: &str) -> Vec<u8> {
    let s = raw;
    let Some(open_end) = find_tag_end_bytes(s, 0) else {
        return raw.to_vec();
    };
    let mut j = open_end.saturating_sub(2);
    while j > 0 && is_xml_space(s[j]) {
        j -= 1;
    }
    if s[j] == b'/' {
        let mut name_end = 1;
        while name_end < s.len()
            && !is_xml_space(s[name_end])
            && s[name_end] != b'>'
            && s[name_end] != b'/'
        {
            name_end += 1;
        }
        let mut out = s[..j].to_vec();
        out.push(b'>');
        out.extend_from_slice(escape_xml(text).as_bytes());
        out.extend_from_slice(b"</");
        out.extend_from_slice(&s[1..name_end]);
        out.push(b'>');
        return out;
    }

    let Some(close_start) = find_last_subslice(s, b"</") else {
        return raw.to_vec();
    };
    if close_start < open_end {
        return raw.to_vec();
    }
    let mut out = Vec::with_capacity(s.len() + text.len());
    out.extend_from_slice(&s[..open_end]);
    out.extend_from_slice(escape_xml(text).as_bytes());
    out.extend_from_slice(&s[close_start..]);
    out
}

fn find_last_subslice(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    if needle.is_empty() || haystack.len() < needle.len() {
        return None;
    }
    (0..=haystack.len() - needle.len())
        .rev()
        .find(|&i| &haystack[i..i + needle.len()] == needle)
}

/// Add a prefix/IRI token pair to the package's `prefix` attribute.
fn merge_prefix(value: &str, prefix: &str, namespace: &str) -> String {
    let token = format!("{prefix}:");
    let fields: Vec<&str> = value.split_whitespace().collect();
    for pair in fields.chunks(2) {
        if pair.first() == Some(&token.as_str()) {
            return value.to_owned();
        }
    }
    if value.trim().is_empty() {
        format!("{token} {namespace}")
    } else {
        format!("{value} {token} {namespace}")
    }
}

/// Locate the quoted value of an unqualified attribute inside a raw start tag.
fn find_tag_attr(tag: &[u8], name: &str) -> Option<(usize, usize)> {
    let mut i = 1; // skip '<'
    while i < tag.len()
        && !is_xml_space(tag[i])
        && tag[i] != b'>'
        && tag[i] != b'/'
    {
        i += 1;
    }
    while i < tag.len() {
        while i < tag.len() && is_xml_space(tag[i]) {
            i += 1;
        }
        if i >= tag.len() {
            break;
        }
        if tag[i] == b'>' || tag[i] == b'/' {
            i += 1;
            continue;
        }
        let name_start = i;
        while i < tag.len()
            && !is_xml_space(tag[i])
            && tag[i] != b'='
            && tag[i] != b'>'
            && tag[i] != b'/'
        {
            i += 1;
        }
        let name_end = i;
        while i < tag.len() && is_xml_space(tag[i]) {
            i += 1;
        }
        if i >= tag.len() || tag[i] != b'=' {
            continue;
        }
        i += 1;
        while i < tag.len() && is_xml_space(tag[i]) {
            i += 1;
        }
        if i >= tag.len() || (tag[i] != b'"' && tag[i] != b'\'') {
            continue;
        }
        let quote = tag[i];
        i += 1;
        let value_start = i;
        while i < tag.len() && tag[i] != quote {
            i += 1;
        }
        let value_end = i;
        if &tag[name_start..name_end] == name.as_bytes() {
            return Some((value_start, value_end));
        }
        i += 1;
    }
    None
}

/// Replace an attribute value, inserting the attribute when absent.
fn set_tag_attr(tag: &[u8], name: &str, value: &str) -> Vec<u8> {
    match find_tag_attr(tag, name) {
        Some((start, end)) => {
            let mut out = Vec::with_capacity(tag.len() + value.len());
            out.extend_from_slice(&tag[..start]);
            out.extend_from_slice(escape_xml(value).as_bytes());
            out.extend_from_slice(&tag[end..]);
            out
        }
        None => {
            insert_tag_attr(tag, &format!("{name}=\"{}\"", escape_xml(value)))
        }
    }
}

/// Add an attribute just before the tag's closing bracket.
fn insert_tag_attr(tag: &[u8], attr: &str) -> Vec<u8> {
    let mut i = tag.len();
    while i > 0 && tag[i - 1] != b'>' {
        i -= 1;
    }
    i = i.saturating_sub(1);
    let mut j = i;
    if j > 0 && tag[j - 1] == b'/' {
        j -= 1;
    }
    let mut k = j;
    while k > 0 && is_xml_space(tag[k - 1]) {
        k -= 1;
    }
    let mut out = Vec::with_capacity(tag.len() + attr.len() + 2);
    out.extend_from_slice(&tag[..k]);
    out.push(b' ');
    out.extend_from_slice(attr.as_bytes());
    out.extend_from_slice(&tag[j..]);
    out
}

/// Return the index just after the closing `>` of a tag, ignoring `>` inside
/// quoted attribute values.
pub fn find_tag_end_bytes(s: &[u8], start: usize) -> Option<usize> {
    let mut quote: Option<u8> = None;
    let mut i = start + 1;
    while i < s.len() {
        let c = s[i];
        match quote {
            Some(q) => {
                if c == q {
                    quote = None;
                }
            }
            None => match c {
                b'"' | b'\'' => quote = Some(c),
                b'>' => return Some(i + 1),
                _ => {}
            },
        }
        i += 1;
    }
    None
}

/// Return the index just after the closing `>` of a tag, ignoring `>` inside
/// quoted attribute values.
pub fn find_tag_end(s: &str, start: usize) -> Option<usize> {
    find_tag_end_bytes(s.as_bytes(), start)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::epub::test_support::fixture_opf;

    #[test]
    fn reads_fixture_metadata() {
        let doc =
            parse_opf("OEBPS/content.opf", fixture_opf().as_bytes()).unwrap();
        let meta = doc.metadata();
        assert_eq!(meta.title, "The Left Hand of Darkness");
        assert_eq!(meta.creator_names(), ["Le Guin, Ursula K."]);
        assert_eq!(meta.subjects, ["Science Fiction"]);
        assert_eq!(meta.identifier, "urn:isbn:1");
        assert_eq!(meta.language, "en");
        assert_eq!(doc.version, "3.0");
        assert_eq!(doc.unique_identifier, "pub-id");
    }

    #[test]
    fn rejects_mismatched_end_tags() {
        let bad = r#"<package version="3.0"><metadata><meta name="a">x</dc:meta></metadata></package>"#;
        assert!(parse_opf("x.opf", bad.as_bytes()).is_err());
    }
}
