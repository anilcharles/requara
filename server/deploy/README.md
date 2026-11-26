# Self-hosting Speklo

Runs the whole server on one machine with Docker Compose: the web UI, the API,
Postgres, OpenBao (secrets) and SeaweedFS (S3 storage). People sign in with the
providers you enable: Google, Microsoft, GitHub, GitLab, or your company's own
SSO (any OpenID Connect provider), several at once if you like.

The desktop app works without a server. People connect it to this server when
they want to collaborate, and pick one of the sign-in options you enabled.

## 1. Choose sign-in providers

Every provider uses the same callback URL: **`<SPEKLO_URL>/auth/callback`**.
Register Speklo as a web application at each provider with that URL, then put
the client ID and secret in `.env` and list the provider in `SPEKLO_AUTH_PROVIDERS`.

| Provider | Where to register | `.env` |
|---|---|---|
| Google | Google Cloud console → APIs & Services → Credentials → OAuth client ID (Web application) | `SPEKLO_AUTH_GOOGLE_CLIENT_ID`, `_CLIENT_SECRET` |
| Microsoft Entra ID | Entra admin center → App registrations → New (Web platform) | `SPEKLO_AUTH_MICROSOFT_CLIENT_ID`, `_CLIENT_SECRET`, `_TENANT` (your tenant ID; default `organizations` = any work account) |
| GitHub | Settings → Developer settings → OAuth Apps (or your org's settings) | `SPEKLO_AUTH_GITHUB_CLIENT_ID`, `_CLIENT_SECRET`, optional `_ALLOWED_ORGS`, `_URL` for GitHub Enterprise Server |
| GitLab | User or group settings → Applications (scopes `openid profile email`) | `SPEKLO_AUTH_GITLAB_CLIENT_ID`, `_CLIENT_SECRET`, `_URL` for self-managed |
| Okta, Auth0, Keycloak, Authentik, Zitadel, Dex, … | Create an OIDC web app | Pick an id (e.g. `okta`): `SPEKLO_AUTH_OKTA_ISSUER`, `_CLIENT_ID`, `_CLIENT_SECRET`, `_NAME` |

Provider settings, all optional except the client ID (and ISSUER for generic OIDC):
`_NAME` (button text), `_SCOPES`, `_ROLES_CLAIM` and `_GROUPS_CLAIM` (claim paths such as
`realm_access.roles`), `_ACCOUNT_URL`, `_DISCOVERY_URL`, `_TYPE` (`oidc` or `github`).

If a setting is missing, the API container stops at start and its log names it.

### Who may join

Google, GitHub (without `ALLOWED_ORGS`), GitLab.com and Microsoft `organizations` let
anyone with an account sign in. Limit who joins:

- `SPEKLO_ALLOWED_DOMAINS=your-company.com` — email domains that may join (`*` = anyone),
- `SPEKLO_AUTH_GITHUB_ALLOWED_ORGS=acme` — only members of your GitHub organization,
- invites — anyone an owner invites can join, whatever their domain.

`SPEKLO_ADMIN_GROUPS` makes people workspace owners and `SPEKLO_PUBLISHER_GROUPS` lets them
publish API versions. They match role or group names from the provider (or the last part of
a group path such as `/engineering/speklo-admin`; GitHub groups are `org` and `org/team`).

The same person signing in with two different providers gets two separate accounts.

## 2. Start

```sh
tar xzf speklo-selfhost-<version>.tar.gz && cd speklo
cp .env.example .env
# edit .env: URL, organization, providers, who may join, and every "change-me" secret
docker compose up -d
```

Open `SPEKLO_URL` (default http://localhost:8080) and sign in.

## Production

- Put `SPEKLO_URL` behind HTTPS (Caddy, Traefik or your load balancer) and use the `https://`
  URL in `.env` and at your providers. Sign-in cookies are marked Secure automatically.
- Pin `SPEKLO_VERSION` to a release instead of `latest`.
- Back up the volumes: `pgdata`, `s3data`, `openbao-data` **and** `openbao-keys`.
  Losing `openbao-keys` makes the encrypted environment secrets unreadable.
- OpenBao unseals itself with a key stored in the `openbao-keys` volume. That keeps the
  install unattended; for stricter setups use OpenBao auto-unseal (cloud KMS/HSM) or an
  existing OpenBao/Vault cluster.
- Provider client secrets can live in OpenBao instead of `.env`: put
  `<id>_client_secret` keys in `secret/speklo/prod/auth`.

## Upgrade

```sh
# set SPEKLO_VERSION in .env to the new release
docker compose pull && docker compose up -d
```

Database migrations run when the API starts.

## Notes

- First start seeds a demo "Acme Payments" workspace with sample APIs, and everyone who signs
  in joins it.
- `SPEKLO_ALLOW_PRIVATE_NETWORK=true` lets the request runner call internal addresses. Only turn
  it on when your APIs are internal: it lets every user reach anything this server can.
