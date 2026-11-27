-- +goose Up
-- Instance administration: admins named by the operator (env / secret file),
-- disabled accounts, email-link and SAML sign-in, admin API tokens, the
-- audit log, instance settings and SCIM provisioning.

-- is_admin is recomputed from REQUARA_ADMIN_EMAILS at every start and sign-in;
-- nobody can grant it from the browser.
ALTER TABLE users ADD COLUMN email_verified boolean NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN is_admin       boolean NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN disabled_at    timestamptz;
CREATE INDEX users_email ON users (lower(email));

-- New sign-ups join every workspace marked auto_join (the seeded one is).
ALTER TABLE workspaces ADD COLUMN auto_join boolean NOT NULL DEFAULT false;
UPDATE workspaces SET auto_join = true WHERE slug = 'acme-payments';

-- Admin API tokens and SCIM can invite people, so the inviter may be no user.
ALTER TABLE invites ALTER COLUMN invited_by DROP NOT NULL;
ALTER TABLE invites DROP CONSTRAINT invites_invited_by_fkey;
ALTER TABLE invites ADD CONSTRAINT invites_invited_by_fkey FOREIGN KEY (invited_by) REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE api_versions DROP CONSTRAINT api_versions_published_by_fkey;
ALTER TABLE api_versions ADD CONSTRAINT api_versions_published_by_fkey FOREIGN KEY (published_by) REFERENCES users(id) ON DELETE SET NULL;

-- Sign-ins that leave the browser's cookies behind (a SAML POST from the
-- identity provider, an email link opened elsewhere) keep their state here.
CREATE TABLE auth_states (
    id         text PRIMARY KEY,
    state      jsonb NOT NULL,
    expires_at timestamptz NOT NULL
);

-- One-time email sign-in links. Only the hash of the token is stored.
CREATE TABLE email_logins (
    token_hash bytea PRIMARY KEY,
    email      text NOT NULL,
    flow_id    text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX email_logins_email ON email_logins (lower(email), created_at DESC);

-- Instance-wide tokens for the admin API and SCIM ("sa_…").
CREATE TABLE admin_tokens (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name         text NOT NULL,
    prefix       text NOT NULL,
    last4        text NOT NULL,
    hash         bytea NOT NULL UNIQUE,
    created_by   text REFERENCES users(id) ON DELETE SET NULL,
    expires_at   timestamptz,
    last_used_at timestamptz,
    revoked_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_events (
    id           bigserial PRIMARY KEY,
    at           timestamptz NOT NULL DEFAULT now(),
    actor_kind   text NOT NULL,                  -- user | token | scim | system | anonymous
    actor_id     text NOT NULL DEFAULT '',
    actor_label  text NOT NULL DEFAULT '',       -- email or token name
    action       text NOT NULL,                  -- auth.signin, user.disabled, …
    outcome      text NOT NULL DEFAULT 'success' CHECK (outcome IN ('success','failure')),
    target_type  text NOT NULL DEFAULT '',
    target_id    text NOT NULL DEFAULT '',
    target_label text NOT NULL DEFAULT '',
    workspace_id uuid,
    ip           text NOT NULL DEFAULT '',
    user_agent   text NOT NULL DEFAULT '',
    details      jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_events_at ON audit_events (at DESC);
CREATE INDEX audit_events_action ON audit_events (action, at DESC);

-- Settings admins change at runtime (the banner, request history).
CREATE TABLE instance_settings (
    key        text PRIMARY KEY,
    value      jsonb NOT NULL,
    updated_by text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- People provisioned by the company directory over SCIM 2.0. An active entry
-- lets that email sign in; deactivating it disables the matching accounts.
CREATE TABLE directory_users (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    external_id text NOT NULL DEFAULT '',
    user_name   text NOT NULL,
    email       text NOT NULL,
    name        text NOT NULL DEFAULT '',
    given_name  text NOT NULL DEFAULT '',
    family_name text NOT NULL DEFAULT '',
    active      boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX directory_users_user_name ON directory_users (lower(user_name));
CREATE INDEX directory_users_email ON directory_users (lower(email));

-- +goose Down
DROP TABLE directory_users, instance_settings, audit_events, admin_tokens, email_logins, auth_states;
ALTER TABLE api_versions DROP CONSTRAINT api_versions_published_by_fkey;
ALTER TABLE api_versions ADD CONSTRAINT api_versions_published_by_fkey FOREIGN KEY (published_by) REFERENCES users(id);
ALTER TABLE invites DROP CONSTRAINT invites_invited_by_fkey;
ALTER TABLE invites ADD CONSTRAINT invites_invited_by_fkey FOREIGN KEY (invited_by) REFERENCES users(id);
ALTER TABLE workspaces DROP COLUMN auto_join;
DROP INDEX users_email;
ALTER TABLE users DROP COLUMN disabled_at, DROP COLUMN is_admin, DROP COLUMN email_verified;
