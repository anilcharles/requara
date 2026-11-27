import { ExportOutlined, KeyOutlined, LeftOutlined, SlidersOutlined, UserOutlined } from "../ui/icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, App, Avatar, Button, Card, Checkbox, Descriptions, Form, Input, InputNumber, Layout, Modal, Segmented, Select, Space, Switch, Table, Tag, theme, Typography } from "../ui/kit";
import dayjs from "dayjs";
import { useState, type ReactNode } from "react";
import { Navigate, useNavigate, useParams } from "react-router-dom";
import { del, get, post, type ApiKey, type Session } from "../api";
import { mono, PageHeader } from "../components/bits";
import { timeAgo } from "../lib";
import { initials } from "../shell/Header";
import { useMe, usePrefs } from "../shell/session";

const SECTIONS = [
  { key: "profile", label: "Profile", icon: <UserOutlined /> },
  { key: "preferences", label: "Preferences", icon: <SlidersOutlined /> },
  { key: "keys", label: "API keys", icon: <KeyOutlined /> },
];

export default function Settings() {
  const { section } = useParams();
  const { token } = theme.useToken();
  const nav = useNavigate();
  if (!SECTIONS.some((s) => s.key === section)) return <Navigate to="/settings/profile" replace />;
  return (
    <>
      <Layout.Sider width={240} style={{ borderRight: `1px solid ${token.colorSplit}`, padding: "16px 8px" }}>
        <div onClick={() => nav("/apis")} style={{ display: "flex", gap: 8, alignItems: "center", padding: "6px 8px 14px", cursor: "pointer", color: token.colorTextSecondary }}>
          <LeftOutlined style={{ fontSize: 12 }} /> Back to workspace
        </div>
        <div style={{ fontSize: 12, color: token.colorTextTertiary, padding: "4px 12px 6px" }}>Account</div>
        {SECTIONS.map((s) => (
          <div
            key={s.key}
            onClick={() => nav(`/settings/${s.key}`)}
            style={{ display: "flex", gap: 10, alignItems: "center", height: 36, padding: "0 12px", borderRadius: 6, cursor: "pointer", background: section === s.key ? token.controlItemBgActive : undefined, color: section === s.key ? token.colorPrimary : undefined, fontWeight: section === s.key ? 500 : 400 }}
          >
            {s.icon}
            {s.label}
          </div>
        ))}
      </Layout.Sider>
      <Layout.Content style={{ overflow: "auto", padding: "28px 40px 40px" }}>
        <div style={{ maxWidth: section === "keys" ? 1000 : 800 }}>{section === "profile" ? <Profile /> : section === "preferences" ? <Preferences /> : <Keys />}</div>
      </Layout.Content>
    </>
  );
}

