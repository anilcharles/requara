package api

// The instance admin API (/api/admin/*): users, invites, workspaces,
// sign-in settings, admin tokens, the audit log and runtime settings.
// Instance admins come from REQUARA_ADMIN_EMAILS / REQUARA_ADMIN_EMAILS_FILE
// on the server; nothing here can make someone an admin.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/anilcharles/requara/server/api/internal/jobs"
	"github.com/jackc/pgx/v5"
)

// ----- overview -----

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var c struct {
		Users        int `json:"users"`
		ActiveUsers  int `json:"activeUsers30d"`
		Deactivated  int `json:"deactivated"`
		Admins       int `json:"admins"`
		Workspaces   int `json:"workspaces"`
		APIs         int `json:"apis"`
		Requests     int `json:"requests"`
		Sends24h     int `json:"sends24h"`
		Invites      int `json:"pendingInvites"`
		Sessions     int `json:"activeSessions"`
		SignIns24h   int `json:"signIns24h"`
		FailedLogins int `json:"failedSignIns24h"`
	}
	err := s.db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM users),
		(SELECT count(*) FROM users WHERE last_login_at > now() - interval '30 days' AND disabled_at IS NULL),
		(SELECT count(*) FROM users WHERE disabled_at IS NOT NULL),
		(SELECT count(*) FROM users WHERE is_admin),
		(SELECT count(*) FROM workspaces),
		(SELECT count(*) FROM apis),
		(SELECT count(*) FROM requests),
		(SELECT count(*) FROM history WHERE created_at > now() - interval '24 hours'),
		(SELECT count(*) FROM invites),
		(SELECT count(*) FROM sessions WHERE expires_at > now()),
		(SELECT count(*) FROM audit_events WHERE action = 'auth.signin' AND outcome = 'success' AND at > now() - interval '24 hours'),
		(SELECT count(*) FROM audit_events WHERE action = 'auth.signin' AND outcome = 'failure' AND at > now() - interval '24 hours')`).
		Scan(&c.Users, &c.ActiveUsers, &c.Deactivated, &c.Admins, &c.Workspaces, &c.APIs, &c.Requests, &c.Sends24h,
			&c.Invites, &c.Sessions, &c.SignIns24h, &c.FailedLogins)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"counts": c, "server": s.serverInfo()})
}

// serverInfo is the configuration admins can see but only change on the
// server (env, secrets): sign-in, who may join, who the admins are.
func (s *Server) serverInfo() map[string]any {
	type prov struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Type       string `json:"type"`
		Public     bool   `json:"public"`
		TrustEmail bool   `json:"trustEmail"`
		Issuer     string `json:"issuer,omitempty"`
		Orgs       string `json:"allowedOrgs,omitempty"`
	}
	var ps []prov
	for _, p := range s.cfg.Providers {
		ps = append(ps, prov{ID: p.ID, Name: p.Name, Type: p.Type, Public: p.Public(), TrustEmail: p.TrustEmail,
			Issuer: p.Issuer, Orgs: strings.Join(p.AllowedOrgs, ", ")})
	}
	return map[string]any{
		"version":            Version,
		"org":                s.cfg.OrgName,
		"url":                s.cfg.FrontendURL,
		"providers":          ps,
		"saml":               s.signin.SAMLProviders(),
		"oauthCallback":      s.cfg.FrontendURL + "/auth/callback",
		"allowedDomains":     s.cfg.AllowedDomains,
		"admins":             s.cfg.Admins,
		"adminSources":       s.cfg.AdminSources,
		"adminGroups":        s.cfg.AdminGroups,
		"publisherGroups":    s.cfg.PublisherGroups,
		"smtp":               s.cfg.SMTPAddr != "",
		"auditRetentionDays": s.cfg.AuditRetentionDays,
		"scimUrl":            s.cfg.FrontendURL + "/scim/v2",
	}
}

// ----- users -----

type adminUser struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Email         string     `json:"email"`
	Username      string     `json:"username"`
	Provider      string     `json:"provider"`
	EmailVerified bool       `json:"emailVerified"`
	IsAdmin       bool       `json:"isAdmin"`
	DisabledAt    *time.Time `json:"disabledAt"`
	CreatedAt     time.Time  `json:"createdAt"`
	LastLoginAt   time.Time  `json:"lastLoginAt"`
	Workspaces    int        `json:"workspaces"`
	Sessions      int        `json:"sessions"`
	Directory     *bool      `json:"directoryActive"` // SCIM: nil = not provisioned
}

const adminUserCols = `u.id, u.name, u.email, u.username, u.provider, u.email_verified, u.is_admin, u.disabled_at, u.created_at, u.last_login_at,
	(SELECT count(*) FROM members m WHERE m.user_id = u.id)::int,
	(SELECT count(*) FROM sessions x WHERE x.user_id = u.id AND x.expires_at > now())::int,
	(SELECT d.active FROM directory_users d WHERE lower(d.email) = lower(u.email) ORDER BY d.active DESC LIMIT 1)`

func scanAdminUser(row pgx.CollectableRow) (adminUser, error) {
	var u adminUser
	err := row.Scan(&u.ID, &u.Name, &u.Email, &u.Username, &u.Provider, &u.EmailVerified, &u.IsAdmin, &u.DisabledAt,
		&u.CreatedAt, &u.LastLoginAt, &u.Workspaces, &u.Sessions, &u.Directory)
	return u, err
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	where := "true"
	switch q.Get("status") {
	case "active":
		where = "u.disabled_at IS NULL"
	case "deactivated":
		where = "u.disabled_at IS NOT NULL"
	case "admins":
		where = "u.is_admin"
	}
	rows, err := s.db.Query(r.Context(), `SELECT `+adminUserCols+` FROM users u
		WHERE `+where+` AND ($1 = '' OR u.name ILIKE '%'||$1||'%' OR u.email ILIKE '%'||$1||'%')
		ORDER BY u.is_admin DESC, lower(u.name) LIMIT 1000`, strings.TrimSpace(q.Get("q")))
	if err != nil {
		serverError(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, scanAdminUser)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.db.Query(ctx, `SELECT `+adminUserCols+` FROM users u WHERE u.id = $1`, urlParam(r, "id"))
	if err != nil {
		serverError(w, err)
		return
	}
	u, err := pgx.CollectExactlyOneRow(rows, scanAdminUser)
	if err != nil {
		httpError(w, http.StatusNotFound, "No such user")
		return
	}
	type membership struct {
		WorkspaceID string `json:"workspaceId"`
		Name        string `json:"name"`
		Role        string `json:"role"`
	}
	mrows, _ := s.db.Query(ctx, `SELECT w.id::text, w.name, m.role FROM members m JOIN workspaces w ON w.id = m.workspace_id WHERE m.user_id = $1 ORDER BY w.name`, u.ID)
	ms, _ := pgx.CollectRows(mrows, pgx.RowToStructByPos[membership])
	type sess struct {
		Device   string    `json:"device"`
		IP       string    `json:"ip"`
		Created  time.Time `json:"createdAt"`
		LastSeen time.Time `json:"lastSeenAt"`
	}
	srows, _ := s.db.Query(ctx, `SELECT user_agent, ip, created_at, last_seen_at FROM sessions WHERE user_id = $1 AND expires_at > now() ORDER BY last_seen_at DESC`, u.ID)
	ss, _ := pgx.CollectRows(srows, func(row pgx.CollectableRow) (sess, error) {
		var x sess
		var ua string
		err := row.Scan(&ua, &x.IP, &x.Created, &x.LastSeen)
		x.Device = device(ua)
		if strings.HasPrefix(ua, "Requara Desktop") {
			x.Device = "Requara Desktop"
		}
		return x, err
	})
	if ms == nil {
		ms = []membership{}
	}
	if ss == nil {
		ss = []sess{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "memberships": ms, "sessions": ss})
}

// userTarget loads the user an admin action is about. Admins are managed on
// the server, so they can't be deactivated or deleted from here.
func (s *Server) userTarget(w http.ResponseWriter, r *http.Request, allowAdmin bool) (id, email string, ok bool) {
	var isAdmin bool
	err := s.db.QueryRow(r.Context(), `SELECT id, email, is_admin FROM users WHERE id = $1`, urlParam(r, "id")).Scan(&id, &email, &isAdmin)
	if err != nil {
		httpError(w, http.StatusNotFound, "No such user")
		return "", "", false
	}
	if isAdmin && !allowAdmin {
		httpError(w, http.StatusBadRequest, email+" is an instance admin. Remove them from REQUARA_ADMIN_EMAILS on the server first.")
		return "", "", false
	}
	return id, email, true
}

func (s *Server) adminDeactivateUser(w http.ResponseWriter, r *http.Request) {
	id, email, ok := s.userTarget(w, r, false)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, err := s.db.Exec(ctx, `UPDATE users SET disabled_at = COALESCE(disabled_at, now()) WHERE id = $1`, id); err != nil {
		serverError(w, err)
		return
	}
	_, _ = s.db.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id)
	s.audit(ctx, r, principal(r), auditEntry{Action: "user.deactivated", TargetType: "user", TargetID: id, TargetLabel: email})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminReactivateUser(w http.ResponseWriter, r *http.Request) {
	id, email, ok := s.userTarget(w, r, true)
	if !ok {
		return
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE users SET disabled_at = NULL WHERE id = $1`, id); err != nil {
		serverError(w, err)
		return
	}
	s.audit(r.Context(), r, principal(r), auditEntry{Action: "user.reactivated", TargetType: "user", TargetID: id, TargetLabel: email})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminSignOutUser(w http.ResponseWriter, r *http.Request) {
	id, email, ok := s.userTarget(w, r, true)
	if !ok {
		return
	}
	tag, err := s.db.Exec(r.Context(), `DELETE FROM sessions WHERE user_id = $1`, id)
	if err != nil {
		serverError(w, err)
		return
	}
	s.audit(r.Context(), r, principal(r), auditEntry{Action: "user.signed_out", TargetType: "user", TargetID: id, TargetLabel: email,
		Details: map[string]any{"sessions": tag.RowsAffected()}})
	writeJSON(w, http.StatusOK, map[string]int64{"signedOut": tag.RowsAffected()})
}

