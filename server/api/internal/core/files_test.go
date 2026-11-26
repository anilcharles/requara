package core

import (
	"archive/zip"
	"bytes"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/anilcharles/requara/server/api/internal/runner"
)

func TestFilesRoundTrip(t *testing.T) {
	c := Collection{
		Name: "Orders API", Description: "Orders", PreScript: "pm.variables.set('a', 1)",
		Variables: []runner.KV{{Key: "base", Value: "https://api.example.com", Enabled: true}, {Key: "off", Value: "x"}},
		Auth:      runner.Auth{Type: "oauth2", GrantType: "authorization_code", AuthURL: "https://id/authorize", TokenURL: "https://id/token", ClientID: "c", Token: "personal-token", RefreshToken: "rt", ExpiresAt: 99},
		Requests: []Request{
			{Name: "Health", Method: "get", URL: "{{base}}/health", BodyMode: "none", Auth: runner.Auth{Type: "inherit"}},
			{Folder: "Orders", Name: "List orders", Method: "GET", URL: "{{base}}/orders", Params: []runner.KV{{Key: "limit", Value: "10", Enabled: true}}, BodyMode: "none", Auth: runner.Auth{Type: "inherit"},
				PostScript: "pm.test('ok', () => {\n  pm.response.to.have.status(200);\n});\n",
				Examples:   []Example{{Name: "200", Status: 200, Body: `{"items":[]}`}}},
			{Folder: "Orders", Name: "Create order", Method: "POST", URL: "{{base}}/orders", BodyMode: "json", Body: "{\n  \"sku\": \"A1\"\n}", Auth: runner.Auth{Type: "bearer", Token: "{{token}}"},
				Headers: []runner.KV{{Key: "Idempotency-Key", Value: "{{$uuid}}", Enabled: true}}},
			{Folder: "Uploads", Name: "Upload", Method: "POST", URL: "{{base}}/files", BodyMode: "form-data", Auth: runner.Auth{Type: "inherit"},
				FormData: []runner.FormField{{Key: "title", Value: "x", Type: "text", Enabled: true}, {Key: "doc", Value: "aGVsbG8=", Type: "file", FileName: "a.txt", Enabled: true}}},
		},
	}
	zipped, err := ExportFiles(c)
	if err != nil {
		t.Fatal(err)
	}
	z, _ := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	files := map[string]string{}
	var names []string
	for _, f := range z.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		files[f.Name] = string(b)
		names = append(names, f.Name)
	}
	sort.Strings(names)
	want := []string{"orders-api/001-health.yaml", "orders-api/collection.yaml", "orders-api/orders/001-list-orders.yaml", "orders-api/orders/002-create-order.yaml", "orders-api/uploads/001-upload.yaml"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("files %v", names)
	}
	head := files["orders-api/collection.yaml"]
	if strings.Contains(head, "personal-token") || strings.Contains(head, "refreshToken") || strings.Contains(head, "expiresAt") {
		t.Fatalf("signed-in tokens belong to a person, not the repo:\n%s", head)
	}
	list := files["orders-api/orders/001-list-orders.yaml"]
	if !strings.Contains(list, "testScript: |") || strings.Contains(list, "\nbody:") || strings.Contains(list, "\nauth:") {
		t.Fatalf("scripts as blocks, empty fields left out:\n%s", list)
	}
	if strings.Contains(files["orders-api/uploads/001-upload.yaml"], "aGVsbG8=") {
		t.Fatal("file contents stay out of the export")
	}

	back, err := ParseFiles(zipped)
	if err != nil {
		t.Fatal(err)
	}
	if back.Name != c.Name || back.PreScript != c.PreScript || len(back.Requests) != 4 || back.Auth.Token != "" || back.Auth.AuthURL != "https://id/authorize" {
		t.Fatalf("collection %+v", back)
	}
	if !back.Variables[0].Enabled || back.Variables[1].Enabled {
		t.Fatalf("variables %+v", back.Variables)
	}
	order := []string{}
	for _, r := range back.Requests {
		order = append(order, r.Folder+"/"+r.Name)
	}
	if strings.Join(order, ",") != "/Health,Orders/List orders,Orders/Create order,Uploads/Upload" {
		t.Fatalf("order %v", order)
	}
	create := back.Requests[2]
	if create.Method != "POST" || create.Body != c.Requests[2].Body || create.Auth.Token != "{{token}}" || create.Headers[0].Value != "{{$uuid}}" {
		t.Fatalf("create %+v", create)
	}
	if back.Requests[1].PostScript != c.Requests[1].PostScript || back.Requests[1].Examples[0].Body != `{"items":[]}` {
		t.Fatalf("list %+v", back.Requests[1])
	}
	up := back.Requests[3]
	if up.BodyMode != "form-data" || up.FormData[0].Type != "text" || up.FormData[1].Type != "file" || up.FormData[1].FileName != "a.txt" {
		t.Fatalf("upload %+v", up.FormData)
	}
	// Exporting again gives the same files: stable for diffs.
	again, _ := ExportFiles(*back)
	z2, _ := zip.NewReader(bytes.NewReader(again), int64(len(again)))
	for _, f := range z2.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		if f.Name != "orders-api/collection.yaml" && files[f.Name] != string(b) {
			t.Errorf("%s changed on a round trip:\n%s\n---\n%s", f.Name, files[f.Name], b)
		}
	}
	if _, err := ParseFiles([]byte("not a zip")); err == nil {
		t.Fatal("not a zip should fail")
	}
}
