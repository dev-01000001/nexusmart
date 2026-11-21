#!/usr/bin/env bash
# One-command Helm rollback for a NexusMart microservice.
# Usage:
#   SERVICE=order REV=1 [NAMESPACE=nexusmart-staging] ./deploy/k8s/scripts/rollback.sh
#   ./deploy/k8s/scripts/rollback.sh <service> [revision] [namespace]
# When REV is omitted or 0, Helm rolls back to the immediately preceding revision.
set -euo pipefail

export PATH="${HOME}/.local/bin:${PATH}"

VALID_SERVICES=(
  gateway
  user
  product
  cart
  promotion
  order
  payment
  shipping
  notification
  search
  chat
  assistant
  aiops
  web
)

SERVICE="${1:-${SERVICE:-}}"
REV="${2:-${REV:-}}"
NAMESPACE="${3:-${NAMESPACE:-nexusmart-staging}}"
TIMEOUT="${ROLLBACK_TIMEOUT:-180s}"
WAIT_FOR_ROLLOUT="${ROLLBACK_WAIT:-true}"

usage() {
  cat >&2 <<EOF
Usage:
  make rollback SERVICE=<service> [REV=<revision>] [NAMESPACE=nexusmart-staging]
  $0 <service> [revision] [namespace]

Valid services:
  ${VALID_SERVICES[*]}
EOF
}

if [[ -z "${SERVICE}" ]]; then
  echo "ERROR: SERVICE is required." >&2
  usage
  exit 1
fi

valid=false
for s in "${VALID_SERVICES[@]}"; do
  if [[ "${s}" == "${SERVICE}" ]]; then
    valid=true
    break
  fi
done

if [[ "${valid}" != "true" ]]; then
  echo "ERROR: unknown service '${SERVICE}'." >&2
  usage
  exit 1
fi

if [[ -n "${REV}" ]] && ! [[ "${REV}" =~ ^[0-9]+$ ]]; then
  echo "ERROR: REV must be a non-negative integer (got '${REV}')." >&2
  exit 1
fi

for bin in helm kubectl; do
  if ! command -v "${bin}" >/dev/null 2>&1; then
    echo "ERROR: required tool '${bin}' not found in PATH" >&2
    exit 1
  fi
done

target_desc="previous revision"
if [[ -n "${REV}" && "${REV}" != "0" ]]; then
  target_desc="revision ${REV}"
fi

echo "==> Rolling back release '${SERVICE}' in namespace '${NAMESPACE}' to ${target_desc}"

helm_args=("${SERVICE}")
if [[ -n "${REV}" ]]; then
  helm_args+=("${REV}")
fi
helm_args+=(--namespace "${NAMESPACE}" --timeout "${TIMEOUT}")
if [[ "${WAIT_FOR_ROLLOUT}" == "true" ]]; then
  helm_args+=(--wait)
fi

helm rollback "${helm_args[@]}"

if [[ "${WAIT_FOR_ROLLOUT}" == "true" ]]; then
  echo "==> Verifying Deployment rollout status for deployment/${SERVICE}"
  kubectl rollout status "deployment/${SERVICE}" \
    --namespace "${NAMESPACE}" \
    --timeout="${TIMEOUT}"
fi

echo "==> Release history for '${SERVICE}' in '${NAMESPACE}':"
helm history "${SERVICE}" --namespace "${NAMESPACE}" --max 5
