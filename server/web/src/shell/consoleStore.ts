// Session log for the Console panel: every request sent from this window, plus script output.
// Kept in memory only (like Postman's console); it clears when the app reloads.
import { useSyncExternalStore } from "react";
import type { SendResult } from "../api";

export type ConsoleEntry = {
  id: number;
  at: number; // ms since epoch
  name: string; // request name
  env: string;
  result?: SendResult; // absent when Requara itself refused the send
  error?: string; // Requara-side failure (validation, auth, server)
  method: string;
  url: string;
};

const MAX = 500;
let entries: ConsoleEntry[] = [];
let nextId = 1;
const listeners = new Set<() => void>();
const emit = () => listeners.forEach((l) => l());

export function logSend(e: Omit<ConsoleEntry, "id" | "at">) {
  entries = [...entries, { ...e, id: nextId++, at: Date.now() }].slice(-MAX);
  emit();
}

export function clearConsole() {
  entries = [];
  emit();
}

export function useConsoleEntries() {
  return useSyncExternalStore(
    (l) => (listeners.add(l), () => void listeners.delete(l)),
    () => entries,
  );
}

/** Problems worth a badge on the status bar: failed sends, script errors, failed tests. */
export function hasProblem(e: ConsoleEntry) {
  const r = e.result;
  return !!e.error || !r || r.status === 0 || r.status >= 400 || !!r.scriptError || (r.tests ?? []).some((t) => !t.pass);
}
