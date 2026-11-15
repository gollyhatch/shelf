//! Fetch book metadata from an online catalogue (Google Books).

use std::io::Read;
use std::time::Duration;

use anyhow::{Context, Result, anyhow, bail};
use regex::Regex;
use serde::Deserialize;
use std::sync::LazyLock;

use crate::epub::{Creator, Metadata};

/// Google Books volumes endpoint.
pub const DEFAULT_BASE_URL: &str =
    "https://www.googleapis.com/books/v1/volumes";

static ISBN_PATTERN: LazyLock<Regex> = LazyLock::new(|| {
    Regex::new(r"^(?:\d{9}[\dX]|\d{13})$").expect("valid ISBN pattern")
});
static HTML_TAG: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"<[^>]*>").expect("valid tag pattern"));

/// Fetches metadata from a Google Books-compatible endpoint.
#[derive(Clone, Debug)]
pub struct Client {
    pub base_url: String,
    pub api_key: String,
    pub timeout: Duration,
}

impl Default for Client {
    fn default() -> Self {
        Self {
            base_url: DEFAULT_BASE_URL.to_owned(),
            api_key: String::new(),
            timeout: Duration::from_secs(15),
        }
    }
}

impl Client {
    pub fn new(api_key: impl Into<String>) -> Self {
        Self {
            api_key: api_key.into(),
            ..Self::default()
        }
    }

    /// Search for a book using its ISBN when available, otherwise its title
    /// and author.
    pub fn lookup(&self, meta: &Metadata) -> Result<Metadata> {
        let query = search_query(meta)?;
        let base = if self.base_url.is_empty() {
            DEFAULT_BASE_URL
        } else {
            &self.base_url
        };
        let mut params = vec![
            ("q", query),
            ("maxResults", "1".to_owned()),
            ("printType", "books".to_owned()),
        ];
        if !self.api_key.is_empty() {
            params.push(("key", self.api_key.clone()));
        }
        let url = build_url(base, &params);

        let agent: ureq::Agent = ureq::Agent::config_builder()
            .timeout_global(Some(self.timeout))
            .build()
            .into();
        let response = agent
            .get(&url)
            .header("Accept", "application/json")
            .header("User-Agent", "shelf")
            .call()
            .map_err(|err| match err {
                ureq::Error::StatusCode(code) => {
                    anyhow!("online lookup: HTTP {code}")
                }
                other => anyhow!("online lookup: {other}"),
            })?;

        let mut body = String::new();
        response
            .into_body()
            .into_reader()
            .take(16 << 20)
            .read_to_string(&mut body)
            .context("online lookup")?;
        let payload: Payload = serde_json::from_str(&body)
            .context("online lookup: invalid response")?;
        let item = payload
            .items
            .and_then(|items| items.into_iter().next())
            .ok_or_else(|| anyhow!("no online match found"))?;
        Ok(metadata_from(item.volume_info))
    }
}

fn build_url(base: &str, params: &[(&str, String)]) -> String {
    let mut url = base.to_owned();
    for (key, value) in params {
        url.push(if url.contains('?') { '&' } else { '?' });
        url.push_str(&query_escape(key));
        url.push('=');
        url.push_str(&query_escape(value));
    }
    url
}

/// `application/x-www-form-urlencoded` escaping: spaces become `+`.
fn query_escape(value: &str) -> String {
    let mut out = String::with_capacity(value.len());
    for byte in value.bytes() {
        match byte {
            b'A'..=b'Z'
            | b'a'..=b'z'
            | b'0'..=b'9'
            | b'-'
            | b'_'
            | b'.'
            | b'~' => {
                out.push(byte as char);
            }
            b' ' => out.push('+'),
            other => out.push_str(&format!("%{other:02X}")),
        }
    }
    out
}

#[derive(Deserialize)]
struct Payload {
    #[serde(default)]
    items: Option<Vec<Item>>,
}

#[derive(Deserialize)]
struct Item {
    #[serde(rename = "volumeInfo", default)]
    volume_info: VolumeInfo,
}

#[derive(Deserialize, Default)]
#[serde(default)]
struct VolumeInfo {
    title: String,
    subtitle: String,
    authors: Vec<String>,
    publisher: String,
    #[serde(rename = "publishedDate")]
    published_date: String,
    description: String,
    categories: Vec<String>,
    language: String,
    #[serde(rename = "industryIdentifiers")]
    industry_identifiers: Vec<IndustryIdentifier>,
}

#[derive(Deserialize)]
struct IndustryIdentifier {
    #[serde(rename = "type")]
    kind: String,
    identifier: String,
}

fn metadata_from(volume: VolumeInfo) -> Metadata {
    let creators = volume
        .authors
        .iter()
        .map(|author| clean_text(author))
        .filter(|name| !name.is_empty())
        .map(Creator::new)
        .collect();
    let subjects = volume
        .categories
        .iter()
        .map(|category| clean_text(category))
        .filter(|category| !category.is_empty())
        .collect();

    let mut identifier = String::new();
    for preferred in ["ISBN_13", "ISBN_10"] {
        if let Some(id) = volume
            .industry_identifiers
            .iter()
            .find(|id| id.kind == preferred && !id.identifier.is_empty())
        {
            identifier = id.identifier.clone();
            break;
        }
    }

    Metadata {
        title: clean_text(&volume.title),
        subtitle: clean_text(&volume.subtitle),
        creators,
        subjects,
        description: clean_text(&volume.description),
        publisher: clean_text(&volume.publisher),
        date: clean_text(&volume.published_date),
        language: clean_text(&volume.language),
        identifier: clean_text(&identifier),
        ..Metadata::default()
    }
}

