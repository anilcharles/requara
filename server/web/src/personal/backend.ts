/* eslint-disable @typescript-eslint/no-explicit-any */
// The personal workspace: the same /api the Requara server offers, answered
// in the browser. Data lives in IndexedDB (db.ts); the API-platform logic
// (OpenAPI, lint, diff, docs, mocks, sending with scripts) is the server's
// own Go code running as WebAssembly (core.ts). Pages don't know which one
// they talk to.
import { core, CoreError, MOCK_BASE } from "./core";
import { db, newId, ready } from "./db";

export const PERSONAL_ID = "personal";

export class LocalError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}
const fail = (status: number, message: string): never => {
  throw new LocalError(status, message);
};

type KV = { key: string; value: string; description?: string; enabled: boolean };
type ApiRow = {
  id: string;
  name: string;
  slug: string;
  description: string;
  owner: string;
  status: "draft" | "published" | "deprecated";
  currentVersion: string;
  draftVersion: string;
  draftSpec: any;
  createdAt: string;
  updatedAt: string;
};
type VersionRow = { id: string; apiId: string; version: string; spec: any; changes: any[]; publishedAt: string };
type RequestRow = {
  id: string;
  apiId: string | null;
  collectionId: string | null;
  kind: "http" | "websocket";
  formData: any[];
  bodyFile: any;
  messages: any[];
  folder: string;
  name: string;
  method: string;
  url: string;
  params: KV[];
  headers: KV[];
  bodyMode: string;
  body: string;
  auth: any;
  preScript: string;
  postScript: string;
  settings: any;
  operationKey: string;
  sort: number;
  createdAt: string;
  updatedAt: string;
};
type EnvRow = { id: string; name: string; color: string; isGlobal: boolean; sort: number };
type VarRow = { id: string; envId: string; key: string; value: string; kind: "default" | "secret" | "script"; enabled: boolean; sort: number };
type HistoryRow = { id: string; requestId: string | null; apiId: string | null; envName: string; createdAt: string; result: any };
type CollectionRow = { id: string; apiId: string | null; name: string; description: string; variables: KV[]; auth: any; preScript: string; postScript: string; sort: number; createdAt: string };
type ExampleRow = { id: string; requestId: string; name: string; status: number; headers: KV[]; body: string; createdAt: string };
type CookieRow = { id: string; domain: string; path: string; name: string; value: string; expires?: string; secure?: boolean; httpOnly?: boolean };
type MockRow = { id: string; apiId: string; name: string; slug: string; enabled: boolean; latencyMs: number; errorRate: number; createdAt: string };
type MockCallRow = { id: string; seq: number; mockId: string; method: string; path: string; status: number; createdAt: string };

const now = () => new Date().toISOString();
const slugify = (s: string) =>
  s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
const normalizeVersion = (v: string) => {
  v = (v ?? "").trim();
  if (!v) return "v1.0.0";
  return v.startsWith("v") ? v : "v" + v;
};
const majorVersion = (v: string) => {
  v = v.replace(/^v/, "");
  const i = v.indexOf(".");
  if (i > 0) v = v.slice(0, i);
  return v ? "v" + v : "";
};
const countOps = (doc: any) => {
  let n = 0;
  for (const item of Object.values<any>(doc?.paths ?? {})) {
    for (const m of Object.keys(item ?? {})) if (["get", "post", "put", "patch", "delete", "head", "options"].includes(m.toLowerCase())) n++;
  }
  return n;
};
const baseURL = (doc: any): string => doc?.servers?.[0]?.url ?? "";
const nonNil = (kv?: KV[]) => kv ?? [];

// ----- personal preferences (kept in this browser) -----

export function prefs(): Record<string, any> {
  return (db.get<any>("meta", "prefs")?.value as Record<string, any>) ?? {};
}
export function setPrefs(p: Record<string, any>) {
  const next = { ...prefs(), ...p };
  db.put("meta", { id: "prefs", value: next });
  return next;
}

// ----- first use -----

let seeded: Promise<void> | null = null;
function init(): Promise<void> {
  seeded ??= (async () => {
    await ready();
    if (db.get("meta", "seeded")) return;
    db.put<EnvRow>("environments", { id: newId(), name: "Globals", color: "#BFBFBF", isGlobal: true, sort: 0 });
    const env = db.put<EnvRow>("environments", { id: newId(), name: "Personal", color: "#1677FF", isGlobal: false, sort: 1 });
    db.put<VarRow>("variables", { id: newId(), envId: env.id, key: "base_url", value: "https://api.github.com", kind: "default", enabled: true, sort: 0 });
    db.put<RequestRow>("requests", {
      ...blankRequest(),
      name: "Example request",
      url: "{{base_url}}/zen",
      postScript: 'test("returns 200", () => expect(res.status).toBe(200));',
    });
    setPrefs({ activeEnvironmentId: env.id });
    db.put("meta", { id: "seeded", value: true });
  })();
  return seeded;
}

function blankRequest(): RequestRow {
  return {
    id: newId(),
    apiId: null,
    collectionId: null,
    kind: "http",
    formData: [],
    bodyFile: null,
    messages: [],
    folder: "Scratch",
    name: "Untitled request",
    method: "GET",
    url: "",
    params: [],
    headers: [],
    bodyMode: "none",
    body: "",
    auth: { type: "inherit" },
    preScript: "",
    postScript: "",
    settings: {},
    operationKey: "",
    sort: 1000,
    createdAt: now(),
    updatedAt: now(),
  };
}

// ----- helpers -----

const apiOr404 = (id: string) => db.get<ApiRow>("apis", id) ?? fail(404, "API not found");
const latest = (apiId: string): VersionRow | undefined =>
  db
    .all<VersionRow>("versions")
    .filter((v) => v.apiId === apiId)
    .sort((a, b) => b.publishedAt.localeCompare(a.publishedAt))[0];

