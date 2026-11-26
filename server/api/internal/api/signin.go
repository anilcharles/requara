package api

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/anilcharles/requara/server/api/internal/auth"
	"github.com/anilcharles/requara/server/api/internal/config"
	"github.com/anilcharles/requara/server/api/internal/jobs"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ----- server-side login state (SAML, email links) -----

// StateStore keeps login state in Postgres, so it works across replicas.
type StateStore struct{ DB *pgxpool.Pool }

func (st StateStore) Put(ctx context.Context, id string, state []byte, ttl time.Duration) error {
	_, err := st.DB.Exec(ctx, `INSERT INTO auth_states (id, state, expires_at) VALUES ($1, $2, $3)`, id, state, time.Now().Add(ttl))
	return err
}

func (st StateStore) Get(ctx context.Context, id string) ([]byte, error) {
	var b []byte
	err := st.DB.QueryRow(ctx, `SELECT state FROM auth_states WHERE id = $1 AND expires_at > now()`, id).Scan(&b)
	return b, err
}

func (st StateStore) Take(ctx context.Context, id string) ([]byte, error) {
	var b []byte
	err := st.DB.QueryRow(ctx, `DELETE FROM auth_states WHERE id = $1 AND expires_at > now() RETURNING state`, id).Scan(&b)
	return b, err
}

// ----- who may sign in -----

// joinDecision says whether someone who signed in may use this install.
//
//  1. A deactivated account stays out.
//  2. Instance admins (REQUARA_ADMIN_EMAILS, verified email) are always let in.
//  3. A SCIM directory entry decides when there is one: active lets them in,
//     deactivated keeps them out.
//  4. Otherwise: an allowed email domain, an invite, or an existing account.
func (s *Server) joinDecision(ctx context.Context, q pgx.Tx, subject, email string, verified bool) (bool, string, error) {
	var disabled bool
	err := q.QueryRow(ctx, `SELECT disabled_at IS NOT NULL FROM users WHERE id = $1`, subject).Scan(&disabled)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, "", err
	}
	if disabled {
		return false, "Your Requara account was deactivated. Ask an admin to reactivate it.", nil
	}
	if s.cfg.IsAdminEmail(email) {
		if verified {
			return true, "", nil
		}
		slog.Warn("admin email signed in without a verified email; not treated as admin", "subject", subject)
	}
	var active *bool
	err = q.QueryRow(ctx, `
		SELECT active FROM directory_users WHERE lower(email) = lower($1) OR lower(user_name) = lower($1)
		ORDER BY active DESC LIMIT 1`, email).Scan(&active)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, "", err
	}
	if active != nil {
		if *active {
			return true, "", nil
		}
		return false, "Your account was deactivated in your company directory.", nil
	}
	_, domain, _ := strings.Cut(strings.ToLower(email), "@")
	if slices.Contains(s.cfg.AllowedDomains, "*") || slices.Contains(s.cfg.AllowedDomains, domain) {
		return true, "", nil
	}
	var ok bool
	err = q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)
		    OR EXISTS (SELECT 1 FROM invites WHERE lower(email) = lower($2))`, subject, email).Scan(&ok)
	if !ok {
		return false, fmt.Sprintf("%s isn't part of %s on Requara. Ask an admin to invite you.", email, s.cfg.OrgName), err
	}
	return true, "", err
}

// ----- finishing a sign-in (every provider) -----

func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	res, err := s.signin.Finish(w, r)
	if err != nil {
		s.signInFailed(w, r, nil, err.Error())
		return
	}
	s.completeSignIn(w, r, res)
}

// signInFailed sends the browser back to where the sign-in started with the
// reason, and records the failure.
func (s *Server) signInFailed(w http.ResponseWriter, r *http.Request, res *auth.Result, msg string) {
	e := auditEntry{Action: "auth.signin", Outcome: "failure", ActorKind: "anonymous", Details: map[string]any{"reason": msg}}
	if res != nil {
		e.ActorID, e.ActorLabel = res.Claims.Subject, res.Claims.Email
		e.Details["provider"] = res.Provider
	}
	s.audit(r.Context(), r, nil, e)
	if res != nil && res.DesktopPort != 0 {
		http.Redirect(w, r, fmt.Sprintf("http://127.0.0.1:%d/callback?error=%s", res.DesktopPort, url.QueryEscape(msg)), http.StatusFound)
		return
	}
	http.Redirect(w, r, s.cfg.FrontendURL+"/signin?error="+urlQuery(msg), http.StatusFound)
}

func (s *Server) completeSignIn(w http.ResponseWriter, r *http.Request, res *auth.Result) {
	cl, idToken, redirect := res.Claims, res.IDToken, res.Redirect
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if ok, why, err := s.joinDecision(ctx, tx, cl.Subject, cl.Email, cl.EmailVerified); err != nil {
		serverError(w, err)
		return
	} else if !ok {
		s.signInFailed(w, r, res, why)
		return
	}
	roles, groups := cl.Roles, cl.Groups
	if roles == nil {
		roles = []string{}
	}
	if groups == nil {
		groups = []string{}
	}
	isAdmin := cl.EmailVerified && s.cfg.IsAdminEmail(cl.Email)
	if _, err := tx.Exec(ctx, `
		INSERT INTO users (id, username, email, name, roles, groups, provider, email_verified, is_admin) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (id) DO UPDATE SET username=$2, email=$3, name=$4, roles=$5, groups=$6, provider=$7,
		    email_verified=$8, is_admin=$9, last_login_at=now()`,
		cl.Subject, cl.Username, cl.Email, cl.Name, roles, groups, res.Provider, cl.EmailVerified, isAdmin); err != nil {
		serverError(w, err)
		return
	}
	// New people join every workspace marked "everyone joins"; admins
	// (REQUARA_ADMIN_GROUPS, matched against roles and groups) as owners.
	role := "editor"
	if auth.Matches(append(slices.Clone(roles), groups...), s.cfg.AdminGroups) {
		role = "owner"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO members (workspace_id, user_id, role)
		SELECT id, $1, $2 FROM workspaces WHERE auto_join
		ON CONFLICT (workspace_id, user_id) DO NOTHING`, cl.Subject, role); err != nil {
		serverError(w, err)
		return
	}
	_, _ = tx.Exec(ctx, `
		INSERT INTO members (workspace_id, user_id, role)
		SELECT i.workspace_id, $1, i.role FROM invites i WHERE lower(i.email) = lower($2)
		ON CONFLICT DO NOTHING`, cl.Subject, cl.Email)
	_, _ = tx.Exec(ctx, `DELETE FROM invites WHERE lower(email) = lower($1)`, cl.Email)

	signedIn := auditEntry{Action: "auth.signin", ActorKind: "user", ActorID: cl.Subject, ActorLabel: cl.Email,
		Details: map[string]any{"provider": res.Provider, "client": "web", "admin": isAdmin}}
	if res.DesktopPort != 0 {
		// Desktop sign-in: hand the app a one-time code on its loopback
		// address. It trades the code (plus the PKCE verifier) for a session.
		code := randomToken(32)
		if _, err := tx.Exec(ctx, `
			INSERT INTO desktop_codes (code_hash, user_id, id_token, challenge, provider, expires_at)
			VALUES ($1,$2,$3,$4,$5, now() + interval '2 minutes')`,
			hashToken(code), cl.Subject, idToken, res.DesktopChallenge, res.Provider); err != nil {
			serverError(w, err)
			return
		}
		if err := tx.Commit(ctx); err != nil {
			serverError(w, err)
			return
		}
		signedIn.Details["client"] = "desktop"
		s.audit(ctx, r, nil, signedIn)
		http.Redirect(w, r, fmt.Sprintf("http://127.0.0.1:%d/callback?code=%s", res.DesktopPort, code), http.StatusFound)
		return
	}

	token := randomToken(32)
	if _, err := tx.Exec(ctx, `
		INSERT INTO sessions (id_hash, user_id, id_token, user_agent, ip, provider, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6, now() + interval '12 hours')`,
		hashToken(token), cl.Subject, idToken, r.UserAgent(), r.RemoteAddr, res.Provider); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	s.audit(ctx, r, nil, signedIn)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true,
		Secure: strings.HasPrefix(s.cfg.FrontendURL, "https://"), SameSite: http.SameSiteLaxMode,
		MaxAge: int((12 * time.Hour).Seconds()),
	})
	http.Redirect(w, r, s.cfg.FrontendURL+redirect, http.StatusFound)
}

