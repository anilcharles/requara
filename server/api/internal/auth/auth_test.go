package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/anilcharles/speklo/server/api/internal/config"
)

// fakeGitHub answers the OAuth and REST endpoints Speklo calls.
func fakeGitHub(t *testing.T, orgs []string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code") != "the-code" || r.Form.Get("code_verifier") == "" {
			http.Error(w, "bad code or missing PKCE verifier", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"gho_test","token_type":"bearer","scope":"read:user,user:email,read:org"}`))
	})
	api := func(path string, body any) {
		mux.HandleFunc("/api/v3"+path, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer gho_test" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(body)
		})
	}
	api("/user", map[string]any{"id": 42, "login": "octocat", "name": "Mona Octocat"})
	api("/user/emails", []map[string]any{
		{"email": "old@example.com", "primary": false, "verified": true},
		{"email": "mona@acme.dev", "primary": true, "verified": true},
	})
	var orgList []map[string]any
	for _, o := range orgs {
		orgList = append(orgList, map[string]any{"login": o})
	}
	api("/user/orgs", orgList)
	api("/user/teams", []map[string]any{{"slug": "platform", "organization": map[string]any{"login": "acme"}}})
	return httptest.NewServer(mux)
}

func githubCfg(base string, allowed ...string) config.AuthProvider {
	return config.AuthProvider{
		ID: "github", Type: "github", Name: "GitHub", ClientID: "cid", ClientSecret: "secret",
		GitHubURL: base, GitHubAPI: base + "/api/v3", Scopes: []string{"read:user", "user:email", "read:org"},
		AllowedOrgs: allowed,
	}
}

// signIn runs Begin and Finish against the fake provider, like a browser would.
func signIn(t *testing.T, m *Manager, query string) (*Result, error) {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := m.Begin(rec, httptest.NewRequest("GET", "/auth/login?"+query, nil), false); err != nil {
		return nil, err
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil || rec.Code != http.StatusFound {
		t.Fatalf("Begin: %d %v", rec.Code, err)
	}
	if loc.Query().Get("code_challenge_method") != "S256" || loc.Query().Get("client_id") != "cid" {
		t.Fatalf("authorize URL is missing PKCE or client id: %s", loc)
	}
	cb := httptest.NewRequest("GET", "/auth/callback?code=the-code&state="+url.QueryEscape(loc.Query().Get("state")), nil)
	for _, c := range rec.Result().Cookies() {
		cb.AddCookie(c)
	}
	return m.Finish(httptest.NewRecorder(), cb)
}

func TestGitHubSignIn(t *testing.T) {
	gh := fakeGitHub(t, []string{"acme"})
	defer gh.Close()
	m, err := New(context.Background(), []config.AuthProvider{githubCfg(gh.URL, "acme")}, "http://speklo.test/auth/callback", []byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	ch := strings.Repeat("a", 43)
	res, err := signIn(t, m, "provider=github&desktop_port=50123&desktop_challenge="+ch)
	if err != nil {
		t.Fatal(err)
	}
	c := res.Claims
	if res.Provider != "github" || c.Subject != "github:42" || c.Email != "mona@acme.dev" || c.Username != "octocat" || c.Name != "Mona Octocat" {
		t.Errorf("claims: %+v", c)
	}
	if !reflect.DeepEqual(c.Groups, []string{"acme", "acme/platform"}) {
		t.Errorf("groups: %v", c.Groups)
	}
	if res.DesktopPort != 50123 || res.DesktopChallenge != ch {
		t.Errorf("desktop state lost: %+v", res)
	}
}

func TestGitHubOrgRestriction(t *testing.T) {
	gh := fakeGitHub(t, []string{"someone-else"})
	defer gh.Close()
	m, _ := New(context.Background(), []config.AuthProvider{githubCfg(gh.URL, "acme")}, "http://speklo.test/auth/callback", []byte(strings.Repeat("k", 32)))
	if _, err := signIn(t, m, "provider=github"); err == nil || !strings.Contains(err.Error(), "acme") {
		t.Fatalf("expected the org restriction to refuse, got %v", err)
	}
}

func TestChoosingAProvider(t *testing.T) {
	gh := fakeGitHub(t, nil)
	defer gh.Close()
	two := []config.AuthProvider{githubCfg(gh.URL), {ID: "okta", Type: "oidc", Name: "Okta", Issuer: "https://okta.invalid", ClientID: "x"}}
	m, _ := New(context.Background(), two, "http://speklo.test/auth/callback", []byte(strings.Repeat("k", 32)))
	if got := m.Providers(); len(got) != 2 || got[0].ID != "github" || got[1].Name != "Okta" {
		t.Errorf("providers: %+v", got)
	}
	// With several providers, the caller has to choose one.
	if err := m.Begin(httptest.NewRecorder(), httptest.NewRequest("GET", "/auth/login", nil), false); err == nil {
		t.Error("Begin without ?provider= should fail when there are several")
	}
	if err := m.Begin(httptest.NewRecorder(), httptest.NewRequest("GET", "/auth/login?provider=nope", nil), false); err == nil {
		t.Error("unknown provider should fail")
	}
	// A tampered state cookie is refused.
	req := httptest.NewRequest("GET", "/auth/callback?code=x&state=y", nil)
	req.AddCookie(&http.Cookie{Name: stateCookie, Value: "eyJwIjoiZ2l0aHViIn0.bm9wZQ"})
	if _, err := m.Finish(httptest.NewRecorder(), req); err == nil {
		t.Error("tampered state should fail")
	}
}
