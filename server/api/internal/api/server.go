// Package api is Speklo's HTTP layer: the JSON API used by the web app, the
// sign-in endpoints, and the public mock-server endpoints.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/anilcharles/speklo/server/api/internal/auth"
	"github.com/anilcharles/speklo/server/api/internal/bao"
	"github.com/anilcharles/speklo/server/api/internal/config"
	"github.com/anilcharles/speklo/server/api/internal/events"
	"github.com/anilcharles/speklo/server/api/internal/runner"
	"github.com/anilcharles/speklo/server/api/internal/storage"
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
	})

	r.HandleFunc("/mock/{slug}/*", s.serveMock)

	r.Route("/api", func(r chi.Router) {
		r.Get("/version", s.version)
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

			r.Get("/workspaces", s.listWorkspaces)
			r.Get("/members", s.listMembers)
			r.Post("/members/invite", s.invite)
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
// (SPEKLO_PUBLISHER_GROUPS). Workspace owners can always publish.
func (s *Server) canPublish(p *Principal) bool {
	return p.MemberRole == "owner" ||
		(p.CanEdit() && auth.Matches(append(slices.Clone(p.Roles), p.Groups...), s.cfg.PublisherGroups))
}

const sessionCookie = "speklo_session"

func hashToken(t string) []byte { h := sha256.Sum256([]byte(t)); return h[:] }

// authenticate accepts the session cookie, a desktop session token
// ("Authorization: Bearer sd_...") or an API key ("Authorization: Bearer rk_...").
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		p := &Principal{}
		var prefs []byte
		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && strings.HasPrefix(bearer, "rk_") {
			var keyID string
			err := s.db.QueryRow(ctx, `
				SELECT k.id, k.scopes, u.id, u.email, u.name, u.username, u.roles, u.groups, u.prefs
				FROM api_keys k JOIN users u ON u.id = k.user_id
				WHERE k.hash = $1 AND k.revoked_at IS NULL AND (k.expires_at IS NULL OR k.expires_at > now())`,
				hashToken(bearer)).Scan(&keyID, &p.KeyScopes, &p.UserID, &p.Email, &p.Name, &p.Username, &p.Roles, &p.Groups, &prefs)
			if err != nil {
				httpError(w, http.StatusUnauthorized, "Invalid, expired or revoked API key")
				return
			}
			if !keyAllows(p.KeyScopes, r) {
				httpError(w, http.StatusForbidden, "This API key doesn't have the scope for this call")
				return
			}
			_, _ = s.db.Exec(ctx, `UPDATE api_keys SET last_used_at = now() WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`, keyID)
		} else if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && strings.HasPrefix(bearer, desktopTokenPrefix) {
			// Desktop app: same sessions table as the browser, but sent as a
			// header, so it needs no CSRF check.
			p.SessionHash = hashToken(bearer)
			err := s.db.QueryRow(ctx, `
				SELECT u.id, u.email, u.name, u.username, u.roles, u.groups, u.prefs
				FROM sessions s JOIN users u ON u.id = s.user_id
				WHERE s.id_hash = $1 AND s.expires_at > now()`, p.SessionHash).
				Scan(&p.UserID, &p.Email, &p.Name, &p.Username, &p.Roles, &p.Groups, &prefs)
			if err != nil {
				httpError(w, http.StatusUnauthorized, "Your session ended. Sign in again.")
				return
			}
			_, _ = s.db.Exec(ctx, `UPDATE sessions SET last_seen_at = now() WHERE id_hash = $1 AND last_seen_at < now() - interval '1 minute'`, p.SessionHash)
		} else {
			c, err := r.Cookie(sessionCookie)
			if err != nil {
				httpError(w, http.StatusUnauthorized, "Sign in required")
				return
			}
			p.SessionHash = hashToken(c.Value)
			err = s.db.QueryRow(ctx, `
				SELECT u.id, u.email, u.name, u.username, u.roles, u.groups, u.prefs
				FROM sessions s JOIN users u ON u.id = s.user_id
				WHERE s.id_hash = $1 AND s.expires_at > now()`, p.SessionHash).
				Scan(&p.UserID, &p.Email, &p.Name, &p.Username, &p.Roles, &p.Groups, &prefs)
			if err != nil {
				httpError(w, http.StatusUnauthorized, "Your session ended. Sign in again.")
				return
			}
			// CSRF: cookie-authenticated writes must come from our own app.
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Requested-With") != "speklo" {
				httpError(w, http.StatusForbidden, "Missing X-Requested-With header")
				return
			}
			_, _ = s.db.Exec(ctx, `UPDATE sessions SET last_seen_at = now() WHERE id_hash = $1 AND last_seen_at < now() - interval '1 minute'`, p.SessionHash)
		}
		_ = json.Unmarshal(prefs, &p.Prefs)
		if p.Prefs == nil {
			p.Prefs = map[string]any{}
		}
		// Workspace: header, then saved preference, then first membership.
		want := r.Header.Get("X-Workspace")
		if want == "" {
			want, _ = p.Prefs["workspaceId"].(string)
		}
		err := s.db.QueryRow(ctx, `
			SELECT workspace_id, role FROM members WHERE user_id = $1
			ORDER BY (workspace_id::text = $2) DESC, created_at LIMIT 1`, p.UserID, want).Scan(&p.WorkspaceID, &p.MemberRole)
		if err != nil {
			httpError(w, http.StatusForbidden, "You're not a member of any workspace")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, ctxKey{}, p)))
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
	case strings.HasSuffix(path, "/send"):
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
			httpError(w, http.StatusForbidden, "Manage keys and sessions from the Speklo app, not with an API key")
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
