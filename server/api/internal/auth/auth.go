// Package auth signs people in with the providers an install configures:
// any OpenID Connect provider (Google, Microsoft, GitLab, Okta, Auth0,
// Keycloak, …) and GitHub, several at once.
//
// It works as a backend-for-frontend: the backend does the code exchange
// (with PKCE), keeps the tokens, and gives the browser only an opaque,
// HttpOnly session cookie (or the desktop app a one-time code). Every
// provider uses the same callback URL, <frontend>/auth/callback; the signed
// login-state cookie remembers which provider the sign-in went to.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/anilcharles/speklo/server/api/internal/config"
)

// Claims describe the person who signed in, whatever the provider.
type Claims struct {
	// Subject is "<provider id>:<the provider's user id>", so ids from
	// different providers never collide.
	Subject    string
	Email      string
	Name       string
	Username   string
	GivenName  string
	FamilyName string
	Roles      []string
	Groups     []string
}

// provider is one configured way to sign in.
type provider interface {
	info() config.AuthProvider
	// authURL is where the browser goes to sign in.
	authURL(ctx context.Context, st *loginState, loginHint string) (string, error)
	// exchange trades the callback's code for the person's claims and, for
	// OIDC providers, the raw ID token (kept for logout).
	exchange(ctx context.Context, code string, st *loginState) (*Claims, string, error)
	// logoutURL ends the provider session too, when the provider supports it.
	logoutURL(idToken, returnTo string) string
}

// ProviderInfo is what sign-in screens show.
type ProviderInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// Manager holds every configured provider.
type Manager struct {
	byID     map[string]provider
	order    []string
	key      []byte // signs the short-lived login-state cookie
	redirect string // <frontend>/auth/callback
}

func New(ctx context.Context, cfgs []config.AuthProvider, redirectURL string, key []byte) (*Manager, error) {
	m := &Manager{byID: map[string]provider{}, key: key, redirect: redirectURL}
	for _, c := range cfgs {
		var p provider
		switch c.Type {
		case "oidc":
			p = newOIDC(c, redirectURL)
		case "github":
			p = newGitHub(c, redirectURL)
		default:
			return nil, fmt.Errorf("provider %s: unknown type %q", c.ID, c.Type)
		}
		m.byID[c.ID] = p
		m.order = append(m.order, c.ID)
		if c.Public() {
			slog.Warn("anyone with an account at this provider can sign in unless SPEKLO_ALLOWED_DOMAINS, an org restriction or invites limit who joins",
				"provider", c.ID)
		}
	}
	// Warm up OIDC discovery in the background so the first sign-in is fast
	// and a misconfigured issuer shows up in the log at start.
	for _, id := range m.order {
		if o, ok := m.byID[id].(*oidcProvider); ok {
			go func() {
				if _, err := o.discover(context.WithoutCancel(ctx)); err != nil {
					slog.Error("sign-in provider unreachable", "provider", id, "err", err)
				}
			}()
		}
	}
	return m, nil
}

// Providers lists the sign-in options in display order.
func (m *Manager) Providers() []ProviderInfo {
	out := make([]ProviderInfo, 0, len(m.order))
	for _, id := range m.order {
		c := m.byID[id].info()
		out = append(out, ProviderInfo{ID: c.ID, Name: c.Name, Type: c.Type})
	}
	return out
}

// Info returns a provider's settings, for its name and account link.
func (m *Manager) Info(id string) (config.AuthProvider, bool) {
	p, ok := m.byID[id]
	if !ok {
		return config.AuthProvider{}, false
	}
	return p.info(), true
}

type loginState struct {
	Provider string `json:"p"`
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Redirect string `json:"r"`
	Exp      int64  `json:"e"`
	// Desktop sign-in (RFC 8252 loopback): where the desktop app listens
	// and the S256 challenge it will prove when it exchanges the code.
	DesktopPort      int    `json:"dp,omitempty"`
	DesktopChallenge string `json:"dc,omitempty"`
}

// Result is a finished sign-in.
type Result struct {
	Provider string
	Claims   *Claims
	IDToken  string
	Redirect string // in-app path to return to (browser)
	// Set when the desktop app started this sign-in.
	DesktopPort      int
	DesktopChallenge string
}

var challengeRE = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

const stateCookie = "speklo_login"

