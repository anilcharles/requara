//! Design (edit an API's endpoints, with style-guide checks, the list of
//! changes and publishing) and Docs (the reference page with code samples),
//! laid out like the web app's.

use super::*;
use crate::api::{
    ApiDetail, ChangeReport, Docs, Field, LintReport, OperationDetail, OperationInput,
};

const TYPES: [&str; 7] = [
    "string", "integer", "number", "boolean", "object", "array", "enum",
];
const DESIGN_METHODS: [&str; 5] = ["GET", "POST", "PUT", "PATCH", "DELETE"];

struct FieldRow {
    name: Entity<InputState>,
    description: Entity<InputState>,
    /// "a, b, c" when the field is an enum.
    options: Entity<InputState>,
    kind: String,
    is_enum: bool,
    required: bool,
    depth: usize,
    is_new: bool,
    /// What the app doesn't edit (format, example).
    extra: serde_json::Map<String, serde_json::Value>,
}

/// The inputs for the endpoint being edited.
struct Form {
    method: String,
    path: Entity<InputState>,
    summary: Entity<InputState>,
    description: Entity<TextareaState>,
    fields: Vec<FieldRow>,
    subs: Vec<Subscription>,
}

enum SaveState {
    Idle,
    Saving,
    Saved,
    Failed(String),
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum DesignMenu {
    Method,
    FieldType(usize),
}

pub(super) struct Design {
    pub(super) api_id: Option<String>,
    pub(super) op_key: Option<String>,
    api: Load<ApiDetail>,
    op: Load<OperationDetail>,
    lint: Load<LintReport>,
    changes: Load<ChangeReport>,
    form: Option<Form>,
    /// What the server has; the form is saved when it differs.
    saved: Option<OperationInput>,
    save: SaveState,
    edits: u64,
    menu: Option<DesignMenu>,
    confirm_discard: bool,
    publish_open: bool,
    publishing: bool,
    publish_version: Entity<InputState>,
    publish_notify: bool,
    just_published: Option<String>,
    notice: Option<String>,

    // Docs
    docs: Load<Docs>,
    pub(super) docs_draft: bool,
    docs_lang: String,
    docs_status: String,
}

impl Design {
    pub(super) fn new(
        subs: &mut Vec<Subscription>,
        window: &mut Window,
        cx: &mut Context<RequaraApp>,
    ) -> Self {
        Design {
            api_id: None,
            op_key: None,
            api: Load::Idle,
            op: Load::Idle,
            lint: Load::Idle,
            changes: Load::Idle,
            form: None,
            saved: None,
            save: SaveState::Idle,
            edits: 0,
            menu: None,
            confirm_discard: false,
            publish_open: false,
            publishing: false,
            publish_version: text_input("v1.0.0", "", subs, window, cx),
            publish_notify: true,
            just_published: None,
            notice: None,
            docs: Load::Idle,
            docs_draft: false,
            docs_lang: "cURL".into(),
            docs_status: String::new(),
        }
    }

    #[cfg(feature = "screenshot")]
    pub(super) fn shot_publish(&mut self) {
        self.publish_open = true;
    }

    pub(super) fn reset(&mut self) {
        self.api_id = None;
        self.op_key = None;
        self.api = Load::Idle;
        self.op = Load::Idle;
        self.lint = Load::Idle;
        self.changes = Load::Idle;
        self.form = None;
        self.saved = None;
        self.save = SaveState::Idle;
        self.menu = None;
        self.publish_open = false;
        self.docs = Load::Idle;
    }
}

/// An input whose edits schedule an autosave.
fn design_input(
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
    subs.push(cx.subscribe(&e, |this, _, event: &InputEvent, cx| {
        if matches!(event, InputEvent::Change) {
            this.design_touched(cx);
        }
    }));
    e
}

fn field_row(
    f: &Field,
    subs: &mut Vec<Subscription>,
    window: &mut Window,
    cx: &mut Context<RequaraApp>,
) -> FieldRow {
    let options = f.options.clone().unwrap_or_default();
    FieldRow {
        name: design_input("name", &f.name, subs, window, cx),
        description: design_input("Description", &f.description, subs, window, cx),
        options: design_input("value, other", &options.join(", "), subs, window, cx),
        kind: if f.kind.is_empty() {
            "string".into()
        } else {
            f.kind.clone()
        },
        is_enum: !options.is_empty(),
        required: f.required,
        depth: f.depth,
        is_new: f.is_new,
        extra: f.extra.clone(),
    }
}

fn form_input(key: &str, form: &Form, cx: &App) -> OperationInput {
    OperationInput {
        key: key.to_string(),
        method: form.method.clone(),
        path: form.path.read(cx).value().trim().to_string(),
        summary: form.summary.read(cx).value().to_string(),
        description: form.description.read(cx).value().to_string(),
        fields: form
            .fields
            .iter()
            .map(|r| Field {
                name: r.name.read(cx).value().to_string(),
                kind: r.kind.clone(),
                options: r.is_enum.then(|| {
                    r.options
                        .read(cx)
                        .value()
                        .split(',')
                        .map(|s| s.trim().to_string())
                        .filter(|s| !s.is_empty())
                        .collect()
                }),
                required: r.required,
                description: r.description.read(cx).value().to_string(),
                depth: r.depth,
                is_new: false,
                extra: r.extra.clone(),
            })
            .collect(),
    }
}

fn op_input(op: &OperationDetail) -> OperationInput {
    OperationInput {
        key: op.key.clone(),
        method: op.method.clone(),
        path: op.path.clone(),
        summary: op.summary.clone(),
        description: op.description.clone(),
        fields: op
            .fields
            .iter()
            .map(|f| Field {
                is_new: false,
                ..f.clone()
            })
            .collect(),
    }
}

impl RequaraApp {
    // ----- loading -----

