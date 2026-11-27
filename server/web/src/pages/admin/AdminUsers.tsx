import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, App, Avatar, Button, Descriptions, Drawer, Form, Input, Modal, Popconfirm, Select, Space, Table, Tabs, Tag, theme, Tooltip, Typography } from "../../ui/kit";
import { useState } from "react";
import { del, get, post, type AdminInvite, type AdminUser, type AdminUserDetail, type AdminWorkspace } from "../../api";
import { PageHeader } from "../../components/bits";
import { timeAgo } from "../../lib";
import { initials } from "../../shell/Header";
import { useMe } from "../../shell/session";
import { useServerInfo } from "./Admin";

const roleLabel: Record<string, string> = { owner: "Owner", editor: "Can edit", viewer: "Can view" };

export default function AdminUsers() {
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("all");
  const [open, setOpen] = useState<string | null>(null);
  const [inviting, setInviting] = useState(false);
  const users = useQuery({
    queryKey: ["admin", "users", q, status],
    queryFn: () => get<AdminUser[]>(`/api/admin/users?q=${encodeURIComponent(q)}&status=${status}`),
    placeholderData: (prev) => prev,
  });
  const invites = useQuery({ queryKey: ["admin", "invites"], queryFn: () => get<AdminInvite[]>("/api/admin/invites") });
  const info = useServerInfo();
  const providerName = (id: string) => info.data?.server.providers.find((p) => p.id === id)?.name ?? id;
  return (
    <>
      <PageHeader
        title="Users"
        subtitle="Everyone who has signed in to this server"
        extra={
          <Button type="primary" onClick={() => setInviting(true)}>
            Invite people
          </Button>
        }
      />
      <Tabs
        items={[
          {
            key: "users",
            label: `People${users.data ? ` (${users.data.length})` : ""}`,
            children: (
              <>
                <Space style={{ marginBottom: 12 }}>
                  <Input.Search allowClear placeholder="Search name or email" onSearch={setQ} style={{ width: 280 }} />
                  <Select
                    value={status}
                    onChange={setStatus}
                    style={{ width: 160 }}
                    options={[
                      { value: "all", label: "Everyone" },
                      { value: "active", label: "Active" },
                      { value: "deactivated", label: "Deactivated" },
                      { value: "admins", label: "Admins" },
                    ]}
                  />
                </Space>
                <Table
                  size="middle"
                  rowKey="id"
                  loading={users.isLoading}
                  dataSource={users.data ?? []}
                  onRow={(u) => ({ onClick: () => setOpen(u.id), style: { cursor: "pointer" } })}
                  pagination={{ pageSize: 50, hideOnSinglePage: true }}
                  columns={[
                    {
                      title: "Name",
                      render: (_, u) => (
                        <Space>
                          <Avatar size="small" style={{ fontSize: 11 }}>
                            {initials(u.name || u.email)}
                          </Avatar>
                          <div>
                            <div style={{ fontWeight: 500 }}>{u.name || u.email}</div>
                            <div style={{ fontSize: 12, opacity: 0.6 }}>{u.email}</div>
                          </div>
                        </Space>
                      ),
                    },
                    { title: "Signs in with", dataIndex: "provider", render: (id: string) => providerName(id) },
                    {
                      title: "Status",
                      render: (_, u) => (
                        <Space size={4} wrap>
                          {u.isAdmin && <Tag color="purple">Admin</Tag>}
                          {u.disabledAt ? <Tag color="error">Deactivated</Tag> : <Tag color="success">Active</Tag>}
                          {u.directoryActive !== null && <Tag>{u.directoryActive ? "SCIM" : "SCIM: deactivated"}</Tag>}
                          {!u.emailVerified && (
                            <Tooltip title="The sign-in provider didn't confirm this email address">
                              <Tag color="warning">Unverified email</Tag>
                            </Tooltip>
                          )}
                        </Space>
                      ),
                    },
                    { title: "Workspaces", dataIndex: "workspaces", width: 110 },
                    { title: "Last sign-in", dataIndex: "lastLoginAt", render: (v: string) => timeAgo(v) },
                  ]}
                />
              </>
            ),
          },
          {
            key: "invites",
            label: `Pending invites${invites.data ? ` (${invites.data.length})` : ""}`,
            children: <Invites invites={invites.data ?? []} loading={invites.isLoading} />,
          },
        ]}
      />
      <UserDrawer id={open} onClose={() => setOpen(null)} />
      <InviteModal open={inviting} onClose={() => setInviting(false)} />
    </>
  );
}

