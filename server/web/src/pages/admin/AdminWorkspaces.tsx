import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Checkbox, Drawer, Form, Input, Modal, Popconfirm, Select, Space, Switch, Table, Tag, Typography } from "../../ui/kit";
import { useState } from "react";
import { del, get, patch, post, put, type AdminUser, type AdminWorkspace, type WorkspaceMember } from "../../api";
import { PageHeader } from "../../components/bits";
import { timeAgo } from "../../lib";

const roles = [
  { value: "owner", label: "Owner" },
  { value: "editor", label: "Can edit" },
  { value: "viewer", label: "Can view" },
];

export default function AdminWorkspaces() {
  const { message } = App.useApp();
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["admin", "workspaces"], queryFn: () => get<AdminWorkspace[]>("/api/admin/workspaces") });
  const [creating, setCreating] = useState(false);
  const [open, setOpen] = useState<AdminWorkspace | null>(null);
  const [form] = Form.useForm<{ name: string; autoJoin: boolean; ownerEmail: string }>();
  const create = useMutation({
    mutationFn: (v: { name: string; autoJoin: boolean; ownerEmail: string }) => post("/api/admin/workspaces", v),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["admin"] });
      qc.invalidateQueries({ queryKey: ["workspaces"] });
      setCreating(false);
      form.resetFields();
    },
    onError: (e: Error) => message.error(e.message),
  });
  return (
    <>
      <PageHeader
        title="Workspaces"
        subtitle="Every workspace on this server"
        extra={
          <Button type="primary" onClick={() => setCreating(true)}>
            New workspace
          </Button>
        }
      />
      <Table
        size="middle"
        rowKey="id"
        loading={list.isLoading}
        dataSource={list.data ?? []}
        onRow={(w) => ({ onClick: () => setOpen(w), style: { cursor: "pointer" } })}
        columns={[
          {
            title: "Name",
            render: (_, w) => (
              <Space>
                <span style={{ fontWeight: 500 }}>{w.name}</span>
                {w.autoJoin && <Tag color="blue">Everyone joins</Tag>}
                {w.owners === 0 && <Tag color="warning">No owner</Tag>}
              </Space>
            ),
          },
          { title: "Members", dataIndex: "members", width: 100 },
          { title: "APIs", dataIndex: "apis", width: 80 },
          { title: "Requests", dataIndex: "requests", width: 100 },
          { title: "Invites", dataIndex: "invites", width: 90 },
          { title: "Created", dataIndex: "createdAt", render: (v: string) => timeAgo(v) },
        ]}
      />
      <Modal title="New workspace" open={creating} onCancel={() => setCreating(false)} okText="Create" confirmLoading={create.isPending} onOk={() => form.submit()}>
        <Form form={form} layout="vertical" initialValues={{ autoJoin: false }} onFinish={(v) => create.mutate(v)}>
          <Form.Item name="name" label="Name" rules={[{ required: true, message: "Name the workspace" }]}>
            <Input placeholder="Platform team" />
          </Form.Item>
          <Form.Item name="ownerEmail" label="Owner (optional)" extra="Someone who has signed in before. Leave empty and invite an owner afterwards.">
            <Input placeholder="ana@acme.com" />
          </Form.Item>
          <Form.Item name="autoJoin" valuePropName="checked">
            <Checkbox>Everyone who signs up joins this workspace</Checkbox>
          </Form.Item>
        </Form>
      </Modal>
      {open && <WorkspaceDrawer ws={open} onClose={() => setOpen(null)} />}
    </>
  );
}

