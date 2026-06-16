#!/usr/bin/env bash

set -euo pipefail

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf '[deploy] ERROR: Missing required command: %s\n' "$1" >&2
    exit 1
  }
}

require_command docker

printf '[deploy] Docker disk usage before cleanup\n'
docker system df

printf '[deploy] Removing stale build cache\n'
docker builder prune -f

if [[ "${PRUNE_IMAGES:-0}" == "1" ]]; then
  printf '[deploy] Removing dangling images\n'
  docker image prune -f
fi

if [[ "${PRUNE_SYSTEM:-0}" == "1" ]]; then
  printf '[deploy] Running broader system prune (volumes not included)\n'
  docker system prune -f
fi

printf '[deploy] Docker disk usage after cleanup\n'
docker system df
