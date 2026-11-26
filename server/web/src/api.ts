// Client for Requara's API. Two workspaces answer it:
//   - "My workspace" (personal): no account, kept in this browser; the same
//     /api is answered locally by personal/backend.ts.
//   - team workspaces on the organization's server, after signing in.
// Signed out, everything is the personal workspace. Signed in, the workspace
// switcher picks; the choice is remembered in this browser.
import { handle, onMockCall, PERSONAL_ID, prefs as personalPrefs, setPrefs as setPersonalPrefs, specFile } from "./personal/backend";

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

const WORKSPACE_KEY = "requara.workspace";
/** Whether someone is signed in to the organization (known after /api/me). */
let signedIn: boolean | undefined;
export const isSignedIn = () => signedIn === true;
export const selectedWorkspace = () => localStorage.getItem(WORKSPACE_KEY) ?? "";
/** The personal workspace is active: signed out, or picked in the switcher. */
export const personalActive = () => signedIn === false || selectedWorkspace() === PERSONAL_ID;
export function selectWorkspace(id: string) {
  localStorage.setItem(WORKSPACE_KEY, id);
}

/** Calls the organization's server, whatever workspace is active. */
export async function serverApi<T = unknown>(path: string, init: RequestInit & { json?: unknown } = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("X-Requested-With", "requara");
  let body = init.body;
  if (init.json !== undefined) {
    headers.set("Content-Type", "application/json");
    body = JSON.stringify(init.json);
  }
  const res = await fetch(path, { ...init, headers, body, credentials: "same-origin" });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      msg = (await res.json()).error ?? msg;
    } catch {
      /* not JSON */
    }
    throw new ApiError(res.status, msg);
  }
  if (res.status === 204) return undefined as T;
  const type = res.headers.get("Content-Type") ?? "";
  return (type.includes("json") ? res.json() : res.text()) as Promise<T>;
}

// Signed out, these say so instead of failing.
const PERSONAL_ME = {
  id: PERSONAL_ID, email: "", name: "You", username: "", roles: [], groups: [], lastLoginAt: "",
  memberRole: "owner", canEdit: true, canPublish: true, identityProvider: "", accountUrl: "", isAdmin: false,
};

async function me(): Promise<unknown> {
  try {
    const m = await serverApi<Record<string, unknown>>("/api/me");
    signedIn = true;
    if (selectedWorkspace() === PERSONAL_ID || !m.workspaceId) {
      if (!m.workspaceId) selectWorkspace(PERSONAL_ID);
      // Keep the person's own settings; the environment is the personal one.
      return { ...m, workspaceId: PERSONAL_ID, memberRole: "owner", canEdit: true, canPublish: true,
        prefs: { ...(m.prefs as object), activeEnvironmentId: personalPrefs().activeEnvironmentId } };
    }
    return m;
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) {
      signedIn = false;
      return { ...PERSONAL_ME, workspaceId: PERSONAL_ID, prefs: personalPrefs() };
    }
    throw e;
  }
}

async function workspaces(): Promise<unknown> {
  const mine = { id: PERSONAL_ID, name: "My workspace", role: "owner" };
  if (!signedIn) return [mine];
  const team = await serverApi<unknown[]>("/api/workspaces");
  return [mine, ...team];
}

async function preferences(json: Record<string, unknown>): Promise<unknown> {
  // The active environment belongs to the workspace; the rest to the person.
  const { activeEnvironmentId, defaultEnvironmentId, workspaceId, ...rest } = json;
  void workspaceId;
  const local: Record<string, unknown> = {};
  if (activeEnvironmentId !== undefined) local.activeEnvironmentId = activeEnvironmentId;
  if (defaultEnvironmentId !== undefined) local.defaultEnvironmentId = defaultEnvironmentId;
  const p = setPersonalPrefs(signedIn ? local : { ...local, ...rest });
  if (signedIn && Object.keys(rest).length) {
    const server = await serverApi<Record<string, unknown>>("/api/me/preferences", { method: "PATCH", json: rest });
    return { ...server, activeEnvironmentId: p.activeEnvironmentId, defaultEnvironmentId: p.defaultEnvironmentId };
  }
  return p;
}

// Calls that always go to the organization (or need it).
const serverOnly = (path: string) =>
  path.startsWith("/api/admin") || path === "/api/version" || path === "/api/instance" || path.startsWith("/api/me/sessions") ||
  (signedIn === true && path.startsWith("/api/me/keys"));

