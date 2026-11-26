#!/usr/bin/env python3
"""End-to-end test of sign-in and administration against a running server
with Keycloak (OIDC "company" and SAML "corp"), email links via Mailpit, and
alex@acme.dev as the instance admin (REQUARA_ADMIN_EMAILS / admins.txt).

    python3 dev/scripts/e2e_admin.py [http://localhost:8080]

Covers: provider list, OIDC + SAML + email sign-in (browser and desktop),
admin from the server's list only, admin API and tokens, deactivation, SCIM,
the audit log and CSV export, banner, workspaces and members."""
import base64, hashlib, html, http.cookiejar, json, os, re, secrets, sys, time
import urllib.error, urllib.parse, urllib.request

BACKEND = sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8080"
MAILPIT = "http://localhost:8025"
PASSWORD = "requara-dev"
RUN = secrets.token_hex(3)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k):
        return None


class Browser:
    """A cookie jar and no automatic redirects, like stepping through a browser."""

    def __init__(self):
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar), NoRedirect)

    def raw(self, method, url, data=None, headers=None):
        req = urllib.request.Request(url, data=data, method=method, headers=headers or {})
        try:
            r = self.opener.open(req)
            out = (r.status, dict(r.headers), r.read())
        except urllib.error.HTTPError as e:
            out = (e.code, dict(e.headers), e.read())
        for c in self.jar:  # browsers treat localhost as secure; Python's jar doesn't
            c.secure = False
        return out

    def api(self, method, path, body=None, headers=None, expect=None):
        h = {"X-Requested-With": "requara", **(headers or {})}
        data = None
        if body is not None:
            data = json.dumps(body).encode()
            h["Content-Type"] = "application/json"
        st, _, out = self.raw(method, BACKEND + path, data, h)
        if expect is not None and st != expect:
            fail(f"{method} {path}: want {expect}, got {st} {out[:300]!r}")
        return st, (json.loads(out) if out and out[:1] in b"[{" else out)

    def keycloak_login(self, location, username):
        """Fill in Keycloak's login form; returns the response to the form post."""
        st, h, page = self.raw("GET", location)
        assert st == 200, (st, page[:200])
        action = html.unescape(re.search(rb'action="([^"]+)"', page).group(1).decode())
        form = urllib.parse.urlencode({"username": username, "password": PASSWORD, "credentialId": ""}).encode()
        return self.raw("POST", action, form, {"Content-Type": "application/x-www-form-urlencoded"})


def fail(msg):
    sys.exit("FAIL " + msg)


def ok(msg):
    print("  ok  " + msg)


def oidc_signin(b, username, query=""):
    st, h, _ = b.raw("GET", f"{BACKEND}/auth/login?provider=company&redirect=/apis{query}")
    assert st == 302 and "realms/acme" in h["Location"], (st, h)
    st, h, page = b.keycloak_login(h["Location"], username)
    assert st == 302, (st, page[:300])
    cb = urllib.parse.urlparse(h["Location"])
    return b.raw("GET", BACKEND + cb.path + "?" + cb.query)


def saml_signin(b, username, query="", keep=None):
    st, h, _ = b.raw("GET", f"{BACKEND}/auth/login?provider=corp&redirect=/apis{query}")
    assert st == 302 and "protocol/saml" in h["Location"] and "SAMLRequest=" in h["Location"], (st, h)
    st, h, page = b.keycloak_login(h["Location"], username)
    assert st == 200 and b"SAMLResponse" in page, (st, h, page[:400])
    fields = dict(re.findall(rb'name="(SAMLResponse|RelayState)"\s+value="([^"]*)"', page))
    acs = html.unescape(re.search(rb'action="([^"]+)"', page).group(1).decode())
    assert acs == f"{BACKEND}/auth/saml/corp/acs", acs
    form = urllib.parse.urlencode({k.decode(): html.unescape(v.decode()) for k, v in fields.items()}).encode()
    if keep is not None:
        keep.append(form)
    # The IdP's auto-submitting form: a cross-site POST, so no Requara cookies matter.
    return b.raw("POST", acs, form, {"Content-Type": "application/x-www-form-urlencoded", "Origin": "http://localhost:8081"})


def mail_ids(to):
    msgs = json.load(urllib.request.urlopen(f"{MAILPIT}/api/v1/search?query=" + urllib.parse.quote(f"to:{to}")))["messages"] or []
    return {m["ID"] for m in msgs}


def mail_link(to, seen):
    """A sign-in link Mailpit received for `to` that isn't in `seen`."""
    for _ in range(40):
        msgs = json.load(urllib.request.urlopen(f"{MAILPIT}/api/v1/search?query=" + urllib.parse.quote(f"to:{to}")))["messages"] or []
        for m in msgs:
            if m["ID"] not in seen and "sign-in link" in m["Subject"]:
                body = json.load(urllib.request.urlopen(f"{MAILPIT}/api/v1/message/{m['ID']}"))["Text"]
                return re.search(r"token=([0-9a-f]+)", body).group(1)
        time.sleep(0.25)
    return None


