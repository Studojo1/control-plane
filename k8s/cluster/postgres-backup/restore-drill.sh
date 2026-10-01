#!/usr/bin/env bash
# Restore drill (audit AR-D04): stream the newest dump of DRILL_DB from
# BACKUP_CONTAINER into a throwaway database on STAGING Postgres, print exact
# row counts per table, then drop the database. Compare the counts with the
# source (same query on the source DB). See restore-drill-job.yaml.
#
# Env: as backup.sh (staging postgres-backup-secret) plus DRILL_DB, BACKUP_CONTAINER.
set -euo pipefail

: "${POSTGRES_HOST:?}" "${POSTGRES_PORT:?}" "${POSTGRES_USER:?}" "${POSTGRES_PASSWORD:?}"
: "${AZURE_STORAGE_ACCOUNT_NAME:?}" "${AZURE_STORAGE_ACCOUNT_KEY:?}" "${BACKUP_CONTAINER:?}" "${DRILL_DB:?}"
case "${POSTGRES_HOST}" in
  *.staging.*) ;;
  *) echo "refusing: the drill restores only into staging Postgres (got ${POSTGRES_HOST})"; exit 1 ;;
esac
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

apt-get update -qq
apt-get install -y -qq --no-install-recommends python3-venv >/dev/null
python3 -m venv /tmp/venv
/tmp/venv/bin/pip install -q azure-storage-blob
BLOB=(/tmp/venv/bin/python "${SCRIPT_DIR}/blob.py")

export PGPASSWORD="${POSTGRES_PASSWORD}"
PG=(-h "${POSTGRES_HOST}" -p "${POSTGRES_PORT}" -U "${POSTGRES_USER}")
TARGET="drill_$(echo "${DRILL_DB}" | tr -c 'a-z0-9_\n' '_')_$(date -u +%Y%m%d%H%M%S)"

NAME=$("${BLOB[@]}" latest "${DRILL_DB}/")
echo "Restoring ${BACKUP_CONTAINER}/${NAME} into ${TARGET} on ${POSTGRES_HOST}"

cleanup() { psql "${PG[@]}" -d postgres -qc "DROP DATABASE IF EXISTS ${TARGET} WITH (FORCE)" && echo "Dropped ${TARGET}"; }
trap cleanup EXIT
psql "${PG[@]}" -d postgres -qc "CREATE DATABASE ${TARGET}"

start=$(date +%s)
"${BLOB[@]}" cat "${NAME}" | gunzip -c | psql "${PG[@]}" -d "${TARGET}" -q -o /dev/null 2>/tmp/restore.err || true
errors=$(grep -c "ERROR" /tmp/restore.err || true)
echo "Restore took $(( $(date +%s) - start ))s with ${errors} error(s)"
grep "ERROR" /tmp/restore.err | sort | uniq -c | head -20 || true

echo "Row counts (schema.table count):"
psql "${PG[@]}" -d "${TARGET}" -At -F ' ' -c "
  SELECT n.nspname || '.' || c.relname,
         (xpath('/row/c/text()', query_to_xml(format('select count(*) as c from %I.%I', n.nspname, c.relname), false, true, '')))[1]::text::bigint
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE c.relkind IN ('r', 'p') AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  ORDER BY 1"
