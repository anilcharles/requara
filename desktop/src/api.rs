//! A small blocking client for the Requara server's HTTP API.
//! Calls run on GPUI's background executor, never on the UI thread.

use std::time::Duration;

use anyhow::{Result, anyhow};
use serde::Deserialize;
use serde::de::DeserializeOwned;
use serde_json::{Value, json};

use crate::local::{RequestDoc, null_default};

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
    #[serde(default)]
    pub can_edit: bool,
    #[serde(default)]
    pub can_publish: bool,
    /// Instance admin (listed in REQUARA_ADMIN_EMAILS on the server).
    #[serde(default)]
    pub is_admin: bool,
    /// The admin's banner (`GET /api/instance`), filled in by `Client::me`.
    #[serde(skip)]
    pub banner: Option<Banner>,
}

/// A message an instance admin shows to everyone.
#[derive(Clone, Debug, Deserialize)]
pub struct Banner {
    pub message: String,
    #[serde(default)]
    pub level: String, // info | warning | danger
}

#[derive(Debug, Deserialize)]
struct Instance {
    banner: Option<Banner>,
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
    #[serde(default)]
    pub description: String,
    pub status: String,
    #[serde(default)]
    pub version: String,
    #[serde(default)]
    pub owner: String,
    #[serde(default)]
    pub updated_at: String,
    #[serde(default)]
    pub endpoints: i64,
    #[serde(default)]
    pub has_draft_changes: bool,
    #[serde(default, rename = "calls24h")]
    pub calls_24h: i64,
    #[serde(default)]
    pub p95_ms: Option<f64>,
    #[serde(default)]
    pub error_rate: Option<f64>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct ApiList {
    #[serde(default)]
    pub items: Vec<ApiItem>,
    #[serde(default)]
    pub counts: std::collections::HashMap<String, i64>,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct HistoryItem {
    pub id: String,
    #[serde(default)]
    pub method: String,
    #[serde(default)]
    pub url: String,
    #[serde(default)]
    pub status: i64,
    #[serde(default)]
    pub duration_ms: i64,
    #[serde(default)]
    pub env_name: String,
    #[serde(default)]
    pub error: String,
    #[serde(default)]
    pub created_at: String,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct HistoryDetail {
    #[serde(default)]
    pub request_id: Option<String>,
    pub result: RunResult,
}

#[derive(Clone, Debug, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Variable {
    #[serde(default)]
    pub key: String,
    #[serde(default)]
    pub value: String,
    /// default | secret | script
    #[serde(default)]
    pub kind: String,
    #[serde(default)]
    pub enabled: bool,
    /// A secret that has a stored value (the value itself never leaves the server).
    #[serde(default)]
    pub has_value: bool,
}

/// One row of `PUT /api/environments/{id}/variables`.
#[derive(Clone, Debug, serde::Serialize, PartialEq)]
pub struct VariableInput {
    pub key: String,
    pub value: String,
    pub kind: String,
    pub enabled: bool,
    /// Leave a stored secret as it is.
    pub keep: bool,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct MockCall {
    #[serde(default)]
    pub method: String,
    #[serde(default)]
    pub path: String,
    #[serde(default)]
    pub status: i64,
    #[serde(default)]
    pub mock_name: String,
    #[serde(default)]
    pub created_at: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Collection {
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub version: String,
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
    /// "POST /v2/charges": the API operation this request calls, if any.
    #[serde(default)]
    pub operation_key: String,
}

// ----- Design and Docs -----

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ApiDetail {
    pub name: String,
    #[serde(default)]
    pub version: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct OperationRef {
    pub key: String,
}

/// One field of a request body. Sent back when the operation is saved, so
/// what the app doesn't edit (format, example) is kept in `extra`.
#[derive(Clone, Debug, Default, Deserialize, serde::Serialize, PartialEq)]
pub struct Field {
    #[serde(default)]
    pub name: String,
    #[serde(default, rename = "type")]
    pub kind: String,
    #[serde(default, rename = "enum", skip_serializing_if = "Option::is_none")]
    pub options: Option<Vec<String>>,
    #[serde(default)]
    pub required: bool,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub depth: usize,
    /// Added since the last published version.
    #[serde(default, rename = "new", skip_serializing)]
    pub is_new: bool,
    #[serde(flatten)]
    pub extra: serde_json::Map<String, Value>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Param {
    #[serde(default)]
    pub name: String,
    #[serde(default, rename = "in")]
    pub location: String,
    #[serde(default, rename = "type")]
    pub kind: String,
    #[serde(default)]
    pub required: bool,
    #[serde(default)]
    pub description: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct ResponseInfo {
    #[serde(default)]
    pub status: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub example: String,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct OperationDetail {
    pub key: String,
    #[serde(default)]
    pub method: String,
    #[serde(default)]
    pub path: String,
    #[serde(default)]
    pub summary: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub tag: String,
    #[serde(default, deserialize_with = "null_default")]
    pub fields: Vec<Field>,
    #[serde(default, deserialize_with = "null_default")]
    pub params: Vec<Param>,
    #[serde(default, deserialize_with = "null_default")]
    pub responses: Vec<ResponseInfo>,
    #[serde(default, deserialize_with = "null_default")]
    pub samples: std::collections::BTreeMap<String, String>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct LintIssue {
    #[serde(default)]
    pub rule: String,
    #[serde(default)]
    pub severity: String,
    #[serde(default)]
    pub message: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct LintReport {
    #[serde(default, deserialize_with = "null_default")]
    pub issues: Vec<LintIssue>,
    #[serde(default, deserialize_with = "null_default")]
    pub passed: Vec<String>,
    #[serde(default)]
    pub rules: i64,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Change {
    #[serde(default)]
    pub kind: String,
    #[serde(default)]
    pub text: String,
    #[serde(default)]
    pub operation: String,
    #[serde(default)]
    pub breaking: bool,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ChangeReport {
    #[serde(default)]
    pub from: String,
    #[serde(default)]
    pub suggested_version: String,
    #[serde(default, deserialize_with = "null_default")]
    pub changes: Vec<Change>,
    #[serde(default)]
    pub breaking: bool,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Docs {
    pub name: String,
    #[serde(default)]
    pub draft: bool,
    #[serde(default)]
    pub base_url: String,
    #[serde(default)]
    pub mock_url: String,
    #[serde(default, deserialize_with = "null_default")]
    pub operations: Vec<OperationDetail>,
}

/// What `PUT /api/apis/{id}/operation` takes.
#[derive(Clone, Debug, serde::Serialize, PartialEq)]
pub struct OperationInput {
    pub key: String,
    pub method: String,
    pub path: String,
    pub summary: String,
    pub description: String,
    pub fields: Vec<Field>,
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
    pub request_headers: Option<Vec<KV>>,
    #[serde(default)]
    pub tests: Option<Vec<TestResult>>,
    #[serde(default)]
    pub timeline: Option<Vec<Phase>>,
    #[serde(default)]
    pub logs: Option<Vec<String>>,
    #[serde(default)]
    pub script_error: Option<String>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct TestResult {
    pub name: String,
    pub pass: bool,
    #[serde(default)]
    pub error: Option<String>,
    #[serde(default)]
    pub ms: f64,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Phase {
    pub name: String,
    #[serde(default)]
    pub start_ms: f64,
    #[serde(default)]
    pub ms: f64,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Environment {
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub count: i64,
    #[serde(default)]
    pub color: String,
    #[serde(default)]
    pub is_global: bool,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Environments {
    #[serde(default)]
    pub items: Vec<Environment>,
    #[serde(default)]
    pub active_id: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct SendResult {
    pub result: RunResult,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Mock {
    pub id: String,
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
            .user_agent(concat!("Requara-Desktop/", env!("CARGO_PKG_VERSION")))
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

    fn request_builder(
        &self,
        method: reqwest::Method,
        path: &str,
    ) -> reqwest::blocking::RequestBuilder {
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
            self.request_builder(reqwest::Method::GET, path)
                .send()
                .map_err(connect_error)?,
        )
    }

    fn post<T: DeserializeOwned>(&self, path: &str, body: Value) -> Result<T> {
        Self::read(
            self.request_builder(reqwest::Method::POST, path)
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

    /// Who is signed in, with the server's banner (older servers have none).
    pub fn me(&self) -> Result<Me> {
        let mut me: Me = self.get("/api/me")?;
        me.banner = self
            .get::<Instance>("/api/instance")
            .ok()
            .and_then(|i| i.banner)
            .filter(|b| !b.message.trim().is_empty());
        Ok(me)
    }

    pub fn workspaces(&self) -> Result<Vec<Workspace>> {
        self.get("/api/workspaces")
    }

    pub fn apis(&self) -> Result<ApiList> {
        self.get("/api/apis")
    }

    fn send_json<T: DeserializeOwned>(
        &self,
        method: reqwest::Method,
        path: &str,
        body: Value,
    ) -> Result<T> {
        Self::read(
            self.request_builder(method, path)
                .json(&body)
                .send()
                .map_err(connect_error)?,
        )
    }

    pub fn api(&self, id: &str) -> Result<ApiDetail> {
        self.get(&format!("/api/apis/{id}"))
    }

    pub fn operations(&self, api_id: &str) -> Result<Vec<OperationRef>> {
        self.get(&format!("/api/apis/{api_id}/operations"))
    }

    pub fn operation(&self, api_id: &str, key: &str) -> Result<OperationDetail> {
        self.get(&format!("/api/apis/{api_id}/operation?key={}", encode(key)))
    }

    /// Saves the draft of an operation; returns its key (it changes with the method or path).
    pub fn save_operation(&self, api_id: &str, input: &OperationInput) -> Result<String> {
        let v: Value = self.send_json(
            reqwest::Method::PUT,
            &format!("/api/apis/{api_id}/operation"),
            json!(input),
        )?;
        Ok(v.get("key")
            .and_then(|k| k.as_str())
            .unwrap_or(&input.key)
            .to_string())
    }

    pub fn lint(&self, api_id: &str) -> Result<LintReport> {
        self.get(&format!("/api/apis/{api_id}/lint"))
    }

    pub fn changes(&self, api_id: &str) -> Result<ChangeReport> {
        self.get(&format!("/api/apis/{api_id}/changes"))
    }

    pub fn publish(&self, api_id: &str, version: &str, notify: bool) -> Result<String> {
        let v: Value = self.post(
            &format!("/api/apis/{api_id}/publish"),
            json!({ "version": version, "notify": notify }),
        )?;
        Ok(v.get("version")
            .and_then(|k| k.as_str())
            .unwrap_or(version)
            .to_string())
    }

    pub fn discard(&self, api_id: &str) -> Result<()> {
        let _: Value = self.post(&format!("/api/apis/{api_id}/discard"), json!({}))?;
        Ok(())
    }

    /// `draft` previews unpublished changes.
    pub fn docs(&self, api_id: &str, draft: bool) -> Result<Docs> {
        self.get(&format!(
            "/api/apis/{api_id}/docs?source={}",
            if draft { "draft" } else { "" }
        ))
    }

    /// The OpenAPI document, as "yaml" or "json".
    pub fn spec(&self, api_id: &str, format: &str) -> Result<String> {
        let res = self
            .request_builder(
                reqwest::Method::GET,
                &format!("/api/apis/{api_id}/spec?format={format}"),
            )
            .send()
            .map_err(connect_error)?;
        if !res.status().is_success() {
            return Err(anyhow!("Couldn't download the spec ({})", res.status()));
        }
        Ok(res.text()?)
    }

    pub fn history(&self) -> Result<Vec<HistoryItem>> {
        self.get("/api/history")
    }

    pub fn history_item(&self, id: &str) -> Result<HistoryDetail> {
        self.get(&format!("/api/history/{id}"))
    }

    pub fn clear_history(&self) -> Result<()> {
        let _: Value = self.send_json(reqwest::Method::DELETE, "/api/history", json!({}))?;
        Ok(())
    }

    pub fn variables(&self, env_id: &str) -> Result<Vec<Variable>> {
        self.get(&format!("/api/environments/{env_id}/variables"))
    }

    pub fn save_variables(&self, env_id: &str, rows: &[VariableInput]) -> Result<Vec<Variable>> {
        self.send_json(
            reqwest::Method::PUT,
            &format!("/api/environments/{env_id}/variables"),
            json!(rows),
        )
    }

    pub fn create_environment(&self, name: &str) -> Result<String> {
        let v: Value = self.post(
            "/api/environments",
            json!({ "name": name, "color": "#1677FF" }),
        )?;
        Ok(v.get("id")
            .and_then(|i| i.as_str())
            .unwrap_or_default()
            .to_string())
    }

    pub fn mock_calls(&self) -> Result<Vec<MockCall>> {
        self.get("/api/mocks/calls")
    }

    pub fn set_mock_enabled(&self, id: &str, enabled: bool) -> Result<()> {
        let _: Value = self.send_json(
            reqwest::Method::PATCH,
            &format!("/api/mocks/{id}"),
            json!({ "enabled": enabled }),
        )?;
        Ok(())
    }

    pub fn collections(&self) -> Result<Vec<Collection>> {
        self.get("/api/collections")
    }

    pub fn request(&self, id: &str) -> Result<RequestDoc> {
        self.get(&format!("/api/requests/{id}"))
    }

    pub fn save_request(&self, doc: &RequestDoc) -> Result<RequestDoc> {
        Self::read(
            self.request_builder(reqwest::Method::PUT, &format!("/api/requests/{}", doc.id))
                .json(doc)
                .send()
                .map_err(connect_error)?,
        )
    }

    /// Sends the request from the organization's server, with unsaved edits
    /// and the chosen environment.
    pub fn send(&self, draft: &RequestDoc, environment_id: Option<&str>) -> Result<SendResult> {
        self.post(
            &format!("/api/requests/{}/send", draft.id),
            json!({ "draft": draft, "environmentId": environment_id.unwrap_or("") }),
        )
    }

    pub fn environments(&self) -> Result<Environments> {
        self.get("/api/environments")
    }

    pub fn mocks(&self) -> Result<Vec<Mock>> {
        // "Calls today" follows this computer's day, not the server's.
        let tz = jiff::tz::TimeZone::system()
            .iana_name()
            .unwrap_or("UTC")
            .to_string();
        self.get(&format!("/api/mocks?tz={}", tz.replace('/', "%2F")))
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
        anyhow!("Can't reach the Requara server ({e}). Check the server address and your network.")
    } else {
        anyhow!(e)
    }
}

fn encode(s: &str) -> String {
    url::form_urlencoded::byte_serialize(s.as_bytes()).collect()
}
