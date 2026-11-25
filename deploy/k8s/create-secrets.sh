#!/usr/bin/env bash
# Creates runtime Kubernetes Secrets for NexusMart microservices in the target namespace.
# No production secrets are stored in this repository:
#   - For nexusmart-production: all required secret env vars MUST be exported by the caller/vault.
#   - For staging/local namespaces: unset secrets are generated randomly at runtime via openssl/urandom.
set -euo pipefail

export PATH="${HOME}/.local/bin:${PATH}"

NAMESPACE="${1:-nexusmart-staging}"

rand_hex() {
  local bytes="${1:-24}"
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex "${bytes}"
  else
    head -c "${bytes}" /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

rand_base64_32() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -base64 32
  else
    head -c 32 /dev/urandom | base64 | tr -d '\n'
  fi
}

is_placeholder_secret() {
  local raw="$1"
  local lower
  lower="$(printf '%s' "${raw}" | tr '[:upper:]' '[:lower:]' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
  if [[ -z "${lower}" ]]; then
    return 0
  fi
  case "${lower}" in
    dev-webhook-secret-change-me|\
    dev-only-payment-webhook-hmac-secret|\
    zalopay-mock-key1-dev|\
    zalopay-mock-key2-dev|\
    sandbox-client-id|\
    sandbox-api-key|\
    sandbox-checksum-key|\
    sandbox-sepay-token|\
    dev-only-*|your_*|your-*|'<'*|\
    *placeholder*|*change-me*|*changeme*)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

PROD_ERRORS=0

require_prod_var() {
  local var_name="$1"
  local context="${2:-}"
  local current_val="${!var_name:-}"
  local suffix=""
  if [[ -n "${context}" ]]; then
    suffix=" (${context})"
  fi
  if [[ -z "${current_val}" ]]; then
    echo "ERROR: ${var_name} must be explicitly provided via environment for ${NAMESPACE}${suffix}" >&2
    PROD_ERRORS=$((PROD_ERRORS + 1))
    return 0
  fi
  if is_placeholder_secret "${current_val}"; then
    echo "ERROR: ${var_name} must not use a placeholder or dev/mock value for ${NAMESPACE}${suffix}" >&2
    PROD_ERRORS=$((PROD_ERRORS + 1))
    return 0
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

require_or_generate_base64_32() {
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
  rand_base64_32
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
  # Matches deploy/helm/payment/values-production.yaml (VIETQR_MODE: "payos", ZALOPAY_MODE: "sandbox").
  VIETQR_MODE="${VIETQR_MODE:-payos}"
else
  POSTGRES_SSLMODE="${POSTGRES_SSLMODE:-disable}"
  VIETQR_MODE="${VIETQR_MODE:-mock}"
fi

VIETQR_MODE_NORM="$(printf '%s' "${VIETQR_MODE}" | tr '[:upper:]' '[:lower:]' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"

if [[ "${NAMESPACE}" == "nexusmart-production" ]]; then
  require_prod_var POSTGRES_PASSWORD
  require_prod_var JWT_SECRET
  require_prod_var SERVICE_JWT_SECRET
  require_prod_var ADMIN_BOOTSTRAP_PASSWORD
  require_prod_var BANK_ACCOUNT_KEY "required by user service startup (32-byte base64)"
  require_prod_var PAYMENT_WEBHOOK_SECRET "required by payment Config::validate"
  require_prod_var ZALOPAY_APP_ID "required by payment Config::validate"
  require_prod_var ZALOPAY_KEY1 "required by payment Config::validate"
  require_prod_var ZALOPAY_KEY2 "required by payment Config::validate"
  require_prod_var SHIPPING_WEBHOOK_SECRET
  require_prod_var SHIPPING_CONFIG_KEY "required by shipping carrier secret encryption (32-byte base64)"
  require_prod_var MEILI_MASTER_KEY

  case "${VIETQR_MODE_NORM}" in
    payos)
      require_prod_var PAYOS_CLIENT_ID "required when VIETQR_MODE=payos"
      require_prod_var PAYOS_API_KEY "required when VIETQR_MODE=payos"
      require_prod_var PAYOS_CHECKSUM_KEY "required when VIETQR_MODE=payos"
      ;;
    sepay)
      require_prod_var SEPAY_API_TOKEN "required when VIETQR_MODE=sepay"
      ;;
    *)
      echo "ERROR: VIETQR_MODE must be 'payos' or 'sepay' for ${NAMESPACE} (got '${VIETQR_MODE}')" >&2
      PROD_ERRORS=$((PROD_ERRORS + 1))
      ;;
  esac

  if [[ "${PROD_ERRORS}" -gt 0 ]]; then
    exit 1
  fi
