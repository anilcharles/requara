// Package spec holds Requara's OpenAPI logic: parsing (including Swagger 2.0),
// listing operations, flattening and editing request-body fields, examples,
// linting (vacuum), change detection (oasdiff) and code samples.
package spec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// Parse accepts OpenAPI 3.x or Swagger 2.0, as YAML or JSON.
func Parse(data []byte) (*openapi3.T, error) {
	var probe map[string]any
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("not valid YAML or JSON: %w", err)
	}
	if _, ok := probe["swagger"]; ok {
		jsonData, err := toJSON(probe)
		if err != nil {
			return nil, err
		}
		var v2 openapi2.T
		if err := json.Unmarshal(jsonData, &v2); err != nil {
			return nil, fmt.Errorf("swagger 2.0: %w", err)
		}
		t, err := openapi2conv.ToV3(&v2)
		if err != nil {
			return nil, fmt.Errorf("convert swagger 2.0: %w", err)
		}
		return t, nil
	}
	if _, ok := probe["openapi"]; !ok {
		return nil, errors.New(`missing "openapi" or "swagger" version field`)
	}
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	t, err := loader.LoadFromData(data)
	if err != nil {
		return nil, err
	}
	if err := t.Validate(context.Background(), openapi3.DisableExamplesValidation()); err != nil {
		return nil, fmt.Errorf("invalid OpenAPI: %w", err)
	}
	return t, nil
}

// toJSON converts YAML-decoded maps (which may have map[any]any) to JSON.
func toJSON(v any) ([]byte, error) {
	var fix func(any) any
	fix = func(x any) any {
		switch m := x.(type) {
		case map[string]any:
			for k, v := range m {
				m[k] = fix(v)
			}
			return m
		case map[any]any:
			out := map[string]any{}
			for k, v := range m {
				out[fmt.Sprint(k)] = fix(v)
			}
			return out
		case []any:
			for i := range m {
				m[i] = fix(m[i])
			}
			return m
		}
		return x
	}
	return json.Marshal(fix(v))
}

// FromJSON loads a stored document (already validated when saved).
func FromJSON(data []byte) (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	return loader.LoadFromData(data)
}

func ToJSON(t *openapi3.T) ([]byte, error) { return json.Marshal(t) }

// Blank returns a minimal valid document.
func Blank(title, version, baseURL string) *openapi3.T {
	t := &openapi3.T{
		OpenAPI: "3.0.3",
		Info:    &openapi3.Info{Title: title, Version: strings.TrimPrefix(version, "v")},
		Paths:   openapi3.NewPaths(),
	}
	if baseURL != "" {
		t.Servers = openapi3.Servers{{URL: baseURL}}
	}
	return t
}

var methodOrder = map[string]int{"POST": 0, "GET": 1, "PUT": 2, "PATCH": 3, "DELETE": 4, "HEAD": 5, "OPTIONS": 6}

