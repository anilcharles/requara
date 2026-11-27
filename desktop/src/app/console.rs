//! The Console: a bar at the bottom of the window that opens a log of every
//! request sent in this session, with script output and errors, like the web
//! app's. Kept in memory only; it clears when the app quits.

use std::collections::HashSet;

use super::*;

const MAX_ENTRIES: usize = 500;
/// Bodies longer than this are cut in the console (the response pane has the rest).
const MAX_BODY: usize = 6000;

#[derive(Clone, Copy, PartialEq, Eq)]
enum Filter {
    All,
    Network,
    Logs,
    Errors,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Detail {
    Request,
    Response,
    Timing,
    Tests,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Kind {
    Log,
    Warn,
    Error,
}

/// What came back, copied from the response pane's data.
struct Sent {
    status: i64,
    status_text: String,
    duration_ms: i64,
    size_bytes: i64,
    headers: Vec<(String, String)>,
    body: String,
    error: Option<String>,
    tests: Vec<api::TestResult>,
    timeline: Vec<api::Phase>,
    logs: Vec<String>,
    script_error: Option<String>,
}

struct Entry {
    id: u64,
    /// "14:03:22.118"
    at: String,
    name: String,
    env: String,
    method: String,
    url: String,
    request_headers: Vec<(String, String)>,
    request_body: String,
    /// None when the request never left (bad URL, no connection, server refused it).
    sent: Option<Sent>,
    error: Option<String>,
}

impl Entry {
    /// Worth a badge on the bar: failed sends, script errors, failed tests.
    fn has_problem(&self) -> bool {
        match &self.sent {
            None => true,
            Some(s) => {
                s.status == 0
                    || s.status >= 400
                    || s.error.is_some()
                    || s.script_error.is_some()
                    || s.tests.iter().any(|t| !t.pass)
            }
        }
    }

    /// Script logs, then problems, in the order things happened. The request row follows.
    fn messages(&self) -> Vec<(Kind, String)> {
        let mut out = vec![];
        if let Some(s) = &self.sent {
            out.extend(s.logs.iter().map(|l| (Kind::Log, l.clone())));
            out.extend(s.script_error.iter().map(|e| (Kind::Warn, e.clone())));
            out.extend(s.tests.iter().filter(|t| !t.pass).map(|t| {
                let why = t
                    .error
                    .as_deref()
                    .filter(|e| !e.is_empty())
                    .map(|e| format!(": {e}"))
                    .unwrap_or_default();
                (Kind::Error, format!("Test failed: {}{why}", t.name))
            }));
            out.extend(s.error.iter().map(|e| (Kind::Error, e.clone())));
        }
        out.extend(self.error.iter().map(|e| (Kind::Error, e.clone())));
        out
    }
}

pub(super) struct Console {
    entries: Vec<Entry>,
    next_id: u64,
    pub(super) open: bool,
    height: f32,
    pub(super) dragging: bool,
    filter: Filter,
    search: Entity<InputState>,
    expanded: HashSet<u64>,
    detail: Detail,
    /// Problems already seen by opening the console.
    seen: usize,
    scroll: ScrollHandle,
}

impl Console {
    pub(super) fn new(
        subs: &mut Vec<Subscription>,
        window: &mut Window,
        cx: &mut Context<RequaraApp>,
    ) -> Self {
        Console {
            entries: vec![],
            next_id: 1,
            open: false,
            height: 280.,
            dragging: false,
            filter: Filter::All,
            search: text_input("Search URLs, status codes and logs", "", subs, window, cx),
            expanded: HashSet::new(),
            detail: Detail::Request,
            seen: 0,
            scroll: ScrollHandle::new(),
        }
    }

    fn problems(&self) -> usize {
        self.entries.iter().filter(|e| e.has_problem()).count()
    }

    pub(super) fn toggle(&mut self) {
        self.open = !self.open;
        if self.open {
            self.seen = self.problems();
            self.scroll.scroll_to_bottom();
        }
    }

    /// Follows the pointer while the top edge is dragged.
    pub(super) fn drag_to(&mut self, pointer_y: f32, window_height: f32) {
        self.height = (window_height - pointer_y - 28.).clamp(120., window_height * 0.75);
    }

    /// Records a finished send.
    pub(super) fn log(&mut self, doc: &RequestDoc, env: String, result: &Load<Resp>) {
        let (sent, error) = match result {
            Load::Ready(r) => (
                Some(Sent {
                    status: r.status,
                    status_text: r.status_text.clone(),
                    duration_ms: r.duration_ms,
                    size_bytes: r.size_bytes,
                    headers: r.headers.clone(),
                    body: clip(&r.body),
                    error: r.error.clone(),
                    tests: r.tests.clone(),
                    timeline: r.timeline.clone(),
                    logs: r.logs.clone(),
                    script_error: r.script_error.clone(),
                }),
                None,
            ),
            Load::Failed(e) => (None, Some(e.clone())),
            _ => return,
        };
        self.entries.push(Entry {
            id: self.next_id,
            at: jiff::ZonedDateTime::now()
                .strftime("%H:%M:%S%.3f")
                .to_string(),
            name: doc.name.clone(),
            env,
            method: doc.method.clone(),
            url: doc.url.clone(),
            request_headers: doc
                .headers
                .iter()
                .filter(|h| h.enabled && !h.key.is_empty())
                .map(|h| (h.key.clone(), h.value.clone()))
                .collect(),
            request_body: if doc.body_mode == "none" {
                String::new()
            } else {
                clip(&doc.body)
            },
            sent,
            error,
        });
        #[cfg(feature = "screenshot")]
        if std::env::var("REQUARA_SHOT_CONSOLE").as_deref() == Ok("expand") {
            self.expanded.insert(self.next_id);
        }
        self.next_id += 1;
        if self.entries.len() > MAX_ENTRIES {
            let extra = self.entries.len() - MAX_ENTRIES;
            self.entries.drain(..extra);
        }
        if self.open {
            self.seen = self.problems();
        }
        self.scroll.scroll_to_bottom();
    }
}

fn clip(body: &str) -> String {
    if body.len() <= MAX_BODY {
        return body.to_string();
    }
    let mut end = MAX_BODY;
    while !body.is_char_boundary(end) {
        end -= 1;
    }
    format!(
        "{}\n… cut here; the response pane has the full body",
        &body[..end]
    )
}

impl RequaraApp {
    pub(super) fn render_console(&mut self, cx: &mut Context<Self>) -> AnyElement {
        let t = cx.theme();
        let (muted, border, danger) = (t.muted_foreground, t.border, t.danger);
        let primary: Hsla = rgb(PRIMARY).into();
        let c = &self.console;
        let unseen = c.problems().saturating_sub(c.seen);

        let bar = h_flex()
            .h(px(28.))
            .flex_none()
            .px_3()
            .gap_2()
            .when(c.open, |el| el.border_t_1().border_color(border))
            .child(
                h_flex()
                    .id("console-toggle")
                    .gap_1p5()
                    .text_xs()
                    .text_color(if c.open { primary } else { muted })
                    .hover(|el| el.text_color(primary))
                    .child(div().font_family(MONO).child(">_"))
                    .child("Console")
                    .when(unseen > 0 && !c.open, |el| {
                        el.child(
                            div()
                                .px_1p5()
                                .rounded_full()
                                .bg(rgb(0xFF4D4F))
                                .text_color(white())
                                .text_size(px(10.))
                                .child(unseen.to_string()),
                        )
                    })
                    .on_click(cx.listener(|this, _, _, cx| {
                        this.console.toggle();
                        cx.notify();
                    })),
            )
            .child(div().flex_1())
            .child(div().text_size(px(11.)).text_color(muted).child(
                if cfg!(target_os = "macos") {
                    "⌥⌘C"
                } else {
                    "Ctrl+Alt+C"
                },
            ));

        if !c.open {
            return v_flex()
                .flex_none()
                .border_t_1()
                .border_color(border)
                .bg(t.background)
                .child(bar)
                .into_any_element();
        }

        let filter_button = |id: &'static str, label: &'static str, f: Filter| {
            let b = Button::new(id).xsmall().label(label).on_click(cx.listener(
                move |this, _, _, cx| {
                    this.console.filter = f;
                    cx.notify();
                },
            ));
            if c.filter == f {
                b.outline()
            } else {
                b.ghost()
            }
        };
        let toolbar = h_flex()
            .flex_none()
            .px_3()
            .pb_1p5()
            .gap_2()
            .child(
                div()
                    .mr_2()
                    .text_sm()
                    .font_weight(FontWeight::SEMIBOLD)
                    .child("Console"),
            )
            .child(div().w(px(280.)).child(Input::new(&c.search).small()))
            .child(filter_button("cf-all", "All", Filter::All))
            .child(filter_button("cf-net", "Network", Filter::Network))
            .child(filter_button("cf-logs", "Logs", Filter::Logs))
            .child(filter_button("cf-err", "Errors", Filter::Errors))
            .child(div().flex_1())
            .child(div().text_xs().text_color(muted).child(format!(
                "{} {} this session",
                c.entries.len(),
                if c.entries.len() == 1 {
                    "request"
                } else {
                    "requests"
                }
            )))
            .child(
                Button::new("console-clear")
                    .ghost()
                    .xsmall()
                    .label("Clear")
                    .on_click(cx.listener(|this, _, _, cx| {
                        this.console.entries.clear();
                        this.console.expanded.clear();
                        this.console.seen = 0;
                        cx.notify();
                    })),
            )
            .child(
                Button::new("console-close")
                    .ghost()
                    .xsmall()
                    .label("✕")
                    .on_click(cx.listener(|this, _, _, cx| {
                        this.console.open = false;
                        cx.notify();
                    })),
            );

        let q = c.search.read(cx).value().trim().to_lowercase();
        let time = |at: &str| {
            div()
                .flex_none()
                .text_color(muted.opacity(0.7))
                .child(at.to_string())
        };
        let mut rows: Vec<AnyElement> = vec![];
        for e in &c.entries {
            for (i, (kind, text)) in e.messages().into_iter().enumerate() {
                let show = match c.filter {
                    Filter::All | Filter::Logs => true,
                    Filter::Network => false,
                    Filter::Errors => kind != Kind::Log,
                };
                if !show || (!q.is_empty() && !text.to_lowercase().contains(&q)) {
                    continue;
                }
                let (color, bg): (Hsla, Hsla) = match kind {
                    Kind::Log => (t.foreground, transparent_black()),
                    Kind::Warn => (rgb(0xD48806).into(), Hsla::from(rgb(0xFAAD14)).opacity(0.1)),
                    Kind::Error => (danger, danger.opacity(0.1)),
                };
                rows.push(
                    h_flex()
                        .id(SharedString::from(format!("cl-{}-{i}", e.id)))
                        .items_start()
                        .px_3()
                        .py_1()
                        .gap_3()
                        .border_b_1()
                        .border_color(border)
                        .bg(bg)
                        .child(time(&e.at))
                        .child(div().w(px(14.)).flex_none())
                        .child(div().flex_1().min_w_0().text_color(color).child(text))
                        .into_any_element(),
                );
            }
            let show = match c.filter {
                Filter::All | Filter::Network => true,
                Filter::Logs => false,
                Filter::Errors => e.has_problem(),
            };
            let hay = format!(
                "{} {} {} {}",
                e.method,
                e.url,
                e.sent.as_ref().map_or(0, |s| s.status),
                e.name
            )
            .to_lowercase();
            if !show || (!q.is_empty() && !hay.contains(&q)) {
                continue;
            }
            let open = c.expanded.contains(&e.id);
            let id = e.id;
            rows.push(
                v_flex()
                    .border_b_1()
                    .border_color(border)
                    .child(
                        h_flex()
                            .id(SharedString::from(format!("cn-{id}")))
                            .px_3()
                            .py_1()
                            .gap_3()
                            .hover(|el| el.bg(t.list_hover))
                            .child(time(&e.at))
                            .child(
                                div()
                                    .w(px(14.))
                                    .flex_none()
                                    .text_size(px(9.))
                                    .text_color(muted)
                                    .child(if open { "▼" } else { "▶" }),
                            )
                            .child(method_badge(&e.method, 34.))
                            .child(div().flex_1().min_w_0().truncate().child(e.url.clone()))
                            .child(match &e.sent {
                                Some(s) => status_tag(s.status, &s.status_text).into_any_element(),
                                None => status_tag(0, "").into_any_element(),
                            })
                            .child(
                                div()
                                    .w(px(64.))
                                    .flex_none()
                                    .text_right()
                                    .text_color(muted)
                                    .child(e.sent.as_ref().map_or("—".to_string(), |s| {
                                        format!("{} ms", s.duration_ms)
                                    })),
                            )
                            .child(
                                div()
                                    .w(px(72.))
                                    .flex_none()
                                    .text_right()
                                    .text_color(muted)
                                    .child(
                                        e.sent
                                            .as_ref()
                                            .map(|s| bytes(s.size_bytes))
                                            .unwrap_or_default(),
                                    ),
                            )
                            .on_click(cx.listener(move |this, _, _, cx| {
                                if !this.console.expanded.remove(&id) {
                                    this.console.expanded.insert(id);
                                }
                                cx.notify();
                            })),
                    )
                    .when(open, |el| el.child(self.render_console_details(e, cx)))
                    .into_any_element(),
            );
        }
        let empty = rows.is_empty();
        let list = v_flex()
            .id("console-list")
            .flex_1()
            .min_h_0()
            .overflow_y_scroll()
            .track_scroll(&c.scroll)
            .border_t_1()
            .border_color(border)
            .font_family(MONO)
            .text_xs()
            .children(rows)
            .when(empty, |el| {
                el.child(
                    div()
                        .py_8()
                        .w_full()
                        .text_center()
                        .font_family(t.font_family.clone())
                        .text_sm()
                        .text_color(muted)
                        .child(if c.entries.is_empty() {
                            "Send a request to see its details, script logs and errors here."
                        } else {
                            "Nothing matches the filter."
                        }),
                )
            });

        v_flex()
            .flex_none()
            .border_t_1()
            .border_color(border)
            .bg(t.background)
            .child(
                v_flex()
                    .h(px(c.height))
                    .child(
                        div()
                            .id("console-resize")
                            .h(px(6.))
                            .flex_none()
                            .cursor_row_resize()
                            .on_mouse_down(
                                MouseButton::Left,
                                cx.listener(|this, _, _, cx| {
                                    this.console.dragging = true;
                                    cx.notify();
                                }),
                            ),
                    )
                    .child(toolbar)
                    .child(list),
            )
            .child(bar)
            .into_any_element()
    }

    fn render_console_details(&self, e: &Entry, cx: &Context<Self>) -> AnyElement {
        let t = cx.theme();
        let (muted, danger) = (t.muted_foreground, t.danger);
        let wrap = || v_flex().pl(px(52.)).pr_3().pb_3().bg(t.muted.opacity(0.5));
        let Some(s) = &e.sent else {
            return wrap()
                .pt_2()
                .text_color(danger)
                .child(e.error.clone().unwrap_or_default())
                .into_any_element();
        };
        let section = |title: String| {
            div()
                .mt_2()
                .mb_1()
                .font_family(t.font_family.clone())
                .font_weight(FontWeight::SEMIBOLD)
                .text_color(muted)
                .child(title)
        };
        let none = |text: &'static str| div().text_color(muted).child(text);
        let headers = |list: &[(String, String)]| {
            if list.is_empty() {
                return none("None").into_any_element();
            }
            v_flex()
                .children(list.iter().map(|(k, v)| {
                    h_flex()
                        .items_start()
                        .gap_4()
                        .child(
                            div()
                                .w(px(200.))
                                .flex_none()
                                .text_color(muted)
                                .child(k.clone()),
                        )
                        .child(div().flex_1().min_w_0().child(v.clone()))
                }))
                .into_any_element()
        };
        let body = |id: &'static str, text: &str| {
            if text.is_empty() {
                return none("No body").into_any_element();
            }
            div()
                .id(SharedString::from(format!("{id}-{}", e.id)))
                .max_h(px(220.))
                .overflow_y_scroll()
                .p_2()
                .rounded_md()
                .bg(t.muted)
                .child(text.to_string())
                .into_any_element()
        };
        let detail = if self.console.detail == Detail::Tests && s.tests.is_empty() {
            Detail::Request
        } else {
            self.console.detail
        };
        let tab = |id: &'static str, label: String, d: Detail| {
            let on = detail == d;
            div()
                .id(SharedString::from(format!("{id}-{}", e.id)))
                .pb_1()
                .font_family(t.font_family.clone())
                .text_color(if on { rgb(PRIMARY).into() } else { muted })
                .when(on, |el| el.border_b_2().border_color(rgb(PRIMARY)))
                .child(label)
                .on_click(cx.listener(move |this, _, _, cx| {
                    this.console.detail = d;
                    cx.notify();
                }))
        };
        let passed = s.tests.iter().filter(|t| t.pass).count();
        let tabs = h_flex()
            .pt_2()
            .gap_5()
            .child(tab("cd-req", "Request".into(), Detail::Request))
            .child(tab("cd-res", "Response".into(), Detail::Response))
            .child(tab("cd-time", "Timing".into(), Detail::Timing))
            .when(!s.tests.is_empty(), |el| {
                el.child(tab(
                    "cd-tests",
                    format!("Tests {passed}/{}", s.tests.len()),
                    Detail::Tests,
                ))
            });
        let content = match detail {
            Detail::Request => v_flex()
                .child(section("General".into()))
                .child(format!("{} {}", e.method, e.url))
                .child(div().text_color(muted).child(if e.env.is_empty() {
                    e.name.clone()
                } else {
                    format!("{} · environment: {}", e.name, e.env)
                }))
                .child(section("Request headers".into()))
                .child(headers(&e.request_headers))
                .child(section("Request body".into()))
                .child(body("cb-req", &e.request_body)),
            Detail::Response => v_flex()
                .children(s.error.clone().map(|err| {
                    v_flex()
                        .child(section("Error".into()))
                        .child(div().text_color(danger).child(err))
                }))
                .child(section("Response headers".into()))
                .child(headers(&s.headers))
                .child(section("Response body".into()))
                .child(body("cb-res", &s.body)),
            Detail::Timing => v_flex()
                .child(section(format!("Total {} ms", s.duration_ms)))
                .children(s.timeline.iter().map(|p| {
                    let ms = if p.ms < 1. {
                        format!("{:.2} ms", p.ms)
                    } else {
                        format!("{} ms", p.ms.round())
                    };
                    h_flex()
                        .child(div().w(px(160.)).text_color(muted).child(p.name.clone()))
                        .child(div().w(px(80.)).text_right().child(ms))
                })),
            Detail::Tests => v_flex().pt_2().children(s.tests.iter().map(|test| {
                let why = test
                    .error
                    .as_deref()
                    .filter(|x| !x.is_empty())
                    .map(|x| format!(": {x}"))
                    .unwrap_or_default();
                div()
                    .text_color(if test.pass {
                        Hsla::from(rgb(0x389E0D))
                    } else {
                        danger
                    })
                    .child(format!(
                        "{} {}{why}",
                        if test.pass { "PASS" } else { "FAIL" },
                        test.name
                    ))
            })),
        };
        wrap().child(tabs).child(content).into_any_element()
    }
}
