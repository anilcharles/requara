package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// auditEntry is one event in the audit log. The actor comes from the
// principal unless ActorKind is set (sign-ins, SCIM, the system).
type auditEntry struct {
	Action      string // area.verb: auth.signin, user.disabled, workspace.created, …
	Outcome     string // success (default) | failure
	ActorKind   string
	ActorID     string
	ActorLabel  string
	TargetType  string
	TargetID    string
	TargetLabel string
	WorkspaceID string
	Details     map[string]any
}

// audit records an event. It never fails the request: a write error is logged.
func (s *Server) audit(ctx context.Context, r *http.Request, p *Principal, e auditEntry) {
	if e.Outcome == "" {
		e.Outcome = "success"
	}
	if e.ActorKind == "" && p != nil {
		switch p.Kind {
		case "token":
			e.ActorKind, e.ActorID, e.ActorLabel = "token", p.TokenID, p.TokenName
		default:
			e.ActorKind, e.ActorID, e.ActorLabel = "user", p.UserID, p.Email
		}
	}
	if e.ActorKind == "" {
		e.ActorKind = "system"
	}
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	details, _ := json.Marshal(e.Details)
	var ws *string
	if e.WorkspaceID != "" {
		ws = &e.WorkspaceID
	}
	ip, ua := "", ""
	if r != nil {
		ip, ua = r.RemoteAddr, r.UserAgent()
		if host, _, err := net.SplitHostPort(ip); err == nil {
			ip = host
		}
	}
	if _, err := s.db.Exec(context.WithoutCancel(ctx), `
		INSERT INTO audit_events (actor_kind, actor_id, actor_label, action, outcome, target_type, target_id, target_label, workspace_id, ip, user_agent, details)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		e.ActorKind, e.ActorID, e.ActorLabel, e.Action, e.Outcome, e.TargetType, e.TargetID, e.TargetLabel, ws, ip, ua, details); err != nil {
		slog.Error("audit log write failed", "action", e.Action, "err", err)
	}
}

type auditEvent struct {
	ID          int64          `json:"id"`
	At          time.Time      `json:"at"`
	ActorKind   string         `json:"actorKind"`
	ActorID     string         `json:"actorId"`
	ActorLabel  string         `json:"actorLabel"`
	Action      string         `json:"action"`
	Outcome     string         `json:"outcome"`
	TargetType  string         `json:"targetType"`
	TargetID    string         `json:"targetId"`
	TargetLabel string         `json:"targetLabel"`
	WorkspaceID *string        `json:"workspaceId"`
	IP          string         `json:"ip"`
	UserAgent   string         `json:"userAgent"`
	Details     map[string]any `json:"details"`
}

// auditQuery turns ?action=&actor=&outcome=&q=&from=&to=&before= into SQL.
func auditQuery(r *http.Request) (string, []any) {
	q := r.URL.Query()
	where := []string{"true"}
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if v := q.Get("action"); v != "" {
		if strings.HasSuffix(v, ".") {
			where = append(where, "action LIKE "+arg(v+"%")) // a whole area: "auth."
		} else {
			where = append(where, "action = "+arg(v))
		}
	}
	if v := q.Get("outcome"); v == "success" || v == "failure" {
		where = append(where, "outcome = "+arg(v))
	}
	if v := q.Get("actor"); v != "" {
		where = append(where, "(actor_id = "+arg(v)+" OR actor_label ILIKE "+arg("%"+v+"%")+")")
	}
	if v := q.Get("q"); v != "" {
		n := arg("%" + v + "%")
		where = append(where, "(actor_label ILIKE "+n+" OR target_label ILIKE "+n+" OR action ILIKE "+n+" OR details::text ILIKE "+n+")")
	}
	if t, err := time.Parse(time.RFC3339, q.Get("from")); err == nil {
		where = append(where, "at >= "+arg(t))
	}
	if t, err := time.Parse(time.RFC3339, q.Get("to")); err == nil {
		where = append(where, "at < "+arg(t))
	}
	if v, err := strconv.ParseInt(q.Get("before"), 10, 64); err == nil {
		where = append(where, "id < "+arg(v))
	}
	return strings.Join(where, " AND "), args
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	where, args := auditQuery(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT id, at, actor_kind, actor_id, actor_label, action, outcome, target_type, target_id, target_label, workspace_id, ip, user_agent, details
		FROM audit_events WHERE `+where+` ORDER BY id DESC LIMIT `+strconv.Itoa(limit+1), args...)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []auditEvent{}
	for rows.Next() {
		var e auditEvent
		if err := rows.Scan(&e.ID, &e.At, &e.ActorKind, &e.ActorID, &e.ActorLabel, &e.Action, &e.Outcome, &e.TargetType,
			&e.TargetID, &e.TargetLabel, &e.WorkspaceID, &e.IP, &e.UserAgent, &e.Details); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, e)
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "more": more, "retentionDays": s.cfg.AuditRetentionDays})
}

// exportAudit streams the filtered log as CSV.
func (s *Server) exportAudit(w http.ResponseWriter, r *http.Request) {
	where, args := auditQuery(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT id, at, actor_kind, actor_id, actor_label, action, outcome, target_type, target_id, target_label,
		       COALESCE(workspace_id::text, ''), ip, user_agent, details::text
		FROM audit_events WHERE `+where+` ORDER BY id DESC LIMIT 100000`, args...)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="requara-audit-`+time.Now().UTC().Format("20060102-150405")+`.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "time", "actor_kind", "actor_id", "actor", "action", "outcome", "target_type", "target_id", "target", "workspace_id", "ip", "user_agent", "details"})
	for rows.Next() {
		var id int64
		var at time.Time
		f := make([]string, 12)
		if err := rows.Scan(&id, &at, &f[0], &f[1], &f[2], &f[3], &f[4], &f[5], &f[6], &f[7], &f[8], &f[9], &f[10], &f[11]); err != nil {
			slog.Error("audit export", "err", err)
			break
		}
		rec := append([]string{strconv.FormatInt(id, 10), at.UTC().Format(time.RFC3339)}, f...)
		for i, v := range rec {
			rec[i] = csvSafe(v)
		}
		_ = cw.Write(rec)
	}
	cw.Flush()
	s.audit(r.Context(), r, principal(r), auditEntry{Action: "audit.exported", Details: map[string]any{"filter": r.URL.RawQuery}})
}

// csvSafe stops spreadsheet apps from running a cell as a formula.
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

// Prune deletes audit events older than the retention period and expired
// sign-in state. main runs it daily.
func (s *Server) Prune(ctx context.Context) {
	if s.cfg.AuditRetentionDays > 0 {
		tag, err := s.db.Exec(ctx, `DELETE FROM audit_events WHERE at < now() - make_interval(days => $1)`, s.cfg.AuditRetentionDays)
		if err != nil {
			slog.Error("audit prune", "err", err)
		} else if tag.RowsAffected() > 0 {
			slog.Info("audit log pruned", "deleted", tag.RowsAffected(), "retention_days", s.cfg.AuditRetentionDays)
		}
	}
	_, _ = s.db.Exec(ctx, `DELETE FROM auth_states WHERE expires_at < now()`)
	_, _ = s.db.Exec(ctx, `DELETE FROM email_logins WHERE expires_at < now() - interval '1 day'`)
	_, _ = s.db.Exec(ctx, `DELETE FROM desktop_codes WHERE expires_at < now()`)
	_, _ = s.db.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
}
