//! The organization pages, laid out like the web app's: APIs, History,
//! Environments and Mock servers.

use gpui_kit::component::switch::Switch;

use super::*;
use crate::api::{ApiList, HistoryItem, Mock, MockCall, Variable, VariableInput};
use crate::time;

#[derive(Clone, Copy, PartialEq, Eq)]
enum Sort {
    Updated,
    Name,
    Traffic,
}

/// One editable row of an environment's variables.
struct VarRow {
    key: Entity<InputState>,
    value: Entity<InputState>,
    /// default | secret | script
    kind: String,
    enabled: bool,
    /// A stored secret nobody has retyped: the server keeps its value.
    keep: bool,
}

pub(super) struct Pages {
    // APIs
    apis: Load<ApiList>,
    api_filter: &'static str,
    api_sort: Sort,
    api_search: Entity<InputState>,

    // History
    history: Load<Vec<HistoryItem>>,
    history_search: Entity<InputState>,
    /// The entry shown in the side panel: id, method, URL.
    history_open: Option<(String, String, String)>,
    history_detail: Load<Resp>,
    history_request: Option<String>,
    history_tab: RespTab,
    confirm_clear: bool,

    // Environments
    env_selected: Option<String>,
    env_vars: Load<Vec<Variable>>,
    env_rows: Option<Vec<VarRow>>,
    env_saving: bool,
    env_notice: Option<String>,
    new_env: Entity<InputState>,
    env_subs: Vec<Subscription>,

    // Mock servers
    pub(super) mocks: Load<Vec<Mock>>,
    mock_calls: Load<Vec<MockCall>>,
}

impl Pages {
    pub(super) fn new(
        subs: &mut Vec<Subscription>,
        window: &mut Window,
        cx: &mut Context<RequaraApp>,
    ) -> Self {
        Pages {
            apis: Load::Idle,
            api_filter: "all",
            api_sort: Sort::Updated,
            api_search: text_input("Search APIs", "", subs, window, cx),
            history: Load::Idle,
            history_search: text_input("Filter by URL or status", "", subs, window, cx),
            history_open: None,
            history_detail: Load::Idle,
            history_request: None,
            history_tab: RespTab::Body,
            confirm_clear: false,
            env_selected: None,
            env_vars: Load::Idle,
            env_rows: None,
            env_saving: false,
            env_notice: None,
            new_env: text_input("New environment name, e.g. QA", "", subs, window, cx),
            env_subs: vec![],
            mocks: Load::Idle,
            mock_calls: Load::Idle,
        }
    }

    /// Signed out: forget everything from the server.
    pub(super) fn reset(&mut self) {
        self.apis = Load::Idle;
        self.history = Load::Idle;
        self.history_open = None;
        self.history_detail = Load::Idle;
        self.confirm_clear = false;
        self.env_selected = None;
        self.env_vars = Load::Idle;
        self.env_rows = None;
        self.env_subs.clear();
        self.env_notice = None;
        self.mocks = Load::Idle;
        self.mock_calls = Load::Idle;
    }
}

fn var_row(
    v: &Variable,
    subs: &mut Vec<Subscription>,
    window: &mut Window,
    cx: &mut Context<RequaraApp>,
) -> VarRow {
    let secret = v.kind == "secret";
    let keep = secret && v.has_value;
    let (value, placeholder) = if secret {
        (
            String::new(),
            if keep {
                "•••••••••••• stored · type to replace"
            } else {
                "Enter a secret value"
            },
        )
    } else {
        (v.value.clone(), "Value")
    };
    let input = cx.new(|cx| {
        InputState::new(window, cx)
            .placeholder(placeholder)
            .default_value(value)
            .masked(secret)
    });
    subs.push(cx.subscribe(&input, |_, _, _: &InputEvent, cx| cx.notify()));
    VarRow {
        key: text_input("Add variable", &v.key, subs, window, cx),
        value: input,
        kind: if v.kind.is_empty() {
            "default".into()
        } else {
            v.kind.clone()
        },
        enabled: v.enabled,
        keep,
    }
}

/// What saving would send, and what the server has: equal means no edits.
fn payload(rows: &[VarRow], cx: &App) -> Vec<VariableInput> {
    rows.iter()
        .filter_map(|r| {
            let key = r.key.read(cx).value().trim().to_string();
            let typed = r.value.read(cx).value().to_string();
            (!key.is_empty()).then(|| VariableInput {
                key,
                keep: r.kind == "secret" && r.keep && typed.is_empty(),
                value: typed,
                kind: r.kind.clone(),
                enabled: r.enabled,
            })
        })
        .collect()
}

fn baseline(vars: &[Variable]) -> Vec<VariableInput> {
    vars.iter()
        .map(|v| VariableInput {
            key: v.key.clone(),
            value: if v.kind == "secret" {
                String::new()
            } else {
                v.value.clone()
            },
            kind: if v.kind.is_empty() {
                "default".into()
            } else {
                v.kind.clone()
            },
            enabled: v.enabled,
            keep: v.kind == "secret" && v.has_value,
        })
        .collect()
}

