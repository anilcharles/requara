#!/bin/sh
# Runs OpenBao for a single-server Requara install and keeps it ready:
#   1. starts the server (file storage in the openbao-data volume)
#   2. first run: initializes it and keeps the unseal key in the openbao-keys volume
#   3. every start: unseals it
#   4. writes Requara's settings from the environment and an AppRole login
#      for the API into the openbao-creds volume
#
# Keeping the unseal key next to the data is the trade-off that makes this
# start unattended. For stricter setups use OpenBao's auto-unseal (cloud KMS
# or HSM) or an external OpenBao cluster and point BAO_ADDR at it.
set -eu

export BAO_ADDR=http://127.0.0.1:8200
KEYS=/openbao/keys
CREDS=/openbao/creds
mkdir -p "$KEYS" "$CREDS"
chmod 700 "$KEYS"
rm -f "$CREDS/ready"

bao server -config=/openbao/config/config.hcl &
SERVER=$!
trap 'kill $SERVER 2>/dev/null' TERM INT

# Wait until the server answers (status exits 2 while sealed, 1 while down).
while :; do
  rc=0; bao status >/dev/null 2>&1 || rc=$?
  [ "$rc" -ne 1 ] && break
  sleep 1
done

json_field() { # $1 = file, $2 = key; first string value of that key
  tr -d '\n ' < "$1" | sed -n "s/.*\"$2\":\[\{0,1\}\"\([^\"]*\)\".*/\1/p"
}

if [ ! -s "$KEYS/init.json" ]; then
  if bao operator init -status >/dev/null 2>&1; then
    echo "OpenBao is initialized but $KEYS/init.json is missing; can't unseal." >&2
    exit 1
  fi
  echo "initializing OpenBao"
  bao operator init -key-shares=1 -key-threshold=1 -format=json > "$KEYS/init.json"
  chmod 600 "$KEYS/init.json"
fi
bao operator unseal "$(json_field "$KEYS/init.json" unseal_keys_b64)" >/dev/null
export BAO_TOKEN="$(json_field "$KEYS/init.json" root_token)"

# --- engines, policy, AppRole (idempotent) ---
bao secrets list | grep -q '^secret/' || bao secrets enable -path=secret kv-v2 >/dev/null
bao secrets list | grep -q '^transit/' || bao secrets enable transit >/dev/null
bao policy write requara-backend /openbao/config/requara-backend.hcl >/dev/null
bao auth list | grep -q '^approle/' || bao auth enable approle >/dev/null
bao write auth/approle/role/requara-backend \
  token_policies=requara-backend token_ttl=1h token_max_ttl=24h secret_id_ttl=0 >/dev/null

# --- Requara's settings, from .env (rewritten each start so edits apply) ---
bao kv put -mount=secret requara/prod/postgres \
  url="postgres://requara:${POSTGRES_PASSWORD}@postgres:5432/requara?sslmode=disable" >/dev/null
bao kv put -mount=secret requara/prod/s3 \
  endpoint="s3:8333" access_key="${S3_ACCESS_KEY}" secret_key="${S3_SECRET_KEY}" bucket=requara-bodies >/dev/null
bao kv put -mount=secret requara/prod/smtp addr="${SMTP_ADDR}" from="${SMTP_FROM}" >/dev/null
# The session signing key is generated once and kept.
if ! bao kv get -mount=secret requara/prod/session >/dev/null 2>&1; then
  bao kv put -mount=secret requara/prod/session \
    hash_key="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')" >/dev/null
fi

# --- AppRole credentials for the API ---
bao read -field=role_id auth/approle/role/requara-backend/role-id > "$CREDS/role-id"
if [ ! -s "$CREDS/secret-id" ]; then
  bao write -f -field=secret_id auth/approle/role/requara-backend/secret-id > "$CREDS/secret-id"
fi
chmod 644 "$CREDS/role-id" "$CREDS/secret-id"
unset BAO_TOKEN

touch "$CREDS/ready"
echo "OpenBao ready for Requara"
wait $SERVER
