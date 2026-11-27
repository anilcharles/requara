// Requara's own controls: buttons, pill tabs, inputs, menus and tooltips,
// drawn by us (ui.css) on top of Radix for keyboard and screen-reader
// behaviour. They replace Ant Design screen by screen.
import * as DM from "@radix-ui/react-dropdown-menu";
import * as TT from "@radix-ui/react-tooltip";
import { Loader2 } from "lucide-react";
import { forwardRef, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode } from "react";
import "./ui.css";

const cx = (...c: (string | false | null | undefined)[]) => c.filter(Boolean).join(" ");

// ----- Button -----

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "secondary" | "ghost";
  size?: "sm" | "md" | "lg";
  icon?: ReactNode;
  kbd?: string; // a shortcut hint shown after the label
  loading?: boolean;
};

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "secondary", size = "md", icon, kbd, loading, children, className, disabled, type = "button", ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      disabled={disabled || loading}
      className={cx("rq-btn", `rq-btn--${variant}`, size !== "md" && `rq-btn--${size}`, !children && "rq-btn--icon", className)}
      {...rest}
    >
      {loading ? <Loader2 className="rq-spin" /> : icon}
      {children}
      {kbd && <span className="rq-kbd-inline">{kbd}</span>}
    </button>
  );
});

/** An icon-only button; label is its tooltip and accessible name. */
export const IconButton = forwardRef<HTMLButtonElement, Omit<ButtonProps, "children"> & { label: string }>(function IconButton({ label, ...rest }, ref) {
  return (
    <Tooltip content={label}>
      <Button ref={ref} variant="ghost" aria-label={label} {...rest} />
    </Tooltip>
  );
});

export const Kbd = ({ children }: { children: ReactNode }) => <span className="rq-kbd">{children}</span>;

// ----- Tabs (pills) -----

export type TabItem<K extends string> = { key: K; label: ReactNode; count?: ReactNode; tone?: "ok" | "err" };

export function Tabs<K extends string>({
  items,
  value,
  onChange,
  label,
  size,
  end,
}: {
  items: TabItem<K>[];
  value: K;
  onChange: (k: K) => void;
  label: string; // accessible name of the tab list
  size?: "sm";
  end?: ReactNode; // shown at the far right of the row
}) {
  return (
    <div className={cx("rq-tabs", size === "sm" && "rq-tabs--sm")}>
      <div role="tablist" aria-label={label} style={{ display: "flex", gap: 2, minWidth: 0 }}>
        {items.map((t) => (
          <button
            key={t.key}
            type="button"
            role="tab"
            className="rq-tab"
            aria-selected={t.key === value}
            tabIndex={t.key === value ? 0 : -1}
            onClick={() => onChange(t.key)}
            onKeyDown={(e) => {
              const i = items.findIndex((x) => x.key === value);
              const next = e.key === "ArrowRight" ? items[(i + 1) % items.length] : e.key === "ArrowLeft" ? items[(i - 1 + items.length) % items.length] : null;
              if (next) {
                e.preventDefault();
                onChange(next.key);
                (e.currentTarget.parentElement?.querySelector(`[data-key="${next.key}"]`) as HTMLElement | null)?.focus();
              }
            }}
            data-key={t.key}
          >
            {t.label}
            {t.count !== undefined && t.count !== null && t.count !== 0 && <span className={cx("rq-count", t.tone && `rq-count--${t.tone}`)}>{t.count}</span>}
          </button>
        ))}
      </div>
      {end && <div style={{ marginLeft: "auto", display: "flex", alignItems: "center", gap: 12, minWidth: 0 }}>{end}</div>}
    </div>
  );
}

// ----- Inputs -----

export const TextInput = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement> & { bare?: boolean; mono?: boolean }>(function TextInput(
  { bare, mono, className, ...rest },
  ref,
) {
  return <input ref={ref} className={cx("rq-input", bare && "rq-input--bare", mono && "rq-mono", className)} spellCheck={false} {...rest} />;
});

// ----- Menu -----

export type MenuItem =
  | { key: string; label: ReactNode; icon?: ReactNode; end?: ReactNode; danger?: boolean; disabled?: boolean }
  | { type: "separator"; key: string }
  | { type: "label"; key: string; label: ReactNode };

export function Menu({
  trigger,
  items,
  onSelect,
  align = "start",
  side = "bottom",
}: {
  trigger: ReactNode; // one element, e.g. a Button
  items: MenuItem[];
  onSelect: (key: string) => void;
  align?: "start" | "center" | "end";
  side?: "top" | "bottom" | "left" | "right";
}) {
  return (
    <DM.Root modal={false}>
      <DM.Trigger asChild>{trigger}</DM.Trigger>
      <DM.Portal>
        <DM.Content className="rq-menu" align={align} side={side} sideOffset={6} collisionPadding={8}>
          {items.map((it) =>
            "type" in it && it.type === "separator" ? (
              <DM.Separator key={it.key} className="rq-menu-sep" />
            ) : "type" in it && it.type === "label" ? (
              <DM.Label key={it.key} className="rq-menu-label">
                {it.label}
              </DM.Label>
            ) : (
              <DM.Item
                key={it.key}
                className={cx("rq-menu-item", "danger" in it && it.danger && "rq-menu-item--danger")}
                disabled={"disabled" in it ? it.disabled : false}
                onSelect={() => onSelect(it.key)}
              >
                {"icon" in it && it.icon}
                {"label" in it && it.label}
                {"end" in it && it.end && <span className="rq-menu-end">{it.end}</span>}
              </DM.Item>
            ),
          )}
        </DM.Content>
      </DM.Portal>
    </DM.Root>
  );
}

// ----- Tooltip -----

export function TooltipProvider({ children }: { children: ReactNode }) {
  return <TT.Provider delayDuration={450} skipDelayDuration={200}>{children}</TT.Provider>;
}

export function Tooltip({ content, children, side = "bottom" }: { content: ReactNode; children: ReactNode; side?: "top" | "bottom" | "left" | "right" }) {
  return (
    <TT.Root>
      <TT.Trigger asChild>{children}</TT.Trigger>
      <TT.Portal>
        <TT.Content className="rq-tooltip" side={side} sideOffset={6}>
          {content}
        </TT.Content>
      </TT.Portal>
    </TT.Root>
  );
}

// ----- small pieces -----

export const Dot = ({ color, size = 7 }: { color: string; size?: number }) => <span className="rq-dot" style={{ background: color, width: size, height: size }} />;

const STATUS_COLOR = { ok: "var(--rq-ok)", warn: "var(--rq-warn)", err: "var(--rq-err)", none: "var(--rq-mute)" };

/** "● 200 OK" in the status's colour. */
export function Status({ status, text }: { status: number; text?: string }) {
  const tone = status === 0 ? "err" : status < 300 ? "ok" : status < 400 ? "warn" : status < 600 ? "err" : "none";
  const color = STATUS_COLOR[tone];
  return (
    <span className="rq-status" style={{ color }}>
      <Dot color={color} />
      {status === 0 ? "Error" : `${status}${text ? " " + text : ""}`}
    </span>
  );
}
