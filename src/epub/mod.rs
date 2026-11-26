//! Reading and editing the Dublin Core metadata of an EPUB file.
//!
//! Reading extracts only the OCF container and the package document from the
//! ZIP, so it does not pay for the book's images or chapters. Saving patches
//! the metadata block of the package document in place and copies every other
//! archive entry in compressed form: fields that were not edited keep their
//! original bytes, including author role/file-as attributes, refinements,
//! namespace prefixes, comments and ordering.

mod archive;
mod escaping;
mod meta_repair;
mod metadata;
mod namespace;
mod opf;
mod pages;
mod repair;

use std::collections::HashMap;
use std::fs::{self, File, OpenOptions};
use std::io::{Read, Write};
use std::path::{Path, PathBuf};
use std::sync::LazyLock;

use anyhow::{Context, Result, anyhow, bail};
use regex::Regex;
use zip::ZipArchive;

pub use metadata::{
    Creator, Field, Metadata, parse_creator, parse_creator_list,
    parse_read_status, split_list,
};
pub use pages::{PageCount, read_page_count};
pub use repair::{is_ncx, is_opf, repair};

use escaping::escape_xml;
use opf::{OpfDocument, edit_opf, parse_opf};

/// The media type every EPUB container must declare.
pub const MIMETYPE: &str = "application/epub+zip";

/// Bounds the container and package documents we decompress. Real-world OPF
/// files are far smaller; the limit only protects a scan from a malicious or
/// corrupt archive.
const MAX_METADATA_ENTRY: u64 = 8 << 20;

/// Read status is stored in shelf's own namespaced meta entry so it cannot
/// collide with Dublin Core or other official EPUB metadata.
pub(crate) const READ_META_NAME: &str = "shelf:read";
pub(crate) const READ_PREFIX: &str = "shelf";
pub(crate) const READ_NAMESPACE: &str = "https://github.com/gollyhatch/shelf#";

/// The id of the title element that holds an EPUB 3 subtitle.
pub(crate) const SUBTITLE_ID: &str = "shelf-subtitle";

/// The id of the EPUB 3.3 belongs-to-collection meta.
pub(crate) const SERIES_COLLECTION_ID: &str = "shelf-series";

/// Read the Dublin Core metadata of an EPUB without loading the rest of the
/// publication.
pub fn read_metadata(name: &Path) -> Result<Metadata> {
    let mut archive = open_archive(name)
        .map_err(|err| anyhow!("{}: {err}", base_name(name)))?;
    let doc = read_package_document(&mut archive)
        .map_err(|err| anyhow!("{}: {err}", base_name(name)))?;
    Ok(doc.metadata())
}

/// Open an EPUB archive and verify its mimetype entry.
fn open_archive(name: &Path) -> Result<ZipArchive<File>> {
    let file = File::open(name)?;
    let mut archive = ZipArchive::new(file)?;

    let mimetype = read_archive_file(&mut archive, "mimetype", 1024)?;
    if String::from_utf8_lossy(&mimetype).trim() != MIMETYPE {
        bail!("mimetype mismatch");
    }
    Ok(archive)
}

/// Read only the entries needed to find and parse the package document.
fn read_package_document(
    archive: &mut ZipArchive<File>,
) -> Result<OpfDocument> {
    let container = read_archive_file(
        archive,
        "META-INF/container.xml",
        MAX_METADATA_ENTRY,
    )
    .map_err(|err| anyhow!("META-INF/container.xml: {err}"))?;
    let opf_name = container_rootfile(&container)
        .map_err(|err| anyhow!("META-INF/container.xml: {err}"))?;
    if opf_name.trim().is_empty() {
        bail!("META-INF/container.xml has no rootfile");
    }
    let opf_name = clean_path(opf_name.trim().trim_start_matches('/'));

    let opf_data = read_archive_file(archive, &opf_name, MAX_METADATA_ENTRY)?;
    let doc = parse_opf(&opf_name, &opf_data)
        .map_err(|err| anyhow!("{opf_name}: {err}"))?;
    Ok(doc)
}

