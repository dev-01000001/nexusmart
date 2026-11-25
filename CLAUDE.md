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

## Chạy nhiều swarm song song

Chủ dự án yêu cầu (2026-11-21): tối ưu swarm, chạy nhiều swarm cùng lúc để tiết kiệm thời gian.
- Mỗi giai đoạn chia thành nhiều swarm agy chạy cùng lúc (2–4 cho một giai đoạn nhỏ; chủ dự án cho phép nhiều hơn, ví dụ 9 swarm cho các mốc tháng 4–6, khi ownership tách được), mỗi swarm một tiến trình nền riêng và một brief riêng trong `.agents/briefs/`.
- Mỗi swarm sở hữu một tập thư mục không giao nhau (ví dụ: `services/` + `proto/`; `web/src/app/(site)` + BFF; seller + admin; `tests/` + `tools/seed`). Brief ghi rõ thư mục được sửa và thư mục cấm.
- Hợp đồng dùng chung (proto, REST, kiểu dữ liệu) do Claude Code chốt trong brief trước khi khởi chạy, để các swarm không chờ nhau.
- Chỉ một swarm (QA) được chạy `make e2e`, `make seed`, `make e2e-web` và rebuild container: stack compose và giới hạn tần suất là tài nguyên dùng chung. Các swarm khác chỉ chạy lint, unit test và build với `NEXT_DIST_DIR` riêng.
- Không chèn swarm mới vào các file mà một swarm đang chạy sở hữu. Không kill swarm đang chạy dở để chia lại.
- Claude Code chạy lại gate và review song song với swarm sửa lỗi, không chờ tuần tự.
- Làm đến đâu commit và push đến đó (chủ dự án yêu cầu 2026-11-21): Claude Code commit checkpoint `wip(<giai đoạn>):` định kỳ khi swarm đang chạy, và commit sạch sau khi nghiệm thu. Push lên nhánh làm việc, không push `main` khi chưa hỏi. Worker agy không commit.

## Model của agy

- Chỉ spawn model Gemini mới nhất, ở mức suy nghĩ cao nhất; không cần tiết kiệm credit.
- Hiện tại: `gemini-4-neon-xhigh`, cố định trong `scripts/agy.sh`, chỉ đổi ở đó. Chủ dự án chọn ngày 2026-11-22, thay `claude-opus-5-5-high`. Theo changelog agy, Neon có từ bản 1.5.14, mới hơn Hydrogen (1.5.10), Carbon và Argon; XHigh là mức cao nhất của Neon.
- Khi `agy models` có Gemini mới hơn (xem `agy changelog`, bản mới ghi trên cùng), cập nhật wrapper.

## Bố cục

