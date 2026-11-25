#!/usr/bin/env bash
set -euo pipefail

export PATH="$HOME/.local/bin:$HOME/go/bin:$HOME/.cargo/bin:$PATH"
export PROTOC="${PROTOC:-$HOME/.local/bin/protoc}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PG_IMAGE="${TEST_DB_IMAGE:-postgres:16-alpine}"
SEARCH_PG_IMAGE="${TEST_SEARCH_DB_IMAGE:-pgvector/pgvector:0.8.7-pg16}"
NATS_IMAGE="${TEST_NATS_IMAGE:-nats:2.10-alpine}"
REDIS_IMAGE="${TEST_REDIS_IMAGE:-redis:7-alpine}"
PG_USER="nexusmart"
PG_PASS="testdb_ephemeral_pw"
CONTAINER_NAME=""
SEARCH_PG_CONTAINER_NAME=""
NATS_CONTAINER_NAME=""
REDIS_CONTAINER_NAME=""
TMP_DIR="$(mktemp -d)"
CLEANED_UP=0

stop_search_pg() {
	if [[ -n "$SEARCH_PG_CONTAINER_NAME" ]]; then
		docker rm -f "$SEARCH_PG_CONTAINER_NAME" >/dev/null 2>&1 || true
		SEARCH_PG_CONTAINER_NAME=""
	fi
}

start_search_pg() {
	stop_search_pg
	SEARCH_PG_CONTAINER_NAME="nexusmart-testsearchdb-$$"

	echo "==> Starting ephemeral search pgvector Postgres ($SEARCH_PG_IMAGE, container=$SEARCH_PG_CONTAINER_NAME, tmpfs, random localhost port)"
	docker run -d \
		--name "$SEARCH_PG_CONTAINER_NAME" \
		-e "POSTGRES_USER=$PG_USER" \
		-e "POSTGRES_PASSWORD=$PG_PASS" \
		-e "POSTGRES_DB=search_db" \
		-p "127.0.0.1::5432" \
		--tmpfs /var/lib/postgresql/data:rw \
		"$SEARCH_PG_IMAGE" \
		postgres -c max_connections=100 >/dev/null

	local search_pg_port
	search_pg_port="$(docker port "$SEARCH_PG_CONTAINER_NAME" 5432/tcp | head -n 1 | awk -F: '{print $NF}')"
	if [[ -z "$search_pg_port" ]]; then
		echo "ERROR: failed to determine mapped host port for $SEARCH_PG_CONTAINER_NAME" >&2
		exit 1
	fi

	echo "==> Waiting for search pgvector TCP readiness on 127.0.0.1:${search_pg_port}"
	local search_ready=0
	for _ in $(seq 1 60); do
		if [[ "$(docker inspect -f '{{.State.Running}}' "$SEARCH_PG_CONTAINER_NAME" 2>/dev/null || true)" != "true" ]]; then
			echo "ERROR: ephemeral search pgvector container exited unexpectedly:" >&2
			docker logs "$SEARCH_PG_CONTAINER_NAME" >&2 || true
			exit 1
		fi
		if docker exec "$SEARCH_PG_CONTAINER_NAME" pg_isready -h 127.0.0.1 -p 5432 -U "$PG_USER" -d search_db >/dev/null 2>&1 \
			&& (exec 3<>/dev/tcp/127.0.0.1/"$search_pg_port") 2>/dev/null \
			&& docker exec "$SEARCH_PG_CONTAINER_NAME" psql -h 127.0.0.1 -U "$PG_USER" -d search_db -c 'SELECT 1' >/dev/null 2>&1; then
			search_ready=1
			break
		fi
		sleep 0.5
	done

	if [[ "$search_ready" -ne 1 ]]; then
		echo "ERROR: ephemeral search pgvector on 127.0.0.1:${search_pg_port} did not become ready in time" >&2
		docker logs "$SEARCH_PG_CONTAINER_NAME" >&2 || true
		exit 1
	fi

	export TEST_SEARCH_DB_URL="postgres://${PG_USER}:${PG_PASS}@127.0.0.1:${search_pg_port}/search_db?sslmode=disable"
}

stop_nats() {
	if [[ -n "$NATS_CONTAINER_NAME" ]]; then
		docker rm -f "$NATS_CONTAINER_NAME" >/dev/null 2>&1 || true
		NATS_CONTAINER_NAME=""
	fi
}

