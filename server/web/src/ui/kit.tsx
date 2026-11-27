/* eslint-disable @typescript-eslint/no-explicit-any */
// Requara's replacements for every Ant Design component the app used, with
// the same names and the props the app passes, drawn by us (ui.css) on
// Radix. Screens import from here instead of "antd".
import * as Dialog from "@radix-ui/react-dialog";
import * as Popover from "@radix-ui/react-popover";
import dayjs, { type Dayjs } from "dayjs";
import { Check, ChevronDown, Copy, Eye, EyeOff, Inbox, Info, Loader2, Search as SearchIcon, TriangleAlert, X, XCircle, CheckCircle2 } from "lucide-react";
import type React from "react";
import {
  Children,
  cloneElement,
  createContext,
  forwardRef,
  isValidElement,
  useContext,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type CSSProperties,
  type ReactElement,
  type ReactNode,
} from "react";
import { Button as KitButton, Menu as KitMenu, Tabs as KitTabs, Tooltip as KitTooltip, type MenuItem } from "./index";

const cx = (...c: (string | false | null | undefined)[]) => c.filter(Boolean).join(" ");
const px = (v: number | string | undefined) => (typeof v === "number" ? `${v}px` : v);

// ===== theme: design tokens as CSS variables =====

const TOKEN = {
  colorText: "var(--rq-text)",
  colorTextSecondary: "var(--rq-dim)",
  colorTextTertiary: "var(--rq-mute)",
  colorTextQuaternary: "var(--rq-faint)",
  colorTextPlaceholder: "var(--rq-mute)",
  colorSplit: "var(--rq-line)",
  colorBorder: "var(--rq-line2)",
  colorBorderSecondary: "var(--rq-line)",
  colorBgContainer: "var(--rq-canvas)",
  colorBgLayout: "var(--rq-frame)",
  colorBgElevated: "var(--rq-popover)",
  colorFillQuaternary: "var(--rq-raise)",
  colorFillTertiary: "var(--rq-hover)",
  colorFillSecondary: "var(--rq-hover)",
  colorPrimary: "var(--rq-accent)",
  colorPrimaryBg: "var(--rq-accent-soft)",
  colorPrimaryText: "var(--rq-var)",
  colorError: "var(--rq-err)",
  colorErrorText: "var(--rq-err)",
  colorErrorBg: "color-mix(in srgb, var(--rq-err) 14%, transparent)",
  colorWarning: "var(--rq-warn)",
  colorWarningText: "var(--rq-warn)",
  colorWarningBg: "color-mix(in srgb, var(--rq-warn) 14%, transparent)",
  colorSuccess: "var(--rq-ok)",
  colorSuccessText: "var(--rq-ok)",
  colorLink: "var(--rq-var)",
  controlItemBgActive: "var(--rq-raise)",
  fontFamily: "inherit",
  borderRadius: 7,
};
export const theme = { useToken: () => ({ token: TOKEN }) };
export const ConfigProvider = ({ children }: { children: ReactNode; [k: string]: any }) => <>{children}</>;

// ===== toasts and confirm dialogs (App.useApp, message) =====

type Toast = { id: number; kind: "success" | "error" | "info" | "warning"; text: ReactNode };
let toasts: Toast[] = [];
let toastId = 0;
const toastSubs = new Set<() => void>();
const emit = () => toastSubs.forEach((f) => f());
function addToast(kind: Toast["kind"], text: ReactNode, seconds = 3) {
  const id = ++toastId;
  toasts = [...toasts, { id, kind, text }];
  emit();
  setTimeout(() => {
    toasts = toasts.filter((t) => t.id !== id);
    emit();
  }, (kind === "error" ? seconds + 2 : seconds) * 1000);
}
export const message = {
  success: (t: ReactNode) => addToast("success", t),
  error: (t: ReactNode) => addToast("error", t),
  info: (t: ReactNode) => addToast("info", t),
  warning: (t: ReactNode) => addToast("warning", t),
};

type ConfirmOpts = { title?: ReactNode; content?: ReactNode; okText?: ReactNode; cancelText?: ReactNode; okButtonProps?: { danger?: boolean }; onOk?: () => unknown; onCancel?: () => void };
let confirms: (ConfirmOpts & { id: number })[] = [];
const confirmSubs = new Set<() => void>();
const modalApi = {
  confirm: (o: ConfirmOpts) => {
    confirms = [...confirms, { ...o, id: ++toastId }];
    confirmSubs.forEach((f) => f());
  },
};

function Toasts() {
  const list = useSyncExternalStore(
    (f) => (toastSubs.add(f), () => toastSubs.delete(f)),
    () => toasts,
  );
  const icon = { success: <CheckCircle2 size={15} color="var(--rq-ok)" />, error: <XCircle size={15} color="var(--rq-err)" />, info: <Info size={15} color="var(--rq-var)" />, warning: <TriangleAlert size={15} color="var(--rq-warn)" /> };
  return (
    <div className="rq-toasts" role="status" aria-live="polite">
      {list.map((t) => (
        <div key={t.id} className="rq-toast">
          {icon[t.kind]}
          <span>{t.text}</span>
        </div>
      ))}
    </div>
  );
}

function Confirms() {
  const list = useSyncExternalStore(
    (f) => (confirmSubs.add(f), () => confirmSubs.delete(f)),
    () => confirms,
  );
  const [busy, setBusy] = useState(false);
  const c = list[0];
  const close = () => {
    confirms = confirms.slice(1);
    confirmSubs.forEach((f) => f());
  };
  return (
    <Modal
      open={!!c}
      title={c?.title}
      okText={c?.okText ?? "OK"}
      cancelText={c?.cancelText}
      okButtonProps={c?.okButtonProps}
      confirmLoading={busy}
      width={420}
      onCancel={() => {
        c?.onCancel?.();
        close();
      }}
      onOk={async () => {
        setBusy(true);
        try {
          await c?.onOk?.();
          close();
        } finally {
          setBusy(false);
        }
      }}
    >
      {c?.content && <div style={{ color: "var(--rq-dim)" }}>{c.content}</div>}
    </Modal>
  );
}

function AppRoot({ children }: { children?: ReactNode; [k: string]: any }) {
  return (
    <>
      {children}
      <Toasts />
      <Confirms />
    </>
  );
}
export const App = Object.assign(AppRoot, { useApp: () => ({ message, modal: modalApi, notification: message }) });

// ===== Button =====

type BtnProps = {
  type?: "primary" | "default" | "text" | "link" | "dashed";
  danger?: boolean;
  icon?: ReactNode;
  loading?: boolean;
  disabled?: boolean;
  size?: "small" | "middle" | "large";
  block?: boolean;
  htmlType?: "button" | "submit" | "reset";
  href?: string;
  target?: string;
  children?: ReactNode;
  [k: string]: any;
};
export const Button = forwardRef<HTMLButtonElement, BtnProps>(function Button({ type = "default", danger, size, block, htmlType, href, target, style, className, ...rest }, ref) {
  const variant = type === "primary" ? "primary" : type === "text" || type === "link" ? "ghost" : "secondary";
  const css: CSSProperties = { ...(block ? { width: "100%" } : {}), ...(danger ? { color: type === "primary" ? "#fff" : "var(--rq-err)", ...(type === "primary" ? { background: "var(--rq-err)" } : {}) } : {}), ...(type === "link" ? { color: "var(--rq-var)", padding: 0, height: "auto" } : {}), ...style };
  if (href) {
    return (
      <a href={href} target={target} className={cx("rq-btn", `rq-btn--${variant}`, size === "small" && "rq-btn--sm", size === "large" && "rq-btn--lg", className)} style={css}>
        {rest.icon}
        {rest.children}
      </a>
    );
  }
  return <KitButton ref={ref} type={htmlType ?? "button"} variant={variant} size={size === "small" ? "sm" : size === "large" ? "lg" : "md"} className={className} style={css} {...rest} />;
});