fn container_rootfile(data: &[u8]) -> Result<String> {
    let mut reader = quick_xml::Reader::from_reader(data);
    loop {
        match reader.read_event() {
            Ok(quick_xml::events::Event::Start(e))
            | Ok(quick_xml::events::Event::Empty(e)) => {
                if e.name().as_ref() == "rootfile" {
                    for attr in e.attributes().with_checks(false) {
                        let attr = attr?;
                        if attr.key.as_ref() == "full-path" {
                            return Ok(attr
                                .normalized_value(
                                    quick_xml::XmlVersion::Implicit1_0,
                                )?
                                .into_owned());
                        }
                    }
                }
            }
            Ok(quick_xml::events::Event::Eof) => break,
            Ok(_) => {}
            Err(err) => return Err(err.into()),
        }
    }
    Ok(String::new())
}

/// Clean a slash-separated archive path.
fn clean_path(path: &str) -> String {
    let mut parts: Vec<&str> = Vec::new();
    for part in path.split('/') {
        match part {
            "" | "." => {}
            ".." => {
                parts.pop();
            }
            other => parts.push(other),
        }
    }
    parts.join("/")
}

fn base_name(path: &Path) -> String {
    path.file_name()
        .map(|n| n.to_string_lossy().into_owned())
        .unwrap_or_else(|| path.to_string_lossy().into_owned())
}

/// Decompressed contents of one ZIP entry, refusing entries that exceed
/// `limit` bytes.
fn read_archive_file<R: Read + std::io::Seek>(
    archive: &mut ZipArchive<R>,
    name: &str,
    limit: u64,
) -> Result<Vec<u8>> {
    let mut entry = archive
        .by_name(name)
        .map_err(|_| anyhow!("{name}: no such file in the EPUB"))?;
    if entry.size() > limit {
        bail!("{name}: too large ({} bytes)", entry.size());
    }
    let mut data = Vec::new();
    entry
        .by_ref()
        .take(limit + 1)
        .read_to_end(&mut data)
        .with_context(|| name.to_owned())?;
    if data.len() as u64 > limit {
        bail!("{name}: too large");
    }
    Ok(data)
}

fn read_entry<R: Read + std::io::Seek>(
    archive: &mut ZipArchive<R>,
    name: &str,
) -> Result<Vec<u8>> {
    let mut entry = archive.by_name(name)?;
    let mut data = Vec::new();
    entry.read_to_end(&mut data)?;
    Ok(data)
}

/// A handle on an EPUB being edited. Field updates are collected in memory;
/// [`update`] writes the changed metadata back into the package document.
pub struct Package {
    path: PathBuf,
    pub(crate) doc: OpfDocument,
    meta: Metadata,
    orig: Metadata,
    dirty: bool,
}

impl Package {
    /// The current (possibly edited) metadata.
    pub fn metadata(&self) -> Metadata {
        self.meta.clone()
    }

    /// Replace the value of a field. An empty value removes the element(s).
    /// Multi-valued fields accept "a; b" for authors and "a, b" for tags.
    pub fn set(&mut self, field: Field, value: &str) -> Result<()> {
        self.set_with(field, value, false)
    }

    /// Append to the existing value of a field instead of replacing it.
    pub fn set_with(
        &mut self,
        field: Field,
        value: &str,
        append: bool,
    ) -> Result<()> {
        self.meta = self.meta.apply(field, value, append);
        self.dirty = true;
        Ok(())
    }

    /// Replace the whole list of authors. An empty list removes them.
    pub fn set_creators(&mut self, creators: Vec<Creator>) -> Result<()> {
        self.meta.creators = creators;
        self.dirty = true;
        Ok(())
    }

    /// Replace the whole list of tags. An empty list removes them.
    pub fn set_subjects(&mut self, subjects: Vec<String>) -> Result<()> {
        self.meta.subjects = subjects;
        self.dirty = true;
        Ok(())
    }
}

/// Read an EPUB, apply `f` to its metadata and write the result back
/// atomically, preserving the original file permissions. Entries other than the
/// package document are copied in compressed form, so they stay byte-identical.
pub fn update<F>(name: &Path, f: F) -> Result<()>
where
    F: FnOnce(&mut Package) -> Result<()>,
{
    fs::metadata(name)?;
    let mut archive = open_archive(name)
        .map_err(|err| anyhow!("{}: {err}", base_name(name)))?;
    let doc = read_package_document(&mut archive)
        .map_err(|err| anyhow!("{}: {err}", base_name(name)))?;
    let meta = doc.metadata();
    let mut package = Package {
        path: name.to_path_buf(),
        doc,
        orig: meta.clone(),
        meta,
        dirty: false,
    };
    f(&mut package)?;
    if !package.dirty {
        return Ok(());
    }
    package.save()
}

