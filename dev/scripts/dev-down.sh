#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."  # repo root: .dev/ lives here
[ -f .dev/bao.pid ] && kill "$(cat .dev/bao.pid)" 2>/dev/null && rm -f .dev/bao.pid || true
docker compose -f dev/docker-compose.yml down
