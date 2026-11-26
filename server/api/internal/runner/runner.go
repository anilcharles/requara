// Package runner sends a request on the user's behalf: fills in {{variables}},
// runs the pre-request script, makes the HTTP call with per-phase timings
// (httptrace), then runs the tests script. Secrets are only ever in memory here.
package runner

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/anilcharles/requara/server/api/internal/sandbox"
	"github.com/google/uuid"
)

const maxBody = 10 << 20 // 10 MB read limit

type KV struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
}

type Auth struct {
	Type     string `json:"type"` // inherit | none | bearer | basic | apikey | oauth2 | jwt | awsv4 | digest
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Key      string `json:"key,omitempty"`
	Value    string `json:"value,omitempty"`
	In       string `json:"in,omitempty"` // header | query

	// OAuth 2.0: a pasted token (grantType manual), one fetched here, or one
	// got by signing in in the browser (authorization_code, with PKCE).
	GrantType    string `json:"grantType,omitempty"` // manual | client_credentials | password | authorization_code
	AuthURL      string `json:"authUrl,omitempty"`   // authorization_code: where people sign in
	TokenURL     string `json:"tokenUrl,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	Scope        string `json:"scope,omitempty"`
	ClientAuth   string `json:"clientAuth,omitempty"` // header (Basic, default) | body
	PKCE         string `json:"pkce,omitempty"`       // authorization_code: S256 (default) | plain | off
	Audience     string `json:"audience,omitempty"`   // optional extra authorize parameter (Auth0, Okta)
	// authorization_code: the token got by signing in. Token holds the access token.
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresAt    int64  `json:"expiresAt,omitempty"` // unix seconds; 0 = unknown

	// AWS Signature v4.
	AccessKey    string `json:"accessKey,omitempty"`
	SecretKey    string `json:"secretKey,omitempty"`
	SessionToken string `json:"sessionToken,omitempty"`
	Region       string `json:"region,omitempty"`
	Service      string `json:"service,omitempty"`

	// JWT bearer, signed here.
	Algorithm    string `json:"algorithm,omitempty"` // HS256 | HS384 | HS512
	Secret       string `json:"secret,omitempty"`
	Payload      string `json:"payload,omitempty"`      // JSON claims
	HeaderPrefix string `json:"headerPrefix,omitempty"` // default "Bearer"
}

// FormField is one multipart/form-data field; files carry base64 data.
type FormField struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Type        string `json:"type,omitempty"` // text | file
	FileName    string `json:"fileName,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Enabled     bool   `json:"enabled"`
}

