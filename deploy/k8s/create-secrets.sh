#!/usr/bin/env bash
# Creates runtime Kubernetes Secrets for NexusMart microservices in the target namespace.
# No production secrets are stored in this repository:
#   - For nexusmart-production: all required secret env vars MUST be exported by the caller/vault.
#   - For staging/local namespaces: unset secrets are generated randomly at runtime via openssl/urandom.
set -euo pipefail

export PATH="${HOME}/.local/bin:${PATH}"

NAMESPACE="${1:-nexusmart-staging}"

if ! command -v kubectl >/dev/null 2>&1; then
  echo "ERROR: kubectl not found in PATH" >&2
  exit 1
fi

rand_hex() {
  local bytes="${1:-24}"
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex "${bytes}"
  else
    head -c "${bytes}" /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

require_or_generate() {
  local var_name="$1"
  local current_val="${!var_name:-}"
  if [[ -n "${current_val}" ]]; then
    printf '%s' "${current_val}"
    return 0
  fi
  if [[ "${NAMESPACE}" == "nexusmart-production" ]]; then
    echo "ERROR: ${var_name} must be explicitly provided via environment for ${NAMESPACE}" >&2
    exit 1
  fi
  rand_hex 24
}

optional_env() {
  local var_name="$1"
  printf '%s' "${!var_name:-}"
}

POSTGRES_HOST="${POSTGRES_HOST:-postgres}"
POSTGRES_PORT="${POSTGRES_PORT:-5432}"
POSTGRES_USER="${POSTGRES_USER:-nexusmart}"
if [[ "${NAMESPACE}" == "nexusmart-production" ]]; then
  POSTGRES_SSLMODE="${POSTGRES_SSLMODE:-require}"
else
  POSTGRES_SSLMODE="${POSTGRES_SSLMODE:-disable}"
fi

POSTGRES_PASSWORD="$(require_or_generate POSTGRES_PASSWORD)"
REDIS_PASSWORD="$(optional_env REDIS_PASSWORD)"
JWT_SECRET="$(require_or_generate JWT_SECRET)"
SERVICE_JWT_SECRET="$(require_or_generate SERVICE_JWT_SECRET)"
ADMIN_BOOTSTRAP_PASSWORD="$(require_or_generate ADMIN_BOOTSTRAP_PASSWORD)"
PAYMENT_WEBHOOK_SECRET="$(require_or_generate PAYMENT_WEBHOOK_SECRET)"
SHIPPING_WEBHOOK_SECRET="$(require_or_generate SHIPPING_WEBHOOK_SECRET)"
MEILI_MASTER_KEY="$(require_or_generate MEILI_MASTER_KEY)"

# Optional third-party sandbox/live credentials (empty in mock mode).
CF_API_TOKEN="$(optional_env CF_API_TOKEN)"
CF_ZONE_ID="$(optional_env CF_ZONE_ID)"
PAYOS_CLIENT_ID="$(optional_env PAYOS_CLIENT_ID)"
PAYOS_API_KEY="$(optional_env PAYOS_API_KEY)"
PAYOS_CHECKSUM_KEY="$(optional_env PAYOS_CHECKSUM_KEY)"
SEPAY_API_TOKEN="$(optional_env SEPAY_API_TOKEN)"
GHN_TOKEN="$(optional_env GHN_TOKEN)"
ANTHROPIC_API_KEY="$(optional_env ANTHROPIC_API_KEY)"
VOYAGE_API_KEY="$(optional_env VOYAGE_API_KEY)"
TELEGRAM_BOT_TOKEN="$(optional_env TELEGRAM_BOT_TOKEN)"
TELEGRAM_CHAT_ID="$(optional_env TELEGRAM_CHAT_ID)"

if [[ "${JWT_SECRET}" == "${SERVICE_JWT_SECRET}" ]]; then
  echo "ERROR: SERVICE_JWT_SECRET must differ from JWT_SECRET" >&2
  exit 1
fi

db_url() {
  local db_name="$1"
  printf 'postgres://%s:%s@%s:%s/%s?sslmode=%s' \
    "${POSTGRES_USER}" "${POSTGRES_PASSWORD}" "${POSTGRES_HOST}" "${POSTGRES_PORT}" "${db_name}" "${POSTGRES_SSLMODE}"
}

apply_secret() {
  local name="$1"
  shift
  kubectl create secret generic "${name}" \
    --namespace "${NAMESPACE}" \
    "$@" \
    --dry-run=client -o yaml \
    | kubectl label --local -f - \
        app.kubernetes.io/part-of=nexusmart \
        app.kubernetes.io/managed-by=create-secrets \
        -o yaml \
    | kubectl apply -f - >/dev/null
  echo "  applied secret/${name} in namespace ${NAMESPACE}"
}

echo "==> Ensuring namespace ${NAMESPACE} exists"
kubectl get namespace "${NAMESPACE}" >/dev/null 2>&1 || kubectl create namespace "${NAMESPACE}" >/dev/null

echo "==> Creating per-service least-privilege Kubernetes Secrets in ${NAMESPACE}"

apply_secret nexusmart-gateway-secret \
  --from-literal="JWT_SECRET=${JWT_SECRET}" \
  --from-literal="REDIS_PASSWORD=${REDIS_PASSWORD}"

apply_secret nexusmart-user-secret \
  --from-literal="DATABASE_URL=$(db_url user_db)" \
  --from-literal="REDIS_PASSWORD=${REDIS_PASSWORD}" \
  --from-literal="JWT_SECRET=${JWT_SECRET}" \
  --from-literal="SERVICE_JWT_SECRET=${SERVICE_JWT_SECRET}" \
  --from-literal="ADMIN_BOOTSTRAP_PASSWORD=${ADMIN_BOOTSTRAP_PASSWORD}" \
  --from-literal="CF_API_TOKEN=${CF_API_TOKEN}" \
  --from-literal="CF_ZONE_ID=${CF_ZONE_ID}"

apply_secret nexusmart-product-secret \
  --from-literal="DATABASE_URL=$(db_url product_db)" \
  --from-literal="JWT_SECRET=${JWT_SECRET}" \
  --from-literal="SERVICE_JWT_SECRET=${SERVICE_JWT_SECRET}"

apply_secret nexusmart-cart-secret \
  --from-literal="REDIS_PASSWORD=${REDIS_PASSWORD}" \
  --from-literal="JWT_SECRET=${JWT_SECRET}" \
  --from-literal="SERVICE_JWT_SECRET=${SERVICE_JWT_SECRET}"

apply_secret nexusmart-promotion-secret \
  --from-literal="DATABASE_URL=$(db_url promotion_db)" \
  --from-literal="REDIS_PASSWORD=${REDIS_PASSWORD}" \
  --from-literal="JWT_SECRET=${JWT_SECRET}" \
  --from-literal="SERVICE_JWT_SECRET=${SERVICE_JWT_SECRET}"

apply_secret nexusmart-order-secret \
  --from-literal="DATABASE_URL=$(db_url order_db)" \
  --from-literal="JWT_SECRET=${JWT_SECRET}" \
  --from-literal="SERVICE_JWT_SECRET=${SERVICE_JWT_SECRET}"

apply_secret nexusmart-payment-secret \
  --from-literal="DATABASE_URL=$(db_url payment_db)" \
  --from-literal="JWT_SECRET=${JWT_SECRET}" \
  --from-literal="SERVICE_JWT_SECRET=${SERVICE_JWT_SECRET}" \
  --from-literal="PAYMENT_WEBHOOK_SECRET=${PAYMENT_WEBHOOK_SECRET}" \
  --from-literal="PAYOS_CLIENT_ID=${PAYOS_CLIENT_ID}" \
  --from-literal="PAYOS_API_KEY=${PAYOS_API_KEY}" \
  --from-literal="PAYOS_CHECKSUM_KEY=${PAYOS_CHECKSUM_KEY}" \
  --from-literal="SEPAY_API_TOKEN=${SEPAY_API_TOKEN}"

apply_secret nexusmart-shipping-secret \
  --from-literal="DATABASE_URL=$(db_url shipping_db)" \
  --from-literal="REDIS_PASSWORD=${REDIS_PASSWORD}" \
  --from-literal="JWT_SECRET=${JWT_SECRET}" \
  --from-literal="SERVICE_JWT_SECRET=${SERVICE_JWT_SECRET}" \
  --from-literal="SHIPPING_WEBHOOK_SECRET=${SHIPPING_WEBHOOK_SECRET}" \
  --from-literal="GHN_TOKEN=${GHN_TOKEN}"

apply_secret nexusmart-notification-secret \
  --from-literal="DATABASE_URL=$(db_url notification_db)" \
  --from-literal="JWT_SECRET=${JWT_SECRET}" \
  --from-literal="SERVICE_JWT_SECRET=${SERVICE_JWT_SECRET}"

apply_secret nexusmart-search-secret \
  --from-literal="DATABASE_URL=$(db_url search_db)" \
  --from-literal="MEILI_MASTER_KEY=${MEILI_MASTER_KEY}" \
  --from-literal="VOYAGE_API_KEY=${VOYAGE_API_KEY}" \
  --from-literal="JWT_SECRET=${JWT_SECRET}" \
  --from-literal="SERVICE_JWT_SECRET=${SERVICE_JWT_SECRET}"

apply_secret nexusmart-chat-secret \
  --from-literal="DATABASE_URL=$(db_url chat_db)" \
  --from-literal="REDIS_PASSWORD=${REDIS_PASSWORD}" \
  --from-literal="JWT_SECRET=${JWT_SECRET}" \
  --from-literal="SERVICE_JWT_SECRET=${SERVICE_JWT_SECRET}"

apply_secret nexusmart-assistant-secret \
  --from-literal="DATABASE_URL=$(db_url assistant_db)" \
  --from-literal="REDIS_PASSWORD=${REDIS_PASSWORD}" \
  --from-literal="JWT_SECRET=${JWT_SECRET}" \
  --from-literal="SERVICE_JWT_SECRET=${SERVICE_JWT_SECRET}" \
  --from-literal="ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}" \
  --from-literal="VOYAGE_API_KEY=${VOYAGE_API_KEY}"

apply_secret nexusmart-aiops-secret \
  --from-literal="ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}" \
  --from-literal="TELEGRAM_BOT_TOKEN=${TELEGRAM_BOT_TOKEN}" \
  --from-literal="TELEGRAM_CHAT_ID=${TELEGRAM_CHAT_ID}"

echo "==> Done: 13 per-service secrets ready in ${NAMESPACE}"