impl Package {
    fn save(self) -> Result<()> {
        let base = base_name(&self.path);
        let opf = edit_opf(&self.doc, &self.orig, &self.meta)
            .map_err(|err| anyhow!("{base}: {err}"))?;

        let file =
            File::open(&self.path).map_err(|err| anyhow!("{base}: {err}"))?;
        let mut archive =
            ZipArchive::new(file).map_err(|err| anyhow!("{base}: {err}"))?;

        let mut replace: HashMap<String, Vec<u8>> = HashMap::new();
        replace.insert(self.doc.name.clone(), opf);

        if self.meta.identifier != self.orig.identifier
            && !self.meta.identifier.is_empty()
        {
            let ncx_names: Vec<String> = archive
                .file_names()
                .filter_map(|name| name.ok())
                .filter(|name| is_ncx(name))
                .map(|name| name.into_owned())
                .collect();
            for name in ncx_names {
                let content = read_entry(&mut archive, &name)
                    .map_err(|err| anyhow!("{base}: {err}"))?;
                replace.insert(
                    name,
                    sync_ncx_identifier(&content, &self.meta.identifier),
                );
            }
        }

        let data = archive::rewrite_archive(&mut archive, &replace)
            .map_err(|err| anyhow!("{base}: {err}"))?;
        write_file_atomic(&self.path, &data)
            .map_err(|err| anyhow!("{base}: {err}"))
    }
}

static NCX_UID_TAG: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r#"<meta\b[^>]*\bname=["']dtb:uid["'][^>]*>"#)
        .expect("valid NCX uid pattern")
});
static CONTENT_ATTR: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r#"(\bcontent=)(["'])[^"']*(["'])"#).expect("valid pattern")
});

/// Point the NCX `dtb:uid` at the package identifier.
fn sync_ncx_identifier(content: &[u8], identifier: &str) -> Vec<u8> {
    let text = String::from_utf8_lossy(content);
    let quoted = escape_xml(identifier);
    let result = NCX_UID_TAG.replace_all(&text, |tag: &regex::Captures<'_>| {
        CONTENT_ATTR
            .replace_all(&tag[0], |caps: &regex::Captures<'_>| {
                format!("{}{}{}{}", &caps[1], &caps[2], quoted, &caps[3])
            })
            .into_owned()
    });
    match result {
        std::borrow::Cow::Borrowed(_) => content.to_vec(),
        std::borrow::Cow::Owned(text) => text.into_bytes(),
    }
}

/// Write `data` to `path` via a temporary file and rename so a failed write can
/// never truncate the original.
pub fn write_file_atomic(path: &Path, data: &[u8]) -> Result<()> {
    let mode = fs::metadata(path).ok().map(|m| m.permissions());
    let dir = path
        .parent()
        .filter(|p| !p.as_os_str().is_empty())
        .unwrap_or(Path::new("."));
    let (tmp_path, mut tmp) = create_temp(dir)?;

    let result = (|| -> Result<()> {
        tmp.write_all(data)?;
        tmp.sync_all()?;
        tmp.flush()?;
        drop(tmp);
        if let Some(mode) = mode {
            fs::set_permissions(&tmp_path, mode)?;
        }
        fs::rename(&tmp_path, path)?;
        Ok(())
    })();

    if result.is_err() {
        let _ = fs::remove_file(&tmp_path);
    }
    result
}

fn create_temp(dir: &Path) -> Result<(PathBuf, File)> {
    let pid = std::process::id();
    for attempt in 0..1000u32 {
        let path = dir.join(format!(".shelf-{pid}-{attempt}.tmp"));
        match OpenOptions::new().write(true).create_new(true).open(&path) {
            Ok(file) => return Ok((path, file)),
            Err(err) if err.kind() == std::io::ErrorKind::AlreadyExists => {
                continue;
            }
            Err(err) => return Err(err.into()),
        }
    }
    bail!("cannot create a temporary file in {}", dir.display())
}

