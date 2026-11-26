import { Alert, Button, Card, Skeleton, theme, Typography } from "antd";
import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Logo } from "../shell/Header";

type Provider = { id: string; name: string; type: string };
type Providers = { org: string; providers: Provider[] };

/** Small, brand-neutral marks so each button is recognizable at a glance. */
function ProviderMark({ p }: { p: Provider }) {
  const letter = p.name.trim()[0]?.toUpperCase() ?? "?";
  const colors: Record<string, string> = { google: "#4285F4", microsoft: "#00A4EF", github: "#24292F", gitlab: "#FC6D26" };
  return (
    <span
      aria-hidden
      style={{
        width: 22,
        height: 22,
        borderRadius: 6,
        display: "inline-grid",
        placeItems: "center",
        fontSize: 12,
        fontWeight: 700,
        color: "#fff",
        background: colors[p.id] ?? "#4F46E5",
      }}
    >
      {letter}
    </span>
  );
}

export default function SignIn() {
  const { token } = theme.useToken();
  const [params] = useSearchParams();
  const [data, setData] = useState<Providers | null>(null);
  const [error, setError] = useState(params.get("error") ?? "");
  const [going, setGoing] = useState("");

  useEffect(() => {
    fetch("/auth/providers")
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`${r.status}`))))
      .then((d: Providers) => setData(d))
      .catch(() => setError("Couldn't load the sign-in options. Try again in a moment."));
  }, []);

  const go = (p: Provider) => {
    setGoing(p.id);
    const redirect = params.get("redirect") ?? "/apis";
    location.assign(`/auth/login?provider=${encodeURIComponent(p.id)}&redirect=${encodeURIComponent(redirect)}`);
  };

  return (
    <div style={{ minHeight: "100vh", display: "grid", placeItems: "center", background: token.colorBgLayout, padding: 16 }}>
      <div style={{ width: 400, maxWidth: "100%", display: "flex", flexDirection: "column", alignItems: "center", gap: 24 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <Logo size={30} />
          <Typography.Text strong style={{ fontSize: 22, letterSpacing: -0.3 }}>
            Speklo
          </Typography.Text>
        </div>
        <Card style={{ width: "100%" }} styles={{ body: { padding: 32 } }}>
          <Typography.Title level={3} style={{ marginTop: 0, marginBottom: 6, fontWeight: 600 }}>
            Sign in
          </Typography.Title>
          <Typography.Paragraph type="secondary">{data ? `to ${data.org}` : " "}</Typography.Paragraph>
          {error && <Alert type="error" showIcon message={error} closable onClose={() => setError("")} style={{ marginBottom: 16 }} />}
          {!data && !error && <Skeleton active paragraph={{ rows: 2 }} title={false} />}
          <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
            {data?.providers.map((p) => (
              <Button key={p.id} size="large" block icon={<ProviderMark p={p} />} loading={going === p.id} disabled={!!going && going !== p.id} onClick={() => go(p)}>
                Continue with {p.name}
              </Button>
            ))}
          </div>
          {data && data.providers.length === 0 && <Alert type="warning" showIcon message="No sign-in options are set up on this server yet. Ask your admin." />}
        </Card>
        <Typography.Text type="secondary" style={{ fontSize: 13 }}>
          Trouble signing in? Ask your workspace admin.
        </Typography.Text>
      </div>
    </div>
  );
}
