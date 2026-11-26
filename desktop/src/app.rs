//! The Requara desktop window, laid out like the Requara web app:
//!
//!   header:   logo · workspace · APIs / Requests / Design / Docs · environment · account
//!   sidebar:  Collections tree (filter, APIs → folders → requests) · History / Mock servers / Environments
//!   content:  request tabs, the request editor (Params, Authorization, Headers, Body, Scripts)
//!             and the response pane (Response, Headers, Tests, Timeline)
//!
//! It works with no account: "My requests" are kept on this computer and sent
//! straight from it. Signing in to the organization's Requara server adds the
//! team's collections, environments, APIs and mock servers.

use std::collections::HashMap;

use gpui_kit::base::input::TextareaState;
use gpui_kit::component::button::{Button, ButtonVariants as _};
use gpui_kit::component::checkbox::Checkbox;
use gpui_kit::component::input::{Input, InputEvent, InputState, Textarea};
use gpui_kit::component::switch::Switch;
use gpui_kit::component::{
    ActiveTheme as _, Disableable as _, Sizable as _, Theme, h_flex, v_flex,
};
use gpui_kit::prelude::FluentBuilder as _;
use gpui_kit::*;

use crate::api::{self, Client, Collection, Environments, Me, Provider, SendResult, Workspace};
use crate::auth;
use crate::local::{self, Auth, Kv, LocalResponse, LocalStore, RequestDoc};
use crate::settings::{Settings, normalize_server_url};

mod console;
mod design;
mod pages;

actions!(requara, [SaveRequest, SendRequest, ToggleConsole]);

/// ⌘S saves and ⌘↵ sends the open request (Ctrl on Windows and Linux).
pub fn bind_keys(cx: &mut App) {
    cx.bind_keys([
        KeyBinding::new(
            if cfg!(target_os = "macos") {
                "cmd-s"
            } else {
                "ctrl-s"
            },
            SaveRequest,
            None,
        ),
        KeyBinding::new(
            if cfg!(target_os = "macos") {
                "cmd-enter"
            } else {
                "ctrl-enter"
            },
            SendRequest,
            None,
        ),
        // ⌥⌘C, as in the web app.
        KeyBinding::new(
            if cfg!(target_os = "macos") {
                "cmd-alt-c"
            } else {
                "ctrl-alt-c"
            },
            ToggleConsole,
            None,
        ),
    ]);
}

/// The web app's primary blue (Ant Design).
const PRIMARY: u32 = 0x1677FF;
const LOGO_DOT: u32 = 0x4F46E5;
const MONO: &str = "Menlo";

/// Buttons and highlights use the same blue as the web app.
pub fn apply_brand(cx: &mut App) {
    // `Theme::update` keeps the colours and the tokens buttons paint from in step.
    Theme::update(cx, |theme| {
        let c = &mut theme.colors;
        c.primary = rgb(PRIMARY).into();
        c.primary_hover = rgb(0x4096FF).into();
        c.primary_active = rgb(0x0958D9).into();
        c.primary_foreground = rgb(0xFFFFFF).into();
        c.ring = rgb(PRIMARY).into();
        c.button_primary = rgb(PRIMARY).into();
        c.button_primary_hover = rgb(0x4096FF).into();
        c.button_primary_active = rgb(0x0958D9).into();
        c.button_primary_foreground = rgb(0xFFFFFF).into();
    });
}

/// Something loaded or sent in the background.
enum Load<T> {
    Idle,
    Loading,
    Ready(T),
    Failed(String),
}

impl<T> Load<T> {
    fn is_loading(&self) -> bool {
        matches!(self, Load::Loading)
    }
    fn ready(&self) -> Option<&T> {
        match self {
            Load::Ready(v) => Some(v),
            _ => None,
        }
    }
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum View {
    Apis,
    Requests,
    Design,
    Docs,
    History,
    Mocks,
    Environments,
}

impl View {
    const NAV: [View; 4] = [View::Apis, View::Requests, View::Design, View::Docs];
    const FOOTER: [View; 3] = [View::History, View::Mocks, View::Environments];

