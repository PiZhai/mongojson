#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

printf '[deploy] deploy-prod.sh now forwards to deploy-release.sh for compatibility.\n'
exec "$SCRIPT_DIR/deploy-release.sh" "$@"
