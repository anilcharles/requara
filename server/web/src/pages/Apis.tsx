import { InboxOutlined, PlusOutlined, SearchOutlined } from "../ui/icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, App, Button, Divider, Form, Input, Layout, Modal, Radio, Segmented, Table, theme, Typography, Upload } from "../ui/kit";
import { useEffect, useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { api, get, post, type ApiSummary, type Collection } from "../api";
import { Dot, PageHeader } from "../components/bits";
import { compactNumber, timeAgo } from "../lib";

const statusDot: Record<string, string> = { published: "#52C41A", draft: "#BFBFBF", deprecated: "#FAAD14" };

/** Opens an API: its first request in the Requests view. */
function useOpenApi() {
  const nav = useNavigate();
  const qc = useQueryClient();
  return async (id: string) => {
    const cols = await qc.fetchQuery({ queryKey: ["collections"], queryFn: () => get<Collection[]>("/api/collections"), staleTime: 0 });
    const first = cols.find((c) => c.id === id)?.folders.flatMap((f) => f.requests)[0];
    nav(first ? `/requests/${first.id}` : "/requests");
  };
}

export default function Apis() {
  const { token } = theme.useToken();
  const openApi = useOpenApi();
  const [params, setParams] = useSearchParams();
  // The command palette opens an API with ?open=<id>.
  useEffect(() => {
    const id = params.get("open");
    if (id) openApi(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params]);
  const [filter, setFilter] = useState<string>("all");
  const [q, setQ] = useState("");
  const [sort, setSort] = useState<"updated" | "name" | "calls">("updated");
  const list = useQuery({
    queryKey: ["apis", filter],
    queryFn: () => get<{ items: ApiSummary[]; counts: Record<string, number> }>(`/api/apis?status=${filter === "all" ? "" : filter}`),
  });
  const items = useMemo(() => {
    const ql = q.toLowerCase();
    const rows = (list.data?.items ?? []).filter((a) => !ql || (a.name + a.description + a.owner).toLowerCase().includes(ql));
    if (sort === "name") rows.sort((a, b) => a.name.localeCompare(b.name));
    if (sort === "calls") rows.sort((a, b) => b.calls24h - a.calls24h);
    return rows;
  }, [list.data, q, sort]);
  const counts = list.data?.counts ?? {};
  const total = (list.data?.items ?? []).reduce((n, a) => n + a.endpoints, 0);
  const num = { textAlign: "right" as const, fontVariantNumeric: "tabular-nums" };

  return (
    <Layout.Content style={{ overflow: "auto", padding: "28px 40px 40px" }}>
      <PageHeader
        title="APIs"
        subtitle={list.data ? `${counts.all ?? 0} APIs and ${total} endpoints${filter !== "all" ? " shown" : ""}` : " "}
        extra={
          <>
            <Input prefix={<SearchOutlined style={{ color: token.colorTextTertiary }} />} placeholder="Search APIs" allowClear style={{ width: 260 }} value={q} onChange={(e) => setQ(e.target.value)} />
            <Button onClick={() => setParams({ import: "1" })}>Import OpenAPI</Button>
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setParams({ new: "1" })}>
              New API
            </Button>
          </>
        }
      />
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 16 }}>
        <Segmented
          value={filter}
          onChange={(v) => setFilter(v as string)}
          options={[
            ["all", "All"],
            ["published", "Published"],
            ["draft", "Draft"],
            ["deprecated", "Deprecated"],
          ].map(([value, label]) => ({
            value,
            label: (
              <span>
                {label} <span style={{ color: token.colorTextTertiary, fontSize: 12 }}>{counts[value] ?? ""}</span>
              </span>
            ),
          }))}
        />
        <span>
          <Typography.Text type="secondary">Sort by </Typography.Text>
          <Segmented
            size="small"
            value={sort}
            onChange={(v) => setSort(v as typeof sort)}
            options={[
              { value: "updated", label: "Last updated" },
              { value: "name", label: "Name" },
              { value: "calls", label: "Traffic" },
            ]}
          />
        </span>
      </div>
      <Table<ApiSummary>
        rowKey="id"
        loading={list.isLoading}
        dataSource={items}
        pagination={false}
        onRow={(a) => ({ onClick: () => openApi(a.id), style: { cursor: "pointer" } })}
        locale={{ emptyText: q ? `No APIs match “${q}”` : "No APIs here yet" }}
        columns={[
          {
            title: "API",
            render: (_, a) => (
              <div>
                <div style={{ fontWeight: 500, color: token.colorText }}>
                  {a.name}
                  {a.hasDraftChanges && a.status !== "draft" && (
                    <Typography.Text type="warning" style={{ fontSize: 12, marginLeft: 8, fontWeight: 400 }}>
                      unpublished changes
                    </Typography.Text>
                  )}
                </div>
                <div style={{ fontSize: 13, color: token.colorTextTertiary }}>{a.description}</div>
              </div>
            ),
          },
          { title: "Version", width: 110, render: (_, a) => <span style={{ fontFamily: "'Geist Mono Variable', ui-monospace, Menlo, monospace", fontSize: 13, color: token.colorTextSecondary }}>{a.version}</span> },
          {
            title: "Status",
            width: 130,
            render: (_, a) => (
              <span style={{ display: "inline-flex", alignItems: "center", gap: 8, color: a.status === "published" ? token.colorText : token.colorTextSecondary }}>
                <Dot color={statusDot[a.status]} />
                {a.status[0].toUpperCase() + a.status.slice(1)}
              </span>
            ),
          },
          { title: "Endpoints", width: 100, align: "right", render: (_, a) => <span style={num}>{a.endpoints}</span> },
          { title: "Requests · 24h", width: 150, align: "right", render: (_, a) => <span style={num}>{a.calls24h ? compactNumber(a.calls24h) : <Typography.Text type="secondary">—</Typography.Text>}</span> },
          { title: "p95 latency", width: 120, align: "right", render: (_, a) => <span style={num}>{a.p95Ms != null ? `${Math.round(a.p95Ms)} ms` : <Typography.Text type="secondary">—</Typography.Text>}</span> },
          {
            title: "Error rate",
            width: 110,
            align: "right",
            render: (_, a) =>
              a.errorRate != null && a.calls24h ? (
                <span style={{ ...num, color: a.errorRate > 0.01 ? token.colorError : undefined }}>{(a.errorRate * 100).toFixed(2)}%</span>
              ) : (
                <Typography.Text type="secondary">—</Typography.Text>
              ),
          },
          { title: "Owner", width: 140, render: (_, a) => <Typography.Text type="secondary">{a.owner}</Typography.Text> },
          { title: "Updated", width: 130, render: (_, a) => <Typography.Text type="secondary">{timeAgo(a.updatedAt)}</Typography.Text> },
        ]}
      />
      <NewApiModal open={params.get("new") === "1"} onClose={() => setParams({})} />
      <ImportModal open={params.get("import") === "1"} onClose={() => setParams({})} />
    </Layout.Content>
  );
}

function NewApiModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [form] = Form.useForm();
  const openApi = useOpenApi();
  const qc = useQueryClient();
  const { message } = App.useApp();
  const [start, setStart] = useState("blank");
  const create = useMutation({
    mutationFn: (v: { name: string; baseUrl: string; version: string }) => post<{ id: string }>("/api/apis", { ...v, startFrom: start }),
    onSuccess: ({ id }) => {
      qc.invalidateQueries({ queryKey: ["apis"] });
      qc.invalidateQueries({ queryKey: ["collections"] });
      message.success("API created");
      onClose();
      openApi(id);
    },
    onError: (e: Error) => message.error(e.message),
  });
  return (
    <Modal open={open} title="Create an API" okText="Create API" onCancel={onClose} onOk={() => form.submit()} confirmLoading={create.isPending} width={520} destroyOnHidden>
      <Form form={form} layout="vertical" requiredMark={false} initialValues={{ version: "v1.0.0" }} onFinish={(v) => create.mutate(v)} style={{ marginTop: 16 }}>
        <Form.Item name="name" label="Name" rules={[{ required: true, message: "Give it a name" }]}>
          <Input placeholder="Ledger API" autoFocus />
        </Form.Item>
        <Form.Item name="baseUrl" label="Base URL" rules={[{ type: "url", message: "Enter a full URL" }]}>
          <Input placeholder="https://api.acme.dev/ledger" style={{ fontFamily: "'Geist Mono Variable', ui-monospace, Menlo, monospace" }} />
        </Form.Item>
        <Form.Item name="version" label="Version">
          <Input style={{ fontFamily: "'Geist Mono Variable', ui-monospace, Menlo, monospace" }} />
        </Form.Item>
        <Form.Item label="Start from" style={{ marginBottom: 0 }}>
          <Radio.Group value={start} onChange={(e) => setStart(e.target.value)} optionType="button" buttonStyle="solid" style={{ display: "flex" }}>
            <Radio.Button value="blank" style={{ flex: 1, textAlign: "center" }}>
              Blank
            </Radio.Button>
            <Radio.Button value="template" style={{ flex: 1, textAlign: "center" }}>
              REST resource template
            </Radio.Button>
          </Radio.Group>
        </Form.Item>
      </Form>
    </Modal>
  );
}

function ImportModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const openApi = useOpenApi();
  const qc = useQueryClient();
  const { message } = App.useApp();
  const [url, setUrl] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [err, setErr] = useState("");
  const run = useMutation({
    mutationFn: async () => {
      if (file) {
        const fd = new FormData();
        fd.append("file", file);
        return api<{ id: string; name: string; operations: number }>("/api/apis/import", { method: "POST", body: fd });
      }
      return post<{ id: string; name: string; operations: number }>("/api/apis/import", { url });
    },
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["apis"] });
      qc.invalidateQueries({ queryKey: ["collections"] });
      message.success(`Imported ${r.name} with ${r.operations} endpoints`);
      setFile(null);
      setUrl("");
      onClose();
      openApi(r.id);
    },
    onError: (e: Error) => setErr(e.message),
  });
  return (
    <Modal open={open} title="Import OpenAPI" okText="Import" okButtonProps={{ disabled: !file && !url }} onOk={() => (setErr(""), run.mutate())} confirmLoading={run.isPending} onCancel={onClose} width={520} destroyOnHidden>
      {err && <Alert type="error" showIcon message={err} style={{ margin: "12px 0" }} />}
      <Upload.Dragger
        accept=".yaml,.yml,.json"
        maxCount={1}
        beforeUpload={(f) => {
          setFile(f);
          setUrl("");
          return false;
        }}
        onRemove={() => setFile(null)}
        fileList={file ? [{ uid: "1", name: file.name, status: "done" }] : []}
        style={{ marginTop: 12 }}
      >
        <p className="rq-dz-icon">
          <InboxOutlined />
        </p>
        <p className="rq-dz-text">Drop an OpenAPI 3.x or Swagger 2.0 file</p>
        <p className="rq-dz-hint">YAML or JSON, up to 10 MB. Requara creates the collection and docs.</p>
      </Upload.Dragger>
      <Divider plain style={{ fontSize: 12 }}>
        or import from a URL
      </Divider>
      <Input
        placeholder="https://example.com/openapi.yaml"
        value={url}
        disabled={!!file}
        onChange={(e) => setUrl(e.target.value)}
        style={{ fontFamily: "'Geist Mono Variable', ui-monospace, Menlo, monospace", fontSize: 13 }}
      />
    </Modal>
  );
}
