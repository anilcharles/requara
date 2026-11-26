// Package core is Requara's API-platform logic with no database: building
// OpenAPI documents, the design editor's operations, version bumps, docs and
// mock responses. The server uses it with Postgres; the web app's personal
// workspace uses the same code compiled to WebAssembly (cmd/requara-wasm), so
// both behave the same.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/anilcharles/requara/server/api/internal/spec"
	"github.com/getkin/kin-openapi/openapi3"
)

// ----- new APIs -----

// AddResourceTemplate fills doc with a small REST resource (/items).
func AddResourceTemplate(doc *openapi3.T) {
	item := openapi3.NewObjectSchema().
		WithProperty("id", openapi3.NewStringSchema()).
		WithProperty("name", openapi3.NewStringSchema()).
		WithProperty("created_at", openapi3.NewDateTimeSchema())
	item.Properties["id"].Value.Example = "itm_123"
	item.Properties["name"].Value.Example = "First item"
	doc.Components = &openapi3.Components{Schemas: openapi3.Schemas{"Item": openapi3.NewSchemaRef("", item)}}
	ref := openapi3.NewSchemaRef("#/components/schemas/Item", item)
	ok := func(desc string, schema *openapi3.SchemaRef) *openapi3.Responses {
		resp := openapi3.NewResponse().WithDescription(desc).WithJSONSchemaRef(schema)
		return openapi3.NewResponses(openapi3.WithStatus(200, &openapi3.ResponseRef{Value: resp}))
	}
	body := &openapi3.RequestBodyRef{Value: openapi3.NewRequestBody().WithRequired(true).WithJSONSchema(
		openapi3.NewObjectSchema().WithProperty("name", openapi3.NewStringSchema()).WithRequired([]string{"name"}))}
	idParam := &openapi3.ParameterRef{Value: openapi3.NewPathParameter("id").WithSchema(openapi3.NewStringSchema())}
	doc.Tags = openapi3.Tags{{Name: "Items"}}
	doc.Paths.Set("/items", &openapi3.PathItem{
		Get:  &openapi3.Operation{Tags: []string{"Items"}, OperationID: "listItems", Summary: "List items", Responses: ok("Items", openapi3.NewSchemaRef("", openapi3.NewArraySchema().WithItems(item)))},
		Post: &openapi3.Operation{Tags: []string{"Items"}, OperationID: "createItem", Summary: "Create an item", RequestBody: body, Responses: ok("Created", ref)},
	})
	doc.Paths.Set("/items/{id}", &openapi3.PathItem{
		Parameters: openapi3.Parameters{idParam},
		Get:        &openapi3.Operation{Tags: []string{"Items"}, OperationID: "getItem", Summary: "Retrieve an item", Responses: ok("The item", ref)},
		Delete:     &openapi3.Operation{Tags: []string{"Items"}, OperationID: "deleteItem", Summary: "Delete an item", Responses: ok("Deleted", ref)},
	})
}

// NormalizeVersion turns "1.2" into "v1.2" (empty is v1.0.0).
func NormalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "v1.0.0"
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

// FirstSentence is a short description for the catalog.
func FirstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ".\n"); i > 0 && i < 120 {
		return s[:i]
	}
	if len(s) > 120 {
		return s[:117] + "…"
	}
	return s
}

// Validate checks a document the way saving it does.
func Validate(ctx context.Context, doc *openapi3.T) error {
	if err := doc.Validate(ctx, openapi3.DisableExamplesValidation()); err != nil {
		return fmt.Errorf("invalid OpenAPI: %w", err)
	}
	return nil
}

// ----- saved requests for an API's operations -----

// NewRequest is a saved request generated for one operation.
type NewRequest struct {
	Folder       string           `json:"folder"`
	Name         string           `json:"name"`
	Method       string           `json:"method"`
	URL          string           `json:"url"`
	Params       []map[string]any `json:"params"`
	Headers      []map[string]any `json:"headers"`
	BodyMode     string           `json:"bodyMode"`
	Body         string           `json:"body"`
	OperationKey string           `json:"operationKey"`
	Sort         int              `json:"sort"`
}

