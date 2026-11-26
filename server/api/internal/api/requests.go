package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/anilcharles/requara/server/api/internal/runner"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type requestDoc struct {
	ID           string             `json:"id"`
	APIID        *string            `json:"apiId"`
	CollectionID *string            `json:"collectionId"`
	Kind         string             `json:"kind"` // http | websocket
	Folder       string             `json:"folder"`
	Name         string             `json:"name"`
	Method       string             `json:"method"`
	URL          string             `json:"url"`
	Params       []runner.KV        `json:"params"`
	Headers      []runner.KV        `json:"headers"`
	BodyMode     string             `json:"bodyMode"`
	Body         string             `json:"body"`
	Auth         runner.Auth        `json:"auth"`
	PreScript    string             `json:"preScript"`
	PostScript   string             `json:"postScript"`
	Settings     runner.Settings    `json:"settings"`
	OperationKey string             `json:"operationKey"`
	FormData     []runner.FormField `json:"formData"`
	BodyFile     *runner.File       `json:"bodyFile"`
	Messages     json.RawMessage    `json:"messages"` // WebSocket: saved messages
	UpdatedAt    time.Time          `json:"updatedAt"`
}

const requestCols = `id, api_id, collection_id, kind, folder, name, method, url, params, headers, body_mode, body, auth, pre_script, post_script, settings, operation_key, form_data, body_file, messages, updated_at`

