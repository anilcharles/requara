-- +goose Up
-- Postman-style collections: requests grouped without an API spec, with
-- collection-level variables, auth and scripts that their requests inherit.
-- An API's own collection keeps its settings in a row with api_id set.
CREATE TABLE collections (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    api_id       uuid UNIQUE REFERENCES apis(id) ON DELETE CASCADE,
    name         text NOT NULL,
    description  text NOT NULL DEFAULT '',
    variables    jsonb NOT NULL DEFAULT '[]',
    auth         jsonb NOT NULL DEFAULT '{"type":"inherit"}',
    pre_script   text NOT NULL DEFAULT '',
    post_script  text NOT NULL DEFAULT '',
    sort         int NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX collections_ws ON collections(workspace_id);

ALTER TABLE requests ADD COLUMN collection_id uuid REFERENCES collections(id) ON DELETE CASCADE;
ALTER TABLE requests ADD COLUMN kind text NOT NULL DEFAULT 'http' CHECK (kind IN ('http','websocket'));
ALTER TABLE requests ADD COLUMN form_data jsonb NOT NULL DEFAULT '[]';   -- multipart fields, files as base64
ALTER TABLE requests ADD COLUMN body_file jsonb NOT NULL DEFAULT '{}';   -- binary body: {name, contentType, data}
ALTER TABLE requests ADD COLUMN messages jsonb NOT NULL DEFAULT '[]';    -- WebSocket: saved messages to send
CREATE INDEX requests_collection ON requests(collection_id);

-- Saved responses (Postman "examples").
CREATE TABLE request_examples (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id uuid NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
    name       text NOT NULL,
    status     int NOT NULL DEFAULT 200,
    headers    jsonb NOT NULL DEFAULT '[]',
    body       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX request_examples_request ON request_examples(request_id);

-- Cookies the request runner received, per workspace and person.
CREATE TABLE cookies (
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id      text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    domain       text NOT NULL,
    path         text NOT NULL DEFAULT '/',
    name         text NOT NULL,
    value        text NOT NULL,
    expires      timestamptz,
    secure       boolean NOT NULL DEFAULT false,
    http_only    boolean NOT NULL DEFAULT false,
    PRIMARY KEY (workspace_id, user_id, domain, path, name)
);

-- Monitors: a collection run on a schedule, with alerts.
CREATE TABLE monitors (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name            text NOT NULL,
    target          text NOT NULL,                 -- collection id, API id or "scratch"
    environment_id  uuid REFERENCES environments(id) ON DELETE SET NULL,
    interval_min    int NOT NULL DEFAULT 60 CHECK (interval_min BETWEEN 5 AND 10080),
    enabled         boolean NOT NULL DEFAULT true,
    notify          text NOT NULL DEFAULT '',      -- comma-separated emails, alerted on failure
    created_by      text REFERENCES users(id) ON DELETE SET NULL,
    next_run_at     timestamptz NOT NULL DEFAULT now(),
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX monitors_due ON monitors(next_run_at) WHERE enabled;

CREATE TABLE monitor_runs (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    monitor_id  uuid NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    started_at  timestamptz NOT NULL DEFAULT now(),
    duration_ms int NOT NULL DEFAULT 0,
    requests    int NOT NULL DEFAULT 0,
    passed      int NOT NULL DEFAULT 0,
    failed      int NOT NULL DEFAULT 0,
    errors      int NOT NULL DEFAULT 0,
    results     jsonb NOT NULL DEFAULT '[]'
);
CREATE INDEX monitor_runs_monitor ON monitor_runs(monitor_id, started_at DESC);

-- +goose Down
DROP TABLE monitor_runs, monitors, cookies, request_examples;
DROP INDEX requests_collection;
ALTER TABLE requests DROP COLUMN messages, DROP COLUMN body_file, DROP COLUMN form_data, DROP COLUMN kind, DROP COLUMN collection_id;
DROP TABLE collections;