    fn label(self) -> &'static str {
        match self {
            View::Apis => "APIs",
            View::Requests => "Requests",
            View::Design => "Design",
            View::Docs => "Docs",
            View::History => "History",
            View::Mocks => "Mock servers",
            View::Environments => "Environments",
        }
    }
    /// Shown next to the Collections sidebar.
    fn has_sidebar(self) -> bool {
        matches!(
            self,
            View::Requests
                | View::Design
                | View::Docs
                | View::History
                | View::Mocks
                | View::Environments
        )
    }
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum ConfigTab {
    Params,
    Auth,
    Headers,
    Body,
    Scripts,
    Settings,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum RespTab {
    Body,
    Headers,
    Tests,
    Timeline,
}

/// The one dropdown that can be open.
#[derive(Clone, Copy, PartialEq, Eq)]
enum Menu {
    Method,
    AuthType,
    SendMore,
    Env,
    Account,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Origin {
    /// Saved on this computer, sent from this computer.
    Local,
    /// Saved on the organization's server, sent from there.
    Team,
}

struct KvRow {
    key: Entity<InputState>,
    value: Entity<InputState>,
    desc: Entity<InputState>,
    enabled: bool,
}

/// A response from a local send or from the organization's server.
struct Resp {
    status: i64,
    status_text: String,
    duration_ms: i64,
    size_bytes: i64,
    headers: Vec<(String, String)>,
    request_headers: usize,
    body: String,
    error: Option<String>,
    tests: Vec<api::TestResult>,
    timeline: Vec<api::Phase>,
    logs: Vec<String>,
    script_error: Option<String>,
}

impl From<LocalResponse> for Resp {
    fn from(r: LocalResponse) -> Self {
        Resp {
            status: r.status as i64,
            status_text: r.status_text,
            duration_ms: r.duration_ms as i64,
            size_bytes: r.size_bytes as i64,
            headers: r.headers,
            request_headers: 0,
            body: r.body,
            error: None,
            tests: vec![],
            timeline: vec![],
            logs: vec![],
            script_error: None,
        }
    }
}

impl From<SendResult> for Resp {
    fn from(s: SendResult) -> Self {
        let r = s.result;
        // Pretty-print JSON, like the web app.
        let body = serde_json::from_str::<serde_json::Value>(&r.body)
            .ok()
            .and_then(|v| serde_json::to_string_pretty(&v).ok())
            .unwrap_or(r.body);
        Resp {
            status: r.status,
            status_text: r.status_text,
            duration_ms: r.duration_ms,
            size_bytes: r.size_bytes,
            headers: r.headers.into_iter().map(|h| (h.key, h.value)).collect(),
            request_headers: r.request_headers.map_or(0, |h| h.len()),
            body,
            error: r.error.filter(|e| !e.is_empty()),
            tests: r.tests.unwrap_or_default(),
            timeline: r.timeline.unwrap_or_default(),
            logs: r.logs.unwrap_or_default(),
            script_error: r.script_error.filter(|e| !e.is_empty()),
        }
    }
}

/// One open request tab. Each keeps its own inputs, so unsaved edits survive
/// switching tabs.
struct Editor {
    origin: Origin,
    id: String,
    api: String,
    folder: String,
    /// As last saved; the tab is "dirty" when the inputs differ from it.
    doc: RequestDoc,
    name: Entity<InputState>,
    url: Entity<InputState>,
    method: String,
    params: Vec<KvRow>,
    headers: Vec<KvRow>,
    auth_kind: String,
    auth_in: String,
    auth_token: Entity<InputState>,
    auth_user: Entity<InputState>,
    auth_pass: Entity<InputState>,
    auth_key: Entity<InputState>,
    auth_value: Entity<InputState>,
    body_mode: String,
    body: Entity<TextareaState>,
    pre: Entity<TextareaState>,
    post: Entity<TextareaState>,
    tab: ConfigTab,
    pre_script_tab: bool,
    /// Per-request settings (followRedirects, verifySSL, encodeURL, timeoutMs).
    settings: serde_json::Map<String, serde_json::Value>,
    timeout: Entity<InputState>,
    result: Load<Resp>,
    resp_tab: RespTab,
    saving: bool,
    notice: Option<String>,
    _subs: Vec<Subscription>,
}

fn text_input(
    placeholder: &'static str,
    value: &str,
    subs: &mut Vec<Subscription>,
    window: &mut Window,
    cx: &mut Context<RequaraApp>,
) -> Entity<InputState> {
    let value = value.to_string();
    let e = cx.new(|cx| {
        InputState::new(window, cx)
            .placeholder(placeholder)
            .default_value(value)
    });
    // Re-render the window as people type (dirty dot, blank table row).
    subs.push(cx.subscribe(&e, |_, _, _: &InputEvent, cx| cx.notify()));
    e
}

fn text_area(
    placeholder: &'static str,
    value: &str,
    subs: &mut Vec<Subscription>,
    window: &mut Window,
    cx: &mut Context<RequaraApp>,
) -> Entity<TextareaState> {
    let value = value.to_string();
    let e = cx.new(|cx| {
        TextareaState::new(window, cx)
            .placeholder(placeholder)
            .default_value(value)
    });
    subs.push(cx.subscribe(&e, |_, _, _: &InputEvent, cx| cx.notify()));
    e
}

fn kv_row(
    kv: &Kv,
    subs: &mut Vec<Subscription>,
    window: &mut Window,
    cx: &mut Context<RequaraApp>,
) -> KvRow {
    KvRow {
        key: text_input("Key", &kv.key, subs, window, cx),
        value: text_input("Value", &kv.value, subs, window, cx),
        desc: text_input("Description", &kv.description, subs, window, cx),
        enabled: kv.enabled,
    }
}

impl KvRow {
    fn read(&self, cx: &App) -> Kv {
        Kv {
            key: self.key.read(cx).value().to_string(),
            value: self.value.read(cx).value().to_string(),
            description: self.desc.read(cx).value().to_string(),
            enabled: self.enabled,
        }
    }
    fn is_blank(&self, cx: &App) -> bool {
        let kv = self.read(cx);
        kv.key.is_empty() && kv.value.is_empty() && kv.description.is_empty()
    }
}

impl Editor {
    fn new(
        origin: Origin,
        doc: RequestDoc,
        api: String,
        folder: String,
        window: &mut Window,
        cx: &mut Context<RequaraApp>,
    ) -> Self {
        let mut subs = vec![];
        let s = &mut subs;
        let a = doc.auth.clone();
        let settings = doc
            .extra
            .get("settings")
            .and_then(|v| v.as_object())
            .cloned()
            .unwrap_or_default();
        let timeout_text = settings
            .get("timeoutMs")
            .and_then(|v| v.as_i64())
            .filter(|ms| *ms > 0)
            .map(|ms| ms.to_string())
            .unwrap_or_default();
        let url = text_input("https://api.example.com/path", &doc.url, s, window, cx);
        // Enter in the URL bar sends, like the web app.
        s.push(cx.subscribe(&url, |this, _, event: &InputEvent, cx| {
            if matches!(event, InputEvent::PressEnter { .. }) {
                this.send_active(cx);
            }
        }));
        Editor {
            origin,
            id: doc.id.clone(),
            api,
            folder,
            name: text_input("Request name", &doc.name, s, window, cx),
            url,
            method: if doc.method.is_empty() {
                "GET".into()
            } else {
                doc.method.clone()
            },
            params: doc
                .params
                .iter()
                .map(|kv| kv_row(kv, s, window, cx))
                .collect(),
            headers: doc
                .headers
                .iter()
                .map(|kv| kv_row(kv, s, window, cx))
                .collect(),
            auth_kind: if a.kind.is_empty() {
                "inherit".into()
            } else {
                a.kind.clone()
            },
            auth_in: if a.location.is_empty() {
                "header".into()
            } else {
                a.location.clone()
            },
            auth_token: text_input("{{secret_key}}", &a.token, s, window, cx),
            auth_user: text_input("Username", &a.username, s, window, cx),
            auth_pass: text_input("Password", &a.password, s, window, cx),
            auth_key: text_input("X-API-Key", &a.key, s, window, cx),
            auth_value: text_input("{{api_key}}", &a.value, s, window, cx),
            body_mode: if doc.body_mode.is_empty() {
                "none".into()
            } else {
                doc.body_mode.clone()
            },
            body: text_area("Request body", &doc.body, s, window, cx),
            pre: text_area(
                "// runs before the request: req, env.get/set, console.log",
                &doc.pre_script,
                s,
                window,
                cx,
            ),
            post: text_area(
                "// runs after the response: res.status, res.json, test(), expect(), env.set",
                &doc.post_script,
                s,
                window,
                cx,
            ),
            tab: if doc.body_mode == "none" || doc.body_mode.is_empty() {
                ConfigTab::Params
            } else {
                ConfigTab::Body
            },
            pre_script_tab: false,
            timeout: text_input("30000", &timeout_text, s, window, cx),
            settings,
            result: Load::Idle,
            resp_tab: RespTab::Body,
            saving: false,
            notice: None,
            doc,
            _subs: subs,
        }
    }

    /// Keeps one empty row at the end of each table; typing in it makes a new entry.
    fn ensure_blank_rows(&mut self, window: &mut Window, cx: &mut Context<RequaraApp>) {
        for rows in [&mut self.params, &mut self.headers] {
            if rows.last().is_none_or(|r| !r.is_blank(cx)) {
                rows.push(kv_row(
                    &Kv {
                        enabled: true,
                        ..Default::default()
                    },
                    &mut self._subs,
                    window,
                    cx,
                ));
            }
        }
    }

    /// The request as the inputs have it now.
    fn to_doc(&self, cx: &App) -> RequestDoc {
        let text = |e: &Entity<InputState>| e.read(cx).value().to_string();
        let rows = |rows: &Vec<KvRow>| {
            rows.iter()
                .filter(|r| !r.is_blank(cx))
                .map(|r| r.read(cx))
                .collect()
        };
        let mut auth = Auth {
            kind: self.auth_kind.clone(),
            ..Default::default()
        };
        match self.auth_kind.as_str() {
            "bearer" => auth.token = text(&self.auth_token),
            "basic" => {
                auth.username = text(&self.auth_user);
                auth.password = text(&self.auth_pass);
            }
            "apikey" => {
                auth.key = text(&self.auth_key);
                auth.value = text(&self.auth_value);
                auth.location = self.auth_in.clone();
            }
            _ => {}
        }
        // Don't count an untouched auth block as an edit.
        if auth.kind == self.doc.auth.kind
            && auth.kind != "apikey"
            && self.doc.auth.location.is_empty()
        {
            auth.location = String::new();
        }
        if self.doc.auth.kind.is_empty() && auth.kind == "inherit" {
            auth.kind = String::new();
        }
        let mut extra = self.doc.extra.clone();
        let mut settings = self.settings.clone();
        match self.timeout.read(cx).value().trim().parse::<i64>() {
            Ok(ms) if ms > 0 => {
                settings.insert("timeoutMs".into(), ms.into());
            }
            _ if settings.contains_key("timeoutMs") => {
                settings.insert("timeoutMs".into(), 0.into());
            }
            _ => {}
        }
        if !settings.is_empty() || extra.contains_key("settings") {
            extra.insert("settings".into(), settings.into());
        }
        RequestDoc {
            name: text(&self.name),
            method: self.method.clone(),
            url: text(&self.url),
            params: rows(&self.params),
            headers: rows(&self.headers),
            body_mode: if self.doc.body_mode.is_empty() && self.body_mode == "none" {
                String::new()
            } else {
                self.body_mode.clone()
            },
            body: self.body.read(cx).value().to_string(),
            auth,
            pre_script: self.pre.read(cx).value().to_string(),
            post_script: self.post.read(cx).value().to_string(),
            extra,
            ..self.doc.clone()
        }
    }

    fn is_dirty(&self, cx: &App) -> bool {
        self.to_doc(cx) != self.doc
    }
}

/// The optional sign-in panel.
enum SignIn {
    Closed,
    Server,
    Checking,
    Choose {
        server: String,
        org: String,
        providers: Vec<Provider>,
    },
    Waiting {
        server: String,
        provider: String,
    },
}

pub struct RequaraApp {
    settings: Settings,
    view: View,
    menu: Option<Menu>,

    // ----- requests -----
    store: LocalStore,
    editors: Vec<Editor>,
    active: usize,
    /// Team requests fetched and waiting for the next render to get their inputs.
    pending_open: Vec<(RequestDoc, String, String)>,
    opening: Option<String>,
    /// A dirty team tab the person asked to close.
    confirm_close: Option<String>,
    filter: Entity<InputState>,
    tree_open: HashMap<String, bool>,
    banner: Option<String>,

    // ----- organization (optional) -----
    client: Option<Client>,
    server_input: Entity<InputState>,
    sign_in: SignIn,
    sign_in_attempt: u64,
    sign_in_error: Option<String>,
    me: Load<(Me, Vec<Workspace>)>,
    envs: Load<Environments>,
    active_env: Option<String>,
    collections: Load<Vec<Collection>>,
    /// APIs, History, Environments and Mock servers.
    pages: pages::Pages,
    /// Design and Docs.
    design: design::Design,
    /// The log of this session's sends, at the bottom of the window.
    console: console::Console,

    _subs: Vec<Subscription>,
}

impl RequaraApp {
    pub fn new(window: &mut Window, cx: &mut Context<Self>) -> Self {
        let settings = Settings::load();
        let store = LocalStore::load();
        let mut subs = vec![];
        let filter = text_input("Filter", "", &mut subs, window, cx);
        let server_input = text_input(
            "requara.your-company.com",
            settings.server_url.as_deref().unwrap_or(""),
            &mut subs,
            window,
            cx,
        );
        apply_brand(cx);
        // Follow the system's light/dark setting, like the web app's default.
        subs.push(cx.observe_window_appearance(window, |_, window, cx| {
            Theme::sync_system_appearance(Some(window), cx);
            apply_brand(cx);
        }));

        // Signed in before? Reconnect quietly; the app still opens on the requests.
        let client = settings
            .server_url
            .as_deref()
            .and_then(|url| Some((url, auth::load_token(url)?)))
            .and_then(|(url, token)| Client::new(url, Some(token)).ok());

        let pages = pages::Pages::new(&mut subs, window, cx);
        let design = design::Design::new(&mut subs, window, cx);
        let console = console::Console::new(&mut subs, window, cx);
        let mut this = Self {
            settings,
            view: View::Requests,
            menu: None,
            store,
            editors: vec![],
            active: 0,
            pending_open: vec![],
            opening: None,
            confirm_close: None,
            filter,
            tree_open: HashMap::new(),
            banner: None,
            client,
            server_input,
            sign_in: SignIn::Closed,
            sign_in_attempt: 0,
            sign_in_error: None,
            me: Load::Idle,
            envs: Load::Idle,
            active_env: None,
            collections: Load::Idle,
            pages,
            design,
            console,
            _subs: subs,
        };
        if let Some(first) = this.store.requests.first().cloned() {
            this.editors.push(Editor::new(
                Origin::Local,
                first,
                "My requests".into(),
                "Scratch".into(),
                window,
                cx,
            ));
        }
        if this.client.is_some() {
            this.load_organization(cx);
        }
        this
    }

    /// Screenshot mode (development builds only): go to the page and state
    /// named by REQUARA_SHOT_* so each screen can be rendered to a PNG.
    #[cfg(feature = "screenshot")]
    pub fn shot_setup(&mut self, cx: &mut Context<Self>) {
        let var = |k: &str| std::env::var(k).ok().filter(|v| !v.is_empty());
        if let (Some(server), Some(token)) = (var("REQUARA_SHOT_SERVER"), var("REQUARA_SHOT_TOKEN"))
            && let Ok(client) = Client::new(&server, Some(token))
        {
            self.client = Some(client);
            self.load_organization(cx);
        }
        let view = match var("REQUARA_SHOT_VIEW").as_deref() {
            Some("apis") => View::Apis,
            Some("design") => View::Design,
            Some("docs") => View::Docs,
            Some("history") => View::History,
            Some("mocks") => View::Mocks,
            Some("environments") => View::Environments,
            _ => View::Requests,
        };
        self.open_view(view, cx);
        if let Some(e) = self.editors.get_mut(self.active) {
            e.tab = match var("REQUARA_SHOT_TAB").as_deref() {
                Some("params") => ConfigTab::Params,
                Some("auth") => ConfigTab::Auth,
                Some("headers") => ConfigTab::Headers,
                Some("body") => ConfigTab::Body,
                Some("scripts") => ConfigTab::Scripts,
                Some("settings") => ConfigTab::Settings,
                _ => e.tab,
            };
        }
        if var("REQUARA_SHOT_PUBLISH").is_some() {
            self.design.shot_publish();
        }
        if var("REQUARA_SHOT_CONSOLE").is_some() {
            self.console.toggle();
        }
        match var("REQUARA_SHOT_SIGNIN").as_deref() {
            // What a server with three sign-in options set up looks like.
            Some("choose") => {
                let provider = |id: &str, name: &str| Provider {
                    id: id.into(),
                    name: name.into(),
                };
                self.sign_in = SignIn::Choose {
                    server: "https://requara.acme.com".into(),
                    org: "Acme".into(),
                    providers: vec![
                        provider("keycloak", "Acme SSO"),
                        provider("google", "Google"),
                        provider("github", "GitHub"),
                    ],
                };
            }
            // The whole sign-in, as typing the address and clicking the first
            // option would do it: "flow:localhost:8080".
            Some(s) if s.starts_with("flow:") => {
                self.check_server_at(s["flow:".len()..].to_string(), cx);
                cx.spawn(async move |this, cx| {
                    loop {
                        cx.background_executor()
                            .timer(std::time::Duration::from_millis(200))
                            .await;
                        let picked = this.update(cx, |this, cx| {
                            let SignIn::Choose {
                                server, providers, ..
                            } = &this.sign_in
                            else {
                                return false;
                            };
                            let (server, provider) = (server.clone(), providers[0].clone());
                            this.sign_in_with(server, provider, cx);
                            true
                        });
                        if !matches!(picked, Ok(false)) {
                            break;
                        }
                    }
                })
                .detach();
            }
            Some(_) => self.sign_in = SignIn::Server,
            None => {}
        }
        if var("REQUARA_SHOT_SEND").is_some() {
            self.send_active(cx);
        }
    }

    fn editor(&self) -> Option<&Editor> {
        self.editors.get(self.active)
    }

    // ----- opening, saving, sending -----

    fn activate(&mut self, id: &str) -> bool {
        match self.editors.iter().position(|e| e.id == id) {
            Some(i) => {
                self.active = i;
                self.view = View::Requests;
                self.menu = None;
                true
            }
            None => false,
        }
    }

    fn open_local(&mut self, id: &str, window: &mut Window, cx: &mut Context<Self>) {
        if !self.activate(id)
            && let Some(doc) = self.store.requests.iter().find(|r| r.id == id).cloned()
        {
            self.editors.push(Editor::new(
                Origin::Local,
                doc,
                "My requests".into(),
                "Scratch".into(),
                window,
                cx,
            ));
            self.active = self.editors.len() - 1;
            self.view = View::Requests;
        }
        cx.notify();
    }

    fn new_local(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let doc = RequestDoc::new_local("Untitled request");
        let id = doc.id.clone();
        self.store.put(doc);
        self.open_local(&id, window, cx);
        if let Some(e) = self.editor() {
            let url = e.url.clone();
            url.update(cx, |s, cx| s.focus(window, cx));
        }
    }

    fn open_team(&mut self, id: String, api: String, folder: String, cx: &mut Context<Self>) {
        if self.activate(&id) {
            cx.notify();
            return;
        }
        self.view = View::Requests;
        self.opening = Some(id.clone());
        self.banner = None;
        self.fetch(
            cx,
            move |c| c.request(&id),
            move |this, r| {
                this.opening = None;
                match r {
                    Ok(doc) => this.pending_open.push((doc, api, folder)),
                    Err(e) => this.banner = Some(e),
                }
            },
        );
        cx.notify();
    }

    fn save_active(&mut self, cx: &mut Context<Self>) {
        let Some(e) = self.editors.get_mut(self.active) else {
            return;
        };
        let doc = e.to_doc(cx);
        match e.origin {
            Origin::Local => {
                e.doc = doc.clone();
                e.notice = None;
                self.store.put(doc);
            }
            Origin::Team => {
                e.saving = true;
                let id = e.id.clone();
                self.fetch(
                    cx,
                    move |c| c.save_request(&doc),
                    move |this, r| {
                        let Some(e) = this.editors.iter_mut().find(|e| e.id == id) else {
                            return;
                        };
                        e.saving = false;
                        match r {
                            Ok(saved) => {
                                e.doc = saved;
                                e.notice = None;
                            }
                            Err(err) => e.notice = Some(format!("Couldn't save: {err}")),
                        }
                    },
                );
                // The name or method may have changed in the tree.
                self.reload_collections(cx);
            }
        }
        cx.notify();
    }

    fn send_active(&mut self, cx: &mut Context<Self>) {
        let Some(e) = self.editors.get_mut(self.active) else {
            return;
        };
        if e.result.is_loading() {
            return;
        }
        let doc = e.to_doc(cx);
        let sent = doc.clone();
        let id = e.id.clone();
        e.result = Load::Loading;
        self.menu = None;
        match e.origin {
            Origin::Local => {
                cx.spawn(async move |this, cx| {
                    let result = cx
                        .background_executor()
                        .spawn(async move { local::send(&doc) })
                        .await;
                    let _ = this.update(cx, |this, cx| {
                        if let Some(e) = this.editors.iter_mut().find(|e| e.id == id) {
                            e.result = match result {
                                Ok(r) => Load::Ready(r.into()),
                                Err(err) => Load::Failed(format!("{err:#}")),
                            };
                            this.console.log(&sent, String::new(), &e.result);
                        }
                        cx.notify();
                    });
                })
                .detach();
            }
            Origin::Team => {
                let env = self.active_env.clone();
                let env_name = self
                    .envs
                    .ready()
                    .and_then(|e| e.items.iter().find(|e| Some(&e.id) == env.as_ref()))
                    .map(|e| e.name.clone())
                    .unwrap_or_default();
                self.fetch(
                    cx,
                    move |c| c.send(&doc, env.as_deref()),
                    move |this, r| {
                        if let Some(e) = this.editors.iter_mut().find(|e| e.id == id) {
                            e.result = match r {
                                Ok(s) => Load::Ready(s.into()),
                                Err(err) => Load::Failed(err),
                            };
                            this.console.log(&sent, env_name, &e.result);
                        }
                    },
                );
            }
        }
        cx.notify();
    }

    fn close_tab(&mut self, id: &str, force: bool, cx: &mut Context<Self>) {
        let Some(i) = self.editors.iter().position(|e| e.id == id) else {
            return;
        };
        if !force && self.editors[i].is_dirty(cx) {
            match self.editors[i].origin {
                // Local requests are saved rather than lost.
                Origin::Local => {
                    let doc = self.editors[i].to_doc(cx);
                    self.store.put(doc);
                }
                Origin::Team => {
                    self.confirm_close = Some(id.to_string());
                    cx.notify();
                    return;
                }
            }
        }
        self.confirm_close = None;
        self.editors.remove(i);
        if self.active >= i && self.active > 0 {
            self.active -= 1;
        }
        cx.notify();
    }

    fn delete_local(&mut self, cx: &mut Context<Self>) {
        let Some(e) = self.editor() else { return };
        if e.origin != Origin::Local {
            return;
        }
        let id = e.id.clone();
        self.store.remove(&id);
        self.menu = None;
        self.close_tab(&id, true, cx);
    }

    // ----- organization server work, off the UI thread -----

    /// Runs `work` with the signed-in client on a background thread, then
    /// hands the result to `done` on the UI thread. An expired session signs
    /// the person out (their own requests stay).
    fn fetch<T: Send + 'static>(
        &mut self,
        cx: &mut Context<Self>,
        work: impl FnOnce(&Client) -> anyhow::Result<T> + Send + 'static,
        done: impl FnOnce(&mut Self, Result<T, String>) + 'static,
    ) {
        let Some(client) = self.client.clone() else {
            return;
        };
        cx.spawn(async move |this, cx| {
            let result = cx
                .background_executor()
                .spawn(async move { work(&client) })
                .await;
            let _ = this.update(cx, |this, cx| {
                match result {
                    Err(e) if api::is_unauthorized(&e) => this.signed_out(Some(format!("{e}")), cx),
                    Err(e) => done(this, Err(format!("{e:#}"))),
                    Ok(v) => done(this, Ok(v)),
                }
                cx.notify();
            });
        })
        .detach();
    }

    fn reload_collections(&mut self, cx: &mut Context<Self>) {
        self.fetch(
            cx,
            |c| c.collections(),
            |this, r| this.collections = r.map_or_else(Load::Failed, Load::Ready),
        );
    }

    /// Everything the shell shows once signed in.
    fn load_organization(&mut self, cx: &mut Context<Self>) {
        self.me = Load::Loading;
        self.fetch(
            cx,
            |c| Ok((c.me()?, c.workspaces()?)),
            |this, r| this.me = r.map_or_else(Load::Failed, Load::Ready),
        );
        self.collections = Load::Loading;
        self.reload_collections(cx);
        self.fetch(
            cx,
            |c| c.environments(),
            |this, r| {
                if let Ok(e) = &r {
                    this.active_env = (!e.active_id.is_empty()).then(|| e.active_id.clone());
                }
                this.envs = r.map_or_else(Load::Failed, Load::Ready);
            },
        );
        self.fetch(
            cx,
            |c| c.mocks(),
            |this, r| this.pages.mocks = r.map_or_else(Load::Failed, Load::Ready),
        );
    }

    fn open_view(&mut self, view: View, cx: &mut Context<Self>) {
        self.view = view;
        self.menu = None;
        self.load_page(view, cx);
        cx.notify();
    }

    // ----- optional sign-in -----

    fn open_sign_in(&mut self, cx: &mut Context<Self>) {
        self.sign_in = SignIn::Server;
        self.menu = None;
        cx.notify();
    }

    fn close_sign_in(&mut self, cx: &mut Context<Self>) {
        self.sign_in_attempt += 1; // forget a sign-in still waiting in the browser
        self.sign_in = SignIn::Closed;
        self.sign_in_error = None;
        cx.notify();
    }

    /// Step 1: check the server and fetch its sign-in options.
    fn check_server(&mut self, cx: &mut Context<Self>) {
        let typed = self.server_input.read(cx).value().to_string();
        self.check_server_at(typed, cx);
    }

    fn check_server_at(&mut self, typed: String, cx: &mut Context<Self>) {
        let Some(server) = normalize_server_url(&typed) else {
            self.sign_in_error = Some(
                "Enter your organization's Requara server, like requara.your-company.com".into(),
            );
            cx.notify();
            return;
        };
        self.sign_in = SignIn::Checking;
        self.sign_in_error = None;
        cx.notify();
        cx.spawn(async move |this, cx| {
            let url = server.clone();
            let result = cx
                .background_executor()
                .spawn(async move {
                    let c = Client::new(&url, None)?;
                    // Only talk to servers whose API this build understands.
                    let v = c.version()?;
                    if !api::SUPPORTED_API.contains(&v.api) {
                        anyhow::bail!(
                            "This server runs Requara {} (API v{}), which this app doesn't support. {}",
                            v.version,
                            v.api,
                            if v.api > *api::SUPPORTED_API.end() {
                                "Update the Requara app."
                            } else {
                                "Ask your admin to upgrade the server."
                            }
                        );
                    }
                    c.providers()
                })
                .await;
            let _ = this.update(cx, |this, cx| {
                if matches!(this.sign_in, SignIn::Closed) {
                    return;
                }
                match result {
                    Ok(p) if p.providers.is_empty() => {
                        this.sign_in = SignIn::Server;
                        this.sign_in_error = Some("This server has no sign-in options set up yet. Ask your admin.".into());
                    }
                    Ok(p) => this.sign_in = SignIn::Choose { server, org: p.org, providers: p.providers },
                    Err(e) => {
                        this.sign_in = SignIn::Server;
                        this.sign_in_error = Some(format!("{e:#}"));
                    }
                }
                cx.notify();
            });
        })
        .detach();
    }

    /// Step 2: sign in with the chosen provider in the browser.
    fn sign_in_with(&mut self, server: String, provider: Provider, cx: &mut Context<Self>) {
        self.sign_in_attempt += 1;
        let attempt = self.sign_in_attempt;
        self.sign_in = SignIn::Waiting {
            server: server.clone(),
            provider: provider.name.clone(),
        };
        self.sign_in_error = None;
        cx.notify();
        cx.spawn(async move |this, cx| {
            let url = server.clone();
            let result = cx
                .background_executor()
                .spawn(async move {
                    let token = auth::sign_in(&url, &provider.id)?;
                    auth::save_token(&url, &token)?;
                    Ok::<_, anyhow::Error>(token)
                })
                .await;
            let _ = this.update(cx, |this, cx| {
                if this.sign_in_attempt != attempt {
                    return; // closed or cancelled
                }
                match result.and_then(|token| Client::new(&server, Some(token))) {
                    Ok(client) => {
                        this.settings.server_url = Some(server);
                        let _ = this.settings.save();
                        this.client = Some(client);
                        this.sign_in = SignIn::Closed;
                        this.load_organization(cx);
                    }
                    Err(e) => {
                        this.sign_in = SignIn::Server;
                        this.sign_in_error = Some(format!("{e:#}"));
                    }
                }
                cx.notify();
            });
        })
        .detach();
    }

    fn sign_out(&mut self, cx: &mut Context<Self>) {
        if let Some(client) = self.client.clone() {
            cx.background_executor()
                .spawn(async move {
                    let _ = client.logout();
                })
                .detach();
        }
        self.signed_out(None, cx);
    }

    fn signed_out(&mut self, reason: Option<String>, cx: &mut Context<Self>) {
        if let Some(c) = self.client.take() {
            auth::delete_token(c.base());
        }
        self.menu = None;
        self.me = Load::Idle;
        self.envs = Load::Idle;
        self.active_env = None;
        self.collections = Load::Idle;
        self.pages.reset();
        self.design.reset();
        self.opening = None;
        self.confirm_close = None;
        // Team tabs can't be saved or sent any more; local ones stay.
        let active_id = self.editor().map(|e| e.id.clone());
        self.editors.retain(|e| e.origin == Origin::Local);
        self.active = active_id
            .and_then(|id| self.editors.iter().position(|e| e.id == id))
            .unwrap_or(0);
        if reason.is_some() {
            // The session ended on its own: say so where they'd sign in again.
            self.sign_in = SignIn::Server;
            self.sign_in_error = reason;
        }
        cx.notify();
    }

    fn toggle_menu(&mut self, m: Menu, cx: &mut Context<Self>) {
        self.menu = if self.menu == Some(m) { None } else { Some(m) };
        cx.notify();
    }

    // ----- header -----

    /// The instance admin's banner, above the header.
    fn render_banner(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let Load::Ready((me, _)) = &self.me else {
            return None;
        };
        let b = me.banner.as_ref()?;
        let t = cx.theme();
        let color = match b.level.as_str() {
            "danger" => t.danger,
            "warning" => t.warning,
            _ => t.info,
        };
        Some(
            div()
                .flex_none()
                .px_5()
                .py_2()
                .text_sm()
                .bg(color.opacity(0.12))
                .border_b_1()
                .border_color(color.opacity(0.4))
                .child(b.message.clone())
                .into_any_element(),
        )
    }

    fn render_header(&mut self, cx: &mut Context<Self>) -> AnyElement {
        let t = cx.theme();
        let primary: Hsla = rgb(PRIMARY).into();
        let workspace = match &self.me {
            Load::Ready((_, ws)) => ws
                .first()
                .map(|w| w.name.clone())
                .unwrap_or_else(|| "Workspace".into()),
            _ if self.client.is_some() => "Workspace".into(),
            // Signed out there is no workspace to name.
            _ => String::new(),
        };

        let nav = h_flex().h_full().children(View::NAV.map(|v| {
            let active = v == self.view;
            h_flex()
                .id(v.label())
                .h_full()
                .px_4()
                .text_sm()
                .border_b_2()
                .border_color(if active { primary } else { transparent_black() })
                .when(active, |d| d.text_color(primary))
                .hover(|d| d.text_color(primary))
                .child(v.label())
                .on_click(cx.listener(move |this, _, _, cx| this.open_view(v, cx)))
        }));

        let right: AnyElement = match &self.client {
            None => Button::new("open-sign-in")
                .primary()
                .label("Sign in to your organization")
                .on_click(cx.listener(|this, _, _, cx| this.open_sign_in(cx)))
                .into_any_element(),
            Some(client) => {
                let envs: Vec<_> = self
                    .envs
                    .ready()
                    .map(|e| e.items.iter().filter(|e| !e.is_global).cloned().collect())
                    .unwrap_or_default();
                let current = envs
                    .iter()
                    .find(|e| Some(&e.id) == self.active_env.as_ref());
                let env_label = current
                    .map(|e| e.name.clone())
                    .unwrap_or_else(|| "No environment".into());
                let env_color = current
                    .and_then(|e| parse_hex(&e.color))
                    .unwrap_or(t.muted_foreground);
                let env_picker = div()
                    .relative()
                    .child(
                        h_flex()
                            .id("env")
                            .w(px(160.))
                            .h(px(32.))
                            .px_3()
                            .gap_2()
                            .rounded_md()
                            .border_1()
                            .border_color(t.border)
                            .text_sm()
                            .child(dot(env_color, 6.))
                            .child(div().flex_1().truncate().child(env_label))
                            .child(div().text_xs().text_color(t.muted_foreground).child("▾"))
                            .on_click(
                                cx.listener(|this, _, _, cx| this.toggle_menu(Menu::Env, cx)),
                            ),
                    )
                    .when(self.menu == Some(Menu::Env), |d| {
                        d.child(deferred(
                            popup(t.border, t.popover)
                                .top(px(38.))
                                .right_0()
                                .w(px(200.))
                                .children(envs.iter().map(|e| {
                                    let id = e.id.clone();
                                    menu_item(
                                        SharedString::from(format!("env-{}", e.id)),
                                        t.list_active,
                                    )
                                    .child(dot(
                                        parse_hex(&e.color).unwrap_or(t.muted_foreground),
                                        6.,
                                    ))
                                    .child(e.name.clone())
                                    .on_click(cx.listener(
                                        move |this, _, _, cx| {
                                            this.active_env = Some(id.clone());
                                            this.menu = None;
                                            cx.notify();
                                        },
                                    ))
                                })),
                        ))
                    });

                let (name, sub) = match &self.me {
                    Load::Ready((me, _)) => (me.name.clone(), me.email.clone()),
                    Load::Failed(e) => ("Signed in".into(), e.clone()),
                    _ => ("…".into(), String::new()),
                };
                let is_admin = matches!(&self.me, Load::Ready((me, _)) if me.is_admin);
                let server = client.base().to_string();
                let admin_url = format!("{}/admin/overview", server.trim_end_matches('/'));
                let account = div()
                    .relative()
                    .child(
                        h_flex()
                            .id("avatar")
                            .w(px(32.))
                            .h(px(32.))
                            .justify_center()
                            .rounded_full()
                            .bg(primary.opacity(0.15))
                            .text_color(primary)
                            .text_xs()
                            .font_weight(FontWeight::MEDIUM)
                            .child(initials(&name))
                            .on_click(
                                cx.listener(|this, _, _, cx| this.toggle_menu(Menu::Account, cx)),
                            ),
                    )
                    .when(self.menu == Some(Menu::Account), |d| {
                        d.child(deferred(
                            popup(t.border, t.popover)
                                .top(px(38.))
                                .right_0()
                                .w(px(260.))
                                .child(
                                    v_flex()
                                        .px_2()
                                        .py_1()
                                        .child(
                                            div()
                                                .text_sm()
                                                .font_weight(FontWeight::MEDIUM)
                                                .child(name),
                                        )
                                        .child(
                                            div()
                                                .text_xs()
                                                .text_color(t.muted_foreground)
                                                .child(sub),
                                        )
                                        .child(
                                            div()
                                                .text_xs()
                                                .text_color(t.muted_foreground)
                                                .child(server),
                                        ),
                                )
                                .child(div().my_1().h(px(1.)).bg(t.border))
                                .when(is_admin, |d| {
                                    // The admin console is part of the web app.
                                    d.child(
                                        menu_item("admin-console", t.list_active)
                                            .child("Admin console ↗")
                                            .on_click(cx.listener(move |this, _, _, cx| {
                                                this.menu = None;
                                                let _ = open::that_detached(&admin_url);
                                                cx.notify();
                                            })),
                                    )
                                    .child(div().my_1().h(px(1.)).bg(t.border))
                                })
                                .child(
                                    menu_item("sign-out", t.list_active)
                                        .text_color(t.danger)
                                        .child("Sign out")
                                        .on_click(cx.listener(|this, _, _, cx| this.sign_out(cx))),
                                ),
                        ))
                    });
                h_flex()
                    .gap_3()
                    .child(env_picker)
                    .child(account)
                    .into_any_element()
            }
        };

        h_flex()
            .h(px(56.))
            .flex_none()
            .px_5()
            .gap_4()
            .bg(t.background)
            .border_b_1()
            .border_color(t.border)
            .child(
                h_flex()
                    .gap_2()
                    .child(logo(rgb(0x14161F).into(), rgb(LOGO_DOT).into(), 22.))
                    .child(div().font_weight(FontWeight::BOLD).child("Requara")),
            )
            .when(!workspace.is_empty(), |el| {
                el.child(div().w(px(1.)).h(px(16.)).bg(t.border)).child(
                    div()
                        .text_sm()
                        .font_weight(FontWeight::MEDIUM)
                        .child(workspace),
                )
            })
            .child(nav)
            .child(div().flex_1())
            .child(right)
            .into_any_element()
    }

    // ----- Collections sidebar -----

    fn is_open(&self, key: &str, default: bool, filtering: bool) -> bool {
        filtering || self.tree_open.get(key).copied().unwrap_or(default)
    }

    /// Opens or closes a node of the Collections tree.
    fn toggle(
        key: String,
        now: bool,
        cx: &Context<Self>,
    ) -> impl Fn(&ClickEvent, &mut Window, &mut App) + 'static {
        cx.listener(move |this, _, _, cx| {
            this.tree_open.insert(key.clone(), !now);
            cx.notify();
        })
    }

    fn render_sidebar(&mut self, cx: &mut Context<Self>) -> AnyElement {
        let t = cx.theme();
        let primary: Hsla = rgb(PRIMARY).into();
        let filter = self.filter.read(cx).value().to_lowercase();
        let filtering = !filter.is_empty();
        let matches = |name: &str, method: &str| {
            !filtering || format!("{name} {method}").to_lowercase().contains(&filter)
        };
        let in_design = matches!(self.view, View::Design | View::Docs);
        let selected = (self.view == View::Requests)
            .then(|| self.editor().map(|e| e.id.clone()))
            .flatten();

        let row = |id: SharedString, depth: f32, active: bool| {
            h_flex()
                .id(id)
                .h(px(32.))
                .pl(px(8. + depth * 16.))
                .pr_2()
                .gap_2()
                .rounded_md()
                .text_sm()
                .when(active, |d| d.bg(primary.opacity(0.1)).text_color(primary))
                .hover(|d| d.bg(t.muted))
        };
        let caret = |open: bool| {
            div()
                .w(px(12.))
                .text_xs()
                .text_color(t.muted_foreground)
                .child(if open { "▾" } else { "▸" })
        };

        let mut tree: Vec<AnyElement> = vec![];

        // Requests on this computer: always there, no account needed.
        let local_open = self.is_open("local", true, filtering);
        tree.push(
            row("c-local".into(), 0., false)
                .child(caret(local_open))
                .child(
                    div()
                        .flex_1()
                        .font_weight(FontWeight::MEDIUM)
                        .child("My requests"),
                )
                .child(
                    div()
                        .text_xs()
                        .text_color(t.muted_foreground)
                        .font_family(MONO)
                        .child("local"),
                )
                .on_click(Self::toggle("local".into(), local_open, cx))
                .into_any_element(),
        );
        if local_open {
            for r in self
                .store
                .requests
                .iter()
                .filter(|r| matches(&r.name, &r.method))
            {
                let id = r.id.clone();
                let active = selected.as_deref() == Some(r.id.as_str());
                tree.push(
                    row(SharedString::from(format!("r-{}", r.id)), 1., active)
                        .child(method_badge(&r.method, 34.))
                        .child(div().flex_1().truncate().child(r.name.clone()))
                        .on_click(
                            cx.listener(move |this, _, window, cx| {
                                this.open_local(&id, window, cx)
                            }),
                        )
                        .into_any_element(),
                );
            }
        }

        // The organization's collections.
        match &self.collections {
            Load::Ready(cols) => {
                for (ci, c) in cols.iter().enumerate() {
                    let folders: Vec<_> = c
                        .folders
                        .iter()
                        .map(|f| (f, f.requests.iter().filter(|r| matches(&r.name, &r.method)).collect::<Vec<_>>()))
                        .filter(|(_, rs)| !filtering || !rs.is_empty())
                        .collect();
                    if filtering && folders.is_empty() && !c.name.to_lowercase().contains(&filter) {
                        continue;
                    }
                    let ck = format!("c{}", c.id);
                    let c_open = self.is_open(&ck, ci == 0, filtering);
                    tree.push(
                        row(SharedString::from(ck.clone()), 0., false)
                            .child(caret(c_open))
                            .child(div().flex_1().truncate().font_weight(FontWeight::MEDIUM).child(c.name.clone()))
                            .child(div().text_xs().text_color(t.muted_foreground).font_family(MONO).child(c.version.clone()))
                            .on_click(Self::toggle(ck.clone(), c_open, cx))
                            .into_any_element(),
                    );
                    if !c_open {
                        continue;
                    }
                    for (fi, (f, requests)) in folders.iter().enumerate() {
                        let fk = format!("{ck}/{}", f.name);
                        let f_open = self.is_open(&fk, ci == 0 && fi == 0, filtering);
                        let folder_name = if f.name.is_empty() { "Requests".to_string() } else { f.name.clone() };
                        tree.push(
                            row(SharedString::from(fk.clone()), 1., false)
                                .child(caret(f_open))
                                .child(div().flex_1().truncate().child(folder_name.clone()))
                                .when(!f_open, |d| {
                                    d.child(div().text_xs().text_color(t.muted_foreground).child(f.requests.len().to_string()))
                                })
                                .on_click(Self::toggle(fk, f_open, cx))
                                .into_any_element(),
                        );
                        if !f_open {
                            continue;
                        }
                        for r in requests {
                            let (id, api, folder) = (r.id.clone(), c.name.clone(), folder_name.clone());
                            let (api_id, op_key) = (c.id.clone(), r.operation_key.clone());
                            // In Design and Docs the tree picks an endpoint; elsewhere a request.
                            let active = if in_design {
                                !r.operation_key.is_empty()
                                    && self.design.api_id.as_deref() == Some(c.id.as_str())
                                    && self.design.op_key.as_deref() == Some(r.operation_key.as_str())
                            } else {
                                selected.as_deref() == Some(r.id.as_str())
                            };
                            tree.push(
                                row(SharedString::from(format!("r-{}", r.id)), 2., active)
                                    .child(method_badge(&r.method, 34.))
                                    .child(div().flex_1().truncate().child(r.name.clone()))
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        if matches!(this.view, View::Design | View::Docs) && !op_key.is_empty() {
                                            this.open_design(api_id.clone(), Some(op_key.clone()), cx)
                                        } else {
                                            this.open_team(id.clone(), api.clone(), folder.clone(), cx)
                                        }
                                    }))
                                    .into_any_element(),
                            );
                        }
                    }
                }
            }
            Load::Loading => tree.push(note("Loading collections…", t.muted_foreground).into_any_element()),
            Load::Failed(e) => tree.push(note(e.clone(), t.danger).into_any_element()),
            Load::Idle => tree.push(
                v_flex()
                    .mt_3()
                    .mx_1()
                    .p_3()
                    .gap_2()
                    .rounded_md()
                    .border_1()
                    .border_color(t.border)
                    .child(
                        div()
                            .text_xs()
                            .text_color(t.muted_foreground)
                            .child("Sign in to see your organization's APIs, collections and environments here."),
                    )
                    .child(
                        Button::new("sidebar-sign-in")
                            .outline()
                            .small()
                            .label("Sign in to your organization")
                            .on_click(cx.listener(|this, _, _, cx| this.open_sign_in(cx))),
                    )
                    .into_any_element(),
            ),
        }

        let running = self
            .pages
            .mocks
            .ready()
            .map_or(0, |m| m.iter().filter(|m| m.enabled).count());
        let footer = View::FOOTER.map(|v| {
            let active = self.view == v;
            row(SharedString::from(v.label()), 0., active)
                .child(div().flex_1().child(v.label()))
                .when(v == View::Mocks && running > 0, |d| {
                    d.child(
                        div()
                            .text_xs()
                            .text_color(t.muted_foreground)
                            .child(format!("{running} running")),
                    )
                })
                .on_click(cx.listener(move |this, _, _, cx| this.open_view(v, cx)))
        });

        v_flex()
            .w(px(272.))
            .h_full()
            .flex_none()
            .bg(t.background)
            .border_r_1()
            .border_color(t.border)
            .child(
                h_flex()
                    .h(px(48.))
                    .flex_none()
                    .pl_4()
                    .pr_2()
                    .justify_between()
                    .child(div().font_weight(FontWeight::SEMIBOLD).child("Collections"))
                    .child(
                        Button::new("new-request").ghost().label("+").on_click(
                            cx.listener(|this, _, window, cx| this.new_local(window, cx)),
                        ),
                    ),
            )
            .child(div().px_3().pb_2().child(Input::new(&self.filter).small()))
            .child(
                div()
                    .id("tree")
                    .flex_1()
                    .min_h_0()
                    .px_2()
                    .pb_2()
                    .overflow_y_scroll()
                    .children(tree),
            )
            .child(
                v_flex()
                    .flex_none()
                    .px_2()
                    .pt_2()
                    .pb_3()
                    .border_t_1()
                    .border_color(t.border)
                    .children(footer),
            )
            .into_any_element()
    }

    // ----- Requests: tabs, editor, response -----

    fn render_requests(&mut self, cx: &mut Context<Self>) -> AnyElement {
        let t = cx.theme();
        let active_id = self.editor().map(|e| e.id.clone());

        let tabs =
            h_flex()
                .h(px(40.))
                .flex_none()
                .items_end()
                .gap_0p5()
                .px_3()
                .pt_1p5()
                .bg(t.muted)
                .border_b_1()
                .border_color(t.border)
                .children(self.editors.iter().map(|e| {
                    let active = Some(&e.id) == active_id.as_ref();
                    let (id, close_id) = (e.id.clone(), e.id.clone());
                    h_flex()
                        .id(SharedString::from(format!("tab-{}", e.id)))
                        .h(px(34.))
                        .max_w(px(240.))
                        .pl_3p5()
                        .pr_2p5()
                        .gap_2()
                        .rounded_t_lg()
                        .text_sm()
                        .border_1()
                        .border_color(if active {
                            t.border
                        } else {
                            transparent_black()
                        })
                        .when(active, |d| d.bg(t.background))
                        .when(!active, |d| d.text_color(t.muted_foreground))
                        .child(method_badge(&e.method, 0.))
                        .child(div().truncate().child(e.name.read(cx).value().to_string()))
                        .when(e.is_dirty(cx), |d| d.child(dot(rgb(0xFAAD14).into(), 6.)))
                        .child(
                            div()
                                .id(SharedString::from(format!("close-{}", e.id)))
                                .px_1()
                                .text_xs()
                                .text_color(t.muted_foreground)
                                .hover(|d| d.text_color(t.foreground))
                                .child("✕")
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    cx.stop_propagation();
                                    this.close_tab(&close_id, false, cx)
                                })),
                        )
                        .on_click(cx.listener(move |this, _, _, cx| {
                            this.activate(&id);
                            cx.notify();
                        }))
                }))
                .child(
                    div().pb_0p5().child(
                        Button::new("tab-new").ghost().small().label("+").on_click(
                            cx.listener(|this, _, window, cx| this.new_local(window, cx)),
                        ),
                    ),
                );

        let confirm = self.confirm_close.clone().and_then(|id| {
            let name = self
                .editors
                .iter()
                .find(|e| e.id == id)?
                .name
                .read(cx)
                .value()
                .to_string();
            let (yes, _) = (id.clone(), id);
            Some(
                h_flex()
                    .px_6()
                    .py_2()
                    .gap_3()
                    .bg(rgb(0xFAAD14).opacity(0.12))
                    .text_sm()
                    .child(
                        div()
                            .flex_1()
                            .child(format!("“{name}” has changes you haven't saved.")),
                    )
                    .child(
                        Button::new("discard")
                            .small()
                            .danger()
                            .label("Discard")
                            .on_click(
                                cx.listener(move |this, _, _, cx| this.close_tab(&yes, true, cx)),
                            ),
                    )
                    .child(
                        Button::new("keep")
                            .small()
                            .ghost()
                            .label("Keep editing")
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.confirm_close = None;
                                cx.notify();
                            })),
                    ),
            )
        });

        let body: AnyElement = if self.opening.is_some() {
            note("Opening request…", t.muted_foreground)
                .p_6()
                .into_any_element()
        } else if self.editor().is_some() {
            self.render_editor(cx)
        } else {
            v_flex()
                .p_10()
                .gap_3()
                .items_start()
                .child(
                    div()
                        .text_color(t.muted_foreground)
                        .child("No request open. Pick one from Collections, or start a new one."),
                )
                .child(
                    Button::new("empty-new")
                        .primary()
                        .label("New request")
                        .on_click(cx.listener(|this, _, window, cx| this.new_local(window, cx))),
                )
                .into_any_element()
        };
        let t = cx.theme();

        v_flex()
            .flex_1()
            .h_full()
            .min_w_0()
            .bg(t.background)
            .child(tabs)
            .children(confirm)
            .children(
                self.banner
                    .clone()
                    .map(|b| div().px_6().py_2().text_sm().text_color(t.danger).child(b)),
            )
            .child(div().flex_1().min_h_0().child(body))
            .into_any_element()
    }

    /// An underlined tab that changes something on the window.
    fn page_tab(
        id: &'static str,
        label: String,
        count: Option<String>,
        active: bool,
        muted: Hsla,
        cx: &Context<Self>,
        on: impl Fn(&mut Self) + 'static,
    ) -> Stateful<Div> {
        let primary: Hsla = rgb(PRIMARY).into();
        h_flex()
            .id(id)
            .gap_1p5()
            .mr_6()
            .pb_2()
            .text_sm()
            .border_b_2()
            .border_color(if active { primary } else { transparent_black() })
            .when(active, |d| d.text_color(primary))
            .hover(|d| d.text_color(primary))
            .child(label)
            .children(count.map(|c| div().text_xs().text_color(muted).child(c)))
            .on_click(cx.listener(move |this, _, _, cx| {
                on(this);
                cx.notify();
            }))
    }

    /// An underlined tab that changes something on the active editor.
    fn tab(
        id: &'static str,
        label: String,
        count: Option<String>,
        active: bool,
        muted: Hsla,
        cx: &Context<Self>,
        on: impl Fn(&mut Editor) + 'static,
    ) -> Stateful<Div> {
        let primary: Hsla = rgb(PRIMARY).into();
        h_flex()
            .id(id)
            .gap_1p5()
            .mr_6()
            .pb_2()
            .text_sm()
            .border_b_2()
            .border_color(if active { primary } else { transparent_black() })
            .when(active, |d| d.text_color(primary))
            .hover(|d| d.text_color(primary))
            .child(label)
            .children(count.map(|c| div().text_xs().text_color(muted).child(c)))
            .on_click(cx.listener(move |this, _, _, cx| {
                if let Some(e) = this.editors.get_mut(this.active) {
                    on(e);
                }
                cx.notify();
            }))
    }

    /// A segmented-control button that changes something on the active editor.
    fn segment(
        id: SharedString,
        label: &str,
        active: bool,
        cx: &Context<Self>,
        on: impl Fn(&mut Editor) + 'static,
    ) -> Button {
        let b = Button::new(id)
            .small()
            .label(label.to_string())
            .on_click(cx.listener(move |this, _, _, cx| {
                if let Some(e) = this.editors.get_mut(this.active) {
                    on(e);
                }
                cx.notify();
            }));
        if active { b.outline() } else { b.ghost() }
    }

    fn render_kv_table(&self, e: &Editor, params: bool, cx: &Context<Self>) -> AnyElement {
        let t = cx.theme();
        let rows = if params { &e.params } else { &e.headers };
        let last = rows.len().saturating_sub(1);
        let cell = || {
            h_flex()
                .flex_1()
                .min_w_0()
                .h_full()
                .px_1()
                .border_l_1()
                .border_color(t.border)
        };
        v_flex()
            .rounded_lg()
            .border_1()
            .border_color(t.border)
            .overflow_hidden()
            .child(
                h_flex()
                    .h(px(38.))
                    .bg(t.muted)
                    .text_sm()
                    .font_weight(FontWeight::SEMIBOLD)
                    .text_color(t.muted_foreground)
                    .child(div().w(px(44.)).flex_none())
                    .children(["Key", "Value", "Description"].map(|h| cell().px_3().child(h)))
                    .child(div().w(px(40.)).flex_none()),
            )
            .children(rows.iter().enumerate().map(|(i, r)| {
                let blank = i == last && r.is_blank(cx);
                let dim = if r.enabled { 1. } else { 0.45 };
                h_flex()
                    .h(px(40.))
                    .border_t_1()
                    .border_color(t.border)
                    .child(
                        h_flex()
                            .w(px(44.))
                            .flex_none()
                            .justify_center()
                            .when(!blank, |d| {
                                d.child(
                                    Checkbox::new(SharedString::from(format!(
                                        "kv-on-{params}-{i}"
                                    )))
                                    .checked(r.enabled)
                                    .on_click(cx.listener(move |this, on: &bool, _, cx| {
                                        if let Some(e) = this.editors.get_mut(this.active) {
                                            let rows = if params {
                                                &mut e.params
                                            } else {
                                                &mut e.headers
                                            };
                                            if let Some(r) = rows.get_mut(i) {
                                                r.enabled = *on;
                                            }
                                        }
                                        cx.notify();
                                    })),
                                )
                            }),
                    )
                    .child(
                        cell()
                            .opacity(dim)
                            .font_family(MONO)
                            .child(Input::new(&r.key).appearance(false)),
                    )
                    .child(
                        cell()
                            .opacity(dim)
                            .font_family(MONO)
                            .child(Input::new(&r.value).appearance(false)),
                    )
                    .child(
                        cell()
                            .text_color(t.muted_foreground)
                            .child(Input::new(&r.desc).appearance(false)),
                    )
                    .child(
                        h_flex()
                            .w(px(40.))
                            .flex_none()
                            .justify_center()
                            .when(!blank, |d| {
                                d.child(
                                    div()
                                        .id(SharedString::from(format!("kv-del-{params}-{i}")))
                                        .px_2()
                                        .text_xs()
                                        .text_color(t.muted_foreground)
                                        .hover(|d| d.text_color(t.danger))
                                        .child("✕")
                                        .on_click(cx.listener(move |this, _, _, cx| {
                                            if let Some(e) = this.editors.get_mut(this.active) {
                                                let rows = if params {
                                                    &mut e.params
                                                } else {
                                                    &mut e.headers
                                                };
                                                if i < rows.len() {
                                                    rows.remove(i);
                                                }
                                            }
                                            cx.notify();
                                        })),
                                )
                            }),
                    )
            }))
            .into_any_element()
    }

    fn render_editor(&mut self, cx: &mut Context<Self>) -> AnyElement {
        let Some(e) = self.editor() else {
            return div().into_any_element();
        };
        let t = cx.theme();
        let (muted, border) = (t.muted_foreground, t.border);
        let dirty = e.is_dirty(cx);
        let doc = e.to_doc(cx);
        let count = |rows: &Vec<Kv>| {
            let n = rows.iter().filter(|r| r.enabled).count();
            (n > 0).then(|| n.to_string())
        };

        // Breadcrumb · Save
        let top = h_flex()
            .gap_2()
            .child(
                h_flex()
                    .flex_1()
                    .min_w_0()
                    .gap_2()
                    .text_sm()
                    .text_color(muted)
                    .child(e.api.clone())
                    .child("/")
                    .child(if e.folder.is_empty() {
                        "Scratch".to_string()
                    } else {
                        e.folder.clone()
                    })
                    .child("/")
                    .child(
                        div()
                            .w(px(320.))
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(t.foreground)
                            .child(Input::new(&e.name).appearance(false)),
                    ),
            )
            .children(
                e.notice
                    .clone()
                    .map(|n| div().text_sm().text_color(t.danger).child(n)),
            )
            .child(
                Button::new("save")
                    .outline()
                    .label("Save")
                    .disabled(!dirty)
                    .loading(e.saving)
                    .on_click(cx.listener(|this, _, _, cx| this.save_active(cx))),
            );

        // Method · URL · Send
        let method_color = method_color(&e.method);
        let method_picker = div()
            .relative()
            .flex_none()
            .child(
                h_flex()
                    .id("method")
                    .w(px(120.))
                    .h(px(40.))
                    .px_3()
                    .justify_between()
                    .rounded_l_md()
                    .border_1()
                    .border_color(border)
                    .font_family(MONO)
                    .font_weight(FontWeight::BOLD)
                    .text_sm()
                    .text_color(method_color)
                    .child(e.method.clone())
                    .child(div().text_xs().text_color(muted).child("▾"))
                    .on_click(cx.listener(|this, _, _, cx| this.toggle_menu(Menu::Method, cx))),
            )
            .when(self.menu == Some(Menu::Method), |d| {
                d.child(deferred(
                    popup(border, t.popover)
                        .top(px(44.))
                        .left_0()
                        .w(px(120.))
                        .children(local::METHODS.map(|m| {
                            menu_item(SharedString::from(format!("m-{m}")), t.list_active)
                                .font_family(MONO)
                                .font_weight(FontWeight::BOLD)
                                .text_color(crate::app::method_color(m))
                                .child(m)
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    if let Some(e) = this.editors.get_mut(this.active) {
                                        e.method = m.to_string();
                                    }
                                    this.menu = None;
                                    cx.notify();
                                }))
                        })),
                ))
            });
        let is_local = e.origin == Origin::Local;
        let send = h_flex()
            .relative()
            .flex_none()
            .gap_px()
            .child(
                Button::new("send")
                    .primary()
                    .large()
                    .label("Send")
                    .loading(e.result.is_loading())
                    .on_click(cx.listener(|this, _, _, cx| this.send_active(cx))),
            )
            .child(
                Button::new("send-more")
                    .primary()
                    .large()
                    .label("▾")
                    .on_click(cx.listener(|this, _, _, cx| this.toggle_menu(Menu::SendMore, cx))),
            )
            .when(self.menu == Some(Menu::SendMore), |d| {
                let curl_doc = doc.clone();
                d.child(deferred(
                    popup(border, t.popover)
                        .top(px(46.))
                        .right_0()
                        .w(px(180.))
                        .child(
                            menu_item("save-send", t.list_active)
                                .child("Save and send")
                                .on_click(cx.listener(|this, _, _, cx| {
                                    this.save_active(cx);
                                    this.send_active(cx);
                                })),
                        )
                        .child(
                            menu_item("curl", t.list_active)
                                .child("Copy as cURL")
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    cx.write_to_clipboard(ClipboardItem::new_string(local::curl(
                                        &curl_doc,
                                    )));
                                    this.menu = None;
                                    cx.notify();
                                })),
                        )
                        .when(is_local, |d| {
                            d.child(
                                menu_item("delete", t.list_active)
                                    .text_color(t.danger)
                                    .child("Delete request")
                                    .on_click(cx.listener(|this, _, _, cx| this.delete_local(cx))),
                            )
                        }),
                ))
            });
        let url_row = h_flex()
            .gap_2()
            .child(
                h_flex().flex_1().min_w_0().child(method_picker).child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .font_family(MONO)
                        .child(Input::new(&e.url).large()),
                ),
            )
            .child(send);

        // Params · Authorization · Headers · Body · Scripts
        let config_tabs = h_flex()
            .border_b_1()
            .border_color(border)
            .child(Self::tab(
                "t-params",
                "Params".into(),
                count(&doc.params),
                e.tab == ConfigTab::Params,
                muted,
                cx,
                |e| e.tab = ConfigTab::Params,
            ))
            .child(Self::tab(
                "t-auth",
                "Authorization".into(),
                None,
                e.tab == ConfigTab::Auth,
                muted,
                cx,
                |e| e.tab = ConfigTab::Auth,
            ))
            .child(Self::tab(
                "t-headers",
                "Headers".into(),
                count(&doc.headers),
                e.tab == ConfigTab::Headers,
                muted,
                cx,
                |e| e.tab = ConfigTab::Headers,
            ))
            .child(Self::tab(
                "t-body",
                "Body".into(),
                None,
                e.tab == ConfigTab::Body,
                muted,
                cx,
                |e| e.tab = ConfigTab::Body,
            ))
            .child(Self::tab(
                "t-scripts",
                "Scripts".into(),
                None,
                e.tab == ConfigTab::Scripts,
                muted,
                cx,
                |e| e.tab = ConfigTab::Scripts,
            ))
            .child(Self::tab(
                "t-settings",
                "Settings".into(),
                None,
                e.tab == ConfigTab::Settings,
                muted,
                cx,
                |e| e.tab = ConfigTab::Settings,
            ));

        let panel: AnyElement = match e.tab {
            ConfigTab::Params => self.render_kv_table(e, true, cx),
            ConfigTab::Headers => self.render_kv_table(e, false, cx),
            ConfigTab::Auth => {
                let current = local::AUTH_TYPES
                    .iter()
                    .find(|(k, _)| *k == e.auth_kind)
                    .map_or("No auth", |(_, l)| l);
                let field = |label: &'static str, input: &Entity<InputState>| {
                    v_flex()
                        .gap_2()
                        .child(div().text_sm().child(label))
                        .child(Input::new(input))
                };
                let type_picker = div()
                    .relative()
                    .child(
                        h_flex()
                            .id("auth-type")
                            .h(px(34.))
                            .px_3()
                            .justify_between()
                            .rounded_md()
                            .border_1()
                            .border_color(border)
                            .text_sm()
                            .child(current.to_string())
                            .child(div().text_xs().text_color(muted).child("▾"))
                            .on_click(
                                cx.listener(|this, _, _, cx| this.toggle_menu(Menu::AuthType, cx)),
                            ),
                    )
                    .when(self.menu == Some(Menu::AuthType), |d| {
                        d.child(deferred(
                            popup(border, t.popover)
                                .top(px(38.))
                                .left_0()
                                .w(px(520.))
                                .children(local::AUTH_TYPES.map(|(kind, label)| {
                                    menu_item(
                                        SharedString::from(format!("auth-{kind}")),
                                        t.list_active,
                                    )
                                    .child(label)
                                    .on_click(cx.listener(
                                        move |this, _, _, cx| {
                                            if let Some(e) = this.editors.get_mut(this.active) {
                                                e.auth_kind = kind.to_string();
                                            }
                                            this.menu = None;
                                            cx.notify();
                                        },
                                    ))
                                })),
                        ))
                    });
                let fields: AnyElement = match e.auth_kind.as_str() {
                    "bearer" => field("Token", &e.auth_token).font_family(MONO).into_any_element(),
                    "basic" => v_flex()
                        .gap_4()
                        .child(field("Username", &e.auth_user))
                        .child(field("Password", &e.auth_pass))
                        .into_any_element(),
                    "apikey" => v_flex()
                        .gap_4()
                        .child(field("Key", &e.auth_key))
                        .child(field("Value", &e.auth_value).font_family(MONO))
                        .child(
                            h_flex()
                                .gap_1()
                                .child(Self::segment("in-header".into(), "Header", e.auth_in != "query", cx, |e| {
                                    e.auth_in = "header".into()
                                }))
                                .child(Self::segment("in-query".into(), "Query", e.auth_in == "query", cx, |e| {
                                    e.auth_in = "query".into()
                                })),
                        )
                        .into_any_element(),
                    "inherit" => div()
                        .text_sm()
                        .text_color(muted)
                        .child(if is_local {
                            "Environments live on your organization's server, so requests on this computer send no inherited auth."
                        } else {
                            "Sends “Authorization: Bearer {{secret_key}}” when the environment defines secret_key."
                        })
                        .into_any_element(),
                    _ => div().into_any_element(),
                };
                v_flex()
                    .w(px(520.))
                    .gap_4()
                    .child(v_flex().gap_2().child(div().text_sm().child("Type")).child(type_picker))
                    .child(fields)
                    .child(div().text_xs().text_color(muted).child(if is_local {
                        "Sent from this computer exactly as typed."
                    } else {
                        "Use {{variables}} for secrets. Their values come from the active environment and never leave the server."
                    }))
                    .into_any_element()
            }
            ConfigTab::Body => {
                let modes = h_flex()
                    .gap_1()
                    .children(local::BODY_MODES.map(|(mode, label)| {
                        Self::segment(
                            SharedString::from(format!("mode-{mode}")),
                            label,
                            e.body_mode == mode,
                            cx,
                            move |e| e.body_mode = mode.to_string(),
                        )
                    }));
                let editor: AnyElement = if e.body_mode == "none" {
                    div()
                        .text_sm()
                        .text_color(muted)
                        .child("This request has no body.")
                        .into_any_element()
                } else {
                    div()
                        .font_family(MONO)
                        .child(Textarea::new(&e.body).h(px(200.)))
                        .into_any_element()
                };
                v_flex()
                    .gap_3()
                    .child(modes)
                    .child(editor)
                    .into_any_element()
            }
            ConfigTab::Scripts => {
                if is_local {
                    div()
                        .text_sm()
                        .text_color(muted)
                        .child("Scripts and tests run on your organization's server. Sign in and open a team request to use them.")
                        .into_any_element()
                } else {
                    v_flex()
                        .gap_3()
                        .child(
                            h_flex()
                                .justify_between()
                                .child(
                                    h_flex()
                                        .gap_1()
                                        .child(Self::segment("s-pre".into(), "Pre-request", e.pre_script_tab, cx, |e| {
                                            e.pre_script_tab = true
                                        }))
                                        .child(Self::segment("s-post".into(), "Post-response", !e.pre_script_tab, cx, |e| {
                                            e.pre_script_tab = false
                                        })),
                                )
                                .child(div().text_xs().text_color(muted).child(if e.pre_script_tab {
                                    "req, env.get/set, console.log · runs in a sandbox, 2 s limit"
                                } else {
                                    "res.status, res.json, res.time, test(), expect(), env.set · runs in a sandbox, 2 s limit"
                                })),
                        )
                        .child(
                            div()
                                .font_family(MONO)
                                .child(Textarea::new(if e.pre_script_tab { &e.pre } else { &e.post }).h(px(200.))),
                        )
                        .into_any_element()
                }
            }
            ConfigTab::Settings => {
                let on = |key: &str| {
                    e.settings
                        .get(key)
                        .and_then(|v| v.as_bool())
                        .unwrap_or(true)
                };
                let row = |label: &'static str, desc: &'static str, control: AnyElement| {
                    h_flex()
                        .py_3p5()
                        .gap_4()
                        .border_b_1()
                        .border_color(border)
                        .child(
                            v_flex()
                                .flex_1()
                                .child(div().font_weight(FontWeight::MEDIUM).child(label))
                                .child(div().text_sm().text_color(muted).child(desc)),
                        )
                        .child(control)
                };
                let switch = |key: &'static str| {
                    Switch::new(key)
                        .checked(on(key))
                        .on_click(cx.listener(move |this, v: &bool, _, cx| {
                            if let Some(e) = this.editors.get_mut(this.active) {
                                e.settings.insert(key.into(), (*v).into());
                            }
                            cx.notify();
                        }))
                        .into_any_element()
                };
                v_flex()
                    .w(px(640.))
                    .child(row(
                        "Follow redirects",
                        "Follow 3xx responses automatically",
                        switch("followRedirects"),
                    ))
                    .child(row(
                        "Verify SSL certificates",
                        "Reject self-signed and expired certificates",
                        switch("verifySSL"),
                    ))
                    .child(row(
                        "Encode URL automatically",
                        "Percent-encode the query before sending",
                        switch("encodeURL"),
                    ))
                    .child(row(
                        "Request timeout",
                        "In milliseconds. Empty uses the default (30 s).",
                        div()
                            .w(px(160.))
                            .child(Input::new(&e.timeout))
                            .into_any_element(),
                    ))
                    .into_any_element()
            }
        };

        let response = self.render_response(
            &e.result,
            e.resp_tab,
            |this, tab| {
                if let Some(e) = this.editors.get_mut(this.active) {
                    e.resp_tab = tab;
                }
            },
            cx,
        );

        v_flex()
            .size_full()
            .child(
                v_flex()
                    .id("config")
                    .h(relative(0.55))
                    .flex_none()
                    .px_6()
                    .py_4()
                    .gap_4()
                    .overflow_y_scroll()
                    .child(top)
                    .child(url_row)
                    .child(config_tabs)
                    .child(panel),
            )
            .child(div().h(px(1.)).flex_none().bg(border))
            .child(div().flex_1().min_h_0().child(response))
            .into_any_element()
    }

    fn render_response(
        &self,
        state: &Load<Resp>,
        current: RespTab,
        set: fn(&mut Self, RespTab),
        cx: &Context<Self>,
    ) -> AnyElement {
        let t = cx.theme();
        let (muted, border, danger) = (t.muted_foreground, t.border, t.danger);
        let result = state.ready();
        let passed = result.map_or(0, |r| r.tests.iter().filter(|t| t.pass).count());
        let total_tests = result.map_or(0, |r| r.tests.len());

        let meta: AnyElement = match &state {
            Load::Loading => div()
                .text_sm()
                .text_color(muted)
                .child("Sending request…")
                .into_any_element(),
            Load::Ready(r) => {
                let body = r.body.clone();
                h_flex()
                    .gap_3()
                    .child(status_tag(r.status, &r.status_text))
                    .child(
                        div()
                            .text_sm()
                            .text_color(muted)
                            .child(format!("{} ms", r.duration_ms)),
                    )
                    .child(div().text_sm().text_color(muted).child(bytes(r.size_bytes)))
                    .child(
                        Button::new("copy-response")
                            .ghost()
                            .small()
                            .label("Copy")
                            .on_click(cx.listener(move |_, _, _, cx| {
                                cx.write_to_clipboard(ClipboardItem::new_string(body.clone()))
                            })),
                    )
                    .into_any_element()
            }
            _ => div().into_any_element(),
        };

        let tabs = h_flex()
            .border_b_1()
            .border_color(border)
            .child(Self::page_tab(
                "r-body",
                "Response".into(),
                None,
                current == RespTab::Body,
                muted,
                cx,
                move |this| set(this, RespTab::Body),
            ))
            .child(Self::page_tab(
                "r-headers",
                "Headers".into(),
                result.map(|r| r.headers.len().to_string()),
                current == RespTab::Headers,
                muted,
                cx,
                move |this| set(this, RespTab::Headers),
            ))
            .child(Self::page_tab(
                "r-tests",
                "Tests".into(),
                (total_tests > 0).then(|| format!("{passed}/{total_tests} passed")),
                current == RespTab::Tests,
                muted,
                cx,
                move |this| set(this, RespTab::Tests),
            ))
            .child(Self::page_tab(
                "r-timeline",
                "Timeline".into(),
                None,
                current == RespTab::Timeline,
                muted,
                cx,
                move |this| set(this, RespTab::Timeline),
            ))
            .child(div().flex_1())
            .child(div().pb_1p5().child(meta));

        let empty = |text: &str| {
            div()
                .pt_10()
                .w_full()
                .text_center()
                .text_sm()
                .text_color(muted)
                .child(text.to_string())
        };
        let content: AnyElement = match (&state, current) {
            (Load::Idle, _) => empty("Send the request to see the response.").into_any_element(),
            (Load::Loading, _) => div().h(px(140.)).rounded_lg().bg(t.muted).into_any_element(),
            (Load::Failed(err), _) => error_box("The request didn't complete", err, danger).into_any_element(),
            (Load::Ready(r), RespTab::Body) => match &r.error {
                Some(err) => error_box("The request didn't complete", err, danger).into_any_element(),
                None => {
                    // Very large bodies would make the text layout slow.
                    const MAX: usize = 200_000;
                    let mut body = r.body.clone();
                    if body.len() > MAX {
                        let cut = (0..=MAX).rev().find(|i| body.is_char_boundary(*i)).unwrap_or(0);
                        body.truncate(cut);
                        body.push_str("\n… (cut off here)");
                    }
                    div()
                        .p_3()
                        .rounded_lg()
                        .border_1()
                        .border_color(border)
                        .bg(t.muted)
                        .text_xs()
                        .font_family(MONO)
                        .child(if body.is_empty() { "(empty body)".to_string() } else { body })
                        .into_any_element()
                }
            },
            (Load::Ready(r), RespTab::Headers) => v_flex()
                .rounded_lg()
                .border_1()
                .border_color(border)
                .overflow_hidden()
                .children(r.headers.iter().enumerate().map(|(i, (k, v))| {
                    h_flex()
                        .px_4()
                        .py_2()
                        .items_start()
                        .text_xs()
                        .font_family(MONO)
                        .when(i > 0, |d| d.border_t_1().border_color(border))
                        .child(div().w(px(260.)).flex_none().text_color(muted).child(k.clone()))
                        .child(div().flex_1().min_w_0().child(v.clone()))
                }))
                .when(r.request_headers > 0, |d| {
                    d.child(div().px_4().py_2().bg(t.muted).border_t_1().border_color(border).text_xs().text_color(muted).child(
                        format!("Sent {} request headers. Secret values are shown as ••••••••.", r.request_headers),
                    ))
                })
                .into_any_element(),
            (Load::Ready(r), RespTab::Tests) if r.tests.is_empty() => {
                empty("No tests. Add them under Scripts → Post-response.").into_any_element()
            }
            (Load::Ready(r), RespTab::Tests) => v_flex()
                .gap_3()
                .child(div().text_sm().text_color(muted).child(format!("{passed} passed · {} failed", total_tests - passed)))
                .child(v_flex().rounded_lg().border_1().border_color(border).children(r.tests.iter().enumerate().map(
                    |(i, test)| {
                        let color: Hsla = if test.pass { rgb(0x389E0D).into() } else { danger };
                        h_flex()
                            .min_h(px(44.))
                            .px_4()
                            .gap_3()
                            .when(i > 0, |d| d.border_t_1().border_color(border))
                            .child(
                                div()
                                    .w(px(40.))
                                    .text_xs()
                                    .font_family(MONO)
                                    .font_weight(FontWeight::SEMIBOLD)
                                    .text_color(color)
                                    .child(if test.pass { "PASS" } else { "FAIL" }),
                            )
                            .child(
                                v_flex()
                                    .flex_1()
                                    .text_sm()
                                    .child(test.name.clone())
                                    .children(test.error.clone().map(|e| div().text_xs().text_color(danger).child(e))),
                            )
                            .child(div().text_sm().text_color(muted).child(format!("{} ms", test.ms.round())))
                    },
                )))
                .when(!r.logs.is_empty(), |d| {
                    d.child(div().p_3().rounded_lg().bg(t.muted).text_xs().font_family(MONO).child(r.logs.join("\n")))
                })
                .into_any_element(),
            (Load::Ready(r), RespTab::Timeline) if r.timeline.is_empty() => {
                empty("The timeline is recorded when the request is sent from your organization's server.").into_any_element()
            }
            (Load::Ready(r), RespTab::Timeline) => {
                let total = (r.duration_ms.max(1)) as f32;
                v_flex()
                    .children(r.timeline.iter().map(|p| {
                        let (start, width) = ((p.start_ms as f32 / total).clamp(0., 1.), (p.ms as f32 / total).clamp(0.004, 1.));
                        h_flex()
                            .h(px(34.))
                            .gap_4()
                            .child(div().w(px(160.)).flex_none().text_sm().text_color(muted).child(p.name.clone()))
                            .child(
                                div().flex_1().h(px(12.)).rounded_sm().bg(t.muted).relative().child(
                                    div()
                                        .absolute()
                                        .top_0()
                                        .h_full()
                                        .left(relative(start))
                                        .w(relative(width.min(1. - start).max(0.004)))
                                        .rounded_sm()
                                        .bg(phase_color(&p.name)),
                                ),
                            )
                            .child(div().w(px(80.)).flex_none().text_sm().child(if p.ms < 1. {
                                format!("{:.2} ms", p.ms)
                            } else {
                                format!("{} ms", p.ms.round())
                            }))
                    }))
                    .child(
                        h_flex()
                            .mt_1p5()
                            .pt_2p5()
                            .justify_between()
                            .border_t_1()
                            .border_color(border)
                            .text_sm()
                            .font_weight(FontWeight::SEMIBOLD)
                            .child("Total")
                            .child(format!("{} ms", r.duration_ms)),
                    )
                    .into_any_element()
            }
        };

        let script_error = result.and_then(|r| r.script_error.clone());
        v_flex()
            .id("response")
            .size_full()
            .px_6()
            .pt_3()
            .pb_5()
            .gap_3()
            .overflow_y_scroll()
            .child(tabs)
            .children(script_error.map(|s| {
                div()
                    .p_3()
                    .rounded_md()
                    .bg(rgb(0xFAAD14).opacity(0.12))
                    .text_sm()
                    .child(s)
            }))
            .child(content)
            .into_any_element()
    }

    // ----- other pages -----

    /// A page that needs the organization's server, shown when signed out.
    fn render_needs_sign_in(&self, cx: &Context<Self>) -> AnyElement {
        let t = cx.theme();
        let what = match self.view {
            View::Apis => "Your organization's API catalog: versions, traffic and error rates.",
            View::Design => "Design APIs in OpenAPI with linting, change review and publishing.",
            View::Docs => "Documentation generated from your organization's APIs.",
            View::History => "Every request your team's server has sent for you.",
            View::Mocks => "Mock servers generated from your organization's API designs.",
            View::Environments => "Variables and encrypted secrets shared with your team.",
            View::Requests => "",
        };
        v_flex()
            .size_full()
            .items_center()
            .justify_center()
            .gap_3()
            .child(div().text_xl().font_weight(FontWeight::SEMIBOLD).child(self.view.label()))
            .child(div().text_sm().text_color(t.muted_foreground).child(what))
            .child(
                Button::new("page-sign-in")
                    .primary()
                    .label("Sign in to your organization")
                    .on_click(cx.listener(|this, _, _, cx| this.open_sign_in(cx))),
            )
            .child(
                div()
                    .text_xs()
                    .text_color(t.muted_foreground)
                    .child("Requests work without an account: open Requests to build and send them from this computer."),
            )
            .into_any_element()
    }

    fn render_sign_in_panel(&mut self, cx: &mut Context<Self>) -> Option<AnyElement> {
        if matches!(self.sign_in, SignIn::Closed) {
            return None;
        }
        let t = cx.theme();
        let (muted, danger) = (t.muted_foreground, t.danger);
        let body: AnyElement = match &self.sign_in {
            SignIn::Closed => unreachable!(),
            SignIn::Server | SignIn::Checking => {
                let checking = matches!(self.sign_in, SignIn::Checking);
                v_flex()
                    .gap_3()
                    .child(
                        div()
                            .text_sm()
                            .text_color(muted)
                            .child("Step 1 of 2. Enter the address of your organization's Requara server. Your admin can tell you what it is."),
                    )
                    .child(div().text_sm().child("Server address"))
                    .child(Input::new(&self.server_input))
                    .child(div().text_xs().text_color(muted).child(
                        "Next you choose how to sign in. The server lists what your organization uses: Google, Microsoft, GitHub, GitLab, Okta, Keycloak or any other single sign-on.",
                    ))
                    .child(
                        Button::new("check-server")
                            .primary()
                            .label(if checking { "Checking…" } else { "Continue" })
                            .loading(checking)
                            .on_click(cx.listener(|this, _, _, cx| {
                                if !matches!(this.sign_in, SignIn::Checking) {
                                    this.check_server(cx)
                                }
                            })),
                    )
                    .into_any_element()
            }
            SignIn::Choose {
                server,
                org,
                providers,
            } => {
                let server = server.clone();
                v_flex()
                    .gap_2()
                    .child(div().text_sm().text_color(muted).child(format!(
                        "Step 2 of 2. Choose how to sign in to {org}. It opens in your browser."
                    )))
                    .children(providers.iter().map(|p| {
                        let (server, p2) = (server.clone(), p.clone());
                        Button::new(SharedString::from(format!("provider-{}", p.id)))
                            .outline()
                            .label(format!("Continue with {}", p.name))
                            .on_click(cx.listener(move |this, _, _, cx| {
                                this.sign_in_with(server.clone(), p2.clone(), cx)
                            }))
                    }))
                    .child(
                        Button::new("other-server")
                            .ghost()
                            .label("Use a different server")
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.sign_in = SignIn::Server;
                                cx.notify();
                            })),
                    )
                    .into_any_element()
            }
            SignIn::Waiting { server, provider } => v_flex()
                .gap_2()
                .child(div().text_sm().child(format!(
                    "Finish signing in with {provider} in your browser."
                )))
                .child(
                    div()
                        .text_xs()
                        .text_color(muted)
                        .child(format!("Connecting to {server}")),
                )
                .into_any_element(),
        };
        let panel = v_flex()
            .id("sign-in-panel")
            .w(px(440.))
            .p_6()
            .gap_4()
            .rounded_lg()
            .border_1()
            .border_color(t.border)
            .bg(t.background)
            .shadow_lg()
            .occlude()
            .child(
                h_flex()
                    .justify_between()
                    .child(div().text_lg().font_weight(FontWeight::SEMIBOLD).child("Sign in to your organization"))
                    .child(
                        Button::new("close-sign-in")
                            .ghost()
                            .label("Close")
                            .on_click(cx.listener(|this, _, _, cx| this.close_sign_in(cx))),
                    ),
            )
            .child(body)
            .children(self.sign_in_error.clone().map(|e| div().text_sm().text_color(danger).child(e)))
            .child(div().text_xs().text_color(muted).child(
                "Signing in adds your team's collections, environments, APIs and mock servers. Your own requests stay on this computer.",
            ));
        Some(
            deferred(
                div()
                    .id("sign-in-backdrop")
                    .absolute()
                    .top_0()
                    .left_0()
                    .size_full()
                    .flex()
                    .items_center()
                    .justify_center()
                    .bg(black().opacity(0.45))
                    .occlude()
                    .child(panel),
            )
            .with_priority(1)
            .into_any_element(),
        )
    }
}

