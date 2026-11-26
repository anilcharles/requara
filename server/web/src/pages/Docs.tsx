import { DownloadOutlined } from "@ant-design/icons";
import { useQuery } from "@tanstack/react-query";
import { Alert, Breadcrumb, Button, ConfigProvider, Empty, Layout, Space, Spin, Tabs, Tag, theme, theme as antTheme, Typography } from "antd";
import { useState, type ReactNode } from "react";
import { Navigate, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { get, type ApiSummary, type Docs as DocsT } from "../api";
import { mono } from "../components/bits";
import CodeEditor from "../components/CodeEditor";
import { methodColor } from "../lib";
import { CollectionsSider } from "../shell/Layouts";
import { PrefsContext, usePrefs } from "../shell/session";

export default function Docs() {
  const { apiId } = useParams();
  const [params, setParams] = useSearchParams();
  const nav = useNavigate();
  const apis = useQuery({ queryKey: ["apis", "all"], queryFn: () => get<{ items: ApiSummary[] }>("/api/apis?status=") });
  const source = params.get("source") ?? "";
  const docs = useQuery({ queryKey: ["docs", apiId, source], queryFn: () => get<DocsT>(`/api/apis/${apiId}/docs?source=${source}`), enabled: !!apiId });
  if (!apiId) {
    const first = apis.data?.items.find((a) => a.name === "Payments API") ?? apis.data?.items[0];
    return first ? <Navigate to={`/docs/${first.id}`} replace /> : <Spin style={{ margin: 40 }} />;
  }
  const wanted = params.get("op");
  const key = docs.data?.operations.some((o) => o.key === wanted) ? wanted! : docs.data?.operations[0]?.key;
  return (
    <>
      <CollectionsSider
        onPick={(r, c) => (!r.operationKey ? nav(`/requests/${r.id}`) : c.id === apiId ? setParams({ op: r.operationKey, ...(source ? { source } : {}) }) : nav(`/docs/${c.id}?op=${encodeURIComponent(r.operationKey)}`))}
        isSelected={(r, c) => c.id === apiId && r.operationKey === key}
      />
      <Layout.Content style={{ overflow: "auto" }}>
        {docs.isLoading ? <Spin style={{ margin: 40 }} /> : docs.isError ? <Alert type="error" showIcon message="Couldn't load the docs" description={(docs.error as Error).message} style={{ margin: 24 }} /> : docs.data ? <Page docs={docs.data} opKey={key} /> : null}
      </Layout.Content>
    </>
  );
}

function Page({ docs, opKey }: { docs: DocsT; opKey?: string }) {
  const { token } = theme.useToken();
  const op = docs.operations.find((o) => o.key === opKey) ?? docs.operations[0];
  const langs = op ? Object.keys(op.samples ?? {}) : [];
  const [lang, setLang] = useState("cURL");
  const [status, setStatus] = useState<string>("");
  if (!op) return <Empty style={{ marginTop: 80 }} description="No endpoints to document yet." />;
  const resp = (op.responses ?? []).find((r) => r.status === status) ?? op.responses?.find((r) => r.status.startsWith("2")) ?? op.responses?.[0];

  const section = (title: string) => (
    <Typography.Title level={5} style={{ marginTop: 32, paddingBottom: 8, borderBottom: `1px solid ${token.colorSplit}` }}>
      {title}
    </Typography.Title>
  );

  return (
    <div style={{ display: "flex", gap: 56, padding: "40px 48px 40px 56px", background: token.colorBgContainer, minHeight: "100%" }}>
      <article style={{ flex: 1, minWidth: 0, maxWidth: 680 }}>
        {docs.draft && <Alert type="warning" showIcon message="Preview of unpublished changes. Readers see the last published version." style={{ marginBottom: 20 }} />}
        <Breadcrumb items={[{ title: docs.name }, { title: op.tag }]} />
        <Typography.Title level={2} style={{ margin: "12px 0", fontWeight: 600, letterSpacing: -0.3 }}>
          {op.summary || op.key}
        </Typography.Title>
        <div style={{ display: "inline-flex", gap: 10, alignItems: "center", padding: "6px 12px 6px 10px", background: token.colorFillQuaternary, border: `1px solid ${token.colorSplit}`, borderRadius: 6 }}>
          <span style={{ ...mono, fontWeight: 700, fontSize: 12, color: methodColor[op.method] }}>{op.method}</span>
          <span style={{ ...mono, fontSize: 13 }}>
            {docs.baseUrl}
            {op.path}
          </span>
        </div>
        {op.description && (
          <Typography.Paragraph style={{ fontSize: 16, lineHeight: "26px", color: token.colorTextSecondary, marginTop: 20 }}>{op.description}</Typography.Paragraph>
        )}
        {op.params?.length > 0 && (
          <>
            {section("Parameters")}
            {op.params.map((p) => (
              <div key={p.in + p.name} style={{ padding: "12px 0", borderBottom: `1px solid ${token.colorSplit}` }}>
                <Space size={10}>
                  <span style={{ ...mono, fontWeight: 600 }}>{p.name}</span>
                  <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                    {p.type} · {p.in}
                  </Typography.Text>
                  {p.required && <Typography.Text style={{ fontSize: 12, color: "#D46B08" }}>required</Typography.Text>}
                </Space>
                {p.description && <div style={{ color: token.colorTextSecondary, marginTop: 4 }}>{p.description}</div>}
              </div>
            ))}
          </>
        )}
        {op.fields?.length > 0 && (
          <>
            {section("Body parameters")}
            {op.fields.map((f, i) => (
              <div key={i} style={{ padding: "12px 0", paddingLeft: f.depth * 24, borderBottom: `1px solid ${token.colorSplit}` }}>
                <Space size={10}>
                  <span style={{ ...mono, fontWeight: 600 }}>{f.name}</span>
                  <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                    {f.type}
                  </Typography.Text>
                  {f.required && <Typography.Text style={{ fontSize: 12, color: "#D46B08" }}>required</Typography.Text>}
                </Space>
                {f.description && <div style={{ color: token.colorTextSecondary, marginTop: 4 }}>{f.description}</div>}
                {f.enum && <div style={{ marginTop: 6 }}>{f.enum.map((e) => <Tag key={e} style={mono}>{e}</Tag>)}</div>}
              </div>
            ))}
          </>
        )}
        {section("Responses")}
        {(op.responses ?? []).map((r) => (
          <div key={r.status} style={{ display: "flex", gap: 12, padding: "8px 0" }}>
            <Tag color={r.status.startsWith("2") ? "success" : r.status.startsWith("4") ? "warning" : "error"} style={mono}>
              {r.status}
            </Tag>
            <span>{r.description}</span>
          </div>
        ))}
        <Space style={{ marginTop: 32 }}>
          <Button icon={<DownloadOutlined />} href={`/api/apis/${docs.id}/spec?format=yaml`}>
            OpenAPI (YAML)
          </Button>
          <Button href={`/api/apis/${docs.id}/spec?format=json`}>JSON</Button>
          {docs.mockUrl && (
            <Typography.Text type="secondary" style={{ fontSize: 13 }}>
              Mock: <Typography.Text copyable style={{ ...mono, fontSize: 12 }}>{docs.mockUrl}</Typography.Text>
            </Typography.Text>
          )}
        </Space>
      </article>
      <aside style={{ width: 520, flexShrink: 0, display: "flex", flexDirection: "column", gap: 16, position: "sticky", top: 24, alignSelf: "flex-start" }}>
        <CodePanel title="Request" tabs={langs} active={langs.includes(lang) ? lang : langs[0]} onTab={setLang} code={op.samples[langs.includes(lang) ? lang : langs[0]]} language={lang === "Python" ? "python" : lang === "Node" ? "javascript" : "shell"} />
        {resp && <CodePanel title="Response" tabs={(op.responses ?? []).map((r) => r.status)} active={resp.status} onTab={setStatus} code={resp.example || "(no body)"} language="json" />}
      </aside>
    </div>
  );
}

function CodePanel({ title, tabs, active, onTab, code, language }: { title: string; tabs: string[]; active: string; onTab: (t: string) => void; code: string; language: string }) {
  return (
    <div style={{ background: "#141414", borderRadius: 10, overflow: "hidden" }}>
      <div style={{ display: "flex", alignItems: "center", height: 44, padding: "0 16px", background: "#1F1F1F", borderBottom: "1px solid #FFFFFF14" }}>
        <span style={{ flex: 1, color: "#FFFFFFD9", fontWeight: 500, fontSize: 13 }}>{title}</span>
        <Tabs size="small" activeKey={active} onChange={onTab} items={tabs.map((t) => ({ key: t, label: t }))} tabBarStyle={{ margin: 0 }} className="speklo-dark-tabs" />
      </div>
      <ForceDark>
        <CodeEditor value={code} language={language} readOnly background="#141414" maxAutoHeight={420} />
      </ForceDark>
    </div>
  );
}

// The code panels are always dark, like the design, regardless of app theme.
function ForceDark({ children }: { children: ReactNode }) {
  const ctx = usePrefs();
  return (
    <ConfigProvider theme={{ algorithm: antTheme.darkAlgorithm }}>
      <PrefsContext.Provider value={{ ...ctx, isDark: true }}>{children}</PrefsContext.Provider>
    </ConfigProvider>
  );
}
