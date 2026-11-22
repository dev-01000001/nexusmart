# ADR-0001: API Gateway tự viết bằng `chi` — giữ nguyên, thêm edge gateway hay thay hẳn

- **Trạng thái**: Đề xuất
- **Ngày**: 2026-11-22

## Bối cảnh

`services/gateway` (Go + `chi/v5`, tổng 4.989 dòng code và 753 dòng unit test) là cổng REST `/api/v1/*` duy nhất trước 8 microservice gRPC nội bộ:

- `services/gateway/cmd/gateway/main.go` (161 dòng): nạp config, 8 gRPC client, Redis, OTel, ops `/healthz`/`/readyz`/`/metrics`.
- `services/gateway/internal/router/router.go` (275 dòng) và 8 file `handlers_*.go` (3.547 dòng; lớn nhất `handlers_user.go` 789, `handlers_promotion.go` 650, `handlers_order.go` 568): map REST JSON <-> Protobuf, chuẩn hoá error envelope + `trace_id` (`internal/httpx/response.go`, 198 dòng), kiểm ownership qua `Payment.GetPayment` trước khi mở SSE.
- `services/gateway/internal/middleware/auth.go` (116 dòng): xác minh JWT HS256, kiểm Redis `user:revoked_jti:<jti>` (fail-closed `503`), `RequireRoles`, `RequireSellerShop`.
- `services/gateway/internal/middleware/ratelimit.go` (193 dòng): Redis Lua sliding-window theo IP/user/flash-sale (fail-open). Lưu ý `ClientIP()` tin `X-Forwarded-For` trực tiếp nếu không có edge proxy tin cậy phía trước.
- `services/gateway/internal/proxy/proxy.go` (147 dòng): `NewWebhookProxy` (giữ nguyên raw body, `X-Signature`, `X-Timestamp`, `traceparent`) và `NewSSEProxy` (tắt write deadline, flush tức thì).
- `services/gateway/internal/clients/clients.go` (137 dòng): 8 gRPC client, chỉ chuyển tiếp `Authorization: Bearer`.
- **Kiểm thử phủ**: unit test tại `httpx/response_test.go`, `middleware/middleware_test.go`, `proxy/proxy_test.go`, `router/router_test.go`; E2E tại `tests/e2e/01_health_test.go`..`08_ledger_test.go` (cả 8 bài đi qua gateway, phủ `X-Trace-Id`, REST->gRPC, RBAC `403`, proxy webhook payment/shipping; riêng 1.000 request đồng thời ở `06_flash_sale_test.go` gọi thẳng gRPC để không đụng rate limit IP). Chưa có E2E tự động cho SSE và HTTP `429`.

Kế hoạch nối frontend (`.agents/briefs/sites-0b-web-gateway.md`) dùng Next.js làm BFF cùng origin (cookie `HttpOnly`, CSRF) gọi vào `services/gateway`. **Nơi triển khai chưa chốt** (Kubernetes tự host hay cloud), ảnh hưởng trực tiếp tới việc chọn edge gateway.

## Ứng viên

1. **Giữ nguyên `services/gateway`**: tiếp tục đảm nhiệm cả REST-to-gRPC lẫn cổng ngoài.
2. **Giữ `services/gateway` làm API facade/BFF nội bộ + đặt gateway OSS phía trước ở tầng edge**: edge lo TLS, L7 routing (`web`/`api`/`webhooks`), bảo vệ DDoS/WAF, ghi đè `X-Forwarded-For` tin cậy.
3. **Thay hẳn `services/gateway` bằng gateway OSS**:
   - **Envoy Gateway** (`envoyproxy/gateway`): Apache-2.0; bảo trì tích cực; bản mới nhất `v1.10.0` (2026-11-05). Triển khai Kubernetes Gateway API, có `grpcJSONTranscoder`, `ext_authz`/`ext_proc` (viết bằng Go hoặc Rust) và Proxy-Wasm (Rust). Thiết kế gắn chặt Kubernetes; kết hợp `grpcJSONTranscoder` + `ext_authz` dễ vướng thứ tự filter/rewrite path.
   - **Traefik Proxy** (`traefik/traefik`): MIT (bản OSS; Hub/Enterprise thương mại); bảo trì tích cực; bản mới nhất `v3.7.19` (2026-11-13). Viết bằng Go, chạy được cả Docker/VM lẫn Kubernetes Gateway API, route gRPC/HTTP2/HTTP3 và `GrpcWeb`, plugin Go/Yaegi. **Không có** HTTP/JSON-to-gRPC transcoding sẵn.
   - **Apache APISIX** (`apache/apisix`): Apache-2.0; bảo trì tích cực; bản mới nhất `v3.21.0` (2026-11-20). Nền OpenResty/NGINX + etcd (hoặc standalone YAML), có plugin `grpc-transcode` (nạp `.proto`) và `ext-plugin` qua IPC/FlatBuffers (`apisix-go-plugin-runner` chính thức cho Go; Rust chỉ tự cài theo `ext-plugin-proto`).

