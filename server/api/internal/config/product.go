package config

import (
	"net/url"
	"strings"
)

// Product is the identity provider behind a sign-in option, so sign-in
// screens can say "Keycloak" or "Okta" even when an admin picks another
// button name.
type Product struct {
	ID   string // keycloak, okta, microsoft, …; "" when unknown
	Name string // Keycloak, Okta, Microsoft Entra ID, …
}

var products = []struct {
	id, name string
	match    func(host, path string) bool
}{
	{"keycloak", "Keycloak", func(h, p string) bool { return strings.Contains(p, "/realms/") }},
	{"okta", "Okta", func(h, p string) bool {
		return strings.HasSuffix(h, ".okta.com") || strings.HasSuffix(h, ".oktapreview.com") || strings.HasSuffix(h, ".okta-emea.com")
	}},
	{"auth0", "Auth0", func(h, p string) bool { return strings.HasSuffix(h, ".auth0.com") }},
	{"microsoft", "Microsoft Entra ID", func(h, p string) bool {
		return h == "login.microsoftonline.com" || h == "sts.windows.net" || strings.HasSuffix(h, ".ciamlogin.com") || strings.HasSuffix(h, ".b2clogin.com")
	}},
	{"google", "Google", func(h, p string) bool { return h == "accounts.google.com" }},
	{"gitlab", "GitLab", func(h, p string) bool { return h == "gitlab.com" }},
	{"authentik", "authentik", func(h, p string) bool {
		return strings.Contains(p, "/application/o/") || strings.Contains(p, "/application/saml/")
	}},
	{"zitadel", "ZITADEL", func(h, p string) bool {
		return strings.HasSuffix(h, ".zitadel.cloud") || strings.Contains(h, "zitadel")
	}},
	{"onelogin", "OneLogin", func(h, p string) bool { return strings.HasSuffix(h, ".onelogin.com") }},
	{"jumpcloud", "JumpCloud", func(h, p string) bool { return strings.HasSuffix(h, ".jumpcloud.com") }},
	{"ping", "Ping Identity", func(h, p string) bool { return strings.Contains(h, "pingone") || strings.Contains(h, "pingidentity") }},
	{"cognito", "Amazon Cognito", func(h, p string) bool {
		return strings.HasSuffix(h, ".amazoncognito.com") || strings.HasPrefix(h, "cognito-idp.")
	}},
	{"adfs", "AD FS", func(h, p string) bool { return strings.HasPrefix(p, "/adfs") }},
	{"dex", "Dex", func(h, p string) bool { return strings.HasPrefix(p, "/dex") }},
	{"casdoor", "Casdoor", func(h, p string) bool { return strings.Contains(h, "casdoor") }},
	{"fusionauth", "FusionAuth", func(h, p string) bool { return strings.Contains(h, "fusionauth") }},
}

// DetectProduct recognizes the identity provider from its addresses.
func DetectProduct(urls ...string) Product {
	for _, raw := range urls {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		h, p := strings.ToLower(u.Hostname()), strings.ToLower(u.Path)
		for _, x := range products {
			if x.match(h, p) {
				return Product{x.id, x.name}
			}
		}
	}
	return Product{}
}

// product is the provider's identity product, for its button and its default name.
func (p AuthProvider) product() Product {
	switch p.Type {
	case "github":
		if p.GitHubURL != "" && p.GitHubURL != "https://github.com" {
			return Product{"github", "GitHub Enterprise"}
		}
		return Product{"github", "GitHub"}
	case "email":
		return Product{"email", "Email"}
	case "saml":
		return DetectProduct(p.SAMLMetadataURL, p.SAMLSSOURL, p.SAMLIdPEntityID)
	}
	switch p.ID {
	case "google":
		return Product{"google", "Google"}
	case "microsoft":
		return Product{"microsoft", "Microsoft"}
	case "gitlab":
		return Product{"gitlab", "GitLab"}
	}
	return DetectProduct(p.Issuer, p.DiscoveryURL)
}
