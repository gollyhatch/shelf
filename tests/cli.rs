//! End-to-end checks of the command line interface against real EPUB files.

use std::io::Write;
use std::path::{Path, PathBuf};
use std::process::{Command, Output};

use serde_json::Value;
use zip::write::SimpleFileOptions;
use zip::{CompressionMethod, ZipWriter};

const CONTAINER: &str = r#"<?xml version="1.0" encoding="UTF-8"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>"#;

fn opf(title: &str, author: &str) -> String {
    format!(
        r#"<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0" unique-identifier="pub-id">
  <metadata>
    <dc:identifier id="pub-id">urn:isbn:1</dc:identifier>
    <dc:title>{title}</dc:title>
    <dc:language>en</dc:language>
    <dc:creator>{author}</dc:creator>
    <dc:publisher>Ace Books</dc:publisher>
    <meta property="dcterms:modified">2026-01-01T00:00:00Z</meta>
  </metadata>
  <manifest><item id="c" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest>
  <spine><itemref idref="c"/></spine>
</package>"#
    )
}

fn write_epub(dir: &Path, name: &str, opf: &str) -> PathBuf {
    let path = dir.join(name);
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
        writer.write_all(b"application/epub+zip").unwrap();
        for (entry, content) in [
            ("META-INF/container.xml", CONTAINER),
            ("OEBPS/content.opf", opf),
            ("OEBPS/chapter.xhtml", "<html/>"),
        ] {
            writer
                .start_file(entry, SimpleFileOptions::default())
                .unwrap();
            writer.write_all(content.as_bytes()).unwrap();
        }
        writer.finish().unwrap();
    }
    std::fs::write(&path, bytes).unwrap();
    path
}

fn shelf(dir: &Path, args: &[&str]) -> Output {
    Command::new(env!("CARGO_BIN_EXE_shelf"))
        .args(args)
        .current_dir(dir)
        .env("XDG_CONFIG_HOME", dir.join("config"))
        .output()
        .expect("run shelf")
}

fn stdout(output: &Output) -> String {
    String::from_utf8_lossy(&output.stdout).into_owned()
}

#[test]
fn list_json_and_get() {
    let dir = tempfile::tempdir().unwrap();
    write_epub(dir.path(), "dune.epub", &opf("Dune", "Frank Herbert"));
    write_epub(dir.path(), "hobbit.epub", &opf("The Hobbit", "Tolkien"));

    let output = shelf(dir.path(), &["list", "--json"]);
    assert!(output.status.success(), "{output:?}");
    let value: Value = serde_json::from_str(&stdout(&output)).unwrap();
    let books = value.as_array().unwrap();
    assert_eq!(books.len(), 2);
    assert_eq!(books[0]["title"], "Dune");
    assert_eq!(books[0]["authors"][0], "Frank Herbert");
    assert!(books[0].get("read").is_none());

    let output = shelf(dir.path(), &["get", "author", "dune.epub"]);
    assert!(output.status.success());
    assert_eq!(stdout(&output), "Frank Herbert\n");

    let output =
        shelf(dir.path(), &["get", "title", "dune.epub", "hobbit.epub"]);
    let text = stdout(&output);
    assert!(text.contains("dune.epub\tDune"), "{text}");
    assert!(text.contains("hobbit.epub\tThe Hobbit"), "{text}");
}

#[test]
fn set_and_modify_rewrite_metadata() {
    let dir = tempfile::tempdir().unwrap();
    let path =
        write_epub(dir.path(), "dune.epub", &opf("Dune", "Frank Herbert"));

    let output = shelf(
        dir.path(),
        &[
            "set",
            "--series",
            "Dune",
            "--series-index",
            "1",
            "--tags",
            "sf, classic",
            "--read",
            "yes",
            "dune.epub",
        ],
    );
    assert!(output.status.success(), "{output:?}");
    let text = stdout(&output);
    assert!(text.contains("Series:     + Dune"), "{text}");
    assert!(text.contains("Read:       no → yes"), "{text}");

    let meta = shelf::epub::read_metadata(&path).unwrap();
    assert_eq!(meta.series, "Dune");
    assert_eq!(meta.series_index, "1");
    assert_eq!(meta.subjects, ["sf", "classic"]);
    assert!(meta.read);

    let output = shelf(
        dir.path(),
        &[
            "modify",
            "--title",
            "-e",
            "Dune",
            "--replacement",
            "DUNE",
            "dune.epub",
        ],
    );
    assert!(output.status.success(), "{output:?}");
    assert_eq!(shelf::epub::read_metadata(&path).unwrap().title, "DUNE");

    let output = shelf(dir.path(), &["set", "--title", "", "dune.epub"]);
    assert!(output.status.success());
    assert_eq!(shelf::epub::read_metadata(&path).unwrap().title, "");
}

#[test]
fn dry_run_does_not_write() {
    let dir = tempfile::tempdir().unwrap();
    let path =
        write_epub(dir.path(), "dune.epub", &opf("Dune", "Frank Herbert"));
    let before = std::fs::read(&path).unwrap();
    let output = shelf(
        dir.path(),
        &["set", "-n", "--title", "Changed", "dune.epub"],
    );
    assert!(output.status.success());
    assert!(stdout(&output).contains("would update"));
    assert_eq!(std::fs::read(&path).unwrap(), before);
}

