#!/bin/sh
# Daily backup (§26). Run via Railway cron or host cron:
#   0 2 * * * DATABASE_URL=... /app/scripts/backup.sh
# Keeps 7 local copies; sync BACKUP_DIR to external storage (S3/R2).
set -eu
: "${DATABASE_URL:?DATABASE_URL required}"
BACKUP_DIR="${BACKUP_DIR:-/data/backups}"
mkdir -p "$BACKUP_DIR"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="$BACKUP_DIR/sellingbot-$TS.sql.gz"
pg_dump "$DATABASE_URL" | gzip > "$OUT"
ls -t "$BACKUP_DIR"/sellingbot-*.sql.gz | tail -n +8 | xargs -r rm --
echo "backup: $OUT"
