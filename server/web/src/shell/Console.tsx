import { CaretDownOutlined, CaretRightOutlined, ClearOutlined, CloseOutlined, CodeOutlined, SearchOutlined } from "@ant-design/icons";
import { Badge, Button, Empty, Input, Segmented, Tabs, theme, Tooltip, Typography } from "antd";
import dayjs from "dayjs";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { KV } from "../api";
import { Method, mono, StatusTag } from "../components/bits";
import { bytes, prettyBody } from "../lib";
import { clearConsole, hasProblem, useConsoleEntries, type ConsoleEntry } from "./consoleStore";

type Filter = "all" | "network" | "logs" | "errors";
type Line =
  | { kind: "net"; key: string; e: ConsoleEntry }
  | { kind: "log" | "warn" | "error"; key: string; e: ConsoleEntry; text: string };

const HEIGHT_KEY = "requara.consoleHeight";
const OPEN_KEY = "requara.consoleOpen";

function load<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key);
    return raw === null ? fallback : (JSON.parse(raw) as T);
  } catch {
    return fallback;
  }
}
function store(key: string, value: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    /* storage unavailable */
  }
}

// One entry becomes several lines, in the order things happened: script logs, problems, then the request.
function linesFor(e: ConsoleEntry): Line[] {
  const r = e.result;
  const out: Line[] = [];
  (r?.logs ?? []).forEach((text, i) => out.push({ kind: "log", key: `${e.id}-l${i}`, e, text }));
  if (r?.scriptError) out.push({ kind: "warn", key: `${e.id}-s`, e, text: r.scriptError });
  (r?.tests ?? []).filter((t) => !t.pass).forEach((t, i) => out.push({ kind: "error", key: `${e.id}-t${i}`, e, text: `Test failed: ${t.name}${t.error ? ` — ${t.error}` : ""}` }));
  if (e.error) out.push({ kind: "error", key: `${e.id}-e`, e, text: e.error });
  else if (r?.error) out.push({ kind: "error", key: `${e.id}-re`, e, text: r.error });
  out.push({ kind: "net", key: `${e.id}-n`, e });
  return out;
}

