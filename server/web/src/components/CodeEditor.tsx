import Editor, { loader, type OnMount } from "@monaco-editor/react";
import * as monaco from "monaco-editor";
import EditorWorker from "monaco-editor/editor/editor.worker?worker";
import JsonWorker from "monaco-editor/language/json/json.worker?worker";
import TsWorker from "monaco-editor/language/typescript/ts.worker?worker";
import { theme } from "antd";
import { useEffect, useRef, useState } from "react";
import { usePrefs } from "../shell/session";

// Bundle Monaco locally instead of loading it from a CDN.
self.MonacoEnvironment = {
  getWorker(_id: string, label: string) {
    if (label === "json") return new JsonWorker();
    if (label === "typescript" || label === "javascript") return new TsWorker();
    return new EditorWorker();
  },
};
loader.config({ monaco });

let themesDefined = false;
function defineThemes() {
  if (themesDefined) return;
  themesDefined = true;
  // Syntax colours from Ant Design's palette, matching the Figma design.
  monaco.editor.defineTheme("requara-light", {
    base: "vs",
    inherit: true,
    rules: [
      { token: "string.key.json", foreground: "0958D9" },
      { token: "string.value.json", foreground: "389E0D" },
      { token: "number", foreground: "D46B08" },
      { token: "keyword.json", foreground: "C41D7F" },
      { token: "string", foreground: "389E0D" },
      { token: "keyword", foreground: "C41D7F" },
      { token: "delimiter", foreground: "8C8C8C" },
    ],
    colors: { "editor.background": "#FFFFFF", "editorLineNumber.foreground": "#BFBFBF", "editorGutter.background": "#FAFAFA" },
  });
  monaco.editor.defineTheme("requara-dark", {
    base: "vs-dark",
    inherit: true,
    rules: [
      { token: "string.key.json", foreground: "69B1FF" },
      { token: "string.value.json", foreground: "95DE64" },
      { token: "number", foreground: "FFC069" },
      { token: "keyword.json", foreground: "FF85C0" },
      { token: "string", foreground: "95DE64" },
      { token: "keyword", foreground: "FF85C0" },
    ],
    colors: { "editor.background": "#141414", "editorGutter.background": "#1A1A1A" },
  });
}

export type CodeEditorProps = {
  value: string;
  language: string;
  onChange?: (v: string) => void;
  readOnly?: boolean;
  height?: number | string;
  path?: string; // model URI, used to attach a JSON schema
  schema?: object | null;
  onSubmit?: () => void; // ⌘↵
  onSave?: () => void; // ⌘S
  background?: string;
  maxAutoHeight?: number; // grow with the content up to this many px
};

export default function CodeEditor({ value, language, onChange, readOnly, height = "100%", path, schema, onSubmit, onSave, background, maxAutoHeight }: CodeEditorProps) {
  const { isDark, prefs } = usePrefs();
  const { token } = theme.useToken();
  const submitRef = useRef(onSubmit);
  const saveRef = useRef(onSave);
  submitRef.current = onSubmit;
  saveRef.current = onSave;
  const [autoHeight, setAutoHeight] = useState(() => Math.min(value.split("\n").length * 22 + 24, maxAutoHeight ?? 0));

  useEffect(() => {
    if (!path || language !== "json") return;
    const uri = monaco.Uri.parse(path).toString();
    const others = (monaco.json.jsonDefaults.diagnosticsOptions.schemas ?? []).filter((s) => !s.fileMatch?.includes(uri));
    monaco.json.jsonDefaults.setDiagnosticsOptions({
      validate: true,
      allowComments: false,
      schemas: schema ? [...others, { uri: `requara://schema/${encodeURIComponent(path)}`, fileMatch: [uri], schema }] : others,
    });
  }, [path, schema, language]);

  const onMount: OnMount = (editor) => {
    if (maxAutoHeight) {
      const fit = () => setAutoHeight(Math.min(editor.getContentHeight(), maxAutoHeight));
      editor.onDidContentSizeChange(fit);
      fit();
    }
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter, () => submitRef.current?.());
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => saveRef.current?.());
  };

  return (
    <div style={{ height: maxAutoHeight ? autoHeight : height, background: background ?? token.colorBgContainer }}>
      <Editor
        beforeMount={defineThemes}
        onMount={onMount}
        theme={isDark ? "requara-dark" : "requara-light"}
        language={language}
        path={path}
        value={value}
        onChange={(v) => onChange?.(v ?? "")}
        options={{
          readOnly,
          minimap: { enabled: false },
          fontFamily: prefs.codeFont ?? "JetBrains Mono, Menlo, monospace",
          fontSize: prefs.codeFontSize ?? 13,
          lineHeight: 22,
          scrollBeyondLastLine: false,
          renderLineHighlight: readOnly ? "none" : "line",
          tabSize: 2,
          padding: { top: 12, bottom: 12 },
          automaticLayout: true,
          wordWrap: "on",
          overviewRulerLanes: 0,
          scrollbar: { verticalScrollbarSize: 8, horizontalScrollbarSize: 8 },
          fixedOverflowWidgets: true,
        }}
      />
    </div>
  );
}
