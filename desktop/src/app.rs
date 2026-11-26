//! The Speklo desktop window.
//!
//! Local first: anyone can open the app and build and send requests from
//! their machine, with no account. Signing in is optional, for collaborating:
//! connect to the team's Speklo server, pick one of the sign-in options it
//! offers (Google, GitHub, company SSO, …), and the shared APIs, requests and
//! mock servers appear next to the local requests.

use gpui_kit::base::input::TextareaState;
use gpui_kit::component::button::{Button, ButtonVariants as _};
use gpui_kit::component::input::{Input, InputState, Textarea};
use gpui_kit::component::{ActiveTheme as _, h_flex, v_flex};
use gpui_kit::prelude::FluentBuilder as _;
use gpui_kit::*;

use crate::api::{
    self, ApiItem, Client, Collection, Me, Mock, Operation, Provider, SendResult, Workspace,
};
use crate::auth;
use crate::local::{self, LocalRequest, LocalResponse, LocalStore};
use crate::settings::{Settings, normalize_server_url};

const ACCENT: u32 = 0x4F46E5;

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
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Section {
    /// Requests kept on this machine. Always available.
    Local,
    /// Connecting to a team server and choosing how to sign in.
    SignIn,
    Apis,
    Requests,
    Mocks,
}

impl Section {
    const TEAM: [Section; 3] = [Section::Apis, Section::Requests, Section::Mocks];

    fn label(self) -> &'static str {
        match self {
            Section::Local => "My requests",
            Section::SignIn => "Sign in",
            Section::Apis => "APIs",
            Section::Requests => "Team requests",
            Section::Mocks => "Mock servers",
        }
    }
}

/// Where the optional sign-in is.
enum SignIn {
    /// Asking for the server address.
    Server,
    /// Looking up the server's version and sign-in options.
    Checking,
    /// Showing the server's sign-in options.
    Choose {
        server: String,
        org: String,
        providers: Vec<Provider>,
    },
    /// The browser is open at the provider.
    Waiting { server: String, provider: String },
}

pub struct SpekloApp {
    settings: Settings,

    // ----- local -----
    store: LocalStore,
    selected: usize,
    method: String,
    name_input: Entity<InputState>,
    url_input: Entity<InputState>,
    headers_input: Entity<TextareaState>,
    body_input: Entity<TextareaState>,
    local_result: Load<LocalResponse>,

    // ----- team (optional) -----
    client: Option<Client>,
    server_input: Entity<InputState>,
    sign_in: SignIn,
    /// Bumped to forget a sign-in the person cancelled.
    sign_in_attempt: u64,
    sign_in_error: Option<String>,
    me: Load<(Me, Vec<Workspace>)>,
    apis: Load<Vec<ApiItem>>,
    selected_api: Option<String>,
    operations: Load<Vec<Operation>>,
    collections: Load<Vec<Collection>>,
    selected_request: Option<(String, String)>,
    sent: Load<SendResult>,
    mocks: Load<Vec<Mock>>,

    section: Section,
}

impl SpekloApp {
    pub fn new(window: &mut Window, cx: &mut Context<Self>) -> Self {
        let settings = Settings::load();
        let store = LocalStore::load();
        let first = store.requests[0].clone();

        let input = |placeholder: &'static str,
                     value: String,
                     window: &mut Window,
                     cx: &mut Context<Self>| {
            cx.new(|cx| {
                InputState::new(window, cx)
                    .placeholder(placeholder)
                    .default_value(value)
            })
        };
        let area = |placeholder: &'static str,
                    value: String,
                    window: &mut Window,
                    cx: &mut Context<Self>| {
            cx.new(|cx| {
                TextareaState::new(window, cx)
                    .placeholder(placeholder)
                    .default_value(value)
            })
        };
        let name_input = input("Request name", first.name.clone(), window, cx);
        let url_input = input(
            "https://api.example.com/v1/things",
            first.url.clone(),
            window,
            cx,
        );
        let headers_input = area(
            "Accept: application/json\nAuthorization: Bearer …",
            first.headers.clone(),
            window,
            cx,
        );
        let body_input = area(
            "Request body (JSON, text, …)",
            first.body.clone(),
            window,
            cx,
        );
        let server_input = input(
            "speklo.your-company.com",
            settings.server_url.clone().unwrap_or_default(),
            window,
            cx,
        );

        // Signed in before? Reconnect quietly; the app still opens on the local requests.
        let client = settings
            .server_url
            .as_deref()
            .and_then(|url| Some((url, auth::load_token(url)?)))
            .and_then(|(url, token)| Client::new(url, Some(token)).ok());

        let mut this = Self {
            settings,
            method: first.method.clone(),
            store,
            selected: 0,
            name_input,
            url_input,
            headers_input,
            body_input,
            local_result: Load::Idle,
            client,
            server_input,
            sign_in: SignIn::Server,
            sign_in_attempt: 0,
            sign_in_error: None,
            me: Load::Idle,
            apis: Load::Idle,
            selected_api: None,
            operations: Load::Idle,
            collections: Load::Idle,
            selected_request: None,
            sent: Load::Idle,
            mocks: Load::Idle,
            section: Section::Local,
        };
        if this.client.is_some() {
            this.load_profile(cx);
        }
        this
    }

