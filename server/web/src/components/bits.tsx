import { theme, Typography } from "../ui/kit";
import type { CSSProperties, ReactNode } from "react";
import { methodColor, shortMethod, statusTone } from "../lib";

export const mono: CSSProperties = { fontFamily: "'Geist Mono Variable', ui-monospace, Menlo, monospace" };

export function Method({ method, size = 11, width = 34 }: { method: string; size?: number; width?: number | "auto" }) {
  return (
    <span style={{ ...mono, fontSize: size, fontWeight: 700, color: methodColor[method] ?? "#8C8C8C", width, display: "inline-block", flexShrink: 0 }}>
      {shortMethod(method)}
    </span>
  );
}

export function StatusTag({ status, text }: { status: number; text?: string }) {
  // A dot and the status in its colour: quieter than a boxed badge.
  const tone = statusTone(status);
  const color = tone === "success" ? "#4ADE80" : tone === "warning" ? "#FBBF24" : tone === "error" ? "#F87171" : "#A8A39A";
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 6, color, fontWeight: 500, fontVariantNumeric: "tabular-nums" }}>
      <Dot color={color} size={7} />
      {status === 0 ? "Error" : `${status}${text ? " " + text : ""}`}
    </span>
  );
}

export function Dot({ color, size = 6 }: { color: string; size?: number }) {
  return <span style={{ width: size, height: size, borderRadius: size, background: color, display: "inline-block", flexShrink: 0 }} />;
}

export function PageHeader({ title, subtitle, extra }: { title: string; subtitle?: ReactNode; extra?: ReactNode }) {
  return (
    <div style={{ display: "flex", alignItems: "flex-end", gap: 12, marginBottom: 20 }}>
      <div style={{ flex: 1, minWidth: 0 }}>
        <Typography.Title level={3} style={{ margin: 0, fontWeight: 600 }}>
          {title}
        </Typography.Title>
        {subtitle && <Typography.Text type="secondary">{subtitle}</Typography.Text>}
      </div>
      {extra && <div style={{ display: "flex", gap: 8 }}>{extra}</div>}
    </div>
  );
}

export function Kbd({ children }: { children: ReactNode }) {
  const { token } = theme.useToken();
  return (
    <span
      style={{
        fontSize: 12,
        color: token.colorTextTertiary,
        background: token.colorFillTertiary,
        border: `1px solid ${token.colorSplit}`,
        borderRadius: 4,
        padding: "0 6px",
        lineHeight: "20px",
      }}
    >
      {children}
    </span>
  );
}

export function Panel({ children, style }: { children: ReactNode; style?: CSSProperties }) {
  const { token } = theme.useToken();
  return (
    <div style={{ background: token.colorBgContainer, border: `1px solid ${token.colorBorderSecondary}`, borderRadius: 8, ...style }}>{children}</div>
  );
}
