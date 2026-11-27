import { Check, ChevronDown, Monitor, Moon, Plus, Search, Sun } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Input, Modal, Typography } from "antd";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { get, isSignedIn, post, selectWorkspace, serverApi } from "../api";
import { SignInModal } from "../components/SignInOptions";
import { PERSONAL_ID } from "../personal/backend";
import CopyToTeamModal from "./CopyToTeam";
import { Button, Kbd, Menu, type MenuItem } from "../ui";
import CommandPalette from "./CommandPalette";
import { useEnvironments, useMe, usePrefs } from "./session";
import ShareModal from "./ShareModal";

export function Logo({ size = 22 }: { size?: number }) {
  return (
    // The Requara mark, "Orbit" (branding/requara-icon.svg): a ring with a speck breaking out of it.
    <svg width={size} height={size} viewBox="0 0 64 64" aria-hidden>
      <rect width="64" height="64" rx="14.4" fill="#14161F" />
      <g transform="translate(32 32) scale(0.68) translate(-32 -32)">
        <circle cx="32" cy="32" r="18" fill="none" stroke="#fff" strokeWidth="5.5" />
        <circle cx="44.7" cy="19.3" r="9" fill="#14161F" />
        <circle cx="44.7" cy="19.3" r="5.5" fill="#3D8BFF" />
      </g>
    </svg>
  );
}

