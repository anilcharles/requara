//go:build js && wasm

// Command requara-wasm is Requara's API-platform logic for the browser: the web
// app's personal workspace (no account, data in the browser) runs the same
// Go code as the server for OpenAPI import, the design editor, lint, change
// detection, docs, mock responses and sending requests with scripts and tests.
//
// It registers requaraCore.call(op, inputJSON) → Promise<outputJSON>.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall/js"
	"time"

	"github.com/anilcharles/requara/server/api/internal/core"
	"github.com/anilcharles/requara/server/api/internal/runner"
	"github.com/anilcharles/requara/server/api/internal/spec"
	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// MockHost is where personal-workspace mock servers live. Only Requara's own
// sends reach them; they're answered here, never on the network.
const MockHost = "mock.requara.local"

type handler func(ctx context.Context, in json.RawMessage) (any, error)

var ops = map[string]handler{
	"parse":           parse,
	"blank":           blank,
	"requestsFor":     requestsFor,
	"operations":      operations,
	"operation":       operation,
	"updateOperation": updateOperation,
	"lint":            lint,
	"changes":         changes,
	"publish":         publish,
	"docs":            docs,
	"yaml":            toYAML,
	"mock":            mock,
	"send":            send,
	"parsePostman":    parsePostman,
	"exportPostman":   exportPostman,
	"parseCurl":       parseCurl,
	"oauth2Authorize": oauth2Authorize,
	"oauth2Token":     oauth2Token,
	"exportFiles":     exportFiles,
	"parseFiles":      parseFiles,
}

func main() {
	call := js.FuncOf(func(_ js.Value, args []js.Value) any {
		op, input := args[0].String(), args[1].String()
		return newPromise(func() (string, error) {
			h, ok := ops[op]
			if !ok {
				return "", fmt.Errorf("unknown operation %q", op)
			}
			out, err := h(context.Background(), json.RawMessage(input))
			if err != nil {
				return "", err
			}
			b, err := json.Marshal(out)
			return string(b), err
		})
	})
	js.Global().Set("requaraCore", js.ValueOf(map[string]any{"call": call, "mockHost": MockHost}))
	js.Global().Call("dispatchEvent", js.Global().Get("Event").New("requara-core-ready"))
	select {}
}

// newPromise runs fn in a goroutine (it may block on fetch) and settles a
// JS promise. Errors reject with {status, message}.
func newPromise(fn func() (string, error)) js.Value {
	return js.Global().Get("Promise").New(js.FuncOf(func(_ js.Value, args []js.Value) any {
		resolve, reject := args[0], args[1]
		go func() {
			defer func() {
				if r := recover(); r != nil {
					reject.Invoke(js.ValueOf(map[string]any{"status": 500, "message": fmt.Sprint(r)}))
				}
			}()
			out, err := fn()
			if err != nil {
				status := 400
				var ee *core.EditError
				if errors.As(err, &ee) {
					status = ee.Status
				}
				reject.Invoke(js.ValueOf(map[string]any{"status": status, "message": err.Error()}))
				return
			}
			resolve.Invoke(out)
		}()
		return nil
	}))
}

func doc(raw json.RawMessage) (*openapi3.T, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	return spec.FromJSON(raw)
}

func decode[T any](in json.RawMessage) (T, error) {
	var v T
	err := json.Unmarshal(in, &v)
	return v, err
}

// ----- operations -----

func parse(ctx context.Context, in json.RawMessage) (any, error) {
	x, err := decode[struct{ Data string }](in)
	if err != nil {
		return nil, err
	}
	d, err := spec.Parse([]byte(x.Data))
	if err != nil {
		return nil, err
	}
	if err := core.Validate(ctx, d); err != nil {
		return nil, err
	}
	name, version, desc := "Imported API", "v1.0.0", ""
	if d.Info != nil {
		if d.Info.Title != "" {
			name = d.Info.Title
		}
		if d.Info.Version != "" {
			version = core.NormalizeVersion(d.Info.Version)
		}
		desc = d.Info.Description
	}
	raw, _ := spec.ToJSON(d)
	return map[string]any{"doc": json.RawMessage(raw), "name": name, "version": version, "description": core.FirstSentence(desc),
		"baseUrl": spec.BaseURL(d), "operations": len(spec.Operations(d))}, nil
}

