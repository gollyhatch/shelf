//! Archive rewriting: replace a few entries and copy the rest untouched.

use std::collections::HashMap;
use std::io::{Cursor, Read, Seek, Write};

use anyhow::{Context, Result};
use zip::write::SimpleFileOptions;
use zip::{CompressionMethod, ZipArchive, ZipWriter};

use super::MIMETYPE;

/// Return a new archive with the named entries replaced by the given content.
/// Every other entry is copied in compressed form, so chapters and images are
/// not decompressed or re-compressed. The OCF `mimetype` entry is always
/// written first and stored.
pub fn rewrite_archive<R: Read + Seek>(
    archive: &mut ZipArchive<R>,
    replace: &HashMap<String, Vec<u8>>,
) -> Result<Vec<u8>> {
    let mut writer = ZipWriter::new(Cursor::new(Vec::new()));

    writer
        .start_file(
            "mimetype",
            SimpleFileOptions::default()
                .compression_method(CompressionMethod::Stored),
        )
        .context("write mimetype")?;
    writer.write_all(MIMETYPE.as_bytes())?;

    for index in 0..archive.len() {
        let file = archive
            .by_index_raw(index)
            .with_context(|| format!("read archive entry {index}"))?;
        let name = match file.name() {
            Ok(name) => name.into_owned(),
            Err(_) => {
                // A legacy code page name we cannot decode: copy it verbatim.
                writer.raw_copy_file(file)?;
                continue;
            }
        };
        if name == "mimetype" {
            continue;
        }
        match replace.get(&name) {
            Some(content) => {
                // Keep the entry's own method, timestamp and permissions.
                writer.start_file(name.clone(), file.options())?;
                writer.write_all(content)?;
            }
            None => {
                writer.raw_copy_file(file)?;
            }
        }
    }

    let cursor = writer.finish()?;
    Ok(cursor.into_inner())
}