def mail_count(to):
    return json.load(urllib.request.urlopen(f"{MAILPIT}/api/v1/search?query=" + urllib.parse.quote(f"to:{to}")))["messages_count"]


def iso_now():
    return time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime())


def email_signin(b, email, query=""):
    st, h, _ = b.raw("GET", f"{BACKEND}/auth/login?provider=email&redirect=/apis{query}")
    assert st == 302 and "/signin/email?flow=" in h["Location"], (st, h)
    flow = urllib.parse.parse_qs(urllib.parse.urlparse(h["Location"]).query)["flow"][0]
    st, info = b.api("GET", f"/auth/email?flow={flow}", expect=200)
    seen = mail_ids(email)
    b.api("POST", "/auth/email/start", {"flow": flow, "email": email}, expect=200)
    token = mail_link(email, seen)
    if token is None:
        return None, info
    st, got = b.api("GET", f"/auth/email?token={token}", expect=200)
    assert got["email"] == email.lower(), got
    form = urllib.parse.urlencode({"token": token}).encode()
    res = b.raw("POST", BACKEND + "/auth/email/verify", form, {"Content-Type": "application/x-www-form-urlencoded", "Origin": BACKEND})
    return (res, token), info


def desktop_params():
    verifier = base64.urlsafe_b64encode(os.urandom(32)).rstrip(b"=").decode()
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b"=").decode()
    return verifier, f"&desktop_port=51234&desktop_challenge={challenge}"


def desktop_exchange(b, location, verifier):
    assert location.startswith("http://127.0.0.1:51234/callback?code="), location
    code = urllib.parse.parse_qs(urllib.parse.urlparse(location).query)["code"][0]
    st, tok = b.api("POST", "/auth/desktop/token", {"code": code, "verifier": verifier}, expect=200)
    return tok["token"]


# ---------------------------------------------------------------------------
print("providers")
pub = Browser()
_, prov = pub.api("GET", "/auth/providers", expect=200)
ids = [p["id"] for p in prov["providers"]]
assert ids == ["company", "corp", "email"], ids
ok(f"sign-in options: {[(p['name'], p['type']) for p in prov['providers']]}")

print("admin from the server's list (admins.txt), OIDC")
alex = Browser()
st, h, _ = oidc_signin(alex, "alex.chen")
assert st == 302 and h["Location"].endswith("/apis"), (st, h)
_, me = alex.api("GET", "/api/me", expect=200)
assert me["isAdmin"] is True, me
_, ov = alex.api("GET", "/api/admin/overview", expect=200)
assert "alex@acme.dev" in ov["server"]["admins"] and ov["server"]["adminSources"][0]["kind"] == "file", ov["server"]
ok(f"alex is admin (sources {ov['server']['adminSources']}); {ov['counts']['users']} users, {ov['counts']['workspaces']} workspaces")

priya = Browser()
st, h, _ = oidc_signin(priya, "priya.raman")
assert st == 302, (st, h)
_, me = priya.api("GET", "/api/me", expect=200)
assert me["isAdmin"] is False, me
priya.api("GET", "/api/admin/overview", expect=403)
priya.api("GET", "/api/admin/users", expect=403)
ok("priya is not admin: /api/admin answers 403")
# There's no API to make someone admin.
st, _ = alex.api("PATCH", "/api/me/preferences", {"isAdmin": True})
assert st == 400, st
ok("no way to grant admin from the browser")

print("SAML (Keycloak)")
jonas = Browser()
kept = []
st, h, body = saml_signin(jonas, "jonas.tan", keep=kept)
assert st == 302 and h["Location"].endswith("/apis"), (st, h, body[:300])
_, me = jonas.api("GET", "/api/me", expect=200)
assert me["email"] == "jonas@acme.dev" and me["id"] == "corp:jonas.tan" and "sre" in me["groups"], me
ok(f"jonas signed in with SAML: id={me['id']} name={me['name']!r} groups={me['groups']}")
replay = Browser()
st, h, _ = replay.raw("POST", f"{BACKEND}/auth/saml/corp/acs", kept[0], {"Content-Type": "application/x-www-form-urlencoded"})
assert st == 302 and "/signin?error=" in h["Location"] and not any(c.name == "requara_session" for c in replay.jar), (st, h)
ok("replaying the same SAML response is refused: " + urllib.parse.unquote_plus(h["Location"].split("error=")[1])[:70])
forged = Browser()
st, h, _ = forged.raw("POST", f"{BACKEND}/auth/saml/corp/acs", b"SAMLResponse=PHNhbWw%2B&RelayState=", {"Content-Type": "application/x-www-form-urlencoded"})
assert st == 302 and "error=" in h["Location"], (st, h)
ok("IdP-initiated / unsolicited response refused")
st, h, md = pub.raw("GET", f"{BACKEND}/auth/saml/corp/metadata")
assert st == 200 and b"AssertionConsumerService" in md and b"/auth/saml/corp/acs" in md, md[:300]
ok("SP metadata served")