function Profile() {
  const { token } = theme.useToken();
  const { message, modal } = App.useApp();
  const qc = useQueryClient();
  const me = useMe();
  const sessions = useQuery({ queryKey: ["sessions"], queryFn: () => get<Session[]>("/api/me/sessions") });
  const signOut = useMutation({ mutationFn: (id: string) => del(`/api/me/sessions/${id}`), onSuccess: () => qc.invalidateQueries({ queryKey: ["sessions"] }) });
  const signOutOthers = useMutation({
    mutationFn: () => del<{ signedOut: number }>("/api/me/sessions"),
    onSuccess: (r) => (message.success(`Signed out ${r?.signedOut ?? 0} other sessions`), qc.invalidateQueries({ queryKey: ["sessions"] })),
  });
  const m = me.data;
  if (!m) return null;
  return (
    <>
      <PageHeader title="Profile" subtitle={`Your identity comes from ${m.identityProvider}. Change it there and Requara picks up the changes at your next sign-in.`} />
      <Space direction="vertical" size={16} style={{ width: "100%" }}>
        <Card>
          <div style={{ display: "flex", alignItems: "center", gap: 16 }}>
            <Avatar size={64} style={{ background: token.colorPrimaryBg, color: token.colorPrimaryText, fontSize: 22 }}>
              {initials(m.name)}
            </Avatar>
            <div style={{ flex: 1 }}>
              <Space>
                <Typography.Text strong style={{ fontSize: 20 }}>
                  {m.name}
                </Typography.Text>
                <Tag color="blue">{m.memberRole === "owner" ? "Owner" : m.memberRole === "editor" ? "Editor" : "Viewer"}</Tag>
              </Space>
              <div style={{ color: token.colorTextTertiary }}>{m.email}</div>
            </div>
            {m.accountUrl && (
              <Button icon={<ExportOutlined />} iconPosition="end" href={m.accountUrl} target="_blank" rel="noreferrer">
                Manage account
              </Button>
            )}
          </div>
        </Card>
        <Card title="Identity" extra={<Typography.Text type="secondary">Read-only, synced from {m.identityProvider}</Typography.Text>}>
          <Descriptions column={1} labelStyle={{ width: 180 }}>
            <Descriptions.Item label="Username">{m.username}</Descriptions.Item>
            <Descriptions.Item label="Email">
              {m.email} <Tag color="success" style={{ marginLeft: 8 }}>Verified</Tag>
            </Descriptions.Item>
            <Descriptions.Item label="Identity provider">{m.identityProvider}</Descriptions.Item>
            <Descriptions.Item label="Subject ID">
              <Typography.Text copyable style={mono}>
                {m.id}
              </Typography.Text>
            </Descriptions.Item>
            <Descriptions.Item label="Last sign-in">{dayjs(m.lastLoginAt).format("MMM D, HH:mm")}</Descriptions.Item>
          </Descriptions>
        </Card>
        <Card title="Roles and groups" extra={<Typography.Text type="secondary">Ask your identity provider's admin to change them</Typography.Text>}>
          <Descriptions column={1} labelStyle={{ width: 180 }}>
            <Descriptions.Item label="Roles">
              {m.roles.filter((r) => !r.startsWith("default-roles")).length ? (
                m.roles
                  .filter((r) => !r.startsWith("default-roles"))
                  .map((r) => (
                    <Tag key={r} color="blue">
                      {r}
                    </Tag>
                  ))
              ) : (
                <Typography.Text type="secondary">None</Typography.Text>
              )}
            </Descriptions.Item>
            <Descriptions.Item label="Groups">{m.groups.length ? m.groups.map((g) => <Tag key={g}>{g}</Tag>) : <Typography.Text type="secondary">None</Typography.Text>}</Descriptions.Item>
            <Descriptions.Item label="Can publish APIs">{m.canPublish ? "Yes" : "No (needs a publisher role or group; ask your admin)"}</Descriptions.Item>
          </Descriptions>
        </Card>
        <Card title="Active sessions" styles={{ body: { padding: 0 } }}>
          {sessions.data?.map((s) => (
            <div key={s.id} style={{ display: "flex", alignItems: "center", gap: 12, padding: "14px 24px", borderBottom: `1px solid ${token.colorSplit}` }}>
              <div style={{ flex: 1 }}>
                <div style={{ fontWeight: 500 }}>{s.device}</div>
                <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                  {s.ip} · started {timeAgo(s.createdAt)} · last active {timeAgo(s.lastSeenAt)}
                </Typography.Text>
              </div>
              {s.current ? <Tag color="success">This device</Tag> : <Button type="text" danger onClick={() => signOut.mutate(s.id)}>Sign out</Button>}
            </div>
          ))}
          <div style={{ padding: "12px 24px" }}>
            <Button
              type="link"
              danger
              style={{ padding: 0 }}
              disabled={(sessions.data?.length ?? 0) < 2}
              onClick={() => modal.confirm({ title: "Sign out of all other sessions?", okText: "Sign out", okButtonProps: { danger: true }, onOk: () => signOutOthers.mutateAsync() })}
            >
              Sign out of all other sessions
            </Button>
          </div>
        </Card>
      </Space>
    </>
  );
}

function Row({ label, desc, children, last }: { label: string; desc?: string; children: ReactNode; last?: boolean }) {
  const { token } = theme.useToken();
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 24, padding: "14px 24px", borderBottom: last ? undefined : `1px solid ${token.colorSplit}` }}>
      <div style={{ flex: 1 }}>
        <div style={{ fontWeight: 500 }}>{label}</div>
        {desc && <Typography.Text type="secondary" style={{ fontSize: 13 }}>{desc}</Typography.Text>}
      </div>
      {children}
    </div>
  );
}

