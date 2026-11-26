import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App as AntApp, ConfigProvider, theme } from "antd";
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { get, patch, type Environment, type Me, type Prefs } from "../api";

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
export function PrefsProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient();
  const onSignIn = location.pathname.startsWith("/signin");
  const me = useMe(!onSignIn);
  const systemDark = useSystemDark();
  const prefs = me.data?.prefs ?? {};
  const mode = prefs.theme ?? "system";
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
    document.body.style.background = isDark ? "#000" : "#F5F5F5";
  }, [isDark]);

  return (
    <Ctx.Provider value={{ prefs, isDark, setPrefs: (p) => save.mutate(p) }}>
      <ConfigProvider
        theme={{
          algorithm: algorithms,
          token: { colorPrimary: "#1677FF", borderRadius: 6, fontFamily: "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif" },
          components: { Layout: { headerBg: isDark ? "#141414" : "#FFFFFF", siderBg: isDark ? "#141414" : "#FFFFFF", bodyBg: isDark ? "#000000" : "#F5F5F5", headerHeight: 56, headerPadding: "0 16px" } },
        }}
      >
        <AntApp>{children}</AntApp>
      </ConfigProvider>
    </Ctx.Provider>
  );
}
