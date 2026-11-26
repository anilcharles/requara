import { InboxOutlined } from "@ant-design/icons";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Alert, App, Input, Modal, Segmented, Typography, Upload } from "antd";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { del, patch, personalActive, post, type Collection, type RequestDoc } from "../api";
import { exportCollection } from "../personal/backend";

/** Downloads a collection as a Postman Collection v2.1 file. */
/** Downloads a collection as a Postman v2.1 file, or as a zip of YAML files for Git. */
export async function downloadCollection(id: string, format: "postman" | "files" = "postman") {
  if (!personalActive()) {
    location.assign(`/api/collections/${id}/export${format === "files" ? "?format=files" : ""}`);
    return;
  }
  const f = await exportCollection(id, format);
  const slug = f.name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "untitled";
  const blob =
    format === "files"
      ? new Blob([Uint8Array.from(atob(f.text), (ch) => ch.charCodeAt(0))], { type: "application/zip" })
      : new Blob([f.text], { type: "application/json" });
  const a = document.createElement("a");
  a.href = URL.createObjectURL(blob);
  a.download = format === "files" ? `${slug}.zip` : `${slug}.postman_collection.json`;
  a.click();
  setTimeout(() => URL.revokeObjectURL(a.href), 1000);
}

/** The requests a collection run sends, in sidebar order (HTTP only). */
export const runOrder = (c: Collection) => c.folders.flatMap((f) => f.requests).filter((r) => r.method !== "WS");

export function useNewRequest() {
  const nav = useNavigate();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { collectionId?: string | null; apiId?: string | null; folder?: string; kind?: "http" | "websocket" }) =>
      post<RequestDoc>("/api/requests", {
        name: v.kind === "websocket" ? "WebSocket request" : "Untitled request",
        method: "GET",
        url: v.kind === "websocket" ? "wss://" : "",
        folder: v.folder ?? (v.collectionId || v.apiId ? "" : "Scratch"),
        collectionId: v.collectionId ?? null,
        apiId: v.apiId ?? null,
        kind: v.kind ?? "http",
      }),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["collections"] });
      nav(`/requests/${r.id}`);
    },
  });
}

export function NewCollectionModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { message } = App.useApp();
  const qc = useQueryClient();
  const nav = useNavigate();
  const [name, setName] = useState("");
  const create = useMutation({
    mutationFn: () => post<{ id: string }>("/api/collections", { name }),
    onSuccess: (c) => {
      qc.invalidateQueries({ queryKey: ["collections"] });
      setName("");
      onClose();
      nav(`/collections/${c.id}`);
    },
    onError: (e: Error) => message.error(e.message),
  });
  return (
    <Modal title="New collection" open={open} onCancel={onClose} okText="Create" okButtonProps={{ disabled: !name.trim() }} confirmLoading={create.isPending} onOk={() => create.mutate()} destroyOnHidden>
      <Typography.Paragraph type="secondary">Group requests, and give them shared variables, auth and scripts.</Typography.Paragraph>
      <Input autoFocus placeholder="Orders" value={name} onChange={(e) => setName(e.target.value)} onPressEnter={() => name.trim() && create.mutate()} />
    </Modal>
  );
}

export function RenameCollectionModal({ collection, onClose }: { collection: Collection | null; onClose: () => void }) {
  const { message } = App.useApp();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const rename = useMutation({
    mutationFn: () => patch(`/api/collections/${collection!.id}`, { name }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["collections"] });
      onClose();
    },
    onError: (e: Error) => message.error(e.message),
  });
  return (
    <Modal
      title="Rename collection"
      open={!!collection}
      onCancel={onClose}
      okText="Rename"
      afterOpenChange={(o) => o && setName(collection?.name ?? "")}
      okButtonProps={{ disabled: !name.trim() }}
      confirmLoading={rename.isPending}
      onOk={() => rename.mutate()}
    >
      <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} onPressEnter={() => name.trim() && rename.mutate()} />
    </Modal>
  );
}