// adminDeleteUser removes the account, its sessions, keys and history.
// Workspace content they created stays.
func (s *Server) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, email, ok := s.userTarget(w, r, false)
	if !ok {
		return
	}
	if _, err := s.db.Exec(r.Context(), `DELETE FROM users WHERE id = $1`, id); err != nil {
		serverError(w, err)
		return
	}
	s.audit(r.Context(), r, principal(r), auditEntry{Action: "user.deleted", TargetType: "user", TargetID: id, TargetLabel: email})
	w.WriteHeader(http.StatusNoContent)
}

// ----- invites -----

type adminInvite struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	Role        string    `json:"role"`
	WorkspaceID string    `json:"workspaceId"`
	Workspace   string    `json:"workspace"`
	InvitedBy   string    `json:"invitedBy"`
	CreatedAt   time.Time `json:"createdAt"`
}

func (s *Server) adminInvites(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `
		SELECT i.id::text, i.email, i.role, w.id::text, w.name, COALESCE(u.email, ''), i.created_at
		FROM invites i JOIN workspaces w ON w.id = i.workspace_id LEFT JOIN users u ON u.id = i.invited_by
		ORDER BY i.created_at DESC`)
	if err != nil {
		serverError(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[adminInvite])
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// adminInvite invites someone to a workspace, emailing them when SMTP is set
// up. The sign-in link is returned so it can be shared another way.
func (s *Server) adminInvite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Emails      []string `json:"emails"`
		Email       string   `json:"email"`
		WorkspaceID string   `json:"workspaceId"`
		Role        string   `json:"role"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Email != "" {
		in.Emails = append(in.Emails, in.Email)
	}
	if in.Role == "" {
		in.Role = "editor"
	}
	if !slices.Contains([]string{"owner", "editor", "viewer"}, in.Role) {
		httpError(w, http.StatusBadRequest, "Role must be owner, editor or viewer")
		return
	}
	var emails []string
	for _, e := range in.Emails {
		for _, one := range strings.FieldsFunc(e, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == ';' }) {
			one = strings.ToLower(strings.TrimSpace(one))
			if at := strings.Index(one, "@"); at <= 0 || at == len(one)-1 {
				httpError(w, http.StatusBadRequest, one+" is not an email address")
				return
			}
			if !slices.Contains(emails, one) {
				emails = append(emails, one)
			}
		}
	}
	if len(emails) == 0 {
		httpError(w, http.StatusBadRequest, "Enter at least one email address")
		return
	}
	ctx := r.Context()
	var wsName string
	if err := s.db.QueryRow(ctx, `SELECT name FROM workspaces WHERE id::text = $1`, in.WorkspaceID).Scan(&wsName); err != nil {
		httpError(w, http.StatusBadRequest, "Pick a workspace")
		return
	}
	p := principal(r)
	var by *string
	if p.Kind == "user" {
		by = &p.UserID
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	link := s.cfg.FrontendURL + "/signin"
	for _, email := range emails {
		if _, err := tx.Exec(ctx, `
			INSERT INTO invites (workspace_id, email, role, invited_by) VALUES ($1,$2,$3,$4)
			ON CONFLICT (workspace_id, email) DO UPDATE SET role = $3`, in.WorkspaceID, email, in.Role, by); err != nil {
			serverError(w, err)
			return
		}
		// Someone who already has an account joins right away.
		_, _ = tx.Exec(ctx, `
			INSERT INTO members (workspace_id, user_id, role)
			SELECT $1, id, $3 FROM users WHERE lower(email) = $2
			ON CONFLICT (workspace_id, user_id) DO NOTHING`, in.WorkspaceID, email, in.Role)
		if s.cfg.SMTPAddr != "" {
			if _, err := s.jobs.InsertTx(ctx, tx, jobs.EmailArgs{
				To: email, Subject: "You're invited to " + wsName + " on Requara",
				Body: "You've been invited to the " + wsName + " workspace of " + s.cfg.OrgName + " on Requara.\n\nSign in with " + email + ": " + link + "\n",
			}, nil); err != nil {
				serverError(w, err)
				return
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	for _, email := range emails {
		s.audit(ctx, r, p, auditEntry{Action: "invite.created", TargetType: "invite", TargetLabel: email, WorkspaceID: in.WorkspaceID,
			Details: map[string]any{"role": in.Role, "workspace": wsName}})
	}
	writeJSON(w, http.StatusCreated, map[string]any{"invited": emails, "link": link, "emailed": s.cfg.SMTPAddr != ""})
}

func (s *Server) adminRevokeInvite(w http.ResponseWriter, r *http.Request) {
	var email, ws string
	err := s.db.QueryRow(r.Context(), `DELETE FROM invites WHERE id::text = $1 RETURNING email, workspace_id::text`, urlParam(r, "id")).Scan(&email, &ws)
	if err != nil {
		httpError(w, http.StatusNotFound, "No such invite")
		return
	}
	s.audit(r.Context(), r, principal(r), auditEntry{Action: "invite.revoked", TargetType: "invite", TargetLabel: email, WorkspaceID: ws})
	w.WriteHeader(http.StatusNoContent)
}

// ----- workspaces -----

type adminWorkspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	AutoJoin  bool      `json:"autoJoin"`
	Members   int       `json:"members"`
	Owners    int       `json:"owners"`
	APIs      int       `json:"apis"`
	Requests  int       `json:"requests"`
	Invites   int       `json:"invites"`
	CreatedAt time.Time `json:"createdAt"`
}

func (s *Server) adminWorkspaces(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `
		SELECT w.id::text, w.name, w.slug, w.auto_join,
		       (SELECT count(*) FROM members m WHERE m.workspace_id = w.id)::int,
		       (SELECT count(*) FROM members m WHERE m.workspace_id = w.id AND m.role = 'owner')::int,
		       (SELECT count(*) FROM apis a WHERE a.workspace_id = w.id)::int,
		       (SELECT count(*) FROM requests q WHERE q.workspace_id = w.id)::int,
		       (SELECT count(*) FROM invites i WHERE i.workspace_id = w.id)::int,
		       w.created_at
		FROM workspaces w ORDER BY lower(w.name)`)
	if err != nil {
		serverError(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[adminWorkspace])
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// createWorkspace makes a workspace with a unique slug and, when owner is
// set, that user as its owner.
func (s *Server) createWorkspace(ctx context.Context, name string, autoJoin bool, owner string) (string, error) {
	base := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if base == "" {
		base = "workspace"
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO workspaces (name, slug, auto_join)
		SELECT $1, CASE WHEN n = 0 THEN $2 ELSE $2 || '-' || (n + 1) END, $3
		FROM (SELECT count(*) AS n FROM workspaces WHERE slug = $2 OR slug ~ ('^' || $2 || '-[0-9]+$')) c
		RETURNING id::text`, name, base, autoJoin).Scan(&id)
	if err != nil {
		return "", err
	}
	if owner != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO members (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, id, owner); err != nil {
			return "", err
		}
	}
	return id, tx.Commit(ctx)
}

