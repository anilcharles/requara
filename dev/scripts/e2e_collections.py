#!/usr/bin/env python3
"""End-to-end test of the Postman-style features on a team workspace:
collections with variables/auth/scripts, Postman import/export, cURL import,
form-data, examples, cookies, the run endpoint, monitors and requara-cli.
Needs a running server (dev/scripts/e2e_admin.py's setup) and internet
access from it (postman-echo.com).

    python3 dev/scripts/e2e_collections.py [http://localhost:8080] [path/to/requara-cli]"""
import base64, json, os, subprocess, sys, tempfile, time
sys.path.insert(0, os.path.dirname(__file__))
src = open(os.path.join(os.path.dirname(__file__), "e2e_admin.py")).read().split("# ---------------------------------------------------------------------------")[0]
ns = {"__file__": __file__}
exec(src, ns)
Browser, oidc_signin, ok, fail = ns["Browser"], ns["oidc_signin"], ns["ok"], ns["fail"]
BACKEND = ns["BACKEND"]
CLI = sys.argv[2] if len(sys.argv) > 2 else None
ECHO = "https://postman-echo.com"

b = Browser()
oidc_signin(b, "alex.chen")
_, ws = b.api("POST", "/api/workspaces", {"name": f"Postman e2e {int(time.time()) % 10000}"}, expect=201)
H = {"X-Workspace": ws["id"]}
api = lambda m, p, body=None, expect=None: b.api(m, p, body, headers=H, expect=expect)
ok(f"fresh team workspace {ws['name']}")

print("collections: variables, auth and scripts are inherited")
_, col = api("POST", "/api/collections", {"name": "Echo"}, expect=201)
api("PUT", f"/api/collections/{col['id']}/settings", {
    "name": "Echo", "description": "postman-echo",
    "variables": [{"key": "base", "value": ECHO, "enabled": True}, {"key": "who", "value": "collection", "enabled": True}],
    "auth": {"type": "bearer", "token": "{{token}}"},
    "preScript": "pm.variables.set('token', 'tok-from-collection-script')",
    "postScript": "pm.test('collection test: answered', () => pm.expect(pm.response.code).to.be.below(400));",
}, expect=200)
_, req = api("POST", "/api/requests", {"name": "Get", "method": "GET", "url": "{{base}}/get", "collectionId": col["id"],
                                       "params": [{"key": "who", "value": "{{who}}", "enabled": True}],
                                       "postScript": "pm.test('echoes', () => pm.expect(pm.response.json().args.who).to.equal(pm.variables.get('who')));"}, expect=201)
_, sent = api("POST", f"/api/requests/{req['id']}/send", {}, expect=200)
res = sent["result"]
echo = json.loads(res["body"])
assert res["status"] == 200 and echo["headers"]["authorization"] == "Bearer tok-from-collection-script", echo["headers"]
assert echo["args"]["who"] == "collection", echo["args"]
assert [t["pass"] for t in res["tests"]] == [True, True], res["tests"]
ok("collection variable, bearer auth set by the collection's pre-request script, and both test scripts")
_, sent = api("POST", f"/api/requests/{req['id']}/send", {"data": {"who": "iteration-data"}}, expect=200)
assert json.loads(sent["result"]["body"])["args"]["who"] == "iteration-data"
ok("runner iteration data overrides collection variables")

print("more auth and body types")
_, r2 = api("POST", "/api/requests", {"name": "Upload", "method": "POST", "url": "{{base}}/post", "collectionId": col["id"], "folder": "Bodies",
                                      "bodyMode": "form-data", "auth": {"type": "basic", "username": "ana", "password": "pw"},
                                      "formData": [{"key": "title", "value": "{{who}}", "type": "text", "enabled": True},
                                                   {"key": "doc", "type": "file", "fileName": "hello.txt", "contentType": "text/plain",
                                                    "value": base64.b64encode(b"hello file").decode(), "enabled": True}]}, expect=201)
_, sent = api("POST", f"/api/requests/{r2['id']}/send", {}, expect=200)
echo = json.loads(sent["result"]["body"])
assert echo["form"]["title"] == "collection" and "hello.txt" in echo["files"] and echo["headers"]["authorization"].startswith("Basic "), echo
ok("multipart form-data with a file, and basic auth overriding the collection's")
_, r3 = api("POST", "/api/requests", {"name": "Digest", "method": "GET", "url": "{{base}}/digest-auth", "collectionId": col["id"], "folder": "Auth",
                                      "auth": {"type": "digest", "username": "postman", "password": "password"}}, expect=201)
_, sent = api("POST", f"/api/requests/{r3['id']}/send", {}, expect=200)
assert sent["result"]["status"] == 200, sent["result"]
ok("HTTP Digest auth against postman-echo")

print("cookies")
_, r4 = api("POST", "/api/requests", {"name": "Set cookie", "method": "GET", "url": "{{base}}/cookies/set?flavor=oat", "collectionId": col["id"], "folder": "Cookies",
                                      "settings": {"followRedirects": False}}, expect=201)