/** Import a Postman collection (file or pasted JSON) or a cURL command. */
export function ImportModal({ open, onClose, collectionId }: { open: boolean; onClose: () => void; collectionId?: string | null }) {
  const { message } = App.useApp();
  const qc = useQueryClient();
  const nav = useNavigate();
  const [kind, setKind] = useState<"postman" | "curl">("postman");
  const [text, setText] = useState("");
  const [zip, setZip] = useState<{ name: string; data: string } | null>(null); // Export as files, base64
  const [err, setErr] = useState("");
  const run = useMutation({
    mutationFn: () =>
      post<{ id?: string; requestId?: string; name: string; requests?: number }>(
        "/api/collections/import",
        kind === "postman" && zip ? { format: "files", data: zip.data } : { format: kind, data: text, collectionId },
      ),
    onSuccess: (r) => {
      qc.invalidateQueries({ queryKey: ["collections"] });
      setText("");
      setZip(null);
      setErr("");
      onClose();
      if (r.requestId) nav(`/requests/${r.requestId}`);
      else {
        message.success(`Imported ${r.name} with ${r.requests} requests`);
        nav(`/collections/${r.id}`);
      }
    },
    onError: (e: Error) => setErr(e.message),
  });
  return (
    <Modal title="Import" open={open} onCancel={onClose} okText="Import" width={620} okButtonProps={{ disabled: !text.trim() && !zip }} confirmLoading={run.isPending} onOk={() => run.mutate()} destroyOnHidden>
      <Segmented
        value={kind}
        onChange={(v) => (setKind(v as "postman" | "curl"), setErr(""))}
        options={[
          { value: "postman", label: "Postman collection" },
          { value: "curl", label: "cURL command" },
        ]}
        style={{ marginBottom: 16 }}
      />
      {kind === "postman" ? (
        <>
          <Upload.Dragger
            accept=".json,application/json,.zip,application/zip"
            showUploadList={false}
            beforeUpload={(f) => {
              if (f.name.toLowerCase().endsWith(".zip")) {
                f.arrayBuffer().then((b) => {
                  let bin = "";
                  new Uint8Array(b).forEach((x) => (bin += String.fromCharCode(x)));
                  setZip({ name: f.name, data: btoa(bin) });
                  setText("");
                });
              } else {
                setZip(null);
                f.text().then(setText);
              }
              return false;
            }}
          >
            <p className="ant-upload-drag-icon">
              <InboxOutlined />
            </p>
            <p className="ant-upload-text">{zip ? zip.name : "Drop a Postman collection (v2.0 or v2.1) or a Requara files .zip here, or click to choose it"}</p>
            <p className="ant-upload-hint">In Postman: ··· on the collection → Export → Collection v2.1. A Git folder from Export as files: zip it first.</p>
          </Upload.Dragger>
          <Input.TextArea rows={5} style={{ marginTop: 12, fontFamily: "JetBrains Mono, monospace", fontSize: 12 }} placeholder="…or paste the collection JSON" value={text} onChange={(e) => setText(e.target.value)} />
        </>
      ) : (
        <Input.TextArea
          rows={8}
          autoFocus
          style={{ fontFamily: "JetBrains Mono, monospace", fontSize: 12 }}
          placeholder={"curl -X POST 'https://api.example.com/v1/orders' \\\n  -H 'Authorization: Bearer …' \\\n  -d '{\"sku\": \"A1\"}'"}
          value={text}
          onChange={(e) => setText(e.target.value)}
        />
      )}
      {err && <Alert type="error" showIcon message={err} style={{ marginTop: 12 }} />}
    </Modal>
  );
}

export function useDeleteCollection() {
  const { modal } = App.useApp();
  const qc = useQueryClient();
  const nav = useNavigate();
  return (c: Collection) =>
    modal.confirm({
      title: `Delete ${c.name}?`,
      content: "Its requests, examples and settings are deleted too.",
      okText: "Delete",
      okButtonProps: { danger: true },
      onOk: async () => {
        await del(`/api/collections/${c.id}`);
        qc.invalidateQueries({ queryKey: ["collections"] });
        nav("/requests");
      },
    });
}
