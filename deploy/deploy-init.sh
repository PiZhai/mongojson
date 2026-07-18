#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/_shared.sh"

REPO_URL="${REPO_URL:-}"
BASIC_AUTH_USER="${BASIC_AUTH_USER:-admin}"
BASIC_AUTH_PASSWORD="${BASIC_AUTH_PASSWORD:-}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-}"
STEWARD_MANAGEMENT_AUTH_TOKEN="${STEWARD_MANAGEMENT_AUTH_TOKEN:-}"
STEWARD_PUBLIC_ORIGIN="${STEWARD_PUBLIC_ORIGIN:-}"
BACKEND_IMAGE="${BACKEND_IMAGE:-}"
FRONTEND_IMAGE="${FRONTEND_IMAGE:-}"

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
  chmod 600 "$ENV_FILE"
  log "Created env file: $ENV_FILE"
fi

if env_has_placeholder_value "STEWARD_MANAGEMENT_AUTH_TOKEN" "replace_with_random_32_character_management_token"; then
  if [[ -z "$STEWARD_MANAGEMENT_AUTH_TOKEN" ]]; then
    STEWARD_MANAGEMENT_AUTH_TOKEN="$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')"
  fi
  [[ ${#STEWARD_MANAGEMENT_AUTH_TOKEN} -ge 32 ]] || die "STEWARD_MANAGEMENT_AUTH_TOKEN must contain at least 32 characters."
  replace_env_value "STEWARD_MANAGEMENT_AUTH_TOKEN" "$STEWARD_MANAGEMENT_AUTH_TOKEN"
  log "Generated and stored STEWARD_MANAGEMENT_AUTH_TOKEN in the protected env file"
fi

if env_has_placeholder_value "STEWARD_PUBLIC_ORIGIN" "https://steward.example.com"; then
  [[ "$STEWARD_PUBLIC_ORIGIN" =~ ^https://[^/?#[:space:]]+$ ]] || die "Set STEWARD_PUBLIC_ORIGIN to the public HTTPS origin, for example https://steward.your-company.com."
  replace_env_value "STEWARD_PUBLIC_ORIGIN" "$STEWARD_PUBLIC_ORIGIN"
  log "Stored STEWARD_PUBLIC_ORIGIN in $ENV_FILE"
fi

if env_has_placeholder_password; then
  [[ -n "$POSTGRES_PASSWORD" ]] || die "Set POSTGRES_PASSWORD before first deploy, or edit $ENV_FILE manually."
  replace_postgres_password "$POSTGRES_PASSWORD"
  log "Updated POSTGRES_PASSWORD in $ENV_FILE"
fi

if env_has_placeholder_value "BACKEND_IMAGE" "replace_with_backend_image"; then
  [[ -n "$BACKEND_IMAGE" ]] || die "Set BACKEND_IMAGE before first deploy, or edit $ENV_FILE manually."
  replace_env_value "BACKEND_IMAGE" "$BACKEND_IMAGE"
  log "Updated BACKEND_IMAGE in $ENV_FILE"
fi

if env_has_placeholder_value "FRONTEND_IMAGE" "replace_with_frontend_image"; then
  [[ -n "$FRONTEND_IMAGE" ]] || die "Set FRONTEND_IMAGE before first deploy, or edit $ENV_FILE manually."
  replace_env_value "FRONTEND_IMAGE" "$FRONTEND_IMAGE"
  log "Updated FRONTEND_IMAGE in $ENV_FILE"
fi

if [[ ! -f "$HTPASSWD_FILE" ]]; then
  [[ -n "$BASIC_AUTH_PASSWORD" ]] || die "Set BASIC_AUTH_PASSWORD before first deploy, or create $HTPASSWD_FILE manually."
  htpasswd -bc "$HTPASSWD_FILE" "$BASIC_AUTH_USER" "$BASIC_AUTH_PASSWORD"
  log "Created Basic Auth file: $HTPASSWD_FILE"
fi

ensure_image_env
ensure_runtime_files
compose pull backend frontend nginx postgres
compose up -d
restart_nginx_gateway
print_status
wait_for_url "$HEALTH_URL" "healthz"
wait_for_url "$READY_URL" "readyz"

log "Initial deployment completed."
