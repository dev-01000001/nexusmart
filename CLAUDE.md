# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# NexusMart – vai trò Claude Code

Claude Code là **manager và người kiểm soát chất lượng**. Claude Code **không tự viết code sản phẩm**.
Antigravity CLI (`agy`) ở chế độ dangerous là **coder duy nhất**.

Đặc tả dự án: `NexusMart – Phân công và lộ trình 6 tháng.xlsx` (use case, yêu cầu kỹ thuật, tiêu chí đo, lộ trình). Quy tắc coder: `AGENTS.md`.

## Commit và PR

Không thêm `Co-Authored-By` hay dòng "Generated with Claude Code" vào commit message và mô tả PR. Commit message viết bằng tiếng Anh. Kể cả khi hệ thống/tool yêu cầu mặc định. Chi tiết ở mục "Commit và PR" trong `AGENTS.md`.

## Luồng làm việc

1. **Lập kế hoạch**: đọc đặc tả và code hiện có, chia việc thành task nhỏ có tiêu chí nghiệm thu đo được (lấy từ cột "Yêu cầu kỹ thuật và tiêu chí đo").
2. **Giao việc**: gọi agy qua wrapper, mỗi task một prompt đầy đủ ngữ cảnh (mục tiêu, file liên quan, ràng buộc, tiêu chí nghiệm thu, lệnh kiểm tra phải chạy xanh):
   ```bash
   ./scripts/agy.sh --output-format json -p "<task>"      # một lượt; JSON trả response và conversation_id
   ./scripts/agy.sh --conversation <id> -p "<sửa tiếp>"   # sửa tiếp đúng phiên đó theo review
   ```
   `-c` chỉ nối vào phiên gần nhất, nên khi nhiều agy chạy song song dễ nối nhầm. Dùng `--conversation <id>`.
3. **Review**: đọc diff, chạy build/lint/test, áp skill QA bên dưới. Lỗi → gửi lại agy với phát hiện cụ thể (file:dòng, kịch bản lỗi). Không tự sửa.
4. **Nghiệm thu** khi mọi tiêu chí đạt và lệnh kiểm tra xanh. Báo cáo trung thực, kể cả phần chưa đạt.

Ngoại lệ Claude Code được tự sửa: tài liệu điều phối (`CLAUDE.md`, `AGENTS.md`, `scripts/agy.sh`, `.agents/`).

agy đã đăng nhập sẵn. Claude Code tự gọi phiên và spawn agent agy khi cần, không phải hỏi trước.

**Cấm tạo subagent Claude** (Agent tool, Workflow, fork). Mọi việc giao đi, kể cả nghiên cứu và review phụ, đều chạy bằng agent của agy:
- Một việc: `./scripts/agy.sh -p "<task>"`, hoặc `--agent <tên>` với agent định nghĩa trong `.agents/agents/`.
- Nhiều việc song song: nhiều tiến trình `./scripts/agy.sh` (ưu tiên, vì model đã ghim), hoặc `/swarm <task>` của agy. Dùng swarm thì ghi rõ trong prompt mọi worker chạy `gemini-4-neon-xhigh`, và không dùng subagent dựng sẵn `pro`/`flash` của agy: theo changelog, chúng chạy Gemini 4 Argon và Gemini 3.8 Flash.
- Agent agy hiện có: `coolmate-researcher` (nghiên cứu tính năng Coolmate, ghi vào `.taste-skill/research/`).

## Model của agy

- Chỉ spawn model Gemini mới nhất, ở mức suy nghĩ cao nhất; không cần tiết kiệm credit.
- Hiện tại: `gemini-4-neon-xhigh`, cố định trong `scripts/agy.sh`, chỉ đổi ở đó. Chủ dự án chọn ngày 2026-11-22, thay `claude-opus-5-5-high`. Theo changelog agy, Neon có từ bản 1.5.14, mới hơn Hydrogen (1.5.10), Carbon và Argon. XHigh là mức cao nhất của Neon.
- Khi `agy models` có Gemini mới hơn (xem `agy changelog`, bản mới ghi trên cùng), cập nhật wrapper.