    // ----- local requests -----

    /// Copies the editor into the selected request and saves the file.
    fn commit_editor(&mut self, cx: &mut Context<Self>) {
        let Some(r) = self.store.requests.get_mut(self.selected) else {
            return;
        };
        r.name = self.name_input.read(cx).value().to_string();
        if r.name.trim().is_empty() {
            r.name = "Untitled request".into();
        }
        r.method = self.method.clone();
        r.url = self.url_input.read(cx).value().to_string();
        r.headers = self.headers_input.read(cx).value().to_string();
        r.body = self.body_input.read(cx).value().to_string();
        let _ = self.store.save();
    }

    /// Shows request `i` in the editor.
    fn open_local(&mut self, i: usize, window: &mut Window, cx: &mut Context<Self>) {
        self.commit_editor(cx);
        let Some(r) = self.store.requests.get(i).cloned() else {
            return;
        };
        self.selected = i;
        self.method = r.method.clone();
        self.name_input
            .update(cx, |s, cx| s.set_value(r.name, window, cx));
        self.url_input
            .update(cx, |s, cx| s.set_value(r.url, window, cx));
        self.headers_input
            .update(cx, |s, cx| s.set_value(r.headers, window, cx));
        self.body_input
            .update(cx, |s, cx| s.set_value(r.body, window, cx));
        self.local_result = Load::Idle;
        cx.notify();
    }

    fn new_local(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.commit_editor(cx);
        self.store.requests.push(LocalRequest::new("New request"));
        let i = self.store.requests.len() - 1;
        self.selected = i; // open_local commits first; point it at the new one
        self.open_local(i, window, cx);
        self.url_input.update(cx, |s, cx| s.focus(window, cx));
    }

    fn delete_local(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if self.store.requests.len() <= 1 {
            // Keep one request to edit; clear it instead.
            self.store.requests[0] = LocalRequest::new("New request");
            self.selected = 0;
        } else {
            self.store.requests.remove(self.selected);
            self.selected = self.selected.min(self.store.requests.len() - 1);
        }
        let _ = self.store.save();
        let i = self.selected;
        // Reload the editor without committing the deleted request back.
        let r = self.store.requests[i].clone();
        self.method = r.method.clone();
        self.name_input
            .update(cx, |s, cx| s.set_value(r.name, window, cx));
        self.url_input
            .update(cx, |s, cx| s.set_value(r.url, window, cx));
        self.headers_input
            .update(cx, |s, cx| s.set_value(r.headers, window, cx));
        self.body_input
            .update(cx, |s, cx| s.set_value(r.body, window, cx));
        self.local_result = Load::Idle;
        cx.notify();
    }

    fn send_local(&mut self, cx: &mut Context<Self>) {
        self.commit_editor(cx);
        let Some(req) = self.store.requests.get(self.selected).cloned() else {
            return;
        };
        self.local_result = Load::Loading;
        cx.notify();
        cx.spawn(async move |this, cx| {
            let result = cx
                .background_executor()
                .spawn(async move { local::send(&req) })
                .await;
            let _ = this.update(cx, |this, cx| {
                this.local_result = match result {
                    Ok(r) => Load::Ready(r),
                    Err(e) => Load::Failed(format!("{e:#}")),
                };
                cx.notify();
            });
        })
        .detach();
    }

    // ----- team server work, off the UI thread -----

    /// Runs `work` with the signed-in client on a background thread, then
    /// hands the result to `done` on the UI thread. An expired session signs
    /// the person out (their local requests stay).
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

