# NexusMart

NexusMart là sàn thương mại điện tử đa gian hàng theo kiến trúc 11 microservice sau một API gateway (hiện repo có 9 service backend lõi trong `services/` và frontend `web/`). Hệ thống dùng Go cho API gateway và phần lớn service, Rust cho Payment (và AIOps theo đặc tả), TypeScript + Next.js cho web người mua, người bán và quản trị. Mỗi service có database riêng, giao tiếp đồng bộ qua REST/gRPC (`proto/`) và bất đồng bộ qua NATS JetStream.

## Tài liệu chính

- `docs/spec.md` — đặc tả phạm vi, lộ trình, use case (xuất từ file xlsx).
- `docs/contracts.md` — hợp đồng REST, gRPC, NATS JetStream, saga đặt hàng.
- `docs/adr/` — các Architecture Decision Record.
- `CLAUDE.md` — quy trình manager, skill review và lệnh kiểm tra.
- `AGENTS.md` — quy tắc và ràng buộc bắt buộc cho coder agent.

## Chạy nhanh

Toolchain cần có: `go` 1.22, `buf`, `protoc`, `golangci-lint`, `sqlc`, `cargo` (kèm `rustfmt`, `clippy`), `docker`, `node`. `Makefile` tự thêm `~/.local/bin`, `~/go/bin`, `~/.cargo/bin` vào `PATH`.

```bash
cp .env.example .env            # tuỳ chọn nếu cần đổi biến môi trường cục bộ
make up                         # cổng host bị chiếm thì override, ví dụ: GATEWAY_PORT=28080 make up
make e2e                        # chạy bộ E2E trong container compose (cần make up trước)
make lint                       # buf lint, gofmt, golangci-lint, rustfmt, clippy
make test                       # go test -race từng module Go + cargo test
```

Kiểm tra frontend (`web/`): `cd web && npm ci`, rồi `npm run lint`, `npm run format:check`, `npm run build`, `npm run check:data`.

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
