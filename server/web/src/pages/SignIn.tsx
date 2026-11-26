import { Card, theme, Typography } from "antd";
import { useSearchParams } from "react-router-dom";
import SignInOptions, { useProviders } from "../components/SignInOptions";
import { Logo } from "../shell/Header";

export default function SignIn() {
  const { token } = theme.useToken();
  const [params] = useSearchParams();
  const { data } = useProviders();
  return (
    <div style={{ minHeight: "100vh", display: "grid", placeItems: "center", background: token.colorBgLayout, padding: 16 }}>
      <div style={{ width: 400, maxWidth: "100%", display: "flex", flexDirection: "column", alignItems: "center", gap: 24 }}>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <Logo size={30} />
          <Typography.Text strong style={{ fontSize: 22, letterSpacing: -0.3 }}>
            Requara
          </Typography.Text>
        </div>
        <Card style={{ width: "100%" }} styles={{ body: { padding: 32 } }}>
          <Typography.Title level={3} style={{ marginTop: 0, marginBottom: 6, fontWeight: 600 }}>
            Sign in
          </Typography.Title>
          <Typography.Paragraph type="secondary">{data ? `to ${data.org}` : " "}</Typography.Paragraph>
          <SignInOptions redirect={params.get("redirect") ?? "/apis"} error={params.get("error") ?? ""} />
        </Card>
        <Typography.Link href="/" style={{ fontSize: 13 }}>
          Continue without signing in (My workspace)
        </Typography.Link>
      </div>
    </div>
  );
}