type Operation struct {
	Key         string `json:"key"` // "POST /v2/charges"
	Method      string `json:"method"`
	Path        string `json:"path"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	OperationID string `json:"operationId"`
	Tag         string `json:"tag"`
	Deprecated  bool   `json:"deprecated"`
}

func OpKey(method, path string) string { return strings.ToUpper(method) + " " + path }

func SplitKey(key string) (method, path string, ok bool) {
	m, p, ok := strings.Cut(key, " ")
	return strings.ToUpper(m), p, ok
}

// Operations lists operations grouped by first tag, in a stable order.
func Operations(t *openapi3.T) []Operation {
	out := []Operation{}
	if t.Paths == nil {
		return out
	}
	tagRank := map[string]int{}
	for i, tg := range t.Tags {
		tagRank[tg.Name] = i
	}
	for path, item := range t.Paths.Map() {
		for method, op := range item.Operations() {
			tag := "General"
			if len(op.Tags) > 0 {
				tag = op.Tags[0]
			}
			out = append(out, Operation{
				Key: OpKey(method, path), Method: strings.ToUpper(method), Path: path,
				Summary: op.Summary, Description: op.Description, OperationID: op.OperationID,
				Tag: tag, Deprecated: op.Deprecated,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		ra, oka := tagRank[a.Tag]
		rb, okb := tagRank[b.Tag]
		if oka != okb {
			return oka
		}
		if ra != rb {
			return ra < rb
		}
		if a.Tag != b.Tag {
			return a.Tag < b.Tag
		}
		if xa, xb := sortKey(a), sortKey(b); xa != xb {
			return xa < xb
		}
		return methodOrder[a.Method] < methodOrder[b.Method]
	})
	return out
}

// sortKey keeps operations in the order they were written when x-order is set.
func sortKey(o Operation) string { return o.Path }

func Find(t *openapi3.T, key string) (*openapi3.Operation, *openapi3.PathItem, error) {
	method, path, ok := SplitKey(key)
	if !ok || t.Paths == nil {
		return nil, nil, fmt.Errorf("unknown operation %q", key)
	}
	item := t.Paths.Value(path)
	if item == nil {
		return nil, nil, fmt.Errorf("unknown path %q", path)
	}
	op := item.GetOperation(method)
	if op == nil {
		return nil, nil, fmt.Errorf("unknown operation %q", key)
	}
	return op, item, nil
}

// ----- request-body fields -----

type Field struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"` // integer | number | string | boolean | object | array
	Format      string   `json:"format,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Required    bool     `json:"required"`
	Description string   `json:"description"`
	Depth       int      `json:"depth"`
	Example     any      `json:"example,omitempty"`
	New         bool     `json:"new,omitempty"` // not in the last published version
}

func bodySchema(op *openapi3.Operation) *openapi3.Schema {
	if op == nil || op.RequestBody == nil || op.RequestBody.Value == nil {
		return nil
	}
	mt := op.RequestBody.Value.Content.Get("application/json")
	if mt == nil || mt.Schema == nil {
		return nil
	}
	return mt.Schema.Value
}

func typeOf(s *openapi3.Schema) string {
	if s == nil || s.Type == nil || len(*s.Type) == 0 {
		if s != nil && len(s.Properties) > 0 {
			return "object"
		}
		return "string"
	}
	return (*s.Type)[0]
}

// orderedProps returns property names in x-order order, then required, then A–Z.
func orderedProps(s *openapi3.Schema) []string {
	seen := map[string]bool{}
	var names []string
	if raw, ok := s.Extensions["x-order"]; ok {
		if arr, ok := raw.([]any); ok {
			for _, n := range arr {
				name := fmt.Sprint(n)
				if _, exists := s.Properties[name]; exists && !seen[name] {
					names, seen[name] = append(names, name), true
				}
			}
		}
	}
	req := map[string]bool{}
	for _, r := range s.Required {
		req[r] = true
	}
	var rest []string
	for name := range s.Properties {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Slice(rest, func(i, j int) bool {
		if req[rest[i]] != req[rest[j]] {
			return req[rest[i]]
		}
		return rest[i] < rest[j]
	})
	return append(names, rest...)
}

func flatten(s *openapi3.Schema, depth int, out *[]Field) {
	if s == nil || depth > 3 {
		return
	}
	req := map[string]bool{}
	for _, r := range s.Required {
		req[r] = true
	}
	for _, name := range orderedProps(s) {
		ref := s.Properties[name]
		if ref == nil || ref.Value == nil {
			continue
		}
		p := ref.Value
		f := Field{Name: name, Type: typeOf(p), Format: p.Format, Required: req[name], Description: p.Description, Depth: depth, Example: p.Example}
		for _, e := range p.Enum {
			f.Enum = append(f.Enum, fmt.Sprint(e))
		}
		*out = append(*out, f)
		if f.Type == "object" {
			flatten(p, depth+1, out)
		}
	}
}

// Fields flattens the JSON request body of an operation.
func Fields(op *openapi3.Operation) []Field {
	out := []Field{} // JSON [] rather than null
	flatten(bodySchema(op), 0, &out)
	return out
}

// MarkNew flags fields that don't exist in the published operation.
func MarkNew(fields []Field, published *openapi3.Operation) {
	if published == nil {
		return
	}
	old := map[string]bool{}
	path := []string{}
	for _, f := range Fields(published) {
		path = append(path[:f.Depth], f.Name)
		old[strings.Join(path, ".")] = true
	}
	path = path[:0]
	for i := range fields {
		path = append(path[:fields[i].Depth], fields[i].Name)
		fields[i].New = !old[strings.Join(path, ".")]
	}
}

func schemaFor(f Field) *openapi3.Schema {
	s := &openapi3.Schema{Type: &openapi3.Types{f.Type}, Format: f.Format, Description: f.Description, Example: f.Example}
	if f.Type == "array" {
		s.Items = openapi3.NewSchemaRef("", openapi3.NewStringSchema())
	}
	for _, e := range f.Enum {
		s.Enum = append(s.Enum, e)
	}
	if f.Type == "object" {
		s.Properties = openapi3.Schemas{}
	}
	return s
}

// SetFields replaces the JSON request body schema with fields.
func SetFields(op *openapi3.Operation, fields []Field) error {
	root := &openapi3.Schema{Type: &openapi3.Types{"object"}, Properties: openapi3.Schemas{}}
	stack := []*openapi3.Schema{root}
	for i, f := range fields {
		f.Name = strings.TrimSpace(f.Name)
		if f.Name == "" {
			return fmt.Errorf("field %d has no name", i+1)
		}
		if f.Depth < 0 || f.Depth > len(stack)-1 {
			return fmt.Errorf("field %q is nested under nothing", f.Name)
		}
		stack = stack[:f.Depth+1]
		parent := stack[f.Depth]
		if _, dup := parent.Properties[f.Name]; dup {
			return fmt.Errorf("field %q appears twice", f.Name)
		}
		s := schemaFor(f)
		parent.Properties[f.Name] = openapi3.NewSchemaRef("", s)
		if f.Required {
			parent.Required = append(parent.Required, f.Name)
		}
		order, _ := parent.Extensions["x-order"].([]any)
		if parent.Extensions == nil {
			parent.Extensions = map[string]any{}
		}
		parent.Extensions["x-order"] = append(order, f.Name)
		if f.Type == "object" {
			stack = append(stack, s)
		}
	}
	if op.RequestBody == nil || op.RequestBody.Value == nil {
		op.RequestBody = &openapi3.RequestBodyRef{Value: openapi3.NewRequestBody().WithRequired(true)}
	}
	if op.RequestBody.Value.Content == nil {
		op.RequestBody.Value.Content = openapi3.Content{}
	}
	op.RequestBody.Value.Content["application/json"] = openapi3.NewMediaType().WithSchema(root)
	return nil
}

// ----- examples -----

// Example builds an example value from a schema (uses explicit examples first).
func Example(s *openapi3.Schema) any { return example(s, 0) }

func example(s *openapi3.Schema, depth int) any {
	if s == nil || depth > 6 {
		return nil
	}
	if s.Example != nil {
		return s.Example
	}
	if s.Default != nil {
		return s.Default
	}
	if len(s.Enum) > 0 {
		return s.Enum[0]
	}
	switch typeOf(s) {
	case "object":
		m := map[string]any{}
		for _, name := range orderedProps(s) {
			if p := s.Properties[name]; p != nil {
				m[name] = example(p.Value, depth+1)
			}
		}
		return m
	case "array":
		if s.Items != nil {
			return []any{example(s.Items.Value, depth+1)}
		}
		return []any{}
	case "integer":
		return 0
	case "number":
		return 0.0
	case "boolean":
		return false
	}
	switch s.Format {
	case "date-time":
		return "2026-11-13T09:12:00Z"
	case "email":
		return "user@example.com"
	case "uuid":
		return "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	}
	return "string"
}

// OrderedJSON renders v as indented JSON keeping x-order key order when s describes it.
func OrderedJSON(v any, s *openapi3.Schema) string {
	var b strings.Builder
	writeJSON(&b, v, s, 0)
	return b.String()
}

func writeJSON(b *strings.Builder, v any, s *openapi3.Schema, indent int) {
	pad := strings.Repeat("  ", indent)
	switch m := v.(type) {
	case map[string]any:
		if len(m) == 0 {
			b.WriteString("{}")
			return
		}
		keys := make([]string, 0, len(m))
		if s != nil && len(s.Properties) > 0 {
			for _, k := range orderedProps(s) {
				if _, ok := m[k]; ok {
					keys = append(keys, k)
				}
			}
		}
		have := map[string]bool{}
		for _, k := range keys {
			have[k] = true
		}
		var rest []string
		for k := range m {
			if !have[k] {
				rest = append(rest, k)
			}
		}
		sort.Strings(rest)
		keys = append(keys, rest...)
		b.WriteString("{\n")
		for i, k := range keys {
			kb, _ := json.Marshal(k)
			b.WriteString(pad + "  " + string(kb) + ": ")
			var child *openapi3.Schema
			if s != nil && s.Properties[k] != nil {
				child = s.Properties[k].Value
			}
			writeJSON(b, m[k], child, indent+1)
			if i < len(keys)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(pad + "}")
	case []any:
		if len(m) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		var item *openapi3.Schema
		if s != nil && s.Items != nil {
			item = s.Items.Value
		}
		for i, x := range m {
			b.WriteString(pad + "  ")
			writeJSON(b, x, item, indent+1)
			if i < len(m)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(pad + "]")
	default:
		raw, _ := json.Marshal(m)
		b.Write(raw)
	}
}

// RequestExample is the JSON body example for an operation ("" if none).
func RequestExample(op *openapi3.Operation) string {
	if op == nil || op.RequestBody == nil || op.RequestBody.Value == nil {
		return ""
	}
	mt := op.RequestBody.Value.Content.Get("application/json")
	if mt == nil || mt.Schema == nil {
		return ""
	}
	if mt.Example != nil {
		return OrderedJSON(mt.Example, mt.Schema.Value)
	}
	return OrderedJSON(Example(mt.Schema.Value), mt.Schema.Value)
}

type ResponseInfo struct {
	Status      string `json:"status"`
	Description string `json:"description"`
	Example     string `json:"example"`
}

// Responses lists responses with JSON examples, 2xx first.
func Responses(op *openapi3.Operation) []ResponseInfo {
	out := []ResponseInfo{}
	if op == nil || op.Responses == nil {
		return out
	}
	for code, ref := range op.Responses.Map() {
		if ref == nil || ref.Value == nil {
			continue
		}
		ri := ResponseInfo{Status: code}
		if ref.Value.Description != nil {
			ri.Description = *ref.Value.Description
		}
		ri.Example = ResponseExample(ref.Value)
		out = append(out, ri)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Status < out[j].Status })
	return out
}

func ResponseExample(r *openapi3.Response) string {
	mt := r.Content.Get("application/json")
	if mt == nil {
		for _, m := range r.Content {
			mt = m
			break
		}
	}
	if mt == nil {
		return ""
	}
	var schema *openapi3.Schema
	if mt.Schema != nil {
		schema = mt.Schema.Value
	}
	if mt.Example != nil {
		return OrderedJSON(mt.Example, schema)
	}
	for _, ex := range mt.Examples {
		if ex != nil && ex.Value != nil && ex.Value.Value != nil {
			return OrderedJSON(ex.Value.Value, schema)
		}
	}
	if schema != nil {
		return OrderedJSON(Example(schema), schema)
	}
	return ""
}

type Param struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

func Params(op *openapi3.Operation, item *openapi3.PathItem) []Param {
	out := []Param{}
	add := func(ps openapi3.Parameters) {
		for _, p := range ps {
			if p == nil || p.Value == nil {
				continue
			}
			typ := "string"
			if p.Value.Schema != nil && p.Value.Schema.Value != nil {
				typ = typeOf(p.Value.Schema.Value)
			}
			out = append(out, Param{Name: p.Value.Name, In: p.Value.In, Type: typ, Required: p.Value.Required, Description: p.Value.Description})
		}
	}
	if item != nil {
		add(item.Parameters)
	}
	add(op.Parameters)
	return out
}

func BaseURL(t *openapi3.T) string {
	if len(t.Servers) > 0 && t.Servers[0] != nil {
		return strings.TrimRight(t.Servers[0].URL, "/")
	}
	return ""
}