function Preferences() {
  const { prefs, setPrefs } = usePrefs();
  const card = { styles: { body: { padding: 0 } } };
  return (
    <>
      <PageHeader title="Preferences" subtitle="Saved to your account and applied on every device." />
      <Space direction="vertical" size={16} style={{ width: "100%" }}>
        <Card title="Appearance" {...card}>
          <Row label="Theme">
            <Segmented value={prefs.theme ?? "dark"} onChange={(v) => setPrefs({ theme: v as "light" })} options={[{ value: "light", label: "Light" }, { value: "dark", label: "Dark" }, { value: "system", label: "System" }]} />
          </Row>
          <Row label="Density" desc="Compact fits more rows in tables and the sidebar.">
            <Segmented value={prefs.density ?? "comfortable"} onChange={(v) => setPrefs({ density: v as "compact" })} options={[{ value: "comfortable", label: "Comfortable" }, { value: "compact", label: "Compact" }]} />
          </Row>
          <Row label="Code font" last>
            <Space.Compact>
              <Select value={prefs.codeFont ?? "Geist Mono Variable"} style={{ width: 170 }} onChange={(v) => setPrefs({ codeFont: v })} options={[{ value: "Geist Mono Variable", label: "Geist Mono" }, ...["JetBrains Mono", "Menlo", "SF Mono", "Fira Code", "Consolas"].map((f) => ({ value: f, label: f }))]} />
              <Select value={prefs.codeFontSize ?? 13} style={{ width: 80 }} onChange={(v) => setPrefs({ codeFontSize: v })} options={[12, 13, 14, 15, 16].map((n) => ({ value: n, label: `${n} px` }))} />
            </Space.Compact>
          </Row>
        </Card>
        <Card title="Requests" {...card}>
          <Row label="Send with ⌘↵" desc="Otherwise ⌘↵ inserts a new line in the body editor.">
            <Switch checked={prefs.sendWithCmdEnter !== false} onChange={(v) => setPrefs({ sendWithCmdEnter: v })} />
          </Row>
          <Row label="Save responses to history" desc="Bodies over 64 KB go to object storage.">
            <Switch checked={prefs.saveResponses !== false} onChange={(v) => setPrefs({ saveResponses: v })} />
          </Row>
          <Row label="Default timeout" last>
            <InputNumber value={prefs.defaultTimeoutMs ?? 30000} min={1000} max={300000} step={1000} addonAfter="ms" style={{ width: 160 }} onChange={(v) => v && setPrefs({ defaultTimeoutMs: v })} />
          </Row>
        </Card>
        <AgentCard card={card} />
        <Card title="Email notifications" {...card}>
          <Row label="An API in my workspace is published">
            <Switch checked={prefs.notifyPublished !== false} onChange={(v) => setPrefs({ notifyPublished: v })} />
          </Row>
          <Row label="A mock server stops">
            <Switch checked={prefs.notifyMockStopped !== false} onChange={(v) => setPrefs({ notifyMockStopped: v })} />
          </Row>
          <Row label="Weekly usage digest" last>
            <Switch checked={!!prefs.weeklyDigest} onChange={(v) => setPrefs({ weeklyDigest: v })} />
          </Row>
        </Card>
      </Space>
    </>
  );
}

type Agent = { enabled: boolean; url: string; token: string };
const AGENT_KEY = "requara.agent";
function loadAgent(): Agent {
  try {
    return { enabled: false, url: "http://127.0.0.1:47321", token: "", ...JSON.parse(localStorage.getItem(AGENT_KEY) ?? "{}") };
  } catch {
    return { enabled: false, url: "http://127.0.0.1:47321", token: "" };
  }
}

