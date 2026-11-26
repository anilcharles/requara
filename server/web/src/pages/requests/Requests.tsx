import { CloseOutlined, DownOutlined, PlusOutlined } from "@ant-design/icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, App, Breadcrumb, Button, Dropdown, Input, InputNumber, Layout, Segmented, Select, Space, Spin, Switch, Tabs, theme, Typography } from "antd";
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { Navigate, useNavigate, useParams } from "react-router-dom";
import { get, post, put, type Field, type OperationDetail, type RequestDoc, type SendResult } from "../../api";
import { Method, mono } from "../../components/bits";
import CodeEditor from "../../components/CodeEditor";
import KeyValueTable from "../../components/KeyValueTable";
import { methodColor } from "../../lib";
import { CollectionsSider, useCollections } from "../../shell/Layouts";
import { useEnvironments, useMe, usePrefs } from "../../shell/session";
import { logSend } from "../../shell/consoleStore";
import ResponsePane from "./ResponsePane";

const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];
const TABS_KEY = "speklo.openTabs";

// Unsaved edits per request, kept while you switch tabs (the editor remounts per request).
const drafts = new Map<string, RequestDoc>();
const draftListeners = new Set<() => void>();
let draftVersion = 0;
function storeDraft(id: string, doc: RequestDoc | null) {
  const had = drafts.has(id);
  if (doc) drafts.set(id, doc);
  else drafts.delete(id);
  if (had !== drafts.has(id)) {
    draftVersion++;
    draftListeners.forEach((l) => l());
  }
}
function useDraftVersion() {
  return useSyncExternalStore(
    (l) => (draftListeners.add(l), () => void draftListeners.delete(l)),
    () => draftVersion,
  );
}

function useOpenTabs(current?: string) {
  const [tabs, setTabs] = useState<string[]>(() => {
    try {
      return [...new Set<string>(JSON.parse(localStorage.getItem(TABS_KEY) ?? "[]"))];
    } catch {
      return [];
    }
  });
  useEffect(() => {
    if (current) setTabs((t) => (t.includes(current) ? t : [...t, current].slice(-8)));
  }, [current]);
  useEffect(() => {
    try {
      localStorage.setItem(TABS_KEY, JSON.stringify(tabs));
    } catch {
      /* ignore */
    }
  }, [tabs]);
  return [tabs, setTabs] as const;
}

// Builds a JSON Schema from the operation's body fields so Monaco can validate.
function schemaFromFields(fields: Field[]): object {
  const root: { type: string; properties: Record<string, object>; required: string[]; additionalProperties: boolean } = { type: "object", properties: {}, required: [], additionalProperties: false };
  const stack: (typeof root)[] = [root];
  for (const f of fields) {
    stack.length = f.depth + 1;
    const parent = stack[f.depth];
    if (!parent) continue;
    const s: Record<string, unknown> = { description: f.description };
    // Values may be {{variables}}, so allow strings everywhere.
    s.type = f.type === "object" ? "object" : f.type === "array" ? "array" : [f.type, "string"];
    if (f.enum?.length) s.anyOf = [{ enum: f.enum }, { pattern: "\\{\\{.+\\}\\}" }];
    if (f.type === "object") Object.assign(s, { properties: {}, required: [], additionalProperties: false });
    parent.properties[f.name] = s;
    if (f.required) parent.required.push(f.name);
    if (f.type === "object") stack.push(s as typeof root);
  }
  return root;
}

export default function Requests() {
  const { id } = useParams();
  const nav = useNavigate();
  const cols = useCollections();
  if (!id) {
    const first = cols.data?.[0]?.folders[0]?.requests[0];
    if (cols.isLoading) return <Spin style={{ margin: 40 }} />;
    return first ? <Navigate to={`/requests/${first.id}`} replace /> : <Typography.Text style={{ margin: 40 }}>No requests yet. Create an API first.</Typography.Text>;
  }
  return (
    <>
      <CollectionsSider onPick={(r) => nav(`/requests/${r.id}`)} isSelected={(r) => r.id === id} />
      <Workspace id={id} />
    </>
  );
}

