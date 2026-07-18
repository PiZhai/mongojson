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
TLS_DIR="${TLS_DIR:-$ENV_DIR/tls}"
TLS_CERT_FILE="${TLS_CERT_FILE:-$TLS_DIR/fullchain.pem}"
TLS_KEY_FILE="${TLS_KEY_FILE:-$TLS_DIR/privkey.pem}"
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
  chmod 600 "$ENV_FILE"
  local management_token
  management_token="$(awk -F= '/^STEWARD_MANAGEMENT_AUTH_TOKEN=/{sub(/^[^=]*=/, ""); print; exit}' "$ENV_FILE")"
  if [[ ${#management_token} -lt 32 || "$management_token" == "replace_with_random_32_character_management_token" ]]; then
    die "Set STEWARD_MANAGEMENT_AUTH_TOKEN to a random value of at least 32 characters in $ENV_FILE before deploying."
  fi
  local public_origin
  public_origin="$(awk -F= '/^STEWARD_PUBLIC_ORIGIN=/{sub(/^[^=]*=/, ""); print; exit}' "$ENV_FILE")"
  if [[ "$public_origin" == "https://steward.example.com" || ! "$public_origin" =~ ^https://[^/?#[:space:]]+$ ]]; then
    die "Replace the sample STEWARD_PUBLIC_ORIGIN with the externally visible HTTPS origin (for example https://steward.your-company.com) in $ENV_FILE."
  fi
}

ensure_runtime_files() {
  ensure_runtime_env
  ensure_file "$HTPASSWD_FILE"
  ensure_file "$TLS_CERT_FILE"
  ensure_file "$TLS_KEY_FILE"
  chmod 600 "$HTPASSWD_FILE"
  chmod 600 "$TLS_KEY_FILE"
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

env_has_placeholder_value() {
  local key="$1"
  local placeholder="$2"

  if ! grep -Eq "^${key}=" "$ENV_FILE"; then
    return 0
  fi

  grep -Eq "^${key}=(${placeholder})?$" "$ENV_FILE"
}

replace_env_value() {
  local key="$1"
  local value="$2"
  local tmp_file

  tmp_file="$(mktemp)"
  awk -v key="$key" -v value="$value" '
    BEGIN { replaced = 0 }
    $0 ~ "^" key "=" {
      print key "=" value
      replaced = 1
      next
    }
    { print }
    END {
      if (!replaced) {
        print key "=" value
      }
    }
  ' "$ENV_FILE" > "$tmp_file"
  mv "$tmp_file" "$ENV_FILE"
}

ensure_image_env() {
  ensure_runtime_env

  if env_has_placeholder_value "BACKEND_IMAGE" "replace_with_backend_image"; then
    die "Set BACKEND_IMAGE in $ENV_FILE before deploying."
  fi

  if env_has_placeholder_value "FRONTEND_IMAGE" "replace_with_frontend_image"; then
    die "Set FRONTEND_IMAGE in $ENV_FILE before deploying."
  fi
}

create_default_dirs() {
  mkdir -p \
    "$APP_DIR" \
    "$ENV_DIR" \
    "$TLS_DIR" \
    "$DATA_DIR/postgres" \
    "$DATA_DIR/backend" \
    "$LOG_DIR" \
    "$BACKUP_DIR"
  chmod 700 "$ENV_DIR" "$TLS_DIR"
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
