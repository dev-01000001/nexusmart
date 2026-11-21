#!/usr/bin/env bash
# Validates all 14 NexusMart Helm charts (staging + production), raw K8s manifests,
# shell scripts, and guarantees ZERO `kind: Secret` manifests are committed or rendered.
set -euo pipefail

export PATH="${HOME}/.local/bin:${PATH}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
HELM_DIR="${REPO_ROOT}/deploy/helm"
K8S_DIR="${REPO_ROOT}/deploy/k8s"
K8S_VERSION="${K8S_VERSION:-1.31.0}"

SERVICES=(
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

HPA_REQUIRED_SERVICES=(gateway order search)
ENVIRONMENTS=(staging production)

for bin in helm kubeconform; do
  if ! command -v "${bin}" >/dev/null 2>&1; then
    echo "ERROR: required tool '${bin}' not found in PATH" >&2
    exit 1
  fi
done

echo "==> [1/5] Shell script syntax checks"
bash -n "${K8S_DIR}/create-secrets.sh"
bash -n "${K8S_DIR}/scripts/lint.sh"
bash -n "${K8S_DIR}/scripts/rollback.sh"
echo "  OK: all deploy/k8s shell scripts pass bash -n"

echo "==> [2/5] Checking repository hygiene (no committed kind: Secret or binary .tgz charts)"
if find "${REPO_ROOT}/deploy" -type f \( -name '*.yaml' -o -name '*.yml' -o -name '*.tpl' \) \
    -exec grep -En '^[[:space:]]*kind:[[:space:]]*Secret([[:space:]]|$)' {} + 2>/dev/null; then
  echo "ERROR: committed 'kind: Secret' manifest found under deploy/!" >&2
  exit 1
fi
if find "${HELM_DIR}" -type f -name '*.tgz' | grep -q .; then
  echo "ERROR: binary .tgz chart archive found under deploy/helm/!" >&2
  exit 1
fi
echo "  OK: 0 committed kind: Secret manifests, 0 .tgz archives"

echo "==> [3/5] Validating deploy/k8s/namespaces.yaml with kubeconform (K8s ${K8S_VERSION})"
kubeconform -strict -summary -kubernetes-version "${K8S_VERSION}" "${K8S_DIR}/namespaces.yaml"

echo "==> [4/5] Running helm lint --strict and helm template | kubeconform across ${#SERVICES[@]} services x ${#ENVIRONMENTS[@]} envs"
total_checks=0
for svc in "${SERVICES[@]}"; do
  chart_dir="${HELM_DIR}/${svc}"
  if [[ ! -f "${chart_dir}/Chart.yaml" ]]; then
    echo "ERROR: missing chart ${chart_dir}/Chart.yaml" >&2
    exit 1
  fi

  for env in "${ENVIRONMENTS[@]}"; do
    values_file="${chart_dir}/values-${env}.yaml"
    if [[ ! -f "${values_file}" ]]; then
      echo "ERROR: missing values file ${values_file}" >&2
      exit 1
    fi

    echo "--- ${svc} (${env}) ---"
    helm lint --strict "${chart_dir}" -f "${values_file}"

    rendered="$(helm template "${svc}" "${chart_dir}" \
      --namespace "nexusmart-${env}" \
      -f "${values_file}")"

    if printf '%s\n' "${rendered}" | grep -Eq '^[[:space:]]*kind:[[:space:]]*Secret([[:space:]]|$)'; then
      echo "ERROR: chart ${svc} (${env}) rendered a 'kind: Secret' resource!" >&2
      exit 1
    fi

    printf '%s\n' "${rendered}" \
      | kubeconform -strict -summary -kubernetes-version "${K8S_VERSION}"

    for hpa_svc in "${HPA_REQUIRED_SERVICES[@]}"; do
      if [[ "${svc}" == "${hpa_svc}" ]]; then
        if ! printf '%s\n' "${rendered}" | grep -Eq '^[[:space:]]*kind:[[:space:]]*HorizontalPodAutoscaler$'; then
          echo "ERROR: expected HorizontalPodAutoscaler for ${svc} (${env})" >&2
          exit 1
        fi
      fi
    done

    if [[ "${env}" == "production" ]]; then
      if ! printf '%s\n' "${rendered}" | grep -Eq '^[[:space:]]*kind:[[:space:]]*PodDisruptionBudget$'; then
        echo "ERROR: expected PodDisruptionBudget for ${svc} (production)" >&2
        exit 1
      fi
    fi

    total_checks=$((total_checks + 1))
  done
done

echo "==> [5/5] Summary"
echo "  PASS: ${total_checks} chart/environment combinations linted & kubeconform-validated cleanly."
