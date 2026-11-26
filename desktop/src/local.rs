//! Requests, in the same shape the Requara server uses (`RequestDoc`), plus
//! working with them locally: a file on this machine and sending straight
//! from it. Nothing in the local half talks to a Requara server.

use std::fs;
use std::path::PathBuf;
use std::time::{Duration, Instant};

use anyhow::{Context as _, Result, bail};
use serde::{Deserialize, Deserializer, Serialize};

pub const METHODS: [&str; 7] = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];
pub const BODY_MODES: [(&str, &str); 5] = [
    ("none", "none"),
    ("form-urlencoded", "urlencoded"),
    ("json", "JSON"),
    ("raw", "raw"),
    ("graphql", "GraphQL"),
];
pub const AUTH_TYPES: [(&str, &str); 5] = [
    ("inherit", "Inherit from environment ({{secret_key}})"),
    ("none", "No auth"),
    ("bearer", "Bearer token"),
    ("basic", "Basic auth"),
    ("apikey", "API key"),
];

fn yes() -> bool {
    true
}

/// `null` (Go's empty slice or string) reads as the default value.
pub(crate) fn null_default<'de, D: Deserializer<'de>, T: Default + Deserialize<'de>>(
    d: D,
) -> Result<T, D::Error> {
    Ok(Option::<T>::deserialize(d)?.unwrap_or_default())
}

#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
pub struct Kv {
    #[serde(default)]
    pub key: String,
    #[serde(default)]
    pub value: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub description: String,
    #[serde(default = "yes")]
    pub enabled: bool,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
pub struct Auth {
    /// inherit | none | bearer | basic | apikey
    #[serde(rename = "type", default)]
    pub kind: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub token: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub username: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub password: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub key: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub value: String,
    /// header | query (for API keys)
    #[serde(rename = "in", default, skip_serializing_if = "String::is_empty")]
    pub location: String,
}

/// A saved request. Fields the desktop app doesn't edit (settings, apiId,
/// folder, …) are kept in `extra` so saving sends them back unchanged.
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct RequestDoc {
    pub id: String,
    #[serde(default, deserialize_with = "null_default")]
    pub name: String,
    #[serde(default, deserialize_with = "null_default")]
    pub method: String,
    #[serde(default, deserialize_with = "null_default")]
    pub url: String,
    #[serde(default, deserialize_with = "null_default")]
    pub params: Vec<Kv>,
    #[serde(default, deserialize_with = "null_default")]
    pub headers: Vec<Kv>,
    #[serde(default, deserialize_with = "null_default")]
    pub body_mode: String,
    #[serde(default, deserialize_with = "null_default")]
    pub body: String,
    #[serde(default, deserialize_with = "null_default")]
    pub auth: Auth,
    #[serde(default, deserialize_with = "null_default")]
    pub pre_script: String,
    #[serde(default, deserialize_with = "null_default")]
    pub post_script: String,
    #[serde(flatten)]
    pub extra: serde_json::Map<String, serde_json::Value>,
}

impl RequestDoc {
    pub fn new_local(name: &str) -> Self {
        Self {
            id: new_id(),
            name: name.to_string(),
            method: "GET".into(),
            body_mode: "none".into(),
            auth: Auth {
                kind: "none".into(),
                ..Default::default()
            },
            ..Default::default()
        }
    }
}

fn new_id() -> String {
    use rand::RngCore as _;
    let mut b = [0u8; 8];
    rand::rng().fill_bytes(&mut b);
    format!(
        "local-{}",
        b.iter().map(|x| format!("{x:02x}")).collect::<String>()
    )
}

/// The requests saved on this machine.
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
pub struct LocalStore {
    pub requests: Vec<RequestDoc>,
}

fn dir() -> Option<PathBuf> {
    Some(dirs::config_dir()?.join("Requara"))
}

/// The first desktop builds kept headers as one text block.
#[derive(Deserialize)]
struct LegacyStore {
    requests: Vec<LegacyRequest>,
}

#[derive(Deserialize)]
struct LegacyRequest {
    name: String,
    method: String,
    url: String,
    #[serde(default)]
    headers: String,
    #[serde(default)]
    body: String,
}

impl LocalStore {
    /// Loads the saved requests, or starts with one example.
    pub fn load() -> Self {
        let Some(dir) = dir() else {
            return Self::example();
        };
        if let Some(s) = fs::read(dir.join("requests-v2.json"))
            .ok()
            .and_then(|b| serde_json::from_slice::<LocalStore>(&b).ok())
        {
            return s;
        }
        // Carry over requests from the earlier file format.
        if let Some(old) = fs::read(dir.join("requests.json"))
            .ok()
            .and_then(|b| serde_json::from_slice::<LegacyStore>(&b).ok())
        {
            let requests = old
                .requests
                .into_iter()
                .map(|r| RequestDoc {
                    method: r.method,
                    url: r.url,
                    headers: parse_header_lines(&r.headers),
                    body_mode: if r.body.is_empty() {
                        "none".into()
                    } else {
                        "raw".into()
                    },
                    body: r.body,
                    ..RequestDoc::new_local(&r.name)
                })
                .collect::<Vec<_>>();
            if !requests.is_empty() {
                let s = LocalStore { requests };
                let _ = s.save();
                return s;
            }
        }
        Self::example()
    }

