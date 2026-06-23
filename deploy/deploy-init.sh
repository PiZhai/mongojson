#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/_shared.sh"

REPO_URL="${REPO_URL:-}"
BASIC_AUTH_USER="${BASIC_AUTH_USER:-admin}"
BASIC_AUTH_PASSWORD="${BASIC_AUTH_PASSWORD:-}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-}"

require_command git
require_command docker
require_command htpasswd

create_default_dirs

if [[ -d "$APP_DIR/.git" ]]; then
  log "Found existing repo in $APP_DIR"
  maybe_pull_code
else
  [[ -n "$REPO_URL" ]] || die "Missing git repo in $APP_DIR. Clone first or rerun with REPO_URL=<repo-url>."
  if [[ -n "$(find "$APP_DIR" -mindepth 1 -maxdepth 1 2>/dev/null)" ]]; then
    die "Target app dir is not empty: $APP_DIR"
  fi
  log "Cloning repo into $APP_DIR"
  git clone "$REPO_URL" "$APP_DIR"
fi

ensure_compose_assets

if [[ ! -f "$ENV_FILE" ]]; then
  cp "$APP_DIR/deploy/.env.prod.example" "$ENV_FILE"
  log "Created env file: $ENV_FILE"
fi

if env_has_placeholder_password; then
  [[ -n "$POSTGRES_PASSWORD" ]] || die "Set POSTGRES_PASSWORD before first deploy, or edit $ENV_FILE manually."
  replace_postgres_password "$POSTGRES_PASSWORD"
  log "Updated POSTGRES_PASSWORD in $ENV_FILE"
fi

if [[ ! -f "$HTPASSWD_FILE" ]]; then
  [[ -n "$BASIC_AUTH_PASSWORD" ]] || die "Set BASIC_AUTH_PASSWORD before first deploy, or create $HTPASSWD_FILE manually."
  htpasswd -bc "$HTPASSWD_FILE" "$BASIC_AUTH_USER" "$BASIC_AUTH_PASSWORD"
  log "Created Basic Auth file: $HTPASSWD_FILE"
fi

compose up -d --build
restart_nginx_gateway
print_status
wait_for_url "$HEALTH_URL" "healthz"
wait_for_url "$READY_URL" "readyz"

log "Initial deployment completed."