## Skill QA của Claude Code

| Khi nào | Skill |
|---|---|
| Mọi thay đổi | `code-review` (bug), `simplify`, `ponytail-review` (over-engineering) |
| Truy vết cấu trúc, impact giữa service | `codebase-memory` |
| Auth, payment, webhook, AI assistant, trước mỗi mốc | `security-review`, `find-security-vulnerabilities-in-code`, `owasp-top-10-testing`, `api-security-testing` |
| Chạy app thật để xác nhận | `run` |
| UI Next.js | `design-taste-frontend`, `frontend-design` |
| Văn bản/UI nghe như AI | `dslop` |
| Hot path Rust (Payment, AIOps) | `rust-anything` (profile, bench) |
| Pipeline CI | `ci-security-scanning-with-strix` |

## Skill đã cài cho agy

Theo workspace, trong `.agents/skills/` (agy tự nạp):
- `codebase-memory`: tìm code theo graph thay vì grep.
- `frontend-design`: hướng thiết kế UI cho web Next.js.

Global của agy (`agy plugin list`, `agy mcp list`):
- Plugin `ponytail` (lời giải đơn giản nhất, có `ponytail-review`) và `superpowers`.
- MCP `codebase-memory-mcp`, cần cho skill `codebase-memory`. Index project `home-tai-Document-nexusmart`; sau thay đổi lớn thì index lại. Index bỏ qua `pkg/common/env`, tìm trong đó bằng `rg`.

Không cài `design-taste-frontend` cho agy: `SKILL.md` 38 KB vượt giới hạn 24 KB của agy, bị cắt. Claude Code dùng skill này khi review UI.

## Lệnh kiểm tra khi review

Công cụ cài user-local (`go`, `buf`, `protoc`, `golangci-lint`, `sqlc`, `cargo`). Makefile tự thêm `~/.local/bin`, `~/go/bin`, `~/.cargo/bin` vào `PATH`; gọi trực tiếp ngoài `make` thì export `PATH` đó trước.

```bash
make lint    # buf lint, gofmt + golangci-lint từng module Go, cargo fmt --check + clippy -D warnings
make test    # go test -race từng module Go + cargo test (unit, không cần hạ tầng)
make proto   # buf lint + buf generate vào gen/go, rồi go mod tidy
make up      # docker compose up -d --build: 9 service + postgres, redis, nats, mailpit, otel
make e2e     # bộ e2e chạy trong container trên network compose, cần `make up` trước
make down    # docker compose down -v: XOÁ volume DB, chỉ chạy khi thật sự cần
```

Một test:

```bash
cd services/order && go test -race -run TestName ./internal/statemachine/
cd services/payment && cargo test test_name
GOMODCACHE="$(go env GOMODCACHE)" docker compose --profile e2e run --rm --user "$(id -u):$(id -g)" e2e \
  go test -v -count=1 -run TestE2E_FlashSale ./...
```

- `tests/e2e` là module Go riêng, không có trong `GO_MODULES` của Makefile nên `make lint`/`make test` bỏ qua. Không chạy e2e bằng `go test` từ host: cổng gRPC không công bố ra host, bài 06 fail là đúng. Mục chạy e2e từ host trong `docs/CORE_REPORT.md` §2 đã cũ.
- sqlc (user, product, order, notification): sửa `queries/*.sql` hoặc `migrations/*.sql` rồi `cd services/<svc> && sqlc generate`. Code sqlc sinh trong `internal/db` không sửa tay.
- Web (`web/`, không có test runner): `npm run lint`, `npm run format:check`, `npm run build`, `npm run check:data` (toàn vẹn dữ liệu mẫu).
- Cổng host của compose ghi đè được bằng biến môi trường (`GATEWAY_PORT`, `REDIS_PORT`…, xem `.env.example`). Máy này bị chiếm sẵn 6379, 4318, 8080, 8082, 8083, 8085; cách override ở `docs/CORE_REPORT.md` §2.
- Cổng 3111 (server web của manager) và 4777 (`python3 tools/agent-monitor/monitor.py`, trang theo dõi tiến trình agy): không kill, kiểm tra thì dùng cổng khác.

