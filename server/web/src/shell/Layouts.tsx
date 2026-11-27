import { Activity, Boxes, ChevronDown, ChevronRight, Cookie, Filter, History, MoreHorizontal, Plus, Send, SlidersHorizontal, SquareTerminal } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Button as AntButton, Layout, Result, Spin } from "../ui/kit";
import { Button, Menu, TextInput, type MenuItem } from "../ui";
import { methodColor } from "../lib";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Navigate, Outlet, useLocation, useNavigate } from "react-router-dom";
import { get, type Collection, type CollectionRequest, type Instance } from "../api";
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
    <Layout style={{ height: "100vh", flexDirection: "column" }}>
      <InstanceBanner />
      <Header />
      {/* The window frame (top bar, sidebar) is one surface; pages sit on an inset canvas. */}
      <div style={{ flex: 1, minHeight: 0, display: "flex" }}>
        <AppSidebar />
        <Canvas>
          <Outlet />
        </Canvas>
      </div>
      <ConsoleDock />
    </Layout>
  );
}

// Navigation at the top of the one sidebar; the collections tree below it.
const NAV = [
  { path: "/requests", icon: <Send />, label: "Requests" },
  { path: "/apis", icon: <Boxes />, label: "APIs" },
  { path: "/environments", icon: <SlidersHorizontal />, label: "Environments" },
  { path: "/history", icon: <History />, label: "History" },
  { path: "/mocks", icon: <SquareTerminal />, label: "Mock servers" },
  { path: "/monitors", icon: <Activity />, label: "Monitors" },
];

function AppSidebar() {
  const nav = useNavigate();
  const loc = useLocation();
  const open = loc.pathname.match(/^\/requests\/([^/]+)/)?.[1];
  return <CollectionsSider onPick={(r) => nav(`/requests/${r.id}`)} isSelected={(r) => r.id === open} />;
}

