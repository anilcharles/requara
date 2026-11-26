//! Working locally, with no account: requests are kept in a file on this
//! machine and sent straight from it. Nothing here talks to a Speklo server.

use std::fs;
use std::path::PathBuf;
use std::time::{Duration, Instant};

use anyhow::{Context as _, Result, bail};
use serde::{Deserialize, Serialize};

pub const METHODS: [&str; 7] = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
pub struct LocalRequest {
    pub id: String,
    pub name: String,
    pub method: String,
    pub url: String,
    /// One header per line, `Name: value`. Lines starting with `#` are skipped.
    #[serde(default)]
    pub headers: String,
    #[serde(default)]
    pub body: String,
}

impl LocalRequest {
    pub fn new(name: &str) -> Self {
        Self {
            id: new_id(),
            name: name.to_string(),
            method: "GET".into(),
            url: String::new(),
            headers: String::new(),
            body: String::new(),
        }
    }
}

fn new_id() -> String {
    use rand::RngCore as _;
    let mut b = [0u8; 8];
    rand::rng().fill_bytes(&mut b);
    b.iter().map(|x| format!("{x:02x}")).collect()
}

/// The requests saved on this machine.
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
pub struct LocalStore {
    pub requests: Vec<LocalRequest>,
}

fn path() -> Option<PathBuf> {
    Some(dirs::config_dir()?.join("Speklo").join("requests.json"))
}

impl LocalStore {
    /// Loads the saved requests, or starts with one example.
    pub fn load() -> Self {
        let saved = path()
            .and_then(|p| fs::read(p).ok())
            .and_then(|b| serde_json::from_slice::<LocalStore>(&b).ok());
        match saved {
            Some(s) if !s.requests.is_empty() => s,
            _ => LocalStore {
                requests: vec![LocalRequest {
                    url: "https://api.github.com/zen".into(),
                    headers: "Accept: text/plain".into(),
                    ..LocalRequest::new("Example request")
                }],
            },
        }
    }

    pub fn save(&self) -> Result<()> {
        let p = path().context("no config directory on this system")?;
        if let Some(dir) = p.parent() {
            fs::create_dir_all(dir)?;
        }
        fs::write(p, serde_json::to_vec_pretty(self)?)?;
        Ok(())
    }
}

#[derive(Clone, Debug)]
pub struct LocalResponse {
    pub status: u16,
    pub status_text: String,
    pub duration_ms: u128,
    pub size_bytes: usize,
    pub headers: Vec<(String, String)>,
    pub body: String,
}

/// Parses `Name: value` lines.
pub fn parse_headers(text: &str) -> Result<Vec<(String, String)>> {
    let mut out = vec![];
    for (i, line) in text.lines().enumerate() {
        let line = line.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        let Some((name, value)) = line.split_once(':') else {
            bail!(
                "Header line {} needs a colon, like `Accept: application/json`",
                i + 1
            );
        };
        out.push((name.trim().to_string(), value.trim().to_string()));
    }
    Ok(out)
}

/// Sends the request from this machine.
pub fn send(req: &LocalRequest) -> Result<LocalResponse> {
    let url = req.url.trim();
    if url.is_empty() {
        bail!("Enter a URL to send the request to.");
    }
    let url = if url.contains("://") {
        url.to_string()
    } else {
        format!("https://{url}")
    };
    let method = reqwest::Method::from_bytes(req.method.as_bytes()).context("unknown method")?;
    let client = reqwest::blocking::Client::builder()
        .user_agent(concat!("Speklo-Desktop/", env!("CARGO_PKG_VERSION")))
        .timeout(Duration::from_secs(60))
        .build()?;
    let mut rb = client.request(method.clone(), &url);
    for (name, value) in parse_headers(&req.headers)? {
        rb = rb.header(name, value);
    }
    if !req.body.is_empty() && method != reqwest::Method::GET && method != reqwest::Method::HEAD {
        rb = rb.body(req.body.clone());
    }
    let started = Instant::now();
    let res = rb.send().with_context(|| format!("Couldn't reach {url}"))?;
    let status = res.status();
    let headers: Vec<(String, String)> = res
        .headers()
        .iter()
        .map(|(k, v)| {
            (
                k.to_string(),
                String::from_utf8_lossy(v.as_bytes()).into_owned(),
            )
        })
        .collect();
    let bytes = res.bytes()?;
    let duration_ms = started.elapsed().as_millis();
    let text = String::from_utf8_lossy(&bytes).into_owned();
    // Pretty-print JSON so it's readable.
    let body = serde_json::from_slice::<serde_json::Value>(&bytes)
        .ok()
        .and_then(|v| serde_json::to_string_pretty(&v).ok())
        .unwrap_or(text);
    Ok(LocalResponse {
        status: status.as_u16(),
        status_text: status.canonical_reason().unwrap_or("").to_string(),
        duration_ms,
        size_bytes: bytes.len(),
        headers,
        body,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{Read, Write};
    use std::net::TcpListener;

    #[test]
    fn parses_headers() {
        let h = parse_headers("Accept: application/json\n\n# comment\nX-Token:  abc:def ").unwrap();
        assert_eq!(
            h,
            vec![
                ("Accept".into(), "application/json".into()),
                ("X-Token".into(), "abc:def".into())
            ]
        );
        assert!(parse_headers("no colon here").is_err());
    }

    #[test]
    fn sends_locally() {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let addr = listener.local_addr().unwrap();
        let server = std::thread::spawn(move || {
            let (mut s, _) = listener.accept().unwrap();
            let mut buf = [0u8; 4096];
            let n = s.read(&mut buf).unwrap();
            let req = String::from_utf8_lossy(&buf[..n]).to_string();
            let body = r#"{"ok":true}"#;
            write!(
                s,
                "HTTP/1.1 201 Created\r\nContent-Type: application/json\r\nX-Test: yes\r\nContent-Length: {}\r\n\r\n{body}",
                body.len()
            )
            .unwrap();
            req
        });
        let res = send(&LocalRequest {
            method: "POST".into(),
            url: format!("http://{addr}/things"),
            headers: "X-Api-Key: secret".into(),
            body: "hello".into(),
            ..LocalRequest::new("t")
        })
        .unwrap();
        let seen = server.join().unwrap();
        assert!(seen.starts_with("POST /things HTTP/1.1"), "{seen}");
        assert!(seen.to_ascii_lowercase().contains("x-api-key: secret"));
        assert!(seen.ends_with("hello"));
        assert_eq!(res.status, 201);
        assert!(res.headers.iter().any(|(k, v)| k == "x-test" && v == "yes"));
        assert_eq!(res.body, "{\n  \"ok\": true\n}");
    }

    #[test]
    fn needs_a_url() {
        assert!(send(&LocalRequest::new("t")).is_err());
    }
}
