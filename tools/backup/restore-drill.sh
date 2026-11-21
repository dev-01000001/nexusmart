#!/usr/bin/env bash
# NexusMart PostgreSQL restore drill and exact row-count verifier.
#
# Steps:
#   1. Verify SHA256SUMS integrity for every file in the snapshot.
#   2. Spin up an isolated temporary Postgres container (default: f-test-restore-pg)
#      with trap cleanup on EXIT/INT/TERM.
#   3. Create each database and restore its .dump or .sql.gz archive.
#   4. Query exact SELECT count(*) for EVERY ordinary table in EVERY restored DB
#      and compare 1:1 against manifest.tsv.
#   5. Print a summary table and exit 0 ONLY if checksums, restores, and 100% of
#      table row counts match exactly.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

BACKUP_DIR="${BACKUP_DIR:-/tmp/nexusmart-backups}"
RESTORE_CONTAINER="${RESTORE_CONTAINER:-f-test-restore-pg}"
PG_IMAGE="${PG_IMAGE:-postgres:16-alpine}"
RESTORE_USER="${RESTORE_USER:-nexusmart}"
RUN_FRESH="false"
SNAPSHOT_DIR=""

usage() {
  cat <<'EOF'
Usage: restore-drill.sh [options] [snapshot_dir]

Options:
  --fresh                Run backup.sh first into BACKUP_DIR, then drill the new snapshot
  --backup-dir <dir>     Backup root directory used to find latest snapshot (default: /tmp/nexusmart-backups)
  --container <name>     Isolated temporary container name (default: f-test-restore-pg)
  --image <image>        Postgres image for restore container (default: postgres:16-alpine;
                         use pgvector/pgvector:pg16 when snapshots include pgvector DBs)
  --user <user>          PostgreSQL superuser inside temporary container (default: nexusmart)
  -h, --help             Show this help

Environment variables:
  BACKUP_DIR, RESTORE_CONTAINER, PG_IMAGE, RESTORE_USER
EOF
}

log() {
  printf '[%s] [restore-drill] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"
}

die() {
  printf '[%s] [restore-drill] ERROR: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >&2
  exit 1
}

now_ms() {
  local ms
  ms="$(date +%s%3N 2>/dev/null || true)"
  if [[ "$ms" =~ ^[0-9]+$ ]]; then
    printf '%s' "$ms"
  else
    printf '%s000' "$(date +%s)"
  fi
}

