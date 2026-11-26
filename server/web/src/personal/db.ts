// The personal workspace's storage: IndexedDB in this browser. Every table
// is loaded into memory once; writes go to memory and then to IndexedDB.
const DB_NAME = "requara-personal";
const TABLES = ["apis", "versions", "requests", "environments", "variables", "history", "mocks", "mockCalls", "meta", "collections", "examples", "cookies"] as const;
export type Table = (typeof TABLES)[number];

type Row = { id: string } & Record<string, unknown>;

const mem = new Map<Table, Map<string, Row>>();
let idb: IDBDatabase | null = null;

function open(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, 2);
    req.onupgradeneeded = () => {
      for (const t of TABLES) if (!req.result.objectStoreNames.contains(t)) req.result.createObjectStore(t, { keyPath: "id" });
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
}

function loadTable(db: IDBDatabase, t: Table): Promise<void> {
  return new Promise((resolve, reject) => {
    const req = db.transaction(t).objectStore(t).getAll();
    req.onsuccess = () => {
      mem.set(t, new Map((req.result as Row[]).map((r) => [r.id, r])));
      resolve();
    };
    req.onerror = () => reject(req.error);
  });
}

let readyPromise: Promise<void> | null = null;
export function ready(): Promise<void> {
  readyPromise ??= (async () => {
    try {
      idb = await open();
      for (const t of TABLES) await loadTable(idb, t);
    } catch (e) {
      // Private browsing without IndexedDB: keep working in memory only.
      console.warn("Personal workspace isn't saved in this browser:", e);
      for (const t of TABLES) if (!mem.has(t)) mem.set(t, new Map());
    }
  })();
  return readyPromise;
}

function persist(t: Table, fn: (s: IDBObjectStore) => void) {
  if (!idb) return;
  try {
    fn(idb.transaction(t, "readwrite").objectStore(t));
  } catch (e) {
    console.error("couldn't save to the personal workspace", e);
  }
}

export const db = {
  all<T = Row>(t: Table): T[] {
    return [...(mem.get(t)?.values() ?? [])] as T[];
  },
  get<T = Row>(t: Table, id: string): T | undefined {
    return mem.get(t)?.get(id) as T | undefined;
  },
  put<T extends { id: string }>(t: Table, row: T): T {
    const copy = structuredClone(row) as unknown as Row;
    mem.get(t)!.set(row.id, copy);
    persist(t, (s) => s.put(copy));
    return row;
  },
  del(t: Table, id: string) {
    mem.get(t)?.delete(id);
    persist(t, (s) => s.delete(id));
  },
  clear(t: Table, keep?: (r: Row) => boolean) {
    for (const r of db.all(t)) if (!keep?.(r)) db.del(t, r.id);
  },
};

export const newId = () => crypto.randomUUID();