// ===== Typography =====

function CopyButton({ text }: { text: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      aria-label="Copy"
      className="rq-copy"
      onClick={(e) => {
        e.stopPropagation();
        navigator.clipboard.writeText(text);
        setDone(true);
        setTimeout(() => setDone(false), 1200);
      }}
    >
      {done ? <Check size={13} /> : <Copy size={13} />}
    </button>
  );
}
const toneColor = (t?: string) => (t === "secondary" ? "var(--rq-mute)" : t === "danger" ? "var(--rq-err)" : t === "warning" ? "var(--rq-warn)" : t === "success" ? "var(--rq-ok)" : undefined);
const textOf = (n: ReactNode): string => (typeof n === "string" || typeof n === "number" ? String(n) : Array.isArray(n) ? n.map(textOf).join("") : isValidElement(n) ? textOf((n.props as any).children) : "");
type TextProps = { type?: string; strong?: boolean; code?: boolean; keyboard?: boolean; copyable?: boolean | { text?: string }; ellipsis?: boolean | object; style?: CSSProperties; children?: ReactNode; [k: string]: any };
function Text({ type, strong, code, keyboard, copyable, ellipsis, style, children, className, ...rest }: TextProps) {
  const copyText = typeof copyable === "object" && copyable?.text ? copyable.text : textOf(children);
  return (
    <span
      className={cx(code && "rq-code", keyboard && "rq-kbd", className)}
      style={{ color: toneColor(type), fontWeight: strong ? 600 : undefined, ...(ellipsis ? { overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", display: "inline-block", maxWidth: "100%", verticalAlign: "bottom" } : {}), ...style }}
      {...rest}
    >
      {children}
      {copyable && <CopyButton text={copyText} />}
    </span>
  );
}
function Title({ level = 1, style, children, type, ...rest }: { level?: 1 | 2 | 3 | 4 | 5; [k: string]: any }) {
  const sizes = { 1: 24, 2: 20, 3: 17, 4: 15, 5: 13.5 } as const;
  const Tag = `h${level}` as "h1";
  return (
    <Tag style={{ margin: "0 0 8px", fontSize: sizes[level], fontWeight: 600, letterSpacing: level <= 2 ? -0.3 : -0.1, lineHeight: 1.3, color: toneColor(type) ?? "var(--rq-text)", ...style }} {...rest}>
      {children}
    </Tag>
  );
}
function Paragraph({ type, style, children, copyable, ...rest }: TextProps) {
  return (
    <p style={{ margin: "0 0 12px", lineHeight: 1.55, color: toneColor(type) ?? "var(--rq-text)", ...style }} {...rest}>
      {children}
      {copyable && <CopyButton text={typeof copyable === "object" && copyable?.text ? copyable.text : textOf(children)} />}
    </p>
  );
}
function Link({ style, children, ...rest }: { [k: string]: any }) {
  return (
    <a style={{ color: "var(--rq-var)", cursor: "pointer", textDecoration: "none", ...style }} {...rest}>
      {children}
    </a>
  );
}
export const Typography = { Text, Title, Paragraph, Link };

// ===== Layout =====

const Div = ({ style, children, className, ...rest }: { [k: string]: any }) => (
  <div className={className} style={style} {...rest}>
    {children}
  </div>
);
export const Layout = Object.assign(({ style, ...rest }: any) => <Div style={{ display: "flex", flexDirection: "column", minHeight: 0, ...style }} {...rest} />, {
  Header: ({ style, ...rest }: any) => <Div style={{ display: "flex", alignItems: "center", height: 48, padding: "0 16px", ...style }} {...rest} />,
  Content: ({ style, ...rest }: any) => <Div style={{ flex: 1, minWidth: 0, minHeight: 0, ...style }} {...rest} />,
  Sider: ({ width = 240, style, ...rest }: any) => <Div style={{ width, flexShrink: 0, ...style }} {...rest} />,
});

// ===== Space, Row, Col, Divider =====

const one = (s: any) => (typeof s === "number" ? s : s === "small" ? 8 : s === "large" ? 24 : 12);
const gap = (s: any) => (Array.isArray(s) ? `${one(s[1])}px ${one(s[0])}px` : one(s));
export const Space = Object.assign(
  function Space({ direction, size, wrap, align, style, children, split, ...rest }: Omit<React.HTMLAttributes<HTMLDivElement>, "align"> & { direction?: "vertical" | "horizontal"; size?: number | string | (number | string)[]; wrap?: boolean; align?: CSSProperties["alignItems"]; split?: ReactNode }) {
    const items = Children.toArray(children);
    return (
      <div
        style={{ display: direction === "vertical" ? "flex" : "inline-flex", flexDirection: direction === "vertical" ? "column" : "row", gap: gap(size), flexWrap: wrap ? "wrap" : undefined, alignItems: (align as any) === "start" ? "flex-start" : (align as any) === "end" ? "flex-end" : (align ?? (direction === "vertical" ? undefined : "center")), ...style }}
        {...rest}
      >
        {split ? items.flatMap((c, i) => (i ? [<span key={`s${i}`}>{split}</span>, c] : [c])) : items}
      </div>
    );
  },
  {
    Compact: ({ style, children, block, size }: { [k: string]: any }) => (
      <div className={cx("rq-compact", size === "large" && "rq-compact--lg")} style={{ display: block ? "flex" : "inline-flex", ...style }}>
        {children}
      </div>
    ),
  },
);
export const Row = ({ gutter, style, align, justify, children, wrap = true }: { [k: string]: any }) => {
  const [gx, gy] = Array.isArray(gutter) ? gutter : [gutter ?? 0, gutter ?? 0];
  return <div style={{ display: "flex", flexWrap: wrap ? "wrap" : "nowrap", gap: `${gy}px ${gx}px`, alignItems: align === "middle" ? "center" : align, justifyContent: justify, ...style }}>{children}</div>;
};
export const Col = ({ span, xs, md, lg, flex, style, children }: { [k: string]: any }) => {
  const n = lg ?? md ?? span ?? xs ?? 24;
  return <div style={{ flex: flex ?? `1 1 calc(${(n / 24) * 100}% - 24px)`, minWidth: n >= 24 ? "100%" : 220, ...style }}>{children}</div>;
};
export const Divider = ({ type, style, children }: { type?: "vertical"; style?: CSSProperties; children?: ReactNode; plain?: boolean; orientation?: string }) =>
  children ? (
    <div style={{ display: "flex", alignItems: "center", gap: 12, margin: "16px 0", color: "var(--rq-mute)", fontSize: 12, ...style }}>
      <span style={{ flex: 1, height: 1, background: "var(--rq-line)" }} />
      {children}
      <span style={{ flex: 1, height: 1, background: "var(--rq-line)" }} />
    </div>
  ) : type === "vertical" ? <span style={{ display: "inline-block", width: 1, height: "1em", background: "var(--rq-line2)", verticalAlign: "middle", margin: "0 8px", ...style }} /> : <div style={{ height: 1, background: "var(--rq-line)", margin: "16px 0", ...style }} />;

// ===== Inputs =====

export type InputRef = { focus: () => void; blur: () => void; input: HTMLInputElement | null };
type InputProps = Omit<React.InputHTMLAttributes<HTMLInputElement>, "size" | "prefix"> & { prefix?: ReactNode; suffix?: ReactNode; allowClear?: boolean; variant?: string; size?: string; addonBefore?: ReactNode; addonAfter?: ReactNode; onPressEnter?: (e: React.KeyboardEvent<HTMLInputElement>) => void; status?: string; visibilityToggle?: boolean };
const InputBase = forwardRef<InputRef, InputProps>(function Input({ prefix, suffix, allowClear, variant, size, addonBefore, addonAfter, onPressEnter, status, style, className, onKeyDown, value, onChange, ...rest }, ref) {
  const el = useRef<HTMLInputElement>(null);
  useImperativeHandle(ref, () => ({ focus: () => el.current?.focus(), blur: () => el.current?.blur(), input: el.current }));
  const clear = allowClear && value ? (
    <button type="button" aria-label="Clear" className="rq-clear" onClick={() => onChange?.({ target: { value: "" } } as React.ChangeEvent<HTMLInputElement>)}>
      <X size={12} />
    </button>
  ) : null;
  const field = (
    <span className={cx("rq-affix", variant === "borderless" && "rq-affix--bare", variant === "filled" && "rq-affix--filled", size === "large" && "rq-affix--lg", size === "small" && "rq-affix--sm", status === "error" && "rq-affix--err", className)} style={addonBefore || addonAfter ? { flex: 1 } : style}>
      {prefix && <span className="rq-affix-side">{prefix}</span>}
      <input
        ref={el}
        spellCheck={false}
        value={value ?? ""}
        onChange={onChange}
        onKeyDown={(e) => {
          if (e.key === "Enter") onPressEnter?.(e);
          onKeyDown?.(e);
        }}
        {...rest}
      />
      {clear}
      {suffix && <span className="rq-affix-side">{suffix}</span>}
    </span>
  );
  if (!addonBefore && !addonAfter) return field;
  return (
    <span className="rq-addon-wrap" style={style}>
      {addonBefore && <span className="rq-addon">{addonBefore}</span>}
      {field}
      {addonAfter && <span className="rq-addon">{addonAfter}</span>}
    </span>
  );
});
const Password = forwardRef<InputRef, InputProps>(function Password({ visibilityToggle = true, ...p }, ref) {
  const [show, setShow] = useState(false);
  return (
    <InputBase
      ref={ref}
      type={show ? "text" : "password"}
      autoComplete="off"
      suffix={
        visibilityToggle ? (
          <button type="button" className="rq-clear" aria-label={show ? "Hide" : "Show"} onClick={() => setShow(!show)}>
            {show ? <EyeOff size={13} /> : <Eye size={13} />}
          </button>
        ) : undefined
      }
      {...p}
    />
  );
});
function TextArea({ rows = 3, autoSize, style, className, variant, value, showCount, maxLength, ...rest }: Omit<React.TextareaHTMLAttributes<HTMLTextAreaElement>, "value"> & { value?: string; autoSize?: boolean | { minRows?: number; maxRows?: number }; variant?: string; showCount?: boolean }) {
  const min = typeof autoSize === "object" ? autoSize.minRows : undefined;
  const area = <textarea className={cx("rq-textarea", variant === "borderless" && "rq-textarea--bare", className)} rows={min ?? rows} spellCheck={false} value={value ?? ""} maxLength={maxLength} style={style} {...rest} />;
  if (!showCount) return area;
  return (
    <div>
      {area}
      <div style={{ textAlign: "right", fontSize: 11.5, color: "var(--rq-mute)", marginTop: 4 }}>
        {(value ?? "").length}
        {maxLength ? ` / ${maxLength}` : ""}
      </div>
    </div>
  );
}
function SearchInput({ onSearch, ...rest }: InputProps & { onSearch?: (v: string) => void }) {
  const [v, setV] = useState("");
  return (
    <InputBase
      prefix={<SearchIcon size={13} />}
      value={rest.value ?? v}
      onChange={(e) => {
        setV(e.target.value);
        rest.onChange?.(e);
        if (!e.target.value) onSearch?.("");
      }}
      onPressEnter={(e) => onSearch?.((e.target as HTMLInputElement).value)}
      {...rest}
    />
  );
}
export const Input = Object.assign(InputBase, { Password, TextArea, Search: SearchInput });

export function InputNumber({ value, onChange, min, max, step = 1, addonAfter, placeholder, style, disabled }: { value?: number | null; onChange?: (v: number | null) => void; min?: number; max?: number; step?: number; addonAfter?: ReactNode; placeholder?: string; style?: CSSProperties; disabled?: boolean }) {
  return (
    <InputBase
      type="number"
      value={value ?? ""}
      min={min}
      max={max}
      step={step}
      placeholder={placeholder}
      disabled={disabled}
      addonAfter={addonAfter}
      style={style}
      onChange={(e) => {
        const t = e.target.value;
        if (t === "") return onChange?.(null);
        let n = Number(t);
        if (Number.isNaN(n)) return;
        if (max !== undefined) n = Math.min(max, n);
        onChange?.(n);
      }}
      onBlur={() => {
        if (min !== undefined && typeof value === "number" && value < min) onChange?.(min);
      }}
    />
  );
}

// ===== Select, Segmented, Radio, Checkbox, Switch =====

type Opt = { value: any; label?: ReactNode; disabled?: boolean };
export function Select({ value, onChange, options = [], placeholder, disabled, style, size, variant, loading, className, allowClear }: { value?: any; onChange?: (value: any, option?: any) => void; options?: { value: any; label?: ReactNode; disabled?: boolean }[]; placeholder?: ReactNode; disabled?: boolean; style?: CSSProperties; size?: string; variant?: string; loading?: boolean; className?: string; status?: string; allowClear?: boolean; showSearch?: boolean; optionFilterProp?: string }) {
  const opts = options as Opt[];
  const cur = opts.find((o) => o.value === value);
  return (
    <KitMenu
      trigger={
        <button
          type="button"
          disabled={disabled}
          className={cx("rq-select", size === "large" && "rq-select--lg", size === "small" && "rq-select--sm", variant === "borderless" && "rq-select--bare", className)}
          style={style}
        >
          <span style={{ flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", textAlign: "left", color: cur ? undefined : "var(--rq-mute)" }}>{cur ? (cur.label ?? String(cur.value)) : placeholder}</span>
          {loading ? <Loader2 size={12} className="rq-spin" /> : <ChevronDown size={12} color="var(--rq-mute)" />}
        </button>
      }
      items={[
        ...(allowClear && cur ? ([{ key: "__clear", label: <span style={{ color: "var(--rq-mute)" }}>None</span> }, { type: "separator", key: "__s" }] as MenuItem[]) : []),
        ...opts.map((o, i) => ({ key: String(i), label: o.label ?? String(o.value), disabled: o.disabled, end: o.value === value ? <Check size={13} /> : undefined })),
      ]}
      onSelect={(k) => {
        if (k === "__clear") return onChange?.(undefined);
        const o = opts[Number(k)];
        onChange?.(o.value, o);
      }}
    />
  );
}

export function Segmented({ value, onChange, options, disabled, size, block, style }: { value?: any; onChange?: (v: any) => void; options: any[]; disabled?: boolean; size?: string; block?: boolean; style?: CSSProperties }) {
  const items = (options as any[]).map((o) => (typeof o === "object" ? { key: String(o.value), label: o.label ?? String(o.value), raw: o.value } : { key: String(o), label: o, raw: o }));
  return (
    <div className="rq-segmented" style={{ display: block ? "flex" : "inline-flex", opacity: disabled ? 0.5 : 1, pointerEvents: disabled ? "none" : undefined, ...style }}>
      <KitTabs size={size === "large" ? undefined : "sm"} label="Options" value={String(value)} onChange={(k) => onChange?.(items.find((i) => i.key === k)!.raw)} items={items.map(({ key, label }) => ({ key, label }))} />
    </div>
  );
}

const RadioCtx = createContext<{ value: any; onChange: (v: any) => void } | null>(null);
export const Radio = {
  Group: ({ value, onChange, children, style }: { value?: any; onChange?: (e: { target: { value: any } }) => void; children?: ReactNode; style?: CSSProperties; optionType?: string; buttonStyle?: string }) => (
    <RadioCtx.Provider value={{ value, onChange: (v) => onChange?.({ target: { value: v } }) }}>
      <div className="rq-segmented" style={{ display: "inline-flex", gap: 2, ...style }}>
        {children}
      </div>
    </RadioCtx.Provider>
  ),
  Button: function RadioButton({ value, children, style }: { [k: string]: any }) {
    const ctx = useContext(RadioCtx);
    return (
      <button type="button" role="radio" aria-checked={ctx?.value === value} className="rq-tab" aria-selected={ctx?.value === value} style={{ height: 24, fontSize: 12, ...style }} onClick={() => ctx?.onChange(value)}>
        {children}
      </button>
    );
  },
};

const CheckGroupCtx = createContext<{ value: any[]; toggle: (v: any) => void } | null>(null);
function CheckboxBase({ checked, onChange, disabled, value, children, style }: { checked?: boolean; onChange?: (e: { target: { checked: boolean } }) => void; disabled?: boolean; value?: any; children?: ReactNode; style?: CSSProperties }) {
  const group = useContext(CheckGroupCtx);
  const on = group ? group.value.includes(value) : !!checked;
  return (
    <label className="rq-checkline" style={{ opacity: disabled ? 0.5 : 1, ...style }}>
      <input type="checkbox" className="rq-check" checked={on} disabled={disabled} onChange={(e) => (group ? group.toggle(value) : onChange?.({ target: { checked: e.target.checked } }))} />
      {children && <span>{children}</span>}
    </label>
  );
}
export const Checkbox = Object.assign(CheckboxBase, {
  Group: ({ value = [], onChange, children, style }: { value?: any[]; onChange?: (v: any[]) => void; children?: ReactNode; style?: CSSProperties }) => (
    <CheckGroupCtx.Provider value={{ value, toggle: (v) => onChange?.(value.includes(v) ? value.filter((x: any) => x !== v) : [...value, v]) }}>
      <div style={style}>{children}</div>
    </CheckGroupCtx.Provider>
  ),
});

export function Switch({ checked, onChange, disabled, size, ...rest }: { checked?: boolean; onChange?: (checked: boolean) => void; disabled?: boolean; size?: string; "aria-label"?: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={!!checked}
      disabled={disabled}
      className={cx("rq-switch", size === "small" && "rq-switch--sm")}
      onClick={() => onChange?.(!checked)}
      aria-label={rest["aria-label"]}
    >
      <span />
    </button>
  );
}

// ===== Feedback: Alert, Tag, Badge, Spin, Skeleton, Empty, Result, Progress, Statistic =====

const alertTone = { success: "var(--rq-ok)", info: "var(--rq-var)", warning: "var(--rq-warn)", error: "var(--rq-err)" } as const;
export function Alert({ type = "info", message: title, description, action, showIcon, onClose, closable, banner, style, onClick }: { [k: string]: any }) {
  const c = alertTone[type as keyof typeof alertTone] ?? alertTone.info;
  const Icon = type === "error" ? XCircle : type === "success" ? CheckCircle2 : type === "warning" ? TriangleAlert : Info;
  return (
    <div className={cx("rq-alert", banner && "rq-alert--banner")} style={{ ["--rq-tone" as any]: c, ...style }} onClick={onClick} role={type === "error" ? "alert" : undefined}>
      {(showIcon || banner) && <Icon size={15} color={c} style={{ flexShrink: 0, marginTop: 1 }} />}
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontWeight: description ? 500 : 400 }}>{title}</div>
        {description && <div style={{ color: "var(--rq-dim)", marginTop: 3 }}>{description}</div>}
      </div>
      {action}
      {(onClose || closable) && (
        <button type="button" className="rq-clear" aria-label="Close" onClick={onClose}>
          <X size={13} />
        </button>
      )}
    </div>
  );
}

const TAG: Record<string, string> = {
  green: "#4ADE80", success: "#4ADE80", lime: "#A3E635",
  red: "#F07A7A", error: "#F07A7A", volcano: "#F2885B", magenta: "#F07AB3",
  orange: "#F2B55B", gold: "#E5C04B", warning: "#E5A54B",
  blue: "#7EB6FF", processing: "#7EB6FF", geekblue: "#8FA2FF", cyan: "#5CD6C0",
  purple: "#B39DFF", default: "",
};
export function Tag({ color, children, style, title }: { [k: string]: any }) {
  const c = color ? (TAG[color] ?? color) : "";
  return (
    <span className="rq-tag" title={title} style={c ? { color: c, background: `color-mix(in srgb, ${c} 13%, transparent)`, ...style } : style}>
      {children}
    </span>
  );
}
export function Badge({ count, style, children }: { [k: string]: any }) {
  const b = count ? <span className="rq-badge" style={style}>{count}</span> : null;
  return children ? (
    <span style={{ position: "relative", display: "inline-flex" }}>
      {children}
      {b && <span style={{ position: "absolute", top: -6, right: -8 }}>{b}</span>}
    </span>
  ) : (
    b
  );
}

export function Spin({ style, size, spinning = true, children }: { [k: string]: any }) {
  const s = size === "small" ? 14 : size === "large" ? 24 : 18;
  const spinner = <Loader2 size={s} className="rq-spin" color="var(--rq-mute)" style={{ display: "block", margin: children ? undefined : "auto", ...style }} />;
  if (!children) return spinner;
  return (
    <div style={{ position: "relative" }}>
      {children}
      {spinning && <div style={{ position: "absolute", inset: 0, display: "grid", placeItems: "center" }}>{spinner}</div>}
    </div>
  );
}
export function Skeleton({ paragraph, title = true }: { [k: string]: any }) {
  const rows = typeof paragraph === "object" ? (paragraph.rows ?? 3) : 3;
  const widths: any[] = typeof paragraph === "object" && Array.isArray(paragraph.width) ? paragraph.width : [];
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      {title && <div className="rq-skel" style={{ width: "36%", height: 14 }} />}
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="rq-skel" style={{ width: widths[i] ?? (i === rows - 1 ? "60%" : "100%") }} />
      ))}
    </div>
  );
}
const SIMPLE = Symbol("simple");
export const Empty = Object.assign(
  function Empty({ description, style, image }: { [k: string]: any }) {
    return (
      <div style={{ padding: "32px 0", textAlign: "center", color: "var(--rq-mute)", ...style }}>
        {image !== SIMPLE && <Inbox size={22} strokeWidth={1.5} style={{ marginBottom: 8, opacity: 0.7 }} />}
        <div>{description ?? "Nothing here yet"}</div>
      </div>
    );
  },
  { PRESENTED_IMAGE_SIMPLE: SIMPLE },
);
export function Result({ icon, title, subTitle, extra, status }: { [k: string]: any }) {
  const ic = icon ?? (status === "error" ? <XCircle size={36} color="var(--rq-err)" /> : status === "success" ? <CheckCircle2 size={36} color="var(--rq-ok)" /> : <Info size={36} color="var(--rq-mute)" />);
  return (
    <div style={{ textAlign: "center", maxWidth: 520, margin: "0 auto", padding: "40px 24px" }}>
      <div style={{ fontSize: 36, marginBottom: 16, display: "grid", placeItems: "center" }}>{ic}</div>
      <div style={{ fontSize: 18, fontWeight: 600, marginBottom: 6 }}>{title}</div>
      {subTitle && <div style={{ color: "var(--rq-dim)", lineHeight: 1.55 }}>{subTitle}</div>}
      {extra && <div style={{ marginTop: 20, display: "flex", gap: 8, justifyContent: "center", flexWrap: "wrap" }}>{extra}</div>}
    </div>
  );
}
export function Progress({ percent = 0, status, size }: { [k: string]: any }) {
  const c = status === "exception" ? "var(--rq-err)" : status === "success" || percent >= 100 ? "var(--rq-ok)" : "var(--rq-accent)";
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 10, width: "100%" }}>
      <div style={{ flex: 1, height: size === "small" ? 4 : 6, borderRadius: 3, background: "var(--rq-raise)", overflow: "hidden" }}>
        <div style={{ width: `${Math.min(100, percent)}%`, height: "100%", background: c, borderRadius: 3 }} />
      </div>
      <span style={{ fontSize: 12, color: "var(--rq-mute)", fontVariantNumeric: "tabular-nums", minWidth: 32, textAlign: "right" }}>{Math.round(percent)}%</span>
    </div>
  );
}
export function Statistic({ title, value, suffix, valueStyle, loading }: { [k: string]: any }) {
  return (
    <div>
      <div style={{ fontSize: 12, color: "var(--rq-mute)", marginBottom: 4 }}>{title}</div>
      <div style={{ fontSize: 24, fontWeight: 600, letterSpacing: -0.4, fontVariantNumeric: "tabular-nums", ...valueStyle }}>
        {loading ? "–" : value}
        {suffix && <span style={{ fontSize: 13, fontWeight: 400, color: "var(--rq-mute)", marginLeft: 4 }}>{suffix}</span>}
      </div>
    </div>
  );
}

