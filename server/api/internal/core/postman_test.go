package core

import (
	"encoding/json"
	"strings"
	"testing"
)

const sample = `{
  "info": {"name": "Shop", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "variable": [{"key": "base", "value": "https://api.shop.test"}],
  "auth": {"type": "bearer", "bearer": [{"key": "token", "value": "{{token}}", "type": "string"}]},
  "event": [{"listen": "prerequest", "script": {"exec": ["console.log('collection pre')"]}}],
  "item": [
    {"name": "Orders", "item": [
      {"name": "List orders", "request": {"method": "GET", "header": [{"key": "Accept", "value": "application/json"}],
        "url": {"raw": "{{base}}/orders?limit=10&status=open", "query": [{"key": "limit", "value": "10"}, {"key": "status", "value": "open", "disabled": true}]}},
       "event": [{"listen": "test", "script": {"exec": ["pm.test('ok', function () {", "  pm.response.to.have.status(200);", "});"]}}],
       "response": [{"name": "Two orders", "code": 200, "header": [{"key": "Content-Type", "value": "application/json"}], "body": "[1,2]"}]},
      {"name": "Admin", "auth": {"type": "basic", "basic": [{"key": "username", "value": "admin"}, {"key": "password", "value": "pw"}]}, "item": [
        {"name": "Create order", "request": {"method": "POST", "header": [], "url": "{{base}}/orders",
          "body": {"mode": "raw", "raw": "{\"sku\": \"A1\"}", "options": {"raw": {"language": "json"}}}}}
      ]}
    ]},
    {"name": "Upload", "request": {"method": "POST", "url": {"raw": "{{base}}/files"}, "auth": {"type": "noauth"},
      "body": {"mode": "formdata", "formdata": [{"key": "title", "value": "x", "type": "text"}, {"key": "file", "type": "file", "src": "/tmp/a.png"}]}}}
  ]
}`

func TestPostmanRoundTrip(t *testing.T) {
	c, err := ParsePostman([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Shop" || len(c.Variables) != 1 || c.Auth.Type != "bearer" || !strings.Contains(c.PreScript, "collection pre") {
		t.Fatalf("collection: %+v", c)
	}
	if len(c.Requests) != 3 {
		t.Fatalf("requests: %d", len(c.Requests))
	}
	list, create, upload := c.Requests[0], c.Requests[1], c.Requests[2]
	if list.Folder != "Orders" || list.URL != "{{base}}/orders" || len(list.Params) != 2 || list.Params[1].Enabled || !strings.Contains(list.PostScript, "pm.response") || len(list.Examples) != 1 || list.Auth.Type != "inherit" {
		t.Errorf("list: %+v", list)
	}
	if create.Folder != "Orders / Admin" || create.BodyMode != "json" || create.Auth.Type != "basic" || create.Auth.Username != "admin" {
		t.Errorf("create: %+v", create)
	}
	if upload.BodyMode != "form-data" || len(upload.FormData) != 2 || upload.FormData[1].Type != "file" || upload.Auth.Type != "none" {
		t.Errorf("upload: %+v", upload)
	}

	out, err := ExportPostman(*c)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ParsePostman(out)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(c.Requests)
	b, _ := json.Marshal(again.Requests)
	if string(a) != string(b) {
		t.Fatalf("round trip changed the requests:\n%s\n%s", a, b)
	}
}

func TestCurl(t *testing.T) {
	r, err := ParseCurl(`curl -X POST 'https://api.example.com/v1/charges?expand=customer' \
  -H 'Authorization: Bearer sk_test_1' -H "Content-Type: application/json" \
  --data-raw '{"amount": 42}'`)
	if err != nil {
		t.Fatal(err)
	}
	if r.Method != "POST" || r.URL != "https://api.example.com/v1/charges" || r.Params[0].Key != "expand" || r.Auth.Type != "bearer" || r.Auth.Token != "sk_test_1" || r.BodyMode != "json" || r.Body != `{"amount": 42}` {
		t.Fatalf("%+v", r)
	}
	r, _ = ParseCurl(`curl https://x.test/upload -F title=hi -F file=@photo.jpg -u ana:pw`)
	if r.Method != "POST" || r.BodyMode != "form-data" || r.FormData[1].Type != "file" || r.Auth.Username != "ana" {
		t.Fatalf("%+v", r)
	}
	r, _ = ParseCurl(`curl -G https://x.test/search -d q=requara -d page=2`)
	if r.Method != "GET" || len(r.Params) != 2 || r.BodyMode != "none" {
		t.Fatalf("%+v", r)
	}
	if _, err := ParseCurl("wget https://x"); err == nil {
		t.Fatal("not curl")
	}
}
