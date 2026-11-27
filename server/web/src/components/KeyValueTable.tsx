import { X } from "lucide-react";
import type { KV } from "../api";
import { IconButton, TextInput } from "../ui";

// Editable key/value rows with a trailing blank row that turns into a new entry.
export default function KeyValueTable({ rows, onChange, keyLabel = "Key", readOnly }: { rows: KV[]; onChange: (rows: KV[]) => void; keyLabel?: string; readOnly?: boolean }) {
  const all = readOnly ? rows : [...rows, { key: "", value: "", description: "", enabled: true }];
  const set = (i: number, p: Partial<KV>) => {
    const next = [...rows];
    if (i === rows.length) next.push({ key: "", value: "", description: "", enabled: true, ...p });
    else next[i] = { ...next[i], ...p };
    onChange(next);
  };
  return (
    <table className="rq-kv">
      <thead>
        <tr>
          <th aria-label="On" />
          <th>{keyLabel}</th>
          <th>Value</th>
          <th>Description</th>
          <th aria-label="Remove" />
        </tr>
      </thead>
      <tbody>
        {all.map((r, i) => {
          const blank = i === rows.length;
          const off = !r.enabled && !blank;
          return (
            <tr key={i}>
              <td>{!blank && <input type="checkbox" className="rq-check" checked={r.enabled} disabled={readOnly} aria-label={`Send ${r.key || "this row"}`} onChange={(e) => set(i, { enabled: e.target.checked })} />}</td>
              <td className={off ? "rq-off" : undefined}>
                <TextInput bare mono placeholder={keyLabel.toLowerCase()} value={r.key} readOnly={readOnly} aria-label={keyLabel} onChange={(e) => set(i, { key: e.target.value })} />
              </td>
              <td className={off ? "rq-off" : undefined}>
                <TextInput
                  bare
                  mono
                  placeholder="value"
                  value={r.value}
                  readOnly={readOnly}
                  aria-label="Value"
                  onChange={(e) => set(i, { value: e.target.value })}
                  style={r.value.includes("{{") ? { color: "var(--rq-var)" } : undefined}
                />
              </td>
              <td>
                <TextInput bare placeholder={blank ? "" : "description"} value={r.description ?? ""} readOnly={readOnly} aria-label="Description" onChange={(e) => set(i, { description: e.target.value })} style={{ color: "var(--rq-dim)" }} />
              </td>
              <td>{!blank && !readOnly && <IconButton className="rq-kv-remove" size="sm" label="Remove row" icon={<X />} onClick={() => onChange(rows.filter((_, j) => j !== i))} />}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
