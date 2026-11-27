import { Alert, Button, Modal, Skeleton, Typography } from "antd";
import { useEffect, useState } from "react";

export type Provider = { id: string; name: string; type: string; product?: string; productName?: string };
export type Providers = { org: string; providers: Provider[] };

let cached: Promise<Providers> | null = null;
/** The sign-in options this server offers (GET /auth/providers). */
export function loadProviders(): Promise<Providers> {
  cached ??= fetch("/auth/providers").then((r) => (r.ok ? r.json() : Promise.reject(new Error(String(r.status)))));
  cached.catch(() => (cached = null));
  return cached;
}

export function useProviders() {
  const [data, setData] = useState<Providers | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    loadProviders()
      .then(setData)
      .catch(() => setError("Couldn't load the sign-in options. Try again in a moment."));
  }, []);
  return { data, error };
}

// Each identity provider's colour and initials, so the buttons are recognizable at a glance.
const MARKS: Record<string, { bg: string; text: string }> = {
  keycloak: { bg: "#4D4D4D", text: "KC" },
  okta: { bg: "#007DC1", text: "O" },
  auth0: { bg: "#EB5424", text: "A0" },
  microsoft: { bg: "#00A4EF", text: "M" },
  google: { bg: "#4285F4", text: "G" },
  github: { bg: "#24292F", text: "GH" },
  gitlab: { bg: "#FC6D26", text: "GL" },
  authentik: { bg: "#FD4B2D", text: "ak" },
  zitadel: { bg: "#5469D4", text: "Z" },
  onelogin: { bg: "#1C1F2A", text: "1" },
  jumpcloud: { bg: "#13A8A8", text: "JC" },
  ping: { bg: "#B3282D", text: "P" },
  cognito: { bg: "#DD344C", text: "AWS" },
  adfs: { bg: "#0078D4", text: "AD" },
  dex: { bg: "#3F5AA8", text: "dex" },
  casdoor: { bg: "#443D80", text: "C" },
  fusionauth: { bg: "#F58320", text: "FA" },
  email: { bg: "#595959", text: "@" },
};

function ProviderMark({ p }: { p: Provider }) {
  const m = (p.product && MARKS[p.product]) || { bg: "#3D8BFF", text: p.name.trim()[0]?.toUpperCase() ?? "?" };
  return (
    <span
      aria-hidden
      style={{ minWidth: 22, height: 22, padding: "0 4px", borderRadius: 6, display: "inline-grid", placeItems: "center", fontSize: m.text.length > 2 ? 9 : 11, fontWeight: 700, color: "#fff", background: m.bg }}
    >
      {m.text}
    </span>
  );
}

/** "Continue with Acme SSO" says which identity provider it is when the name doesn't. */
function via(p: Provider): string {
  const product = p.productName ?? "";
  const kind = p.type === "saml" ? "SAML" : p.type === "oidc" && !["google", "microsoft", "gitlab"].includes(p.product ?? "") ? "OpenID Connect" : "";
  if (product && p.name.toLowerCase().includes(product.toLowerCase().split(" ")[0])) return "";
  return [product, kind].filter(Boolean).join(" · ");
}

/** One button per sign-in option the backend has configured. */
export default function SignInOptions({ redirect, error: initialError = "" }: { redirect: string; error?: string }) {
  const { data, error } = useProviders();
  const [shown, setShown] = useState(initialError);
  const [going, setGoing] = useState("");
  const go = (p: Provider) => {
    setGoing(p.id);
    location.assign(`/auth/login?provider=${encodeURIComponent(p.id)}&redirect=${encodeURIComponent(redirect)}`);
  };
  return (
    <>
      {(shown || error) && <Alert type="error" showIcon message={shown || error} closable onClose={() => setShown("")} style={{ marginBottom: 16 }} />}
      {!data && !error && <Skeleton active paragraph={{ rows: 2 }} title={false} />}
      <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
        {data?.providers.map((p) => (
          <Button key={p.id} size="large" block icon={<ProviderMark p={p} />} loading={going === p.id} disabled={!!going && going !== p.id} onClick={() => go(p)} style={{ height: via(p) ? 52 : undefined }}>
            <span style={{ display: "inline-flex", flexDirection: "column", alignItems: "flex-start", lineHeight: 1.2 }}>
              <span>Continue with {p.name}</span>
              {via(p) && <span style={{ fontSize: 12, opacity: 0.6 }}>{via(p)}</span>}
            </span>
          </Button>
        ))}
      </div>
      {data && data.providers.length === 0 && <Alert type="warning" showIcon message="No sign-in options are set up on this server yet. Ask your admin." />}
      {data && data.providers.length > 0 && (
        <Typography.Paragraph type="secondary" style={{ fontSize: 13, margin: "16px 0 0" }}>
          Signing in gives you {data.org}'s shared APIs, environments and workspaces.
        </Typography.Paragraph>
      )}
    </>
  );
}

/** "Sign in to your organization": the backend's sign-in options in a dialog. */
export function SignInModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { data } = useProviders();
  return (
    <Modal open={open} onCancel={onClose} footer={null} width={420} title={data?.org ? `Sign in to ${data.org}` : "Sign in to your organization"} destroyOnHidden>
      <div style={{ paddingTop: 8 }}>
        <SignInOptions redirect={location.pathname + location.search} />
      </div>
    </Modal>
  );
}