    fn load_profile(&mut self, cx: &mut Context<Self>) {
        self.me = Load::Loading;
        self.fetch(
            cx,
            |c| Ok((c.me()?, c.workspaces()?)),
            |this, r| this.me = r.map_or_else(Load::Failed, Load::Ready),
        );
    }

    fn open_section(&mut self, section: Section, cx: &mut Context<Self>) {
        if self.section == Section::Local && section != Section::Local {
            self.commit_editor(cx);
        }
        self.section = section;
        match section {
            Section::Apis => {
                self.apis = Load::Loading;
                self.fetch(
                    cx,
                    |c| c.apis(),
                    |this, r| this.apis = r.map_or_else(Load::Failed, Load::Ready),
                );
            }
            Section::Requests => {
                self.collections = Load::Loading;
                self.fetch(
                    cx,
                    |c| c.collections(),
                    |this, r| this.collections = r.map_or_else(Load::Failed, Load::Ready),
                );
            }
            Section::Mocks => {
                self.mocks = Load::Loading;
                self.fetch(
                    cx,
                    |c| c.mocks(),
                    |this, r| this.mocks = r.map_or_else(Load::Failed, Load::Ready),
                );
            }
            Section::Local | Section::SignIn => {}
        }
        cx.notify();
    }

    fn select_api(&mut self, id: String, cx: &mut Context<Self>) {
        self.selected_api = Some(id.clone());
        self.operations = Load::Loading;
        self.fetch(
            cx,
            move |c| c.operations(&id),
            |this, r| this.operations = r.map_or_else(Load::Failed, Load::Ready),
        );
    }

    fn send_team_request(&mut self, cx: &mut Context<Self>) {
        let Some((id, _)) = self.selected_request.clone() else {
            return;
        };
        self.sent = Load::Loading;
        self.fetch(
            cx,
            move |c| c.send(&id),
            |this, r| this.sent = r.map_or_else(Load::Failed, Load::Ready),
        );
    }

    // ----- optional sign-in -----

