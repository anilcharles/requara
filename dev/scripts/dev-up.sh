#!/usr/bin/env bash
# Starts everything Requara needs locally (except the backend and frontend themselves).
set -euo pipefail
cd "$(dirname "$0")/../.."  # repo root: .dev/ lives here
mkdir -p .dev
docker compose -f dev/docker-compose.yml up -d
if ! curl -sf http://127.0.0.1:8200/v1/sys/health >/dev/null; then
  echo "starting OpenBao (dev mode) on :8200"
  nohup bao server -dev -dev-root-token-id=root -dev-listen-address=127.0.0.1:8200 > .dev/bao.log 2>&1 &
  echo $! > .dev/bao.pid
fi
printf "waiting for OpenBao"; until curl -sf http://127.0.0.1:8200/v1/sys/health >/dev/null; do printf .; sleep 1; done; echo
printf "waiting for Keycloak"; until curl -sf http://localhost:8081/realms/acme/.well-known/openid-configuration >/dev/null; do printf .; sleep 2; done; echo
./dev/scripts/bao-bootstrap.sh
echo
echo "Keycloak   http://localhost:8081  (admin/admin; realm acme, user alex.chen / requara-dev)"
echo "OpenBao    http://localhost:8200  (root token: root, or sign in with OIDC)"
echo "S3         http://localhost:9000  (SeaweedFS; requara / requara-dev-s3)"
echo "Mailpit    http://localhost:8025"
