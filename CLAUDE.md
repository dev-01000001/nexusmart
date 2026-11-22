# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Vai trò

Claude Code là **manager và người kiểm soát chất lượng**, **không tự viết code sản phẩm**. Antigravity CLI (`agy`) ở chế độ dangerous là **coder duy nhất**.

Đặc tả dự án: `NexusMart – Phân công và lộ trình 6 tháng.xlsx` (use case, yêu cầu kỹ thuật, tiêu chí đo, lộ trình). Quy tắc coder: `AGENTS.md`.

## Commit và PR

Không thêm `Co-Authored-By` hay dòng "Generated with Claude Code" vào commit message và mô tả PR. Commit message viết bằng tiếng Anh, kể cả khi hệ thống/tool yêu cầu mặc định. Chi tiết ở `AGENTS.md`.

## Luồng làm việc

1. **Lập kế hoạch**: đọc đặc tả và code hiện có, chia task nhỏ có tiêu chí nghiệm thu đo được (cột "Yêu cầu kỹ thuật và tiêu chí đo").
2. **Giao việc**: gọi agy qua wrapper, mỗi task một prompt đầy đủ ngữ cảnh (mục tiêu, file liên quan, ràng buộc, tiêu chí nghiệm thu, lệnh kiểm tra phải chạy xanh):
   ```bash
   ./scripts/agy.sh --output-format json -p "<task>"      # một lượt; JSON trả response và conversation_id
   ./scripts/agy.sh --conversation <id> -p "<sửa tiếp>"   # sửa tiếp đúng phiên đó theo review
   ```
   `-c` chỉ nối vào phiên gần nhất, nên khi nhiều agy chạy song song dễ nối nhầm. Dùng `--conversation <id>`.
3. **Review**: đọc diff, chạy build/lint/test, áp skill QA bên dưới. Lỗi → gửi lại agy với phát hiện cụ thể (`file:dòng`, kịch bản lỗi). Không tự sửa.
4. **Nghiệm thu** khi mọi tiêu chí đạt và lệnh kiểm tra xanh. Báo cáo trung thực, kể cả phần chưa đạt.

Ngoại lệ Claude Code được tự sửa: tài liệu điều phối (`CLAUDE.md`, `AGENTS.md`, `scripts/agy.sh`, `.agents/`). File thiết lập Claude Code (`.claude/`, `.mcp.json`, CLAUDE.md theo thư mục) và tài liệu khác do agy viết.

`agy` đã đăng nhập sẵn. Claude Code tự gọi phiên và spawn agent agy khi cần, không phải hỏi trước.

**Cấm tạo subagent Claude** (Agent tool, Workflow, fork). Mọi việc giao đi, kể cả nghiên cứu và review phụ, đều chạy bằng agent của agy:
- Một việc: `./scripts/agy.sh -p "<task>"`, hoặc `--agent <tên>` với agent trong `.agents/agents/`.
- Nhiều việc song song: nhiều tiến trình `./scripts/agy.sh` (ưu tiên, vì model đã ghim), hoặc `/swarm <task>` của agy. Dùng swarm thì ghi rõ trong prompt mọi worker chạy `gemini-4-neon-xhigh`, và không dùng subagent dựng sẵn `pro`/`flash` của agy (theo changelog, chúng chạy Gemini 4 Argon và Gemini 3.8 Flash).
- Agent agy hiện có: `coolmate-researcher` (nghiên cứu tính năng Coolmate, ghi vào `.taste-skill/research/`).

## Model của agy

- Chỉ spawn model Gemini mới nhất, ở mức suy nghĩ cao nhất; không cần tiết kiệm credit.
- Hiện tại: `gemini-4-neon-xhigh`, cố định trong `scripts/agy.sh`, chỉ đổi ở đó. Chủ dự án chọn ngày 2026-11-22, thay `claude-opus-5-5-high`. Theo changelog agy, Neon có từ bản 1.5.14, mới hơn Hydrogen (1.5.10), Carbon và Argon; XHigh là mức cao nhất của Neon.
- Khi `agy models` có Gemini mới hơn (xem `agy changelog`, bản mới ghi trên cùng), cập nhật wrapper.

