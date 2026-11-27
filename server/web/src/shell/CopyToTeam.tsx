import { useQuery } from "@tanstack/react-query";
import { Alert, App, Checkbox, Modal, Progress, Select, Space, Typography } from "../ui/kit";
import { useEffect, useState } from "react";
import { isSignedIn, serverApi } from "../api";
import { snapshot } from "../personal/backend";

type Snapshot = Awaited<ReturnType<typeof snapshot>>;

/**
 * Copies things from My workspace (this browser) into a team workspace on the
 * organization's server: APIs (with their specs; the server makes their saved
 * requests), scratch requests and environments. Secret values stay behind.
 */
export default function CopyToTeamModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { message } = App.useApp();
  const teams = useQuery({
    queryKey: ["team-workspaces"],
    queryFn: () => serverApi<{ id: string; name: string; role: string }[]>("/api/workspaces"),
    enabled: open && isSignedIn(),
  });
  const [snap, setSnap] = useState<Snapshot | null>(null);
  const [target, setTarget] = useState<string>();
  const [pick, setPick] = useState<Set<string>>(new Set());
  const [progress, setProgress] = useState<number | null>(null);
  const [errors, setErrors] = useState<string[]>([]);

  useEffect(() => {
    if (!open) return;
    setErrors([]);
    setProgress(null);
    snapshot().then((s) => {
      setSnap(s);
      setPick(new Set([...s.apis.map((a) => "api:" + a.id), ...(s.scratch.length ? ["scratch"] : []), ...s.environments.map((e) => "env:" + e.name)]));
    });
  }, [open]);
  const writable = (teams.data ?? []).filter((w) => w.role !== "viewer");
  useEffect(() => {
    if (!target && writable[0]) setTarget(writable[0].id);
  }, [writable, target]);

  const copy = async () => {
    if (!snap || !target) return;
    const ws = { "X-Workspace": target };
    const steps: [string, () => Promise<unknown>][] = [];
    for (const a of snap.apis.filter((a) => pick.has("api:" + a.id))) {
      steps.push([
        `API ${a.name}`,
        () => {
          const fd = new FormData();
          fd.append("file", new Blob([JSON.stringify(a.spec)], { type: "application/json" }), `${a.name}.json`);
          return serverApi("/api/apis/import", { method: "POST", body: fd, headers: ws });
        },
      ]);
    }
    if (pick.has("scratch")) {
      for (const r of snap.scratch) {
        steps.push([`Request ${r.name}`, () => serverApi("/api/requests", { method: "POST", headers: ws, json: { ...r, apiId: null, folder: r.folder || "Scratch" } })]);
      }
    }
    for (const e of snap.environments.filter((e) => pick.has("env:" + e.name))) {
      steps.push([
        `Environment ${e.name}`,
        async () => {
          const { id } = await serverApi<{ id: string }>("/api/environments", { method: "POST", headers: ws, json: { name: e.name, color: e.color } });
          await serverApi(`/api/environments/${id}/variables`, {
            method: "PUT",
            headers: ws,
            json: e.variables.map((v) => ({ key: v.key, value: v.kind === "secret" ? "" : v.value, kind: v.kind, enabled: v.enabled })),
          });
        },
      ]);
    }
    const failed: string[] = [];
    for (let i = 0; i < steps.length; i++) {
      setProgress(Math.round((i / steps.length) * 100));
      try {
        await steps[i][1]();
      } catch (err) {
        failed.push(`${steps[i][0]}: ${(err as Error).message}`);
      }
    }
    setProgress(100);
    setErrors(failed);
    if (!failed.length) {
      message.success(`Copied ${steps.length} item${steps.length === 1 ? "" : "s"} to ${writable.find((w) => w.id === target)?.name}`);
      onClose();
    }
  };

  const toggle = (k: string, on: boolean) => {
    const next = new Set(pick);
    if (on) next.add(k);
    else next.delete(k);
    setPick(next);
  };

  return (
    <Modal
      title="Copy to a team workspace"
      open={open}
      onCancel={onClose}
      okText="Copy"
      okButtonProps={{ disabled: !target || pick.size === 0 || progress !== null && progress < 100 }}
      confirmLoading={progress !== null && progress < 100}
      onOk={copy}
      width={520}
    >
      <Typography.Paragraph type="secondary">
        Your personal workspace stays as it is. Team members see the copies. Secret values aren't copied; enter them again in the team workspace.
      </Typography.Paragraph>
      {!teams.isLoading && writable.length === 0 && <Alert type="info" showIcon message="You can't edit any team workspace. Ask an owner to invite you as an editor, or create a team workspace." />}
      {writable.length > 0 && (
        <Space direction="vertical" style={{ width: "100%" }} size={16}>
          <Select value={target} onChange={setTarget} style={{ width: "100%" }} options={writable.map((w) => ({ value: w.id, label: w.name }))} />
          <div style={{ maxHeight: 280, overflow: "auto" }}>
            {snap?.apis.map((a) => (
              <div key={a.id}>
                <Checkbox checked={pick.has("api:" + a.id)} onChange={(e) => toggle("api:" + a.id, e.target.checked)}>
                  API: {a.name}
                </Checkbox>
              </div>
            ))}
            {!!snap?.scratch.length && (
              <div>
                <Checkbox checked={pick.has("scratch")} onChange={(e) => toggle("scratch", e.target.checked)}>
                  Scratch requests ({snap.scratch.length})
                </Checkbox>
              </div>
            )}
            {snap?.environments.map((e) => (
              <div key={e.name}>
                <Checkbox checked={pick.has("env:" + e.name)} onChange={(ev) => toggle("env:" + e.name, ev.target.checked)}>
                  Environment: {e.name} ({e.variables.length} variables)
                </Checkbox>
              </div>
            ))}
            {snap && !snap.apis.length && !snap.scratch.length && !snap.environments.length && <Typography.Text type="secondary">Your personal workspace is empty.</Typography.Text>}
          </div>
          {progress !== null && <Progress percent={progress} size="small" />}
          {errors.length > 0 && <Alert type="warning" showIcon message="Some items weren't copied" description={errors.map((e) => <div key={e}>{e}</div>)} />}
        </Space>
      )}
    </Modal>
  );
}