// ===== Surfaces: Card, Descriptions, Avatar, Tooltip, Breadcrumb =====

export function Card({ title, extra, size, style, styles, children, onClick, hoverable, loading, type }: { [k: string]: any }) {
  const pad = size === "small" ? 14 : 20;
  return (
    <section className={cx("rq-card", (hoverable || onClick) && "rq-card--hover", type === "inner" && "rq-card--inner")} style={style} onClick={onClick}>
      {(title || extra) && (
        <header style={{ display: "flex", alignItems: "center", gap: 12, minHeight: 44, padding: `0 ${pad}px`, borderBottom: "1px solid var(--rq-line)", ...styles?.header }}>
          <div style={{ flex: 1, fontWeight: 500, minWidth: 0 }}>{title}</div>
          {extra}
        </header>
      )}
      <div style={{ padding: pad, ...styles?.body }}>{loading ? <Skeleton /> : children}</div>
    </section>
  );
}
const DescItem = ({ children }: { label?: ReactNode; children?: ReactNode; [k: string]: any }) => <>{children}</>;
export const Descriptions = Object.assign(
  function Descriptions({ column = 1, labelStyle, children, style }: { [k: string]: any }) {
    const items = Children.toArray(children).filter(isValidElement) as ReactElement<any>[];
    return (
      <div style={{ display: "grid", gridTemplateColumns: `repeat(${column}, minmax(0, 1fr))`, gap: "0 24px", ...style }}>
        {items.map((it, i) => (
          <div key={i} style={{ display: "grid", gridTemplateColumns: "minmax(110px, 34%) 1fr", gap: 12, padding: "8px 0", borderBottom: "1px solid var(--rq-line)" }}>
            <span style={{ color: "var(--rq-mute)", ...labelStyle }}>{it.props.label}</span>
            <span style={{ minWidth: 0, wordBreak: "break-word" }}>{it.props.children}</span>
          </div>
        ))}
      </div>
    );
  },
  { Item: DescItem },
);
export function Avatar({ size = 28, style, children, icon }: { [k: string]: any }) {
  const s = typeof size === "number" ? size : size === "large" ? 36 : size === "small" ? 22 : 28;
  return <span style={{ width: s, height: s, borderRadius: "50%", background: "#20304A", color: "#9CC3FF", display: "inline-grid", placeItems: "center", fontSize: s * 0.42, fontWeight: 600, flexShrink: 0, ...style }}>{icon ?? children}</span>;
}
export function Tooltip({ title, children, placement }: { [k: string]: any }) {
  if (!title) return children;
  const side = placement?.startsWith("top") ? "top" : placement?.startsWith("left") ? "left" : placement?.startsWith("right") ? "right" : "bottom";
  return (
    <KitTooltip content={title} side={side}>
      {isValidElement(children) ? children : <span>{children}</span>}
    </KitTooltip>
  );
}
export function Breadcrumb({ items = [], style }: { [k: string]: any }) {
  return (
    <nav aria-label="Breadcrumb" style={{ display: "flex", alignItems: "center", gap: 6, color: "var(--rq-mute)", ...style }}>
      {(items as any[]).map((it, i) => (
        <span key={i} style={{ display: "inline-flex", alignItems: "center", gap: 6, color: i === items.length - 1 ? "var(--rq-text)" : undefined }}>
          {i > 0 && <span style={{ opacity: 0.6 }}>/</span>}
          {it.title}
        </span>
      ))}
    </nav>
  );
}

