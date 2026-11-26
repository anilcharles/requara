import { DesktopOutlined, DownOutlined, MoonOutlined, SearchOutlined, SunOutlined } from "@ant-design/icons";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Avatar, Button, Divider, Dropdown, Layout, Menu, Select, Space, theme, Tooltip, Typography } from "antd";
import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { get, patch, post } from "../api";
import { Dot, Kbd } from "../components/bits";
import CommandPalette from "./CommandPalette";
import { useEnvironments, useMe, usePrefs } from "./session";
import ShareModal from "./ShareModal";

export function Logo({ size = 22 }: { size?: number }) {
  return (
    // The Speklo mark, "Orbit" (branding/speklo-icon.svg): a ring with a speck breaking out of it.
    <svg width={size} height={size} viewBox="0 0 64 64" aria-hidden>
      <rect width="64" height="64" rx="14.4" fill="#14161F" />
      <g transform="translate(32 32) scale(0.68) translate(-32 -32)">
        <circle cx="32" cy="32" r="18" fill="none" stroke="#fff" strokeWidth="5.5" />
        <circle cx="44.7" cy="19.3" r="9" fill="#14161F" />
        <circle cx="44.7" cy="19.3" r="5.5" fill="#4F46E5" />
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

const NAV = [
  { key: "apis", label: "APIs", path: "/apis" },
  { key: "requests", label: "Requests", path: "/requests" },
  { key: "design", label: "Design", path: "/design" },
  { key: "docs", label: "Docs", path: "/docs" },
];

export default function Header() {
  const { token } = theme.useToken();
  const nav = useNavigate();
  const loc = useLocation();
  const qc = useQueryClient();
  const me = useMe();
  const { prefs, isDark, setPrefs } = usePrefs();
  const mode = prefs.theme ?? "system";
  const envs = useEnvironments();
  const workspaces = useQuery({ queryKey: ["workspaces"], queryFn: () => get<{ id: string; name: string; role: string }[]>("/api/workspaces") });
  const [palette, setPalette] = useState(false);
  const [share, setShare] = useState(false);

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

  const section = loc.pathname.split("/")[1];
  const current = workspaces.data?.find((w) => w.id === me.data?.workspaceId);
  const nonGlobal = envs.data?.items.filter((e) => !e.isGlobal) ?? [];

  const signOut = async () => {
    const { logoutUrl } = await post<{ logoutUrl: string }>("/auth/logout");
    qc.clear();
    location.assign(logoutUrl);
  };

  return (
    <Layout.Header style={{ display: "flex", alignItems: "center", gap: 16, borderBottom: `1px solid ${token.colorSplit}`, lineHeight: "normal" }}>
      <a onClick={() => nav("/apis")} style={{ display: "flex", alignItems: "center", gap: 8, cursor: "pointer" }}>
        <Logo />
        <Typography.Text strong style={{ fontSize: 16, letterSpacing: -0.2 }}>
          Speklo
        </Typography.Text>
      </a>
      <Divider type="vertical" style={{ height: 16, margin: 0 }} />
      <Dropdown
        trigger={["click"]}
        menu={{
          selectable: true,
          selectedKeys: me.data ? [me.data.workspaceId] : [],
          items: (workspaces.data ?? []).map((w) => ({ key: w.id, label: w.name, extra: w.role })),
          // The server picks the workspace from saved prefs, so save first, then refetch everything.
          onClick: ({ key }) => {
            if (key === me.data?.workspaceId) return;
            patch("/api/me/preferences", { workspaceId: key }).then(() => qc.invalidateQueries());
          },
        }}
      >
        <Button type="text" style={{ fontWeight: 500 }}>
          {current?.name ?? "Workspace"} <DownOutlined style={{ fontSize: 10, color: token.colorTextTertiary }} />
        </Button>
      </Dropdown>
      <Menu
        mode="horizontal"
        selectedKeys={[section]}
        items={NAV.map((n) => ({ key: n.key, label: n.label }))}
        onClick={({ key }) => nav(NAV.find((n) => n.key === key)!.path)}
        style={{ borderBottom: "none", minWidth: 330, background: "transparent", lineHeight: "54px" }}
      />
      <div style={{ flex: 1 }} />
      <Button
        onClick={() => setPalette(true)}
        style={{ width: 320, justifyContent: "flex-start", color: token.colorTextPlaceholder, display: "flex", alignItems: "center", gap: 8 }}
      >
        <SearchOutlined />
        <span style={{ flex: 1, textAlign: "left" }}>Search requests, endpoints, schemas</span>
        <Kbd>⌘K</Kbd>
      </Button>
      <div style={{ flex: 1 }} />
      <Select
        value={envs.data?.activeId || undefined}
        placeholder="No environment"
        style={{ width: 150 }}
        onChange={(id) => {
          setPrefs({ activeEnvironmentId: id });
          qc.setQueryData(["environments"], (d: typeof envs.data) => (d ? { ...d, activeId: id } : d));
        }}
        options={nonGlobal.map((e) => ({
          value: e.id,
          label: (
            <Space size={8}>
              <Dot color={e.color} />
              {e.name}
            </Space>
          ),
        }))}
        dropdownRender={(menu) => (
          <>
            {menu}
            <Divider style={{ margin: "4px 0" }} />
            <Button type="text" block style={{ textAlign: "left" }} onClick={() => nav("/environments")}>
              Manage environments
            </Button>
          </>
        )}
      />
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
      <Button onClick={() => setShare(true)}>Share</Button>
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
                    {me.data?.email} · {me.data?.memberRole}
                  </div>
                </div>
              ),
            },
            { type: "divider" },
            { key: "/settings/profile", label: "Profile" },
            { key: "/settings/keys", label: "API keys" },
            { key: "/settings/preferences", label: "Preferences", extra: "⌘," },
            { type: "divider" },
            { key: "signout", label: "Sign out", danger: true },
          ],
          onClick: ({ key }) => (key === "signout" ? signOut() : nav(key)),
        }}
      >
        <Avatar style={{ background: token.colorPrimaryBg, color: token.colorPrimaryText, cursor: "pointer", fontSize: 12, fontWeight: 500 }}>
          {me.data ? initials(me.data.name) : ""}
        </Avatar>
      </Dropdown>
      <CommandPalette open={palette} onClose={() => setPalette(false)} />
      <ShareModal open={share} onClose={() => setShare(false)} />
    </Layout.Header>
  );
}