impl Render for RequaraApp {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        // Team requests that finished loading get their inputs here, where a window is at hand.
        for (doc, api, folder) in std::mem::take(&mut self.pending_open) {
            if !self.activate(&doc.id) {
                self.editors
                    .push(Editor::new(Origin::Team, doc, api, folder, window, cx));
                self.active = self.editors.len() - 1;
            }
        }
        if self.active >= self.editors.len() {
            self.active = self.editors.len().saturating_sub(1);
        }
        if let Some(e) = self.editors.get_mut(self.active) {
            e.ensure_blank_rows(window, cx);
        }
        self.materialize_pages(window, cx);
        self.materialize_design(window, cx);
        // The component library re-applies its own theme (first frame, system
        // light/dark changes); keep the web app's blue on top of it.
        if cx.theme().primary != Hsla::from(rgb(PRIMARY)) {
            apply_brand(cx);
        }

        let signed_in = self.client.is_some();
        let page: AnyElement = match self.view {
            View::Requests => self.render_requests(cx),
            _ if !signed_in => self.render_needs_sign_in(cx),
            View::Apis => self.render_apis(cx),
            View::History => self.render_history(cx),
            View::Environments => self.render_environments(cx),
            View::Mocks => self.render_mocks(cx),
            View::Design => self.render_design(cx),
            View::Docs => self.render_docs(cx),
        };
        let header = self.render_header(cx);
        let banner = self.render_banner(cx);
        let sidebar = self.view.has_sidebar().then(|| self.render_sidebar(cx));
        let sign_in = self.render_sign_in_panel(cx);
        let publish = self.render_publish_panel(cx);
        let console = self.render_console(cx);
        let t = cx.theme();
        v_flex()
            .relative()
            .size_full()
            .bg(t.background)
            .text_color(t.foreground)
            .on_action(cx.listener(|this, _: &SaveRequest, _, cx| {
                if this.view == View::Requests {
                    this.save_active(cx);
                }
            }))
            .on_action(cx.listener(|this, _: &SendRequest, _, cx| {
                if this.view == View::Requests {
                    this.send_active(cx);
                }
            }))
            .on_action(cx.listener(|this, _: &ToggleConsole, _, cx| {
                this.console.toggle();
                cx.notify();
            }))
            // Dragging the console's top edge resizes it.
            .on_mouse_move(cx.listener(|this, e: &MouseMoveEvent, window, cx| {
                if this.console.dragging {
                    let height = window.viewport_size().height;
                    this.console.drag_to(e.position.y.into(), height.into());
                    cx.notify();
                }
            }))
            .on_mouse_up(
                MouseButton::Left,
                cx.listener(|this, _, _, cx| {
                    if this.console.dragging {
                        this.console.dragging = false;
                        cx.notify();
                    }
                }),
            )
            .children(banner)
            .child(header)
            .child(
                h_flex()
                    .flex_1()
                    .min_h_0()
                    .children(sidebar)
                    .child(div().flex_1().h_full().min_w_0().child(page)),
            )
            .child(console)
            .children(publish)
            .children(sign_in)
    }
}

