import { App } from "antd";
import { Check, Copy, Loader2, MoreHorizontal, X } from "lucide-react";
import { useEffect, useMemo, useState, type CSSProperties, type ReactNode } from "react";
import type { SendResult } from "../../api";
import CodeEditor from "../../components/CodeEditor";
import { bytes, prettyBody } from "../../lib";
import { Button, Menu, Status, Tabs } from "../../ui";

const phaseColors: Record<string, string> = {
  "Socket wait": "#6B6B74",
  "DNS lookup": "#5CD6C0",
  "TCP connect": "#F2B55B",
  "TLS handshake": "#B39DFF",
  "Request sent": "#7EB6FF",
  "Waiting (TTFB)": "#4ADE80",
  "Content download": "#3D8BFF",
};

const mono: CSSProperties = { fontFamily: "'Geist Mono Variable', ui-monospace, Menlo, monospace", fontSize: 12.5 };
const row: CSSProperties = { borderBottom: "1px solid var(--rq-line)" };
const note: CSSProperties = { padding: "10px 0", fontSize: 12, color: "var(--rq-mute)" };

/** A quiet message in the middle of the pane. */
const Quiet = ({ children }: { children: ReactNode }) => <div style={{ padding: "48px 0", textAlign: "center", color: "var(--rq-mute)" }}>{children}</div>;

