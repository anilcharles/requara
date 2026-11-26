import { ApiOutlined, CaretDownOutlined, CaretRightOutlined, ClockCircleOutlined, CodeOutlined, DashboardOutlined, FilterOutlined, GlobalOutlined, PlusOutlined, SendOutlined, SettingOutlined, SlidersOutlined } from "@ant-design/icons";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button, Dropdown, Input, Layout, Result, Spin, theme, Tooltip } from "antd";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Navigate, Outlet, useLocation, useNavigate } from "react-router-dom";
import { get, type Collection, type CollectionRequest, type Instance } from "../api";
import { Method } from "../components/bits";
import { downloadCollection, ImportModal, NewCollectionModal, RenameCollectionModal, useDeleteCollection, useNewRequest } from "./CollectionActions";
import ConsoleDock from "./Console";
import CookiesModal from "./Cookies";
import Header, { CreateWorkspaceModal } from "./Header";
import { useMe } from "./session";

export const useInstance = () => useQuery({ queryKey: ["instance"], queryFn: () => get<Instance>("/api/instance"), staleTime: 60_000 });

export function useCollections() {
  return useQuery({ queryKey: ["collections"], queryFn: () => get<Collection[]>("/api/collections"), staleTime: 30_000 });
}

// AppLayout: header on every signed-in page.
export function AppLayout() {
  const me = useMe();
  const loc = useLocation();
  if (me.isLoading) {
    return (
      <div style={{ height: "100vh", display: "grid", placeItems: "center" }}>
        <Spin />
      </div>
    );
  }
  if (me.isError) return <Navigate to={`/signin?redirect=${encodeURIComponent(loc.pathname + loc.search)}`} replace />;
  if (me.data && !me.data.workspaceId && !loc.pathname.startsWith("/admin")) return <NoWorkspace />;
  return (
    <Layout style={{ height: "100vh" }}>
      <InstanceBanner />
      <Header />
      <Layout style={{ flex: 1, minHeight: 0, flexDirection: "row" }}>
        <IconRail />
        <Layout style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
          <Outlet />
        </Layout>
      </Layout>
      <ConsoleDock />
    </Layout>
  );
}

// IconRail: Postman-style navigation down the left edge. Each icon opens a
// section; the panel beside it (collections, environments…) belongs to the page.
const RAIL = [
  { path: "/requests", icon: <SendOutlined />, label: "Collections" },
  { path: "/apis", icon: <ApiOutlined />, label: "APIs" },
  { path: "/environments", icon: <SlidersOutlined />, label: "Environments" },
  { path: "/history", icon: <ClockCircleOutlined />, label: "History" },
  { path: "/mocks", icon: <CodeOutlined />, label: "Mocks" },
  { path: "/monitors", icon: <DashboardOutlined />, label: "Monitors" },
];