func random(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (m *Manager) sign(b []byte) string {
	h := hmac.New(sha256.New, m.key)
	h.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func (m *Manager) unsign(s string) ([]byte, error) {
	data, sig, ok := strings.Cut(s, ".")
	if !ok {
		return nil, errors.New("bad state")
	}
	raw, err := base64.RawURLEncoding.DecodeString(data)
	if err != nil {
		return nil, err
	}
	want, _ := base64.RawURLEncoding.DecodeString(sig)
	h := hmac.New(sha256.New, m.key)
	h.Write(raw)
	if !hmac.Equal(h.Sum(nil), want) {
		return nil, errors.New("bad state signature")
	}
	return raw, nil
}

// safeRedirect only allows paths inside the app.
func safeRedirect(r string) string {
	if !strings.HasPrefix(r, "/") || strings.HasPrefix(r, "//") || strings.HasPrefix(r, "/\\") {
		return "/"
	}
	return r
}

// Begin sends the browser to the provider named by ?provider= (optional when
// only one is configured).
func (m *Manager) Begin(w http.ResponseWriter, r *http.Request, secure bool) error {
	q := r.URL.Query()
	id := q.Get("provider")
	if id == "" && len(m.order) == 1 {
		id = m.order[0]
	}
	p, ok := m.byID[id]
	if !ok {
		return fmt.Errorf("choose how to sign in")
	}
	st := loginState{
		Provider: id, State: random(24), Nonce: random(24), Verifier: oauth2.GenerateVerifier(),
		Redirect: safeRedirect(q.Get("redirect")), Exp: time.Now().Add(10 * time.Minute).Unix(),
	}
	if port, err := strconv.Atoi(q.Get("desktop_port")); err == nil && port >= 1024 && port <= 65535 {
		if ch := q.Get("desktop_challenge"); challengeRE.MatchString(ch) {
			st.DesktopPort, st.DesktopChallenge = port, ch
		}
	}
	to, err := p.authURL(r.Context(), &st, q.Get("login_hint"))
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(st)
	http.SetCookie(w, &http.Cookie{
		Name: stateCookie, Value: m.sign(raw), Path: "/auth", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteLaxMode, MaxAge: 600,
	})
	http.Redirect(w, r, to, http.StatusFound)
	return nil
}

// Finish handles the provider's callback and returns the verified sign-in.
func (m *Manager) Finish(w http.ResponseWriter, r *http.Request) (*Result, error) {
	c, err := r.Cookie(stateCookie)
	if err != nil {
		return nil, errors.New("sign-in expired, please try again")
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: "", Path: "/auth", MaxAge: -1})
	raw, err := m.unsign(c.Value)
	if err != nil {
		return nil, err
	}
	var st loginState
	if err := json.Unmarshal(raw, &st); err != nil || time.Now().Unix() > st.Exp {
		return nil, errors.New("sign-in expired, please try again")
	}
	p, ok := m.byID[st.Provider]
	if !ok {
		return nil, errors.New("that sign-in option is no longer available")
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		return nil, fmt.Errorf("%s: %s %s", p.info().Name, e, q.Get("error_description"))
	}
	if q.Get("state") != st.State {
		return nil, errors.New("state mismatch")
	}
	cl, idToken, err := p.exchange(r.Context(), q.Get("code"), &st)
	if err != nil {
		return nil, err
	}
	cl.Subject = st.Provider + ":" + cl.Subject
	if cl.Name == "" {
		cl.Name = strings.TrimSpace(cl.GivenName + " " + cl.FamilyName)
	}
	if cl.Name == "" {
		cl.Name = cl.Username
	}
	if cl.Email == "" {
		return nil, fmt.Errorf("%s didn't share an email address; Speklo needs one", p.info().Name)
	}
	return &Result{
		Provider: st.Provider, Claims: cl, IDToken: idToken, Redirect: st.Redirect,
		DesktopPort: st.DesktopPort, DesktopChallenge: st.DesktopChallenge,
	}, nil
}

// LogoutURL ends the provider's session too (when it supports RP-initiated
// logout) and returns to returnTo.
func (m *Manager) LogoutURL(providerID, idToken, returnTo string) string {
	if p, ok := m.byID[providerID]; ok {
		return p.logoutURL(idToken, returnTo)
	}
	return returnTo
}