function WorkspaceDrawer({ ws, onClose }: { ws: AdminWorkspace; onClose: () => void }) {
  const { message, modal } = App.useApp();
  const qc = useQueryClient();
  const members = useQuery({ queryKey: ["admin", "ws-members", ws.id], queryFn: () => get<WorkspaceMember[]>(`/api/admin/workspaces/${ws.id}/members`) });
  const users = useQuery({ queryKey: ["admin", "users", "", "active"], queryFn: () => get<AdminUser[]>("/api/admin/users?status=active") });
  const [name, setName] = useState(ws.name);
  const [autoJoin, setAutoJoin] = useState(ws.autoJoin);
  const [adding, setAdding] = useState<{ userId?: string; role: string }>({ role: "editor" });
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["admin"] });
    qc.invalidateQueries({ queryKey: ["workspaces"] });
  };
  const fail = (e: Error) => message.error(e.message);
  const update = useMutation({ mutationFn: (v: { name?: string; autoJoin?: boolean }) => patch(`/api/admin/workspaces/${ws.id}`, v), onSuccess: refresh, onError: fail });
  const setRole = useMutation({ mutationFn: (v: { userId: string; role: string }) => put(`/api/admin/workspaces/${ws.id}/members/${encodeURIComponent(v.userId)}`, { role: v.role }), onSuccess: refresh, onError: fail });
  const remove = useMutation({ mutationFn: (userId: string) => del(`/api/admin/workspaces/${ws.id}/members/${encodeURIComponent(userId)}`), onSuccess: refresh, onError: fail });
  const destroy = useMutation({
    mutationFn: () => del(`/api/admin/workspaces/${ws.id}`),
    onSuccess: () => {
      refresh();
      onClose();
    },
    onError: fail,
  });
  const memberIds = new Set((members.data ?? []).map((m) => m.userId));
  return (
    <Drawer open onClose={onClose} width={640} title={ws.name}>
      <Space.Compact block style={{ marginBottom: 12 }}>
        <Input value={name} onChange={(e) => setName(e.target.value)} />
        <Button disabled={!name.trim() || name === ws.name} onClick={() => update.mutate({ name })}>
          Rename
        </Button>
      </Space.Compact>
      <Space style={{ marginBottom: 24 }}>
        <Switch
          checked={autoJoin}
          onChange={(v) => {
            setAutoJoin(v);
            update.mutate({ autoJoin: v });
          }}
        />
        Everyone who signs up joins this workspace (as an editor)
      </Space>
      <Typography.Title level={5}>Members</Typography.Title>
      <Space.Compact block style={{ marginBottom: 12 }}>
        <Select
          showSearch
          placeholder="Add someone who has signed in"
          style={{ flex: 1 }}
          value={adding.userId}
          onChange={(userId) => setAdding({ ...adding, userId })}
          optionFilterProp="label"
          options={(users.data ?? []).filter((u) => !memberIds.has(u.id)).map((u) => ({ value: u.id, label: `${u.name} <${u.email}>` }))}
        />
        <Select value={adding.role} onChange={(role) => setAdding({ ...adding, role })} options={roles} style={{ width: 120 }} />
        <Button
          type="primary"
          disabled={!adding.userId}
          onClick={() => setRole.mutate({ userId: adding.userId!, role: adding.role }, { onSuccess: () => setAdding({ role: adding.role }) })}
        >
          Add
        </Button>
      </Space.Compact>
      <Table
        size="small"
        rowKey="userId"
        loading={members.isLoading}
        dataSource={members.data ?? []}
        pagination={{ pageSize: 20, hideOnSinglePage: true }}
        columns={[
          {
            title: "Person",
            render: (_, m) => (
              <div>
                <div style={{ fontWeight: 500 }}>
                  {m.name} {m.disabled && <Tag color="error">Deactivated</Tag>}
                </div>
                <div style={{ fontSize: 12, opacity: 0.6 }}>{m.email}</div>
              </div>
            ),
          },
          {
            title: "Role",
            width: 130,
            render: (_, m) => <Select size="small" value={m.role} options={roles} style={{ width: 115 }} onChange={(role) => setRole.mutate({ userId: m.userId, role })} />,
          },
          {
            title: "",
            width: 80,
            align: "right",
            render: (_, m) => (
              <Popconfirm title={`Remove ${m.name} from ${ws.name}?`} okText="Remove" okButtonProps={{ danger: true }} onConfirm={() => remove.mutate(m.userId)}>
                <Button size="small" type="text" danger>
                  Remove
                </Button>
              </Popconfirm>
            ),
          },
        ]}
      />
      <Typography.Title level={5} style={{ marginTop: 32 }} type="danger">
        Delete workspace
      </Typography.Title>
      <Typography.Paragraph type="secondary">Deletes its APIs, requests, environments, history and mock servers for everyone. It can't be undone.</Typography.Paragraph>
      <Button
        danger
        onClick={() => {
          let typed = "";
          modal.confirm({
            title: `Delete ${ws.name}?`,
            content: (
              <>
                <p>
                  Type <strong>{ws.name}</strong> to confirm.
                </p>
                <Input onChange={(e) => (typed = e.target.value)} />
              </>
            ),
            okText: "Delete workspace",
            okButtonProps: { danger: true },
            onOk: () => {
              if (typed !== ws.name) {
                message.error("The name doesn't match");
                return Promise.reject();
              }
              return destroy.mutateAsync();
            },
          });
        }}
      >
        Delete workspace
      </Button>
    </Drawer>
  );
}