export async function api<T = unknown>(path: string, init: RequestInit & { json?: unknown } = {}): Promise<T> {
  const method = (init.method ?? "GET").toUpperCase();
  if (path === "/api/me" && method === "GET") return (await me()) as T;
  if (path === "/api/workspaces" && method === "GET") return (await workspaces()) as T;
  if (signedIn === undefined) await me().catch(() => undefined);
  if (signedIn === false) {
    // Signed out there's no organization to ask.
    if (path === "/api/instance") return { org: "", allowWorkspaceCreation: false, historyEnabled: true } as T;
    if (path.startsWith("/api/me/sessions")) return [] as T;
    if (path.startsWith("/api/admin")) throw new ApiError(403, "Sign in to your organization first");
  }
  if (personalActive() && path === "/api/me/preferences" && method === "PATCH") return (await preferences(init.json as Record<string, unknown>)) as T;
  if (personalActive() && path.startsWith("/api/") && !serverOnly(path) && !(signedIn && path === "/api/workspaces")) {
    const { status, data } = await handle(method, path, init.json ?? init.body);
    if (status >= 400) throw new ApiError(status, (data as { error?: string })?.error ?? "Something went wrong");
    return data as T;
  }
  try {
    return await serverApi<T>(path, init);
  } catch (e) {
    // A team page whose session ended: sign in again.
    if (e instanceof ApiError && e.status === 401 && !location.pathname.startsWith("/signin")) {
      location.assign(`/signin?redirect=${encodeURIComponent(location.pathname + location.search)}`);
    }
    throw e;
  }
}

export const get = <T,>(path: string) => api<T>(path);

/** Live mock-server calls: Server-Sent Events from the server, or the personal workspace's own. */
export function subscribeMockCalls(onCall: (c: MockCall) => void, onLive: (live: boolean) => void): () => void {
  if (personalActive()) {
    onLive(true);
    return onMockCall((c) => onCall(c as MockCall));
  }
  const es = new EventSource("/api/mocks/stream");
  es.onopen = () => onLive(true);
  es.onerror = () => onLive(false);
  es.onmessage = (ev) => {
    const e = JSON.parse(ev.data) as { type: string; data: MockCall };
    if (e.type === "mock_call") onCall(e.data);
  };
  return () => es.close();
}

/** Downloads an API's spec (published version, else the draft). */
export async function downloadSpec(apiId: string, format: "yaml" | "json") {
  if (!personalActive()) {
    location.assign(`/api/apis/${apiId}/spec?format=${format}`);
    return;
  }
  const f = await specFile(apiId, format);
  const a = document.createElement("a");
  a.href = URL.createObjectURL(new Blob([f.text], { type: f.type }));
  a.download = f.name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}

// Mock servers' "calls today" is counted in the viewer's time zone.
export const mocksPath = () => `/api/mocks?tz=${encodeURIComponent(Intl.DateTimeFormat().resolvedOptions().timeZone)}`;
export const post = <T,>(path: string, json?: unknown) => api<T>(path, { method: "POST", json: json ?? {} });
export const put = <T,>(path: string, json: unknown) => api<T>(path, { method: "PUT", json });
export const patch = <T,>(path: string, json: unknown) => api<T>(path, { method: "PATCH", json });
export const del = <T,>(path: string) => api<T>(path, { method: "DELETE" });

// ----- types (mirror the Go handlers) -----

export type Prefs = {
  theme?: "light" | "dark" | "system";
  density?: "comfortable" | "compact";
  codeFont?: string;
  codeFontSize?: number;
  activeEnvironmentId?: string;
  defaultEnvironmentId?: string;
  sendWithCmdEnter?: boolean;
  saveResponses?: boolean;
  defaultTimeoutMs?: number;
  notifyPublished?: boolean;
  notifyMockStopped?: boolean;
  weeklyDigest?: boolean;
  workspaceId?: string;
};

export type Me = {
  id: string;
  email: string;
  name: string;
  username: string;
  roles: string[];
  groups: string[];
  prefs: Prefs;
  lastLoginAt: string;
  workspaceId: string;
  memberRole: "owner" | "editor" | "viewer";
  canEdit: boolean;
  canPublish: boolean;
  identityProvider: string;
  /** Where people manage their account at the identity provider; empty if not configured. */
  accountUrl: string;
  /** Instance admin: listed in REQUARA_ADMIN_EMAILS on the server. */
  isAdmin: boolean;
};

export type Banner = { enabled: boolean; message: string; level: "info" | "warning" | "danger" };
export type Instance = { org: string; banner?: Banner; allowWorkspaceCreation: boolean; historyEnabled: boolean };