/** Pairs this browser with requara-agent, which sends My workspace's requests without CORS limits. */
function AgentCard({ card }: { card: object }) {
  const { message } = App.useApp();
  const [a, setA] = useState<Agent>(loadAgent);
  const [status, setStatus] = useState<"" | "ok" | "down">("");
  const save = (p: Partial<Agent>) => {
    const next = { ...a, ...p };
    setA(next);
    localStorage.setItem(AGENT_KEY, JSON.stringify(next));
  };
  const test = async () => {
    try {
      const r = await fetch(a.url.replace(/\/$/, "") + "/send", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-Requara-Agent-Token": a.token },
        body: JSON.stringify({ method: "GET", url: a.url.replace(/\/$/, "") + "/health", headers: {}, body: "" }),
      });
      if (r.status === 401) throw new Error("The token doesn't match. Copy it again from the agent's window.");
      if (!r.ok) throw new Error(`The agent answered ${r.status}`);
      setStatus("ok");
      message.success("Connected to the Requara agent");
    } catch (e) {
      setStatus("down");
      message.error((e as Error).message.includes("token") ? (e as Error).message : "Couldn't reach the agent. Is requara-agent running?");
    }
  };
  return (
    <Card title="Local agent" {...card}>
      <Typography.Paragraph type="secondary" style={{ margin: "12px 24px" }}>
        In My workspace, requests are sent by this browser, so APIs that don't allow other websites (CORS), cookies and some headers don't work. Run <Typography.Text code>requara-agent</Typography.Text> on your
        computer and paste the token it prints: Requara then sends My workspace's requests through it. Team workspaces send from your organization's server and don't need it.
      </Typography.Paragraph>
      <Row label="Send through the agent">
        <Switch checked={a.enabled} disabled={!a.token} onChange={(enabled) => save({ enabled })} />
      </Row>
      <Row label="Agent address">
        <Input value={a.url} onChange={(e) => save({ url: e.target.value })} style={{ width: 260, fontFamily: "'Geist Mono Variable', ui-monospace, Menlo, monospace" }} />
      </Row>
      <Row label="Pairing token" last>
        <Space.Compact>
          <Input.Password value={a.token} onChange={(e) => save({ token: e.target.value.trim() })} placeholder="ska_…" style={{ width: 260 }} />
          <Button onClick={test} disabled={!a.token}>
            {status === "ok" ? "Connected" : "Test"}
          </Button>
        </Space.Compact>
      </Row>
    </Card>
  );
}

const SCOPES = [
  { value: "collections:read", desc: "Read collections and requests" },
  { value: "collections:write", desc: "Create and edit requests" },
  { value: "runs:write", desc: "Send requests" },
  { value: "metrics:read", desc: "Read history and mock calls" },
  { value: "full access", desc: "Everything you can do" },
];

