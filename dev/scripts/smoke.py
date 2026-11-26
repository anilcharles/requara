#!/usr/bin/env python3
"""End-to-end smoke test: signs in through Keycloak's real login form, then
exercises the Speklo API (send, secrets, keys, design, publish, mocks)."""
import http.cookiejar, json, re, sys, urllib.parse, urllib.request, html

BACKEND = "http://localhost:8080"
jar = http.cookiejar.CookieJar()

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k): return None

opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar), NoRedirect)

def raw(method, url, data=None, headers=None):
    req = urllib.request.Request(url, data=data, method=method, headers=headers or {})
    try:
        r = opener.open(req)
        return r.status, dict(r.headers), r.read()
    except urllib.error.HTTPError as e:
        return e.code, dict(e.headers), e.read()

def api(method, path, body=None, headers=None, expect=None):
    h = {"X-Requested-With": "speklo", **(headers or {})}
    data = None
    if body is not None:
        data = json.dumps(body).encode(); h["Content-Type"] = "application/json"
    st, _, out = raw(method, BACKEND + path, data, h)
    if expect and st != expect:
        sys.exit(f"FAIL {method} {path}: {st} {out[:300]!r}")
    return st, (json.loads(out) if out and out[:1] in b"[{" else out)

def ok(msg): print("  ok  " + msg)

# --- sign in through Keycloak ---
st, h, _ = raw("GET", BACKEND + "/auth/login?login_hint=alex@acme.dev&redirect=/apis")
assert st == 302 and "realms/acme" in h["Location"], (st, h)
st, h, page = raw("GET", h["Location"])
for c in jar:  # browsers treat localhost as secure; Python's cookie jar doesn't
    c.secure = False
action = html.unescape(re.search(rb'action="([^"]+)"', page).group(1).decode())
form = urllib.parse.urlencode({"username": "alex.chen", "password": "speklo-dev", "credentialId": ""}).encode()
st, h, page = raw("POST", action, form, {"Content-Type": "application/x-www-form-urlencoded"})
assert st == 302, (st, page[:300])
cb = urllib.parse.urlparse(h["Location"])            # http://localhost:5173/auth/callback?...
st, h, _ = raw("GET", BACKEND + cb.path + "?" + cb.query)
assert st == 302 and h["Location"].endswith("/apis"), (st, h)
ok("signed in via Keycloak (alex.chen)")

_, me = api("GET", "/api/me", expect=200)
assert "speklo-admin" in me["roles"] and me["canPublish"], me
ok(f"me: {me['name']} roles={me['roles']} member={me['memberRole']}")

_, apis = api("GET", "/api/apis", expect=200)
names = [a["name"] for a in apis["items"]]
assert len(names) == 6, names
pay = next(a for a in apis["items"] if a["name"] == "Payments API")
ok(f"catalog: {len(names)} APIs, counts={apis['counts']}, payments endpoints={pay['endpoints']} draftChanges={pay['hasDraftChanges']}")

_, cols = api("GET", "/api/collections", expect=200)
create = next(r for c in cols for f in c["folders"] for r in f["requests"] if r["name"] == "Create a charge")
ok(f"collections: {sum(len(f['requests']) for c in cols for f in c['folders'])} requests")

# --- send (mock server, encrypted secret, scripts) ---
_, sent = api("POST", f"/api/requests/{create['id']}/send", {}, expect=200)
res = sent["result"]
assert res["status"] == 201, res
assert all(t["pass"] for t in res["tests"]) and len(res["tests"]) == 4, res["tests"]
auth = [h for h in res["requestHeaders"] if h["key"] == "Authorization"][0]["value"]
assert "sk_live" not in auth, auth
assert json.loads(res["body"])["captured"] is False
ok(f"send: {res['status']} in {res['durationMs']}ms, tests 4/4, phases={[p['name'] for p in res['timeline']]}, auth stored as {auth!r}")

_, envs = api("GET", "/api/environments", expect=200)
prod = next(e for e in envs["items"] if e["name"] == "Production")
_, vars_ = api("GET", f"/api/environments/{prod['id']}/variables", expect=200)
sk = next(v for v in vars_ if v["key"] == "secret_key")
cid = next(v for v in vars_ if v["key"] == "charge_id")
assert sk["value"] == "" and sk["hasValue"] and cid["kind"] == "script" and cid["value"].startswith("ch_")
ok(f"environment: secret_key hidden (hasValue={sk['hasValue']}), charge_id set by script = {cid['value']}")

_, hist = api("GET", "/api/history", expect=200)
assert hist and hist[0]["status"] == 201
ok(f"history: {len(hist)} entries, latest {hist[0]['method']} {hist[0]['url']}")

# --- API keys ---
_, created = api("POST", "/api/me/keys", {"name": "Smoke test", "scopes": ["collections:read"], "expiresInDays": 1}, expect=201)
key = created["key"]
st, body = api("GET", "/api/apis", headers={"Authorization": "Bearer " + key, "Cookie": ""})
assert st == 200, (st, body)
st, _ = api("POST", f"/api/requests/{create['id']}/send", {}, headers={"Authorization": "Bearer " + key})
assert st == 403, st
api("DELETE", f"/api/me/keys/{created['apiKey']['id']}", expect=204)
st, _ = api("GET", "/api/apis", headers={"Authorization": "Bearer " + key})
assert st == 401, st
ok("api keys: read scope works, send blocked (403), revoked key rejected (401)")

# --- design: operation, lint, changes, publish ---
pid = pay["id"]
_, op = api("GET", f"/api/apis/{pid}/operation?key=" + urllib.parse.quote("POST /v2/charges"), expect=200)
newf = [f["name"] for f in op["fields"] if f.get("new")]
assert newf == ["capture"], newf
_, lint = api("GET", f"/api/apis/{pid}/lint", expect=200)
_, ch = api("GET", f"/api/apis/{pid}/changes", expect=200)
assert ch["suggestedVersion"] == "v2.5.0" and not ch["breaking"], ch
ok(f"design: new fields={newf}, lint {len(lint['issues'])} issues/{lint['rules']} rules, {len(ch['changes'])} changes → {ch['suggestedVersion']}")
_, pubd = api("POST", f"/api/apis/{pid}/publish", {"version": "", "notify": True}, expect=200)
_, ch2 = api("GET", f"/api/apis/{pid}/changes", expect=200)
assert pubd["version"] == "v2.5.0" and ch2["changes"] == [], (pubd, ch2)
_, docs = api("GET", f"/api/apis/{pid}/docs", expect=200)
assert docs["version"] == "v2.5.0" and "cURL" in docs["operations"][0]["samples"]
ok(f"published {pubd['version']}; docs show {len(docs['operations'])} operations")

# --- mocks ---
_, mocks = api("GET", "/api/mocks", expect=200)
st, _, body = raw("POST", BACKEND + "/mock/payments-sandbox/v2/charges", b'{"amount":999,"capture":false}', {"Content-Type": "application/json", "Prefer": "code=402"})
assert st == 402, (st, body)
_, calls = api("GET", "/api/mocks/calls", expect=200)
ok(f"mocks: {[m['name'] for m in mocks]}, Prefer: code=402 → {st}, {len(calls)} calls logged")
print("ALL SMOKE TESTS PASSED")