export default function ResponsePane({ result: raw, sending, onCancel, onSaveExample }: { result: SendResult | null; sending: boolean; onCancel: () => void; onSaveExample?: (r: SendResult) => void }) {
  // A request that didn't complete (bad URL, unreachable host, CORS) has no
  // response headers or timeline; older servers and history send null for them.
  const result = useMemo(
    () => (raw ? { ...raw, headers: raw.headers ?? [], requestHeaders: raw.requestHeaders ?? [], timeline: raw.timeline ?? [], body: raw.body ?? "" } : null),
    [raw],
  );
  const { message } = App.useApp();
  const [tab, setTab] = useState("body");
  const events = result?.events ?? [];
  // A stream opens on its events.
  useEffect(() => {
    if (result?.events?.length) setTab("events");
    else setTab((t) => (t === "events" ? "body" : t));
  }, [result]);
  const body = useMemo(() => (result ? prettyBody(result.body) : null), [result]);
  const tests = result?.tests ?? [];
  const passed = tests.filter((t) => t.pass).length;

  const meta = sending ? (
    <>
      <span style={{ display: "inline-flex", alignItems: "center", gap: 6, color: "var(--rq-dim)" }}>
        <Loader2 className="rq-spin" size={14} /> Sending…
      </span>
      <Button size="sm" onClick={onCancel}>
        Cancel
      </Button>
    </>
  ) : result ? (
    <>
      <Status status={result.status} text={result.statusText} />
      <span style={{ color: "var(--rq-mute)", fontVariantNumeric: "tabular-nums" }}>{result.durationMs} ms</span>
      <span style={{ color: "var(--rq-mute)" }}>{bytes(result.sizeBytes)}</span>
      <Menu
        align="end"
        trigger={<Button variant="ghost" size="sm" icon={<MoreHorizontal />} aria-label="Response actions" />}
        items={[
          { key: "copy", icon: <Copy />, label: "Copy response" },
          ...(onSaveExample && result.status > 0 ? [{ key: "example", label: "Save as example" }] : []),
        ]}
        onSelect={(k) => {
          if (k === "copy") {
            navigator.clipboard.writeText(result.body);
            message.success("Response copied");
          }
          if (k === "example") onSaveExample?.(result);
        }}
      />
    </>
  ) : null;

  let content: ReactNode;
  if (sending) {
    content = (
      <div style={{ display: "flex", flexDirection: "column", gap: 10, paddingTop: 6 }}>
        {[38, 62, 54, 46, 58, 30].map((w, i) => (
          <div key={i} style={{ height: 10, width: `${w}%`, borderRadius: 4, background: "var(--rq-raise)" }} />
        ))}
      </div>
    );
  } else if (!result) {
    content = (
      <Quiet>
        Send the request to see the response. <span className="rq-kbd">⌘↵</span>
      </Quiet>
    );
  } else if (tab === "body") {
    content = result.error ? (
      <div style={{ borderLeft: "2px solid var(--rq-err)", padding: "4px 0 4px 12px" }}>
        <div style={{ fontWeight: 500, marginBottom: 4 }}>The request didn't complete</div>
        <div style={{ color: "var(--rq-dim)", lineHeight: 1.55 }}>{result.error}</div>
      </div>
    ) : (
      <div style={{ height: "100%", overflow: "hidden" }}>
        <CodeEditor value={body!.text} language={body!.language} readOnly />
      </div>
    );
  } else if (tab === "events") {
    const ended = { closed: "The server closed the stream.", timeout: "Stopped at the request timeout (Settings → Request timeout).", limit: "Stopped at the size or event limit." }[result.streamEnded ?? "closed"];
    content = (
      <div>
        <div style={note}>
          {events.length} event{events.length === 1 ? "" : "s"}. {ended}
        </div>
        {events.map((e, i) => (
          <div key={i} style={{ ...row, display: "flex", gap: 12, padding: "7px 0", alignItems: "baseline" }}>
            <span style={{ width: 64, flexShrink: 0, color: "var(--rq-mute)", fontVariantNumeric: "tabular-nums" }}>{Math.round(e.atMs)} ms</span>
            <span style={{ width: 110, flexShrink: 0, color: "var(--rq-var)", overflow: "hidden", textOverflow: "ellipsis" }}>
              {e.event || "message"}
              {e.id ? ` #${e.id}` : ""}
            </span>
            <pre style={{ ...mono, margin: 0, whiteSpace: "pre-wrap", wordBreak: "break-all", flex: 1 }}>{prettyData(e.data)}</pre>
          </div>
        ))}
      </div>
    );
  } else if (tab === "headers") {
    content = (
      <div>
        {result.headers.map((h, i) => (
          <div key={i} style={{ ...row, display: "grid", gridTemplateColumns: "minmax(140px, 38%) 1fr", gap: 16, padding: "8px 0" }}>
            <span style={{ ...mono, color: "var(--rq-dim)" }}>{h.key}</span>
            <span style={{ ...mono, wordBreak: "break-all" }}>{h.value}</span>
          </div>
        ))}
        <div style={note}>Sent {result.requestHeaders.length} request headers. Secret values show as ••••••••.</div>
      </div>
    );
  } else if (tab === "tests") {
    content = tests.length ? (
      <div>
        <div style={note}>
          {passed} passed · {tests.length - passed} failed
        </div>
        {tests.map((t, i) => (
          <div key={i} style={{ ...row, display: "flex", alignItems: "flex-start", gap: 10, padding: "9px 0" }}>
            {t.pass ? <Check size={15} color="var(--rq-ok)" style={{ marginTop: 1 }} /> : <X size={15} color="var(--rq-err)" style={{ marginTop: 1 }} />}
            <span style={{ flex: 1 }}>
              {t.name}
              {t.error && <div style={{ fontSize: 12, color: "var(--rq-err)", marginTop: 2 }}>{t.error}</div>}
            </span>
            <span style={{ fontSize: 12, color: "var(--rq-mute)" }}>{t.ms} ms</span>
          </div>
        ))}
        {result.logs && result.logs.length > 0 && <pre style={{ ...mono, fontSize: 12, marginTop: 12, padding: 12, background: "var(--rq-raise)", borderRadius: 8 }}>{result.logs.join("\n")}</pre>}
      </div>
    ) : (
      <Quiet>No tests yet. Add them under Scripts → After response.</Quiet>
    );
  } else if (tab === "cookies") {
    content = (
      <div>
        {(result.cookies ?? []).map((c, i) => (
          <div key={i} style={{ ...row, display: "grid", gridTemplateColumns: "160px 1fr 160px", gap: 16, padding: "8px 0" }}>
            <span style={{ ...mono, color: "var(--rq-dim)" }}>{c.name}</span>
            <span style={{ ...mono, wordBreak: "break-all" }}>{c.value}</span>
            <span style={{ color: "var(--rq-mute)" }}>
              {c.domain}
              {c.path}
            </span>
          </div>
        ))}
        <div style={note}>Kept and sent with later requests to this site. Manage them under Cookies in the sidebar.</div>
      </div>
    );
  } else {
    const total = Math.max(result.durationMs, 1);
    content = (
      <div style={{ paddingTop: 4 }}>
        {result.timeline.map((p) => (
          <div key={p.name} style={{ display: "flex", alignItems: "center", gap: 14, height: 32 }}>
            <span style={{ width: 130, color: "var(--rq-dim)" }}>{p.name}</span>
            <div style={{ flex: 1, height: 6, borderRadius: 3, background: "var(--rq-raise)", position: "relative", overflow: "hidden" }}>
              <div style={{ position: "absolute", left: `${(p.startMs / total) * 100}%`, width: `max(2px, ${(p.ms / total) * 100}%)`, top: 0, bottom: 0, borderRadius: 3, background: phaseColors[p.name] ?? "var(--rq-accent)" }} />
            </div>
            <span style={{ width: 70, textAlign: "right", color: "var(--rq-dim)", fontVariantNumeric: "tabular-nums" }}>{p.ms < 1 ? p.ms.toFixed(2) : Math.round(p.ms)} ms</span>
          </div>
        ))}
        <div style={{ display: "flex", justifyContent: "space-between", borderTop: "1px solid var(--rq-line)", marginTop: 6, paddingTop: 10, fontWeight: 500 }}>
          <span>Total</span>
          <span style={{ fontVariantNumeric: "tabular-nums" }}>{result.durationMs} ms</span>
        </div>
      </div>
    );
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%", padding: "10px 24px 16px 20px" }}>
      {/* Same row as the request's tabs, so the two halves line up. */}
      <div style={{ marginBottom: 12 }}>
        <Tabs
          label="Response"
          value={tab}
          onChange={setTab}
          end={meta}
          items={[
            { key: "body", label: "Body" },
            ...(events.length ? [{ key: "events", label: "Events", count: events.length }] : []),
            { key: "headers", label: "Headers", count: result?.headers.length },
            { key: "tests", label: "Tests", count: tests.length ? `${passed}/${tests.length}` : undefined, tone: tests.length ? (passed === tests.length ? ("ok" as const) : ("err" as const)) : undefined },
            { key: "timeline", label: "Timeline" },
            ...(result?.cookies?.length ? [{ key: "cookies", label: "Cookies", count: result.cookies.length }] : []),
          ]}
        />
      </div>
      {result?.scriptError && !sending && (
        <div style={{ borderLeft: "2px solid var(--rq-warn)", padding: "2px 0 2px 10px", marginBottom: 12, color: "var(--rq-dim)", fontSize: 12.5 }}>{result.scriptError}</div>
      )}
      <div style={{ flex: 1, minHeight: 0, overflow: tab === "body" ? "hidden" : "auto" }}>{content}</div>
    </div>
  );
}

/** An event's data, indented when it's JSON. */
function prettyData(d: string): string {
  const t = d.trim();
  if (!t.startsWith("{") && !t.startsWith("[")) return d;
  try {
    return JSON.stringify(JSON.parse(t), null, 2);
  } catch {
    return d;
  }
}