func blank(ctx context.Context, in json.RawMessage) (any, error) {
	x, err := decode[struct {
		Name, BaseURL, Version, Description string
		Template                            bool
	}](in)
	if err != nil {
		return nil, err
	}
	version := core.NormalizeVersion(x.Version)
	d := spec.Blank(x.Name, version, strings.TrimSpace(x.BaseURL))
	if x.Description != "" {
		d.Info.Description = x.Description
	}
	if x.Template {
		core.AddResourceTemplate(d)
	}
	if err := core.Validate(ctx, d); err != nil {
		return nil, err
	}
	raw, _ := spec.ToJSON(d)
	return map[string]any{"doc": json.RawMessage(raw), "version": version, "baseUrl": spec.BaseURL(d), "description": core.FirstSentence(x.Description)}, nil
}

func requestsFor(_ context.Context, in json.RawMessage) (any, error) {
	x, err := decode[struct {
		Doc       json.RawMessage
		Existing  []string
		URLPrefix string `json:"urlPrefix"`
	}](in)
	if err != nil {
		return nil, err
	}
	d, err := doc(x.Doc)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, k := range x.Existing {
		seen[k] = true
	}
	return core.RequestsFor(d, seen, x.URLPrefix), nil
}

func operations(_ context.Context, in json.RawMessage) (any, error) {
	x, err := decode[struct{ Doc json.RawMessage }](in)
	if err != nil {
		return nil, err
	}
	d, err := doc(x.Doc)
	if err != nil {
		return nil, err
	}
	return spec.Operations(d), nil
}

func operation(_ context.Context, in json.RawMessage) (any, error) {
	x, err := decode[struct {
		Draft, Published json.RawMessage
		Key              string
	}](in)
	if err != nil {
		return nil, err
	}
	draft, err := doc(x.Draft)
	if err != nil {
		return nil, err
	}
	pub, err := doc(x.Published)
	if err != nil {
		return nil, err
	}
	out, err := core.OperationDetail(draft, pub, x.Key)
	if err != nil {
		return nil, &core.EditError{Status: 404, Msg: err.Error()}
	}
	return out, nil
}

func updateOperation(ctx context.Context, in json.RawMessage) (any, error) {
	x, err := decode[struct {
		Doc  json.RawMessage
		Edit core.OperationEdit
	}](in)
	if err != nil {
		return nil, err
	}
	d, err := doc(x.Doc)
	if err != nil {
		return nil, err
	}
	key, err := core.UpdateOperation(ctx, d, x.Edit)
	if err != nil {
		return nil, err
	}
	raw, _ := spec.ToJSON(d)
	return map[string]any{"doc": json.RawMessage(raw), "key": key, "baseUrl": spec.BaseURL(d)}, nil
}

func lint(_ context.Context, in json.RawMessage) (any, error) {
	x, err := decode[struct{ Doc json.RawMessage }](in)
	if err != nil {
		return nil, err
	}
	return spec.Lint(x.Doc), nil
}

func changes(_ context.Context, in json.RawMessage) (any, error) {
	x, err := decode[struct {
		Draft, Published               json.RawMessage
		PublishedVersion, DraftVersion string
	}](in)
	if err != nil {
		return nil, err
	}
	draft, err := doc(x.Draft)
	if err != nil {
		return nil, err
	}
	pub, err := doc(x.Published)
	if err != nil {
		return nil, err
	}
	cs, from, next, err := core.Changes(draft, pub, x.PublishedVersion, x.DraftVersion)
	if err != nil {
		return nil, err
	}
	breaking := false
	for _, c := range cs {
		breaking = breaking || c.Breaking
	}
	return map[string]any{"from": from, "suggestedVersion": next, "changes": cs, "breaking": breaking}, nil
}

func publish(_ context.Context, in json.RawMessage) (any, error) {
	x, err := decode[struct {
		Doc     json.RawMessage
		Version string
	}](in)
	if err != nil {
		return nil, err
	}
	d, err := doc(x.Doc)
	if err != nil {
		return nil, err
	}
	d.Info.Version = strings.TrimPrefix(x.Version, "v")
	raw, _ := spec.ToJSON(d)
	return map[string]any{"doc": json.RawMessage(raw)}, nil
}