function Keys() {
  const { token } = theme.useToken();
  const { modal, message } = App.useApp();
  const qc = useQueryClient();
  const keys = useQuery({ queryKey: ["keys"], queryFn: () => get<ApiKey[]>("/api/me/keys") });
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<{ key: string; apiKey: ApiKey } | null>(null);
  const revoke = useMutation({ mutationFn: (id: string) => del(`/api/me/keys/${id}`), onSuccess: () => qc.invalidateQueries({ queryKey: ["keys"] }) });
  const dead = (k: ApiKey) => !!k.revokedAt || (!!k.expiresAt && dayjs(k.expiresAt).isBefore(dayjs()));

  return (
    <>
      <PageHeader title="API keys" subtitle="Personal keys call the Requara API as you. Use them in CI and scripts." extra={<Button type="primary" onClick={() => setCreating(true)}>Create key</Button>} />
      <Alert type="info" showIcon message={<span>Keys inherit your roles and groups. Send them as <Typography.Text code>Authorization: Bearer rk_live_…</Typography.Text>. Revoked keys stop working immediately.</span>} style={{ marginBottom: 16 }} />
      <Table<ApiKey>
        rowKey="id"
        loading={keys.isLoading}
        dataSource={keys.data}
        pagination={false}
        locale={{ emptyText: "No keys yet" }}
        columns={[
          { title: "Name", render: (_, k) => <span style={{ fontWeight: 500, color: dead(k) ? token.colorTextQuaternary : undefined }}>{k.name}</span> },
          { title: "Key", width: 200, render: (_, k) => <span style={{ ...mono, fontSize: 12, color: dead(k) ? token.colorTextQuaternary : token.colorTextSecondary }}>{k.prefix}…{k.last4}</span> },
          { title: "Scopes", render: (_, k) => k.scopes.map((s) => <Tag key={s} color={dead(k) ? undefined : s === "full access" ? "warning" : undefined}>{s}</Tag>) },
          { title: "Last used", width: 120, render: (_, k) => <Typography.Text type="secondary">{k.lastUsedAt ? timeAgo(k.lastUsedAt) : "Never"}</Typography.Text> },
          {
            title: "Expires",
            width: 130,
            render: (_, k) =>
              k.revokedAt ? <Typography.Text type="secondary">Revoked</Typography.Text> : !k.expiresAt ? <Typography.Text type="warning">Never</Typography.Text> : dead(k) ? <Typography.Text type="secondary">Expired</Typography.Text> : dayjs(k.expiresAt).format("MMM D, YYYY"),
          },
          {
            width: 90,
            align: "right",
            render: (_, k) => (
              <Button
                type="link"
                danger
                style={{ padding: 0 }}
                onClick={() =>
                  dead(k)
                    ? revoke.mutate(k.id)
                    : modal.confirm({
                        title: `Revoke ${k.name}?`,
                        content: `Anything using ${k.prefix}…${k.last4} will start getting 401 errors. You can't undo this.`,
                        okText: "Revoke key",
                        okButtonProps: { danger: true },
                        onOk: () => revoke.mutateAsync(k.id).then(() => message.success("Key revoked")),
                      })
                }
              >
                {dead(k) ? "Delete" : "Revoke"}
              </Button>
            ),
          },
        ]}
      />
      <CreateKey open={creating} onClose={() => setCreating(false)} onCreated={(c) => (setCreating(false), setCreated(c), qc.invalidateQueries({ queryKey: ["keys"] }))} />
      <Modal open={!!created} title="Copy your new key" onCancel={() => setCreated(null)} footer={<Button type="primary" onClick={() => setCreated(null)}>Done</Button>}>
        <Alert type="warning" showIcon message="This is the only time you'll see the full key. Store it in your secrets manager." style={{ margin: "12px 0" }} />
        <Input readOnly value={created?.key} style={mono} addonAfter={<Typography.Text copyable={{ text: created?.key ?? "" }} />} />
        <Typography.Text type="secondary" style={{ display: "block", marginTop: 12, fontSize: 13 }}>
          {created?.apiKey.name} · {created?.apiKey.scopes.join(", ")} · {created?.apiKey.expiresAt ? `expires ${dayjs(created.apiKey.expiresAt).format("MMM D, YYYY")}` : "never expires"}
        </Typography.Text>
      </Modal>
    </>
  );
}

function CreateKey({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (c: { key: string; apiKey: ApiKey }) => void }) {
  const [form] = Form.useForm();
  const { message } = App.useApp();
  const create = useMutation({
    mutationFn: (v: { name: string; scopes: string[]; expiresInDays: number }) => post<{ key: string; apiKey: ApiKey }>("/api/me/keys", v),
    onSuccess: (c) => (form.resetFields(), onCreated(c)),
    onError: (e: Error) => message.error(e.message),
  });
  return (
    <Modal open={open} title="Create API key" okText="Create key" onOk={() => form.submit()} onCancel={onClose} confirmLoading={create.isPending} width={520}>
      <Form form={form} layout="vertical" requiredMark={false} initialValues={{ scopes: ["collections:read", "runs:write"], expiresInDays: 90 }} onFinish={(v) => create.mutate(v)} style={{ marginTop: 16 }}>
        <Form.Item name="name" label="Name" rules={[{ required: true, message: "Name the key after where it's used" }]}>
          <Input placeholder="Staging deploy bot" autoFocus />
        </Form.Item>
        <Form.Item name="scopes" label="Scopes" rules={[{ required: true, message: "Pick at least one scope" }]}>
          <Checkbox.Group style={{ display: "flex", flexDirection: "column", gap: 10 }}>
            {SCOPES.map((s) => (
              <Checkbox key={s.value} value={s.value}>
                <span style={{ ...mono, fontSize: 13 }}>{s.value}</span> <Typography.Text type="secondary" style={{ fontSize: 13 }}>{s.desc}</Typography.Text>
              </Checkbox>
            ))}
          </Checkbox.Group>
        </Form.Item>
        <Form.Item name="expiresInDays" label="Expires">
          <Select options={[{ value: 30, label: "In 30 days" }, { value: 90, label: "In 90 days" }, { value: 365, label: "In 1 year" }, { value: 0, label: "Never (not recommended)" }]} />
        </Form.Item>
      </Form>
    </Modal>
  );
}
