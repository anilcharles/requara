//! A small blocking client for the Speklo server's HTTP API.
//! Calls run on GPUI's background executor, never on the UI thread.

use std::time::Duration;

use anyhow::{Result, anyhow};
use serde::Deserialize;
use serde::de::DeserializeOwned;
use serde_json::{Value, json};

/// The server API versions this build understands (`/api/version` → `api`).
pub const SUPPORTED_API: std::ops::RangeInclusive<u32> = 1..=1;

#[derive(Clone)]
pub struct Client {
    base: String,
    token: Option<String>,
    http: reqwest::blocking::Client,
}

#[derive(Clone, Debug, Deserialize)]
pub struct ServerVersion {
    pub version: String,
    pub api: u32,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Me {
    pub name: String,
    pub email: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Workspace {
    pub name: String,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ApiItem {
    pub id: String,
    pub name: String,
    pub status: String,
    #[serde(default)]
    pub version: String,
    #[serde(default)]
    pub endpoints: i64,
    #[serde(default)]
    pub has_draft_changes: bool,
}

#[derive(Deserialize)]
struct ApiList {
    items: Vec<ApiItem>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Operation {
    pub method: String,
    pub path: String,
    #[serde(default)]
    pub summary: String,
    #[serde(default)]
    pub deprecated: bool,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Collection {
    pub name: String,
    #[serde(default)]
    pub folders: Vec<Folder>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Folder {
    pub name: String,
    #[serde(default)]
    pub requests: Vec<SavedRequest>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct SavedRequest {
    pub id: String,
    pub name: String,
    pub method: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct KV {
    pub key: String,
    pub value: String,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct RunResult {
    pub status: i64,
    #[serde(default)]
    pub status_text: String,
    #[serde(default)]
    pub duration_ms: i64,
    #[serde(default)]
    pub size_bytes: i64,
    #[serde(default)]
    pub headers: Vec<KV>,
    #[serde(default)]
    pub body: String,
    #[serde(default)]
    pub error: Option<String>,
    #[serde(default)]
    pub url: String,
    #[serde(default)]
    pub method: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct SendResult {
    pub result: RunResult,
    #[serde(default)]
    pub environment: String,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Mock {
    pub name: String,
    pub api_name: String,
    #[serde(default)]
    pub api_version: String,
    pub url: String,
    pub enabled: bool,
    #[serde(default)]
    pub latency_ms: i64,
    #[serde(default)]
    pub error_rate: i64,
    #[serde(default)]
    pub calls_today: i64,
}

/// A way to sign in that the server offers (`GET /auth/providers`).
#[derive(Clone, Debug, Deserialize)]
pub struct Provider {
    pub id: String,
    pub name: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Providers {
    pub org: String,
    pub providers: Vec<Provider>,
}

#[derive(Deserialize)]
pub struct TokenResponse {
    pub token: String,
}

impl Client {
    pub fn new(base: &str, token: Option<String>) -> Result<Self> {
        let http = reqwest::blocking::Client::builder()
            .user_agent(concat!("Speklo-Desktop/", env!("CARGO_PKG_VERSION")))
            .timeout(Duration::from_secs(60))
            .build()?;
        Ok(Self {
            base: base.trim_end_matches('/').to_string(),
            token,
            http,
        })
    }

    pub fn base(&self) -> &str {
        &self.base
    }

    fn request(&self, method: reqwest::Method, path: &str) -> reqwest::blocking::RequestBuilder {
        let mut rb = self.http.request(method, format!("{}{}", self.base, path));
        if let Some(t) = &self.token {
            rb = rb.bearer_auth(t);
        }
        rb
    }

    fn read<T: DeserializeOwned>(res: reqwest::blocking::Response) -> Result<T> {
        let status = res.status();
        let bytes = res.bytes()?;
        if !status.is_success() {
            // The server answers {"error": "..."} or a problem+json {"title": "..."}.
            let msg = serde_json::from_slice::<Value>(&bytes)
                .ok()
                .and_then(|v| {
                    v.get("error")
                        .or_else(|| v.get("title"))
                        .and_then(|m| m.as_str())
                        .map(str::to_string)
                })
                .unwrap_or_else(|| format!("{status}"));
            return Err(if status == reqwest::StatusCode::UNAUTHORIZED {
                anyhow!(Unauthorized(msg))
            } else {
                anyhow!(msg)
            });
        }
        serde_json::from_slice(&bytes)
            .map_err(|e| anyhow!("Unexpected response from the server: {e}"))
    }

    fn get<T: DeserializeOwned>(&self, path: &str) -> Result<T> {
        Self::read(
            self.request(reqwest::Method::GET, path)
                .send()
                .map_err(connect_error)?,
        )
    }

    fn post<T: DeserializeOwned>(&self, path: &str, body: Value) -> Result<T> {
        Self::read(
            self.request(reqwest::Method::POST, path)
                .json(&body)
                .send()
                .map_err(connect_error)?,
        )
    }

    pub fn version(&self) -> Result<ServerVersion> {
        self.get("/api/version")
    }

    pub fn providers(&self) -> Result<Providers> {
        self.get("/auth/providers")
    }

    pub fn exchange(&self, code: &str, verifier: &str) -> Result<TokenResponse> {
        self.post(
            "/auth/desktop/token",
            json!({ "code": code, "verifier": verifier }),
        )
    }

    pub fn me(&self) -> Result<Me> {
        self.get("/api/me")
    }

    pub fn workspaces(&self) -> Result<Vec<Workspace>> {
        self.get("/api/workspaces")
    }

    pub fn apis(&self) -> Result<Vec<ApiItem>> {
        Ok(self.get::<ApiList>("/api/apis")?.items)
    }

    pub fn operations(&self, api_id: &str) -> Result<Vec<Operation>> {
        self.get(&format!("/api/apis/{api_id}/operations"))
    }

    pub fn collections(&self) -> Result<Vec<Collection>> {
        self.get("/api/collections")
    }

    pub fn send(&self, request_id: &str) -> Result<SendResult> {
        self.post(&format!("/api/requests/{request_id}/send"), json!({}))
    }

    pub fn mocks(&self) -> Result<Vec<Mock>> {
        self.get("/api/mocks")
    }

    /// Ends this desktop session on the server.
    pub fn logout(&self) -> Result<()> {
        let _: Value = self.post("/auth/logout", json!({}))?;
        Ok(())
    }
}

/// The session token is no longer valid; the app should ask to sign in again.
#[derive(Debug)]
pub struct Unauthorized(pub String);

impl std::fmt::Display for Unauthorized {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for Unauthorized {}

pub fn is_unauthorized(e: &anyhow::Error) -> bool {
    e.downcast_ref::<Unauthorized>().is_some()
}

fn connect_error(e: reqwest::Error) -> anyhow::Error {
    if e.is_connect() || e.is_timeout() {
        anyhow!("Can't reach the Speklo server ({e}). Check the server address and your network.")
    } else {
        anyhow!(e)
    }
}
