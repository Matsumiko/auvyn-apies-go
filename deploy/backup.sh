#!/usr/bin/env bash
# Backup SQLite secara online. Jalankan dari cron harian.
set -euo pipefail
umask 077

DB_PATH="${DB_PATH:-/opt/auvyn-apies/data/auvyn.db}"
BACKUP_DIR="${BACKUP_DIR:-/opt/auvyn-apies/backup}"
KEEP_DAYS="${KEEP_DAYS:-14}"

mkdir -p "$BACKUP_DIR"
OUT="$BACKUP_DIR/auvyn-$(date +%F-%H%M).db"
sqlite3 "$DB_PATH" "VACUUM INTO '$OUT'"
find "$BACKUP_DIR" -type f -name 'auvyn-*.db' -mtime "+$KEEP_DAYS" -delete
echo "backup written: $OUT"
