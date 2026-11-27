import {
  ApartmentOutlined,
  AuditOutlined,
  CloudServerOutlined,
  DashboardOutlined,
  KeyOutlined,
  LeftOutlined,
  SettingOutlined,
  TeamOutlined,
} from "../../ui/icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, App, Button, Card, Col, Descriptions, Empty, Form, Input, Layout, Modal, Popconfirm, Row, Segmented, Select, Space, Statistic, Switch, Table, Tag, theme, Typography } from "../../ui/kit";
import dayjs from "dayjs";
import { useEffect, useState } from "react";
import { Navigate, useNavigate, useParams } from "react-router-dom";
import { del, get, post, put, type AdminOverview, type AdminToken, type DirectoryUser, type InstanceSettings, type ServerInfo } from "../../api";
import { mono, PageHeader } from "../../components/bits";
import { timeAgo } from "../../lib";
import { useMe } from "../../shell/session";
import AdminAudit from "./AdminAudit";
import AdminUsers from "./AdminUsers";
import AdminWorkspaces from "./AdminWorkspaces";

const SECTIONS = [
  { key: "overview", label: "Overview", icon: <DashboardOutlined /> },
  { key: "users", label: "Users", icon: <TeamOutlined /> },
  { key: "workspaces", label: "Workspaces", icon: <ApartmentOutlined /> },
  { key: "audit", label: "Audit log", icon: <AuditOutlined /> },
  { key: "provisioning", label: "Provisioning (SCIM)", icon: <CloudServerOutlined /> },
  { key: "tokens", label: "Admin tokens", icon: <KeyOutlined /> },
  { key: "settings", label: "Settings", icon: <SettingOutlined /> },
];

export const useServerInfo = () => useQuery({ queryKey: ["admin", "overview"], queryFn: () => get<AdminOverview>("/api/admin/overview"), staleTime: 30_000 });

export default function Admin() {
  const { section } = useParams();
  const { token } = theme.useToken();
  const nav = useNavigate();
  const me = useMe();
  if (me.data && !me.data.isAdmin) {
    return (
      <Layout.Content style={{ padding: 48 }}>
        <Alert
          type="info"
          showIcon
          message="The admin console is for instance admins"
          description="Admins are named by whoever runs this Requara server, with REQUARA_ADMIN_EMAILS or REQUARA_ADMIN_EMAILS_FILE (a Docker or Kubernetes secret). Ask them to add your email."
        />
      </Layout.Content>
    );
  }
  if (!SECTIONS.some((s) => s.key === section)) return <Navigate to="/admin/overview" replace />;
  return (
    <>
      <Layout.Sider width={240} style={{ borderRight: `1px solid ${token.colorSplit}`, padding: "16px 8px" }}>
        <div onClick={() => nav("/apis")} style={{ display: "flex", gap: 8, alignItems: "center", padding: "6px 8px 14px", cursor: "pointer", color: token.colorTextSecondary }}>
          <LeftOutlined style={{ fontSize: 12 }} /> Back to workspace
        </div>
        <div style={{ fontSize: 12, color: token.colorTextTertiary, padding: "4px 12px 6px" }}>Admin console</div>
        {SECTIONS.map((s) => (
          <div
            key={s.key}
            onClick={() => nav(`/admin/${s.key}`)}
            style={{
              display: "flex",
              gap: 10,
              alignItems: "center",
              height: 36,
              padding: "0 12px",
              borderRadius: 6,
              cursor: "pointer",
              background: section === s.key ? token.controlItemBgActive : undefined,
              color: section === s.key ? token.colorPrimary : undefined,
              fontWeight: section === s.key ? 500 : 400,
            }}
          >
            {s.icon}
            {s.label}
          </div>
        ))}
      </Layout.Sider>
      <Layout.Content style={{ overflow: "auto", padding: "28px 40px 48px" }}>
        <div style={{ maxWidth: 1180 }}>
          {section === "overview" && <Overview />}
          {section === "users" && <AdminUsers />}
          {section === "workspaces" && <AdminWorkspaces />}
          {section === "audit" && <AdminAudit />}
          {section === "provisioning" && <Provisioning />}
          {section === "tokens" && <Tokens />}
          {section === "settings" && <SettingsSection />}
        </div>
      </Layout.Content>
    </>
  );
}