func docs(_ context.Context, in json.RawMessage) (any, error) {
	x, err := decode[struct{ Doc json.RawMessage }](in)
	if err != nil {
		return nil, err
	}
	d, err := doc(x.Doc)
	if err != nil {
		return nil, err
	}
	desc := ""
	if d.Info != nil {
		desc = d.Info.Description
	}
	return map[string]any{"baseUrl": spec.BaseURL(d), "description": desc, "operations": core.DocOperations(d)}, nil
}

func toYAML(_ context.Context, in json.RawMessage) (any, error) {
	x, err := decode[struct{ Doc json.RawMessage }](in)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(x.Doc, &v); err != nil {
		return nil, err
	}
	out, err := yaml.Marshal(v)
	return map[string]string{"text": string(out)}, err
}

type localMock struct {
	Slug      string          `json:"slug"`
	Doc       json.RawMessage `json:"doc"`
	Enabled   bool            `json:"enabled"`
	LatencyMs int             `json:"latencyMs"`
	ErrorRate int             `json:"errorRate"`
}

func answer(m localMock, method, path, prefer string, body io.Reader) core.MockResponse {
	if !m.Enabled {
		return core.Problem(http.StatusServiceUnavailable, "Mock server is stopped", "Turn it on in Requara → Mock servers.")
	}
	d, err := doc(m.Doc)
	if err != nil || d == nil {
		return core.Problem(http.StatusInternalServerError, "Spec unavailable", "")
	}
	if m.LatencyMs > 0 {
		time.Sleep(time.Duration(m.LatencyMs) * time.Millisecond)
	}
	if res, failed := core.InjectedError(m.ErrorRate); failed {
		return res
	}
	return core.MockAnswer(d, method, path, prefer, body)
}

func mock(_ context.Context, in json.RawMessage) (any, error) {
	x, err := decode[struct {
		Mock                 localMock
		Method, Path, Prefer string
		Body                 string
	}](in)
	if err != nil {
		return nil, err
	}
	return answer(x.Mock, strings.ToUpper(x.Method), x.Path, x.Prefer, strings.NewReader(x.Body)), nil
}

// mockCall is a call one of the personal mock servers answered.
type mockCall struct {
	Slug   string `json:"slug"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Status int    `json:"status"`
}

// agent is the local Requara agent (cmd/requara-agent), when paired.
type agent struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// transport sends with the browser's fetch (or through the local agent,
// which has no CORS limits), except calls to personal mock servers, which
// are answered right here.
type transport struct {
	mocks map[string]localMock
	calls *[]mockCall
	agent *agent
}

func (t transport) viaAgent(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	headers := map[string]string{}
	for k, v := range req.Header {
		headers[k] = strings.Join(v, ", ")
	}
	in, _ := json.Marshal(map[string]any{"method": req.Method, "url": req.URL.String(), "headers": headers, "body": base64.StdEncoding.EncodeToString(body)})
	ar, err := http.NewRequestWithContext(req.Context(), http.MethodPost, strings.TrimRight(t.agent.URL, "/")+"/send", bytes.NewReader(in))
	if err != nil {
		return nil, err
	}
	ar.Header.Set("Content-Type", "application/json")
	ar.Header.Set("X-Requara-Agent-Token", t.agent.Token)
	resp, err := http.DefaultTransport.RoundTrip(ar)
	if err != nil {
		return nil, fmt.Errorf("the Requara agent at %s isn't answering (is it running?): %w", t.agent.URL, err)
	}
	defer resp.Body.Close()
	var out struct {
		Status     int         `json:"status"`
		StatusText string      `json:"statusText"`
		Headers    [][2]string `json:"headers"`
		Body       string      `json:"body"`
		Error      string      `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("the Requara agent sent a bad answer: %w", err)
	}
	if out.Error != "" {
		return nil, errors.New(out.Error)
	}
	b, _ := base64.StdEncoding.DecodeString(out.Body)
	h := http.Header{}
	for _, kv := range out.Headers {
		h.Add(kv[0], kv[1])
	}
	return &http.Response{
		StatusCode: out.Status, Status: fmt.Sprintf("%d %s", out.Status, out.StatusText), Header: h,
		Body: io.NopCloser(bytes.NewReader(b)), ContentLength: int64(len(b)), Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Request: req,
	}, nil
}

