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

// oauth2Token returns an access token: the one given, one fetched with the
// client-credentials or password grant (cached until it expires), or for the
// authorization-code grant the one got by signing in, refreshed when it has
// expired and a refresh token is there.
func (r *Runner) oauth2Token(ctx context.Context, a Auth, vars map[string]string) (string, error) {
	sub := func(s string) string { return Substitute(s, vars) }
	switch a.GrantType {
	case "", "manual":
		return sub(a.Token), nil
	case "authorization_code":
		tok := sub(a.Token)
		fresh := a.ExpiresAt == 0 || time.Now().Unix() < a.ExpiresAt-30
		if tok != "" && fresh {
			return tok, nil
		}
		if refresh := sub(a.RefreshToken); refresh != "" {
			ts, err := r.cachedToken(ctx, a, vars, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
			if err != nil {
				return "", fmt.Errorf("%w. Sign in again with Get new access token", err)
			}
			return ts.AccessToken, nil
		}
		if tok != "" {
			return "", errors.New("OAuth 2.0: the access token has expired. Get a new one in the Auth tab")
		}
		return "", errors.New("OAuth 2.0: there's no access token yet. Click Get new access token in the Auth tab to sign in")
	case "client_credentials":
		form := url.Values{"grant_type": {a.GrantType}}
		return r.grantToken(ctx, a, vars, form)
	case "password":
		form := url.Values{"grant_type": {a.GrantType}, "username": {sub(a.Username)}, "password": {sub(a.Password)}}
		return r.grantToken(ctx, a, vars, form)
	}
	return "", fmt.Errorf("OAuth 2.0: grant type %q isn't supported", a.GrantType)
}

func (r *Runner) grantToken(ctx context.Context, a Auth, vars map[string]string, form url.Values) (string, error) {
	if s := Substitute(a.Scope, vars); s != "" {
		form.Set("scope", s)
	}
	ts, err := r.cachedToken(ctx, a, vars, form)
	if err != nil {
		return "", err
	}
	return ts.AccessToken, nil
}

// cachedToken asks the token URL, reusing an earlier answer to the same
// question until it expires.
func (r *Runner) cachedToken(ctx context.Context, a Auth, vars map[string]string, form url.Values) (*TokenSet, error) {
	tokenURL, clientID, secret := Substitute(a.TokenURL, vars), Substitute(a.ClientID, vars), Substitute(a.ClientSecret, vars)
	key := tokenURL + "\x00" + form.Encode() + "\x00" + clientID + "\x00" + secret
	tokenMu.Lock()
	if c, ok := tokenCache[key]; ok && time.Now().Before(c.expires) {
		tokenMu.Unlock()
		return &TokenSet{AccessToken: c.token}, nil
	}
	tokenMu.Unlock()
	ts, err := r.requestToken(ctx, a, vars, form)
	if err != nil {
		return nil, err
	}
	exp := 5 * time.Minute
	if ts.ExpiresAt > 0 {
		exp = time.Until(time.Unix(ts.ExpiresAt, 0))
	}
	tokenMu.Lock()
	tokenCache[key] = cachedToken{ts.AccessToken, time.Now().Add(exp - 30*time.Second)}
	tokenMu.Unlock()
	return ts, nil
}

// TokenSet is what an OAuth 2.0 token endpoint answered.
type TokenSet struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken,omitempty"`
	TokenType    string `json:"tokenType,omitempty"`
	Scope        string `json:"scope,omitempty"`
	IDToken      string `json:"idToken,omitempty"`
	ExpiresAt    int64  `json:"expiresAt,omitempty"` // unix seconds; 0 = not said
}

