// Package runner sends a request on the user's behalf: fills in {{variables}},
// runs the pre-request script, makes the HTTP call with per-phase timings
// (httptrace), then runs the tests script. Secrets are only ever in memory here.
package runner

import (
	"bytes"
	"context"
	"crypto/tls"
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

	"github.com/anilcharles/speklo/server/api/internal/sandbox"
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
	Type     string `json:"type"` // inherit | none | bearer | basic | apikey
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Key      string `json:"key,omitempty"`
	Value    string `json:"value,omitempty"`
	In       string `json:"in,omitempty"` // header | query
}

type Settings struct {
	FollowRedirects *bool `json:"followRedirects,omitempty"`
	VerifySSL       *bool `json:"verifySSL,omitempty"`
	EncodeURL       *bool `json:"encodeURL,omitempty"`
	TimeoutMs       int   `json:"timeoutMs,omitempty"`
}

func (s Settings) follow() bool { return s.FollowRedirects == nil || *s.FollowRedirects }
func (s Settings) verify() bool { return s.VerifySSL == nil || *s.VerifySSL }
func (s Settings) encode() bool { return s.EncodeURL == nil || *s.EncodeURL }

type Spec struct {
	Method     string   `json:"method"`
	URL        string   `json:"url"`
	Params     []KV     `json:"params"`
	Headers    []KV     `json:"headers"`
	BodyMode   string   `json:"bodyMode"` // none | json | raw | form-urlencoded | form-data | graphql
	Body       string   `json:"body"`
	Auth       Auth     `json:"auth"`
	PreScript  string   `json:"preScript"`
	PostScript string   `json:"postScript"`
	Settings   Settings `json:"settings"`
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
	EnvChanges  map[string]string    `json:"-"` // variables scripts set; caller persists them
}

type Runner struct {
	AllowPrivateNetwork bool
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
	res := &Result{Method: strings.ToUpper(s.Method)}
	redact := redactor(secretValues)

	// 1. Build the request with variables filled in.
	req := sandbox.Request{Method: strings.ToUpper(s.Method), URL: Substitute(s.URL, vars), Headers: map[string]string{}}
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
	for _, h := range s.Headers {
		if h.Enabled && h.Key != "" {
			req.Headers[Substitute(h.Key, vars)] = Substitute(h.Value, vars)
		}
	}
	switch s.Auth.Type {
	case "bearer":
		req.Headers["Authorization"] = "Bearer " + Substitute(s.Auth.Token, vars)
	case "basic":
		cred := Substitute(s.Auth.Username, vars) + ":" + Substitute(s.Auth.Password, vars)
		req.Headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(cred))
	case "apikey":
		if s.Auth.In == "query" {
			q.Set(Substitute(s.Auth.Key, vars), Substitute(s.Auth.Value, vars))
		} else {
			req.Headers[Substitute(s.Auth.Key, vars)] = Substitute(s.Auth.Value, vars)
		}
	}
	if s.Settings.encode() {
		u.RawQuery = q.Encode()
	} else {
		u.RawQuery = rawQuery(q)
	}
	req.URL = u.String()
	if s.BodyMode != "none" && s.BodyMode != "" {
		req.Body = Substitute(s.Body, vars)
		if _, ok := headerLookup(req.Headers, "Content-Type"); !ok {
			switch s.BodyMode {
			case "json":
				req.Headers["Content-Type"] = "application/json"
			case "form-urlencoded":
				req.Headers["Content-Type"] = "application/x-www-form-urlencoded"
			case "graphql":
				req.Headers["Content-Type"] = "application/json"
			}
		}
	}

	envOut := map[string]string{}
	// 2. Pre-request script may change the request and set variables.
	if strings.TrimSpace(s.PreScript) != "" {
		out, err := sandbox.Run(ctx, s.PreScript, sandbox.Input{Request: req, Env: vars})
		if err != nil {
			res.Error = "Pre-request script failed: " + err.Error()
			return res
		}
		if out.Error != "" {
			res.ScriptError = "Pre-request: " + out.Error
		}
		req = out.Request
		for k, v := range out.Env {
			envOut[k], vars[k] = v, v
		}
		res.Logs = append(res.Logs, out.Logs...)
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
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bodyReader(req.Body))
	if err != nil {
		res.Error = err.Error()
		return res
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	if httpReq.Header.Get("User-Agent") == "" {
		httpReq.Header.Set("User-Agent", "Speklo/1.0")
	}
	t := &tracer{}
	httpReq = httpReq.WithContext(httptrace.WithClientTrace(ctx, t.trace()))
	client := &http.Client{Transport: r.transport(s.Settings.verify())}
	if !s.Settings.follow() {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	t.start = time.Now()
	resp, err := client.Do(httpReq)
	if err != nil {
		res.DurationMs = time.Since(t.start).Milliseconds()
		res.Error = friendlyErr(err, timeout)
		res.Timeline = t.phases(time.Now())
		return res
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	end := time.Now()
	if err != nil {
		res.Error = "Reading the response failed: " + err.Error()
	}
	if len(body) > maxBody {
		body, res.Truncated = body[:maxBody], true
	}
	res.Status, res.StatusText = resp.StatusCode, http.StatusText(resp.StatusCode)
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
		out, err := sandbox.Run(ctx, s.PostScript, sandbox.Input{
			Request:  req,
			Response: &sandbox.Response{Status: res.Status, Headers: hdrs, Body: res.Body, Time: res.DurationMs},
			Env:      vars,
		})
		if err != nil {
			res.ScriptError = "Tests: " + err.Error()
		} else {
			res.Tests = out.Tests
			res.Logs = append(res.Logs, out.Logs...)
			if out.Error != "" {
				res.ScriptError = "Tests: " + out.Error
			}
			for k, v := range out.Env {
				envOut[k] = v
			}
		}
	}
	res.EnvChanges = envOut
	return res
}

func bodyReader(s string) io.Reader {
	if s == "" {
		return nil
	}
	return bytes.NewBufferString(s)
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
	return fmt.Sprintf("Blocked: %s is a private or internal address. Speklo only calls public hosts.", e.ip)
}

func blocked(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.IPv4bcast) ||
		ip.Equal(net.ParseIP("169.254.169.254")) // cloud metadata
}

func (r *Runner) transport(verify bool) *http.Transport {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: -1}
	if !r.AllowPrivateNetwork {
		// Checked after DNS resolution, on the actual IP being dialled, so
		// DNS rebinding can't sneak an internal address past us.
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, _ := net.SplitHostPort(address)
			if ip := net.ParseIP(host); ip != nil && blocked(ip) {
				return &blockedErr{ip: ip}
			}
			return nil
		}
	}
	return &http.Transport{
		DialContext:         dialer.DialContext,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: !verify}, //nolint:gosec // user setting
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
	var out []Phase
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
	resp, err := (&http.Client{Transport: r.transport(true)}).Do(req)
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
