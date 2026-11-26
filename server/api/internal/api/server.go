// Package api is Requara's HTTP layer: the JSON API used by the web app, the
// sign-in endpoints, and the public mock-server endpoints.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/anilcharles/requara/server/api/internal/auth"
	"github.com/anilcharles/requara/server/api/internal/bao"
	"github.com/anilcharles/requara/server/api/internal/config"
	"github.com/anilcharles/requara/server/api/internal/events"
	"github.com/anilcharles/requara/server/api/internal/runner"
	"github.com/anilcharles/requara/server/api/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

type Server struct {
	cfg    *config.Config
	db     *pgxpool.Pool
	bao    *bao.Client
	signin *auth.Manager
	runner *runner.Runner
	s3     *storage.S3
	jobs   *river.Client[pgx.Tx]
	hub    *events.Hub
}

func New(cfg *config.Config, db *pgxpool.Pool, b *bao.Client, signin *auth.Manager, s3 *storage.S3, jobs *river.Client[pgx.Tx]) *Server {
	return &Server{cfg: cfg, db: db, bao: b, s3: s3, jobs: jobs, hub: events.NewHub(), signin: signin,
		runner: &runner.Runner{AllowPrivateNetwork: cfg.AllowPrivateNetwork}}
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer, requestLog)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	r.Route("/auth", func(r chi.Router) {
		r.Post("/desktop/token", s.desktopToken)
		r.Get("/providers", s.providers)
		r.Get("/login", s.login)
		r.Get("/callback", s.callback)
		r.Post("/logout", s.logout)
		// SAML 2.0: metadata for the identity provider, and where it posts back.
		r.Get("/saml/{id}/metadata", s.samlMetadata)
		r.Post("/saml/{id}/acs", s.samlACS)
		// One-time email links.
		r.Get("/email", s.emailFlow)
		r.Post("/email/start", s.emailStart)
		r.Post("/email/verify", s.emailVerify)
	})

	// SCIM 2.0 provisioning, with an admin token.
	r.Route("/scim/v2", func(r chi.Router) {
		r.Use(s.adminOnly)
		r.Get("/ServiceProviderConfig", s.scimServiceProviderConfig)
		r.Get("/ResourceTypes", s.scimResourceTypes)
		r.Get("/Users", s.scimListUsers)
		r.Post("/Users", s.scimCreateUser)
		r.Get("/Users/{id}", s.scimGetUser)
		r.Put("/Users/{id}", s.scimReplaceUser)
		r.Patch("/Users/{id}", s.scimPatchUser)
		r.Delete("/Users/{id}", s.scimDeleteUser)
		r.HandleFunc("/Groups", s.scimGroups)
		r.HandleFunc("/Groups/*", s.scimGroups)
	})

	r.HandleFunc("/mock/{slug}/*", s.serveMock)

	r.Route("/api", func(r chi.Router) {
		r.Get("/version", s.version)

		// Instance administration: admins from REQUARA_ADMIN_EMAILS, or an admin token.
		r.Route("/admin", func(r chi.Router) {
			r.Use(s.adminOnly)
			r.Get("/overview", s.adminOverview)
			r.Get("/users", s.adminUsers)
			r.Get("/users/{id}", s.adminUser)
			r.Post("/users/{id}/deactivate", s.adminDeactivateUser)
			r.Post("/users/{id}/reactivate", s.adminReactivateUser)
			r.Post("/users/{id}/signout", s.adminSignOutUser)
			r.Delete("/users/{id}", s.adminDeleteUser)
			r.Get("/invites", s.adminInvites)
			r.Post("/invites", s.adminInvite)
			r.Delete("/invites/{id}", s.adminRevokeInvite)
			r.Get("/workspaces", s.adminWorkspaces)
			r.Post("/workspaces", s.adminCreateWorkspace)
			r.Patch("/workspaces/{id}", s.adminUpdateWorkspace)
			r.Delete("/workspaces/{id}", s.adminDeleteWorkspace)
			r.Get("/workspaces/{id}/members", s.adminWorkspaceMembers)
			r.Put("/workspaces/{id}/members/{userId}", s.adminSetMember)
			r.Delete("/workspaces/{id}/members/{userId}", s.adminRemoveMember)
			r.Get("/directory", s.adminDirectory)
			r.Get("/audit", s.listAudit)
			r.Get("/audit.csv", s.exportAudit)
			r.Get("/settings", s.adminGetSettings)
			r.Put("/settings", s.adminPutSettings)
			r.Post("/history/purge", s.adminPurgeHistory)
			r.Group(func(r chi.Router) {
				r.Use(sessionAdminOnly)
				r.Get("/tokens", s.adminTokens)
				r.Post("/tokens", s.adminCreateToken)
				r.Delete("/tokens/{id}", s.adminRevokeToken)
			})
		})

		r.Group(func(r chi.Router) {
			r.Use(s.authenticate)

			r.Get("/me", s.me)
			r.Patch("/me/preferences", s.updatePrefs)
			r.Group(func(r chi.Router) {
				r.Use(sessionOnly) // keys and sessions can't be managed with an API key
				r.Get("/me/sessions", s.listSessions)
				r.Delete("/me/sessions/{id}", s.deleteSession)
				r.Delete("/me/sessions", s.deleteOtherSessions)
				r.Get("/me/keys", s.listKeys)
				r.Post("/me/keys", s.createKey)
				r.Delete("/me/keys/{id}", s.revokeKey)
			})

			r.Get("/instance", s.instance)
			r.Get("/workspaces", s.listWorkspaces)
			r.Post("/workspaces", s.newWorkspace)
			r.Get("/members", s.listMembers)
			r.Post("/members/invite", s.invite)
			r.Delete("/members/invite", s.revokeInvite)
			r.Patch("/members/{userId}", s.setMember)
			r.Delete("/members/{userId}", s.removeMember)
			r.Get("/search", s.search)

			r.Get("/apis", s.listAPIs)
			r.Post("/apis", s.createAPI)
			r.Post("/apis/import", s.importAPI)
			r.Get("/apis/{id}", s.getAPI)
			r.Get("/apis/{id}/operations", s.listOperations)
			r.Get("/apis/{id}/operation", s.getOperation)
			r.Put("/apis/{id}/operation", s.updateOperation)
			r.Get("/apis/{id}/lint", s.lintAPI)
			r.Get("/apis/{id}/changes", s.apiChanges)
			r.Post("/apis/{id}/publish", s.publishAPI)
			r.Post("/apis/{id}/discard", s.discardAPI)
			r.Get("/apis/{id}/docs", s.apiDocs)
			r.Get("/apis/{id}/spec", s.downloadSpec)

			r.Get("/collections", s.collections)
			r.Post("/collections", s.createCollection)
			r.Post("/collections/import", s.importCollection)
			r.Patch("/collections/{id}", s.renameCollection)
			r.Delete("/collections/{id}", s.deleteCollection)
			r.Get("/collections/{id}/settings", s.getCollectionSettings)
			r.Put("/collections/{id}/settings", s.putCollectionSettings)
			r.Get("/collections/{id}/export", s.exportCollection)
			r.Get("/requests/{id}/examples", s.listExamples)
			r.Post("/requests/{id}/examples", s.createExample)
			r.Delete("/examples/{id}", s.deleteExample)
			r.Post("/collections/{id}/run", s.runCollectionAPI)
			r.Get("/monitors", s.listMonitors)
			r.Post("/monitors", s.createMonitor)
			r.Patch("/monitors/{id}", s.updateMonitor)
			r.Delete("/monitors/{id}", s.deleteMonitor)
			r.Get("/monitors/{id}/runs", s.monitorRuns)
			r.Post("/monitors/{id}/run", s.runMonitorNow)
			r.Get("/cookies", s.listCookies)
			r.Delete("/cookies", s.deleteCookies)
			r.Post("/requests", s.createRequest)
			r.Get("/requests/{id}", s.getRequest)
			r.Put("/requests/{id}", s.updateRequest)
			r.Delete("/requests/{id}", s.deleteRequest)
			r.Post("/requests/{id}/send", s.sendRequest)

			r.Get("/environments", s.listEnvironments)
			r.Post("/environments", s.createEnvironment)
			r.Get("/environments/{id}/variables", s.listVariables)
			r.Put("/environments/{id}/variables", s.saveVariables)

			r.Get("/history", s.listHistory)
			r.Get("/history/{id}", s.getHistory)
			r.Delete("/history", s.clearHistory)

			r.Get("/mocks", s.listMocks)
			r.Post("/mocks", s.createMock)
			r.Patch("/mocks/{id}", s.updateMock)
			r.Get("/mocks/calls", s.mockCalls)
			r.Get("/mocks/stream", s.mockStream)
		})
	})
	if s.cfg.StaticDir != "" {
		// Everything that isn't the API, sign-in or a mock server is the web UI.
		r.NotFound(webUI(s.cfg.StaticDir))
	}
	return r
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		if !strings.HasSuffix(r.URL.Path, "/stream") {
			slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "ms", time.Since(start).Milliseconds())
		}
	})
}

