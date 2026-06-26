#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/_shared.sh"

require_command git
require_command docker

ensure_runtime_files
ensure_image_env
maybe_pull_code

compose pull frontend nginx
compose up -d frontend nginx
restart_nginx_gateway
print_status
wait_for_url "$HEALTH_URL" "healthz"
wait_for_url "$READY_URL" "readyz"

log "Frontend release completed."
