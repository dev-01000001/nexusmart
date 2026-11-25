# NexusMart PostgreSQL Backup & Restore Drill Runbook

Zero-dependency bash tooling for daily logical backups of every NexusMart microservice database and automated restore verification with exact per-table row-count matching.

## Files

| File | Purpose |
|---|---|
| `backup.sh` | Dynamically discovers and dumps all non-system databases using MVCC-synchronized snapshots (`pg_export_snapshot` + `pg_dump --snapshot`), records exact per-table row counts in `manifest.tsv`, writes `SHA256SUMS`, and prunes old snapshots. |
| `restore-drill.sh` | Verifies `SHA256SUMS`, boots an isolated temporary Postgres container (`f-test-restore-pg`, always cleaned up on exit), restores every database, and verifies 100% exact `SELECT count(*)` parity against `manifest.tsv`. |
| `daily-backup.cron` | Host cron schedule example (daily backup at 02:00 UTC, weekly restore drill Sunday at 03:00 UTC). |
| `k8s-cronjob.yaml` | Kubernetes `batch/v1` `CronJob` (`0 2 * * *` UTC), non-root UID 70, `readOnlyRootFilesystem`, credentials strictly via `secretKeyRef`. |

## Snapshot Layout

Snapshots are written atomically to `${BACKUP_DIR}/${UTC_TIMESTAMP}/` (default `BACKUP_DIR=/tmp/nexusmart-backups`, outside the git repo):

```text
/tmp/nexusmart-backups/
├── latest -> 20261121T020000Z
└── 20261121T020000Z/
    ├── globals.sql          # Cluster roles/tablespaces (pg_dumpall --globals-only)
    ├── manifest.tsv         # Exact per-table row counts (database\tschema.table\trow_count)
    ├── metadata.env         # Timestamp, Postgres version, format, totals
    ├── SHA256SUMS           # SHA-256 checksums for all dumps, globals.sql, manifest.tsv
    ├── notification_db.dump # Custom compressed archive (-Fc -Z6) per discovered DB
    ├── order_db.dump
    ├── payment_db.dump
    ├── product_db.dump
    ├── promotion_db.dump
    ├── shipping_db.dump
    └── user_db.dump
```

New databases added by services (e.g., `search_db`, `chat_db`, `assistant_db`) are discovered automatically on the next run without modifying the script.

## RPO / RTO Targets

| Objective | Target | Mechanism |
|---|---|---|
| **RPO** (Recovery Point Objective) | `<= 24 hours` | Daily scheduled backup at `02:00 UTC` (`daily-backup.cron` / `k8s-cronjob.yaml`). For tighter RPO (< 5 min), enable PostgreSQL continuous WAL archiving (PITR) on top of these daily base dumps. |
| **RTO** (Recovery Time Objective) | `<= 15 minutes` (staging/prod); `< 10 seconds` on current dev dataset | Compressed custom-format `.dump` archives support fast `pg_restore` (single-DB or parallel `-j`). Verified regularly via `restore-drill.sh`. |
| **Retention** | `7 days` (default) | Configurable via `RETENTION_DAYS` (`0` disables pruning). Only completed `YYYYMMDDTHHMMSSZ` snapshot directories are pruned. |

## Running Backups

### Manual backup (Docker Compose target)

```bash
# Default: discovers all DBs in nexus_ecom-postgres-1, writes to /tmp/nexusmart-backups
./tools/backup/backup.sh

# Custom destination, retention, or format
BACKUP_DIR=/var/backups/nexusmart RETENTION_DAYS=14 ./tools/backup/backup.sh
./tools/backup/backup.sh --format=sql           # Plain SQL + gzip (.sql.gz)
./tools/backup/backup.sh --verify               # Backup + immediate restore drill
PG_DATABASES="order_db payment_db" ./tools/backup/backup.sh  # Subset override
```

### Daily schedule — Host Cron

See `tools/backup/daily-backup.cron`. Create `/var/backups/nexusmart` and `/var/log/nexusmart`, update `REPO_DIR`, and install in crontab.

### Daily schedule — Kubernetes CronJob

1. Ensure the `nexusmart-postgres-credentials` Secret (`host`, `port`, `username`, `password`) and PVC `nexusmart-postgres-backups` exist in namespace `nexusmart-production` (never commit Secret values to git).
2. Apply the CronJob:
   ```bash
   kubectl apply -f tools/backup/k8s-cronjob.yaml
   ```
3. Trigger an on-demand test job:
   ```bash
   kubectl -n nexusmart-production create job --from=cronjob/nexusmart-postgres-backup manual-backup-$(date +%s)
   ```

## Running a Restore Drill

`restore-drill.sh` never touches or mutates the live `nexus_ecom-postgres-1` container. It spins up an isolated `f-test-restore-pg` container with no published host ports and removes it automatically via `trap` on exit.

```bash
# Drill the latest snapshot in BACKUP_DIR
./tools/backup/restore-drill.sh

# Take a fresh backup and immediately run a full restore drill
./tools/backup/restore-drill.sh --fresh

# Drill a specific snapshot directory
./tools/backup/restore-drill.sh /tmp/nexusmart-backups/20261121T020000Z

# If snapshots include pgvector databases (e.g. search_db), override PG_IMAGE:
PG_IMAGE=pgvector/pgvector:pg16 ./tools/backup/restore-drill.sh /tmp/nexusmart-backups/20261121T020000Z
```

Exit code is `0` **only** when `SHA256SUMS` passes, every database restores with zero errors, and 100% of tables and exact row counts (`SELECT count(*)`) match `manifest.tsv`.

## Restoring a Single Database

> **Warning:** Stop or scale down the owning microservice before replacing its production database to prevent concurrent writes during restore.

```bash
SNAPSHOT=/var/backups/nexusmart/latest
DB=order_db
PG_CONTAINER=nexus_ecom-postgres-1
PG_USER=nexusmart

# 1. Verify archive integrity first
(cd "$SNAPSHOT" && sha256sum -c --strict SHA256SUMS)

# 2. Restore into a staging database first (safe, non-destructive)
docker exec "$PG_CONTAINER" psql -U "$PG_USER" -d postgres -v ON_ERROR_STOP=1 \
  -c "CREATE DATABASE ${DB}_restore;"
docker exec -i "$PG_CONTAINER" pg_restore -U "$PG_USER" -d "${DB}_restore" \
  --no-owner --no-privileges --exit-on-error < "${SNAPSHOT}/${DB}.dump"

# 3. Verify exact row counts against manifest.tsv
awk -F'\t' -v db="$DB" 'NR > 1 && $1 == db { print $2 "\t" $3 }' "${SNAPSHOT}/manifest.tsv"

# 4. Once verified, stop the owning service, terminate connections, and swap DBs:
# docker exec "$PG_CONTAINER" psql -U "$PG_USER" -d postgres -c \
#   "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '${DB}' AND pid <> pg_backend_pid();"
# docker exec "$PG_CONTAINER" psql -U "$PG_USER" -d postgres -c "ALTER DATABASE ${DB} RENAME TO ${DB}_old;"
# docker exec "$PG_CONTAINER" psql -U "$PG_USER" -d postgres -c "ALTER DATABASE ${DB}_restore RENAME TO ${DB};"
```

### Note on `globals.sql`

`globals.sql` records cluster-level roles (`pg_dumpall --globals-only`) for full bare-metal / new-cluster disaster recovery. When restoring into an existing cluster where the bootstrap superuser (`nexusmart`) already exists, skip re-applying `globals.sql` (or apply only custom non-bootstrap roles) to avoid `role already exists` errors.