function Overview() {
  const ov = useServerInfo();
  const nav = useNavigate();
  const c = ov.data?.counts;
  const stat = (title: string, value: number | undefined, to?: string, suffix?: string) => (
    <Col xs={12} md={6}>
      <Card size="small" hoverable={!!to} onClick={to ? () => nav(to) : undefined}>
        <Statistic title={title} value={value ?? "—"} suffix={suffix} loading={ov.isLoading} />
      </Card>
    </Col>
  );
  return (
    <>
      <PageHeader title="Overview" subtitle={ov.data ? `${ov.data.server.org} · Requara ${ov.data.server.version}` : " "} />
      <Row gutter={[12, 12]}>
        {stat("People", c?.users, "/admin/users")}
        {stat("Active in 30 days", c?.activeUsers30d, "/admin/users")}
        {stat("Workspaces", c?.workspaces, "/admin/workspaces")}
        {stat("Pending invites", c?.pendingInvites, "/admin/users")}
        {stat("Sign-ins (24 h)", c?.signIns24h, "/admin/audit")}
        {stat("Failed sign-ins (24 h)", c?.failedSignIns24h, "/admin/audit")}
        {stat("Requests sent (24 h)", c?.sends24h)}
        {stat("Active sessions", c?.activeSessions)}
      </Row>
      {ov.data && <ServerConfig info={ov.data.server} />}
    </>
  );
}

/** What admins can see but only change on the server (env vars and secrets). */
function ServerConfig({ info }: { info: ServerInfo }) {
  const { token } = theme.useToken();
  const sources = info.adminSources ?? [];
  const code = (v: string) => <Typography.Text style={{ ...mono, fontSize: 12 }} copyable>{v}</Typography.Text>;
  return (
    <>
      <Typography.Title level={5} style={{ marginTop: 32 }}>
        Server configuration
      </Typography.Title>
      <Typography.Paragraph type="secondary" style={{ marginTop: -4 }}>
        Sign-in, who may join and who the admins are come from the server's environment and secrets, so they can't be changed from a browser. Change them in your Docker
        Compose <code>.env</code>, Kubernetes Secret or OpenBao and restart.
      </Typography.Paragraph>
      <Card size="small">
        <Descriptions column={1} size="small" labelStyle={{ width: 220, color: token.colorTextSecondary }}>
          <Descriptions.Item label="Instance admins">
            <Space direction="vertical" size={4}>
              <Space wrap size={[4, 4]}>
                {(info.admins ?? []).map((a) => (
                  <Tag key={a}>{a}</Tag>
                ))}
                {!info.admins?.length && <Typography.Text type="warning">None. Set REQUARA_ADMIN_EMAILS.</Typography.Text>}
              </Space>
              {sources.length > 0 && (
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  From {sources.map((s) => (s.kind === "env" ? `env ${s.name}` : s.kind === "file" ? `file ${s.name}` : `OpenBao ${s.name}`)).join(" + ")}
                </Typography.Text>
              )}
            </Space>
          </Descriptions.Item>
          <Descriptions.Item label="Sign-in providers">
            <Space wrap size={[4, 4]}>
              {info.providers.map((p) => (
                <Tag key={p.id} color={p.public ? "orange" : undefined} title={p.public ? "Anyone with an account here can sign in unless domains, orgs or invites limit it" : undefined}>
                  {p.name} <span style={{ opacity: 0.6 }}>({p.type})</span>
                </Tag>
              ))}
            </Space>
          </Descriptions.Item>
          <Descriptions.Item label="Who may join">
            {info.allowedDomains.includes("*") ? (
              <Typography.Text type="warning">Anyone the providers let in (REQUARA_ALLOWED_DOMAINS=*)</Typography.Text>
            ) : (
              <span>
                Email domains {info.allowedDomains.join(", ") || "none"}, plus invited and provisioned (SCIM) people
              </span>
            )}
          </Descriptions.Item>
          <Descriptions.Item label="OAuth / OIDC callback URL">{code(info.oauthCallback)}</Descriptions.Item>
          {(info.saml ?? []).map((s) => (
            <Descriptions.Item key={s.id} label={`SAML: ${s.name}`}>
              <Space direction="vertical" size={2}>
                <span>ACS URL {code(s.acsUrl)}</span>
                <span>Entity ID {code(s.entityId)}</span>
                <span>Metadata {code(s.metadataUrl)}</span>
              </Space>
            </Descriptions.Item>
          ))}
          <Descriptions.Item label="Workspace owners by group">{info.adminGroups.join(", ") || "—"}</Descriptions.Item>
          <Descriptions.Item label="Publishers by group">{info.publisherGroups.join(", ") || "—"}</Descriptions.Item>
          <Descriptions.Item label="Email (SMTP)">{info.smtp ? <Tag color="success">Configured</Tag> : <Tag>Not configured: invites show a link to share instead</Tag>}</Descriptions.Item>
          <Descriptions.Item label="Audit log retention">{info.auditRetentionDays ? `${info.auditRetentionDays} days` : "Forever"}</Descriptions.Item>
        </Descriptions>
      </Card>
    </>
  );
}