#[cfg(test)]
pub mod test_support {
    use std::io::Write;
    use std::path::{Path, PathBuf};

    use zip::write::SimpleFileOptions;
    use zip::{CompressionMethod, ZipWriter};

    pub const FIXTURE_OPF: &str = r#"<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0" unique-identifier="pub-id">
  <metadata>
    <!-- extra -->
    <dc:identifier id="pub-id">urn:isbn:1</dc:identifier>
    <dc:title id="title">The Left Hand of Darkness</dc:title>
    <dc:language>en</dc:language>
    <dc:creator id="author">Le Guin, Ursula K.</dc:creator>
    <dc:subject id="sf">Science Fiction</dc:subject>
    <dc:publisher id="publisher">Ace Books</dc:publisher>
    <dc:description>A novel about a planet called Winter.</dc:description>
    <meta property="dcterms:modified">2026-01-01T00:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="chapter-1" href="chapter-1.xhtml" media-type="application/xhtml+xml"/>
    <item id="toc" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
  </manifest>
  <spine toc="toc">
    <itemref idref="chapter-1"/>
  </spine>
</package>"#;

    pub const FIXTURE_CONTAINER: &str = r#"<?xml version="1.0" encoding="UTF-8"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>"#;

    pub const FIXTURE_CHAPTER: &str = r#"<html xmlns="http://www.w3.org/1999/xhtml"><body><h1>One</h1></body></html>"#;

    pub const FIXTURE_NCX: &str = r#"<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><head></head><docTitle><text>Book</text></docTitle></ncx>"#;

    pub fn fixture_opf() -> String {
        FIXTURE_OPF.to_owned()
    }

    /// Build an EPUB from explicit `(name, content)` entries. The mimetype entry
    /// is always written first and stored.
    pub fn write_entries(
        entries: &[(&str, &str)],
    ) -> (tempfile::TempDir, PathBuf) {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("book.epub");
        let mut bytes = Vec::new();
        {
            let mut writer = ZipWriter::new(std::io::Cursor::new(&mut bytes));
            writer
                .start_file(
                    "mimetype",
                    SimpleFileOptions::default()
                        .compression_method(CompressionMethod::Stored),
                )
                .unwrap();
            writer.write_all(super::MIMETYPE.as_bytes()).unwrap();
            for (name, content) in entries {
                writer
                    .start_file(*name, SimpleFileOptions::default())
                    .unwrap();
                writer.write_all(content.as_bytes()).unwrap();
            }
            writer.finish().unwrap();
        }
        std::fs::write(&path, bytes).unwrap();
        (dir, path)
    }

    /// Build an EPUB around the given package document in a fresh temp dir.
    pub fn write_epub(opf: &str) -> (tempfile::TempDir, PathBuf) {
        write_epub_with_ncx(opf, FIXTURE_NCX)
    }

    pub fn write_epub_with_ncx(
        opf: &str,
        ncx: &str,
    ) -> (tempfile::TempDir, PathBuf) {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("book.epub");
        let mut bytes = Vec::new();
        {
            let mut writer = ZipWriter::new(std::io::Cursor::new(&mut bytes));
            writer
                .start_file(
                    "mimetype",
                    SimpleFileOptions::default()
                        .compression_method(CompressionMethod::Stored),
                )
                .unwrap();
            writer.write_all(super::MIMETYPE.as_bytes()).unwrap();
            let mut write = |name: &str, content: &str| {
                writer
                    .start_file(name, SimpleFileOptions::default())
                    .unwrap();
                writer.write_all(content.as_bytes()).unwrap();
            };
            write("META-INF/container.xml", FIXTURE_CONTAINER);
            write("OEBPS/content.opf", opf);
            write("OEBPS/chapter-1.xhtml", FIXTURE_CHAPTER);
            write("OEBPS/toc.ncx", ncx);
            writer.finish().unwrap();
        }
        std::fs::write(&path, bytes).unwrap();
        (dir, path)
    }

    pub fn make_epub() -> (tempfile::TempDir, PathBuf) {
        write_epub(&FIXTURE_OPF.replace("<!-- extra -->", ""))
    }

    pub fn make_epub_with(extra: &str) -> (tempfile::TempDir, PathBuf) {
        write_epub(&FIXTURE_OPF.replace("<!-- extra -->", extra))
    }