// ===== Menus: Dropdown, Tabs =====

export function Dropdown({ menu, children, placement, disabled }: { menu?: { items?: any[]; onClick?: (info: { key: string; domEvent: { stopPropagation: () => void } }) => void }; children?: ReactNode; placement?: string; trigger?: string[]; disabled?: boolean }) {
  if (disabled) return <>{children}</>;
  const toItems = (list: any[]): MenuItem[] =>
    list.flatMap((it: any, i: number): MenuItem[] =>
      !it ? [] : it.type === "divider" ? [{ type: "separator", key: `d${i}` }] : it.type === "group" ? [{ type: "label", key: `g${i}`, label: it.label }, ...toItems(it.children ?? [])] : [{ key: String(it.key), label: it.label, icon: it.icon, danger: it.danger, disabled: it.disabled, end: it.extra }],
    );
  return <KitMenu align={placement?.includes("Right") ? "end" : "start"} trigger={isValidElement(children) ? children : <span>{children}</span>} items={toItems(menu?.items ?? [])} onSelect={(key) => menu?.onClick?.({ key, domEvent: { stopPropagation() {} } })} />;
}
export function Tabs({ items = [], activeKey, defaultActiveKey, onChange, size, tabBarExtraContent, style }: { items?: { key: string; label: ReactNode; children?: ReactNode }[]; activeKey?: string; defaultActiveKey?: string; onChange?: (k: string) => void; size?: string; tabBarExtraContent?: ReactNode; style?: CSSProperties }) {
  const [own, setOwn] = useState(defaultActiveKey ?? items[0]?.key);
  const active = activeKey ?? own;
  const cur = (items as any[]).find((i) => i.key === active);
  return (
    <div style={style}>
      <div style={{ marginBottom: 14 }}>
        <KitTabs
          label="Sections"
          size={size === "small" ? "sm" : undefined}
          value={active}
          onChange={(k) => {
            setOwn(k);
            onChange?.(k);
          }}
          items={(items as any[]).map((i) => ({ key: i.key, label: i.label }))}
          end={tabBarExtraContent}
        />
      </div>
      {cur?.children}
    </div>
  );
}