    /// Opens Design (or Docs, if that's the current view) on an API and endpoint.
    pub(super) fn open_design(
        &mut self,
        api_id: String,
        op_key: Option<String>,
        cx: &mut Context<Self>,
    ) {
        let same_api = self.design.api_id.as_deref() == Some(api_id.as_str());
        self.design.api_id = Some(api_id.clone());
        self.design.op_key = op_key.clone();
        self.design.form = None;
        self.design.saved = None;
        self.design.save = SaveState::Idle;
        self.design.menu = None;
        self.design.notice = None;
        self.design.op = Load::Loading;
        if !same_api {
            self.design.api = Load::Loading;
            self.design.lint = Load::Loading;
            self.design.changes = Load::Loading;
            self.design.docs = Load::Idle;
            self.design.docs_draft = false;
            self.design.just_published = None;
        }
        let id = api_id.clone();
        self.fetch(
            cx,
            move |c| {
                // No endpoint named: open the API's first one.
                let key = match op_key {
                    Some(k) => Some(k),
                    None => c.operations(&id)?.into_iter().next().map(|o| o.key),
                };
                Ok(match key {
                    Some(k) => Some(c.operation(&id, &k)?),
                    None => None,
                })
            },
            |this, r| match r {
                Ok(Some(op)) => {
                    this.design.op_key = Some(op.key.clone());
                    this.design.op = Load::Ready(op);
                }
                Ok(None) => {
                    this.design.op = Load::Failed(
                        "This API has no endpoints yet. Import a spec or start from the template."
                            .into(),
                    )
                }
                Err(e) => this.design.op = Load::Failed(e),
            },
        );
        self.refresh_quality(cx);
        if self.view == View::Docs {
            self.load_docs(cx);
        }
        cx.notify();
    }

    /// The API's name and version, the style-guide report and the changes.
    fn refresh_quality(&mut self, cx: &mut Context<Self>) {
        let Some(id) = self.design.api_id.clone() else {
            return;
        };
        let (a, b, c) = (id.clone(), id.clone(), id);
        self.fetch(
            cx,
            move |cl| cl.api(&a),
            |this, r| this.design.api = r.map_or_else(Load::Failed, Load::Ready),
        );
        self.fetch(
            cx,
            move |cl| cl.lint(&b),
            |this, r| this.design.lint = r.map_or_else(Load::Failed, Load::Ready),
        );
        self.fetch(
            cx,
            move |cl| cl.changes(&c),
            |this, r| this.design.changes = r.map_or_else(Load::Failed, Load::Ready),
        );
    }

    pub(super) fn load_docs(&mut self, cx: &mut Context<Self>) {
        let Some(id) = self.design.api_id.clone() else {
            return;
        };
        let draft = self.design.docs_draft;
        if self.design.docs.ready().is_none() {
            self.design.docs = Load::Loading;
        }
        self.fetch(
            cx,
            move |c| c.docs(&id, draft),
            |this, r| this.design.docs = r.map_or_else(Load::Failed, Load::Ready),
        );
    }

    /// Called from `render`: picks a default API and builds the form's inputs.
    pub(super) fn materialize_design(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if !matches!(self.view, View::Design | View::Docs) || self.client.is_none() {
            return;
        }
        // Like the web app: Payments API if there is one, else the first.
        if self.design.api_id.is_none()
            && let Some(cols) = self.collections.ready()
            && let Some(c) = cols
                .iter()
                .find(|c| c.name == "Payments API")
                .or_else(|| cols.first())
        {
            let id = c.id.clone();
            self.open_design(id, None, cx);
        }
        let d = &mut self.design;
        if d.form.is_none()
            && let Load::Ready(op) = &d.op
        {
            let mut subs = vec![];
            let description = {
                let value = op.description.clone();
                let e = cx.new(|cx| {
                    TextareaState::new(window, cx)
                        .placeholder("What this endpoint does")
                        .default_value(value)
                });
                subs.push(cx.subscribe(&e, |this, _, event: &InputEvent, cx| {
                    if matches!(event, InputEvent::Change) {
                        this.design_touched(cx);
                    }
                }));
                e
            };
            d.form = Some(Form {
                method: op.method.clone(),
                path: design_input("/v1/things/{id}", &op.path, &mut subs, window, cx),
                summary: design_input(
                    "Short name, e.g. Create a charge",
                    &op.summary,
                    &mut subs,
                    window,
                    cx,
                ),
                description,
                fields: op
                    .fields
                    .iter()
                    .map(|f| field_row(f, &mut subs, window, cx))
                    .collect(),
                subs,
            });
            d.saved = Some(op_input(op));
        }
    }

    // ----- editing and autosave -----

    /// An edit happened: save 700 ms after the last one, like the web app.
    pub(super) fn design_touched(&mut self, cx: &mut Context<Self>) {
        self.design.edits += 1;
        let edit = self.design.edits;
        cx.spawn(async move |this, cx| {
            cx.background_executor()
                .timer(std::time::Duration::from_millis(700))
                .await;
            let _ = this.update(cx, |this, cx| {
                if this.design.edits == edit {
                    this.save_design(cx);
                }
            });
        })
        .detach();
        cx.notify();
    }

    fn save_design(&mut self, cx: &mut Context<Self>) {
        let d = &self.design;
        let (Some(api_id), Some(key), Some(form)) = (d.api_id.clone(), d.op_key.clone(), &d.form)
        else {
            return;
        };
        if !self.me.ready().is_some_and(|(me, _)| me.can_edit) {
            return;
        }
        let input = form_input(&key, form, cx);
        if input.path.is_empty() || d.saved.as_ref() == Some(&input) {
            return;
        }
        self.design.save = SaveState::Saving;
        let sent = input.clone();
        self.fetch(
            cx,
            move |c| c.save_operation(&api_id, &input),
            move |this, r| match r {
                Ok(new_key) => {
                    this.design.save = SaveState::Saved;
                    this.design.saved = Some(OperationInput {
                        key: new_key.clone(),
                        ..sent
                    });
                    if this.design.op_key.as_deref() != Some(new_key.as_str()) {
                        // The method or path changed, so the endpoint has a new key.
                        this.design.op_key = Some(new_key);
                    }
                }
                Err(e) => this.design.save = SaveState::Failed(e),
            },
        );
        // Checks, changes and the sidebar reflect the saved draft.
        cx.spawn(async move |this, cx| {
            cx.background_executor()
                .timer(std::time::Duration::from_millis(400))
                .await;
            let _ = this.update(cx, |this, cx| {
                this.refresh_quality(cx);
                this.reload_collections(cx);
            });
        })
        .detach();
        cx.notify();
    }