function IconRail() {
  const { token } = theme.useToken();
  const nav = useNavigate();
  const loc = useLocation();
  const [cookies, setCookies] = useState(false);
  // A collection's settings and runs belong to Collections.
  const active = (p: string) => loc.pathname.startsWith(p) || (p === "/requests" && /^\/(collections|runner)\//.test(loc.pathname));
  const item = (key: string, icon: ReactNode, label: string, on: boolean, onClick: () => void) => (
      <button
        key={key}
        type="button"
        onClick={onClick}
        aria-label={label}
        aria-current={on ? "page" : undefined}
        className="requara-rail-item"
        style={{
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          gap: 4,
          width: 76,
          padding: "8px 0",
          border: "none",
          borderRadius: 8,
          cursor: "pointer",
          background: on ? token.colorFillSecondary : "transparent",
          color: on ? token.colorText : token.colorTextSecondary,
          fontSize: 11,
          fontFamily: "inherit",
        }}
      >
        <span style={{ fontSize: 18, lineHeight: 1, color: on ? token.colorPrimary : undefined }}>{icon}</span>
        <span style={{ whiteSpace: "nowrap" }}>{label}</span>
      </button>
  );
  return (
    <nav
      aria-label="Sections"
      style={{ width: 88, flexShrink: 0, display: "flex", flexDirection: "column", alignItems: "center", gap: 4, padding: "10px 0", borderRight: `1px solid ${token.colorSplit}`, background: token.Layout?.siderBg ?? token.colorBgContainer }}
    >
      {RAIL.map((r) => item(r.path, r.icon, r.label, active(r.path), () => nav(r.path)))}
      <div style={{ flex: 1 }} />
      {item("cookies", <GlobalOutlined />, "Cookies", false, () => setCookies(true))}
      {item("settings", <SettingOutlined />, "Settings", loc.pathname.startsWith("/settings"), () => nav("/settings/preferences"))}
      <CookiesModal open={cookies} onClose={() => setCookies(false)} />
    </nav>
  );
}

// The admin's banner, on every page.
function InstanceBanner() {
  const inst = useInstance();
  const b = inst.data?.banner;
  if (!b?.enabled || !b.message) return null;
  return <Alert banner type={b.level === "danger" ? "error" : b.level} message={b.message} style={{ flexShrink: 0 }} />;
}

// Someone who signed in but belongs to no workspace yet.
function NoWorkspace() {
  const me = useMe();
  const inst = useInstance();
  const nav = useNavigate();
  const [creating, setCreating] = useState(false);
  return (
    <div style={{ height: "100vh", display: "grid", placeItems: "center" }}>
      <Result
        title="You're not in a workspace yet"
        subTitle={`Ask a workspace owner on ${inst.data?.org ?? "this server"} to invite ${me.data?.email}${inst.data?.allowWorkspaceCreation ? ", or create your own" : ""}.`}
        extra={[
          inst.data?.allowWorkspaceCreation && (
            <Button key="new" type="primary" onClick={() => setCreating(true)}>
              Create a workspace
            </Button>
          ),
          me.data?.isAdmin && (
            <Button key="admin" onClick={() => nav("/admin/overview")}>
              Admin console
            </Button>
          ),
          <Button key="out" type="link" href="/signin" onClick={() => fetch("/auth/logout", { method: "POST", headers: { "X-Requested-With": "requara" } })}>
            Sign out
          </Button>,
        ]}
      />
      <CreateWorkspaceModal open={creating} onClose={() => setCreating(false)} />
    </div>
  );
}

function useStored<T>(key: string, init: T): [T, (v: T) => void] {
  const [v, setV] = useState<T>(() => {
    try {
      const raw = localStorage.getItem(key);
      return raw ? (JSON.parse(raw) as T) : init;
    } catch {
      return init;
    }
  });
  return [
    v,
    (nv: T) => {
      setV(nv);
      try {
        localStorage.setItem(key, JSON.stringify(nv));
      } catch {
        /* storage unavailable */
      }
    },
  ];
}

type SiderProps = {
  selected?: string; // request id, or "apiId|operationKey"
  onPick: (r: CollectionRequest, c: Collection) => void;
  isSelected?: (r: CollectionRequest, c: Collection) => boolean;
};

export function CollectionsSider({ onPick, isSelected }: SiderProps) {
  const { token } = theme.useToken();
  const nav = useNavigate();
  const newRequest = useNewRequest();
  const deleteCollection = useDeleteCollection();
  const [creating, setCreating] = useState(false);
  const [importing, setImporting] = useState<{ collectionId?: string | null } | null>(null);
  const [renaming, setRenaming] = useState<Collection | null>(null);
  const collectionMenu = (c: Collection) => ({
    items: [
      { key: "add", label: "Add request" },
      { key: "ws", label: "Add WebSocket request" },
      ...(c.kind === "collection" ? [{ key: "curl", label: "Import cURL here" }] : []),
      { type: "divider" as const },
      { key: "run", label: "Run collection" },
      ...(c.kind !== "scratch" ? [{ key: "settings", label: "Variables, auth and scripts" }] : []),
      { key: "export", label: "Export (Postman v2.1)" },
      { key: "export-files", label: "Export as files (YAML, for Git)" },
      ...(c.kind === "collection"
        ? [{ type: "divider" as const }, { key: "rename", label: "Rename" }, { key: "delete", label: "Delete", danger: true }]
        : []),
    ],
    onClick: ({ key, domEvent }: { key: string; domEvent: { stopPropagation: () => void } }) => {
      domEvent.stopPropagation();
      const target = c.kind === "collection" ? { collectionId: c.id } : c.kind === "api" ? { apiId: c.id } : {};
      if (key === "add") newRequest.mutate(target);
      if (key === "ws") newRequest.mutate({ ...target, kind: "websocket" });
      if (key === "curl") setImporting({ collectionId: c.id });
      if (key === "run") nav(`/runner/${c.id}`);
      if (key === "settings") nav(`/collections/${c.id}`);
      if (key === "export") downloadCollection(c.id);
      if (key === "export-files") downloadCollection(c.id, "files");
      if (key === "rename") setRenaming(c);
      if (key === "delete") deleteCollection(c);
    },
  });
  const cols = useCollections();
  const [filter, setFilter] = useState("");
  const [open, setOpen] = useStored<Record<string, boolean>>("requara.tree", {});

  const tree = useMemo(() => {
    const f = filter.toLowerCase();
    if (!f) return cols.data ?? [];
    return (cols.data ?? [])
      .map((c) => ({
        ...c,
        folders: c.folders
          .map((fo) => ({ ...fo, requests: fo.requests.filter((r) => (r.name + " " + r.method + " " + r.operationKey).toLowerCase().includes(f)) }))
          .filter((fo) => fo.requests.length),
      }))
      .filter((c) => c.folders.length || c.name.toLowerCase().includes(f));
  }, [cols.data, filter]);

  // Expand the API and folder that hold the selected request, and scroll it into view,
  // so picking an API elsewhere (APIs table, search, docs links) shows up here too.
  const selectedPath: string[] = [];
  for (const c of cols.data ?? [])
    for (const f of c.folders)
      if (f.requests.some((r) => isSelected?.(r, c))) selectedPath.push("c" + c.id, "c" + c.id + "/" + f.name);
  const selectedKey = selectedPath.join("|");
  const selectedRow = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (!selectedKey) return;
    if (selectedPath.some((k) => open[k] === false || open[k] === undefined)) {
      setOpen({ ...open, ...Object.fromEntries(selectedPath.map((k) => [k, true])) });
    }
    requestAnimationFrame(() => selectedRow.current?.scrollIntoView({ block: "nearest" }));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedKey]);

  const isOpen = (k: string, dflt: boolean) => (filter ? true : (open[k] ?? dflt));
  const toggle = (k: string, dflt: boolean) => setOpen({ ...open, [k]: !isOpen(k, dflt) });

  const row = (key: string, depth: number, content: ReactNode, onClick: () => void, selected = false) => (
    <div
      key={key}
      ref={selected && depth === 2 ? selectedRow : undefined}
      onClick={onClick}
      className="requara-row"
      style={{
        display: "flex",
        alignItems: "center",
        gap: 8,
        height: 32,
        padding: `0 8px 0 ${8 + depth * 16}px`,
        borderRadius: 6,
        cursor: "pointer",
        background: selected ? token.controlItemBgActive : undefined,
        color: selected ? token.colorPrimary : token.colorText,
      }}
    >
      {content}
    </div>
  );
  const caret = (o: boolean) => (o ? <CaretDownOutlined style={{ fontSize: 10, color: token.colorTextTertiary, width: 12 }} /> : <CaretRightOutlined style={{ fontSize: 10, color: token.colorTextTertiary, width: 12 }} />);
  return (
    <Layout.Sider width={272} style={{ borderRight: `1px solid ${token.colorSplit}`, display: "flex", flexDirection: "column" }}>
      <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", height: 48, padding: "0 8px 0 16px" }}>
          <span style={{ fontWeight: 600 }}>Collections</span>
          <Dropdown
            trigger={["click"]}
            menu={{
              items: [
                { key: "request", label: "New request" },
                { key: "ws", label: "New WebSocket request" },
                { key: "collection", label: "New collection" },
                { type: "divider" },
                { key: "import", label: "Import Postman collection or cURL…" },
                { key: "api", label: "New API from an OpenAPI spec…" },
              ],
              onClick: ({ key }) => {
                if (key === "request") newRequest.mutate({});
                if (key === "ws") newRequest.mutate({ kind: "websocket" });
                if (key === "collection") setCreating(true);
                if (key === "import") setImporting({});
                if (key === "api") nav("/apis?new=1");
              },
            }}
          >
            <Tooltip title="New">
              <Button type="text" icon={<PlusOutlined />} aria-label="New" />
            </Tooltip>
          </Dropdown>
        </div>
        <div style={{ padding: "0 12px 8px" }}>
          <Input size="small" allowClear prefix={<FilterOutlined style={{ color: token.colorTextTertiary }} />} placeholder="Filter" value={filter} onChange={(e) => setFilter(e.target.value)} />
        </div>
        <div style={{ flex: 1, overflowY: "auto", padding: "0 8px 8px" }}>
          {cols.isLoading && <Spin size="small" style={{ margin: 16 }} />}
          {tree.map((c, ci) => {
            const ck = "c" + c.id;
            const co = isOpen(ck, ci === 0);
            return (
              <div key={c.id}>
                {row(
                  ck,
                  0,
                  <>
                    {caret(co)}
                    <span style={{ flex: 1, fontWeight: 500, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: c.status === "deprecated" ? token.colorTextTertiary : undefined }}>{c.name}</span>
                    <span style={{ fontSize: 11, color: token.colorTextTertiary, fontFamily: "JetBrains Mono, monospace" }}>{c.version}</span>
                    <Dropdown trigger={["click"]} menu={collectionMenu(c)}>
                      <span className="requara-more" role="button" aria-label={`${c.name} actions`} onClick={(e) => e.stopPropagation()} style={{ padding: "0 4px", color: token.colorTextTertiary, fontWeight: 700 }}>
                        ···
                      </span>
                    </Dropdown>
                  </>,
                  () => toggle(ck, ci === 0),
                )}
                {co &&
                  c.folders.map((f, fi) => {
                    const fk = ck + "/" + f.name;
                    const fo = isOpen(fk, ci === 0 && fi === 0);
                    return (
                      <div key={fk}>
                        {row(
                          fk,
                          1,
                          <>
                            {caret(fo)}
                            <span style={{ flex: 1 }}>{f.name || "Requests"}</span>
                            {!fo && <span style={{ fontSize: 12, color: token.colorTextTertiary }}>{f.requests.length}</span>}
                          </>,
                          () => toggle(fk, ci === 0 && fi === 0),
                        )}
                        {fo &&
                          f.requests.map((r) => {
                            const sel = isSelected?.(r, c) ?? false;
                            return row(
                              r.id,
                              2,
                              <>
                                <Method method={r.method} />
                                <span style={{ flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontWeight: sel ? 500 : 400 }}>{r.name}</span>
                              </>,
                              () => onPick(r, c),
                              sel,
                            );
                          })}
                      </div>
                    );
                  })}
              </div>
            );
          })}
          {!cols.isLoading && tree.length === 0 && (
            <div style={{ padding: 16, color: token.colorTextTertiary, fontSize: 13 }}>
              <ApiOutlined /> {filter ? "Nothing matches." : "No collections yet. Use + to add a request, a collection or an import."}
            </div>
          )}
        </div>
      </div>
      <NewCollectionModal open={creating} onClose={() => setCreating(false)} />
      <ImportModal open={!!importing} collectionId={importing?.collectionId} onClose={() => setImporting(null)} />
      <RenameCollectionModal collection={renaming} onClose={() => setRenaming(null)} />
    </Layout.Sider>
  );
}

// WorkspaceLayout: collections sidebar + page. Clicking a request opens it in Requests.
export function WorkspaceLayout() {
  const nav = useNavigate();
  return (
    <>
      <CollectionsSider onPick={(r) => nav(`/requests/${r.id}`)} />
      <Layout.Content style={{ overflow: "auto" }}>
        <Outlet />
      </Layout.Content>
    </>
  );
}

// PageLayout: a rail section that lists its own items (environments, history…).
export function PageLayout() {
  return (
    <Layout.Content style={{ overflow: "auto" }}>
      <Outlet />
    </Layout.Content>
  );
}
