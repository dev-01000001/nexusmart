SHELL := /usr/bin/env bash
export PATH := $(HOME)/.local/bin:$(HOME)/go/bin:$(HOME)/.cargo/bin:$(PATH)
export PROTOC := $(HOME)/.local/bin/protoc

GO_MODULES := pkg/common gen/go \
	$(wildcard services/gateway services/user services/product services/cart services/promotion services/order services/shipping services/notification)

.PHONY: proto lint test test-db coverage up down e2e seed e2e-web

proto:
	buf lint
	buf generate
	cd gen/go && go mod tidy

lint:
	buf lint
	@set -euo pipefail; \
	for mod in $(GO_MODULES); do \
		if [ -f "$$mod/go.mod" ]; then \
			echo "==> gofmt & golangci-lint $$mod"; \
			unfmt=$$(gofmt -l "$$mod"); \
			if [ -n "$$unfmt" ]; then echo "Unformatted files: $$unfmt"; exit 1; fi; \
			(cd "$$mod" && golangci-lint run -c "$(CURDIR)/.golangci.yml" ./...) || exit 1; \
		fi; \
	done
	@set -euo pipefail; \
	if [ -f services/payment/Cargo.toml ]; then \
		echo "==> rustfmt & clippy services/payment"; \
		(cd services/payment && cargo fmt --all -- --check && cargo clippy --all-targets -- -D warnings); \
	fi

test:
	@set -euo pipefail; \
	for mod in $(GO_MODULES); do \
		if [ -f "$$mod/go.mod" ]; then \
			echo "==> go test $$mod"; \
			(cd "$$mod" && go test -race ./...) || exit 1; \
		fi; \
	done
	@set -euo pipefail; \
	if [ -f services/payment/Cargo.toml ]; then \
		echo "==> cargo test services/payment"; \
		(cd services/payment && cargo test); \
	fi

test-db:
	@bash scripts/test-db.sh

coverage:
	@bash scripts/coverage.sh

up:
	docker compose up -d --build

down:
	docker compose down -v

# E2E runs inside the compose network (service gRPC ports are not published to the host).
e2e:
	GOMODCACHE="$$(go env GOMODCACHE)" docker compose --profile e2e run --rm --user "$$(id -u):$$(id -g)" e2e

# Seed backend via gateway REST using web/src/data/* as single source of truth (idempotent).
seed:
	@set -euo pipefail; \
	_seed_gw="$${SEED_GATEWAY_URL:-}"; \
	_mp="$${MAILPIT_URL:-}"; \
	_admin_email="$${ADMIN_BOOTSTRAP_EMAIL:-}"; \
	_admin_pass="$${ADMIN_BOOTSTRAP_PASSWORD:-}"; \
	if [ -f .env ]; then set -a; source .env; set +a; fi; \
	export SEED_GATEWAY_URL="$${_seed_gw:-http://localhost:$${GATEWAY_PORT:-8080}}"; \
	export MAILPIT_URL="$${_mp:-http://localhost:$${MAILPIT_UI_PORT:-8025}}"; \
	export ADMIN_BOOTSTRAP_EMAIL="$${_admin_email:-$${ADMIN_BOOTSTRAP_EMAIL:-admin@nexusmart.local}}"; \
	export ADMIN_BOOTSTRAP_PASSWORD="$${_admin_pass:-$${ADMIN_BOOTSTRAP_PASSWORD:-AdminDevPassword123!}}"; \
	node tools/seed/seed.mjs

# End-to-end web BFF test suite (written by QA in tests/e2e-web/).
e2e-web:
	@set -euo pipefail; \
	_web_url="$${WEB_URL:-}"; \
	_mp_url="$${MAILPIT_URL:-}"; \
	_gw_url="$${GATEWAY_URL:-}"; \
	if [ -f .env ]; then set -a; source .env; set +a; fi; \
	export WEB_URL="$${_web_url:-http://localhost:$${WEB_PORT:-3000}}"; \
	export MAILPIT_URL="$${_mp_url:-http://localhost:$${MAILPIT_UI_PORT:-8025}}"; \
	export GATEWAY_URL="$${_gw_url:-http://localhost:$${GATEWAY_PORT:-8080}}"; \
	node --test --test-concurrency=1 'tests/e2e-web/*.test.mjs'
