#!/usr/bin/env bash
# NexusMart PostgreSQL daily backup script.
# Dynamically discovers all application databases, dumps them using an MVCC
# synchronized snapshot (pg_export_snapshot + pg_dump --snapshot), records
# exact per-table row counts in manifest.tsv, writes SHA256SUMS, and applies
# retention pruning.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

PG_CONTAINER="${PG_CONTAINER:-nexus_ecom-postgres-1}"
PG_HOST="${PG_HOST:-}"
PG_PORT="${PG_PORT:-5432}"
PG_USER="${PG_USER:-nexusmart}"
PG_DATABASES="${PG_DATABASES:-}"
BACKUP_DIR="${BACKUP_DIR:-/tmp/nexusmart-backups}"
RETENTION_DAYS="${RETENTION_DAYS:-7}"
DUMP_FORMAT="${DUMP_FORMAT:-custom}"
INCLUDE_GLOBALS="${INCLUDE_GLOBALS:-true}"
VERIFY_AFTER_BACKUP="false"

usage() {
  cat <<'EOF'
Usage: backup.sh [options]

Options:
  --container <name>       Target Postgres Docker container (default: nexus_ecom-postgres-1)
  --direct                 Use local psql/pg_dump over PGHOST/PGPORT instead of docker exec
  --user <user>            PostgreSQL role (default: nexusmart)
  --databases "<db1 db2>"  Space-separated DB list override (default: discover all non-system DBs)
  --backup-dir <dir>       Backup root directory (default: /tmp/nexusmart-backups)
  --format <custom|sql>    Dump format: custom (.dump, default) or sql (.sql.gz)
  --retention-days <days>  Delete snapshots older than N days; 0 disables pruning (default: 7)
  --no-globals             Skip pg_dumpall --globals-only
  --verify                 Run restore-drill.sh on the newly created snapshot
  -h, --help               Show this help

Environment variables:
  PG_CONTAINER, PG_HOST, PG_PORT, PG_USER, PGPASSWORD, PG_DATABASES,
  BACKUP_DIR, RETENTION_DAYS, DUMP_FORMAT, INCLUDE_GLOBALS
EOF
}

log() {
  printf '[%s] [backup] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"
}