var pathParam = regexp.MustCompile(`\{([^}]+)\}`)

// RequestsFor returns a request for every operation not in existing.
func RequestsFor(doc *openapi3.T, existing map[string]bool, urlPrefix string) []NewRequest {
	out := []NewRequest{}
	for i, op := range spec.Operations(doc) {
		if existing[op.Key] {
			continue
		}
		o, _, _ := spec.Find(doc, op.Key)
		body, mode := spec.RequestExample(o), "none"
		headers := []map[string]any{{"key": "Accept", "value": "application/json", "enabled": true}}
		if body != "" {
			mode = "json"
			headers = append([]map[string]any{{"key": "Content-Type", "value": "application/json", "enabled": true}}, headers...)
		}
		params := []map[string]any{}
		for _, prm := range o.Parameters {
			if prm.Value != nil && prm.Value.In == "query" {
				params = append(params, map[string]any{"key": prm.Value.Name, "value": "", "description": prm.Value.Description, "enabled": false})
			}
		}
		name := op.Summary
		if name == "" {
			name = op.Key
		}
		out = append(out, NewRequest{
			Folder: op.Tag, Name: name, Method: op.Method, URL: urlPrefix + pathParam.ReplaceAllString(op.Path, "{{$1}}"),
			Params: params, Headers: headers, BodyMode: mode, Body: body, OperationKey: op.Key, Sort: i,
		})
	}
	return out
}

// ----- design editor -----

// OperationDetail is one operation for the design editor, with fields new
// since the published version marked.
func OperationDetail(draft, published *openapi3.T, key string) (map[string]any, error) {
	op, item, err := spec.Find(draft, key)
	if err != nil {
		return nil, err
	}
	fields := spec.Fields(op)
	isNewOp := false
	if published != nil {
		if pubOp, _, perr := spec.Find(published, key); perr == nil {
			spec.MarkNew(fields, pubOp)
		} else {
			isNewOp = true
		}
	}
	method, path, _ := spec.SplitKey(key)
	return map[string]any{
		"key": key, "method": method, "path": path, "summary": op.Summary, "description": op.Description,
		"operationId": op.OperationID, "fields": fields, "params": spec.Params(op, item),
		"contentType": "application/json", "new": isNewOp, "responses": spec.Responses(op),
	}, nil
}

// OperationEdit is what the design editor saves.
type OperationEdit struct {
	Key         string       `json:"key"`
	Method      string       `json:"method"`
	Path        string       `json:"path"`
	Summary     string       `json:"summary"`
	Description string       `json:"description"`
	Fields      []spec.Field `json:"fields"`
}

// EditError carries the HTTP status an edit should fail with.
type EditError struct {
	Status int
	Msg    string
}

func (e *EditError) Error() string { return e.Msg }

// UpdateOperation applies an edit to doc and returns the operation's key
// (which changes when the method or path does).
func UpdateOperation(ctx context.Context, doc *openapi3.T, in OperationEdit) (string, error) {
	op, item, err := spec.Find(doc, in.Key)
	if err != nil {
		return "", &EditError{http.StatusNotFound, err.Error()}
	}
	op.Summary, op.Description = strings.TrimSpace(in.Summary), strings.TrimSpace(in.Description)
	if in.Fields != nil {
		if len(in.Fields) == 0 {
			op.RequestBody = nil
		} else if err := spec.SetFields(op, in.Fields); err != nil {
			return "", &EditError{http.StatusBadRequest, err.Error()}
		}
	}
	newMethod, newPath := strings.ToUpper(strings.TrimSpace(in.Method)), strings.TrimSpace(in.Path)
	oldMethod, oldPath, _ := spec.SplitKey(in.Key)
	newKey := in.Key
	if newMethod != "" && newPath != "" && (newMethod != oldMethod || newPath != oldPath) {
		if !strings.HasPrefix(newPath, "/") {
			return "", &EditError{http.StatusBadRequest, "Paths start with /"}
		}
		if _, _, err := spec.Find(doc, spec.OpKey(newMethod, newPath)); err == nil {
			return "", &EditError{http.StatusConflict, newMethod + " " + newPath + " already exists"}
		}
		item.SetOperation(oldMethod, nil)
		if len(item.Operations()) == 0 {
			doc.Paths.Delete(oldPath)
		}
		target := doc.Paths.Value(newPath)
		if target == nil {
			target = &openapi3.PathItem{}
			doc.Paths.Set(newPath, target)
		}
		target.SetOperation(newMethod, op)
		newKey = spec.OpKey(newMethod, newPath)
	}
	if err := doc.Validate(ctx, openapi3.DisableExamplesValidation()); err != nil {
		return "", &EditError{http.StatusBadRequest, "That change makes the spec invalid: " + err.Error()}
	}
	return newKey, nil
}

