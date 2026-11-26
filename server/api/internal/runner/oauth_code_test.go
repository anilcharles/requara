package runner

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAuthorizeURLHasPKCE(t *testing.T) {
	a := Auth{Type: "oauth2", GrantType: "authorization_code", AuthURL: "https://id.example.com/authorize?prompt=login", ClientID: "{{cid}}", Scope: "openid profile", Audience: "api://orders"}
	raw, err := AuthorizeURL(a, map[string]string{"cid": "app-1"}, "https://requara.example.com/oauth2/callback", "st4te", "verifier-abc")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	q := u.Query()
	want := map[string]string{
		"response_type": "code", "client_id": "app-1", "redirect_uri": "https://requara.example.com/oauth2/callback",
		"state": "st4te", "scope": "openid profile", "audience": "api://orders", "prompt": "login",
		"code_challenge": PKCEChallenge("S256", "verifier-abc"), "code_challenge_method": "S256",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	// RFC 7636 appendix B test vector.
	if got := PKCEChallenge("S256", "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Errorf("S256 challenge = %s", got)
	}
	a.PKCE = "off"
	raw, _ = AuthorizeURL(a, map[string]string{"cid": "app-1"}, "https://x/cb", "s", "v")
	if strings.Contains(raw, "code_challenge") {
		t.Error("PKCE off should leave out the challenge")
	}
	if _, err := AuthorizeURL(Auth{ClientID: "x"}, nil, "https://x/cb", "s", "v"); err == nil {
		t.Error("a missing authorization URL should be an error")
	}
}

func TestAuthorizationCodeExchangeAndRefresh(t *testing.T) {
	refreshes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = r.ParseForm()
			f := r.Form
			switch f.Get("grant_type") {
			case "authorization_code":
				// A public client: no secret, so client_id comes in the body.
				if f.Get("code") != "the-code" || f.Get("code_verifier") != "v-123" || f.Get("client_id") != "app" || f.Get("redirect_uri") != "https://x/cb" {
					http.Error(w, `{"error":"invalid_grant","error_description":"bad code"}`, 400)
					return
				}
				fmt.Fprint(w, `{"access_token":"at-1","refresh_token":"rt-1","expires_in":3600,"token_type":"Bearer"}`)
			case "refresh_token":
				refreshes++
				if f.Get("refresh_token") != "rt-1" {
					http.Error(w, `{"error":"invalid_grant"}`, 400)
					return
				}
				fmt.Fprint(w, `{"access_token":"at-2","expires_in":3600}`)
			}
		default:
			fmt.Fprint(w, r.Header.Get("Authorization"))
		}
	}))
	defer srv.Close()
	a := Auth{Type: "oauth2", GrantType: "authorization_code", AuthURL: srv.URL + "/authorize", TokenURL: srv.URL + "/token", ClientID: "app"}
	r := &Runner{AllowPrivateNetwork: true}

	ts, err := r.ExchangeCode(context.Background(), a, nil, "the-code", "v-123", "https://x/cb")
	if err != nil {
		t.Fatal(err)
	}
	if ts.AccessToken != "at-1" || ts.RefreshToken != "rt-1" || ts.ExpiresAt < time.Now().Unix()+3000 {
		t.Fatalf("token set = %+v", ts)
	}
	if _, err := r.ExchangeCode(context.Background(), a, nil, "wrong", "v-123", "https://x/cb"); err == nil || !strings.Contains(err.Error(), "bad code") {
		t.Fatalf("a bad code should fail with the server's reason, got %v", err)
	}

	// A fresh token is sent as is.
	a.Token, a.RefreshToken, a.ExpiresAt = ts.AccessToken, ts.RefreshToken, ts.ExpiresAt
	if res := send(t, Spec{Method: "GET", URL: srv.URL + "/api", Auth: a}, nil); res.Body != "Bearer at-1" {
		t.Fatalf("got %q %q", res.Body, res.Error)
	}
	// An expired one is refreshed, once, then reused.
	a.ExpiresAt = time.Now().Unix() - 10
	for i := 0; i < 2; i++ {
		if res := send(t, Spec{Method: "GET", URL: srv.URL + "/api", Auth: a}, nil); res.Body != "Bearer at-2" {
			t.Fatalf("got %q %q", res.Body, res.Error)
		}
	}
	if refreshes != 1 {
		t.Fatalf("refreshed %d times, want 1", refreshes)
	}
	// No token and no refresh token: say how to get one.
	res := send(t, Spec{Method: "GET", URL: srv.URL + "/api", Auth: Auth{Type: "oauth2", GrantType: "authorization_code"}}, nil)
	if !strings.Contains(res.Error, "Get new access token") {
		t.Fatalf("error = %q", res.Error)
	}
}
