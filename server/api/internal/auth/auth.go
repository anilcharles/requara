// Package auth signs people in with the providers an install configures:
// any OpenID Connect provider (Google, Microsoft, GitLab, Okta, Auth0,
// Keycloak, …), GitHub, SAML 2.0 identity providers and one-time email
// links, several at once.
//
// It works as a backend-for-frontend: the backend does the code exchange
// (with PKCE), keeps the tokens, and gives the browser only an opaque,
// HttpOnly session cookie (or the desktop app a one-time code). Every OAuth
// provider uses the same callback URL, <frontend>/auth/callback; the signed
// login-state cookie remembers which provider the sign-in went to. SAML and
// email sign-ins come back without that cookie, so their state is stored
// server-side (StateStore) and used once.
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
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/anilcharles/requara/server/api/internal/config"
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
	// EmailVerified: the provider vouched for Email (an email_verified
	// claim, GitHub's verified primary email, a SAML IdP or provider marked
	// TRUST_EMAIL, or a clicked email link).
	EmailVerified bool
}

// StateStore keeps login state for sign-ins that return without the
// browser's cookie (SAML posts, email links). Take reads and deletes.
type StateStore interface {
	Put(ctx context.Context, id string, state []byte, ttl time.Duration) error
	Get(ctx context.Context, id string) ([]byte, error)
	Take(ctx context.Context, id string) ([]byte, error)
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
	// The identity provider behind the button (keycloak, okta, …; "" unknown).
	Product     string `json:"product"`
	ProductName string `json:"productName"`
}

// Manager holds every configured provider.
type Manager struct {
	cfgs     map[string]config.AuthProvider
	byID     map[string]provider // OAuth / OIDC
	saml     map[string]*samlProvider
	order    []string
	key      []byte // signs the short-lived login-state cookie
	frontend string
	redirect string // <frontend>/auth/callback
	store    StateStore
}