fn search_query(meta: &Metadata) -> Result<String> {
    let isbn = isbn_from_identifier(&meta.identifier);
    if !isbn.is_empty() {
        return Ok(format!("isbn:{isbn}"));
    }
    let title = meta.title.trim();
    let author = meta.author_string();
    let author = author.trim();
    match (title.is_empty(), author.is_empty()) {
        (false, false) => Ok(format!("{title} {author}")),
        (false, true) => Ok(title.to_owned()),
        (true, false) => Ok(author.to_owned()),
        (true, true) => bail!(
            "not enough metadata to search online (need an ISBN, title or author)"
        ),
    }
}

/// Extract an ISBN from an identifier value, accepting URN and prefix forms.
pub fn isbn_from_identifier(identifier: &str) -> String {
    let mut s = identifier.trim().to_ascii_lowercase();
    for prefix in ["urn:isbn:", "isbn:", "isbn ", "isbn-13:", "isbn-10:"] {
        if let Some(rest) = s.strip_prefix(prefix) {
            s = rest.to_owned();
            break;
        }
    }
    s = s.replace(['-', ' ', '_'], "");
    let upper = s.to_ascii_uppercase();
    if ISBN_PATTERN.is_match(&upper) {
        upper
    } else {
        String::new()
    }
}

/// Remove HTML markup and collapse whitespace.
fn clean_text(s: &str) -> String {
    let stripped = HTML_TAG.replace_all(s, " ");
    let decoded = html_escape::decode_html_entities(&stripped);
    decoded.split_whitespace().collect::<Vec<_>>().join(" ")
}

#[cfg(test)]
mod tests {
    use std::io::{Read, Write};
    use std::net::TcpListener;
    use std::thread;

    use super::*;

    fn serve_once(
        body: &'static str,
        status: &'static str,
    ) -> (String, thread::JoinHandle<String>) {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = listener.local_addr().unwrap();
        let handle = thread::spawn(move || {
            let (mut stream, _) = listener.accept().unwrap();
            let mut request = [0u8; 4096];
            let n = stream.read(&mut request).unwrap();
            let request = String::from_utf8_lossy(&request[..n]).into_owned();
            let response = format!(
                "HTTP/1.1 {status}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
                body.len()
            );
            stream.write_all(response.as_bytes()).unwrap();
            request
        });
        (format!("http://{addr}/volumes"), handle)
    }

    #[test]
    fn looks_up_by_isbn() {
        let body = r#"{"items":[{"volumeInfo":{"title":"Dune","authors":["Frank Herbert"],"industryIdentifiers":[{"type":"ISBN_13","identifier":"9780441013593"}],"publishedDate":"1965"}}]}"#;
        let (base, handle) = serve_once(body, "200 OK");
        let client = Client {
            base_url: base,
            ..Client::default()
        };
        let meta = client
            .lookup(&Metadata {
                identifier: "urn:isbn:978-0-441-01359-3".into(),
                ..Metadata::default()
            })
            .unwrap();
        assert_eq!(meta.title, "Dune");
        assert_eq!(meta.creator_names(), ["Frank Herbert"]);
        assert_eq!(meta.identifier, "9780441013593");
        let request = handle.join().unwrap();
        assert!(request.contains("q=isbn%3A9780441013593"), "{request}");
    }

    #[test]
    fn looks_up_by_title_and_author() {
        let body = r#"{"items":[{"volumeInfo":{"title":"Dune","description":"<p>A &amp; B</p>"}}]}"#;
        let (base, handle) = serve_once(body, "200 OK");
        let client = Client {
            base_url: base,
            ..Client::default()
        };
        let meta = client
            .lookup(&Metadata {
                title: "Dune".into(),
                creators: vec![Creator::new("Frank Herbert")],
                ..Metadata::default()
            })
            .unwrap();
        assert_eq!(meta.description, "A & B");
        let request = handle.join().unwrap();
        assert!(request.contains("q=Dune+Frank+Herbert"), "{request}");
    }

    #[test]
    fn http_error_is_reported() {
        let (base, handle) = serve_once("{}", "429 Too Many Requests");
        let client = Client {
            base_url: base,
            ..Client::default()
        };
        let err = client
            .lookup(&Metadata {
                title: "Dune".into(),
                ..Metadata::default()
            })
            .unwrap_err();
        assert!(err.to_string().contains("429"), "{err}");
        let _ = handle.join();
    }

    #[test]
    fn no_match_is_an_error() {
        let (base, handle) = serve_once(r#"{"items":[]}"#, "200 OK");
        let client = Client {
            base_url: base,
            ..Client::default()
        };
        let err = client
            .lookup(&Metadata {
                title: "Dune".into(),
                ..Metadata::default()
            })
            .unwrap_err();
        assert!(err.to_string().contains("no online match"));
        let _ = handle.join();
    }

    #[test]
    fn needs_metadata() {
        let err = search_query(&Metadata::default()).unwrap_err();
        assert!(err.to_string().contains("not enough metadata"));
    }

    #[test]
    fn isbn_extraction() {
        assert_eq!(
            isbn_from_identifier("urn:isbn:978-0-441-01359-3"),
            "9780441013593"
        );
        assert_eq!(isbn_from_identifier("ISBN 0-306-40615-2"), "0306406152");
        assert_eq!(isbn_from_identifier("not an isbn"), "");
    }
}
