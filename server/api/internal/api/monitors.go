package api

// Monitors run a collection on a schedule on the server and email people
// when a run fails (a test fails or a request errors).

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/anilcharles/requara/server/api/internal/jobs"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// RunResult is one request in a collection run.
type RunResult struct {
	RequestID  string `json:"requestId"`
	Name       string `json:"name"`
	Method     string `json:"method"`
	URL        string `json:"url"`
	Status     int    `json:"status"`
	DurationMs int64  `json:"durationMs"`
	Tests      []struct {
		Name  string `json:"name"`
		Pass  bool   `json:"pass"`
		Error string `json:"error,omitempty"`
	} `json:"tests"`
	Error string `json:"error,omitempty"`
}

// collectionRequests lists a collection's requests in sidebar order:
// target is a collection id, an API id or "scratch".
func (s *Server) collectionRequests(ctx context.Context, ws, target string) ([]*requestDoc, error) {
	where := "collection_id::text = $2"
	switch {
	case target == "scratch":
		where = "api_id IS NULL AND collection_id IS NULL AND $2 = 'scratch'"
	default:
		var isAPI bool
		_ = s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM apis WHERE workspace_id=$1 AND id::text=$2)`, ws, target).Scan(&isAPI)
		if isAPI {
			where = "api_id::text = $2"
		}
	}
	rows, err := s.db.Query(ctx, `SELECT `+requestCols+` FROM requests WHERE workspace_id=$1 AND kind='http' AND `+where+` ORDER BY sort, created_at, name`, ws, target)
	if err != nil {
		return nil, err
	}
	docs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*requestDoc, error) { return scanRequest(row) })
	if err != nil {
		return nil, err
	}
	// Folders in the order they first appear, like the sidebar.
	var order []string
	byFolder := map[string][]*requestDoc{}
	for _, d := range docs {
		if _, ok := byFolder[d.Folder]; !ok {
			order = append(order, d.Folder)
		}
		byFolder[d.Folder] = append(byFolder[d.Folder], d)
	}
	out := make([]*requestDoc, 0, len(docs))
	for _, f := range order {
		out = append(out, byFolder[f]...)
	}
	return out, nil
}

func (s *Server) runCollection(ctx context.Context, p *Principal, target, envID string, data map[string]string) ([]RunResult, error) {
	docs, err := s.collectionRequests(ctx, p.WorkspaceID, target)
	if err != nil {
		return nil, err
	}
	out := []RunResult{}
	for _, d := range docs {
		if ctx.Err() != nil {
			break
		}
		res, _, _, err := s.sendDoc(ctx, p, d, envID, data)
		rr := RunResult{RequestID: d.ID, Name: d.Name, Method: d.Method}
		if err != nil {
			rr.Error = err.Error()
		} else {
			rr.URL, rr.Status, rr.DurationMs, rr.Error = res.URL, res.Status, res.DurationMs, res.Error
			if res.ScriptError != "" && rr.Error == "" {
				rr.Error = res.ScriptError
			}
			for _, t := range res.Tests {
				rr.Tests = append(rr.Tests, struct {
					Name  string `json:"name"`
					Pass  bool   `json:"pass"`
					Error string `json:"error,omitempty"`
				}{t.Name, t.Pass, t.Error})
			}
		}
		out = append(out, rr)
	}
	return out, nil
}

type monitorOut struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Target        string     `json:"target"`
	TargetName    string     `json:"targetName"`
	EnvironmentID *string    `json:"environmentId"`
	IntervalMin   int        `json:"intervalMin"`
	Enabled       bool       `json:"enabled"`
	Notify        string     `json:"notify"`
	NextRunAt     time.Time  `json:"nextRunAt"`
	LastRunAt     *time.Time `json:"lastRunAt"`
	LastFailed    *int       `json:"lastFailed"`
	LastErrors    *int       `json:"lastErrors"`
	LastPassed    *int       `json:"lastPassed"`
}

func (s *Server) listMonitors(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `
		SELECT m.id::text, m.name, m.target,
		       COALESCE((SELECT name FROM collections c WHERE c.id::text = m.target), (SELECT name FROM apis a WHERE a.id::text = m.target), 'Scratch'),
		       m.environment_id::text, m.interval_min, m.enabled, m.notify, m.next_run_at,
		       lr.started_at, lr.failed, lr.errors, lr.passed
		FROM monitors m
		LEFT JOIN LATERAL (SELECT started_at, failed, errors, passed FROM monitor_runs WHERE monitor_id = m.id ORDER BY started_at DESC LIMIT 1) lr ON true
		WHERE m.workspace_id = $1 ORDER BY m.created_at`, principal(r).WorkspaceID)
	if err != nil {
		serverError(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[monitorOut])
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type monitorIn struct {
	Name          *string `json:"name"`
	Target        *string `json:"target"`
	EnvironmentID *string `json:"environmentId"`
	IntervalMin   *int    `json:"intervalMin"`
	Enabled       *bool   `json:"enabled"`
	Notify        *string `json:"notify"`
}

func (in *monitorIn) validate() string {
	if in.IntervalMin != nil && (*in.IntervalMin < 5 || *in.IntervalMin > 10080) {
		return "Run every 5 minutes to 7 days"
	}
	if in.Notify != nil {
		for _, e := range strings.FieldsFunc(*in.Notify, func(r rune) bool { return r == ',' || r == ' ' }) {
			if !strings.Contains(e, "@") {
				return e + " is not an email address"
			}
		}
	}
	if in.EnvironmentID != nil && *in.EnvironmentID == "" {
		in.EnvironmentID = nil
	}
	return ""
}

func (s *Server) createMonitor(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	var in monitorIn
	if !readJSON(w, r, &in) {
		return
	}
	if msg := in.validate(); msg != "" {
		httpError(w, http.StatusBadRequest, msg)
		return
	}
	if in.Target == nil || *in.Target == "" {
		httpError(w, http.StatusBadRequest, "Pick a collection to monitor")
		return
	}
	name, interval, notify := "Monitor", 60, ""
	if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
		name = strings.TrimSpace(*in.Name)
	}
	if in.IntervalMin != nil {
		interval = *in.IntervalMin
	}
	if in.Notify != nil {
		notify = *in.Notify
	}
	var id string
	if err := s.db.QueryRow(r.Context(), `
		INSERT INTO monitors (workspace_id, name, target, environment_id, interval_min, notify, created_by, next_run_at)
		VALUES ($1,$2,$3,$4::uuid,$5,$6,$7, now()) RETURNING id::text`,
		p.WorkspaceID, name, *in.Target, in.EnvironmentID, interval, notify, p.UserID).Scan(&id); err != nil {
		httpError(w, http.StatusBadRequest, "Couldn't create the monitor: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) updateMonitor(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	var in monitorIn
	if !readJSON(w, r, &in) {
		return
	}
	if msg := in.validate(); msg != "" {
		httpError(w, http.StatusBadRequest, msg)
		return
	}
	tag, err := s.db.Exec(r.Context(), `
		UPDATE monitors SET name=COALESCE($3,name), environment_id=CASE WHEN $4::boolean THEN $5::uuid ELSE environment_id END,
		       interval_min=COALESCE($6,interval_min), enabled=COALESCE($7,enabled), notify=COALESCE($8,notify)
		WHERE workspace_id=$1 AND id::text=$2`,
		p.WorkspaceID, chi.URLParam(r, "id"), in.Name, in.EnvironmentID != nil, in.EnvironmentID, in.IntervalMin, in.Enabled, in.Notify)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		httpError(w, http.StatusNotFound, "Monitor not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteMonitor(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	if _, err := s.db.Exec(r.Context(), `DELETE FROM monitors WHERE workspace_id=$1 AND id::text=$2`, p.WorkspaceID, chi.URLParam(r, "id")); err != nil {
		serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type runOut struct {
	ID         string          `json:"id"`
	StartedAt  time.Time       `json:"startedAt"`
	DurationMs int             `json:"durationMs"`
	Requests   int             `json:"requests"`
	Passed     int             `json:"passed"`
	Failed     int             `json:"failed"`
	Errors     int             `json:"errors"`
	Results    json.RawMessage `json:"results"`
}

func (s *Server) monitorRuns(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `
		SELECT x.id::text, x.started_at, x.duration_ms, x.requests, x.passed, x.failed, x.errors, x.results
		FROM monitor_runs x JOIN monitors m ON m.id = x.monitor_id
		WHERE m.workspace_id=$1 AND m.id::text=$2 ORDER BY x.started_at DESC LIMIT 50`, principal(r).WorkspaceID, chi.URLParam(r, "id"))
	if err != nil {
		serverError(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[runOut])
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// runMonitorNow runs a monitor right away (the "Run" button).
func (s *Server) runMonitorNow(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !requireEdit(w, p) {
		return
	}
	run, err := s.runMonitor(r.Context(), chi.URLParam(r, "id"), p.WorkspaceID)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// monitorPrincipal acts as the person who made the monitor.
func (s *Server) monitorPrincipal(ctx context.Context, ws, userID string) (*Principal, error) {
	p := &Principal{Kind: "user", WorkspaceID: ws, Prefs: map[string]any{}}
	var prefs []byte
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.email, u.name, u.username, u.roles, u.groups, u.prefs, m.role
		FROM users u JOIN members m ON m.user_id = u.id AND m.workspace_id = $1
		WHERE u.id = $2 AND u.disabled_at IS NULL`, ws, userID).Scan(&p.UserID, &p.Email, &p.Name, &p.Username, &p.Roles, &p.Groups, &prefs, &p.MemberRole)
	if err != nil {
		return nil, fmt.Errorf("the monitor's creator can no longer use this workspace")
	}
	_ = json.Unmarshal(prefs, &p.Prefs)
	return p, nil
}