// ----- principal -----

type Principal struct {
	Kind        string // user | token (admin token)
	IsAdmin     bool   // instance admin
	TokenID     string // admin token
	TokenName   string
	UserID      string
	Email       string
	Name        string
	Username    string
	Roles       []string
	Groups      []string
	Prefs       map[string]any
	WorkspaceID string
	MemberRole  string // owner | editor | viewer
	SessionHash []byte // set for cookie sessions
	KeyScopes   []string
}

type ctxKey struct{}

func principal(r *http.Request) *Principal { return r.Context().Value(ctxKey{}).(*Principal) }

func (p *Principal) CanEdit() bool { return p.MemberRole == "owner" || p.MemberRole == "editor" }

// canPublish: editors whose roles or groups include a publisher group
// (REQUARA_PUBLISHER_GROUPS). Workspace owners can always publish.
func (s *Server) canPublish(p *Principal) bool {
	return p.MemberRole == "owner" ||
		(p.CanEdit() && auth.Matches(append(slices.Clone(p.Roles), p.Groups...), s.cfg.PublisherGroups))
}

const sessionCookie = "requara_session"

// adminTokenPrefix marks instance-wide admin API tokens.
const adminTokenPrefix = "sa_"

func hashToken(t string) []byte { h := sha256.Sum256([]byte(t)); return h[:] }