- `services/<tên>`: 13 service — 11 Go (`gateway`, `user`, `product`, `cart`, `promotion`, `order`, `shipping`, `notification`, `search`, `chat`, `assistant`) và 2 Rust (`payment`, `aiops`).
- `proto/`, `gen/go`: hợp đồng Protobuf (`proto/nexusmart/<domain>/v1/`, 11 miền) và code Go sinh tự động (có commit).
- `pkg/common`: module Go dùng chung (`env`, `logger`, `telemetry`, `metrics`, `auth`, `jetstream`, `secretbox`, `textnorm`).
- `tests/e2e` và `tests/e2e-web`: bộ E2E Go (backend) và Node 24 (web BFF) chạy trong network compose.
- `web/`: Next.js 15 App Router (Node 24 standalone) gồm 4 route group — `(shop)` (chỉ trang giới thiệu nền tảng + danh bạ site + xác thực + redirect 308; **không còn sàn chung**), `(site)/s/[host]` (toàn bộ mua hàng theo từng site gian hàng độc lập), `(seller)` và `(admin)` trên host gốc; nối backend thật qua 179 BFF route tường minh (`/api/*`).
- `docs/`: đặc tả, hợp đồng (`docs/contracts.md`), hướng dẫn nâng cấp (`docs/UPGRADE_FROM_PHASE_0B.md`), báo cáo nghiệm thu và `docs/adr/`.
- `deploy/`, `infra/`, `scripts/`, `tools/`, `evals/assistant/`: Helm/K8s/observability (`deploy/`), Postgres init 9 DB + OTel config (`infra/`), `agy.sh` & `test-db.sh` (`scripts/`), `tools/seed/`, `tools/backup/`, `tools/chaos/`, `tools/catalog-gen/`, `tools/agent-monitor/` (`monitor.py`), bộ chấm AI assistant (`evals/assistant/`).
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
make lint           # buf lint, gofmt + golangci-lint mọi module Go, cargo fmt --check + clippy -D warnings (payment & aiops)
make test           # go test -race từng module Go + cargo test (payment & aiops; unit, không cần hạ tầng)
make test-db        # scripts/test-db.sh: test tích hợp Postgres + pgvector + Redis + NATS thật (0 skip)
make proto          # buf lint + buf generate vào gen/go, rồi go mod tidy
make up             # docker compose up -d --build: 12 backend service + web + postgres, search-postgres, meilisearch, redis, nats, mailpit, otel
make seed           # nạp dữ liệu mẫu idempotent qua Gateway REST + Mailpit (tools/seed/seed.mjs)
make e2e            # bộ E2E Go chạy trong container trên network compose, cần `make up` trước
make e2e-web        # rebuild web (--no-deps) + chạy bộ E2E web BFF trong network compose
make eval-assistant # chấm định lượng AI assistant (100 ca tiếng Việt + 36 ca prompt-injection)
make obs-up         # bật stack observability (Prometheus, Alertmanager, Loki, Tempo, Grafana) + aiops (profile obs)
make helm-lint      # kiểm chart Helm & manifest K8s (deploy/k8s/scripts/lint.sh)
make backup         # sao lưu DB (tools/backup/backup.sh); make restore-drill để diễn tập khôi phục
make down           # docker compose down -v: XOÁ volume DB, chỉ chạy khi thật sự cần
```

Một test:

```bash
cd services/order && go test -race -run TestName ./internal/statemachine/
cd services/payment && cargo test test_name
GOMODCACHE="$(go env GOMODCACHE)" docker compose --profile e2e run --rm --user "$(id -u):$(id -g)" e2e \
  go test -v -count=1 -run TestE2E_FlashSale ./...