impl RequaraApp {
    /// Loads what a page shows when it's opened.
    pub(super) fn load_page(&mut self, view: View, cx: &mut Context<Self>) {
        match view {
            View::Apis => {
                if self.pages.apis.ready().is_none() {
                    self.pages.apis = Load::Loading;
                }
                self.fetch(
                    cx,
                    |c| c.apis(),
                    |this, r| this.pages.apis = r.map_or_else(Load::Failed, Load::Ready),
                );
            }
            View::Docs => self.load_docs(cx),
            View::History => self.load_history(cx),
            View::Mocks => self.load_mocks(cx),
            View::Environments => {
                self.fetch(
                    cx,
                    |c| c.environments(),
                    |this, r| {
                        if let Ok(e) = r {
                            this.envs = Load::Ready(e);
                        }
                    },
                );
                if self.pages.env_selected.is_none() {
                    let first = self.active_env.clone().or_else(|| {
                        self.envs
                            .ready()
                            .and_then(|e| e.items.first().map(|e| e.id.clone()))
                    });
                    if let Some(id) = first {
                        self.select_env(id, cx);
                    }
                }
            }
            _ => {}
        }
    }

    /// Builds inputs that need a window (called from `render`).
    pub(super) fn materialize_pages(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if self.view != View::Environments {
            return;
        }
        // Pick an environment once the list has arrived: the active one, else the first.
        if self.pages.env_selected.is_none()
            && let Some(envs) = self.envs.ready()
            && let Some(id) = self
                .active_env
                .clone()
                .filter(|id| envs.items.iter().any(|e| e.id == *id))
                .or_else(|| envs.items.first().map(|e| e.id.clone()))
        {
            self.select_env(id, cx);
        }
        let p = &mut self.pages;
        if p.env_rows.is_none()
            && let Load::Ready(vars) = &p.env_vars
        {
            p.env_subs.clear();
            let rows = vars
                .iter()
                .map(|v| var_row(v, &mut p.env_subs, window, cx))
                .collect();
            p.env_rows = Some(rows);
        }
        // One empty row at the end; typing in it adds a variable.
        if let Some(rows) = &mut p.env_rows
            && rows
                .last()
                .is_none_or(|r| !r.key.read(cx).value().is_empty())
        {
            let blank = Variable {
                key: String::new(),
                value: String::new(),
                kind: "default".into(),
                enabled: true,
                has_value: false,
            };
            rows.push(var_row(&blank, &mut p.env_subs, window, cx));
        }
    }

    // ----- APIs -----

    pub(super) fn render_apis(&mut self, cx: &mut Context<Self>) -> AnyElement {
        let t = cx.theme();
        let (muted, border) = (t.muted_foreground, t.border);
        let p = &self.pages;
        let q = p.api_search.read(cx).value().to_lowercase();

        let (subtitle, counts) = match &p.apis {
            Load::Ready(l) => (
                format!(
                    "{} APIs and {} endpoints{}",
                    l.counts.get("all").copied().unwrap_or(l.items.len() as i64),
                    l.items.iter().map(|a| a.endpoints).sum::<i64>(),
                    if p.api_filter != "all" { " shown" } else { "" }
                ),
                l.counts.clone(),
            ),
            _ => (String::new(), Default::default()),
        };

        let filters = h_flex().gap_1().children(
            [
                ("all", "All"),
                ("published", "Published"),
                ("draft", "Draft"),
                ("deprecated", "Deprecated"),
            ]
            .map(|(value, label)| {
                let text = match counts.get(value) {
                    Some(n) => format!("{label}  {n}"),
                    None => label.to_string(),
                };
                let b = Button::new(SharedString::from(format!("f-{value}")))
                    .small()
                    .label(text)
                    .on_click(cx.listener(move |this, _, _, cx| {
                        this.pages.api_filter = value;
                        cx.notify();
                    }));
                if p.api_filter == value {
                    b.outline()
                } else {
                    b.ghost()
                }
            }),
        );
        let sorts = h_flex()
            .gap_1()
            .child(div().text_sm().text_color(muted).child("Sort by"))
            .children(
                [
                    (Sort::Updated, "Last updated"),
                    (Sort::Name, "Name"),
                    (Sort::Traffic, "Traffic"),
                ]
                .map(|(sort, label)| {
                    let b = Button::new(SharedString::from(format!("s-{label}")))
                        .small()
                        .label(label)
                        .on_click(cx.listener(move |this, _, _, cx| {
                            this.pages.api_sort = sort;
                            cx.notify();
                        }));
                    if p.api_sort == sort {
                        b.outline()
                    } else {
                        b.ghost()
                    }
                }),
            );

        let head = |label: &'static str, w: f32, right: bool| {
            div()
                .w(px(w))
                .flex_none()
                .px_3()
                .when(right, |d| d.text_right())
                .child(label)
        };
        let header = h_flex()
            .h(px(46.))
            .bg(t.muted)
            .text_sm()
            .font_weight(FontWeight::SEMIBOLD)
            .text_color(muted)
            .child(div().flex_1().min_w(px(220.)).px_3().child("API"))
            .child(head("Version", 96., false))
            .child(head("Status", 124., false))
            .child(head("Endpoints", 96., true))
            .child(head("Requests · 24h", 128., true))
            .child(head("p95 latency", 108., true))
            .child(head("Error rate", 96., true))
            .child(head("Owner", 124., false))
            .child(head("Updated", 140., false));

