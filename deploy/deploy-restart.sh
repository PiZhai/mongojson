#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/_shared.sh"

require_command docker

ensure_runtime_files

compose restart postgres mongo-script-analyzer backend frontend nginx
print_status
wait_for_url "$HEALTH_URL" "healthz"
wait_for_url "$READY_URL" "readyz"

log "Stack restart completed."
