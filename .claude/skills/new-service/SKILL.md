---
name: new-service
description: Tạo microservice Go mới trong NexusMart theo đúng khuôn monorepo hiện có (module, common interceptors, ops endpoints, Postgres/sqlc, Dockerfile non-root, compose, Makefile, CI). Dùng khi cần dựng một service Go mới hoàn toàn.
disable-model-invocation: true
---

# Tạo microservice Go mới

Đầu vào (`$ARGUMENTS`): `<tên-service>` (ví dụ: `review`, `search`; kèm cổng gRPC/HTTP và datastore nếu đã chốt).

Nếu service cần thành phần hạ tầng mới ngoài Postgres/Redis/NATS/OTel hiện tại (`docs/contracts.md` §1), chạy skill `evaluate-oss` và chờ chủ dự án duyệt ADR trước.

## 1. Chốt cổng, datastore và module Go

- [ ] Kiểm tra bảng cổng trong `docs/contracts.md` §1 (hiện dùng gRPC `50051`–`50058`, HTTP `8080`–`8088`), chọn cặp cổng kế tiếp chưa trùng.
- [ ] Tạo `services/<svc>/go.mod` (`go 1.22`) với `replace` cục bộ theo mẫu `services/notification/go.mod` và `services/order/go.mod`:
  - `github.com/nexusmart/core/gen/go => ../../gen/go`
  - `github.com/nexusmart/core/pkg/common => ../../pkg/common`

## 2. Khung mã nguồn theo mẫu repo

Dùng `services/notification/` (gọn) và `services/order/` (đầy đủ outbox + `schema_migrations`) làm mẫu:

- [ ] `services/<svc>/internal/config/config.go`: đọc biến môi trường qua `pkg/common/env` (`EnvString`, `EnvInt`, `EnvBool`, `EnvDuration`) kèm default dev an toàn, không nhúng secret thật (mẫu `services/notification/internal/config/config.go`, `services/order/internal/config/config.go`).
- [ ] `services/<svc>/cmd/server/main.go` (mẫu `services/notification/cmd/server/main.go`, `services/order/cmd/server/main.go`):
  - Logger JSON: `logger.New(cfg.ServiceName, logger.WithLevel(cfg.LogLevel))` (`pkg/common/logger`).
  - Tracing: `telemetry.Init(...)` (`pkg/common/telemetry`).
  - RED metrics: `metrics.NewRED(cfg.ServiceName, reg)` (`pkg/common/metrics`).
  - Auth: khởi tạo 2 manager `jwtMgr` (`JWT_SECRET`, `auth.DefaultAccessTTL`) và `svcMgr` (`SERVICE_JWT_SECRET`, `auth.ServiceTokenTTL`).
  - gRPC server gắn đủ 3 interceptor theo đúng thứ tự:
    1. `telemetry.GRPCUnaryServerInterceptor(cfg.ServiceName)`
    2. `red.GRPCUnaryServerInterceptor()`
    3. `auth.GRPCUnaryServerInterceptor(jwtMgr, svcMgr)`
  - HTTP ops server mở đủ `/healthz`, `/readyz` (ping mọi dependency DB/Redis/NATS), `/metrics` qua `metrics.NewOpsHandler` bọc trong `telemetry.HTTPMiddleware` và `red.HTTPMiddleware`.
  - Graceful shutdown trên `SIGINT`/`SIGTERM` cho HTTP, gRPC và background worker.
- [ ] `services/<svc>/internal/grpcserver/server.go`: implement `*ServiceServer`, kiểm quyền theo đối tượng bằng `auth.ClaimsFromContext(ctx)` (mẫu `services/notification/internal/grpcserver/server.go`). Không tin metadata `x-user-*`.

## 3. Database riêng và sqlc (nếu dùng PostgreSQL)

- [ ] Thêm `CREATE DATABASE <svc>_db;` vào `infra/postgres/init-dbs.sql`. Cấm truy vấn trực tiếp DB của service khác.
- [ ] Tạo `services/<svc>/migrations/0001_init.sql` và `services/<svc>/migrations/embed.go` (`//go:embed *.sql`, mẫu `services/order/migrations/embed.go`).
- [ ] Tạo `services/<svc>/internal/db/migrate.go` theo mẫu `services/order/internal/db/migrate.go` (`ApplyMigrations`: ghi tên file vào `schema_migrations`, chạy mỗi file trong một transaction).
- [ ] Tạo `services/<svc>/sqlc.yaml` (`sql_package: "pgx/v5"`, `schema: "migrations/"`, `queries: "queries/"`, `out: "internal/db"`, mẫu `services/order/sqlc.yaml` hoặc `services/notification/sqlc.yaml`), viết `queries/<svc>.sql`, rồi chạy `cd services/<svc> && sqlc generate`.

## 4. Protobuf, Gateway và Docker

- [ ] Định nghĩa hợp đồng gRPC qua skill `add-rpc` (`proto/nexusmart/<svc>/v1/<svc>.proto`, `make proto`).
- [ ] Nếu client gọi qua REST: thêm `<SVC>_GRPC_ADDR` vào `services/gateway/internal/config/config.go`, dial client trong `services/gateway/internal/clients/clients.go`, thêm route ở `services/gateway/internal/router/`.
- [ ] Tạo `services/<svc>/Dockerfile` multi-stage non-root theo mẫu `services/notification/Dockerfile` hoặc `services/order/Dockerfile`:
  - Build context là root repo (`.`), copy `gen/go`, `pkg/common`, `services/<svc>`.
  - Runtime `alpine:3.20`, cài `ca-certificates wget`, chạy dưới `USER appuser` (không root).
- [ ] Thêm service vào `docker-compose.yml`:
  - `build.context: .`, `dockerfile: services/<svc>/Dockerfile`.
  - `depends_on` dùng `condition: service_healthy` (postgres/redis/nats) và `service_started` (`otel-collector`).
  - `healthcheck` gọi `wget -qO- http://127.0.0.1:<HTTP_PORT>/healthz || exit 1`.
  - **Không publish cổng gRPC ra host**; cổng HTTP map qua `${<SVC>_HTTP_PORT:-<port>}:<port>` và khai báo biến mặc định trong `.env.example`.

## 5. Build, CI, test và tài liệu

- [ ] Thêm `services/<svc>` vào `GO_MODULES` trong `Makefile`.
- [ ] Thêm `services/<svc>` vào matrix `go-lint-and-test` và `<svc>` vào matrix `docker-build` trong `.github/workflows/ci.yml`. Giữ nguyên `on: workflow_dispatch` (không thêm `push`, `pull_request`, `schedule`).
- [ ] Viết unit test cho gRPC auth/ownership (mẫu `services/notification/internal/grpcserver/server_test.go`, `services/product/internal/server/grpc_trust_test.go`) và logic nghiệp vụ.
- [ ] Cập nhật bảng service/cổng/datastore trong `docs/contracts.md` §1 và biến môi trường trong §7.

## Gate

```bash
cd services/<svc> && go mod tidy && sqlc generate   # bỏ sqlc generate nếu không dùng sqlc
make proto
make lint
make test
docker build -f services/<svc>/Dockerfile -t nexusmart/<svc>:check .
```
