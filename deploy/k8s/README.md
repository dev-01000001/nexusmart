# NexusMart Kubernetes & Helm Deployment

Per-service Helm charts (`deploy/helm/<service>/`) and cluster operations scripts (`deploy/k8s/`) for deploying all 14 NexusMart services across `nexusmart-staging` and `nexusmart-production`.

## Architecture & Guarantees

- **14 independent Helm v2 charts** (`deploy/helm/<service>/`):
  - Core & gateway: `gateway` (8080), `user` (8081/50051), `product` (8082/50052), `cart` (8083/50053), `promotion` (8084/50054), `order` (8085/50055), `payment` (8086/50056), `shipping` (8087/50057), `notification` (8088/50058).
  - Phase 4–5 services: `search` (8089/50059), `chat` (8090/50060), `assistant` (8091/50061), `aiops` (8092).
  - Storefront BFF: `web` (3000, Next.js standalone, UID/GID `1000`).
- **Config separated from images**: non-secret configuration lives in per-service `ConfigMap`s (`envFrom.configMapRef`) with `checksum/config` pod annotations to trigger rolling restarts on config change.
- **Zero secrets in Git**: charts reference runtime Kubernetes Secrets via `secrets.existingSecret` (`envFrom.secretRef`, `optional: false`). No `kind: Secret` YAML is committed or rendered by any chart.
- **Zero-downtime rolling updates**: `RollingUpdate` with `maxUnavailable: 0`, `maxSurge: 1`, HTTP readiness (`/readyz` for backends, `/` for `web`) and liveness (`/healthz` for backends, `/` for `web`) probes.
- **Autoscaling & HA**: `HorizontalPodAutoscaler` (`autoscaling/v2`) enabled by default for load-bearing services (`gateway`, `order`, `search`) and configurable for every service. `PodDisruptionBudget` (`policy/v1`) enabled in `values-production.yaml` for all 14 services.
- **Hardened pods**: `runAsNonRoot: true`, UID/GID `10001` (`1000` for `web`), `allowPrivilegeEscalation: false`, `capabilities.drop: ["ALL"]`, `seccompProfile.type: RuntimeDefault`, `automountServiceAccountToken: false`.
- **Observability**: Prometheus scrape annotations (`prometheus.io/scrape`, `prometheus.io/port`, `prometheus.io/path: /metrics`) on all 13 backend services.

## Prerequisites

User-local binaries in `~/.local/bin`:

```bash
export PATH="$HOME/.local/bin:$PATH"
helm version --short
kubectl version --client
kind version
kubeconform -v
```

## Validate All Charts Offline

```bash
./deploy/k8s/scripts/lint.sh
```

Runs `bash -n`, secret/binary hygiene checks, `helm lint --strict`, and `helm template | kubeconform -strict -kubernetes-version 1.31.0` across all 14 services for both `staging` and `production`.

## Deploy to Staging / Production

### 1. Create namespaces

```bash
kubectl apply -f deploy/k8s/namespaces.yaml
```

### 2. Inject runtime secrets (never committed)

For **staging** (generates random credentials for any unset env vars):

```bash
./deploy/k8s/create-secrets.sh nexusmart-staging
```

For **production** (fails closed unless all required secrets are exported from your vault/CI):

```bash
export POSTGRES_HOST="pg.internal"
export POSTGRES_PASSWORD="..."
export JWT_SECRET="..."
export SERVICE_JWT_SECRET="..."
export ADMIN_BOOTSTRAP_PASSWORD="..."
export PAYMENT_WEBHOOK_SECRET="..."
export ZALOPAY_KEY1="..."
export ZALOPAY_KEY2="..."
export VIETQR_MODE="payos" # or "sepay" (requires SEPAY_API_TOKEN instead of PAYOS_*)
export PAYOS_CLIENT_ID="..."
export PAYOS_API_KEY="..."
export PAYOS_CHECKSUM_KEY="..."
export SHIPPING_WEBHOOK_SECRET="..."
export MEILI_MASTER_KEY="..."
./deploy/k8s/create-secrets.sh nexusmart-production
```

### 3. Install or upgrade services

Deploy a single service independently:

```bash
helm upgrade --install order deploy/helm/order \
  --namespace nexusmart-staging \
  -f deploy/helm/order/values-staging.yaml \
  --set image.tag="v0.1.0" \
  --wait
```

Or deploy all 14 services to staging:

```bash
for svc in gateway user product cart promotion order payment shipping notification search chat assistant aiops web; do
  helm upgrade --install "$svc" "deploy/helm/$svc" \
    --namespace nexusmart-staging \
    -f "deploy/helm/$svc/values-staging.yaml"
done
```

For production, pass `-f deploy/helm/<svc>/values-production.yaml` and `--namespace nexusmart-production`.

## One-Command Rollback

Roll back a service to a specific revision (or omit `REV` to roll back to the previous revision):

```bash
make rollback SERVICE=order REV=1
# or directly:
./deploy/k8s/scripts/rollback.sh order 1 nexusmart-staging
# previous revision in production:
NAMESPACE=nexusmart-production ./deploy/k8s/scripts/rollback.sh gateway
```

## Local Kind Cluster Verification

```bash
kind create cluster --name nexusmart-local --config deploy/k8s/kind-config.yaml
kubectl apply -f deploy/k8s/namespaces.yaml
./deploy/k8s/create-secrets.sh nexusmart-staging
# Load local image and install:
kind load docker-image nexus_ecom-gateway:latest --name nexusmart-local
helm upgrade --install gateway deploy/helm/gateway \
  -n nexusmart-staging \
  -f deploy/helm/gateway/values-staging.yaml \
  --set image.repository=nexus_ecom-gateway \
  --set image.tag=latest \
  --set image.pullPolicy=Never
# Clean up when finished:
kind delete cluster --name nexusmart-local
```
