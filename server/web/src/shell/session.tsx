import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App as AntApp } from "../ui/kit";
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

  useEffect(() => {
    document.documentElement.style.colorScheme = isDark ? "dark" : "light";
    document.documentElement.dataset.theme = isDark ? "dark" : "light"; // src/ui colours
    document.body.style.background = ""; // ui.css paints it from --rq-frame
    document.documentElement.dataset.density = prefs.density === "compact" ? "compact" : "comfortable";
  }, [isDark, prefs.density]);

  return (
    <Ctx.Provider value={{ prefs, isDark, setPrefs: (p) => save.mutate(p) }}>
      <AntApp>{children}</AntApp>
    </Ctx.Provider>
  );
}
