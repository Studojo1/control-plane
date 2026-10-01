#!/usr/bin/env bash
# Nightly logical backup of EVERY database on one Postgres server (audit AR-D04).
# Runs inside the postgres-backup CronJobs (see cronjobs.yaml), image
# postgres:16-bookworm so pg_dump matches the 16.x servers.
#
# Env: POSTGRES_HOST POSTGRES_PORT POSTGRES_USER POSTGRES_PASSWORD
#      AZURE_STORAGE_ACCOUNT_NAME AZURE_STORAGE_ACCOUNT_KEY BACKUP_CONTAINER
#      RETENTION_DAYS (default 30)
#
# Blob layout in BACKUP_CONTAINER:
#   <db>/<db>-<YYYYmmdd-HHMMSS>.sql.gz        one plain-SQL dump per database
#   _globals/globals-<YYYYmmdd-HHMMSS>.sql.gz roles (no password hashes)
# Root-level postgres-backup-*.sql.gz blobs are the pre-2026-10-01 single-DB
# dumps (prod and staging: the "postgres" DB; bob: "bobprod").
set -euo pipefail

: "${POSTGRES_HOST:?}" "${POSTGRES_PORT:?}" "${POSTGRES_USER:?}" "${POSTGRES_PASSWORD:?}"
: "${AZURE_STORAGE_ACCOUNT_NAME:?}" "${AZURE_STORAGE_ACCOUNT_KEY:?}" "${BACKUP_CONTAINER:?}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "Installing the Azure Blob client..."
apt-get update -qq
apt-get install -y -qq --no-install-recommends python3-venv >/dev/null
python3 -m venv /tmp/venv
/tmp/venv/bin/pip install -q azure-storage-blob

export PGPASSWORD="${POSTGRES_PASSWORD}"
PG=(-h "${POSTGRES_HOST}" -p "${POSTGRES_PORT}" -U "${POSTGRES_USER}")
TS=$(date -u +%Y%m%d-%H%M%S)
OUT=/tmp/backup
mkdir -p "${OUT}"
pg_dump --version

upload() { # upload <local file> <blob name>
  /tmp/venv/bin/python "${SCRIPT_DIR}/blob.py" upload "$1" "$2"
  rm -f "$1"
}

mapfile -t DBS < <(psql "${PG[@]}" -d postgres -Atc \
  "select datname from pg_database where datallowconn and not datistemplate order by 1")
[ ${#DBS[@]} -gt 0 ] || { echo "no databases found on ${POSTGRES_HOST}"; exit 1; }
echo "Databases on ${POSTGRES_HOST}: ${DBS[*]}"

pg_dumpall "${PG[@]}" --globals-only --no-role-passwords | gzip -c > "${OUT}/globals.sql.gz"
upload "${OUT}/globals.sql.gz" "_globals/globals-${TS}.sql.gz"

for db in "${DBS[@]}"; do
  f="${OUT}/${db}.sql.gz"
  echo "Dumping ${db}..."
  pg_dump "${PG[@]}" -d "${db}" --no-owner --no-acl | gzip -c > "${f}"
  gzip -t "${f}"
  echo "  ${db}: $(du -m "${f}" | cut -f1) MB"
  upload "${f}" "${db}/${db}-${TS}.sql.gz"
done
unset PGPASSWORD

/tmp/venv/bin/python "${SCRIPT_DIR}/blob.py" prune "${RETENTION_DAYS:-30}"
echo "Backup completed: ${#DBS[@]} databases + globals -> ${BACKUP_CONTAINER}"
