package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/anilcharles/speklo/server/api/internal/config"
)

// oidcProvider signs in with an OpenID Connect provider. Discovery happens on
// first use (and is retried on the next sign-in if the provider was down).
type oidcProvider struct {
	cfg      config.AuthProvider
	redirect string

	mu         sync.Mutex
	provider   *oidc.Provider
	verifier   *oidc.IDTokenVerifier
	endSession string
}

func newOIDC(cfg config.AuthProvider, redirect string) *oidcProvider {
	return &oidcProvider{cfg: cfg, redirect: redirect}
}

func (o *oidcProvider) info() config.AuthProvider { return o.cfg }

func (o *oidcProvider) discover(ctx context.Context) (*oidc.Provider, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider != nil {
		return o.provider, nil
	}
	from := o.cfg.Issuer
	if o.cfg.DiscoveryURL != "" && o.cfg.DiscoveryURL != o.cfg.Issuer {
		// The backend reaches the provider at another address than browsers.
		from = o.cfg.DiscoveryURL
		ctx = oidc.InsecureIssuerURLContext(ctx, o.cfg.Issuer)
	} else if o.cfg.SkipIssuerCheck {
		// Microsoft multi-tenant: the metadata names "{tenantid}" as issuer.
		ctx = oidc.InsecureIssuerURLContext(ctx, o.cfg.Issuer)
	}
	p, err := oidc.NewProvider(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("can't reach %s (%s): %w", o.cfg.Name, o.cfg.Issuer, err)
	}
	var meta struct {
		EndSession string `json:"end_session_endpoint"`
	}
	_ = p.Claims(&meta)
	o.provider = p
	o.verifier = p.Verifier(&oidc.Config{ClientID: o.cfg.ClientID, SkipIssuerCheck: o.cfg.SkipIssuerCheck})
	o.endSession = meta.EndSession
	return p, nil
}

func (o *oidcProvider) oauth(p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID: o.cfg.ClientID, ClientSecret: o.cfg.ClientSecret, RedirectURL: o.redirect,
		Endpoint: p.Endpoint(), Scopes: o.cfg.Scopes,
	}
}

func (o *oidcProvider) authURL(ctx context.Context, st *loginState, loginHint string) (string, error) {
	p, err := o.discover(ctx)
	if err != nil {
		return "", err
	}
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(st.Verifier), oidc.Nonce(st.Nonce)}
	if loginHint != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", loginHint))
	}
	return o.oauth(p).AuthCodeURL(st.State, opts...), nil
}

func (o *oidcProvider) exchange(ctx context.Context, code string, st *loginState) (*Claims, string, error) {
	p, err := o.discover(ctx)
	if err != nil {
		return nil, "", err
	}
	tok, err := o.oauth(p).Exchange(ctx, code, oauth2.VerifierOption(st.Verifier))
	if err != nil {
		return nil, "", fmt.Errorf("%s code exchange: %w", o.cfg.Name, err)
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		return nil, "", fmt.Errorf("%s sent no id_token", o.cfg.Name)
	}
	idt, err := o.verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, "", fmt.Errorf("verify %s id_token: %w", o.cfg.Name, err)
	}
	var std struct {
		Subject       string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
		Name          string `json:"name"`
		Username      string `json:"preferred_username"`
		GivenName     string `json:"given_name"`
		FamilyName    string `json:"family_name"`
		Nonce         string `json:"nonce"`
	}
	if err := idt.Claims(&std); err != nil {
		return nil, "", err
	}
	if std.Nonce != st.Nonce {
		return nil, "", errors.New("nonce mismatch")
	}
	var all map[string]any
	_ = idt.Claims(&all)
	// Some providers put groups and email only in userinfo (Okta, Entra ID).
	// Ask for it, and let the ID token win where both have a claim.
	if ui, err := p.UserInfo(ctx, oauth2.StaticTokenSource(tok)); err == nil {
		var extra map[string]any
		if ui.Claims(&extra) == nil {
			for k, v := range extra {
				if _, ok := all[k]; !ok {
					all[k] = v
				}
			}
		}
	}
	cl := &Claims{
		Subject: std.Subject, Email: std.Email, Name: std.Name, Username: std.Username,
		GivenName: std.GivenName, FamilyName: std.FamilyName,
		Roles: claimStrings(all, o.cfg.RolesClaims), Groups: claimStrings(all, o.cfg.GroupsClaims),
	}
	if cl.Email == "" {
		cl.Email, _ = all["email"].(string)
	}
	if v, ok := all["email_verified"].(bool); ok && !v {
		return nil, "", fmt.Errorf("verify your email address with %s first", o.cfg.Name)
	}
	if cl.Email == "" && strings.Contains(cl.Username, "@") {
		cl.Email = cl.Username // Entra ID: preferred_username is the UPN
	}
	return cl, rawID, nil
}

func (o *oidcProvider) logoutURL(idToken, returnTo string) string {
	o.mu.Lock()
	end := o.endSession
	o.mu.Unlock()
	if end == "" {
		return returnTo
	}
	v := url.Values{"post_logout_redirect_uri": {returnTo}, "client_id": {o.cfg.ClientID}}
	if idToken != "" {
		v.Set("id_token_hint", idToken)
	}
	return end + "?" + v.Encode()
}
