#!/usr/bin/env bash

set -euo pipefail

APP_DIR="${APP_DIR:-/opt/personal-tooling/app}"
ENV_FILE="${ENV_FILE:-/opt/personal-tooling/env/prod.env}"
COMPOSE_FILE="${COMPOSE_FILE:-deploy/docker-compose.prod.yml}"

cd "$APP_DIR"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "Missing env file: $ENV_FILE" >&2
  exit 1
fi

if [[ ! -f /opt/personal-tooling/env/.htpasswd ]]; then
  echo "Missing Basic Auth file: /opt/personal-tooling/env/.htpasswd" >&2
  exit 1
fi

docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" pull || true
docker builder prune -f >/dev/null 2>&1 || true
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" up -d --build
docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" ps