export const initials = (name: string) =>
  name
    .split(/\s+/)
    .map((p) => p[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();

/** Make a workspace (you become its owner) and switch to it. */
export function CreateWorkspaceModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { message } = App.useApp();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const create = useMutation({
    mutationFn: () => serverApi<{ id: string }>("/api/workspaces", { method: "POST", json: { name } }),
    onSuccess: async (ws) => {
      selectWorkspace(ws.id);
      await serverApi("/api/me/preferences", { method: "PATCH", json: { workspaceId: ws.id } });
      setName("");
      onClose();
      qc.invalidateQueries();
    },
    onError: (e: Error) => message.error(e.message),
  });
  return (
    <Modal title="New workspace" open={open} onCancel={onClose} okText="Create" okButtonProps={{ disabled: !name.trim() }} confirmLoading={create.isPending} onOk={() => create.mutate()}>
      <Typography.Paragraph type="secondary">You'll be its owner and can invite people.</Typography.Paragraph>
      <Input autoFocus placeholder="Platform team" value={name} onChange={(e) => setName(e.target.value)} onPressEnter={() => name.trim() && create.mutate()} />
    </Modal>
  );
}

export default function Header() {
  const nav = useNavigate();
  const qc = useQueryClient();
  const me = useMe();
  const { prefs, isDark, setPrefs } = usePrefs();
  const envs = useEnvironments();
  const mode = prefs.theme ?? "dark";
  const workspaces = useQuery({ queryKey: ["workspaces"], queryFn: () => get<{ id: string; name: string; role: string }[]>("/api/workspaces") });
  const [palette, setPalette] = useState(false);
  const [share, setShare] = useState(false);
  const [creating, setCreating] = useState(false);
  const [signingIn, setSigningIn] = useState(false);
  const [copying, setCopying] = useState(false);
  const signedIn = isSignedIn();
  const personal = me.data?.workspaceId === PERSONAL_ID;
  const instance = useQuery({ queryKey: ["instance"], queryFn: () => get<{ allowWorkspaceCreation: boolean }>("/api/instance"), staleTime: 60_000 });

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPalette(true);
      }
      if ((e.metaKey || e.ctrlKey) && e.key === ",") {
        e.preventDefault();
        nav("/settings/preferences");
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [nav]);

  const current = workspaces.data?.find((w) => w.id === me.data?.workspaceId);
  const envList = (envs.data?.items ?? []).filter((e) => !e.isGlobal);
  const activeEnv = envList.find((e) => e.id === envs.data?.activeId);

  const signOut = async () => {
    const { logoutUrl } = await post<{ logoutUrl: string }>("/auth/logout");
    selectWorkspace(PERSONAL_ID);
    qc.clear();
    location.assign(logoutUrl);
  };
  const switchWorkspace = (key: string) => {
    if (key === "__new") return setCreating(true);
    if (key === "__signin") return setSigningIn(true);
    if (key === me.data?.workspaceId) return;
    selectWorkspace(key);
    // A team workspace is also remembered on the server, for the desktop app.
    const saved = key === PERSONAL_ID ? Promise.resolve() : serverApi("/api/me/preferences", { method: "PATCH", json: { workspaceId: key } });
    saved.finally(() => {
      qc.removeQueries();
      nav("/apis");
    });
  };
  const ws = workspaces.data ?? [];
  const check = (on: boolean) => <Check style={{ visibility: on ? "visible" : "hidden" }} />;

  return (
    <header style={{ height: 44, flexShrink: 0, display: "flex", alignItems: "center", gap: 12, padding: "0 12px 0 14px", background: "var(--rq-frame)" }}>
      {/* Brand and workspace fill the sidebar's column; search starts where the canvas starts. */}
      <div style={{ width: 234, flexShrink: 0, display: "flex", alignItems: "center", gap: 6, minWidth: 0 }}>
        <a onClick={() => nav("/apis")} style={{ display: "flex", alignItems: "center", gap: 9, cursor: "pointer", flexShrink: 0, color: "var(--rq-text)", fontWeight: 600, fontSize: 14, letterSpacing: -0.1 }}>
          <Logo size={20} />
          Requara
        </a>
        <span style={{ flex: 1 }} />
        <Menu
          align="end"
          trigger={
            <Button variant="ghost" style={{ minWidth: 0, maxWidth: 140 }}>
              <span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>{current?.name ?? "Workspace"}</span>
              <ChevronDown style={{ width: 12, height: 12 }} />
            </Button>
          }
          items={[
            { type: "label", key: "l1", label: "Personal" },
            ...ws.filter((w) => w.id === PERSONAL_ID).map((w) => ({ key: w.id, label: w.name, icon: check(w.id === me.data?.workspaceId), end: "only you" })),
            { type: "label", key: "l2", label: "Team workspaces" },
            ...(signedIn
              ? ws.filter((w) => w.id !== PERSONAL_ID).map((w) => ({ key: w.id, label: w.name, icon: check(w.id === me.data?.workspaceId), end: w.role }))
              : [{ key: "__signin", label: "Sign in to your organization…", icon: check(false) }]),
            ...(signedIn && instance.data?.allowWorkspaceCreation ? ([{ type: "separator", key: "s" }, { key: "__new", label: "New team workspace…", icon: <Plus /> }] as MenuItem[]) : []),
          ]}
          onSelect={switchWorkspace}
        />
      </div>
      <button
        type="button"
        onClick={() => setPalette(true)}
        className="rq-input"
        style={{ width: 360, display: "flex", alignItems: "center", gap: 8, cursor: "pointer", color: "var(--rq-mute)", textAlign: "left" }}
      >
        <Search style={{ width: 14, height: 14 }} />
        <span style={{ flex: 1 }}>Search requests and APIs</span>
        <Kbd>⌘K</Kbd>
      </button>
      <div style={{ flex: 1 }} />
      {envList.length > 0 && (
        <Menu
          align="end"
          trigger={
            <Button style={{ borderColor: "var(--rq-line)", color: "var(--rq-dim)" }} aria-label="Environment">
              <span className="rq-dot" style={{ background: activeEnv?.color || "var(--rq-mute)" }} />
              {activeEnv?.name ?? "No environment"}
              <ChevronDown style={{ width: 12, height: 12, color: "var(--rq-mute)" }} />
            </Button>
          }
          items={[
            { type: "label", key: "l", label: "Environment" },
            ...envList.map((e) => ({ key: e.id, label: e.name, icon: <span className="rq-dot" style={{ background: e.color || "var(--rq-mute)", margin: "0 3px" }} />, end: e.id === activeEnv?.id ? <Check style={{ width: 13, height: 13 }} /> : undefined })),
            { type: "separator", key: "s" },
            { key: "__manage", label: "Manage environments" },
          ]}
          onSelect={(k) => (k === "__manage" ? nav("/environments") : setPrefs({ activeEnvironmentId: k }))}
        />
      )}
      <Menu
        align="end"
        trigger={<Button variant="ghost" icon={mode === "system" ? <Monitor /> : isDark ? <Moon /> : <Sun />} aria-label="Theme" />}
        items={[
          { key: "dark", label: "Dark", icon: <Moon />, end: mode === "dark" ? <Check style={{ width: 13, height: 13 }} /> : undefined },
          { key: "light", label: "Light", icon: <Sun />, end: mode === "light" ? <Check style={{ width: 13, height: 13 }} /> : undefined },
          { key: "system", label: "Match system", icon: <Monitor />, end: mode === "system" ? <Check style={{ width: 13, height: 13 }} /> : undefined },
        ]}
        onSelect={(k) => setPrefs({ theme: k as "light" | "dark" | "system" })}
      />
      {!personal ? (
        <Button variant="ghost" onClick={() => setShare(true)}>
          Share
        </Button>
      ) : signedIn ? (
        <Button variant="ghost" onClick={() => setCopying(true)}>
          Copy to team workspace
        </Button>
      ) : (
        <Button variant="ghost" onClick={() => setSigningIn(true)}>
          Sign in
        </Button>
      )}
      <Menu
        align="end"
        trigger={
          <button type="button" aria-label="Account" style={{ width: 24, height: 24, borderRadius: "50%", border: 0, background: "#20304A", color: "#9CC3FF", fontSize: 10.5, fontWeight: 600, cursor: "pointer", marginLeft: 2, fontFamily: "inherit" }}>
            {me.data ? initials(me.data.name) : ""}
          </button>
        }
        items={[
          {
            type: "label",
            key: "who",
            label: (
              <div>
                <div style={{ color: "var(--rq-text)", fontWeight: 500, fontSize: 13 }}>{me.data?.name}</div>
                <div>{signedIn ? `${me.data?.email} · ${personal ? "My workspace" : me.data?.memberRole}` : "Not signed in · kept in this browser"}</div>
              </div>
            ),
          },
          { type: "separator", key: "s1" },
          ...((signedIn
            ? [
                { key: "/settings/profile", label: "Profile" },
                { key: "/settings/keys", label: "API keys" },
                { key: "/settings/preferences", label: "Preferences", end: "⌘," },
                ...(me.data?.isAdmin ? [{ type: "separator", key: "s2" }, { key: "/admin/overview", label: "Admin console" }] : []),
                { type: "separator", key: "s3" },
                { key: "signout", label: "Sign out", danger: true },
              ]
            : [{ key: "/settings/preferences", label: "Preferences", end: "⌘," }, { type: "separator", key: "s4" }, { key: "signin", label: "Sign in to your organization" }]) as MenuItem[]),
        ]}
        onSelect={(k) => (k === "signout" ? signOut() : k === "signin" ? setSigningIn(true) : nav(k))}
      />
      <CommandPalette open={palette} onClose={() => setPalette(false)} />
      <ShareModal open={share} onClose={() => setShare(false)} />
      <CreateWorkspaceModal open={creating} onClose={() => setCreating(false)} />
      <SignInModal open={signingIn} onClose={() => setSigningIn(false)} />
      <CopyToTeamModal open={copying} onClose={() => setCopying(false)} />
    </header>
  );
}
