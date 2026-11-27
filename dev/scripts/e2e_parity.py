#!/usr/bin/env python3
"""End-to-end test of the Postman-parity features on a team workspace:
OAuth 2.0 authorization code with PKCE (and refresh), request proxies,
pm.sendRequest, and separate variable scopes saved where they live.

It runs a fake identity provider, echo API and HTTP proxy on 127.0.0.1, so
the server needs REQUARA_ALLOW_PRIVATE_NETWORK=true and the "email" sign-in
provider (with Mailpit).

    python3 dev/scripts/e2e_parity.py [http://localhost:8080] [http://localhost:8025]"""
import base64, hashlib, http.server, json, os, secrets, sys, threading, time, urllib.parse

sys.path.insert(0, os.path.dirname(__file__))
src = open(os.path.join(os.path.dirname(__file__), "e2e_admin.py")).read().split("# ---------------------------------------------------------------------------")[0]
ns = {"__file__": __file__}
exec(src, ns)
if len(sys.argv) > 2:
    ns["MAILPIT"] = sys.argv[2]
Browser, email_signin, ok, fail, BACKEND = ns["Browser"], ns["email_signin"], ns["ok"], ns["fail"], ns["BACKEND"]

# --- a fake identity provider, echo API and HTTP proxy --------------------
codes, tokens = {}, {}


class Fake(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def send(self, status, body, headers=None):
        data = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        for k, v in (headers or {}).items():
            self.send_header(k, v)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path.startswith("http://"):  # we're the proxy
            return self.send(200, {"proxied": self.path})
        u = urllib.parse.urlparse(self.path)
        q = dict(urllib.parse.parse_qsl(u.query))
        if u.path == "/authorize":
            code = secrets.token_hex(8)
            codes[code] = q
            loc = q["redirect_uri"] + "?" + urllib.parse.urlencode({"code": code, "state": q["state"]})
            self.send_response(302)
            self.send_header("Location", loc)
            self.end_headers()
            return
        if u.path == "/me":
            auth = self.headers.get("Authorization", "")
            return self.send(200 if auth.removeprefix("Bearer ") in tokens else 401, {"auth": auth})
        self.send(200, {"path": u.path, "query": q, "headers": {k.lower(): v for k, v in self.headers.items()}})

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        form = dict(urllib.parse.parse_qsl(self.rfile.read(n).decode()))
        if self.path == "/token":
            if form.get("grant_type") == "authorization_code":
                asked = codes.pop(form.get("code"), None)
                if not asked:
                    return self.send(400, {"error": "invalid_grant", "error_description": "unknown code"})
                challenge = base64.urlsafe_b64encode(hashlib.sha256(form.get("code_verifier", "").encode()).digest()).rstrip(b"=").decode()
                if challenge != asked.get("code_challenge") or form.get("redirect_uri") != asked["redirect_uri"] or form.get("client_id") != "spa-client":
                    return self.send(400, {"error": "invalid_grant", "error_description": "PKCE or redirect mismatch"})
                at = "at-" + secrets.token_hex(4)
                tokens[at] = 1
                return self.send(200, {"access_token": at, "refresh_token": "rt-1", "expires_in": 3600, "token_type": "Bearer"})
            if form.get("grant_type") == "refresh_token" and form.get("refresh_token") == "rt-1":
                at = "refreshed-" + secrets.token_hex(4)
                tokens[at] = 1
                return self.send(200, {"access_token": at, "expires_in": 3600})
            return self.send(400, {"error": "unsupported_grant_type"})
        if self.path == "/sub-token":
            return self.send(200, {"token": "sub-" + form.get("who", "?")})
        self.send(404, {})


srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Fake)
threading.Thread(target=srv.serve_forever, daemon=True).start()
FAKE = f"http://127.0.0.1:{srv.server_address[1]}"

# --- sign in, fresh workspace ---------------------------------------------
b = Browser()
email = f"parity-{secrets.token_hex(3)}@example.com"
(res, _), _ = email_signin(b, email)
if res[0] not in (302, 303):
    fail(f"email sign-in: {res[0]} {res[2][:200]!r}")
_, ws = b.api("POST", "/api/workspaces", {"name": f"Parity e2e {int(time.time()) % 10000}"}, expect=201)
H = {"X-Workspace": ws["id"]}
api = lambda m, p, body=None, expect=None: b.api(m, p, body, headers=H, expect=expect)
ok(f"signed in as {email}, workspace {ws['name']}")