// requestToken POSTs form to the token URL with the client's credentials.
func (r *Runner) requestToken(ctx context.Context, a Auth, vars map[string]string, form url.Values) (*TokenSet, error) {
	tokenURL := Substitute(a.TokenURL, vars)
	if tokenURL == "" {
		return nil, errors.New("OAuth 2.0: set the access token URL")
	}
	clientID, secret := Substitute(a.ClientID, vars), Substitute(a.ClientSecret, vars)
	// Public clients (PKCE, no secret) send their ID in the body.
	if a.ClientAuth == "body" || secret == "" {
		if clientID != "" {
			form.Set("client_id", clientID)
		}
		if secret != "" {
			form.Set("client_secret", secret)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("OAuth 2.0 token URL: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if a.ClientAuth != "body" && secret != "" {
		req.SetBasicAuth(url.QueryEscape(clientID), url.QueryEscape(secret))
	}
	resp, err := (&http.Client{Transport: r.roundTripper(true)}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("OAuth 2.0 token request failed: %s", friendlyErr(err, 20*time.Second))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Error        string `json:"error"`
		Description  string `json:"error_description"`
	}
	jerr := json.Unmarshal(body, &tok)
	if jerr != nil && resp.StatusCode < 400 {
		// Some older servers (GitHub without Accept) answer form-encoded.
		if v, perr := url.ParseQuery(string(body)); perr == nil && v.Get("access_token") != "" {
			tok.AccessToken, tok.RefreshToken, tok.TokenType, tok.Scope = v.Get("access_token"), v.Get("refresh_token"), v.Get("token_type"), v.Get("scope")
			jerr = nil
		}
	}
	if jerr != nil || resp.StatusCode >= 400 || tok.AccessToken == "" {
		msg := strings.TrimSpace(tok.Error + " " + tok.Description)
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("OAuth 2.0 token request: %s", msg)
	}
	ts := &TokenSet{AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, TokenType: tok.TokenType, Scope: tok.Scope, IDToken: tok.IDToken}
	if tok.ExpiresIn > 0 {
		ts.ExpiresAt = time.Now().Unix() + tok.ExpiresIn
	}
	return ts, nil
}

// PKCEChallenge turns a code verifier into the code_challenge for method
// ("S256", "plain"; "off" or anything else means no challenge).
func PKCEChallenge(method, verifier string) string {
	switch strings.ToLower(method) {
	case "", "s256":
		sum := sha256.Sum256([]byte(verifier))
		return base64.RawURLEncoding.EncodeToString(sum[:])
	case "plain":
		return verifier
	}
	return ""
}

// AuthorizeURL is where the browser goes to sign in for the
// authorization-code grant. codeVerifier is kept by the caller and given
// back to ExchangeCode.
func AuthorizeURL(a Auth, vars map[string]string, redirectURI, state, codeVerifier string) (string, error) {
	sub := func(s string) string { return Substitute(s, vars) }
	u, err := url.Parse(sub(a.AuthURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("OAuth 2.0: set the authorization URL, for example https://auth.example.com/authorize")
	}
	if sub(a.ClientID) == "" {
		return "", errors.New("OAuth 2.0: set the client ID")
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", sub(a.ClientID))
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	if s := sub(a.Scope); s != "" {
		q.Set("scope", s)
	}
	if aud := sub(a.Audience); aud != "" {
		q.Set("audience", aud)
	}
	if ch := PKCEChallenge(a.PKCE, codeVerifier); ch != "" {
		q.Set("code_challenge", ch)
		if strings.EqualFold(a.PKCE, "plain") {
			q.Set("code_challenge_method", "plain")
		} else {
			q.Set("code_challenge_method", "S256")
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// ExchangeCode trades the code the sign-in page sent back for tokens.
func (r *Runner) ExchangeCode(ctx context.Context, a Auth, vars map[string]string, code, codeVerifier, redirectURI string) (*TokenSet, error) {
	if code == "" {
		return nil, errors.New("OAuth 2.0: the sign-in page didn't send a code")
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI}}
	if PKCEChallenge(a.PKCE, codeVerifier) != "" {
		form.Set("code_verifier", codeVerifier)
	}
	return r.requestToken(ctx, a, vars, form)
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
