# Swarm H – QA tích hợp, đánh giá định lượng, báo cáo, demo (Nhật ký tiến độ)

- **Bắt đầu phiên:** `2026-11-21 14:47 +07:00`
- **Hợp đồng tham chiếu:** `.agents/briefs/m-h-qa-docs.md`, `.agents/briefs/m-all-contract.md`
- **Nền hiện tại (trước vòng tích hợp mới):** Phase 5 (`f3dc328`, checkpoint `a73a38f`), stack 15 container đang chạy (`gateway` `:28080`, `web` `:3300`, cổng nội bộ `127.0.0.1`, 0 cổng `5005x` public).

## Trạng thái theo dõi 8 swarm song song

| Swarm | Phạm vi | Báo cáo (`docs/reports/`) | Rebuild stack | E2E Go (`16_*`+) | E2E Web (`06_*`+) | Seed | Ghi chú |
|---|---|---|---|---|---|---|---|
| A (`search`) | `services/search`, `product` outbox, `tools/catalog-gen` | Đang chạy | Chưa | `16_search_test.go` (chờ A) | `06_*` (chờ G+A) | 20k catalog (chờ A) | — |
| B (`chat`) | `services/chat` (WS + Redis pub/sub) | Đang chạy | Chưa | `17_chat_test.go` (chờ B) | `06_*` (chờ G+B) | Hội thoại mẫu (chờ B) | — |
| C (`assistant`) | `services/assistant`, `evals/assistant` | Đang chạy | Chưa | `18_assistant_test.go` (chờ C) | `06_*` (chờ G+C) | — | — |
| D (`order-depth`) | `order`, `payment`, `shipping`, `promotion` | Đang chạy | Chưa | `19_returns_order_depth_test.go` (chờ D) | `07_*` (chờ G+D) | Đơn trả hàng mẫu (chờ D) | — |
| E (`aiops`) | `services/aiops`, `deploy/observability`, `tools/chaos` | Đang chạy | Chưa | Kiểm tra health/metrics (chờ E) | — | — | Phối hợp `.runs/stack.lock` |
| F (`platform`) | `deploy/helm`, `tests/load`, `tests/playwright`, `tools/backup`, CI | Đang chạy | Chưa | Kiểm tra CI `workflow_dispatch` | — | — | Phối hợp `.runs/stack.lock` |
| G (`web`) | `web/` (search, chat, assistant, returns, seller, admin) | Đang chạy | Chưa | — | `06_*`, `07_*` (chờ G) | — | — |
| T (`templates`) | `user/internal/site`, site templates `web/` | Đang chạy | Chưa | `20_site_templates_test.go` (chờ T) | `08_*` (chờ T) | Template 4 shop (chờ T) | — |

## Lịch sử vòng tích hợp

- **14:50**: Khởi tạo Swarm H, đọc toàn bộ brief A–H, T, hợp đồng `m-all-contract.md`, `docs/spec.md`, `tools/seed/seed.mjs`, `tests/e2e/*`, `tests/e2e-web/*`, xác nhận bộ override cổng host (`REDIS_PORT=26379 OTEL_HTTP_PORT=24318 GATEWAY_PORT=28080 PRODUCT_HTTP_PORT=28082 CART_HTTP_PORT=28083 ORDER_HTTP_PORT=28085`). Chưa giữ `.runs/stack.lock` để E/F dùng tự do khi cần. Hẹn kiểm tra vòng 1 sau 15 phút.
