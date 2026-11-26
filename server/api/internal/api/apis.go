package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anilcharles/speklo/server/api/internal/jobs"
	"github.com/anilcharles/speklo/server/api/internal/spec"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"
)

type apiRow struct {
	ID, WorkspaceID, Name, Slug, Description, Owner, Status, BaseURL, CurrentVersion, DraftVersion string
	DraftSpec                                                                                      []byte
	UpdatedAt                                                                                      time.Time
}

func (s *Server) loadAPI(ctx context.Context, ws, id string) (*apiRow, error) {
	var a apiRow
	err := s.db.QueryRow(ctx, `
		SELECT id, workspace_id, name, slug, description, owner, status, base_url, current_version, draft_version, draft_spec, updated_at
		FROM apis WHERE workspace_id=$1 AND id::text=$2`, ws, id).
		Scan(&a.ID, &a.WorkspaceID, &a.Name, &a.Slug, &a.Description, &a.Owner, &a.Status, &a.BaseURL, &a.CurrentVersion, &a.DraftVersion, &a.DraftSpec, &a.UpdatedAt)
	return &a, err
}

// published returns the latest published document, or nil if never published.
func (s *Server) published(ctx context.Context, apiID string) (*openapi3.T, string, error) {
	var raw []byte
	var version string
	err := s.db.QueryRow(ctx, `SELECT spec, version FROM api_versions WHERE api_id=$1 ORDER BY published_at DESC LIMIT 1`, apiID).Scan(&raw, &version)
	if notFound(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	doc, err := spec.FromJSON(raw)
	return doc, version, err
}

func (s *Server) apiOr404(w http.ResponseWriter, r *http.Request) (*apiRow, *openapi3.T, bool) {
	a, err := s.loadAPI(r.Context(), principal(r).WorkspaceID, chi.URLParam(r, "id"))
	if notFound(err) {
		httpError(w, http.StatusNotFound, "API not found")
		return nil, nil, false
	}
	if err != nil {
		serverError(w, err)
		return nil, nil, false
	}
	doc, err := spec.FromJSON(a.DraftSpec)
	if err != nil {
		serverError(w, err)
		return nil, nil, false
	}
	return a, doc, true
}

func countOps(raw []byte) int {
	var d struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	_ = json.Unmarshal(raw, &d)
	n := 0
	for _, item := range d.Paths {
		for m := range item {
			switch strings.ToLower(m) {
			case "get", "post", "put", "patch", "delete", "head", "options":
				n++
			}
		}
	}
	return n
}

// ----- catalog -----

func (s *Server) listAPIs(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	status := r.URL.Query().Get("status")
	rows, err := s.db.Query(r.Context(), `
		SELECT a.id, a.name, a.description, a.status, a.current_version, a.draft_version, a.owner, a.updated_at, a.draft_spec,
		       a.draft_spec IS DISTINCT FROM (SELECT v.spec FROM api_versions v WHERE v.api_id=a.id ORDER BY v.published_at DESC LIMIT 1),
		       COALESCE(m.calls, 0), m.p95, m.err
		FROM apis a
		LEFT JOIN LATERAL (
		  SELECT count(*) AS calls,
		         percentile_cont(0.95) WITHIN GROUP (ORDER BY h.duration_ms) FILTER (WHERE h.status > 0) AS p95,
		         avg(CASE WHEN h.status >= 500 OR h.status = 0 THEN 1.0 ELSE 0.0 END) AS err
		  FROM history h WHERE h.api_id = a.id AND h.created_at > now() - interval '24 hours'
		) m ON true
		WHERE a.workspace_id=$1 AND ($2 = '' OR a.status = $2)
		ORDER BY a.updated_at DESC`, p.WorkspaceID, status)
	if err != nil {
		serverError(w, err)
		return
	}
	type item struct {
		ID              string    `json:"id"`
		Name            string    `json:"name"`
		Description     string    `json:"description"`
		Status          string    `json:"status"`
		Version         string    `json:"version"`
		DraftVersion    string    `json:"draftVersion"`
		Owner           string    `json:"owner"`
		UpdatedAt       time.Time `json:"updatedAt"`
		Endpoints       int       `json:"endpoints"`
		HasDraftChanges bool      `json:"hasDraftChanges"`
		Calls24h        int64     `json:"calls24h"`
		P95Ms           *float64  `json:"p95Ms"`
		ErrorRate       *float64  `json:"errorRate"`
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (item, error) {
		var x item
		var raw []byte
		err := row.Scan(&x.ID, &x.Name, &x.Description, &x.Status, &x.Version, &x.DraftVersion, &x.Owner, &x.UpdatedAt, &raw,
			&x.HasDraftChanges, &x.Calls24h, &x.P95Ms, &x.ErrorRate)
		x.Endpoints = countOps(raw)
		if x.Version == "" {
			x.Version = x.DraftVersion
		}
		return x, err
	})
	if err != nil {
		serverError(w, err)
		return
	}
	var all, published, draft, deprecated int
	_ = s.db.QueryRow(r.Context(), `
		SELECT count(*), count(*) FILTER (WHERE status='published'), count(*) FILTER (WHERE status='draft'),
		       count(*) FILTER (WHERE status='deprecated')
		FROM apis WHERE workspace_id=$1`, p.WorkspaceID).Scan(&all, &published, &draft, &deprecated)
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "counts": map[string]int{
		"all": all, "published": published, "draft": draft, "deprecated": deprecated,
	}})
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "v1.0.0"
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