die() {
  printf '[%s] [backup] ERROR: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --container)
      [[ $# -ge 2 ]] || die "--container requires an argument"
      PG_CONTAINER="$2"
      shift 2
      ;;
    --direct)
      PG_CONTAINER=""
      PG_HOST="${PG_HOST:-127.0.0.1}"
      shift
      ;;
    --user)
      [[ $# -ge 2 ]] || die "--user requires an argument"
      PG_USER="$2"
      shift 2
      ;;
    --databases)
      [[ $# -ge 2 ]] || die "--databases requires an argument"
      PG_DATABASES="$2"
      shift 2
      ;;
    --backup-dir)
      [[ $# -ge 2 ]] || die "--backup-dir requires an argument"
      BACKUP_DIR="$2"
      shift 2
      ;;
    --format)
      [[ $# -ge 2 ]] || die "--format requires an argument"
      DUMP_FORMAT="$2"
      shift 2
      ;;
    --format=*)
      DUMP_FORMAT="${1#*=}"
      shift
      ;;
    --retention-days)
      [[ $# -ge 2 ]] || die "--retention-days requires an argument"
      RETENTION_DAYS="$2"
      shift 2
      ;;
    --no-globals)
      INCLUDE_GLOBALS="false"
      shift
      ;;
    --verify)
      VERIFY_AFTER_BACKUP="true"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "Unknown argument: $1 (use --help)"
      ;;
  esac
done

case "$DUMP_FORMAT" in
  custom|sql) ;;
  *) die "Unsupported --format '$DUMP_FORMAT' (expected 'custom' or 'sql')" ;;
esac

if ! [[ "$RETENTION_DAYS" =~ ^[0-9]+$ ]]; then
  die "RETENTION_DAYS must be a non-negative integer, got '$RETENTION_DAYS'"
fi

USE_DOCKER="true"
if [[ -n "$PG_HOST" || -z "$PG_CONTAINER" ]]; then
  USE_DOCKER="false"
  PG_HOST="${PG_HOST:-127.0.0.1}"
fi

# Wrappers execute strictly read-only client commands against the source Postgres.
run_psql() {
  if [[ "$USE_DOCKER" == "true" ]]; then
    docker exec -i "$PG_CONTAINER" psql -U "$PG_USER" "$@"
  else
    psql -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" "$@"
  fi
}

run_pg_dump() {
  if [[ "$USE_DOCKER" == "true" ]]; then
    docker exec "$PG_CONTAINER" pg_dump -U "$PG_USER" "$@"
  else
    pg_dump -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" "$@"
  fi
}

run_pg_dumpall() {
  if [[ "$USE_DOCKER" == "true" ]]; then
    docker exec "$PG_CONTAINER" pg_dumpall -U "$PG_USER" "$@"
  else
    pg_dumpall -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" "$@"
  fi
}

STAGING_DIR=""
ACTIVE_SNAP_PID=""

cleanup() {
  local rc=$?
  exec 3>&- 2>/dev/null || true
  exec 4<&- 2>/dev/null || true
  if [[ -n "$ACTIVE_SNAP_PID" ]]; then
    kill "$ACTIVE_SNAP_PID" >/dev/null 2>&1 || true
    wait "$ACTIVE_SNAP_PID" >/dev/null 2>&1 || true
    ACTIVE_SNAP_PID=""
  fi
  if [[ -n "$STAGING_DIR" && -d "$STAGING_DIR" ]]; then
    rm -rf "$STAGING_DIR"
  fi
  exit "$rc"
}
trap cleanup EXIT INT TERM

# Preflight checks.
if [[ "$USE_DOCKER" == "true" ]]; then
  command -v docker >/dev/null 2>&1 || die "docker CLI not found in PATH"
  if [[ "$(docker inspect -f '{{.State.Running}}' "$PG_CONTAINER" 2>/dev/null || true)" != "true" ]]; then
    die "Postgres container '$PG_CONTAINER' is not running"
  fi
else
  command -v psql >/dev/null 2>&1 || die "psql not found in PATH"
  command -v pg_dump >/dev/null 2>&1 || die "pg_dump not found in PATH"
fi
command -v sha256sum >/dev/null 2>&1 || die "sha256sum not found in PATH"
if [[ "$DUMP_FORMAT" == "sql" ]]; then
  command -v gzip >/dev/null 2>&1 || die "gzip not found in PATH"
fi

PG_VERSION="$(run_psql -d postgres -X -Atc "SELECT current_setting('server_version');" </dev/null)"
[[ -n "$PG_VERSION" ]] || die "Failed to connect to PostgreSQL and read server_version"

# Discover databases dynamically unless PG_DATABASES is explicitly set.
declare -a DATABASES=()
if [[ -n "$PG_DATABASES" ]]; then
  read -r -a DATABASES <<< "$PG_DATABASES"
else
  while IFS= read -r db; do
    [[ -n "$db" ]] && DATABASES+=("$db")
  done < <(
    run_psql -d postgres -X -Atc \
      "SELECT datname FROM pg_database WHERE datistemplate = false AND datname <> 'postgres' ORDER BY datname;" \
      </dev/null
  )
fi

if [[ ${#DATABASES[@]} -eq 0 ]]; then
  die "No non-system databases discovered to back up"
fi

for db in "${DATABASES[@]}"; do
  if ! [[ "$db" =~ ^[A-Za-z0-9_.-]+$ ]]; then
    die "Refusing unsafe database name '$db'"
  fi
done

mkdir -p "$BACKUP_DIR"

TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
if [[ -e "${BACKUP_DIR}/${TIMESTAMP}" ]]; then
  sleep 1
  TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
fi
SNAPSHOT_DIR="${BACKUP_DIR}/${TIMESTAMP}"
STAGING_DIR="${BACKUP_DIR}/.${TIMESTAMP}.$$.tmp"
mkdir -p "$STAGING_DIR"

SOURCE_DESC="${PG_HOST}:${PG_PORT}"
if [[ "$USE_DOCKER" == "true" ]]; then
  SOURCE_DESC="docker:${PG_CONTAINER}"
fi

log "Starting backup snapshot=${TIMESTAMP} source=${SOURCE_DESC} pg=${PG_VERSION} format=${DUMP_FORMAT} databases=${#DATABASES[@]}"

MANIFEST_FILE="${STAGING_DIR}/manifest.tsv"
printf 'database\tschema.table\trow_count\n' > "$MANIFEST_FILE"

declare -a CHECKSUM_FILES=()

# Optional cluster globals (roles, tablespaces). Kept for full DR; restore-drill skips
# re-applying it by default to avoid bootstrap superuser conflicts.
if [[ "$INCLUDE_GLOBALS" == "true" ]]; then
  run_pg_dumpall --globals-only > "${STAGING_DIR}/globals.sql"
  CHECKSUM_FILES+=("globals.sql")
  log "Dumped cluster globals -> globals.sql ($(wc -c < "${STAGING_DIR}/globals.sql") bytes)"
fi

ROW_COUNT_SQL="$(cat <<'SQL'
SELECT n.nspname || '.' || c.relname || E'\t' ||
       (xpath('/row/cnt/text()', query_to_xml(
         format('SELECT count(*) AS cnt FROM %I.%I', n.nspname, c.relname),
         false, true, ''
       )))[1]::text::bigint
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind = 'r'
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg_toast%'
ORDER BY n.nspname, c.relname;
SQL
)"

TOTAL_TABLES=0
TOTAL_ROWS=0
START_EPOCH="$(date +%s)"

for db in "${DATABASES[@]}"; do
  db_start="$(date +%s)"

  # Hold a REPEATABLE READ READ ONLY transaction and export its MVCC snapshot so
  # pg_dump and exact per-table SELECT count(*) read the exact same point in time.
  coproc PSQL_SNAP {
    run_psql -d "$db" -X -q -v ON_ERROR_STOP=1 -At
  }
  ACTIVE_SNAP_PID="$PSQL_SNAP_PID"
  exec 3>&"${PSQL_SNAP[1]}" 4<&"${PSQL_SNAP[0]}"

  printf 'BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;\nSELECT pg_export_snapshot();\n' >&3
  if ! IFS= read -r -t 30 snap_id <&4; then
    die "Timed out exporting MVCC snapshot for database '${db}'"
  fi
  if ! [[ "$snap_id" =~ ^[0-9A-Fa-f]+-[0-9A-Fa-f]+-[0-9]+$ ]]; then
    die "Unexpected snapshot identifier '${snap_id}' for database '${db}'"
  fi

  if [[ "$DUMP_FORMAT" == "custom" ]]; then
    dump_name="${db}.dump"
    run_pg_dump -d "$db" --snapshot="$snap_id" --format=custom --compress=6 > "${STAGING_DIR}/${dump_name}"
  else
    dump_name="${db}.sql.gz"
    run_pg_dump -d "$db" --snapshot="$snap_id" --format=plain | gzip -6 > "${STAGING_DIR}/${dump_name}"
  fi
  CHECKSUM_FILES+=("$dump_name")

  printf '%s\nCOMMIT;\n\\q\n' "$ROW_COUNT_SQL" >&3
  exec 3>&-

  db_tables=0
  db_rows=0
  while IFS=$'\t' read -r tbl cnt; do
    [[ -n "$tbl" ]] || continue
    printf '%s\t%s\t%s\n' "$db" "$tbl" "$cnt" >> "$MANIFEST_FILE"
    db_tables=$((db_tables + 1))
    db_rows=$((db_rows + cnt))
  done <&4
  exec 4<&-

  wait "$ACTIVE_SNAP_PID" || die "Row-count snapshot transaction failed for database '${db}'"
  ACTIVE_SNAP_PID=""

  dump_bytes="$(wc -c < "${STAGING_DIR}/${dump_name}")"
  db_elapsed=$(( $(date +%s) - db_start ))
  TOTAL_TABLES=$((TOTAL_TABLES + db_tables))
  TOTAL_ROWS=$((TOTAL_ROWS + db_rows))

  log "Backed up ${db} -> ${dump_name} (${dump_bytes} bytes, ${db_tables} tables, ${db_rows} rows, ${db_elapsed}s)"
done

CHECKSUM_FILES+=("manifest.tsv")

# Sort checksum targets deterministically and generate SHA256SUMS.
mapfile -t SORTED_FILES < <(printf '%s\n' "${CHECKSUM_FILES[@]}" | LC_ALL=C sort)
(
  cd "$STAGING_DIR"
  sha256sum "${SORTED_FILES[@]}" > SHA256SUMS
)

cat > "${STAGING_DIR}/metadata.env" <<EOF
TIMESTAMP=${TIMESTAMP}
PG_VERSION=${PG_VERSION}
DUMP_FORMAT=${DUMP_FORMAT}
SOURCE=${SOURCE_DESC}
PG_USER=${PG_USER}
DB_COUNT=${#DATABASES[@]}
TOTAL_TABLES=${TOTAL_TABLES}
TOTAL_ROWS=${TOTAL_ROWS}
EOF

# Atomically publish the completed snapshot directory.
mv "$STAGING_DIR" "$SNAPSHOT_DIR"
STAGING_DIR=""
ln -sfn "$TIMESTAMP" "${BACKUP_DIR}/latest"

TOTAL_ELAPSED=$(( $(date +%s) - START_EPOCH ))
log "Snapshot complete: ${SNAPSHOT_DIR} (${#DATABASES[@]} DBs, ${TOTAL_TABLES} tables, ${TOTAL_ROWS} rows, ${TOTAL_ELAPSED}s)"

# Retention policy: prune completed timestamped snapshots older than RETENTION_DAYS.
if [[ "$RETENTION_DAYS" -gt 0 ]]; then
  pruned=0
  while IFS= read -r old_dir; do
    [[ -n "$old_dir" && "$old_dir" != "$SNAPSHOT_DIR" ]] || continue
    rm -rf "$old_dir"
    log "Pruned expired snapshot (>${RETENTION_DAYS}d): ${old_dir}"
    pruned=$((pruned + 1))
  done < <(
    find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d \
      -name '[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]T[0-9][0-9][0-9][0-9][0-9][0-9]Z' \
      -mtime "+${RETENTION_DAYS}" | LC_ALL=C sort
  )
  log "Retention check complete (RETENTION_DAYS=${RETENTION_DAYS}, pruned=${pruned})"
fi

if [[ "$VERIFY_AFTER_BACKUP" == "true" ]]; then
  log "Running post-backup restore drill (--verify)..."
  "${SCRIPT_DIR}/restore-drill.sh" "$SNAPSHOT_DIR"
fi
