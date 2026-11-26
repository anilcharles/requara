import { ApiOutlined, CaretDownOutlined, CaretRightOutlined, ClockCircleOutlined, CodeOutlined, FilterOutlined, PlusOutlined, SlidersOutlined } from "@ant-design/icons";
import { useQuery } from "@tanstack/react-query";
import { Button, Input, Layout, Spin, theme, Tooltip } from "antd";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Navigate, Outlet, useLocation, useNavigate } from "react-router-dom";
import { get, mocksPath, type Collection, type CollectionRequest, type Mock } from "../api";
import { Method } from "../components/bits";
import ConsoleDock from "./Console";
import Header from "./Header";
import { useMe } from "./session";

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
  return (
    <Layout style={{ height: "100vh" }}>
      <Header />
      <Layout style={{ flex: 1, minHeight: 0 }}>
        <Outlet />
      </Layout>
      <ConsoleDock />
    </Layout>
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
  const loc = useLocation();
  const cols = useCollections();
  const mocks = useQuery({ queryKey: ["mocks"], queryFn: () => get<Mock[]>(mocksPath()), staleTime: 30_000 });
  const [filter, setFilter] = useState("");
  const [open, setOpen] = useStored<Record<string, boolean>>("speklo.tree", {});

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
      className="speklo-row"
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
  const running = (mocks.data ?? []).filter((m) => m.enabled).length;
  const footer = [
    { path: "/history", icon: <ClockCircleOutlined />, label: "History" },
    { path: "/mocks", icon: <CodeOutlined />, label: "Mock servers", extra: running ? `${running} running` : "" },
    { path: "/environments", icon: <SlidersOutlined />, label: "Environments" },
  ];

  return (
    <Layout.Sider width={272} style={{ borderRight: `1px solid ${token.colorSplit}`, display: "flex", flexDirection: "column" }}>
      <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", height: 48, padding: "0 8px 0 16px" }}>
          <span style={{ fontWeight: 600 }}>Collections</span>
          <Tooltip title="New API">
            <Button type="text" icon={<PlusOutlined />} onClick={() => nav("/apis?new=1")} />
          </Tooltip>
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
              <ApiOutlined /> {filter ? "Nothing matches." : "No APIs yet."}
            </div>
          )}
        </div>
        <div style={{ borderTop: `1px solid ${token.colorSplit}`, padding: "8px 8px 12px" }}>
          {footer.map((f) =>
            row(
              f.path,
              0,
              <>
                <span style={{ color: loc.pathname.startsWith(f.path) ? token.colorPrimary : token.colorTextSecondary }}>{f.icon}</span>
                <span style={{ flex: 1 }}>{f.label}</span>
                {f.extra && <span style={{ fontSize: 12, color: token.colorTextTertiary }}>{f.extra}</span>}
              </>,
              () => nav(f.path),
              loc.pathname.startsWith(f.path),
            ),
          )}
        </div>
      </div>
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