func (s *Server) getAPI(w http.ResponseWriter, r *http.Request) {
	a, doc, ok := s.apiOr404(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": a.ID, "name": a.Name, "slug": a.Slug, "description": a.Description, "status": a.Status,
		"version": a.CurrentVersion, "draftVersion": a.DraftVersion, "baseUrl": spec.BaseURL(doc), "owner": a.Owner,
	})
}

// createAPI makes a blank API or one from the REST resource template.
func (s *Server) createAPI(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	var in struct {
		Name, BaseURL, Version, StartFrom, Description string
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		httpError(w, http.StatusBadRequest, "Give the API a name")
		return
	}
	version := normalizeVersion(in.Version)
	doc := spec.Blank(in.Name, version, strings.TrimSpace(in.BaseURL))
	if in.Description != "" {
		doc.Info.Description = in.Description
	}
	if in.StartFrom == "template" {
		addResourceTemplate(doc)
	}
	id, err := s.saveNewAPI(r.Context(), p, in.Name, in.Description, version, doc)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) saveNewAPI(ctx context.Context, p *Principal, name, description, version string, doc *openapi3.T) (string, error) {
	if err := doc.Validate(ctx, openapi3.DisableExamplesValidation()); err != nil {
		return "", fmt.Errorf("invalid OpenAPI: %w", err)
	}
	raw, err := spec.ToJSON(doc)
	if err != nil {
		return "", err
	}
	if description == "" && doc.Info != nil {
		description = doc.Info.Description
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	slug := slugify(name)
	var n int
	_ = tx.QueryRow(ctx, `SELECT count(*) FROM apis WHERE workspace_id=$1 AND (slug=$2 OR slug LIKE $2 || '-%')`, p.WorkspaceID, slug).Scan(&n)
	if n > 0 {
		slug = fmt.Sprintf("%s-%d", slug, n+1)
	}
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO apis (workspace_id, name, slug, description, owner, base_url, draft_spec, draft_version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		p.WorkspaceID, name, slug, firstSentence(description), p.Name, spec.BaseURL(doc), raw, version).Scan(&id); err != nil {
		return "", err
	}
	if err := SyncRequests(ctx, tx, p.WorkspaceID, id, doc, spec.BaseURL(doc)); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ".\n"); i > 0 && i < 120 {
		return s[:i]
	}
	if len(s) > 120 {
		return s[:117] + "…"
	}
	return s
}

var pathParam = regexp.MustCompile(`\{([^}]+)\}`)

