import { Button, Input, Segmented, Select, Space, Typography, message } from "../ui/kit";
import { useEffect, useRef, useState, type ReactNode } from "react";
import type { Auth } from "../api";
import { mono } from "./bits";
import { getAuthorizationCodeToken, redirectUri } from "./oauth2";

const TYPES: { value: Auth["type"]; label: string }[] = [
  { value: "inherit", label: "Inherit from collection" },
  { value: "none", label: "No auth" },
  { value: "bearer", label: "Bearer token" },
  { value: "basic", label: "Basic auth" },
  { value: "apikey", label: "API key" },
  { value: "oauth2", label: "OAuth 2.0" },
  { value: "jwt", label: "JWT bearer" },
  { value: "awsv4", label: "AWS Signature" },
  { value: "digest", label: "Digest auth" },
];

/** Every auth type Requara sends, for a request or a whole collection. */
export default function AuthEditor({ auth: a, onChange, inheritLabel, readOnly, collectionId }: { auth: Auth; onChange: (a: Auth) => void; inheritLabel?: string; readOnly?: boolean; collectionId?: string | null }) {
  const set = (p: Partial<Auth>) => onChange({ ...a, ...p });
  const field = (label: string, key: keyof Auth, placeholder?: string, secret = false) => {
    const Comp = secret ? Input.Password : Input;
    return (
      <div key={key}>
        <div style={{ marginBottom: 6 }}>{label}</div>
        <Comp value={(a[key] as string) ?? ""} onChange={(e) => set({ [key]: e.target.value } as Partial<Auth>)} placeholder={placeholder} style={mono} disabled={readOnly} />
      </div>
    );
  };
  return (
    <Space direction="vertical" size={14} style={{ width: 560, maxWidth: "100%" }}>
      <div>
        <div style={{ marginBottom: 6 }}>Type</div>
        <Select
          value={a.type}
          style={{ width: "100%" }}
          disabled={readOnly}
          onChange={(type) => onChange({ type })}
          options={TYPES.map((t) => (t.value === "inherit" && inheritLabel ? { ...t, label: inheritLabel } : t))}
        />
      </div>
      {a.type === "bearer" && field("Token", "token", "{{token}}", true)}
      {a.type === "basic" && (
        <>
          {field("Username", "username")}
          {field("Password", "password", "", true)}
        </>
      )}
      {a.type === "apikey" && (
        <>
          {field("Key", "key", "X-API-Key")}
          {field("Value", "value", "{{api_key}}", true)}
          <Segmented value={a.in ?? "header"} disabled={readOnly} onChange={(v) => set({ in: v as "header" | "query" })} options={[{ value: "header", label: "Header" }, { value: "query", label: "Query" }]} />
        </>
      )}
      {a.type === "oauth2" && (
        <>
          <div>
            <div style={{ marginBottom: 6 }}>Grant type</div>
            <Segmented
              value={a.grantType ?? "manual"}
              disabled={readOnly}
              onChange={(v) => set({ grantType: v as Auth["grantType"] })}
              options={[
                { value: "manual", label: "Paste a token" },
                { value: "client_credentials", label: "Client credentials" },
                { value: "password", label: "Password" },
                { value: "authorization_code", label: "Authorization code" },
              ]}
            />
          </div>
          {(a.grantType ?? "manual") === "manual" ? (
            field("Access token", "token", "{{access_token}}", true)
          ) : a.grantType === "authorization_code" ? (
            <AuthorizationCode auth={a} set={set} field={field} readOnly={readOnly} collectionId={collectionId} />
          ) : (
            <>
              {field("Access token URL", "tokenUrl", "https://auth.example.com/oauth/token")}
              {field("Client ID", "clientId")}
              {field("Client secret", "clientSecret", "", true)}
              {a.grantType === "password" && (
                <>
                  {field("Username", "username")}
                  {field("Password", "password", "", true)}
                </>
              )}
              {field("Scope", "scope", "read write")}
              <ClientAuth auth={a} set={set} readOnly={readOnly} />
              <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                Requara fetches the token before sending and reuses it until it expires.
              </Typography.Text>
            </>
          )}
        </>
      )}
      {a.type === "jwt" && (
        <>
          <div>
            <div style={{ marginBottom: 6 }}>Algorithm</div>
            <Select value={a.algorithm ?? "HS256"} disabled={readOnly} style={{ width: 160 }} onChange={(algorithm) => set({ algorithm })} options={["HS256", "HS384", "HS512"].map((v) => ({ value: v, label: v }))} />
          </div>
          {field("Secret", "secret", "{{jwt_secret}}", true)}
          <div>
            <div style={{ marginBottom: 6 }}>Payload (JSON)</div>
            <Input.TextArea rows={4} value={a.payload ?? ""} disabled={readOnly} onChange={(e) => set({ payload: e.target.value })} placeholder='{"sub": "{{user_id}}", "exp": 1924992000}' style={mono} />
          </div>
          {field("Header prefix", "headerPrefix", "Bearer")}
        </>
      )}
      {a.type === "awsv4" && (
        <>
          {field("Access key", "accessKey", "{{aws_access_key_id}}")}
          {field("Secret key", "secretKey", "{{aws_secret_access_key}}", true)}
          {field("Session token (optional)", "sessionToken", "", true)}
          {field("Region", "region", "us-east-1")}
          {field("Service", "service", "execute-api")}
        </>
      )}
      {a.type === "digest" && (
        <>
          {field("Username", "username")}
          {field("Password", "password", "", true)}
        </>
      )}
      {a.type !== "none" && a.type !== "inherit" && (
        <Typography.Text type="secondary" style={{ fontSize: 13 }}>
          Use {"{{variables}}"} for secrets and keep their values in an environment.
        </Typography.Text>
      )}
    </Space>
  );
}