// identify works out who is calling: the session cookie, a desktop session
// token ("Bearer sd_…"), an API key ("Bearer rk_…") or an admin token
// ("Bearer sa_…"). It returns an HTTP status and message when nobody is.
func (s *Server) identify(r *http.Request) (*Principal, int, string) {
	ctx := r.Context()
	p := &Principal{Kind: "user"}
	var prefs []byte
	bearer, hasBearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	const userCols = `u.id, u.email, u.name, u.username, u.roles, u.groups, u.prefs, u.is_admin`
	scanUser := func(row pgx.Row, extra ...any) error {
		return row.Scan(append(extra, &p.UserID, &p.Email, &p.Name, &p.Username, &p.Roles, &p.Groups, &prefs, &p.IsAdmin)...)
	}
	switch {
	case hasBearer && strings.HasPrefix(bearer, adminTokenPrefix):
		err := s.db.QueryRow(ctx, `
			UPDATE admin_tokens SET last_used_at = now()
			WHERE hash = $1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())
			RETURNING id, name`, hashToken(bearer)).Scan(&p.TokenID, &p.TokenName)
		if err != nil {
			return nil, http.StatusUnauthorized, "Invalid, expired or revoked admin token"
		}
		p.Kind, p.IsAdmin = "token", true
		return p, 0, ""
	case hasBearer && strings.HasPrefix(bearer, "rk_"):
		var keyID string
		err := scanUser(s.db.QueryRow(ctx, `
			SELECT k.id, k.scopes, `+userCols+`
			FROM api_keys k JOIN users u ON u.id = k.user_id
			WHERE k.hash = $1 AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at > now())
			  AND u.disabled_at IS NULL`, hashToken(bearer)), &keyID, &p.KeyScopes)
		if err != nil {
			return nil, http.StatusUnauthorized, "Invalid, expired or revoked API key"
		}
		if !keyAllows(p.KeyScopes, r) {
			return nil, http.StatusForbidden, "This API key doesn't have the scope for this call"
		}
		_, _ = s.db.Exec(ctx, `UPDATE api_keys SET last_used_at = now() WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`, keyID)
	case hasBearer && strings.HasPrefix(bearer, desktopTokenPrefix):
		// Desktop app: same sessions table as the browser, but sent as a
		// header, so it needs no CSRF check.
		p.SessionHash = hashToken(bearer)
		err := scanUser(s.db.QueryRow(ctx, `
			SELECT `+userCols+` FROM sessions s JOIN users u ON u.id = s.user_id
			WHERE s.id_hash = $1 AND s.expires_at > now() AND u.disabled_at IS NULL`, p.SessionHash))
		if err != nil {
			return nil, http.StatusUnauthorized, "Your session ended. Sign in again."
		}
		_, _ = s.db.Exec(ctx, `UPDATE sessions SET last_seen_at = now() WHERE id_hash = $1 AND last_seen_at < now() - interval '1 minute'`, p.SessionHash)
	default:
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			return nil, http.StatusUnauthorized, "Sign in required"
		}
		p.SessionHash = hashToken(c.Value)
		err = scanUser(s.db.QueryRow(ctx, `
			SELECT `+userCols+` FROM sessions s JOIN users u ON u.id = s.user_id
			WHERE s.id_hash = $1 AND s.expires_at > now() AND u.disabled_at IS NULL`, p.SessionHash))
		if err != nil {
			return nil, http.StatusUnauthorized, "Your session ended. Sign in again."
		}
		// CSRF: cookie-authenticated writes must come from our own app.
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Requested-With") != "requara" {
			return nil, http.StatusForbidden, "Missing X-Requested-With header"
		}
		_, _ = s.db.Exec(ctx, `UPDATE sessions SET last_seen_at = now() WHERE id_hash = $1 AND last_seen_at < now() - interval '1 minute'`, p.SessionHash)
	}
	_ = json.Unmarshal(prefs, &p.Prefs)
	if p.Prefs == nil {
		p.Prefs = map[string]any{}
	}
	return p, 0, ""
}