```

- `tests/e2e` là module Go riêng, ngoài `GO_MODULES` nên `make lint`/`make test` bỏ qua. Không chạy e2e từ host: cổng gRPC không công bố ra host, bài 06 fail là đúng; mục chạy e2e từ host trong `docs/CORE_REPORT.md` §2 đã cũ.
- `sqlc` (`user`, `product`, `order`, `notification`, `search`): sửa `queries/*.sql` hoặc `migrations/*.sql` rồi `cd services/<svc> && sqlc generate`.
- `web/`: `npm run lint`, `npm run format:check`, `npm run test:unit`, `npm run check:data`, `npm run build` (build với `NEXT_DIST_DIR` riêng thì hoàn nguyên `web/tsconfig.json` trước khi commit); E2E web qua `make e2e-web`.
- Cổng mặc định: `GATEWAY_PORT=8080`, `WEB_PORT=3300` (mạng nội bộ `bff` `172.31.250.0/28`). Nếu cổng host đang bị chiếm (`8080`, `6379`, `4318`), override khi chạy `make up` (ví dụ `GATEWAY_PORT=28080 REDIS_PORT=26379 OTEL_HTTP_PORT=24318`, xem `.env.example`).
- Cổng `3111` (server web của manager) và `4777` (`python3 tools/agent-monitor/monitor.py`, trang theo dõi tiến trình agy): không kill, kiểm tra thì dùng cổng khác.

## Kiến trúc

Bảng 13 service, cổng, datastore, REST API, NATS event, kênh bán và máy trạng thái đơn nằm ở `docs/contracts.md` (nguồn sự thật). Các điểm bổ sung khi review:

- **Monorepo nhiều module Go**: `pkg/common`, `gen/go`, mỗi `services/<x>` là một module nối nhau bằng `replace => ../../gen/go` và `../../pkg/common`. Lệnh `go` chạy trong thư mục module, không chạy ở root; Dockerfile build với context là root repo.
- **Mô hình tin cậy gRPC** (`pkg/common/auth/auth.go`; Rust: `services/payment/src/auth.rs`): gateway chỉ chuyển `Authorization: Bearer`, mỗi service xác minh lại JWT người dùng qua `GRPCUnaryServerInterceptor(users, svc)` (`GrpcAuth::authenticate` ở Rust). Lời gọi không có người dùng (saga bù trừ, consumer, watchdog) dùng token service ký bằng `SERVICE_JWT_SECRET`, chỉ nhận ở allowlist `internalRPCs` / `SERVICE_METHOD_ALLOWLIST`. Phân quyền 6 vai trò quản trị đọc từ một nguồn `pkg/common/auth/capabilities.json`. Metadata `x-user-*` bị bỏ qua.
- **Migration nhúng**: `go:embed` ở `services/<x>/migrations` (riêng `promotion` ở `services/promotion/internal/store/migrations/`), `payment` dùng `sqlx::migrate!`, tự chạy khi khởi động. `user`/`product`/`order`/`shipping`/`search`/`assistant` ghi bảng `schema_migrations` (`shipping` có bootstrap nhận diện DB `phase-0b` qua cột `shipments.direction`), `promotion` ghi `promotion_schema_migrations`, `payment` kiểm checksum, còn `notification` và `chat` chạy lại toàn bộ `.sql` khi khởi động nên SQL phải idempotent. Bảng ánh xạ số migration đợt gộp ghi trong `docs/adr/0010-unify-main-and-phase-0b.md`.
- **Checkout & Kênh bán**: `order` điều phối saga checkout (giữ tồn ở `product`, reserve khuyến mãi ở `promotion`, tạo thanh toán ở `payment`) và bù trừ khi lỗi; code ở `services/order/internal/{service,statemachine}`. `POST /api/v1/checkout` mặc định `sales_channel = "STOREFRONT"` (bắt buộc `storefront_shop_id`); `MARKETPLACE` gửi tường minh vẫn được gateway nhận cho saga đa gian hàng & E2E, web BFF không bao giờ gửi `MARKETPLACE`.
- **Web đa cửa hàng & BFF tường minh**: Next.js 15 App Router (Node 24, `web/Dockerfile` build từ root repo, đã có trong `docker-compose.yml` và `.github/workflows/ci.yml`), 4 route group `(shop)`, `(site)/s/[host]`, `(seller)`, `(admin)` — **đã bỏ sàn chung và dữ liệu mẫu thương mại**. Toàn bộ luồng mua hàng diễn ra trên site gian hàng độc lập (`<subdomain>.<SITE_ROOT_DOMAIN>` hoặc tên miền riêng) qua 179 BFF route tường minh `/api/*` đi qua mạng Docker nội bộ `bff` (`172.31.250.0/28` → `http://gateway-bff:8080`, 0 CORS trên gateway). Art direction: `.taste-skill/direction.json`, `web/src/styles/tokens.css`, font Archivo variable. Chi tiết xem `web/CLAUDE.md`.

## Tài liệu

- `docs/spec.md`: bản markdown các sheet của file xlsx đặc tả (dễ grep hơn).
- `docs/contracts.md`: hợp đồng REST, gRPC, NATS, saga, host routing, kênh bán cho cả 13 service.
- `docs/adr/`: ADR kiến trúc và hạ tầng (đề xuất và đã duyệt), chỉ mục ở `docs/adr/README.md`, quyết định gộp hai nhánh ở `docs/adr/0010-unify-main-and-phase-0b.md`.
- `docs/UPGRADE_FROM_PHASE_0B.md`: quy trình nâng cấp từ `phase-0b` (backup, biến env, image pgvector/Meilisearch, rà tay hoàn tiền VietQR `payment 0005`, reindex, dọn consumer cũ, rollback).
- `docs/CORE_REPORT.md`, `docs/SECURITY_0A_REPORT.md`, `docs/WEB_INTEGRATION_REPORT.md`: nghiệm thu từng giai đoạn, kèm phần chưa xong và chỗ lệch đặc tả.
- `web/DESIGN_REPORT.md`, `web/FEATURE_MATRIX.md`: nghiệm thu UI và ma trận tuyến. `.taste-skill/`: art direction và ảnh inspect.
- `.agents/briefs/` và `docs/core-brief.md`: brief đã giao swarm agy theo giai đoạn, dùng làm mẫu khi viết brief mới.
