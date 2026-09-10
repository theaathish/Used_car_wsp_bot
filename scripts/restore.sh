#!/bin/sh
# Restore drill (§26): restores a backup into a FRESH database and verifies
# row counts. Usage: DATABASE_URL=<target> ./restore.sh <backup.sql.gz>
set -eu
: "${DATABASE_URL:?DATABASE_URL required}"
F="${1:?backup file required}"
gunzip -c "$F" | psql "$DATABASE_URL" -q -v ON_ERROR_STOP=1
for t in customers leads conversations messages vehicles requirements vehicle_matches test_drives followups bookings users; do
  printf "%-16s " "$t"
  psql "$DATABASE_URL" -tAc "SELECT COUNT(*) FROM $t"
done
