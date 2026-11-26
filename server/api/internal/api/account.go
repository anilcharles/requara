package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/anilcharles/requara/server/api/internal/jobs"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// ----- sign-in -----

// providers lists the ways to sign in, for the web sign-in page and the
// desktop app. It needs no sign-in.
func (s *Server) providers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"org": s.cfg.OrgName, "providers": s.signin.Providers()})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if err := s.signin.Begin(w, r, strings.HasPrefix(s.cfg.FrontendURL, "https://")); err != nil {
		http.Redirect(w, r, s.cfg.FrontendURL+"/signin?error="+urlQuery(err.Error()), http.StatusFound)
	}
}

// desktopTokenPrefix marks session tokens issued to the desktop app.
const desktopTokenPrefix = "sd_"

// desktopSessionTTL is how long a desktop sign-in lasts.
const desktopSessionTTL = 30 * 24 * time.Hour

// desktopToken trades a one-time desktop sign-in code for a session token.
// The app proves it started the sign-in with the PKCE verifier (S256).
func (s *Server) desktopToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code     string `json:"code"`
		Verifier string `json:"verifier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Code == "" || len(in.Verifier) < 43 {
		httpError(w, http.StatusBadRequest, "code and verifier are required")
		return
	}
	ctx := r.Context()
	var userID, idToken, challenge, provider string
	err := s.db.QueryRow(ctx, `
		DELETE FROM desktop_codes WHERE code_hash = $1 AND expires_at > now()
		RETURNING user_id, id_token, challenge, provider`, hashToken(in.Code)).Scan(&userID, &idToken, &challenge, &provider)
	if err != nil {
		httpError(w, http.StatusUnauthorized, "Sign-in code expired or already used. Sign in again.")
		return
	}
	sum := sha256.Sum256([]byte(in.Verifier))
	if subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) != 1 {
		httpError(w, http.StatusUnauthorized, "Sign-in verification failed. Sign in again.")
		return
	}
	token := desktopTokenPrefix + randomToken(32)
	expires := time.Now().Add(desktopSessionTTL)
	if _, err := s.db.Exec(ctx, `
		INSERT INTO sessions (id_hash, user_id, id_token, user_agent, ip, provider, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		hashToken(token), userID, idToken, "Requara Desktop · "+r.UserAgent(), r.RemoteAddr, provider, expires); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expiresAt": expires})
}

// version tells clients (the desktop app) what this server supports.
// It needs no sign-in, so the app can check before starting one.
func (s *Server) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name": "requara", "version": Version, "api": APIVersion,
		"features": []string{"desktop-login", "auth-providers", "email-login", "saml", "admin", "audit-log", "scim", "banner"},
		"org":      s.cfg.OrgName,
	})
}

// logout ends the Requara session and returns the provider's logout URL.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && strings.HasPrefix(bearer, desktopTokenPrefix) {
		var idToken, provider string
		var uid string
		_ = s.db.QueryRow(r.Context(), `DELETE FROM sessions WHERE id_hash = $1 RETURNING id_token, provider, user_id`, hashToken(bearer)).Scan(&idToken, &provider, &uid)
		if uid != "" {
			s.audit(r.Context(), r, nil, auditEntry{Action: "auth.signout", ActorKind: "user", ActorID: uid, Details: map[string]any{"client": "desktop"}})
		}
		writeJSON(w, http.StatusOK, map[string]string{"logoutUrl": s.signin.LogoutURL(provider, idToken, s.cfg.FrontendURL+"/signin")})
		return
	}
	if r.Header.Get("X-Requested-With") != "requara" {
		httpError(w, http.StatusForbidden, "Missing X-Requested-With header")
		return
	}
	var idToken, provider string
	if c, err := r.Cookie(sessionCookie); err == nil {
		var uid string
		_ = s.db.QueryRow(r.Context(), `DELETE FROM sessions WHERE id_hash = $1 RETURNING id_token, provider, user_id`, hashToken(c.Value)).Scan(&idToken, &provider, &uid)
		if uid != "" {
			s.audit(r.Context(), r, nil, auditEntry{Action: "auth.signout", ActorKind: "user", ActorID: uid, Details: map[string]any{"client": "web"}})
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, http.StatusOK, map[string]string{"logoutUrl": s.signin.LogoutURL(provider, idToken, s.cfg.FrontendURL+"/signin")})
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func urlQuery(s string) string {
	return strings.NewReplacer(" ", "+", "&", "%26", "#", "%23", "?", "%3F").Replace(s)
}

// ----- profile & preferences -----

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	var lastLogin time.Time
	var providerID string
	_ = s.db.QueryRow(r.Context(), `SELECT last_login_at, provider FROM users WHERE id=$1`, p.UserID).Scan(&lastLogin, &providerID)
	prov, _ := s.signin.Info(providerID)
	if prov.Name == "" {
		prov.Name = "your identity provider"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": p.UserID, "email": p.Email, "name": p.Name, "username": p.Username,
		"roles": p.Roles, "groups": p.Groups, "prefs": p.Prefs, "lastLoginAt": lastLogin,
		"workspaceId": p.WorkspaceID, "memberRole": p.MemberRole,
		"canEdit": p.CanEdit(), "canPublish": s.canPublish(p),
		"identityProvider": prov.Name,
		"accountUrl":       prov.AccountURL,
		"isAdmin":          p.IsAdmin,
	})
}