func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != MockHost {
		if t.agent != nil && t.agent.URL != "" {
			return t.viaAgent(req)
		}
		return http.DefaultTransport.RoundTrip(req)
	}
	slug, rest, _ := strings.Cut(strings.TrimPrefix(req.URL.Path, "/"), "/")
	m, ok := t.mocks[slug]
	var res core.MockResponse
	if !ok {
		res = core.Problem(http.StatusNotFound, "Mock server not found", "")
	} else {
		res = answer(m, req.Method, "/"+rest, req.Header.Get("Prefer"), req.Body)
		*t.calls = append(*t.calls, mockCall{Slug: slug, Method: req.Method, Path: "/" + rest, Status: res.Status})
	}
	h := http.Header{}
	h.Set("Content-Type", res.ContentType)
	h.Set("X-Requara-Mock", "true")
	return &http.Response{
		StatusCode: res.Status, Status: fmt.Sprintf("%d %s", res.Status, http.StatusText(res.Status)),
		Header: h, Body: io.NopCloser(bytes.NewBufferString(res.Body)), ContentLength: int64(len(res.Body)),
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Request: req,
	}, nil
}

func send(ctx context.Context, in json.RawMessage) (any, error) {
	x, err := decode[struct {
		Spec    runner.Spec
		Vars    map[string]string
		Secrets []string
		Mocks   []localMock
		Agent   *agent
	}](in)
	if err != nil {
		return nil, err
	}
	if x.Vars == nil {
		x.Vars = map[string]string{}
	}
	calls := []mockCall{}
	t := transport{mocks: map[string]localMock{}, calls: &calls, agent: x.Agent}
	for _, m := range x.Mocks {
		t.mocks[m.Slug] = m
	}
	res := (&runner.Runner{Transport: t}).Send(ctx, x.Spec, x.Vars, x.Secrets)
	if res.EnvChanges == nil {
		res.EnvChanges = map[string]string{}
	}
	return map[string]any{"result": res, "envChanges": res.EnvChanges, "globalChanges": res.GlobalChanges, "collectionChanges": res.CollectionChanges, "mockCalls": calls}, nil
}

type oauth2In struct {
	Auth         runner.Auth
	Vars         map[string]string
	RedirectURI  string
	State        string
	CodeVerifier string
	Code         string
	Agent        *agent
}

func oauth2Authorize(_ context.Context, in json.RawMessage) (any, error) {
	x, err := decode[oauth2In](in)
	if err != nil {
		return nil, err
	}
	u, err := runner.AuthorizeURL(x.Auth, x.Vars, x.RedirectURI, x.State, x.CodeVerifier)
	return map[string]string{"url": u}, err
}

// oauth2Token trades the code with the browser's fetch (or the local agent).
func oauth2Token(ctx context.Context, in json.RawMessage) (any, error) {
	x, err := decode[oauth2In](in)
	if err != nil {
		return nil, err
	}
	r := &runner.Runner{Transport: transport{mocks: map[string]localMock{}, calls: &[]mockCall{}, agent: x.Agent}}
	return r.ExchangeCode(ctx, x.Auth, x.Vars, x.Code, x.CodeVerifier, x.RedirectURI)
}

func exportFiles(_ context.Context, in json.RawMessage) (any, error) {
	c, err := decode[core.Collection](in)
	if err != nil {
		return nil, err
	}
	out, err := core.ExportFiles(c)
	return map[string]string{"data": base64.StdEncoding.EncodeToString(out)}, err
}

func parseFiles(_ context.Context, in json.RawMessage) (any, error) {
	x, err := decode[struct{ Data string }](in)
	if err != nil {
		return nil, err
	}
	zipped, err := base64.StdEncoding.DecodeString(x.Data)
	if err != nil {
		return nil, errors.New("that isn't a zip file")
	}
	return core.ParseFiles(zipped)
}

func parsePostman(_ context.Context, in json.RawMessage) (any, error) {
	x, err := decode[struct{ Data string }](in)
	if err != nil {
		return nil, err
	}
	return core.ParsePostman([]byte(x.Data))
}

func exportPostman(_ context.Context, in json.RawMessage) (any, error) {
	c, err := decode[core.Collection](in)
	if err != nil {
		return nil, err
	}
	out, err := core.ExportPostman(c)
	return map[string]string{"text": string(out)}, err
}

func parseCurl(_ context.Context, in json.RawMessage) (any, error) {
	x, err := decode[struct{ Data string }](in)
	if err != nil {
		return nil, err
	}
	return core.ParseCurl(x.Data)
}
