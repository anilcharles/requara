package config

import (
	"fmt"
	"slices"
	"strings"
)

// Instance admins are named by whoever runs the server, never chosen in the
// browser. Any of these sources can list them (they add up):
//
//	REQUARA_ADMIN_EMAILS=ana@acme.com,ops@acme.com
//	REQUARA_ADMIN_EMAILS_FILE=/run/secrets/requara-admins   # Docker or Kubernetes secret
//	OpenBao: secret/requara/<env>/admins, key "emails"
//
// The file holds emails separated by commas, spaces or new lines; lines
// starting with # are ignored. An admin is recognized when they sign in with
// that email and their provider has verified it. Removing someone from the
// list takes their admin rights away at the next start.

// AdminSource tells the admin console where the list came from.
type AdminSource struct {
	Kind string `json:"kind"` // env | file | openbao
	Name string `json:"name"` // variable name, file path or secret path
}

func (c *Config) loadAdmins(getenv func(string) string, readFile func(string) ([]byte, error), bao map[string]string, baoPath string) error {
	add := func(raw string, src AdminSource) error {
		found := false
		for _, line := range strings.Split(raw, "\n") {
			if t := strings.TrimSpace(line); strings.HasPrefix(t, "#") {
				continue
			}
			for _, e := range list(line) {
				e = strings.ToLower(strings.TrimSpace(e))
				if e == "" {
					continue
				}
				if at := strings.Index(e, "@"); at <= 0 || at == len(e)-1 {
					return fmt.Errorf("%s: %q is not an email address", src.Name, e)
				}
				found = true
				if !slices.Contains(c.Admins, e) {
					c.Admins = append(c.Admins, e)
				}
			}
		}
		if found {
			c.AdminSources = append(c.AdminSources, src)
		}
		return nil
	}
	if v := getenv("REQUARA_ADMIN_EMAILS"); v != "" {
		if err := add(v, AdminSource{"env", "REQUARA_ADMIN_EMAILS"}); err != nil {
			return err
		}
	}
	if path := getenv("REQUARA_ADMIN_EMAILS_FILE"); path != "" {
		b, err := readFile(path)
		if err != nil {
			return fmt.Errorf("REQUARA_ADMIN_EMAILS_FILE: %w", err)
		}
		if err := add(string(b), AdminSource{"file", path}); err != nil {
			return err
		}
	}
	if v := bao["emails"]; v != "" {
		if err := add(v, AdminSource{"openbao", baoPath}); err != nil {
			return err
		}
	}
	return nil
}

// IsAdminEmail reports whether email is on the operator's admin list.
func (c *Config) IsAdminEmail(email string) bool {
	return slices.Contains(c.Admins, strings.ToLower(strings.TrimSpace(email)))
}