_, col = api("POST", "/api/collections", {"name": "Parity"}, expect=201)
api("PUT", f"/api/collections/{col['id']}/settings", {"name": "Parity", "variables": [
    {"key": "fake", "value": FAKE, "enabled": True}, {"key": "cid", "value": "spa-client", "enabled": True},
    {"key": "who", "value": "collection", "enabled": True}]}, expect=200)

print("OAuth 2.0 authorization code with PKCE")
auth = {"type": "oauth2", "grantType": "authorization_code", "authUrl": "{{fake}}/authorize", "tokenUrl": "{{fake}}/token",
        "clientId": "{{cid}}", "scope": "openid orders", "pkce": "S256"}
verifier, state = secrets.token_urlsafe(48), secrets.token_urlsafe(24)
redirect = BACKEND + "/oauth2/callback.html"
base = {"auth": auth, "collectionId": col["id"], "redirectUri": redirect, "codeVerifier": verifier}
_, out = api("POST", "/api/oauth2/authorize", {**base, "state": state}, expect=200)
q = dict(urllib.parse.parse_qsl(urllib.parse.urlparse(out["url"]).query))
assert out["url"].startswith(FAKE + "/authorize?") and q["client_id"] == "spa-client" and q["code_challenge_method"] == "S256" and q["state"] == state, out
st, h, _ = b.raw("GET", out["url"])
cb = urllib.parse.urlparse(h["Location"])
got = dict(urllib.parse.parse_qsl(cb.query))
assert st == 302 and h["Location"].startswith(redirect) and got["state"] == state, (st, h)
ok("authorize URL has variables filled, PKCE S256 and state; the provider redirects to /oauth2/callback.html")
st, _, page = b.raw("GET", h["Location"])
assert st == 200 and b"requara-oauth2" in page, (st, page[:200])
ok("the callback page is served")
_, ts = api("POST", "/api/oauth2/token", {**base, "code": got["code"]}, expect=200)
assert ts["accessToken"].startswith("at-") and ts["refreshToken"] == "rt-1" and ts["expiresAt"] > time.time() + 3000, ts
ok("code + verifier exchanged for access and refresh tokens")
st, bad = api("POST", "/api/oauth2/token", {**base, "code": got["code"]})
assert st == 502 and "unknown code" in bad["error"], (st, bad)
ok("a used code fails with the provider's reason")

_, r = api("POST", "/api/requests", {"name": "Me", "method": "GET", "url": "{{fake}}/me", "collectionId": col["id"],
                                     "auth": {**auth, "token": ts["accessToken"], "refreshToken": ts["refreshToken"], "expiresAt": ts["expiresAt"]}}, expect=201)
_, sent = api("POST", f"/api/requests/{r['id']}/send", {}, expect=200)
assert sent["result"]["status"] == 200 and ts["accessToken"] not in json.dumps(sent["result"]["requestHeaders"]), sent["result"]
ok("the signed-in token is sent (and redacted in what's stored)")
api("PUT", f"/api/requests/{r['id']}", {"name": "Me", "method": "GET", "url": "{{fake}}/me", "collectionId": col["id"],
                                        "auth": {**auth, "token": ts["accessToken"], "refreshToken": "rt-1", "expiresAt": int(time.time()) - 60}}, expect=200)
_, sent = api("POST", f"/api/requests/{r['id']}/send", {}, expect=200)
assert sent["result"]["status"] == 200 and "refreshed-" in sent["result"]["body"], sent["result"]
ok("an expired token is refreshed with the refresh token")

print("proxy")
_, r = api("POST", "/api/requests", {"name": "Via proxy", "method": "GET", "url": "http://api.not-real.test/orders?x=1", "collectionId": col["id"],
                                     "settings": {"proxy": "{{fake}}"}}, expect=201)
_, sent = api("POST", f"/api/requests/{r['id']}/send", {}, expect=200)
assert json.loads(sent["result"]["body"]) == {"proxied": "http://api.not-real.test/orders?x=1"}, sent["result"]
ok("a request's own proxy is used")

print("pm.sendRequest and variable scopes")
_, env = api("POST", "/api/environments", {"name": "Staging"}, expect=201)
api("PUT", f"/api/environments/{env['id']}/variables", [{"key": "who", "value": "environment", "kind": "plain", "enabled": True}], expect=200)
_, envs = api("GET", "/api/environments", expect=200)
assert not any(e["isGlobal"] for e in envs["items"]), envs
_, r = api("POST", "/api/requests", {"name": "Init", "method": "GET", "url": "{{fake}}/init", "collectionId": col["id"],
                                     "postScript": 'pm.globals.set("g", "global-1")'}, expect=201)