print("SAML from the desktop app")
d = Browser()
verifier, q = desktop_params()
st, h, _ = saml_signin(d, "jonas.tan", q)
token = desktop_exchange(d, h["Location"], verifier)
st, me = d.api("GET", "/api/me", headers={"Authorization": "Bearer " + token}, expect=200)
assert me["email"] == "jonas@acme.dev", me
ok("desktop loopback + PKCE works with SAML")

print("email links (Mailpit)")
sam_email = f"sam.{RUN}@acme.dev"
sam = Browser()
(res, token), info = email_signin(sam, sam_email)
st, h, _ = res
assert st == 302 and h["Location"].endswith("/apis"), (st, h)
_, me = sam.api("GET", "/api/me", expect=200)
assert me["id"] == f"email:{sam_email}" and me["email"] == sam_email, me
ok(f"signed in with an email link as {sam_email}")
again = Browser()
st, h, _ = again.raw("POST", BACKEND + "/auth/email/verify", urllib.parse.urlencode({"token": token}).encode(),
                     {"Content-Type": "application/x-www-form-urlencoded", "Origin": BACKEND})
assert st == 302 and "error=" in h["Location"], (st, h)
ok("a link works only once")
evil = Browser()
st, h, _ = evil.raw("POST", BACKEND + "/auth/email/verify", b"token=x", {"Content-Type": "application/x-www-form-urlencoded", "Origin": "https://evil.example"})
assert st == 403, st
ok("link confirmation from another origin refused")
outsider = f"eve.{RUN}@evil.example"
r, _ = email_signin(Browser(), outsider)
assert r is None and mail_count(outsider) == 0
ok("no link sent to an address that may not join (same answer either way)")

print("email link from the desktop app")
d = Browser()
verifier, q = desktop_params()
(res, _), info = email_signin(d, sam_email, q)
assert info["desktop"] is True
token = desktop_exchange(d, res[1]["Location"], verifier)
d.api("GET", "/api/me", headers={"Authorization": "Bearer " + token}, expect=200)
ok("desktop loopback + PKCE works with email links")

print("admin tokens")
_, created = alex.api("POST", "/api/admin/tokens", {"name": f"e2e {RUN}", "expiresInDays": 1}, expect=201)
tok = {"Authorization": "Bearer " + created["token"]}
bot = Browser()
_, users = bot.api("GET", "/api/admin/users", headers=tok, expect=200)
ok(f"admin token lists {len(users)} users")
bot.api("GET", "/api/apis", headers=tok, expect=403)
bot.api("POST", "/api/admin/tokens", {"name": "x"}, headers=tok, expect=403)
ok("admin token can't use the workspace API or mint tokens")
_, keyres = priya.api("POST", "/api/me/keys", {"name": "full", "scopes": ["full access"]}, expect=201)
bot.api("GET", "/api/admin/users", headers={"Authorization": "Bearer " + keyres["key"]}, expect=403)
ok("a user's full-access API key can't use the admin API")

print("deactivate / reactivate")
_, users = alex.api("GET", "/api/admin/users?q=priya", expect=200)
pid = users[0]["id"]
alex.api("POST", f"/api/admin/users/{urllib.parse.quote(pid)}/deactivate", expect=204)
priya.api("GET", "/api/me", expect=401)
st, h, _ = oidc_signin(Browser(), "priya.raman")
assert st == 302 and "deactivated" in urllib.parse.unquote_plus(h["Location"]), h
ok("deactivated: signed out, and sign-in refused")
alex.api("POST", f"/api/admin/users/{urllib.parse.quote(pid)}/reactivate", expect=204)
priya = Browser()
st, h, _ = oidc_signin(priya, "priya.raman")
assert h["Location"].endswith("/apis"), h
ok("reactivated: can sign in again")
_, admins = alex.api("GET", "/api/admin/users?status=admins", expect=200)
st, err = alex.api("POST", f"/api/admin/users/{urllib.parse.quote(admins[0]['id'])}/deactivate")
assert st == 400 and "REQUARA_ADMIN_EMAILS" in err["error"], (st, err)
ok("admins can't be deactivated from the console")