function Provisioning() {
  const ov = useServerInfo();
  const nav = useNavigate();
  const dir = useQuery({ queryKey: ["admin", "directory"], queryFn: () => get<DirectoryUser[]>("/api/admin/directory") });
  return (
    <>
      <PageHeader title="Provisioning (SCIM)" subtitle="Let your identity provider create and deactivate Requara accounts" />
      <Card size="small" style={{ marginBottom: 20 }}>
        <Descriptions column={1} size="small" labelStyle={{ width: 180 }}>
          <Descriptions.Item label="SCIM base URL">
            <Typography.Text style={mono} copyable>
              {ov.data?.server.scimUrl ?? "…"}
            </Typography.Text>
          </Descriptions.Item>
          <Descriptions.Item label="Authentication">
            HTTP header bearer token: an <a onClick={() => nav("/admin/tokens")}>admin token</a>
          </Descriptions.Item>
          <Descriptions.Item label="Supported">Users: create, update, deactivate, delete; filter by userName, externalId or email. Groups aren't provisioned.</Descriptions.Item>
        </Descriptions>
        <Typography.Paragraph type="secondary" style={{ margin: "12px 0 0" }}>
          A provisioned person may sign in whatever the allowed domains are. When your directory deactivates or removes them, their Requara account is deactivated and
          signed out everywhere. Instance admins are managed on the server and are never deactivated by SCIM.
        </Typography.Paragraph>
      </Card>
      <Table
        size="middle"
        rowKey="id"
        loading={dir.isLoading}
        dataSource={dir.data ?? []}
        locale={{ emptyText: <Empty description="Nobody provisioned yet" /> }}
        columns={[
          { title: "User name", dataIndex: "userName" },
          { title: "Name", dataIndex: "name" },
          { title: "Email", dataIndex: "email" },
          { title: "Status", dataIndex: "active", render: (a: boolean) => (a ? <Tag color="success">Active</Tag> : <Tag color="error">Deactivated</Tag>) },
          { title: "Updated", dataIndex: "updatedAt", render: (v: string) => timeAgo(v) },
        ]}
      />
    </>
  );
}

