# Speklo

Design, mock and test APIs with your team. Speklo ships as two products from this repo:

| Product | What it is | Where it's released |
|---|---|---|
| **Server** | The Go API and the React web UI. Companies self-host it with Docker. | Images `ghcr.io/<owner>/<repo>/api` and `/web`, plus a compose bundle on each `server-vX.Y.Z` GitHub Release |
| **Desktop** | A native GPUI app. Works on its own as a local API client; signing in to a team's server (Google, GitHub, Microsoft, company SSO, …) adds collaboration. | Installers on each `desktop-vX.Y.Z` GitHub Release |

## Layout

```
server/
  api/        Go backend (HTTP API, OIDC sign-in, request runner, mocks, jobs)
  web/        React + TypeScript web UI, served by nginx in its image
  deploy/     What customers run: docker-compose.yml, .env.example, OpenBao setup (bring your own OIDC provider)
  version.txt Server version (managed by release-please)
desktop/      GPUI desktop app (Rust)
branding/     Logo SVGs and icongen, which renders every app icon from them
dev/          Local development only: docker-compose for Postgres, Keycloak (local test IdP), S3, Mailpit, scripts
.github/      CI, PR-title check, releases
```

## Develop

```sh
./dev/scripts/dev-up.sh                        # Postgres, Keycloak, OpenBao (dev mode), S3, Mailpit
(cd server/api && go run ./cmd/speklo)         # API on :8080
(cd server/web && npm install && npm run dev)  # web UI on http://localhost:5173
(cd desktop && cargo run)                      # desktop app; works locally, or sign in via http://localhost:5173
```

Sign in as `alex.chen` / `speklo-dev`. Stop everything with `./dev/scripts/dev-down.sh`.

## Releases

[release-please](https://github.com/googleapis/release-please) reads commit messages (the PR title, since PRs are squash-merged) and keeps one release PR open per product:

- `fix: …` → patch, `feat: …` → minor, `feat!: …` → major (0.x: one level lower)
- Changes under `server/` release the server; changes under `desktop/` release the desktop app. Each only when its own files change.
- Merging a release PR tags it (`server-v0.3.0`, `desktop-v0.2.1`) and the release workflow builds and attaches what that product ships.

After the first server release, make the two GHCR packages public (Package settings → Change visibility) so customers can pull them.

## The desktop app and the server

The desktop app checks `GET /api/version` before signing in. The server reports its `api` level; the app knows which levels it supports (`desktop/src/api.rs`, `SUPPORTED_API`). Raise `APIVersion` in `server/api/internal/api/version.go` only for changes older apps can't handle, and ship a desktop release that supports it first.