        let body: AnyElement = match &p.apis {
            Load::Ready(list) => {
                let mut rows: Vec<_> = list
                    .items
                    .iter()
                    .filter(|a| p.api_filter == "all" || a.status == p.api_filter)
                    .filter(|a| {
                        q.is_empty()
                            || format!("{}{}{}", a.name, a.description, a.owner)
                                .to_lowercase()
                                .contains(&q)
                    })
                    .collect();
                match p.api_sort {
                    Sort::Name => rows.sort_by_key(|a| a.name.to_lowercase()),
                    Sort::Traffic => rows.sort_by_key(|a| std::cmp::Reverse(a.calls_24h)),
                    Sort::Updated => {}
                }
                if rows.is_empty() {
                    div()
                        .py_10()
                        .text_center()
                        .text_sm()
                        .text_color(muted)
                        .child(if q.is_empty() {
                            "No APIs here yet".to_string()
                        } else {
                            format!("No APIs match “{q}”")
                        })
                        .into_any_element()
                } else {
                    let dash = || div().text_color(muted).child("—");
                    let num = |w: f32| div().w(px(w)).flex_none().px_3().text_right().text_sm();
                    v_flex()
                        .children(rows.into_iter().map(|a| {
                            let status_color: Hsla = match a.status.as_str() {
                                "published" => rgb(0x52C41A),
                                "deprecated" => rgb(0xFAAD14),
                                _ => rgb(0xBFBFBF),
                            }
                            .into();
                            let api_id = a.id.clone();
                            let mut status = a.status.clone();
                            if let Some(f) = status.get_mut(0..1) {
                                f.make_ascii_uppercase();
                            }
                            h_flex()
                                .id(SharedString::from(format!("api-{}", a.id)))
                                .min_h(px(64.))
                                .py_2()
                                .border_t_1()
                                .border_color(border)
                                .hover(|d| d.bg(t.muted))
                                .child(
                                    v_flex()
                                        .flex_1()
                                        .min_w(px(220.))
                                        .px_3()
                                        .overflow_hidden()
                                        .child(
                                            h_flex()
                                                .gap_2()
                                                .child(
                                                    div()
                                                        .font_weight(FontWeight::MEDIUM)
                                                        .child(a.name.clone()),
                                                )
                                                .when(
                                                    a.has_draft_changes && a.status != "draft",
                                                    |d| {
                                                        d.child(
                                                            div()
                                                                .text_xs()
                                                                .text_color(rgb(0xD48806))
                                                                .child("unpublished changes"),
                                                        )
                                                    },
                                                ),
                                        )
                                        .child(
                                            div()
                                                .text_sm()
                                                .text_color(muted)
                                                .truncate()
                                                .child(a.description.clone()),
                                        ),
                                )
                                .child(
                                    div()
                                        .w(px(96.))
                                        .flex_none()
                                        .px_3()
                                        .text_sm()
                                        .font_family(MONO)
                                        .text_color(muted)
                                        .child(a.version.clone()),
                                )
                                .child(
                                    h_flex()
                                        .w(px(124.))
                                        .flex_none()
                                        .px_3()
                                        .gap_2()
                                        .text_sm()
                                        .child(dot(status_color, 6.))
                                        .child(status),
                                )
                                .child(num(96.).child(a.endpoints.to_string()))
                                .child(if a.calls_24h > 0 {
                                    num(128.).child(time::compact(a.calls_24h))
                                } else {
                                    num(128.).child(dash())
                                })
                                .child(match a.p95_ms {
                                    Some(ms) => num(108.).child(format!("{} ms", ms.round())),
                                    None => num(108.).child(dash()),
                                })
                                .child(match a.error_rate.filter(|_| a.calls_24h > 0) {
                                    Some(rate) => num(96.)
                                        .when(rate > 0.01, |d| d.text_color(t.danger))
                                        .child(format!("{:.2}%", rate * 100.)),
                                    None => num(96.).child(dash()),
                                })
                                .child(
                                    div()
                                        .w(px(124.))
                                        .flex_none()
                                        .px_3()
                                        .text_sm()
                                        .text_color(muted)
                                        .truncate()
                                        .child(a.owner.clone()),
                                )
                                .child(
                                    div()
                                        .w(px(140.))
                                        .flex_none()
                                        .px_3()
                                        .text_sm()
                                        .text_color(muted)
                                        .child(time::ago(&a.updated_at)),
                                )
                                // The API opens in Design, as in the web app.
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    this.open_view(View::Design, cx);
                                    this.open_design(api_id.clone(), None, cx);
                                }))
                        }))
                        .into_any_element()
                }
            }
            other => status(other, "Loading APIs…", muted, t.danger).into_any_element(),
        };

        v_flex()
            .id("apis")
            .size_full()
            .px_10()
            .pt_7()
            .pb_10()
            .gap_4()
            .overflow_y_scroll()
            .child(
                page_header("APIs", &subtitle, muted)
                    .child(div().w(px(260.)).child(Input::new(&p.api_search))),
            )
            .child(h_flex().justify_between().child(filters).child(sorts))
            .child(
                v_flex()
                    .rounded_lg()
                    .border_1()
                    .border_color(border)
                    .overflow_hidden()
                    .child(header)
                    .child(body),
            )
            .into_any_element()
    }

    // ----- History -----

    fn load_history(&mut self, cx: &mut Context<Self>) {
        if self.pages.history.ready().is_none() {
            self.pages.history = Load::Loading;
        }
        self.fetch(
            cx,
            |c| c.history(),
            |this, r| this.pages.history = r.map_or_else(Load::Failed, Load::Ready),
        );
    }

    fn open_history(&mut self, h: &HistoryItem, cx: &mut Context<Self>) {
        let id = h.id.clone();
        self.pages.history_open = Some((h.id.clone(), h.method.clone(), h.url.clone()));
        self.pages.history_detail = Load::Loading;
        self.pages.history_request = None;
        self.fetch(
            cx,
            move |c| c.history_item(&id),
            |this, r| match r {
                Ok(d) => {
                    this.pages.history_request = d.request_id;
                    this.pages.history_detail = Load::Ready(SendResult { result: d.result }.into());
                }
                Err(e) => this.pages.history_detail = Load::Failed(e),
            },
        );
        cx.notify();
    }

    pub(super) fn render_history(&mut self, cx: &mut Context<Self>) -> AnyElement {
        let t = cx.theme();
        let (muted, border) = (t.muted_foreground, t.border);
        let p = &self.pages;
        let q = p.history_search.read(cx).value().to_lowercase();
        let has_items = p.history.ready().is_some_and(|h| !h.is_empty());

        let clear: AnyElement = if p.confirm_clear {
            h_flex()
                .gap_2()
                .child(
                    div()
                        .text_sm()
                        .text_color(muted)
                        .child("Delete every request you sent, with stored responses?"),
                )
                .child(
                    Button::new("clear-yes")
                        .danger()
                        .label("Clear")
                        .on_click(cx.listener(|this, _, _, cx| {
                            this.pages.confirm_clear = false;
                            this.pages.history_open = None;
                            this.fetch(
                                cx,
                                |c| c.clear_history(),
                                |this, _| this.pages.history = Load::Ready(vec![]),
                            );
                            cx.notify();
                        })),
                )
                .child(
                    Button::new("clear-no")
                        .ghost()
                        .label("Cancel")
                        .on_click(cx.listener(|this, _, _, cx| {
                            this.pages.confirm_clear = false;
                            cx.notify();
                        })),
                )
                .into_any_element()
        } else {
            Button::new("clear")
                .outline()
                .label("Clear history")
                .disabled(!has_items)
                .on_click(cx.listener(|this, _, _, cx| {
                    this.pages.confirm_clear = true;
                    cx.notify();
                }))
                .into_any_element()
        };

        let list: AnyElement = match &p.history {
            Load::Ready(items) => {
                let shown: Vec<_> = items
                    .iter()
                    .filter(|h| {
                        q.is_empty()
                            || format!("{} {} {}", h.url, h.status, h.method)
                                .to_lowercase()
                                .contains(&q)
                    })
                    .collect();
                if shown.is_empty() {
                    div()
                        .py_10()
                        .text_center()
                        .text_sm()
                        .text_color(muted)
                        .child("Nothing sent yet. Requests you send through your organization's server show up here.")
                        .into_any_element()
                } else {
                    let mut rows: Vec<AnyElement> = vec![];
                    let mut last = String::new();
                    for h in shown {
                        let day = time::day_label(&h.created_at);
                        if day != last {
                            rows.push(
                                div()
                                    .px_4()
                                    .py_2()
                                    .bg(t.muted)
                                    .text_sm()
                                    .font_weight(FontWeight::SEMIBOLD)
                                    .when(!rows.is_empty(), |d| d.border_t_1().border_color(border))
                                    .child(day.clone())
                                    .into_any_element(),
                            );
                            last = day;
                        }
                        let item = h.clone();
                        let open = p
                            .history_open
                            .as_ref()
                            .is_some_and(|(id, _, _)| *id == h.id);
                        rows.push(
                            h_flex()
                                .id(SharedString::from(format!("h-{}", h.id)))
                                .min_h(px(46.))
                                .px_4()
                                .gap_3()
                                .border_t_1()
                                .border_color(border)
                                .when(open, |d| d.bg(t.muted))
                                .hover(|d| d.bg(t.muted))
                                .child(
                                    div()
                                        .w(px(48.))
                                        .flex_none()
                                        .text_sm()
                                        .text_color(muted)
                                        .child(time::clock(&h.created_at, false)),
                                )
                                .child(method_badge(&h.method, 48.))
                                .child(
                                    div()
                                        .flex_1()
                                        .min_w_0()
                                        .truncate()
                                        .font_family(MONO)
                                        .text_sm()
                                        .child(h.url.clone()),
                                )
                                .child(div().w(px(110.)).flex_none().child(if h.status > 0 {
                                    status_tag(h.status, "").into_any_element()
                                } else {
                                    div()
                                        .text_sm()
                                        .text_color(t.danger)
                                        .truncate()
                                        .child(h.error.clone())
                                        .into_any_element()
                                }))
                                .child(
                                    div()
                                        .w(px(80.))
                                        .flex_none()
                                        .text_right()
                                        .text_sm()
                                        .child(format!("{} ms", h.duration_ms)),
                                )
                                .child(
                                    div()
                                        .w(px(110.))
                                        .flex_none()
                                        .text_right()
                                        .text_sm()
                                        .text_color(muted)
                                        .truncate()
                                        .child(h.env_name.clone()),
                                )
                                .on_click(
                                    cx.listener(move |this, _, _, cx| this.open_history(&item, cx)),
                                )
                                .into_any_element(),
                        );
                    }
                    v_flex()
                        .rounded_lg()
                        .border_1()
                        .border_color(border)
                        .overflow_hidden()
                        .children(rows)
                        .into_any_element()
                }
            }
            other => status(other, "Loading history…", muted, t.danger).into_any_element(),
        };

        let main = v_flex()
            .id("history")
            .flex_1()
            .h_full()
            .min_w_0()
            .px_8()
            .pt_7()
            .pb_8()
            .gap_4()
            .overflow_y_scroll()
            .child(
                page_header(
                    "History",
                    "Requests you sent from this workspace. Secrets are redacted before they're stored.",
                    muted,
                )
                .child(clear),
            )
            .child(div().w(px(320.)).child(Input::new(&p.history_search)))
            .child(list);

        // The response of the selected entry, in a panel on the right.
        let detail = p.history_open.as_ref().map(|(_, method, url)| {
            let request = p.history_request.clone();
            v_flex()
                .w(px(560.))
                .h_full()
                .flex_none()
                .border_l_1()
                .border_color(border)
                .bg(t.background)
                .child(
                    h_flex()
                        .h(px(56.))
                        .flex_none()
                        .px_6()
                        .gap_3()
                        .border_b_1()
                        .border_color(border)
                        .child(method_badge(method, 0.))
                        .child(
                            div()
                                .flex_1()
                                .min_w_0()
                                .truncate()
                                .font_family(MONO)
                                .text_sm()
                                .child(url.clone()),
                        )
                        .children(request.map(|id| {
                            Button::new("open-request")
                                .outline()
                                .small()
                                .label("Open request")
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    this.open_team(id.clone(), "Requests".into(), String::new(), cx)
                                }))
                        }))
                        .child(
                            Button::new("close-detail")
                                .ghost()
                                .small()
                                .label("✕")
                                .on_click(cx.listener(|this, _, _, cx| {
                                    this.pages.history_open = None;
                                    cx.notify();
                                })),
                        ),
                )
                .child(div().flex_1().min_h_0().child(self.render_response(
                    &p.history_detail,
                    p.history_tab,
                    |this, tab| this.pages.history_tab = tab,
                    cx,
                )))
        });

        h_flex()
            .size_full()
            .child(main)
            .children(detail)
            .into_any_element()
    }

    // ----- Environments -----

    fn select_env(&mut self, id: String, cx: &mut Context<Self>) {
        self.pages.env_selected = Some(id.clone());
        self.pages.env_vars = Load::Loading;
        self.pages.env_rows = None;
        self.pages.env_notice = None;
        self.fetch(
            cx,
            move |c| c.variables(&id).map(|v| (id, v)),
            |this, r| match r {
                // Ignore an answer for an environment the person has since left.
                Ok((id, vars)) if this.pages.env_selected.as_deref() == Some(id.as_str()) => {
                    this.pages.env_vars = Load::Ready(vars);
                    this.pages.env_rows = None;
                }
                Ok(_) => {}
                Err(e) => this.pages.env_vars = Load::Failed(e),
            },
        );
        cx.notify();
    }

    fn save_env(&mut self, cx: &mut Context<Self>) {
        let (Some(id), Some(rows)) = (self.pages.env_selected.clone(), &self.pages.env_rows) else {
            return;
        };
        let body = payload(rows, cx);
        self.pages.env_saving = true;
        self.pages.env_notice = None;
        self.fetch(
            cx,
            move |c| c.save_variables(&id, &body),
            |this, r| {
                this.pages.env_saving = false;
                match r {
                    Ok(vars) => {
                        this.pages.env_vars = Load::Ready(vars);
                        this.pages.env_rows = None; // rebuilt from what the server stored
                    }
                    Err(e) => this.pages.env_notice = Some(format!("Couldn't save: {e}")),
                }
            },
        );
        // The counts in the list and the header's environment picker.
        self.fetch(
            cx,
            |c| c.environments(),
            |this, r| {
                if let Ok(e) = r {
                    this.envs = Load::Ready(e);
                }
            },
        );
        cx.notify();
    }

    fn create_env(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let name = self.pages.new_env.read(cx).value().trim().to_string();
        if name.is_empty() {
            return;
        }
        self.pages
            .new_env
            .update(cx, |s, cx| s.set_value("", window, cx));
        self.fetch(
            cx,
            move |c| {
                let id = c.create_environment(&name)?;
                Ok((id, c.environments()?))
            },
            |this, r| match r {
                Ok((id, envs)) => {
                    this.envs = Load::Ready(envs);
                    this.pages.env_selected = Some(id);
                    this.pages.env_vars = Load::Ready(vec![]);
                    this.pages.env_rows = None;
                }
                Err(e) => {
                    this.pages.env_notice = Some(format!("Couldn't create the environment: {e}"))
                }
            },
        );
    }

    pub(super) fn render_environments(&mut self, cx: &mut Context<Self>) -> AnyElement {
        let t = cx.theme();
        let (muted, border) = (t.muted_foreground, t.border);
        let primary: Hsla = rgb(PRIMARY).into();
        let p = &self.pages;
        let envs = self
            .envs
            .ready()
            .map(|e| e.items.clone())
            .unwrap_or_default();
        let selected = envs.iter().find(|e| Some(&e.id) == p.env_selected.as_ref());

        let list = v_flex()
            .w(px(240.))
            .flex_none()
            .p_1()
            .rounded_lg()
            .border_1()
            .border_color(border)
            .children(envs.iter().map(|e| {
                let active = Some(&e.id) == p.env_selected.as_ref();
                let id = e.id.clone();
                h_flex()
                    .id(SharedString::from(format!("env-row-{}", e.id)))
                    .h(px(36.))
                    .px_3()
                    .gap_2p5()
                    .rounded_md()
                    .text_sm()
                    .when(active, |d| {
                        d.bg(primary.opacity(0.1))
                            .text_color(primary)
                            .font_weight(FontWeight::MEDIUM)
                    })
                    .hover(|d| d.bg(t.muted))
                    .when(!e.is_global, |d| {
                        d.child(dot(parse_hex(&e.color).unwrap_or(muted), 6.))
                    })
                    .child(div().flex_1().truncate().child(e.name.clone()))
                    .child(div().text_xs().text_color(muted).child(e.count.to_string()))
                    .on_click(cx.listener(move |this, _, _, cx| this.select_env(id.clone(), cx)))
            }));

        let table: AnyElement = match (&p.env_rows, &p.env_vars, selected) {
            (Some(rows), Load::Ready(vars), Some(env)) => {
                let dirty = payload(rows, cx) != baseline(vars);
                let last = rows.len().saturating_sub(1);
                let head = h_flex()
                    .h(px(40.))
                    .bg(t.muted)
                    .text_sm()
                    .font_weight(FontWeight::SEMIBOLD)
                    .text_color(muted)
                    .child(div().w(px(44.)).flex_none())
                    .child(div().w(px(220.)).flex_none().px_3().child("Variable"))
                    .child(div().flex_1().px_3().child("Value"))
                    .child(div().w(px(140.)).flex_none().px_3().child("Type"))
                    .child(div().w(px(40.)).flex_none());
                let body =
                    rows.iter().enumerate().map(|(i, r)| {
                        let blank = i == last && r.key.read(cx).value().is_empty();
                        let kind_label = match r.kind.as_str() {
                            "secret" => "secret",
                            "script" => "set by script",
                            _ => "default",
                        };
                        h_flex()
                            .h(px(44.))
                            .border_t_1()
                            .border_color(border)
                            .child(h_flex().w(px(44.)).flex_none().justify_center().when(
                                !blank,
                                |d| {
                                    d.child(
                                        Checkbox::new(SharedString::from(format!("var-on-{i}")))
                                            .checked(r.enabled)
                                            .on_click(cx.listener(
                                                move |this, on: &bool, _, cx| {
                                                    if let Some(r) = this
                                                        .pages
                                                        .env_rows
                                                        .as_mut()
                                                        .and_then(|rows| rows.get_mut(i))
                                                    {
                                                        r.enabled = *on;
                                                    }
                                                    cx.notify();
                                                },
                                            )),
                                    )
                                },
                            ))
                            .child(
                                div()
                                    .w(px(220.))
                                    .flex_none()
                                    .font_family(MONO)
                                    .child(Input::new(&r.key).appearance(false)),
                            )
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .font_family(MONO)
                                    .child(Input::new(&r.value).appearance(false)),
                            )
                            .child(
                                // Click to change the type: default → secret → set by script.
                                h_flex()
                                    .id(SharedString::from(format!("var-kind-{i}")))
                                    .w(px(140.))
                                    .flex_none()
                                    .px_3()
                                    .gap_1()
                                    .text_sm()
                                    .hover(|d| d.text_color(primary))
                                    .child(kind_label)
                                    .child(div().text_xs().text_color(muted).child("▾"))
                                    .on_click(cx.listener(move |this, _, window, cx| {
                                        if let Some(r) = this
                                            .pages
                                            .env_rows
                                            .as_mut()
                                            .and_then(|rows| rows.get_mut(i))
                                        {
                                            r.kind = match r.kind.as_str() {
                                                "default" => "secret",
                                                "secret" => "script",
                                                _ => "default",
                                            }
                                            .to_string();
                                            r.keep = false;
                                            let secret = r.kind == "secret";
                                            r.value.update(cx, |s, cx| {
                                                s.set_masked(secret, window, cx)
                                            });
                                        }
                                        cx.notify();
                                    })),
                            )
                            .child(h_flex().w(px(40.)).flex_none().justify_center().when(
                                !blank,
                                |d| {
                                    d.child(
                                        div()
                                            .id(SharedString::from(format!("var-del-{i}")))
                                            .px_2()
                                            .text_xs()
                                            .text_color(muted)
                                            .hover(|d| d.text_color(t.danger))
                                            .child("✕")
                                            .on_click(cx.listener(move |this, _, _, cx| {
                                                if let Some(rows) = this.pages.env_rows.as_mut()
                                                    && i < rows.len()
                                                {
                                                    rows.remove(i);
                                                }
                                                cx.notify();
                                            })),
                                    )
                                },
                            ))
                    });
                let is_active = Some(&env.id) == self.active_env.as_ref();
                v_flex()
                    .flex_1()
                    .min_w_0()
                    .gap_3()
                    .child(
                        h_flex()
                            .gap_2()
                            .child(div().flex_1().font_weight(FontWeight::SEMIBOLD).child(env.name.clone()))
                            .when(is_active, |d| d.child(pill("Active", Hsla::from(rgb(0x52C41A)).opacity(0.15), rgb(0x389E0D).into())))
                            .when(env.is_global, |d| d.child(pill("Applies to every environment", t.muted, muted)))
                            .child(
                                Button::new("save-env")
                                    .primary()
                                    .label("Save")
                                    .disabled(!dirty)
                                    .loading(p.env_saving)
                                    .on_click(cx.listener(|this, _, _, cx| this.save_env(cx))),
                            ),
                    )
                    .child(v_flex().rounded_lg().border_1().border_color(border).overflow_hidden().child(head).children(body))
                    .child(
                        div().p_3().rounded_md().bg(rgb(0xFAAD14).opacity(0.12)).text_sm().child(
                            "Secret values are encrypted on your organization's server and never sent back to this app, written to history, or included in shared links.",
                        ),
                    )
                    .into_any_element()
            }
            (_, Load::Failed(e), _) => note(e.clone(), t.danger).into_any_element(),
            _ if envs.is_empty() && self.envs.ready().is_some() => {
                note("No environments yet. Create one above.", muted).into_any_element()
            }
            _ => note("Loading variables…", muted).into_any_element(),
        };

        v_flex()
            .id("environments")
            .size_full()
            .px_8()
            .pt_7()
            .pb_8()
            .gap_4()
            .overflow_y_scroll()
            .child(
                page_header(
                    "Environments",
                    "Variables resolve in URLs, headers, bodies and scripts as {{name}}.",
                    muted,
                )
                .child(div().w(px(240.)).child(Input::new(&p.new_env)))
                .child(
                    Button::new("new-env")
                        .primary()
                        .label("New environment")
                        .on_click(cx.listener(|this, _, window, cx| this.create_env(window, cx))),
                ),
            )
            .children(
                p.env_notice
                    .clone()
                    .map(|n| div().text_sm().text_color(t.danger).child(n)),
            )
            .child(h_flex().items_start().gap_4().child(list).child(table))
            .into_any_element()
    }

    // ----- Mock servers -----

    fn load_mocks(&mut self, cx: &mut Context<Self>) {
        self.fetch(
            cx,
            |c| c.mocks(),
            |this, r| this.pages.mocks = r.map_or_else(Load::Failed, Load::Ready),
        );
        self.fetch(
            cx,
            |c| c.mock_calls(),
            |this, r| this.pages.mock_calls = r.map_or_else(Load::Failed, Load::Ready),
        );
    }

    fn toggle_mock(&mut self, id: String, enabled: bool, cx: &mut Context<Self>) {
        // Show the change straight away; reload to confirm what the server did.
        if let Load::Ready(mocks) = &mut self.pages.mocks
            && let Some(m) = mocks.iter_mut().find(|m| m.id == id)
        {
            m.enabled = enabled;
        }
        self.fetch(
            cx,
            move |c| {
                c.set_mock_enabled(&id, enabled)?;
                c.mocks()
            },
            |this, r| this.pages.mocks = r.map_or_else(Load::Failed, Load::Ready),
        );
        cx.notify();
    }

    pub(super) fn render_mocks(&mut self, cx: &mut Context<Self>) -> AnyElement {
        let t = cx.theme();
        let (muted, border) = (t.muted_foreground, t.border);
        let p = &self.pages;

        let cards: AnyElement = match &p.mocks {
            Load::Ready(m) if m.is_empty() => div()
                .py_10()
                .text_center()
                .text_sm()
                .text_color(muted)
                .child("No mock servers yet")
                .into_any_element(),
            Load::Ready(mocks) => h_flex()
                .flex_wrap()
                .items_start()
                .gap_4()
                .children(mocks.iter().map(|m| {
                    let id = m.id.clone();
                    let faded = if m.enabled { t.foreground } else { muted };
                    let stat = |label: &'static str, value: String| {
                        v_flex()
                            .flex_1()
                            .child(div().text_xs().text_color(muted).child(label))
                            .child(
                                div()
                                    .text_lg()
                                    .font_weight(FontWeight::SEMIBOLD)
                                    .text_color(faded)
                                    .child(value),
                            )
                    };
                    v_flex()
                        .w(px(360.))
                        .p_5()
                        .gap_3p5()
                        .rounded_lg()
                        .border_1()
                        .border_color(border)
                        .child(
                            h_flex()
                                .items_start()
                                .gap_2()
                                .child(
                                    v_flex()
                                        .flex_1()
                                        .min_w_0()
                                        .child(
                                            div()
                                                .font_weight(FontWeight::SEMIBOLD)
                                                .truncate()
                                                .child(m.name.clone()),
                                        )
                                        .child(
                                            div()
                                                .text_sm()
                                                .text_color(muted)
                                                .child(format!("{} {}", m.api_name, m.api_version)),
                                        ),
                                )
                                .child(
                                    Switch::new(SharedString::from(format!("mock-{}", m.id)))
                                        .checked(m.enabled)
                                        .on_click(cx.listener(move |this, on: &bool, _, cx| {
                                            this.toggle_mock(id.clone(), *on, cx)
                                        })),
                                ),
                        )
                        .child({
                            let url = m.url.clone();
                            h_flex()
                                .px_2p5()
                                .py_1p5()
                                .gap_2()
                                .rounded_md()
                                .border_1()
                                .border_color(border)
                                .bg(t.muted)
                                .child(
                                    div()
                                        .flex_1()
                                        .min_w_0()
                                        .truncate()
                                        .text_xs()
                                        .font_family(MONO)
                                        .text_color(faded)
                                        .child(
                                            m.url
                                                .trim_start_matches("https://")
                                                .trim_start_matches("http://")
                                                .to_string(),
                                        ),
                                )
                                .child(
                                    Button::new(SharedString::from(format!("copy-{}", m.id)))
                                        .ghost()
                                        .xsmall()
                                        .label("Copy")
                                        .on_click(move |_, _, cx| {
                                            cx.write_to_clipboard(ClipboardItem::new_string(
                                                url.clone(),
                                            ))
                                        }),
                                )
                        })
                        .child(
                            h_flex()
                                .child(stat("Calls today", m.calls_today.to_string()))
                                .child(stat(
                                    "Added latency",
                                    if m.latency_ms > 0 {
                                        format!("{} ms", m.latency_ms)
                                    } else {
                                        "—".into()
                                    },
                                ))
                                .child(stat("Error injection", format!("{}%", m.error_rate))),
                        )
                        .child(
                            h_flex()
                                .gap_2()
                                .child(dot(
                                    if m.enabled {
                                        rgb(0x52C41A).into()
                                    } else {
                                        rgb(0xBFBFBF).into()
                                    },
                                    6.,
                                ))
                                .child(div().text_sm().text_color(muted).child(if m.enabled {
                                    "Running"
                                } else {
                                    "Stopped"
                                })),
                        )
                }))
                .into_any_element(),
            other => status(other, "Loading mock servers…", muted, t.danger).into_any_element(),
        };

        let calls: AnyElement = match &p.mock_calls {
            Load::Ready(c) if c.is_empty() => div()
                .py_8()
                .text_center()
                .text_sm()
                .text_color(muted)
                .child("No calls yet. Send a request to a mock URL.")
                .into_any_element(),
            Load::Ready(calls) => v_flex()
                .children(calls.iter().take(50).enumerate().map(|(i, c)| {
                    h_flex()
                        .h(px(44.))
                        .px_6()
                        .gap_3()
                        .when(i > 0, |d| d.border_t_1().border_color(border))
                        .child(
                            div()
                                .w(px(84.))
                                .flex_none()
                                .text_xs()
                                .font_family(MONO)
                                .text_color(muted)
                                .child(time::clock(&c.created_at, true)),
                        )
                        .child(method_badge(&c.method, 48.))
                        .child(
                            div()
                                .flex_1()
                                .min_w_0()
                                .truncate()
                                .text_sm()
                                .font_family(MONO)
                                .child(c.path.clone()),
                        )
                        .child(status_tag(c.status, ""))
                        .child(
                            div()
                                .w(px(180.))
                                .flex_none()
                                .text_right()
                                .text_sm()
                                .text_color(muted)
                                .truncate()
                                .child(c.mock_name.clone()),
                        )
                }))
                .into_any_element(),
            other => status(other, "Loading calls…", muted, t.danger).into_any_element(),
        };

        v_flex()
            .id("mocks")
            .size_full()
            .px_8()
            .pt_7()
            .pb_8()
            .gap_5()
            .overflow_y_scroll()
            .child(page_header(
                "Mock servers",
                "Serve example responses from your specs. Send Prefer: code=402 to get a specific documented response.",
                muted,
            ))
            .child(cards)
            .child(
                v_flex()
                    .rounded_lg()
                    .border_1()
                    .border_color(border)
                    .child(
                        h_flex()
                            .h(px(56.))
                            .px_6()
                            .justify_between()
                            .border_b_1()
                            .border_color(border)
                            .child(div().font_weight(FontWeight::SEMIBOLD).child("Recent mock calls"))
                            .child(Button::new("refresh-calls").outline().small().label("Refresh").on_click(cx.listener(
                                |this, _, _, cx| this.load_mocks(cx),
                            ))),
                    )
                    .child(calls),
            )
            .into_any_element()
    }
}

/// Title and subtitle on the left; add the page's buttons with `.child()`.
fn page_header(title: &str, subtitle: &str, muted: Hsla) -> Div {
    h_flex().items_end().gap_3().child(
        v_flex()
            .flex_1()
            .min_w_0()
            .child(
                div()
                    .text_2xl()
                    .font_weight(FontWeight::SEMIBOLD)
                    .child(title.to_string()),
            )
            .child(
                div()
                    .text_sm()
                    .text_color(muted)
                    .child(subtitle.to_string()),
            ),
    )
}
