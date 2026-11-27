import { DesktopOutlined, DownOutlined, MoonOutlined, SearchOutlined, SunOutlined } from "@ant-design/icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Avatar, Button, Dropdown, Input, Layout, Modal, theme, Tooltip, Typography } from "antd";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { get, isSignedIn, post, selectWorkspace, serverApi } from "../api";
import { SignInModal } from "../components/SignInOptions";
import { PERSONAL_ID } from "../personal/backend";
import CopyToTeamModal from "./CopyToTeam";
import { Kbd } from "../components/bits";
import CommandPalette from "./CommandPalette";
import { useMe, usePrefs } from "./session";
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
  const { token } = theme.useToken();
  const nav = useNavigate();
  const qc = useQueryClient();
  const me = useMe();
  const { prefs, isDark, setPrefs } = usePrefs();
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

  const signOut = async () => {
    const { logoutUrl } = await post<{ logoutUrl: string }>("/auth/logout");
    selectWorkspace(PERSONAL_ID);
    qc.clear();
    location.assign(logoutUrl);
  };

  return (
    <Layout.Header style={{ display: "flex", alignItems: "center", gap: 16, lineHeight: "normal" }}>
      {/* Brand and workspace fill the sidebar's column; search starts where the canvas starts. */}
      <div style={{ width: 236, flexShrink: 0, display: "flex", alignItems: "center", gap: 10, minWidth: 0 }}>
      <a onClick={() => nav("/apis")} style={{ display: "flex", alignItems: "center", gap: 8, cursor: "pointer", flexShrink: 0, whiteSpace: "nowrap" }}>
        <Logo />
        <Typography.Text strong style={{ fontSize: 16, letterSpacing: -0.2 }}>
          Requara
        </Typography.Text>
      </a>
      <Dropdown
        trigger={["click"]}
        menu={{
          selectable: true,
          selectedKeys: me.data ? [me.data.workspaceId] : [],
          items: [
            { type: "group" as const, label: "Personal", children: (workspaces.data ?? []).filter((w) => w.id === PERSONAL_ID).map((w) => ({ key: w.id, label: w.name, extra: "only you" })) },
            signedIn
              ? { type: "group" as const, label: "Team workspaces", children: (workspaces.data ?? []).filter((w) => w.id !== PERSONAL_ID).map((w) => ({ key: w.id, label: w.name, extra: w.role })) }
              : { type: "group" as const, label: "Team workspaces", children: [{ key: "__signin", label: "Sign in to your organization…" }] },
            ...(signedIn && instance.data?.allowWorkspaceCreation ? [{ type: "divider" as const }, { key: "__new", label: "New team workspace…" }] : []),
          ],
          onClick: ({ key }) => {
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
          },
        }}
      >
        <Button type="text" style={{ fontWeight: 500, minWidth: 0, overflow: "hidden" }}>
          <span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>{current?.name ?? "Workspace"}</span> <DownOutlined style={{ fontSize: 10, color: token.colorTextTertiary }} />
        </Button>
      </Dropdown>
      </div>
      <Button
        onClick={() => setPalette(true)}
        style={{ width: 360, height: 30, justifyContent: "flex-start", color: token.colorTextPlaceholder, display: "flex", alignItems: "center", gap: 8, background: token.colorFillTertiary, border: "none" }}
      >
        <SearchOutlined />
        <span style={{ flex: 1, textAlign: "left" }}>Search requests and APIs</span>
        <Kbd>⌘K</Kbd>
      </Button>
      <div style={{ flex: 1 }} />
      <Dropdown
        trigger={["click"]}
        placement="bottomRight"
        menu={{
          selectable: true,
          selectedKeys: [mode],
          items: [
            { key: "light", icon: <SunOutlined />, label: "Light" },
            { key: "dark", icon: <MoonOutlined />, label: "Dark" },
            { key: "system", icon: <DesktopOutlined />, label: "Match system" },
          ],
          onClick: ({ key }) => setPrefs({ theme: key as "light" | "dark" | "system" }),
        }}
      >
        <Tooltip title={`Theme: ${mode === "system" ? `system (${isDark ? "dark" : "light"})` : mode}`} placement="bottom">
          <Button type="text" aria-label="Change theme" icon={mode === "system" ? <DesktopOutlined /> : isDark ? <MoonOutlined /> : <SunOutlined />} />
        </Tooltip>
      </Dropdown>
      {!personal ? (
        <Button onClick={() => setShare(true)}>Share</Button>
      ) : signedIn ? (
        <Button onClick={() => setCopying(true)}>Copy to team workspace</Button>
      ) : (
        <Button size="small" onClick={() => setSigningIn(true)}>
          Sign in
        </Button>
      )}
      <Dropdown
        trigger={["click"]}
        placement="bottomRight"
        menu={{
          items: [
            {
              key: "who",
              disabled: true,
              label: (
                <div style={{ padding: "2px 0" }}>
                  <div style={{ color: token.colorText, fontWeight: 500 }}>{me.data?.name}</div>
                  <div style={{ fontSize: 12, color: token.colorTextTertiary }}>
                    {signedIn ? `${me.data?.email} · ${personal ? "My workspace" : me.data?.memberRole}` : "Not signed in · My workspace is kept in this browser"}
                  </div>
                </div>
              ),
            },
            { type: "divider" },
            ...(signedIn
              ? [
                  { key: "/settings/profile", label: "Profile" },
                  { key: "/settings/keys", label: "API keys" },
                  { key: "/settings/preferences", label: "Preferences", extra: "⌘," },
                  ...(me.data?.isAdmin ? [{ type: "divider" as const }, { key: "/admin/overview", label: "Admin console" }] : []),
                  { type: "divider" as const },
                  { key: "signout", label: "Sign out", danger: true },
                ]
              : [
                  { key: "/settings/preferences", label: "Preferences", extra: "⌘," },
                  { type: "divider" as const },
                  { key: "signin", label: "Sign in to your organization" },
                ]),
          ],
          onClick: ({ key }) => (key === "signout" ? signOut() : key === "signin" ? setSigningIn(true) : nav(key)),
        }}
      >
        <Avatar size={28} style={{ background: token.colorPrimaryBg, color: token.colorPrimaryText, cursor: "pointer", fontSize: 12, fontWeight: 500 }}>
          {me.data ? initials(me.data.name) : ""}
        </Avatar>
      </Dropdown>
      <CommandPalette open={palette} onClose={() => setPalette(false)} />
      <ShareModal open={share} onClose={() => setShare(false)} />
      <CreateWorkspaceModal open={creating} onClose={() => setCreating(false)} />
      <SignInModal open={signingIn} onClose={() => setSigningIn(false)} />
      <CopyToTeamModal open={copying} onClose={() => setCopying(false)} />
    </Layout.Header>
  );
}
