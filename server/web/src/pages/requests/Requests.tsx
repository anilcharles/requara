import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, App, Breadcrumb, Button, Input, InputNumber, Layout, Space, Spin, Switch, theme, Typography } from "../../ui/kit";
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { Navigate, useNavigate, useParams } from "react-router-dom";
import { api, get, personalActive, post, put, type Field, type OperationDetail, type RequestDoc, type SendResult } from "../../api";
import { mono } from "../../components/bits";
import CodeEditor from "../../components/CodeEditor";
import KeyValueTable from "../../components/KeyValueTable";
import AuthEditor from "../../components/AuthEditor";
import CodeSnippetModal from "../../components/CodeSnippet";
import { BinaryEditor, FormDataEditor } from "../../components/FormDataEditor";
import ExamplesButton from "./Examples";
import WebSocketPanel from "./WebSocket";
import { methodColor } from "../../lib";
import { useCollections } from "../../shell/Layouts";
import { useEnvironments, useMe, usePrefs } from "../../shell/session";
import { Button as UIButton, IconButton, Menu, Tabs, TextInput } from "../../ui";
import { ChevronDown, Code2, MoreHorizontal, Plus, X } from "lucide-react";
import { logSend } from "../../shell/consoleStore";
import ResponsePane from "./ResponsePane";

const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];
const TABS_KEY = "requara.openTabs";

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
  const cols = useCollections();
  if (!id) {
    const first = cols.data?.[0]?.folders[0]?.requests[0];
    if (cols.isLoading) return <Spin style={{ margin: 40 }} />;
    return first ? <Navigate to={`/requests/${first.id}`} replace /> : <EmptyRequests />;
  }
  return <Workspace id={id} />;
}