// ----- versions -----

// Bump suggests the next version: major for breaking changes, minor for
// additions, patch otherwise.
func Bump(version string, changes []spec.Change) string {
	v := strings.TrimPrefix(version, "v")
	parts := strings.SplitN(v, ".", 3)
	for len(parts) < 3 {
		parts = append(parts, "0")
	}
	n := make([]int, 3)
	for i, s := range parts {
		n[i], _ = strconv.Atoi(strings.SplitN(s, "-", 2)[0])
	}
	breaking, added := false, false
	for _, c := range changes {
		breaking = breaking || c.Breaking
		added = added || c.Kind == "added"
	}
	switch {
	case breaking:
		n = []int{n[0] + 1, 0, 0}
	case added:
		n = []int{n[0], n[1] + 1, 0}
	default:
		n[2]++
	}
	return fmt.Sprintf("v%d.%d.%d", n[0], n[1], n[2])
}

// Changes compares the draft with the published version.
func Changes(draft, published *openapi3.T, publishedVersion, draftVersion string) (changes []spec.Change, from, next string, err error) {
	if published == nil {
		return []spec.Change{}, "", draftVersion, nil
	}
	changes, err = spec.Diff(published, draft)
	if err != nil {
		return nil, "", "", err
	}
	if changes == nil {
		changes = []spec.Change{}
	}
	return changes, publishedVersion, Bump(publishedVersion, changes), nil
}

// ----- docs -----

// OpDoc is one operation in the docs.
type OpDoc struct {
	spec.Operation
	Params         []spec.Param        `json:"params"`
	Fields         []spec.Field        `json:"fields"`
	RequestExample string              `json:"requestExample"`
	Responses      []spec.ResponseInfo `json:"responses"`
	Samples        map[string]string   `json:"samples"`
}

// DocOperations documents every operation, with code samples.
func DocOperations(doc *openapi3.T) []OpDoc {
	base := spec.BaseURL(doc)
	ops := []OpDoc{}
	for _, o := range spec.Operations(doc) {
		op, item, _ := spec.Find(doc, o.Key)
		ops = append(ops, OpDoc{
			Operation: o, Params: spec.Params(op, item), Fields: spec.Fields(op),
			RequestExample: spec.RequestExample(op), Responses: spec.Responses(op),
			Samples: spec.Samples(base, o.Method, o.Path, op),
		})
	}
	return ops
}

// ----- mock servers -----

type route struct {
	re     *regexp.Regexp
	names  []string
	path   string
	params int
}

var paramRe = regexp.MustCompile(`\{([^}]+)\}`)

func routesFor(doc *openapi3.T) []route {
	var out []route
	for path := range doc.Paths.Map() {
		var names []string
		segs := strings.Split(strings.Trim(path, "/"), "/")
		for i, seg := range segs {
			if m := paramRe.FindStringSubmatch(seg); m != nil && m[0] == seg {
				names = append(names, m[1])
				segs[i] = `([^/]+)`
			} else {
				segs[i] = regexp.QuoteMeta(seg)
			}
		}
		pattern := "^/" + strings.Join(segs, "/") + "/?$"
		out = append(out, route{re: regexp.MustCompile(pattern), names: names, path: path, params: len(names)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].params < out[j].params }) // literal paths win
	return out
}

