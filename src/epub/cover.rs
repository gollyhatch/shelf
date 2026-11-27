//! Finding and decoding a book's cover image.

use std::path::Path;

use anyhow::Result;
use image::imageops::FilterType;
use image::RgbaImage;
use quick_xml::Reader;
use quick_xml::events::{BytesStart, Event};

use super::opf::OpfDocument;
use super::pages::read_entry_opt_with_limit;
use super::{open_archive, read_package_document};

/// Covers can be much larger than the package document; this still bounds a
/// decompression bomb.
const MAX_COVER_ENTRY: u64 = 32 << 20;

/// Read and decode the selected book's cover, if it has one.
pub fn read_cover(name: &Path) -> Result<Option<RgbaImage>> {
    let mut archive = open_archive(name)?;
    let doc = read_package_document(&mut archive)?;
    let opf_dir = match doc.name.rsplit_once('/') {
        Some((dir, _)) => dir,
        None => "",
    };

    let Some(href) = cover_href(&doc) else {
        return Ok(None);
    };
    let Some(data) = read_entry_opt_with_limit(
        &mut archive,
        opf_dir,
        &href,
        MAX_COVER_ENTRY,
    ) else {
        return Ok(None);
    };
    let Ok(image) = image::load_from_memory(&data) else {
        return Ok(None);
    };
    Ok(Some(thumbnail(image.to_rgba8())))
}

/// Keep the cached image small enough that redrawing a pane does not rescale a
/// full-resolution cover on every frame.
fn thumbnail(image: RgbaImage) -> RgbaImage {
    const MAX_W: u32 = 1024;
    const MAX_H: u32 = 1536;
    if image.width() <= MAX_W && image.height() <= MAX_H {
        return image;
    }
    let ratio = image.width() as f64 / image.height() as f64;
    let mut width = MAX_W;
    let mut height = (width as f64 / ratio).round().max(1.0) as u32;
    if height > MAX_H {
        height = MAX_H;
        width = (height as f64 * ratio).round().max(1.0) as u32;
    }
    image::imageops::resize(&image, width, height, FilterType::Triangle)
}

/// Locate the cover entry: EPUB 3 manifest property, EPUB 2 `meta name=cover`,
/// the guide, then the first image in the manifest.
fn cover_href(doc: &OpfDocument) -> Option<String> {
    if let Some(item) = doc
        .manifest
        .iter()
        .find(|item| has_token(&item.properties, "cover-image"))
        && is_image(&item.media_type)
        && !item.href.trim().is_empty()
    {
        return Some(item.href.clone());
    }

    for child in &doc.children {
        if child.local != "meta" || !child.name.eq_ignore_ascii_case("cover")
        {
            continue;
        }
        let value = child.content.trim();
        if value.is_empty() {
            continue;
        }
        if let Some(item) = doc
            .manifest
            .iter()
            .find(|item| item.id == value || item.href == value)
        {
            if is_image(&item.media_type) {
                return Some(item.href.clone());
            }
            continue;
        }
        return Some(value.to_owned());
    }

    if let Some(href) = guide_cover_href(doc) {
        match doc.manifest.iter().find(|item| item.href == href) {
            Some(item) if is_image(&item.media_type) => {
                return Some(item.href.clone());
            }
            Some(_) => {}
            None => return Some(href),
        }
    }

    doc.manifest
        .iter()
        .find(|item| is_image(&item.media_type))
        .map(|item| item.href.clone())
}

fn is_image(media_type: &str) -> bool {
    media_type
        .trim()
        .to_ascii_lowercase()
        .starts_with("image/")
}

fn has_token(value: &str, token: &str) -> bool {
    value.split_whitespace().any(|part| part == token)
}

fn local_name(name: &str) -> &str {
    match name.rfind(':') {
        Some(i) => &name[i + 1..],
        None => name,
    }
}

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

/// The guide's `reference type="cover"` target, which may be an XHTML wrapper
/// rather than the image itself.
fn guide_cover_href(doc: &OpfDocument) -> Option<String> {
    // Kept separate from `parse_opf`: the guide is only needed for covers.
    let mut reader = Reader::from_reader(doc.data.as_slice());
    loop {
        match reader.read_event() {
            Ok(Event::Start(start)) | Ok(Event::Empty(start)) => {
                let name = start.name();
                if local_name(name.as_ref()) == "reference"
                    && attr_local(&start, "type")
                        .is_some_and(|value| {
                            value.trim().eq_ignore_ascii_case("cover")
                        })
                    && let Some(href) = attr_local(&start, "href")
                    && !href.trim().is_empty()
                {
                    return Some(href);
                }
            }
            Ok(Event::Eof) => break,
            Err(_) => return None,
            _ => {}
        }
    }
    None
}

#[cfg(test)]
mod tests {
    use std::io::Cursor;

    use super::*;
    use crate::epub::test_support::{
        FIXTURE_CONTAINER, write_binary_entries,
    };

    fn png_bytes(color: [u8; 4]) -> Vec<u8> {
        let image = RgbaImage::from_pixel(3, 2, image::Rgba(color));
        let mut bytes = Cursor::new(Vec::new());
        image::DynamicImage::ImageRgba8(image)
            .write_to(&mut bytes, image::ImageFormat::Png)
            .unwrap();
        bytes.into_inner()
    }

    fn opf(metadata: &str, manifest: &str) -> String {
        format!(
            r#"<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0" unique-identifier="pub-id">
  <metadata>
    <dc:identifier id="pub-id">urn:isbn:1</dc:identifier>
    <dc:title>Book</dc:title>
    {metadata}
  </metadata>
  <manifest>
    {manifest}
  </manifest>
</package>"#
        )
    }

    #[test]
    fn reads_epub3_cover_image() {
        let cover = png_bytes([10, 20, 30, 255]);
        let opf = opf(
            "",
            r#"<item id="cover" href="cover.png" media-type="image/png" properties="cover-image"/>"#,
        );
        let (_dir, path) = write_binary_entries(&[
            ("META-INF/container.xml", FIXTURE_CONTAINER.as_bytes()),
            ("OEBPS/content.opf", opf.as_bytes()),
            ("OEBPS/cover.png", &cover),
        ]);
        let image = read_cover(&path).unwrap().unwrap();
        assert_eq!(image.dimensions(), (3, 2));
        assert_eq!(image.get_pixel(0, 0).0, [10, 20, 30, 255]);
    }

    #[test]
    fn reads_epub2_cover_meta() {
        let cover = png_bytes([200, 100, 50, 255]);
        let opf = opf(
            r#"<meta name="cover" content="cover-image"/>"#,
            r#"<item id="cover-image" href="cover.png" media-type="image/png"/>"#,
        );
        let (_dir, path) = write_binary_entries(&[
            ("META-INF/container.xml", FIXTURE_CONTAINER.as_bytes()),
            ("OEBPS/content.opf", opf.as_bytes()),
            ("OEBPS/cover.png", &cover),
        ]);
        let image = read_cover(&path).unwrap().unwrap();
        assert_eq!(image.dimensions(), (3, 2));
    }

    #[test]
    fn missing_cover_is_none() {
        let opf = opf(
            "",
            r#"<item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>"#,
        );
        let (_dir, path) = write_binary_entries(&[
            ("META-INF/container.xml", FIXTURE_CONTAINER.as_bytes()),
            ("OEBPS/content.opf", opf.as_bytes()),
        ]);
        assert!(read_cover(&path).unwrap().is_none());
    }
}
