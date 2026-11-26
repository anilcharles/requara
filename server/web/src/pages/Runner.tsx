import { CheckCircleFilled, CloseCircleFilled, InboxOutlined, WarningFilled } from "@ant-design/icons";
import { Alert, Button, Card, Checkbox, Col, Empty, InputNumber, Layout, Progress, Row, Select, Space, Statistic, Tag, theme, Typography, Upload } from "antd";
import { useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { api, type SendResult } from "../api";
import { Method, mono, PageHeader } from "../components/bits";
import { runOrder } from "../shell/CollectionActions";
import { useCollections } from "../shell/Layouts";
import { useEnvironments } from "../shell/session";

type Row = Record<string, string>;
type Item = { iteration: number; requestId: string; name: string; method: string; result?: SendResult; error?: string };

/** Parses CSV with a header row (quotes and escaped quotes supported). */
export function parseCSV(text: string): Row[] {
  const rows: string[][] = [];
  let row: string[] = [];
  let cur = "";
  let q = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (q) {
      if (c === '"' && text[i + 1] === '"') (cur += '"', i++);
      else if (c === '"') q = false;
      else cur += c;
    } else if (c === '"') q = true;
    else if (c === ",") (row.push(cur), (cur = ""));
    else if (c === "\n" || c === "\r") {
      if (c === "\r" && text[i + 1] === "\n") i++;
      row.push(cur);
      cur = "";
      if (row.some((x) => x !== "")) rows.push(row);
      row = [];
    } else cur += c;
  }
  row.push(cur);
  if (row.some((x) => x !== "")) rows.push(row);
  const [head, ...body] = rows;
  return (body ?? []).map((r) => Object.fromEntries((head ?? []).map((h, i) => [h.trim(), r[i] ?? ""])));
}

function parseData(name: string, text: string): Row[] {
  if (name.toLowerCase().endsWith(".json")) {
    const v = JSON.parse(text);
    if (!Array.isArray(v)) throw new Error("A JSON data file is an array of objects");
    return v.map((o: Record<string, unknown>) => Object.fromEntries(Object.entries(o).map(([k, x]) => [k, typeof x === "string" ? x : JSON.stringify(x)])));
  }
  return parseCSV(text);
}

const failed = (it: Item) => !!it.error || !it.result || it.result.status === 0 || !!it.result.error || (it.result.tests ?? []).some((t) => !t.pass);

