# Mọi mốc còn lại – hợp đồng chung (chốt 2026-11-21)

Chủ dự án yêu cầu: chạy nhiều agent song song, làm tất cả mốc trong `docs/spec.md` (tháng 4–6) và thêm hệ thống template cho site gian hàng.
Mỗi swarm có brief riêng `.agents/briefs/m-<chữ>-*.md`. Đọc `AGENTS.md`, `CLAUDE.md`, `docs/spec.md` (cột "Yêu cầu kỹ thuật và tiêu chí đo") và file này trước.

## Swarm và quyền sở hữu (không giao nhau)
| Swarm | Sở hữu |
|---|---|
| A search | `services/search/` (mới), `proto/nexusmart/search/`, phát event trong `services/product/` (outbox), `tools/catalog-gen/` (mới) |
| B chat | `services/chat/` (mới), `proto/nexusmart/chat/` |
| C assistant | `services/assistant/` (mới), `proto/nexusmart/assistant/`, `evals/assistant/` (mới) |
| D order-depth | `services/order/`, `services/payment/`, `services/shipping/`, `services/promotion/`, `proto/nexusmart/{order,payment,shipping,promotion}/` |
| E aiops | `services/aiops/` (mới, Rust), `deploy/observability/` (mới), `tools/chaos/` (mới) |
| F platform | `deploy/helm/` (mới), `deploy/k8s/` (mới), `tests/load/` (mới, k6), `tests/playwright/` (mới), `tools/backup/` (mới), `.github/workflows/` (chỉ `workflow_dispatch`) |
| G web | `web/` TRỪ phần của T: tìm kiếm, chat, trợ lý AI, trả hàng/hoàn tiền phía người mua; seller (chat, báo cáo AI, thay 3 màn mock `ho-so`, `tuong-tac`, `chat`); admin (khiếu nại, cấu hình) |
| T templates | `services/user/internal/site/`, migration mới của user cho template, `web/src/features/site/`, `web/src/app/(site)/`, `web/src/features/seller/site/`, `web/src/app/(seller)/seller/site/` |
| H qa-docs | `tests/e2e/` (file mới số 16+), `tests/e2e-web/` (file mới), `tools/seed/`, `docs/FINAL_REPORT.md`, `docs/DEMO.md`, `docs/EVAL_REPORT.md` |

Đang có một job sửa `services/user/internal/service/domains.go` và `tests/e2e/15_custom_domains_test.go`: không ai sửa hai file đó.

## File dùng chung (chỉ được THÊM, không sửa phần của người khác)
- `services/gateway/internal/router/router.go`: mỗi swarm thêm đúng một dòng mount trong nhóm route của mình, handler nằm trong file riêng `handlers_<swarm>.go`. Route mới chạy qua JWT, RBAC, rate limit sẵn có.
- `docker-compose.yml`: mỗi swarm thêm block service của mình. Cổng chỉ bind `127.0.0.1`. Chỉ 28080 và 3300 public.
- `Makefile`: chỉ thêm target mới. `make lint` và `make test` phải bao gồm service mới.
- `.env.example`: thêm biến của mình, giá trị giả.

## Cổng
search HTTP 8089 gRPC 50059; chat HTTP 8090 (WebSocket) gRPC 50060; assistant HTTP 8091 (SSE) gRPC 50061; aiops HTTP 8092; meilisearch 7700; prometheus 9090; grafana 3000→ bind 127.0.0.1:3400; loki 3100; tempo 3200.

## Event (NATS JetStream, outbox, consumer idempotent + DLQ như các service hiện có)
- Product phát: `product.created`, `product.updated`, `product.deleted`, `product.stock_changed` (payload: product_id, shop_id, version, occurred_at).
- Đã có: `order.*`, `payment.*`, `shipment.updated`. D thêm: `order.return_requested`, `order.return_approved`, `order.refunded`.

