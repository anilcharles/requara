import { DeleteOutlined } from "@ant-design/icons";
import { Button, Checkbox, Input, theme } from "antd";
import type { KV } from "../api";
import { mono } from "./bits";

// Editable key/value rows with a trailing blank row that turns into a new entry.
export default function KeyValueTable({ rows, onChange, keyLabel = "Key", readOnly }: { rows: KV[]; onChange: (rows: KV[]) => void; keyLabel?: string; readOnly?: boolean }) {
  const { token } = theme.useToken();
  const all = readOnly ? rows : [...rows, { key: "", value: "", description: "", enabled: true }];
  const set = (i: number, p: Partial<KV>) => {
    const next = [...rows];
    if (i === rows.length) next.push({ key: "", value: "", description: "", enabled: true, ...p });
    else next[i] = { ...next[i], ...p };
    onChange(next);
  };
  const cell = { borderLeft: `1px solid ${token.colorSplit}`, padding: "0 4px", display: "flex", alignItems: "center" } as const;
  const varStyle = (v: string) => (v.includes("{{") ? { color: "#531DAB" } : {});
  return (
    <div style={{ border: `1px solid ${token.colorSplit}`, borderRadius: 8, overflow: "hidden" }}>
      <div style={{ display: "grid", gridTemplateColumns: "44px 1fr 1fr 1fr 40px", height: 38, background: token.colorFillQuaternary, fontSize: 13, fontWeight: 600, color: token.colorTextSecondary }}>
        <span />
        {[keyLabel, "Value", "Description"].map((h) => (
          <span key={h} style={{ ...cell, padding: "0 12px" }}>
            {h}
          </span>
        ))}
        <span />
      </div>
      {all.map((r, i) => {
        const blank = i === rows.length;
        return (
          <div key={i} style={{ display: "grid", gridTemplateColumns: "44px 1fr 1fr 1fr 40px", height: 40, borderTop: `1px solid ${token.colorSplit}` }}>
            <span style={{ display: "grid", placeItems: "center" }}>{!blank && <Checkbox checked={r.enabled} disabled={readOnly} onChange={(e) => set(i, { enabled: e.target.checked })} />}</span>
            <span style={cell}>
              <Input variant="borderless" placeholder={keyLabel} value={r.key} readOnly={readOnly} onChange={(e) => set(i, { key: e.target.value })} style={{ ...mono, fontSize: 13, opacity: r.enabled ? 1 : 0.45 }} />
            </span>
            <span style={cell}>
              <Input variant="borderless" placeholder="Value" value={r.value} readOnly={readOnly} onChange={(e) => set(i, { value: e.target.value })} style={{ ...mono, fontSize: 13, opacity: r.enabled ? 1 : 0.45, ...varStyle(r.value) }} />
            </span>
            <span style={cell}>
              <Input variant="borderless" placeholder="Description" value={r.description ?? ""} readOnly={readOnly} onChange={(e) => set(i, { description: e.target.value })} style={{ fontSize: 13, color: token.colorTextSecondary }} />
            </span>
            <span style={{ display: "grid", placeItems: "center" }}>
              {!blank && !readOnly && <Button type="text" size="small" icon={<DeleteOutlined />} aria-label="Remove row" onClick={() => onChange(rows.filter((_, j) => j !== i))} />}
            </span>
          </div>
        );
      })}
    </div>
  );
}
