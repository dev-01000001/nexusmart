SHELL := /usr/bin/env bash
export PATH := $(HOME)/.local/bin:$(HOME)/go/bin:$(HOME)/.cargo/bin:$(PATH)
export PROTOC := $(HOME)/.local/bin/protoc

GO_MODULES := pkg/common gen/go \
	$(wildcard services/gateway services/user services/product services/cart services/promotion services/order services/shipping services/notification)

.PHONY: proto lint test up down e2e seed e2e-web

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

up:
	docker compose up -d --build

down:
	docker compose down -v

# E2E runs inside the compose network (service gRPC ports are not published to the host).
e2e:
	GOMODCACHE="$$(go env GOMODCACHE)" docker compose --profile e2e run --rm --user "$$(id -u):$$(id -g)" e2e

# Idempotently seed storefront catalog (4 shops, 6 categories, 18 products/159 SKUs, 11 vouchers,
# 3 flash-sale windows) + demo buyer/seller/admin accounts via the gateway REST API.
seed:
	node --no-warnings=MODULE_TYPELESS_PACKAGE_JSON tools/seed/seed.mjs

# Web E2E suite through the BFF inside the compose network (never touches host web/.next or ports 3111/4777).
e2e-web:
	docker compose up -d --build --no-deps --wait web
	docker compose --profile e2e-web run --rm e2e-web
