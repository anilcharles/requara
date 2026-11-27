package api

import (
	"net/http"
	"net/url"

	"github.com/anilcharles/requara/server/api/internal/runner"
)

// OAuth 2.0 authorization code (with PKCE) for a request's Auth tab.
//
// The browser makes the state and the PKCE code verifier and keeps them; it
// opens the authorize URL from here in a popup, the identity provider sends
// the person to the web app's /oauth2/callback page, which hands the code
// back to the opener, and the opener trades it for tokens here. The server
// fills in {{variables}} (including secrets, such as the client secret) and
// talks to the token endpoint, so secrets never reach the browser.

type oauth2In struct {
	Auth          runner.Auth `json:"auth"`
	EnvironmentID string      `json:"environmentId"`
	CollectionID  string      `json:"collectionId"` // or API ID: whose variables apply
	RedirectURI   string      `json:"redirectUri"`
	State         string      `json:"state"`
	CodeVerifier  string      `json:"codeVerifier"`
	Code          string      `json:"code"`
}

func (s *Server) oauth2Vars(w http.ResponseWriter, r *http.Request, in *oauth2In) (map[string]string, bool) {
	if u, err := url.Parse(in.RedirectURI); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		httpError(w, http.StatusBadRequest, "redirectUri must be an http or https URL")
		return nil, false
	}
	p := principal(r)
	var collectionVars []runner.KV
	if in.CollectionID != "" {
		if c, err := s.loadSettings(r.Context(), p.WorkspaceID, in.CollectionID); err == nil {
			collectionVars = c.Variables
		}
	}
	_, _, vars, _, _, err := s.resolveEnv(r.Context(), p, in.EnvironmentID, collectionVars)
	if err != nil {
		serverError(w, err)
		return nil, false
	}
	return vars, true
}

// oauth2Authorize answers {url}: where the popup signs in.
func (s *Server) oauth2Authorize(w http.ResponseWriter, r *http.Request) {
	var in oauth2In
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.State) < 16 || len(in.CodeVerifier) < 43 || len(in.CodeVerifier) > 128 {
		httpError(w, http.StatusBadRequest, "state must be at least 16 characters and codeVerifier 43 to 128")
		return
	}
	vars, ok := s.oauth2Vars(w, r, &in)
	if !ok {
		return
	}
	u, err := runner.AuthorizeURL(in.Auth, vars, in.RedirectURI, in.State, in.CodeVerifier)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": u})
}

// oauth2Token trades the code from the callback for tokens.
func (s *Server) oauth2Token(w http.ResponseWriter, r *http.Request) {
	var in oauth2In
	if !readJSON(w, r, &in) {
		return
	}
	vars, ok := s.oauth2Vars(w, r, &in)
	if !ok {
		return
	}
	ts, err := s.runner.ExchangeCode(r.Context(), in.Auth, vars, in.Code, in.CodeVerifier, in.RedirectURI)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ts)
}