function Workspace({ id }: { id: string }) {
  const { token } = theme.useToken();
  const { modal } = App.useApp();
  const nav = useNavigate();
  useDraftVersion(); // re-render when a tab gains or loses unsaved changes
  const cols = useCollections();
  const [tabs, setTabs] = useOpenTabs(id);
  const byId = useMemo(() => {
    const m = new Map<string, { name: string; method: string; api: string; folder: string }>();
    cols.data?.forEach((c) => c.folders.forEach((f) => f.requests.forEach((r) => m.set(r.id, { name: r.name, method: r.method, api: c.name, folder: f.name }))));
    return m;
  }, [cols.data]);
  const visible = tabs.filter((t) => byId.has(t) || t === id);

  const doClose = (t: string) => {
    storeDraft(t, null);
    const rest = visible.filter((x) => x !== t);
    setTabs(rest);
    if (t === id) nav(rest.length ? `/requests/${rest[rest.length - 1]}` : "/requests");
  };
  const close = (t: string) => {
    if (!drafts.has(t)) return doClose(t);
    modal.confirm({
      title: "Discard unsaved changes?",
      content: `"${byId.get(t)?.name ?? "This request"}" has changes you haven't saved.`,
      okText: "Discard",
      okButtonProps: { danger: true },
      onOk: () => doClose(t),
    });
  };

  return (
    <Layout.Content style={{ display: "flex", flexDirection: "column", minWidth: 0 }}>
      <div style={{ display: "flex", alignItems: "flex-end", gap: 2, height: 40, padding: "6px 12px 0", background: token.colorBgLayout, borderBottom: `1px solid ${token.colorSplit}` }}>
        {visible.map((t) => {
          const r = byId.get(t);
          const active = t === id;
          return (
            <div
              key={t}
              onClick={() => nav(`/requests/${t}`)}
              style={{
                display: "flex",
                alignItems: "center",
                gap: 8,
                height: 34,
                padding: "0 10px 0 14px",
                cursor: "pointer",
                borderRadius: "8px 8px 0 0",
                background: active ? token.colorBgContainer : undefined,
                border: active ? `1px solid ${token.colorSplit}` : "1px solid transparent",
                borderBottom: "none",
                color: active ? token.colorText : token.colorTextSecondary,
                maxWidth: 240,
              }}
            >
              <Method method={r?.method ?? "GET"} width="auto" />
              <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{r?.name ?? "…"}</span>
              {drafts.has(t) && <span title="Unsaved changes" style={{ width: 6, height: 6, borderRadius: 6, background: token.colorWarning, flexShrink: 0 }} />}
              <CloseOutlined
                aria-label="Close tab"
                style={{ fontSize: 10, color: token.colorTextTertiary }}
                onClick={(e) => {
                  e.stopPropagation();
                  close(t);
                }}
              />
            </div>
          );
        })}
        <NewRequestButton />
      </div>
      <Editor key={id} id={id} where={byId.get(id)} />
    </Layout.Content>
  );
}

function NewRequestButton() {
  const nav = useNavigate();
  const qc = useQueryClient();
  const create = useMutation({
    mutationFn: () => post<RequestDoc>("/api/requests", { name: "Untitled request", method: "GET", url: "{{base_url}}/", folder: "Scratch" }),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["collections"] });
      nav(`/requests/${r.id}`);
    },
  });
  return <Button type="text" icon={<PlusOutlined />} aria-label="New request" loading={create.isPending} onClick={() => create.mutate()} style={{ marginBottom: 2 }} />;
}