    fn edit_field(&mut self, i: usize, cx: &mut Context<Self>, edit: impl FnOnce(&mut FieldRow)) {
        if let Some(row) = self.design.form.as_mut().and_then(|f| f.fields.get_mut(i)) {
            edit(row);
        }
        self.design.menu = None;
        self.design_touched(cx);
    }

    fn publish(&mut self, cx: &mut Context<Self>) {
        let Some(id) = self.design.api_id.clone() else {
            return;
        };
        let version = self
            .design
            .publish_version
            .read(cx)
            .value()
            .trim()
            .to_string();
        let notify = self.design.publish_notify;
        self.design.publishing = true;
        self.fetch(
            cx,
            move |c| c.publish(&id, &version, notify),
            |this, r| {
                this.design.publishing = false;
                match r {
                    Ok(v) => {
                        this.design.publish_open = false;
                        this.design.just_published = Some(v);
                        this.design.notice = None;
                    }
                    Err(e) => this.design.notice = Some(format!("Couldn't publish: {e}")),
                }
            },
        );
        cx.spawn(async move |this, cx| {
            cx.background_executor()
                .timer(std::time::Duration::from_millis(600))
                .await;
            let _ = this.update(cx, |this, cx| {
                this.refresh_quality(cx);
                this.reload_collections(cx);
            });
        })
        .detach();
        cx.notify();
    }

    fn discard(&mut self, cx: &mut Context<Self>) {
        let (Some(id), key) = (self.design.api_id.clone(), self.design.op_key.clone()) else {
            return;
        };
        self.design.confirm_discard = false;
        let api = id.clone();
        self.fetch(
            cx,
            move |c| c.discard(&api),
            move |this, r| {
                if let Err(e) = r {
                    this.design.notice = Some(format!("Couldn't discard: {e}"));
                }
            },
        );
        // Reload the endpoint as published.
        cx.spawn(async move |this, cx| {
            cx.background_executor()
                .timer(std::time::Duration::from_millis(500))
                .await;
            let _ = this.update(cx, |this, cx| {
                this.design.api_id = None; // force a full reload
                this.open_design(id, key, cx);
                this.reload_collections(cx);
            });
        })
        .detach();
        cx.notify();
    }

    // ----- Design page -----

