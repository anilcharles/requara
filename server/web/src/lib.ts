import dayjs from "dayjs";
import relativeTime from "dayjs/plugin/relativeTime";

dayjs.extend(relativeTime);

// Method colours: Ant Design's green/gold/blue/purple/red, 7th shade.
export const methodColor: Record<string, string> = {
  GET: "#389E0D",
  POST: "#D46B08",
  PUT: "#0958D9",
  PATCH: "#531DAB",
  DELETE: "#CF1322",
  HEAD: "#8C8C8C",
  OPTIONS: "#8C8C8C",
};

export const shortMethod = (m: string) => (m === "DELETE" ? "DEL" : m === "OPTIONS" ? "OPT" : m);

export function timeAgo(iso: string | null | undefined): string {
  if (!iso) return "—";
  const d = dayjs(iso);
  if (dayjs().diff(d, "day") >= 7) return d.format("MMM D");
  if (dayjs().diff(d, "day") >= 1) return d.isSame(dayjs().subtract(1, "day"), "day") ? "Yesterday" : d.fromNow();
  return d.fromNow();
}

export function bytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(2)} KB`;
  return `${(n / 1024 / 1024).toFixed(2)} MB`;
}

export function compactNumber(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(n >= 10_000 ? 0 : 1)}K`;
  return String(n);
}

export function prettyBody(body: string): { text: string; language: string } {
  const t = body.trim();
  if (t.startsWith("{") || t.startsWith("[")) {
    try {
      return { text: JSON.stringify(JSON.parse(t), null, 2), language: "json" };
    } catch {
      /* fall through */
    }
  }
  if (t.startsWith("<")) return { text: body, language: "html" };
  return { text: body, language: "plaintext" };
}

export function statusTone(status: number): "success" | "warning" | "error" | "default" {
  if (status === 0) return "error";
  if (status < 300) return "success";
  if (status < 400) return "default";
  if (status < 500 && status !== 404) return "warning";
  return "error";
}

export const pluralize = (n: number, one: string, many = one + "s") => `${n} ${n === 1 ? one : many}`;