#[test]
fn check_reports_issues_and_exits_non_zero() {
    let dir = tempfile::tempdir().unwrap();
    let bad = r#"<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0" unique-identifier="i">
  <metadata>
    <dc:identifier id="i">urn:isbn:1</dc:identifier>
    <dc:language>english</dc:language>
  </metadata>
</package>"#;
    write_epub(dir.path(), "bad.epub", bad);
    let output = shelf(dir.path(), &["check"]);
    assert!(!output.status.success());
    let text = stdout(&output);
    assert!(text.contains("missing title"), "{text}");
    assert!(text.contains("missing author"), "{text}");
    assert!(text.contains("invalid language \"english\""), "{text}");
    assert!(String::from_utf8_lossy(&output.stderr).contains("issues found"));

    write_epub(dir.path(), "good.epub", &opf("Dune", "Frank Herbert"));
    let output = shelf(dir.path(), &["check", "--json"]);
    assert!(!output.status.success());
    let value: Value = serde_json::from_str(&stdout(&output)).unwrap();
    assert!(
        value
            .as_array()
            .unwrap()
            .iter()
            .all(|issue| issue["path"].as_str().unwrap().ends_with("bad.epub"))
    );
}

#[test]
fn stats_json_shape() {
    let dir = tempfile::tempdir().unwrap();
    write_epub(dir.path(), "dune.epub", &opf("Dune", "Frank Herbert"));
    write_epub(dir.path(), "messiah.epub", &opf("Messiah", "Frank Herbert"));
    let output = shelf(dir.path(), &["stats", "--json", "--top", "5"]);
    assert!(output.status.success());
    let value: Value = serde_json::from_str(&stdout(&output)).unwrap();
    assert_eq!(value["books"], 2);
    assert_eq!(value["authors"], 1);
    assert_eq!(value["topAuthors"][0]["name"], "Frank Herbert");
    assert_eq!(value["topAuthors"][0]["count"], 2);
    assert!(value["size"].as_u64().unwrap() > 0);
}

#[test]
fn rename_and_repair() {
    let dir = tempfile::tempdir().unwrap();
    let path =
        write_epub(dir.path(), "messy.epub", &opf("Dune", "Frank Herbert"));
    let output =
        shelf(dir.path(), &["rename", "-n", "-f", "%a - %t", "messy.epub"]);
    assert!(output.status.success());
    assert!(stdout(&output).contains("→ Frank Herbert - Dune.epub"));
    assert!(path.exists());

    let output = shelf(dir.path(), &["rename", "-f", "%a - %t", "messy.epub"]);
    assert!(output.status.success(), "{output:?}");
    let renamed = dir.path().join("Frank Herbert - Dune.epub");
    assert!(renamed.exists());

    let legacy = opf("Dune", "Frank Herbert").replace(
        "<dc:title>Dune</dc:title>",
        "<title xmlns=\"http://purl.org/dc/elements/1.1/\">Dune</title>",
    );
    let legacy_path = write_epub(dir.path(), "legacy.epub", &legacy);
    let output = shelf(dir.path(), &["repair", "-n", "legacy.epub"]);
    assert!(output.status.success());
    assert!(stdout(&output).contains("would repair"));
    let output = shelf(dir.path(), &["repair", "legacy.epub"]);
    assert!(output.status.success());
    assert!(stdout(&output).contains("(repaired)"));
    let opf = read_zip_entry(&legacy_path, "OEBPS/content.opf");
    assert!(opf.contains("<dc:title>Dune</dc:title>"));
}

#[test]
fn invalid_format_is_rejected_before_renaming() {
    let dir = tempfile::tempdir().unwrap();
    write_epub(dir.path(), "dune.epub", &opf("Dune", "Frank Herbert"));
    let output = shelf(dir.path(), &["rename", "-f", "%q", "dune.epub"]);
    assert!(!output.status.success());
    assert!(
        String::from_utf8_lossy(&output.stderr)
            .contains("unknown placeholder %q")
    );
    assert!(dir.path().join("dune.epub").exists());
}

#[test]
fn completion_and_man_pages() {
    let dir = tempfile::tempdir().unwrap();
    let output = shelf(dir.path(), &["completion", "bash"]);
    assert!(output.status.success());
    assert!(stdout(&output).contains("_shelf"));

    let output = shelf(dir.path(), &["completion", "tcsh"]);
    assert!(!output.status.success());

    let output = shelf(dir.path(), &["man"]);
    assert!(output.status.success());
    assert!(stdout(&output).contains(".TH SHELF 1"));

    let man_dir = dir.path().join("man");
    let output =
        shelf(dir.path(), &["man", "--dir", man_dir.to_str().unwrap()]);
    assert!(output.status.success());
    assert!(man_dir.join("shelf.1").exists());
    assert!(man_dir.join("shelf-list.1").exists());
}

fn read_zip_entry(path: &Path, name: &str) -> String {
    let data = std::fs::read(path).unwrap();
    let mut archive = zip::ZipArchive::new(std::io::Cursor::new(data)).unwrap();
    let mut entry = archive.by_name(name).unwrap();
    let mut text = String::new();
    std::io::Read::read_to_string(&mut entry, &mut text).unwrap();
    text
}
