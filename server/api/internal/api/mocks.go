package api

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/anilcharles/requara/server/api/internal/core"
	"github.com/anilcharles/requara/server/api/internal/events"
	"github.com/anilcharles/requara/server/api/internal/spec"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type mockOut struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	APIID      string `json:"apiId"`
	APIName    string `json:"apiName"`
	APIVersion string `json:"apiVersion"`
	Slug       string `json:"slug"`
	URL        string `json:"url"`
	Enabled    bool   `json:"enabled"`
	LatencyMs  int    `json:"latencyMs"`
	ErrorRate  int    `json:"errorRate"`
	CallsToday int    `json:"callsToday"`
}

func (s *Server) listMocks(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	// "Today" is the viewer's day, not the database's (UTC): the app sends its IANA time zone.
	tz := r.URL.Query().Get("tz")
	if _, err := time.LoadLocation(tz); tz == "" || err != nil {
		tz = "UTC"
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT m.id, m.name, a.id, a.name, COALESCE(NULLIF(a.current_version,''), a.draft_version), m.slug, '', m.enabled, m.latency_ms, m.error_rate,
		       (SELECT count(*)::int FROM mock_calls c WHERE c.mock_id = m.id
		          AND c.created_at >= date_trunc('day', now() AT TIME ZONE $2) AT TIME ZONE $2)
		FROM mock_servers m JOIN apis a ON a.id = m.api_id
		WHERE m.workspace_id=$1 ORDER BY m.created_at`, p.WorkspaceID, tz)
	if err != nil {
		serverError(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[mockOut])
	if err != nil {
		serverError(w, err)
		return
	}
	for i := range out {
		out[i].URL = s.cfg.PublicURL + "/mock/" + out[i].Slug
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createMock(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	var in struct {
		APIID     string `json:"apiId"`
		Name      string `json:"name"`
		LatencyMs int    `json:"latencyMs"`
		ErrorRate int    `json:"errorRate"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	a, err := s.loadAPI(r.Context(), p.WorkspaceID, in.APIID)
	if err != nil {
		httpError(w, http.StatusBadRequest, "Pick an API")
		return
	}
	if in.Name = strings.TrimSpace(in.Name); in.Name == "" {
		in.Name = a.Name + " mock"
	}
	if in.ErrorRate < 0 || in.ErrorRate > 100 || in.LatencyMs < 0 || in.LatencyMs > 10000 {
		httpError(w, http.StatusBadRequest, "Latency is 0–10000 ms and error rate 0–100%")
		return
	}
	slug := slugify(in.Name)
	var n int
	_ = s.db.QueryRow(r.Context(), `SELECT count(*) FROM mock_servers WHERE slug=$1 OR slug LIKE $1 || '-%'`, slug).Scan(&n)
	if n > 0 {
		slug = fmt.Sprintf("%s-%d", slug, n+1)
	}
	var id string
	if err := s.db.QueryRow(r.Context(), `
		INSERT INTO mock_servers (workspace_id, api_id, name, slug, latency_ms, error_rate) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		p.WorkspaceID, a.ID, in.Name, slug, in.LatencyMs, in.ErrorRate).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "url": s.cfg.PublicURL + "/mock/" + slug})
}

func (s *Server) updateMock(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	var in struct {
		Name      *string `json:"name"`
		Enabled   *bool   `json:"enabled"`
		LatencyMs *int    `json:"latencyMs"`
		ErrorRate *int    `json:"errorRate"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if (in.ErrorRate != nil && (*in.ErrorRate < 0 || *in.ErrorRate > 100)) || (in.LatencyMs != nil && (*in.LatencyMs < 0 || *in.LatencyMs > 10000)) {
		httpError(w, http.StatusBadRequest, "Latency is 0–10000 ms and error rate 0–100%")
		return
	}
	tag, err := s.db.Exec(r.Context(), `
		UPDATE mock_servers SET name=COALESCE($3,name), enabled=COALESCE($4,enabled),
		       latency_ms=COALESCE($5,latency_ms), error_rate=COALESCE($6,error_rate)
		WHERE workspace_id=$1 AND id::text=$2`, p.WorkspaceID, chi.URLParam(r, "id"), in.Name, in.Enabled, in.LatencyMs, in.ErrorRate)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		httpError(w, http.StatusNotFound, "Mock server not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type mockCall struct {
	ID        int64     `json:"id"`
	Method    string    `json:"method"`
	Path      string    `json:"path"`
	Status    int       `json:"status"`
	MockName  string    `json:"mockName"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Server) mockCalls(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `
		SELECT c.id, c.method, c.path, c.status, m.name, c.created_at
		FROM mock_calls c JOIN mock_servers m ON m.id = c.mock_id
		WHERE m.workspace_id=$1 ORDER BY c.id DESC LIMIT 50`, principal(r).WorkspaceID)
	if err != nil {
		serverError(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[mockCall])
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// mockStream pushes new mock calls to the browser (Server-Sent Events).
func (s *Server) mockStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	ch, cancel := s.hub.Subscribe(principal(r).WorkspaceID)
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			fl.Flush()
		}
	}
}

// ----- the public mock endpoint -----

func (s *Server) serveMock(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ctx := r.Context()
	var mockID, ws, apiID, name string
	var enabled bool
	var latency, errRate int
	err := s.db.QueryRow(ctx, `SELECT id, workspace_id, api_id, name, enabled, latency_ms, error_rate FROM mock_servers WHERE slug=$1`,
		chi.URLParam(r, "slug")).Scan(&mockID, &ws, &apiID, &name, &enabled, &latency, &errRate)
	if err != nil {
		problem(w, http.StatusNotFound, "Mock server not found", "")
		return
	}
	if !enabled {
		problem(w, http.StatusServiceUnavailable, "Mock server is stopped", "Turn it on in Requara → Mock servers.")
		return
	}
	path := "/" + chi.URLParam(r, "*")
	status := s.answerMock(w, r, apiID, path, latency, errRate)
	var callID int64
	_ = s.db.QueryRow(ctx, `INSERT INTO mock_calls (mock_id, method, path, status) VALUES ($1,$2,$3,$4) RETURNING id`, mockID, r.Method, path, status).Scan(&callID)
	s.hub.Publish(ws, events.Event{Type: "mock_call", Data: mockCall{ID: callID, Method: r.Method, Path: path, Status: status, MockName: name, CreatedAt: time.Now()}})
}

func problem(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"title": title, "status": status, "detail": detail})
}

// answerMock writes the response and returns its status code.
func (s *Server) answerMock(w http.ResponseWriter, r *http.Request, apiID, path string, latency, errRate int) int {
	ctx := r.Context()
	doc, _, err := s.published(ctx, apiID)
	if err != nil || doc == nil {
		var raw []byte
		if err := s.db.QueryRow(ctx, `SELECT draft_spec FROM apis WHERE id=$1`, apiID).Scan(&raw); err != nil {
			problem(w, http.StatusInternalServerError, "Spec unavailable", "")
			return 500
		}
		if doc, err = spec.FromJSON(raw); err != nil {
			problem(w, http.StatusInternalServerError, "Spec unavailable", "")
			return 500
		}
	}
	if latency > 0 {
		select {
		case <-time.After(time.Duration(latency) * time.Millisecond):
		case <-ctx.Done():
			return 499
		}
	}
	if res, failed := core.InjectedError(errRate); failed {
		return writeMock(w, res)
	}
	res := core.MockAnswer(doc, r.Method, path, r.Header.Get("Prefer"), r.Body)
	w.Header().Set("X-Requara-Mock", "true")
	w.Header().Set("Request-Id", fmt.Sprintf("req_%x", rand.Uint64()))
	return writeMock(w, res)
}

func writeMock(w http.ResponseWriter, res core.MockResponse) int {
	w.Header().Set("Content-Type", res.ContentType)
	w.WriteHeader(res.Status)
	_, _ = io.WriteString(w, res.Body)
	return res.Status
}