async function addRequestsFor(api: ApiRow, doc: any, urlPrefix: string) {
  const existing = db
    .all<RequestRow>("requests")
    .filter((r) => r.apiId === api.id)
    .map((r) => r.operationKey);
  const fresh = await core<any[]>("requestsFor", { doc, existing, urlPrefix });
  for (const nr of fresh) {
    db.put<RequestRow>("requests", {
      ...blankRequest(),
      apiId: api.id,
      folder: nr.folder,
      name: nr.name,
      method: nr.method,
      url: nr.url,
      params: nr.params,
      headers: nr.headers,
      bodyMode: nr.bodyMode,
      body: nr.body,
      operationKey: nr.operationKey,
      sort: nr.sort,
    });
  }
}

async function saveNewAPI(name: string, description: string, version: string, doc: any): Promise<ApiRow> {
  let slug = slugify(name);
  const taken = db.all<ApiRow>("apis").filter((a) => a.slug === slug || a.slug.startsWith(slug + "-")).length;
  if (taken) slug = `${slug}-${taken + 1}`;
  const api = db.put<ApiRow>("apis", {
    id: newId(),
    name,
    slug,
    description,
    owner: "You",
    status: "draft",
    currentVersion: "",
    draftVersion: version,
    draftSpec: doc,
    createdAt: now(),
    updatedAt: now(),
  });
  await addRequestsFor(api, doc, baseURL(doc));
  return api;
}

/** Variables for a send: globals, then the collection's, then the chosen environment's own. */
function resolveEnv(envId: string, collectionVars: KV[] = []) {
  const envs = db.all<EnvRow>("environments");
  const chosen =
    envs.find((e) => !e.isGlobal && e.id === (envId || prefs().activeEnvironmentId)) ??
    envs.filter((e) => !e.isGlobal).sort((a, b) => a.sort - b.sort)[0];
  const ids = new Set(envs.filter((e) => e.isGlobal).map((e) => e.id));
  if (chosen) ids.add(chosen.id);
  const vars: Record<string, string> = {};
  const secrets: string[] = [];
  const rows = db
    .all<VarRow>("variables")
    .filter((v) => ids.has(v.envId) && v.enabled)
    .sort((a, b) => Number(envs.find((e) => e.id === b.envId)?.isGlobal) - Number(envs.find((e) => e.id === a.envId)?.isGlobal) || a.sort - b.sort);
  const globalIds = new Set(envs.filter((e) => e.isGlobal).map((e) => e.id));
  for (const v of rows.filter((r) => globalIds.has(r.envId))) vars[v.key] = v.value;
  for (const v of collectionVars) if (v.enabled && v.key) vars[v.key] = v.value;
  for (const v of rows.filter((r) => !globalIds.has(r.envId))) vars[v.key] = v.value;
  for (const v of rows) if (v.kind === "secret" && v.value) secrets.push(v.value);
  return { env: chosen, vars, secrets };
}

function mockSpecs() {
  return db.all<MockRow>("mocks").map((m) => {
    const api = db.get<ApiRow>("apis", m.apiId);
    return { slug: m.slug, enabled: m.enabled, latencyMs: m.latencyMs, errorRate: m.errorRate, doc: latest(m.apiId)?.spec ?? api?.draftSpec ?? null };
  });
}

// ----- collections -----

function settingsFor(id: string): (CollectionRow & { kind: "api" | "collection" }) | undefined {
  const c = db.get<CollectionRow>("collections", id) ?? db.all<CollectionRow>("collections").find((x) => x.apiId === id);
  if (c) return { ...c, id: c.apiId ?? c.id, kind: c.apiId ? "api" : "collection" };
  const a = db.get<ApiRow>("apis", id);
  if (a) return { id, apiId: id, name: a.name, description: "", variables: [], auth: { type: "inherit" }, preScript: "", postScript: "", sort: 0, createdAt: a.createdAt, kind: "api" };
  return undefined;
}

function inheritFor(r: RequestRow) {
  const id = r.collectionId ?? r.apiId;
  const c = id ? settingsFor(id) : undefined;
  return { vars: c?.variables ?? [], auth: c?.auth ?? { type: "inherit" }, pre: c?.preScript ?? "", post: c?.postScript ?? "" };
}

const joinScripts = (collection: string, request: string) =>
  !collection?.trim() ? request : !request?.trim() ? collection : `{\n${collection}\n}\n{\n${request}\n}`;

function saveImported(collectionId: string | null, r: any, sort: number, folderDefault = "") {
  const row = db.put<RequestRow>("requests", {
    ...blankRequest(),
    collectionId,
    folder: r.folder || folderDefault,
    name: r.name,
    method: r.method,
    url: r.url,
    params: r.params ?? [],
    headers: r.headers ?? [],
    bodyMode: r.bodyMode || "none",
    body: r.body ?? "",
    formData: r.formData ?? [],
    auth: r.auth?.type ? r.auth : { type: "inherit" },
    preScript: r.preScript ?? "",
    postScript: r.postScript ?? "",
    sort,
  });
  for (const ex of r.examples ?? []) db.put<ExampleRow>("examples", { id: newId(), requestId: row.id, name: ex.name, status: ex.status, headers: ex.headers ?? [], body: ex.body ?? "", createdAt: now() });
  return row;
}

/** Requests of a collection, an API or Scratch, in sidebar order. */
export function collectionRequestIds(target: string): string[] {
  const all = db.all<RequestRow>("requests").filter((r) => (r.kind ?? "http") === "http");
  const rs =
    target === "scratch"
      ? all.filter((r) => !r.apiId && !r.collectionId)
      : db.get("apis", target)
        ? all.filter((r) => r.apiId === target)
        : all.filter((r) => r.collectionId === target);
  rs.sort((a, b) => a.sort - b.sort || a.createdAt.localeCompare(b.createdAt));
  const order: string[] = [];
  const by = new Map<string, RequestRow[]>();
  for (const r of rs) {
    if (!by.has(r.folder)) (order.push(r.folder), by.set(r.folder, []));
    by.get(r.folder)!.push(r);
  }
  return order.flatMap((f) => by.get(f)!.map((r) => r.id));
}

function cookieRows(): CookieRow[] {
  const nowMs = Date.now();
  return db.all<CookieRow>("cookies").filter((c) => !c.expires || Date.parse(c.expires) > nowMs);
}
function saveCookies(cookies: any[]) {
  for (const c of cookies ?? []) {
    const id = `${c.domain}|${c.path}|${c.name}`;
    if (c.expires && Date.parse(c.expires) < Date.now()) db.del("cookies", id);
    else db.put<CookieRow>("cookies", { id, ...c });
  }
}

