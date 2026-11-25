# NexusMart

NexusMart là nền tảng thương mại điện tử đa cửa hàng độc lập theo tên miền phụ/tên miền riêng (đã bỏ sàn chung), gồm **13 service backend** trong `services/` (1 API gateway + 12 microservice: 11 Go là `gateway`, `user`, `product`, `cart`, `promotion`, `order`, `shipping`, `notification`, `search`, `chat`, `assistant`; 2 Rust là `payment`, `aiops`) và frontend Next.js 15 + TypeScript (`web/`). Mỗi service sở hữu datastore riêng (10 DB PostgreSQL tách biệt, Redis cho `cart`/`gateway`, Meilisearch + `pgvector` cho `search`), giao tiếp đồng bộ qua REST/gRPC (`proto/`) và bất đồng bộ qua NATS JetStream.

## Tài liệu chính

- `docs/spec.md` — đặc tả phạm vi, lộ trình, use case (xuất từ file xlsx).
- `docs/contracts.md` — hợp đồng REST, gRPC, NATS JetStream, saga đặt hàng, host routing và kênh bán.
- `docs/UPGRADE_FROM_PHASE_0B.md` — hướng dẫn nâng cấp từng bước từ nhánh `phase-0b` và quy trình quay lui.
- `docs/adr/` — các Architecture Decision Record (quyết định gộp nhánh ở `docs/adr/0010-unify-main-and-phase-0b.md`).
- `CLAUDE.md` — quy trình manager, skill review và lệnh kiểm tra.
- `AGENTS.md` — quy tắc và ràng buộc bắt buộc cho coder agent.
- `web/CLAUDE.md` và `services/payment/CLAUDE.md` — hướng dẫn riêng cho frontend và Payment Rust.

## Chạy nhanh

Toolchain cần có: `go` 1.22, `buf`, `protoc`, `golangci-lint`, `sqlc`, `cargo` (kèm `rustfmt`, `clippy`), `docker`, `node` (24). `Makefile` tự thêm `~/.local/bin`, `~/go/bin`, `~/.cargo/bin` vào `PATH`.

```bash
cp .env.example .env            # tuỳ chọn nếu cần đổi biến môi trường cục bộ
make up                         # khởi động 12 backend service + web + hạ tầng (cổng bị chiếm thì override, ví dụ: GATEWAY_PORT=28080 make up)
make seed                       # nạp dữ liệu mẫu idempotent qua Gateway REST + Mailpit
make e2e                        # chạy bộ E2E Go trong container compose (cần make up trước)
make e2e-web                    # chạy bộ E2E web BFF trong container compose
make lint                       # buf lint, gofmt, golangci-lint, rustfmt, clippy (payment & aiops)
make test                       # go test -race từng module Go + cargo test (payment & aiops)
make test-db                    # chạy toàn bộ test tích hợp Postgres/pgvector/Redis/NATS thật (0 skip)
make obs-up                     # tùy chọn: bật stack observability (Prometheus/Loki/Tempo/Grafana) + aiops
```

Kiểm tra frontend (`web/`): `cd web && npm ci`, rồi `npm run lint`, `npm run format:check`, `npm run test:unit`, `npm run check:data`, `npm run build`.

## Thiết lập Claude Code cho thành viên mới

1. Cài ba language server:
   - `gopls`: `go install golang.org/x/tools/gopls@latest` (Go cũ thì thêm `GOTOOLCHAIN=auto`)
   - `rust-analyzer`: `rustup component add rust-analyzer`
   - `typescript-language-server`: `npm i -g typescript-language-server typescript`

2. Cài plugin project scope một lần trên máy mình:

```bash
claude plugin install gopls-lsp@claude-plugins-official --scope project
claude plugin install rust-analyzer-lsp@claude-plugins-official --scope project
claude plugin install typescript-lsp@claude-plugins-official --scope project
claude plugin install security-guidance@claude-plugins-official --scope project
claude plugin install frontend-design@claude-plugins-official --scope project
claude plugin install playwright@claude-plugins-official --scope project
claude plugin install context7@claude-plugins-official --scope project
claude plugin install commit-commands@claude-plugins-official --scope project
```

3. MCP Postgres chỉ đọc (`.mcp.json`): cần Docker; chạy `export POSTGRES_PASSWORD=...` (giá trị dev trong `.env.example`) trước khi mở Claude Code và khởi động stack bằng `make up`. Lần đầu mở, Claude Code sẽ hỏi duyệt 7 server (`pg-user` … `pg-notification`).

4. Hook trong `.claude/settings.json`:
   - `.claude/hooks/format.sh` tự format file vừa sửa (`.go`, `.rs`, và `web/` — cần `cd web && npm ci` để có Prettier).
   - `.claude/hooks/guard.sh` chặn sửa `.env*` (trừ `.env.example`), `gen/go/`, file code sinh (`Code generated` + `DO NOT EDIT`), và file migration đã merge trên nhánh `main`.

5. Người điều phối gọi coder agent `agy` qua `scripts/agy.sh` (xem `CLAUDE.md`).
