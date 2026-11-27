package config

import (
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestPresetsAndGeneric(t *testing.T) {
	ps, err := parseProviders(envMap(map[string]string{
		"REQUARA_AUTH_PROVIDERS":                "google, github,microsoft,company-sso",
		"REQUARA_AUTH_GOOGLE_CLIENT_ID":         "g",
		"REQUARA_AUTH_GITHUB_CLIENT_ID":         "gh",
		"REQUARA_AUTH_GITHUB_ALLOWED_ORGS":      "acme",
		"REQUARA_AUTH_MICROSOFT_CLIENT_ID":      "ms",
		"REQUARA_AUTH_COMPANY_SSO_CLIENT_ID":    "c",
		"REQUARA_AUTH_COMPANY_SSO_ISSUER":       "https://acme.okta.com/oauth2/default/",
		"REQUARA_AUTH_COMPANY_SSO_NAME":         "Acme SSO",
		"REQUARA_AUTH_COMPANY_SSO_GROUPS_CLAIM": "groups,teams",
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
		"REQUARA_AUTH_PROVIDERS":           "github,microsoft",
		"REQUARA_AUTH_GITHUB_CLIENT_ID":    "gh",
		"REQUARA_AUTH_GITHUB_URL":          "https://github.acme.com/",
		"REQUARA_AUTH_MICROSOFT_CLIENT_ID": "ms",
		"REQUARA_AUTH_MICROSOFT_TENANT":    "7f3c2e9a-0000-4000-8000-000000000000",
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
	ps, err := parseProviders(envMap(map[string]string{"REQUARA_OIDC_PROVIDER_NAME": "Acme SSO"}), nil,
		map[string]string{"issuer": "http://localhost:8081/realms/acme", "client_id": "requara-web", "client_secret": "s"})
	if err != nil || len(ps) != 1 || ps[0].ID != "sso" || ps[0].Name != "Acme SSO" || ps[0].ClientSecret != "s" {
		t.Fatalf("legacy: %+v %v", ps, err)
	}
}

func TestErrors(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"nothing":        {},
		"no client id":   {"REQUARA_AUTH_PROVIDERS": "google"},
		"oidc no issuer": {"REQUARA_AUTH_PROVIDERS": "okta", "REQUARA_AUTH_OKTA_CLIENT_ID": "x"},
		"bad type":       {"REQUARA_AUTH_PROVIDERS": "x", "REQUARA_AUTH_X_TYPE": "saml", "REQUARA_AUTH_X_CLIENT_ID": "x"},
	} {
		if _, err := parseProviders(envMap(env), nil, nil); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestProductNames(t *testing.T) {
	ps, err := parseProviders(envMap(map[string]string{
		"REQUARA_AUTH_PROVIDERS":         "company,corp,okta,custom",
		"REQUARA_AUTH_COMPANY_ISSUER":    "http://localhost:8081/realms/acme",
		"REQUARA_AUTH_COMPANY_CLIENT_ID": "requara-web",
		"REQUARA_AUTH_CORP_TYPE":         "saml",
		"REQUARA_AUTH_CORP_METADATA_URL": "http://idp:8080/realms/acme/protocol/saml/descriptor",
		"REQUARA_AUTH_OKTA_ISSUER":       "https://acme.okta.com/oauth2/default",
		"REQUARA_AUTH_OKTA_CLIENT_ID":    "x",
		"REQUARA_AUTH_OKTA_NAME":         "Acme SSO",
		"REQUARA_AUTH_CUSTOM_ISSUER":     "https://id.internal.example/",
		"REQUARA_AUTH_CUSTOM_CLIENT_ID":  "x",
	}), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ name, product string }{{"Keycloak", "keycloak"}, {"Keycloak (SAML)", "keycloak"}, {"Acme SSO", "okta"}, {"Custom", ""}}
	for i, w := range want {
		if ps[i].Name != w.name || ps[i].Product.ID != w.product {
			t.Errorf("%s: got %q/%q, want %q/%q", ps[i].ID, ps[i].Name, ps[i].Product.ID, w.name, w.product)
		}
	}
}