fi

POSTGRES_PASSWORD="$(require_or_generate POSTGRES_PASSWORD)"
REDIS_PASSWORD="$(optional_env REDIS_PASSWORD)"
JWT_SECRET="$(require_or_generate JWT_SECRET)"
SERVICE_JWT_SECRET="$(require_or_generate SERVICE_JWT_SECRET)"
ADMIN_BOOTSTRAP_PASSWORD="$(require_or_generate ADMIN_BOOTSTRAP_PASSWORD)"
BANK_ACCOUNT_KEY="$(require_or_generate_base64_32 BANK_ACCOUNT_KEY)"
PAYMENT_WEBHOOK_SECRET="$(require_or_generate PAYMENT_WEBHOOK_SECRET)"
SHIPPING_WEBHOOK_SECRET="$(require_or_generate SHIPPING_WEBHOOK_SECRET)"
SHIPPING_CONFIG_KEY="$(require_or_generate_base64_32 SHIPPING_CONFIG_KEY)"
MEILI_MASTER_KEY="$(require_or_generate MEILI_MASTER_KEY)"

# Payment provider credentials (required in nexusmart-production per payment Config::validate; optional/empty in mock mode).
ZALOPAY_APP_ID="$(optional_env ZALOPAY_APP_ID)"
ZALOPAY_KEY1="$(optional_env ZALOPAY_KEY1)"
ZALOPAY_KEY2="$(optional_env ZALOPAY_KEY2)"
PAYOS_CLIENT_ID="$(optional_env PAYOS_CLIENT_ID)"
PAYOS_API_KEY="$(optional_env PAYOS_API_KEY)"
PAYOS_CHECKSUM_KEY="$(optional_env PAYOS_CHECKSUM_KEY)"
SEPAY_API_TOKEN="$(optional_env SEPAY_API_TOKEN)"

# Optional third-party sandbox/live credentials (empty in mock mode).
CF_API_TOKEN="$(optional_env CF_API_TOKEN)"
CF_ZONE_ID="$(optional_env CF_ZONE_ID)"
GHN_TOKEN="$(optional_env GHN_TOKEN)"
GHTK_TOKEN="$(optional_env GHTK_TOKEN)"
ANTHROPIC_API_KEY="$(optional_env ANTHROPIC_API_KEY)"
EMBEDDING_API_KEY="$(optional_env EMBEDDING_API_KEY)"
VOYAGE_API_KEY="$(optional_env VOYAGE_API_KEY)"
TELEGRAM_BOT_TOKEN="$(optional_env TELEGRAM_BOT_TOKEN)"
TELEGRAM_CHAT_ID="$(optional_env TELEGRAM_CHAT_ID)"

if [[ "${JWT_SECRET}" == "${SERVICE_JWT_SECRET}" ]]; then
  echo "ERROR: SERVICE_JWT_SECRET must differ from JWT_SECRET" >&2
  exit 1
fi

if ! command -v kubectl >/dev/null 2>&1; then
  echo "ERROR: kubectl not found in PATH" >&2
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
  --from-literal="BANK_ACCOUNT_KEY=${BANK_ACCOUNT_KEY}" \
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
  --from-literal="ZALOPAY_APP_ID=${ZALOPAY_APP_ID}" \
  --from-literal="ZALOPAY_KEY1=${ZALOPAY_KEY1}" \
  --from-literal="ZALOPAY_KEY2=${ZALOPAY_KEY2}" \
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
  --from-literal="SHIPPING_CONFIG_KEY=${SHIPPING_CONFIG_KEY}" \
  --from-literal="GHN_TOKEN=${GHN_TOKEN}" \
  --from-literal="GHTK_TOKEN=${GHTK_TOKEN}"

apply_secret nexusmart-notification-secret \
  --from-literal="DATABASE_URL=$(db_url notification_db)" \
  --from-literal="JWT_SECRET=${JWT_SECRET}" \
  --from-literal="SERVICE_JWT_SECRET=${SERVICE_JWT_SECRET}"

apply_secret nexusmart-search-secret \
  --from-literal="DATABASE_URL=$(db_url search_db)" \
  --from-literal="MEILI_MASTER_KEY=${MEILI_MASTER_KEY}" \
  --from-literal="EMBEDDING_API_KEY=${EMBEDDING_API_KEY}" \
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