    /// Step 1: check the server and fetch its sign-in options.
    fn check_server(&mut self, cx: &mut Context<Self>) {
        let typed = self.server_input.read(cx).value().to_string();
        let Some(server) = normalize_server_url(&typed) else {
            self.sign_in_error =
                Some("Enter your team's Speklo server, like speklo.your-company.com".into());
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
                            "This server runs Speklo {} (API v{}), which this app doesn't support. {}",
                            v.version,
                            v.api,
                            if v.api > *api::SUPPORTED_API.end() {
                                "Update the Speklo app."
                            } else {
                                "Ask your admin to upgrade the server."
                            }
                        );
                    }
                    c.providers()
                })
                .await;
            let _ = this.update(cx, |this, cx| {
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
                    return; // cancelled
                }
                match result.and_then(|token| Client::new(&server, Some(token))) {
                    Ok(client) => {
                        this.settings.server_url = Some(server);
                        let _ = this.settings.save();
                        this.client = Some(client);
                        this.sign_in = SignIn::Server;
                        this.load_profile(cx);
                        this.open_section(Section::Apis, cx);
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
        self.sign_in = SignIn::Server;
        self.sign_in_error = reason.clone();
        self.me = Load::Idle;
        self.apis = Load::Idle;
        self.operations = Load::Idle;
        self.collections = Load::Idle;
        self.mocks = Load::Idle;
        self.sent = Load::Idle;
        self.selected_api = None;
        self.selected_request = None;
        if Section::TEAM.contains(&self.section) {
            // Session ended: say so on the sign-in page; a deliberate sign-out returns to local work.
            self.section = if reason.is_some() {
                Section::SignIn
            } else {
                Section::Local
            };
        }
        cx.notify();
    }

    // ----- rendering -----

    fn render_sidebar(&mut self, cx: &mut Context<Self>) -> impl IntoElement {
        let t = cx.theme();
        let nav = |s: Section, active: bool, cx: &Context<Self>| {
            div()
                .id(s.label())
                .px_2()
                .py_1p5()
                .rounded_md()
                .text_sm()
                .when(active, |d| {
                    d.bg(t.list_active).font_weight(FontWeight::MEDIUM)
                })
                .hover(|d| d.bg(t.list_active))
                .child(s.label())
                .on_click(cx.listener(move |this, _, _, cx| this.open_section(s, cx)))
        };
        let heading = |text: &'static str| {
            div()
                .px_2()
                .pt_4()
                .pb_1()
                .text_xs()
                .font_weight(FontWeight::SEMIBOLD)
                .text_color(t.muted_foreground)
                .child(text)
        };

        let team: AnyElement = if let Some(client) = &self.client {
            let who = match &self.me {
                Load::Ready((me, ws)) => (
                    me.name.clone(),
                    format!(
                        "{} · {}",
                        me.email,
                        ws.first().map(|w| w.name.as_str()).unwrap_or("")
                    ),
                ),
                Load::Failed(e) => ("Couldn't load your profile".into(), e.clone()),
                _ => ("Loading…".into(), String::new()),
            };
            v_flex()
                .gap_0p5()
                .children(Section::TEAM.map(|s| nav(s, s == self.section, cx)))
                .child(
                    v_flex()
                        .gap_0p5()
                        .px_2()
                        .pt_4()
                        .child(div().text_sm().font_weight(FontWeight::MEDIUM).child(who.0))
                        .child(div().text_xs().text_color(t.muted_foreground).child(who.1))
                        .child(
                            div()
                                .text_xs()
                                .text_color(t.muted_foreground)
                                .child(client.base().to_string()),
                        ),
                )
                .child(
                    div().pt_2().child(
                        Button::new("sign-out")
                            .ghost()
                            .label("Sign out")
                            .on_click(cx.listener(|this, _, _, cx| this.sign_out(cx))),
                    ),
                )
                .into_any_element()
        } else {
            v_flex()
                .gap_2()
                .px_2()
                .child(
                    div()
                        .text_xs()
                        .text_color(t.muted_foreground)
                        .child("Share APIs, requests and mock servers with your team."),
                )
                .child(
                    Button::new("collab")
                        .outline()
                        .label("Sign in to collaborate")
                        .on_click(
                            cx.listener(|this, _, _, cx| this.open_section(Section::SignIn, cx)),
                        ),
                )
                .into_any_element()
        };

        v_flex()
            .w(px(232.))
            .h_full()
            .flex_none()
            .p_3()
            .gap_0p5()
            .bg(t.sidebar)
            .border_r_1()
            .border_color(t.border)
            .child(
                h_flex()
                    .gap_2()
                    .px_2()
                    .pb_2()
                    .pt_2()
                    .child(logo(t.foreground, rgb(ACCENT).into(), t.sidebar, 22.))
                    .child(div().font_weight(FontWeight::SEMIBOLD).child("speklo")),
            )
            .child(heading("ON THIS COMPUTER"))
            .child(nav(Section::Local, self.section == Section::Local, cx))
            .child(heading("TEAM"))
            .child(team)
    }

    fn render_local(&mut self, cx: &mut Context<Self>) -> AnyElement {
        let t = cx.theme();
        let list = v_flex()
            .gap_1()
            .child(
                Button::new("new-local")
                    .outline()
                    .label("New request")
                    .on_click(cx.listener(|this, _, window, cx| this.new_local(window, cx))),
            )
            .children(self.store.requests.iter().enumerate().map(|(i, r)| {
                let active = i == self.selected;
                h_flex()
                    .id(SharedString::from(format!("local-{}", r.id)))
                    .gap_2()
                    .px_2()
                    .py_1()
                    .rounded_md()
                    .when(active, |d| d.bg(t.list_active))
                    .hover(|d| d.bg(t.list_active))
                    .child(method_badge(&r.method))
                    .child(div().text_sm().truncate().child(r.name.clone()))
                    .on_click(
                        cx.listener(move |this, _, window, cx| this.open_local(i, window, cx)),
                    )
            }));

        let methods = h_flex().gap_1().children(local::METHODS.map(|m| {
            let b = Button::new(SharedString::from(format!("method-{m}")))
                .label(m)
                .on_click(cx.listener(move |this, _, _, cx| {
                    this.method = m.to_string();
                    cx.notify();
                }));
            if self.method == m {
                b.primary()
            } else {
                b.ghost()
            }
        }));

        let response = match &self.local_result {
            Load::Idle => note(
                "Send the request to see the response here. It goes straight from this computer.",
                t.muted_foreground,
            ),
            Load::Loading => note("Sending…", t.muted_foreground),
            Load::Failed(e) => note(e.clone(), t.danger),
            Load::Ready(r) => render_response(
                &Response {
                    status: r.status as i64,
                    status_text: r.status_text.clone(),
                    duration_ms: r.duration_ms as i64,
                    size_bytes: r.size_bytes as i64,
                    url_line: String::new(),
                    env: String::new(),
                    headers: r.headers.clone(),
                    body: r.body.clone(),
                },
                t.muted_foreground,
                t.danger,
                t.border,
            ),
        };

        let label = |text: &'static str| {
            div()
                .text_xs()
                .font_weight(FontWeight::MEDIUM)
                .text_color(t.muted_foreground)
                .child(text)
        };
        let editor = v_flex()
            .gap_3()
            .child(
                h_flex()
                    .gap_2()
                    .child(div().flex_1().child(Input::new(&self.name_input)))
                    .child(
                        Button::new("delete-local")
                            .ghost()
                            .label("Delete")
                            .on_click(
                                cx.listener(|this, _, window, cx| this.delete_local(window, cx)),
                            ),
                    ),
            )
            .child(methods)
            .child(
                h_flex()
                    .gap_2()
                    .child(div().flex_1().child(Input::new(&self.url_input)))
                    .child(
                        Button::new("send-local")
                            .primary()
                            .label("Send")
                            .loading(self.local_result.is_loading())
                            .on_click(cx.listener(|this, _, _, cx| this.send_local(cx))),
                    ),
            )
            .child(label("Headers · one per line, Name: value"))
            .child(Textarea::new(&self.headers_input).h(px(84.)))
            .child(label("Body"))
            .child(Textarea::new(&self.body_input).h(px(132.)))
            .child(label("Response"))
            .child(response);

        split(list, editor, t.border).into_any_element()
    }

    fn render_sign_in(&mut self, cx: &mut Context<Self>) -> AnyElement {
        let t = cx.theme();
        let (muted, danger) = (t.muted_foreground, t.danger);
        let body: AnyElement = match &self.sign_in {
            SignIn::Server | SignIn::Checking => {
                let checking = matches!(self.sign_in, SignIn::Checking);
                v_flex()
                    .gap_3()
                    .child(div().text_sm().text_color(muted).child(
                        "Enter your team's Speklo server. Your company runs it, or it's the address your admin gave you.",
                    ))
                    .child(Input::new(&self.server_input))
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
                    .gap_3()
                    .child(
                        div()
                            .text_sm()
                            .text_color(muted)
                            .child(format!("Sign in to {org} at {server}")),
                    )
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
                .gap_3()
                .child(div().text_sm().child(format!(
                    "Finish signing in with {provider} in your browser."
                )))
                .child(
                    div()
                        .text_xs()
                        .text_color(muted)
                        .child(format!("Connecting to {server}")),
                )
                .child(
                    Button::new("cancel-sign-in")
                        .ghost()
                        .label("Cancel")
                        .on_click(cx.listener(|this, _, _, cx| {
                            this.sign_in_attempt += 1;
                            this.sign_in = SignIn::Server;
                            cx.notify();
                        })),
                )
                .into_any_element(),
        };
        div()
            .id("sign-in")
            .size_full()
            .overflow_y_scroll()
            .child(
                v_flex()
                    .w(px(440.))
                    .mx_auto()
                    .mt(px(72.))
                    .gap_4()
                    .child(logo(t.foreground, rgb(ACCENT).into(), t.background, 48.))
                    .child(div().text_2xl().font_weight(FontWeight::SEMIBOLD).child("Collaborate with your team"))
                    .child(body)
                    .children(self.sign_in_error.clone().map(|e| div().text_sm().text_color(danger).child(e)))
                    .child(div().text_xs().text_color(muted).child(
                        "Your requests on this computer stay here and keep working, signed in or not.",
                    )),
            )
            .into_any_element()
    }

    fn render_apis(&mut self, cx: &mut Context<Self>) -> AnyElement {
        let t = cx.theme();
        let list = match &self.apis {
            Load::Ready(apis) if apis.is_empty() => note(
                "No APIs in this workspace yet. Create one in the web app.",
                t.muted_foreground,
            ),
            Load::Ready(apis) => v_flex().gap_1().children(apis.iter().map(|a| {
                let id = a.id.clone();
                let active = self.selected_api.as_deref() == Some(a.id.as_str());
                v_flex()
                    .id(SharedString::from(format!("api-{}", a.id)))
                    .p_2()
                    .rounded_md()
                    .when(active, |d| d.bg(t.list_active))
                    .hover(|d| d.bg(t.list_active))
                    .child(
                        h_flex()
                            .gap_2()
                            .child(
                                div()
                                    .text_sm()
                                    .font_weight(FontWeight::MEDIUM)
                                    .child(a.name.clone()),
                            )
                            .child(pill(&a.status, t.muted, t.muted_foreground)),
                    )
                    .child(
                        div()
                            .text_xs()
                            .text_color(t.muted_foreground)
                            .child(format!(
                                "v{} · {} endpoints{}",
                                a.version,
                                a.endpoints,
                                if a.has_draft_changes {
                                    " · unpublished changes"
                                } else {
                                    ""
                                }
                            )),
                    )
                    .on_click(cx.listener(move |this, _, _, cx| this.select_api(id.clone(), cx)))
            })),
            other => status(other, "Loading APIs…", t.muted_foreground, t.danger),
        };
        let detail = match (&self.selected_api, &self.operations) {
            (None, _) => note("Select an API to see its endpoints.", t.muted_foreground),
            (Some(_), Load::Ready(ops)) => v_flex().gap_1().children(ops.iter().map(|op| {
                h_flex()
                    .gap_3()
                    .py_1()
                    .child(method_badge(&op.method))
                    .child(div().font_family("Menlo").text_sm().child(op.path.clone()))
                    .child(
                        div()
                            .text_sm()
                            .text_color(t.muted_foreground)
                            .child(op.summary.clone()),
                    )
                    .when(op.deprecated, |d| {
                        d.child(pill("deprecated", t.muted, t.danger))
                    })
            })),
            (Some(_), other) => status(other, "Loading endpoints…", t.muted_foreground, t.danger),
        };
        split(list, detail, t.border).into_any_element()
    }

    fn render_team_requests(&mut self, cx: &mut Context<Self>) -> AnyElement {
        let t = cx.theme();
        let tree = match &self.collections {
            Load::Ready(cols) if cols.is_empty() => {
                note("No saved requests yet.", t.muted_foreground)
            }
            Load::Ready(cols) => v_flex().gap_3().children(cols.iter().map(|c| {
                v_flex()
                    .gap_0p5()
                    .child(
                        div()
                            .text_xs()
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(t.muted_foreground)
                            .child(c.name.to_uppercase()),
                    )
                    .children(c.folders.iter().flat_map(|f| {
                        let folder = (!f.name.is_empty()).then(|| {
                            div()
                                .pt_1()
                                .text_xs()
                                .text_color(t.muted_foreground)
                                .child(f.name.clone())
                        });
                        folder.into_iter().map(IntoElement::into_any_element).chain(
                            f.requests.iter().map(|r| {
                                let pick = (r.id.clone(), r.name.clone());
                                let active =
                                    self.selected_request.as_ref().map(|s| &s.0) == Some(&r.id);
                                h_flex()
                                    .id(SharedString::from(format!("req-{}", r.id)))
                                    .gap_2()
                                    .px_2()
                                    .py_1()
                                    .rounded_md()
                                    .when(active, |d| d.bg(t.list_active))
                                    .hover(|d| d.bg(t.list_active))
                                    .child(method_badge(&r.method))
                                    .child(div().text_sm().child(r.name.clone()))
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        this.selected_request = Some(pick.clone());
                                        this.sent = Load::Idle;
                                        cx.notify();
                                    }))
                                    .into_any_element()
                            }),
                        )
                    }))
            })),
            other => status(other, "Loading requests…", t.muted_foreground, t.danger),
        };
        let detail = match &self.selected_request {
            None => note(
                "Select a saved request, then send it. It runs on the team's server.",
                t.muted_foreground,
            ),
            Some((_, name)) => {
                let response = match &self.sent {
                    Load::Idle => note("Not sent yet.", t.muted_foreground),
                    Load::Loading => note("Sending…", t.muted_foreground),
                    Load::Failed(e) => note(e.clone(), t.danger),
                    Load::Ready(r) => match r.result.error.as_ref().filter(|e| !e.is_empty()) {
                        Some(e) => note(e.clone(), t.danger),
                        None => render_response(
                            &Response {
                                status: r.result.status,
                                status_text: r.result.status_text.clone(),
                                duration_ms: r.result.duration_ms,
                                size_bytes: r.result.size_bytes,
                                url_line: format!("{} {}", r.result.method, r.result.url),
                                env: r.environment.clone(),
                                headers: r
                                    .result
                                    .headers
                                    .iter()
                                    .map(|h| (h.key.clone(), h.value.clone()))
                                    .collect(),
                                body: r.result.body.clone(),
                            },
                            t.muted_foreground,
                            t.danger,
                            t.border,
                        ),
                    },
                };
                v_flex()
                    .gap_3()
                    .child(
                        h_flex()
                            .justify_between()
                            .child(
                                div()
                                    .text_lg()
                                    .font_weight(FontWeight::SEMIBOLD)
                                    .child(name.clone()),
                            )
                            .child(
                                Button::new("send-team")
                                    .primary()
                                    .label("Send")
                                    .loading(self.sent.is_loading())
                                    .on_click(
                                        cx.listener(|this, _, _, cx| this.send_team_request(cx)),
                                    ),
                            ),
                    )
                    .child(response)
            }
        };
        split(tree, detail, t.border).into_any_element()
    }

    fn render_mocks(&mut self, cx: &mut Context<Self>) -> AnyElement {
        let t = cx.theme();
        let body =
            match &self.mocks {
                Load::Ready(m) if m.is_empty() => note(
                    "No mock servers yet. Start one from an API in the web app.",
                    t.muted_foreground,
                ),
                Load::Ready(mocks) => v_flex().gap_2().children(mocks.iter().map(|m| {
                    h_flex()
                        .gap_4()
                        .p_3()
                        .rounded_md()
                        .border_1()
                        .border_color(t.border)
                        .child(
                            v_flex()
                                .flex_1()
                                .gap_0p5()
                                .child(
                                    h_flex()
                                        .gap_2()
                                        .child(
                                            div()
                                                .text_sm()
                                                .font_weight(FontWeight::MEDIUM)
                                                .child(m.name.clone()),
                                        )
                                        .child(pill(
                                            if m.enabled { "running" } else { "stopped" },
                                            t.muted,
                                            t.muted_foreground,
                                        )),
                                )
                                .child(
                                    div()
                                        .text_xs()
                                        .text_color(t.muted_foreground)
                                        .child(format!("{} {}", m.api_name, m.api_version)),
                                )
                                .child(div().text_xs().font_family("Menlo").child(m.url.clone())),
                        )
                        .child(
                            v_flex()
                                .items_end()
                                .child(
                                    div()
                                        .text_sm()
                                        .child(format!("{} calls today", m.calls_today)),
                                )
                                .child(div().text_xs().text_color(t.muted_foreground).child(
                                    format!(
                                        "{} ms latency · {}% errors",
                                        m.latency_ms, m.error_rate
                                    ),
                                )),
                        )
                })),
                other => status(other, "Loading mock servers…", t.muted_foreground, t.danger),
            };
        div()
            .id("mocks")
            .size_full()
            .p_6()
            .overflow_y_scroll()
            .child(body)
            .into_any_element()
    }
}

