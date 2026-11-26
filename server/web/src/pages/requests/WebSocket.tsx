import { ArrowDownOutlined, ArrowUpOutlined, DeleteOutlined, InfoCircleOutlined } from "@ant-design/icons";
import { useQuery } from "@tanstack/react-query";
import { App, Button, Empty, Input, Space, Tag, theme, Typography } from "antd";
import dayjs from "dayjs";
import { useEffect, useRef, useState } from "react";
import { get, type RequestDoc, type Variable, type WsMessage } from "../../api";
import { mono } from "../../components/bits";
import CodeEditor from "../../components/CodeEditor";
import { useEnvironments } from "../../shell/session";

type LogLine = { dir: "out" | "in" | "info" | "error"; data: string; at: number };

/** A WebSocket request: connect from the browser, send and receive messages. */
export default function WebSocketPanel({ draft, set, readOnly }: { draft: RequestDoc; set: (p: Partial<RequestDoc>) => void; readOnly?: boolean }) {
  const { token } = theme.useToken();
  const { message } = App.useApp();
  const envs = useEnvironments();
  const activeId = envs.data?.activeId;
  // Non-secret variables of the active environment fill in {{name}}.
  const vars = useQuery({ queryKey: ["variables", activeId], queryFn: () => get<Variable[]>(`/api/environments/${activeId}/variables`), enabled: !!activeId });
  const sub = (s: string) => s.replace(/\{\{\s*([^}\s]+)\s*\}\}/g, (m, k) => vars.data?.find((v) => v.key === k && v.kind !== "secret")?.value ?? m);
  const [ws, setWs] = useState<WebSocket | null>(null);
  const [state, setState] = useState<"closed" | "connecting" | "open">("closed");
  const [log, setLog] = useState<LogLine[]>([]);
  const [compose, setCompose] = useState("");
  const [filter, setFilter] = useState("");
  const add = (l: Omit<LogLine, "at">) => setLog((x) => [...x.slice(-999), { ...l, at: Date.now() }]);
  const logEnd = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    logEnd.current?.scrollIntoView({ block: "nearest" }); // returns a Promise in newer browsers; don't hand it to React
  }, [log.length]);
  useEffect(() => () => ws?.close(), [ws]);

  const protocols = draft.headers.filter((h) => h.enabled && h.key.toLowerCase() === "sec-websocket-protocol").flatMap((h) => h.value.split(",").map((p) => p.trim()).filter(Boolean));
  const connect = () => {
    const url = sub(draft.url);
    if (!/^wss?:\/\//.test(url)) return message.info("A WebSocket URL starts with ws:// or wss://");
    try {
      const s = new WebSocket(url, protocols.length ? protocols : undefined);
      setState("connecting");
      add({ dir: "info", data: `Connecting to ${url}` });
      s.onopen = () => (setState("open"), add({ dir: "info", data: `Connected${s.protocol ? ` (protocol ${s.protocol})` : ""}` }));
      s.onmessage = (e) => add({ dir: "in", data: typeof e.data === "string" ? e.data : `(binary, ${(e.data as Blob).size ?? 0} bytes)` });
      s.onerror = () => add({ dir: "error", data: "Connection error" });
      s.onclose = (e) => (setState("closed"), add({ dir: "info", data: `Disconnected (${e.code}${e.reason ? ` ${e.reason}` : ""})` }));
      setWs(s);
    } catch (e) {
      add({ dir: "error", data: (e as Error).message });
    }
  };
  const send = () => {
    if (!ws || state !== "open" || !compose) return;
    const data = sub(compose);
    ws.send(data);
    add({ dir: "out", data });
  };
  const saveMessage = () => {
    const name = compose.trim().slice(0, 40) || "Message";
    set({ messages: [...(draft.messages ?? []), { id: crypto.randomUUID(), name, body: compose }] });
  };
  const messages: WsMessage[] = draft.messages ?? [];
  const shown = filter ? log.filter((l) => l.data.toLowerCase().includes(filter.toLowerCase())) : log;

  return (
    <div style={{ flex: 1, display: "flex", flexDirection: "column", minHeight: 0, padding: "16px 24px", gap: 12 }}>
      <div style={{ display: "flex", gap: 8 }}>
        <Input size="large" value={draft.url} disabled={readOnly || state !== "closed"} onChange={(e) => set({ url: e.target.value })} onPressEnter={connect} placeholder="wss://echo.websocket.org" style={{ ...mono, flex: 1 }} prefix={<Tag color="purple">WS</Tag>} />
        {state === "closed" ? (
          <Button size="large" type="primary" onClick={connect} style={{ width: 120 }}>
            Connect
          </Button>
        ) : (
          <Button size="large" danger onClick={() => ws?.close()} style={{ width: 120 }} loading={state === "connecting"}>
            Disconnect
          </Button>
        )}
      </div>
      <Input
        addonBefore="Subprotocols"
        disabled={readOnly || state !== "closed"}
        placeholder="graphql-ws, v1.chat (optional)"
        value={protocols.join(", ")}
        onChange={(e) =>
          set({
            headers: [...draft.headers.filter((h) => h.key.toLowerCase() !== "sec-websocket-protocol"), ...(e.target.value ? [{ key: "Sec-WebSocket-Protocol", value: e.target.value, enabled: true }] : [])],
          })
        }
      />
      <div style={{ display: "flex", gap: 12, minHeight: 180 }}>
        <div style={{ flex: 1, display: "flex", flexDirection: "column", gap: 8 }}>
          <div style={{ flex: 1, minHeight: 120, border: `1px solid ${token.colorBorder}`, borderRadius: 8, overflow: "hidden" }}>
            <CodeEditor path={`requara://ws/${draft.id}.json`} language={compose.trim().startsWith("{") ? "json" : "plaintext"} value={compose} onChange={setCompose} onSubmit={send} />
          </div>
          <Space>
            <Button type="primary" disabled={state !== "open" || !compose} onClick={send}>
              Send
            </Button>
            <Button disabled={!compose || readOnly} onClick={saveMessage}>
              Save message
            </Button>
          </Space>
        </div>
        <div style={{ width: 240, border: `1px solid ${token.colorSplit}`, borderRadius: 8, padding: 8, overflow: "auto" }}>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            Saved messages
          </Typography.Text>
          {messages.length === 0 && <div style={{ fontSize: 12, color: token.colorTextTertiary, marginTop: 6 }}>Save messages you send often.</div>}
          {messages.map((m) => (
            <div key={m.id} className="requara-row" style={{ display: "flex", alignItems: "center", gap: 6, padding: "4px 6px", borderRadius: 6, cursor: "pointer" }} onClick={() => setCompose(m.body)}>
              <span style={{ flex: 1, fontSize: 13, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{m.name}</span>
              {!readOnly && (
                <DeleteOutlined
                  className="requara-more"
                  onClick={(e) => {
                    e.stopPropagation();
                    set({ messages: messages.filter((x) => x.id !== m.id) });
                  }}
                />
              )}
            </div>
          ))}
        </div>
      </div>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <Typography.Text strong>Messages</Typography.Text>
        <Tag color={state === "open" ? "success" : state === "connecting" ? "processing" : "default"}>{state === "open" ? "Connected" : state === "connecting" ? "Connecting" : "Not connected"}</Tag>
        <div style={{ flex: 1 }} />
        <Input size="small" allowClear placeholder="Search messages" value={filter} onChange={(e) => setFilter(e.target.value)} style={{ width: 200 }} />
        <Button size="small" onClick={() => setLog([])}>
          Clear
        </Button>
      </div>
      <div style={{ flex: 1, minHeight: 160, overflow: "auto", border: `1px solid ${token.colorSplit}`, borderRadius: 8 }}>
        {shown.length === 0 && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="Connect, then send a message" style={{ marginTop: 24 }} />}
        {shown.map((l, i) => (
          <div key={i} style={{ display: "flex", gap: 10, padding: "6px 12px", borderTop: i ? `1px solid ${token.colorSplit}` : undefined, alignItems: "flex-start" }}>
            <span style={{ width: 16, color: l.dir === "out" ? token.colorPrimary : l.dir === "in" ? "#389E0D" : l.dir === "error" ? token.colorError : token.colorTextTertiary }}>
              {l.dir === "out" ? <ArrowUpOutlined /> : l.dir === "in" ? <ArrowDownOutlined /> : <InfoCircleOutlined />}
            </span>
            <span style={{ ...mono, fontSize: 12, flex: 1, whiteSpace: "pre-wrap", wordBreak: "break-all", color: l.dir === "info" ? token.colorTextSecondary : l.dir === "error" ? token.colorError : undefined }}>{l.data}</span>
            <span style={{ fontSize: 11, color: token.colorTextTertiary }}>{dayjs(l.at).format("HH:mm:ss.SSS")}</span>
          </div>
        ))}
        <div ref={logEnd} />
      </div>
    </div>
  );
}