## REST (gateway, JSON snake_case, lỗi `{error:{code,message,trace_id}}` như hiện có)
- Search: `GET /api/v1/search?q=&shop_id=&category=&min_price=&max_price=&sort=relevance|price_asc|price_desc|newest&page=&page_size=` → `{items,total,facets}`; `GET /api/v1/search/suggest?q=&shop_id=` → `{suggestions}`. Có dấu và không dấu cho cùng kết quả. Site host luôn truyền `shop_id` của site.
- Chat: `GET /api/v1/chat/conversations`, `POST /api/v1/chat/conversations` `{shop_id}`, `GET /api/v1/chat/conversations/{id}/messages?before=`, WebSocket `GET /api/v1/chat/ws` (JWT qua cookie BFF hoặc ticket ngắn hạn `POST /api/v1/chat/ws-ticket`). Tin nhắn: `{id, conversation_id, sender_id, kind: text|image|product|order, body, ref_id, created_at}`.
- Assistant: `POST /api/v1/assistant/chat` (SSE, sự kiện `token`, `tool_call`, `tool_result`, `done`, `error`) body `{messages, shop_id}`; `POST /api/v1/assistant/handoff` → tạo hội thoại chat với gian hàng; seller: `POST /api/v1/seller/assistant/report`, `POST /api/v1/seller/assistant/describe-product`, `POST /api/v1/seller/assistant/suggest-reply`; `GET /api/v1/products/{id}/review-summary`.
- Trả hàng: `POST /api/v1/orders/{id}/returns` `{items, reason, photos}`, `GET /api/v1/orders/{id}/returns`; seller `POST /api/v1/seller/returns/{id}/approve|reject`; admin `GET /api/v1/admin/complaints`, `POST /api/v1/admin/complaints/{id}/resolve`.
- Template: `GET /api/v1/site-templates` (công khai, danh sách template của nền tảng), `GET /api/v1/site-templates/{id}`, seller `POST /api/v1/seller/site/apply-template` `{template_id}` (ghi vào bản nháp), sửa template qua `PUT /api/v1/seller/site/draft` sẵn có (config mở rộng).

## LLM và embedding
- Interface provider trong từng service cần dùng. `LLM_MODE=mock|anthropic` (mặc định mock, chạy được offline và trong e2e). Live: Claude API qua HTTP thư viện chuẩn, model mặc định `claude-opus-5-5` (env `LLM_MODEL`), streaming SSE, giới hạn chi phí theo ngày và cache câu trả lời lặp lại.
- Embedding: `EMBED_MODE=mock|voyage` (mock: vector băm xác định, 256 chiều). Vector lưu trong pgvector của search service (image `pgvector/pgvector`), assistant truy hồi qua gRPC search, không giữ bản vector thứ hai.
- Không key thật trong repo.

## Luật chung
- Ngôn ngữ, thư viện theo sheet Công nghệ trong spec. Ponytail: thư viện chuẩn trước, không framework mới.
- Không commit, không đổi nhánh. Không chạy `make e2e`, `make seed`, `make e2e-web` và không rebuild stack, trừ swarm H. Swarm khác kiểm tra bằng unit test, integration test với database tạm (container riêng tên `<swarm>-test-*`, xóa khi xong) và build image của chính mình.
- Web: không build vào `web/.next` (server 3111). Dùng `NEXT_DIST_DIR=.next-<swarm>` rồi xóa và khôi phục `web/tsconfig.json`, `web/next-env.d.ts`.
- Ổ đĩa còn khoảng 50 GB: xóa image tạm, container test và thư mục build của mình khi xong. Không xóa image hay cache của dự án khác.
- Mỗi swarm viết phần của mình vào `docs/reports/<swarm>.md`: đã làm, lệnh kiểm tra và đầu ra thật, tiêu chí đo của spec đạt hay chưa, phần chưa làm kèm lý do. Lỗi phát hiện ở phần của swarm khác: ghi vào `docs/reports/issues.md` (swarm đích, file:dòng, kịch bản).
