package sandbox

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestTestsAndEnv(t *testing.T) {
	out, err := Run(context.Background(), `
test("returns 201", () => expect(res.status).toBe(201));
test("is uncaptured", () => expect(res.json.captured).toBe(false));
test("fails", () => expect(res.json.amount).toBe(1));
env.set("charge_id", res.json.id);
console.log("done", 1);
`, Input{
		Request:  Request{Method: "POST", URL: "http://x"},
		Response: &Response{Status: 201, Body: `{"id":"ch_1","captured":false,"amount":4200}`, Time: 12},
		Env:      map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Tests) != 3 || !out.Tests[0].Pass || !out.Tests[1].Pass || out.Tests[2].Pass {
		t.Fatalf("unexpected tests: %+v", out.Tests)
	}
	if out.Env["charge_id"] != "ch_1" {
		t.Fatalf("env not set: %+v", out.Env)
	}
	if len(out.Logs) != 1 || out.Logs[0] != "done 1" {
		t.Fatalf("logs: %+v", out.Logs)
	}
}

func TestPreRequestMutation(t *testing.T) {
	out, err := Run(context.Background(), `req.headers["X-Sig"] = "abc" + env.get("k");`, Input{
		Request: Request{Method: "GET", URL: "http://x"}, Env: map[string]string{"k": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Request.Headers["X-Sig"] != "abc1" {
		t.Fatalf("header not set: %+v", out.Request)
	}
}

func TestInfiniteLoopTimesOut(t *testing.T) {
	out, err := Run(context.Background(), `while (true) {}`, Input{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Error == "" {
		t.Fatal("expected a timeout error")
	}
}

func TestMemoryLimit(t *testing.T) {
	out, err := Run(context.Background(), `const a = []; while (true) a.push("x".repeat(1e6));`, Input{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Error == "" {
		t.Fatal("expected an out-of-memory error")
	}
}

// Scripts must not see the server's files or the QuickJS std/os modules.
func TestNoFilesystem(t *testing.T) {
	for _, script := range []string{
		`console.log(typeof std, typeof os)`,
		`console.log(String(globalThis.std && std.loadFile('/sandbox.go')))`,
	} {
		out, err := Run(context.Background(), script, Input{})
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range out.Logs {
			if strings.Contains(l, "package sandbox") || strings.Contains(l, "object") {
				t.Fatalf("%s: script reached the filesystem: %q", script, out.Logs)
			}
		}
	}
	dir, err := emptyRoot()
	if err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("sandbox root %s isn't empty", dir)
	}
}

// Postman scripts run unchanged: pm.*, Chai-style expect and the legacy globals.
func TestPostmanScripts(t *testing.T) {
	in := Input{
		Request:  Request{Method: "GET", URL: "https://x.test/a", Headers: map[string]string{"Accept": "application/json"}},
		Response: &Response{Status: 200, Headers: map[string]string{"content-type": "application/json"}, Body: `{"id":"o1","items":[1,2],"ok":true}`, Time: 42},
		Env:      map[string]string{"base": "https://x.test"},
	}
	out, err := Run(context.Background(), `
pm.test("status", () => pm.response.to.have.status(200));
pm.test("ok", () => pm.response.to.be.ok);
pm.test("json", () => { const b = pm.response.json(); pm.expect(b.id).to.equal("o1"); pm.expect(b.items).to.have.lengthOf(2); pm.expect(b).to.have.property("ok", true); pm.expect(b.items).to.include(2); });
pm.test("header", () => pm.response.to.have.header("Content-Type"));
pm.test("time", () => pm.expect(pm.response.responseTime).to.be.below(1000));
pm.test("not", () => pm.expect(1).to.not.equal(2));
pm.test("fails", () => pm.expect("a").to.be.a("number"));
pm.environment.set("orderId", pm.response.json().id);
pm.collectionVariables.set("seen", pm.variables.get("base"));
tests["legacy"] = responseCode.code === 200;
`, in)
	if err != nil || out.Error != "" {
		t.Fatalf("%v %q", err, out.Error)
	}
	got := map[string]bool{}
	for _, tr := range out.Tests {
		got[tr.Name] = tr.Pass
	}
	for _, name := range []string{"status", "ok", "json", "header", "time", "not", "legacy"} {
		if !got[name] {
			t.Errorf("%s should pass: %+v", name, out.Tests)
		}
	}
	if got["fails"] {
		t.Error("a failing assertion passed")
	}
	if out.Env["orderId"] != "o1" || out.Env["seen"] != "https://x.test" {
		t.Errorf("env: %v", out.Env)
	}
	pre, _ := Run(context.Background(), `pm.request.headers.add({key: "X-Trace", value: pm.variables.replaceIn("{{base}}/t")});`, Input{Request: in.Request, Env: in.Env})
	if pre.Request.Headers["X-Trace"] != "https://x.test/t" {
		t.Errorf("pre-request header: %v", pre.Request.Headers)
	}
}