// ----- small pieces -----

/// The Requara mark, "Orbit", on its dark tile (branding/requara-icon.svg),
/// drawn with plain elements so it needs no image asset.
fn logo(tile: Hsla, dot_color: Hsla, size: f32) -> impl IntoElement {
    // The mark sits in the middle 68% of the tile, like the icon.
    let k = size * 0.68 / 46.;
    let off = size * 0.16;
    let circle = |x: f32, y: f32, d: f32| {
        div()
            .absolute()
            .left(px(off + x * k))
            .top(px(off + y * k))
            .w(px(d * k))
            .h(px(d * k))
            .rounded_full()
    };
    div()
        .relative()
        .flex_none()
        .w(px(size))
        .h(px(size))
        .rounded(px(size * 0.225))
        .bg(tile)
        .child(
            circle(2.5, 2.5, 41.)
                .border(px((5.5 * k).max(1.5)))
                .border_color(white()),
        )
        .child(circle(26.7, 1.3, 18.).bg(tile))
        .child(circle(30.2, 4.8, 11.).bg(dot_color))
}

fn dot(color: Hsla, size: f32) -> Div {
    div()
        .w(px(size))
        .h(px(size))
        .flex_none()
        .rounded_full()
        .bg(color)
}

/// A dropdown panel; position it with `.top()`/`.left()`/`.right_0()`.
fn popup(border: Hsla, bg: Hsla) -> Div {
    v_flex()
        .absolute()
        .p_1()
        .rounded_md()
        .border_1()
        .border_color(border)
        .bg(bg)
        .shadow_md()
        .occlude()
}