func scanRequest(row pgx.Row) (*requestDoc, error) {
	var d requestDoc
	var params, headers, auth, settings, formData, bodyFile []byte
	err := row.Scan(&d.ID, &d.APIID, &d.CollectionID, &d.Kind, &d.Folder, &d.Name, &d.Method, &d.URL, &params, &headers, &d.BodyMode, &d.Body, &auth,
		&d.PreScript, &d.PostScript, &settings, &d.OperationKey, &formData, &bodyFile, &d.Messages, &d.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(formData, &d.FormData)
	if d.FormData == nil {
		d.FormData = []runner.FormField{}
	}
	if string(bodyFile) != "{}" {
		_ = json.Unmarshal(bodyFile, &d.BodyFile)
	}
	_ = json.Unmarshal(params, &d.Params)
	_ = json.Unmarshal(headers, &d.Headers)
	_ = json.Unmarshal(auth, &d.Auth)
	_ = json.Unmarshal(settings, &d.Settings)
	d.Params, d.Headers = nonNil(d.Params), nonNil(d.Headers)
	return &d, nil
}

func nonNil(kv []runner.KV) []runner.KV {
	if kv == nil {
		return []runner.KV{}
	}
	return kv
}

func (s *Server) loadRequest(ctx context.Context, ws, id string) (*requestDoc, error) {
	return scanRequest(s.db.QueryRow(ctx, `SELECT `+requestCols+` FROM requests WHERE workspace_id=$1 AND id::text=$2`, ws, id))
}

// collections returns the sidebar tree: APIs → folders → requests.
func (s *Server) collections(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT a.id, a.name, a.current_version, a.draft_version, a.status, r.id, r.name, r.method, r.folder, r.operation_key
		FROM apis a LEFT JOIN requests r ON r.api_id = a.id
		WHERE a.workspace_id = $1
		ORDER BY a.status = 'deprecated', a.created_at, r.sort, r.name`, p.WorkspaceID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type req struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Method       string `json:"method"`
		OperationKey string `json:"operationKey"`
	}
	type folder struct {
		Name     string `json:"name"`
		Requests []req  `json:"requests"`
	}
	type coll struct {
		ID      string    `json:"id"`
		Kind    string    `json:"kind"` // api | collection | scratch
		Name    string    `json:"name"`
		Version string    `json:"version"`
		Status  string    `json:"status"`
		Folders []*folder `json:"folders"`
	}
	out := []*coll{}
	byID := map[string]*coll{}
	for rows.Next() {
		var aid, aname, cur, draft, status string
		var rid, rname, rmethod, rfolder, rop *string
		if err := rows.Scan(&aid, &aname, &cur, &draft, &status, &rid, &rname, &rmethod, &rfolder, &rop); err != nil {
			serverError(w, err)
			return
		}
		c := byID[aid]
		if c == nil {
			v := cur
			if v == "" {
				v = draft
			}
			c = &coll{ID: aid, Kind: "api", Name: aname, Version: majorVersion(v), Status: status, Folders: []*folder{}}
			byID[aid] = c
			out = append(out, c)
		}
		if rid == nil {
			continue
		}
		var f *folder
		for _, x := range c.Folders {
			if x.Name == *rfolder {
				f = x
			}
		}
		if f == nil {
			f = &folder{Name: *rfolder}
			c.Folders = append(c.Folders, f)
		}
		f.Requests = append(f.Requests, req{ID: *rid, Name: *rname, Method: *rmethod, OperationKey: *rop})
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}

	// Collections people made or imported.
	crows, err := s.db.Query(r.Context(), `
		SELECT c.id::text, c.name, r.id::text, r.name, r.method, r.folder, r.kind
		FROM collections c LEFT JOIN requests r ON r.collection_id = c.id
		WHERE c.workspace_id = $1 AND c.api_id IS NULL
		ORDER BY c.sort, c.created_at, r.sort, r.created_at`, p.WorkspaceID)
	if err != nil {
		serverError(w, err)
		return
	}
	for crows.Next() {
		var cid, cname string
		var rid, rname, rmethod, rfolder, rkind *string
		if err := crows.Scan(&cid, &cname, &rid, &rname, &rmethod, &rfolder, &rkind); err != nil {
			crows.Close()
			serverError(w, err)
			return
		}
		c := byID[cid]
		if c == nil {
			c = &coll{ID: cid, Kind: "collection", Name: cname, Status: "draft", Folders: []*folder{}}
			byID[cid] = c
			out = append(out, c)
		}
		if rid == nil {
			continue
		}
		var f *folder
		for _, x := range c.Folders {
			if x.Name == *rfolder {
				f = x
			}
		}
		if f == nil {
			f = &folder{Name: *rfolder}
			c.Folders = append(c.Folders, f)
		}
		m := *rmethod
		if *rkind == "websocket" {
			m = "WS"
		}
		f.Requests = append(f.Requests, req{ID: *rid, Name: *rname, Method: m})
	}
	crows.Close()

	// Requests made with "+" in the editor belong to no API or collection; list them under "Scratch".
	loose, err := s.db.Query(r.Context(), `
		SELECT id, name, CASE WHEN kind = 'websocket' THEN 'WS' ELSE method END, folder FROM requests
		WHERE workspace_id = $1 AND api_id IS NULL AND collection_id IS NULL ORDER BY folder, sort, created_at`, p.WorkspaceID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer loose.Close()
	scratch := &coll{ID: "scratch", Kind: "scratch", Name: "Scratch", Status: "draft", Folders: []*folder{}}
	for loose.Next() {
		var x req
		var fname string
		if err := loose.Scan(&x.ID, &x.Name, &x.Method, &fname); err != nil {
			serverError(w, err)
			return
		}
		if fname == "Scratch" {
			fname = "" // already the collection's name
		}
		var f *folder
		for _, y := range scratch.Folders {
			if y.Name == fname {
				f = y
			}
		}
		if f == nil {
			f = &folder{Name: fname}
			scratch.Folders = append(scratch.Folders, f)
		}
		f.Requests = append(f.Requests, x)
	}
	if len(scratch.Folders) > 0 {
		out = append(out, scratch)
	}
	writeJSON(w, http.StatusOK, out)
}

func majorVersion(v string) string {
	v = strings.TrimPrefix(v, "v")
	if i := strings.Index(v, "."); i > 0 {
		v = v[:i]
	}
	if v == "" {
		return ""
	}
	return "v" + v
}

func (s *Server) getRequest(w http.ResponseWriter, r *http.Request) {
	d, err := s.loadRequest(r.Context(), principal(r).WorkspaceID, chi.URLParam(r, "id"))
	if notFound(err) {
		httpError(w, http.StatusNotFound, "Request not found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func marshalRequest(in *requestDoc) (params, headers, auth, settings []byte) {
	params, _ = json.Marshal(nonNil(in.Params))
	headers, _ = json.Marshal(nonNil(in.Headers))
	auth, _ = json.Marshal(in.Auth)
	settings, _ = json.Marshal(in.Settings)
	return
}

// marshalExtras encodes multipart fields, the binary body and WebSocket messages.
func marshalExtras(in *requestDoc) (formData, bodyFile, messages []byte) {
	if in.FormData == nil {
		in.FormData = []runner.FormField{}
	}
	formData, _ = json.Marshal(in.FormData)
	bodyFile = []byte("{}")
	if in.BodyFile != nil {
		bodyFile, _ = json.Marshal(in.BodyFile)
	}
	messages = in.Messages
	if len(messages) == 0 || string(messages) == "null" {
		messages = []byte("[]")
	}
	return
}

const maxRequestFile = 10 << 20 // base64 data in form fields and binary bodies

func requestTooBig(in *requestDoc) bool {
	n := 0
	for _, f := range in.FormData {
		n += len(f.Value)
	}
	if in.BodyFile != nil {
		n += len(in.BodyFile.Data)
	}
	return n > maxRequestFile*4/3
}

func (s *Server) createRequest(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	var in requestDoc
	if !readJSON(w, r, &in) {
		return
	}
	if in.Name == "" {
		in.Name = "Untitled request"
	}
	if in.Method == "" {
		in.Method = "GET"
	}
	if in.BodyMode == "" {
		in.BodyMode = "none"
	}
	if in.Auth.Type == "" {
		in.Auth.Type = "inherit"
	}
	if in.Kind != "websocket" {
		in.Kind = "http"
	}
	if requestTooBig(&in) {
		httpError(w, http.StatusRequestEntityTooLarge, "Files in a request can be up to 10 MB in total")
		return
	}
	if in.CollectionID != nil && !s.collectionInWorkspace(r.Context(), p.WorkspaceID, *in.CollectionID) {
		httpError(w, http.StatusBadRequest, "Collection not found")
		return
	}
	params, headers, auth, settings := marshalRequest(&in)
	formData, bodyFile, messages := marshalExtras(&in)
	d, err := scanRequest(s.db.QueryRow(r.Context(), `
		INSERT INTO requests (workspace_id, api_id, collection_id, kind, folder, name, method, url, params, headers, body_mode, body, auth, pre_script, post_script, settings, form_data, body_file, messages, sort)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,
		        COALESCE((SELECT max(sort) + 1 FROM requests WHERE workspace_id = $1 AND collection_id IS NOT DISTINCT FROM $3 AND api_id IS NOT DISTINCT FROM $2), 0))
		RETURNING `+requestCols,
		p.WorkspaceID, in.APIID, in.CollectionID, in.Kind, in.Folder, in.Name, strings.ToUpper(in.Method), in.URL, params, headers, in.BodyMode, in.Body, auth,
		in.PreScript, in.PostScript, settings, formData, bodyFile, messages))
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) updateRequest(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	var in requestDoc
	if !readJSON(w, r, &in) {
		return
	}
	if requestTooBig(&in) {
		httpError(w, http.StatusRequestEntityTooLarge, "Files in a request can be up to 10 MB in total")
		return
	}
	params, headers, auth, settings := marshalRequest(&in)
	formData, bodyFile, messages := marshalExtras(&in)
	d, err := scanRequest(s.db.QueryRow(r.Context(), `
		UPDATE requests SET name=$3, method=$4, url=$5, params=$6, headers=$7, body_mode=$8, body=$9, auth=$10,
		       pre_script=$11, post_script=$12, settings=$13, form_data=$14, body_file=$15, messages=$16,
		       folder=COALESCE(NULLIF($17,''), folder), updated_at=now()
		WHERE workspace_id=$1 AND id::text=$2 RETURNING `+requestCols,
		p.WorkspaceID, chi.URLParam(r, "id"), in.Name, strings.ToUpper(in.Method), in.URL, params, headers, in.BodyMode, in.Body, auth,
		in.PreScript, in.PostScript, settings, formData, bodyFile, messages, in.Folder))
	if notFound(err) {
		httpError(w, http.StatusNotFound, "Request not found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) deleteRequest(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	if _, err := s.db.Exec(r.Context(), `DELETE FROM requests WHERE workspace_id=$1 AND id::text=$2`, p.WorkspaceID, chi.URLParam(r, "id")); err != nil {
		serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- send -----

func transitKey(ws string) string { return "ws-" + ws }

// resolveEnv returns the chosen environment, every variable value (globals,
// then the environment's own) and the list of secret values to redact.
// Secrets that can't be decrypted are left out and named in unreadable.
func (s *Server) resolveEnv(ctx context.Context, p *Principal, envID string, collectionVars []runner.KV) (id, name string, vars map[string]string, secrets, unreadable []string, err error) {
	if envID == "" {
		envID, _ = p.Prefs["activeEnvironmentId"].(string)
	}
	err = s.db.QueryRow(ctx, `
		SELECT id, name FROM environments WHERE workspace_id=$1 AND NOT is_global
		ORDER BY (id::text = $2) DESC, sort LIMIT 1`, p.WorkspaceID, envID).Scan(&id, &name)
	if err != nil && !notFound(err) {
		return
	}
	rows, err := s.db.Query(ctx, `
		SELECT v.key, v.value, v.ciphertext, v.kind, e.is_global
		FROM env_vars v JOIN environments e ON e.id = v.env_id
		WHERE e.workspace_id=$1 AND v.enabled AND (e.is_global OR e.id::text = $2)
		ORDER BY e.is_global DESC, v.sort`, p.WorkspaceID, id)
	if err != nil {
		return
	}
	type row struct {
		key, value, ct, kind string
		global               bool
	}
	all, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var x row
		return x, r.Scan(&x.key, &x.value, &x.ct, &x.kind, &x.global)
	})
	if err != nil {
		return
	}
	var cts []string
	var idx []int
	for i, x := range all {
		if x.kind == "secret" && x.ct != "" {
			cts, idx = append(cts, x.ct), append(idx, i)
		}
	}
	plain, ok, err := s.bao.DecryptBatch(ctx, transitKey(p.WorkspaceID), cts)
	if err != nil {
		return
	}
	skip := map[int]bool{}
	for j, i := range idx {
		if !ok[j] {
			skip[i] = true
			unreadable = append(unreadable, all[i].key)
			continue
		}
		all[i].value = plain[j]
		secrets = append(secrets, plain[j])
	}
	// Postman's order: globals, then the collection's variables, then the environment's.
	vars = map[string]string{}
	collectionDone := false
	for i, x := range all {
		if !x.global && !collectionDone {
			for _, v := range collectionVars {
				if v.Enabled && v.Key != "" {
					vars[v.Key] = v.Value
				}
			}
			collectionDone = true
		}
		if !skip[i] {
			vars[x.key] = x.value
		}
	}
	if !collectionDone {
		for _, v := range collectionVars {
			if v.Enabled && v.Key != "" {
				vars[v.Key] = v.Value
			}
		}
	}
	return
}

func (s *Server) sendRequest(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	ctx := r.Context()
	d, err := s.loadRequest(ctx, p.WorkspaceID, chi.URLParam(r, "id"))
	if notFound(err) {
		httpError(w, http.StatusNotFound, "Request not found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	var in struct {
		Draft         *requestDoc       `json:"draft"` // unsaved edits from the editor
		EnvironmentID string            `json:"environmentId"`
		Data          map[string]string `json:"data"` // the Collection Runner's iteration data
	}
	if r.ContentLength != 0 && !readJSON(w, r, &in) {
		return
	}
	if in.Draft != nil {
		in.Draft.ID, in.Draft.APIID, in.Draft.CollectionID = d.ID, d.APIID, d.CollectionID
		d = in.Draft
	}
	res, envName, historyID, err := s.sendDoc(ctx, p, d, in.EnvironmentID, in.Data)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": res, "historyId": historyID, "environment": envName})
}

// sendDoc sends a request the way its collection and environment say, keeps
// what scripts and cookies changed, and records it in history.
func (s *Server) sendDoc(ctx context.Context, p *Principal, d *requestDoc, envID string, data map[string]string) (*runner.Result, string, string, error) {
	inh := s.inheritFor(ctx, p.WorkspaceID, d)
	envID, envName, vars, secrets, unreadable, err := s.resolveEnv(ctx, p, envID, inh.vars)
	if err != nil {
		return nil, "", "", err
	}
	for k, v := range data {
		vars[k] = v
	}
	auth := d.Auth
	if auth.Type == "inherit" || auth.Type == "" {
		auth = inh.auth
	}
	if auth.Type == "inherit" || auth.Type == "" {
		auth = runner.Auth{Type: "none"}
		if _, ok := vars["secret_key"]; ok {
			auth = runner.Auth{Type: "bearer", Token: "{{secret_key}}"}
		}
	}
	if d.Settings.TimeoutMs == 0 {
		if t, ok := p.Prefs["defaultTimeoutMs"].(float64); ok {
			d.Settings.TimeoutMs = int(t)
		}
	}
	res := s.runner.Send(ctx, runner.Spec{
		Method: d.Method, URL: d.URL, Params: d.Params, Headers: d.Headers, BodyMode: d.BodyMode, Body: d.Body,
		FormData: d.FormData, BodyFile: d.BodyFile, Cookies: s.loadCookies(ctx, p),
		Auth: auth, PreScript: joinScripts(inh.pre, d.PreScript), PostScript: joinScripts(inh.post, d.PostScript), Settings: d.Settings,
	}, vars, secrets)
	if len(res.Cookies) > 0 && p.UserID != "" {
		s.saveCookies(ctx, p, res.Cookies)
	}
	if len(res.EnvChanges) > 0 && envID != "" && p.CanEdit() {
		if err := s.applyScriptVars(ctx, p, envID, res.EnvChanges); err != nil {
			res.ScriptError = strings.TrimSpace(res.ScriptError + " (couldn't save variables: " + err.Error() + ")")
		}
	}
	if len(unreadable) > 0 {
		res.ScriptError = strings.TrimSpace(fmt.Sprintf("Couldn't decrypt %s, so %s left out. OpenBao no longer has this workspace's key (a dev-mode restart clears it). Re-enter the value in Environments. %s",
			strings.Join(unreadable, ", "), map[bool]string{true: "it was", false: "they were"}[len(unreadable) == 1], res.ScriptError))
	}
	historyID, err := s.saveHistory(ctx, p, d, envName, res)
	if err != nil {
		return nil, "", "", err
	}
	return res, envName, historyID, nil
}

// applyScriptVars stores variables set by scripts (re-encrypting secrets).
func (s *Server) applyScriptVars(ctx context.Context, p *Principal, envID string, changes map[string]string) error {
	keys := make([]string, 0, len(changes))
	for k := range changes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := changes[k]
		var kind string
		err := s.db.QueryRow(ctx, `SELECT kind FROM env_vars WHERE env_id=$1 AND key=$2`, envID, k).Scan(&kind)
		switch {
		case notFound(err):
			_, err = s.db.Exec(ctx, `INSERT INTO env_vars (env_id, key, value, kind, sort) VALUES ($1,$2,$3,'script', 1000)`, envID, k, v)
		case err != nil:
		case kind == "secret":
			var ct string
			if ct, err = s.bao.Encrypt(ctx, transitKey(p.WorkspaceID), v); err == nil {
				_, err = s.db.Exec(ctx, `UPDATE env_vars SET ciphertext=$3, updated_at=now() WHERE env_id=$1 AND key=$2`, envID, k, ct)
			}
		default:
			_, err = s.db.Exec(ctx, `UPDATE env_vars SET value=$3, updated_at=now() WHERE env_id=$1 AND key=$2`, envID, k, v)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

const inlineBodyLimit = 64 << 10

func (s *Server) saveHistory(ctx context.Context, p *Principal, d *requestDoc, envName string, res *runner.Result) (string, error) {
	if !s.settings(ctx).HistoryEnabled {
		return "", nil // an admin turned request history off
	}
	body := res.Body
	if save, ok := p.Prefs["saveResponses"].(bool); ok && !save {
		body = ""
	}
	reqHeaders, _ := json.Marshal(res.ReqHeaders)
	resHeaders, _ := json.Marshal(res.ResHeaders)
	timeline, _ := json.Marshal(res.Timeline)
	tests, _ := json.Marshal(res.Tests)
	inline := body
	if len(body) > inlineBodyLimit {
		inline = ""
	}
	var id string
	err := s.db.QueryRow(ctx, `
		INSERT INTO history (workspace_id, user_id, request_id, api_id, method, url, status, duration_ms, size_bytes, env_name, error,
		                     req_headers, res_headers, res_body, timeline, tests)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) RETURNING id`,
		p.WorkspaceID, p.UserID, d.ID, d.APIID, res.Method, res.URL, res.Status, res.DurationMs, res.SizeBytes, envName, res.Error,
		reqHeaders, resHeaders, inline, timeline, tests).Scan(&id)
	if err != nil {
		return "", err
	}
	if len(body) > inlineBodyLimit {
		object := "history/" + id
		if err := s.s3.Put(ctx, object, []byte(body), "application/octet-stream"); err == nil {
			_, _ = s.db.Exec(ctx, `UPDATE history SET body_object=$2 WHERE id=$1`, id, object)
		}
	}
	return id, nil
}
