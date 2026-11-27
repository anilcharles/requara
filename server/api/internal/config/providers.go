package config

import (
	"fmt"
	"os"
	"strings"
)

// AuthProvider is one way to sign in. Requara supports any number at once.
//
// Configure them with environment variables:
//
//	REQUARA_AUTH_PROVIDERS=google,github,okta     # ids, in display order
//	REQUARA_AUTH_GOOGLE_CLIENT_ID=…               # REQUARA_AUTH_<ID>_<SETTING>
//	REQUARA_AUTH_GOOGLE_CLIENT_SECRET=…
//	REQUARA_AUTH_OKTA_TYPE=oidc
//	REQUARA_AUTH_OKTA_ISSUER=https://acme.okta.com/oauth2/default
//
// The ids google, microsoft, github and gitlab are presets: they need only a
// client ID and secret. "email" signs people in with a one-time link (needs
// SMTP). TYPE=saml makes a SAML 2.0 provider (METADATA_URL, or SSO_URL and
// IDP_CERT). Any other id is a generic OpenID Connect provider and needs an
// ISSUER. Every OAuth/OIDC provider uses the same callback URL,
// <REQUARA_FRONTEND_URL>/auth/callback; SAML uses /auth/saml/<id>/acs.
type AuthProvider struct {
	ID   string // url-safe id, e.g. "google"
	Type string // oidc | github | saml | email
	Name string // shown on the button: "Continue with <Name>"

	ClientID     string
	ClientSecret string // OpenBao secret/requara/<env>/auth "<id>_client_secret", else env; empty for public clients
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

	Product Product // the identity provider behind it (Keycloak, Okta, …), detected

	// TrustEmail treats the email this provider reports as verified even
	// without an email_verified claim. Instance admins (REQUARA_ADMIN_EMAILS)
	// are only recognized by a verified email.
	TrustEmail bool

	// SAML 2.0: the identity provider's metadata (by URL or file), or its
	// SSO URL and signing certificate.
	SAMLMetadataURL  string
	SAMLMetadataFile string
	SAMLSSOURL       string
	SAMLIdPEntityID  string
	SAMLIdPCert      string // PEM, or a path to a PEM file
	SAMLEntityID     string // this service provider's entity ID (default <frontend>/auth/saml/<id>/metadata)
	SAMLSPCertFile   string // optional: sign requests / decrypt assertions
	SAMLSPKeyFile    string
	SAMLEmailAttrs   []string // attribute names that hold the email, in order
	SAMLNameAttrs    []string
	SAMLGroupsAttrs  []string
}

// Public reports whether anyone on the internet can get an account at this
// provider (so Requara must restrict who joins by domain, invite or org).
func (p AuthProvider) Public() bool {
	switch p.Type {
	case "github":
		return len(p.AllowedOrgs) == 0
	case "oidc":
		return p.ID == "google" || p.ID == "gitlab" || (p.ID == "microsoft" && p.SkipIssuerCheck)
	case "email":
		return true // anyone with a mailbox
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
	return "REQUARA_AUTH_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_")) + "_" + setting
}