/** The local agent (requara-agent), when the person paired it. */
function agentSettings() {
  let a: { enabled?: boolean; url?: string; token?: string } | undefined;
  try {
    a = JSON.parse(localStorage.getItem("requara.agent") ?? "null") ?? undefined;
  } catch {
    a = undefined;
  }
  return a?.enabled && a.url && a.token ? { url: a.url, token: a.token } : null;
}

// Live mock calls for the Mock servers page.
type Listener = (call: unknown) => void;
const listeners = new Set<Listener>();
export function onMockCall(l: Listener) {
  listeners.add(l);
  return () => void listeners.delete(l);
}
let callSeq = 0;
function recordMockCalls(calls: { slug: string; method: string; path: string; status: number }[]) {
  for (const c of calls) {
    const m = db.all<MockRow>("mocks").find((x) => x.slug === c.slug);
    if (!m) continue;
    callSeq = Math.max(callSeq, ...db.all<MockCallRow>("mockCalls").map((x) => x.seq), 0) + 1;
    const row = db.put<MockCallRow>("mockCalls", { id: newId(), seq: callSeq, mockId: m.id, method: c.method, path: c.path, status: c.status, createdAt: now() });
    listeners.forEach((l) => l({ id: row.seq, method: row.method, path: row.path, status: row.status, mockName: m.name, createdAt: row.createdAt }));
  }
  // Keep the newest 500.
  const all = db.all<MockCallRow>("mockCalls").sort((a, b) => b.seq - a.seq);
  all.slice(500).forEach((r) => db.del("mockCalls", r.id));
}

function listVariables(envId: string) {
  return db
    .all<VarRow>("variables")
    .filter((v) => v.envId === envId)
    .sort((a, b) => a.sort - b.sort || a.key.localeCompare(b.key))
    .map((v) => ({ id: v.id, key: v.key, value: v.kind === "secret" ? "" : v.value, kind: v.kind, enabled: v.enabled, hasValue: v.value !== "" }));
}

function toRequestDoc(r: RequestRow) {
  return {
    id: r.id,
    apiId: r.apiId,
    collectionId: r.collectionId ?? null,
    kind: r.kind ?? "http",
    formData: r.formData ?? [],
    bodyFile: r.bodyFile ?? null,
    messages: r.messages ?? [],
    folder: r.folder,
    name: r.name,
    method: r.method,
    url: r.url,
    params: nonNil(r.params),
    headers: nonNil(r.headers),
    bodyMode: r.bodyMode,
    body: r.body,
    auth: r.auth,
    preScript: r.preScript,
    postScript: r.postScript,
    settings: r.settings ?? {},
    operationKey: r.operationKey,
    updatedAt: r.updatedAt,
  };
}

// ----- the router -----

type Ctx = { method: string; path: string; query: URLSearchParams; body: any };
type Route = [method: string, pattern: RegExp, handler: (c: Ctx, ...params: string[]) => any | Promise<any>];