// ===== Overlays: Modal, Drawer, Popconfirm =====

export function Modal({ open, title, onCancel, onOk, okText = "OK", cancelText = "Cancel", okButtonProps, cancelButtonProps, confirmLoading, width = 520, footer, children, afterOpenChange, styles, closable = true, style }: { open?: boolean; title?: ReactNode; onCancel?: () => void; onOk?: () => unknown; okText?: ReactNode; cancelText?: ReactNode; okButtonProps?: { disabled?: boolean; danger?: boolean; loading?: boolean }; confirmLoading?: boolean; width?: number | string; footer?: ReactNode; children?: ReactNode; afterOpenChange?: (open: boolean) => void; styles?: { body?: CSSProperties; content?: CSSProperties }; closable?: boolean; style?: CSSProperties; destroyOnHidden?: boolean; centered?: boolean; maskClosable?: boolean; cancelButtonProps?: { style?: CSSProperties } }) {
  useEffect(() => {
    afterOpenChange?.(!!open);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  return (
    <Dialog.Root open={!!open} onOpenChange={(o) => !o && onCancel?.()}>
      <Dialog.Portal>
        <Dialog.Overlay className="rq-overlay" />
        <Dialog.Content className="rq-dialog" style={{ width: `min(${px(width)}, calc(100vw - 32px))`, ...(style?.top !== undefined ? { top: px(style.top as any) } : {}), ...styles?.content }} aria-describedby={undefined}>
          {title || closable ? (
            <div style={{ display: "flex", alignItems: "flex-start", gap: 12, marginBottom: 14 }}>
              <Dialog.Title style={{ flex: 1, margin: 0, fontSize: 15, fontWeight: 600 }}>{title ?? ""}</Dialog.Title>
              {closable && (
                <Dialog.Close asChild>
                  <button type="button" className="rq-clear" aria-label="Close">
                    <X size={15} />
                  </button>
                </Dialog.Close>
              )}
            </div>
          ) : (
            <Dialog.Title style={{ position: "absolute", width: 1, height: 1, overflow: "hidden", clip: "rect(0 0 0 0)" }}>Dialog</Dialog.Title>
          )}
          <div style={{ ...styles?.body }}>{children}</div>
          {footer === undefined ? (
            <div style={{ display: "flex", justifyContent: "flex-end", gap: 8, marginTop: 20 }}>
              <Button onClick={onCancel} style={cancelButtonProps?.style}>
                {cancelText}
              </Button>
              {onOk && (
                <Button type="primary" danger={okButtonProps?.danger} disabled={okButtonProps?.disabled} loading={confirmLoading || okButtonProps?.loading} onClick={() => onOk()}>
                  {okText}
                </Button>
              )}
            </div>
          ) : footer === null ? null : (
            <div style={{ display: "flex", justifyContent: "flex-end", gap: 8, marginTop: 20 }}>{footer}</div>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
export function Drawer({ open, title, onClose, width = 480, children, loading, extra }: { [k: string]: any }) {
  return (
    <Dialog.Root open={!!open} onOpenChange={(o) => !o && onClose?.()}>
      <Dialog.Portal>
        <Dialog.Overlay className="rq-overlay" />
        <Dialog.Content className="rq-drawer" style={{ width: `min(${px(width)}, 100vw)` }} aria-describedby={undefined}>
          <div style={{ display: "flex", alignItems: "center", gap: 12, height: 52, padding: "0 20px", borderBottom: "1px solid var(--rq-line)", flexShrink: 0 }}>
            <Dialog.Title style={{ flex: 1, margin: 0, fontSize: 15, fontWeight: 600 }}>{title ?? ""}</Dialog.Title>
            {extra}
            <Dialog.Close asChild>
              <button type="button" className="rq-clear" aria-label="Close">
                <X size={15} />
              </button>
            </Dialog.Close>
          </div>
          <div style={{ flex: 1, overflow: "auto", padding: 20 }}>{loading ? <Skeleton paragraph={{ rows: 6 }} /> : children}</div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
export function Popconfirm({ title, description, onConfirm, okText = "OK", cancelText = "Cancel", okButtonProps, children }: { [k: string]: any }) {
  const [open, setOpen] = useState(false);
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>{children}</Popover.Trigger>
      <Popover.Portal>
        <Popover.Content className="rq-menu" side="top" sideOffset={6} style={{ padding: 12, maxWidth: 300 }}>
          <div style={{ fontWeight: 500 }}>{title}</div>
          {description && <div style={{ color: "var(--rq-dim)", marginTop: 4 }}>{description}</div>}
          <div style={{ display: "flex", justifyContent: "flex-end", gap: 6, marginTop: 12 }}>
            <Button size="small" onClick={() => setOpen(false)}>
              {cancelText}
            </Button>
            <Button
              size="small"
              type="primary"
              danger={okButtonProps?.danger}
              onClick={async () => {
                await onConfirm?.();
                setOpen(false);
              }}
            >
              {okText}
            </Button>
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

// ===== Upload =====

type UploadProps = { beforeUpload?: (file: File, list: File[]) => unknown; accept?: string; disabled?: boolean; children?: ReactNode; multiple?: boolean; showUploadList?: boolean; maxCount?: number; style?: CSSProperties; fileList?: { uid: string; name: string; status?: string }[]; onRemove?: () => void };
function UploadBase({ beforeUpload, accept, disabled, children, dragger, multiple, style, fileList, onRemove }: UploadProps & { dragger?: boolean }) {
  const input = useRef<HTMLInputElement>(null);
  const [over, setOver] = useState(false);
  const take = (files: FileList | null) => {
    for (const f of Array.from(files ?? [])) beforeUpload?.(f, [f]);
    if (input.current) input.current.value = "";
  };
  const hidden = <input ref={input} type="file" hidden accept={accept} multiple={multiple} onChange={(e) => take(e.target.files)} />;
  if (!dragger) {
    return (
      <span onClick={() => !disabled && input.current?.click()} style={{ display: "inline-flex", ...style }}>
        {hidden}
        {children}
      </span>
    );
  }
  const list = fileList?.length ? (
    <div style={{ marginTop: 8 }}>
      {fileList.map((f) => (
        <div key={f.uid} style={{ display: "flex", alignItems: "center", gap: 8, height: 28, padding: "0 8px", borderRadius: 6, background: "var(--rq-raise)", fontSize: 12.5 }}>
          <span style={{ flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{f.name}</span>
          {onRemove && (
            <button type="button" className="rq-clear" aria-label={`Remove ${f.name}`} onClick={onRemove}>
              <X size={12} />
            </button>
          )}
        </div>
      ))}
    </div>
  ) : null;
  return (
    <div style={style}>
    <div
      role="button"
      tabIndex={0}
      className={cx("rq-dropzone", over && "rq-dropzone--over")}
      aria-disabled={disabled}
      onClick={() => !disabled && input.current?.click()}
      onKeyDown={(e) => e.key === "Enter" && input.current?.click()}
      onDragOver={(e) => {
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setOver(false);
        if (!disabled) take(e.dataTransfer.files);
      }}
    >
      {hidden}
      {children}
    </div>
    {list}
    </div>
  );
}
export const Upload = Object.assign((p: UploadProps) => <UploadBase {...p} />, { Dragger: (p: UploadProps) => <UploadBase dragger {...p} /> });

// ===== DatePicker (a range of two days) =====

export const DatePicker = {
  RangePicker: function RangePicker({ value, onChange }: { value?: [Dayjs | null, Dayjs | null] | null; onChange?: (v: [Dayjs | null, Dayjs | null] | null) => void }) {
    const [a, b] = value ?? [null, null];
    const set = (i: 0 | 1, s: string) => {
      const next: [Dayjs | null, Dayjs | null] = [a, b];
      next[i] = s ? dayjs(s) : null;
      onChange?.(next[0] || next[1] ? next : null);
    };
    return (
      <span className="rq-addon-wrap">
        <input type="date" className="rq-input" style={{ width: 150, colorScheme: "inherit" }} value={a ? a.format("YYYY-MM-DD") : ""} onChange={(e) => set(0, e.target.value)} aria-label="From" />
        <span className="rq-addon">to</span>
        <input type="date" className="rq-input" style={{ width: 150, colorScheme: "inherit" }} value={b ? b.format("YYYY-MM-DD") : ""} onChange={(e) => set(1, e.target.value)} aria-label="To" />
      </span>
    );
  },
};

// ===== Table =====

export type ColumnType<T> = { title?: ReactNode; dataIndex?: string | string[]; key?: string; width?: number | string; render?: (v: any, r: T, i: number) => ReactNode | { children: ReactNode; props: { colSpan?: number } }; align?: "left" | "right" | "center"; onCell?: (r: T) => any; ellipsis?: boolean };
export type ColumnsType<T> = ColumnType<T>[];
type TableProps<T> = { columns?: ColumnsType<T>; dataSource?: readonly T[] | T[]; rowKey?: string | ((r: T) => string); loading?: boolean; locale?: { emptyText?: ReactNode }; onRow?: (r: T) => any; pagination?: false | { pageSize?: number; hideOnSinglePage?: boolean }; size?: string; showHeader?: boolean; expandable?: { expandedRowRender: (r: T) => ReactNode; rowExpandable?: (r: T) => boolean }; style?: CSSProperties };
export function Table<T = any>({ columns = [], dataSource = [], rowKey = "key", loading, locale, onRow, pagination, size, showHeader = true, expandable, style }: TableProps<T>) {
  const cols = columns;
  const pageSize = pagination === false ? Infinity : (pagination?.pageSize ?? 10);
  const [page, setPage] = useState(1);
  const [open, setOpen] = useState<Set<string>>(new Set());
  const rows = (dataSource ?? []) as any[];
  const pages = Math.max(1, Math.ceil(rows.length / pageSize));
  useEffect(() => {
    if (page > pages) setPage(1);
  }, [page, pages]);
  const shown = pageSize === Infinity ? rows : rows.slice((page - 1) * pageSize, page * pageSize);
  const keyOf = (r: any, i: number) => String(typeof rowKey === "function" ? rowKey(r) : (r?.[rowKey as string] ?? i));
  const get = (r: any, di?: string | string[]) => (di === undefined ? undefined : Array.isArray(di) ? di.reduce((o, k) => o?.[k], r) : r?.[di]);
  const hide = pagination !== false && pagination?.hideOnSinglePage && pages <= 1;
  return (
    <div style={{ overflowX: "auto", ...style }}>
      <table className={cx("rq-table", size === "small" && "rq-table--sm")}>
        {showHeader && (
          <thead>
            <tr>
              {expandable && <th style={{ width: 28 }} />}
              {cols.map((c, i) => (
                <th key={c.key ?? i} style={{ width: c.width, minWidth: c.width ?? 220, textAlign: c.align }}>
                  {c.title}
                </th>
              ))}
            </tr>
          </thead>
        )}
        <tbody>
          {loading && rows.length === 0 ? (
            <tr>
              <td colSpan={cols.length + (expandable ? 1 : 0)}>
                <Spin style={{ margin: "24px auto" }} />
              </td>
            </tr>
          ) : rows.length === 0 ? (
            <tr>
              <td colSpan={cols.length + (expandable ? 1 : 0)} style={{ borderBottom: 0 }}>
                {locale?.emptyText ?? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} />}
              </td>
            </tr>
          ) : (
            shown.flatMap((r, i) => {
              const k = keyOf(r, i);
              const rp = onRow?.(r) ?? {};
              const isOpen = open.has(k);
              const can = expandable && (expandable.rowExpandable ? expandable.rowExpandable(r) : true);
              const main = (
                <tr key={k} {...rp} className={cx(rp.onClick && "rq-tr-click", rp.className)}>
                  {expandable && (
                    <td style={{ width: 28 }}>
                      {can && (
                        <button
                          type="button"
                          className="rq-clear"
                          aria-label={isOpen ? "Collapse row" : "Expand row"}
                          onClick={(e) => {
                            e.stopPropagation();
                            const n = new Set(open);
                            if (isOpen) n.delete(k);
                            else n.add(k);
                            setOpen(n);
                          }}
                        >
                          <ChevronDown size={13} style={{ transform: isOpen ? undefined : "rotate(-90deg)", transition: "transform .12s" }} />
                        </button>
                      )}
                    </td>
                  )}
                  {cols.map((c, ci) => {
                    const v = get(r, c.dataIndex);
                    const cellProps = c.onCell?.(r) ?? {};
                    let out: any = c.render ? c.render(v, r, i) : v;
                    // Ant's cell merging: { children, props: { colSpan } }; colSpan 0 hides the cell.
                    let span: number | undefined;
                    if (out && typeof out === "object" && !isValidElement(out) && "props" in out && "children" in out) {
                      span = out.props?.colSpan;
                      out = out.children;
                    }
                    if (span === 0) return null;
                    return (
                      <td key={c.key ?? ci} colSpan={span} {...cellProps} style={{ textAlign: c.align, ...(c.ellipsis ? { maxWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" } : {}), ...cellProps.style }}>
                        {out as ReactNode}
                      </td>
                    );
                  })}
                </tr>
              );
              return isOpen
                ? [
                    main,
                    <tr key={k + ":x"} className="rq-tr-expanded">
                      <td colSpan={cols.length + 1}>{expandable!.expandedRowRender(r)}</td>
                    </tr>,
                  ]
                : [main];
            })
          )}
        </tbody>
      </table>
      {pagination !== false && pages > 1 && !hide && (
        <div style={{ display: "flex", justifyContent: "flex-end", alignItems: "center", gap: 8, marginTop: 12, color: "var(--rq-mute)", fontSize: 12 }}>
          <Button size="small" disabled={page <= 1} onClick={() => setPage(page - 1)}>
            Previous
          </Button>
          <span style={{ fontVariantNumeric: "tabular-nums" }}>
            {page} / {pages}
          </span>
          <Button size="small" disabled={page >= pages} onClick={() => setPage(page + 1)}>
            Next
          </Button>
        </div>
      )}
    </div>
  );
}

// ===== Form =====

type Rule = { required?: boolean; message?: string; type?: "url" | "email" };
type FormStore = {
  values: Record<string, any>;
  errors: Record<string, string>;
  rules: Record<string, Rule[]>;
  listeners: Set<() => void>;
  onFinish?: (v: any) => void;
  initial: Record<string, any>;
};
export type FormInstance = {
  submit: () => void;
  resetFields: () => void;
  setFieldsValue: (v: Record<string, any>) => void;
  getFieldsValue: () => Record<string, any>;
  getFieldValue: (n: string) => any;
  validateFields: () => Promise<Record<string, any>>;
  __store: FormStore;
};
function makeForm(): FormInstance {
  const store: FormStore = { values: {}, errors: {}, rules: {}, listeners: new Set(), initial: {} };
  const notify = () => store.listeners.forEach((f) => f());
  const validate = () => {
    const errs: Record<string, string> = {};
    for (const [name, rules] of Object.entries(store.rules)) {
      const v = store.values[name];
      for (const r of rules) {
        const empty = v === undefined || v === null || v === "" || (Array.isArray(v) && v.length === 0);
        if (r.required && empty) {
          errs[name] = r.message ?? "Required";
          break;
        }
        if (r.type === "url" && !empty) {
          try {
            new URL(String(v));
          } catch {
            errs[name] = r.message ?? "Enter a full URL";
            break;
          }
        }
        if (r.type === "email" && !empty && !/^\S+@\S+\.\S+$/.test(String(v))) {
          errs[name] = r.message ?? "Enter an email address";
          break;
        }
      }
    }
    store.errors = errs;
    notify();
    return errs;
  };
  const f: FormInstance = {
    __store: store,
    submit: () => {
      if (Object.keys(validate()).length === 0) store.onFinish?.({ ...store.values });
    },
    resetFields: () => {
      store.values = { ...store.initial };
      store.errors = {};
      notify();
    },
    setFieldsValue: (v) => {
      store.values = { ...store.values, ...v };
      notify();
    },
    getFieldsValue: () => ({ ...store.values }),
    getFieldValue: (n) => store.values[n],
    validateFields: () => {
      const errs = validate();
      return Object.keys(errs).length ? Promise.reject({ errorFields: errs }) : Promise.resolve({ ...store.values });
    },
  };
  return f;
}
const FormCtx = createContext<{ form: FormInstance; layout: string; requiredMark: boolean } | null>(null);
function FormRoot({ form, layout = "horizontal", initialValues, onFinish, requiredMark = true, style, children, className }: { form?: FormInstance; layout?: string; initialValues?: Record<string, any>; onFinish?: (values: any) => void; requiredMark?: boolean | string; style?: CSSProperties; children?: ReactNode; className?: string }) {
  const own = useMemo(makeForm, []);
  const inst: FormInstance = form ?? own;
  const first = useRef(true);
  if (first.current) {
    first.current = false;
    if (initialValues && Object.keys(inst.__store.values).length === 0) {
      inst.__store.values = { ...initialValues };
      inst.__store.initial = { ...initialValues };
    }
  }
  inst.__store.onFinish = onFinish;
  return (
    <FormCtx.Provider value={{ form: inst, layout, requiredMark: requiredMark !== false }}>
      <form
        className={className}
        style={style}
        onSubmit={(e) => {
          e.preventDefault();
          inst.submit();
        }}
        noValidate
      >
        {children}
      </form>
    </FormCtx.Provider>
  );
}
function FormItem({ name, label, rules, extra, tooltip, valuePropName = "value", children, style, help }: { [k: string]: any }) {
  const ctx = useContext(FormCtx);
  const [, force] = useState(0);
  useEffect(() => {
    if (!ctx) return;
    const f = () => force((n) => n + 1);
    ctx.form.__store.listeners.add(f);
    return () => {
      ctx.form.__store.listeners.delete(f);
    };
  }, [ctx]);
  if (ctx && name && rules) ctx.form.__store.rules[name] = rules;
  const required = (rules as Rule[] | undefined)?.some((r) => r.required);
  const err = name && ctx ? ctx.form.__store.errors[name] : undefined;
  let control = children;
  if (ctx && name && isValidElement(children)) {
    const el = children as ReactElement<any>;
    const store = ctx.form.__store;
    control = cloneElement(el, {
      [valuePropName]: store.values[name] ?? (valuePropName === "checked" ? false : el.props[valuePropName]),
      onChange: (e: any, ...more: any[]) => {
        const v = e && typeof e === "object" && "target" in e ? (valuePropName === "checked" ? e.target.checked : e.target.value) : e;
        store.values = { ...store.values, [name]: v };
        if (store.errors[name]) delete store.errors[name];
        store.listeners.forEach((f) => f());
        el.props.onChange?.(e, ...more);
      },
      status: err ? "error" : undefined,
    });
  }
  return (
    <div style={{ marginBottom: 16, ...style }}>
      {label && (
        <label style={{ display: "flex", alignItems: "center", gap: 6, marginBottom: 6, fontSize: 12.5, color: "var(--rq-dim)" }}>
          {label}
          {required && ctx?.requiredMark && <span style={{ color: "var(--rq-mute)" }}>·</span>}
          {tooltip && (
            <KitTooltip content={tooltip}>
              <Info size={12} color="var(--rq-mute)" />
            </KitTooltip>
          )}
        </label>
      )}
      {control}
      {(err || help) && <div style={{ fontSize: 12, color: err ? "var(--rq-err)" : "var(--rq-mute)", marginTop: 5 }}>{err ?? help}</div>}
      {extra && !err && <div style={{ fontSize: 12, color: "var(--rq-mute)", marginTop: 5, lineHeight: 1.5 }}>{extra}</div>}
    </div>
  );
}
export const Form = Object.assign(FormRoot, { Item: FormItem, useForm: <T = any,>() => [useMemo(makeForm, [])] as [FormInstance & { __t?: T }] });

// ===== misc =====

export const CopyIcon = Copy;
