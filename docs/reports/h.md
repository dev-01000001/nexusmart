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
| G (`web`) | `web/` (search, chat, assistant, returns, seller, admin) | Đã ghi `g-web.md` (138/138 unit test PASS; chờ T sửa TS/format để build toàn `web/`) | Chờ T xong | — | `06_*`, `07_*` (đang viết) | — | — |
| T (`templates`) | `user/internal/site`, site templates `web/` | Đang chạy | Chưa | `20_site_templates_test.go` (chờ T) | `08_*` (chờ T) | Template 4 shop (chờ T) | — |

## Lịch sử vòng tích hợp

- **14:50**: Khởi tạo Swarm H, đọc toàn bộ brief A–H, T, hợp đồng `m-all-contract.md`, `docs/spec.md`, `tools/seed/seed.mjs`, `tests/e2e/*`, `tests/e2e-web/*`, xác nhận bộ override cổng host (`REDIS_PORT=26379 OTEL_HTTP_PORT=24318 GATEWAY_PORT=28080 PRODUCT_HTTP_PORT=28082 CART_HTTP_PORT=28083 ORDER_HTTP_PORT=28085`). Chưa giữ `.runs/stack.lock` để E/F dùng tự do khi cần. Hẹn kiểm tra vòng 1 sau 15 phút.
- **15:06 (Vòng 1)**: Cả 8 swarm A, B, C, D, E, F, G, T vẫn đang chạy tích cực (đã dựng xong cấu trúc `services/{search,chat,assistant,aiops}`, `tools/{catalog-gen,backup,chaos}`, `evals/assistant`, `tests/{load,playwright}`, `deploy/{helm,observability}`, `docs/adr/0001-site-template-format.md`, `.github/workflows/security.yml`). Hiện `docs/reports/` có `g-web.md` (hợp đồng component cho T) và `issues.md` (14 mục audit observability từ E); chưa có swarm nào báo xanh cuối. `.runs/stack.lock` đang được `F` giữ (chạy Playwright/k6). Đã kiểm tra sớm CI triggers (`.github/workflows/{ci,security}.yml` chỉ có `workflow_dispatch:`). Ổ đĩa còn `44G` trống. Hẹn kiểm tra vòng 2 sau 12 phút.
- **15:23 (Vòng 2)**: Swarm **G (`web`)** đã hoàn tất và ghi đầy đủ `docs/reports/g-web.md` (138/138 unit test PASS; ghi 3 lỗi biên dịch/format ở phần Swarm T vào `docs/reports/issues.md` để T xử lý). 7 swarm còn lại (A, B, C, D, E, F, T) vẫn đang chạy; tất cả service mới (`search-postgres`, `meilisearch`, `search`, `chat`, `assistant`, profile `obs` + `aiops`) đã có mặt trong `docker-compose.yml` và `Makefile`, toàn bộ cổng nội bộ ràng buộc `127.0.0.1`. Xác nhận `chat` và `assistant` tự động tạo `chat_db`/`assistant_db` khi khởi động; phát hiện trên máy host hiện tại các cổng `8090`, `8091`, `8099` đã bị tiến trình ngoài chiếm nên khi dựng stack sẽ bổ sung override `CHAT_HTTP_PORT=28090 ASSISTANT_HTTP_PORT=28091 TELEGRAM_RELAY_PORT=28099`. `.runs/stack.lock` hiện đã trống (F đã nhả). Đã khởi chạy 2 worker của Swarm H (`go-e2e` viết `tests/e2e/16..20`, `web-e2e` viết `tests/e2e-web/06..08`) song song trong khi chờ A/B/C/D/E/F/T ghi báo cáo xanh để tiến hành rebuild và chạy chuỗi gate. Ổ đĩa còn `40G` trống.