type Field = (label: string, key: keyof Auth, placeholder?: string, secret?: boolean) => ReactNode;

function ClientAuth({ auth: a, set, readOnly }: { auth: Auth; set: (p: Partial<Auth>) => void; readOnly?: boolean }) {
  return (
    <Segmented
      value={a.clientAuth ?? "header"}
      disabled={readOnly}
      onChange={(v) => set({ clientAuth: v as "header" | "body" })}
      options={[
        { value: "header", label: "Client credentials in Basic header" },
        { value: "body", label: "In the request body" },
      ]}
    />
  );
}

/** Authorization code (with PKCE): sign in in a popup, keep the tokens on the request. */
function AuthorizationCode({ auth: a, set, field, readOnly, collectionId }: { auth: Auth; set: (p: Partial<Auth>) => void; field: Field; readOnly?: boolean; collectionId?: string | null }) {
  const [busy, setBusy] = useState(false);
  const abort = useRef<AbortController | null>(null);
  useEffect(() => () => abort.current?.abort(), []);
  const signIn = async () => {
    abort.current = new AbortController();
    setBusy(true);
    try {
      const t = await getAuthorizationCodeToken(a, collectionId, abort.current.signal);
      set({ token: t.accessToken, refreshToken: t.refreshToken ?? "", expiresAt: t.expiresAt ?? 0 });
      message.success("Signed in. The token is saved with this request's auth.");
    } catch (e) {
      if (!abort.current?.signal.aborted) message.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const expires = a.expiresAt ? new Date(a.expiresAt * 1000) : null;
  const expired = !!expires && expires.getTime() < Date.now();
  return (
    <>
      {field("Authorization URL", "authUrl", "https://auth.example.com/authorize")}
      {field("Access token URL", "tokenUrl", "https://auth.example.com/oauth/token")}
      {field("Client ID", "clientId")}
      {field("Client secret (leave empty for public clients)", "clientSecret", "", true)}
      {field("Scope", "scope", "openid profile offline_access")}
      {field("Audience (optional)", "audience", "https://api.example.com")}
      <div>
        <div style={{ marginBottom: 6 }}>PKCE</div>
        <Segmented
          value={a.pkce ?? "S256"}
          disabled={readOnly}
          onChange={(v) => set({ pkce: v as Auth["pkce"] })}
          options={[
            { value: "S256", label: "SHA-256" },
            { value: "plain", label: "Plain" },
            { value: "off", label: "Off" },
          ]}
        />
      </div>
      {a.clientSecret ? <ClientAuth auth={a} set={set} readOnly={readOnly} /> : null}
      <div>
        <div style={{ marginBottom: 6 }}>Redirect URI (register it with the identity provider)</div>
        <Typography.Text code copyable style={mono}>
          {redirectUri()}
        </Typography.Text>
      </div>
      <Space wrap>
        <Button type="primary" loading={busy} disabled={readOnly} onClick={signIn}>
          Get new access token
        </Button>
        {busy && <Button onClick={() => abort.current?.abort()}>Cancel</Button>}
        {a.token && !busy && (
          <Button disabled={readOnly} onClick={() => set({ token: "", refreshToken: "", expiresAt: 0 })}>
            Clear token
          </Button>
        )}
      </Space>
      <Typography.Text type={expired && !a.refreshToken ? "danger" : "secondary"} style={{ fontSize: 13 }}>
        {!a.token
          ? "No token yet. Sign in to get one."
          : expires
            ? `${expired ? "Expired" : "Expires"} ${expires.toLocaleString()}${a.refreshToken ? ". Requara refreshes it when it expires." : "."}`
            : "Signed in. The provider didn't say when the token expires."}
      </Typography.Text>
    </>
  );
}
