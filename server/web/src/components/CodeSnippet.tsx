import { CopyOutlined } from "../ui/icons";
import { App, Button, Modal, Segmented, theme } from "../ui/kit";
import { useMemo, useState } from "react";
import type { RequestDoc } from "../api";
import CodeEditor from "./CodeEditor";

// Code for a request in other languages, like Postman's "Code" button.
// {{variables}} are left as they are.

type Lang = "curl" | "fetch" | "python" | "go" | "httpie";

function parts(r: RequestDoc) {
  const qs = r.params.filter((p) => p.enabled && p.key).map((p) => `${encodeURIComponent(p.key)}=${encodeURIComponent(p.value)}`);
  let url = r.url + (qs.length ? (r.url.includes("?") ? "&" : "?") + qs.join("&") : "");
  const headers: [string, string][] = r.headers.filter((h) => h.enabled && h.key).map((h) => [h.key, h.value]);
  const a = r.auth;
  if (a.type === "bearer") headers.push(["Authorization", `Bearer ${a.token ?? ""}`]);
  if (a.type === "basic") headers.push(["Authorization", `Basic <base64 of ${a.username ?? ""}:${a.password ? "…" : ""}>`]);
  if (a.type === "apikey" && a.key) {
    if (a.in === "query") url += (url.includes("?") ? "&" : "?") + `${encodeURIComponent(a.key)}=${encodeURIComponent(a.value ?? "")}`;
    else headers.push([a.key, a.value ?? ""]);
  }
  if (a.type === "oauth2") headers.push(["Authorization", "Bearer {{access_token}}"]);
  if (a.type === "jwt") headers.push(["Authorization", `${a.headerPrefix || "Bearer"} {{jwt}}`]);
  const hasCT = headers.some(([k]) => k.toLowerCase() === "content-type");
  let body = "";
  if (["json", "raw", "graphql", "form-urlencoded"].includes(r.bodyMode)) {
    body = r.body;
    if (!hasCT && r.bodyMode === "json") headers.push(["Content-Type", "application/json"]);
    if (!hasCT && r.bodyMode === "form-urlencoded") headers.push(["Content-Type", "application/x-www-form-urlencoded"]);
    if (!hasCT && r.bodyMode === "graphql") headers.push(["Content-Type", "application/json"]);
  }
  const form = r.bodyMode === "form-data" ? r.formData.filter((f) => f.enabled && f.key) : [];
  return { url, headers, body, form };
}

