#!/usr/bin/env bash
# Configures a dev OpenBao (root token "root") for Requara:
#  - platform secrets in KV v2 at secret/requara/dev/*
#  - Transit engine for customer secrets
#  - AppRole login for the backend (credentials written to .dev/)
#  - OIDC login for developers through Keycloak, mapped by Keycloak group
set -euo pipefail
cd "$(dirname "$0")/../.."  # repo root: .dev/ lives here
export BAO_ADDR=${BAO_ADDR:-http://127.0.0.1:8200}
export BAO_TOKEN=${BAO_TOKEN:-root}
mkdir -p .dev

# --- platform secrets the backend needs ---
bao kv put -mount=secret requara/dev/postgres url="postgres://requara:requara@localhost:5433/requara?sslmode=disable" >/dev/null
bao kv put -mount=secret requara/dev/oidc issuer="http://localhost:8081/realms/acme" client_id="requara-web" client_secret="dev-requara-web-secret" >/dev/null
bao kv put -mount=secret requara/dev/s3 endpoint="localhost:9000" access_key="requara" secret_key="requara-dev-s3" bucket="requara-bodies" >/dev/null
bao kv put -mount=secret requara/dev/smtp addr="localhost:1025" from="Requara <no-reply@requara.local>" >/dev/null
if ! bao kv get -mount=secret requara/dev/session >/dev/null 2>&1; then
  bao kv put -mount=secret requara/dev/session hash_key="$(openssl rand -hex 32)" >/dev/null
fi

# --- Transit for customer environment secrets ---
bao secrets list -format=json | grep -q '"transit/"' || bao secrets enable transit >/dev/null

# --- policies ---
for p in dev/infra/openbao/policies/*.hcl; do bao policy write "$(basename "$p" .hcl)" "$p" >/dev/null; done

# --- AppRole for the backend ---
bao auth list -format=json | grep -q '"approle/"' || bao auth enable approle >/dev/null
bao write auth/approle/role/requara-backend token_policies=requara-backend token_ttl=1h token_max_ttl=24h secret_id_ttl=0 >/dev/null
bao read -field=role_id auth/approle/role/requara-backend/role-id > .dev/approle-role-id
bao write -f -field=secret_id auth/approle/role/requara-backend/secret-id > .dev/approle-secret-id

# --- developers sign in with Keycloak ---
bao auth list -format=json | grep -q '"oidc/"' || bao auth enable oidc >/dev/null
bao write auth/oidc/config \
  oidc_discovery_url="http://localhost:8081/realms/acme" \
  oidc_client_id="openbao" oidc_client_secret="dev-openbao-secret" default_role="dev" >/dev/null
bao write auth/oidc/role/dev \
  user_claim="preferred_username" groups_claim="groups" oidc_scopes="openid,profile,email" \
  allowed_redirect_uris="http://localhost:8200/ui/vault/auth/oidc/oidc/callback,http://127.0.0.1:8200/ui/vault/auth/oidc/oidc/callback,http://localhost:8250/oidc/callback" \
  token_policies="default" token_ttl=1h >/dev/null
ACCESSOR=$(bao auth list -format=json | python3 -c 'import json,sys; print(json.load(sys.stdin)["oidc/"]["accessor"])')
map_group() { # $1 = keycloak group path, $2 = openbao policy
  local gid
  gid=$(bao write -field=id identity/group name="$2" type=external policies="$2" 2>/dev/null || bao read -field=id identity/group/name/"$2")
  bao list -format=json identity/group-alias/id 2>/dev/null | grep -q . || true
  bao write identity/group-alias name="$1" mount_accessor="$ACCESSOR" canonical_id="$gid" >/dev/null 2>&1 || true
}
map_group "/engineering/requara-dev" requara-dev
map_group "/engineering/requara-admin" requara-admin
map_group "/sre" sre
echo "OpenBao ready: platform secrets, transit, approle (.dev/approle-*), oidc via Keycloak"
