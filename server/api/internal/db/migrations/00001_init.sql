-- +goose Up
CREATE TABLE users (
    id            text PRIMARY KEY,               -- Keycloak subject (sub)
    username      text NOT NULL,
    email         text NOT NULL,
    name          text NOT NULL,
    roles         text[] NOT NULL DEFAULT '{}',   -- Keycloak realm roles
    groups        text[] NOT NULL DEFAULT '{}',   -- Keycloak groups
    prefs         jsonb  NOT NULL DEFAULT '{}',
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id_hash      bytea PRIMARY KEY,               -- sha256 of the cookie value
    user_id      text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    id_token     text NOT NULL,                   -- kept server-side for logout
    user_agent   text NOT NULL DEFAULT '',
    ip           text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE workspaces (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    slug       text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE members (
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id      text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role         text NOT NULL CHECK (role IN ('owner','editor','viewer')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id)
);

CREATE TABLE invites (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    email        text NOT NULL,
    role         text NOT NULL,
    invited_by   text NOT NULL REFERENCES users(id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, email)
);

CREATE TABLE apis (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name              text NOT NULL,
    slug              text NOT NULL,
    description       text NOT NULL DEFAULT '',
    owner             text NOT NULL DEFAULT '',
    status            text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published','deprecated')),
    base_url          text NOT NULL DEFAULT '',
    current_version   text NOT NULL DEFAULT '',   -- last published version
    draft_spec        jsonb NOT NULL,             -- OpenAPI 3 document being edited
    draft_version     text NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, slug)
);

CREATE TABLE api_versions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    api_id       uuid NOT NULL REFERENCES apis(id) ON DELETE CASCADE,
    version      text NOT NULL,
    spec         jsonb NOT NULL,
    changes      jsonb NOT NULL DEFAULT '[]',
    published_by text REFERENCES users(id),
    published_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (api_id, version)
);

CREATE TABLE requests (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    api_id        uuid REFERENCES apis(id) ON DELETE CASCADE,
    folder        text NOT NULL DEFAULT '',
    name          text NOT NULL,
    method        text NOT NULL,
    url           text NOT NULL,
    params        jsonb NOT NULL DEFAULT '[]',
    headers       jsonb NOT NULL DEFAULT '[]',
    body_mode     text  NOT NULL DEFAULT 'none',
    body          text  NOT NULL DEFAULT '',
    auth          jsonb NOT NULL DEFAULT '{"type":"inherit"}',
    pre_script    text  NOT NULL DEFAULT '',
    post_script   text  NOT NULL DEFAULT '',
    settings      jsonb NOT NULL DEFAULT '{}',
    operation_key text  NOT NULL DEFAULT '',     -- "POST /v2/charges" when generated from a spec
    sort          int   NOT NULL DEFAULT 0,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX requests_ws ON requests(workspace_id);

CREATE TABLE environments (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name         text NOT NULL,
    color        text NOT NULL DEFAULT '#BFBFBF',
    is_global    boolean NOT NULL DEFAULT false,
    sort         int NOT NULL DEFAULT 0,
    UNIQUE (workspace_id, name)
);

CREATE TABLE env_vars (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    env_id     uuid NOT NULL REFERENCES environments(id) ON DELETE CASCADE,
    key        text NOT NULL,
    value      text NOT NULL DEFAULT '',          -- plain value (non-secret only)
    ciphertext text NOT NULL DEFAULT '',          -- OpenBao Transit ciphertext (secret only)
    kind       text NOT NULL DEFAULT 'default' CHECK (kind IN ('default','secret','script')),
    enabled    boolean NOT NULL DEFAULT true,
    sort       int NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (env_id, key)
);

CREATE TABLE history (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id      text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    request_id   uuid REFERENCES requests(id) ON DELETE SET NULL,
    api_id       uuid REFERENCES apis(id) ON DELETE SET NULL,
    method       text NOT NULL,
    url          text NOT NULL,                   -- secrets already redacted
    status       int  NOT NULL DEFAULT 0,
    duration_ms  int  NOT NULL DEFAULT 0,
    size_bytes   int  NOT NULL DEFAULT 0,
    env_name     text NOT NULL DEFAULT '',
    error        text NOT NULL DEFAULT '',
    req_headers  jsonb NOT NULL DEFAULT '[]',
    res_headers  jsonb NOT NULL DEFAULT '[]',
    res_body     text NOT NULL DEFAULT '',        -- small bodies inline
    body_object  text NOT NULL DEFAULT '',        -- large bodies: S3 object key
    timeline     jsonb NOT NULL DEFAULT '[]',
    tests        jsonb NOT NULL DEFAULT '[]',
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX history_ws_time ON history(workspace_id, created_at DESC);
CREATE INDEX history_api_time ON history(api_id, created_at DESC);

CREATE TABLE api_keys (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         text NOT NULL,
    prefix       text NOT NULL,                   -- rk_live_8Fq2
    last4        text NOT NULL,
    hash         bytea NOT NULL UNIQUE,           -- sha256 of the full key
    scopes       text[] NOT NULL,
    expires_at   timestamptz,
    last_used_at timestamptz,
    revoked_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mock_servers (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    api_id       uuid NOT NULL REFERENCES apis(id) ON DELETE CASCADE,
    name         text NOT NULL,
    slug         text NOT NULL UNIQUE,
    enabled      boolean NOT NULL DEFAULT true,
    latency_ms   int NOT NULL DEFAULT 0,
    error_rate   int NOT NULL DEFAULT 0 CHECK (error_rate BETWEEN 0 AND 100),
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mock_calls (
    id         bigserial PRIMARY KEY,
    mock_id    uuid NOT NULL REFERENCES mock_servers(id) ON DELETE CASCADE,
    method     text NOT NULL,
    path       text NOT NULL,
    status     int  NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mock_calls_time ON mock_calls(mock_id, created_at DESC);

-- +goose Down
DROP TABLE mock_calls, mock_servers, api_keys, history, env_vars, environments,
           requests, api_versions, apis, invites, members, workspaces, sessions, users;
