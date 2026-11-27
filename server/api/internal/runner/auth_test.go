package runner

import (
	"context"
	"crypto/md5" //nolint:gosec // test server for Digest
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func send(t *testing.T, s Spec, vars map[string]string) *Result {
	t.Helper()
	r := &Runner{AllowPrivateNetwork: true}
	if vars == nil {
		vars = map[string]string{}
	}
	return r.Send(context.Background(), s, vars, nil)
}

func TestOAuth2ClientCredentials(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			calls++
			_ = r.ParseForm()
			id, secret, _ := r.BasicAuth()
			if r.Form.Get("grant_type") != "client_credentials" || id != "cid" || secret != "s3cret" || r.Form.Get("scope") != "read" {
				http.Error(w, `{"error":"invalid_client"}`, 401)
				return
			}
			fmt.Fprint(w, `{"access_token":"tok-123","expires_in":3600}`)
		default:
			fmt.Fprint(w, r.Header.Get("Authorization"))
		}
	}))
	defer srv.Close()
	spec := Spec{Method: "GET", URL: srv.URL + "/api", Auth: Auth{Type: "oauth2", GrantType: "client_credentials", TokenURL: srv.URL + "/token", ClientID: "cid", ClientSecret: "{{secret}}", Scope: "read"}}
	for i := 0; i < 2; i++ {
		res := send(t, spec, map[string]string{"secret": "s3cret"})
		if res.Error != "" || res.Body != "Bearer tok-123" {
			t.Fatalf("got %q %q", res.Body, res.Error)
		}
		for _, h := range res.ReqHeaders {
			if h.Key == "Authorization" && strings.Contains(h.Value, "tok-123") {
				t.Fatal("the token should be redacted in what's stored")
			}
		}
	}
	if calls != 1 {
		t.Fatalf("token fetched %d times; want it cached", calls)
	}
}

func TestJWTAndMultipartAndBinary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := map[string]any{"auth": r.Header.Get("Authorization"), "ct": r.Header.Get("Content-Type")}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			_ = r.ParseMultipartForm(1 << 20)
			out["name"] = r.FormValue("name")
			f, h, _ := r.FormFile("doc")
			b, _ := io.ReadAll(f)
			out["file"], out["filename"] = string(b), h.Filename
		} else {
			b, _ := io.ReadAll(r.Body)
			out["body"] = string(b)
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	res := send(t, Spec{Method: "GET", URL: srv.URL, Auth: Auth{Type: "jwt", Secret: "k", Payload: `{"sub":"ana"}`}}, nil)
	var got map[string]any
	_ = json.Unmarshal([]byte(res.Body), &got)
	tok := strings.TrimPrefix(got["auth"].(string), "Bearer ")
	parts := strings.Split(tok, ".")
	claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if len(parts) != 3 || !strings.Contains(string(claims), `"sub":"ana"`) {
		t.Fatalf("jwt: %q", tok)
	}

	res = send(t, Spec{Method: "POST", URL: srv.URL, BodyMode: "form-data", FormData: []FormField{
		{Key: "name", Value: "{{who}}", Enabled: true},
		{Key: "doc", Type: "file", FileName: "a.txt", Value: base64.StdEncoding.EncodeToString([]byte("hello file")), Enabled: true},
		{Key: "skipped", Value: "x", Enabled: false},
	}}, map[string]string{"who": "ana"})
	_ = json.Unmarshal([]byte(res.Body), &got)
	if got["name"] != "ana" || got["file"] != "hello file" || got["filename"] != "a.txt" {
		t.Fatalf("multipart: %v %s", got, res.Error)
	}

	res = send(t, Spec{Method: "PUT", URL: srv.URL, BodyMode: "binary", BodyFile: &File{Name: "x.bin", ContentType: "image/png", Data: base64.StdEncoding.EncodeToString([]byte{1, 2, 3})}}, nil)
	_ = json.Unmarshal([]byte(res.Body), &got)
	if got["ct"] != "image/png" || got["body"] != "\x01\x02\x03" {
		t.Fatalf("binary: %v", got)
	}
}

func TestDigest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := r.Header.Get("Authorization")
		if !strings.HasPrefix(a, "Digest ") {
			w.Header().Set("WWW-Authenticate", `Digest realm="test", qop="auth", nonce="abc123", opaque="op"`)
			w.WriteHeader(401)
			return
		}
		p := map[string]string{}
		for _, part := range splitParams(strings.TrimPrefix(a, "Digest ")) {
			k, v, _ := strings.Cut(part, "=")
			p[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
		}
		h := func(s string) string { x := md5.Sum([]byte(s)); return hex.EncodeToString(x[:]) } //nolint:gosec
		want := h(h("ana:test:pw") + ":abc123:" + p["nc"] + ":" + p["cnonce"] + ":auth:" + h("GET:"+r.URL.RequestURI()))
		if p["response"] != want {
			w.WriteHeader(403)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()
	res := send(t, Spec{Method: "GET", URL: srv.URL + "/secret?x=1", Auth: Auth{Type: "digest", Username: "ana", Password: "pw"}}, nil)
	if res.Status != 200 || res.Body != "ok" {
		t.Fatalf("digest: %d %q %s", res.Status, res.Body, res.Error)
	}
}

func TestAWSSigV4KnownVector(t *testing.T) {
	// AWS's documented example (IAM ListUsers, us-east-1).
	req, _ := http.NewRequest("GET", "https://iam.amazonaws.com/?Action=ListUsers&Version=2010-05-08", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	signAWS(req, nil, "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "", "us-east-1", "iam", mustTime("20150830T123600Z"))
	a := req.Header.Get("Authorization")
	if !strings.HasPrefix(a, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/iam/aws4_request, SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date, Signature=") {
		t.Fatalf("authorization: %s", a)
	}
}

func TestCookiesRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "s1", Path: "/"})
		fmt.Fprint(w, r.Header.Get("Cookie"))
	}))
	defer srv.Close()
	res := send(t, Spec{Method: "GET", URL: srv.URL}, nil)
	if len(res.Cookies) != 1 || res.Cookies[0].Name != "sid" {
		t.Fatalf("cookies: %+v", res.Cookies)
	}
	res = send(t, Spec{Method: "GET", URL: srv.URL + "/x", Cookies: res.Cookies}, nil)
	if res.Body != "sid=s1" {
		t.Fatalf("cookie header: %q", res.Body)
	}
}

func mustTime(s string) time.Time {
	t, err := time.Parse("20060102T150405Z", s)
	if err != nil {
		panic(err)
	}
	return t
}

// Auth is applied after the pre-request script, so a script can set its token.
func TestAuthAfterPreRequestScript(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Header.Get("Authorization"), "|", r.URL.Query().Get("k"))
	}))
	defer srv.Close()
	res := send(t, Spec{Method: "GET", URL: srv.URL, Auth: Auth{Type: "bearer", Token: "{{tok}}"}, PreScript: `env.set("tok", "from-script")`}, nil)
	if res.Body != "Bearer from-script|" {
		t.Fatalf("bearer: %q %s", res.Body, res.Error)
	}
	res = send(t, Spec{Method: "GET", URL: srv.URL, Auth: Auth{Type: "apikey", Key: "k", Value: "{{v}}", In: "query"}, PreScript: `env.set("v", "42")`}, nil)
	if res.Body != "|42" {
		t.Fatalf("api key in query: %q", res.Body)
	}
}
