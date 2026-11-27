#!/bin/sh
# The bundled OpenBao for the Helm chart (like ../../openbao/entrypoint.sh
# for Docker Compose):
#   1. starts the server (file storage on its volume)
#   2. first run: initializes it and keeps the unseal key on the same volume
#   3. every start: unseals it
#   4. writes Requara's settings and an AppRole login whose role and secret
#      IDs come from the chart's generated Secret, which Requara also reads
#
# Keeping the unseal key next to the data is the trade-off that makes this
# start unattended. For stricter setups use an external OpenBao (openbao.addr).
set -eu

export BAO_ADDR=http://127.0.0.1:8200
KEYS=/openbao/data/keys
mkdir -p "$KEYS" /openbao/data/store
chmod 700 "$KEYS"
rm -f /tmp/ready

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
bao write auth/approle/role/requara-backend/role-id role_id="$ROLE_ID" >/dev/null
# The secret ID can only be registered once; skip if it already logs in.
if ! bao write auth/approle/login role_id="$ROLE_ID" secret_id="$SECRET_ID" >/dev/null 2>&1; then
  bao write auth/approle/role/requara-backend/custom-secret-id secret_id="$SECRET_ID" >/dev/null
fi

# --- Requara's settings (rewritten each start so changes apply) ---
bao kv put -mount=secret requara/prod/postgres \
  url="postgres://requara:${POSTGRES_PASSWORD}@${POSTGRES_HOST}:5432/requara?sslmode=disable" >/dev/null
bao kv put -mount=secret requara/prod/s3 \
  endpoint="${S3_ENDPOINT}" access_key=requara secret_key="${S3_SECRET_KEY}" bucket=requara-bodies >/dev/null
bao kv put -mount=secret requara/prod/smtp addr="${SMTP_ADDR}" from="${SMTP_FROM}" >/dev/null
# The session signing key is generated once and kept.
if ! bao kv get -mount=secret requara/prod/session >/dev/null 2>&1; then
  bao kv put -mount=secret requara/prod/session \
    hash_key="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')" >/dev/null
fi
unset BAO_TOKEN

touch /tmp/ready
echo "OpenBao ready for Requara"
wait $SERVER