// loadProviders reads REQUARA_AUTH_PROVIDERS and each provider's settings.
// Without it, the single-provider REQUARA_OIDC_* settings still work (as id "sso").
func (c *Config) loadProviders(read func(string) (map[string]string, error)) error {
	c.AdminGroups = list(env("REQUARA_ADMIN_GROUPS", "requara-admin"))
	c.PublisherGroups = list(env("REQUARA_PUBLISHER_GROUPS", "requara-admin,api-designer"))

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
	ids := list(getenv("REQUARA_AUTH_PROVIDERS"))
	if len(ids) == 0 {
		// Single-provider settings from before REQUARA_AUTH_PROVIDERS.
		issuer := get("REQUARA_OIDC_ISSUER", legacy["issuer"])
		if issuer == "" {
			return nil, fmt.Errorf("no sign-in providers: set REQUARA_AUTH_PROVIDERS (e.g. google,github) and their REQUARA_AUTH_<ID>_* settings")
		}
		secret := legacy["client_secret"]
		if secret == "" {
			secret = getenv("REQUARA_OIDC_CLIENT_SECRET")
		}
		p := AuthProvider{
			ID: "sso", Type: "oidc", Name: get("REQUARA_OIDC_PROVIDER_NAME", "SSO"),
			Issuer:       strings.TrimRight(issuer, "/"),
			ClientID:     get("REQUARA_OIDC_CLIENT_ID", legacy["client_id"]),
			ClientSecret: secret,
			DiscoveryURL: strings.TrimRight(getenv("REQUARA_OIDC_DISCOVERY_URL"), "/"),
			Scopes:       list(get("REQUARA_OIDC_SCOPES", "openid profile email")),
			RolesClaims:  list(get("REQUARA_OIDC_ROLES_CLAIM", "roles,realm_access.roles")),
			GroupsClaims: list(get("REQUARA_OIDC_GROUPS_CLAIM", "groups")),
			AccountURL:   getenv("REQUARA_OIDC_ACCOUNT_URL"),
		}
		if p.ClientID == "" {
			return nil, fmt.Errorf("REQUARA_OIDC_CLIENT_ID is required with REQUARA_OIDC_ISSUER")
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
			Type:         strings.ToLower(or("TYPE", presetType(id))),
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
			p.Name = k("NAME") // else named after the product below
			p.Issuer = k("ISSUER")
		case p.Type == "email":
			// Sign in with a one-time link sent by email (needs SMTP).
			p.Name = or("NAME", "Email")
			p.Product = Product{"email", "Email"}
			p.TrustEmail = true
			out = append(out, p)
			continue
		case p.Type == "saml":
			p.Name = k("NAME") // named after the product below
			p.SAMLMetadataURL = k("METADATA_URL")
			p.SAMLMetadataFile = k("METADATA_FILE")
			p.SAMLSSOURL = k("SSO_URL")
			p.SAMLIdPEntityID = k("IDP_ENTITY_ID")
			p.SAMLIdPCert = or("IDP_CERT", k("IDP_CERT_FILE"))
			p.SAMLEntityID = k("ENTITY_ID")
			p.SAMLSPCertFile = k("SP_CERT_FILE")
			p.SAMLSPKeyFile = k("SP_KEY_FILE")
			p.SAMLEmailAttrs = list(or("EMAIL_ATTR", "email,mail,emailAddress,http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress,urn:oid:0.9.2342.19200300.100.1.3"))
			p.SAMLNameAttrs = list(or("NAME_ATTR", "displayName,name,http://schemas.microsoft.com/identity/claims/displayname,http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name,urn:oid:2.16.840.1.113730.3.1.241"))
			p.SAMLGroupsAttrs = list(or("GROUPS_ATTR", "groups,memberOf,http://schemas.microsoft.com/ws/2008/06/identity/claims/groups,http://schemas.microsoft.com/ws/2008/06/identity/claims/role"))
			// The company's own identity provider vouches for its people.
			p.TrustEmail = k("TRUST_EMAIL") != "false"
			if p.SAMLMetadataURL == "" && p.SAMLMetadataFile == "" && (p.SAMLSSOURL == "" || p.SAMLIdPCert == "") {
				return nil, fmt.Errorf("%s (or %s, or %s and %s) is required for the SAML provider %q",
					envKey(id, "METADATA_URL"), envKey(id, "METADATA_FILE"), envKey(id, "SSO_URL"), envKey(id, "IDP_CERT"), id)
			}
			if (p.SAMLSPCertFile == "") != (p.SAMLSPKeyFile == "") {
				return nil, fmt.Errorf("set both %s and %s, or neither", envKey(id, "SP_CERT_FILE"), envKey(id, "SP_KEY_FILE"))
			}
			p.Product = p.product()
			if p.Name == "" {
				p.Name = "SSO (SAML)"
				if p.Product.Name != "" {
					p.Name = p.Product.Name + " (SAML)"
				}
			}
			out = append(out, p)
			continue
		default:
			return nil, fmt.Errorf("%s: unknown type %q (use oidc, github, saml or email)", envKey(id, "TYPE"), p.Type)
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
		p.Product = p.product()
		if p.Name == "" {
			// No NAME: call it what it is (Keycloak, Okta, …).
			p.Name = p.Product.Name
			if p.Name == "" {
				p.Name = strings.ToUpper(id[:1]) + id[1:]
			}
		}
		switch v := k("TRUST_EMAIL"); {
		case v != "":
			p.TrustEmail = v == "true"
		case p.Type == "github":
			p.TrustEmail = true // only the primary verified email is used
		case id == "microsoft":
			// A single tenant's directory is the company's own; the multi-tenant
			// endpoints accept any tenant, whose admins can set any email.
			p.TrustEmail = !p.SkipIssuerCheck
		}
		out = append(out, p)
	}
	return out, nil
}

func presetType(id string) string {
	switch id {
	case "github", "email":
		return id
	case "saml":
		return "saml"
	}
	return "oidc"
}
