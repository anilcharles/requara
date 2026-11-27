// OAuth 2.0 authorization code with PKCE, from the Auth tab. This browser
// makes the state and the code verifier and keeps them; the server (or, in
// My workspace, the engine) fills in variables and talks to the provider.
// The provider redirects the popup to /oauth2/callback.html, which hands the
// code back here. See server/api/internal/api/oauth2.go.
import { api, type Auth } from "../api";

export type TokenSet = { accessToken: string; refreshToken?: string; tokenType?: string; scope?: string; idToken?: string; expiresAt?: number };

/** The redirect URI to register with the identity provider. */
export const redirectUri = () => `${location.origin}/oauth2/callback.html`;

function random(n: number): string {
  // RFC 7636 unreserved characters.
  const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~";
  const bytes = crypto.getRandomValues(new Uint8Array(n));
  return Array.from(bytes, (b) => chars[b % chars.length]).join("");
}

/** Waits for the callback page to report the result for state. */
function waitForCallback(state: string, signal: AbortSignal): Promise<Record<string, string>> {
  return new Promise((resolve, reject) => {
    const channel = "BroadcastChannel" in window ? new BroadcastChannel("requara-oauth2") : null;
    const timer = window.setTimeout(() => finish(new Error("Sign-in timed out. Try again.")), 5 * 60_000);
    const onData = (data: unknown) => {
      const d = data as { source?: string; params?: Record<string, string> };
      if (d?.source !== "requara-oauth2" || d.params?.state !== state) return;
      finish(undefined, d.params);
    };
    const onMessage = (e: MessageEvent) => e.origin === location.origin && onData(e.data);
    const onAbort = () => finish(new Error("Sign-in cancelled"));
    function finish(err?: Error, params?: Record<string, string>) {
      window.clearTimeout(timer);
      window.removeEventListener("message", onMessage);
      signal.removeEventListener("abort", onAbort);
      channel?.close();
      if (err) reject(err);
      else resolve(params!);
    }
    if (channel) channel.onmessage = (e) => onData(e.data);
    window.addEventListener("message", onMessage);
    signal.addEventListener("abort", onAbort);
  });
}

/** Signs in in a popup and returns the tokens. collectionId picks whose variables apply. */
export async function getAuthorizationCodeToken(auth: Auth, collectionId: string | null | undefined, signal: AbortSignal): Promise<TokenSet> {
  // Open the popup now, while the click still counts, so it isn't blocked.
  const popup = window.open("about:blank", "requara-oauth2", "popup,width=520,height=720");
  if (!popup) throw new Error("The browser blocked the sign-in window. Allow pop-ups for this site and try again.");
  const state = random(32);
  const codeVerifier = random(64);
  const base = { auth, collectionId: collectionId ?? "", redirectUri: redirectUri(), codeVerifier };
  try {
    const { url } = await api<{ url: string }>("/api/oauth2/authorize", { method: "POST", json: { ...base, state } });
    popup.location.href = url;
    const params = await waitForCallback(state, signal);
    if (params.error) throw new Error(`Sign-in failed: ${params.error_description || params.error}`);
    return await api<TokenSet>("/api/oauth2/token", { method: "POST", json: { ...base, code: params.code } });
  } catch (e) {
    if (!popup.closed) popup.close();
    throw e;
  }
}
