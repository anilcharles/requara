package config

import (
	"fmt"
	"os"
	"strings"
)

// AuthProvider is one way to sign in. Speklo supports any number at once.
//
// Configure them with environment variables:
//
//	SPEKLO_AUTH_PROVIDERS=google,github,okta     # ids, in display order
//	SPEKLO_AUTH_GOOGLE_CLIENT_ID=…               # SPEKLO_AUTH_<ID>_<SETTING>
//	SPEKLO_AUTH_GOOGLE_CLIENT_SECRET=…
//	SPEKLO_AUTH_OKTA_TYPE=oidc
//	SPEKLO_AUTH_OKTA_ISSUER=https://acme.okta.com/oauth2/default
//
// The ids google, microsoft, github and gitlab are presets: they need only a
// client ID and secret. Any other id is a generic OpenID Connect provider and
// needs an ISSUER. Every provider uses the same callback URL:
// <SPEKLO_FRONTEND_URL>/auth/callback.
type AuthProvider struct {
	ID   string // url-safe id, e.g. "google"
	Type string // oidc | github
	Name string // shown on the button: "Continue with <Name>"

	ClientID     string
	ClientSecret string // OpenBao secret/speklo/<env>/auth "<id>_client_secret", else env; empty for public clients
	Scopes       []string

	// OIDC
	Issuer          string
	DiscoveryURL    string   // where the backend fetches the metadata, if not from Issuer
	SkipIssuerCheck bool     // Microsoft multi-tenant ("organizations", "common")
	RolesClaims     []string // claim paths, e.g. "roles", "realm_access.roles"
	GroupsClaims    []string // claim paths, e.g. "groups"

	// GitHub (OAuth, not OIDC)
	GitHubURL   string   // https://github.com, or a GitHub Enterprise Server URL
	GitHubAPI   string   // https://api.github.com, or <GitHubURL>/api/v3
	AllowedOrgs []string // only members of these organizations may sign in

	AccountURL string // "Manage account" link on the profile page (optional)
}

// Public reports whether anyone on the internet can get an account at this
// provider (so Speklo must restrict who joins by domain, invite or org).
func (p AuthProvider) Public() bool {
	switch p.Type {
	case "github":
		return len(p.AllowedOrgs) == 0
	case "oidc":
		return p.ID == "google" || p.ID == "gitlab" || (p.ID == "microsoft" && p.SkipIssuerCheck)
	}
	return false
}

func list(s string) []string {
	var out []string
	for _, v := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		out = append(out, v)
	}
	return out
}

func envKey(id, setting string) string {
	return "SPEKLO_AUTH_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_" + setting
}

// loadProviders reads SPEKLO_AUTH_PROVIDERS and each provider's settings.
// Without it, the single-provider SPEKLO_OIDC_* settings still work (as id "sso").
func (c *Config) loadProviders(read func(string) (map[string]string, error)) error {
	c.AdminGroups = list(env("SPEKLO_ADMIN_GROUPS", "speklo-admin"))
	c.PublisherGroups = list(env("SPEKLO_PUBLISHER_GROUPS", "speklo-admin,api-designer"))

	secrets, _ := read("auth") // optional: "<id>_client_secret" per provider
	if secrets == nil {
		secrets = map[string]string{}
	}
	legacy, _ := read("oidc") // optional: older single-provider settings
	if legacy == nil {
		legacy = map[string]string{}
	}
	providers, err := parseProviders(os.Getenv, secrets, legacy)
	if err != nil {
		return err
	}
	c.Providers = providers
	return nil
}

