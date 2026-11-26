package runner

import (
	"context"
	"crypto/hmac"
	"crypto/md5" //nolint:gosec // HTTP Digest auth is defined over MD5
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// ----- OAuth 2.0 -----

type cachedToken struct {
	token   string
	expires time.Time
}

var (
	tokenMu    sync.Mutex
	tokenCache = map[string]cachedToken{}
)

// oauth2Token returns an access token: the one given, or one fetched with
// the client-credentials or password grant (cached until it expires).
func (r *Runner) oauth2Token(ctx context.Context, a Auth, vars map[string]string) (string, error) {
	sub := func(s string) string { return Substitute(s, vars) }
	if a.GrantType == "" || a.GrantType == "manual" {
		return sub(a.Token), nil
	}
	tokenURL := sub(a.TokenURL)
	if tokenURL == "" {
		return "", errors.New("OAuth 2.0: set the access token URL")
	}
	form := url.Values{"grant_type": {a.GrantType}}
	if s := sub(a.Scope); s != "" {
		form.Set("scope", s)
	}
	switch a.GrantType {
	case "client_credentials":
	case "password":
		form.Set("username", sub(a.Username))
		form.Set("password", sub(a.Password))
	default:
		return "", fmt.Errorf("OAuth 2.0: grant type %q isn't supported (use client_credentials, password, or paste a token)", a.GrantType)
	}
	clientID, secret := sub(a.ClientID), sub(a.ClientSecret)
	if a.ClientAuth == "body" {
		form.Set("client_id", clientID)
		form.Set("client_secret", secret)
	}
	key := tokenURL + "\x00" + form.Encode() + "\x00" + clientID + "\x00" + secret
	tokenMu.Lock()
	if c, ok := tokenCache[key]; ok && time.Now().Before(c.expires) {
		tokenMu.Unlock()
		return c.token, nil
	}
	tokenMu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("OAuth 2.0 token URL: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if a.ClientAuth != "body" {
		req.SetBasicAuth(url.QueryEscape(clientID), url.QueryEscape(secret))
	}
	resp, err := (&http.Client{Transport: r.roundTripper(true)}).Do(req)
	if err != nil {
		return "", fmt.Errorf("OAuth 2.0 token request failed: %s", friendlyErr(err, 20*time.Second))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || resp.StatusCode >= 400 || tok.AccessToken == "" {
		msg := strings.TrimSpace(tok.Error + " " + tok.Description)
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("OAuth 2.0 token request: %s", msg)
	}
	exp := time.Duration(tok.ExpiresIn) * time.Second
	if exp <= 0 {
		exp = 5 * time.Minute
	}
	tokenMu.Lock()
	tokenCache[key] = cachedToken{tok.AccessToken, time.Now().Add(exp - 30*time.Second)}
	tokenMu.Unlock()
	return tok.AccessToken, nil
}

// ----- JWT -----

// signJWT makes an HS256/384/512 token from the payload (JSON) and secret.
// iat is added when missing.
func signJWT(alg, secret, payload string) (string, error) {
	var h func() hash.Hash
	switch strings.ToUpper(alg) {
	case "", "HS256":
		alg, h = "HS256", sha256.New
	case "HS384":
		alg, h = "HS384", sha512.New384
	case "HS512":
		alg, h = "HS512", sha512.New
	default:
		return "", fmt.Errorf("JWT: algorithm %s isn't supported (HS256, HS384, HS512)", alg)
	}
	claims := map[string]any{}
	if strings.TrimSpace(payload) != "" {
		if err := json.Unmarshal([]byte(payload), &claims); err != nil {
			return "", fmt.Errorf("JWT payload isn't valid JSON: %w", err)
		}
	}
	if _, ok := claims["iat"]; !ok {
		claims["iat"] = time.Now().Unix()
	}
	enc := base64.RawURLEncoding
	head, _ := json.Marshal(map[string]string{"alg": alg, "typ": "JWT"})
	body, _ := json.Marshal(claims)
	unsigned := enc.EncodeToString(head) + "." + enc.EncodeToString(body)
	m := hmac.New(h, []byte(secret))
	m.Write([]byte(unsigned))
	return unsigned + "." + enc.EncodeToString(m.Sum(nil)), nil
}

// ----- AWS Signature Version 4 -----

func sha256Hex(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// awsEscape percent-encodes like AWS wants (RFC 3986, spaces as %20).
func awsEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// signAWS adds AWS SigV4 headers to req (body is the exact payload).
func signAWS(req *http.Request, body []byte, accessKey, secretKey, sessionToken, region, service string, now time.Time) {
	amzDate := now.UTC().Format("20060102T150405Z")
	day := amzDate[:8]
	payloadHash := sha256Hex(body)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", sessionToken)
	}
	host := req.URL.Host
	// Canonical headers: host plus every x-amz-* and content-type header.
	names := []string{"host"}
	values := map[string]string{"host": host}
	for k, v := range req.Header {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-amz-") || lk == "content-type" {
			names = append(names, lk)
			values[lk] = strings.Join(v, ",")
		}
	}
	sort.Strings(names)
	var canonHeaders strings.Builder
	for _, n := range names {
		canonHeaders.WriteString(n + ":" + strings.TrimSpace(values[n]) + "\n")
	}
	signed := strings.Join(names, ";")
	// Canonical query: sorted, AWS-escaped.
	q := req.URL.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var pairs []string
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			pairs = append(pairs, awsEscape(k)+"="+awsEscape(v))
		}
	}
	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	canonical := strings.Join([]string{req.Method, path, strings.Join(pairs, "&"), canonHeaders.String(), signed, payloadHash}, "\n")
	scope := day + "/" + region + "/" + service + "/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonical))
	key := hmacSHA256(hmacSHA256(hmacSHA256(hmacSHA256([]byte("AWS4"+secretKey), day), region), service), "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(key, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+accessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

// ----- HTTP Digest -----

// digestAuthorization answers a WWW-Authenticate: Digest challenge (RFC 7616,
// MD5 and SHA-256, qop=auth).
func digestAuthorization(challenge, method, uri, username, password string) (string, error) {
	params := map[string]string{}
	rest, ok := strings.CutPrefix(strings.TrimSpace(challenge), "Digest ")
	if !ok {
		return "", errors.New("the server didn't offer Digest authentication")
	}
	for _, part := range splitParams(rest) {
		k, v, _ := strings.Cut(part, "=")
		params[strings.ToLower(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	var h func() hash.Hash
	switch strings.ToUpper(params["algorithm"]) {
	case "", "MD5":
		h = md5.New
	case "SHA-256":
		h = sha256.New
	default:
		return "", fmt.Errorf("Digest algorithm %s isn't supported", params["algorithm"])
	}
	sum := func(s string) string { x := h(); x.Write([]byte(s)); return hex.EncodeToString(x.Sum(nil)) }
	ha1 := sum(username + ":" + params["realm"] + ":" + password)
	ha2 := sum(method + ":" + uri)
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	cnonce := hex.EncodeToString(b)
	nc := "00000001"
	var response string
	qop := ""
	if strings.Contains(params["qop"], "auth") {
		qop = "auth"
		response = sum(ha1 + ":" + params["nonce"] + ":" + nc + ":" + cnonce + ":auth:" + ha2)
	} else {
		response = sum(ha1 + ":" + params["nonce"] + ":" + ha2)
	}
	out := fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`, username, params["realm"], params["nonce"], uri, response)
	if params["algorithm"] != "" {
		out += ", algorithm=" + params["algorithm"]
	}
	if params["opaque"] != "" {
		out += fmt.Sprintf(`, opaque="%s"`, params["opaque"])
	}
	if qop != "" {
		out += fmt.Sprintf(`, qop=auth, nc=%s, cnonce="%s"`, nc, cnonce)
	}
	return out, nil
}

// splitParams splits a=b, c="d, e" on commas outside quotes.
func splitParams(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			cur.WriteRune(r)
		case r == ',' && !quoted:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
