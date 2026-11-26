package runner

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendWithVariablesScriptsAndRedaction(t *testing.T) {
	var gotAuth, gotBody, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":"ch_9","captured":false}`))
	}))
	defer srv.Close()

	r := &Runner{AllowPrivateNetwork: true}
	vars := map[string]string{"base_url": srv.URL, "secret_key": "test-redact-me-supersecret", "cust": "cus_1"}
	res := r.Send(context.Background(), Spec{
		Method:     "post",
		URL:        "{{base_url}}/v2/charges",
		Params:     []KV{{Key: "expand[]", Value: "customer", Enabled: true}, {Key: "off", Value: "x", Enabled: false}},
		BodyMode:   "json",
		Body:       `{"customer":"{{cust}}"}`,
		Auth:       Auth{Type: "bearer", Token: "{{secret_key}}"},
		PostScript: `test("201", () => expect(res.status).toBe(201)); env.set("charge_id", res.json.id);`,
	}, vars, []string{"test-redact-me-supersecret"})

	if res.Error != "" {
		t.Fatal(res.Error)
	}
	if res.Status != 201 || gotAuth != "Bearer test-redact-me-supersecret" || gotBody != `{"customer":"cus_1"}` {
		t.Fatalf("status=%d auth=%q body=%q", res.Status, gotAuth, gotBody)
	}
	if gotQuery != "expand%5B%5D=customer" {
		t.Fatalf("query=%q", gotQuery)
	}
	if len(res.Tests) != 1 || !res.Tests[0].Pass || res.EnvChanges["charge_id"] != "ch_9" {
		t.Fatalf("tests=%+v env=%+v", res.Tests, res.EnvChanges)
	}
	for _, h := range res.ReqHeaders {
		if strings.Contains(h.Value, "supersecret") {
			t.Fatalf("secret leaked in stored headers: %+v", h)
		}
	}
	if len(res.Timeline) == 0 {
		t.Fatal("no timeline")
	}
}

func TestBlocksPrivateNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	res := (&Runner{AllowPrivateNetwork: false}).Send(context.Background(), Spec{Method: "GET", URL: srv.URL}, map[string]string{}, nil)
	if !strings.Contains(res.Error, "Blocked") {
		t.Fatalf("expected block, got %q", res.Error)
	}
}
