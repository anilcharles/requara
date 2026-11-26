package sandbox

import (
	"context"
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
