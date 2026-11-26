# Requara desktop

A native app built with [GPUI](https://gpui.rs) through [gpui-kit](https://gpui-kit.com).
It works on its own, with no account: build requests and send them from this computer.
Signing in to a team's Requara server is optional and adds the shared workspace.

## What it does

- **My requests (no account):** method, URL, headers and body; Send runs from this computer
  (`src/local.rs`). Requests are saved in `<config dir>/Requara/requests.json`.
- **Sign in to collaborate (optional):** enter the team's server; the app checks
  `GET /api/version` (refusing API levels it doesn't support, `SUPPORTED_API` in `src/api.rs`),
  lists the server's sign-in options from `GET /auth/providers` (Google, GitHub, Microsoft,
  company SSO, …), and opens the chosen one in the browser. The app never sees a password
  (RFC 8252 loopback + PKCE, see `src/auth.rs`). The session token is kept in the OS keychain
  and lasts 30 days. Local requests keep working signed in or out.
- **APIs:** the workspace's APIs and their endpoints.
- **Requests:** saved requests by collection; send one and see the response.
- **Mock servers:** URLs, status and today's call counts.

Creating and editing APIs, requests and environments stays in the web app for now.

## Run

```sh
cargo run                 # with the dev stack running, sign in via http://localhost:5173
cargo test
```

Linux needs: `libxkbcommon-dev libxkbcommon-x11-dev libwayland-dev libvulkan-dev libx11-xcb-dev libxcb1-dev libfontconfig-dev libdbus-1-dev`.

## Package

```sh
cargo install cargo-packager --locked
cargo packager --release   # → target/packages/
```

Icons come from `assets/icons/`, rendered from `branding/requara-icon.svg` by
`cargo run --manifest-path ../branding/icongen/Cargo.toml`.
Code signing (Apple Developer ID, Windows) isn't configured yet.
