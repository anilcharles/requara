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
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
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

// The QuickJS build ships its std and os modules (files, directories,
// timers) as globals. Scripts get none of them.
const prelude = `
delete globalThis.std; delete globalThis.os; delete globalThis.scriptArgs;
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
const __type = (v) => v === null ? "null" : Array.isArray(v) ? "array" : typeof v;
const __includes = (a, x) => {
  if (a == null) return false;
  if (typeof a === "string" || Array.isArray(a)) return a.indexOf(x) >= 0;
  if (typeof a === "object" && x && typeof x === "object") return Object.keys(x).every((k) => __fmt(a[k]) === __fmt(x[k]));
  return false;
};
// expect() speaks both Jest (toBe, toEqual, …) and Chai (to.equal, to.be.ok, …),
// so Postman scripts run unchanged.
function expect(actual, negate) {
  const A = {};
  const fail = (msg) => { throw new Error(msg); };
  const check = (cond, msg, nmsg) => { if (negate ? cond : !cond) fail(negate ? (nmsg || msg.replace(" to ", " not to ")) : msg); return A; };
  const f = __fmt;
  ["to", "be", "been", "is", "that", "which", "and", "has", "have", "with", "at", "of", "same", "does", "also", "deep", "nested", "own"].forEach((k) => Object.defineProperty(A, k, { get: () => A }));
  Object.defineProperty(A, "not", { get: () => expect(actual, !negate) });
  const getter = (name, fn) => Object.defineProperty(A, name, { get: fn });
  const isResponse = actual && actual.__response;
  getter("ok", () => isResponse ? check(actual.code >= 200 && actual.code < 300, "expected response to be OK (2xx) but got " + actual.code) : check(!!actual, "expected " + f(actual) + " to be truthy"));
  getter("true", () => check(actual === true, "expected " + f(actual) + " to be true"));
  getter("false", () => check(actual === false, "expected " + f(actual) + " to be false"));
  getter("null", () => check(actual === null, "expected " + f(actual) + " to be null"));
  getter("undefined", () => check(actual === undefined, "expected " + f(actual) + " to be undefined"));
  getter("exist", () => check(actual != null, "expected value to exist"));
  getter("empty", () => check(actual != null && (actual.length === 0 || (typeof actual === "object" && Object.keys(actual).length === 0)), "expected " + f(actual) + " to be empty"));
  getter("json", () => isResponse ? check((() => { try { JSON.parse(actual.text()); return true; } catch (e) { return false; } })(), "expected response body to be JSON") : A);
  A.equal = A.equals = A.eq = (e) => check(actual === e, "expected " + f(actual) + " to equal " + f(e));
  A.eql = A.eqls = (e) => check(f(actual) === f(e), "expected " + f(actual) + " to deeply equal " + f(e));
  A.a = A.an = (t) => check(__type(actual) === String(t).toLowerCase(), "expected " + f(actual) + " to be a " + t);
  A.include = A.includes = A.contain = A.contains = (x) => check(__includes(actual, x), "expected " + f(actual) + " to include " + f(x));
  A.property = (k, v) => check(actual != null && Object(actual)[k] !== undefined && (v === undefined || f(Object(actual)[k]) === f(v)), "expected " + f(actual) + " to have property " + f(k) + (v === undefined ? "" : " of " + f(v)));
  A.lengthOf = A.length = (n) => check(actual != null && actual.length === n, "expected " + f(actual) + " to have length " + n);
  A.above = A.gt = A.greaterThan = (n) => check(actual > n, "expected " + f(actual) + " to be above " + n);
  A.below = A.lt = A.lessThan = (n) => check(actual < n, "expected " + f(actual) + " to be below " + n);
  A.least = A.gte = (n) => check(actual >= n, "expected " + f(actual) + " to be at least " + n);
  A.most = A.lte = (n) => check(actual <= n, "expected " + f(actual) + " to be at most " + n);
  A.within = (lo, hi) => check(actual >= lo && actual <= hi, "expected " + f(actual) + " to be within " + lo + ".." + hi);
  A.oneOf = (arr) => check(arr.indexOf(actual) >= 0, "expected " + f(actual) + " to be one of " + f(arr));
  A.match = (re) => check(re.test(actual), "expected " + f(actual) + " to match " + re);
  A.keys = A.key = (...ks) => { ks = Array.isArray(ks[0]) ? ks[0] : ks; return check(actual != null && ks.every((k) => k in Object(actual)), "expected " + f(actual) + " to have keys " + f(ks)); };
  A.instanceof = A.instanceOf = (C) => check(actual instanceof C, "expected value to be an instance of " + (C && C.name));
  A.status = (code) => check(isResponse && (typeof code === "number" ? actual.code === code : actual.status === code), "expected response to have status " + code + " but got " + (actual && actual.code));
  A.header = (name, value) => check(isResponse && actual.headers.has(name) && (value === undefined || actual.headers.get(name) === value), "expected response to have header " + name + (value === undefined ? "" : ": " + value));
  A.body = (b) => check(isResponse && actual.text() === b, "expected response body to be " + f(b));
  A.jsonBody = (path, value) => { let v; try { v = JSON.parse(actual.text()); } catch (e) { v = undefined; } return check(v !== undefined && (path === undefined || (Object(v)[path] !== undefined && (value === undefined || f(v[path]) === f(value)))), "expected JSON body" + (path ? " to have " + path : "")); };
  A.responseTime = { below: (ms) => check(isResponse && actual.responseTime < ms, "expected response time below " + ms + "ms") };
  // Jest style.
  A.toBe = (e) => check(actual === e, "expected " + f(actual) + " to be " + f(e));
  A.toEqual = (e) => check(f(actual) === f(e), "expected " + f(actual) + " to equal " + f(e));
  A.toBeLessThan = (n) => check(actual < n, "expected " + f(actual) + " to be less than " + n);
  A.toBeGreaterThan = (n) => check(actual > n, "expected " + f(actual) + " to be greater than " + n);
  A.toContain = (x) => check(actual != null && actual.indexOf(x) >= 0, "expected " + f(actual) + " to contain " + f(x));
  A.toBeTruthy = () => check(!!actual, "expected " + f(actual) + " to be truthy");
  A.toBeDefined = () => check(actual !== undefined, "expected value to be defined");
  return A;
}
function test(name, fn) {
  const t0 = Date.now();
  try { fn(); __out.tests.push({ name: String(name), pass: true, ms: Date.now() - t0 }); }
  catch (e) { __out.tests.push({ name: String(name), pass: false, error: String((e && e.message) || e), ms: Date.now() - t0 }); }
}
const console = { log: (...a) => { if (__out.logs.length < 100) __out.logs.push(a.map((x) => typeof x === "string" ? x : __fmt(x)).join(" ")); } };
console.info = console.warn = console.error = console.debug = console.log;
// Postman's pm API (and its legacy globals) over the same request, response and variables.
const __vars = { get: env.get, set: env.set, unset: (k) => env.set(k, ""), has: (k) => env.get(k) !== undefined,
  toObject: () => Object.assign({}, __in.env, __out.env), replaceIn: (s) => String(s).replace(/\{\{\s*([^}\s]+)\s*\}\}/g, (m, k) => { const v = env.get(k); return v === undefined ? m : v; }) };
const __hdr = (h) => ({
  get: (n) => { const k = Object.keys(h).find((x) => x.toLowerCase() === String(n).toLowerCase()); return k === undefined ? undefined : h[k]; },
  has: (n) => Object.keys(h).some((x) => x.toLowerCase() === String(n).toLowerCase()),
  add: (o) => { h[o.key] = String(o.value); }, upsert: (o) => { h[o.key] = String(o.value); },
  remove: (n) => { Object.keys(h).forEach((x) => { if (x.toLowerCase() === String(n).toLowerCase()) delete h[x]; }); },
  toObject: () => Object.assign({}, h), all: () => Object.keys(h).map((k) => ({ key: k, value: h[k] })),
});
const __response = res ? { __response: true, code: res.status, status: res.status, responseTime: res.time, headers: __hdr(res.headers || {}),
  json: () => JSON.parse(res.body), text: () => res.body, size: () => ({ body: res.body.length }) } : undefined;
if (__response) Object.defineProperty(__response, "to", { get: () => expect(__response) });
const pm = {
  test, expect,
  environment: __vars, variables: __vars, collectionVariables: __vars, globals: __vars, iterationData: __vars,
  request: { method: req.method, headers: __hdr(req.headers),
    url: { toString: () => req.url, getHost: () => (req.url.split("://")[1] || "").split("/")[0] },
    get body() { return { raw: req.body, mode: "raw" }; } },
  response: __response,
  info: { requestName: "", iteration: 0, eventName: __in.response ? "test" : "prerequest" },
  sendRequest: () => { throw new Error("pm.sendRequest isn't supported in Requara yet"); },
};
const postman = { setEnvironmentVariable: env.set, getEnvironmentVariable: env.get, setGlobalVariable: env.set, getGlobalVariable: env.get, clearEnvironmentVariable: (k) => env.set(k, "") };
const tests = {};
const responseBody = res ? res.body : undefined;
const responseCode = res ? { code: res.status } : undefined;
const responseTime = res ? res.time : undefined;
const responseHeaders = res ? res.headers : undefined;
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
		"\ntry { (function () {\n" + script + "\n})(); } catch (e) { __out.error = String((e && e.message) || e); }\n" +
		"Object.keys(tests).forEach((k) => __out.tests.push({ name: k, pass: !!tests[k], ms: 0, error: tests[k] ? undefined : \"failed\" }));\nJSON.stringify(__out);"

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
	dir, err := emptyRoot()
	if err != nil {
		return "", fmt.Errorf("start sandbox: %w", err)
	}
	rt, err := qjs.New(qjs.Option{
		// The runtime mounts CWD as the script's "/": an empty, read-only
		// directory, never the server's own working directory.
		CWD:                dir,
		Context:            ctx,
		CloseOnContextDone: true,
		MemoryLimit:        memoryLimit,
		MaxStackSize:       stackLimit,
		// Scripts log through console.log (collected in __out); nothing goes to
		// the process's stdout, which the browser build doesn't have.
		Stdout: io.Discard,
		Stderr: io.Discard,
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

var (
	rootOnce sync.Once
	rootDir  string
	rootErr  error
)

// emptyRoot is an empty directory nobody can write to, created once.
func emptyRoot() (string, error) {
	rootOnce.Do(func() {
		if runtime.GOOS == "js" {
			rootDir = "/" // the browser build has no filesystem at all
			return
		}
		if rootDir, rootErr = os.MkdirTemp("", "requara-sandbox-"); rootErr == nil {
			rootErr = os.Chmod(rootDir, 0o500)
		}
	})
	return rootDir, rootErr
}

func cleanErr(err error) string {
	s := err.Error()
	if i := strings.Index(s, "\n"); i > 0 {
		s = s[:i]
	}
	return s
}
