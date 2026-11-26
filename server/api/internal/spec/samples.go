package spec

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// Samples returns code samples for calling an operation.
func Samples(baseURL, method, path string, op *openapi3.Operation) map[string]string {
	url := strings.TrimRight(baseURL, "/") + path
	body := RequestExample(op)
	var fields map[string]any
	if body != "" {
		_ = json.Unmarshal([]byte(body), &fields)
	}
	var keys []string
	if s := bodySchema(op); s != nil {
		keys = orderedProps(s)
	} else {
		for k := range fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
	}
	return map[string]string{
		"cURL":   curl(method, url, body),
		"Node":   node(method, url, body, op),
		"Python": python(method, url, keys, fields, op),
	}
}

func curl(method, url, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "curl -X %s %s \\\n  -H \"Authorization: Bearer $ACME_KEY\"", method, url)
	if body != "" {
		b.WriteString(" \\\n  -H \"Content-Type: application/json\" \\\n  -d '" + compact(body) + "'")
	}
	return b.String()
}

func compact(s string) string {
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return s
	}
	out, _ := json.Marshal(v)
	return string(out)
}

var nonIdent = regexp.MustCompile(`[^A-Za-z0-9]+`)

func funcName(op *openapi3.Operation, method string) string {
	if op != nil && op.OperationID != "" {
		return op.OperationID
	}
	return strings.ToLower(method)
}

func node(method, url, body string, op *openapi3.Operation) string {
	var b strings.Builder
	b.WriteString("const res = await fetch(\"" + url + "\", {\n")
	b.WriteString("  method: \"" + method + "\",\n")
	b.WriteString("  headers: {\n    Authorization: `Bearer ${process.env.ACME_KEY}`,\n")
	if body != "" {
		b.WriteString("    \"Content-Type\": \"application/json\",\n  },\n")
		b.WriteString("  body: JSON.stringify(" + indentJS(body) + "),\n")
	} else {
		b.WriteString("  },\n")
	}
	b.WriteString("});\nconst " + safeVar(funcName(op, method)) + " = await res.json();")
	return b.String()
}

func safeVar(s string) string {
	s = nonIdent.ReplaceAllString(s, "_")
	if s == "" || (s[0] >= '0' && s[0] <= '9') {
		s = "data"
	}
	return s
}

func indentJS(body string) string {
	return strings.ReplaceAll(body, "\n", "\n  ")
}

func python(method, url string, keys []string, fields map[string]any, op *openapi3.Operation) string {
	var b strings.Builder
	b.WriteString("import os, requests\n\n")
	b.WriteString("res = requests." + strings.ToLower(method) + "(\n")
	b.WriteString("    \"" + url + "\",\n")
	b.WriteString("    headers={\"Authorization\": f\"Bearer {os.environ['ACME_KEY']}\"},\n")
	if len(fields) > 0 {
		b.WriteString("    json={\n")
		for _, k := range keys {
			v, ok := fields[k]
			if !ok {
				continue
			}
			fmt.Fprintf(&b, "        %q: %s,\n", k, pyValue(v))
		}
		b.WriteString("    },\n")
	}
	b.WriteString(")\n" + safeVar(funcName(op, method)) + " = res.json()")
	return b.String()
}

func pyValue(v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	case string:
		return fmt.Sprintf("%q", x)
	default:
		raw, _ := json.Marshal(x)
		s := string(raw)
		s = strings.ReplaceAll(s, "true", "True")
		s = strings.ReplaceAll(s, "false", "False")
		return strings.ReplaceAll(s, "null", "None")
	}
}