func (s *Server) runMonitor(ctx context.Context, id, ws string) (*runOut, error) {
	var name, target, notify string
	var envID, createdBy *string
	err := s.db.QueryRow(ctx, `SELECT name, target, environment_id::text, notify, created_by FROM monitors WHERE id::text=$1 AND workspace_id=$2`, id, ws).
		Scan(&name, &target, &envID, &notify, &createdBy)
	if err != nil {
		return nil, fmt.Errorf("monitor not found")
	}
	started := time.Now()
	var results []RunResult
	var runErr error
	if createdBy == nil {
		runErr = fmt.Errorf("the monitor's creator was deleted; recreate the monitor")
	} else if p, err := s.monitorPrincipal(ctx, ws, *createdBy); err != nil {
		runErr = err
	} else {
		env := ""
		if envID != nil {
			env = *envID
		}
		results, runErr = s.runCollection(ctx, p, target, env, nil)
	}
	run := &runOut{StartedAt: started, DurationMs: int(time.Since(started).Milliseconds()), Requests: len(results)}
	if runErr != nil {
		run.Errors = 1
		results = []RunResult{{Name: name, Error: runErr.Error()}}
	}
	for _, r := range results {
		if r.Error != "" || r.Status == 0 {
			run.Errors++
		}
		for _, t := range r.Tests {
			if t.Pass {
				run.Passed++
			} else {
				run.Failed++
			}
		}
	}
	run.Results, _ = json.Marshal(results)
	if err := s.db.QueryRow(ctx, `
		INSERT INTO monitor_runs (monitor_id, started_at, duration_ms, requests, passed, failed, errors, results)
		VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`,
		id, run.StartedAt, run.DurationMs, run.Requests, run.Passed, run.Failed, run.Errors, run.Results).Scan(&run.ID); err != nil {
		return nil, err
	}
	_, _ = s.db.Exec(ctx, `DELETE FROM monitor_runs WHERE monitor_id=$1::uuid AND id NOT IN (SELECT id FROM monitor_runs WHERE monitor_id=$1::uuid ORDER BY started_at DESC LIMIT 500)`, id)
	if (run.Failed > 0 || run.Errors > 0) && strings.TrimSpace(notify) != "" && s.jobs != nil {
		var lines []string
		for _, r := range results {
			if r.Error != "" {
				lines = append(lines, fmt.Sprintf("✗ %s: %s", r.Name, r.Error))
			}
			for _, t := range r.Tests {
				if !t.Pass {
					lines = append(lines, fmt.Sprintf("✗ %s › %s: %s", r.Name, t.Name, t.Error))
				}
			}
		}
		for _, to := range strings.FieldsFunc(notify, func(r rune) bool { return r == ',' || r == ' ' }) {
			_, _ = s.jobs.Insert(ctx, jobs.EmailArgs{
				To:      to,
				Subject: fmt.Sprintf("Requara monitor %q failed", name),
				Body: fmt.Sprintf("The monitor %q ran at %s: %d tests passed, %d failed, %d errors.\n\n%s\n\n%s/monitors\n",
					name, started.UTC().Format(time.RFC1123), run.Passed, run.Failed, run.Errors, strings.Join(lines, "\n"), s.cfg.FrontendURL),
			}, nil)
		}
	}
	return run, nil
}