/** Postman-style console: a status bar at the bottom of the window that opens a log of every send. */
export default function ConsoleDock() {
  const { token } = theme.useToken();
  const entries = useConsoleEntries();
  const [open, setOpenState] = useState(() => load(OPEN_KEY, false));
  const [height, setHeight] = useState(() => load(HEIGHT_KEY, 280));
  const [filter, setFilter] = useState<Filter>("all");
  const [q, setQ] = useState("");
  const [expanded, setExpanded] = useState<Set<number>>(new Set());
  const [seen, setSeen] = useState(0); // problems acknowledged by opening the console
  const list = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const heightRef = useRef(height);

  const setOpen = (v: boolean) => {
    setOpenState(v);
    store(OPEN_KEY, v);
  };

  // ⌥⌘C toggles, as in Postman.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.altKey && e.code === "KeyC") {
        e.preventDefault();
        setOpenState((o) => (store(OPEN_KEY, !o), !o));
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const problems = entries.filter(hasProblem).length;
  useEffect(() => {
    if (open) setSeen(problems);
  }, [open, problems]);

  const lines = useMemo(() => {
    const ql = q.trim().toLowerCase();
    return entries.flatMap(linesFor).filter((l) => {
      if (filter === "network" && l.kind !== "net") return false;
      if (filter === "logs" && l.kind === "net") return false;
      if (filter === "errors" && !(l.kind === "error" || l.kind === "warn" || (l.kind === "net" && hasProblem(l.e)))) return false;
      if (!ql) return true;
      const hay = l.kind === "net" ? `${l.e.method} ${l.e.result?.url ?? l.e.url} ${l.e.result?.status ?? ""} ${l.e.name}` : l.text;
      return hay.toLowerCase().includes(ql);
    });
  }, [entries, filter, q]);

  // Follow new output while scrolled to the bottom.
  useEffect(() => {
    const el = list.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [lines.length, open]);

  const startDrag = (e: React.MouseEvent) => {
    e.preventDefault();
    const startY = e.clientY;
    const startH = heightRef.current;
    const move = (ev: MouseEvent) => {
      heightRef.current = Math.min(window.innerHeight * 0.75, Math.max(120, startH + startY - ev.clientY));
      setHeight(heightRef.current);
    };
    const up = () => {
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", up);
      store(HEIGHT_KEY, heightRef.current);
    };
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", up);
  };

  const toggle = (id: number) => setExpanded((s) => (s.has(id) ? (s.delete(id), new Set(s)) : new Set(s.add(id))));
  const unseen = Math.max(0, problems - seen);
  const tone = { log: token.colorText, warn: token.colorWarningText, error: token.colorErrorText };

  return (
    <div style={{ flexShrink: 0, background: token.colorBgContainer, borderTop: `1px solid ${token.colorSplit}` }}>
      {open && (
        <div style={{ height, display: "flex", flexDirection: "column", minHeight: 0 }}>
          <div onMouseDown={startDrag} style={{ height: 5, marginTop: -3, cursor: "row-resize", flexShrink: 0 }} />
          <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "0 12px 6px", flexShrink: 0 }}>
            <Typography.Text strong style={{ marginRight: 8 }}>
              Console
            </Typography.Text>
            <Input
              size="small"
              allowClear
              prefix={<SearchOutlined style={{ color: token.colorTextTertiary }} />}
              placeholder="Search URLs, status codes and logs"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              style={{ width: 280 }}
            />
            <Segmented
              size="small"
              value={filter}
              onChange={(v) => setFilter(v as Filter)}
              options={[
                { value: "all", label: "All" },
                { value: "network", label: "Network" },
                { value: "logs", label: "Logs" },
                { value: "errors", label: "Errors" },
              ]}
            />
            <div style={{ flex: 1 }} />
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {entries.length} {entries.length === 1 ? "request" : "requests"} this session
            </Typography.Text>
            <Tooltip title="Clear console">
              <Button size="small" type="text" icon={<ClearOutlined />} aria-label="Clear console" onClick={() => (clearConsole(), setExpanded(new Set()), setSeen(0))} />
            </Tooltip>
            <Tooltip title="Close (⌥⌘C)">
              <Button size="small" type="text" icon={<CloseOutlined />} aria-label="Close console" onClick={() => setOpen(false)} />
            </Tooltip>
          </div>
          <div
            ref={list}
            onScroll={(e) => {
              const el = e.currentTarget;
              stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
            }}
            style={{ flex: 1, minHeight: 0, overflow: "auto", borderTop: `1px solid ${token.colorSplit}`, ...mono, fontSize: 12 }}
          >
            {lines.length === 0 ? (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                style={{ margin: "24px 0" }}
                description={entries.length ? "Nothing matches the filter." : "Send a request to see its details, script logs and errors here."}
              />
            ) : (
              lines.map((l) =>
                l.kind === "net" ? (
                  <NetRow key={l.key} e={l.e} open={expanded.has(l.e.id)} onToggle={() => toggle(l.e.id)} />
                ) : (
                  <div key={l.key} style={{ display: "flex", gap: 12, padding: "4px 12px", borderBottom: `1px solid ${token.colorSplit}`, background: l.kind === "error" ? token.colorErrorBg : l.kind === "warn" ? token.colorWarningBg : undefined }}>
                    <Time at={l.e.at} />
                    <span style={{ width: 14 }} />
                    <span style={{ flex: 1, whiteSpace: "pre-wrap", wordBreak: "break-word", color: tone[l.kind] }}>{l.text}</span>
                  </div>
                ),
              )
            )}
          </div>
        </div>
      )}
      <div style={{ display: "flex", alignItems: "center", gap: 4, height: 28, padding: "0 8px", borderTop: open ? `1px solid ${token.colorSplit}` : undefined }}>
        <Button size="small" type="text" icon={<CodeOutlined />} onClick={() => setOpen(!open)} style={{ fontSize: 12, color: open ? token.colorPrimary : token.colorTextSecondary }}>
          Console
          {unseen > 0 && !open && <Badge count={unseen} size="small" style={{ marginLeft: 4 }} />}
        </Button>
        <div style={{ flex: 1 }} />
        <Typography.Text type="secondary" style={{ fontSize: 11 }}>
          ⌥⌘C
        </Typography.Text>
      </div>
    </div>
  );
}

function Time({ at }: { at: number }) {
  const { token } = theme.useToken();
  return <span style={{ color: token.colorTextTertiary, flexShrink: 0, fontVariantNumeric: "tabular-nums" }}>{dayjs(at).format("HH:mm:ss.SSS")}</span>;
}

