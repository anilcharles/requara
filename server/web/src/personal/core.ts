// The personal workspace's engine: the server's Go code compiled to
// WebAssembly (server/api/cmd/requara-wasm), run in a Web Worker so the page
// stays responsive. It downloads once (about 16 MB) and is then cached.
let worker: Worker | null = null;
let nextId = 1;
const pending = new Map<number, { resolve: (v: unknown) => void; reject: (e: unknown) => void }>();

export class CoreError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

function start(): Worker {
  if (worker) return worker;
  worker = new Worker("/core/worker.js");
  worker.onmessage = (e: MessageEvent<{ id: number; ok?: unknown; err?: { status: number; message: string } }>) => {
    const p = pending.get(e.data.id);
    if (!p) return;
    pending.delete(e.data.id);
    if (e.data.err) p.reject(new CoreError(e.data.err.status, e.data.err.message));
    else p.resolve(e.data.ok);
  };
  worker.onerror = (e) => {
    const err = new CoreError(500, "Requara's engine couldn't start in this browser: " + (e.message || "unknown error"));
    pending.forEach((p) => p.reject(err));
    pending.clear();
    worker = null;
  };
  return worker;
}

/** Calls an engine operation; see cmd/requara-wasm for the list. */
export function core<T = unknown>(op: string, input: unknown): Promise<T> {
  const w = start();
  const id = nextId++;
  return new Promise<T>((resolve, reject) => {
    pending.set(id, { resolve: resolve as (v: unknown) => void, reject });
    w.postMessage({ id, op, input });
  });
}

/** Starts loading the engine in the background (first use is then quick). */
export const warmCore = () => void start();

/** Personal mock servers live at this address; only Requara's own sends reach them. */
export const MOCK_BASE = "https://mock.requara.local";