fn menu_item(id: impl Into<ElementId>, hover: Hsla) -> Stateful<Div> {
    h_flex()
        .id(id)
        .gap_2()
        .px_2()
        .py_1p5()
        .rounded_sm()
        .text_sm()
        .hover(move |d| d.bg(hover))
}

fn note(text: impl Into<SharedString>, color: Hsla) -> Div {
    v_flex()
        .p_2()
        .child(div().text_sm().text_color(color).child(text.into()))
}

fn status<T>(state: &Load<T>, loading: &str, muted: Hsla, danger: Hsla) -> Div {
    match state {
        Load::Failed(e) => note(e.clone(), danger),
        _ => note(loading.to_string(), muted),
    }
}

fn error_box(title: &str, detail: &str, danger: Hsla) -> Div {
    v_flex()
        .p_3()
        .gap_1()
        .rounded_lg()
        .border_1()
        .border_color(danger.opacity(0.4))
        .bg(danger.opacity(0.08))
        .child(
            div()
                .text_sm()
                .font_weight(FontWeight::MEDIUM)
                .child(title.to_string()),
        )
        .child(div().text_sm().child(detail.to_string()))
}

fn pill(text: &str, bg: Hsla, fg: Hsla) -> impl IntoElement {
    div()
        .px_1p5()
        .rounded_sm()
        .bg(bg)
        .text_color(fg)
        .text_xs()
        .child(text.to_string())
}

