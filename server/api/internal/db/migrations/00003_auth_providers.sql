-- +goose Up
-- Requara signs in with several providers at once; remember which one each
-- user, session and desktop sign-in code came from (for its name, account
-- link and logout).
ALTER TABLE users ADD COLUMN provider text NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN provider text NOT NULL DEFAULT '';
ALTER TABLE desktop_codes ADD COLUMN provider text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE desktop_codes DROP COLUMN provider;
ALTER TABLE sessions DROP COLUMN provider;
ALTER TABLE users DROP COLUMN provider;