func (s *Server) adminCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name       string `json:"name"`
		AutoJoin   bool   `json:"autoJoin"`
		OwnerEmail string `json:"ownerEmail"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 {
		httpError(w, http.StatusBadRequest, "Give the workspace a name (up to 80 characters)")
		return
	}
	ctx := r.Context()
	owner := ""
	if in.OwnerEmail != "" {
		if err := s.db.QueryRow(ctx, `SELECT id FROM users WHERE lower(email) = lower($1) AND disabled_at IS NULL ORDER BY last_login_at DESC LIMIT 1`, in.OwnerEmail).Scan(&owner); err != nil {
			httpError(w, http.StatusBadRequest, in.OwnerEmail+" hasn't signed in yet. Create the workspace, then invite them as owner.")
			return
		}
	}
	id, err := s.createWorkspace(ctx, in.Name, in.AutoJoin, owner)
	if err != nil {
		serverError(w, err)
		return
	}
	s.audit(ctx, r, principal(r), auditEntry{Action: "workspace.created", TargetType: "workspace", TargetID: id, TargetLabel: in.Name, WorkspaceID: id,
		Details: map[string]any{"autoJoin": in.AutoJoin, "owner": in.OwnerEmail}})
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) adminUpdateWorkspace(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     *string `json:"name"`
		AutoJoin *bool   `json:"autoJoin"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Name != nil {
		*in.Name = strings.TrimSpace(*in.Name)
		if *in.Name == "" || len(*in.Name) > 80 {
			httpError(w, http.StatusBadRequest, "Give the workspace a name (up to 80 characters)")
			return
		}
	}
	id := urlParam(r, "id")
	var name string
	err := s.db.QueryRow(r.Context(), `
		UPDATE workspaces SET name = COALESCE($2, name), auto_join = COALESCE($3, auto_join)
		WHERE id::text = $1 RETURNING name`, id, in.Name, in.AutoJoin).Scan(&name)
	if err != nil {
		httpError(w, http.StatusNotFound, "No such workspace")
		return
	}
	changes := map[string]any{}
	if in.Name != nil {
		changes["name"] = *in.Name
	}
	if in.AutoJoin != nil {
		changes["autoJoin"] = *in.AutoJoin
	}
	s.audit(r.Context(), r, principal(r), auditEntry{Action: "workspace.updated", TargetType: "workspace", TargetID: id, TargetLabel: name, WorkspaceID: id, Details: changes})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	id := urlParam(r, "id")
	var name string
	if err := s.db.QueryRow(r.Context(), `DELETE FROM workspaces WHERE id::text = $1 RETURNING name`, id).Scan(&name); err != nil {
		httpError(w, http.StatusNotFound, "No such workspace")
		return
	}
	s.audit(r.Context(), r, principal(r), auditEntry{Action: "workspace.deleted", TargetType: "workspace", TargetID: id, TargetLabel: name})
	w.WriteHeader(http.StatusNoContent)
}

