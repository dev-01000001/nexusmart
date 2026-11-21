# NexusMart Chaos Engineering Harness (`tools/chaos/`)

Controlled fault-injection suite and AIOps measurement harness implementing `docs/spec.md` (`UC-OPS08`) and `.agents/briefs/m-e-aiops.md`.

## Scenarios

| # | Scenario | Target | Fault Injected | Expected Root Cause | Idempotent Cleanup |
|---|---|---|---|---|---|
| 1 | `slow-payment` | `nexus_ecom-payment-1` (`:8086`) | `tc netem delay 1200ms` if available, else synchronized `docker pause`/`unpause` pulsing with `/readyz` & `/healthz` load | `payment` (`latency_degradation`) | Remove netem qdisc + `docker unpause` + verify `/readyz == 200` |
| 2 | `stop-order` | `nexus_ecom-order-1` (`:8085`) | `docker stop` while traffic hits gateway `/api/v1/orders` and order `/healthz` | `order` (`service_unavailable`) | `docker start` + wait healthy + verify `/healthz == 200` |
| 3 | `promotion-db-error` | `promotion_db` in `nexus_ecom-postgres-1` | `ALTER DATABASE promotion_db WITH ALLOW_CONNECTIONS false` + `pg_terminate_backend` (isolated ONLY to `promotion_db`) + traffic to `promotion /readyz`, gateway `/api/v1/flash-sales` & `/api/v1/vouchers` | `promotion` (`database_error`) | `ALTER DATABASE promotion_db WITH ALLOW_CONNECTIONS true` + verify `promotion /readyz == 200` |
| 4 | `nats-outage` | `nexus_ecom-nats-1` (`:8222`) | `docker stop` so AIOps NATS probe and services detect broker outage | `nats` (`message_broker_outage`) | `docker start` + wait healthy + verify NATS `/healthz` & `promotion /readyz` |
| 5 | `search-high-cpu` | `nexus_ecom-search-1` or `e-test-search` stand-in | Bounded 2-worker CPU burn via `docker exec` if `nexus_ecom-search-1` is running with `/bin/sh`; else labeled `alpine:3.20` container `e-test-search` (`com.docker.compose.service=search`, `--cpus=1.5`) | `search` (`cpu_saturation`) | Kill saved PIDs or `docker rm -f e-test-search` + verify removed/healthy |

## Stack Lock Coordination (`.runs/stack.lock`)

Before mutating the shared Docker Compose stack, `run_chaos.py` atomically acquires `.runs/stack.lock` (`O_CREAT | O_EXCL`). If another swarm (e.g., Swarm H E2E) holds the lock, it logs the holder and polls every 5s up to `--lock-timeout` (default `600s`). The lock is **only** released by the process that acquired it, including on `SIGINT` / `SIGTERM`.

## Usage

### Safe Dry-Run (touches zero Docker containers or live services)

```bash
python3 tools/chaos/run_chaos.py --dry-run \
  --lock-file /tmp/e-chaos-dryrun.lock \
  --output /tmp/e-chaos-dryrun.json
```

### Single Scenario (live stack)

```bash
python3 tools/chaos/run_chaos.py --scenario slow-payment
# or via thin shell wrapper:
./tools/chaos/run.sh --scenario promotion-db-error
```

### Full 5-Scenario Suite (live stack, UC-OPS08 gate)

```bash
python3 tools/chaos/run_chaos.py \
  --output .runs/chaos-results.json \
  --cooldown 25 \
  --detect-timeout 120
```

Useful flags:
- `--scenario <name>` (repeatable or comma-separated) — run specific scenario(s).
- `--trigger-scan` — trigger `POST /api/v1/scan` on every poll (default: off; measures natural background detection).
- `--lock-file <path>` / `--lock-timeout <secs>` — configure `.runs/stack.lock` path and wait timeout.
- `--output <path>` — JSON report path (also writes sibling `.md` table and prints Markdown to stdout).
- `--aiops-url`, `--gateway-url`, `--payment-url`, `--promotion-url`, `--order-url`, `--nats-monitor-url` — override auto-discovered container host ports.

## Pass Gate (`UC-OPS08`)

- Every scenario detected in `< 120s` (measured from `fault_started_at` to AIOps incident appearance).
- Correct root cause predicted in `>= 4/5` scenarios.
- All injected faults cleanly restored and verified healthy (`exit 0` on PASS, `exit 2` on gate FAIL, `exit 130` on interrupt after cleanup).

## Unit Tests

```bash
python3 -m unittest discover -s tools/chaos -v
```