// File is a binary request body (base64 data).
type File struct {
	Name        string `json:"name,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Data        string `json:"data,omitempty"`
}

// Cookie is one stored cookie.
type Cookie struct {
	Domain   string     `json:"domain"`
	Path     string     `json:"path"`
	Name     string     `json:"name"`
	Value    string     `json:"value"`
	Expires  *time.Time `json:"expires,omitempty"`
	Secure   bool       `json:"secure,omitempty"`
	HTTPOnly bool       `json:"httpOnly,omitempty"`
}

type Settings struct {
	FollowRedirects *bool `json:"followRedirects,omitempty"`
	VerifySSL       *bool `json:"verifySSL,omitempty"`
	EncodeURL       *bool `json:"encodeURL,omitempty"`
	TimeoutMs       int   `json:"timeoutMs,omitempty"`

	// Network. Each takes {{variables}}, so PEMs can live in secret variables.
	Proxy      string `json:"proxy,omitempty"`      // http://, https:// or socks5:// URL; empty = the server's own
	ClientCert string `json:"clientCert,omitempty"` // PEM certificate (chain) for mutual TLS
	ClientKey  string `json:"clientKey,omitempty"`  // its unencrypted PEM private key
	CACert     string `json:"caCert,omitempty"`     // PEM certificates to trust as well as the system's
}

func (s Settings) follow() bool { return s.FollowRedirects == nil || *s.FollowRedirects }
func (s Settings) verify() bool { return s.VerifySSL == nil || *s.VerifySSL }
func (s Settings) encode() bool { return s.EncodeURL == nil || *s.EncodeURL }

type Spec struct {
	Method     string      `json:"method"`
	URL        string      `json:"url"`
	Params     []KV        `json:"params"`
	Headers    []KV        `json:"headers"`
	BodyMode   string      `json:"bodyMode"` // none | json | raw | form-urlencoded | form-data | graphql | binary
	Body       string      `json:"body"`
	FormData   []FormField `json:"formData,omitempty"`
	BodyFile   *File       `json:"bodyFile,omitempty"`
	Cookies    []Cookie    `json:"cookies,omitempty"` // sent when they match the URL
	Auth       Auth        `json:"auth"`
	PreScript  string      `json:"preScript"`
	PostScript string      `json:"postScript"`
	Settings   Settings    `json:"settings"`
	// Scopes keeps Postman's variable scopes apart for scripts; vars passed
	// to Send must then be what they resolve to. Nil: one set of variables.
	Scopes *sandbox.Scopes `json:"scopes,omitempty"`
	Info   sandbox.Info    `json:"info"`
}

type Phase struct {
	Name    string  `json:"name"`
	StartMs float64 `json:"startMs"`
	Ms      float64 `json:"ms"`
}

type Result struct {
	Status      int                  `json:"status"`
	StatusText  string               `json:"statusText"`
	DurationMs  int64                `json:"durationMs"`
	SizeBytes   int                  `json:"sizeBytes"`
	ReqHeaders  []KV                 `json:"requestHeaders"` // secrets redacted
	ReqBody     string               `json:"requestBody"`    // as sent, secrets redacted, capped
	ResHeaders  []KV                 `json:"headers"`
	Body        string               `json:"body"`
	Truncated   bool                 `json:"truncated"`
	Timeline    []Phase              `json:"timeline"`
	Tests       []sandbox.TestResult `json:"tests"`
	Logs        []string             `json:"logs"`
	ScriptError string               `json:"scriptError,omitempty"`
	Error       string               `json:"error,omitempty"`
	URL         string               `json:"url"` // secrets redacted
	Method      string               `json:"method"`
	Cookies     []Cookie             `json:"cookies,omitempty"`     // Set-Cookie the response sent
	Events      []SSEEvent           `json:"events,omitempty"`      // text/event-stream responses
	StreamEnded string               `json:"streamEnded,omitempty"` // closed | timeout | limit
	EnvChanges  map[string]string    `json:"-"`                     // variables scripts set; caller persists them
	// With Spec.Scopes: what pm.globals and pm.collectionVariables set
	// (EnvChanges is then pm.environment's only).
	GlobalChanges     map[string]string `json:"-"`
	CollectionChanges map[string]string `json:"-"`
}

type Runner struct {
	AllowPrivateNetwork bool
	// Transport, when set, sends every request (the browser build uses the
	// browser's fetch; tests can fake the network). Network settings
	// (proxy, certificates) don't apply then.
	Transport http.RoundTripper
	// Proxy is the operator's outbound proxy (the server uses HTTPS_PROXY,
	// HTTP_PROXY and NO_PROXY). Unlike a request's own proxy, it may be on a
	// private network.
	Proxy func(*http.Request) (*url.URL, error)
}

var varRe = regexp.MustCompile(`\{\{\s*([$A-Za-z0-9_.\-]+)\s*\}\}`)

// Substitute replaces {{name}} with vars[name]; {{$uuid}} and {{$timestamp}} are built in.
func Substitute(s string, vars map[string]string) string {
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		name := varRe.FindStringSubmatch(m)[1]
		switch name {
		case "$uuid", "$guid":
			return uuid.NewString()
		case "$timestamp":
			return fmt.Sprint(time.Now().Unix())
		case "$isoTimestamp":
			return time.Now().UTC().Format(time.RFC3339)
		}
		if v, ok := vars[name]; ok {
			return v
		}
		return m
	})
}

// Send runs the full pipeline. vars holds every variable value, including
// decrypted secrets; secretValues lists the ones to redact from what's stored.
func (r *Runner) Send(ctx context.Context, s Spec, vars map[string]string, secretValues []string) *Result {
	// Lists are never nil, so a request that fails early still answers [] for them.
	res := &Result{Method: strings.ToUpper(s.Method), ReqHeaders: []KV{}, ResHeaders: []KV{}, Timeline: []Phase{}, Tests: []sandbox.TestResult{}, Logs: []string{}}
	redact := redactor(secretValues)

	// 1. The request as written: like Postman, the pre-request script sees
	// {{variables}} unfilled, and what it sets is used to fill them.
	req := sandbox.Request{Method: strings.ToUpper(s.Method), URL: s.URL, Headers: map[string]string{}}
	for _, h := range s.Headers {
		if h.Enabled && h.Key != "" {
			req.Headers[h.Key] = h.Value
		}
	}
	if s.BodyMode != "none" && s.BodyMode != "" {
		req.Body = s.Body
	}
	scopes := cloneScopes(s.Scopes)
	envOut, globalsOut, collectionOut := map[string]string{}, map[string]string{}, map[string]string{}
	keep := func(out *sandbox.Output) {
		for k, v := range out.Env {
			envOut[k] = v
		}
		for k, v := range out.Globals {
			globalsOut[k] = v
		}
		for k, v := range out.Collection {
			collectionOut[k] = v
		}
		if scopes == nil {
			// One set of variables: whatever scope a script set, it's that.
			for _, m := range []map[string]string{out.Globals, out.Collection, out.Env, out.Local} {
				for k, v := range m {
					vars[k] = v
				}
			}
			return
		}
		for k, v := range out.Globals {
			scopes.Globals[k] = v
		}
		for k, v := range out.Collection {
			scopes.Collection[k] = v
		}
		for k, v := range out.Env {
			scopes.Environment[k] = v
		}
		for k, v := range out.Local {
			scopes.Data[k] = v // local outranks data for the rest of this send
		}
		for k, v := range ResolveScopes(scopes) {
			vars[k] = v
		}
	}
	sendSub := r.subRequestSender()

	// 2. Pre-request script may change the request and set variables.
	if strings.TrimSpace(s.PreScript) != "" {
		out, err := sandbox.RunWith(ctx, s.PreScript, sandbox.Input{Request: req, Env: vars, Scopes: scopes, Info: s.Info}, sendSub)
		if err != nil {
			res.Error = "Pre-request script failed: " + err.Error()
			return res
		}
		if out.Error != "" {
			res.ScriptError = "Pre-request: " + out.Error
		}
		req = out.Request
		if req.Headers == nil {
			req.Headers = map[string]string{}
		}
		keep(out)
		res.Logs = append(res.Logs, out.Logs...)
	}

	// 3. Fill in variables.
	req.Method = strings.ToUpper(req.Method)
	req.URL = Substitute(req.URL, vars)
	filled := map[string]string{}
	for k, v := range req.Headers {
		filled[Substitute(k, vars)] = Substitute(v, vars)
	}
	req.Headers = filled
	req.Body = Substitute(req.Body, vars)
	u, err := url.Parse(req.URL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		res.URL = redact(req.URL)
		res.Error = "Invalid URL. Include the scheme, for example https://api.example.com"
		return res
	}
	q := u.Query()
	for _, p := range s.Params {
		if p.Enabled && p.Key != "" {
			q.Add(Substitute(p.Key, vars), Substitute(p.Value, vars))
		}
	}
	if s.Settings.encode() {
		u.RawQuery = q.Encode()
	} else {
		u.RawQuery = rawQuery(q)
	}
	req.URL = u.String()
	if s.BodyMode != "none" && s.BodyMode != "" {
		if _, ok := headerLookup(req.Headers, "Content-Type"); !ok {
			switch s.BodyMode {
			case "json", "graphql":
				req.Headers["Content-Type"] = "application/json"
			case "form-urlencoded":
				req.Headers["Content-Type"] = "application/x-www-form-urlencoded"
			}
		}
	}

	// Auth goes on after the pre-request script, like Postman, so scripts
	// can set the variables it uses (a token, a signature).
	switch s.Auth.Type {
	case "bearer":
		req.Headers["Authorization"] = "Bearer " + Substitute(s.Auth.Token, vars)
	case "basic":
		cred := Substitute(s.Auth.Username, vars) + ":" + Substitute(s.Auth.Password, vars)
		req.Headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(cred))
	case "apikey":
		if s.Auth.In == "query" {
			if u, err := url.Parse(req.URL); err == nil {
				qq := u.Query()
				qq.Set(Substitute(s.Auth.Key, vars), Substitute(s.Auth.Value, vars))
				u.RawQuery = qq.Encode()
				req.URL = u.String()
			}
		} else {
			req.Headers[Substitute(s.Auth.Key, vars)] = Substitute(s.Auth.Value, vars)
		}
	case "oauth2":
		tok, err := r.oauth2Token(ctx, s.Auth, vars)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		req.Headers["Authorization"] = "Bearer " + tok
		secretValues = append(secretValues, tok)
	case "jwt":
		tok, err := signJWT(s.Auth.Algorithm, Substitute(s.Auth.Secret, vars), Substitute(s.Auth.Payload, vars))
		if err != nil {
			res.Error = err.Error()
			return res
		}
		if s.Auth.In == "query" {
			if u, err := url.Parse(req.URL); err == nil {
				qq := u.Query()
				qq.Set(Substitute(s.Auth.Key, vars), tok)
				u.RawQuery = qq.Encode()
				req.URL = u.String()
			}
		} else {
			prefix := s.Auth.HeaderPrefix
			if prefix == "" {
				prefix = "Bearer"
			}
			req.Headers["Authorization"] = strings.TrimSpace(prefix + " " + tok)
		}
		secretValues = append(secretValues, tok)
	case "awsv4", "digest":
		secretValues = append(secretValues, Substitute(s.Auth.SecretKey, vars), Substitute(s.Auth.Password, vars))
	}
	redact = redactor(secretValues)
	if _, ok := headerLookup(req.Headers, "Cookie"); !ok {
		if c := cookieHeader(s.Cookies, req.URL, time.Now()); c != "" {
			req.Headers["Cookie"] = c
		}
	}

	// Multipart and binary bodies are built from their parts.
	rawBody := []byte(req.Body)
	switch s.BodyMode {
	case "form-data":
		b, ct, err := multipartBody(s.FormData, vars)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		rawBody = b
		req.Headers["Content-Type"] = ct
		req.Body = fmt.Sprintf("(multipart form, %d bytes)", len(b))
	case "binary":
		if s.BodyFile == nil || s.BodyFile.Data == "" {
			rawBody = nil
			req.Body = ""
			break
		}
		b, err := base64.StdEncoding.DecodeString(s.BodyFile.Data)
		if err != nil {
			res.Error = "The body file couldn't be read"
			return res
		}
		rawBody = b
		if _, ok := headerLookup(req.Headers, "Content-Type"); !ok {
			ct := s.BodyFile.ContentType
			if ct == "" {
				ct = "application/octet-stream"
			}
			req.Headers["Content-Type"] = ct
		}
		req.Body = fmt.Sprintf("(file %s, %d bytes)", s.BodyFile.Name, len(b))
	}

	res.URL = redact(req.URL)
	for k, v := range req.Headers {
		res.ReqHeaders = append(res.ReqHeaders, KV{Key: k, Value: redact(v), Enabled: true})
	}
	sort.Slice(res.ReqHeaders, func(i, j int) bool { return res.ReqHeaders[i].Key < res.ReqHeaders[j].Key })
	if body := req.Body; body != "" {
		const maxLogged = 64 << 10
		if len(body) > maxLogged {
			body = body[:maxLogged] + "\n… (truncated)"
		}
		res.ReqBody = redact(body)
	}

	// 3. Send it, timing each phase.
	timeout := time.Duration(s.Settings.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	build := func() (*http.Request, error) {
		hr, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bytesReader(rawBody))
		if err != nil {
			return nil, err
		}
		for k, v := range req.Headers {
			hr.Header.Set(k, v)
		}
		if hr.Header.Get("User-Agent") == "" {
			hr.Header.Set("User-Agent", "Requara/1.0")
		}
		if s.Auth.Type == "awsv4" {
			signAWS(hr, rawBody, Substitute(s.Auth.AccessKey, vars), Substitute(s.Auth.SecretKey, vars),
				Substitute(s.Auth.SessionToken, vars), Substitute(s.Auth.Region, vars), Substitute(s.Auth.Service, vars), time.Now())
		}
		return hr, nil
	}
	httpReq, err := build()
	if err != nil {
		res.Error = err.Error()
		return res
	}
	t := &tracer{}
	httpReq = httpReq.WithContext(httptrace.WithClientTrace(ctx, t.trace()))
	rt, err := r.requestTransport(ctx, s.Settings, vars, httpReq)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	client := &http.Client{Transport: rt}
	if !s.Settings.follow() {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	t.start = time.Now()
	resp, err := client.Do(httpReq)
	// HTTP Digest: answer the server's challenge and send again.
	if err == nil && s.Auth.Type == "digest" && resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("WWW-Authenticate")
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		authz, derr := digestAuthorization(challenge, req.Method, httpReq.URL.RequestURI(), Substitute(s.Auth.Username, vars), Substitute(s.Auth.Password, vars))
		if derr != nil {
			res.Error = derr.Error()
			return res
		}
		retry, berr := build()
		if berr != nil {
			res.Error = berr.Error()
			return res
		}
		retry.Header.Set("Authorization", authz)
		resp, err = client.Do(retry.WithContext(httptrace.WithClientTrace(ctx, t.trace())))
	}
	if err != nil {
		res.DurationMs = time.Since(t.start).Milliseconds()
		res.Error = friendlyErr(err, timeout)
		res.Timeline = t.phases(time.Now())
		return res
	}
	defer resp.Body.Close()
	var body []byte
	if isEventStream(resp.Header.Get("Content-Type")) {
		// A stream: keep its events; the timeout ends it, as a stop, not a failure.
		body, res.Events, res.StreamEnded, err = readEvents(ctx, resp.Body, t.start)
		res.Truncated = res.StreamEnded == "limit"
	} else {
		body, err = io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
		if len(body) > maxBody {
			body, res.Truncated = body[:maxBody], true
		}
	}
	end := time.Now()
	if err != nil {
		res.Error = "Reading the response failed: " + err.Error()
	}
	res.Status, res.StatusText = resp.StatusCode, http.StatusText(resp.StatusCode)
	res.Cookies = responseCookies(resp)
	res.DurationMs = end.Sub(t.start).Milliseconds()
	res.SizeBytes = len(body)
	res.Body = string(body)
	res.Timeline = t.phases(end)
	for k, vs := range resp.Header {
		for _, v := range vs {
			res.ResHeaders = append(res.ResHeaders, KV{Key: strings.ToLower(k), Value: v, Enabled: true})
		}
	}
	sort.Slice(res.ResHeaders, func(i, j int) bool { return res.ResHeaders[i].Key < res.ResHeaders[j].Key })

	// 4. Tests script.
	if strings.TrimSpace(s.PostScript) != "" {
		hdrs := map[string]string{}
		for _, h := range res.ResHeaders {
			hdrs[h.Key] = h.Value
		}
		out, err := sandbox.RunWith(ctx, s.PostScript, sandbox.Input{
			Request:  req,
			Response: &sandbox.Response{Status: res.Status, Headers: hdrs, Body: res.Body, Time: res.DurationMs},
			Env:      vars,
			Scopes:   scopes,
			Info:     s.Info,
		}, sendSub)
		if err != nil {
			res.ScriptError = "Tests: " + err.Error()
		} else {
			if out.Tests != nil {
				res.Tests = out.Tests
			}
			res.Logs = append(res.Logs, out.Logs...)
			if out.Error != "" {
				res.ScriptError = "Tests: " + out.Error
			}
			keep(out)
		}
	}
	if scopes == nil {
		// One set of variables: every change goes to the environment.
		for _, m := range []map[string]string{globalsOut, collectionOut} {
			for k, v := range m {
				envOut[k] = v
			}
		}
		globalsOut, collectionOut = map[string]string{}, map[string]string{}
	}
	res.GlobalChanges, res.CollectionChanges = globalsOut, collectionOut
	res.EnvChanges = envOut
	return res
}

func cloneScopes(sc *sandbox.Scopes) *sandbox.Scopes {
	if sc == nil {
		return nil
	}
	cp := func(m map[string]string) map[string]string {
		out := make(map[string]string, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	return &sandbox.Scopes{Globals: cp(sc.Globals), Collection: cp(sc.Collection), Environment: cp(sc.Environment), Data: cp(sc.Data)}
}

// ResolveScopes is the variables a request sees: globals, then the
// collection's, then the environment's, then the iteration data.
func ResolveScopes(sc *sandbox.Scopes) map[string]string {
	out := map[string]string{}
	if sc == nil {
		return out
	}
	for _, m := range []map[string]string{sc.Globals, sc.Collection, sc.Environment, sc.Data} {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// subRequestSender sends scripts' pm.sendRequest calls, with the same
// network guard and proxy as Requara's own calls.
func (r *Runner) subRequestSender() sandbox.Sender {
	return func(ctx context.Context, sr sandbox.SubRequest) sandbox.SubResponse {
		u, err := url.Parse(sr.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return sandbox.SubResponse{Error: "pm.sendRequest: invalid URL " + sr.URL}
		}
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		hr, err := http.NewRequestWithContext(ctx, sr.Method, u.String(), bytesReader([]byte(sr.Body)))
		if err != nil {
			return sandbox.SubResponse{Error: err.Error()}
		}
		for k, v := range sr.Headers {
			hr.Header.Set(k, v)
		}
		if hr.Header.Get("User-Agent") == "" {
			hr.Header.Set("User-Agent", "Requara/1.0")
		}
		start := time.Now()
		resp, err := (&http.Client{Transport: r.roundTripper(true)}).Do(hr)
		if err != nil {
			return sandbox.SubResponse{Error: friendlyErr(err, 20*time.Second)}
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		h := map[string]string{}
		for k, vs := range resp.Header {
			h[strings.ToLower(k)] = strings.Join(vs, ", ")
		}
		return sandbox.SubResponse{Status: resp.StatusCode, StatusText: http.StatusText(resp.StatusCode), Headers: h, Body: string(body), Time: time.Since(start).Milliseconds()}
	}
}

func bytesReader(b []byte) io.Reader {
	if len(b) == 0 {
		return nil
	}
	return bytes.NewReader(b)
}

func headerLookup(h map[string]string, name string) (string, bool) {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

func rawQuery(q url.Values) string {
	var parts []string
	for k, vs := range q {
		for _, v := range vs {
			parts = append(parts, k+"="+v)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "&")
}

func redactor(secrets []string) func(string) string {
	var list []string
	for _, s := range secrets {
		if len(s) >= 4 {
			list = append(list, s)
		}
	}
	sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
	return func(s string) string {
		for _, sec := range list {
			s = strings.ReplaceAll(s, sec, "••••••••")
		}
		return s
	}
}

func friendlyErr(err error, timeout time.Duration) string {
	var blocked *blockedErr
	switch {
	case errors.As(err, &blocked):
		return blocked.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("Timed out after %s", timeout)
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "Could not resolve host " + dnsErr.Name
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "Connection refused"
	}
	return err.Error()
}

// --- network guard ---

type blockedErr struct{ ip net.IP }

func (e *blockedErr) Error() string {
	return fmt.Sprintf("Blocked: %s is a private or internal address. Requara only calls public hosts.", e.ip)
}

func blocked(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.IPv4bcast) ||
		ip.Equal(net.ParseIP("169.254.169.254")) // cloud metadata
}

// netOpts is how one send reaches the network.
type netOpts struct {
	verify  bool
	proxy   func(*http.Request) (*url.URL, error)
	trusted map[string]bool // IPs the guard lets through: the operator's proxy
	certs   []tls.Certificate
	roots   *x509.CertPool
}

// roundTripper is for Requara's own calls (OAuth token, spec import): the
// operator's proxy, no client certificate.
func (r *Runner) roundTripper(verify bool) http.RoundTripper {
	if r.Transport != nil {
		return r.Transport
	}
	o := netOpts{verify: verify, proxy: r.Proxy}
	if r.Proxy != nil {
		o.trusted = r.operatorProxyIPs()
	}
	return r.transport(o)
}

// operatorProxyIPs are the addresses of the operator's proxies, which may be
// on a private network.
func (r *Runner) operatorProxyIPs() map[string]bool {
	out := map[string]bool{}
	for _, scheme := range []string{"http", "https"} {
		pu, err := r.Proxy(&http.Request{URL: &url.URL{Scheme: scheme, Host: "example.com"}})
		if err != nil || pu == nil {
			continue
		}
		if ips, err := net.LookupIP(pu.Hostname()); err == nil {
			for _, ip := range ips {
				out[ip.String()] = true
			}
		}
	}
	return out
}

// requestTransport builds the transport for one send from its settings.
func (r *Runner) requestTransport(ctx context.Context, s Settings, vars map[string]string, req *http.Request) (http.RoundTripper, error) {
	if r.Transport != nil {
		return r.Transport, nil
	}
	o := netOpts{verify: s.verify(), proxy: r.Proxy}
	if p := strings.TrimSpace(Substitute(s.Proxy, vars)); p != "" {
		pu, err := url.Parse(p)
		if err != nil || pu.Host == "" || (pu.Scheme != "http" && pu.Scheme != "https" && pu.Scheme != "socks5" && pu.Scheme != "socks5h") {
			return nil, errors.New("Proxy: use a URL such as http://proxy.example.com:3128 or socks5://127.0.0.1:1080")
		}
		o.proxy = http.ProxyURL(pu)
	} else if r.Proxy != nil {
		o.trusted = r.operatorProxyIPs()
	}
	if cert, key := strings.TrimSpace(Substitute(s.ClientCert, vars)), strings.TrimSpace(Substitute(s.ClientKey, vars)); cert != "" || key != "" {
		pair, err := tls.X509KeyPair([]byte(cert), []byte(key))
		if err != nil {
			return nil, fmt.Errorf("Client certificate: %s. Paste the PEM certificate and its unencrypted PEM key", cleanTLSErr(err))
		}
		o.certs = []tls.Certificate{pair}
	}
	if ca := strings.TrimSpace(Substitute(s.CACert, vars)); ca != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(ca)) {
			return nil, errors.New("CA certificate: no PEM certificate found in it")
		}
		o.roots = pool
	}
	// Through a proxy the guard only sees the proxy's address, so check
	// where the request is going before handing it over.
	if o.proxy != nil && !r.AllowPrivateNetwork {
		if pu, err := o.proxy(req); err == nil && pu != nil {
			if err := checkHost(ctx, req.URL.Hostname()); err != nil {
				return nil, err
			}
		}
	}
	return r.transport(o), nil
}

func cleanTLSErr(err error) string {
	return strings.TrimPrefix(err.Error(), "tls: ")
}

// checkHost refuses a host that is, or resolves to, a blocked address.
func checkHost(ctx context.Context, host string) error {
	if ip := net.ParseIP(host); ip != nil {
		if blocked(ip) {
			return &blockedErr{ip: ip}
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil // the proxy resolves it; it answers if it can't
	}
	for _, a := range addrs {
		if blocked(a.IP) {
			return &blockedErr{ip: a.IP}
		}
	}
	return nil
}

func (r *Runner) transport(o netOpts) *http.Transport {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: -1}
	if !r.AllowPrivateNetwork {
		// Checked after DNS resolution, on the actual IP being dialled, so
		// DNS rebinding can't sneak an internal address past us.
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, _ := net.SplitHostPort(address)
			if ip := net.ParseIP(host); ip != nil && blocked(ip) && !o.trusted[ip.String()] {
				return &blockedErr{ip: ip}
			}
			return nil
		}
	}
	return &http.Transport{
		Proxy:               o.proxy,
		DialContext:         dialer.DialContext,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: !o.verify, Certificates: o.certs, RootCAs: o.roots}, //nolint:gosec // user setting
		TLSHandshakeTimeout: 10 * time.Second,
		DisableKeepAlives:   true, // every send measures a fresh connection
		ForceAttemptHTTP2:   true,
	}
}

// --- timing ---

type tracer struct {
	start               time.Time
	dnsStart, dnsDone   time.Time
	connStart, connDone time.Time
	tlsStart, tlsDone   time.Time
	wrote, firstByte    time.Time
}

func (t *tracer) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { t.dnsStart = time.Now() },
		DNSDone:  func(httptrace.DNSDoneInfo) { t.dnsDone = time.Now() },
		ConnectStart: func(string, string) {
			if t.connStart.IsZero() {
				t.connStart = time.Now()
			}
		},
		ConnectDone:          func(string, string, error) { t.connDone = time.Now() },
		TLSHandshakeStart:    func() { t.tlsStart = time.Now() },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { t.tlsDone = time.Now() },
		WroteRequest:         func(httptrace.WroteRequestInfo) { t.wrote = time.Now() },
		GotFirstResponseByte: func() { t.firstByte = time.Now() },
	}
}

func (t *tracer) phases(end time.Time) []Phase {
	ms := func(a, b time.Time) float64 { return float64(b.Sub(a).Microseconds()) / 1000 }
	at := func(a time.Time) float64 { return ms(t.start, a) }
	out := []Phase{}
	add := func(name string, a, b time.Time) {
		if !a.IsZero() && !b.IsZero() && !b.Before(a) {
			out = append(out, Phase{Name: name, StartMs: at(a), Ms: ms(a, b)})
		}
	}
	first := t.start
	for _, x := range []time.Time{t.dnsStart, t.connStart} {
		if !x.IsZero() {
			first = x
			break
		}
	}
	add("Socket wait", t.start, first)
	add("DNS lookup", t.dnsStart, t.dnsDone)
	add("TCP connect", t.connStart, t.connDone)
	add("TLS handshake", t.tlsStart, t.tlsDone)
	sendFrom := t.connDone
	if !t.tlsDone.IsZero() {
		sendFrom = t.tlsDone
	}
	add("Request sent", sendFrom, t.wrote)
	add("Waiting (TTFB)", t.wrote, t.firstByte)
	add("Content download", t.firstByte, end)
	// Without connection-level timings (the browser's fetch), show the total.
	if len(out) <= 1 && !end.Before(t.start) {
		out = []Phase{{Name: "Total", StartMs: 0, Ms: ms(t.start, end)}}
	}
	return out
}

// Fetch downloads a URL (for importing specs) with the same network guard as Send.
func (r *Runner) Fetch(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("enter an http or https URL")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Transport: r.roundTripper(true)}).Do(req)
	if err != nil {
		return nil, errors.New(friendlyErr(err, 20*time.Second))
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("the server answered %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("file is larger than %d MB", limit>>20)
	}
	return b, nil
}