function NavList() {
  const nav = useNavigate();
  const loc = useLocation();
  const [cookies, setCookies] = useState(false);
  const active = (p: string) => loc.pathname.startsWith(p) || (p === "/requests" && /^\/(collections|runner)\//.test(loc.pathname));
  const row = (key: string, icon: ReactNode, label: string, on: boolean, onClick: () => void) => (
    <div key={key} role="link" aria-label={label} tabIndex={0} aria-current={on ? "page" : undefined} onClick={onClick} onKeyDown={(e) => e.key === "Enter" && onClick()} className="rq-row">
      {icon}
      {label}
    </div>
  );
  return (
    <nav aria-label="Sections" style={{ display: "flex", flexDirection: "column", gap: 1 }}>
      {NAV.map((n) => row(n.path, n.icon, n.label, active(n.path), () => nav(n.path)))}
      {row("cookies", <Cookie />, "Cookies", false, () => setCookies(true))}
      <CookiesModal open={cookies} onClose={() => setCookies(false)} />
    </nav>
  );
}

// The work area: a rounded panel inset in the window frame.
function Canvas({ children }: { children: ReactNode }) {
  return <div className="rq-canvas">{children}</div>;
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
            <AntButton key="new" type="primary" onClick={() => setCreating(true)}>
              Create a workspace
            </AntButton>
          ),
          me.data?.isAdmin && (
            <AntButton key="admin" onClick={() => nav("/admin/overview")}>
              Admin console
            </AntButton>
          ),
          <AntButton key="out" type="link" href="/signin" onClick={() => fetch("/auth/logout", { method: "POST", headers: { "X-Requested-With": "requara" } })}>
            Sign out
          </AntButton>,
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
  const nav = useNavigate();
  const newRequest = useNewRequest();
  const deleteCollection = useDeleteCollection();
  const [creating, setCreating] = useState(false);
  const [importing, setImporting] = useState<{ collectionId?: string | null } | null>(null);
  const [renaming, setRenaming] = useState<Collection | null>(null);
  const collectionItems = (c: Collection): MenuItem[] => [
    { key: "add", label: "Add request" },
    { key: "ws", label: "Add WebSocket request" },
    ...(c.kind === "collection" ? [{ key: "curl", label: "Import cURL here" }] : []),
    { type: "separator", key: "s1" },
    { key: "run", label: "Run collection" },
    ...(c.kind !== "scratch" ? [{ key: "settings", label: "Variables, auth and scripts" }] : []),
    { key: "export", label: "Export (Postman v2.1)" },
    { key: "export-files", label: "Export as files (YAML, for Git)" },
    ...(c.kind === "collection" ? ([{ type: "separator", key: "s2" }, { key: "rename", label: "Rename" }, { key: "delete", label: "Delete", danger: true }] as MenuItem[]) : []),
  ];
  const onCollection = (c: Collection, key: string) => {
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
  };
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

  // A folder that is the collection's only one, unnamed, adds nothing: its requests sit under the collection.
  const row = (key: string, depth: number, content: ReactNode, onClick: () => void, selected = false) => (
    <div
      key={key}
      ref={selected ? selectedRow : undefined}
      onClick={onClick}
      onKeyDown={(e) => e.key === "Enter" && onClick()}
      tabIndex={0}
      className="rq-row"
      aria-selected={selected || undefined}
      style={{ gap: 8, paddingLeft: 10 + depth * 16 }}
    >
      {content}
    </div>
  );
  const chevron = (o: boolean) => (o ? <ChevronDown style={{ width: 13, height: 13 }} /> : <ChevronRight style={{ width: 13, height: 13 }} />);
  return (
    <aside style={{ width: 248, flexShrink: 0, display: "flex", flexDirection: "column", padding: "4px 8px 8px", minHeight: 0 }}>
      <NavList />
      <div className="rq-section">
        <span>Collections</span>
        <Menu
          align="end"
          trigger={<Button variant="ghost" size="sm" icon={<Plus />} aria-label="New" />}
          items={[
            { key: "request", label: "New request" },
            { key: "ws", label: "New WebSocket request" },
            { key: "collection", label: "New collection" },
            { type: "separator", key: "s" },
            { key: "import", label: "Import Postman collection or cURL…" },
            { key: "api", label: "New API from an OpenAPI spec…" },
          ]}
          onSelect={(key) => {
            if (key === "request") newRequest.mutate({});
            if (key === "ws") newRequest.mutate({ kind: "websocket" });
            if (key === "collection") setCreating(true);
            if (key === "import") setImporting({});
            if (key === "api") nav("/apis?new=1");
          }}
        />
      </div>
      <div style={{ position: "relative", margin: "2px 0 4px" }}>
        <Filter style={{ position: "absolute", left: 10, top: 8, width: 13, height: 13, color: "var(--rq-mute)" }} />
        <TextInput placeholder="Filter" value={filter} onChange={(e) => setFilter(e.target.value)} aria-label="Filter collections" style={{ paddingLeft: 29, borderColor: "transparent" }} />
      </div>
      <div style={{ flex: 1, overflowY: "auto", display: "flex", flexDirection: "column", gap: 1 }}>
        {cols.isLoading && <div style={{ padding: 10, color: "var(--rq-mute)" }}>Loading…</div>}
        {tree.map((c, ci) => {
          const ck = "c" + c.id;
          const co = isOpen(ck, ci === 0);
          const flat = c.folders.length === 1 && !c.folders[0].name;
          return (
            <div key={c.id} style={{ display: "flex", flexDirection: "column", gap: 1 }}>
              {row(
                ck,
                0,
                <>
                  {chevron(co)}
                  <span style={{ flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", color: c.status === "deprecated" ? "var(--rq-mute)" : undefined }}>{c.name}</span>
                  <span className="rq-row-end">
                    {c.version && <span className="rq-mono" style={{ fontSize: 11, color: "var(--rq-mute)" }}>{c.version}</span>}
                    <span onClick={(e) => e.stopPropagation()}>
                      <Menu align="start" trigger={<Button variant="ghost" size="sm" className="rq-reveal" icon={<MoreHorizontal />} aria-label={`${c.name} actions`} style={{ width: 22, height: 22 }} />} items={collectionItems(c)} onSelect={(k) => onCollection(c, k)} />
                    </span>
                  </span>
                </>,
                () => toggle(ck, ci === 0),
              )}
              {co &&
                c.folders.map((f, fi) => {
                  const fk = ck + "/" + f.name;
                  const fo = flat || isOpen(fk, ci === 0 && fi === 0);
                  const depth = flat ? 1 : 2;
                  return (
                    <div key={fk} style={{ display: "flex", flexDirection: "column", gap: 1 }}>
                      {!flat &&
                        row(
                          fk,
                          1,
                          <>
                            {chevron(fo)}
                            <span style={{ flex: 1 }}>{f.name || "Requests"}</span>
                            {!fo && <span style={{ fontSize: 11.5, color: "var(--rq-mute)" }}>{f.requests.length}</span>}
                          </>,
                          () => toggle(fk, ci === 0 && fi === 0),
                        )}
                      {fo &&
                        f.requests.map((r) => {
                          const sel = isSelected?.(r, c) ?? false;
                          return row(
                            r.id,
                            depth,
                            <>
                              <span className="rq-mono" style={{ width: 34, flexShrink: 0, fontSize: 10.5, fontWeight: 500, letterSpacing: 0.2, color: methodColor[r.method] ?? "var(--rq-mute)" }}>{r.method === "DELETE" ? "DEL" : r.method}</span>
                              <span style={{ flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{r.name}</span>
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
        {!cols.isLoading && tree.length === 0 && <div style={{ padding: 10, color: "var(--rq-mute)", fontSize: 12.5 }}>{filter ? "Nothing matches." : "No collections yet. Use + to add a request, a collection or an import."}</div>}
      </div>
      <NewCollectionModal open={creating} onClose={() => setCreating(false)} />
      <ImportModal open={!!importing} collectionId={importing?.collectionId} onClose={() => setImporting(null)} />
      <RenameCollectionModal collection={renaming} onClose={() => setRenaming(null)} />
    </aside>
  );
}

// WorkspaceLayout: a page beside the app sidebar (the sidebar is AppLayout's).
export function WorkspaceLayout() {
  return (
    <Layout.Content style={{ overflow: "auto" }}>
      <Outlet />
    </Layout.Content>
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