    fn example() -> Self {
        LocalStore {
            requests: vec![RequestDoc {
                url: "https://api.github.com/zen".into(),
                ..RequestDoc::new_local("Example request")
            }],
        }
    }

    pub fn save(&self) -> Result<()> {
        let dir = dir().context("no config directory on this system")?;
        fs::create_dir_all(&dir)?;
        fs::write(
            dir.join("requests-v2.json"),
            serde_json::to_vec_pretty(self)?,
        )?;
        Ok(())
    }

    /// Adds the request, or replaces the one with the same id.
    pub fn put(&mut self, doc: RequestDoc) {
        match self.requests.iter_mut().find(|r| r.id == doc.id) {
            Some(r) => *r = doc,
            None => self.requests.push(doc),
        }
        let _ = self.save();
    }

    pub fn remove(&mut self, id: &str) {
        self.requests.retain(|r| r.id != id);
        let _ = self.save();
    }
}

fn parse_header_lines(text: &str) -> Vec<Kv> {
    text.lines()
        .filter_map(|l| {
            let l = l.trim();
            let (k, v) = l.split_once(':')?;
            (!l.starts_with('#')).then(|| Kv {
                key: k.trim().into(),
                value: v.trim().into(),
                enabled: true,
                ..Default::default()
            })
        })
        .collect()
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

/// Sends the request from this machine. `{{variables}}` are sent as written:
/// environments live on the organization's server.
pub fn send(req: &RequestDoc) -> Result<LocalResponse> {
    let raw = req.url.trim();
    if raw.is_empty() {
        bail!("Enter a URL to send the request to.");
    }
    let raw = if raw.contains("://") {
        raw.to_string()
    } else {
        format!("https://{raw}")
    };
    let mut url = url::Url::parse(&raw).with_context(|| format!("{raw} isn't a valid URL"))?;
    for p in req.params.iter().filter(|p| p.enabled && !p.key.is_empty()) {
        url.query_pairs_mut().append_pair(&p.key, &p.value);
    }
    if req.auth.kind == "apikey" && req.auth.location == "query" && !req.auth.key.is_empty() {
        url.query_pairs_mut()
            .append_pair(&req.auth.key, &req.auth.value);
    }

    let method = reqwest::Method::from_bytes(req.method.as_bytes()).context("unknown method")?;
    // The request's own Settings tab.
    let setting = |key: &str| req.extra.get("settings").and_then(|s| s.get(key)).cloned();
    let flag = |key: &str| setting(key).and_then(|v| v.as_bool()).unwrap_or(true);
    let timeout_ms = setting("timeoutMs")
        .and_then(|v| v.as_u64())
        .filter(|ms| *ms > 0)
        .unwrap_or(30_000);
    let mut builder = reqwest::blocking::Client::builder()
        .user_agent(concat!("Requara-Desktop/", env!("CARGO_PKG_VERSION")))
        .timeout(Duration::from_millis(timeout_ms));
    if !flag("followRedirects") {
        builder = builder.redirect(reqwest::redirect::Policy::none());
    }
    if !flag("verifySSL") {
        builder = builder.danger_accept_invalid_certs(true);
    }
    let client = builder.build()?;
    let mut rb = client.request(method.clone(), url.clone());
    let mut has_content_type = false;
    for h in req
        .headers
        .iter()
        .filter(|h| h.enabled && !h.key.is_empty())
    {
        has_content_type |= h.key.eq_ignore_ascii_case("content-type");
        rb = rb.header(&h.key, &h.value);
    }
    match req.auth.kind.as_str() {
        "bearer" if !req.auth.token.is_empty() => rb = rb.bearer_auth(&req.auth.token),
        "basic" => rb = rb.basic_auth(&req.auth.username, Some(&req.auth.password)),
        "apikey" if req.auth.location != "query" && !req.auth.key.is_empty() => {
            rb = rb.header(&req.auth.key, &req.auth.value)
        }
        _ => {}
    }
    let has_body = req.body_mode != "none"
        && !req.body.is_empty()
        && method != reqwest::Method::GET
        && method != reqwest::Method::HEAD;
    if has_body {
        let (content_type, body) = match req.body_mode.as_str() {
            "json" => ("application/json", req.body.clone()),
            "graphql" => (
                "application/json",
                serde_json::json!({ "query": req.body }).to_string(),
            ),
            "form-urlencoded" => ("application/x-www-form-urlencoded", req.body.clone()),
            _ => ("text/plain", req.body.clone()),
        };
        if !has_content_type {
            rb = rb.header("Content-Type", content_type);
        }
        rb = rb.body(body);
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

/// The request as a cURL command (variables are left as `{{name}}`).
pub fn curl(req: &RequestDoc) -> String {
    let mut s = format!("curl -X {} '{}'", req.method, req.url);
    for h in req
        .headers
        .iter()
        .filter(|h| h.enabled && !h.key.is_empty())
    {
        s.push_str(&format!(" \\\n  -H '{}: {}'", h.key, h.value));
    }
    if req.body_mode != "none" && !req.body.is_empty() {
        let one_line = req.body.split_whitespace().collect::<Vec<_>>().join(" ");
        s.push_str(&format!(" \\\n  -d '{one_line}'"));
    }
    s
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{Read, Write};
    use std::net::TcpListener;

    fn kv(k: &str, v: &str, enabled: bool) -> Kv {
        Kv {
            key: k.into(),
            value: v.into(),
            enabled,
            ..Default::default()
        }
    }

    #[test]
    fn reads_server_json_and_keeps_unknown_fields() {
        let json = r#"{"id":"r1","apiId":"a1","folder":"Charges","name":"Create","method":"POST","url":"{{base_url}}/v2",
            "params":null,"headers":[{"key":"X","value":"1","enabled":true}],"bodyMode":"json","body":"{}",
            "auth":{"type":"bearer","token":"{{secret_key}}"},"preScript":"","postScript":"test()",
            "settings":{"timeoutMs":500},"operationKey":"POST /v2","updatedAt":"2026-01-01T00:00:00Z"}"#;
        let doc: RequestDoc = serde_json::from_str(json).unwrap();
        assert_eq!(doc.auth.kind, "bearer");
        assert!(doc.params.is_empty());
        assert_eq!(doc.post_script, "test()");
        let back = serde_json::to_value(&doc).unwrap();
        assert_eq!(back["settings"]["timeoutMs"], 500);
        assert_eq!(back["apiId"], "a1");
        assert_eq!(back["bodyMode"], "json");
        assert_eq!(back["auth"]["type"], "bearer");
    }

    #[test]
    fn sends_locally_with_params_headers_auth_and_body() {
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
        let res = send(&RequestDoc {
            method: "POST".into(),
            url: format!("http://{addr}/things"),
            params: vec![kv("expand", "a b", true), kv("off", "x", false)],
            headers: vec![kv("X-Api-Key", "secret", true), kv("X-Off", "1", false)],
            auth: Auth {
                kind: "bearer".into(),
                token: "tok".into(),
                ..Default::default()
            },
            body_mode: "json".into(),
            body: r#"{"a":1}"#.into(),
            ..RequestDoc::new_local("t")
        })
        .unwrap();
        let seen = server.join().unwrap().to_ascii_lowercase();
        assert!(
            seen.starts_with("post /things?expand=a+b http/1.1"),
            "{seen}"
        );
        assert!(seen.contains("x-api-key: secret") && !seen.contains("x-off"));
        assert!(seen.contains("authorization: bearer tok"));
        assert!(seen.contains("content-type: application/json"));
        assert!(seen.ends_with(r#"{"a":1}"#));
        assert_eq!(res.status, 201);
        assert!(res.headers.iter().any(|(k, v)| k == "x-test" && v == "yes"));
        assert_eq!(res.body, "{\n  \"ok\": true\n}");
    }

    #[test]
    fn needs_a_url_and_builds_curl() {
        assert!(send(&RequestDoc::new_local("t")).is_err());
        let c = curl(&RequestDoc {
            url: "https://x.test".into(),
            headers: vec![kv("A", "b", true)],
            ..RequestDoc::new_local("t")
        });
        assert_eq!(c, "curl -X GET 'https://x.test' \\\n  -H 'A: b'");
    }
}
