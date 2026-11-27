import { MailOutlined } from "../ui/icons";
import { Alert, Button, Card, Input, Result, Skeleton, theme, Typography } from "../ui/kit";
import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Logo } from "../shell/Header";

// Email sign-in, two steps on one page:
//   ?flow=…   enter your address; Requara emails a one-time link
//   ?token=…  the link: press Continue to sign in (a plain form POST, so mail
//             scanners that open links can't use it up)
export default function SignInEmail() {
  const { token: t } = theme.useToken();
  const [params] = useSearchParams();
  const flow = params.get("flow") ?? "";
  const linkToken = params.get("token") ?? "";
  const [info, setInfo] = useState<{ org: string; email?: string; desktop?: boolean } | null>(null);
  const [error, setError] = useState("");
  const [email, setEmail] = useState(params.get("email") ?? "");
  const [sending, setSending] = useState(false);
  const [sent, setSent] = useState<{ to: string; minutes: number } | null>(null);

  useEffect(() => {
    const q = linkToken ? `token=${encodeURIComponent(linkToken)}` : `flow=${encodeURIComponent(flow)}`;
    fetch(`/auth/email?${q}`)
      .then(async (r) => {
        const body = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(body.error ?? "This sign-in expired. Start again.");
        setInfo(body);
      })
      .catch((e: Error) => setError(e.message));
  }, [flow, linkToken]);

  const send = async () => {
    setSending(true);
    setError("");
    try {
      const r = await fetch("/auth/email/start", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-Requested-With": "requara" },
        body: JSON.stringify({ flow, email }),
      });
      const body = await r.json().catch(() => ({}));
      if (!r.ok) throw new Error(body.error ?? "Couldn't send the link");
      setSent({ to: email.trim(), minutes: body.expiresInMinutes ?? 15 });
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSending(false);
    }
  };

  let body;
  if (!info && !error) {
    body = <Skeleton active paragraph={{ rows: 2 }} title={false} />;
  } else if (linkToken) {
    body = info?.email ? (
      // A regular form post: the server signs in and redirects (to the app, or back to the desktop app).
      <form method="POST" action="/auth/email/verify">
        <input type="hidden" name="token" value={linkToken} />
        <Typography.Paragraph>
          Sign in to {info.org} as <strong>{info.email}</strong>.
        </Typography.Paragraph>
        <Button type="primary" size="large" block htmlType="submit">
          Continue
        </Button>
      </form>
    ) : (
      <Button block href="/signin">
        Back to sign in
      </Button>
    );
  } else if (sent) {
    body = (
      <Result
        icon={<MailOutlined style={{ color: t.colorPrimary }} />}
        title="Check your email"
        subTitle={`If ${sent.to} may sign in to ${info?.org ?? "this server"}, a sign-in link is on its way. It works once and expires in ${sent.minutes} minutes.${info?.desktop ? " Open it on this computer to finish signing in to the desktop app." : ""}`}
        extra={
          <Button type="link" onClick={() => setSent(null)}>
            Use a different address
          </Button>
        }
        style={{ padding: 0 }}
      />
    );
  } else if (info) {
    body = (
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (email.includes("@")) send();
        }}
      >
        <Typography.Paragraph type="secondary">We'll email you a link that signs you in.</Typography.Paragraph>
        <Input size="large" type="email" autoFocus autoComplete="email" placeholder="you@company.com" value={email} onChange={(e) => setEmail(e.target.value)} style={{ marginBottom: 12 }} />
        <Button type="primary" size="large" block htmlType="submit" loading={sending} disabled={!email.includes("@")}>
          Email me a sign-in link
        </Button>
      </form>
    );
  } else {
    body = (
      <Button block href="/signin">
        Back to sign in
      </Button>
    );
  }

  return (
    <div style={{ minHeight: "100vh", display: "grid", placeItems: "center", background: t.colorBgLayout, padding: 16 }}>
      <div style={{ width: 400, maxWidth: "100%", display: "flex", flexDirection: "column", alignItems: "center", gap: 24 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <Logo size={30} />
          <Typography.Text strong style={{ fontSize: 22, letterSpacing: -0.3 }}>
            Requara
          </Typography.Text>
        </div>
        <Card style={{ width: "100%" }} styles={{ body: { padding: 32 } }}>
          <Typography.Title level={3} style={{ marginTop: 0, marginBottom: 6, fontWeight: 600 }}>
            Sign in with email
          </Typography.Title>
          <Typography.Paragraph type="secondary">{info ? `to ${info.org}` : " "}</Typography.Paragraph>
          {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 16 }} />}
          {body}
        </Card>
        <Typography.Link href="/signin" style={{ fontSize: 13 }}>
          Other ways to sign in
        </Typography.Link>
      </div>
    </div>
  );
}
