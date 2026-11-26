package api

// Postman-style collections: requests grouped without an API spec, with
// collection-level variables, auth and scripts their requests inherit (an
// API's own collection keeps those settings in a row with api_id set),
// saved examples, cookies, and Postman / cURL import and export.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/anilcharles/requara/server/api/internal/core"
	"github.com/anilcharles/requara/server/api/internal/runner"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type collectionSettings struct {
	ID          string      `json:"id"`
	Kind        string      `json:"kind"` // collection | api
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Variables   []runner.KV `json:"variables"`
	Auth        runner.Auth `json:"auth"`
	PreScript   string      `json:"preScript"`
	PostScript  string      `json:"postScript"`
}

func (s *Server) collectionInWorkspace(ctx context.Context, ws, id string) bool {
	var ok bool
	_ = s.db.QueryRow(ctx, `SELECT true FROM collections WHERE workspace_id=$1 AND id::text=$2 AND api_id IS NULL`, ws, id).Scan(&ok)
	return ok
}

// loadSettings returns a collection's settings. id is a collection id or an
// API id (whose settings row may not exist yet).
func (s *Server) loadSettings(ctx context.Context, ws, id string) (*collectionSettings, error) {
	c := &collectionSettings{ID: id, Kind: "collection", Variables: []runner.KV{}, Auth: runner.Auth{Type: "inherit"}}
	var vars, auth []byte
	var apiID *string
	err := s.db.QueryRow(ctx, `
		SELECT c.name, c.description, c.variables, c.auth, c.pre_script, c.post_script, c.api_id::text
		FROM collections c WHERE c.workspace_id=$1 AND (c.id::text=$2 OR c.api_id::text=$2)`, ws, id).
		Scan(&c.Name, &c.Description, &vars, &auth, &c.PreScript, &c.PostScript, &apiID)
	if err == nil {
		_ = json.Unmarshal(vars, &c.Variables)
		_ = json.Unmarshal(auth, &c.Auth)
		if apiID != nil {
			c.Kind, c.ID = "api", *apiID
		}
		return c, nil
	}
	if !notFound(err) {
		return nil, err
	}
	// An API whose collection settings were never saved.
	if err := s.db.QueryRow(ctx, `SELECT name FROM apis WHERE workspace_id=$1 AND id::text=$2`, ws, id).Scan(&c.Name); err != nil {
		return nil, err
	}
	c.Kind = "api"
	return c, nil
}

