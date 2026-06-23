#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/_shared.sh"

require_command git
require_command docker

ensure_runtime_files
maybe_pull_code
maybe_pull_images nginx postgres

compose up -d --build backend nginx
print_status
wait_for_url "$HEALTH_URL" "healthz"
wait_for_url "$READY_URL" "readyz"

log "Backend release completed."
