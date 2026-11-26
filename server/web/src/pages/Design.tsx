import { ArrowLeftOutlined, ArrowRightOutlined, DeleteOutlined, PlusOutlined } from "@ant-design/icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, App, Breadcrumb, Button, Card, Checkbox, Empty, Input, Layout, Modal, Select, Space, Spin, Tag, theme, Typography } from "antd";
import { useEffect, useRef, useState } from "react";
import { Navigate, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { get, post, put, type ApiSummary, type ChangeReport, type Field, type LintReport, type OperationDetail } from "../api";
import { Dot, mono } from "../components/bits";
import { methodColor } from "../lib";
import { CollectionsSider } from "../shell/Layouts";
import { useMe } from "../shell/session";

const TYPES = ["string", "integer", "number", "boolean", "object", "array"];

export default function Design() {
  const { apiId } = useParams();
  const [params, setParams] = useSearchParams();
  const nav = useNavigate();
  const apis = useQuery({ queryKey: ["apis", "all"], queryFn: () => get<{ items: ApiSummary[] }>("/api/apis?status=") });
  const ops = useQuery({ queryKey: ["operations", apiId], queryFn: () => get<{ key: string }[]>(`/api/apis/${apiId}/operations`), enabled: !!apiId });
  if (!apiId) {
    const first = apis.data?.items.find((a) => a.name === "Payments API") ?? apis.data?.items[0];
    if (!first) return <Spin style={{ margin: 40 }} />;
    return <Navigate to={`/design/${first.id}`} replace />;
  }
  // Ignore an ?op= that isn't in this API (stale link, or a path that was renamed).
  const wanted = params.get("op");
  const key = ops.data ? (ops.data.some((o) => o.key === wanted) ? wanted! : ops.data[0]?.key) : undefined;
  return (
    <>
      <CollectionsSider
        onPick={(r, c) => (!r.operationKey ? nav(`/requests/${r.id}`) : c.id === apiId ? setParams({ op: r.operationKey }) : nav(`/design/${c.id}?op=${encodeURIComponent(r.operationKey)}`))}
        isSelected={(r, c) => c.id === apiId && r.operationKey === key}
      />
      <Layout.Content style={{ overflow: "auto" }}>
        {ops.isLoading && <Spin style={{ margin: 40 }} />}
        {ops.isError && <Alert type="error" showIcon message="Couldn't load this API" description={(ops.error as Error).message} style={{ margin: 24 }} />}
        {ops.data && ops.data.length === 0 && <Empty style={{ marginTop: 80 }} description="This API has no endpoints yet. Import a spec or start from the template." />}
        {key && <Editor key={apiId + key} apiId={apiId} opKey={key} onKeyChange={(k) => setParams({ op: k })} />}
      </Layout.Content>
    </>
  );
}

function Editor({ apiId, opKey, onKeyChange }: { apiId: string; opKey: string; onKeyChange: (k: string) => void }) {
  const { token } = theme.useToken();
  const { message, modal } = App.useApp();
  const qc = useQueryClient();
  const nav = useNavigate();
  const me = useMe();
  const api = useQuery({ queryKey: ["api", apiId], queryFn: () => get<{ name: string; version: string; draftVersion: string; status: string }>(`/api/apis/${apiId}`) });
  const op = useQuery({ queryKey: ["design-op", apiId, opKey], queryFn: () => get<OperationDetail>(`/api/apis/${apiId}/operation?key=${encodeURIComponent(opKey)}`) });
  const lint = useQuery({ queryKey: ["lint", apiId], queryFn: () => get<LintReport>(`/api/apis/${apiId}/lint`) });
  const changes = useQuery({ queryKey: ["changes", apiId], queryFn: () => get<ChangeReport>(`/api/apis/${apiId}/changes`) });
  const [form, setForm] = useState<{ method: string; path: string; summary: string; description: string; fields: Field[] } | null>(null);
  const [status, setStatus] = useState<"idle" | "saving" | "saved" | "error">("idle");
  const [error, setError] = useState("");
  const [publishOpen, setPublishOpen] = useState(false);
  const [justPublished, setJustPublished] = useState("");
  const first = useRef(true);

  useEffect(() => {
    if (op.data && !form) setForm({ method: op.data.method, path: op.data.path, summary: op.data.summary, description: op.data.description, fields: op.data.fields ?? [] });
  }, [op.data, form]);

  const refreshQuality = () => {
    qc.invalidateQueries({ queryKey: ["lint", apiId] });
    qc.invalidateQueries({ queryKey: ["changes", apiId] });
    qc.invalidateQueries({ queryKey: ["apis"] });
  };

  const save = useMutation({
    mutationFn: (f: NonNullable<typeof form>) => put<{ key: string }>(`/api/apis/${apiId}/operation`, { key: opKey, ...f }),
    onMutate: () => setStatus("saving"),
    onSuccess: ({ key }) => {
      setStatus("saved");
      setError("");
      refreshQuality();
      if (key !== opKey) {
        qc.invalidateQueries({ queryKey: ["collections"] });
        onKeyChange(key);
      } else qc.invalidateQueries({ queryKey: ["design-op", apiId, opKey] });
    },
    onError: (e: Error) => {
      setStatus("error");
      setError(e.message);
    },
  });

  // Autosave 700 ms after the last edit.
  useEffect(() => {
    if (!form) return;
    if (first.current) {
      first.current = false;
      return;
    }
    const t = setTimeout(() => save.mutate(form), 700);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [form]);

  const discard = useMutation({
    mutationFn: () => post(`/api/apis/${apiId}/discard`),
    onSuccess: () => {
      message.success("Changes discarded");
      qc.invalidateQueries();
      setForm(null);
      first.current = true;
    },
    onError: (e: Error) => message.error(e.message),
  });

  if (op.isError) return <Alert type="error" showIcon message="Couldn't load this endpoint" description={(op.error as Error).message} style={{ margin: 24 }} />;
  if (!form || !op.data) return <Spin style={{ margin: 40 }} />;
  const canEdit = !!me.data?.canEdit;
  const pending = changes.data?.changes.length ?? 0;
  const setField = (i: number, p: Partial<Field>) => setForm({ ...form, fields: form.fields.map((f, j) => (j === i ? { ...f, ...p } : f)) });
  const warnings = (lint.data?.issues ?? []).filter((i) => i.severity === "error" || i.severity === "warn");
  const hints = (lint.data?.issues ?? []).filter((i) => i.severity !== "error" && i.severity !== "warn");

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", gap: 12, height: 56, padding: "0 24px", background: token.colorBgContainer, borderBottom: `1px solid ${token.colorSplit}`, position: "sticky", top: 0, zIndex: 2 }}>
        <Breadcrumb style={{ flex: 1 }} items={[{ title: api.data?.name }, { title: op.data.tag }, { title: <b style={{ color: token.colorText }}>{form.summary || opKey}</b> }]} />
        <Typography.Text type="secondary" style={{ fontSize: 13 }}>
          {status === "saving" ? "Saving…" : status === "saved" ? "Draft saved" : ""}
        </Typography.Text>
        {pending > 0 ? <Tag color="warning">Unpublished changes</Tag> : api.data?.version ? <Tag color="success">Published {api.data.version}</Tag> : <Tag>Draft</Tag>}
        {pending > 0 && api.data?.version && (
          <Button
            type="text"
            disabled={!canEdit}
            onClick={() =>
              modal.confirm({
                title: "Discard changes?",
                content: `This removes ${pending} unpublished ${pending === 1 ? "change" : "changes"} across ${api.data?.name}. You can't undo this.`,
                okText: "Discard",
                okButtonProps: { danger: true },
                onOk: () => discard.mutateAsync(),
              })
            }
          >
            Discard
          </Button>
        )}
        <Button onClick={() => nav(`/docs/${apiId}?op=${encodeURIComponent(opKey)}&source=draft`)}>Preview docs</Button>
        <Button type="primary" disabled={!me.data?.canPublish || (pending === 0 && !!api.data?.version)} onClick={() => setPublishOpen(true)}>
          Publish {changes.data?.suggestedVersion ?? ""}
        </Button>
      </div>
      {justPublished && <Alert type="success" showIcon closable message={`${api.data?.name} ${justPublished} is live. Docs are updated and subscribers were emailed.`} style={{ margin: "16px 24px 0" }} onClose={() => setJustPublished("")} />}
      {error && <Alert type="error" showIcon message={error} style={{ margin: "16px 24px 0" }} />}
      {!me.data?.canPublish && <Alert type="info" showIcon message="Publishing needs a publisher role or group from your identity provider. Ask your admin." style={{ margin: "16px 24px 0" }} />}
      <div style={{ display: "flex", gap: 24, padding: 24, alignItems: "flex-start" }}>
        <Space direction="vertical" size={16} style={{ flex: 1, minWidth: 0 }}>
          <Card>
            <Space.Compact style={{ width: "100%", marginBottom: 16 }}>
              <Select
                value={form.method}
                disabled={!canEdit}
                onChange={(method) => setForm({ ...form, method })}
                style={{ width: 120 }}
                options={["GET", "POST", "PUT", "PATCH", "DELETE"].map((m) => ({ value: m, label: <span style={{ ...mono, fontWeight: 700, color: methodColor[m] }}>{m}</span> }))}
              />
              <Input
                key={form.path}
                defaultValue={form.path}
                disabled={!canEdit}
                style={mono}
                onBlur={(e) => e.target.value.trim() && e.target.value !== form.path && setForm({ ...form, path: e.target.value.trim() })}
                onPressEnter={(e) => (e.target as HTMLInputElement).blur()}
              />
            </Space.Compact>
            <div style={{ marginBottom: 8 }}>Summary</div>
            <Input value={form.summary} disabled={!canEdit} onChange={(e) => setForm({ ...form, summary: e.target.value })} style={{ marginBottom: 16 }} />
            <div style={{ marginBottom: 8 }}>Description</div>
            <Input.TextArea value={form.description} disabled={!canEdit} autoSize={{ minRows: 2, maxRows: 6 }} onChange={(e) => setForm({ ...form, description: e.target.value })} />
            <Typography.Text type="secondary" style={{ fontSize: 13, display: "block", marginTop: 8 }}>
              Markdown supported. Shown at the top of this endpoint's reference page.
            </Typography.Text>
          </Card>
          <Card
            title="Request body"
            styles={{ body: { padding: 0 } }}
            extra={
              <Space>
                <Typography.Text type="secondary" style={mono}>
                  application/json
                </Typography.Text>
                <Button size="small" icon={<PlusOutlined />} disabled={!canEdit} onClick={() => setForm({ ...form, fields: [...form.fields, { name: "", type: "string", required: false, description: "", depth: 0 }] })}>
                  Add field
                </Button>
              </Space>
            }
          >
            {form.fields.length === 0 ? (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No request body" style={{ padding: 24 }} />
            ) : (
              <div>
                <div style={{ display: "grid", gridTemplateColumns: "minmax(120px, 0.9fr) 160px 72px minmax(180px, 1.6fr) 96px", padding: "10px 20px", background: token.colorFillQuaternary, fontSize: 13, fontWeight: 600, color: token.colorTextSecondary, borderBottom: `1px solid ${token.colorSplit}` }}>
                  <span>Field</span>
                  <span>Type</span>
                  <span>Required</span>
                  <span>Description</span>
                  <span />
                </div>
                {form.fields.map((f, i) => (
                  <div key={i} style={{ display: "grid", gridTemplateColumns: "minmax(120px, 0.9fr) 160px 72px minmax(180px, 1.6fr) 96px", alignItems: "center", gap: 8, padding: "6px 20px", borderBottom: `1px solid ${token.colorSplit}`, background: f.new ? token.colorSuccessBg : undefined }}>
                    <span style={{ display: "flex", alignItems: "center", gap: 6, paddingLeft: f.depth * 20 }}>
                      {f.depth > 0 && <span style={{ color: token.colorTextQuaternary }}>└</span>}
                      <Input size="small" variant="borderless" value={f.name} disabled={!canEdit} placeholder="name" onChange={(e) => setField(i, { name: e.target.value })} style={{ ...mono, fontWeight: 500 }} />
                      {f.new && <Tag color="success" style={{ marginInlineEnd: 0 }}>New</Tag>}
                    </span>
                    <Select size="small" variant="borderless" value={f.enum?.length ? "enum" : f.type} disabled={!canEdit} onChange={(t) => setField(i, t === "enum" ? { type: "string", enum: f.enum?.length ? f.enum : ["value"] } : { type: t, enum: undefined })} options={[...TYPES, "enum"].map((t) => ({ value: t, label: <span style={{ ...mono, color: t === "object" ? "#531DAB" : "#0958D9" }}>{t === "enum" ? "string · enum" : t}</span> }))} />
                    <Checkbox checked={f.required} disabled={!canEdit} onChange={(e) => setField(i, { required: e.target.checked })} />
                    <div>
                      <Input size="small" variant="borderless" value={f.description} disabled={!canEdit} placeholder="Description" onChange={(e) => setField(i, { description: e.target.value })} />
                      {f.enum && <Input size="small" variant="borderless" value={f.enum.join(", ")} disabled={!canEdit} prefix={<Typography.Text type="secondary" style={{ fontSize: 12 }}>one of</Typography.Text>} onChange={(e) => setField(i, { enum: e.target.value.split(",").map((s) => s.trim()).filter(Boolean) })} style={mono} />}
                    </div>
                    <Space size={0}>
                      <Button size="small" type="text" icon={<ArrowLeftOutlined />} aria-label="Outdent" disabled={!canEdit || f.depth === 0} onClick={() => setField(i, { depth: f.depth - 1 })} />
                      <Button size="small" type="text" icon={<ArrowRightOutlined />} aria-label="Nest under the object above" disabled={!canEdit || !(i > 0 && (form.fields[i - 1].type === "object" ? form.fields[i - 1].depth >= f.depth : form.fields[i - 1].depth > f.depth))} onClick={() => setField(i, { depth: f.depth + 1 })} />
                      <Button size="small" type="text" icon={<DeleteOutlined />} aria-label="Delete field" disabled={!canEdit} onClick={() => setForm({ ...form, fields: form.fields.filter((_, j) => j !== i) })} />
                    </Space>
                  </div>
                ))}
              </div>
            )}
          </Card>
          <Card title="Responses" styles={{ body: { padding: "8px 20px" } }}>
            {(op.data.responses ?? []).map((r) => (
              <div key={r.status} style={{ display: "flex", gap: 12, padding: "8px 0", alignItems: "center" }}>
                <Tag color={r.status.startsWith("2") ? "success" : r.status.startsWith("4") ? "warning" : "error"} style={mono}>
                  {r.status}
                </Tag>
                <span>{r.description}</span>
              </div>
            ))}
          </Card>
        </Space>
        <Space direction="vertical" size={16} style={{ width: 340, flexShrink: 0 }}>
          <Card title="Style guide" extra={warnings.length ? <Tag color="warning">{warnings.length} warnings</Tag> : <Tag color="success">Passing</Tag>} loading={lint.isLoading} styles={{ body: { padding: "8px 20px 16px" } }}>
            {[...warnings, ...hints].slice(0, 6).map((w, i) => (
              <div key={i} style={{ display: "flex", gap: 10, padding: "8px 0" }}>
                <span style={{ paddingTop: 7 }}>
                  <Dot color={w.severity === "error" ? "#FF4D4F" : w.severity === "warn" ? "#FAAD14" : "#BFBFBF"} />
                </span>
                <div style={{ minWidth: 0 }}>
                  <div>{w.message}</div>
                  <div style={{ ...mono, fontSize: 12, color: token.colorTextTertiary, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{w.rule}</div>
                </div>
              </div>
            ))}
            {lint.data && (
              <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                {lint.data.passed.length} of {lint.data.rules} rules passing
                {lint.data.issues.length > 6 ? ` · ${lint.data.issues.length - 6} more findings` : ""}
              </Typography.Text>
            )}
          </Card>
          <Card title={changes.data?.from ? `Changes since ${changes.data.from}` : "Changes"} loading={changes.isLoading} styles={{ body: { padding: "12px 20px 16px" } }}>
            {changes.data && !changes.data.from && <Typography.Text type="secondary">Never published. Everything here is new.</Typography.Text>}
            {changes.data?.from && pending === 0 && <Typography.Text type="secondary">No changes since the last publish.</Typography.Text>}
            {changes.data?.changes.map((c, i) => (
              <div key={i} style={{ display: "flex", gap: 10, padding: "4px 0" }}>
                <span style={{ ...mono, fontWeight: 700, color: c.breaking ? token.colorError : c.kind === "added" ? "#389E0D" : c.kind === "removed" ? token.colorError : "#D46B08" }}>{c.kind === "added" ? "+" : c.kind === "removed" ? "−" : "~"}</span>
                <span style={{ color: token.colorTextSecondary }}>
                  {c.text}
                  {c.operation && <div style={{ ...mono, fontSize: 12, color: token.colorTextTertiary }}>{c.operation}</div>}
                </span>
              </div>
            ))}
            {pending > 0 && (
              <Alert
                style={{ marginTop: 12 }}
                type={changes.data?.breaking ? "error" : "success"}
                message={changes.data?.breaking ? `Breaking. Publishing needs a new major version (${changes.data.suggestedVersion}).` : "Non-breaking. Safe to ship as a minor version."}
              />
            )}
          </Card>
        </Space>
      </div>
      <PublishModal
        open={publishOpen}
        apiId={apiId}
        report={changes.data}
        onClose={() => setPublishOpen(false)}
        onDone={(v) => {
          setPublishOpen(false);
          setJustPublished(v);
          qc.invalidateQueries();
        }}
      />
    </div>
  );
}

function PublishModal({ open, apiId, report, onClose, onDone }: { open: boolean; apiId: string; report?: ChangeReport; onClose: () => void; onDone: (v: string) => void }) {
  const { message } = App.useApp();
  const [version, setVersion] = useState("");
  const [notify, setNotify] = useState(true);
  useEffect(() => setVersion(report?.suggestedVersion ?? ""), [report?.suggestedVersion, open]);
  const publish = useMutation({
    mutationFn: () => post<{ version: string }>(`/api/apis/${apiId}/publish`, { version, notify }),
    onSuccess: (r) => onDone(r.version),
    onError: (e: Error) => message.error(e.message),
  });
  const n = report?.changes.length ?? 0;
  return (
    <Modal open={open} title={`Publish ${version}?`} okText="Publish" onOk={() => publish.mutate()} confirmLoading={publish.isPending} onCancel={onClose} width={460}>
      <Typography.Paragraph type="secondary">
        {report?.from
          ? `${n} ${report.breaking ? "changes, including breaking ones," : "non-breaking changes"} will go live for everyone using this API.`
          : "This is the first published version. Docs and mock servers will use it."}
      </Typography.Paragraph>
      {n > 0 && (
        <div style={{ background: "var(--ant-color-fill-quaternary, #FAFAFA)", borderRadius: 6, padding: "10px 12px", marginBottom: 16 }}>
          {report!.changes.map((c, i) => (
            <div key={i} style={{ fontSize: 13 }}>
              <span style={{ ...mono, fontWeight: 700, color: c.breaking ? "#CF1322" : "#389E0D" }}>{c.kind === "added" ? "+ " : "~ "}</span>
              {c.text}
            </div>
          ))}
        </div>
      )}
      <div style={{ marginBottom: 8 }}>Version</div>
      <Input value={version} onChange={(e) => setVersion(e.target.value)} style={{ ...mono, marginBottom: 16 }} />
      <Checkbox checked={notify} onChange={(e) => setNotify(e.target.checked)}>
        Email workspace members the changelog
      </Checkbox>
    </Modal>
  );
}
