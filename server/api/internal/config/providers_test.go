package config

import (
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestPresetsAndGeneric(t *testing.T) {
	ps, err := parseProviders(envMap(map[string]string{
		"SPEKLO_AUTH_PROVIDERS":                "google, github,microsoft,company-sso",
		"SPEKLO_AUTH_GOOGLE_CLIENT_ID":         "g",
		"SPEKLO_AUTH_GITHUB_CLIENT_ID":         "gh",
		"SPEKLO_AUTH_GITHUB_ALLOWED_ORGS":      "acme",
		"SPEKLO_AUTH_MICROSOFT_CLIENT_ID":      "ms",
		"SPEKLO_AUTH_COMPANY_SSO_CLIENT_ID":    "c",
		"SPEKLO_AUTH_COMPANY_SSO_ISSUER":       "https://acme.okta.com/oauth2/default/",
		"SPEKLO_AUTH_COMPANY_SSO_NAME":         "Acme SSO",
		"SPEKLO_AUTH_COMPANY_SSO_GROUPS_CLAIM": "groups,teams",
	}), map[string]string{"google_client_secret": "from-openbao"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 4 {
		t.Fatalf("got %d providers", len(ps))
	}
	g, gh, ms, c := ps[0], ps[1], ps[2], ps[3]
	if g.Issuer != "https://accounts.google.com" || g.Name != "Google" || g.ClientSecret != "from-openbao" || !g.Public() {
		t.Errorf("google preset: %+v", g)
	}
	if gh.Type != "github" || gh.GitHubAPI != "https://api.github.com" || gh.Public() || gh.AllowedOrgs[0] != "acme" {
		t.Errorf("github preset: %+v", gh)
	}
	if ms.Issuer != "https://login.microsoftonline.com/organizations/v2.0" || !ms.SkipIssuerCheck {
		t.Errorf("microsoft preset: %+v", ms)
	}
	if c.Type != "oidc" || c.Issuer != "https://acme.okta.com/oauth2/default" || c.Name != "Acme SSO" || strings.Join(c.GroupsClaims, ",") != "groups,teams" || c.Public() {
		t.Errorf("generic oidc: %+v", c)
	}
}

func TestGitHubEnterpriseAndSingleTenantMicrosoft(t *testing.T) {
	ps, err := parseProviders(envMap(map[string]string{
		"SPEKLO_AUTH_PROVIDERS":           "github,microsoft",
		"SPEKLO_AUTH_GITHUB_CLIENT_ID":    "gh",
		"SPEKLO_AUTH_GITHUB_URL":          "https://github.acme.com/",
		"SPEKLO_AUTH_MICROSOFT_CLIENT_ID": "ms",
		"SPEKLO_AUTH_MICROSOFT_TENANT":    "7f3c2e9a-0000-4000-8000-000000000000",
	}), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ps[0].GitHubURL != "https://github.acme.com" || ps[0].GitHubAPI != "https://github.acme.com/api/v3" {
		t.Errorf("enterprise: %+v", ps[0])
	}
	if ps[1].SkipIssuerCheck || ps[1].Public() {
		t.Errorf("single-tenant microsoft should check the issuer and not be public: %+v", ps[1])
	}
}

func TestLegacySingleProvider(t *testing.T) {
	ps, err := parseProviders(envMap(map[string]string{"SPEKLO_OIDC_PROVIDER_NAME": "Acme SSO"}), nil,
		map[string]string{"issuer": "http://localhost:8081/realms/acme", "client_id": "speklo-web", "client_secret": "s"})
	if err != nil || len(ps) != 1 || ps[0].ID != "sso" || ps[0].Name != "Acme SSO" || ps[0].ClientSecret != "s" {
		t.Fatalf("legacy: %+v %v", ps, err)
	}
}

func TestErrors(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"nothing":        {},
		"no client id":   {"SPEKLO_AUTH_PROVIDERS": "google"},
		"oidc no issuer": {"SPEKLO_AUTH_PROVIDERS": "okta", "SPEKLO_AUTH_OKTA_CLIENT_ID": "x"},
		"bad type":       {"SPEKLO_AUTH_PROVIDERS": "x", "SPEKLO_AUTH_X_TYPE": "saml", "SPEKLO_AUTH_X_CLIENT_ID": "x"},
	} {
		if _, err := parseProviders(envMap(env), nil, nil); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
