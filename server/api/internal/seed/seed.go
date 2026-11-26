// Package seed creates the demo "Acme Payments" workspace on first start:
// six APIs (Payments with unpublished changes), saved requests, environments
// with OpenBao-encrypted secrets, and mock servers so Send works out of the box.
package seed

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/anilcharles/speklo/server/api/internal/bao"
	"github.com/anilcharles/speklo/server/api/internal/spec"
)

//go:embed specs/payments.yaml
var paymentsYAML []byte

type Syncer func(ctx context.Context, tx pgx.Tx, ws, apiID string, doc *openapi3.T, urlPrefix string) error

func Run(ctx context.Context, db *pgxpool.Pool, b *bao.Client, publicURL string, sync Syncer) error {
	var exists bool
	if err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM workspaces WHERE slug='acme-payments')`).Scan(&exists); err != nil || exists {
		return err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var ws string
	if err := tx.QueryRow(ctx, `INSERT INTO workspaces (name, slug) VALUES ('Acme Payments','acme-payments') RETURNING id`).Scan(&ws); err != nil {
		return err
	}

	// --- Payments API: published v2.4.0 plus unpublished v2.5.0 changes ---
	pub, err := spec.Parse(paymentsYAML)
	if err != nil {
		return fmt.Errorf("payments spec: %w", err)
	}
	draft, _ := spec.Parse(paymentsYAML)
	addDraftChanges(draft)
	payments, err := insertAPI(ctx, tx, ws, "Payments API", "payments-api", "Charges, refunds, payouts and disputes", "Payments",
		"published", "v2.4.0", draft, pub)
	if err != nil {
		return err
	}
	if err := sync(ctx, tx, ws, payments, pub, "{{base_url}}"); err != nil {
		return err
	}
	if err := customizeCreateCharge(ctx, tx, payments); err != nil {
		return err
	}

	// --- the rest of the catalog ---
	type small struct {
		name, slug, desc, owner, status, version, varName string
		ops                                               []op
	}
	others := []small{
		{"Identity API", "identity-api", "Users, sessions and OAuth clients", "Platform", "published", "v1.9.2", "identity_url", identityOps},
		{"Notifications", "notifications", "Email, SMS and push delivery", "Growth", "published", "v3.1.0", "notifications_url", notificationOps},
		{"Webhooks", "webhooks", "Event subscriptions and delivery logs", "Payments", "published", "v2.0.0", "webhooks_url", webhookOps},
		{"Ledger API", "ledger-api", "Double-entry balances and journals", "Finance eng", "draft", "v0.7.0", "ledger_url", ledgerOps},
		{"Reports API", "reports-api", "CSV and scheduled financial exports", "Finance eng", "deprecated", "v1.0.3", "reports_url", reportOps},
	}
	ids := map[string]string{"payments-api": payments}
	for _, o := range others {
		doc := build(o.name, o.version, "https://api.acme.dev", o.ops)
		var published *openapi3.T
		if o.status != "draft" {
			published = doc
		}
		id, err := insertAPI(ctx, tx, ws, o.name, o.slug, o.desc, o.owner, o.status, o.version, doc, published)
		if err != nil {
			return err
		}
		ids[o.slug] = id
		if err := sync(ctx, tx, ws, id, doc, "{{"+o.varName+"}}"); err != nil {
			return err
		}
	}

	// --- mock servers ---
	for _, m := range []struct {
		api, name, slug string
		enabled         bool
		latency         int
	}{
		{"payments-api", "Payments sandbox", "payments-sandbox", true, 120},
		{"identity-api", "Identity local", "identity-local", true, 40},
		{"webhooks", "Webhooks replay", "webhooks-replay", false, 0},
	} {
		if _, err := tx.Exec(ctx, `INSERT INTO mock_servers (workspace_id, api_id, name, slug, enabled, latency_ms) VALUES ($1,$2,$3,$4,$5,$6)`,
			ws, ids[m.api], m.name, m.slug, m.enabled, m.latency); err != nil {
			return err
		}
	}

	// --- environments; secrets are encrypted by OpenBao Transit ---
	mock := func(slug string) string { return publicURL + "/mock/" + slug }
	type v struct{ key, value, kind string }
	envs := []struct {
		name, color string
		global      bool
		vars        []v
	}{
		{"Production", "#52C41A", false, []v{
			{"base_url", mock("payments-sandbox"), "default"},
			{"identity_url", mock("identity-local"), "default"},
			{"webhooks_url", mock("webhooks-replay"), "default"},
			{"notifications_url", "https://api.acme.dev", "default"},
			{"ledger_url", "https://api.acme.dev", "default"},
			{"reports_url", "https://api.acme.dev", "default"},
			{"secret_key", "demo-live-secret-not-a-real-key", "secret"},
			{"customer_id", "cus_Qw81nTz4", "default"},
			{"webhook_secret", "demo-webhook-secret-not-real", "secret"},
			{"timeout_ms", "30000", "default"},
		}},
		{"Staging", "#FAAD14", false, []v{
			{"base_url", mock("payments-sandbox"), "default"},
			{"identity_url", mock("identity-local"), "default"},
			{"secret_key", "demo-staging-secret-not-a-real-key", "secret"},
			{"customer_id", "cus_Stg41nTz", "default"},
		}},
		{"Local", "#BFBFBF", false, []v{
			{"base_url", "http://localhost:4010", "default"},
			{"identity_url", "http://localhost:4011", "default"},
			{"secret_key", "demo-local-secret", "secret"},
			{"customer_id", "cus_Local01", "default"},
		}},
		{"Globals", "#BFBFBF", true, []v{
			{"acme_version", "2026-10-01", "default"},
		}},
	}
	for i, e := range envs {
		var envID string
		if err := tx.QueryRow(ctx, `INSERT INTO environments (workspace_id, name, color, is_global, sort) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
			ws, e.name, e.color, e.global, i).Scan(&envID); err != nil {
			return err
		}
		for j, x := range e.vars {
			value, ct := x.value, ""
			if x.kind == "secret" {
				if ct, err = b.Encrypt(ctx, "ws-"+ws, x.value); err != nil {
					return err
				}
				value = ""
			}
			if _, err := tx.Exec(ctx, `INSERT INTO env_vars (env_id, key, value, ciphertext, kind, sort) VALUES ($1,$2,$3,$4,$5,$6)`,
				envID, x.key, value, ct, x.kind, j); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

func insertAPI(ctx context.Context, tx pgx.Tx, ws, name, slug, desc, owner, status, version string, draft, published *openapi3.T) (string, error) {
	draftJSON, err := spec.ToJSON(draft)
	if err != nil {
		return "", err
	}
	current := ""
	if published != nil {
		current = version
	}
	draftVersion := version
	if published != nil && name == "Payments API" {
		draftVersion = "v2.5.0"
	}
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO apis (workspace_id, name, slug, description, owner, status, base_url, current_version, draft_spec, draft_version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		ws, name, slug, desc, owner, status, spec.BaseURL(draft), current, draftJSON, draftVersion).Scan(&id); err != nil {
		return "", err
	}
	if published != nil {
		pubJSON, _ := spec.ToJSON(published)
		if _, err := tx.Exec(ctx, `INSERT INTO api_versions (api_id, version, spec) VALUES ($1,$2,$3)`, id, version, pubJSON); err != nil {
			return "", err
		}
	}
	return id, nil
}

// addDraftChanges makes the three unpublished changes shown in the design.
func addDraftChanges(doc *openapi3.T) {
	cc := doc.Components.Schemas["ChargeCreate"].Value
	capture := openapi3.NewBoolSchema()
	capture.Description = "Capture immediately. Defaults to true"
	capture.Example = false
	cc.Properties["capture"] = openapi3.NewSchemaRef("", capture)
	cc.Extensions["x-order"] = []any{"amount", "currency", "customer", "capture", "description", "metadata"}
	op := doc.Paths.Value("/v2/charges").Post
	expand := openapi3.NewQueryParameter("expand[]").WithSchema(openapi3.NewStringSchema().WithEnum("customer"))
	expand.Description = "Inline related objects in the response"
	op.Parameters = append(op.Parameters, &openapi3.ParameterRef{Value: expand})
	desc := "The card was declined"
	declined := &openapi3.Response{Description: &desc, Content: openapi3.NewContentWithJSONSchemaRef(
		openapi3.NewSchemaRef("#/components/schemas/Problem", doc.Components.Schemas["Problem"].Value))}
	declined.Content["application/json"].Example = map[string]any{
		"type": "https://docs.acme.dev/errors/card-declined", "title": "Card declined", "status": 402, "detail": "insufficient_funds",
	}
	op.Responses.Set("402", &openapi3.ResponseRef{Value: declined})
	op.Description = "Creates a charge against a customer's default payment method. Pass capture: false to authorize now and capture later, within 7 days."
	doc.Info.Version = "2.5.0"
}

func customizeCreateCharge(ctx context.Context, tx pgx.Tx, apiID string) error {
	headers, _ := json.Marshal([]map[string]any{
		{"key": "Content-Type", "value": "application/json", "enabled": true},
		{"key": "Idempotency-Key", "value": "{{$uuid}}", "description": "New value on every send", "enabled": true},
		{"key": "Acme-Version", "value": "{{acme_version}}", "description": "Pins the API version", "enabled": true},
		{"key": "Accept", "value": "application/json", "enabled": true},
	})
	params, _ := json.Marshal([]map[string]any{
		{"key": "expand[]", "value": "customer", "description": "Inline the customer object", "enabled": true},
		{"key": "idempotent", "value": "true", "description": "Retry safely on network errors", "enabled": true},
		{"key": "livemode", "value": "false", "description": "Ignored by this endpoint", "enabled": false},
	})
	body := strings.TrimSpace(`
{
  "amount": 4200,
  "currency": "usd",
  "customer": "{{customer_id}}",
  "capture": false,
  "description": "Order 1042, annual plan",
  "metadata": {
    "order_id": "ord_8Kx2Lm",
    "channel": "web"
  }
}`)
	tests := `test("returns 201", () => expect(res.status).toBe(201));
test("is uncaptured", () => expect(res.json.captured).toBe(false));
test("amount matches", () => expect(res.json.amount).toBe(4200));
test("under 500 ms", () => expect(res.time).toBeLessThan(500));

env.set("charge_id", res.json.id);`
	_, err := tx.Exec(ctx, `UPDATE requests SET headers=$2, params=$3, body=$4, body_mode='json', post_script=$5, sort=-1
		WHERE api_id=$1 AND operation_key='POST /v2/charges'`, apiID, headers, params, body, tests)
	return err
}

// ----- small generated specs -----

type op struct {
	method, path, summary, tag string
	body                       map[string]any // request example (nil = no body)
	resp                       map[string]any // 2xx example
}

var identityOps = []op{
	{"GET", "/v1/users/me", "Get the current user", "Users", nil, map[string]any{"id": "usr_7Hn2", "email": "alex@acme.dev", "name": "Alex Chen"}},
	{"GET", "/v1/users/{id}", "Retrieve a user", "Users", nil, map[string]any{"id": "usr_7Hn2", "email": "alex@acme.dev", "name": "Alex Chen"}},
	{"POST", "/v1/sessions", "Create a session", "Sessions", map[string]any{"user_id": "usr_7Hn2", "ttl_seconds": 3600}, map[string]any{"id": "ses_Qx81", "expires_in": 3600}},
	{"DELETE", "/v1/sessions/{id}", "End a session", "Sessions", nil, map[string]any{"id": "ses_Qx81", "ended": true}},
	{"POST", "/v1/oauth/clients", "Create an OAuth client", "OAuth clients", map[string]any{"name": "Mobile app", "redirect_uris": []any{"acme://callback"}}, map[string]any{"client_id": "cli_4kT9", "name": "Mobile app"}},
}
var notificationOps = []op{
	{"POST", "/v3/messages", "Send a message", "Messages", map[string]any{"channel": "email", "to": "jo@example.com", "template": "receipt"}, map[string]any{"id": "msg_91aZ", "status": "queued"}},
	{"GET", "/v3/messages/{id}", "Retrieve a message", "Messages", nil, map[string]any{"id": "msg_91aZ", "status": "delivered"}},
	{"GET", "/v3/templates", "List templates", "Templates", nil, map[string]any{"data": []any{map[string]any{"id": "receipt", "channel": "email"}}}},
}
var webhookOps = []op{
	{"POST", "/v2/webhook_endpoints", "Create an endpoint", "Endpoints", map[string]any{"url": "https://example.com/hooks", "events": []any{"charge.succeeded"}}, map[string]any{"id": "we_12Lm", "url": "https://example.com/hooks"}},
	{"GET", "/v2/webhook_endpoints", "List endpoints", "Endpoints", nil, map[string]any{"data": []any{}}},
	{"GET", "/v2/deliveries", "List deliveries", "Deliveries", nil, map[string]any{"data": []any{map[string]any{"id": "dlv_8Kq", "status": 200}}}},
}
var ledgerOps = []op{
	{"POST", "/v1/journals", "Post a journal entry", "Journals", map[string]any{"debit": "cash", "credit": "revenue", "amount": 4200}, map[string]any{"id": "jrn_1", "balanced": true}},
	{"GET", "/v1/accounts/{id}/balance", "Get an account balance", "Accounts", nil, map[string]any{"account": "cash", "balance": 4200}},
}
var reportOps = []op{
	{"POST", "/v1/exports", "Create an export", "Exports", map[string]any{"type": "payouts", "format": "csv"}, map[string]any{"id": "exp_3", "status": "pending"}},
	{"GET", "/v1/exports/{id}", "Retrieve an export", "Exports", nil, map[string]any{"id": "exp_3", "status": "ready"}},
}

func schemaOf(v any) *openapi3.Schema {
	switch x := v.(type) {
	case map[string]any:
		s := openapi3.NewObjectSchema()
		for k, val := range x {
			s.Properties[k] = openapi3.NewSchemaRef("", schemaOf(val))
		}
		return s
	case []any:
		item := openapi3.NewStringSchema()
		if len(x) > 0 {
			item = schemaOf(x[0])
		}
		return openapi3.NewArraySchema().WithItems(item)
	case int, float64:
		return openapi3.NewIntegerSchema()
	case bool:
		return openapi3.NewBoolSchema()
	default:
		return openapi3.NewStringSchema()
	}
}

func build(title, version, server string, ops []op) *openapi3.T {
	doc := spec.Blank(title, version, server)
	seen := map[string]bool{}
	for _, o := range ops {
		if !seen[o.tag] {
			doc.Tags = append(doc.Tags, &openapi3.Tag{Name: o.tag})
			seen[o.tag] = true
		}
		item := doc.Paths.Value(o.path)
		if item == nil {
			item = &openapi3.PathItem{}
			if strings.Contains(o.path, "{id}") {
				item.Parameters = openapi3.Parameters{{Value: openapi3.NewPathParameter("id").WithSchema(openapi3.NewStringSchema())}}
			}
			doc.Paths.Set(o.path, item)
		}
		code := 200
		operation := &openapi3.Operation{Tags: []string{o.tag}, Summary: o.summary, OperationID: camel(o.summary)}
		if o.body != nil {
			code = 201
			mt := openapi3.NewMediaType().WithSchema(schemaOf(o.body))
			mt.Example = o.body
			operation.RequestBody = &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{Required: true, Content: openapi3.Content{"application/json": mt}}}
		}
		desc := "OK"
		mt := openapi3.NewMediaType().WithSchema(schemaOf(o.resp))
		mt.Example = o.resp
		operation.Responses = openapi3.NewResponses(openapi3.WithStatus(code, &openapi3.ResponseRef{Value: &openapi3.Response{
			Description: &desc, Content: openapi3.Content{"application/json": mt},
		}}))
		item.SetOperation(o.method, operation)
	}
	return doc
}

func camel(s string) string {
	parts := strings.Fields(strings.ToLower(s))
	for i := 1; i < len(parts); i++ {
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, "")
}
