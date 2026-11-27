import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App as AntApp, ConfigProvider, theme } from "antd";
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { get, patch, personalActive, type Environment, type Me, type Prefs } from "../api";
import { warmCore } from "../personal/core";

type PrefsCtx = { prefs: Prefs; isDark: boolean; setPrefs: (p: Partial<Prefs>) => void };
const Ctx = createContext<PrefsCtx>({ prefs: {}, isDark: false, setPrefs: () => {} });
export const usePrefs = () => useContext(Ctx);
export const PrefsContext = Ctx;

export function useMe(enabled = true) {
  return useQuery({ queryKey: ["me"], queryFn: () => get<Me>("/api/me"), enabled, staleTime: 60_000, retry: false });
}

export function useEnvironments() {
  return useQuery({
    queryKey: ["environments"],
    queryFn: () => get<{ items: Environment[]; activeId: string }>("/api/environments"),
    staleTime: 30_000,
  });
}

function useSystemDark() {
  const mq = window.matchMedia("(prefers-color-scheme: dark)");
  const [dark, setDark] = useState(mq.matches);
  useEffect(() => {
    const on = (e: MediaQueryListEvent) => setDark(e.matches);
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, [mq]);
  return dark;
}

// Applies the signed-in user's theme and density to Ant Design.
// Dark surfaces: the window chrome (header, rail, sidebar) a step darker than
// the page, and panels on top, like Postman's dark theme.
// Near-black window frame; the work canvas one step lighter.
const DARK = { chrome: "#0E0E10", page: "#0E0E10", panel: "#161618" };

export function PrefsProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient();
  const onSignIn = location.pathname.startsWith("/signin");
  const me = useMe(!onSignIn);
  useEffect(() => {
    // The personal workspace's engine downloads once; start it early.
    if (personalActive()) (window.requestIdleCallback ?? setTimeout)(() => warmCore());
  }, [me.data?.workspaceId]);

  const systemDark = useSystemDark();
  const prefs = me.data?.prefs ?? {};
  const mode = prefs.theme ?? "dark";
  const isDark = mode === "dark" || (mode === "system" && systemDark);

  const save = useMutation({
    mutationFn: (p: Partial<Prefs>) => patch<Prefs>("/api/me/preferences", p),
    onMutate: (p) => {
      qc.setQueryData<Me>(["me"], (m) => (m ? { ...m, prefs: { ...m.prefs, ...p } } : m));
      if (p.activeEnvironmentId) {
        qc.setQueryData<{ items: Environment[]; activeId: string }>(["environments"], (d) => (d ? { ...d, activeId: p.activeEnvironmentId! } : d));
      }
    },
    onSuccess: (saved) => qc.setQueryData<Me>(["me"], (m) => (m ? { ...m, prefs: saved } : m)),
  });

  const algorithms = [isDark ? theme.darkAlgorithm : theme.defaultAlgorithm];
  if (prefs.density === "compact") algorithms.push(theme.compactAlgorithm);

  useEffect(() => {
    document.documentElement.style.colorScheme = isDark ? "dark" : "light";
    document.body.style.background = isDark ? DARK.page : "#F5F5F5";
  }, [isDark]);

  return (
    <Ctx.Provider value={{ prefs, isDark, setPrefs: (p) => save.mutate(p) }}>
      <ConfigProvider
        theme={{
          algorithm: algorithms,
          token: {
            colorPrimary: "#3D8BFF", // Requara blue (also the logo's dot)
            borderRadius: 6,
            fontSize: 13,
            controlHeight: 30,
            fontFamily: "'Geist Variable', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif",
            fontFamilyCode: "\'Geist Mono Variable\', ui-monospace, Menlo, monospace",
            // One calm charcoal family instead of black and grey patches.
            ...(isDark
              ? { colorBgLayout: DARK.page, colorBgContainer: DARK.panel, colorBgElevated: "#1E1E21", colorBorder: "#2E2E33", colorBorderSecondary: "#232327", colorSplit: "#232327", colorText: "#EDEDEF", colorTextSecondary: "#A0A0A8", colorTextTertiary: "#6F6F78", colorFillTertiary: "rgba(255,255,255,0.05)", colorLink: "#7AB0FF", controlItemBgActive: "#1F1F23", controlItemBgActiveHover: "#232327" }
              : {}),
          },
          components: {
            Layout: { headerBg: isDark ? DARK.chrome : "#FFFFFF", siderBg: isDark ? DARK.chrome : "#FFFFFF", bodyBg: isDark ? DARK.page : "#F5F5F5", headerHeight: 48, headerPadding: "0 12px" },
            Tabs: { horizontalItemPadding: "10px 0", horizontalItemGutter: 24, titleFontSize: 13 },
            Button: { fontWeight: 500 },
          },
        }}
      >
        <AntApp>{children}</AntApp>
      </ConfigProvider>
    </Ctx.Provider>
  );
}