var prefKeys = map[string]bool{
	"theme": true, "density": true, "codeFont": true, "codeFontSize": true, "defaultEnvironmentId": true,
	"activeEnvironmentId": true, "sendWithCmdEnter": true, "saveResponses": true, "defaultTimeoutMs": true,
	"notifyPublished": true, "notifyMockStopped": true, "weeklyDigest": true, "workspaceId": true,
}

func (s *Server) updatePrefs(w http.ResponseWriter, r *http.Request) {
	var in map[string]any
	if !readJSON(w, r, &in) {
		return
	}
	for k := range in {
		if !prefKeys[k] {
			httpError(w, http.StatusBadRequest, "Unknown preference "+k)
			return
		}
	}
	raw, _ := json.Marshal(in)
	var out []byte
	if err := s.db.QueryRow(r.Context(), `UPDATE users SET prefs = prefs || $2::jsonb WHERE id=$1 RETURNING prefs`,
		principal(r).UserID, raw).Scan(&out); err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}

// ----- sessions -----

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT encode(id_hash,'hex'), user_agent, ip, created_at, last_seen_at, id_hash = $2
		FROM sessions WHERE user_id = $1 AND expires_at > now() ORDER BY last_seen_at DESC`, p.UserID, p.SessionHash)
	if err != nil {
		serverError(w, err)
		return
	}
	type sess struct {
		ID        string    `json:"id"`
		Device    string    `json:"device"`
		IP        string    `json:"ip"`
		CreatedAt time.Time `json:"createdAt"`
		LastSeen  time.Time `json:"lastSeenAt"`
		Current   bool      `json:"current"`
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (sess, error) {
		var x sess
		var ua string
		err := row.Scan(&x.ID, &ua, &x.IP, &x.CreatedAt, &x.LastSeen, &x.Current)
		x.Device = device(ua)
		return x, err
	})
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// device turns a User-Agent into "Chrome on macOS".
func device(ua string) string {
	browser := "Browser"
	switch {
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	case strings.Contains(ua, "curl/"):
		return "curl"
	}
	osName := "unknown OS"
	switch {
	case strings.Contains(ua, "iPhone"):
		osName = "iPhone"
	case strings.Contains(ua, "iPad"):
		osName = "iPad"
	case strings.Contains(ua, "Android"):
		osName = "Android"
	case strings.Contains(ua, "Mac OS X"):
		osName = "macOS"
	case strings.Contains(ua, "Windows"):
		osName = "Windows"
	case strings.Contains(ua, "Linux"):
		osName = "Linux"
	}
	return browser + " on " + osName
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if _, err := s.db.Exec(r.Context(), `DELETE FROM sessions WHERE user_id=$1 AND encode(id_hash,'hex')=$2`, p.UserID, chi.URLParam(r, "id")); err != nil {
		serverError(w, err)
		return
	}
	s.audit(r.Context(), r, p, auditEntry{Action: "session.revoked", TargetType: "user", TargetID: p.UserID, TargetLabel: p.Email})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteOtherSessions(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	tag, err := s.db.Exec(r.Context(), `DELETE FROM sessions WHERE user_id=$1 AND id_hash <> $2`, p.UserID, p.SessionHash)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"signedOut": tag.RowsAffected()})
}

// ----- API keys -----

var validScopes = []string{"collections:read", "collections:write", "runs:write", "metrics:read", "full access"}

type apiKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Last4      string     `json:"last4"`
	Scopes     []string   `json:"scopes"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
	CreatedAt  time.Time  `json:"createdAt"`
}

func (s *Server) listKeys(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `
		SELECT id, name, prefix, last4, scopes, expires_at, last_used_at, revoked_at, created_at
		FROM api_keys WHERE user_id=$1 ORDER BY revoked_at NULLS FIRST, created_at DESC`, principal(r).UserID)
	if err != nil {
		serverError(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[apiKey])
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func newKey() string {
	b := make([]byte, 32)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(base62))))
		b[i] = base62[n.Int64()]
	}
	return "rk_live_" + string(b)
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name          string   `json:"name"`
		Scopes        []string `json:"scopes"`
		ExpiresInDays int      `json:"expiresInDays"` // 0 = never
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		httpError(w, http.StatusBadRequest, "Give the key a name")
		return
	}
	if len(in.Scopes) == 0 {
		httpError(w, http.StatusBadRequest, "Pick at least one scope")
		return
	}
	for _, sc := range in.Scopes {
		if !slices.Contains(validScopes, sc) {
			httpError(w, http.StatusBadRequest, "Unknown scope "+sc)
			return
		}
	}
	var expires *time.Time
	if in.ExpiresInDays > 0 {
		expires = ptr(time.Now().AddDate(0, 0, in.ExpiresInDays))
	}
	key := newKey()
	var k apiKey
	err := s.db.QueryRow(r.Context(), `
		INSERT INTO api_keys (user_id, name, prefix, last4, hash, scopes, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING id, name, prefix, last4, scopes, expires_at, last_used_at, revoked_at, created_at`,
		principal(r).UserID, in.Name, key[:12], key[len(key)-4:], hashToken(key), in.Scopes, expires).
		Scan(&k.ID, &k.Name, &k.Prefix, &k.Last4, &k.Scopes, &k.ExpiresAt, &k.LastUsedAt, &k.RevokedAt, &k.CreatedAt)
	if err != nil {
		serverError(w, err)
		return
	}
	s.audit(r.Context(), r, principal(r), auditEntry{Action: "api_key.created", TargetType: "api_key", TargetID: k.ID, TargetLabel: k.Name,
		Details: map[string]any{"scopes": k.Scopes}})
	// The full key is returned exactly once; only its hash is stored.
	writeJSON(w, http.StatusCreated, map[string]any{"key": key, "apiKey": k})
}