type wsMember struct {
	UserID   string    `json:"userId"`
	Name     string    `json:"name"`
	Email    string    `json:"email"`
	Role     string    `json:"role"`
	Disabled bool      `json:"disabled"`
	Since    time.Time `json:"since"`
}

func (s *Server) adminWorkspaceMembers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `
		SELECT u.id, u.name, u.email, m.role, u.disabled_at IS NOT NULL, m.created_at
		FROM members m JOIN users u ON u.id = m.user_id WHERE m.workspace_id::text = $1
		ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'editor' THEN 1 ELSE 2 END, lower(u.name)`, urlParam(r, "id"))
	if err != nil {
		serverError(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[wsMember])
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// setMemberRole adds someone to a workspace or changes their role.
func (s *Server) setMemberRole(ctx context.Context, ws, userID, role string) error {
	if !slices.Contains([]string{"owner", "editor", "viewer"}, role) {
		return errBadRole
	}
	if role != "owner" {
		if err := s.keepAnOwner(ctx, ws, userID); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO members (workspace_id, user_id, role) VALUES ($1::uuid, $2, $3)
		ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = $3`, ws, userID, role)
	return err
}

var (
	errBadRole   = errors.New("Role must be owner, editor or viewer")
	errLastOwner = errors.New("A workspace needs at least one owner. Make someone else owner first.")
)

// keepAnOwner refuses to demote or remove a workspace's last owner.
func (s *Server) keepAnOwner(ctx context.Context, ws, userID string) error {
	var isOwner bool
	var owners int
	_ = s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE workspace_id::text = $1 AND user_id = $2 AND role = 'owner'),
		(SELECT count(*) FROM members WHERE workspace_id::text = $1 AND role = 'owner')`, ws, userID).Scan(&isOwner, &owners)
	if isOwner && owners <= 1 {
		return errLastOwner
	}
	return nil
}

func (s *Server) adminSetMember(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role string `json:"role"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ws, uid := urlParam(r, "id"), urlParam(r, "userId")
	var email string
	if err := s.db.QueryRow(r.Context(), `SELECT email FROM users WHERE id = $1`, uid).Scan(&email); err != nil {
		httpError(w, http.StatusNotFound, "No such user")
		return
	}
	if err := s.setMemberRole(r.Context(), ws, uid, in.Role); err != nil {
		if errors.Is(err, errBadRole) || errors.Is(err, errLastOwner) {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		serverError(w, err)
		return
	}
	s.audit(r.Context(), r, principal(r), auditEntry{Action: "member.role_set", TargetType: "user", TargetID: uid, TargetLabel: email, WorkspaceID: ws,
		Details: map[string]any{"role": in.Role}})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminRemoveMember(w http.ResponseWriter, r *http.Request) {
	ws, uid := urlParam(r, "id"), urlParam(r, "userId")
	if err := s.keepAnOwner(r.Context(), ws, uid); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	var email string
	err := s.db.QueryRow(r.Context(), `
		DELETE FROM members m USING users u WHERE m.workspace_id::text = $1 AND m.user_id = $2 AND u.id = m.user_id
		RETURNING u.email`, ws, uid).Scan(&email)
	if err != nil {
		httpError(w, http.StatusNotFound, "Not a member")
		return
	}
	s.audit(r.Context(), r, principal(r), auditEntry{Action: "member.removed", TargetType: "user", TargetID: uid, TargetLabel: email, WorkspaceID: ws})
	w.WriteHeader(http.StatusNoContent)
}

// ----- admin tokens -----

type adminToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Last4      string     `json:"last4"`
	CreatedBy  string     `json:"createdBy"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
	CreatedAt  time.Time  `json:"createdAt"`
}

func (s *Server) adminTokens(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `
		SELECT t.id::text, t.name, t.prefix, t.last4, COALESCE(u.email, ''), t.expires_at, t.last_used_at, t.revoked_at, t.created_at
		FROM admin_tokens t LEFT JOIN users u ON u.id = t.created_by
		ORDER BY t.revoked_at NULLS FIRST, t.created_at DESC`)
	if err != nil {
		serverError(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[adminToken])
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// sessionAdminOnly keeps admin tokens from minting or revoking admin tokens.
func sessionAdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if principal(r).Kind != "user" {
			httpError(w, http.StatusForbidden, "Manage admin tokens from the admin console, signed in as an admin")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) adminCreateToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name          string `json:"name"`
		ExpiresInDays int    `json:"expiresInDays"` // 0 = never
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		httpError(w, http.StatusBadRequest, "Give the token a name, such as \"Okta SCIM\" or \"Terraform\"")
		return
	}
	if in.ExpiresInDays < 0 || in.ExpiresInDays > 3650 {
		httpError(w, http.StatusBadRequest, "Expiry must be between 1 and 3650 days, or never")
		return
	}
	var expires *time.Time
	if in.ExpiresInDays > 0 {
		expires = ptr(time.Now().AddDate(0, 0, in.ExpiresInDays))
	}
	p := principal(r)
	token := adminTokenPrefix + newKey()[len("rk_live_"):]
	var id string
	if err := s.db.QueryRow(r.Context(), `
		INSERT INTO admin_tokens (name, prefix, last4, hash, created_by, expires_at) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id::text`,
		in.Name, token[:7], token[len(token)-4:], hashToken(token), p.UserID, expires).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	s.audit(r.Context(), r, p, auditEntry{Action: "admin_token.created", TargetType: "admin_token", TargetID: id, TargetLabel: in.Name,
		Details: map[string]any{"expiresInDays": in.ExpiresInDays}})
	// The full token is shown exactly once; only its hash is stored.
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "token": token})
}