function Invites({ invites, loading }: { invites: AdminInvite[]; loading: boolean }) {
  const qc = useQueryClient();
  const { message } = App.useApp();
  const revoke = useMutation({
    mutationFn: (id: string) => del(`/api/admin/invites/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["admin"] }),
  });
  return (
    <Table
      size="middle"
      rowKey="id"
      loading={loading}
      dataSource={invites}
      columns={[
        { title: "Email", dataIndex: "email" },
        { title: "Workspace", dataIndex: "workspace" },
        { title: "Role", dataIndex: "role", render: (r: string) => roleLabel[r] ?? r },
        { title: "Invited", render: (_, i) => `${timeAgo(i.createdAt)}${i.invitedBy ? ` by ${i.invitedBy}` : ""}` },
        {
          title: "",
          align: "right",
          render: (_, i) => (
            <Space>
              <Button
                size="small"
                onClick={() => {
                  navigator.clipboard.writeText(`${location.origin}/signin`);
                  message.success(`Sign-in link copied. Send it to ${i.email}; they sign in with that address.`);
                }}
              >
                Copy link
              </Button>
              <Popconfirm title={`Cancel the invite for ${i.email}?`} okText="Cancel invite" okButtonProps={{ danger: true }} onConfirm={() => revoke.mutate(i.id)}>
                <Button size="small" danger>
                  Revoke
                </Button>
              </Popconfirm>
            </Space>
          ),
        },
      ]}
    />
  );
}

function InviteModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { message } = App.useApp();
  const qc = useQueryClient();
  const [form] = Form.useForm<{ emails: string; workspaceId: string; role: string }>();
  const workspaces = useQuery({ queryKey: ["admin", "workspaces"], queryFn: () => get<AdminWorkspace[]>("/api/admin/workspaces"), enabled: open });
  const [result, setResult] = useState<{ invited: string[]; link: string; emailed: boolean } | null>(null);
  const invite = useMutation({
    mutationFn: (v: { emails: string; workspaceId: string; role: string }) =>
      post<{ invited: string[]; link: string; emailed: boolean }>("/api/admin/invites", { emails: [v.emails], workspaceId: v.workspaceId, role: v.role }),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["admin"] });
      form.resetFields();
      if (r.emailed) {
        message.success(`Invited ${r.invited.length} ${r.invited.length === 1 ? "person" : "people"}`);
        onClose();
      } else setResult(r);
    },
    onError: (e: Error) => message.error(e.message),
  });
  const close = () => {
    setResult(null);
    onClose();
  };
  return (
    <Modal title="Invite people" open={open} onCancel={close} okText="Invite" confirmLoading={invite.isPending} onOk={() => (result ? close() : form.submit())} cancelButtonProps={{ style: result ? { display: "none" } : undefined }}>
      {result ? (
        <Alert
          type="success"
          showIcon
          message={`Invited ${result.invited.join(", ")}`}
          description={
            <>
              Email isn't set up on this server, so send them the sign-in link yourself:{" "}
              <Typography.Text copyable>{result.link}</Typography.Text>
            </>
          }
        />
      ) : (
        <Form
          form={form}
          layout="vertical"
          initialValues={{ role: "editor", workspaceId: workspaces.data?.find((w) => w.autoJoin)?.id ?? workspaces.data?.[0]?.id }}
          onFinish={(v) => invite.mutate(v)}
          key={workspaces.data?.length}
        >
          <Form.Item name="emails" label="Email addresses" rules={[{ required: true, message: "Enter at least one address" }]} extra="Separate several with commas or new lines.">
            <Input.TextArea rows={3} placeholder="ana@acme.com, ben@acme.com" />
          </Form.Item>
          <Space.Compact block>
            <Form.Item name="workspaceId" label="Workspace" style={{ flex: 1 }} rules={[{ required: true }]}>
              <Select options={(workspaces.data ?? []).map((w) => ({ value: w.id, label: w.name }))} />
            </Form.Item>
            <Form.Item name="role" label="Role" style={{ width: 150 }}>
              <Select options={Object.entries(roleLabel).map(([value, label]) => ({ value, label }))} />
            </Form.Item>
          </Space.Compact>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            Invited people may sign in even when their email domain isn't in REQUARA_ALLOWED_DOMAINS.
          </Typography.Text>
        </Form>
      )}
    </Modal>
  );
}

function UserDrawer({ id, onClose }: { id: string | null; onClose: () => void }) {
  const { token } = theme.useToken();
  const { message } = App.useApp();
  const qc = useQueryClient();
  const me = useMe();
  const detail = useQuery({ queryKey: ["admin", "user", id], queryFn: () => get<AdminUserDetail>(`/api/admin/users/${encodeURIComponent(id!)}`), enabled: !!id });
  const act = useMutation({
    mutationFn: (a: "deactivate" | "reactivate" | "signout" | "delete") =>
      a === "delete" ? del(`/api/admin/users/${encodeURIComponent(id!)}`) : post(`/api/admin/users/${encodeURIComponent(id!)}/${a}`),
    onSuccess: (_, a) => {
      qc.invalidateQueries({ queryKey: ["admin"] });
      message.success({ deactivate: "Deactivated and signed out", reactivate: "Reactivated", signout: "Signed out everywhere", delete: "Deleted" }[a]);
      if (a === "delete") onClose();
    },
    onError: (e: Error) => message.error(e.message),
  });
  const u = detail.data?.user;
  const self = u?.id === me.data?.id;
  return (
    <Drawer open={!!id} onClose={onClose} width={560} title={u ? u.name || u.email : " "} loading={detail.isLoading}>
      {u && (
        <>
          {u.isAdmin && (
            <Alert
              type="info"
              showIcon
              style={{ marginBottom: 16 }}
              message="Instance admin"
              description="Listed in REQUARA_ADMIN_EMAILS on the server. To take admin rights away, remove the address there and restart."
            />
          )}
          <Descriptions column={1} size="small" labelStyle={{ width: 140, color: token.colorTextSecondary }}>
            <Descriptions.Item label="Email">
              {u.email} {!u.emailVerified && <Tag color="warning">Unverified</Tag>}
            </Descriptions.Item>
            <Descriptions.Item label="Signs in with">{u.provider}</Descriptions.Item>
            <Descriptions.Item label="Account id">
              <Typography.Text copyable style={{ fontSize: 12 }}>
                {u.id}
              </Typography.Text>
            </Descriptions.Item>
            <Descriptions.Item label="Joined">{timeAgo(u.createdAt)}</Descriptions.Item>
            <Descriptions.Item label="Last sign-in">{timeAgo(u.lastLoginAt)}</Descriptions.Item>
            <Descriptions.Item label="Status">{u.disabledAt ? <Tag color="error">Deactivated {timeAgo(u.disabledAt)}</Tag> : <Tag color="success">Active</Tag>}</Descriptions.Item>
          </Descriptions>
          <Typography.Title level={5} style={{ marginTop: 24 }}>
            Workspaces
          </Typography.Title>
          <Table
            size="small"
            pagination={false}
            rowKey="workspaceId"
            dataSource={detail.data?.memberships}
            locale={{ emptyText: "Not in any workspace" }}
            columns={[
              { title: "Workspace", dataIndex: "name" },
              { title: "Role", dataIndex: "role", render: (r: string) => roleLabel[r] },
            ]}
          />
          <Typography.Title level={5} style={{ marginTop: 24 }}>
            Signed in on
          </Typography.Title>
          <Table
            size="small"
            pagination={false}
            rowKey={(s) => s.createdAt + s.device}
            dataSource={detail.data?.sessions}
            locale={{ emptyText: "No active sessions" }}
            columns={[
              { title: "Device", dataIndex: "device" },
              { title: "IP", dataIndex: "ip" },
              { title: "Last seen", dataIndex: "lastSeenAt", render: (v: string) => timeAgo(v) },
            ]}
          />
          <Space style={{ marginTop: 24 }} wrap>
            <Button disabled={!u.sessions} loading={act.isPending && act.variables === "signout"} onClick={() => act.mutate("signout")}>
              Sign out everywhere
            </Button>
            {u.disabledAt ? (
              <Button onClick={() => act.mutate("reactivate")} loading={act.isPending && act.variables === "reactivate"}>
                Reactivate
              </Button>
            ) : (
              <Popconfirm
                title={`Deactivate ${u.name || u.email}?`}
                description="They're signed out and can't sign in until reactivated. Their work stays."
                okText="Deactivate"
                okButtonProps={{ danger: true }}
                disabled={u.isAdmin || self}
                onConfirm={() => act.mutate("deactivate")}
              >
                <Button danger disabled={u.isAdmin || self}>
                  Deactivate
                </Button>
              </Popconfirm>
            )}
            <Popconfirm
              title={`Delete ${u.name || u.email}?`}
              description="Removes the account, its API keys and request history. APIs and requests they made stay in their workspaces. If they can still sign in, they get a new, empty account."
              okText="Delete"
              okButtonProps={{ danger: true }}
              disabled={u.isAdmin || self}
              onConfirm={() => act.mutate("delete")}
            >
              <Button danger type="text" disabled={u.isAdmin || self}>
                Delete account
              </Button>
            </Popconfirm>
          </Space>
        </>
      )}
    </Drawer>
  );
}