/// Method colours from the web app (Ant Design's 7th shades).
fn method_color(method: &str) -> Hsla {
    match method {
        "GET" => rgb(0x389E0D),
        "POST" => rgb(0xD46B08),
        "PUT" => rgb(0x0958D9),
        "PATCH" => rgb(0x531DAB),
        "DELETE" => rgb(0xCF1322),
        _ => rgb(0x8C8C8C),
    }
    .into()
}

/// `width` 0 sizes to the text.
fn method_badge(method: &str, width: f32) -> impl IntoElement {
    let short = match method {
        "DELETE" => "DEL",
        "OPTIONS" => "OPT",
        m => m,
    };
    div()
        .when(width > 0., |d| d.w(px(width)))
        .flex_none()
        .text_size(px(11.))
        .font_family(MONO)
        .font_weight(FontWeight::BOLD)
        .text_color(method_color(method))
        .child(short.to_string())
}

fn status_tag(status: i64, text: &str) -> impl IntoElement {
    let color: Hsla = match status {
        0 => rgb(0xCF1322),
        s if s < 300 => rgb(0x389E0D),
        s if s < 400 => rgb(0x8C8C8C),
        s if s < 500 && s != 404 => rgb(0xD48806),
        _ => rgb(0xCF1322),
    }
    .into();
    div()
        .px_2()
        .rounded_sm()
        .border_1()
        .border_color(color.opacity(0.4))
        .bg(color.opacity(0.1))
        .text_color(color)
        .text_xs()
        .font_weight(FontWeight::MEDIUM)
        .child(if status == 0 {
            "Error".to_string()
        } else {
            format!("{status} {text}").trim().to_string()
        })
}

fn phase_color(name: &str) -> Hsla {
    match name {
        "Socket wait" => rgb(0xBFBFBF),
        "DNS lookup" => rgb(0x36CFC9),
        "TCP connect" => rgb(0xFFA940),
        "TLS handshake" => rgb(0x9254DE),
        "Request sent" => rgb(0x69B1FF),
        "Waiting (TTFB)" => rgb(0x52C41A),
        _ => rgb(PRIMARY),
    }
    .into()
}

fn bytes(n: i64) -> String {
    let n = n as f64;
    if n < 1024. {
        format!("{n} B")
    } else if n < 1024. * 1024. {
        format!("{:.2} KB", n / 1024.)
    } else {
        format!("{:.2} MB", n / 1024. / 1024.)
    }
}

fn initials(name: &str) -> String {
    name.split_whitespace()
        .filter_map(|p| p.chars().next())
        .take(2)
        .collect::<String>()
        .to_uppercase()
}

/// "#52C41A" → a colour.
fn parse_hex(s: &str) -> Option<Hsla> {
    let h = s.strip_prefix('#')?;
    (h.len() == 6)
        .then(|| u32::from_str_radix(h, 16).ok())
        .flatten()
        .map(|v| rgb(v).into())
}