func (s *Server) adminRevokeToken(w http.ResponseWriter, r *http.Request) {
	var name string
	err := s.db.QueryRow(r.Context(), `UPDATE admin_tokens SET revoked_at = COALESCE(revoked_at, now()) WHERE id::text = $1 RETURNING name`,
		urlParam(r, "id")).Scan(&name)
	if err != nil {
		httpError(w, http.StatusNotFound, "No such token")
		return
	}
	s.audit(r.Context(), r, principal(r), auditEntry{Action: "admin_token.revoked", TargetType: "admin_token", TargetID: urlParam(r, "id"), TargetLabel: name})
	w.WriteHeader(http.StatusNoContent)
}

// ----- runtime settings -----

// Settings admins change from the console. Everything about sign-in and
// admins stays in the server's environment.
type instanceSettings struct {
	Banner struct {
		Enabled bool   `json:"enabled"`
		Message string `json:"message"`
		Level   string `json:"level"` // info | warning | danger
	} `json:"banner"`
	HistoryEnabled         bool `json:"historyEnabled"`
	AllowWorkspaceCreation bool `json:"allowWorkspaceCreation"`
}

func defaultSettings() instanceSettings {
	var st instanceSettings
	st.Banner.Level = "info"
	st.HistoryEnabled = true
	st.AllowWorkspaceCreation = true
	return st
}

