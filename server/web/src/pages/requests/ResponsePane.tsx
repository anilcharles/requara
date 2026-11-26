import { CheckCircleFilled, CloseCircleFilled, CopyOutlined, LoadingOutlined } from "@ant-design/icons";
import { Alert, App, Button, Divider, Empty, Skeleton, Space, Tabs, theme, Typography } from "antd";
import { useMemo, useState } from "react";
import type { SendResult } from "../../api";
import { mono, StatusTag } from "../../components/bits";
import CodeEditor from "../../components/CodeEditor";
import { bytes, prettyBody } from "../../lib";

const phaseColors: Record<string, string> = {
  "Socket wait": "#BFBFBF",
  "DNS lookup": "#36CFC9",
  "TCP connect": "#FFA940",
  "TLS handshake": "#9254DE",
  "Request sent": "#69B1FF",
  "Waiting (TTFB)": "#52C41A",
  "Content download": "#1677FF",
};

export default function ResponsePane({ result, sending, onCancel, onSaveExample }: { result: SendResult | null; sending: boolean; onCancel: () => void; onSaveExample?: (r: SendResult) => void }) {
  const { token } = theme.useToken();
  const { message } = App.useApp();
  const [tab, setTab] = useState("body");
  const body = useMemo(() => (result ? prettyBody(result.body) : null), [result]);
  const tests = result?.tests ?? [];
  const passed = tests.filter((t) => t.pass).length;

  const meta = sending ? (
    <Space size={10}>
      <LoadingOutlined style={{ color: token.colorPrimary }} />
      <Typography.Text type="secondary">Sending request…</Typography.Text>
      <Button size="small" onClick={onCancel}>
        Cancel
      </Button>
    </Space>
  ) : result ? (
    <Space size={10} split={<Divider type="vertical" style={{ margin: 0 }} />}>
      <StatusTag status={result.status} text={result.statusText} />
      <Typography.Text type="secondary" style={{ fontVariantNumeric: "tabular-nums" }}>
        {result.durationMs} ms
      </Typography.Text>
      <Typography.Text type="secondary">{bytes(result.sizeBytes)}</Typography.Text>
      <Button
        type="link"
        size="small"
        icon={<CopyOutlined />}
        style={{ padding: 0 }}
        onClick={() => {
          navigator.clipboard.writeText(result.body);
          message.success("Response copied");
        }}
      >
        Copy
      </Button>
      {onSaveExample && result.status > 0 && (
        <Button type="link" size="small" style={{ padding: 0 }} onClick={() => onSaveExample(result)}>
          Save as example
        </Button>
      )}
    </Space>
  ) : null;

  let content;
  if (sending) {
    content = (
      <div style={{ padding: "20px 24px", background: token.colorFillQuaternary, borderRadius: 8, height: "100%" }}>
        <Skeleton active title={false} paragraph={{ rows: 7, width: ["10%", "40%", "32%", "26%", "36%", "30%", "22%"] }} />
      </div>
    );
  } else if (!result) {
    content = <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={<span>Send the request to see the response. <Typography.Text keyboard>⌘↵</Typography.Text></span>} style={{ marginTop: 40 }} />;
  } else if (tab === "body") {
    content = result.error ? (
      <Alert type="error" showIcon message="The request didn't complete" description={result.error} />
    ) : (
      <div style={{ height: "100%", borderRadius: 8, overflow: "hidden", border: `1px solid ${token.colorSplit}` }}>
        <CodeEditor value={body!.text} language={body!.language} readOnly background={token.colorFillQuaternary} />
      </div>
    );
  } else if (tab === "headers") {
    content = (
      <div style={{ border: `1px solid ${token.colorSplit}`, borderRadius: 8, overflow: "hidden" }}>
        {result.headers.map((h, i) => (
          <div key={i} style={{ display: "grid", gridTemplateColumns: "260px 1fr", padding: "9px 16px", borderTop: i ? `1px solid ${token.colorSplit}` : undefined, fontSize: 13 }}>
            <span style={{ ...mono, color: token.colorTextSecondary }}>{h.key}</span>
            <span style={{ ...mono, wordBreak: "break-all" }}>{h.value}</span>
          </div>
        ))}
        <div style={{ padding: "8px 16px", background: token.colorFillQuaternary, fontSize: 12, color: token.colorTextTertiary, borderTop: `1px solid ${token.colorSplit}` }}>
          Sent {result.requestHeaders.length} request headers. Secret values are shown as ••••••••.
        </div>
      </div>
    );
  } else if (tab === "tests") {
    content = tests.length ? (
      <>
        <div style={{ display: "flex", alignItems: "center", gap: 16, marginBottom: 12 }}>
          <div style={{ flex: 1, height: 8, borderRadius: 4, background: token.colorErrorBg, overflow: "hidden" }}>
            <div style={{ width: `${(passed / tests.length) * 100}%`, height: "100%", background: "#52C41A" }} />
          </div>
          <Typography.Text type="secondary">
            {passed} passed · {tests.length - passed} failed
          </Typography.Text>
        </div>
        <div style={{ border: `1px solid ${token.colorSplit}`, borderRadius: 8 }}>
          {tests.map((t, i) => (
            <div key={i} style={{ display: "flex", alignItems: "center", gap: 12, padding: "0 16px", minHeight: 44, borderTop: i ? `1px solid ${token.colorSplit}` : undefined }}>
              {t.pass ? <CheckCircleFilled style={{ color: "#52C41A" }} /> : <CloseCircleFilled style={{ color: token.colorError }} />}
              <span style={{ ...mono, fontSize: 12, fontWeight: 600, color: t.pass ? "#389E0D" : token.colorError }}>{t.pass ? "PASS" : "FAIL"}</span>
              <span style={{ flex: 1 }}>
                {t.name}
                {t.error && <div style={{ fontSize: 12, color: token.colorError }}>{t.error}</div>}
              </span>
              <Typography.Text type="secondary" style={{ fontSize: 13 }}>
                {t.ms} ms
              </Typography.Text>
            </div>
          ))}
        </div>
        {result.logs && result.logs.length > 0 && (
          <pre style={{ ...mono, fontSize: 12, marginTop: 12, padding: 12, background: token.colorFillQuaternary, borderRadius: 8 }}>{result.logs.join("\n")}</pre>
        )}
      </>
    ) : (
      <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="No tests. Add them under Scripts → Post-response." />
    );
  } else if (tab === "cookies") {
    content = (
      <div style={{ border: `1px solid ${token.colorSplit}`, borderRadius: 8, overflow: "hidden" }}>
        {(result.cookies ?? []).map((c, i) => (
          <div key={i} style={{ display: "grid", gridTemplateColumns: "200px 1fr 200px", padding: "9px 16px", borderTop: i ? `1px solid ${token.colorSplit}` : undefined, fontSize: 13 }}>
            <span style={{ ...mono, color: token.colorTextSecondary }}>{c.name}</span>
            <span style={{ ...mono, wordBreak: "break-all" }}>{c.value}</span>
            <span style={{ color: token.colorTextTertiary }}>
              {c.domain}
              {c.path}
            </span>
          </div>
        ))}
        <div style={{ padding: "8px 16px", background: token.colorFillQuaternary, fontSize: 12, color: token.colorTextTertiary, borderTop: `1px solid ${token.colorSplit}` }}>
          Kept and sent with later requests to this site. Manage them under Cookies in the sidebar.
        </div>
      </div>
    );
  } else {
    const total = Math.max(result.durationMs, 1);
    content = (
      <div>
        {result.timeline.map((p) => (
          <div key={p.name} style={{ display: "flex", alignItems: "center", gap: 16, height: 34 }}>
            <span style={{ width: 160, fontSize: 13, color: token.colorTextSecondary }}>{p.name}</span>
            <div style={{ flex: 1, height: 12, borderRadius: 3, background: token.colorFillQuaternary, position: "relative", overflow: "hidden" }}>
              <div style={{ position: "absolute", left: `${(p.startMs / total) * 100}%`, width: `max(2px, ${(p.ms / total) * 100}%)`, top: 0, bottom: 0, borderRadius: 3, background: phaseColors[p.name] ?? token.colorPrimary }} />
            </div>
            <span style={{ width: 80, textAlign: "right", fontSize: 13, fontVariantNumeric: "tabular-nums" }}>{p.ms < 1 ? p.ms.toFixed(2) : Math.round(p.ms)} ms</span>
          </div>
        ))}
        <div style={{ display: "flex", justifyContent: "space-between", borderTop: `1px solid ${token.colorSplit}`, marginTop: 6, paddingTop: 10, fontWeight: 600, fontSize: 13 }}>
          <span>Total</span>
          <span style={{ fontVariantNumeric: "tabular-nums" }}>{result.durationMs} ms</span>
        </div>
      </div>
    );
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%", padding: "0 24px 20px" }}>
      <Tabs
        activeKey={tab}
        onChange={setTab}
        tabBarExtraContent={meta}
        items={[
          { key: "body", label: "Response" },
          { key: "headers", label: <span>Headers {result && <Typography.Text type="secondary" style={{ fontSize: 12 }}>{result.headers.length}</Typography.Text>}</span> },
          {
            key: "tests",
            label: (
              <span>
                Tests{" "}
                {tests.length > 0 && (
                  <span style={{ fontSize: 12, color: passed === tests.length ? "#389E0D" : token.colorError }}>
                    {passed}/{tests.length} passed
                  </span>
                )}
              </span>
            ),
          },
          { key: "timeline", label: "Timeline" },
          ...(result?.cookies?.length ? [{ key: "cookies", label: <span>Cookies <Typography.Text type="secondary" style={{ fontSize: 12 }}>{result.cookies.length}</Typography.Text></span> }] : []),
        ]}
        style={{ marginBottom: 0 }}
      />
      {result?.scriptError && !sending && <Alert type="warning" showIcon message={result.scriptError} style={{ marginBottom: 12 }} />}
      <div style={{ flex: 1, minHeight: 0, overflow: tab === "body" ? "hidden" : "auto", paddingTop: 4 }}>{content}</div>
    </div>
  );
}