impl Render for SpekloApp {
    fn render(&mut self, _: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        // A team section without a session (e.g. it just expired) shows sign-in.
        if self.client.is_none() && Section::TEAM.contains(&self.section) {
            self.section = Section::SignIn;
        }
        let content = match self.section {
            Section::Local => self.render_local(cx),
            Section::SignIn => self.render_sign_in(cx),
            Section::Apis => self.render_apis(cx),
            Section::Requests => self.render_team_requests(cx),
            Section::Mocks => self.render_mocks(cx),
        };
        let sidebar = self.render_sidebar(cx).into_any_element();
        let t = cx.theme();
        h_flex()
            .size_full()
            .bg(t.background)
            .text_color(t.foreground)
            .child(sidebar)
            .child(
                v_flex()
                    .flex_1()
                    .h_full()
                    .min_w_0()
                    .child(
                        h_flex()
                            .h(px(48.))
                            .px_6()
                            .gap_3()
                            .border_b_1()
                            .border_color(t.border)
                            .child(
                                div()
                                    .font_weight(FontWeight::SEMIBOLD)
                                    .child(self.section.label()),
                            )
                            .when(self.section == Section::Local, |d| {
                                d.child(
                                    div()
                                        .text_xs()
                                        .text_color(t.muted_foreground)
                                        .child("Saved on this computer"),
                                )
                            }),
                    )
                    .child(div().flex_1().min_h_0().child(content)),
            )
    }
}