const routes: Route[] = [
  // --- APIs ---
  [
    "GET",
    /^\/api\/apis$/,
    (c) => {
      const status = c.query.get("status") ?? "";
      const all = db.all<ApiRow>("apis");
      const dayAgo = Date.now() - 864e5;
      const hist = db.all<HistoryRow>("history").filter((h) => Date.parse(h.createdAt) > dayAgo);
      const items = all
        .filter((a) => !status || a.status === status)
        .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
        .map((a) => {
          const v = latest(a.id);
          const calls = hist.filter((h) => h.apiId === a.id);
          const ok = calls.filter((h) => h.result.status > 0).map((h) => h.result.durationMs as number).sort((x, y) => x - y);
          return {
            id: a.id,
            name: a.name,
            description: a.description,
            status: a.status,
            version: a.currentVersion || a.draftVersion,
            draftVersion: a.draftVersion,
            owner: a.owner,
            updatedAt: a.updatedAt,
            endpoints: countOps(a.draftSpec),
            hasDraftChanges: !v || JSON.stringify(v.spec) !== JSON.stringify(a.draftSpec),
            calls24h: calls.length,
            p95Ms: ok.length ? ok[Math.min(ok.length - 1, Math.floor(ok.length * 0.95))] : null,
            errorRate: calls.length ? calls.filter((h) => h.result.status === 0 || h.result.status >= 500).length / calls.length : null,
          };
        });
      const n = (s: string) => all.filter((a) => a.status === s).length;
      return { items, counts: { all: all.length, published: n("published"), draft: n("draft"), deprecated: n("deprecated") } };
    },
  ],
  [
    "POST",
    /^\/api\/apis$/,
    async (c) => {
      const name = String(c.body?.name ?? "").trim();
      if (!name) fail(400, "Give the API a name");
      const b = await core<any>("blank", { name, baseUrl: c.body.baseUrl ?? "", version: c.body.version ?? "", description: c.body.description ?? "", template: c.body.startFrom === "template" });
      const api = await saveNewAPI(name, b.description, b.version, b.doc);
      return { id: api.id };
    },
  ],
  [
    "POST",
    /^\/api\/apis\/import$/,
    async (c) => {
      let text: string;
      if (c.body instanceof FormData) {
        const f = c.body.get("file");
        if (!(f instanceof Blob)) fail(400, "Choose a file to import");
        text = await (f as Blob).text();
      } else {
        const url = String(c.body?.url ?? "").trim();
        if (!/^https?:\/\//i.test(url)) fail(400, "Enter an http or https URL");
        let res: Response;
        try {
          res = await fetch(url, { credentials: "omit" });
        } catch {
          return fail(400, "Couldn't read the spec: your browser can't fetch that URL (it may not allow other websites, or it's unreachable). Download the file and upload it instead.");
        }
        if (!res.ok) fail(400, `Couldn't read the spec: the server answered ${res.status}`);
        text = await res.text();
      }
      const p = await core<any>("parse", { data: text });
      const api = await saveNewAPI(p.name, p.description, p.version, p.doc);
      return { id: api.id, name: p.name, operations: p.operations };
    },
  ],
  [
    "GET",
    /^\/api\/apis\/([^/]+)$/,
    (_c, id) => {
      const a = apiOr404(id);
      return { id: a.id, name: a.name, slug: a.slug, description: a.description, status: a.status, version: a.currentVersion, draftVersion: a.draftVersion, baseUrl: baseURL(a.draftSpec), owner: a.owner };
    },
  ],
  ["GET", /^\/api\/apis\/([^/]+)\/operations$/, (_c, id) => core("operations", { doc: apiOr404(id).draftSpec })],
  ["GET", /^\/api\/apis\/([^/]+)\/operation$/, (c, id) => core("operation", { draft: apiOr404(id).draftSpec, published: latest(id)?.spec ?? null, key: c.query.get("key") ?? "" })],
  [
    "PUT",
    /^\/api\/apis\/([^/]+)\/operation$/,
    async (c, id) => {
      const a = apiOr404(id);
      const out = await core<any>("updateOperation", { doc: a.draftSpec, edit: c.body });
      db.put<ApiRow>("apis", { ...a, draftSpec: out.doc, updatedAt: now() });
      if (out.key !== c.body.key) {
        const method = String(out.key).split(" ")[0];
        for (const r of db.all<RequestRow>("requests").filter((r) => r.apiId === id && r.operationKey === c.body.key)) {
          db.put<RequestRow>("requests", { ...r, operationKey: out.key, method });
        }
      }
      return { key: out.key };
    },
  ],
  ["GET", /^\/api\/apis\/([^/]+)\/lint$/, (_c, id) => core("lint", { doc: apiOr404(id).draftSpec })],
  [
    "GET",
    /^\/api\/apis\/([^/]+)\/changes$/,
    (_c, id) => {
      const a = apiOr404(id);
      const v = latest(id);
      return core("changes", { draft: a.draftSpec, published: v?.spec ?? null, publishedVersion: v?.version ?? "", draftVersion: a.draftVersion });
    },
  ],
  [
    "POST",
    /^\/api\/apis\/([^/]+)\/publish$/,
    async (c, id) => {
      const a = apiOr404(id);
      const v = latest(id);
      const ch = await core<any>("changes", { draft: a.draftSpec, published: v?.spec ?? null, publishedVersion: v?.version ?? "", draftVersion: a.draftVersion });
      const version = String(c.body?.version ?? "").trim() ? normalizeVersion(c.body.version) : ch.suggestedVersion;
      if (db.all<VersionRow>("versions").some((x) => x.apiId === id && x.version === version)) fail(409, `${version} is already published. Pick a new version.`);
      const { doc } = await core<any>("publish", { doc: a.draftSpec, version });
      db.put<VersionRow>("versions", { id: newId(), apiId: id, version, spec: doc, changes: ch.changes, publishedAt: now() });
      const updated = db.put<ApiRow>("apis", { ...a, draftSpec: doc, currentVersion: version, draftVersion: version, status: a.status === "deprecated" ? a.status : "published", updatedAt: now() });
      await addRequestsFor(updated, doc, "{{base_url}}");
      return { version, changes: ch.changes };
    },
  ],
  [
    "POST",
    /^\/api\/apis\/([^/]+)\/discard$/,
    (_c, id) => {
      const a = apiOr404(id);
      const v = latest(id) ?? fail(400, "Nothing published yet, so there's nothing to go back to");
      db.put<ApiRow>("apis", { ...a, draftSpec: v.spec, draftVersion: v.version, updatedAt: now() });
      return undefined;
    },
  ],
  [
    "GET",
    /^\/api\/apis\/([^/]+)\/docs$/,
    async (c, id) => {
      const a = apiOr404(id);
      const v = latest(id);
      const draft = !v || c.query.get("source") === "draft";
      const doc = draft ? a.draftSpec : v.spec;
      const d = await core<any>("docs", { doc });
      const mock = db
        .all<MockRow>("mocks")
        .filter((m) => m.apiId === id && m.enabled)
        .sort((x, y) => x.createdAt.localeCompare(y.createdAt))[0];
      return { id: a.id, name: a.name, version: draft ? a.draftVersion : v.version, draft, baseUrl: d.baseUrl, description: d.description, mockUrl: mock ? `${MOCK_BASE}/${mock.slug}` : "", operations: d.operations };
    },
  ],

  // --- collections and requests ---
  [
    "GET",
    /^\/api\/collections$/,
    () => {
      const reqs = db.all<RequestRow>("requests");
      const apis = db.all<ApiRow>("apis").sort((a, b) => Number(a.status === "deprecated") - Number(b.status === "deprecated") || a.createdAt.localeCompare(b.createdAt));
      const out: any[] = [];
      const group = (rs: RequestRow[], scratch: boolean) => {
        const folders: { name: string; requests: any[] }[] = [];
        for (const r of rs) {
          const name = scratch && r.folder === "Scratch" ? "" : r.folder;
          let f = folders.find((x) => x.name === name);
          if (!f) folders.push((f = { name, requests: [] }));
          f.requests.push({ id: r.id, name: r.name, method: r.kind === "websocket" ? "WS" : r.method, operationKey: r.operationKey });
        }
        return folders;
      };
      for (const a of apis) {
        const rs = reqs.filter((r) => r.apiId === a.id).sort((x, y) => x.sort - y.sort || x.name.localeCompare(y.name));
        out.push({ id: a.id, kind: "api", name: a.name, version: majorVersion(a.currentVersion || a.draftVersion), status: a.status, folders: group(rs, false) });
      }
      const cols = db.all<CollectionRow>("collections").filter((c) => !c.apiId).sort((a, b) => a.sort - b.sort || a.createdAt.localeCompare(b.createdAt));
      for (const c of cols) {
        const rs = reqs.filter((r) => r.collectionId === c.id).sort((x, y) => x.sort - y.sort || x.createdAt.localeCompare(y.createdAt));
        out.push({ id: c.id, kind: "collection", name: c.name, version: "", status: "draft", folders: group(rs, false) });
      }
      const loose = reqs.filter((r) => !r.apiId && !r.collectionId).sort((x, y) => x.folder.localeCompare(y.folder) || x.sort - y.sort || x.createdAt.localeCompare(y.createdAt));
      if (loose.length) out.push({ id: "scratch", kind: "scratch", name: "Scratch", status: "draft", version: "", folders: group(loose, true) });
      return out;
    },
  ],
  [
    "POST",
    /^\/api\/requests$/,
    (c) => {
      const b = c.body ?? {};
      if (b.collectionId && !db.get("collections", b.collectionId)) fail(400, "Collection not found");
      const siblings = db.all<RequestRow>("requests").filter((x) => x.collectionId === (b.collectionId ?? null) && x.apiId === (b.apiId ?? null));
      const r = db.put<RequestRow>("requests", {
        ...blankRequest(),
        sort: siblings.reduce((m, x) => Math.max(m, x.sort + 1), 0),
        apiId: b.apiId ?? null,
        collectionId: b.collectionId ?? null,
        kind: b.kind === "websocket" ? "websocket" : "http",
        formData: b.formData ?? [],
        bodyFile: b.bodyFile ?? null,
        messages: b.messages ?? [],
        folder: b.folder ?? "",
        name: b.name || "Untitled request",
        method: String(b.method || "GET").toUpperCase(),
        url: b.url ?? "",
        params: nonNil(b.params),
        headers: nonNil(b.headers),
        bodyMode: b.bodyMode || "none",
        body: b.body ?? "",
        auth: b.auth?.type ? b.auth : { type: "inherit" },
        preScript: b.preScript ?? "",
        postScript: b.postScript ?? "",
        settings: b.settings ?? {},
      });
      return toRequestDoc(r);
    },
  ],
  ["GET", /^\/api\/requests\/([^/]+)$/, (_c, id) => toRequestDoc(db.get<RequestRow>("requests", id) ?? fail(404, "Request not found"))],
  [
    "PUT",
    /^\/api\/requests\/([^/]+)$/,
    (c, id) => {
      const r = db.get<RequestRow>("requests", id) ?? fail(404, "Request not found");
      const b = c.body;
      return toRequestDoc(
        db.put<RequestRow>("requests", {
          ...r,
          name: b.name,
          method: String(b.method).toUpperCase(),
          url: b.url,
          params: nonNil(b.params),
          headers: nonNil(b.headers),
          bodyMode: b.bodyMode,
          body: b.body,
          auth: b.auth,
          preScript: b.preScript,
          postScript: b.postScript,
          settings: b.settings ?? {},
          formData: b.formData ?? [],
          bodyFile: b.bodyFile ?? null,
          messages: b.messages ?? [],
          folder: b.folder || r.folder,
          updatedAt: now(),
        }),
      );
    },
  ],
  ["DELETE", /^\/api\/requests\/([^/]+)$/, (_c, id) => void db.del("requests", id)],
  [
    "POST",
    /^\/api\/requests\/([^/]+)\/send$/,
    async (c, id) => {
      const row = db.get<RequestRow>("requests", id) ?? fail(404, "Request not found");
      let d: any = toRequestDoc(row);
      if (c.body?.draft) d = { ...c.body.draft, id: d.id, apiId: d.apiId, collectionId: d.collectionId };
      const inh = inheritFor(row);
      const { env, vars, secrets } = resolveEnv(c.body?.environmentId ?? "", inh.vars);
      Object.assign(vars, c.body?.data ?? {});
      let auth = d.auth;
      if (!auth?.type || auth.type === "inherit") auth = inh.auth;
      if (!auth?.type || auth.type === "inherit") auth = "secret_key" in vars ? { type: "bearer", token: "{{secret_key}}" } : { type: "none" };
      const settings = { ...(d.settings ?? {}) };
      if (!settings.timeoutMs && prefs().defaultTimeoutMs) settings.timeoutMs = prefs().defaultTimeoutMs;
      const out = await core<any>("send", {
        spec: {
          method: d.method, url: d.url, params: nonNil(d.params), headers: nonNil(d.headers), bodyMode: d.bodyMode, body: d.body,
          formData: d.formData ?? [], bodyFile: d.bodyFile ?? null, cookies: cookieRows(),
          auth, preScript: joinScripts(inh.pre, d.preScript), postScript: joinScripts(inh.post, d.postScript), settings,
        },
        vars,
        secrets,
        mocks: mockSpecs(),
        agent: agentSettings(),
      });
      saveCookies(out.result?.cookies);
      const res = out.result;
      if (res.error && !res.status && !String(res.url).startsWith(MOCK_BASE) && !agentSettings()) {
        res.error +=
          " My workspace sends from this browser, so APIs that don't allow other websites (CORS) can't be reached. Run the Requara agent on your computer and pair it in Preferences, or use a team workspace on your organization's server.";
      }
      // Variables the scripts set go into the environment.
      if (env) {
        const existing = db.all<VarRow>("variables").filter((v) => v.envId === env.id);
        for (const [k, v] of Object.entries<string>(out.envChanges ?? {})) {
          const row = existing.find((x) => x.key === k);
          if (row) db.put<VarRow>("variables", { ...row, value: v });
          else db.put<VarRow>("variables", { id: newId(), envId: env.id, key: k, value: v, kind: "script", enabled: true, sort: 1000 });
        }
      }
      recordMockCalls(out.mockCalls ?? []);
      const stored = prefs().saveResponses === false ? { ...res, body: "" } : res;
      const h = db.put<HistoryRow>("history", { id: newId(), requestId: d.id, apiId: d.apiId ?? null, envName: env?.name ?? "", createdAt: now(), result: stored });
      const all = db.all<HistoryRow>("history").sort((a, b) => b.createdAt.localeCompare(a.createdAt));
      all.slice(1000).forEach((x) => db.del("history", x.id));
      return { result: res, historyId: h.id, environment: env?.name ?? "" };
    },
  ],

  // --- OAuth 2.0 authorization code (see server/api/internal/api/oauth2.go) ---
  [
    "POST",
    /^\/api\/oauth2\/(authorize|token)$/,
    (c, step) => {
      const b = c.body ?? {};
      const collectionVars = b.collectionId ? settingsFor(b.collectionId)?.variables ?? [] : [];
      const { vars } = resolveEnv(b.environmentId ?? "", collectionVars);
      const input = { auth: b.auth, vars, redirectUri: b.redirectUri, state: b.state, codeVerifier: b.codeVerifier, code: b.code, agent: agentSettings() };
      return core(step === "authorize" ? "oauth2Authorize" : "oauth2Token", input);
    },
  ],

  // --- collections ---
  [
    "POST",
    /^\/api\/collections$/,
    (c) => {
      const name = String(c.body?.name ?? "").trim();
      if (!name) fail(400, "Give the collection a name");
      const sort = db.all<CollectionRow>("collections").reduce((m, x) => Math.max(m, x.sort + 1), 0);
      const row = db.put<CollectionRow>("collections", { id: newId(), apiId: null, name, description: c.body.description ?? "", variables: [], auth: { type: "inherit" }, preScript: "", postScript: "", sort, createdAt: now() });
      return { id: row.id, name };
    },
  ],
  [
    "POST",
    /^\/api\/collections\/import$/,
    async (c) => {
      const { format, data, collectionId, folder } = c.body ?? {};
      if (format === "curl") {
        const r = await core<any>("parseCurl", { data });
        const col = collectionId && db.get("collections", collectionId) ? collectionId : null;
        const row = saveImported(col, { ...r, folder: folder || (col ? "" : "Scratch") }, 1000);
        return { requestId: row.id, name: row.name };
      }
      const pc = await core<any>("parsePostman", { data });
      const sort = db.all<CollectionRow>("collections").reduce((m, x) => Math.max(m, x.sort + 1), 0);
      const col = db.put<CollectionRow>("collections", { id: newId(), apiId: null, name: pc.name, description: pc.description, variables: pc.variables ?? [], auth: pc.auth, preScript: pc.preScript, postScript: pc.postScript, sort, createdAt: now() });
      (pc.requests ?? []).forEach((r: any, i: number) => saveImported(col.id, r, i));
      return { id: col.id, name: pc.name, requests: (pc.requests ?? []).length };
    },
  ],
  [
    "PATCH",
    /^\/api\/collections\/([^/]+)$/,
    (c, id) => {
      const col = db.get<CollectionRow>("collections", id) ?? fail(404, "Collection not found");
      if (!String(c.body?.name ?? "").trim()) fail(400, "Give the collection a name");
      db.put<CollectionRow>("collections", { ...col, name: String(c.body.name).trim() });
      return undefined;
    },
  ],
  [
    "DELETE",
    /^\/api\/collections\/([^/]+)$/,
    (_c, id) => {
      db.del("collections", id);
      for (const r of db.all<RequestRow>("requests").filter((r) => r.collectionId === id)) db.del("requests", r.id);
      return undefined;
    },
  ],
  [
    "GET",
    /^\/api\/collections\/([^/]+)\/settings$/,
    (_c, id) => {
      const s = settingsFor(id) ?? fail(404, "Collection not found");
      return { id: s.id, kind: s.kind, name: s.name, description: s.description, variables: s.variables, auth: s.auth, preScript: s.preScript, postScript: s.postScript };
    },
  ],
  [
    "PUT",
    /^\/api\/collections\/([^/]+)\/settings$/,
    (c, id) => {
      const cur = settingsFor(id) ?? fail(404, "Collection not found");
      const b = c.body ?? {};
      const existing = db.get<CollectionRow>("collections", id) ?? db.all<CollectionRow>("collections").find((x) => x.apiId === id);
      db.put<CollectionRow>("collections", {
        id: existing?.id ?? newId(),
        apiId: cur.kind === "api" ? cur.id : null,
        name: cur.kind === "api" ? cur.name : String(b.name ?? cur.name).trim() || cur.name,
        description: b.description ?? "",
        variables: b.variables ?? [],
        auth: b.auth?.type ? b.auth : { type: "inherit" },
        preScript: b.preScript ?? "",
        postScript: b.postScript ?? "",
        sort: existing?.sort ?? 0,
        createdAt: existing?.createdAt ?? now(),
      });
      const s = settingsFor(id)!;
      return { id: s.id, kind: s.kind, name: s.name, description: s.description, variables: s.variables, auth: s.auth, preScript: s.preScript, postScript: s.postScript };
    },
  ],
  ["GET", /^\/api\/collections\/([^/]+)\/export$/, (_c, id) => exportCollection(id)],

  // --- examples ---
  ["GET", /^\/api\/requests\/([^/]+)\/examples$/, (_c, id) => db.all<ExampleRow>("examples").filter((e) => e.requestId === id).sort((a, b) => a.createdAt.localeCompare(b.createdAt))],
  [
    "POST",
    /^\/api\/requests\/([^/]+)\/examples$/,
    (c, id) => {
      if (!db.get("requests", id)) fail(404, "Request not found");
      const b = c.body ?? {};
      return { id: db.put<ExampleRow>("examples", { id: newId(), requestId: id, name: b.name || "Example", status: b.status ?? 200, headers: b.headers ?? [], body: b.body ?? "", createdAt: now() }).id };
    },
  ],
  ["DELETE", /^\/api\/examples\/([^/]+)$/, (_c, id) => void db.del("examples", id)],

  // --- cookies ---
  ["GET", /^\/api\/cookies$/, () => cookieRows().map(({ id: _id, ...c }) => c)],
  [
    "DELETE",
    /^\/api\/cookies$/,
    (c) => {
      const q = c.query;
      for (const k of db.all<CookieRow>("cookies")) {
        if ((!q.get("domain") || k.domain === q.get("domain")) && (!q.get("path") || k.path === q.get("path")) && (!q.get("name") || k.name === q.get("name"))) db.del("cookies", k.id);
      }
      return undefined;
    },
  ],

  // --- monitors need a server ---
  ["GET", /^\/api\/monitors$/, () => []],
  ["POST", /^\/api\/monitors$/, () => fail(403, "Monitors run on your organization's server, so they keep running when this browser is closed. Sign in and use a team workspace.")],

  // --- environments ---
  [
    "GET",
    /^\/api\/environments$/,
    () => {
      const vars = db.all<VarRow>("variables");
      const items = db
        .all<EnvRow>("environments")
        .sort((a, b) => Number(a.isGlobal) - Number(b.isGlobal) || a.sort - b.sort || a.name.localeCompare(b.name))
        .map((e) => ({ id: e.id, name: e.name, color: e.color, isGlobal: e.isGlobal, count: vars.filter((v) => v.envId === e.id).length }));
      let active = prefs().activeEnvironmentId ?? "";
      if (!items.some((e) => e.id === active && !e.isGlobal)) active = items.find((e) => !e.isGlobal)?.id ?? "";
      return { items, activeId: active };
    },
  ],
  [
    "POST",
    /^\/api\/environments$/,
    (c) => {
      const name = String(c.body?.name ?? "").trim();
      if (!name) fail(400, "Give the environment a name");
      if (db.all<EnvRow>("environments").some((e) => e.name === name)) fail(409, "An environment with that name already exists");
      return { id: db.put<EnvRow>("environments", { id: newId(), name, color: c.body.color || "#1677FF", isGlobal: false, sort: 100 }).id };
    },
  ],
  ["GET", /^\/api\/environments\/([^/]+)\/variables$/, (_c, id) => (db.get("environments", id) ? listVariables(id) : fail(404, "Environment not found"))],
  [
    "PUT",
    /^\/api\/environments\/([^/]+)\/variables$/,
    (c, id) => {
      if (!db.get("environments", id)) fail(404, "Environment not found");
      const old = db.all<VarRow>("variables").filter((v) => v.envId === id);
      const seen = new Set<string>();
      const next: VarRow[] = [];
      (c.body as any[]).forEach((v, i) => {
        const key = String(v.key ?? "").trim();
        if (!key) return;
        if (seen.has(key)) fail(400, `Variable ${key} appears twice`);
        seen.add(key);
        const kind = v.kind === "secret" || v.kind === "script" ? v.kind : "default";
        const value = kind === "secret" && v.keep ? (old.find((o) => o.key === key)?.value ?? "") : (v.value ?? "");
        next.push({ id: newId(), envId: id, key, value, kind, enabled: !!v.enabled, sort: i });
      });
      old.forEach((o) => db.del("variables", o.id));
      next.forEach((v) => db.put("variables", v));
      return listVariables(id);
    },
  ],

  // --- history ---
  [
    "GET",
    /^\/api\/history$/,
    (c) => {
      const q = (c.query.get("q") ?? "").trim().toLowerCase();
      const method = (c.query.get("method") ?? "").toUpperCase();
      const status = c.query.get("status") ?? "";
      const envName = c.query.get("env") ?? "";
      return db
        .all<HistoryRow>("history")
        .filter((h) => {
          const r = h.result;
          if (q && !String(r.url).toLowerCase().includes(q) && String(r.status) !== q) return false;
          if (method && r.method !== method) return false;
          if (status === "error" ? r.status !== 0 : status && `${Math.floor(r.status / 100)}xx` !== status) return false;
          return !envName || h.envName === envName;
        })
        .sort((a, b) => b.createdAt.localeCompare(a.createdAt))
        .slice(0, 200)
        .map((h) => ({ id: h.id, requestId: h.requestId, method: h.result.method, url: h.result.url, status: h.result.status, durationMs: h.result.durationMs, sizeBytes: h.result.sizeBytes, envName: h.envName, error: h.result.error ?? "", createdAt: h.createdAt }));
    },
  ],
  [
    "GET",
    /^\/api\/history\/([^/]+)$/,
    (_c, id) => {
      const h = db.get<HistoryRow>("history", id) ?? fail(404, "Not found");
      return { requestId: h.requestId, result: h.result };
    },
  ],
  ["DELETE", /^\/api\/history$/, () => void db.clear("history")],

  // --- mock servers ---
  [
    "GET",
    /^\/api\/mocks$/,
    () => {
      const today = new Date();
      today.setHours(0, 0, 0, 0);
      const calls = db.all<MockCallRow>("mockCalls").filter((c) => Date.parse(c.createdAt) >= today.getTime());
      return db
        .all<MockRow>("mocks")
        .sort((a, b) => a.createdAt.localeCompare(b.createdAt))
        .map((m) => {
          const a = db.get<ApiRow>("apis", m.apiId);
          return {
            id: m.id,
            name: m.name,
            apiId: m.apiId,
            apiName: a?.name ?? "(deleted API)",
            apiVersion: a ? a.currentVersion || a.draftVersion : "",
            slug: m.slug,
            url: `${MOCK_BASE}/${m.slug}`,
            enabled: m.enabled,
            latencyMs: m.latencyMs,
            errorRate: m.errorRate,
            callsToday: calls.filter((c) => c.mockId === m.id).length,
          };
        });
    },
  ],
  [
    "POST",
    /^\/api\/mocks$/,
    (c) => {
      const a = db.get<ApiRow>("apis", c.body?.apiId ?? "") ?? fail(400, "Pick an API");
      const name = String(c.body.name ?? "").trim() || a.name + " mock";
      const latencyMs = Number(c.body.latencyMs ?? 0);
      const errorRate = Number(c.body.errorRate ?? 0);
      if (errorRate < 0 || errorRate > 100 || latencyMs < 0 || latencyMs > 10000) fail(400, "Latency is 0–10000 ms and error rate 0–100%");
      let slug = slugify(name);
      const taken = db.all<MockRow>("mocks").filter((m) => m.slug === slug || m.slug.startsWith(slug + "-")).length;
      if (taken) slug = `${slug}-${taken + 1}`;
      const m = db.put<MockRow>("mocks", { id: newId(), apiId: a.id, name, slug, enabled: true, latencyMs, errorRate, createdAt: now() });
      return { id: m.id, url: `${MOCK_BASE}/${slug}` };
    },
  ],
  ["GET", /^\/api\/mocks\/calls$/, () => {
    const names = new Map(db.all<MockRow>("mocks").map((m) => [m.id, m.name]));
    return db
      .all<MockCallRow>("mockCalls")
      .sort((a, b) => b.seq - a.seq)
      .slice(0, 50)
      .map((c) => ({ id: c.seq, method: c.method, path: c.path, status: c.status, mockName: names.get(c.mockId) ?? "", createdAt: c.createdAt }));
  }],
  [
    "PATCH",
    /^\/api\/mocks\/([^/]+)$/,
    (c, id) => {
      const m = db.get<MockRow>("mocks", id) ?? fail(404, "Mock server not found");
      const b = c.body ?? {};
      if ((b.errorRate != null && (b.errorRate < 0 || b.errorRate > 100)) || (b.latencyMs != null && (b.latencyMs < 0 || b.latencyMs > 10000))) fail(400, "Latency is 0–10000 ms and error rate 0–100%");
      db.put<MockRow>("mocks", { ...m, name: b.name ?? m.name, enabled: b.enabled ?? m.enabled, latencyMs: b.latencyMs ?? m.latencyMs, errorRate: b.errorRate ?? m.errorRate });
      return undefined;
    },
  ],

  // --- search ---
  [
    "GET",
    /^\/api\/search$/,
    (c) => {
      const q = (c.query.get("q") ?? "").trim().toLowerCase();
      const apis = db.all<ApiRow>("apis");
      const reqs = db
        .all<RequestRow>("requests")
        .filter((r) => r.name.toLowerCase().includes(q) || r.url.toLowerCase().includes(q))
        .sort((a, b) => a.name.localeCompare(b.name))
        .slice(0, 8)
        .map((r) => ({ kind: "request", id: r.id, title: r.name, meta: apis.find((a) => a.id === r.apiId)?.name ?? "", method: r.method }));
      const as = apis
        .filter((a) => a.name.toLowerCase().includes(q))
        .sort((a, b) => a.name.localeCompare(b.name))
        .slice(0, 5)
        .map((a) => ({ kind: "api", id: a.id, title: a.name, meta: a.status, method: "" }));
      return [...reqs, ...as];
    },
  ],

  // --- account things that need an organization ---
  ["GET", /^\/api\/me\/sessions$/, () => []],
  ["GET", /^\/api\/me\/keys$/, () => []],
  ["POST", /^\/api\/me\/keys$/, () => fail(403, "API keys belong to an organization account. Sign in to your organization to create one.")],
  ["GET", /^\/api\/members$/, () => [{ userId: PERSONAL_ID, name: "You", email: "", role: "owner", pending: false }]],
  ["POST", /^\/api\/members\/invite$/, () => fail(403, "Your personal workspace is just for you. Sign in to your organization and use a team workspace to collaborate.")],
];

/** Answers a /api call for the personal workspace. */
export async function handle(method: string, path: string, body: unknown): Promise<{ status: number; data: unknown }> {
  await init();
  const url = new URL(path, location.origin);
  for (const [m, re, fn] of routes) {
    if (m !== method) continue;
    const match = re.exec(url.pathname);
    if (!match) continue;
    try {
      const data = await fn({ method, path: url.pathname, query: url.searchParams, body }, ...match.slice(1).map(decodeURIComponent));
      return { status: data === undefined ? 204 : method === "POST" && /\/(apis|requests|environments|mocks|apis\/import)$/.test(url.pathname) ? 201 : 200, data };
    } catch (e) {
      if (e instanceof LocalError || e instanceof CoreError) return { status: e.status, data: { error: e.message } };
      console.error(e);
      return { status: 500, data: { error: (e as Error).message || "Something went wrong" } };
    }
  }
  return { status: 404, data: { error: "Not available in your personal workspace" } };
}

/** The published (or draft) spec of a personal API, for downloads. */
export async function specFile(apiId: string, format: "yaml" | "json"): Promise<{ name: string; text: string; type: string }> {
  await init();
  const a = apiOr404(apiId);
  const doc = latest(apiId)?.spec ?? a.draftSpec;
  if (format === "json") return { name: `${a.slug}.json`, text: JSON.stringify(doc, null, 2), type: "application/json" };
  const { text } = await core<{ text: string }>("yaml", { doc });
  return { name: `${a.slug}.yaml`, text, type: "application/yaml" };
}

/** Everything in the personal workspace, for copying it to a team workspace. */
export async function snapshot() {
  await init();
  return {
    apis: db.all<ApiRow>("apis").map((a) => ({ id: a.id, name: a.name, spec: latest(a.id)?.spec ?? a.draftSpec })),
    scratch: db.all<RequestRow>("requests").filter((r) => !r.apiId).map(toRequestDoc),
    environments: db
      .all<EnvRow>("environments")
      .filter((e) => !e.isGlobal)
      .map((e) => ({ name: e.name, color: e.color, variables: db.all<VarRow>("variables").filter((v) => v.envId === e.id) })),
  };
}

/** A personal collection as a Postman Collection v2.1 (JSON text). */
export async function exportCollection(id: string): Promise<{ name: string; text: string }> {
  await init();
  const s = id === "scratch" ? undefined : (settingsFor(id) ?? fail(404, "Collection not found"));
  const requests = collectionRequestIds(id).map((rid) => {
    const r = db.get<RequestRow>("requests", rid)!;
    return {
      folder: r.folder, name: r.name, method: r.method, url: r.url, params: r.params, headers: r.headers, bodyMode: r.bodyMode, body: r.body,
      formData: (r.formData ?? []).map((f: any) => (f.type === "file" ? { ...f, value: "" } : f)), auth: r.auth, preScript: r.preScript, postScript: r.postScript,
      examples: db.all<ExampleRow>("examples").filter((e) => e.requestId === rid).map((e) => ({ name: e.name, status: e.status, headers: e.headers, body: e.body })),
    };
  });
  const c = { name: s?.name ?? "Scratch", description: s?.description ?? "", variables: s?.variables ?? [], auth: s?.auth ?? { type: "inherit" }, preScript: s?.preScript ?? "", postScript: s?.postScript ?? "", requests };
  const { text } = await core<{ text: string }>("exportPostman", c);
  return { name: c.name, text };
}

/** Request ids of a personal collection in run order (for the Collection Runner). */
export async function runOrder(target: string): Promise<string[]> {
  await init();
  return collectionRequestIds(target);
}
