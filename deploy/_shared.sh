#!/usr/bin/env bash

set -euo pipefail

APP_DIR="${APP_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
BASE_DIR="${BASE_DIR:-$(cd "$APP_DIR/.." && pwd)}"
ENV_DIR="${ENV_DIR:-$BASE_DIR/env}"
DATA_DIR="${DATA_DIR:-$BASE_DIR/data}"
LOG_DIR="${LOG_DIR:-$BASE_DIR/logs}"
BACKUP_DIR="${BACKUP_DIR:-$BASE_DIR/backups}"
ENV_FILE="${ENV_FILE:-$ENV_DIR/prod.env}"
COMPOSE_FILE="${COMPOSE_FILE:-$APP_DIR/deploy/docker-compose.prod.yml}"
HTPASSWD_FILE="${HTPASSWD_FILE:-$ENV_DIR/.htpasswd}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1/healthz}"
READY_URL="${READY_URL:-http://127.0.0.1/readyz}"

log() {
  printf '[deploy] %s\n' "$*"
}

die() {
  printf '[deploy] ERROR: %s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "Missing required command: $1"
}

ensure_file() {
  local path="$1"
  [[ -f "$path" ]] || die "Missing file: $path"
}

ensure_repo() {
  [[ -d "$APP_DIR/.git" ]] || die "Missing git repo: $APP_DIR"
}

ensure_compose_assets() {
  ensure_repo
  ensure_file "$COMPOSE_FILE"
  ensure_file "$APP_DIR/deploy/.env.prod.example"
}

ensure_runtime_env() {
  ensure_compose_assets
  ensure_file "$ENV_FILE"
}

ensure_runtime_files() {
  ensure_runtime_env
  ensure_file "$HTPASSWD_FILE"
}

compose() {
  (
    cd "$APP_DIR"
    docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"
  )
}

git_pull_latest() {
  ensure_repo
  log "Pulling latest code in $APP_DIR"
  git -C "$APP_DIR" pull --ff-only
}

maybe_pull_code() {
  if [[ "${SKIP_PULL:-0}" == "1" ]]; then
    log "Skipping git pull by request"
    return 0
  fi

  git_pull_latest
}

maybe_pull_images() {
  if [[ "${PULL_IMAGES:-0}" == "1" ]]; then
    log "Pulling remote image updates"
    compose pull "$@" || true
  fi
}

restart_nginx_gateway() {
  log "Restarting nginx gateway to refresh upstream container IPs"
  compose restart nginx
}

replace_postgres_password() {
  local password="$1"
  local tmp_file

  tmp_file="$(mktemp)"
  awk -v password="$password" '
    BEGIN { replaced = 0 }
    /^POSTGRES_PASSWORD=/ {
      print "POSTGRES_PASSWORD=" password
      replaced = 1
      next
    }
    { print }
    END {
      if (!replaced) {
        print "POSTGRES_PASSWORD=" password
      }
    }
  ' "$ENV_FILE" > "$tmp_file"
  mv "$tmp_file" "$ENV_FILE"
}

env_has_placeholder_password() {
  grep -Eq '^POSTGRES_PASSWORD=(replace_with_strong_password)?$' "$ENV_FILE"
}

create_default_dirs() {
  mkdir -p \
    "$APP_DIR" \
    "$ENV_DIR" \
    "$DATA_DIR/postgres" \
    "$DATA_DIR/backend" \
    "$LOG_DIR" \
    "$BACKUP_DIR"
}

wait_for_url() {
  local url="$1"
  local label="$2"
  local attempts="${3:-30}"
  local delay="${4:-2}"
  local i

  require_command curl

  for ((i = 1; i <= attempts; i += 1)); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      log "$label is ready: $url"
      return 0
    fi
    sleep "$delay"
  done

  die "Timed out waiting for $label: $url"
}

print_status() {
  log "Container status"
  compose ps
}
