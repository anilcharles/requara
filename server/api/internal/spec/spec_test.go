package spec

import (
	"os"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func loadPayments(t *testing.T) *openapi3.T {
	t.Helper()
	raw, err := os.ReadFile("../seed/specs/payments.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Round-trip through JSON the way the store does.
	js, _ := ToJSON(doc)
	doc, err = FromJSON(js)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestOperationsFieldsAndEdit(t *testing.T) {
	doc := loadPayments(t)
	ops := Operations(doc)
	if len(ops) != 11 || ops[0].Tag != "Charges" {
		t.Fatalf("ops: %d first=%+v", len(ops), ops[0])
	}
	op, _, err := Find(doc, "POST /v2/charges")
	if err != nil {
		t.Fatal(err)
	}
	fields := Fields(op)
	names := []string{}
	for _, f := range fields {
		names = append(names, strings.Repeat(">", f.Depth)+f.Name)
	}
	if strings.Join(names, ",") != "amount,currency,customer,description,metadata,>order_id,>channel" {
		t.Fatalf("fields: %v", names)
	}

	published := loadPayments(t)
	pubOp, _, _ := Find(published, "POST /v2/charges")

	// Add "capture" after customer, like the design.
	edited := append([]Field{}, fields[:3]...)
	edited = append(edited, Field{Name: "capture", Type: "boolean", Description: "Capture immediately. Defaults to true"})
	edited = append(edited, fields[3:]...)
	if err := SetFields(op, edited); err != nil {
		t.Fatal(err)
	}
	js, _ := ToJSON(doc)
	doc, _ = FromJSON(js)
	op, _, _ = Find(doc, "POST /v2/charges")
	again := Fields(op)
	MarkNew(again, pubOp)
	if again[3].Name != "capture" || !again[3].New || again[0].New || !again[0].Required {
		t.Fatalf("after edit: %+v", again)
	}

	changes, err := Diff(published, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) == 0 {
		t.Fatal("expected changes")
	}
	for _, c := range changes {
		if c.Breaking {
			t.Fatalf("adding an optional field should not be breaking: %+v", c)
		}
	}

	ex := RequestExample(op)
	if !strings.HasPrefix(ex, "{\n  \"amount\": 4200,\n  \"currency\": \"usd\"") {
		t.Fatalf("example order: %s", ex)
	}
	s := Samples("https://api.acme.dev", "POST", "/v2/charges", op)
	if !strings.Contains(s["cURL"], "-X POST https://api.acme.dev/v2/charges") || !strings.Contains(s["Python"], `"capture": False`) {
		t.Fatalf("samples: %+v", s)
	}
}

func TestLint(t *testing.T) {
	raw, _ := os.ReadFile("../seed/specs/payments.yaml")
	rep := Lint(raw)
	if rep.Rules == 0 {
		t.Fatal("no rules ran")
	}
	t.Logf("%d rules, %d issues, %d passed", rep.Rules, len(rep.Issues), len(rep.Passed))
}

func TestSwagger2(t *testing.T) {
	doc, err := Parse([]byte(`swagger: "2.0"
info: {title: Old, version: "1.0"}
host: api.example.com
basePath: /v1
schemes: [https]
paths:
  /pets:
    get:
      summary: List pets
      responses:
        "200": {description: ok}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(Operations(doc)) != 1 || BaseURL(doc) != "https://api.example.com/v1" {
		t.Fatalf("ops=%v base=%s", Operations(doc), BaseURL(doc))
	}
}
