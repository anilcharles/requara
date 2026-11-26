-- +goose Up
-- One-time codes for desktop sign-in (RFC 8252 loopback + PKCE). A code
-- lives two minutes and is deleted when the desktop app exchanges it.
CREATE TABLE desktop_codes (
    code_hash  bytea PRIMARY KEY,               -- sha256 of the code
    user_id    text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    id_token   text NOT NULL,
    challenge  text NOT NULL,                   -- base64url(sha256(verifier))
    expires_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE desktop_codes;
