# NexusMart Observability Stack (`deploy/observability/`)

Complete metrics, logs, traces, SLO-as-code, and alerting stack for NexusMart (spec: *Giám sát và log*, UC-OPS01 / UC-OPS02 / UC-OPS07). Enabled via Docker Compose profile `obs` so the default stack stays lightweight.

## Components & Ports (all bound to `127.0.0.1`)

| Component | Host Port | Config Path | Purpose |
|---|---:|---|---|
| Prometheus | `127.0.0.1:9090` | `prometheus/prometheus.yml`, `prometheus/rules/slo.yml` | 10s scrape of all 13 services, OTel Collector, and cAdvisor; SLO recording & burn-rate alert rules |
| Alertmanager | `127.0.0.1:9093` | `alertmanager/alertmanager.yml` | Deduplicates & groups alerts by `[alertname, service]`, routes to `telegram-relay` and `aiops` |
| Telegram Relay | `127.0.0.1:8099` | `telegram-relay/server.mjs` | Zero-dep Node 22 webhook relay; defaults to mock mode (`GET /alerts`), switches to live Telegram Bot API when `TELEGRAM_BOT_TOKEN` & `TELEGRAM_CHAT_ID` are set |
| Loki | `127.0.0.1:3100` | `loki/loki-config.yaml` | Loki 3.1.1 TSDB local storage, 30-day (`720h`) retention, structured metadata enabled |
| Promtail | internal | `promtail/promtail-config.yaml` | Docker SD log shipper parsing Go (`msg`) and Rust (`message`) JSON logs into `service`, `level`, and `trace_id` |
| Tempo | `127.0.0.1:3200` | `tempo/tempo.yaml` | Distributed tracing backend (OTLP gRPC `4317`, HTTP `4318`) with search enabled |
| cAdvisor | internal | Compose service | Container CPU & memory metrics relabeled via `com.docker.compose.service` -> `service` |
| Grafana | `127.0.0.1:3400` | `grafana/provisioning/`, `grafana/dashboards/` | Auto-provisioned Prometheus/Loki/Tempo (correlated via `trace_id`) + 4 dashboards |
| AIOps Engine | `127.0.0.1:8092` | `services/aiops/` | Anomaly detection, RCA, and safe remediation |

## Usage

After integrating `compose.obs.snippet.yml` into root `docker-compose.yml`:

```bash
# Start observability & AIOps alongside running services
docker compose --profile obs up -d

# Optional: also export OTel Collector traces to Grafana Tempo without modifying infra/otel-collector-config.yaml
docker compose -f docker-compose.yml -f deploy/observability/compose.otel-tempo.yml --profile obs up -d
```

## Dashboards

Auto-provisioned in Grafana under folder **NexusMart** (`http://127.0.0.1:3400`):

1. `service-red-overview.json` — Per-service HTTP & gRPC RED metrics, service availability, container CPU/RAM, 30-day 99.5% SLO error budget & burn rate, and correlated Loki error logs (`$service` filter).
2. `flow-checkout.json` — Checkout & Order Saga business flow (`gateway`, `cart`, `order`, `product`, `shipping`).
3. `flow-payment.json` — Payment business flow (`payment` HTTP & gRPC RED, `payment_webhooks_total`, `payment_ledger_transactions_total`, p95 latency, error logs).
4. `flow-flash-sale.json` — Flash Sale & Promotion flow (`gateway` flash-sale/voucher routes, `promotion` RED, `product` stock ops, p95 latency, error logs).

## Telegram Relay Tests

```bash
node --test deploy/observability/telegram-relay/server.test.mjs
```