func (s *Server) getCollectionSettings(w http.ResponseWriter, r *http.Request) {
	c, err := s.loadSettings(r.Context(), principal(r).WorkspaceID, chi.URLParam(r, "id"))
	if notFound(err) {
		httpError(w, http.StatusNotFound, "Collection not found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) putCollectionSettings(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	id := chi.URLParam(r, "id")
	cur, err := s.loadSettings(r.Context(), p.WorkspaceID, id)
	if notFound(err) {
		httpError(w, http.StatusNotFound, "Collection not found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	var in collectionSettings
	if !readJSON(w, r, &in) {
		return
	}
	if in.Variables == nil {
		in.Variables = []runner.KV{}
	}
	if in.Auth.Type == "" {
		in.Auth.Type = "inherit"
	}
	vars, _ := json.Marshal(in.Variables)
	auth, _ := json.Marshal(in.Auth)
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = cur.Name
	}
	if cur.Kind == "api" {
		_, err = s.db.Exec(r.Context(), `
			INSERT INTO collections (workspace_id, api_id, name, description, variables, auth, pre_script, post_script)
			VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (api_id) DO UPDATE SET description=$4, variables=$5, auth=$6, pre_script=$7, post_script=$8, updated_at=now()`,
			p.WorkspaceID, cur.ID, cur.Name, in.Description, vars, auth, in.PreScript, in.PostScript)
	} else {
		_, err = s.db.Exec(r.Context(), `
			UPDATE collections SET name=$3, description=$4, variables=$5, auth=$6, pre_script=$7, post_script=$8, updated_at=now()
			WHERE workspace_id=$1 AND id::text=$2`, p.WorkspaceID, id, name, in.Description, vars, auth, in.PreScript, in.PostScript)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	s.getCollectionSettings(w, r)
}

func (s *Server) createCollection(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		httpError(w, http.StatusBadRequest, "Give the collection a name")
		return
	}
	var id string
	if err := s.db.QueryRow(r.Context(), `
		INSERT INTO collections (workspace_id, name, description, sort)
		VALUES ($1, $2, $3, COALESCE((SELECT max(sort) + 1 FROM collections WHERE workspace_id=$1), 0)) RETURNING id::text`,
		p.WorkspaceID, in.Name, in.Description).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "name": in.Name})
}

func (s *Server) renameCollection(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		httpError(w, http.StatusBadRequest, "Give the collection a name")
		return
	}
	tag, err := s.db.Exec(r.Context(), `UPDATE collections SET name=$3, updated_at=now() WHERE workspace_id=$1 AND id::text=$2 AND api_id IS NULL`,
		p.WorkspaceID, chi.URLParam(r, "id"), strings.TrimSpace(in.Name))
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		httpError(w, http.StatusNotFound, "Collection not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteCollection(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	if _, err := s.db.Exec(r.Context(), `DELETE FROM collections WHERE workspace_id=$1 AND id::text=$2 AND api_id IS NULL`, p.WorkspaceID, chi.URLParam(r, "id")); err != nil {
		serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- import / export -----

// importCollection takes {format: "postman" | "curl", data, collectionId?}.
// A Postman collection becomes a new collection; a cURL command becomes a
// request in collectionId (or Scratch).
func (s *Server) importCollection(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	var in struct {
		Format       string  `json:"format"`
		Data         string  `json:"data"`
		CollectionID *string `json:"collectionId"`
		Folder       string  `json:"folder"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	switch in.Format {
	case "curl":
		req, err := core.ParseCurl(in.Data)
		if err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		if in.CollectionID != nil && !s.collectionInWorkspace(ctx, p.WorkspaceID, *in.CollectionID) {
			in.CollectionID = nil
		}
		req.Folder = in.Folder
		if in.CollectionID == nil && req.Folder == "" {
			req.Folder = "Scratch"
		}
		tx, err := s.db.Begin(ctx)
		if err != nil {
			serverError(w, err)
			return
		}
		defer tx.Rollback(ctx)
		id, err := insertImported(ctx, tx, p.WorkspaceID, in.CollectionID, *req, 1000)
		if err != nil {
			serverError(w, err)
			return
		}
		if err := tx.Commit(ctx); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"requestId": id, "name": req.Name})
	case "postman", "":
		c, err := core.ParsePostman([]byte(in.Data))
		if err != nil {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		id, err := s.saveCollection(ctx, p.WorkspaceID, c)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": c.Name, "requests": len(c.Requests)})
	default:
		httpError(w, http.StatusBadRequest, "format is postman or curl")
	}
}

func (s *Server) saveCollection(ctx context.Context, ws string, c *core.Collection) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	vars, _ := json.Marshal(c.Variables)
	auth, _ := json.Marshal(c.Auth)
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO collections (workspace_id, name, description, variables, auth, pre_script, post_script, sort)
		VALUES ($1,$2,$3,$4,$5,$6,$7, COALESCE((SELECT max(sort) + 1 FROM collections WHERE workspace_id=$1), 0)) RETURNING id::text`,
		ws, c.Name, c.Description, vars, auth, c.PreScript, c.PostScript).Scan(&id); err != nil {
		return "", err
	}
	for i, req := range c.Requests {
		if _, err := insertImported(ctx, tx, ws, &id, req, i); err != nil {
			return "", err
		}
	}
	return id, tx.Commit(ctx)
}

func insertImported(ctx context.Context, tx pgx.Tx, ws string, collectionID *string, req core.Request, sort int) (string, error) {
	in := requestDoc{Params: req.Params, Headers: req.Headers, Auth: req.Auth, FormData: req.FormData}
	if in.Auth.Type == "" {
		in.Auth.Type = "inherit"
	}
	params, headers, auth, settings := marshalRequest(&in)
	formData, bodyFile, messages := marshalExtras(&in)
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO requests (workspace_id, collection_id, folder, name, method, url, params, headers, body_mode, body, auth, pre_script, post_script, settings, form_data, body_file, messages, sort)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18) RETURNING id::text`,
		ws, collectionID, req.Folder, req.Name, req.Method, req.URL, params, headers, req.BodyMode, req.Body, auth, req.PreScript, req.PostScript,
		settings, formData, bodyFile, messages, sort).Scan(&id); err != nil {
		return "", err
	}
	for _, ex := range req.Examples {
		h, _ := json.Marshal(nonNil(ex.Headers))
		if _, err := tx.Exec(ctx, `INSERT INTO request_examples (request_id, name, status, headers, body) VALUES ($1,$2,$3,$4,$5)`,
			id, ex.Name, ex.Status, h, ex.Body); err != nil {
			return "", err
		}
	}
	return id, nil
}

// exportCollection downloads a collection (or an API's requests, or
// Scratch) as a Postman Collection v2.1.
func (s *Server) exportCollection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := principal(r)
	id := chi.URLParam(r, "id")
	c := core.Collection{Variables: []runner.KV{}, Auth: runner.Auth{Type: "inherit"}}
	where := "collection_id::text = $2"
	if id == "scratch" {
		c.Name, where = "Scratch", "api_id IS NULL AND collection_id IS NULL AND $2 = 'scratch'"
	} else {
		set, err := s.loadSettings(ctx, p.WorkspaceID, id)
		if err != nil {
			httpError(w, http.StatusNotFound, "Collection not found")
			return
		}
		c.Name, c.Description, c.Variables, c.Auth, c.PreScript, c.PostScript = set.Name, set.Description, set.Variables, set.Auth, set.PreScript, set.PostScript
		if set.Kind == "api" {
			where = "api_id::text = $2"
		}
	}
	rows, err := s.db.Query(ctx, `SELECT `+requestCols+` FROM requests WHERE workspace_id=$1 AND kind='http' AND `+where+` ORDER BY folder, sort, name`, p.WorkspaceID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	docs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*requestDoc, error) { return scanRequest(row) })
	if err != nil {
		serverError(w, err)
		return
	}
	for _, d := range docs {
		req := core.Request{Folder: d.Folder, Name: d.Name, Method: d.Method, URL: d.URL, Params: d.Params, Headers: d.Headers, BodyMode: d.BodyMode,
			Body: d.Body, FormData: d.FormData, Auth: d.Auth, PreScript: d.PreScript, PostScript: d.PostScript, Examples: []core.Example{}}
		for i := range req.FormData {
			if req.FormData[i].Type == "file" {
				req.FormData[i].Value = "" // file contents stay in Requara
			}
		}
		erows, _ := s.db.Query(ctx, `SELECT name, status, headers, body FROM request_examples WHERE request_id=$1 ORDER BY created_at`, d.ID)
		for erows.Next() {
			var ex core.Example
			var h []byte
			_ = erows.Scan(&ex.Name, &ex.Status, &h, &ex.Body)
			_ = json.Unmarshal(h, &ex.Headers)
			req.Examples = append(req.Examples, ex)
		}
		erows.Close()
		c.Requests = append(c.Requests, req)
	}
	out, err := core.ExportPostman(c)
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+slugify(c.Name)+`.postman_collection.json"`)
	_, _ = w.Write(out)
}

// ----- examples (saved responses) -----

type exampleOut struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	Status    int         `json:"status"`
	Headers   []runner.KV `json:"headers"`
	Body      string      `json:"body"`
	CreatedAt time.Time   `json:"createdAt"`
}

