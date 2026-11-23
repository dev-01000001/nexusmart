# Worker `plt` – nền tảng xuyên suốt và các mảng chưa có service

Luật chung: `.agents/briefs/audit/rules.md`. Báo cáo: `docs/audit/plt.md`, mã `PLT-B<n>`, `PLT-D<n>`, `PLT-F<n>`.

## UC phụ trách
- Trợ lý AI: UC-AI01 … UC-AI14. Chat: UC-B14, UC-S09. Phân tích: UC-S06, UC-A06.
- Giám sát, AIOps, DevOps: UC-OPS01 … UC-OPS09.
- Phần gateway và bảo mật xuyên suốt của UC-B02 (rate limit theo IP và theo tài khoản, RBAC ở gateway).

## Đối chiếu Shopify/sàn
- Kênh bán: online store, POS, app, mạng xã hội, Google, marketplace bên ngoài, nhập hàng của người bán khác.
- POS: bán tại quầy, thanh toán trực tiếp, quản lý ca và tiền mặt, trả hàng tại cửa hàng, mua online nhận tại quầy.
- Bán quốc tế (Markets): giá và catalog theo thị trường, tên miền địa phương, thuế nhập khẩu, ngôn ngữ.
- B2B: hồ sơ công ty, bảng giá riêng, điều khoản thanh toán, số lượng tối thiểu, báo giá.
- Phân tích: dashboard, báo cáo bán hàng, khách hàng, tồn kho, hiệu quả marketing, xem trực tiếp.
- Tự động hoá và AI: workflow theo sự kiện, hẹn giờ phát hành hoặc A/B test, trợ lý cho người bán. Trợ lý AI làm kênh bán (checkout qua trợ lý).
- Nền tảng cho nhà phát triển: app store, Admin API và Storefront API, webhook ra ngoài, functions, extension, theme store.
- Thông báo và tuân thủ (phần nền tảng): cookie và quyền riêng tư, chống bot, nhật ký hoạt động.
- Chat người mua với người bán (sàn).

## Phải kiểm
- AI assistant, Search service (hybrid/vector, pgvector), Chat service (WebSocket + Redis pub/sub), AIOps engine (Rust), analytics: có code/service/route web nào chưa (kể cả trang web chỉ dùng mock: `web/src/mock`, `web/src/data`). Spec cần 11 microservice; liệt kê service hiện có và service thiếu. Mục nào thiếu: ghi rõ cần ADR gì (AGENTS.md luật 11, `docs/adr/README.md` nhóm B) trước khi code.
- Gateway: rate limit theo IP và theo tài khoản có chạy không (đọc `services/gateway/internal/middleware/ratelimit.go`; thử nhẹ không quá 20 request bằng header `X-Forwarded-For` giả để xem có né được không — đây là rủi ro đã biết), RBAC route seller/admin ở gateway, lỗi `codes.Internal` có lộ thông điệp nội bộ qua gateway không, CORS, header bảo mật, kích thước body tối đa.
- Quan sát: log JSON có `trace_id` ở mọi service (`docker compose logs`), trace xuyên gRPC và NATS (cùng trace_id ở order, product, payment cho một lần checkout — tự đặt 1 đơn bằng buyer riêng), che PII/secret trong log (email, SĐT, token, chữ ký), metric RED (`/metrics` ở cổng 4808x, cổng prometheus otel 48889), dashboard, cảnh báo Telegram, SLO dạng code.
- NATS: stream `NEXUSMART` và `NEXUSMART_DLQ` (48222/jsz), consumer, outbox (`outbox_events` của order/payment/shipping có bản ghi kẹt không), `dead_letter_events`.
- DevOps: Kubernetes/Helm/HPA, CI (`.github/workflows`, chỉ `workflow_dispatch` theo AGENTS.md), quét lỗ hổng dependency/image, sao lưu và khôi phục, k6, Playwright E2E, contract test gRPC; coverage Order/Payment/Promotion so với mục tiêu 60% (chạy `go test -cover ./...` trong `services/order`, `services/promotion`; payment: ghi lại nếu không đo được bằng công cụ sẵn có, không cài thêm).
- Docker image nhiều giai đoạn, user không phải root (`docker compose exec <svc> id`), secret không nằm trong repo (`rg` các khoá thật).