    pub(super) fn render_design(&mut self, cx: &mut Context<Self>) -> AnyElement {
        let t = cx.theme();
        let (muted, border, danger) = (t.muted_foreground, t.border, t.danger);
        let primary: Hsla = rgb(PRIMARY).into();
        let d = &self.design;
        let (form, op) = match (&d.form, &d.op) {
            (Some(form), Load::Ready(op)) => (form, op),
            (_, Load::Failed(e)) => return note(e.clone(), danger).p_6().into_any_element(),
            _ => {
                return note("Loading the endpoint…", muted)
                    .p_6()
                    .into_any_element();
            }
        };
        let (can_edit, can_publish) = self
            .me
            .ready()
            .map_or((false, false), |(me, _)| (me.can_edit, me.can_publish));
        let api_name = d.api.ready().map(|a| a.name.clone()).unwrap_or_default();
        let version = d.api.ready().map(|a| a.version.clone()).unwrap_or_default();
        let changes = d.changes.ready();
        let pending = changes.map_or(0, |c| c.changes.len());
        let suggested = changes
            .map(|c| c.suggested_version.clone())
            .unwrap_or_default();
        let summary = form.summary.read(cx).value().to_string();

        let tag = |text: String, color: Hsla| {
            div()
                .px_2()
                .rounded_sm()
                .border_1()
                .border_color(color.opacity(0.4))
                .bg(color.opacity(0.1))
                .text_color(color)
                .text_xs()
                .child(text)
        };
        let state_tag = if pending > 0 {
            tag("Unpublished changes".into(), rgb(0xD48806).into())
        } else if !version.is_empty() {
            tag(format!("Published {version}"), rgb(0x389E0D).into())
        } else {
            tag("Draft".into(), muted)
        };

        // Breadcrumb · save status · state · Discard · Preview docs · Publish
        let bar = h_flex()
            .h(px(56.))
            .flex_none()
            .px_6()
            .gap_3()
            .bg(t.background)
            .border_b_1()
            .border_color(border)
            .child(
                h_flex()
                    .flex_1()
                    .min_w_0()
                    .gap_2()
                    .text_sm()
                    .text_color(muted)
                    .child(api_name.clone())
                    .child("/")
                    .when(!op.tag.is_empty(), |el| el.child(op.tag.clone()).child("/"))
                    .child(
                        div()
                            .truncate()
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(t.foreground)
                            .child(if summary.is_empty() {
                                op.key.clone()
                            } else {
                                summary
                            }),
                    ),
            )
            .child(div().text_sm().text_color(muted).child(match &d.save {
                SaveState::Saving => "Saving…",
                SaveState::Saved => "Draft saved",
                _ => "",
            }))
            .child(state_tag)
            .when(
                pending > 0 && !version.is_empty() && !d.confirm_discard,
                |b| {
                    b.child(
                        Button::new("discard")
                            .ghost()
                            .label("Discard")
                            .disabled(!can_edit)
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.design.confirm_discard = true;
                                cx.notify();
                            })),
                    )
                },
            )
            .child(
                Button::new("preview-docs")
                    .outline()
                    .label("Preview docs")
                    .on_click(cx.listener(|this, _, _, cx| {
                        this.design.docs_draft = true;
                        this.design.docs = Load::Idle;
                        this.open_view(View::Docs, cx);
                    })),
            )
            .child(
                Button::new("publish")
                    .primary()
                    .label(format!("Publish {suggested}").trim().to_string())
                    .disabled(!can_publish || (pending == 0 && !version.is_empty()))
                    .on_click(cx.listener(move |this, _, window, cx| {
                        let v = this
                            .design
                            .changes
                            .ready()
                            .map(|c| c.suggested_version.clone())
                            .unwrap_or_default();
                        this.design
                            .publish_version
                            .update(cx, |s, cx| s.set_value(v, window, cx));
                        this.design.publish_open = true;
                        cx.notify();
                    })),
            );

        let banner = |text: String, color: Hsla| {
            div()
                .mx_6()
                .mt_4()
                .p_3()
                .rounded_md()
                .bg(color.opacity(0.12))
                .text_sm()
                .child(text)
        };
        let banners = v_flex()
            .when(d.confirm_discard, |b| {
                b.child(
                    h_flex()
                        .mx_6()
                        .mt_4()
                        .p_3()
                        .gap_3()
                        .rounded_md()
                        .bg(danger.opacity(0.1))
                        .text_sm()
                        .child(div().flex_1().child(format!(
                            "Discard {pending} unpublished {} across {api_name}? You can't undo this.",
                            if pending == 1 { "change" } else { "changes" }
                        )))
                        .child(Button::new("discard-yes").danger().small().label("Discard").on_click(cx.listener(|this, _, _, cx| this.discard(cx))))
                        .child(Button::new("discard-no").ghost().small().label("Cancel").on_click(cx.listener(|this, _, _, cx| {
                            this.design.confirm_discard = false;
                            cx.notify();
                        }))),
                )
            })
            .children(d.just_published.clone().map(|v| banner(format!("{api_name} {v} is live. Docs are updated and subscribers were emailed."), rgb(0x52C41A).into())))
            .children(match &d.save {
                SaveState::Failed(e) => Some(banner(e.clone(), danger)),
                _ => None,
            })
            .children(d.notice.clone().map(|n| banner(n, danger)))
            .when(!can_publish, |b| {
                b.child(banner("Publishing needs a publisher role or group from your identity provider. Ask your admin.".into(), primary))
            });

        let card = || {
            v_flex()
                .rounded_lg()
                .border_1()
                .border_color(border)
                .bg(t.background)
        };
        let card_title = |title: String| {
            h_flex()
                .h(px(56.))
                .px_6()
                .gap_3()
                .border_b_1()
                .border_color(border)
                .child(
                    div()
                        .flex_1()
                        .font_weight(FontWeight::SEMIBOLD)
                        .child(title),
                )
        };

        // Method + path, summary, description
        let method_picker = div()
            .relative()
            .flex_none()
            .child(
                h_flex()
                    .id("design-method")
                    .w(px(120.))
                    .h(px(34.))
                    .px_3()
                    .justify_between()
                    .rounded_l_md()
                    .border_1()
                    .border_color(border)
                    .font_family(MONO)
                    .font_weight(FontWeight::BOLD)
                    .text_sm()
                    .text_color(method_color(&form.method))
                    .child(form.method.clone())
                    .child(div().text_xs().text_color(muted).child("▾"))
                    .on_click(cx.listener(|this, _, _, cx| {
                        this.design.menu = if this.design.menu == Some(DesignMenu::Method) {
                            None
                        } else {
                            Some(DesignMenu::Method)
                        };
                        cx.notify();
                    })),
            )
            .when(d.menu == Some(DesignMenu::Method) && can_edit, |el| {
                el.child(deferred(
                    popup(border, t.popover)
                        .top(px(38.))
                        .left_0()
                        .w(px(120.))
                        .children(DESIGN_METHODS.map(|m| {
                            menu_item(SharedString::from(format!("dm-{m}")), t.list_active)
                                .font_family(MONO)
                                .font_weight(FontWeight::BOLD)
                                .text_color(method_color(m))
                                .child(m)
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    if let Some(f) = this.design.form.as_mut() {
                                        f.method = m.to_string();
                                    }
                                    this.design.menu = None;
                                    this.design_touched(cx);
                                }))
                        })),
                ))
            });
        let endpoint =
            card()
                .p_6()
                .gap_2()
                .child(
                    h_flex().pb_2().child(method_picker).child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .font_family(MONO)
                            .child(Input::new(&form.path).disabled(!can_edit)),
                    ),
                )
                .child(div().text_sm().child("Summary"))
                .child(Input::new(&form.summary).disabled(!can_edit))
                .child(div().pt_2().text_sm().child("Description"))
                .child(Textarea::new(&form.description).h(px(84.)))
                .child(div().text_sm().text_color(muted).child(
                    "Markdown supported. Shown at the top of this endpoint's reference page.",
                ));

        // Request body fields
        let head = h_flex()
            .px_4()
            .py_2p5()
            .gap_2()
            .bg(t.muted)
            .text_sm()
            .font_weight(FontWeight::SEMIBOLD)
            .text_color(muted)
            .border_b_1()
            .border_color(border)
            .child(div().flex_1().min_w(px(110.)).child("Field"))
            .child(div().w(px(124.)).flex_none().child("Type"))
            .child(div().w(px(64.)).flex_none().child("Required"))
            .child(div().flex_1().min_w(px(110.)).child("Description"))
            .child(div().w(px(84.)).flex_none());
        let icon = |id: SharedString, glyph: &'static str, enabled: bool| {
            div()
                .id(id)
                .w(px(28.))
                .text_center()
                .text_sm()
                .text_color(if enabled { muted } else { muted.opacity(0.3) })
                .when(enabled, |el| el.hover(|el| el.text_color(primary)))
                .child(glyph)
        };
        let rows = form.fields.iter().enumerate().map(|(i, f)| {
            let prev = i.checked_sub(1).and_then(|p| form.fields.get(p));
            // A field can nest under the object (or deeper field) right above it.
            let can_nest = can_edit
                && prev.is_some_and(|p| {
                    if p.kind == "object" {
                        p.depth >= f.depth
                    } else {
                        p.depth > f.depth
                    }
                });
            let can_outdent = can_edit && f.depth > 0;
            let type_label = if f.is_enum {
                "string · enum".to_string()
            } else {
                f.kind.clone()
            };
            let type_color: Hsla = if f.kind == "object" {
                rgb(0x9254DE).into()
            } else {
                rgb(0x4096FF).into()
            };
            h_flex()
                .px_4()
                .py_1p5()
                .gap_2()
                .border_b_1()
                .border_color(border)
                .when(f.is_new, |el| {
                    el.bg(Hsla::from(rgb(0x52C41A)).opacity(0.08))
                })
                .child(
                    h_flex()
                        .flex_1()
                        .min_w(px(110.))
                        .pl(px(f.depth as f32 * 20.))
                        .gap_1p5()
                        .when(f.depth > 0, |el| {
                            el.child(div().text_color(muted).child("└"))
                        })
                        .child(
                            div()
                                .flex_1()
                                .min_w_0()
                                .font_family(MONO)
                                .font_weight(FontWeight::MEDIUM)
                                .child(
                                    Input::new(&f.name)
                                        .small()
                                        .appearance(false)
                                        .disabled(!can_edit),
                                ),
                        )
                        .when(f.is_new, |el| {
                            el.child(tag("New".into(), rgb(0x389E0D).into()))
                        }),
                )
                .child(
                    div()
                        .relative()
                        .w(px(124.))
                        .flex_none()
                        .child(
                            h_flex()
                                .id(SharedString::from(format!("ft-{i}")))
                                .gap_1()
                                .text_sm()
                                .font_family(MONO)
                                .text_color(type_color)
                                .child(type_label)
                                .child(div().text_xs().text_color(muted).child("▾"))
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    let m = DesignMenu::FieldType(i);
                                    this.design.menu = if this.design.menu == Some(m) {
                                        None
                                    } else {
                                        Some(m)
                                    };
                                    cx.notify();
                                })),
                        )
                        .when(d.menu == Some(DesignMenu::FieldType(i)) && can_edit, |el| {
                            el.child(deferred(
                                popup(border, t.popover)
                                    .top(px(26.))
                                    .left_0()
                                    .w(px(150.))
                                    .children(TYPES.map(|ty| {
                                        menu_item(
                                            SharedString::from(format!("ft-{i}-{ty}")),
                                            t.list_active,
                                        )
                                        .font_family(MONO)
                                        .child(if ty == "enum" { "string · enum" } else { ty })
                                        .on_click(
                                            cx.listener(move |this, _, _, cx| {
                                                this.edit_field(i, cx, |r| {
                                                    r.is_enum = ty == "enum";
                                                    r.kind = if ty == "enum" {
                                                        "string".into()
                                                    } else {
                                                        ty.to_string()
                                                    };
                                                })
                                            }),
                                        )
                                    })),
                            ))
                        }),
                )
                .child(
                    div().w(px(64.)).flex_none().child(
                        Checkbox::new(SharedString::from(format!("fr-{i}")))
                            .checked(f.required)
                            .disabled(!can_edit)
                            .on_click(cx.listener(move |this, on: &bool, _, cx| {
                                let on = *on;
                                this.edit_field(i, cx, |r| r.required = on)
                            })),
                    ),
                )
                .child(
                    v_flex()
                        .flex_1()
                        .min_w(px(110.))
                        .child(
                            Input::new(&f.description)
                                .small()
                                .appearance(false)
                                .disabled(!can_edit),
                        )
                        .when(f.is_enum, |el| {
                            el.child(
                                h_flex()
                                    .gap_1()
                                    .child(div().pl_2().text_xs().text_color(muted).child("one of"))
                                    .child(
                                        div().flex_1().font_family(MONO).child(
                                            Input::new(&f.options)
                                                .small()
                                                .appearance(false)
                                                .disabled(!can_edit),
                                        ),
                                    ),
                            )
                        }),
                )
                .child(
                    h_flex()
                        .w(px(84.))
                        .flex_none()
                        .child(
                            icon(SharedString::from(format!("fo-{i}")), "←", can_outdent).when(
                                can_outdent,
                                |el| {
                                    el.on_click(cx.listener(move |this, _, _, cx| {
                                        this.edit_field(i, cx, |r| r.depth -= 1)
                                    }))
                                },
                            ),
                        )
                        .child(
                            icon(SharedString::from(format!("fn-{i}")), "→", can_nest).when(
                                can_nest,
                                |el| {
                                    el.on_click(cx.listener(move |this, _, _, cx| {
                                        this.edit_field(i, cx, |r| r.depth += 1)
                                    }))
                                },
                            ),
                        )
                        .child(
                            icon(SharedString::from(format!("fd-{i}")), "✕", can_edit).when(
                                can_edit,
                                |el| {
                                    el.on_click(cx.listener(move |this, _, _, cx| {
                                        if let Some(f) = this.design.form.as_mut()
                                            && i < f.fields.len()
                                        {
                                            f.fields.remove(i);
                                        }
                                        this.design.menu = None;
                                        this.design_touched(cx);
                                    }))
                                },
                            ),
                        ),
                )
        });
        let body = card()
            .child(
                card_title("Request body".into())
                    .child(
                        div()
                            .text_sm()
                            .font_family(MONO)
                            .text_color(muted)
                            .child("application/json"),
                    )
                    .child(
                        Button::new("add-field")
                            .outline()
                            .small()
                            .label("+ Add field")
                            .disabled(!can_edit)
                            .on_click(cx.listener(|this, _, window, cx| {
                                if let Some(f) = this.design.form.as_mut() {
                                    let blank = Field {
                                        kind: "string".into(),
                                        ..Default::default()
                                    };
                                    let row = field_row(&blank, &mut f.subs, window, cx);
                                    f.fields.push(row);
                                }
                                this.design_touched(cx);
                            })),
                    ),
            )
            .child(if form.fields.is_empty() {
                div()
                    .p_6()
                    .text_center()
                    .text_sm()
                    .text_color(muted)
                    .child("No request body")
                    .into_any_element()
            } else {
                v_flex().child(head).children(rows).into_any_element()
            });

        let responses =
            card()
                .child(card_title("Responses".into()))
                .child(
                    v_flex()
                        .px_5()
                        .py_2()
                        .children(op.responses.iter().map(|r| {
                            h_flex()
                                .py_2()
                                .gap_3()
                                .child(status_code_tag(&r.status))
                                .child(div().text_sm().child(r.description.clone()))
                        })),
                );

        // Style guide
        let lint_card = {
            let (count, items, footer): (usize, Vec<AnyElement>, String) = match &d.lint {
                Load::Ready(l) => {
                    let serious = |s: &str| s == "error" || s == "warn";
                    let warnings = l.issues.iter().filter(|i| serious(&i.severity)).count();
                    let mut sorted: Vec<_> = l.issues.iter().collect();
                    sorted.sort_by_key(|i| !serious(&i.severity));
                    let items = sorted
                        .into_iter()
                        .take(6)
                        .map(|w| {
                            let color: Hsla = match w.severity.as_str() {
                                "error" => rgb(0xFF4D4F).into(),
                                "warn" => rgb(0xFAAD14).into(),
                                _ => rgb(0xBFBFBF).into(),
                            };
                            h_flex()
                                .items_start()
                                .py_2()
                                .gap_2p5()
                                .child(div().pt_1p5().child(dot(color, 6.)))
                                .child(
                                    v_flex()
                                        .flex_1()
                                        .min_w_0()
                                        .child(div().text_sm().child(w.message.clone()))
                                        .child(
                                            div()
                                                .text_xs()
                                                .font_family(MONO)
                                                .text_color(muted)
                                                .truncate()
                                                .child(w.rule.clone()),
                                        ),
                                )
                                .into_any_element()
                        })
                        .collect();
                    let more = l.issues.len().saturating_sub(6);
                    (
                        warnings,
                        items,
                        format!(
                            "{} of {} rules passing{}",
                            l.passed.len(),
                            l.rules,
                            if more > 0 {
                                format!(" · {more} more findings")
                            } else {
                                String::new()
                            }
                        ),
                    )
                }
                Load::Failed(e) => (0, vec![], e.clone()),
                _ => (0, vec![], "Checking…".into()),
            };
            card()
                .child(card_title("Style guide".into()).child(if count > 0 {
                    tag(format!("{count} warnings"), rgb(0xD48806).into())
                } else {
                    tag("Passing".into(), rgb(0x389E0D).into())
                }))
                .child(
                    v_flex()
                        .px_5()
                        .pt_2()
                        .pb_4()
                        .children(items)
                        .child(div().text_sm().text_color(muted).child(footer)),
                )
        };

        // Changes since the last publish
        let changes_card = {
            let title = match changes {
                Some(c) if !c.from.is_empty() => format!("Changes since {}", c.from),
                _ => "Changes".into(),
            };
            let content: AnyElement = match &d.changes {
                Load::Ready(c) => v_flex()
                    .when(c.from.is_empty(), |el| {
                        el.child(
                            div()
                                .text_sm()
                                .text_color(muted)
                                .child("Never published. Everything here is new."),
                        )
                    })
                    .when(!c.from.is_empty() && c.changes.is_empty(), |el| {
                        el.child(
                            div()
                                .text_sm()
                                .text_color(muted)
                                .child("No changes since the last publish."),
                        )
                    })
                    .children(c.changes.iter().map(|ch| {
                        let (glyph, color): (&str, Hsla) = match ch.kind.as_str() {
                            "added" => ("+", rgb(0x389E0D).into()),
                            "removed" => ("−", danger),
                            _ => ("~", rgb(0xD46B08).into()),
                        };
                        h_flex()
                            .items_start()
                            .py_1()
                            .gap_2p5()
                            .child(
                                div()
                                    .font_family(MONO)
                                    .font_weight(FontWeight::BOLD)
                                    .text_color(if ch.breaking { danger } else { color })
                                    .child(glyph),
                            )
                            .child(
                                v_flex()
                                    .flex_1()
                                    .min_w_0()
                                    .child(div().text_sm().text_color(muted).child(ch.text.clone()))
                                    .when(!ch.operation.is_empty(), |el| {
                                        el.child(
                                            div()
                                                .text_xs()
                                                .font_family(MONO)
                                                .text_color(muted)
                                                .child(ch.operation.clone()),
                                        )
                                    }),
                            )
                    }))
                    .when(!c.changes.is_empty(), |el| {
                        let (text, color): (String, Hsla) = if c.breaking {
                            (
                                format!(
                                    "Breaking. Publishing needs a new major version ({}).",
                                    c.suggested_version
                                ),
                                danger,
                            )
                        } else {
                            (
                                "Non-breaking. Safe to ship as a minor version.".into(),
                                rgb(0x52C41A).into(),
                            )
                        };
                        el.child(
                            div()
                                .mt_3()
                                .p_3()
                                .rounded_md()
                                .bg(color.opacity(0.12))
                                .text_sm()
                                .child(text),
                        )
                    })
                    .into_any_element(),
                Load::Failed(e) => div()
                    .text_sm()
                    .text_color(danger)
                    .child(e.clone())
                    .into_any_element(),
                _ => div()
                    .text_sm()
                    .text_color(muted)
                    .child("Comparing with the published version…")
                    .into_any_element(),
            };
            card()
                .child(card_title(title))
                .child(div().px_5().pt_3().pb_4().child(content))
        };

        v_flex()
            .size_full()
            .child(bar)
            .child(
                v_flex()
                    .id("design")
                    .flex_1()
                    .min_h_0()
                    .overflow_y_scroll()
                    .child(banners)
                    .child(
                        h_flex()
                            .items_start()
                            .p_6()
                            .gap_6()
                            .child(
                                v_flex()
                                    .flex_1()
                                    .min_w_0()
                                    .gap_4()
                                    .child(endpoint)
                                    .child(body)
                                    .child(responses),
                            )
                            .child(
                                v_flex()
                                    .w(px(340.))
                                    .flex_none()
                                    .gap_4()
                                    .child(lint_card)
                                    .child(changes_card),
                            ),
                    ),
            )
            .into_any_element()
    }

    /// The "Publish vX?" dialog, over the window.
    pub(super) fn render_publish_panel(&mut self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let d = &self.design;
        if !d.publish_open || self.view != View::Design {
            return None;
        }
        let t = cx.theme();
        let (muted, danger) = (t.muted_foreground, t.danger);
        let version = d.publish_version.read(cx).value().to_string();
        let report = d.changes.ready();
        let n = report.map_or(0, |r| r.changes.len());
        let intro = match report {
            Some(r) if !r.from.is_empty() => format!(
                "{n} {} will go live for everyone using this API.",
                if r.breaking {
                    "changes, including breaking ones,"
                } else {
                    "non-breaking changes"
                }
            ),
            _ => "This is the first published version. Docs and mock servers will use it."
                .to_string(),
        };
        let panel = v_flex()
            .id("publish-panel")
            .w(px(460.))
            .p_6()
            .gap_4()
            .rounded_lg()
            .border_1()
            .border_color(t.border)
            .bg(t.background)
            .shadow_lg()
            .occlude()
            .child(
                div()
                    .text_lg()
                    .font_weight(FontWeight::SEMIBOLD)
                    .child(format!("Publish {version}?")),
            )
            .child(div().text_sm().text_color(muted).child(intro))
            .when(n > 0, |el| {
                el.child(v_flex().p_3().rounded_md().bg(t.muted).children(
                    report.into_iter().flat_map(|r| r.changes.iter()).map(|c| {
                        h_flex()
                            .gap_2()
                            .text_sm()
                            .child(
                                div()
                                    .font_family(MONO)
                                    .font_weight(FontWeight::BOLD)
                                    .text_color(if c.breaking {
                                        danger
                                    } else {
                                        rgb(0x389E0D).into()
                                    })
                                    .child(if c.kind == "added" { "+" } else { "~" }),
                            )
                            .child(div().flex_1().min_w_0().child(c.text.clone()))
                    }),
                ))
            })
            .child(
                v_flex()
                    .gap_2()
                    .child(div().text_sm().child("Version"))
                    .child(
                        div()
                            .font_family(MONO)
                            .child(Input::new(&d.publish_version)),
                    ),
            )
            .child(
                Checkbox::new("publish-notify")
                    .label("Email workspace members the changelog")
                    .checked(d.publish_notify)
                    .on_click(cx.listener(|this, on: &bool, _, cx| {
                        this.design.publish_notify = *on;
                        cx.notify();
                    })),
            )
            .children(
                d.notice
                    .clone()
                    .map(|n| div().text_sm().text_color(danger).child(n)),
            )
            .child(
                h_flex()
                    .justify_end()
                    .gap_2()
                    .child(
                        Button::new("publish-cancel")
                            .ghost()
                            .label("Cancel")
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.design.publish_open = false;
                                cx.notify();
                            })),
                    )
                    .child(
                        Button::new("publish-ok")
                            .primary()
                            .label("Publish")
                            .loading(d.publishing)
                            .on_click(cx.listener(|this, _, _, cx| this.publish(cx))),
                    ),
            );
        Some(
            deferred(
                div()
                    .id("publish-backdrop")
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

    // ----- Docs page -----

    fn download_spec(&mut self, format: &'static str, cx: &mut Context<Self>) {
        let Some(id) = self.design.api_id.clone() else {
            return;
        };
        let name = self
            .design
            .docs
            .ready()
            .map(|d| d.name.clone())
            .unwrap_or_else(|| "api".into());
        self.fetch(
            cx,
            move |c| {
                let text = c.spec(&id, format)?;
                let dir =
                    dirs::download_dir().ok_or_else(|| anyhow::anyhow!("no Downloads folder"))?;
                let file = dir.join(format!(
                    "{}.openapi.{format}",
                    name.to_lowercase().replace(' ', "-")
                ));
                std::fs::write(&file, text)?;
                Ok(file.display().to_string())
            },
            |this, r| {
                this.design.notice = Some(match r {
                    Ok(path) => format!("Saved to {path}"),
                    Err(e) => format!("Couldn't download the spec: {e}"),
                })
            },
        );
    }

    pub(super) fn render_docs(&mut self, cx: &mut Context<Self>) -> AnyElement {
        let t = cx.theme();
        let (muted, border, danger) = (t.muted_foreground, t.border, t.danger);
        let d = &self.design;
        let docs = match &d.docs {
            Load::Ready(docs) => docs,
            Load::Failed(e) => {
                return error_box("Couldn't load the docs", e, danger)
                    .m_6()
                    .into_any_element();
            }
            _ => return note("Loading the docs…", muted).p_6().into_any_element(),
        };
        let Some(op) = docs
            .operations
            .iter()
            .find(|o| Some(&o.key) == d.op_key.as_ref())
            .or_else(|| docs.operations.first())
        else {
            return div()
                .pt_20()
                .w_full()
                .text_center()
                .text_color(muted)
                .child("No endpoints to document yet.")
                .into_any_element();
        };

        let section = |title: &'static str| {
            div()
                .mt_8()
                .pb_2()
                .border_b_1()
                .border_color(border)
                .font_weight(FontWeight::SEMIBOLD)
                .child(title)
        };
        let required = || div().text_xs().text_color(rgb(0xD46B08)).child("required");
        let entry = |name: String,
                     kind: String,
                     is_required: bool,
                     description: String,
                     depth: usize,
                     options: Vec<String>| {
            v_flex()
                .py_3()
                .pl(px(depth as f32 * 24.))
                .border_b_1()
                .border_color(border)
                .child(
                    h_flex()
                        .gap_2p5()
                        .child(
                            div()
                                .font_family(MONO)
                                .font_weight(FontWeight::SEMIBOLD)
                                .child(name),
                        )
                        .child(div().text_sm().text_color(muted).child(kind))
                        .when(is_required, |el| el.child(required())),
                )
                .when(!description.is_empty(), |el| {
                    el.child(div().mt_1().text_sm().text_color(muted).child(description))
                })
                .when(!options.is_empty(), |el| {
                    el.child(h_flex().flex_wrap().mt_1p5().gap_1p5().children(
                        options.into_iter().map(|o| {
                            div()
                                .px_1p5()
                                .rounded_sm()
                                .border_1()
                                .border_color(border)
                                .bg(t.muted)
                                .text_xs()
                                .font_family(MONO)
                                .child(o)
                        }),
                    ))
                })
        };

        let article = v_flex()
            .flex_1()
            .min_w_0()
            .max_w(px(680.))
            .when(docs.draft, |el| {
                el.child(
                    div()
                        .mb_5()
                        .p_3()
                        .rounded_md()
                        .bg(rgb(0xFAAD14).opacity(0.12))
                        .text_sm()
                        .child("Preview of unpublished changes. Readers see the last published version."),
                )
            })
            .child(h_flex().gap_2().text_sm().text_color(muted).child(docs.name.clone()).child("/").child(op.tag.clone()))
            .child(div().my_3().text_3xl().font_weight(FontWeight::SEMIBOLD).child(if op.summary.is_empty() { op.key.clone() } else { op.summary.clone() }))
            .child(
                h_flex().child(
                    h_flex()
                        .px_3()
                        .py_1p5()
                        .gap_2p5()
                        .rounded_md()
                        .border_1()
                        .border_color(border)
                        .bg(t.muted)
                        .child(div().text_xs().font_family(MONO).font_weight(FontWeight::BOLD).text_color(method_color(&op.method)).child(op.method.clone()))
                        .child(div().text_sm().font_family(MONO).child(format!("{}{}", docs.base_url, op.path))),
                ),
            )
            .when(!op.description.is_empty(), |el| el.child(div().mt_5().text_color(muted).child(op.description.clone())))
            .when(!op.params.is_empty(), |el| {
                el.child(section("Parameters")).children(
                    op.params.iter().map(|p| entry(p.name.clone(), format!("{} · {}", p.kind, p.location), p.required, p.description.clone(), 0, vec![])),
                )
            })
            .when(!op.fields.is_empty(), |el| {
                el.child(section("Body parameters")).children(op.fields.iter().map(|f| {
                    entry(f.name.clone(), f.kind.clone(), f.required, f.description.clone(), f.depth, f.options.clone().unwrap_or_default())
                }))
            })
            .child(section("Responses"))
            .children(
                op.responses
                    .iter()
                    .map(|r| h_flex().py_2().gap_3().child(status_code_tag(&r.status)).child(div().text_sm().child(r.description.clone()))),
            )
            .child(
                h_flex()
                    .flex_wrap()
                    .mt_8()
                    .gap_2()
                    .child(Button::new("spec-yaml").outline().label("OpenAPI (YAML)").on_click(cx.listener(|this, _, _, cx| this.download_spec("yaml", cx))))
                    .child(Button::new("spec-json").outline().label("JSON").on_click(cx.listener(|this, _, _, cx| this.download_spec("json", cx))))
                    .when(!docs.mock_url.is_empty(), |el| {
                        let url = docs.mock_url.clone();
                        el.child(div().text_sm().text_color(muted).child("Mock:")).child(div().text_xs().font_family(MONO).child(docs.mock_url.clone())).child(
                            Button::new("copy-mock")
                                .ghost()
                                .xsmall()
                                .label("Copy")
                                .on_click(move |_, _, cx| cx.write_to_clipboard(ClipboardItem::new_string(url.clone()))),
                        )
                    }),
            )
            .children(d.notice.clone().map(|n| div().mt_3().text_sm().text_color(muted).child(n)));

        // The code panels are always dark, like the web app's.
        let mut langs: Vec<String> = op.samples.keys().cloned().collect();
        langs.sort_by_key(|l| l != "cURL"); // cURL first, like the web app
        let lang = if langs.contains(&d.docs_lang) {
            d.docs_lang.clone()
        } else {
            langs.first().cloned().unwrap_or_default()
        };
        let statuses: Vec<String> = op.responses.iter().map(|r| r.status.clone()).collect();
        let response = op
            .responses
            .iter()
            .find(|r| r.status == d.docs_status)
            .or_else(|| op.responses.iter().find(|r| r.status.starts_with('2')))
            .or_else(|| op.responses.first());
        let code_panel = |title: &'static str,
                          tabs: Vec<String>,
                          active: String,
                          code: String,
                          is_lang: bool,
                          cx: &Context<Self>| {
            v_flex()
                .rounded_lg()
                .overflow_hidden()
                .bg(rgb(0x141414))
                .child(
                    h_flex()
                        .h(px(44.))
                        .px_4()
                        .gap_4()
                        .bg(rgb(0x1F1F1F))
                        .child(
                            div()
                                .flex_1()
                                .text_sm()
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(rgb(0xD9D9D9))
                                .child(title),
                        )
                        .children(tabs.into_iter().map(|tab| {
                            let on = tab == active;
                            let value = tab.clone();
                            div()
                                .id(SharedString::from(format!("{title}-{tab}")))
                                .text_sm()
                                .text_color(if on { rgb(0x4096FF) } else { rgb(0x8C8C8C) })
                                .hover(|el| el.text_color(rgb(0x4096FF)))
                                .child(tab)
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    if is_lang {
                                        this.design.docs_lang = value.clone();
                                    } else {
                                        this.design.docs_status = value.clone();
                                    }
                                    cx.notify();
                                }))
                        })),
                )
                .child(
                    div()
                        .p_4()
                        .text_xs()
                        .font_family(MONO)
                        .text_color(rgb(0xD9D9D9))
                        .child(code),
                )
        };
        let aside = v_flex()
            .w(px(480.))
            .flex_none()
            .gap_4()
            .when(!langs.is_empty(), |el| {
                el.child(code_panel(
                    "Request",
                    langs.clone(),
                    lang.clone(),
                    op.samples.get(&lang).cloned().unwrap_or_default(),
                    true,
                    cx,
                ))
            })
            .children(response.map(|r| {
                code_panel(
                    "Response",
                    statuses,
                    r.status.clone(),
                    if r.example.is_empty() {
                        "(no body)".into()
                    } else {
                        r.example.clone()
                    },
                    false,
                    cx,
                )
            }));

        div()
            .id("docs")
            .size_full()
            .overflow_y_scroll()
            .child(
                h_flex()
                    .items_start()
                    .gap_12()
                    .pl_12()
                    .pr_10()
                    .py_10()
                    .child(article)
                    .child(aside),
            )
            .into_any_element()
    }
}

/// "201" in green, "402" in amber, "500" in red.
fn status_code_tag(status: &str) -> impl IntoElement {
    let color: Hsla = if status.starts_with('2') {
        rgb(0x389E0D)
    } else if status.starts_with('4') {
        rgb(0xD48806)
    } else {
        rgb(0xCF1322)
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
        .font_family(MONO)
        .child(status.to_string())
}