api("POST", f"/api/requests/{r['id']}/send", {"environmentId": env["id"]}, expect=200)
_, envs = api("GET", "/api/environments", expect=200)
glob = next(e for e in envs["items"] if e["isGlobal"])
ok("the first pm.globals.set makes the workspace's Globals environment")
pre = """
pm.sendRequest({ url: pm.variables.get("fake") + "/sub-token", method: "POST",
  body: { mode: "urlencoded", urlencoded: [{ key: "who", value: pm.variables.get("who") }] } },
  (err, res) => pm.variables.set("tok", res.json().token));
pm.environment.set("orderId", "o-42");
"""
post = """
pm.test("scopes", () => {
  pm.expect(pm.variables.get("who")).to.equal("environment");
  pm.expect(pm.collectionVariables.get("who")).to.equal("collection");
  pm.expect(pm.globals.get("g")).to.equal("global-1");
});
pm.globals.set("lastOrder", pm.environment.get("orderId"));
pm.collectionVariables.set("seen", "yes");
pm.environment.set("status", String(pm.response.code));
"""
_, r = api("POST", "/api/requests", {"name": "Scripts", "method": "GET", "url": "{{fake}}/orders/{{orderId}}", "collectionId": col["id"],
                                     "headers": [{"key": "Authorization", "value": "Bearer {{tok}}", "enabled": True}],
                                     "preScript": pre, "postScript": post}, expect=201)
_, sent = api("POST", f"/api/requests/{r['id']}/send", {"environmentId": env["id"]}, expect=200)
res = sent["result"]
echo = json.loads(res["body"])
assert not res.get("scriptError") and not res.get("error"), res
assert echo["path"] == "/orders/o-42" and echo["headers"]["authorization"] == "Bearer sub-environment", echo
assert [t["pass"] for t in res["tests"]] == [True], res["tests"]
ok("pre-request script's pm.sendRequest and pm.environment.set feed the same request")
_, gv = api("GET", f"/api/environments/{glob['id']}/variables", expect=200)
_, ev = api("GET", f"/api/environments/{env['id']}/variables", expect=200)
_, cs = api("GET", f"/api/collections/{col['id']}/settings", expect=200)
gkeys = {v["key"]: v["value"] for v in gv}
ekeys = {v["key"]: v["value"] for v in ev}
ckeys = {v["key"]: v["value"] for v in cs["variables"]}
assert gkeys.get("lastOrder") == "o-42" and "lastOrder" not in ekeys, (gkeys, ekeys)
assert ckeys.get("seen") == "yes" and "seen" not in ekeys, ckeys
assert ekeys.get("orderId") == "o-42" and ekeys.get("status") == "200", ekeys
assert "tok" not in ekeys and "tok" not in gkeys and "tok" not in ckeys, "pm.variables.set is for this send only"
ok("globals, collection and environment changes saved where each lives; local ones not kept")
print("export as files (for Git) and import them back")
st, h, zipped = b.raw("GET", f"{BACKEND}/api/collections/{col['id']}/export?format=files", headers={"X-Requested-With": "requara", **H})
assert st == 200 and h["Content-Type"] == "application/zip" and "parity.zip" in h["Content-Disposition"], (st, h)
import io, zipfile
names = sorted(zipfile.ZipFile(io.BytesIO(zipped)).namelist())
assert names[0] == "parity/001-me.yaml" and "parity/collection.yaml" in names and len(names) == 5, names
me = zipfile.ZipFile(io.BytesIO(zipped)).read("parity/001-me.yaml").decode()
assert "refreshToken" not in me and ts["accessToken"] not in me, me
ok(f"zip of {len(names)} YAML files; signed-in tokens left out")
_, imp = api("POST", "/api/collections/import", {"format": "files", "data": base64.b64encode(zipped).decode()}, expect=201)
assert imp["name"] == "Parity" and imp["requests"] == 4, imp
_, cs2 = api("GET", f"/api/collections/{imp['id']}/settings", expect=200)
assert {v["key"] for v in cs2["variables"]} >= {"fake", "cid", "who", "seen"}, cs2
ok("imported back as a new collection with its variables")
print("all parity checks passed")
