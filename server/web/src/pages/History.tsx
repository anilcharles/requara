import { SearchOutlined } from "../ui/icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Drawer, Input, Select, Space, Spin, Table, theme, Typography } from "../ui/kit";
import dayjs from "dayjs";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { del, get, type HistoryItem, type SendResult } from "../api";
import { Method, mono, PageHeader, StatusTag } from "../components/bits";
import { useEnvironments } from "../shell/session";
import ResponsePane from "./requests/ResponsePane";

function dayLabel(d: string) {
  const x = dayjs(d);
  if (x.isSame(dayjs(), "day")) return "Today";
  if (x.isSame(dayjs().subtract(1, "day"), "day")) return "Yesterday";
  return x.format("dddd, MMM D");
}

export default function History() {
  const { token } = theme.useToken();
  const { modal } = App.useApp();
  const nav = useNavigate();
  const qc = useQueryClient();
  const envs = useEnvironments();
  const [q, setQ] = useState("");
  const [method, setMethod] = useState("");
  const [status, setStatus] = useState("");
  const [env, setEnv] = useState("");
  const [open, setOpen] = useState<string | null>(null);
  const list = useQuery({
    queryKey: ["history", q, method, status, env],
    queryFn: () => get<HistoryItem[]>(`/api/history?q=${encodeURIComponent(q)}&method=${method}&status=${status}&env=${encodeURIComponent(env)}`),
  });
  const detail = useQuery({ queryKey: ["history-item", open], queryFn: () => get<{ requestId: string | null; result: SendResult }>(`/api/history/${open}`), enabled: !!open });
  const clear = useMutation({ mutationFn: () => del("/api/history"), onSuccess: () => qc.invalidateQueries({ queryKey: ["history"] }) });

  // Group rows by day, with a header row per day.
  const rows: (HistoryItem & { header?: string })[] = [];
  let last = "";
  for (const h of list.data ?? []) {
    const label = dayLabel(h.createdAt);
    if (label !== last) rows.push({ ...h, id: "h-" + label, header: label });
    last = label;
    rows.push(h);
  }

  return (
    <div style={{ padding: "28px 32px 32px" }}>
      <PageHeader
        title="History"
        subtitle="Requests you sent from this workspace. Secrets are redacted before they're stored."
        extra={
          <Button
            disabled={!list.data?.length}
            onClick={() => modal.confirm({ title: "Clear your history?", content: "This deletes every request you sent, with stored responses.", okText: "Clear", okButtonProps: { danger: true }, onOk: () => clear.mutateAsync() })}
          >
            Clear history
          </Button>
        }
      />
      <Space style={{ marginBottom: 16 }} wrap>
        <Input prefix={<SearchOutlined style={{ color: token.colorTextTertiary }} />} placeholder="Filter by URL or status" allowClear style={{ width: 320 }} value={q} onChange={(e) => setQ(e.target.value)} />
        <Select value={method} onChange={setMethod} style={{ width: 140 }} options={[{ value: "", label: "All methods" }, ...["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"].map((m) => ({ value: m, label: m }))]} />
        <Select value={status} onChange={setStatus} style={{ width: 140 }} options={[{ value: "", label: "All statuses" }, { value: "2xx", label: "2xx" }, { value: "3xx", label: "3xx" }, { value: "4xx", label: "4xx" }, { value: "5xx", label: "5xx" }, { value: "error", label: "Failed to send" }]} />
        <Select value={env} onChange={setEnv} style={{ width: 160 }} options={[{ value: "", label: "All environments" }, ...(envs.data?.items.filter((e) => !e.isGlobal).map((e) => ({ value: e.name, label: e.name })) ?? [])]} />
      </Space>
      <Table
        rowKey="id"
        loading={list.isLoading}
        dataSource={rows}
        pagination={false}
        showHeader={false}
        locale={{ emptyText: "Nothing sent yet. Requests you send show up here." }}
        onRow={(r) => (r.header ? {} : { onClick: () => setOpen(r.id), style: { cursor: "pointer" } })}
        columns={[
          {
            render: (_, r) =>
              r.header ? { children: <Typography.Text strong style={{ fontSize: 13 }}>{r.header}</Typography.Text>, props: { colSpan: 6 } } : <Typography.Text type="secondary" style={{ fontVariantNumeric: "tabular-nums" }}>{dayjs(r.createdAt).format("HH:mm")}</Typography.Text>,
            width: 72,
            onCell: (r) => (r.header ? { colSpan: 6, style: { background: token.colorFillQuaternary } } : {}),
          },
          { width: 72, render: (_, r) => <Method method={r.method} width="auto" />, onCell: (r) => (r.header ? { colSpan: 0 } : {}) },
          { render: (_, r) => <span style={{ ...mono, fontSize: 13, wordBreak: "break-all" }}>{r.url}</span>, onCell: (r) => (r.header ? { colSpan: 0 } : {}) },
          { width: 110, render: (_, r) => (r.status ? <StatusTag status={r.status} /> : <Typography.Text type="danger" style={{ fontSize: 13 }}>{r.error.slice(0, 40)}</Typography.Text>), onCell: (r) => (r.header ? { colSpan: 0 } : {}) },
          { width: 90, align: "right", render: (_, r) => <span style={{ fontVariantNumeric: "tabular-nums" }}>{r.durationMs} ms</span>, onCell: (r) => (r.header ? { colSpan: 0 } : {}) },
          { width: 120, align: "right", render: (_, r) => <Typography.Text type="secondary">{r.envName}</Typography.Text>, onCell: (r) => (r.header ? { colSpan: 0 } : {}) },
        ]}
      />
      <Drawer
        open={!!open}
        onClose={() => setOpen(null)}
        width={720}
        title={detail.data ? <span><Method method={detail.data.result.method} width="auto" /> <span style={{ ...mono, fontSize: 13 }}>{detail.data.result.url}</span></span> : "Request"}
        extra={detail.data?.requestId && <Button onClick={() => nav(`/requests/${detail.data!.requestId}`)}>Open request</Button>}
        styles={{ body: { padding: 0, display: "flex", flexDirection: "column" } }}
      >
        {detail.isLoading ? <Spin style={{ margin: 40 }} /> : detail.data && <div style={{ flex: 1, minHeight: 0 }}><ResponsePane result={detail.data.result} sending={false} onCancel={() => {}} /></div>}
      </Drawer>
    </div>
  );
}
