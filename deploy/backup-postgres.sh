#!/usr/bin/env bash

set -euo pipefail

APP_DIR="${APP_DIR:-/opt/personal-tooling/app}"
ENV_FILE="${ENV_FILE:-/opt/personal-tooling/env/prod.env}"
COMPOSE_FILE="${COMPOSE_FILE:-deploy/docker-compose.prod.yml}"
BACKUP_DIR="${BACKUP_DIR:-/opt/personal-tooling/backups}"
DB_NAME="${DB_NAME:-mongojson}"
DB_USER="${DB_USER:-tooling_app}"

mkdir -p "$BACKUP_DIR"

cd "$APP_DIR"

timestamp="$(date +%F-%H%M%S)"
output_file="$BACKUP_DIR/${DB_NAME}-${timestamp}.sql"

docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE" exec -T postgres \
  pg_dump -U "$DB_USER" -d "$DB_NAME" > "$output_file"

gzip -f "$output_file"
find "$BACKUP_DIR" -type f -name "${DB_NAME}-*.sql.gz" -mtime +14 -delete

echo "Backup created: ${output_file}.gz"

