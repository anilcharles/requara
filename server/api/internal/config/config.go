// Package config loads the backend's settings. Plain settings come from the
// environment; every secret comes from OpenBao, so none live in code or Git.
package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/anilcharles/requara/server/api/internal/bao"
)

type Config struct {
	Env                 string // dev | prod
	Addr                string // listen address, e.g. :8080
	FrontendURL         string // where the browser app lives
	PublicURL           string // where mock servers are reachable
	AllowPrivateNetwork bool   // let the request runner call localhost/private IPs (dev only)
	OrgName             string // organization shown on the sign-in page
	StaticDir           string // built web UI to serve at / (the Docker image sets it); empty = API only

	DatabaseURL string

	// Sign-in providers, in the order the sign-in screens list them.
	Providers []AuthProvider

	// Who may join: email domains ("*" = anyone the providers let in). People
	// with a pending invite or an existing account can always sign in.
	AllowedDomains []string

	// Who gets what, matched against the person's roles and groups from the
	// identity provider (exact name, or the last segment of a group path).
	AdminGroups     []string // become workspace owners
	PublisherGroups []string // may publish API versions

	// Instance admins: lower-cased emails from REQUARA_ADMIN_EMAILS,
	// REQUARA_ADMIN_EMAILS_FILE and OpenBao (see admins.go).
	Admins       []string
	AdminSources []AdminSource

	AuditRetentionDays int // 0 = keep forever

	SessionKey []byte

	S3Endpoint, S3AccessKey, S3SecretKey, S3Bucket string

	SMTPAddr, SMTPFrom string
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// Load signs in to OpenBao with AppRole and reads secret/requara/<env>/*.
func Load(ctx context.Context) (*Config, *bao.Client, error) {
	c := &Config{
		Env:         env("REQUARA_ENV", "dev"),
		Addr:        env("REQUARA_ADDR", ":8080"),
		FrontendURL: strings.TrimRight(env("REQUARA_FRONTEND_URL", "http://localhost:5173"), "/"),
		PublicURL:   strings.TrimRight(env("REQUARA_PUBLIC_URL", "http://localhost:8080"), "/"),
		OrgName:     env("REQUARA_ORG_NAME", "Requara"),
		StaticDir:   os.Getenv("REQUARA_STATIC_DIR"),
	}
	// REQUARA_SSO_DOMAINS is the older name.
	for _, d := range list(env("REQUARA_ALLOWED_DOMAINS", env("REQUARA_SSO_DOMAINS", "*"))) {
		c.AllowedDomains = append(c.AllowedDomains, strings.ToLower(d))
	}
	switch os.Getenv("REQUARA_ALLOW_PRIVATE_NETWORK") {
	case "true":
		c.AllowPrivateNetwork = true
	case "":
		// In dev, Send must reach the built-in mock servers on localhost.
		c.AllowPrivateNetwork = c.Env == "dev"
	}

	roleID, secretID := os.Getenv("BAO_ROLE_ID"), os.Getenv("BAO_SECRET_ID")
	var err error
	if roleID == "" {
		if roleID, err = readFile(env("BAO_ROLE_ID_FILE", "../../.dev/approle-role-id")); err != nil {
			return nil, nil, fmt.Errorf("openbao role id: %w (run scripts/dev-up.sh)", err)
		}
	}
	if secretID == "" {
		if secretID, err = readFile(env("BAO_SECRET_ID_FILE", "../../.dev/approle-secret-id")); err != nil {
			return nil, nil, fmt.Errorf("openbao secret id: %w (run scripts/dev-up.sh)", err)
		}
	}
	b, err := bao.New(ctx, env("BAO_ADDR", "http://127.0.0.1:8200"), roleID, secretID)
	if err != nil {
		return nil, nil, err
	}

	read := func(name string) (map[string]string, error) { return b.KV(ctx, "requara/"+c.Env+"/"+name) }
	pg, err := read("postgres")
	if err != nil {
		return nil, nil, err
	}
	s3, err := read("s3")
	if err != nil {
		return nil, nil, err
	}
	smtp, err := read("smtp")
	if err != nil {
		return nil, nil, err
	}
	sess, err := read("session")
	if err != nil {
		return nil, nil, err
	}

	c.DatabaseURL = pg["url"]
	c.S3Endpoint, c.S3AccessKey, c.S3SecretKey, c.S3Bucket = s3["endpoint"], s3["access_key"], s3["secret_key"], s3["bucket"]
	c.SMTPAddr, c.SMTPFrom = smtp["addr"], smtp["from"]
	c.SessionKey = []byte(sess["hash_key"])
	if len(c.SessionKey) < 32 {
		return nil, nil, fmt.Errorf("session hash_key in OpenBao is too short")
	}
	if err := c.loadProviders(read); err != nil {
		return nil, nil, err
	}
	admins, _ := read("admins") // optional
	if err := c.loadAdmins(os.Getenv, os.ReadFile, admins, "secret/requara/"+c.Env+"/admins"); err != nil {
		return nil, nil, err
	}
	if len(c.Admins) == 0 {
		slog.Warn("no instance admins: set REQUARA_ADMIN_EMAILS or REQUARA_ADMIN_EMAILS_FILE to manage users, workspaces and the audit log")
	}
	c.AuditRetentionDays = 365
	if v := os.Getenv("REQUARA_AUDIT_RETENTION_DAYS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return nil, nil, fmt.Errorf("REQUARA_AUDIT_RETENTION_DAYS must be a number of days (0 = forever)")
		}
		c.AuditRetentionDays = n
	}
	for _, p := range c.Providers {
		if p.Type == "email" && c.SMTPAddr == "" {
			return nil, nil, fmt.Errorf("the email sign-in provider needs SMTP: set addr in OpenBao secret/requara/%s/smtp", c.Env)
		}
	}
	return c, b, nil
}