start_nats() {
	local tag="${1:-}"
	stop_nats

	NATS_CONTAINER_NAME="nexusmart-testnats-$$${tag:+-$tag}"

	echo "==> Starting ephemeral NATS JetStream ($NATS_IMAGE, container=$NATS_CONTAINER_NAME, random localhost port)"
	docker run -d \
		--name "$NATS_CONTAINER_NAME" \
		-p "127.0.0.1::4222" \
		-p "127.0.0.1::8222" \
		"$NATS_IMAGE" \
		-js -m 8222 -sd /tmp/js >/dev/null

	local nats_port nats_http_port
	nats_port="$(docker port "$NATS_CONTAINER_NAME" 4222/tcp | head -n 1 | awk -F: '{print $NF}')"
	nats_http_port="$(docker port "$NATS_CONTAINER_NAME" 8222/tcp | head -n 1 | awk -F: '{print $NF}')"
	if [[ -z "$nats_port" || -z "$nats_http_port" ]]; then
		echo "ERROR: failed to determine mapped host ports for $NATS_CONTAINER_NAME" >&2
		exit 1
	fi

	echo "==> Waiting for NATS JetStream readiness on 127.0.0.1:${nats_port}"
	local nats_ready=0
	for _ in $(seq 1 60); do
		if [[ "$(docker inspect -f '{{.State.Running}}' "$NATS_CONTAINER_NAME" 2>/dev/null || true)" != "true" ]]; then
			echo "ERROR: ephemeral NATS container exited unexpectedly:" >&2
			docker logs "$NATS_CONTAINER_NAME" >&2 || true
			exit 1
		fi
		if (exec 3<>/dev/tcp/127.0.0.1/"$nats_port") 2>/dev/null \
			&& curl -fsS --max-time 2 "http://127.0.0.1:${nats_http_port}/jsz" 2>/dev/null | grep -q '"streams"'; then
			nats_ready=1
			break
		fi
		sleep 0.25
	done

	if [[ "$nats_ready" -ne 1 ]]; then
		echo "ERROR: ephemeral NATS JetStream on 127.0.0.1:${nats_port} did not become ready in time" >&2
		docker logs "$NATS_CONTAINER_NAME" >&2 || true
		exit 1
	fi

	export TEST_NATS_URL="nats://127.0.0.1:${nats_port}"
}