function Editor({ id, where }: { id: string; where?: { api: string; folder: string } }) {
  const { token } = theme.useToken();
  const { message } = App.useApp();
  const nav = useNavigate();
  const qc = useQueryClient();
  const me = useMe();
  const { prefs } = usePrefs();
  const envs = useEnvironments();
  const saved = useQuery({ queryKey: ["request", id], queryFn: () => get<RequestDoc>(`/api/requests/${id}`) });
  const [draft, setDraft] = useState<RequestDoc | null>(() => drafts.get(id) ?? null);
  const [tab, setTab] = useState("body");
  const [scriptTab, setScriptTab] = useState<"pre" | "post">("post");
  const [result, setResult] = useState<SendResult | null>(null);
  const [split, setSplit] = useState(() => Number(localStorage.getItem("speklo.split") ?? 0.55));
  const splitRef = useRef(split);
  const abort = useRef<AbortController | null>(null);

  useEffect(() => {
    if (saved.data && !draft) setDraft(saved.data);
  }, [saved.data, draft]);

  const op = useQuery({
    queryKey: ["operation", draft?.apiId, draft?.operationKey],
    queryFn: () => get<OperationDetail>(`/api/apis/${draft!.apiId}/operation?key=${encodeURIComponent(draft!.operationKey)}`),
    enabled: !!draft?.apiId && !!draft?.operationKey,
    retry: false,
  });
  const schema = useMemo(() => (op.data?.fields?.length ? schemaFromFields(op.data.fields) : null), [op.data]);

  const dirty = !!draft && !!saved.data && JSON.stringify({ ...draft, updatedAt: "" }) !== JSON.stringify({ ...saved.data, updatedAt: "" });
  const set = (p: Partial<RequestDoc>) => setDraft((d) => (d ? { ...d, ...p } : d));
  useEffect(() => storeDraft(id, dirty ? draft : null), [id, dirty, draft]);

  const save = useMutation({
    mutationFn: () => put<RequestDoc>(`/api/requests/${id}`, draft),
    onSuccess: (r) => {
      qc.setQueryData(["request", id], r);
      setDraft(r);
      qc.invalidateQueries({ queryKey: ["collections"] });
      message.success("Saved");
    },
    onError: (e: Error) => message.error(e.message),
  });

  const [sending, setSending] = useState(false);
  const send = useCallback(async () => {
    if (!draft || sending) return;
    setSending(true);
    abort.current = new AbortController();
    const envId = envs.data?.activeId;
    const entry = { name: draft.name, method: draft.method, url: draft.url, env: envs.data?.items.find((e) => e.id === envId)?.name ?? "" };
    try {
      const res = await fetch(`/api/requests/${id}/send`, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-Requested-With": "speklo" },
        body: JSON.stringify({ draft, environmentId: envId }),
        signal: abort.current.signal,
      });
      const data = await res.json().catch(() => ({ error: `${res.status} ${res.statusText}` }));
      if (!res.ok) {
        logSend({ ...entry, error: data.error ?? `${res.status} ${res.statusText}` });
        throw new Error(data.error);
      }
      setResult(data.result);
      logSend({ ...entry, env: data.environment || entry.env, result: data.result });
      qc.invalidateQueries({ queryKey: ["history"] });
      qc.invalidateQueries({ queryKey: ["variables"] });
    } catch (e) {
      if ((e as Error).name !== "AbortError") message.error((e as Error).message);
    } finally {
      setSending(false);
    }
  }, [draft, sending, id, envs.data?.activeId, qc, message]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey)) return;
      if (e.key === "s") {
        e.preventDefault();
        if (dirty) save.mutate();
      }
      if (e.key === "Enter" && prefs.sendWithCmdEnter !== false) {
        e.preventDefault();
        send();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [dirty, save, send, prefs.sendWithCmdEnter]);

  if (saved.isError) return <Alert type="error" showIcon message="This request doesn't exist anymore." style={{ margin: 24 }} action={<Button onClick={() => nav("/requests")}>Open another</Button>} />;
  if (!draft) return <Spin style={{ margin: 40 }} />;

  const readOnly = me.data && !me.data.canEdit;
  const urlParts = draft.url.split(/(\{\{[^}]+\}\})/g);
  const counts = { params: draft.params.filter((p) => p.enabled).length, headers: draft.headers.filter((h) => h.enabled).length };

  const configTabs = [
    { key: "params", label: <span>Params {counts.params > 0 && <Typography.Text type="secondary" style={{ fontSize: 12 }}>{counts.params}</Typography.Text>}</span> },
    { key: "auth", label: "Authorization" },
    { key: "headers", label: <span>Headers {counts.headers > 0 && <Typography.Text type="secondary" style={{ fontSize: 12 }}>{counts.headers}</Typography.Text>}</span> },
    { key: "body", label: "Body" },
    { key: "scripts", label: "Scripts" },
    { key: "settings", label: "Settings" },
  ];

  let panel;
  if (tab === "params") panel = <KeyValueTable rows={draft.params} onChange={(params) => set({ params })} readOnly={readOnly} />;
  else if (tab === "headers") panel = <KeyValueTable rows={draft.headers} onChange={(headers) => set({ headers })} readOnly={readOnly} />;
  else if (tab === "auth") {
    const a = draft.auth;
    panel = (
      <div style={{ display: "flex", gap: 32, alignItems: "flex-start" }}>
        <Space direction="vertical" size={16} style={{ width: 520 }}>
          <div>
            <div style={{ marginBottom: 8 }}>Type</div>
            <Select
              value={a.type}
              style={{ width: "100%" }}
              onChange={(type) => set({ auth: { ...a, type } })}
              options={[
                { value: "inherit", label: "Inherit from environment ({{secret_key}})" },
                { value: "none", label: "No auth" },
                { value: "bearer", label: "Bearer token" },
                { value: "basic", label: "Basic auth" },
                { value: "apikey", label: "API key" },
              ]}
            />
          </div>
          {a.type === "bearer" && (
            <div>
              <div style={{ marginBottom: 8 }}>Token</div>
              <Input value={a.token} onChange={(e) => set({ auth: { ...a, token: e.target.value } })} placeholder="{{secret_key}}" style={mono} />
            </div>
          )}
          {a.type === "basic" && (
            <>
              <Input addonBefore="Username" value={a.username} onChange={(e) => set({ auth: { ...a, username: e.target.value } })} />
              <Input.Password addonBefore="Password" value={a.password} onChange={(e) => set({ auth: { ...a, password: e.target.value } })} />
            </>
          )}
          {a.type === "apikey" && (
            <>
              <Input addonBefore="Key" value={a.key} onChange={(e) => set({ auth: { ...a, key: e.target.value } })} placeholder="X-API-Key" />
              <Input addonBefore="Value" value={a.value} onChange={(e) => set({ auth: { ...a, value: e.target.value } })} placeholder="{{api_key}}" style={mono} />
              <Segmented value={a.in ?? "header"} onChange={(v) => set({ auth: { ...a, in: v as "header" | "query" } })} options={[{ value: "header", label: "Header" }, { value: "query", label: "Query" }]} />
            </>
          )}
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            Use {"{{variables}}"} for secrets. Their values come from the active environment, are encrypted in OpenBao, and never leave the server.
          </Typography.Text>
        </Space>
        {a.type === "inherit" && (
          <Alert style={{ flex: 1 }} type="info" showIcon message={`Requests in ${where?.folder ?? "this folder"} send "Authorization: Bearer {{secret_key}}" when the environment defines secret_key.`} />
        )}
      </div>
    );
  } else if (tab === "body") {
    panel = (
      <div style={{ display: "flex", flexDirection: "column", gap: 12, height: "100%" }}>
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
          <Segmented
            value={draft.bodyMode}
            onChange={(v) => set({ bodyMode: v as RequestDoc["bodyMode"] })}
            options={[
              { value: "none", label: "none" },
              { value: "form-urlencoded", label: "urlencoded" },
              { value: "json", label: "JSON" },
              { value: "raw", label: "raw" },
              { value: "graphql", label: "GraphQL" },
            ]}
          />
          <Space size={16}>
            {schema && draft.bodyMode === "json" && <Typography.Text type="secondary">Validated against {op.data?.operationId || "the"} schema</Typography.Text>}
            {draft.bodyMode === "json" && (
              <Typography.Link
                onClick={() => {
                  try {
                    set({ body: JSON.stringify(JSON.parse(draft.body), null, 2) });
                  } catch {
                    message.warning("The body isn't valid JSON yet");
                  }
                }}
              >
                Beautify
              </Typography.Link>
            )}
          </Space>
        </div>
        {draft.bodyMode === "none" ? (
          <Typography.Text type="secondary">This request has no body.</Typography.Text>
        ) : (
          <div style={{ flex: 1, minHeight: 160, border: `1px solid ${token.colorBorder}`, borderRadius: 8, overflow: "hidden" }}>
            <CodeEditor
              path={`speklo://request/${id}.json`}
              language={draft.bodyMode === "json" || draft.bodyMode === "graphql" ? "json" : "plaintext"}
              value={draft.body}
              schema={draft.bodyMode === "json" ? schema : null}
              onChange={(body) => set({ body })}
              onSubmit={send}
              onSave={() => dirty && save.mutate()}
              readOnly={readOnly}
            />
          </div>
        )}
      </div>
    );
  } else if (tab === "scripts") {
    const key = scriptTab === "pre" ? "preScript" : "postScript";
    panel = (
      <div style={{ display: "flex", flexDirection: "column", gap: 12, height: "100%" }}>
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
          <Segmented value={scriptTab} onChange={(v) => setScriptTab(v as "pre" | "post")} options={[{ value: "pre", label: "Pre-request" }, { value: "post", label: "Post-response" }]} />
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            {scriptTab === "pre" ? "req, env.get/set, console.log" : "res.status, res.json, res.time, test(), expect(), env.set"} · runs in a sandbox, 2 s limit
          </Typography.Text>
        </div>
        <div style={{ flex: 1, minHeight: 160, border: `1px solid ${token.colorBorder}`, borderRadius: 8, overflow: "hidden" }}>
          <CodeEditor path={`speklo://script/${id}-${scriptTab}.js`} language="javascript" value={draft[key]} onChange={(v) => set({ [key]: v })} onSubmit={send} readOnly={readOnly} />
        </div>
      </div>
    );
  } else {
    const s = draft.settings;
    const row = (label: string, desc: string, control: React.ReactNode) => (
      <div style={{ display: "flex", alignItems: "center", gap: 16, padding: "14px 0", borderBottom: `1px solid ${token.colorSplit}` }}>
        <div style={{ flex: 1 }}>
          <div style={{ fontWeight: 500 }}>{label}</div>
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            {desc}
          </Typography.Text>
        </div>
        {control}
      </div>
    );
    panel = (
      <div style={{ width: 640 }}>
        {row("Follow redirects", "Follow 3xx responses automatically", <Switch checked={s.followRedirects !== false} onChange={(v) => set({ settings: { ...s, followRedirects: v } })} />)}
        {row("Verify SSL certificates", "Reject self-signed and expired certificates", <Switch checked={s.verifySSL !== false} onChange={(v) => set({ settings: { ...s, verifySSL: v } })} />)}
        {row("Encode URL automatically", "Percent-encode the query before sending", <Switch checked={s.encodeURL !== false} onChange={(v) => set({ settings: { ...s, encodeURL: v } })} />)}
        {row("Request timeout", "Empty uses your default from Preferences", <InputNumber value={s.timeoutMs || null} min={100} max={300000} addonAfter="ms" placeholder={String(prefs.defaultTimeoutMs ?? 30000)} onChange={(v) => set({ settings: { ...s, timeoutMs: v ?? 0 } })} style={{ width: 160 }} />)}
      </div>
    );
  }

  const startDrag = (e: React.MouseEvent) => {
    const box = (e.currentTarget.parentElement as HTMLElement).getBoundingClientRect();
    const move = (ev: MouseEvent) => {
      splitRef.current = Math.min(0.8, Math.max(0.25, (ev.clientY - box.top) / box.height));
      setSplit(splitRef.current);
    };
    const up = () => {
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", up);
      localStorage.setItem("speklo.split", String(splitRef.current));
    };
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", up);
  };

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0, background: token.colorBgContainer }}>
      <div style={{ flex: `0 0 ${split * 100}%`, display: "flex", flexDirection: "column", padding: "16px 24px", gap: 16, minHeight: 0 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <Breadcrumb style={{ flex: 1 }} items={[{ title: where?.api ?? "Requests" }, { title: where?.folder || "Scratch" }, { title: <Input variant="borderless" value={draft.name} onChange={(e) => set({ name: e.target.value })} style={{ fontWeight: 600, padding: 0, width: Math.max(120, draft.name.length * 8 + 20) }} /> }]} />
          {draft.apiId && draft.operationKey && (
            <Button type="text" onClick={() => nav(`/docs/${draft.apiId}?op=${encodeURIComponent(draft.operationKey)}`)}>
              View docs
            </Button>
          )}
          <Button disabled={!dirty || readOnly} loading={save.isPending} onClick={() => save.mutate()}>
            Save <Typography.Text type="secondary" style={{ fontSize: 12, marginLeft: 4 }}>⌘S</Typography.Text>
          </Button>
        </div>
        <div style={{ display: "flex", gap: 8 }}>
          <Space.Compact style={{ flex: 1 }} size="large">
            <Select
              value={draft.method}
              onChange={(method) => set({ method })}
              style={{ width: 120 }}
              labelRender={(o) => <span style={{ ...mono, fontWeight: 700, color: methodColor[String(o.value)] }}>{o.value}</span>}
              options={METHODS.map((m) => ({ value: m, label: <span style={{ ...mono, fontWeight: 700, color: methodColor[m] }}>{m}</span> }))}
            />
            <Input
              value={draft.url}
              onChange={(e) => set({ url: e.target.value })}
              onPressEnter={send}
              style={{ ...mono, fontSize: 14 }}
              placeholder="https://api.example.com/path"
              suffix={urlParts.some((p) => p.startsWith("{{")) ? <Typography.Text type="secondary" style={{ fontSize: 12 }}>{urlParts.filter((p) => p.startsWith("{{")).join(" ")}</Typography.Text> : null}
            />
          </Space.Compact>
          <Space.Compact size="large">
            <Button type="primary" onClick={send} loading={sending} style={{ width: 96, fontWeight: 500 }}>
              Send
            </Button>
            <Dropdown
              trigger={["click"]}
              menu={{
                items: [
                  { key: "save", label: "Save and send", disabled: !dirty },
                  { key: "curl", label: "Copy as cURL" },
                ],
                onClick: ({ key }) => {
                  if (key === "save") save.mutateAsync().then(send);
                  if (key === "curl") {
                    const h = draft.headers.filter((x) => x.enabled).map((x) => ` \\\n  -H '${x.key}: ${x.value}'`).join("");
                    const b = draft.bodyMode !== "none" && draft.body ? ` \\\n  -d '${draft.body.replace(/\s*\n\s*/g, " ")}'` : "";
                    navigator.clipboard.writeText(`curl -X ${draft.method} '${draft.url}'${h}${b}`);
                    message.success("Copied. Variables are left as {{name}}.");
                  }
                },
              }}
            >
              <Button type="primary" icon={<DownOutlined />} aria-label="More send options" />
            </Dropdown>
          </Space.Compact>
        </div>
        <Tabs activeKey={tab} onChange={setTab} items={configTabs} style={{ marginBottom: -16 }} />
        <div style={{ flex: 1, minHeight: 0, overflow: tab === "body" || tab === "scripts" ? "hidden" : "auto" }}>{panel}</div>
      </div>
      <div onMouseDown={startDrag} style={{ height: 6, cursor: "row-resize", borderTop: `1px solid ${token.colorSplit}`, flexShrink: 0 }} />
      <div style={{ flex: 1, minHeight: 0 }}>
        <ResponsePane result={result} sending={sending} onCancel={() => abort.current?.abort()} />
      </div>
    </div>
  );
}
