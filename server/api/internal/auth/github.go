package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/oauth2"

	"github.com/anilcharles/requara/server/api/internal/config"
)

// githubProvider signs in with GitHub (github.com or GitHub Enterprise
// Server). GitHub speaks OAuth 2, not OpenID Connect, so the person's
// details come from its REST API: the profile, the primary verified email,
// and organizations and teams as groups ("acme", "acme/platform").
type githubProvider struct {
	cfg   config.AuthProvider
	oauth *oauth2.Config
	http  *http.Client
}

func newGitHub(cfg config.AuthProvider, redirect string) *githubProvider {
	return &githubProvider{
		cfg: cfg,
		oauth: &oauth2.Config{
			ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, RedirectURL: redirect, Scopes: cfg.Scopes,
			Endpoint: oauth2.Endpoint{
				AuthURL:  cfg.GitHubURL + "/login/oauth/authorize",
				TokenURL: cfg.GitHubURL + "/login/oauth/access_token",
			},
		},
		http: http.DefaultClient,
	}
}

func (g *githubProvider) info() config.AuthProvider { return g.cfg }

func (g *githubProvider) authURL(_ context.Context, st *loginState, loginHint string) (string, error) {
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(st.Verifier)}
	if loginHint != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login", loginHint))
	}
	return g.oauth.AuthCodeURL(st.State, opts...), nil
}

func (g *githubProvider) get(ctx context.Context, tok *oauth2.Token, path string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, g.cfg.GitHubAPI+path, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	res, err := g.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub %s: %s", path, res.Status)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func (g *githubProvider) exchange(ctx context.Context, code string, st *loginState) (*Claims, string, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, g.http)
	tok, err := g.oauth.Exchange(ctx, code, oauth2.VerifierOption(st.Verifier))
	if err != nil {
		return nil, "", fmt.Errorf("GitHub code exchange: %w", err)
	}
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err := g.get(ctx, tok, "/user", &user); err != nil {
		return nil, "", err
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := g.get(ctx, tok, "/user/emails", &emails); err != nil {
		return nil, "", err
	}
	cl := &Claims{Subject: strconv.FormatInt(user.ID, 10), Username: user.Login, Name: user.Name}
	for _, e := range emails {
		if e.Primary && e.Verified {
			cl.Email = e.Email
			cl.EmailVerified = true
		}
	}
	if cl.Email == "" {
		return nil, "", fmt.Errorf("your GitHub account has no verified primary email")
	}

	// Organizations and teams become groups. They need the read:org scope;
	// without it GitHub just returns nothing.
	var orgs []struct {
		Login string `json:"login"`
	}
	_ = g.get(ctx, tok, "/user/orgs?per_page=100", &orgs)
	for _, o := range orgs {
		cl.Groups = append(cl.Groups, o.Login)
	}
	var teams []struct {
		Slug string `json:"slug"`
		Org  struct {
			Login string `json:"login"`
		} `json:"organization"`
	}
	_ = g.get(ctx, tok, "/user/teams?per_page=100", &teams)
	for _, t := range teams {
		cl.Groups = append(cl.Groups, t.Org.Login+"/"+t.Slug)
	}
	if len(g.cfg.AllowedOrgs) > 0 && !slices.ContainsFunc(orgs, func(o struct {
		Login string `json:"login"`
	}) bool {
		return slices.ContainsFunc(g.cfg.AllowedOrgs, func(a string) bool { return strings.EqualFold(a, o.Login) })
	}) {
		return nil, "", fmt.Errorf("sign in with a GitHub account in %s (and allow Requara to see that organization)",
			strings.Join(g.cfg.AllowedOrgs, " or "))
	}
	return cl, "", nil
}

// GitHub has no single sign-out to send people to.
func (g *githubProvider) logoutURL(_, returnTo string) string { return returnTo }