const sq = (s: string) => `'${s.replace(/'/g, `'\\''`)}'`;
const dq = (s: string) => JSON.stringify(s);

export function snippet(r: RequestDoc, lang: Lang): string {
  const { url, headers, body, form } = parts(r);
  const m = r.method.toUpperCase();
  switch (lang) {
    case "curl": {
      const lines = [`curl -X ${m} ${sq(url)}`];
      headers.forEach(([k, v]) => lines.push(`  -H ${sq(`${k}: ${v}`)}`));
      form.forEach((f) => lines.push(`  -F ${sq(f.type === "file" ? `${f.key}=@${f.fileName || "file"}` : `${f.key}=${f.value}`)}`));
      if (r.bodyMode === "binary") lines.push(`  --data-binary @${sq(r.bodyFile?.name || "file")}`);
      if (body) lines.push(`  --data-raw ${sq(body)}`);
      return lines.join(" \\\n");
    }
    case "httpie": {
      const lines = [`http ${m} ${sq(url)}`];
      headers.forEach(([k, v]) => lines.push(`  ${sq(`${k}:${v}`)}`));
      form.forEach((f) => lines.push(`  ${sq(f.type === "file" ? `${f.key}@${f.fileName || "file"}` : `${f.key}=${f.value}`)}`));
      const cmd = lines.join(" \\\n");
      return body ? `echo ${sq(body)} | ${cmd}` : form.length ? cmd.replace(/^http /, "http --form ") : cmd;
    }
    case "fetch": {
      const opts: string[] = [`  method: ${dq(m)},`];
      if (headers.length) opts.push(`  headers: {\n${headers.map(([k, v]) => `    ${dq(k)}: ${dq(v)},`).join("\n")}\n  },`);
      let pre = "";
      if (form.length) {
        pre = `const form = new FormData();\n${form.map((f) => (f.type === "file" ? `form.append(${dq(f.key)}, fileInput.files[0]); // ${f.fileName || "file"}` : `form.append(${dq(f.key)}, ${dq(f.value)});`)).join("\n")}\n\n`;
        opts.push("  body: form,");
      } else if (body) opts.push(`  body: ${r.bodyMode === "json" ? `JSON.stringify(${body.trim()})` : dq(body)},`);
      return `${pre}const res = await fetch(${dq(url)}, {\n${opts.join("\n")}\n});\nconsole.log(res.status, await res.text());`;
    }
    case "python": {
      const lines = ["import requests", ""];
      lines.push(`url = ${dq(url)}`);
      if (headers.length) lines.push(`headers = {\n${headers.map(([k, v]) => `    ${dq(k)}: ${dq(v)},`).join("\n")}\n}`);
      const args = [`"${m}"`, "url"];
      if (headers.length) args.push("headers=headers");
      if (form.length) {
        const data = form.filter((f) => f.type !== "file");
        const files = form.filter((f) => f.type === "file");
        if (data.length) (lines.push(`data = {${data.map((f) => `${dq(f.key)}: ${dq(f.value)}`).join(", ")}}`), args.push("data=data"));
        if (files.length) (lines.push(`files = {${files.map((f) => `${dq(f.key)}: open(${dq(f.fileName || "file")}, "rb")`).join(", ")}}`), args.push("files=files"));
      } else if (body) {
        lines.push(`payload = ${dq(body)}`);
        args.push("data=payload");
      }
      lines.push("", `response = requests.request(${args.join(", ")})`, "print(response.status_code, response.text)");
      return lines.join("\n");
    }
    case "go": {
      const lines = ["package main", "", "import (", '\t"fmt"', '\t"io"', '\t"net/http"'];
      if (body) lines.push('\t"strings"');
      lines.push(")", "", "func main() {");
      lines.push(body ? `\tbody := strings.NewReader(${dq(body)})` : "\tvar body io.Reader");
      lines.push(`\treq, err := http.NewRequest(${dq(m)}, ${dq(url)}, body)`, "\tif err != nil {", "\t\tpanic(err)", "\t}");
      headers.forEach(([k, v]) => lines.push(`\treq.Header.Set(${dq(k)}, ${dq(v)})`));
      lines.push("\tres, err := http.DefaultClient.Do(req)", "\tif err != nil {", "\t\tpanic(err)", "\t}", "\tdefer res.Body.Close()", "\tout, _ := io.ReadAll(res.Body)", "\tfmt.Println(res.Status, string(out))", "}");
      return lines.join("\n");
    }
  }
}

const LANGS: { value: Lang; label: string; editor: string }[] = [
  { value: "curl", label: "cURL", editor: "shell" },
  { value: "fetch", label: "JavaScript", editor: "javascript" },
  { value: "python", label: "Python", editor: "python" },
  { value: "go", label: "Go", editor: "go" },
  { value: "httpie", label: "HTTPie", editor: "shell" },
];

export default function CodeSnippetModal({ request, open, onClose }: { request: RequestDoc | null; open: boolean; onClose: () => void }) {
  const { token } = theme.useToken();
  const { message } = App.useApp();
  const [lang, setLang] = useState<Lang>(() => (localStorage.getItem("requara.codeLang") as Lang) || "curl");
  const code = useMemo(() => (request ? snippet(request, lang) : ""), [request, lang]);
  return (
    <Modal
      title="Code snippet"
      open={open}
      onCancel={onClose}
      width={760}
      footer={
        <Button
          type="primary"
          icon={<CopyOutlined />}
          onClick={() => {
            navigator.clipboard.writeText(code);
            message.success("Copied");
          }}
        >
          Copy
        </Button>
      }
    >
      <Segmented
        value={lang}
        onChange={(v) => {
          setLang(v as Lang);
          localStorage.setItem("requara.codeLang", v as string);
        }}
        options={LANGS.map((l) => ({ value: l.value, label: l.label }))}
        style={{ marginBottom: 12 }}
      />
      <div style={{ height: 360, border: `1px solid ${token.colorBorder}`, borderRadius: 8, overflow: "hidden" }}>
        <CodeEditor value={code} language={LANGS.find((l) => l.value === lang)!.editor} readOnly />
      </div>
    </Modal>
  );
}
