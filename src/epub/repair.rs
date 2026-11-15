//! Repair of EPUBs damaged by older shelf releases.

use std::collections::HashMap;
use std::io::{Cursor, Read};
use std::path::Path;

use anyhow::{Context, Result, bail};
use zip::ZipArchive;

use super::archive::rewrite_archive;
use super::meta_repair::repair_meta_collisions;
use super::namespace::prefix_dublin_core;
use super::write_file_atomic;

/// Rewrite the package document of an EPUB to restore the conventional `dc:`
/// prefixes on Dublin Core elements and to normalize colliding meta keys.
///
/// It exists for books damaged by older shelf releases; the current writer
/// emits prefixed elements and reads colliding metas, so repair is not a
/// prerequisite for editing. Metadata values are not changed.
///
/// Returns whether the file needed a rewrite. With `dry_run` set, the file is
/// inspected but not written.
pub fn repair(name: &Path, dry_run: bool) -> Result<bool> {
    let data = std::fs::read(name)?;
    let (fixed, changed) = repair_archive(&data)?;
    if !changed || dry_run {
        return Ok(changed);
    }
    write_file_atomic(name, &fixed)?;
    Ok(true)
}

/// Apply the package-document fixes to an EPUB archive and report whether
/// anything changed.
fn repair_archive(data: &[u8]) -> Result<(Vec<u8>, bool)> {
    let mut archive =
        ZipArchive::new(Cursor::new(data)).context("invalid archive")?;

    let opf_names: Vec<String> = archive
        .file_names()
        .filter_map(|name| name.ok())
        .filter(|name| is_opf(name))
        .map(|name| name.into_owned())
        .collect();
    if opf_names.is_empty() {
        bail!("no package document (.opf) found");
    }

    let mut replace: HashMap<String, Vec<u8>> = HashMap::new();
    for name in opf_names {
        let mut entry = archive.by_name(&name)?;
        let mut content = Vec::new();
        entry.read_to_end(&mut content)?;
        let fixed = repair_meta_collisions(&prefix_dublin_core(&content));
        if fixed != content {
            replace.insert(name, fixed);
        }
    }
    if replace.is_empty() {
        return Ok((data.to_vec(), false));
    }
    let fixed = rewrite_archive(&mut archive, &replace)?;
    Ok((fixed, true))
}

/// Whether a ZIP entry is a package document, regardless of the suffix's case.
pub fn is_opf(name: &str) -> bool {
    name.to_ascii_lowercase().ends_with(".opf")
}

/// Whether a ZIP entry is a legacy navigation document.
pub fn is_ncx(name: &str) -> bool {
    name.to_ascii_lowercase().ends_with(".ncx")
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::epub::test_support::{
        make_epub, make_epub_with, write_entries, zip_entry,
    };

    const LEGACY_OPF: &str = r#"<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="pub-id" version="3.0">
  <metadata xmlns="http://www.idpf.org/2007/opf">
    <identifier xmlns="http://purl.org/dc/elements/1.1/" id="pub-id">urn:isbn:1</identifier>
    <title xmlns="http://purl.org/dc/elements/1.1/" id="title">The Left Hand of Darkness</title>
    <creator xmlns="http://purl.org/dc/elements/1.1/" id="author">Le Guin, Ursula K.</creator>
    <language xmlns="http://purl.org/dc/elements/1.1/">en</language>
    <meta property="dcterms:modified">2026-01-01T00:00:00Z</meta>
  </metadata>
  <manifest/>
  <spine/>
</package>"#;

    #[test]
    fn rewrites_default_namespace_to_dc_prefix() {
        let opf = r#"<package version="3.0"><metadata xmlns="http://www.idpf.org/2007/opf"><title xmlns="http://purl.org/dc/elements/1.1/">T</title><meta name="a" content="b"/></metadata></package>"#;
        let fixed =
            String::from_utf8(prefix_dublin_core(opf.as_bytes())).unwrap();
        assert!(fixed.contains("<dc:title>T</dc:title>"));
        assert!(fixed.contains("<meta name=\"a\" content=\"b\"/>"));
        assert!(fixed.contains("xmlns:dc="));
    }

    #[test]
    fn repair_restores_prefixes_in_archive() {
        let (_dir, path) = write_entries(&[
            (
                "META-INF/container.xml",
                crate::epub::test_support::FIXTURE_CONTAINER,
            ),
            ("OEBPS/content.opf", LEGACY_OPF),
        ]);
        let before = std::fs::read(&path).unwrap();
        assert!(repair(&path, true).unwrap());
        assert_eq!(
            std::fs::read(&path).unwrap(),
            before,
            "dry run wrote the file"
        );

        assert!(repair(&path, false).unwrap());
        let saved = String::from_utf8(zip_entry(&path, ".opf")).unwrap();
        assert!(saved.contains("<dc:title"));
        assert!(saved.contains("<dc:creator"));
        assert!(!saved.contains("xmlns=\"http://purl.org/dc/elements/1.1/\""));

        let after = std::fs::read(&path).unwrap();
        assert!(
            !repair(&path, false).unwrap(),
            "second repair must be a no-op"
        );
        assert_eq!(std::fs::read(&path).unwrap(), after);
    }

    #[test]
    fn fixes_colliding_meta_keys_in_epub3() {
        let (_dir, path) = make_epub_with(
            r#"<meta name="schema:accessMode" content="textual"/><meta property="schema:accessMode">visual</meta>"#,
        );
        assert!(repair(&path, true).unwrap());
        assert!(repair(&path, false).unwrap());
        let saved = String::from_utf8(zip_entry(&path, ".opf")).unwrap();
        assert!(!saved.contains("name=\"schema:accessMode\""));
        assert!(saved.contains("property=\"schema:accessMode\""));
    }

    #[test]
    fn keeps_name_form_in_epub2() {
        let mut opf = crate::epub::test_support::FIXTURE_OPF
            .replace("version=\"3.0\"", "version=\"2.0\"")
            .replace("<!-- extra -->", "");
        opf = opf.replace(
            "<dc:subject id=\"sf\">Science Fiction</dc:subject>",
            r#"<dc:subject id="sf">Science Fiction</dc:subject><meta name="x:y" content="one"/><meta property="x:y">two</meta>"#,
        );
        let (_dir, path) = crate::epub::test_support::write_epub(&opf);
        assert!(repair(&path, false).unwrap());
        let saved = String::from_utf8(zip_entry(&path, ".opf")).unwrap();
        assert!(saved.contains("<meta name=\"x:y\" content=\"one\"/>"));
        assert!(!saved.contains("<meta property=\"x:y\">"));
    }

    #[test]
    fn no_package_document_is_an_error() {
        let (_dir, path) = write_entries(&[(
            "META-INF/container.xml",
            crate::epub::test_support::FIXTURE_CONTAINER,
        )]);
        let err = repair(&path, true).unwrap_err();
        assert!(err.to_string().contains("no package document"));
    }

    #[test]
    fn invalid_archive_is_an_error() {
        let (_dir, path) = make_epub();
        std::fs::write(&path, b"not a zip").unwrap();
        assert!(repair(&path, true).is_err());
    }
}