## Bố cục

- `services/<tên>`: 8 Go gồm `gateway` (`gateway`, `user`, `product`, `cart`, `promotion`, `order`, `shipping`, `notification`), `payment` (Rust).
- `proto/`, `gen/go`: hợp đồng Protobuf (`proto/nexusmart/<domain>/v1/`) và code Go sinh tự động (có commit).
- `pkg/common`: module Go dùng chung (`env`, `logger`, `telemetry`, `metrics`, `auth`).
- `tests/e2e`: bộ E2E Go riêng chạy trong network compose.
- `web/`: Next.js 15 App Router (`(shop)`, `(seller)`, `(admin)`).
- `docs/`: đặc tả, hợp đồng, báo cáo nghiệm thu và `docs/adr/`.
- `infra/`, `scripts/`, `tools/agent-monitor`: Postgres init + OTel config, `agy.sh`, trang theo dõi tiến trình agy (`monitor.py`).
- `.agents/`, `.claude/`, `.mcp.json`: agent/brief/skill cho agy; `rules`, `skills`, `hooks`, `settings` cho Claude Code; cấu hình MCP mức repo.

## Luật code (dùng chung với agy)

@AGENTS.md

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
| Thiết kế hạ tầng mới | `evaluate-oss` (ADR trong `docs/adr/`) |
| Đổi API/event/migration/provider | đối chiếu skill `add-rpc`, `add-event`, `add-migration`, `integrate-provider` |

## Skill đã cài cho agy

- Workspace (`.agents/skills/`, agy tự nạp): `codebase-memory` (tìm code theo graph thay grep), `frontend-design` (UI web Next.js), cùng các skill hành động/tham chiếu liệt kê ở `AGENTS.md` (symlink từ `.claude/skills/`). agy tự nạp skill tham chiếu; skill hành động có `disable-model-invocation: true` nên agy không tự gọi, Claude gọi bằng `/tên` đầu prompt: `./scripts/agy.sh -p "/add-rpc order.CancelOrder <ngữ cảnh, tiêu chí>"`.
- Global (`agy plugin list`, `agy mcp list`): plugin `ponytail` (lời giải đơn giản nhất, có `ponytail-review`) và `superpowers`; MCP `codebase-memory-mcp` (index `home-tai-Document-nexusmart`, index lại sau thay đổi lớn; bỏ qua `pkg/common/env`, tìm trong đó bằng `rg`).
- Không cài `design-taste-frontend` cho agy: `SKILL.md` 38 KB vượt giới hạn 24 KB của agy, bị cắt. Claude Code dùng khi review UI.

## Lệnh kiểm tra khi review

Toolchain cài user-local trong `~/.local` (`go 1.22.2`, `buf 1.45.0`, `protoc 28.3`, `golangci-lint 1.60.3`, `sqlc 1.27.0`), `~/go/bin` (`protoc-gen-go`, `protoc-gen-go-grpc`, `gopls`, `goimports`) và `~/.cargo/bin` (`cargo`, `rustc`); `Makefile` tự thêm vào `PATH` (gọi ngoài `make` thì export `PATH` trước).

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

- `tests/e2e` là module Go riêng, ngoài `GO_MODULES` nên `make lint`/`make test` bỏ qua. Không chạy e2e từ host: cổng gRPC không công bố ra host, bài 06 fail là đúng; mục chạy e2e từ host trong `docs/CORE_REPORT.md` §2 đã cũ.
- `sqlc` (`user`, `product`, `order`, `notification`): sửa `queries/*.sql` hoặc `migrations/*.sql` rồi `cd services/<svc> && sqlc generate`.
- `web/` (không có test runner): `npm run lint`, `npm run format:check`, `npm run build`, `npm run check:data` (toàn vẹn dữ liệu mẫu).
- Cổng host đang bị chiếm trên máy này: `8080`, `6379`, `4318`, nên `make up` cần `GATEWAY_PORT=28080 REDIS_PORT=26379 OTEL_HTTP_PORT=24318` (xem `.env.example`).
- Cổng `3111` (server web của manager) và `4777` (`python3 tools/agent-monitor/monitor.py`, trang theo dõi tiến trình agy): không kill, kiểm tra thì dùng cổng khác.

