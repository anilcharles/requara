import { DeleteOutlined, LockOutlined } from "../ui/icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Alert, App, Button, Card, Checkbox, Input, Modal, Select, Space, Spin, Tag, theme, Typography } from "../ui/kit";
import { useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { get, post, put, type Variable } from "../api";
import { Dot, mono, PageHeader } from "../components/bits";
import { useEnvironments, useMe } from "../shell/session";

type Row = Variable & { keep?: boolean; newValue?: string };

const toRows = (vars: Variable[]): Row[] => vars.map((v) => ({ ...v, keep: v.kind === "secret" && v.hasValue }));
const changed = (rows: Row[], vars: Variable[] | undefined) =>
  JSON.stringify(rows.map(({ id: _id, hasValue: _h, ...r }) => r)) !==
  JSON.stringify((vars ?? []).map((v) => ({ key: v.key, value: v.value, kind: v.kind, enabled: v.enabled, keep: v.kind === "secret" && v.hasValue })));

export default function Environments() {
  const { envId } = useParams();
  const { token } = theme.useToken();
  const nav = useNavigate();
  const envs = useEnvironments();
  const [creating, setCreating] = useState(false);
  const selected = envId ?? envs.data?.activeId ?? envs.data?.items[0]?.id;
  const env = envs.data?.items.find((e) => e.id === selected);

  return (
    <div style={{ padding: "28px 32px 32px" }}>
      <PageHeader title="Environments" subtitle="Variables resolve in URLs, headers, bodies and scripts as {{name}}." extra={<Button type="primary" onClick={() => setCreating(true)}>New environment</Button>} />
      <div style={{ display: "flex", gap: 16, alignItems: "flex-start" }}>
        <Card style={{ width: 240, flexShrink: 0 }} styles={{ body: { padding: 4 } }}>
          {envs.data?.items.map((e) => (
            <div
              key={e.id}
              onClick={() => nav(`/environments/${e.id}`)}
              style={{ display: "flex", alignItems: "center", gap: 10, height: 36, padding: "0 12px", borderRadius: 6, cursor: "pointer", background: e.id === selected ? token.controlItemBgActive : undefined, color: e.id === selected ? token.colorPrimary : undefined }}
            >
              {!e.isGlobal && <Dot color={e.color} />}
              <span style={{ flex: 1, fontWeight: e.id === selected ? 500 : 400 }}>{e.name}</span>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                {e.count}
              </Typography.Text>
            </div>
          ))}
        </Card>
        <div style={{ flex: 1, minWidth: 0 }}>{env ? <Variables key={env.id} envId={env.id} name={env.name} active={env.id === envs.data?.activeId} global={env.isGlobal} /> : <Spin />}</div>
      </div>
      <NewEnv open={creating} onClose={() => setCreating(false)} onCreated={(id) => nav(`/environments/${id}`)} />
    </div>
  );
}

function Variables({ envId, name, active, global }: { envId: string; name: string; active: boolean; global: boolean }) {
  const { token } = theme.useToken();
  const { message } = App.useApp();
  const qc = useQueryClient();
  const me = useMe();
  const vars = useQuery({ queryKey: ["variables", envId], queryFn: () => get<Variable[]>(`/api/environments/${envId}/variables`) });
  const [rows, setRows] = useState<Row[] | null>(null);
  // Refetches (e.g. a script set a variable during Send) refresh the table only if you have no unsaved edits.
  const lastServer = useRef<Variable[] | undefined>(undefined);
  useEffect(() => {
    if (!vars.data) return;
    const prev = lastServer.current;
    setRows((cur) => (cur === null || !changed(cur, prev) ? toRows(vars.data!) : cur));
    lastServer.current = vars.data;
  }, [vars.data]);
  const save = useMutation({
    mutationFn: () =>
      put<Variable[]>(
        `/api/environments/${envId}/variables`,
        rows!.filter((r) => r.key.trim()).map((r) => ({ key: r.key, value: r.kind === "secret" ? (r.newValue ?? "") : r.value, kind: r.kind, enabled: r.enabled, keep: r.kind === "secret" && r.keep && !r.newValue })),
      ),
    onSuccess: (v) => {
      setRows(toRows(v));
      qc.setQueryData(["variables", envId], v);
      qc.invalidateQueries({ queryKey: ["environments"] });
      message.success("Variables saved");
    },
    onError: (e: Error) => message.error(e.message),
  });
  if (!rows) return <Spin />;
  const canEdit = !!me.data?.canEdit;
  const dirty = changed(rows, vars.data);
  const set = (i: number, p: Partial<Row>) => setRows(rows.map((r, j) => (j === i ? { ...r, ...p } : r)));
  const all = [...rows, { key: "", value: "", kind: "default", enabled: true } as Row];
  const grid = "44px 200px 1fr 140px 40px";

  return (
    <Space direction="vertical" size={12} style={{ width: "100%" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <Typography.Text strong style={{ fontSize: 16, flex: 1 }}>
          {name}
        </Typography.Text>
        {active && <Tag color="success">Active</Tag>}
        {global && <Tag>Applies to every environment</Tag>}
        <Button type="primary" disabled={!dirty || !canEdit} loading={save.isPending} onClick={() => save.mutate()}>
          Save
        </Button>
      </div>
      <div style={{ background: token.colorBgContainer, border: `1px solid ${token.colorSplit}`, borderRadius: 8, overflow: "hidden" }}>
        <div style={{ display: "grid", gridTemplateColumns: grid, height: 40, alignItems: "center", background: token.colorFillQuaternary, fontSize: 13, fontWeight: 600, color: token.colorTextSecondary }}>
          <span />
          <span style={{ padding: "0 12px" }}>Variable</span>
          <span style={{ padding: "0 12px" }}>Value</span>
          <span style={{ padding: "0 12px" }}>Type</span>
          <span />
        </div>
        {all.map((r, i) => {
          const blank = i === rows.length;
          const update = (p: Partial<Row>) => (blank ? setRows([...rows, { ...r, ...p }]) : set(i, p));
          return (
            <div key={i} style={{ display: "grid", gridTemplateColumns: grid, height: 44, alignItems: "center", borderTop: `1px solid ${token.colorSplit}` }}>
              <span style={{ display: "grid", placeItems: "center" }}>{!blank && <Checkbox checked={r.enabled} disabled={!canEdit} onChange={(e) => set(i, { enabled: e.target.checked })} />}</span>
              <Input variant="borderless" placeholder="Add variable" value={r.key} disabled={!canEdit} onChange={(e) => update({ key: e.target.value })} style={{ ...mono, fontSize: 13 }} />
              {r.kind === "secret" ? (
                <Input.Password
                  variant="borderless"
                  prefix={<LockOutlined style={{ color: token.colorTextTertiary }} />}
                  placeholder={r.keep ? "•••••••••••• stored in OpenBao · type to replace" : "Enter a secret value"}
                  value={r.newValue ?? ""}
                  disabled={!canEdit}
                  visibilityToggle={false}
                  onChange={(e) => update({ newValue: e.target.value })}
                  style={{ ...mono, fontSize: 13 }}
                />
              ) : (
                <Input variant="borderless" placeholder="Value" value={r.value} disabled={!canEdit} onChange={(e) => update({ value: e.target.value })} style={{ ...mono, fontSize: 13 }} />
              )}
              <Select
                variant="borderless"
                value={r.kind}
                disabled={!canEdit}
                onChange={(kind) => update({ kind, keep: false, newValue: kind === "secret" ? r.value : undefined, value: kind === "secret" ? "" : r.value })}
                options={[
                  { value: "default", label: "default" },
                  { value: "secret", label: "secret" },
                  { value: "script", label: "set by script" },
                ]}
              />
              <span style={{ display: "grid", placeItems: "center" }}>{!blank && canEdit && <Button type="text" size="small" icon={<DeleteOutlined />} aria-label="Delete variable" onClick={() => setRows(rows.filter((_, j) => j !== i))} />}</span>
            </div>
          );
        })}
      </div>
      <Alert type="warning" showIcon message="Secret values are encrypted with OpenBao Transit and never sent back to the browser, written to history, or included in shared links." />
    </Space>
  );
}

function NewEnv({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (id: string) => void }) {
  const [name, setName] = useState("");
  const qc = useQueryClient();
  const { message } = App.useApp();
  const create = useMutation({
    mutationFn: () => post<{ id: string }>("/api/environments", { name, color: "#1677FF" }),
    onSuccess: ({ id }) => {
      qc.invalidateQueries({ queryKey: ["environments"] });
      setName("");
      onClose();
      onCreated(id);
    },
    onError: (e: Error) => message.error(e.message),
  });
  return (
    <Modal open={open} title="New environment" okText="Create" okButtonProps={{ disabled: !name.trim() }} onOk={() => create.mutate()} confirmLoading={create.isPending} onCancel={onClose} destroyOnHidden>
      <Input autoFocus placeholder="QA" value={name} onChange={(e) => setName(e.target.value)} onPressEnter={() => name.trim() && create.mutate()} style={{ marginTop: 12 }} />
    </Modal>
  );
}