function Workspace({ id }: { id: string }) {
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
      {/* Open requests: soft pills on the canvas itself, starting on the content's left edge. */}
      <div role="tablist" aria-label="Open requests" style={{ display: "flex", alignItems: "center", gap: 4, height: 42, padding: "0 14px", borderBottom: "1px solid var(--rq-line)", flexShrink: 0, overflowX: "auto" }}>
        {visible.map((t) => {
          const r = byId.get(t);
          const active = t === id;
          return (
            <div
              key={t}
              role="tab"
              aria-selected={active}
              tabIndex={0}
              className="rq-row"
              onClick={() => nav(`/requests/${t}`)}
              onKeyDown={(e) => e.key === "Enter" && nav(`/requests/${t}`)}
              style={{ height: 28, gap: 8, flexShrink: 0, maxWidth: 240 }}
            >
              <span className="rq-mono" style={{ fontSize: 10.5, fontWeight: 500, color: methodColor[r?.method ?? "GET"] }}>{r?.method === "DELETE" ? "DEL" : (r?.method ?? "GET")}</span>
              <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{r?.name ?? "…"}</span>
              {drafts.has(t) && <span title="Unsaved changes" className="rq-dot" style={{ width: 6, height: 6, background: "var(--rq-warn)" }} />}
              <X
                role="button"
                aria-label="Close tab"
                style={{ width: 12, height: 12 }}
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

function EmptyRequests() {
  const nav = useNavigate();
  const qc = useQueryClient();
  const create = useMutation({
    mutationFn: () => post<RequestDoc>("/api/requests", { name: "Untitled request", method: "GET", url: "", folder: "Scratch" }),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["collections"] });
      nav(`/requests/${r.id}`);
    },
  });
  return (
    <div style={{ margin: "80px auto", textAlign: "center" }}>
      <Typography.Title level={4}>No requests yet</Typography.Title>
      <Typography.Paragraph type="secondary">Send a request to any URL, or create or import an API to get one for every endpoint.</Typography.Paragraph>
      <Space>
        <Button type="primary" loading={create.isPending} onClick={() => create.mutate()}>
          New request
        </Button>
        <Button onClick={() => nav("/apis")}>Go to APIs</Button>
      </Space>
    </div>
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
  return <IconButton label="New request" icon={<Plus />} loading={create.isPending} onClick={() => create.mutate()} />;
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
  // Editor and response side by side; split is the editor's share of the width.
  const [split, setSplit] = useState(() => Number(localStorage.getItem("requara.hsplit") ?? 0.5));
  const [showCode, setShowCode] = useState(false);
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
      let data: { result: SendResult; environment: string };
      try {
        data = await api(`/api/requests/${id}/send`, { method: "POST", json: { draft, environmentId: envId }, signal: abort.current.signal });
      } catch (err) {
        if ((err as Error).name !== "AbortError") logSend({ ...entry, error: (err as Error).message });
        throw err;
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


  let panel;
  if (tab === "params") panel = <KeyValueTable rows={draft.params} onChange={(params) => set({ params })} readOnly={readOnly} />;
  else if (tab === "headers") panel = <KeyValueTable rows={draft.headers} onChange={(headers) => set({ headers })} readOnly={readOnly} />;
  else if (tab === "auth") {
    panel = <AuthEditor auth={draft.auth} onChange={(auth) => set({ auth })} inheritLabel={draft.apiId || draft.collectionId ? "Inherit from collection" : "Inherit (Bearer {{secret_key}} when the environment has it)"} readOnly={!!readOnly} collectionId={draft.collectionId ?? draft.apiId} />;
  } else if (tab === "body") {
    panel = (
      <div style={{ display: "flex", flexDirection: "column", gap: 12, height: "100%" }}>
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
          <Tabs
            size="sm"
            label="Body type"
            value={draft.bodyMode}
            onChange={(v) => set({ bodyMode: v as RequestDoc["bodyMode"] })}
            items={[
              { key: "none", label: "None" },
              { key: "json", label: "JSON" },
              { key: "form-urlencoded", label: "URL-encoded" },
              { key: "form-data", label: "Form data" },
              { key: "raw", label: "Raw" },
              { key: "graphql", label: "GraphQL" },
              { key: "binary", label: "Binary" },
            ]}
          />
          <div style={{ display: "flex", alignItems: "center", gap: 12, color: "var(--rq-mute)", fontSize: 12 }}>
            {schema && draft.bodyMode === "json" && <span>Checked against {op.data?.operationId || "the"} schema</span>}
            {draft.bodyMode === "json" && (
              <UIButton
                variant="ghost"
                size="sm"
                onClick={() => {
                  try {
                    set({ body: JSON.stringify(JSON.parse(draft.body), null, 2) });
                  } catch {
                    message.warning("The body isn't valid JSON yet");
                  }
                }}
              >
                Beautify
              </UIButton>
            )}
          </div>
        </div>
        {draft.bodyMode === "none" ? (
          <span style={{ color: "var(--rq-mute)" }}>This request has no body.</span>
        ) : draft.bodyMode === "form-data" ? (
          <div style={{ overflow: "auto" }}>
            <FormDataEditor rows={draft.formData ?? []} onChange={(formData) => set({ formData })} readOnly={!!readOnly} />
          </div>
        ) : draft.bodyMode === "binary" ? (
          <BinaryEditor file={draft.bodyFile} onChange={(bodyFile) => set({ bodyFile })} readOnly={!!readOnly} />
        ) : (
          <div style={{ flex: 1, minHeight: 160, border: "1px solid var(--rq-line)", borderRadius: 8, overflow: "hidden" }}>
            <CodeEditor
              path={`requara://request/${id}.json`}
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
          <Tabs size="sm" label="Script" value={scriptTab} onChange={setScriptTab} items={[{ key: "pre", label: "Before request" }, { key: "post", label: "After response" }]} />
          <span style={{ fontSize: 12, color: "var(--rq-mute)" }}>{scriptTab === "pre" ? "pm.request, pm.variables, pm.sendRequest" : "pm.response, pm.test, pm.expect"} · sandboxed, 2 s</span>
        </div>
        <div style={{ flex: 1, minHeight: 160, border: "1px solid var(--rq-line)", borderRadius: 8, overflow: "hidden" }}>
          <CodeEditor path={`requara://script/${id}-${scriptTab}.js`} language="javascript" value={draft[key]} onChange={(v) => set({ [key]: v })} onSubmit={send} readOnly={readOnly} />
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
        <Typography.Title level={5} style={{ marginTop: 24 }}>
          Network
        </Typography.Title>
        {personalActive() && (
          <Alert type="info" showIcon style={{ marginBottom: 8 }} message="My workspace sends from this browser, which uses its own proxy and certificates. These settings apply when a team workspace sends from your organization's server." />
        )}
        {row("Proxy", "Empty uses the server's (HTTPS_PROXY). http://, https:// or socks5:// URL", <Input value={s.proxy ?? ""} disabled={readOnly} onChange={(e) => set({ settings: { ...s, proxy: e.target.value } })} placeholder="http://proxy.example.com:3128" style={{ ...mono, width: 280 }} />)}
        <div style={{ padding: "14px 0", borderBottom: `1px solid ${token.colorSplit}` }}>
          <div style={{ fontWeight: 500 }}>Client certificate</div>
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            For mutual TLS: the PEM certificate (chain). Use a {"{{variable}}"} to keep it in an environment.
          </Typography.Text>
          <Input.TextArea rows={2} value={s.clientCert ?? ""} disabled={readOnly} onChange={(e) => set({ settings: { ...s, clientCert: e.target.value } })} placeholder="{{client_cert}}" style={{ ...mono, marginTop: 8 }} />
        </div>
        <div style={{ padding: "14px 0", borderBottom: `1px solid ${token.colorSplit}` }}>
          <div style={{ fontWeight: 500 }}>Client certificate key</div>
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            Its unencrypted PEM private key. Keep it in a secret variable.
          </Typography.Text>
          <Input.TextArea rows={2} value={s.clientKey ?? ""} disabled={readOnly} onChange={(e) => set({ settings: { ...s, clientKey: e.target.value } })} placeholder="{{client_key}}" style={{ ...mono, marginTop: 8 }} />
        </div>
        <div style={{ padding: "14px 0", borderBottom: `1px solid ${token.colorSplit}` }}>
          <div style={{ fontWeight: 500 }}>CA certificates</div>
          <Typography.Text type="secondary" style={{ fontSize: 13 }}>
            PEM certificates to trust as well as the usual ones, for internal or self-signed servers.
          </Typography.Text>
          <Input.TextArea rows={2} value={s.caCert ?? ""} disabled={readOnly} onChange={(e) => set({ settings: { ...s, caCert: e.target.value } })} placeholder="-----BEGIN CERTIFICATE-----" style={{ ...mono, marginTop: 8 }} />
        </div>
      </div>
    );
  }

  const startDrag = (e: React.MouseEvent) => {
    const box = (e.currentTarget.parentElement as HTMLElement).getBoundingClientRect();
    const move = (ev: MouseEvent) => {
      splitRef.current = Math.min(0.75, Math.max(0.3, (ev.clientX - box.left) / box.width));
      setSplit(splitRef.current);
    };
    const up = () => {
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", up);
      localStorage.setItem("requara.hsplit", String(splitRef.current));
    };
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", up);
  };

  const saveExample = async (r: SendResult) => {
    const name = window.prompt("Name this example", `${r.status} ${r.statusText || ""}`.trim());
    if (!name) return;
    try {
      await post(`/api/requests/${id}/examples`, { name, status: r.status, headers: r.headers, body: r.body });
      qc.invalidateQueries({ queryKey: ["examples", id] });
      message.success("Saved as an example");
    } catch (e) {
      message.error((e as Error).message);
    }
  };

  const topBar = (
    <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
      <Breadcrumb style={{ flex: 1 }} items={[{ title: where?.api ?? "Requests" }, { title: where?.folder || "Scratch" }, { title: <Input variant="borderless" value={draft.name} onChange={(e) => set({ name: e.target.value })} style={{ fontWeight: 600, padding: 0, width: Math.max(120, draft.name.length * 8 + 20) }} /> }]} />
      <Button disabled={!dirty || readOnly} loading={save.isPending} onClick={() => save.mutate()}>
        Save <Typography.Text type="secondary" style={{ fontSize: 12, marginLeft: 4 }}>⌘S</Typography.Text>
      </Button>
    </div>
  );
  if (draft.kind === "websocket") {
    return (
      <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0, background: token.colorBgContainer }}>
        <div style={{ padding: "16px 24px 0" }}>{topBar}</div>
        <WebSocketPanel draft={draft} set={set} readOnly={!!readOnly} />
      </div>
    );
  }

  const curl = () => {
    const h = draft.headers.filter((x) => x.enabled).map((x) => ` \\\n  -H '${x.key}: ${x.value}'`).join("");
    const b = draft.bodyMode !== "none" && draft.body ? ` \\\n  -d '${draft.body.replace(/\s*\n\s*/g, " ")}'` : "";
    navigator.clipboard.writeText(`curl -X ${draft.method} '${draft.url}'${h}${b}`);
    message.success("Copied. Variables are left as {{name}}.");
  };

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0, background: "var(--rq-canvas)" }}>
      <CodeSnippetModal request={draft} open={showCode} onClose={() => setShowCode(false)} />
      {/* The request reads like a document: where it lives, then its name as a title. */}
      <div style={{ display: "flex", alignItems: "flex-end", gap: 6, padding: "20px 24px 14px" }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontSize: 12, color: "var(--rq-mute)", marginBottom: 3 }}>{[...new Set([where?.api ?? "Requests", where?.folder || "Scratch"])].join("  /  ")}</div>
          <TextInput
            bare
            value={draft.name}
            disabled={!!readOnly}
            onChange={(e) => set({ name: e.target.value })}
            aria-label="Request name"
            style={{ fontSize: 20, fontWeight: 600, letterSpacing: -0.35, lineHeight: 1.2, height: 26 }}
          />
        </div>
        <ExamplesButton requestId={id} />
        <UIButton variant="ghost" icon={<Code2 />} onClick={() => setShowCode(true)}>
          Code
        </UIButton>
        <UIButton disabled={!dirty || !!readOnly} loading={save.isPending} onClick={() => save.mutate()} kbd="⌘S">
          Save
        </UIButton>
      </div>
      {/* Method, address and Send. */}
      <div style={{ display: "flex", gap: 8, padding: "0 24px 16px" }}>
        <div className="rq-field">
          <Menu
            trigger={
              <button type="button" className="rq-method" style={{ color: methodColor[draft.method] }} aria-label={`Method ${draft.method}`} disabled={!!readOnly}>
                {draft.method}
                <ChevronDown />
              </button>
            }
            items={METHODS.map((m) => ({ key: m, label: <span className="rq-mono" style={{ fontWeight: 600, fontSize: 12, color: methodColor[m] }}>{m}</span> }))}
            onSelect={(method) => set({ method })}
          />
          <TextInput
            mono
            value={draft.url}
            onChange={(e) => set({ url: e.target.value })}
            onKeyDown={(e) => e.key === "Enter" && send()}
            placeholder="https://api.example.com/path"
            aria-label="Request URL"
          />
          {urlParts.some((p) => p.startsWith("{{")) && (
            <span className="rq-mono" style={{ padding: "0 12px", fontSize: 11.5, color: "var(--rq-mute)", whiteSpace: "nowrap" }}>
              {urlParts.filter((p) => p.startsWith("{{")).join(" ")}
            </span>
          )}
        </div>
        <UIButton variant="primary" size="lg" loading={sending} onClick={send} kbd="⌘↵">
          Send
        </UIButton>
        <Menu
          align="end"
          trigger={<UIButton variant="ghost" icon={<MoreHorizontal />} aria-label="More send options" style={{ height: 36, width: 32 }} />}
          items={[
            { key: "save", label: "Save and send", disabled: !dirty },
            { key: "curl", label: "Copy as cURL" },
          ]}
          onSelect={(k) => (k === "save" ? save.mutateAsync().then(send) : curl())}
        />
      </div>
      {/* Editor and response side by side. */}
      <div style={{ flex: 1, minHeight: 0, display: "flex", borderTop: "1px solid var(--rq-line)" }}>
        <div style={{ flex: `0 0 ${split * 100}%`, minWidth: 0, display: "flex", flexDirection: "column", padding: "10px 20px 16px 24px" }}>
          <div style={{ marginBottom: 12 }}>
            <Tabs
              label="Request"
              value={tab}
              onChange={setTab}
              items={[
                { key: "params", label: "Params", count: counts.params },
                { key: "headers", label: "Headers", count: counts.headers },
                { key: "auth", label: "Auth" },
                { key: "body", label: "Body" },
                { key: "scripts", label: "Scripts" },
                { key: "settings", label: "Settings" },
              ]}
            />
          </div>
          <div style={{ flex: 1, minHeight: 0, overflow: tab === "body" || tab === "scripts" ? "hidden" : "auto" }}>{panel}</div>
        </div>
        <div onMouseDown={startDrag} role="separator" aria-orientation="vertical" style={{ width: 9, marginLeft: -4, cursor: "col-resize", flexShrink: 0, display: "flex", justifyContent: "center" }}>
          <div style={{ width: 1, background: "var(--rq-line)" }} />
        </div>
        <div style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
          <ResponsePane result={result} sending={sending} onCancel={() => abort.current?.abort()} onSaveExample={readOnly ? undefined : (r) => saveExample(r)} />
        </div>
      </div>
    </div>
  );
}