export type ApiSummary = {
  id: string;
  name: string;
  description: string;
  status: "draft" | "published" | "deprecated";
  version: string;
  draftVersion: string;
  owner: string;
  updatedAt: string;
  endpoints: number;
  hasDraftChanges: boolean;
  calls24h: number;
  p95Ms: number | null;
  errorRate: number | null;
};

export type CollectionRequest = { id: string; name: string; method: string; operationKey: string };
export type Collection = {
  id: string;
  /** api: generated from an API's spec; collection: made or imported; scratch: loose requests. */
  kind: "api" | "collection" | "scratch";
  name: string;
  version: string;
  status: string;
  folders: { name: string; requests: CollectionRequest[] }[];
};

export type KV = { key: string; value: string; description?: string; enabled: boolean };
export type Auth = {
  type: "inherit" | "none" | "bearer" | "basic" | "apikey" | "oauth2" | "jwt" | "awsv4" | "digest";
  token?: string;
  username?: string;
  password?: string;
  key?: string;
  value?: string;
  in?: "header" | "query";
  grantType?: "manual" | "client_credentials" | "password" | "authorization_code";
  authUrl?: string;
  tokenUrl?: string;
  clientId?: string;
  clientSecret?: string;
  scope?: string;
  clientAuth?: "header" | "body";
  pkce?: "S256" | "plain" | "off";
  audience?: string;
  refreshToken?: string;
  expiresAt?: number;
  accessKey?: string;
  secretKey?: string;
  sessionToken?: string;
  region?: string;
  service?: string;
  algorithm?: "HS256" | "HS384" | "HS512";
  secret?: string;
  payload?: string;
  headerPrefix?: string;
};
export type FormField = { key: string; value: string; type?: "text" | "file"; fileName?: string; contentType?: string; enabled: boolean };
export type FileData = { name?: string; contentType?: string; data?: string };
export type WsMessage = { id: string; name: string; body: string };
export type CollectionSettings = { id: string; kind: "api" | "collection"; name: string; description: string; variables: KV[]; auth: Auth; preScript: string; postScript: string };
export type Example = { id: string; name: string; status: number; headers: KV[]; body: string; createdAt: string };
export type Cookie = { domain: string; path: string; name: string; value: string; expires?: string; secure?: boolean; httpOnly?: boolean };
export type Monitor = {
  id: string; name: string; target: string; targetName: string; environmentId: string | null; intervalMin: number; enabled: boolean; notify: string;
  nextRunAt: string; lastRunAt: string | null; lastFailed: number | null; lastErrors: number | null; lastPassed: number | null;
};
export type RunResult = { requestId: string; name: string; method: string; url: string; status: number; durationMs: number; tests: { name: string; pass: boolean; error?: string }[] | null; error?: string };
export type MonitorRun = { id: string; startedAt: string; durationMs: number; requests: number; passed: number; failed: number; errors: number; results: RunResult[] };
export type Settings = { followRedirects?: boolean; verifySSL?: boolean; encodeURL?: boolean; timeoutMs?: number; proxy?: string; clientCert?: string; clientKey?: string; caCert?: string };

export type RequestDoc = {
  id: string;
  apiId: string | null;
  collectionId: string | null;
  kind: "http" | "websocket";
  formData: FormField[];
  bodyFile: FileData | null;
  messages: WsMessage[];
  folder: string;
  name: string;
  method: string;
  url: string;
  params: KV[];
  headers: KV[];
  bodyMode: "none" | "json" | "raw" | "form-urlencoded" | "form-data" | "graphql" | "binary";
  body: string;
  auth: Auth;
  preScript: string;
  postScript: string;
  settings: Settings;
  operationKey: string;
  updatedAt: string;
};

export type TestResult = { name: string; pass: boolean; error?: string; ms: number };
export type Phase = { name: string; startMs: number; ms: number };
export type SendResult = {
  status: number;
  statusText: string;
  durationMs: number;
  sizeBytes: number;
  requestHeaders: KV[];
  requestBody?: string; // as sent, secrets redacted
  headers: KV[];
  body: string;
  truncated: boolean;
  timeline: Phase[];
  tests: TestResult[] | null;
  logs: string[] | null;
  scriptError?: string;
  error?: string;
  url: string;
  method: string;
  cookies?: Cookie[];
};

export type Environment = { id: string; name: string; color: string; isGlobal: boolean; count: number };
export type Variable = { id?: string; key: string; value: string; kind: "default" | "secret" | "script"; enabled: boolean; hasValue?: boolean };

