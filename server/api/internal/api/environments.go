package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/anilcharles/requara/server/api/internal/runner"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// ----- environments -----

func (s *Server) listEnvironments(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT e.id, e.name, e.color, e.is_global, count(v.id)::int
		FROM environments e LEFT JOIN env_vars v ON v.env_id = e.id
		WHERE e.workspace_id=$1 GROUP BY e.id ORDER BY e.is_global, e.sort, e.name`, p.WorkspaceID)
	if err != nil {
		serverError(w, err)
		return
	}
	type env struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Color    string `json:"color"`
		IsGlobal bool   `json:"isGlobal"`
		Count    int    `json:"count"`
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[env])
	if err != nil {
		serverError(w, err)
		return
	}
	active, _ := p.Prefs["activeEnvironmentId"].(string)
	found := false
	for _, e := range out {
		found = found || (e.ID == active && !e.IsGlobal)
	}
	if !found {
		active = ""
		for _, e := range out {
			if !e.IsGlobal {
				active = e.ID
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "activeId": active})
}

func (s *Server) createEnvironment(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	var in struct{ Name, Color string }
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		httpError(w, http.StatusBadRequest, "Give the environment a name")
		return
	}
	if in.Color == "" {
		in.Color = "#1677FF"
	}
	var id string
	if err := s.db.QueryRow(r.Context(), `
		INSERT INTO environments (workspace_id, name, color, sort) VALUES ($1,$2,$3, 100) RETURNING id`,
		p.WorkspaceID, in.Name, in.Color).Scan(&id); err != nil {
		httpError(w, http.StatusConflict, "An environment with that name already exists")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

type varOut struct {
	ID       string `json:"id"`
	Key      string `json:"key"`
	Value    string `json:"value"` // always "" for secrets: they're write-only
	Kind     string `json:"kind"`
	Enabled  bool   `json:"enabled"`
	HasValue bool   `json:"hasValue"`
}

func (s *Server) envInWorkspace(ctx context.Context, ws, id string) bool {
	var ok bool
	_ = s.db.QueryRow(ctx, `SELECT true FROM environments WHERE workspace_id=$1 AND id::text=$2`, ws, id).Scan(&ok)
	return ok
}

func (s *Server) listVariables(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	id := chi.URLParam(r, "id")
	if !s.envInWorkspace(r.Context(), p.WorkspaceID, id) {
		httpError(w, http.StatusNotFound, "Environment not found")
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT id, key, CASE WHEN kind='secret' THEN '' ELSE value END, kind, enabled,
		       CASE WHEN kind='secret' THEN ciphertext <> '' ELSE value <> '' END
		FROM env_vars WHERE env_id::text=$1 ORDER BY sort, key`, id)
	if err != nil {
		serverError(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[varOut])
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// saveVariables replaces an environment's variables. Secret values are
// write-only: send "keep": true to leave a stored secret as it is.
func (s *Server) saveVariables(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	id := chi.URLParam(r, "id")
	ctx := r.Context()
	if !s.envInWorkspace(ctx, p.WorkspaceID, id) {
		httpError(w, http.StatusNotFound, "Environment not found")
		return
	}
	var in []struct {
		Key     string `json:"key"`
		Value   string `json:"value"`
		Kind    string `json:"kind"`
		Enabled bool   `json:"enabled"`
		Keep    bool   `json:"keep"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	existing := map[string]string{} // secret key -> ciphertext
	if rows, err := s.db.Query(ctx, `SELECT key, ciphertext FROM env_vars WHERE env_id::text=$1 AND kind='secret'`, id); err == nil {
		for rows.Next() {
			var k, c string
			_ = rows.Scan(&k, &c)
			existing[k] = c
		}
		rows.Close()
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM env_vars WHERE env_id::text=$1`, id); err != nil {
		serverError(w, err)
		return
	}
	seen := map[string]bool{}
	for i, v := range in {
		v.Key = strings.TrimSpace(v.Key)
		if v.Key == "" {
			continue
		}
		if seen[v.Key] {
			httpError(w, http.StatusBadRequest, "Variable "+v.Key+" appears twice")
			return
		}
		seen[v.Key] = true
		if v.Kind != "secret" && v.Kind != "script" {
			v.Kind = "default"
		}
		value, ct := v.Value, ""
		if v.Kind == "secret" {
			value = ""
			if v.Keep {
				ct = existing[v.Key]
			} else if v.Value != "" {
				if ct, err = s.bao.Encrypt(ctx, transitKey(p.WorkspaceID), v.Value); err != nil {
					serverError(w, err)
					return
				}
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO env_vars (env_id, key, value, ciphertext, kind, enabled, sort) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			id, v.Key, value, ct, v.Kind, v.Enabled, i); err != nil {
			serverError(w, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	s.listVariables(w, r)
}

// ----- history -----

func (s *Server) listHistory(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	q := r.URL.Query()
	rows, err := s.db.Query(r.Context(), `
		SELECT id, request_id, method, url, status, duration_ms, size_bytes, env_name, error, created_at
		FROM history WHERE workspace_id=$1
		  AND ($2 = '' OR url ILIKE '%' || $2 || '%' OR status::text = $2)
		  AND ($3 = '' OR method = $3)
		  AND ($4 = '' OR (status / 100)::text || 'xx' = $4 OR ($4 = 'error' AND status = 0))
		  AND ($5 = '' OR env_name = $5)
		ORDER BY created_at DESC LIMIT 200`,
		p.WorkspaceID, strings.TrimSpace(q.Get("q")), strings.ToUpper(q.Get("method")), q.Get("status"), q.Get("env"))
	if err != nil {
		serverError(w, err)
		return
	}
	type item struct {
		ID         string    `json:"id"`
		RequestID  *string   `json:"requestId"`
		Method     string    `json:"method"`
		URL        string    `json:"url"`
		Status     int       `json:"status"`
		DurationMs int       `json:"durationMs"`
		SizeBytes  int       `json:"sizeBytes"`
		EnvName    string    `json:"envName"`
		Error      string    `json:"error"`
		CreatedAt  time.Time `json:"createdAt"`
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[item])
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getHistory(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	var res runner.Result
	var reqH, resH, tl, tests []byte
	var object string
	var requestID *string
	err := s.db.QueryRow(r.Context(), `
		SELECT request_id, method, url, status, duration_ms, size_bytes, error, req_headers, res_headers, res_body, body_object, timeline, tests
		FROM history WHERE workspace_id=$1 AND id::text=$2`, p.WorkspaceID, chi.URLParam(r, "id")).
		Scan(&requestID, &res.Method, &res.URL, &res.Status, &res.DurationMs, &res.SizeBytes, &res.Error, &reqH, &resH, &res.Body, &object, &tl, &tests)
	if notFound(err) {
		httpError(w, http.StatusNotFound, "Not found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	_ = json.Unmarshal(reqH, &res.ReqHeaders)
	_ = json.Unmarshal(resH, &res.ResHeaders)
	_ = json.Unmarshal(tl, &res.Timeline)
	_ = json.Unmarshal(tests, &res.Tests)
	res.StatusText = http.StatusText(res.Status)
	if object != "" {
		if b, err := s.s3.Get(r.Context(), object); err == nil {
			res.Body = string(b)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"requestId": requestID, "result": res})
}

func (s *Server) clearHistory(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	rows, err := s.db.Query(r.Context(), `DELETE FROM history WHERE workspace_id=$1 AND user_id=$2 RETURNING body_object`, p.WorkspaceID, p.UserID)
	if err != nil {
		serverError(w, err)
		return
	}
	objects, _ := pgx.CollectRows(rows, pgx.RowTo[string])
	for _, o := range objects {
		if o != "" {
			_ = s.s3.Delete(r.Context(), o)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