    pub fn make_epub2() -> (tempfile::TempDir, PathBuf) {
        let opf = FIXTURE_OPF
            .replace("version=\"3.0\"", "version=\"2.0\"")
            .replace("<!-- extra -->", "");
        write_epub_with_ncx(
            &opf,
            r#"<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><head><meta name="dtb:uid" content="urn:isbn:1"/></head><docTitle><text>Book</text></docTitle></ncx>"#,
        )
    }

    /// Content of the first archive entry ending in `suffix`.
    pub fn zip_entry(path: &Path, suffix: &str) -> Vec<u8> {
        let data = std::fs::read(path).unwrap();
        let mut archive =
            zip::ZipArchive::new(std::io::Cursor::new(data)).unwrap();
        for i in 0..archive.len() {
            let mut entry = archive.by_index(i).unwrap();
            let name = entry.name().map(|n| n.into_owned()).unwrap_or_default();
            if name.ends_with(suffix) {
                let mut out = Vec::new();
                std::io::Read::read_to_end(&mut entry, &mut out).unwrap();
                return out;
            }
        }
        panic!("no entry ending in {suffix}");
    }

    pub fn set_field(path: &Path, field: super::Field, value: &str) {
        super::update(path, |p| p.set(field, value)).unwrap();
    }
}

#[cfg(test)]
mod tests {
    use std::io::Read;

    use super::test_support::*;
    use super::*;

    #[test]
    fn update_round_trip() {
        let (_dir, path) = make_epub();
        update(&path, |p| {
            p.set(Field::Title, "There and Back Again")?;
            p.set(Field::Subtitle, "A Hobbit's Tale")?;
            p.set(Field::Tags, "fantasy, classic")
        })
        .unwrap();

        let meta = read_metadata(&path).unwrap();
        assert_eq!(meta.title, "There and Back Again");
        assert_eq!(meta.subtitle, "A Hobbit's Tale");
        assert_eq!(meta.subjects, ["fantasy", "classic"]);
        assert_eq!(meta.publisher, "Ace Books");
        assert_eq!(meta.identifier, "urn:isbn:1");
    }

    #[test]
    fn series_round_trip() {
        let (_dir, path) = make_epub();
        update(&path, |p| {
            p.set(Field::Series, "The Culture")?;
            p.set(Field::SeriesIndex, "3")
        })
        .unwrap();
        let meta = read_metadata(&path).unwrap();
        assert_eq!(meta.series, "The Culture");
        assert_eq!(meta.series_index, "3");

        set_field(&path, Field::Series, "");
        let got = read_metadata(&path).unwrap();
        assert_eq!(got.series, "");
        assert_eq!(got.series_index, "3");
    }

    #[test]
    fn set_creators_replaces() {
        let (_dir, path) = make_epub();
        update(&path, |p| {
            p.set_creators(vec![
                Creator::new("Jane Doe"),
                Creator::new("John Roe"),
            ])
        })
        .unwrap();
        assert_eq!(
            read_metadata(&path).unwrap().creator_names(),
            ["Jane Doe", "John Roe"]
        );
    }

    #[test]
    fn append_authors() {
        let (_dir, path) = make_epub();
        update(&path, |p| {
            p.set_with(Field::Author, "Christopher Tolkien", true)
        })
        .unwrap();
        assert_eq!(
            read_metadata(&path).unwrap().creator_names(),
            ["Le Guin, Ursula K.", "Christopher Tolkien"]
        );
    }

    #[test]
    fn clear_field() {
        let (_dir, path) = make_epub();
        set_field(&path, Field::Publisher, "");
        let meta = read_metadata(&path).unwrap();
        assert_eq!(meta.publisher, "");
        assert!(!meta.title.is_empty());
    }

    #[test]
    fn no_change_does_not_write() {
        let (_dir, path) = make_epub();
        let before = std::fs::read(&path).unwrap();
        update(&path, |_| Ok(())).unwrap();
        assert_eq!(std::fs::read(&path).unwrap(), before);
    }