// MockResponse is what a mock server answers.
type MockResponse struct {
	Status      int    `json:"status"`
	ContentType string `json:"contentType"`
	Body        string `json:"body"`
}

// Problem is an RFC 9457 problem response.
func Problem(status int, title, detail string) MockResponse {
	b, _ := json.Marshal(map[string]any{"title": title, "status": status, "detail": detail})
	return MockResponse{Status: status, ContentType: "application/problem+json", Body: string(b) + "\n"}
}

// InjectedError decides whether this call fails on purpose (errorRate %).
func InjectedError(errorRate int) (MockResponse, bool) {
	if errorRate > 0 && rand.IntN(100) < errorRate {
		return Problem(http.StatusInternalServerError, "Injected error", "This mock server returns errors for "+strconv.Itoa(errorRate)+"% of calls."), true
	}
	return MockResponse{}, false
}

// MockAnswer answers method path from the spec's examples. Prefer: code=402
// picks a documented response; path ids and request fields are echoed back.
func MockAnswer(doc *openapi3.T, method, path, prefer string, reqBody io.Reader) MockResponse {
	var op *openapi3.Operation
	pathVals := map[string]string{}
	for _, rt := range routesFor(doc) {
		m := rt.re.FindStringSubmatch(path)
		if m == nil {
			continue
		}
		item := doc.Paths.Value(rt.path)
		if op = item.GetOperation(method); op == nil {
			return Problem(http.StatusMethodNotAllowed, "Method not allowed", method+" isn't defined for "+rt.path)
		}
		for i, n := range rt.names {
			pathVals[n] = m[i+1]
		}
		break
	}
	if op == nil {
		return Problem(http.StatusNotFound, "No such endpoint", path+" isn't in the spec")
	}
	want := ""
	if strings.HasPrefix(prefer, "code=") {
		want = strings.TrimPrefix(prefer, "code=")
	}
	codes := []string{}
	for c := range op.Responses.Map() {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	chosen := ""
	for _, c := range codes {
		if (want != "" && c == want) || (want == "" && strings.HasPrefix(c, "2")) {
			chosen = c
			break
		}
	}
	if chosen == "" && len(codes) > 0 {
		chosen = codes[0]
	}
	status, _ := strconv.Atoi(chosen)
	if status == 0 {
		status = 200
	}
	resp := op.Responses.Value(chosen)
	body := ""
	if resp != nil && resp.Value != nil {
		body = spec.ResponseExample(resp.Value)
	}
	if body != "" && strings.HasPrefix(body, "{") {
		var obj map[string]any
		if json.Unmarshal([]byte(body), &obj) == nil {
			if id, ok := pathVals["id"]; ok {
				if _, has := obj["id"]; has {
					obj["id"] = id
				}
			}
			if status < 300 && (method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch) && reqBody != nil {
				var in map[string]any
				raw, _ := io.ReadAll(io.LimitReader(reqBody, 1<<20))
				if json.Unmarshal(raw, &in) == nil {
					for k, v := range in {
						if _, has := obj[k]; has {
							obj[k] = v
						}
					}
					if c, ok := in["capture"].(bool); ok && !c {
						if _, has := obj["captured"]; has {
							obj["captured"], obj["status"], obj["amount_captured"] = false, "requires_capture", 0
						}
					}
				}
			}
			var schema *openapi3.Schema
			if mt := resp.Value.Content.Get("application/json"); mt != nil && mt.Schema != nil {
				schema = mt.Schema.Value
			}
			body = spec.OrderedJSON(obj, schema)
		}
	}
	ct := "application/json"
	if resp != nil && resp.Value != nil {
		for k := range resp.Value.Content {
			ct = k
			break
		}
	}
	return MockResponse{Status: status, ContentType: ct, Body: body}
}

// ErrNotFound is returned when something doesn't exist.
var ErrNotFound = errors.New("not found")