func New(ctx context.Context, cfgs []config.AuthProvider, frontendURL string, key []byte, store StateStore) (*Manager, error) {
	redirectURL := frontendURL + "/auth/callback"
	m := &Manager{
		cfgs: map[string]config.AuthProvider{}, byID: map[string]provider{}, saml: map[string]*samlProvider{},
		key: key, frontend: frontendURL, redirect: redirectURL, store: store,
	}
	for _, c := range cfgs {
		switch c.Type {
		case "oidc":
			m.byID[c.ID] = newOIDC(c, redirectURL)
		case "github":
			m.byID[c.ID] = newGitHub(c, redirectURL)
		case "saml":
			m.saml[c.ID] = newSAML(c, frontendURL)
		case "email":
		default:
			return nil, fmt.Errorf("provider %s: unknown type %q", c.ID, c.Type)
		}
		if (c.Type == "saml" || c.Type == "email") && store == nil {
			return nil, fmt.Errorf("provider %s needs a state store", c.ID)
		}
		m.cfgs[c.ID] = c
		m.order = append(m.order, c.ID)
		if c.Public() {
			slog.Warn("anyone with an account at this provider can sign in unless REQUARA_ALLOWED_DOMAINS, an org restriction or invites limit who joins",
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
		if sp, ok := m.saml[id]; ok {
			go func() {
				if _, err := sp.load(context.WithoutCancel(ctx)); err != nil {
					slog.Error("SAML provider unreachable", "provider", id, "err", err)
				} else {
					slog.Info("SAML provider ready", "provider", id, "acs", sp.acsURL(), "entity_id", sp.entityID())
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
		c := m.cfgs[id]
		out = append(out, ProviderInfo{ID: c.ID, Name: c.Name, Type: c.Type, Product: c.Product.ID, ProductName: c.Product.Name})
	}
	return out
}

// Info returns a provider's settings, for its name and account link.
func (m *Manager) Info(id string) (config.AuthProvider, bool) {
	c, ok := m.cfgs[id]
	return c, ok
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
	// SAML: the AuthnRequest ID the response must answer.
	SAMLRequestID string `json:"sr,omitempty"`
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

const stateCookie = "requara_login"

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
	cfg, ok := m.cfgs[id]
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
	switch cfg.Type {
	case "email", "saml":
		// Stored server-side under a random id: the RelayState for SAML,
		// the flow id the email page and link carry.
		flow := random(18)
		to := m.frontend + "/signin/email?flow=" + flow
		if cfg.Type == "email" && q.Get("login_hint") != "" {
			to += "&email=" + url.QueryEscape(q.Get("login_hint"))
		}
		if cfg.Type == "saml" {
			var err error
			if to, err = m.saml[id].authURL(r.Context(), &st, flow); err != nil {
				return err
			}
		}
		ttl := 10 * time.Minute
		if cfg.Type == "email" {
			ttl = 30 * time.Minute // time to find the email
		}
		st.Exp = time.Now().Add(ttl).Unix()
		raw, _ := json.Marshal(st)
		if err := m.store.Put(r.Context(), flow, raw, ttl); err != nil {
			return err
		}
		http.Redirect(w, r, to, http.StatusFound)
		return nil
	}
	p := m.byID[id]
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
	return m.result(&st, cl, idToken)
}

// result finishes any provider's sign-in: namespaced subject, a name, and
// the email Requara needs.
func (m *Manager) result(st *loginState, cl *Claims, idToken string) (*Result, error) {
	name := m.cfgs[st.Provider].Name
	cl.Subject = st.Provider + ":" + cl.Subject
	cl.Email = strings.TrimSpace(cl.Email)
	if cl.Name == "" {
		cl.Name = strings.TrimSpace(cl.GivenName + " " + cl.FamilyName)
	}
	if cl.Name == "" {
		cl.Name = cl.Username
	}
	if cl.Email == "" {
		return nil, fmt.Errorf("%s didn't share an email address; Requara needs one", name)
	}
	if cl.Name == "" {
		cl.Name, _, _ = strings.Cut(cl.Email, "@")
	}
	return &Result{
		Provider: st.Provider, Claims: cl, IDToken: idToken, Redirect: st.Redirect,
		DesktopPort: st.DesktopPort, DesktopChallenge: st.DesktopChallenge,
	}, nil
}

func (m *Manager) loadState(raw []byte, err error) (*loginState, error) {
	if err != nil {
		return nil, errors.New("sign-in expired, please try again")
	}
	var st loginState
	if err := json.Unmarshal(raw, &st); err != nil || time.Now().Unix() > st.Exp {
		return nil, errors.New("sign-in expired, please try again")
	}
	return &st, nil
}

// ----- SAML -----

// FinishSAML checks the identity provider's POST to /auth/saml/<id>/acs.
// The RelayState names the stored login state, which is used up here.
func (m *Manager) FinishSAML(r *http.Request, id string) (*Result, error) {
	sp, ok := m.saml[id]
	if !ok {
		return nil, errors.New("that sign-in option is no longer available")
	}
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	relay := r.PostForm.Get("RelayState")
	if relay == "" {
		return nil, fmt.Errorf("start signing in from Requara: sign-ins started at %s aren't accepted", sp.cfg.Name)
	}
	st, err := m.loadState(m.store.Take(r.Context(), relay))
	if err != nil {
		return nil, err
	}
	if st.Provider != id {
		return nil, errors.New("state mismatch")
	}
	cl, err := sp.parse(r, st)
	if err != nil {
		return nil, err
	}
	return m.result(st, cl, "")
}

// SAMLMetadata is the service provider metadata to give the identity provider.
func (m *Manager) SAMLMetadata(ctx context.Context, id string) (any, error) {
	sp, ok := m.saml[id]
	if !ok {
		return nil, errors.New("no such SAML provider")
	}
	return sp.metadata(ctx)
}

// SAMLInfo is what an admin registers at the identity provider.
type SAMLInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	EntityID    string `json:"entityId"`
	ACSURL      string `json:"acsUrl"`
	MetadataURL string `json:"metadataUrl"`
}

func (m *Manager) SAMLProviders() []SAMLInfo {
	var out []SAMLInfo
	for _, id := range m.order {
		if sp, ok := m.saml[id]; ok {
			out = append(out, SAMLInfo{ID: id, Name: sp.cfg.Name, EntityID: sp.entityID(), ACSURL: sp.acsURL(), MetadataURL: sp.metadataURL()})
		}
	}
	return out
}

// ----- email links -----

// EmailFlow returns the provider of a pending email sign-in, without using it up.
func (m *Manager) EmailFlow(ctx context.Context, flow string) (provider string, desktop bool, err error) {
	st, err := m.loadState(m.store.Get(ctx, flow))
	if err != nil {
		return "", false, err
	}
	if m.cfgs[st.Provider].Type != "email" {
		return "", false, errors.New("state mismatch")
	}
	return st.Provider, st.DesktopPort != 0, nil
}

// FinishEmail completes an email sign-in once the person opened the link
// sent to email; the flow's state is used up.
func (m *Manager) FinishEmail(ctx context.Context, flow, email string) (*Result, error) {
	st, err := m.loadState(m.store.Take(ctx, flow))
	if err != nil {
		return nil, err
	}
	if m.cfgs[st.Provider].Type != "email" {
		return nil, errors.New("state mismatch")
	}
	email = strings.ToLower(strings.TrimSpace(email))
	local, _, _ := strings.Cut(email, "@")
	return m.result(st, &Claims{Subject: email, Email: email, Username: email, Name: local, EmailVerified: true}, "")
}

// LogoutURL ends the provider's session too (when it supports RP-initiated
// logout) and returns to returnTo.
func (m *Manager) LogoutURL(providerID, idToken, returnTo string) string {
	if p, ok := m.byID[providerID]; ok {
		return p.logoutURL(idToken, returnTo)
	}
	return returnTo
}