print("SCIM")
ext = f"kim.{RUN}@partner.example"   # outside REQUARA_ALLOWED_DOMAINS
assert email_signin(Browser(), ext)[0] is None
st, u = bot.api("POST", "/scim/v2/Users", {"schemas": ["urn:ietf:params:scim:schemas:core:2.0:User"], "userName": ext, "externalId": "00u" + RUN,
                                           "name": {"givenName": "Kim", "familyName": "Lee"}, "emails": [{"value": ext, "primary": True}], "active": True},
                headers=tok, expect=201)
_, lst = bot.api("GET", "/scim/v2/Users?filter=" + urllib.parse.quote(f'userName eq "{ext}"'), headers=tok, expect=200)
assert lst["totalResults"] == 1 and lst["Resources"][0]["id"] == u["id"], lst
kim = Browser()
(res, _), _ = email_signin(kim, ext)
assert res[1]["Location"].endswith("/apis"), res
kim.api("GET", "/api/me", expect=200)
ok("SCIM-provisioned outsider can sign in")
bot.api("PATCH", f"/scim/v2/Users/{u['id']}", {"schemas": ["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
                                               "Operations": [{"op": "Replace", "path": "active", "value": "False"}]}, headers=tok, expect=200)
kim.api("GET", "/api/me", expect=401)
assert email_signin(Browser(), ext)[0] is None
ok("SCIM deactivation signs them out and stops sign-in")
bot.api("DELETE", f"/scim/v2/Users/{u['id']}", headers=tok, expect=204)
bot.api("GET", f"/scim/v2/Users/{u['id']}", headers=tok, expect=404)
ok("SCIM delete")

print("workspaces and members")
_, ws = alex.api("POST", "/api/admin/workspaces", {"name": f"E2E {RUN}", "autoJoin": False, "ownerEmail": "alex@acme.dev"}, expect=201)
alex.api("PUT", f"/api/admin/workspaces/{ws['id']}/members/{urllib.parse.quote(pid)}", {"role": "viewer"}, expect=204)
_, mem = alex.api("GET", f"/api/admin/workspaces/{ws['id']}/members", expect=200)
assert {m["email"]: m["role"] for m in mem} == {"alex@acme.dev": "owner", "priya@acme.dev": "viewer"}, mem
st, err = alex.api("PUT", f"/api/admin/workspaces/{ws['id']}/members/{urllib.parse.quote(admins[0]['id'])}", {"role": "editor"})
assert st == 400 and "owner" in err["error"], (st, err)
ok("admin made a workspace, added priya as viewer; last owner can't be demoted")
_, own = priya.api("POST", "/api/workspaces", {"name": f"Priya {RUN}"}, expect=201)
_, wss = priya.api("GET", "/api/workspaces", expect=200)
assert any(w["id"] == own["id"] and w["role"] == "owner" for w in wss), wss
ok("anyone can create a workspace and owns it")
alex.api("DELETE", f"/api/admin/workspaces/{ws['id']}", expect=204)
alex.api("DELETE", f"/api/admin/workspaces/{own['id']}", expect=204)

print("banner")
_, before = alex.api("GET", "/api/admin/settings", expect=200)
alex.api("PUT", "/api/admin/settings", {**before, "banner": {"enabled": True, "message": f"Maintenance {RUN}", "level": "warning"}}, expect=200)
_, inst = jonas.api("GET", "/api/instance", expect=200)
assert inst["banner"]["message"] == f"Maintenance {RUN}", inst
alex.api("PUT", "/api/admin/settings", before, expect=200)
ok("banner shown to everyone, then cleared")

print("audit log")
_, log = alex.api("GET", "/api/admin/audit?limit=200", expect=200)
actions = {e["action"] for e in log["items"]}
for want in ["auth.signin", "user.deactivated", "user.reactivated", "admin_token.created", "scim.user_created", "workspace.created", "settings.updated", "auth.email_link"]:
    assert want in actions, (want, actions)
fails = [e for e in log["items"] if e["action"] == "auth.signin" and e["outcome"] == "failure"]
assert fails, "failed sign-ins are logged"
_, only = alex.api("GET", "/api/admin/audit?action=auth.&outcome=failure", expect=200)
assert only["items"] and all(e["action"].startswith("auth.") and e["outcome"] == "failure" for e in only["items"])
st, h, csv = alex.raw("GET", BACKEND + "/api/admin/audit.csv?action=user.")
assert st == 200 and h["Content-Type"].startswith("text/csv") and csv.startswith(b"id,time,actor_kind"), (st, h, csv[:80])
ok(f"{len(log['items'])} events, {len(fails)} failed sign-ins, filters and CSV export work")

alex.api("DELETE", f"/api/admin/tokens/{created['id']}", expect=204)
bot.api("GET", "/api/admin/users", headers=tok, expect=401)
ok("revoked admin token stops working")
print("PASS")