/** Runs every request of a collection, any number of times, with data files. */
export default function Runner() {
  const { id } = useParams();
  const { token } = theme.useToken();
  const nav = useNavigate();
  const cols = useCollections();
  const envs = useEnvironments();
  const col = cols.data?.find((c) => c.id === id);
  const requests = useMemo(() => (col ? runOrder(col) : []), [col]);
  const [skip, setSkip] = useState<Set<string>>(new Set());
  const [env, setEnv] = useState<string>();
  const [iterations, setIterations] = useState(1);
  const [delay, setDelay] = useState(0);
  const [stopOnFail, setStopOnFail] = useState(false);
  const [data, setData] = useState<{ name: string; rows: Row[] } | null>(null);
  const [dataErr, setDataErr] = useState("");
  const [items, setItems] = useState<Item[]>([]);
  const [running, setRunning] = useState(false);
  const [started, setStarted] = useState(0);
  const [elapsed, setElapsed] = useState(0);
  const stop = useRef(false);

  const chosen = requests.filter((r) => !skip.has(r.id));
  const total = chosen.length * (data ? Math.max(iterations, data.rows.length) : iterations);

  const run = async () => {
    stop.current = false;
    setItems([]);
    setRunning(true);
    const t0 = Date.now();
    setStarted(t0);
    const n = data ? Math.max(iterations, data.rows.length) : iterations;
    const environmentId = env ?? envs.data?.activeId;
    outer: for (let i = 0; i < n; i++) {
      const row = data?.rows.length ? data.rows[i % data.rows.length] : undefined;
      for (const r of chosen) {
        if (stop.current) break outer;
        const item: Item = { iteration: i + 1, requestId: r.id, name: r.name, method: r.method };
        try {
          const res = await api<{ result: SendResult }>(`/api/requests/${r.id}/send`, { method: "POST", json: { environmentId, data: row } });
          item.result = res.result;
        } catch (e) {
          item.error = (e as Error).message;
        }
        setItems((x) => [...x, item]);
        setElapsed(Date.now() - t0);
        if (stopOnFail && failed(item)) break outer;
      }
      if (delay > 0 && i + 1 < n) await new Promise((res) => setTimeout(res, delay));
    }
    setElapsed(Date.now() - t0);
    setRunning(false);
  };

  const tests = items.flatMap((i) => i.result?.tests ?? []);
  const passed = tests.filter((t) => t.pass).length;
  const errors = items.filter((i) => i.error || i.result?.error || i.result?.status === 0).length;
  const avg = items.length ? Math.round(items.reduce((s, i) => s + (i.result?.durationMs ?? 0), 0) / items.length) : 0;

  const exportResults = () => {
    const blob = new Blob([JSON.stringify({ collection: col?.name, startedAt: new Date(started).toISOString(), durationMs: elapsed, passed, failed: tests.length - passed, errors, items }, null, 2)], { type: "application/json" });
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = `${(col?.name ?? "run").toLowerCase().replace(/[^a-z0-9]+/g, "-")}-run.json`;
    a.click();
  };

  if (!col) return <Layout.Content style={{ padding: 40 }}>{cols.isLoading ? "…" : "Collection not found."}</Layout.Content>;
  return (
    <Layout.Content style={{ overflow: "auto", padding: "28px 40px", background: token.colorBgContainer, minHeight: "100%" }}>
      <PageHeader title={`Run ${col.name}`} subtitle="Send every request in order, as many times as you like, with variables from a data file" />
      <Row gutter={24}>
        <Col xs={24} lg={9}>
          <Card size="small" title={`Requests (${chosen.length} of ${requests.length})`} style={{ marginBottom: 16 }}>
            {requests.length === 0 && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="This collection has no requests" />}
            <div style={{ maxHeight: 260, overflow: "auto" }}>
              {requests.map((r) => (
                <div key={r.id} style={{ display: "flex", alignItems: "center", gap: 8, height: 30 }}>
                  <Checkbox
                    checked={!skip.has(r.id)}
                    disabled={running}
                    onChange={(e) => {
                      const s = new Set(skip);
                      if (e.target.checked) s.delete(r.id);
                      else s.add(r.id);
                      setSkip(s);
                    }}
                  />
                  <Method method={r.method} />
                  <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{r.name}</span>
                </div>
              ))}
            </div>
          </Card>
          <Card size="small" title="Run settings">
            <Space direction="vertical" style={{ width: "100%" }} size={12}>
              <div>
                <div style={{ marginBottom: 4 }}>Environment</div>
                <Select
                  value={env ?? envs.data?.activeId}
                  onChange={setEnv}
                  style={{ width: "100%" }}
                  disabled={running}
                  options={(envs.data?.items ?? []).filter((e) => !e.isGlobal).map((e) => ({ value: e.id, label: e.name }))}
                />
              </div>
              <Space size={16}>
                <div>
                  <div style={{ marginBottom: 4 }}>Iterations</div>
                  <InputNumber min={1} max={1000} value={iterations} onChange={(v) => setIterations(v ?? 1)} disabled={running} />
                </div>
                <div>
                  <div style={{ marginBottom: 4 }}>Delay</div>
                  <InputNumber min={0} max={60000} step={100} value={delay} onChange={(v) => setDelay(v ?? 0)} addonAfter="ms" disabled={running} style={{ width: 140 }} />
                </div>
              </Space>
              <div>
                <div style={{ marginBottom: 4 }}>Data file</div>
                <Upload.Dragger
                  accept=".csv,.json"
                  showUploadList={false}
                  disabled={running}
                  beforeUpload={(f) => {
                    f.text().then((t) => {
                      try {
                        const rows = parseData(f.name, t);
                        setData({ name: f.name, rows });
                        setDataErr("");
                      } catch (e) {
                        setDataErr((e as Error).message);
                      }
                    });
                    return false;
                  }}
                  style={{ padding: 4 }}
                >
                  <InboxOutlined style={{ fontSize: 20, color: token.colorTextTertiary }} />
                  <div style={{ fontSize: 13 }}>{data ? `${data.name}: ${data.rows.length} rows` : "CSV or JSON: one iteration per row"}</div>
                </Upload.Dragger>
                {data && (
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                    Columns become variables: {Object.keys(data.rows[0] ?? {}).map((k) => `{{${k}}}`).join(", ")}{" "}
                    <Typography.Link onClick={() => setData(null)}>Remove</Typography.Link>
                  </Typography.Text>
                )}
                {dataErr && <Alert type="error" showIcon message={dataErr} style={{ marginTop: 8 }} />}
              </div>
              <Checkbox checked={stopOnFail} onChange={(e) => setStopOnFail(e.target.checked)} disabled={running}>
                Stop at the first failure
              </Checkbox>
              {running ? (
                <Button danger block onClick={() => (stop.current = true)}>
                  Stop
                </Button>
              ) : (
                <Button type="primary" block disabled={!chosen.length} onClick={run}>
                  Run {col.name}
                </Button>
              )}
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                In CI: <span style={mono}>requara-cli run &quot;{col.name}&quot;</span>
              </Typography.Text>
            </Space>
          </Card>
        </Col>
        <Col xs={24} lg={15}>
          {items.length === 0 && !running ? (
            <Empty description="Results show up here" style={{ marginTop: 60 }} />
          ) : (
            <>
              <Row gutter={12} style={{ marginBottom: 12 }}>
                <Col span={6}>
                  <Statistic title="Tests passed" value={passed} valueStyle={{ color: "#389E0D" }} />
                </Col>
                <Col span={6}>
                  <Statistic title="Tests failed" value={tests.length - passed} valueStyle={{ color: tests.length - passed ? token.colorError : undefined }} />
                </Col>
                <Col span={6}>
                  <Statistic title="Errors" value={errors} valueStyle={{ color: errors ? token.colorError : undefined }} />
                </Col>
                <Col span={6}>
                  <Statistic title="Avg response" value={avg} suffix="ms" />
                </Col>
              </Row>
              <Progress percent={total ? Math.round((items.length / total) * 100) : 0} status={running ? "active" : errors || tests.length - passed ? "exception" : "success"} />
              <Space style={{ margin: "8px 0 12px" }}>
                <Typography.Text type="secondary">
                  {items.length} of {total} requests · {(elapsed / 1000).toFixed(1)} s
                </Typography.Text>
                {!running && <Button size="small" onClick={exportResults}>Export results</Button>}
              </Space>
              <div style={{ border: `1px solid ${token.colorSplit}`, borderRadius: 8 }}>
                {items.map((it, i) => {
                  const bad = failed(it);
                  return (
                    <div key={i} style={{ padding: "8px 12px", borderTop: i ? `1px solid ${token.colorSplit}` : undefined }}>
                      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                        {bad ? it.result?.tests?.some((t) => !t.pass) ? <CloseCircleFilled style={{ color: token.colorError }} /> : <WarningFilled style={{ color: token.colorWarning }} /> : <CheckCircleFilled style={{ color: "#52C41A" }} />}
                        {(data || iterations > 1) && <Tag style={{ marginInlineEnd: 0 }}>#{it.iteration}</Tag>}
                        <Method method={it.method} />
                        <a onClick={() => nav(`/requests/${it.requestId}`)} style={{ flex: 1 }}>
                          {it.name}
                        </a>
                        {it.result && (
                          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                            {it.result.status || "—"} · {it.result.durationMs} ms
                          </Typography.Text>
                        )}
                      </div>
                      {(it.error || it.result?.error) && <div style={{ color: token.colorError, fontSize: 12, marginLeft: 22 }}>{it.error || it.result?.error}</div>}
                      {(it.result?.tests ?? []).map((t, j) => (
                        <div key={j} style={{ fontSize: 12, marginLeft: 22, color: t.pass ? token.colorTextSecondary : token.colorError }}>
                          {t.pass ? "✓" : "✗"} {t.name}
                          {t.error ? `: ${t.error}` : ""}
                        </div>
                      ))}
                    </div>
                  );
                })}
              </div>
            </>
          )}
        </Col>
      </Row>
    </Layout.Content>
  );
}