function NetRow({ e, open, onToggle }: { e: ConsoleEntry; open: boolean; onToggle: () => void }) {
  const { token } = theme.useToken();
  const r = e.result;
  return (
    <div style={{ borderBottom: `1px solid ${token.colorSplit}` }}>
      <div onClick={onToggle} className="requara-row" style={{ display: "flex", alignItems: "center", gap: 12, padding: "4px 12px", cursor: "pointer" }}>
        <Time at={e.at} />
        {open ? <CaretDownOutlined style={{ fontSize: 10, width: 14, color: token.colorTextTertiary }} /> : <CaretRightOutlined style={{ fontSize: 10, width: 14, color: token.colorTextTertiary }} />}
        <Method method={e.method} width={34} />
        <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{r?.url || e.url}</span>
        {r ? <StatusTag status={r.status} text={r.status ? r.statusText : undefined} /> : <StatusTag status={0} />}
        <span style={{ width: 64, textAlign: "right", color: token.colorTextSecondary }}>{r ? `${r.durationMs} ms` : "—"}</span>
        <span style={{ width: 72, textAlign: "right", color: token.colorTextSecondary }}>{r ? bytes(r.sizeBytes) : ""}</span>
      </div>
      {open && <Details e={e} />}
    </div>
  );
}

function Details({ e }: { e: ConsoleEntry }) {
  const { token } = theme.useToken();
  const r = e.result;
  if (!r) return <div style={{ padding: "8px 12px 12px 52px", color: token.colorErrorText }}>{e.error}</div>;
  const resBody = r.body ? prettyBody(r.body).text : "";
  const reqBody = r.requestBody ? prettyBody(r.requestBody).text : "";
  const section = (title: string, children: ReactNode) => (
    <div style={{ marginBottom: 10 }}>
      <div style={{ color: token.colorTextTertiary, marginBottom: 4, fontFamily: token.fontFamily, fontWeight: 600 }}>{title}</div>
      {children}
    </div>
  );
  const headers = (h: KV[]) =>
    h.length ? (
      <div style={{ display: "grid", gridTemplateColumns: "minmax(140px, max-content) 1fr", columnGap: 16, rowGap: 2 }}>
        {h.map((x, i) => [
          <span key={`k${i}`} style={{ color: token.colorTextSecondary }}>{x.key}</span>,
          <span key={`v${i}`} style={{ wordBreak: "break-all" }}>{x.value}</span>,
        ])}
      </div>
    ) : (
      <span style={{ color: token.colorTextTertiary }}>None</span>
    );
  const body = (text: string, note?: string) =>
    text ? (
      <>
        <pre style={{ margin: 0, maxHeight: 260, overflow: "auto", padding: 8, background: token.colorFillQuaternary, borderRadius: 6, whiteSpace: "pre-wrap", wordBreak: "break-word" }}>{text}</pre>
        {note && <div style={{ color: token.colorTextTertiary, marginTop: 4 }}>{note}</div>}
      </>
    ) : (
      <span style={{ color: token.colorTextTertiary }}>No body</span>
    );

  return (
    <div style={{ padding: "0 12px 8px 52px", background: token.colorFillQuaternary }}>
      <Tabs
        size="small"
        items={[
          {
            key: "req",
            label: "Request",
            children: (
              <>
                {section("General", (
                  <div>
                    {e.method} {r.url}
                    <div style={{ color: token.colorTextTertiary }}>
                      {e.name}
                      {e.env ? ` · environment: ${e.env}` : ""}
                    </div>
                  </div>
                ))}
                {section("Request headers", headers(r.requestHeaders ?? []))}
                {section("Request body", body(reqBody))}
              </>
            ),
          },
          {
            key: "res",
            label: "Response",
            children: (
              <>
                {r.error ? section("Error", <span style={{ color: token.colorErrorText }}>{r.error}</span>) : null}
                {section("Response headers", headers(r.headers ?? []))}
                {section("Response body", body(resBody, r.truncated ? "Truncated: the runner keeps the first part of large bodies." : undefined))}
              </>
            ),
          },
          {
            key: "timing",
            label: "Timing",
            children: section(
              `Total ${r.durationMs} ms`,
              <div style={{ display: "grid", gridTemplateColumns: "160px 80px", rowGap: 2 }}>
                {(r.timeline ?? []).map((p) => [
                  <span key={p.name} style={{ color: token.colorTextSecondary }}>{p.name}</span>,
                  <span key={p.name + "ms"} style={{ textAlign: "right" }}>{p.ms < 1 ? p.ms.toFixed(2) : Math.round(p.ms)} ms</span>,
                ])}
              </div>,
            ),
          },
          ...((r.tests ?? []).length
            ? [
                {
                  key: "tests",
                  label: `Tests ${(r.tests ?? []).filter((t) => t.pass).length}/${(r.tests ?? []).length}`,
                  children: (
                    <div>
                      {(r.tests ?? []).map((t, i) => (
                        <div key={i} style={{ color: t.pass ? token.colorSuccessText : token.colorErrorText }}>
                          {t.pass ? "PASS" : "FAIL"} {t.name}
                          {t.error ? ` — ${t.error}` : ""}
                        </div>
                      ))}
                    </div>
                  ),
                },
              ]
            : []),
        ]}
      />
    </div>
  );
}