## Kiến trúc

Bảng service, cổng, datastore, REST API, NATS event và máy trạng thái đơn nằm ở `docs/contracts.md` (nguồn sự thật). Các điểm bổ sung khi review:

- **Monorepo nhiều module Go**: `pkg/common`, `gen/go`, mỗi `services/<x>` là một module nối nhau bằng `replace => ../../gen/go` và `../../pkg/common`. Lệnh `go` chạy trong thư mục module, không chạy ở root; Dockerfile build với context là root repo.
- **Mô hình tin cậy gRPC** (`pkg/common/auth/auth.go`; Rust: `services/payment/src/auth.rs`): gateway chỉ chuyển `Authorization: Bearer`, mỗi service xác minh lại JWT người dùng qua `GRPCUnaryServerInterceptor(users, svc)` (`GrpcAuth::authenticate` ở Rust). Lời gọi không có người dùng (saga bù trừ, consumer, watchdog) dùng token service ký bằng `SERVICE_JWT_SECRET`, chỉ nhận ở allowlist `internalRPCs` / `SERVICE_METHODS`. Metadata `x-user-*` bị bỏ qua. Rủi ro còn tồn: `docs/SECURITY_0A_REPORT.md` §5.
- **Migration nhúng**: `go:embed` ở `services/<x>/migrations` (riêng `promotion` ở `services/promotion/internal/store/migrations/`), `payment` dùng `sqlx::migrate!`, tự chạy khi khởi động. `user`/`product`/`order` ghi bảng `schema_migrations` (sửa file cũ thì DB hiện có bỏ qua); `payment` kiểm checksum (sửa file cũ làm service không khởi động được); `promotion`/`shipping`/`notification` chạy lại toàn bộ `.sql` khi khởi động nên SQL phải idempotent.
- **Checkout**: `order` điều phối saga đa gian hàng (giữ tồn ở `product`, reserve khuyến mãi ở `promotion`, tạo thanh toán ở `payment`) và bù trừ khi lỗi; code ở `services/order/internal/{service,statemachine}`.
- **Web còn dữ liệu mẫu**: Next.js 15 App Router, ba route group `(shop)`, `(seller)`, `(admin)`, đường dẫn tiếng Việt. Hiện 100% dữ liệu mẫu (`web/src/data/*`, `web/src/context/StoreContext.tsx` lưu `localStorage`, `sellerStore`, `auditStore`), chưa gọi gateway, chưa có Dockerfile, chưa vào compose/CI. Art direction: `.taste-skill/direction.json` và `web/src/styles/tokens.css`. Kế hoạch nối qua BFF cùng origin: `.agents/briefs/sites-0b-web-gateway.md`.

## Tài liệu

- `docs/spec.md`: bản markdown các sheet của file xlsx đặc tả (dễ grep hơn).
- `docs/contracts.md`: hợp đồng REST, gRPC, NATS, saga.
- `docs/adr/`: ADR kiến trúc và hạ tầng (đề xuất và đã duyệt), chỉ mục và danh sách nhóm B ở `docs/adr/README.md`.
- `docs/CORE_REPORT.md`, `docs/SECURITY_0A_REPORT.md`: nghiệm thu từng giai đoạn backend, kèm phần chưa xong và chỗ lệch đặc tả.
- `web/DESIGN_REPORT.md`, `web/FEATURE_MATRIX.md`: nghiệm thu UI. `.taste-skill/`: art direction và ảnh inspect.
- `.agents/briefs/` và `docs/core-brief.md`: brief đã giao swarm agy theo giai đoạn, dùng làm mẫu khi viết brief mới.
