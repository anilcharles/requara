# Self-hosting Requara

Runs the whole server on one machine with Docker Compose: the web UI, the API,
Postgres, OpenBao (secrets) and SeaweedFS (S3 storage). People sign in with the
providers you enable: Google, Microsoft, GitHub, GitLab, your company's own
SSO (any OpenID Connect or SAML 2.0 provider) or one-time email links, several
at once if you like. For Kubernetes, use the Helm chart in [helm/requara](helm/requara/values.yaml)
(published as `oci://ghcr.io/anilcharles/charts/requara`), or adapt [kubernetes/requara.yaml](kubernetes/requara.yaml).

The desktop app works without a server. People connect it to this server when
they want to collaborate, and pick one of the sign-in options you enabled.

## 1. Choose sign-in providers

Every provider uses the same callback URL: **`<REQUARA_URL>/auth/callback`**.
Register Requara as a web application at each provider with that URL, then put
the client ID and secret in `.env` and list the provider in `REQUARA_AUTH_PROVIDERS`.

| Provider | Where to register | `.env` |
|---|---|---|
| Google | Google Cloud console → APIs & Services → Credentials → OAuth client ID (Web application) | `REQUARA_AUTH_GOOGLE_CLIENT_ID`, `_CLIENT_SECRET` |
| Microsoft Entra ID | Entra admin center → App registrations → New (Web platform) | `REQUARA_AUTH_MICROSOFT_CLIENT_ID`, `_CLIENT_SECRET`, `_TENANT` (your tenant ID; default `organizations` = any work account) |
| GitHub | Settings → Developer settings → OAuth Apps (or your org's settings) | `REQUARA_AUTH_GITHUB_CLIENT_ID`, `_CLIENT_SECRET`, optional `_ALLOWED_ORGS`, `_URL` for GitHub Enterprise Server |
| GitLab | User or group settings → Applications (scopes `openid profile email`) | `REQUARA_AUTH_GITLAB_CLIENT_ID`, `_CLIENT_SECRET`, `_URL` for self-managed |
| Okta, Auth0, Keycloak, Authentik, Zitadel, Dex, … | Create an OIDC web app | Pick an id (e.g. `okta`): `REQUARA_AUTH_OKTA_ISSUER`, `_CLIENT_ID`, `_CLIENT_SECRET`, `_NAME` |
| SAML 2.0 (Okta, Entra ID, Google Workspace, ADFS, OneLogin, JumpCloud, Keycloak, …) | Create a SAML app with ACS URL `<REQUARA_URL>/auth/saml/<id>/acs` and entity ID `<REQUARA_URL>/auth/saml/<id>/metadata` | Pick an id (e.g. `corp`): `REQUARA_AUTH_CORP_TYPE=saml`, `_METADATA_URL` (or `_METADATA_FILE`, or `_SSO_URL` + `_IDP_CERT_FILE`), `_NAME` |
| Email links | Nothing to register; needs `SMTP_ADDR` | Add `email` to `REQUARA_AUTH_PROVIDERS` |

Provider settings, all optional except the client ID (and ISSUER for generic OIDC):
`_NAME` (button text; by default the provider's own name, recognized from its address: Keycloak, Okta, Microsoft Entra ID, Auth0, authentik, ZITADEL, OneLogin, JumpCloud, Ping, Cognito, AD FS, Dex, …), `_SCOPES`, `_ROLES_CLAIM` and `_GROUPS_CLAIM` (claim paths such as
`realm_access.roles`), `_ACCOUNT_URL`, `_DISCOVERY_URL`, `_TYPE` (`oidc`, `github`, `saml` or
`email`), `_TRUST_EMAIL` (`true` treats the provider's email as verified even without an
`email_verified` claim).

**SAML details.** Requara reads the email from the `email`, `mail` or Microsoft `emailaddress`
attribute (or an email-format NameID), the name from `displayName`, and groups from `groups`,
`memberOf` or Microsoft's `groups` claim; change them with `_EMAIL_ATTR`, `_NAME_ATTR` and
`_GROUPS_ATTR`. Sign-ins must start in Requara (IdP-initiated sign-ins are refused), and each
response is accepted once. To sign requests or receive encrypted assertions, give Requara a
certificate and key with `_SP_CERT_FILE` and `_SP_KEY_FILE` (put files in `./saml`, mounted at
`/etc/requara/saml`). The admin console's Overview shows the exact ACS URL and entity ID.

**Email links.** People enter their address and get a link that works once for 15 minutes.
The link opens a page with a Continue button, so mail scanners that open links can't use it
up. Links are only sent to addresses that may join, and the page answers the same way either
way.

If a setting is missing, the API container stops at start and its log names it.

### Who may join

Google, GitHub (without `ALLOWED_ORGS`), GitLab.com and Microsoft `organizations` let
anyone with an account sign in. Limit who joins:

- `REQUARA_ALLOWED_DOMAINS=your-company.com` — email domains that may join (`*` = anyone),
- `REQUARA_AUTH_GITHUB_ALLOWED_ORGS=acme` — only members of your GitHub organization,
- invites — anyone an owner or admin invites can join, whatever their domain,
- SCIM — people your directory provisions can join (see below).

`REQUARA_ADMIN_GROUPS` makes people workspace owners and `REQUARA_PUBLISHER_GROUPS` lets them
publish API versions. They match role or group names from the provider (or the last part of
a group path such as `/engineering/requara-admin`; GitHub groups are `org` and `org/team`).

The same person signing in with two different providers gets two separate accounts.

### Instance admins

Admins run the admin console at `<REQUARA_URL>/admin`: users (deactivate, reactivate, sign out
everywhere, delete), invites, workspaces and their members, the audit log, SCIM provisioning,
admin tokens, the banner and request-history settings. **Admins are named on the server, never
in the browser**, so nobody can make themselves admin:

- `admins.txt` next to `docker-compose.yml`, mounted as a Docker secret
  (`REQUARA_ADMIN_EMAILS_FILE=/run/secrets/requara_admins`); one email per line,
- `REQUARA_ADMIN_EMAILS=ana@acme.com,ops@acme.com` in `.env`,
- on Kubernetes, a Secret mounted as a file or passed as an env var (see
  [kubernetes/requara.yaml](kubernetes/requara.yaml)),
- or OpenBao: `secret/requara/prod/admins`, key `emails`.

The lists add up. Someone is an admin when they sign in with a listed email that their provider
has verified (GitHub's verified primary email, an `email_verified` claim, a SAML IdP, an email
link, or a provider with `_TRUST_EMAIL=true`). Microsoft's multi-tenant `organizations` setting
doesn't verify emails, so use your tenant ID there. Admins always may sign in, can't be
deactivated from the console or by SCIM, and lose admin rights at the next restart after you
remove them. Workspace owners (`REQUARA_ADMIN_GROUPS`) are a different thing: they manage one
workspace.

### SCIM provisioning

Your identity provider can create and deactivate accounts with SCIM 2.0:

- base URL `<REQUARA_URL>/scim/v2`, bearer token: an admin token (admin console → Admin tokens),
- users only (create, update, deactivate, delete; filters on `userName`, `externalId`, email).

An active provisioned person may sign in whatever `REQUARA_ALLOWED_DOMAINS` says. Deactivating
or deleting them in the directory deactivates their Requara account and signs them out. Workspace
membership is managed in Requara (new people join workspaces marked "Everyone joins").

### Audit log

Sign-ins (and failed ones), sign-outs, account, invite, membership, workspace, API key, admin
token, SCIM, settings and publishing events are kept in Postgres for
`REQUARA_AUDIT_RETENTION_DAYS` (default 365, `0` keeps them forever). Admins filter and export
them as CSV in the console, or read them with `GET /api/admin/audit` and an admin token.

### Admin API

Everything in the console is an API under `/api/admin` (users, invites, workspaces, members,
audit, settings, history purge), usable with `Authorization: Bearer sa_…`. Admin tokens can't
create or revoke admin tokens.

## 2. Start

```sh
tar xzf requara-selfhost-<version>.tar.gz && cd requara
cp .env.example .env
# edit .env: URL, organization, providers, who may join, and every "change-me" secret
docker compose up -d
```

Open `REQUARA_URL` (default http://localhost:8080) and sign in.

## Production

- Put `REQUARA_URL` behind HTTPS (Caddy, Traefik or your load balancer) and use the `https://`
  URL in `.env` and at your providers. Sign-in cookies are marked Secure automatically.
- Pin `REQUARA_VERSION` to a release instead of `latest`.
- Back up the volumes: `pgdata`, `s3data`, `openbao-data` **and** `openbao-keys`.
  Losing `openbao-keys` makes the encrypted environment secrets unreadable.
- OpenBao unseals itself with a key stored in the `openbao-keys` volume. That keeps the
  install unattended; for stricter setups use OpenBao auto-unseal (cloud KMS/HSM) or an
  existing OpenBao/Vault cluster.
- Provider client secrets can live in OpenBao instead of `.env`: put
  `<id>_client_secret` keys in `secret/requara/prod/auth`.

## Upgrade

```sh
# set REQUARA_VERSION in .env to the new release
docker compose pull && docker compose up -d
```

Database migrations run when the API starts.

## Collections, runner, monitors and the CLI

- **Collections** work like Postman's: variables, auth and pre-request/test scripts on the
  collection apply to every request in it. Import Postman collections (v2.0/v2.1) and cURL
  commands, export as Postman v2.1. Scripts written for Postman (`pm.test`, `pm.expect`,
  `pm.response`, `pm.environment`, …) run unchanged.
- **Collection Runner** sends a collection in order, any number of iterations, with CSV or JSON
  data files whose columns become variables.
- **Monitors** run a collection on the server on a schedule (every 5 minutes to every week) and
  email the addresses you give when a test fails or a request errors (needs SMTP).
- **requara-cli** runs collections in CI, like Newman: create an API key with
  `collections:read` and `runs:write`, then

  ```sh
  REQUARA_URL=https://requara.acme.com REQUARA_API_KEY=rk_live_… \
    requara-cli run "Orders API" --env Staging --data users.csv --reporter junit --out results.xml
  ```

  It exits with 1 when a test fails. Build it with `go build ./cmd/requara-cli` in `server/api`.
- **requara-agent** lets My workspace (in the browser) send requests without CORS limits, cookie
  restrictions or localhost blocks, like the Postman Agent. Run it on your computer, paste the
  token it prints in Preferences → Local agent. It listens on 127.0.0.1 only, and only paired
  pages can use it. Build it with `go build ./cmd/requara-agent`.

## Notes

- People can use Requara without signing in: **My workspace** keeps their APIs, requests,
  environments, history and mock servers in their own browser, and runs the same engine as the
  server (OpenAPI design, lint, docs, mocks, scripts and tests) as WebAssembly. Signing in adds
  the organization's team workspaces; "Copy to team workspace" moves personal work over. Requests
  from My workspace are sent by the browser, so APIs that don't allow other websites (CORS) can
  only be reached from a team workspace or the desktop app. The engine (`/core/`, about 16 MB
  compressed) downloads once and is cached.

- First start seeds a demo "Acme Payments" workspace with sample APIs. It's marked "Everyone
  joins", so everyone who signs in joins it; change that, or delete it, in the admin console.
  People can create their own workspaces unless an admin turns that off.
- `REQUARA_ALLOW_PRIVATE_NETWORK=true` lets the request runner call internal addresses. Only turn
  it on when your APIs are internal: it lets every user reach anything this server can.
