// Package sandbox runs customers' Pre-request and Tests scripts in QuickJS
// compiled to WebAssembly (wazero). Scripts get no filesystem, network or Go
// callbacks: input goes in as JSON, results come back as JSON. Each run has a
// memory cap, a stack cap and a wall-clock deadline.
package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fastschema/qjs"
)

const (
	memoryLimit = 32 << 20 // 32 MB
	stackLimit  = 512 << 10
	timeLimit   = 2 * time.Second
)

// Request is what a pre-request script can read and change.
type Request struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// Response is what a tests script can read.
type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
	Time    int64             `json:"time"` // milliseconds
}

type Input struct {
	Request  Request           `json:"request"`
	Response *Response         `json:"response,omitempty"`
	Env      map[string]string `json:"env"`
}

type TestResult struct {
	Name  string `json:"name"`
	Pass  bool   `json:"pass"`
	Error string `json:"error,omitempty"`
	Ms    int64  `json:"ms"`
}

type Output struct {
	Request Request           `json:"request"`
	Env     map[string]string `json:"env"` // variables the script set
	Tests   []TestResult      `json:"tests"`
	Logs    []string          `json:"logs"`
	Error   string            `json:"error,omitempty"`
}

const prelude = `
const __in = JSON.parse(__INPUT__);
const __out = { request: __in.request, env: {}, tests: [], logs: [] };
const env = {
  get: (k) => (k in __out.env ? __out.env[k] : __in.env[k]),
  set: (k, v) => { __out.env[String(k)] = String(v); },
};
const req = __out.request;
const res = __in.response ? Object.assign({}, __in.response, {
  json: (() => { try { return JSON.parse(__in.response.body); } catch (e) { return undefined; } })(),
}) : undefined;
const __fmt = (v) => { try { return JSON.stringify(v); } catch (e) { return String(v); } };
function expect(actual) {
  const fail = (msg) => { throw new Error(msg); };
  const m = {
    toBe: (e) => actual === e || fail("expected " + __fmt(actual) + " to be " + __fmt(e)),
    toEqual: (e) => __fmt(actual) === __fmt(e) || fail("expected " + __fmt(actual) + " to equal " + __fmt(e)),
    toBeLessThan: (n) => actual < n || fail("expected " + __fmt(actual) + " to be less than " + n),
    toBeGreaterThan: (n) => actual > n || fail("expected " + __fmt(actual) + " to be greater than " + n),
    toContain: (x) => (actual != null && actual.indexOf(x) >= 0) || fail("expected " + __fmt(actual) + " to contain " + __fmt(x)),
    toBeTruthy: () => !!actual || fail("expected " + __fmt(actual) + " to be truthy"),
    toBeDefined: () => actual !== undefined || fail("expected value to be defined"),
  };
  m.not = {
    toBe: (e) => actual !== e || fail("expected " + __fmt(actual) + " not to be " + __fmt(e)),
    toEqual: (e) => __fmt(actual) !== __fmt(e) || fail("expected " + __fmt(actual) + " not to equal " + __fmt(e)),
  };
  return m;
}
function test(name, fn) {
  const t0 = Date.now();
  try { fn(); __out.tests.push({ name: String(name), pass: true, ms: Date.now() - t0 }); }
  catch (e) { __out.tests.push({ name: String(name), pass: false, error: String((e && e.message) || e), ms: Date.now() - t0 }); }
}
const console = { log: (...a) => { if (__out.logs.length < 100) __out.logs.push(a.map((x) => typeof x === "string" ? x : __fmt(x)).join(" ")); } };
`

// Run executes script with input. An empty script returns the input unchanged.
func Run(ctx context.Context, script string, in Input) (*Output, error) {
	if in.Env == nil {
		in.Env = map[string]string{}
	}
	if in.Request.Headers == nil {
		in.Request.Headers = map[string]string{}
	}
	if strings.TrimSpace(script) == "" {
		return &Output{Request: in.Request, Env: map[string]string{}}, nil
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	lit, _ := json.Marshal(string(raw)) // a JS string literal holding the JSON
	code := strings.Replace(prelude, "__INPUT__", string(lit), 1) +
		"\ntry { (function () {\n" + script + "\n})(); } catch (e) { __out.error = String((e && e.message) || e); }\nJSON.stringify(__out);"

	ctx, cancel := context.WithTimeout(ctx, timeLimit)
	defer cancel()
	result, err := eval(ctx, code)
	if err != nil {
		msg := cleanErr(err)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			msg = "script timed out after 2s"
		}
		return &Output{Request: in.Request, Env: map[string]string{}, Error: msg}, nil
	}
	var out Output
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		return nil, fmt.Errorf("read script result: %w", err)
	}
	if out.Env == nil {
		out.Env = map[string]string{}
	}
	return &out, nil
}

// eval runs code in a fresh runtime. The qjs library panics when the deadline
// closes the module mid-run, so panics are turned into errors here.
func eval(ctx context.Context, code string) (result string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	rt, err := qjs.New(qjs.Option{
		Context:            ctx,
		CloseOnContextDone: true,
		MemoryLimit:        memoryLimit,
		MaxStackSize:       stackLimit,
	})
	if err != nil {
		return "", fmt.Errorf("start sandbox: %w", err)
	}
	defer func() {
		defer func() { _ = recover() }() // closing an already-closed module also panics
		rt.Close()
	}()
	val, err := rt.Context().Eval("script.js", qjs.Code(code))
	if err != nil {
		return "", err
	}
	defer val.Free()
	return val.String(), nil
}

func cleanErr(err error) string {
	s := err.Error()
	if i := strings.Index(s, "\n"); i > 0 {
		s = s[:i]
	}
	return s
}