// parseProviders is loadProviders without OpenBao, for tests.
func parseProviders(getenv func(string) string, secrets, legacy map[string]string) ([]AuthProvider, error) {
	get := func(k, def string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return def
	}
	ids := list(getenv("SPEKLO_AUTH_PROVIDERS"))
	if len(ids) == 0 {
		// Single-provider settings from before SPEKLO_AUTH_PROVIDERS.
		issuer := get("SPEKLO_OIDC_ISSUER", legacy["issuer"])
		if issuer == "" {
			return nil, fmt.Errorf("no sign-in providers: set SPEKLO_AUTH_PROVIDERS (e.g. google,github) and their SPEKLO_AUTH_<ID>_* settings")
		}
		secret := legacy["client_secret"]
		if secret == "" {
			secret = getenv("SPEKLO_OIDC_CLIENT_SECRET")
		}
		p := AuthProvider{
			ID: "sso", Type: "oidc", Name: get("SPEKLO_OIDC_PROVIDER_NAME", "SSO"),
			Issuer:       strings.TrimRight(issuer, "/"),
			ClientID:     get("SPEKLO_OIDC_CLIENT_ID", legacy["client_id"]),
			ClientSecret: secret,
			DiscoveryURL: strings.TrimRight(getenv("SPEKLO_OIDC_DISCOVERY_URL"), "/"),
			Scopes:       list(get("SPEKLO_OIDC_SCOPES", "openid profile email")),
			RolesClaims:  list(get("SPEKLO_OIDC_ROLES_CLAIM", "roles,realm_access.roles")),
			GroupsClaims: list(get("SPEKLO_OIDC_GROUPS_CLAIM", "groups")),
			AccountURL:   getenv("SPEKLO_OIDC_ACCOUNT_URL"),
		}
		if p.ClientID == "" {
			return nil, fmt.Errorf("SPEKLO_OIDC_CLIENT_ID is required with SPEKLO_OIDC_ISSUER")
		}
		return []AuthProvider{p}, nil
	}

	var out []AuthProvider
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.ToLower(id)
		if seen[id] {
			continue
		}
		seen[id] = true
		k := func(setting string) string { return getenv(envKey(id, setting)) }
		or := func(setting, def string) string {
			if v := k(setting); v != "" {
				return v
			}
			return def
		}
		p := AuthProvider{
			ID:           id,
			Type:         or("TYPE", presetType(id)),
			ClientID:     k("CLIENT_ID"),
			ClientSecret: secrets[id+"_client_secret"],
			DiscoveryURL: strings.TrimRight(k("DISCOVERY_URL"), "/"),
			AccountURL:   k("ACCOUNT_URL"),
		}
		if p.ClientSecret == "" {
			p.ClientSecret = k("CLIENT_SECRET")
		}
		base := strings.TrimRight(k("URL"), "/") // self-hosted GitLab / GitHub Enterprise
		switch {
		case p.Type == "github":
			p.Name = or("NAME", "GitHub")
			p.GitHubURL = or("URL", "https://github.com")
			p.GitHubAPI = "https://api.github.com"
			if base != "" {
				p.GitHubAPI = base + "/api/v3"
			}
			p.GitHubAPI = strings.TrimRight(or("API_URL", p.GitHubAPI), "/")
			p.GitHubURL = strings.TrimRight(p.GitHubURL, "/")
			p.Scopes = list(or("SCOPES", "read:user user:email read:org"))
			p.AllowedOrgs = list(k("ALLOWED_ORGS"))
		case p.Type == "oidc" && id == "google":
			p.Name = or("NAME", "Google")
			p.Issuer = "https://accounts.google.com"
			p.AccountURL = or("ACCOUNT_URL", "https://myaccount.google.com")
		case p.Type == "oidc" && id == "microsoft":
			p.Name = or("NAME", "Microsoft")
			tenant := or("TENANT", "organizations")
			p.Issuer = "https://login.microsoftonline.com/" + tenant + "/v2.0"
			// Multi-tenant endpoints issue tokens naming each user's own tenant.
			p.SkipIssuerCheck = tenant == "organizations" || tenant == "common" || tenant == "consumers"
			p.AccountURL = or("ACCOUNT_URL", "https://myaccount.microsoft.com")
		case p.Type == "oidc" && id == "gitlab":
			p.Name = or("NAME", "GitLab")
			p.Issuer = or("URL", "https://gitlab.com")
		case p.Type == "oidc":
			p.Name = or("NAME", strings.ToUpper(id[:1])+id[1:])
			p.Issuer = k("ISSUER")
		default:
			return nil, fmt.Errorf("%s: unknown type %q (use oidc or github)", envKey(id, "TYPE"), p.Type)
		}
		if p.Type == "oidc" {
			if iss := k("ISSUER"); iss != "" {
				p.Issuer = iss
			}
			p.Issuer = strings.TrimRight(p.Issuer, "/")
			p.Scopes = list(or("SCOPES", "openid profile email"))
			groups := "groups"
			if id == "gitlab" {
				groups = "groups,groups_direct"
			}
			p.RolesClaims = list(or("ROLES_CLAIM", "roles,realm_access.roles"))
			p.GroupsClaims = list(or("GROUPS_CLAIM", groups))
			if p.Issuer == "" {
				return nil, fmt.Errorf("%s is required for the %q provider", envKey(id, "ISSUER"), id)
			}
		}
		if p.ClientID == "" {
			return nil, fmt.Errorf("%s is required", envKey(id, "CLIENT_ID"))
		}
		out = append(out, p)
	}
	return out, nil
}

func presetType(id string) string {
	if id == "github" {
		return "github"
	}
	return "oidc"
}
