#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/_shared.sh"

LOG_TAIL="${LOG_TAIL:-20}"

require_command docker

ensure_runtime_env
print_status

if command -v curl >/dev/null 2>&1; then
  log "Health checks"
  curl -fsS "$HEALTH_URL" || true
  printf '\n'
  curl -fsS "$READY_URL" || true
  printf '\n'
else
  log "curl not found, skip health checks"
fi

log "Recent logs (tail: $LOG_TAIL)"
compose logs --tail "$LOG_TAIL" nginx backend postgres