api("POST", f"/api/requests/{r4['id']}/send", {}, expect=200)
_, jar = api("GET", "/api/cookies", expect=200)
assert any(c["name"] == "flavor" and c["value"] == "oat" for c in jar), jar
_, r5 = api("POST", "/api/requests", {"name": "Read cookies", "method": "GET", "url": "{{base}}/cookies", "collectionId": col["id"], "folder": "Cookies"}, expect=201)
_, sent = api("POST", f"/api/requests/{r5['id']}/send", {}, expect=200)
assert json.loads(sent["result"]["body"])["cookies"].get("flavor") == "oat", sent["result"]["body"]
api("DELETE", "/api/cookies?name=flavor", expect=204)
ok("Set-Cookie kept and sent back to the same site; cookie deleted")

print("examples")
api("POST", f"/api/requests/{req['id']}/examples", {"name": "OK", "status": 200, "headers": [{"key": "Content-Type", "value": "application/json", "enabled": True}], "body": "{\"ok\":true}"}, expect=201)
_, exs = api("GET", f"/api/requests/{req['id']}/examples", expect=200)
assert exs[0]["name"] == "OK"
ok("saved an example")

print("export, Postman import, cURL import")
st, h, exported = b.raw("GET", f"{BACKEND}/api/collections/{col['id']}/export", headers=H)
pm = json.loads(exported)
assert st == 200 and pm["info"]["schema"].endswith("v2.1.0/collection.json") and pm["auth"]["type"] == "bearer", pm["info"]
names = [i["name"] for i in pm["item"]]
assert "Get" in names and "Bodies" in names, names
ok(f"exported as Postman v2.1: {names}")
pm["info"]["name"] = "Echo (imported)"
_, imp = api("POST", "/api/collections/import", {"format": "postman", "data": json.dumps(pm)}, expect=201)
assert imp["requests"] == 5, imp
_, cols = api("GET", "/api/collections", expect=200)
imported = next(c for c in cols if c["id"] == imp["id"])
ok(f"re-imported: {imported['name']} with folders {[f['name'] for f in imported['folders']]}")
_, cu = api("POST", "/api/collections/import", {"format": "curl", "collectionId": col["id"],
                                                 "data": f"curl -X POST '{ECHO}/post?src=curl' -H 'X-Trace: 1' --json '{{\"a\": 1}}'"}, expect=201)
_, d = api("GET", f"/api/requests/{cu['requestId']}", expect=200)
assert d["method"] == "POST" and d["bodyMode"] == "json" and d["params"][0]["key"] == "src", d
ok("cURL command became a request")

print("collection run (what requara-cli uses)")
_, run = api("POST", f"/api/collections/{imp['id']}/run", {"iterations": 2, "data": [{"who": "row-1"}, {"who": "row-2"}]}, expect=200)
assert len(run["iterations"]) == 2 and run["errors"] == 0, json.dumps(run)[:500]
whos = [json.loads(json.dumps(it["data"]))["who"] for it in run["iterations"]]
assert whos == ["row-1", "row-2"], whos
ok(f"2 iterations with data rows: {run['passed']} passed, {run['failed']} failed, {run['errors']} errors")

print("monitors")
_, mon = api("POST", "/api/monitors", {"name": "Echo health", "target": col["id"], "intervalMin": 60}, expect=201)
_, mrun = api("POST", f"/api/monitors/{mon['id']}/run", {}, expect=200)
assert mrun["requests"] >= 5 and mrun["passed"] >= 2, mrun
_, runs = api("GET", f"/api/monitors/{mon['id']}/runs", expect=200)
_, mons = api("GET", "/api/monitors", expect=200)
assert len(runs) == 1 and mons[0]["lastPassed"] == mrun["passed"], (runs, mons)
ok(f"monitor ran: {mrun['passed']} passed, {mrun['failed']} failed, {mrun['errors']} errors")

if CLI:
    print("requara-cli")
    _, key = b.api("POST", "/api/me/keys", {"name": "ci", "scopes": ["collections:read", "runs:write"]}, expect=201)
    with tempfile.TemporaryDirectory() as d:
        csv = os.path.join(d, "data.csv")
        open(csv, "w").write("who\nci-1\nci-2\nci-3\n")
        out = os.path.join(d, "junit.xml")
        env = {**os.environ, "REQUARA_URL": BACKEND, "REQUARA_API_KEY": key["key"], "REQUARA_WORKSPACE": ws["id"]}
        p = subprocess.run([CLI, "run", "Echo (imported)", "--data", csv, "--reporter", "junit", "--out", out], env=env, capture_output=True, text=True, timeout=600)
        print("    " + p.stdout.strip().splitlines()[-1])
        xml = open(out).read()
        assert p.returncode == 0, (p.returncode, p.stdout[-800:], p.stderr)
        assert xml.count("<testsuite ") == 15 and "failures=\"0\"" in xml, xml[:400]
        bad = subprocess.run([CLI, "run", "Nope"], env=env, capture_output=True, text=True)
        assert bad.returncode == 2 and "no collection" in bad.stderr, bad.stderr
    ok("CLI ran 3 data rows, wrote JUnit, exit code 0; unknown collection exits 2")
print("PASS")
