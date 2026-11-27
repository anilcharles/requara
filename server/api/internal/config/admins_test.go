package config

import (
	"errors"
	"slices"
	"testing"
)

func TestAdminsFromEnvFileAndOpenBao(t *testing.T) {
	files := map[string]string{"/run/secrets/admins": "# Requara admins\nOps@Acme.com\n  sec@acme.com, ana@acme.com\n"}
	read := func(p string) ([]byte, error) {
		if v, ok := files[p]; ok {
			return []byte(v), nil
		}
		return nil, errors.New("no such file")
	}
	c := &Config{}
	err := c.loadAdmins(envMap(map[string]string{
		"REQUARA_ADMIN_EMAILS":      "ana@acme.com, Root@Acme.com",
		"REQUARA_ADMIN_EMAILS_FILE": "/run/secrets/admins",
	}), read, map[string]string{"emails": "bao@acme.com"}, "secret/requara/prod/admins")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ana@acme.com", "root@acme.com", "ops@acme.com", "sec@acme.com", "bao@acme.com"}
	if !slices.Equal(c.Admins, want) {
		t.Fatalf("admins = %v, want %v", c.Admins, want)
	}
	if len(c.AdminSources) != 3 || c.AdminSources[1].Kind != "file" || c.AdminSources[2].Kind != "openbao" {
		t.Errorf("sources = %+v", c.AdminSources)
	}
	if !c.IsAdminEmail(" OPS@acme.com ") || c.IsAdminEmail("someone@acme.com") {
		t.Error("IsAdminEmail")
	}
}

func TestAdminsRejectsBadEntriesAndMissingFile(t *testing.T) {
	c := &Config{}
	if err := c.loadAdmins(envMap(map[string]string{"REQUARA_ADMIN_EMAILS": "ana@acme.com,not-an-email"}), nil, nil, ""); err == nil {
		t.Error("want an error for a non-email entry")
	}
	c = &Config{}
	read := func(string) ([]byte, error) { return nil, errors.New("missing") }
	if err := c.loadAdmins(envMap(map[string]string{"REQUARA_ADMIN_EMAILS_FILE": "/nope"}), read, nil, ""); err == nil {
		t.Error("want an error when the admin file can't be read")
	}
	c = &Config{}
	if err := c.loadAdmins(envMap(nil), nil, nil, ""); err != nil || len(c.Admins) != 0 {
		t.Errorf("no admins configured: %v %v", c.Admins, err)
	}
}

func TestEmailAndSAMLProviders(t *testing.T) {
	ps, err := parseProviders(envMap(map[string]string{
		"REQUARA_AUTH_PROVIDERS":              "email,okta-saml,microsoft",
		"REQUARA_AUTH_OKTA_SAML_TYPE":         "saml",
		"REQUARA_AUTH_OKTA_SAML_NAME":         "Okta",
		"REQUARA_AUTH_OKTA_SAML_METADATA_URL": "https://acme.okta.com/app/abc/sso/saml/metadata",
		"REQUARA_AUTH_OKTA_SAML_GROUPS_ATTR":  "groups",
		"REQUARA_AUTH_MICROSOFT_CLIENT_ID":    "ms",
		"REQUARA_AUTH_MICROSOFT_TENANT":       "1f2e3d4c-0000-1111-2222-333344445555",
	}), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	email, saml, ms := ps[0], ps[1], ps[2]
	if email.Type != "email" || email.Name != "Email" || !email.TrustEmail || !email.Public() {
		t.Errorf("email: %+v", email)
	}
	if saml.Type != "saml" || saml.Name != "Okta" || !saml.TrustEmail || saml.SAMLMetadataURL == "" || saml.SAMLGroupsAttrs[0] != "groups" {
		t.Errorf("saml: %+v", saml)
	}
	if ms.SkipIssuerCheck || !ms.TrustEmail {
		t.Errorf("single-tenant Microsoft should trust its emails: %+v", ms)
	}

	if _, err := parseProviders(envMap(map[string]string{"REQUARA_AUTH_PROVIDERS": "corp", "REQUARA_AUTH_CORP_TYPE": "saml"}), nil, nil); err == nil {
		t.Error("SAML without metadata or SSO URL + certificate should fail")
	}
	multi, _ := parseProviders(envMap(map[string]string{"REQUARA_AUTH_PROVIDERS": "microsoft", "REQUARA_AUTH_MICROSOFT_CLIENT_ID": "ms"}), nil, nil)
	if multi[0].TrustEmail {
		t.Error("multi-tenant Microsoft emails must not be trusted")
	}
}