function Tokens() {
  const { message } = App.useApp();
  const qc = useQueryClient();
  const tokens = useQuery({ queryKey: ["admin", "tokens"], queryFn: () => get<AdminToken[]>("/api/admin/tokens") });
  const [open, setOpen] = useState(false);
  const [created, setCreated] = useState<string | null>(null);
  const [form] = Form.useForm<{ name: string; expires: number }>();
  const create = useMutation({
    mutationFn: (v: { name: string; expires: number }) => post<{ token: string }>("/api/admin/tokens", { name: v.name, expiresInDays: v.expires }),
    onSuccess: (r) => {
      setCreated(r.token);
      setOpen(false);
      form.resetFields();
      qc.invalidateQueries({ queryKey: ["admin", "tokens"] });
    },
    onError: (e: Error) => message.error(e.message),
  });
  const revoke = useMutation({
    mutationFn: (id: string) => del(`/api/admin/tokens/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin", "tokens"] }),
  });
  return (
    <>
      <PageHeader
        title="Admin tokens"
        subtitle="For SCIM provisioning and automation against the admin API (Authorization: Bearer sa_…)"
        extra={
          <Button type="primary" onClick={() => setOpen(true)}>
            New token
          </Button>
        }
      />
      <Alert
        type="warning"
        showIcon
        style={{ marginBottom: 16 }}
        message="An admin token can do everything an admin can, except manage admin tokens. Give each integration its own token and an expiry."
      />
      <Table
        size="middle"
        rowKey="id"
        loading={tokens.isLoading}
        dataSource={tokens.data ?? []}
        columns={[
          { title: "Name", dataIndex: "name" },
          { title: "Token", render: (_, t) => <span style={mono}>{`${t.prefix}…${t.last4}`}</span> },
          { title: "Created", render: (_, t) => `${timeAgo(t.createdAt)}${t.createdBy ? ` by ${t.createdBy}` : ""}` },
          { title: "Last used", dataIndex: "lastUsedAt", render: (v: string | null) => timeAgo(v) },
          {
            title: "Status",
            render: (_, t) =>
              t.revokedAt ? (
                <Tag>Revoked</Tag>
              ) : t.expiresAt && dayjs(t.expiresAt).isBefore(dayjs()) ? (
                <Tag>Expired</Tag>
              ) : (
                <Tag color="success">{t.expiresAt ? `Expires ${dayjs(t.expiresAt).format("MMM D, YYYY")}` : "No expiry"}</Tag>
              ),
          },
          {
            title: "",
            align: "right",
            render: (_, t) =>
              !t.revokedAt && (
                <Popconfirm title={`Revoke ${t.name}?`} description="Anything using it stops working right away." okText="Revoke" okButtonProps={{ danger: true }} onConfirm={() => revoke.mutate(t.id)}>
                  <Button size="small" danger>
                    Revoke
                  </Button>
                </Popconfirm>
              ),
          },
        ]}
      />
      <Modal title="New admin token" open={open} onCancel={() => setOpen(false)} okText="Create token" confirmLoading={create.isPending} onOk={() => form.submit()}>
        <Form form={form} layout="vertical" initialValues={{ expires: 90 }} onFinish={(v) => create.mutate(v)}>
          <Form.Item name="name" label="Name" rules={[{ required: true, message: "Name the token after what uses it" }]}>
            <Input placeholder="Okta SCIM" />
          </Form.Item>
          <Form.Item name="expires" label="Expires">
            <Select
              options={[
                { value: 7, label: "In 7 days" },
                { value: 30, label: "In 30 days" },
                { value: 90, label: "In 90 days" },
                { value: 365, label: "In a year" },
                { value: 0, label: "Never" },
              ]}
            />
          </Form.Item>
        </Form>
      </Modal>
      <Modal title="Copy your admin token" open={!!created} onCancel={() => setCreated(null)} footer={<Button type="primary" onClick={() => setCreated(null)}>Done</Button>}>
        <Typography.Paragraph>This is the only time it's shown.</Typography.Paragraph>
        <Typography.Paragraph copyable style={{ ...mono, wordBreak: "break-all" }}>
          {created}
        </Typography.Paragraph>
      </Modal>
    </>
  );
}

function SettingsSection() {
  const { message, modal } = App.useApp();
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: ["admin", "settings"], queryFn: () => get<InstanceSettings>("/api/admin/settings") });
  const [draft, setDraft] = useState<InstanceSettings | null>(null);
  useEffect(() => {
    if (settings.data) setDraft(settings.data);
  }, [settings.data]);
  const save = useMutation({
    mutationFn: (s: InstanceSettings) => put<InstanceSettings>("/api/admin/settings", s),
    onSuccess: (s) => {
      qc.setQueryData(["admin", "settings"], s);
      qc.invalidateQueries({ queryKey: ["instance"] });
      message.success("Saved");
    },
    onError: (e: Error) => message.error(e.message),
  });
  const purge = useMutation({
    mutationFn: () => post<{ deleted: number }>("/api/admin/history/purge"),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["history"] });
      message.success(`Deleted ${r.deleted} history entries`);
    },
  });
  if (!draft) return <Card loading />;
  const set = (p: Partial<InstanceSettings>) => setDraft({ ...draft, ...p });
  const dirty = JSON.stringify(draft) !== JSON.stringify(settings.data);
  return (
    <>
      <PageHeader
        title="Settings"
        subtitle="Changes apply right away for everyone"
        extra={
          <Button type="primary" disabled={!dirty} loading={save.isPending} onClick={() => save.mutate(draft)}>
            Save
          </Button>
        }
      />
      <Card size="small" title="Banner" style={{ marginBottom: 16 }}>
        <Space direction="vertical" style={{ width: "100%" }} size={12}>
          <Space>
            <Switch checked={draft.banner.enabled} onChange={(v) => set({ banner: { ...draft.banner, enabled: v } })} />
            Show a banner to everyone, in the web app and the desktop app
          </Space>
          <Segmented
            value={draft.banner.level}
            onChange={(v) => set({ banner: { ...draft.banner, level: v as InstanceSettings["banner"]["level"] } })}
            options={[
              { value: "info", label: "Information" },
              { value: "warning", label: "Warning" },
              { value: "danger", label: "Danger" },
            ]}
          />
          <Input.TextArea
            rows={2}
            maxLength={500}
            showCount
            placeholder="Requara will be down for maintenance on Saturday 10:00–11:00 UTC."
            value={draft.banner.message}
            onChange={(e) => set({ banner: { ...draft.banner, message: e.target.value } })}
          />
          {draft.banner.message && <Alert banner type={draft.banner.level === "danger" ? "error" : draft.banner.level} message={draft.banner.message} />}
        </Space>
      </Card>
      <Card size="small" title="Workspaces" style={{ marginBottom: 16 }}>
        <Space>
          <Switch checked={draft.allowWorkspaceCreation} onChange={(v) => set({ allowWorkspaceCreation: v })} />
          Let anyone create workspaces (admins always can)
        </Space>
      </Card>
      <Card size="small" title="Request history">
        <Space direction="vertical" size={12}>
          <Space>
            <Switch checked={draft.historyEnabled} onChange={(v) => set({ historyEnabled: v })} />
            Keep request history (requests and responses people send)
          </Space>
          <Button
            danger
            loading={purge.isPending}
            onClick={() =>
              modal.confirm({
                title: "Delete everyone's request history?",
                content: "This removes every saved request and response in every workspace. It can't be undone.",
                okText: "Delete history",
                okButtonProps: { danger: true },
                onOk: () => purge.mutateAsync(),
              })
            }
          >
            Delete all request history
          </Button>
        </Space>
      </Card>
    </>
  );
}
