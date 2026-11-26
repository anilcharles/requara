import { Input, Segmented, Select, Space, Typography } from "antd";
import type { Auth } from "../api";
import { mono } from "./bits";

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
export default function AuthEditor({ auth: a, onChange, inheritLabel, readOnly }: { auth: Auth; onChange: (a: Auth) => void; inheritLabel?: string; readOnly?: boolean }) {
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
              ]}
            />
          </div>
          {(a.grantType ?? "manual") === "manual" ? (
            field("Access token", "token", "{{access_token}}", true)
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
              <Segmented
                value={a.clientAuth ?? "header"}
                disabled={readOnly}
                onChange={(v) => set({ clientAuth: v as "header" | "body" })}
                options={[
                  { value: "header", label: "Client credentials in Basic header" },
                  { value: "body", label: "In the request body" },
                ]}
              />
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
