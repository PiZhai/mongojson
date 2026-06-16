#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/_shared.sh"

require_command git
require_command docker

ensure_runtime_files
git_pull_latest
maybe_pull_images nginx

compose up -d --build frontend nginx
print_status
wait_for_url "$HEALTH_URL" "healthz"
wait_for_url "$READY_URL" "readyz"

log "Frontend release completed."