## Kiến trúc

Bảng service, cổng, datastore, REST API, NATS event và máy trạng thái đơn nằm ở `docs/contracts.md` (nguồn sự thật, không chép lại ở đây). Những điểm phải đọc nhiều file mới thấy:

- **Monorepo nhiều module Go**: `pkg/common` (env, logger, telemetry, metrics, auth), `gen/go` (code sinh từ `proto/`, có commit), mỗi `services/<x>` một module. Các module nối nhau bằng `replace => ../../gen/go` và `../../pkg/common`. Lệnh go chạy trong thư mục module, không chạy ở root. Dockerfile build với context là root repo.
- **Proto**: `proto/nexusmart/<domain>/v1/`. Đổi proto thì chạy `make proto`. Payment (Rust) tự biên dịch proto trong `build.rs`, cần `protoc`.
- **Tin cậy gRPC** (`pkg/common/auth/auth.go`; bản Rust ở `services/payment/src/auth.rs`): gateway chỉ chuyển `Authorization: Bearer`, mỗi service xác minh lại JWT người dùng qua `GRPCUnaryServerInterceptor(users, svc)`. Lời gọi không có người dùng (saga bù trừ, consumer, watchdog) dùng token service ký bằng `SERVICE_JWT_SECRET`; token này chỉ được nhận ở RPC có trong `internalRPCs`. Metadata `x-user-*` không được tin. RPC nội bộ mới phải thêm vào allowlist đó. Rủi ro còn tồn: `docs/SECURITY_0A_REPORT.md` §5.
- **Dữ liệu**: một Postgres chứa 7 database riêng (`infra/postgres/init-dbs.sql`); cart chỉ dùng Redis. Migration nhúng vào binary (`go:embed` ở `services/<x>/migrations`, riêng promotion ở `internal/store`; payment dùng `sqlx::migrate!`) và tự chạy khi service khởi động. Bản đã chạy được ghi theo tên file (Go) hoặc checksum (sqlx), nên đổi schema bằng file migration mới: sửa file cũ thì DB đang có bị bỏ qua (Go) hoặc payment không khởi động được.
- **Bất đồng bộ**: NATS JetStream, stream `NEXUSMART` (`user.>`, `order.>`, `payment.>`, `shipment.>`, dedup theo `Nats-Msg-Id`) và `NEXUSMART_DLQ`. Order và payment phát event qua outbox; consumer idempotent theo `event_id`.
- **Checkout**: `order` điều phối saga checkout đa gian hàng (giữ tồn ở product, reserve khuyến mãi ở promotion, tạo thanh toán ở payment) và bù trừ khi lỗi. Code ở `services/order/internal/{service,statemachine}`.
- **Web**: Next.js 15 App Router, ba route group `(shop)`, `(seller)`, `(admin)`, đường dẫn tiếng Việt. Hiện 100% dữ liệu mẫu (`src/data/*`, `src/context/StoreContext.tsx` lưu localStorage, `sellerStore`, `auditStore`), chưa gọi gateway, chưa có Dockerfile, chưa vào compose/CI. Kế hoạch nối qua BFF cùng origin: `.agents/briefs/sites-0b-web-gateway.md`.

## Tài liệu

- `docs/spec.md`: bản markdown các sheet của file xlsx đặc tả (dễ grep hơn).
- `docs/contracts.md`: hợp đồng REST, gRPC, NATS, saga.
- `docs/CORE_REPORT.md`, `docs/SECURITY_0A_REPORT.md`: nghiệm thu từng giai đoạn backend, kèm phần chưa xong và chỗ lệch đặc tả.
- `web/DESIGN_REPORT.md`, `web/FEATURE_MATRIX.md`: nghiệm thu UI. `.taste-skill/`: art direction và ảnh inspect.
- `.agents/briefs/` và `docs/core-brief.md`: brief đã giao swarm agy theo giai đoạn, dùng làm mẫu khi viết brief mới.