func (s *Server) settings(ctx context.Context) instanceSettings {
	st := defaultSettings()
	var raw []byte
	if err := s.db.QueryRow(ctx, `SELECT value FROM instance_settings WHERE key = 'settings'`).Scan(&raw); err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	return st
}

func (s *Server) adminGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settings(r.Context()))
}

func (s *Server) adminPutSettings(w http.ResponseWriter, r *http.Request) {
	in := s.settings(r.Context())
	before := in
	if !readJSON(w, r, &in) {
		return
	}
	in.Banner.Message = strings.TrimSpace(in.Banner.Message)
	if len(in.Banner.Message) > 500 {
		httpError(w, http.StatusBadRequest, "Keep the banner under 500 characters")
		return
	}
	if !slices.Contains([]string{"info", "warning", "danger"}, in.Banner.Level) {
		in.Banner.Level = "info"
	}
	if in.Banner.Enabled && in.Banner.Message == "" {
		httpError(w, http.StatusBadRequest, "Write the banner message")
		return
	}
	raw, _ := json.Marshal(in)
	p := principal(r)
	by := p.Email
	if p.Kind == "token" {
		by = "token:" + p.TokenName
	}
	if _, err := s.db.Exec(r.Context(), `
		INSERT INTO instance_settings (key, value, updated_by, updated_at) VALUES ('settings', $1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = $1, updated_by = $2, updated_at = now()`, raw, by); err != nil {
		serverError(w, err)
		return
	}
	s.audit(r.Context(), r, p, auditEntry{Action: "settings.updated", TargetType: "settings", Details: map[string]any{"before": before, "after": in}})
	writeJSON(w, http.StatusOK, in)
}

// adminPurgeHistory deletes everyone's request history (and stored bodies).
func (s *Server) adminPurgeHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.db.Query(ctx, `DELETE FROM history RETURNING body_object`)
	if err != nil {
		serverError(w, err)
		return
	}
	var n int64
	var objects []string
	for rows.Next() {
		var obj string
		_ = rows.Scan(&obj)
		n++
		if obj != "" {
			objects = append(objects, obj)
		}
	}
	rows.Close()
	go func() {
		for _, o := range objects {
			_ = s.s3.Delete(context.WithoutCancel(ctx), o)
		}
	}()
	s.audit(ctx, r, principal(r), auditEntry{Action: "history.purged", Details: map[string]any{"entries": n}})
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": n})
}

// instance is what every signed-in client shows: the banner, and whether
// people may create workspaces.
func (s *Server) instance(w http.ResponseWriter, r *http.Request) {
	st := s.settings(r.Context())
	out := map[string]any{"org": s.cfg.OrgName, "allowWorkspaceCreation": st.AllowWorkspaceCreation || principal(r).IsAdmin, "historyEnabled": st.HistoryEnabled}
	if st.Banner.Enabled {
		out["banner"] = st.Banner
	}
	writeJSON(w, http.StatusOK, out)
}