// RunDueMonitors runs monitors whose time has come. main calls it every minute;
// with several servers, each monitor runs once (SKIP LOCKED).
func (s *Server) RunDueMonitors(ctx context.Context) {
	for {
		var id, ws string
		err := s.db.QueryRow(ctx, `
			UPDATE monitors SET next_run_at = now() + make_interval(mins => interval_min)
			WHERE id = (SELECT id FROM monitors WHERE enabled AND next_run_at <= now() ORDER BY next_run_at LIMIT 1 FOR UPDATE SKIP LOCKED)
			RETURNING id::text, workspace_id::text`).Scan(&id, &ws)
		if err != nil {
			if !notFound(err) {
				slog.Error("monitors", "err", err)
			}
			return
		}
		run, err := s.runMonitor(ctx, id, ws)
		if err != nil {
			slog.Error("monitor run failed", "monitor", id, "err", err)
			continue
		}
		slog.Info("monitor ran", "monitor", id, "passed", run.Passed, "failed", run.Failed, "errors", run.Errors)
	}
}

// runCollectionAPI runs a whole collection on the server, for the command
// line (requara-cli run) and CI: {environmentId, iterations, data: [{…}], delayMs}.
func (s *Server) runCollectionAPI(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	var in struct {
		EnvironmentID string              `json:"environmentId"`
		Iterations    int                 `json:"iterations"`
		Data          []map[string]string `json:"data"`
		DelayMs       int                 `json:"delayMs"`
	}
	if r.ContentLength != 0 && !readJSON(w, r, &in) {
		return
	}
	n := in.Iterations
	if len(in.Data) > 0 && n == 0 {
		n = len(in.Data)
	}
	if n <= 0 {
		n = 1
	}
	if n > 1000 || in.DelayMs < 0 || in.DelayMs > 60000 {
		httpError(w, http.StatusBadRequest, "Up to 1000 iterations and a delay of up to 60 s")
		return
	}
	target := chi.URLParam(r, "id")
	started := time.Now()
	type iteration struct {
		Iteration int         `json:"iteration"`
		Data      any         `json:"data,omitempty"`
		Results   []RunResult `json:"results"`
	}
	out := []iteration{}
	passed, failed, errs := 0, 0, 0
	for i := 0; i < n && r.Context().Err() == nil; i++ {
		var row map[string]string
		if len(in.Data) > 0 {
			row = in.Data[i%len(in.Data)]
		}
		res, err := s.runCollection(r.Context(), p, target, in.EnvironmentID, row)
		if err != nil {
			serverError(w, err)
			return
		}
		for _, x := range res {
			if x.Error != "" || x.Status == 0 {
				errs++
			}
			for _, t := range x.Tests {
				if t.Pass {
					passed++
				} else {
					failed++
				}
			}
		}
		out = append(out, iteration{Iteration: i + 1, Data: row, Results: res})
		if in.DelayMs > 0 && i+1 < n {
			time.Sleep(time.Duration(in.DelayMs) * time.Millisecond)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"iterations": out, "passed": passed, "failed": failed, "errors": errs, "durationMs": time.Since(started).Milliseconds(),
	})
}