// ----- SAML -----

func (s *Server) samlMetadata(w http.ResponseWriter, r *http.Request) {
	md, err := s.signin.SAMLMetadata(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	out, err := xml.MarshalIndent(md, "", "  ")
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/samlmetadata+xml")
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(out)
}

func (s *Server) samlACS(w http.ResponseWriter, r *http.Request) {
	res, err := s.signin.FinishSAML(r, chi.URLParam(r, "id"))
	if err != nil {
		slog.Warn("SAML sign-in failed", "provider", chi.URLParam(r, "id"), "err", err)
		s.signInFailed(w, r, nil, err.Error())
		return
	}
	s.completeSignIn(w, r, res)
}

// ----- email links -----

const emailLinkTTL = 15 * time.Minute

// emailFlow tells the email sign-in page whether its flow is still valid.
func (s *Server) emailFlow(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if t := q.Get("token"); t != "" {
		// The confirm step: show which address the link is for.
		var email string
		err := s.db.QueryRow(r.Context(), `SELECT email FROM email_logins WHERE token_hash = $1 AND expires_at > now()`, hashToken(t)).Scan(&email)
		if err != nil {
			httpError(w, http.StatusGone, "This sign-in link expired or was already used. Request a new one.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"email": email, "org": s.cfg.OrgName})
		return
	}
	_, desktop, err := s.signin.EmailFlow(r.Context(), q.Get("flow"))
	if err != nil {
		httpError(w, http.StatusGone, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"desktop": desktop, "org": s.cfg.OrgName})
}

// emailStart sends a one-time sign-in link. It answers the same way whether
// or not the address may join, so it can't be used to find out who has an
// account; the link is only sent to people who may.
func (s *Server) emailStart(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Requested-With") != "requara" {
		httpError(w, http.StatusForbidden, "Missing X-Requested-With header")
		return
	}
	var in struct {
		Flow  string `json:"flow"`
		Email string `json:"email"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if at := strings.Index(email, "@"); at <= 0 || at == len(email)-1 || strings.ContainsAny(email, " \r\n<>") {
		httpError(w, http.StatusBadRequest, "Enter your email address")
		return
	}
	ctx := r.Context()
	provider, _, err := s.signin.EmailFlow(ctx, in.Flow)
	if err != nil {
		httpError(w, http.StatusGone, err.Error())
		return
	}
	sent := map[string]any{"sent": true, "expiresInMinutes": int(emailLinkTTL.Minutes())}
	var recent int
	_ = s.db.QueryRow(ctx, `SELECT count(*) FROM email_logins WHERE lower(email) = $1 AND created_at > now() - interval '10 minutes'`, email).Scan(&recent)
	if recent >= 5 {
		httpError(w, http.StatusTooManyRequests, "Too many sign-in links requested. Wait a few minutes and try again.")
		return
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	ok, why, err := s.joinDecision(ctx, tx, provider+":"+email, email, true)
	if err != nil {
		serverError(w, err)
		return
	}
	if !ok {
		s.audit(ctx, r, nil, auditEntry{Action: "auth.email_link", Outcome: "failure", ActorKind: "anonymous", ActorLabel: email, Details: map[string]any{"reason": why}})
		writeJSON(w, http.StatusOK, sent)
		return
	}
	token := randomToken(32)
	if _, err := tx.Exec(ctx, `INSERT INTO email_logins (token_hash, email, flow_id, expires_at) VALUES ($1,$2,$3,$4)`,
		hashToken(token), email, in.Flow, time.Now().Add(emailLinkTTL)); err != nil {
		serverError(w, err)
		return
	}
	link := s.cfg.FrontendURL + "/signin/email?token=" + token
	if _, err := s.jobs.InsertTx(ctx, tx, jobs.EmailArgs{
		To:      email,
		Subject: "Your Requara sign-in link",
		Body: "Sign in to " + s.cfg.OrgName + " on Requara:\n\n" + link + "\n\nThe link works once and expires in " +
			fmt.Sprint(int(emailLinkTTL.Minutes())) + " minutes. If you didn't ask for it, ignore this email.\n",
	}, nil); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	s.audit(ctx, r, nil, auditEntry{Action: "auth.email_link", ActorKind: "anonymous", ActorLabel: email})
	writeJSON(w, http.StatusOK, sent)
}

// emailVerify is the "Continue" button on the page the link opens. It is a
// plain form POST (so the sign-in can redirect to the app or the desktop
// app) and only accepted from Requara's own origin. Mail scanners that open
// links don't press it, so they can't use the link up.
func (s *Server) emailVerify(w http.ResponseWriter, r *http.Request) {
	if o := r.Header.Get("Origin"); o != "" && o != originOf(s.cfg.FrontendURL) {
		httpError(w, http.StatusForbidden, "Sign-in links must be confirmed on "+s.cfg.FrontendURL)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	var email, flow string
	err := s.db.QueryRow(ctx, `DELETE FROM email_logins WHERE token_hash = $1 AND expires_at > now() RETURNING email, flow_id`,
		hashToken(r.PostForm.Get("token"))).Scan(&email, &flow)
	if err != nil {
		s.signInFailed(w, r, nil, "This sign-in link expired or was already used. Request a new one.")
		return
	}
	res, err := s.signin.FinishEmail(ctx, flow, email)
	if err != nil {
		s.signInFailed(w, r, nil, err.Error())
		return
	}
	s.completeSignIn(w, r, res)
}

func originOf(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	return p.Scheme + "://" + p.Host
}

// SyncAdmins makes the admin flags in the database match the server's admin
// list (REQUARA_ADMIN_EMAILS …) at start: people added become admins, people
// removed stop being admins, and admins can't stay locked out.
func SyncAdmins(ctx context.Context, db *pgxpool.Pool, cfg *config.Config) error {
	admins := cfg.Admins
	if admins == nil {
		admins = []string{}
	}
	tag, err := db.Exec(ctx, `
		UPDATE users SET is_admin = (email_verified AND lower(email) = ANY($1))
		WHERE is_admin <> (email_verified AND lower(email) = ANY($1))`, admins)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `UPDATE users SET disabled_at = NULL WHERE is_admin AND disabled_at IS NOT NULL`)
	if err != nil {
		return err
	}
	slog.Info("instance admins", "configured", len(admins), "sources", cfg.AdminSources, "changed", tag.RowsAffected())
	return nil
}