export type Field = {
  name: string;
  type: string;
  format?: string;
  enum?: string[];
  required: boolean;
  description: string;
  depth: number;
  example?: unknown;
  new?: boolean;
};
export type Param = { name: string; in: string; type: string; required: boolean; description: string };
export type ResponseInfo = { status: string; description: string; example: string };
export type Operation = { key: string; method: string; path: string; summary: string; description: string; operationId: string; tag: string; deprecated: boolean };
export type OperationDetail = Operation & { fields: Field[]; params: Param[]; responses: ResponseInfo[]; new: boolean };
export type LintReport = { issues: { rule: string; severity: string; message: string; path: string }[]; passed: string[]; rules: number };
export type Change = { kind: "added" | "changed" | "removed"; text: string; operation: string; breaking: boolean };
export type ChangeReport = { from: string; suggestedVersion: string; changes: Change[]; breaking: boolean };

export type DocsOperation = Operation & {
  params: Param[];
  fields: Field[];
  requestExample: string;
  responses: ResponseInfo[];
  samples: Record<string, string>;
};
export type Docs = { id: string; name: string; version: string; draft: boolean; baseUrl: string; description: string; mockUrl: string; operations: DocsOperation[] };

export type HistoryItem = { id: string; requestId: string | null; method: string; url: string; status: number; durationMs: number; sizeBytes: number; envName: string; error: string; createdAt: string };

export type Mock = { id: string; name: string; apiId: string; apiName: string; apiVersion: string; slug: string; url: string; enabled: boolean; latencyMs: number; errorRate: number; callsToday: number };
export type MockCall = { id: number; method: string; path: string; status: number; mockName: string; createdAt: string };

export type ApiKey = { id: string; name: string; prefix: string; last4: string; scopes: string[]; expiresAt: string | null; lastUsedAt: string | null; revokedAt: string | null; createdAt: string };
export type Session = { id: string; device: string; ip: string; createdAt: string; lastSeenAt: string; current: boolean };
export type Member = { userId: string; name: string; email: string; role: string; pending: boolean };

// ----- admin console -----

export type AdminProvider = { id: string; name: string; type: string; public: boolean; trustEmail: boolean; issuer?: string; allowedOrgs?: string };
export type ServerInfo = {
  version: string;
  org: string;
  url: string;
  providers: AdminProvider[];
  saml: { id: string; name: string; entityId: string; acsUrl: string; metadataUrl: string }[] | null;
  oauthCallback: string;
  allowedDomains: string[];
  admins: string[] | null;
  adminSources: { kind: "env" | "file" | "openbao"; name: string }[] | null;
  adminGroups: string[];
  publisherGroups: string[];
  smtp: boolean;
  auditRetentionDays: number;
  scimUrl: string;
};
export type AdminOverview = {
  counts: {
    users: number;
    activeUsers30d: number;
    deactivated: number;
    admins: number;
    workspaces: number;
    apis: number;
    requests: number;
    sends24h: number;
    pendingInvites: number;
    activeSessions: number;
    signIns24h: number;
    failedSignIns24h: number;
  };
  server: ServerInfo;
};
export type AdminUser = {
  id: string;
  name: string;
  email: string;
  username: string;
  provider: string;
  emailVerified: boolean;
  isAdmin: boolean;
  disabledAt: string | null;
  createdAt: string;
  lastLoginAt: string;
  workspaces: number;
  sessions: number;
  directoryActive: boolean | null;
};
export type AdminUserDetail = {
  user: AdminUser;
  memberships: { workspaceId: string; name: string; role: string }[];
  sessions: { device: string; ip: string; createdAt: string; lastSeenAt: string }[];
};
export type AdminWorkspace = { id: string; name: string; slug: string; autoJoin: boolean; members: number; owners: number; apis: number; requests: number; invites: number; createdAt: string };
export type WorkspaceMember = { userId: string; name: string; email: string; role: string; disabled: boolean; since: string };
export type AdminInvite = { id: string; email: string; role: string; workspaceId: string; workspace: string; invitedBy: string; createdAt: string };
export type AuditEvent = {
  id: number;
  at: string;
  actorKind: string;
  actorId: string;
  actorLabel: string;
  action: string;
  outcome: "success" | "failure";
  targetType: string;
  targetId: string;
  targetLabel: string;
  workspaceId: string | null;
  ip: string;
  userAgent: string;
  details: Record<string, unknown>;
};
export type AdminToken = { id: string; name: string; prefix: string; last4: string; createdBy: string; expiresAt: string | null; lastUsedAt: string | null; revokedAt: string | null; createdAt: string };
export type InstanceSettings = { banner: Banner; historyEnabled: boolean; allowWorkspaceCreation: boolean };
export type DirectoryUser = { id: string; userName: string; email: string; name: string; active: boolean; externalId: string; updatedAt: string };
