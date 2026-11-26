import { DeleteOutlined, PaperClipOutlined } from "@ant-design/icons";
import { App, Button, Checkbox, Input, Select, theme, Upload } from "antd";
import type { FileData, FormField } from "../api";
import { bytes } from "../lib";
import { mono } from "./bits";

const MAX = 10 << 20;

/** Reads a file as base64 (without the data: prefix). */
export function readBase64(f: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result).replace(/^data:[^,]*,/, ""));
    r.onerror = () => reject(r.error);
    r.readAsDataURL(f);
  });
}
const b64Size = (s?: string) => (s ? Math.floor((s.length * 3) / 4) : 0);

/** multipart/form-data fields: text, or files kept with the request. */
export function FormDataEditor({ rows, onChange, readOnly }: { rows: FormField[]; onChange: (rows: FormField[]) => void; readOnly?: boolean }) {
  const { token } = theme.useToken();
  const { message } = App.useApp();
  const all = readOnly ? rows : [...rows, { key: "", value: "", type: "text" as const, enabled: true }];
  const set = (i: number, p: Partial<FormField>) => {
    const next = [...rows];
    if (i === rows.length) next.push({ key: "", value: "", type: "text", enabled: true, ...p });
    else next[i] = { ...next[i], ...p };
    onChange(next);
  };
  const pick = async (i: number, f: File) => {
    if (f.size > MAX) return message.error("Files can be up to 10 MB");
    set(i, { type: "file", fileName: f.name, contentType: f.type || "application/octet-stream", value: await readBase64(f) });
  };
  return (
    <div style={{ border: `1px solid ${token.colorSplit}`, borderRadius: 8, overflow: "hidden" }}>
      {all.map((r, i) => (
        <div key={i} style={{ display: "grid", gridTemplateColumns: "40px 1fr 90px 1.4fr 40px", alignItems: "center", borderTop: i ? `1px solid ${token.colorSplit}` : undefined, minHeight: 38 }}>
          <div style={{ textAlign: "center" }}>{i < rows.length && <Checkbox checked={r.enabled} disabled={readOnly} onChange={(e) => set(i, { enabled: e.target.checked })} />}</div>
          <Input variant="borderless" placeholder="Key" value={r.key} disabled={readOnly} onChange={(e) => set(i, { key: e.target.value })} style={mono} />
          <Select size="small" variant="borderless" value={r.type ?? "text"} disabled={readOnly} onChange={(type) => set(i, { type, value: "", fileName: undefined })} options={[{ value: "text", label: "Text" }, { value: "file", label: "File" }]} />
          {r.type === "file" ? (
            <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "0 8px" }}>
              <Upload showUploadList={false} disabled={readOnly} beforeUpload={(f) => (pick(i, f), false)}>
                <Button size="small" icon={<PaperClipOutlined />}>
                  {r.fileName ? "Change" : "Choose file"}
                </Button>
              </Upload>
              {r.fileName && (
                <span style={{ fontSize: 12, color: r.value ? token.colorText : token.colorWarning }}>
                  {r.fileName} {r.value ? `· ${bytes(b64Size(r.value))}` : "· pick it again (not kept on import)"}
                </span>
              )}
            </div>
          ) : (
            <Input variant="borderless" placeholder="Value" value={r.value} disabled={readOnly} onChange={(e) => set(i, { value: e.target.value })} style={mono} />
          )}
          <div style={{ textAlign: "center" }}>
            {i < rows.length && !readOnly && <Button size="small" type="text" icon={<DeleteOutlined />} aria-label="Remove field" onClick={() => onChange(rows.filter((_, j) => j !== i))} />}
          </div>
        </div>
      ))}
    </div>
  );
}

/** A file sent as the whole request body. */
export function BinaryEditor({ file, onChange, readOnly }: { file: FileData | null; onChange: (f: FileData | null) => void; readOnly?: boolean }) {
  const { message } = App.useApp();
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 12 }}>
      <Upload
        showUploadList={false}
        disabled={readOnly}
        beforeUpload={(f) => {
          if (f.size > MAX) message.error("Files can be up to 10 MB");
          else readBase64(f).then((data) => onChange({ name: f.name, contentType: f.type || "application/octet-stream", data }));
          return false;
        }}
      >
        <Button icon={<PaperClipOutlined />}>{file?.data ? "Change file" : "Choose a file"}</Button>
      </Upload>
      {file?.data && (
        <>
          <span>
            {file.name} · {bytes(b64Size(file.data))} · <span style={mono}>{file.contentType}</span>
          </span>
          {!readOnly && (
            <Button type="link" onClick={() => onChange(null)}>
              Remove
            </Button>
          )}
        </>
      )}
    </div>
  );
}
