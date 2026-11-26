// Thin client for the Speklo backend. Every call carries the session cookie
// automatically; writes add X-Requested-With so the backend's CSRF check passes.

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

export async function api<T = unknown>(path: string, init: RequestInit & { json?: unknown } = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("X-Requested-With", "speklo");
  let body = init.body;
  if (init.json !== undefined) {
    headers.set("Content-Type", "application/json");
    body = JSON.stringify(init.json);
  }
  const res = await fetch(path, { ...init, headers, body, credentials: "same-origin" });
  if (res.status === 401 && !location.pathname.startsWith("/signin")) {
    const back = encodeURIComponent(location.pathname + location.search);
    location.assign(`/signin?redirect=${back}`);
  }
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

export const get = <T,>(path: string) => api<T>(path);

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
};

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
  name: string;
  version: string;
  status: string;
  folders: { name: string; requests: CollectionRequest[] }[];
};

export type KV = { key: string; value: string; description?: string; enabled: boolean };
export type Auth = { type: "inherit" | "none" | "bearer" | "basic" | "apikey"; token?: string; username?: string; password?: string; key?: string; value?: string; in?: "header" | "query" };
export type Settings = { followRedirects?: boolean; verifySSL?: boolean; encodeURL?: boolean; timeoutMs?: number };

export type RequestDoc = {
  id: string;
  apiId: string | null;
  folder: string;
  name: string;
  method: string;
  url: string;
  params: KV[];
  headers: KV[];
  bodyMode: "none" | "json" | "raw" | "form-urlencoded" | "form-data" | "graphql";
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
export type Member = { name: string; email: string; role: string; pending: boolean };
