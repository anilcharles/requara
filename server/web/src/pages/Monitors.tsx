import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, App, Button, Drawer, Empty, Form, Input, Layout, Modal, Popconfirm, Select, Space, Switch, Table, Tag, theme, Typography } from "antd";
import dayjs from "dayjs";
import { useState } from "react";
import { del, get, isSignedIn, patch, personalActive, post, type Monitor, type MonitorRun } from "../api";
import { PageHeader } from "../components/bits";
import { SignInModal } from "../components/SignInOptions";
import { timeAgo } from "../lib";
import { useCollections } from "../shell/Layouts";
import { useEnvironments } from "../shell/session";

const INTERVALS = [
  { value: 5, label: "Every 5 minutes" },
  { value: 15, label: "Every 15 minutes" },
  { value: 60, label: "Every hour" },
  { value: 360, label: "Every 6 hours" },
  { value: 1440, label: "Every day" },
  { value: 10080, label: "Every week" },
];

/** Collections run on a schedule on the server, with email alerts. */
export default function Monitors() {
  const { token } = theme.useToken();
  const { message } = App.useApp();
  const qc = useQueryClient();
  const monitors = useQuery({ queryKey: ["monitors"], queryFn: () => get<Monitor[]>("/api/monitors") });
  const cols = useCollections();
  const envs = useEnvironments();
  const [creating, setCreating] = useState(false);
  const [open, setOpen] = useState<Monitor | null>(null);
  const [signingIn, setSigningIn] = useState(false);
  const [form] = Form.useForm();
  const refresh = () => qc.invalidateQueries({ queryKey: ["monitors"] });
  const fail = (e: Error) => message.error(e.message);
  const create = useMutation({
    mutationFn: (v: Record<string, unknown>) => post("/api/monitors", v),
    onSuccess: () => (refresh(), setCreating(false), form.resetFields()),
    onError: fail,
  });
  const toggle = useMutation({ mutationFn: (m: Monitor) => patch(`/api/monitors/${m.id}`, { enabled: !m.enabled }), onSuccess: refresh, onError: fail });
  const remove = useMutation({ mutationFn: (m: Monitor) => del(`/api/monitors/${m.id}`), onSuccess: refresh, onError: fail });
  const runNow = useMutation({
    mutationFn: (m: Monitor) => post<MonitorRun>(`/api/monitors/${m.id}/run`),
    onSuccess: (r) => {
      refresh();
      qc.invalidateQueries({ queryKey: ["monitor-runs"] });
      message[r.failed || r.errors ? "warning" : "success"](`${r.passed} passed, ${r.failed} failed, ${r.errors} errors`);
    },
    onError: fail,
  });

  if (personalActive()) {
    return (
      <Layout.Content style={{ padding: "28px 40px" }}>
        <PageHeader title="Monitors" subtitle="Run a collection on a schedule and get an email when something breaks" />
        <Alert
          type="info"
          showIcon
          message="Monitors run on your organization's server"
          description="That way they keep running when your browser is closed. Use a team workspace to create one."
          action={isSignedIn() ? undefined : <Button onClick={() => setSigningIn(true)}>Sign in</Button>}
        />
        <SignInModal open={signingIn} onClose={() => setSigningIn(false)} />
      </Layout.Content>
    );
  }
  return (
    <Layout.Content style={{ overflow: "auto", padding: "28px 40px" }}>
      <PageHeader
        title="Monitors"
        subtitle="Run a collection on a schedule and get an email when a test fails or a request errors"
        extra={
          <Button type="primary" onClick={() => setCreating(true)}>
            New monitor
          </Button>
        }
      />
      <Table
        rowKey="id"
        loading={monitors.isLoading}
        dataSource={monitors.data ?? []}
        locale={{ emptyText: <Empty description="No monitors yet" /> }}
        onRow={(m) => ({ onClick: () => setOpen(m), style: { cursor: "pointer" } })}
        columns={[
          { title: "Monitor", render: (_, m) => <span style={{ fontWeight: 500 }}>{m.name}</span> },
          { title: "Collection", dataIndex: "targetName" },
          { title: "Schedule", dataIndex: "intervalMin", render: (v: number) => INTERVALS.find((i) => i.value === v)?.label ?? `Every ${v} min` },
          {
            title: "Last run",
            render: (_, m) =>
              m.lastRunAt ? (
                <Space size={6}>
                  {m.lastFailed || m.lastErrors ? <Tag color="error">Failed</Tag> : <Tag color="success">Passed</Tag>}
                  <Typography.Text type="secondary">{timeAgo(m.lastRunAt)}</Typography.Text>
                </Space>
              ) : (
                <Typography.Text type="secondary">Not yet</Typography.Text>
              ),
          },
          { title: "Next run", render: (_, m) => (m.enabled ? dayjs(m.nextRunAt).format("MMM D, HH:mm") : "Paused") },
          {
            title: "",
            align: "right",
            render: (_, m) => (
              <Space onClick={(e) => e.stopPropagation()}>
                <Button size="small" loading={runNow.isPending && runNow.variables?.id === m.id} onClick={() => runNow.mutate(m)}>
                  Run now
                </Button>
                <Switch size="small" checked={m.enabled} onChange={() => toggle.mutate(m)} />
                <Popconfirm title={`Delete ${m.name}?`} okText="Delete" okButtonProps={{ danger: true }} onConfirm={() => remove.mutate(m)}>
                  <Button size="small" type="text" danger>
                    Delete
                  </Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
      <Modal title="New monitor" open={creating} onCancel={() => setCreating(false)} okText="Create" confirmLoading={create.isPending} onOk={() => form.submit()} destroyOnHidden>
        <Form form={form} layout="vertical" initialValues={{ intervalMin: 60 }} onFinish={(v) => create.mutate(v)}>
          <Form.Item name="name" label="Name" rules={[{ required: true }]}>
            <Input placeholder="Checkout API health" />
          </Form.Item>
          <Form.Item name="target" label="Collection" rules={[{ required: true }]}>
            <Select options={(cols.data ?? []).map((c) => ({ value: c.id, label: c.name }))} />
          </Form.Item>
          <Form.Item name="environmentId" label="Environment">
            <Select allowClear options={(envs.data?.items ?? []).filter((e) => !e.isGlobal).map((e) => ({ value: e.id, label: e.name }))} />
          </Form.Item>
          <Form.Item name="intervalMin" label="Run">
            <Select options={INTERVALS} />
          </Form.Item>
          <Form.Item name="notify" label="Email on failure" extra="Comma-separated. Needs email set up on the server.">
            <Input placeholder="oncall@acme.com" />
          </Form.Item>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            The monitor runs as you, with your access to this workspace.
          </Typography.Text>
        </Form>
      </Modal>
      {open && <RunsDrawer monitor={open} onClose={() => setOpen(null)} token={token} />}
    </Layout.Content>
  );
}

function RunsDrawer({ monitor, onClose, token }: { monitor: Monitor; onClose: () => void; token: ReturnType<typeof theme.useToken>["token"] }) {
  const runs = useQuery({ queryKey: ["monitor-runs", monitor.id], queryFn: () => get<MonitorRun[]>(`/api/monitors/${monitor.id}/runs`), refetchInterval: 15000 });
  return (
    <Drawer open onClose={onClose} width={640} title={monitor.name}>
      <Table
        size="small"
        rowKey="id"
        loading={runs.isLoading}
        dataSource={runs.data ?? []}
        pagination={{ pageSize: 20, hideOnSinglePage: true }}
        expandable={{
          expandedRowRender: (r) => (
            <div>
              {r.results.map((x, i) => (
                <div key={i} style={{ fontSize: 12, padding: "2px 0" }}>
                  <strong>
                    {x.method} {x.name}
                  </strong>{" "}
                  <span style={{ color: token.colorTextTertiary }}>
                    {x.status || "—"} · {x.durationMs} ms
                  </span>
                  {x.error && <div style={{ color: token.colorError }}>{x.error}</div>}
                  {(x.tests ?? []).map((t, j) => (
                    <div key={j} style={{ marginLeft: 12, color: t.pass ? undefined : token.colorError }}>
                      {t.pass ? "✓" : "✗"} {t.name}
                      {t.error ? `: ${t.error}` : ""}
                    </div>
                  ))}
                </div>
              ))}
            </div>
          ),
        }}
        columns={[
          { title: "When", dataIndex: "startedAt", render: (v: string) => dayjs(v).format("MMM D, HH:mm:ss") },
          { title: "Result", render: (_, r) => (r.failed || r.errors ? <Tag color="error">Failed</Tag> : <Tag color="success">Passed</Tag>) },
          { title: "Tests", render: (_, r) => `${r.passed} ✓ / ${r.failed} ✗` },
          { title: "Errors", dataIndex: "errors" },
          { title: "Time", dataIndex: "durationMs", render: (v: number) => `${(v / 1000).toFixed(1)} s` },
        ]}
      />
    </Drawer>
  );
}