## Tiêu chí

| Tiêu chí | Giữ nguyên | Facade hiện có + Edge OSS | Thay hẳn bằng OSS |
|---|---|---|---|
| Độ phức tạp vận hành | Thấp (1 binary Go) | Trung bình (thêm 1 tầng edge chuẩn) | Cao (etcd/K8s CRD + proto descriptor + ext_authz) |
| Rủi ro chuyển đổi | Không | Thấp (không đổi hợp đồng REST) | Rất cao (viết lại 3.547 dòng mapping + SSE check + error envelope) |
| Khớp gRPC + Go/Rust | Rất tốt (gRPC client sinh từ `proto/`) | Rất tốt (edge nói HTTP/2 -> gateway -> gRPC Go/Rust) | Trung bình (Traefik thiếu transcode; Envoy/APISIX khó giữ JSON custom) |
| Chi phí vận hành | Thấp | Thấp–trung bình | Trung bình–cao (vận hành + bảo trì plugin ngoài) |
| Bảo mật | Thiếu TLS/WAF/sanitise `X-Forwarded-For` ở biên | Tốt (edge chấm dứt TLS, lọc biên; gateway giữ JTI fail-closed + RBAC) | Tốt nếu cấu hình chuẩn, nhưng dễ lệch RBAC khi tách `ext_authz` |

## Quyết định

- **Đề xuất của coder**: **Giữ `services/gateway` làm lớp REST-to-gRPC facade (cùng Next.js BFF phía web), và đặt gateway OSS phía trước ở tầng edge khi triển khai ngoài local**. Không thay hẳn `services/gateway`: phần lớn mã nguồn là ánh xạ hợp đồng REST tuỳ biến (`error.trace_id`, pagination, kiểm ownership SSE, thu hồi JTI fail-closed), không thể thay sạch bằng transcoding thuần mà không tăng độ phức tạp.
- **Lựa chọn edge phụ thuộc ADR nơi triển khai (chưa chốt)**:
  - Nếu chốt **Kubernetes**: ưu tiên **Envoy Gateway** (Gateway API chuẩn CNCF, Apache-2.0) hoặc **Traefik** nếu ưu tiên cấu hình gọn.
  - Nếu chốt **VM / Docker Compose / Cloud managed LB**: ưu tiên **Traefik** (nhẹ, không cần etcd/K8s) hoặc LB có sẵn của cloud.
- **Điều kiện kích hoạt để thay hẳn `services/gateway`**: khi có >15–20 service, toàn bộ `.proto` chuẩn hoá `google.api.http`, logic ownership SSE chuyển vào `services/payment`, và chấp nhận bỏ envelope REST tự định nghĩa.
- **Chủ dự án quyết**: (trống)

## Hệ quả

- **Giữ nguyên**: gọn ở dev/staging nội bộ, nhưng lên production phải tự lo TLS, bảo vệ Slowloris/DDoS, và sửa `ClientIP()` không tin `X-Forwarded-For` tuỳ tiện.
- **Giữ facade + thêm edge OSS (đề xuất)**: giữ nguyên 8/8 E2E và hợp đồng `docs/contracts.md`; tách rõ trách nhiệm mạng biên (TLS, routing, WAF) khỏi logic ứng dụng; đánh đổi thêm 1 hop HTTP nội bộ và phải cấu hình không buffer cho `/api/v1/payments/{id}/events` (SSE) và giữ raw body cho `/webhooks/*`.
- **Thay hẳn bằng OSS**: giảm code Go trong `handlers_*.go`, nhưng phải gánh vận hành APISIX/etcd hoặc Envoy Gateway + `ext_authz` kiểm Redis JTI, tốn công migration và rủi ro phá tương thích REST.

## Ngày kiểm tra trạng thái ứng viên

- **Ngày kiểm tra**: 2026-11-22
- **Nguồn**:
  - Envoy Gateway: https://github.com/envoyproxy/gateway, https://github.com/envoyproxy/gateway/releases, https://gateway.envoyproxy.io/
  - Traefik Proxy: https://github.com/traefik/traefik, https://github.com/traefik/traefik/releases
  - Apache APISIX: https://github.com/apache/apisix, https://apisix.apache.org/downloads/, https://github.com/apache/apisix-go-plugin-runner