// ----- small pieces -----

/// The Speklo mark, "Orbit": a ring with a speck breaking out of it
/// (branding/speklo-mark-*.svg), drawn with plain elements so it needs no
/// image asset. `gap` is the colour behind the logo, used for the cut-out
/// around the speck.
fn logo(stroke: Hsla, dot: Hsla, gap: Hsla, size: f32) -> impl IntoElement {
    // Coordinates from the SVG's 46-unit view box, scaled to `size`.
    let k = size / 46.;
    let circle = |x: f32, y: f32, d: f32| {
        div()
            .absolute()
            .left(px(x * k))
            .top(px(y * k))
            .w(px(d * k))
            .h(px(d * k))
            .rounded_full()
    };
    div()
        .relative()
        .flex_none()
        .w(px(size))
        .h(px(size))
        .child(
            circle(2.5, 2.5, 41.)
                .border(px(5. * k))
                .border_color(stroke),
        )
        .child(circle(26.7, 1.3, 18.).bg(gap))
        .child(circle(30.2, 4.8, 11.).bg(dot))
}

fn split(left: impl IntoElement, right: impl IntoElement, border: Hsla) -> impl IntoElement {
    h_flex()
        .size_full()
        .items_start()
        .child(
            div()
                .id("left")
                .w(px(300.))
                .h_full()
                .flex_none()
                .p_3()
                .border_r_1()
                .border_color(border)
                .overflow_y_scroll()
                .child(left),
        )
        .child(
            div()
                .id("right")
                .flex_1()
                .h_full()
                .min_w_0()
                .p_6()
                .overflow_y_scroll()
                .child(right),
        )
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

fn pill(text: &str, bg: Hsla, fg: Hsla) -> impl IntoElement {
    div()
        .px_1p5()
        .rounded_sm()
        .bg(bg)
        .text_color(fg)
        .text_xs()
        .child(text.to_string())
}

fn method_badge(method: &str) -> impl IntoElement {
    let color = match method {
        "GET" => rgb(0x16A34A),
        "POST" => rgb(0x2563EB),
        "PUT" | "PATCH" => rgb(0xD97706),
        "DELETE" => rgb(0xDC2626),
        _ => rgb(0x6B7280),
    };
    div()
        .w(px(52.))
        .flex_none()
        .text_xs()
        .font_family("Menlo")
        .font_weight(FontWeight::BOLD)
        .text_color(color)
        .child(method.to_string())
}

/// A response, from either a local send or the team server.
struct Response {
    status: i64,
    status_text: String,
    duration_ms: i64,
    size_bytes: i64,
    url_line: String,
    env: String,
    headers: Vec<(String, String)>,
    body: String,
}

fn render_response(r: &Response, muted: Hsla, danger: Hsla, border: Hsla) -> Div {
    let status_color: Hsla = match r.status {
        200..=299 => rgb(0x16A34A).into(),
        300..=399 => rgb(0xD97706).into(),
        _ => danger,
    };
    // Very large bodies would make the text layout slow.
    const MAX: usize = 200_000;
    let mut body = r.body.clone();
    if body.len() > MAX {
        let cut = (0..=MAX)
            .rev()
            .find(|i| body.is_char_boundary(*i))
            .unwrap_or(0);
        body.truncate(cut);
        body.push_str("\n… (cut off here)");
    }
    v_flex()
        .gap_3()
        .child(
            h_flex()
                .gap_4()
                .child(
                    div()
                        .font_weight(FontWeight::SEMIBOLD)
                        .text_color(status_color)
                        .child(format!("{} {}", r.status, r.status_text)),
                )
                .child(
                    div()
                        .text_sm()
                        .text_color(muted)
                        .child(format!("{} ms · {} bytes", r.duration_ms, r.size_bytes)),
                )
                .when(!r.env.is_empty(), |d| {
                    d.child(
                        div()
                            .text_sm()
                            .text_color(muted)
                            .child(format!("env: {}", r.env)),
                    )
                }),
        )
        .when(!r.url_line.is_empty(), |d| {
            d.child(
                div()
                    .text_xs()
                    .font_family("Menlo")
                    .text_color(muted)
                    .child(r.url_line.clone()),
            )
        })
        .child(
            v_flex()
                .gap_0p5()
                .children(r.headers.iter().take(30).map(|(k, v)| {
                    h_flex()
                        .gap_2()
                        .text_xs()
                        .font_family("Menlo")
                        .child(div().text_color(muted).child(format!("{k}:")))
                        .child(v.clone())
                })),
        )
        .child(
            div()
                .p_3()
                .rounded_md()
                .border_1()
                .border_color(border)
                .text_xs()
                .font_family("Menlo")
                .child(body),
        )
}