// SyncRequests creates a saved request for every operation that lacks one.
func SyncRequests(ctx context.Context, tx pgx.Tx, ws, apiID string, doc *openapi3.T, urlPrefix string) error {
	existing := map[string]bool{}
	rows, err := tx.Query(ctx, `SELECT operation_key FROM requests WHERE api_id=$1`, apiID)
	if err != nil {
		return err
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, k := range keys {
		existing[k] = true
	}
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
		var params []map[string]any
		for _, prm := range o.Parameters {
			if prm.Value != nil && prm.Value.In == "query" {
				params = append(params, map[string]any{"key": prm.Value.Name, "value": "", "description": prm.Value.Description, "enabled": false})
			}
		}
		if params == nil {
			params = []map[string]any{}
		}
		name := op.Summary
		if name == "" {
			name = op.Key
		}
		url := urlPrefix + pathParam.ReplaceAllString(op.Path, "{{$1}}")
		hj, _ := json.Marshal(headers)
		pj, _ := json.Marshal(params)
		if _, err := tx.Exec(ctx, `
			INSERT INTO requests (workspace_id, api_id, folder, name, method, url, params, headers, body_mode, body, operation_key, sort)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			ws, apiID, op.Tag, name, op.Method, url, pj, hj, mode, body, op.Key, i); err != nil {
			return err
		}
	}
	return nil
}

func addResourceTemplate(doc *openapi3.T) {
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

// importAPI takes an uploaded file (multipart "file") or {"url": "..."}.
func (s *Server) importAPI(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	var data []byte
	var err error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			httpError(w, http.StatusBadRequest, "Upload a file up to 10 MB")
			return
		}
		f, _, ferr := r.FormFile("file")
		if ferr != nil {
			httpError(w, http.StatusBadRequest, "Choose a file to import")
			return
		}
		defer f.Close()
		data, err = io.ReadAll(io.LimitReader(f, 10<<20))
	} else {
		var in struct{ URL string }
		if !readJSON(w, r, &in) {
			return
		}
		data, err = s.runner.Fetch(r.Context(), strings.TrimSpace(in.URL), 10<<20)
	}
	if err != nil {
		httpError(w, http.StatusBadRequest, "Couldn't read the spec: "+err.Error())
		return
	}
	doc, err := spec.Parse(data)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := "Imported API"
	version := "v1.0.0"
	desc := ""
	if doc.Info != nil {
		if doc.Info.Title != "" {
			name = doc.Info.Title
		}
		if doc.Info.Version != "" {
			version = normalizeVersion(doc.Info.Version)
		}
		desc = doc.Info.Description
	}
	id, err := s.saveNewAPI(r.Context(), p, name, desc, version, doc)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": name, "operations": len(spec.Operations(doc))})
}

// ----- design -----

func (s *Server) listOperations(w http.ResponseWriter, r *http.Request) {
	_, doc, ok := s.apiOr404(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, spec.Operations(doc))
}

func (s *Server) getOperation(w http.ResponseWriter, r *http.Request) {
	a, doc, ok := s.apiOr404(w, r)
	if !ok {
		return
	}
	key := r.URL.Query().Get("key")
	op, item, err := spec.Find(doc, key)
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	fields := spec.Fields(op)
	pub, _, err := s.published(r.Context(), a.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	isNewOp := false
	if pub != nil {
		pubOp, _, perr := spec.Find(pub, key)
		if perr == nil {
			spec.MarkNew(fields, pubOp)
		} else {
			isNewOp = true
		}
	}
	method, path, _ := spec.SplitKey(key)
	writeJSON(w, http.StatusOK, map[string]any{
		"key": key, "method": method, "path": path, "summary": op.Summary, "description": op.Description,
		"operationId": op.OperationID, "fields": fields, "params": spec.Params(op, item),
		"contentType": "application/json", "new": isNewOp, "responses": spec.Responses(op),
	})
}

func (s *Server) updateOperation(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	a, doc, ok := s.apiOr404(w, r)
	if !ok {
		return
	}
	var in struct {
		Key         string       `json:"key"`
		Method      string       `json:"method"`
		Path        string       `json:"path"`
		Summary     string       `json:"summary"`
		Description string       `json:"description"`
		Fields      []spec.Field `json:"fields"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	op, item, err := spec.Find(doc, in.Key)
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	op.Summary, op.Description = strings.TrimSpace(in.Summary), strings.TrimSpace(in.Description)
	if in.Fields != nil {
		if len(in.Fields) == 0 {
			op.RequestBody = nil
		} else if err := spec.SetFields(op, in.Fields); err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	newMethod, newPath := strings.ToUpper(strings.TrimSpace(in.Method)), strings.TrimSpace(in.Path)
	oldMethod, oldPath, _ := spec.SplitKey(in.Key)
	newKey := in.Key
	if newMethod != "" && newPath != "" && (newMethod != oldMethod || newPath != oldPath) {
		if !strings.HasPrefix(newPath, "/") {
			httpError(w, http.StatusBadRequest, "Paths start with /")
			return
		}
		if _, _, err := spec.Find(doc, spec.OpKey(newMethod, newPath)); err == nil {
			httpError(w, http.StatusConflict, newMethod+" "+newPath+" already exists")
			return
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
	if err := doc.Validate(r.Context(), openapi3.DisableExamplesValidation()); err != nil {
		httpError(w, http.StatusBadRequest, "That change makes the spec invalid: "+err.Error())
		return
	}
	raw, _ := spec.ToJSON(doc)
	if _, err := s.db.Exec(r.Context(), `UPDATE apis SET draft_spec=$2, updated_at=now() WHERE id=$1`, a.ID, raw); err != nil {
		serverError(w, err)
		return
	}
	if newKey != in.Key {
		_, _ = s.db.Exec(r.Context(), `UPDATE requests SET operation_key=$3, method=$4 WHERE api_id=$1 AND operation_key=$2`, a.ID, in.Key, newKey, newMethod)
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": newKey})
}

func (s *Server) lintAPI(w http.ResponseWriter, r *http.Request) {
	a, _, ok := s.apiOr404(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, spec.Lint(a.DraftSpec))
}

func bump(version string, changes []spec.Change) string {
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

func (s *Server) changesFor(ctx context.Context, a *apiRow, draft *openapi3.T) ([]spec.Change, string, string, error) {
	pub, pubVersion, err := s.published(ctx, a.ID)
	if err != nil {
		return nil, "", "", err
	}
	if pub == nil {
		return []spec.Change{}, "", a.DraftVersion, nil
	}
	changes, err := spec.Diff(pub, draft)
	if err != nil {
		return nil, "", "", err
	}
	if changes == nil {
		changes = []spec.Change{}
	}
	return changes, pubVersion, bump(pubVersion, changes), nil
}

func (s *Server) apiChanges(w http.ResponseWriter, r *http.Request) {
	a, doc, ok := s.apiOr404(w, r)
	if !ok {
		return
	}
	changes, from, next, err := s.changesFor(r.Context(), a, doc)
	if err != nil {
		serverError(w, err)
		return
	}
	breaking := false
	for _, c := range changes {
		breaking = breaking || c.Breaking
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from, "suggestedVersion": next, "changes": changes, "breaking": breaking})
}

func (s *Server) publishAPI(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !s.canPublish(p) {
		httpError(w, http.StatusForbidden, "Publishing needs a publisher role or group from your identity provider (ask your admin), or workspace owner access")
		return
	}
	a, doc, ok := s.apiOr404(w, r)
	if !ok {
		return
	}
	var in struct {
		Version string `json:"version"`
		Notify  bool   `json:"notify"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	changes, _, suggested, err := s.changesFor(r.Context(), a, doc)
	if err != nil {
		serverError(w, err)
		return
	}
	version := normalizeVersion(in.Version)
	if strings.TrimSpace(in.Version) == "" {
		version = suggested
	}
	doc.Info.Version = strings.TrimPrefix(version, "v")
	raw, _ := spec.ToJSON(doc)
	changesJSON, _ := json.Marshal(changes)
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO api_versions (api_id, version, spec, changes, published_by) VALUES ($1,$2,$3,$4,$5)`,
		a.ID, version, raw, changesJSON, p.UserID); err != nil {
		httpError(w, http.StatusConflict, version+" is already published. Pick a new version.")
		return
	}
	if _, err := tx.Exec(ctx, `
		UPDATE apis SET draft_spec=$2, current_version=$3, draft_version=$3,
		       status = CASE WHEN status='deprecated' THEN status ELSE 'published' END, updated_at=now()
		WHERE id=$1`, a.ID, raw, version); err != nil {
		serverError(w, err)
		return
	}
	if err := SyncRequests(ctx, tx, a.WorkspaceID, a.ID, doc, "{{base_url}}"); err != nil {
		serverError(w, err)
		return
	}
	if in.Notify {
		var lines []string
		for _, c := range changes {
			lines = append(lines, c.Text)
		}
		if len(lines) == 0 {
			lines = []string{"No API changes"}
		}
		if _, err := s.jobs.InsertTx(ctx, tx, jobs.PublishedArgs{
			WorkspaceID: a.WorkspaceID, APIName: a.Name, Version: version, Changes: lines,
			DocsURL: s.cfg.FrontendURL + "/docs/" + a.ID,
		}, nil); err != nil {
			serverError(w, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": version, "changes": changes})
}

func (s *Server) discardAPI(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	a, _, ok := s.apiOr404(w, r)
	if !ok {
		return
	}
	tag, err := s.db.Exec(r.Context(), `
		UPDATE apis SET draft_spec = v.spec, draft_version = v.version, updated_at = now()
		FROM (SELECT spec, version FROM api_versions WHERE api_id=$1 ORDER BY published_at DESC LIMIT 1) v
		WHERE apis.id = $1`, a.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		httpError(w, http.StatusBadRequest, "Nothing published yet, so there's nothing to go back to")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- docs -----

func (s *Server) apiDocs(w http.ResponseWriter, r *http.Request) {
	a, draft, ok := s.apiOr404(w, r)
	if !ok {
		return
	}
	doc, version, err := s.published(r.Context(), a.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	isDraft := doc == nil
	if isDraft || r.URL.Query().Get("source") == "draft" {
		doc, version, isDraft = draft, a.DraftVersion, true
	}
	base := spec.BaseURL(doc)
	type opDoc struct {
		spec.Operation
		Params         []spec.Param        `json:"params"`
		Fields         []spec.Field        `json:"fields"`
		RequestExample string              `json:"requestExample"`
		Responses      []spec.ResponseInfo `json:"responses"`
		Samples        map[string]string   `json:"samples"`
	}
	ops := []opDoc{}
	for _, o := range spec.Operations(doc) {
		op, item, _ := spec.Find(doc, o.Key)
		ops = append(ops, opDoc{
			Operation: o, Params: spec.Params(op, item), Fields: spec.Fields(op),
			RequestExample: spec.RequestExample(op), Responses: spec.Responses(op),
			Samples: spec.Samples(base, o.Method, o.Path, op),
		})
	}
	var mockURL string
	var slug string
	if err := s.db.QueryRow(r.Context(), `SELECT slug FROM mock_servers WHERE api_id=$1 AND enabled ORDER BY created_at LIMIT 1`, a.ID).Scan(&slug); err == nil {
		mockURL = s.cfg.PublicURL + "/mock/" + slug
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": a.ID, "name": a.Name, "version": version, "draft": isDraft, "baseUrl": base,
		"description": doc.Info.Description, "mockUrl": mockURL, "operations": ops,
	})
}

func (s *Server) downloadSpec(w http.ResponseWriter, r *http.Request) {
	a, doc, ok := s.apiOr404(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Get("source") != "draft" {
		if pub, _, err := s.published(r.Context(), a.ID); err == nil && pub != nil {
			doc = pub
		}
	}
	raw, _ := spec.ToJSON(doc)
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="`+a.Slug+`.json"`)
		w.Write(raw)
		return
	}
	var v any
	_ = json.Unmarshal(raw, &v)
	out, _ := yaml.Marshal(v)
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Content-Disposition", `attachment; filename="`+a.Slug+`.yaml"`)
	w.Write(out)
}
