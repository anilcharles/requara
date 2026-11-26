import { SettingOutlined } from "@ant-design/icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Card, Empty, Form, Input, InputNumber, Modal, Select, Space, Switch, Table, Tag, theme, Typography } from "antd";
import dayjs from "dayjs";
import { useEffect, useState } from "react";
import { get, mocksPath, patch, post, type ApiSummary, type Mock, type MockCall } from "../api";
import { Dot, Method, mono, PageHeader, StatusTag } from "../components/bits";

export default function Mocks() {
  const { token } = theme.useToken();
  const { message } = App.useApp();
  const qc = useQueryClient();
  const mocks = useQuery({ queryKey: ["mocks"], queryFn: () => get<Mock[]>(mocksPath()) });
  const calls = useQuery({ queryKey: ["mock-calls"], queryFn: () => get<MockCall[]>("/api/mocks/calls") });
  const [live, setLive] = useState(false);
  const [edit, setEdit] = useState<Mock | "new" | null>(null);

  // Live log over Server-Sent Events.
  useEffect(() => {
    const es = new EventSource("/api/mocks/stream");
    es.onopen = () => setLive(true);
    es.onerror = () => setLive(false);
    es.onmessage = (ev) => {
      const e = JSON.parse(ev.data) as { type: string; data: MockCall };
      if (e.type !== "mock_call") return;
      qc.setQueryData<MockCall[]>(["mock-calls"], (old) => [e.data, ...(old ?? [])].slice(0, 50));
      qc.setQueryData<Mock[]>(["mocks"], (old) => old?.map((m) => (m.name === e.data.mockName ? { ...m, callsToday: m.callsToday + 1 } : m)));
    };
    return () => es.close();
  }, [qc]);

  const toggle = useMutation({
    mutationFn: (m: Mock) => patch(`/api/mocks/${m.id}`, { enabled: !m.enabled }),
    onMutate: (m) => qc.setQueryData<Mock[]>(["mocks"], (old) => old?.map((x) => (x.id === m.id ? { ...x, enabled: !x.enabled } : x))),
    onError: (e: Error) => (message.error(e.message), qc.invalidateQueries({ queryKey: ["mocks"] })),
  });

  return (
    <div style={{ padding: "28px 32px 32px" }}>
      <PageHeader title="Mock servers" subtitle="Serve example responses from your specs. Send Prefer: code=402 to get a specific documented response." extra={<Button type="primary" onClick={() => setEdit("new")}>New mock server</Button>} />
      {mocks.data?.length === 0 && <Empty description="No mock servers yet" />}
      <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(340px, 1fr))", gap: 16, marginBottom: 20 }}>
        {mocks.data?.map((m) => (
          <Card key={m.id} loading={mocks.isLoading}>
            <div style={{ display: "flex", gap: 8, alignItems: "flex-start" }}>
              <div style={{ flex: 1 }}>
                <Typography.Text strong style={{ fontSize: 16 }}>
                  {m.name}
                </Typography.Text>
                <div style={{ fontSize: 13, color: token.colorTextTertiary }}>
                  {m.apiName} {m.apiVersion}
                </div>
              </div>
              <Button type="text" size="small" icon={<SettingOutlined />} aria-label="Mock settings" onClick={() => setEdit(m)} />
              <Switch checked={m.enabled} onChange={() => toggle.mutate(m)} aria-label={m.enabled ? "Stop" : "Start"} />
            </div>
            <div style={{ display: "flex", alignItems: "center", gap: 8, margin: "14px 0", padding: "6px 10px", background: token.colorFillQuaternary, border: `1px solid ${token.colorSplit}`, borderRadius: 6 }}>
              <Typography.Text copyable={{ text: m.url }} style={{ ...mono, fontSize: 12, flex: 1, color: m.enabled ? undefined : token.colorTextQuaternary }} ellipsis>
                {m.url.replace(/^https?:\/\//, "")}
              </Typography.Text>
            </div>
            <div style={{ display: "flex" }}>
              {[
                ["Calls today", String(m.callsToday)],
                ["Added latency", m.latencyMs ? `${m.latencyMs} ms` : "—"],
                ["Error injection", `${m.errorRate}%`],
              ].map(([l, v]) => (
                <div key={l} style={{ flex: 1 }}>
                  <div style={{ fontSize: 12, color: token.colorTextTertiary }}>{l}</div>
                  <div style={{ fontSize: 18, fontWeight: 600, fontVariantNumeric: "tabular-nums", color: m.enabled ? undefined : token.colorTextQuaternary }}>{v}</div>
                </div>
              ))}
            </div>
            <Space size={8} style={{ marginTop: 14 }}>
              <Dot color={m.enabled ? "#52C41A" : "#BFBFBF"} />
              <Typography.Text type="secondary">{m.enabled ? "Running" : "Stopped"}</Typography.Text>
            </Space>
          </Card>
        ))}
      </div>
      <Card title="Recent mock calls" extra={live ? <Tag color="success">Live</Tag> : <Tag>Reconnecting</Tag>} styles={{ body: { padding: 0 } }}>
        <Table
          rowKey="id"
          dataSource={calls.data}
          loading={calls.isLoading}
          pagination={false}
          size="middle"
          showHeader={false}
          locale={{ emptyText: "No calls yet. Send a request to a mock URL." }}
          columns={[
            { width: 116, onCell: () => ({ style: { paddingLeft: 24 } }), render: (_, c) => <span style={{ ...mono, fontSize: 12, color: token.colorTextTertiary }}>{dayjs(c.createdAt).format("HH:mm:ss")}</span> },
            { width: 70, render: (_, c) => <Method method={c.method} width="auto" /> },
            { render: (_, c) => <span style={{ ...mono, fontSize: 13 }}>{c.path}</span> },
            { width: 80, render: (_, c) => <StatusTag status={c.status} /> },
            { width: 196, align: "right", onCell: () => ({ style: { paddingRight: 24 } }), render: (_, c) => <Typography.Text type="secondary">{c.mockName}</Typography.Text> },
          ]}
        />
      </Card>
      <MockModal mock={edit} onClose={() => setEdit(null)} />
    </div>
  );
}

function MockModal({ mock, onClose }: { mock: Mock | "new" | null; onClose: () => void }) {
  const [form] = Form.useForm();
  const qc = useQueryClient();
  const { message } = App.useApp();
  const apis = useQuery({ queryKey: ["apis", "all"], queryFn: () => get<{ items: ApiSummary[] }>("/api/apis?status="), enabled: mock === "new" });
  const isNew = mock === "new";
  useEffect(() => {
    if (mock && mock !== "new") form.setFieldsValue({ name: mock.name, latencyMs: mock.latencyMs, errorRate: mock.errorRate });
    else form.resetFields();
  }, [mock, form]);
  const save = useMutation({
    mutationFn: (v: { apiId?: string; name: string; latencyMs: number; errorRate: number }) => (isNew ? post("/api/mocks", v) : patch(`/api/mocks/${(mock as Mock).id}`, v)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["mocks"] });
      message.success(isNew ? "Mock server created" : "Saved");
      onClose();
    },
    onError: (e: Error) => message.error(e.message),
  });
  return (
    <Modal open={!!mock} title={isNew ? "New mock server" : "Mock server settings"} okText={isNew ? "Create" : "Save"} onOk={() => form.submit()} onCancel={onClose} confirmLoading={save.isPending} destroyOnHidden>
      <Form form={form} layout="vertical" requiredMark={false} initialValues={{ latencyMs: 0, errorRate: 0 }} onFinish={(v) => save.mutate(v)} style={{ marginTop: 16 }}>
        {isNew && (
          <Form.Item name="apiId" label="API" rules={[{ required: true, message: "Pick an API" }]}>
            <Select options={apis.data?.items.map((a) => ({ value: a.id, label: `${a.name} ${a.version}` }))} />
          </Form.Item>
        )}
        <Form.Item name="name" label="Name">
          <Input placeholder="Payments sandbox" />
        </Form.Item>
        <Space size={16}>
          <Form.Item name="latencyMs" label="Added latency">
            <InputNumber min={0} max={10000} addonAfter="ms" />
          </Form.Item>
          <Form.Item name="errorRate" label="Error injection" tooltip="Share of calls that get a 500">
            <InputNumber min={0} max={100} addonAfter="%" />
          </Form.Item>
        </Space>
      </Form>
    </Modal>
  );
}
