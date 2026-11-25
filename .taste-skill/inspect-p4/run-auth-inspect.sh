#!/usr/bin/env bash
set -euo pipefail

INSPECT=/home/dev/.claude/skills/design-taste-frontend/scripts/inspect.mjs
PROXY=/home/dev/Documents/Nexus_Ecom/.taste-skill/inspect-p4/authproxy.mjs
BASE=/home/dev/Documents/Nexus_Ecom/.taste-skill/inspect-p4
OUT="${1:-$BASE/run1}"
mkdir -p "$OUT"

PROXY_PID=""
cleanup() {
  if [ -n "${PROXY_PID:-}" ] && kill -0 "$PROXY_PID" 2>/dev/null; then
    kill "$PROXY_PID" 2>/dev/null || true
    wait "$PROXY_PID" 2>/dev/null || true
  fi
  PROXY_PID=""
}
trap cleanup EXIT

start_proxy() {
  cleanup
  local jar="$1"
  LISTEN_PORT=3421 UPSTREAM_PORT=3300 JAR="$jar" node "$PROXY" >"$OUT/proxy.log" 2>&1 &
  PROXY_PID=$!
  for _ in $(seq 1 20); do
    if grep -q "authproxy :3421" "$OUT/proxy.log" 2>/dev/null; then
      return 0
    fi
    sleep 0.2
  done
  echo "ERROR: authproxy failed to start" >&2
  cat "$OUT/proxy.log" >&2
  exit 1
}

run_one() {
  local name="$1"
  local url="$2"
  shift 2
  echo "=== $name ($url) ==="
  node "$INSPECT" "$url" --out "$OUT/$name" --widths 1440,768,390 "$@" || echo "EXITED_NONZERO:$name"
}

# 1. Buyer authenticated pages (bach-dang & sai-gon)
start_proxy "$BASE/jar-buyer.json"
run_one "05-bd-cart" "http://bach-dang.nexusmart.localhost:3421/gio-hang"
run_one "06-bd-checkout" "http://bach-dang.nexusmart.localhost:3421/thanh-toan"
run_one "08-bd-orders" "http://bach-dang.nexusmart.localhost:3421/don-hang"
run_one "12-sg-cart" "http://sai-gon.nexusmart.localhost:3421/gio-hang"
run_one "13-sg-checkout" "http://sai-gon.nexusmart.localhost:3421/thanh-toan"
run_one "15-sg-orders" "http://sai-gon.nexusmart.localhost:3421/don-hang"

# 2. Seller dashboard (--app)
start_proxy "$BASE/jar-seller.json"
run_one "16-seller" "http://nexusmart.localhost:3421/seller" --app

# 3. Admin dashboard (--app)
start_proxy "$BASE/jar-admin.json"
run_one "17-admin" "http://nexusmart.localhost:3421/admin" --app

cleanup
echo "ALL_AUTH_DONE"