cleanup() {
	if [[ "$CLEANED_UP" -ne 0 ]]; then
		return
	fi
	CLEANED_UP=1
	if [[ -n "$CONTAINER_NAME" ]]; then
		docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
	fi
	stop_search_pg
	stop_nats
	if [[ -n "$REDIS_CONTAINER_NAME" ]]; then
		docker rm -f "$REDIS_CONTAINER_NAME" >/dev/null 2>&1 || true
	fi
	rm -rf "$TMP_DIR"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

run_rust_payment_tests() {
	echo "==> [test-db] cargo test services/payment"
	local payment_log="$TMP_DIR/payment.log"
	# --nocapture ensures eprintln!("skipping ...: TEST_PAYMENT_DB_URL not set") is visible even on ok tests.
	if ! (cd "$ROOT_DIR/services/payment" && cargo test -- --nocapture) >"$payment_log" 2>&1; then
		cat "$payment_log"
		echo "ERROR: cargo test failed in services/payment" >&2
		exit 1
	fi
	if grep -Eq 'test result:.*; [1-9][0-9]* ignored;' "$payment_log" \
		|| grep -Eiq 'skipping.*TEST_PAYMENT_DB_URL|TEST_PAYMENT_DB_URL not set' "$payment_log"; then
		cat "$payment_log"
		echo "ERROR: Postgres test(s) were skipped/ignored in services/payment" >&2
		exit 1
	fi
	local payment_passed
	payment_passed="$(awk '/test result: ok\./ { sum += $4 } END { print sum + 0 }' "$payment_log")"
	if [[ "$payment_passed" -eq 0 ]]; then
		cat "$payment_log"
		echo "ERROR: zero Rust tests passed in services/payment" >&2
		exit 1
	fi
	echo "    PASS services/payment (${payment_passed} Rust tests, 0 ignored)"
}

# Counter-proof entrypoint for the Rust SKIP gate (runs payment tests with caller's env; fails when TEST_PAYMENT_DB_URL is unset).
if [[ "${1:-}" == "--run-payment-only" ]]; then
	run_rust_payment_tests
	exit 0
fi

if [[ "${1:-}" == "--run" && "$#" -lt 2 ]]; then
	echo "ERROR: --run requires a command to execute" >&2
	exit 2
fi

CONTAINER_NAME="nexusmart-testdb-$$"

echo "==> Starting ephemeral Postgres ($PG_IMAGE, container=$CONTAINER_NAME, tmpfs, random localhost port)"
docker run -d \
	--name "$CONTAINER_NAME" \
	-e "POSTGRES_USER=$PG_USER" \
	-e "POSTGRES_PASSWORD=$PG_PASS" \
	-e "POSTGRES_DB=postgres" \
	-p "127.0.0.1::5432" \
	--tmpfs /var/lib/postgresql/data:rw \
	"$PG_IMAGE" \
	postgres -c max_connections=200 >/dev/null

PG_PORT="$(docker port "$CONTAINER_NAME" 5432/tcp | head -n 1 | awk -F: '{print $NF}')"
if [[ -z "$PG_PORT" ]]; then
	echo "ERROR: failed to determine mapped host port for $CONTAINER_NAME" >&2
	exit 1
fi

echo "==> Waiting for TCP readiness on 127.0.0.1:${PG_PORT}"
ready=0
for _ in $(seq 1 60); do
	if [[ "$(docker inspect -f '{{.State.Running}}' "$CONTAINER_NAME" 2>/dev/null || true)" != "true" ]]; then
		echo "ERROR: ephemeral Postgres container exited unexpectedly:" >&2
		docker logs "$CONTAINER_NAME" >&2 || true
		exit 1
	fi
	if docker exec "$CONTAINER_NAME" pg_isready -h 127.0.0.1 -p 5432 -U "$PG_USER" -d postgres >/dev/null 2>&1 \
		&& (exec 3<>/dev/tcp/127.0.0.1/"$PG_PORT") 2>/dev/null \
		&& docker exec "$CONTAINER_NAME" psql -h 127.0.0.1 -U "$PG_USER" -d postgres -c 'SELECT 1' >/dev/null 2>&1; then
		ready=1
		break
	fi
	sleep 0.5
done

if [[ "$ready" -ne 1 ]]; then
	echo "ERROR: ephemeral Postgres on 127.0.0.1:${PG_PORT} did not become ready in time" >&2
	docker logs "$CONTAINER_NAME" >&2 || true
	exit 1
fi

for db in order_db product_db promotion_db shipping_db user_db payment_db chat_db assistant_db; do
	docker exec "$CONTAINER_NAME" psql -h 127.0.0.1 -U "$PG_USER" -d postgres -v ON_ERROR_STOP=1 -c "CREATE DATABASE ${db};" >/dev/null
done

export TEST_ORDER_DB_URL="postgres://${PG_USER}:${PG_PASS}@127.0.0.1:${PG_PORT}/order_db?sslmode=disable"
export TEST_PRODUCT_DB_URL="postgres://${PG_USER}:${PG_PASS}@127.0.0.1:${PG_PORT}/product_db?sslmode=disable"
export TEST_PROMOTION_DB_URL="postgres://${PG_USER}:${PG_PASS}@127.0.0.1:${PG_PORT}/promotion_db?sslmode=disable"
export TEST_SHIPPING_DB_URL="postgres://${PG_USER}:${PG_PASS}@127.0.0.1:${PG_PORT}/shipping_db?sslmode=disable"
export TEST_USER_DB_URL="postgres://${PG_USER}:${PG_PASS}@127.0.0.1:${PG_PORT}/user_db?sslmode=disable"
export TEST_PAYMENT_DB_URL="postgres://${PG_USER}:${PG_PASS}@127.0.0.1:${PG_PORT}/payment_db?sslmode=disable"
export TEST_CHAT_DB_URL="postgres://${PG_USER}:${PG_PASS}@127.0.0.1:${PG_PORT}/chat_db?sslmode=disable"
export TEST_ASSISTANT_DB_URL="postgres://${PG_USER}:${PG_PASS}@127.0.0.1:${PG_PORT}/assistant_db?sslmode=disable"

REDIS_CONTAINER_NAME="nexusmart-testredis-$$"

echo "==> Starting ephemeral Redis ($REDIS_IMAGE, container=$REDIS_CONTAINER_NAME, tmpfs, random localhost port)"
docker run -d \
	--name "$REDIS_CONTAINER_NAME" \
	-p "127.0.0.1::6379" \
	--tmpfs /data:rw \
	"$REDIS_IMAGE" \
	redis-server --save "" --appendonly no >/dev/null

REDIS_PORT="$(docker port "$REDIS_CONTAINER_NAME" 6379/tcp | head -n 1 | awk -F: '{print $NF}')"
if [[ -z "$REDIS_PORT" ]]; then
	echo "ERROR: failed to determine mapped host port for $REDIS_CONTAINER_NAME" >&2
	exit 1
fi

echo "==> Waiting for Redis readiness on 127.0.0.1:${REDIS_PORT}"
redis_ready=0
for _ in $(seq 1 60); do
	if [[ "$(docker inspect -f '{{.State.Running}}' "$REDIS_CONTAINER_NAME" 2>/dev/null || true)" != "true" ]]; then
		echo "ERROR: ephemeral Redis container exited unexpectedly:" >&2
		docker logs "$REDIS_CONTAINER_NAME" >&2 || true
		exit 1
	fi
	if (exec 3<>/dev/tcp/127.0.0.1/"$REDIS_PORT") 2>/dev/null \
		&& [[ "$(docker exec "$REDIS_CONTAINER_NAME" redis-cli ping 2>/dev/null || true)" == "PONG" ]]; then
		redis_ready=1
		break
	fi
	sleep 0.25
done

if [[ "$redis_ready" -ne 1 ]]; then
	echo "ERROR: ephemeral Redis on 127.0.0.1:${REDIS_PORT} did not become ready in time" >&2
	docker logs "$REDIS_CONTAINER_NAME" >&2 || true
	exit 1
fi

export TEST_REDIS_ADDR="127.0.0.1:${REDIS_PORT}"

# Wrapper mode: allow other scripts (such as scripts/coverage.sh) to share this ephemeral Postgres + NATS + Redis lifecycle.
# Set TEST_WITH_SEARCH_DB=1 to also launch the ephemeral pgvector/pgvector:0.8.7-pg16 container (TEST_SEARCH_DB_URL).
if [[ "${1:-}" == "--run" ]]; then
	if [[ "${TEST_WITH_SEARCH_DB:-0}" == "1" ]]; then
		start_search_pg
	fi
	start_nats
	shift
	"$@"
	exit $?
fi

discover_go_pg_pkgs() {
	local svc_dir="$1"
	(
		cd "$svc_dir"
		# Find *_test.go files referencing TEST_*_DB_URL, TEST_NATS_URL, or TEST_REDIS_ADDR on non-comment lines.
		grep -rnE 'TEST_(([A-Z]+_DB|NATS)_URL|REDIS_ADDR)' --include='*_test.go' . 2>/dev/null \
			| grep -vE '^[^:]+:[0-9]+:[[:space:]]*//' \
			| cut -d: -f1 \
			| xargs -r -n1 dirname \
			| sort -u
	)
}

run_go_pg_tests() {
	local svc="$1"
	local svc_dir="$ROOT_DIR/services/$svc"
	local log_file="$TMP_DIR/${svc}.log"
	local pkgs=()
	mapfile -t pkgs < <(discover_go_pg_pkgs "$svc_dir")

	if [[ "${#pkgs[@]}" -eq 0 ]]; then
		echo "ERROR: no Go integration test packages found in services/$svc" >&2
		exit 1
	fi

	echo "==> [test-db] go test services/$svc (${pkgs[*]})"
	if ! (cd "$svc_dir" && go test -v -race -count=1 -p 1 "${pkgs[@]}") >"$log_file" 2>&1; then
		cat "$log_file"
		echo "ERROR: integration tests failed in services/$svc" >&2
		exit 1
	fi

	if grep -qE '^[[:space:]]*--- SKIP:' "$log_file"; then
		cat "$log_file"
		echo "ERROR: integration test(s) were SKIPPED in services/$svc:" >&2
		grep -E '^[[:space:]]*--- SKIP:' "$log_file" >&2
		exit 1
	fi

	local pass_count
	pass_count="$(grep -cE '^[[:space:]]*--- PASS:' "$log_file" || true)"
	if [[ "$pass_count" -eq 0 ]]; then
		cat "$log_file"
		echo "ERROR: zero tests passed in services/$svc" >&2
		exit 1
	fi
	echo "    PASS services/$svc (${pass_count} test cases/subtests, 0 skipped)"
}

for svc in order product promotion shipping user notification chat assistant; do
	start_nats "$svc"
	run_go_pg_tests "$svc"
	stop_nats
done

if [[ -d "$ROOT_DIR/services/search/migrations" ]]; then
	start_search_pg
	start_nats "search"
	run_go_pg_tests "search"
	stop_nats
	stop_search_pg
fi

start_nats "payment"
run_rust_payment_tests
stop_nats
echo "==> All Postgres, NATS, and Redis tests passed with 0 skips."