    #[test]
    fn permission_preserved() {
        use std::os::unix::fs::PermissionsExt;
        let (_dir, path) = make_epub();
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o600))
            .unwrap();
        set_field(&path, Field::Title, "Renamed");
        let mode =
            std::fs::metadata(&path).unwrap().permissions().mode() & 0o777;
        assert_eq!(mode, 0o600);
    }

    #[test]
    fn save_keeps_other_entries_verbatim() {
        let (_dir, path) = make_epub();
        let before = std::fs::read(&path).unwrap();
        let compressed = raw_entries(&before);
        let before_opf = zip_entry(&path, ".opf");

        set_field(&path, Field::Publisher, "Orbit");
        let after = std::fs::read(&path).unwrap();
        for (name, bytes) in raw_entries(&after) {
            let want = compressed
                .get(&name)
                .unwrap_or_else(|| panic!("{name} appeared after the save"));
            if name != "OEBPS/content.opf" {
                assert_eq!(&bytes, want, "{name} was rewritten");
            }
        }

        let after_opf = zip_entry(&path, ".opf");
        let head = |b: &[u8]| b[..find(b, b"<metadata").unwrap()].to_vec();
        let tail = |b: &[u8]| b[find(b, b"</metadata>").unwrap()..].to_vec();
        assert_eq!(head(&before_opf), head(&after_opf));
        assert_eq!(tail(&before_opf), tail(&after_opf));
        assert!(
            String::from_utf8_lossy(&after_opf)
                .contains("<dc:publisher>Orbit</dc:publisher>")
        );
    }

    fn find(haystack: &[u8], needle: &[u8]) -> Option<usize> {
        haystack.windows(needle.len()).position(|w| w == needle)
    }

    fn raw_entries(data: &[u8]) -> std::collections::HashMap<String, Vec<u8>> {
        let mut archive =
            zip::ZipArchive::new(std::io::Cursor::new(data)).unwrap();
        let mut out = std::collections::HashMap::new();
        for i in 0..archive.len() {
            let mut entry = archive.by_index_raw(i).unwrap();
            let name = entry.name().unwrap().into_owned();
            let mut bytes = Vec::new();
            entry.read_to_end(&mut bytes).unwrap();
            out.insert(name, bytes);
        }
        out
    }

    #[test]
    fn creator_attributes_preserved() {
        let mut opf = fixture_opf().replace(
            "xmlns:dc=\"http://purl.org/dc/elements/1.1/\"",
            "xmlns:dc=\"http://purl.org/dc/elements/1.1/\" xmlns:opf=\"http://www.idpf.org/2007/opf\"",
        );
        opf = opf.replace(
            "<dc:creator id=\"author\">",
            "<dc:creator id=\"author\" opf:role=\"aut\" opf:file-as=\"Le Guin, Ursula K.\">",
        );
        opf = opf.replace(
            "<!-- extra -->",
            r##"<meta property="file-as" refines="#author">Le Guin, Ursula K.</meta>"##,
        );
        let (_dir, path) = write_epub(&opf);

        set_field(&path, Field::Publisher, "Orbit");
        let saved = String::from_utf8(zip_entry(&path, ".opf")).unwrap();
        assert!(
            saved.contains(
                "opf:role=\"aut\" opf:file-as=\"Le Guin, Ursula K.\""
            )
        );
        assert!(saved.contains("refines=\"#author\""));

        set_field(&path, Field::Author, "Jane Doe");
        let saved = String::from_utf8(zip_entry(&path, ".opf")).unwrap();
        assert!(!saved.contains("opf:role=\"aut\""));
        assert!(!saved.contains("refines=\"#author\""));
        assert!(saved.contains(">Jane Doe<"));
    }

    #[test]
    fn series_meta_shape() {
        let (_dir, path) = make_epub();
        for _ in 0..2 {
            update(&path, |p| {
                p.set(Field::Series, "Foundation")?;
                p.set(Field::SeriesIndex, "2")
            })
            .unwrap();
        }
        let saved = String::from_utf8(zip_entry(&path, ".opf")).unwrap();
        assert_eq!(
            saved.matches("name=\"calibre:series\"").count(),
            1,
            "{saved}"
        );
        assert_eq!(
            saved.matches("name=\"calibre:series_index\"").count(),
            1,
            "{saved}"
        );
        assert_eq!(
            saved.matches("property=\"belongs-to-collection\"").count(),
            1,
            "{saved}"
        );
        for want in [
            "id=\"shelf-series\"",
            ">Foundation<",
            "property=\"collection-type\"",
            ">series<",
            "property=\"group-position\"",
            ">2<",
        ] {
            assert!(saved.contains(want), "missing {want}: {saved}");
        }
    }

    #[test]
    fn series_epub2() {
        let (_dir, path) = make_epub2();
        update(&path, |p| {
            p.set(Field::Series, "Foundation")?;
            p.set(Field::SeriesIndex, "2")
        })
        .unwrap();
        let saved = String::from_utf8(zip_entry(&path, ".opf")).unwrap();
        assert!(!saved.contains("belongs-to-collection"));
        let meta = read_metadata(&path).unwrap();
        assert_eq!(
            (meta.series.as_str(), meta.series_index.as_str()),
            ("Foundation", "2")
        );
    }

    #[test]
    fn title_refinements() {
        let (_dir, path) = make_epub_with(
            r##"<meta property="file-as" refines="#title">Left Hand of Darkness, The</meta><meta property="title-type" refines="#title">main</meta>"##,
        );
        set_field(&path, Field::Publisher, "Orbit");
        assert!(
            String::from_utf8_lossy(&zip_entry(&path, ".opf"))
                .contains("refines=\"#title\"")
        );
        set_field(&path, Field::Title, "There and Back Again");
        let saved = String::from_utf8(zip_entry(&path, ".opf")).unwrap();
        assert!(!saved.contains("refines=\"#title\""));
        assert!(saved.contains(">There and Back Again<"));
    }

    #[test]
    fn subject_refinements() {
        let (_dir, path) = make_epub_with(
            r##"<meta property="authority" refines="#sf">BISAC</meta>"##,
        );
        set_field(&path, Field::Publisher, "Orbit");
        assert!(
            String::from_utf8_lossy(&zip_entry(&path, ".opf"))
                .contains("refines=\"#sf\"")
        );
        set_field(&path, Field::Tags, "fantasy, classic");
        let saved = String::from_utf8(zip_entry(&path, ".opf")).unwrap();
        assert!(!saved.contains("refines=\"#sf\""));
        assert!(saved.contains(">fantasy<") && saved.contains(">classic<"));
    }

    #[test]
    fn collection_preserved() {
        let (_dir, path) = make_epub_with(
            r##"<meta id="boxed" property="belongs-to-collection">The Boxed Set</meta><meta refines="#boxed" property="collection-type">set</meta><meta id="list" property="belongs-to-collection">Best Novels of All Time</meta>"##,
        );
        assert_eq!(read_metadata(&path).unwrap().series, "");
        set_field(&path, Field::Series, "Foundation");
        let saved = String::from_utf8(zip_entry(&path, ".opf")).unwrap();
        assert!(saved.contains(">The Boxed Set<"));
        assert!(saved.contains(">Best Novels of All Time<"));
        assert_eq!(read_metadata(&path).unwrap().series, "Foundation");
    }

    #[test]
    fn reads_standard_collection_metadata() {
        let (_dir, path) = make_epub_with(
            r##"<meta id="c1" property="belongs-to-collection">The Culture</meta><meta refines="#c1" property="collection-type">series</meta><meta refines="#c1" property="group-position">4</meta>"##,
        );
        let meta = read_metadata(&path).unwrap();
        assert_eq!(meta.series, "The Culture");
        assert_eq!(meta.series_index, "4");
    }

    #[test]
    fn subtitle_epub2() {
        let (_dir, path) = make_epub2();
        set_field(&path, Field::Subtitle, "A Novel");
        assert_eq!(read_metadata(&path).unwrap().subtitle, "A Novel");
        let saved = String::from_utf8(zip_entry(&path, ".opf")).unwrap();
        assert!(!saved.contains("<subtitle"));
        assert!(saved.contains("name=\"subtitle\""));
        assert!(!saved.contains("property=\"dcterms:modified\""));
    }

    #[test]
    fn identifier_syncs_ncx() {
        let (_dir, path) = make_epub2();
        set_field(&path, Field::Identifier, "9780141439518");
        let ncx = String::from_utf8(zip_entry(&path, ".ncx")).unwrap();
        assert!(ncx.contains("content=\"9780141439518\""));
        assert!(!ncx.contains("content=\"urn:isbn:1\""));
    }

    #[test]
    fn archive_mimetype_first_and_stored() {
        let (_dir, path) = make_epub2();
        set_field(&path, Field::Title, "Renamed");
        let data = std::fs::read(&path).unwrap();
        let mut archive =
            zip::ZipArchive::new(std::io::Cursor::new(data)).unwrap();
        let first = archive.by_index_raw(0).unwrap();
        assert_eq!(first.name().unwrap(), "mimetype");
        assert_eq!(first.compression(), zip::CompressionMethod::Stored);
        assert!(first.extra_data_fields().count() == 0);
    }

    #[test]
    fn meta_key_collisions_are_readable() {
        let (_dir, path) = make_epub_with(
            r#"<meta name="schema:accessMode" content="textual"/><meta property="schema:accessMode">visual</meta>"#,
        );
        assert_eq!(
            read_metadata(&path).unwrap().title,
            "The Left Hand of Darkness"
        );
        set_field(&path, Field::Publisher, "Orbit");
        let saved = String::from_utf8(zip_entry(&path, ".opf")).unwrap();
        assert!(saved.contains("name=\"schema:accessMode\""));
        assert!(saved.contains("property=\"schema:accessMode\""));
        assert!(saved.contains("<dc:publisher>Orbit</dc:publisher>"));
    }

    #[test]
    fn subtitle_epub3() {
        let (_dir, path) = make_epub();
        for subtitle in ["A Novel", "A Gothic Novel"] {
            set_field(&path, Field::Subtitle, subtitle);
            assert_eq!(read_metadata(&path).unwrap().subtitle, subtitle);
        }
        let saved = String::from_utf8(zip_entry(&path, ".opf")).unwrap();
        assert!(!saved.contains("<subtitle"));
        assert_eq!(
            saved.matches("id=\"shelf-subtitle\"").count(),
            1,
            "{saved}"
        );
        assert!(saved.contains("property=\"title-type\""));
        assert!(saved.contains("refines=\"#shelf-subtitle\""));
        assert!(saved.contains("dcterms:modified"));
    }

    #[test]
    fn escaped_values_round_trip() {
        let title = "A & B: \"Quotes\" <Tags>";
        let series = "S & \"T\" <U>";
        let (_dir, path) = make_epub();
        update(&path, |p| {
            p.set(Field::Title, title)?;
            p.set(Field::Series, series)
        })
        .unwrap();
        let meta = read_metadata(&path).unwrap();
        assert_eq!(meta.title, title);
        assert_eq!(meta.series, series);
        assert!(
            String::from_utf8_lossy(&zip_entry(&path, ".opf"))
                .contains("A &amp; B")
        );

        let (_dir, path) = make_epub2();
        set_field(&path, Field::Subtitle, series);
        assert_eq!(read_metadata(&path).unwrap().subtitle, series);
    }

    #[test]
    fn read_status_round_trip() {
        let (_dir, path) = make_epub();
        set_field(&path, Field::Read, "yes");
        assert!(read_metadata(&path).unwrap().read);
        let saved = String::from_utf8(zip_entry(&path, ".opf")).unwrap();
        assert!(saved.contains("property=\"shelf:read\""));
        assert!(saved.contains(
            "prefix=\"shelf: https://github.com/gollyhatch/shelf#\""
        ));

        set_field(&path, Field::Read, "no");
        assert!(!read_metadata(&path).unwrap().read);
    }

    #[test]
    fn read_status_epub2() {
        let (_dir, path) = make_epub2();
        set_field(&path, Field::Read, "yes");
        assert!(read_metadata(&path).unwrap().read);
        let saved = String::from_utf8(zip_entry(&path, ".opf")).unwrap();
        assert!(saved.contains("name=\"shelf:read\""));
        assert!(!saved.contains("property=\"shelf:read\""));
    }

    #[test]
    fn unrelated_archive_damage_is_ignored() {
        let (_dir, path) = write_entries(&[
            ("META-INF/container.xml", FIXTURE_CONTAINER),
            (
                "OEBPS/content.opf",
                &FIXTURE_OPF.replace("<!-- extra -->", ""),
            ),
            ("OEBPS/broken.png", "not a png"),
        ]);
        assert_eq!(
            read_metadata(&path).unwrap().title,
            "The Left Hand of Darkness"
        );
    }
}