// noWorkspaceOK lists the calls that work before someone belongs to a
// workspace: who am I, and creating or listing workspaces.
func noWorkspaceOK(r *http.Request) bool {
	return r.URL.Path == "/api/me" || r.URL.Path == "/api/workspaces" || r.URL.Path == "/api/instance"
}

// authenticate lets in people (and their API keys) and picks the workspace
// the call is about.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, status, msg := s.identify(r)
		if p == nil {
			httpError(w, status, msg)
			return
		}
		if p.Kind == "token" {
			httpError(w, http.StatusForbidden, "Admin tokens work only with /api/admin and /scim/v2")
			return
		}
		ctx := r.Context()
		// Workspace: header, then saved preference, then first membership.
		want := r.Header.Get("X-Workspace")
		if want == "" {
			want, _ = p.Prefs["workspaceId"].(string)
		}
		err := s.db.QueryRow(ctx, `
			SELECT workspace_id, role FROM members WHERE user_id = $1
			ORDER BY (workspace_id::text = $2) DESC, created_at LIMIT 1`, p.UserID, want).Scan(&p.WorkspaceID, &p.MemberRole)
		if err != nil && !noWorkspaceOK(r) {
			httpError(w, http.StatusForbidden, "You're not a member of any workspace")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, ctxKey{}, p)))
	})
}

// adminOnly lets in instance admins (named in REQUARA_ADMIN_EMAILS) with
// their browser or desktop session, and admin tokens. API keys never work
// here, whatever their scope.
func (s *Server) adminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, status, msg := s.identify(r)
		if p == nil {
			httpError(w, status, msg)
			return
		}
		if p.KeyScopes != nil {
			httpError(w, http.StatusForbidden, "API keys can't use the admin API; use an admin token")
			return
		}
		if !p.IsAdmin {
			httpError(w, http.StatusForbidden, "Only instance admins can do this. Admins are listed in REQUARA_ADMIN_EMAILS on the server.")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, p)))
	})
}

// keyAllows maps API-key scopes to calls: reads need collections:read,
// sending needs runs:write, other writes need collections:write.
func keyAllows(scopes []string, r *http.Request) bool {
	if slices.Contains(scopes, "full access") {
		return true
	}
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/api/me/") && r.Method != http.MethodGet:
		return false
	case strings.HasSuffix(path, "/send") || (strings.HasPrefix(path, "/api/collections/") && strings.HasSuffix(path, "/run")):
		return slices.Contains(scopes, "runs:write")
	case strings.HasPrefix(path, "/api/history") || strings.HasPrefix(path, "/api/mocks/calls"):
		return slices.Contains(scopes, "metrics:read") || slices.Contains(scopes, "collections:read")
	case r.Method == http.MethodGet:
		return slices.Contains(scopes, "collections:read")
	default:
		return slices.Contains(scopes, "collections:write")
	}
}

func sessionOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if principal(r).SessionHash == nil {
			httpError(w, http.StatusForbidden, "Manage keys and sessions from the Requara app, not with an API key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requireEdit(w http.ResponseWriter, p *Principal) bool {
	if !p.CanEdit() {
		httpError(w, http.StatusForbidden, "You have view-only access to this workspace")
		return false
	}
	return true
}

// ----- helpers -----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 5<<20))
	if err := dec.Decode(v); err != nil {
		httpError(w, http.StatusBadRequest, "Invalid JSON: "+err.Error())
		return false
	}
	return true
}

func serverError(w http.ResponseWriter, err error) {
	slog.Error("request failed", "err", err)
	httpError(w, http.StatusInternalServerError, "Something went wrong on our side")
}

func notFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func ptr[T any](v T) *T { return &v }

// urlParam is a URL path parameter, unescaped: user ids such as
// "company:1234" arrive as "company%3A1234".
func urlParam(r *http.Request, name string) string {
	v := chi.URLParam(r, name)
	if u, err := url.PathUnescape(v); err == nil {
		return u
	}
	return v
}
