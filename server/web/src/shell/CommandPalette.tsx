import { SearchOutlined } from "../ui/icons";
import { useQuery } from "@tanstack/react-query";
import { Input, Modal, theme, Typography, type InputRef } from "../ui/kit";
import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { get } from "../api";
import { Kbd, Method } from "../components/bits";

type Item = { key: string; title: string; meta: string; method?: string; go: string; group: string };

const PAGES: Item[] = [
  { key: "p-apis", title: "APIs", meta: "Catalog", go: "/apis", group: "Go to" },
  { key: "p-req", title: "Requests", meta: "Workspace", go: "/requests", group: "Go to" },
  { key: "p-hist", title: "History", meta: "Sent requests", go: "/history", group: "Go to" },
  { key: "p-mock", title: "Mock servers", meta: "Live mocks", go: "/mocks", group: "Go to" },
  { key: "p-env", title: "Environments", meta: "Variables", go: "/environments", group: "Go to" },
  { key: "p-keys", title: "API keys", meta: "Settings", go: "/settings/keys", group: "Go to" },
  { key: "p-prefs", title: "Preferences", meta: "Settings", go: "/settings/preferences", group: "Go to" },
];

export default function CommandPalette({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { token } = theme.useToken();
  const nav = useNavigate();
  const [q, setQ] = useState("");
  const [active, setActive] = useState(0);
  const input = useRef<InputRef>(null);
  const search = useQuery({
    queryKey: ["search", q],
    queryFn: () => get<{ kind: string; id: string; title: string; meta: string; method: string }[]>(`/api/search?q=${encodeURIComponent(q)}`),
    enabled: open,
    staleTime: 10_000,
  });

  const items = useMemo<Item[]>(() => {
    const ql = q.toLowerCase();
    const pages = PAGES.filter((p) => !ql || p.title.toLowerCase().includes(ql));
    const hits = (search.data ?? []).map<Item>((h) =>
      h.kind === "api"
        ? { key: "a" + h.id, title: h.title, meta: h.meta, go: `/apis?open=${h.id}`, group: "APIs" }
        : { key: "r" + h.id, title: h.title, meta: h.meta, method: h.method, go: `/requests/${h.id}`, group: "Requests" },
    );
    return [...pages, ...hits];
  }, [q, search.data]);

  useEffect(() => setActive(0), [q, open]);
  useEffect(() => {
    if (!open) setQ("");
  }, [open]);

  const go = (it?: Item) => {
    if (!it) return;
    onClose();
    nav(it.go);
  };

  let lastGroup = "";
  return (
    <Modal open={open} onCancel={onClose} afterOpenChange={(o) => o && input.current?.focus()} footer={null} closable={false} width={640} styles={{ content: { padding: 0, overflow: "hidden" } }} style={{ top: 96 }} destroyOnHidden>
      <div style={{ display: "flex", alignItems: "center", gap: 12, padding: "0 16px", height: 52, borderBottom: `1px solid ${token.colorSplit}` }}>
        <SearchOutlined style={{ color: token.colorTextTertiary, fontSize: 16 }} />
        <Input
          ref={input}
          autoFocus
          variant="borderless"
          placeholder="Jump to a page, request or API…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          style={{ fontSize: 16, padding: 0 }}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown") (e.preventDefault(), setActive((a) => Math.min(a + 1, items.length - 1)));
            if (e.key === "ArrowUp") (e.preventDefault(), setActive((a) => Math.max(a - 1, 0)));
            if (e.key === "Enter") go(items[active]);
          }}
        />
        <Kbd>esc</Kbd>
      </div>
      <div style={{ maxHeight: 420, overflowY: "auto", padding: 8 }}>
        {items.length === 0 && <Typography.Text type="secondary" style={{ display: "block", padding: 16 }}>Nothing matches “{q}”.</Typography.Text>}
        {items.map((it, i) => {
          const header = it.group !== lastGroup;
          lastGroup = it.group;
          return (
            <div key={it.key}>
              {header && <div style={{ fontSize: 12, color: token.colorTextTertiary, fontWeight: 500, padding: "8px 12px 4px" }}>{it.group}</div>}
              <div
                onMouseEnter={() => setActive(i)}
                onClick={() => go(it)}
                style={{ display: "flex", alignItems: "center", gap: 12, height: 38, padding: "0 12px", borderRadius: 6, cursor: "pointer", background: i === active ? token.colorFillTertiary : undefined }}
              >
                {it.method && <Method method={it.method} width={36} />}
                <span style={{ flex: 1 }}>{it.title}</span>
                <span style={{ fontSize: 12, color: token.colorTextTertiary }}>{it.meta}</span>
              </div>
            </div>
          );
        })}
      </div>
      <div style={{ display: "flex", gap: 16, padding: "0 16px", height: 36, alignItems: "center", background: token.colorFillQuaternary, borderTop: `1px solid ${token.colorSplit}`, fontSize: 12, color: token.colorTextTertiary }}>
        <span>↑↓ Navigate</span>
        <span>↵ Open</span>
        <span>esc Close</span>
      </div>
    </Modal>
  );
}