fmt_ms() {
  local ms="$1"
  awk -v m="$ms" 'BEGIN { printf "%.2fs", m / 1000.0 }'
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --fresh)
      RUN_FRESH="true"
      shift
      ;;
    --backup-dir)
      [[ $# -ge 2 ]] || die "--backup-dir requires an argument"
      BACKUP_DIR="$2"
      shift 2
      ;;
    --container)
      [[ $# -ge 2 ]] || die "--container requires an argument"
      RESTORE_CONTAINER="$2"
      shift 2
      ;;
    --image)
      [[ $# -ge 2 ]] || die "--image requires an argument"
      PG_IMAGE="$2"
      shift 2
      ;;
    --user)
      [[ $# -ge 2 ]] || die "--user requires an argument"
      RESTORE_USER="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      die "Unknown option: $1 (use --help)"
      ;;
    *)
      if [[ -n "$SNAPSHOT_DIR" ]]; then
        die "Unexpected extra argument: $1"
      fi
      SNAPSHOT_DIR="$1"
      shift
      ;;
  esac
done

command -v docker >/dev/null 2>&1 || die "docker CLI not found in PATH"
command -v sha256sum >/dev/null 2>&1 || die "sha256sum not found in PATH"

if [[ "$RUN_FRESH" == "true" ]]; then
  [[ -z "$SNAPSHOT_DIR" ]] || die "Do not pass both --fresh and a snapshot_dir"
  log "Running fresh backup via backup.sh into ${BACKUP_DIR}..."
  BACKUP_DIR="$BACKUP_DIR" "${SCRIPT_DIR}/backup.sh"
fi

if [[ -z "$SNAPSHOT_DIR" ]]; then
  if [[ -L "${BACKUP_DIR}/latest" && -d "${BACKUP_DIR}/latest" ]]; then
    SNAPSHOT_DIR="$(cd -P -- "${BACKUP_DIR}/latest" && pwd -P)"
  else
    latest_dir="$(
      find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d \
        -name '[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]T[0-9][0-9][0-9][0-9][0-9][0-9]Z' \
        2>/dev/null | LC_ALL=C sort | tail -n 1
    )"
    [[ -n "$latest_dir" ]] || die "No backup snapshots found in '${BACKUP_DIR}'"
    SNAPSHOT_DIR="$latest_dir"
  fi
fi

[[ -d "$SNAPSHOT_DIR" ]] || die "Snapshot directory does not exist: ${SNAPSHOT_DIR}"
SNAPSHOT_DIR="$(cd -P -- "$SNAPSHOT_DIR" && pwd -P)"
[[ -f "${SNAPSHOT_DIR}/SHA256SUMS" ]] || die "Missing ${SNAPSHOT_DIR}/SHA256SUMS"
[[ -f "${SNAPSHOT_DIR}/manifest.tsv" ]] || die "Missing ${SNAPSHOT_DIR}/manifest.tsv"

# Discover archive files in snapshot.
declare -a DUMP_FILES=()
while IFS= read -r f; do
  [[ -n "$f" ]] && DUMP_FILES+=("$f")
done < <(
  find "$SNAPSHOT_DIR" -maxdepth 1 -type f \( -name '*.dump' -o -name '*.sql.gz' \) | LC_ALL=C sort
)
if [[ ${#DUMP_FILES[@]} -eq 0 ]]; then
  die "No .dump or .sql.gz database archives found in ${SNAPSHOT_DIR}"
fi

DRILL_START_MS="$(now_ms)"
STARTED_CONTAINER="false"
TMP_DIR="$(mktemp -d -t nexusmart-restore-drill.XXXXXX)"

cleanup() {
  local rc=$?
  if [[ "$STARTED_CONTAINER" == "true" ]]; then
    log "Cleaning up temporary container ${RESTORE_CONTAINER}..."
    docker rm -f "$RESTORE_CONTAINER" >/dev/null 2>&1 || true
  fi
  if [[ -n "$TMP_DIR" && -d "$TMP_DIR" ]]; then
    rm -rf "$TMP_DIR"
  fi
  exit "$rc"
}
trap cleanup EXIT INT TERM

# Step 1: Verify SHA256SUMS.
log "Step 1/4: Verifying SHA256SUMS in ${SNAPSHOT_DIR}"
(
  cd "$SNAPSHOT_DIR"
  sha256sum -c --strict SHA256SUMS
) || die "SHA256SUMS verification failed"

# Ensure no conflicting container already exists before starting our isolated instance.
if docker ps -a --format '{{.Names}}' | grep -Fxq "$RESTORE_CONTAINER"; then
  die "Container '${RESTORE_CONTAINER}' already exists; remove it or pass --container <name>"
fi

# Step 2: Spin up isolated temporary Postgres container (no host ports published).
log "Step 2/4: Starting isolated Postgres container '${RESTORE_CONTAINER}' (image=${PG_IMAGE})"
RESTORE_PASSWORD="drill_${$}_$(date +%s)"
STARTED_CONTAINER="true"
docker run -d \
  --name "$RESTORE_CONTAINER" \
  -e "POSTGRES_USER=${RESTORE_USER}" \
  -e "POSTGRES_PASSWORD=${RESTORE_PASSWORD}" \
  -e "POSTGRES_DB=postgres" \
  "$PG_IMAGE" \
  postgres -c fsync=off -c synchronous_commit=off -c full_page_writes=off >/dev/null

# Wait for the real TCP listener on 127.0.0.1 inside the container (not the temporary initdb socket).
ready="false"
for _ in $(seq 1 60); do
  if docker exec "$RESTORE_CONTAINER" pg_isready -h 127.0.0.1 -U "$RESTORE_USER" -d postgres >/dev/null 2>&1 \
     && docker exec "$RESTORE_CONTAINER" psql -h 127.0.0.1 -U "$RESTORE_USER" -d postgres -X -Atc "SELECT 1;" >/dev/null 2>&1; then
    ready="true"
    break
  fi
  sleep 0.5
done
[[ "$ready" == "true" ]] || die "Temporary Postgres container '${RESTORE_CONTAINER}' did not become ready in time"

AVAILABLE_EXTS="$(
  docker exec "$RESTORE_CONTAINER" psql -h 127.0.0.1 -U "$RESTORE_USER" -d postgres -X -Atc \
    "SELECT name FROM pg_available_extensions;"
)"

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

# Step 3 & 4: Restore every database archive and verify exact per-table row counts.
log "Step 3/4: Restoring ${#DUMP_FILES[@]} databases into '${RESTORE_CONTAINER}' and verifying exact row counts"

declare -a SUMMARY_LINES=()
declare -a RESTORED_DBS=()
OVERALL_FAILED=0
GRAND_TABLES=0
GRAND_EXPECTED=0
GRAND_RESTORED=0
GRAND_RESTORE_MS=0

for dump_file in "${DUMP_FILES[@]}"; do
  base="$(basename -- "$dump_file")"
  if [[ "$base" == *.dump ]]; then
    db="${base%.dump}"
    dump_kind="custom"
  else
    db="${base%.sql.gz}"
    dump_kind="sql"
  fi
  RESTORED_DBS+=("$db")

  # Extract expected table counts for this DB from manifest.tsv.
  expected_file="${TMP_DIR}/${db}.expected.tsv"
  actual_file="${TMP_DIR}/${db}.actual.tsv"
  restore_err="${TMP_DIR}/${db}.restore.err"

  awk -F'\t' -v target="$db" 'NR > 1 && $1 == target { print $2 "\t" $3 }' "${SNAPSHOT_DIR}/manifest.tsv" \
    | LC_ALL=C sort > "$expected_file"

  expected_tables="$(wc -l < "$expected_file" | tr -d ' ')"
  expected_rows="$(awk -F'\t' '{ s += $2 } END { printf "%d", s + 0 }' "$expected_file")"

  # Detect missing extensions (e.g. pgvector 'vector' in search_db) cleanly before restore.
  missing_ext=""
  if [[ "$dump_kind" == "custom" ]]; then
    while IFS= read -r ext; do
      [[ -n "$ext" ]] || continue
      if ! printf '%s\n' "$AVAILABLE_EXTS" | grep -Fxq "$ext"; then
        missing_ext="$ext"
        break
      fi
    done < <(
      docker exec -i "$RESTORE_CONTAINER" pg_restore -l < "$dump_file" 2>/dev/null \
        | awk '$4 == "EXTENSION" && $5 == "-" { print $6 }'
    )
  else
    if gzip -dc "$dump_file" | grep -Eq 'CREATE EXTENSION.*"?vector"?'; then
      if ! printf '%s\n' "$AVAILABLE_EXTS" | grep -Fxq "vector"; then
        missing_ext="vector"
      fi
    fi
  fi

  if [[ -n "$missing_ext" ]]; then
    log "ERROR: ${db} requires extension '${missing_ext}' which is unavailable in ${PG_IMAGE}."
    if [[ "$missing_ext" == "vector" ]]; then
      log "HINT: Re-run with PG_IMAGE=pgvector/pgvector:pg16 to verify pgvector databases."
    fi
    SUMMARY_LINES+=("$(printf '%-20s %6s %14s %14s %10s   %s' "$db" "$expected_tables" "$expected_rows" "N/A" "0.00s" "FAIL(ext:${missing_ext})")")
    OVERALL_FAILED=1
    GRAND_TABLES=$((GRAND_TABLES + expected_tables))
    GRAND_EXPECTED=$((GRAND_EXPECTED + expected_rows))
    continue
  fi

  # Safely create database using format('%I').
  if ! docker exec -i "$RESTORE_CONTAINER" psql -h 127.0.0.1 -U "$RESTORE_USER" -d postgres \
       -X -q -v ON_ERROR_STOP=1 -v dbname="$db" <<'SQL'
SELECT format('CREATE DATABASE %I', :'dbname') \gexec
SQL
  then
    log "ERROR: Failed to create database '${db}'"
    SUMMARY_LINES+=("$(printf '%-20s %6s %14s %14s %10s   %s' "$db" "$expected_tables" "$expected_rows" "N/A" "0.00s" "FAIL(createdb)")")
    OVERALL_FAILED=1
    GRAND_TABLES=$((GRAND_TABLES + expected_tables))
    GRAND_EXPECTED=$((GRAND_EXPECTED + expected_rows))
    continue
  fi

  t0="$(now_ms)"
  restore_ok="true"
  if [[ "$dump_kind" == "custom" ]]; then
    if ! docker exec -i "$RESTORE_CONTAINER" pg_restore \
         -h 127.0.0.1 -U "$RESTORE_USER" -d "$db" \
         --no-owner --no-privileges --exit-on-error < "$dump_file" 2>"$restore_err"; then
      restore_ok="false"
    fi
  else
    if ! gzip -dc "$dump_file" \
         | docker exec -i "$RESTORE_CONTAINER" psql \
           -h 127.0.0.1 -U "$RESTORE_USER" -d "$db" \
           -X -q -v ON_ERROR_STOP=1 >/dev/null 2>"$restore_err"; then
      restore_ok="false"
    fi
  fi
  db_ms=$(( $(now_ms) - t0 ))
  GRAND_RESTORE_MS=$((GRAND_RESTORE_MS + db_ms))

  if [[ "$restore_ok" != "true" ]]; then
    log "ERROR: Restore failed for '${db}':"
    sed 's/^/  /' "$restore_err" >&2 || true
    SUMMARY_LINES+=("$(printf '%-20s %6s %14s %14s %10s   %s' "$db" "$expected_tables" "$expected_rows" "N/A" "$(fmt_ms "$db_ms")" "FAIL(restore)")")
    OVERALL_FAILED=1
    GRAND_TABLES=$((GRAND_TABLES + expected_tables))
    GRAND_EXPECTED=$((GRAND_EXPECTED + expected_rows))
    continue
  fi

  # Step 4: Exact row counts on EVERY ordinary table in the restored DB.
  docker exec -i "$RESTORE_CONTAINER" psql -h 127.0.0.1 -U "$RESTORE_USER" -d "$db" \
    -X -q -v ON_ERROR_STOP=1 -At <<< "$ROW_COUNT_SQL" \
    | LC_ALL=C sort > "$actual_file"

  restored_tables="$(wc -l < "$actual_file" | tr -d ' ')"
  restored_rows="$(awk -F'\t' '{ s += $2 } END { printf "%d", s + 0 }' "$actual_file")"

  GRAND_TABLES=$((GRAND_TABLES + expected_tables))
  GRAND_EXPECTED=$((GRAND_EXPECTED + expected_rows))
  GRAND_RESTORED=$((GRAND_RESTORED + restored_rows))

  if cmp -s "$expected_file" "$actual_file"; then
    status="PASS"
  else
    status="FAIL(rowcount)"
    OVERALL_FAILED=1
    log "MISMATCH detected in database '${db}' (expected_tables=${expected_tables} restored_tables=${restored_tables}):"
    diff -u --label "manifest:${db}" --label "restored:${db}" "$expected_file" "$actual_file" >&2 || true
  fi

  SUMMARY_LINES+=("$(printf '%-20s %6s %14s %14s %10s   %s' "$db" "$restored_tables" "$expected_rows" "$restored_rows" "$(fmt_ms "$db_ms")" "$status")")
done

# Ensure every database referenced in manifest.tsv actually had a dump restored.
while IFS= read -r manifest_db; do
  [[ -n "$manifest_db" ]] || continue
  found="false"
  for rdb in "${RESTORED_DBS[@]}"; do
    if [[ "$rdb" == "$manifest_db" ]]; then
      found="true"
      break
    fi
  done
  if [[ "$found" != "true" ]]; then
    log "ERROR: Database '${manifest_db}' appears in manifest.tsv but no dump archive was found!"
    SUMMARY_LINES+=("$(printf '%-20s %6s %14s %14s %10s   %s' "$manifest_db" "-" "-" "0" "0.00s" "FAIL(missing-dump)")")
    OVERALL_FAILED=1
  fi
done < <(awk -F'\t' 'NR > 1 && $1 != "" { print $1 }' "${SNAPSHOT_DIR}/manifest.tsv" | LC_ALL=C sort -u)

DRILL_ELAPSED_MS=$(( $(now_ms) - DRILL_START_MS ))
FINAL_STATUS="PASS"
if [[ "$OVERALL_FAILED" -ne 0 ]]; then
  FINAL_STATUS="FAIL"
fi

log "Step 4/4: Restore drill summary (snapshot=${SNAPSHOT_DIR}, total_drill_time=$(fmt_ms "$DRILL_ELAPSED_MS"))"
printf '\n'
printf '%-20s %6s %14s %14s %10s   %s\n' "DATABASE" "TABLES" "EXPECTED_ROWS" "RESTORED_ROWS" "RESTORE_S" "STATUS"
printf '%s\n' "--------------------------------------------------------------------------------"
for line in "${SUMMARY_LINES[@]}"; do
  printf '%s\n' "$line"
done
printf '%s\n' "--------------------------------------------------------------------------------"
printf '%-20s %6s %14s %14s %10s   %s\n' \
  "TOTAL (${#DUMP_FILES[@]} DBs)" "$GRAND_TABLES" "$GRAND_EXPECTED" "$GRAND_RESTORED" "$(fmt_ms "$GRAND_RESTORE_MS")" "$FINAL_STATUS"
printf '\n'

if [[ "$OVERALL_FAILED" -ne 0 ]]; then
  die "Restore drill FAILED — see mismatches above"
fi

log "Restore drill PASSED: SHA256SUMS valid, ${#DUMP_FILES[@]} DBs restored, ${GRAND_TABLES} tables and ${GRAND_RESTORED} rows matched 100%."