func (s *Server) listExamples(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT e.id::text, e.name, e.status, e.headers, e.body, e.created_at FROM request_examples e
		JOIN requests q ON q.id = e.request_id WHERE q.workspace_id=$1 AND q.id::text=$2 ORDER BY e.created_at`, p.WorkspaceID, chi.URLParam(r, "id"))
	if err != nil {
		serverError(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (exampleOut, error) {
		var e exampleOut
		var h []byte
		err := row.Scan(&e.ID, &e.Name, &e.Status, &h, &e.Body, &e.CreatedAt)
		_ = json.Unmarshal(h, &e.Headers)
		e.Headers = nonNil(e.Headers)
		return e, err
	})
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createExample(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	var in exampleOut
	if !readJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		in.Name = "Example"
	}
	h, _ := json.Marshal(nonNil(in.Headers))
	var id string
	err := s.db.QueryRow(r.Context(), `
		INSERT INTO request_examples (request_id, name, status, headers, body)
		SELECT id, $3, $4, $5, $6 FROM requests WHERE workspace_id=$1 AND id::text=$2 RETURNING id::text`,
		p.WorkspaceID, chi.URLParam(r, "id"), in.Name, in.Status, h, in.Body).Scan(&id)
	if err != nil {
		httpError(w, http.StatusNotFound, "Request not found")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) deleteExample(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	if _, err := s.db.Exec(r.Context(), `
		DELETE FROM request_examples e USING requests q WHERE q.id = e.request_id AND q.workspace_id=$1 AND e.id::text=$2`,
		p.WorkspaceID, chi.URLParam(r, "id")); err != nil {
		serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- cookies -----

func (s *Server) loadCookies(ctx context.Context, p *Principal) []runner.Cookie {
	rows, err := s.db.Query(ctx, `
		SELECT domain, path, name, value, expires, secure, http_only FROM cookies
		WHERE workspace_id=$1 AND user_id=$2 AND (expires IS NULL OR expires > now())`, p.WorkspaceID, p.UserID)
	if err != nil {
		return nil
	}
	out, _ := pgx.CollectRows(rows, func(row pgx.CollectableRow) (runner.Cookie, error) {
		var c runner.Cookie
		err := row.Scan(&c.Domain, &c.Path, &c.Name, &c.Value, &c.Expires, &c.Secure, &c.HTTPOnly)
		return c, err
	})
	return out
}

func (s *Server) saveCookies(ctx context.Context, p *Principal, cookies []runner.Cookie) {
	for _, c := range cookies {
		if c.Expires != nil && c.Expires.Before(time.Now()) {
			_, _ = s.db.Exec(ctx, `DELETE FROM cookies WHERE workspace_id=$1 AND user_id=$2 AND domain=$3 AND path=$4 AND name=$5`,
				p.WorkspaceID, p.UserID, c.Domain, c.Path, c.Name)
			continue
		}
		_, _ = s.db.Exec(ctx, `
			INSERT INTO cookies (workspace_id, user_id, domain, path, name, value, expires, secure, http_only) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT (workspace_id, user_id, domain, path, name) DO UPDATE SET value=$6, expires=$7, secure=$8, http_only=$9`,
			p.WorkspaceID, p.UserID, c.Domain, c.Path, c.Name, c.Value, c.Expires, c.Secure, c.HTTPOnly)
	}
}

func (s *Server) listCookies(w http.ResponseWriter, r *http.Request) {
	out := s.loadCookies(r.Context(), principal(r))
	if out == nil {
		out = []runner.Cookie{}
	}
	writeJSON(w, http.StatusOK, out)
}

// deleteCookies removes one cookie (?domain=&path=&name=), a domain's, or all.
func (s *Server) deleteCookies(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	q := r.URL.Query()
	if _, err := s.db.Exec(r.Context(), `
		DELETE FROM cookies WHERE workspace_id=$1 AND user_id=$2
		  AND ($3 = '' OR domain = $3) AND ($4 = '' OR path = $4) AND ($5 = '' OR name = $5)`,
		p.WorkspaceID, p.UserID, q.Get("domain"), q.Get("path"), q.Get("name")); err != nil {
		serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- sending with what the collection gives -----

// inherited is what a request gets from its collection.
type inherited struct {
	vars       []runner.KV
	auth       runner.Auth
	pre, post  string
	collection string // name, for history
}

func (s *Server) inheritFor(ctx context.Context, ws string, d *requestDoc) inherited {
	id := ""
	switch {
	case d.CollectionID != nil:
		id = *d.CollectionID
	case d.APIID != nil:
		id = *d.APIID
	default:
		return inherited{auth: runner.Auth{Type: "inherit"}}
	}
	c, err := s.loadSettings(ctx, ws, id)
	if err != nil {
		return inherited{auth: runner.Auth{Type: "inherit"}}
	}
	return inherited{vars: c.Variables, auth: c.Auth, pre: c.PreScript, post: c.PostScript, collection: c.Name}
}

// joinScripts runs the collection's script, then the request's, each in its own block.
func joinScripts(collection, request string) string {
	switch {
	case strings.TrimSpace(collection) == "":
		return request
	case strings.TrimSpace(request) == "":
		return collection
	}
	return "{\n" + collection + "\n}\n{\n" + request + "\n}"
}