func (s *Server) revokeKey(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	id := chi.URLParam(r, "id")
	// Revoking an active key marks it; deleting an already revoked/expired key removes it.
	tag, err := s.db.Exec(r.Context(), `
		UPDATE api_keys SET revoked_at = now()
		WHERE id::text=$1 AND user_id=$2 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())`, id, p.UserID)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.db.Exec(r.Context(), `DELETE FROM api_keys WHERE id::text=$1 AND user_id=$2`, id, p.UserID); err != nil {
			serverError(w, err)
			return
		}
	} else {
		s.audit(r.Context(), r, p, auditEntry{Action: "api_key.revoked", TargetType: "api_key", TargetID: id})
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- workspace, members, search -----

func (s *Server) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `
		SELECT w.id, w.name, m.role FROM members m JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.user_id=$1 ORDER BY w.name`, principal(r).UserID)
	if err != nil {
		serverError(w, err)
		return
	}
	type ws struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Role string `json:"role"`
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ws])
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT u.id, u.name, u.email, m.role, false FROM members m JOIN users u ON u.id = m.user_id WHERE m.workspace_id=$1
		UNION ALL
		SELECT '', '', email, role, true FROM invites WHERE workspace_id=$1
		ORDER BY 5, 2`, p.WorkspaceID)
	if err != nil {
		serverError(w, err)
		return
	}
	type member struct {
		UserID  string `json:"userId"`
		Name    string `json:"name"`
		Email   string `json:"email"`
		Role    string `json:"role"`
		Pending bool   `json:"pending"`
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[member])
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) invite(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p.MemberRole != "owner" {
		httpError(w, http.StatusForbidden, "Only workspace owners can invite people")
		return
	}
	var in struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if !strings.Contains(in.Email, "@") || !slices.Contains([]string{"owner", "editor", "viewer"}, in.Role) {
		httpError(w, http.StatusBadRequest, "Enter an email and pick Owner, Can edit or Can view")
		return
	}
	var wsName string
	_ = s.db.QueryRow(r.Context(), `SELECT name FROM workspaces WHERE id=$1`, p.WorkspaceID).Scan(&wsName)
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), `
		INSERT INTO invites (workspace_id, email, role, invited_by) VALUES ($1,$2,$3,$4)
		ON CONFLICT (workspace_id, email) DO UPDATE SET role=$3`, p.WorkspaceID, in.Email, in.Role, p.UserID); err != nil {
		serverError(w, err)
		return
	}
	if _, err := s.jobs.InsertTx(r.Context(), tx, jobs.EmailArgs{
		To: in.Email, Subject: p.Name + " invited you to " + wsName + " on Requara",
		Body: p.Name + " invited you to the " + wsName + " workspace on Requara.\n\nSign in with your work account: " + s.cfg.FrontendURL + "/signin\n",
	}, nil); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		serverError(w, err)
		return
	}
	s.audit(r.Context(), r, p, auditEntry{Action: "invite.created", TargetType: "invite", TargetLabel: in.Email, WorkspaceID: p.WorkspaceID,
		Details: map[string]any{"role": in.Role, "workspace": wsName}})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	q := "%" + strings.TrimSpace(r.URL.Query().Get("q")) + "%"
	type hit struct {
		Kind   string `json:"kind"`
		ID     string `json:"id"`
		Title  string `json:"title"`
		Meta   string `json:"meta"`
		Method string `json:"method"`
	}
	rows, err := s.db.Query(r.Context(), `
		(SELECT 'request', r.id::text, r.name, COALESCE(a.name,''), r.method
		   FROM requests r LEFT JOIN apis a ON a.id = r.api_id
		  WHERE r.workspace_id=$1 AND (r.name ILIKE $2 OR r.url ILIKE $2) ORDER BY r.name LIMIT 8)
		UNION ALL
		(SELECT 'api', id::text, name, status, '' FROM apis WHERE workspace_id=$1 AND name ILIKE $2 ORDER BY name LIMIT 5)`,
		p.WorkspaceID, q)
	if err != nil {
		serverError(w, err)
		return
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[hit])
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
