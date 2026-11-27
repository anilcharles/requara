package sandbox

import (
	"context"
	"strings"
	"testing"
)

func TestVariableScopes(t *testing.T) {
	in := Input{
		Request: Request{Method: "GET", URL: "http://x"},
		Env:     map[string]string{"host": "env.example", "who": "data"},
		Scopes: &Scopes{
			Globals:     map[string]string{"host": "global.example", "g": "1"},
			Collection:  map[string]string{"host": "collection.example", "c": "2"},
			Environment: map[string]string{"host": "env.example"},
			Data:        map[string]string{"who": "data"},
		},
		Info: Info{RequestName: "List orders", Iteration: 3},
	}
	out, err := Run(context.Background(), `
console.log(pm.variables.get("host"), pm.globals.get("host"), pm.collectionVariables.get("host"), pm.environment.get("host"), pm.iterationData.get("who"));
console.log(pm.info.requestName, pm.info.iteration, pm.variables.has("g"), pm.environment.has("g"));
pm.globals.set("g", 10);
pm.collectionVariables.set("c", "20");
pm.environment.set("e", {a: 1});
pm.variables.set("local", "only now");
pm.iterationData.set("who", "ignored");
postman.setGlobalVariable("legacy", "yes");
console.log(pm.variables.get("local"), pm.variables.get("g"), pm.variables.replaceIn("{{host}}/{{c}}"));
`, in)
	if err != nil || out.Error != "" {
		t.Fatalf("%v %q", err, out.Error)
	}
	want := []string{
		"env.example global.example collection.example env.example data",
		"List orders 3 true false",
		"only now 10 env.example/20",
	}
	if strings.Join(out.Logs, "\n") != strings.Join(want, "\n") {
		t.Fatalf("logs:\n%s\nwant:\n%s", strings.Join(out.Logs, "\n"), strings.Join(want, "\n"))
	}
	if out.Globals["g"] != "10" || out.Globals["legacy"] != "yes" || out.Collection["c"] != "20" || out.Env["e"] != `{"a":1}` || out.Local["local"] != "only now" {
		t.Fatalf("globals %v collection %v env %v local %v", out.Globals, out.Collection, out.Env, out.Local)
	}
	if len(out.Env) != 1 {
		t.Fatalf("only the environment's own changes belong in Env: %v", out.Env)
	}
}

func TestSendRequest(t *testing.T) {
	var sent []SubRequest
	send := func(_ context.Context, r SubRequest) SubResponse {
		sent = append(sent, r)
		if strings.Contains(r.URL, "down") {
			return SubResponse{Error: "Connection refused"}
		}
		return SubResponse{Status: 200, StatusText: "OK", Headers: map[string]string{"content-type": "application/json"}, Body: `{"access_token":"t-` + r.Method + `"}`, Time: 5}
	}
	out, err := RunWith(context.Background(), `
console.log("start");
pm.sendRequest({
  url: "https://id.example/token", method: "POST",
  header: [{ key: "Accept", value: "application/json" }, { key: "X-Off", value: "1", disabled: true }],
  body: { mode: "urlencoded", urlencoded: [{ key: "grant_type", value: "client_credentials" }, { key: "scope", value: "a b" }] },
}, (err, res) => {
  pm.environment.set("token", res.json().access_token);
  pm.test("token ok", () => res.to.have.status(200));
});
pm.sendRequest("https://down.example/").then(() => console.log("no"), (e) => console.log("failed:", e.message));
pm.sendRequest({ url: "https://api.example/x", method: "put", body: { mode: "raw", raw: "{}" } }).then((r) => console.log("then", r.code));
console.log("end");
`, Input{Request: Request{Method: "GET", URL: "http://x"}}, send)
	if err != nil || out.Error != "" {
		t.Fatalf("%v %q", err, out.Error)
	}
	if len(sent) != 3 {
		t.Fatalf("sent %d requests, want 3: %+v", len(sent), sent)
	}
	first := sent[0]
	if first.Method != "POST" || first.Body != "grant_type=client_credentials&scope=a%20b" || first.Headers["Accept"] != "application/json" ||
		first.Headers["Content-Type"] != "application/x-www-form-urlencoded" || first.Headers["X-Off"] != "" {
		t.Fatalf("first request: %+v", first)
	}
	if sent[1].Method != "GET" || sent[2].Method != "PUT" || sent[2].Body != "{}" {
		t.Fatalf("requests: %+v", sent)
	}
	if out.Env["token"] != "t-POST" || len(out.Tests) != 1 || !out.Tests[0].Pass {
		t.Fatalf("env %v tests %+v", out.Env, out.Tests)
	}
	// Logs are the last run's only: each line once.
	if got := strings.Join(out.Logs, "|"); got != "start|failed: Connection refused|then 200|end" {
		t.Fatalf("logs %q", got)
	}
}

func TestSendRequestLimits(t *testing.T) {
	n := 0
	send := func(context.Context, SubRequest) SubResponse { n++; return SubResponse{Status: 204} }
	out, _ := RunWith(context.Background(), `for (let i = 0; i < 50; i++) pm.sendRequest("https://x.example/" + i);`, Input{}, send)
	if n != MaxSubRequests || !strings.Contains(out.Error, "at most") {
		t.Fatalf("sent %d, error %q", n, out.Error)
	}
	out, _ = Run(context.Background(), `pm.sendRequest("https://x.example/")`, Input{})
	if !strings.Contains(out.Error, "isn't available") {
		t.Fatalf("without a sender: %q", out.Error)
	}
}
