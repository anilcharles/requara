package runner

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anilcharles/requara/server/api/internal/sandbox"
)

// Like Postman: the pre-request script runs before {{variables}} are filled,
// so what it sets is used; pm.sendRequest can fetch what it needs first.
func TestPreRequestScriptFeedsTheRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			fmt.Fprint(w, `{"token":"fresh-tok"}`)
		default:
			fmt.Fprintf(w, "%s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
	}))
	defer srv.Close()
	spec := Spec{
		Method: "GET", URL: "{{base}}/orders/{{orderId}}",
		Headers: []KV{{Key: "Authorization", Value: "Bearer {{tok}}", Enabled: true}},
		PreScript: `
pm.environment.set("orderId", "o-7");
console.log(pm.request.url.toString());
pm.sendRequest(pm.variables.get("base") + "/token", (err, res) => pm.variables.set("tok", res.json().token));
`,
		PostScript: `pm.test("ok", () => pm.expect(pm.response.text()).to.include("fresh-tok")); pm.globals.set("last", pm.environment.get("orderId"));`,
		Scopes:     &sandbox.Scopes{Globals: map[string]string{}, Collection: map[string]string{"base": srv.URL}, Environment: map[string]string{"orderId": "old"}, Data: map[string]string{}},
	}
	vars := map[string]string{"base": srv.URL, "orderId": "old"}
	res := send(t, spec, vars)
	if res.Error != "" || res.ScriptError != "" {
		t.Fatalf("%q %q", res.Error, res.ScriptError)
	}
	if want := "/orders/o-7 Bearer fresh-tok"; res.Body != want {
		t.Fatalf("body %q, want %q", res.Body, want)
	}
	if len(res.Logs) == 0 || res.Logs[0] != "{{base}}/orders/{{orderId}}" {
		t.Fatalf("the pre-request script should see the request as written: %q", res.Logs)
	}
	if len(res.Tests) != 1 || !res.Tests[0].Pass {
		t.Fatalf("tests %+v", res.Tests)
	}
	// Each scope's changes come back apart; local (pm.variables) isn't kept.
	if res.EnvChanges["orderId"] != "o-7" || len(res.EnvChanges) != 1 || res.GlobalChanges["last"] != "o-7" || len(res.CollectionChanges) != 0 {
		t.Fatalf("env %v globals %v collection %v", res.EnvChanges, res.GlobalChanges, res.CollectionChanges)
	}
}
