# Báo cáo Swarm B – Chat Service

- **Phạm vi**: `services/chat/`, `proto/nexusmart/chat/`, `gen/go/nexusmart/chat/v1/`, tích hợp API Gateway (`services/gateway/internal/router/handlers_chat.go`, 1 dòng mount trong `services/gateway/internal/router/router.go`), và bổ sung block/biến cấu hình cho `chat` trong `docker-compose.yml`, `Makefile`, `.env.example`.
- **Hợp đồng áp dụng**: `.agents/briefs/m-b-chat.md`, `.agents/briefs/m-all-contract.md`, `docs/spec.md` (hàng *Chat*, UC-B14, UC-S09).

---

## 1. Phần đã làm

### 1.1 Protobuf & gRPC Contract
- `proto/nexusmart/chat/v1/chat.proto` -> sinh code Go tại `gen/go/nexusmart/chat/v1/` (`buf lint --path proto/nexusmart/chat` sạch).
- `service ChatService` gồm 10 RPC:
  - `CreateOrGetConversation`, `GetConversation`, `ListConversations`, `ListMessages`, `SendMessage`, `MarkRead`
  - `CreateWSTicket`, `ExchangeWSTicket`
  - `GetAutoReplyConfig`, `UpsertAutoReplyConfig`
- `MessageKind`: `TEXT`, `IMAGE`, `PRODUCT`, `ORDER` (trả ra REST/WS dạng chữ thường `text|image|product|order`).

### 1.2 Microservice `services/chat` (Go 1.22, `coder/websocket`, Redis Pub/Sub, PostgreSQL)
- **CSDL riêng (`chat_db`) & Migration**:
  - `services/chat/migrations/000001_init.sql` (`chat_conversations`, `chat_messages` với `seq BIGSERIAL PRIMARY KEY`, `chat_shop_auto_replies`, chỉ mục `(conversation_id, seq DESC)`, chỉ mục idempotent `(conversation_id, sender_id, client_msg_id)`).
  - `services/chat/internal/db/db.go`: `EnsureDatabase` (tự tạo `chat_db` an toàn khi gặp mã PostgreSQL `3D000` sau khi kiểm tra regex tên DB) và `Migrate` tự chạy migration nhúng theo thứ tự từ vựng.
- **Lưu trữ & Đồng bộ theo cursor (`internal/store/`)**:
  - `PGStore` (`pgxpool`) + `MemStore` cho unit test nhanh.
  - `CreateOrGetConversation` idempotent theo `(buyer_id, shop_id)`, lưu `site_host` (gian hàng của site) và hỗ trợ `initial_message` (dùng cho handoff từ trợ lý AI).
  - `CreateMessage` idempotent theo `client_msg_id`, cập nhật `last_message_*` trong cùng transaction.
  - `ListMessages` hỗ trợ phân trang ngược `before=<cursor>` và đồng bộ tin bị lỡ khi mất kết nối `after=<cursor>` (chấp nhận cả `seq` số lẫn UUID tin nhắn), trả về theo thứ tự thời gian `seq ASC` cùng `next_before`, `latest_cursor`, `has_more`.
  - `ListMissedMessages` đồng bộ tin mới trên toàn bộ hội thoại mà người dùng/gian hàng có quyền đọc (kèm kiểm tra quyền sở hữu khi cursor là UUID).
  - `MarkRead` cập nhật đơn điệu (`GREATEST`) cho `buyer_last_read_seq` / `seller_last_read_seq`, trả `unread_count` còn lại và phát sự kiện `read_receipt`.
- **Kiểm tra nội dung & ảnh chống SSRF (`internal/validator/image.go`)**:
  - Hỗ trợ 4 loại tin: `text` (kèm cảnh báo không chặn `external_transaction_warning` khi phát hiện SĐT VN hoặc link ngoài sàn theo UC-B14), `image`, `product` (bắt buộc `ref_id`), `order` (bắt buộc `ref_id`).
  - Xác thực URL ảnh: chỉ `http`/`https`, cấm userinfo, chống SSRF chặn IP loopback/private/link-local/cloud metadata/CGNAT cả ở dạng literal lẫn sau phân giải DNS trong custom `DialContext` và khi redirect (trừ `CHAT_IMAGE_ALLOWED_HOSTS`).
  - Kiểm tra loại (`.jpg/.jpeg/.png/.webp/.gif`; MIME `image/jpeg`, `image/png`, `image/webp`, `image/gif`; đọc magic bytes 512B bằng `Range` GET) và kích thước (`1..CHAT_IMAGE_MAX_BYTES`, mặc định 5 MiB). Hỗ trợ 3 chế độ `strict`, `hybrid` (mặc định), `metadata`.
- **Trả lời tự động theo cấu hình gian hàng (`internal/service/chat.go`)**:
  - Cấu hình theo `shop_id`: bật/tắt, lời chào (`greeting_message`), ngoài giờ (`offline_enabled`, `offline_message`, `work_start_hour..work_end_hour`, múi giờ mặc định `Asia/Ho_Chi_Minh` với `time/tzdata` nhúng sẵn), luật từ khóa (`keyword_rules`), thời gian chờ chống spam (`cooldown_minutes`).
  - Chỉ kích hoạt khi người mua gửi tin, chiếm slot cooldown nguyên tử (`ClaimAutoReplySlot` trên DB) để nhiều instance không bắn trùng, lưu tin với `is_auto_reply=true` và phát realtime qua Redis Pub/Sub.
- **Nhiều instance qua Redis Pub/Sub (`internal/hub/hub.go`)**:
  - 1 kết nối Redis Pub/Sub dùng chung mỗi instance trên kênh `chat:events` với vòng lặp tự kết nối lại có backoff; định tuyến O(1) theo `byUser` và `byShop`, khử trùng lặp trên từng socket, hàng đợi gửi giới hạn 64 frame/client để client chậm không làm nghẽn hub (đã ghi chú `ponytail:` ngưỡng tải và hướng nâng cấp sharding).
- **WebSocket & Ticket ngắn hạn (`internal/ws/handler.go`, `internal/ticket/ticket.go`)**:
  - Dùng `github.com/coder/websocket` (`CompressionDisabled` để giảm RAM cho 1.000+ kết nối đồng thời, `ReadLimit=64 KiB`, `OriginPatterns` an toàn cho `*.SITE_ROOT_DOMAIN` và localhost).
  - Xác thực qua `Authorization: Bearer <jwt>`, cookie BFF (`__Host-nm_at` / `nm_at`), hoặc `?ticket=<t>` (256-bit random lưu SHA-256 trên Redis TTL 60s, tiêu thụ nguyên tử 1 lần bằng `GETDEL`, kiểm tra JTI thu hồi `user:revoked_jti:<jti>`).
  - Hỗ trợ `?after=<cursor>` khi kết nối lại và các frame JSON `send`, `read`, `sync`, `ping` (`connected`, `message`, `ack`, `read_receipt`, `read_ack`, `sync_result`, `pong`, `error`).
- **Quyền truy cập 2 lớp (Gateway + Service)**:
  - Hàm `CanAccessConversation` kiểm tra chặt ở cả `services/chat` và `services/gateway`: chỉ `claims.UserID() == conv.BuyerID` hoặc `claims.ShopID == conv.ShopID` (nhân viên/chủ gian hàng đó) mới đọc/gửi/đánh dấu đã đọc được; người mua khác, gian hàng khác, và admin không thuộc hội thoại đều nhận `403 PERMISSION_DENIED`.
  - Quản lý cấu hình trả lời tự động (`/api/v1/seller/chat/auto-reply`) khóa chặt theo role `seller` + `shop_id` trong JWT.
- **Observability, Packaging & Cấu hình dùng chung**:
  - OpenTelemetry tracing, log JSON có `trace_id` (không log JWT/ticket/cookie), Prometheus RED + metric riêng (`chat_ws_connections_active`, `chat_messages_total`, `chat_pubsub_events_total`, `chat_ws_dropped_messages_total`), `/healthz`, `/readyz`, `/metrics` trên `:8090` và gRPC nội bộ `:50060`.
  - `services/chat/Dockerfile` multi-stage (`golang:1.22-alpine` -> `alpine:3.20`), chạy bằng `USER appuser` (uid 10001).
  - `services/gateway/internal/router/handlers_chat.go` + 1 dòng `mountChatRoutes(r, deps)` trong `services/gateway/internal/router/router.go`: REST (`/api/v1/chat/conversations`, `POST /api/v1/chat/conversations`, `GET/POST /api/v1/chat/conversations/{id}/messages`, `POST /api/v1/chat/conversations/{id}/read`, `POST /api/v1/chat/ws-ticket`, `/api/v1/seller/chat/conversations`, `GET/PUT /api/v1/seller/chat/auto-reply`) và proxy WebSocket `GET /api/v1/chat/ws` (xóa read/write deadline, loại bỏ `?ticket=` và `Cookie` trước khi chuyển tiếp `Authorization: Bearer` lên upstream).
  - Cập nhật `docker-compose.yml` (cổng HTTP `127.0.0.1:8090:8090`, gRPC `50060` nội bộ), `Makefile` (`GO_MODULES` gồm `services/chat`), `.env.example`.

---

## 2. Lệnh kiểm tra và đầu ra thật

### 2.1 Proto lint, format, `golangci-lint`, unit test (`-race`) và `docker compose config`
```bash
buf lint --path proto/nexusmart/chat
gofmt -l services/chat services/gateway
(cd services/chat && golangci-lint run -c ../../.golangci.yml ./...)
(cd services/gateway && golangci-lint run -c ../../.golangci.yml ./...)
(cd services/chat && go test -race -count=1 ./...)
(cd services/gateway && go test -race -count=1 ./...)
docker compose config -q
```
Đầu ra thật:
```text
=== Final buf lint ===
=== Final gofmt & golangci-lint (services/chat, services/gateway) ===
=== Final go test -race (services/chat, services/gateway) ===
ok  	github.com/nexusmart/core/services/chat/internal/db	1.011s
ok  	github.com/nexusmart/core/services/chat/internal/grpcserver	1.016s
ok  	github.com/nexusmart/core/services/chat/internal/hub	1.165s
ok  	github.com/nexusmart/core/services/chat/internal/integration	1.019s
ok  	github.com/nexusmart/core/services/chat/internal/service	1.021s
ok  	github.com/nexusmart/core/services/chat/internal/store	1.016s
ok  	github.com/nexusmart/core/services/chat/internal/ticket	1.020s
ok  	github.com/nexusmart/core/services/chat/internal/validator	1.020s
ok  	github.com/nexusmart/core/services/chat/internal/ws	1.030s
ok  	github.com/nexusmart/core/services/gateway/internal/httpx	1.011s
ok  	github.com/nexusmart/core/services/gateway/internal/middleware	1.018s
ok  	github.com/nexusmart/core/services/gateway/internal/proxy	1.015s
ok  	github.com/nexusmart/core/services/gateway/internal/router	1.130s
docker compose config OK
```
*(Bao gồm `TestChatRESTEndpointsAndObjectPermissions` và `TestChatWebSocketAuthAnd101Tunnel` kiểm chứng tunnel HTTP `101 Switching Protocols` 2 chiều qua toàn bộ middleware của gateway với Bearer JWT, cookie `__Host-nm_at`/`nm_at` và `?ticket=`.)*

### 2.2 Integration test với Postgres 16 + Redis 7 tạm và 2 instance trao đổi tin qua Redis Pub/Sub
Chạy trên 2 container tạm `b-chat-test-postgres` (`postgres:16-alpine`) và `b-chat-test-redis` (`redis:7-alpine`), xóa sạch ngay sau khi xong:
```bash
CHAT_TEST_DATABASE_URL="postgres://nexusmart:nexusmart_dev_password@127.0.0.1:${PG_PORT}/chat_db?sslmode=disable" \
CHAT_TEST_REDIS_ADDR="127.0.0.1:${RD_PORT}" \
  go test -race -v -count=1 ./internal/integration/...
```
Đầu ra thật:
```text
Temp containers: postgres=127.0.0.1:32778 redis=127.0.0.1:32779
Ready after 4 checks
=== Integration & 2-Instance Redis Pub/Sub Test ===
=== RUN   TestChatIntegrationPostgresRedisAndTwoInstances
--- PASS: TestChatIntegrationPostgresRedisAndTwoInstances (3.94s)
PASS
ok  	github.com/nexusmart/core/services/chat/internal/integration	4.959s
```
Kịch bản đã kiểm chứng thật trên DB + Redis:
- Tự tạo `chat_db` (`EnsureDatabase`) & migrate schema.
- 4 loại tin (`text` kèm cảnh báo SĐT ngoài sàn, `image` kiểm tra PNG thật / từ chối ảnh quá 5 MiB / từ chối `text/html` giả `.png` / chặn SSRF `169.254.169.254` / chặn `.svg`, `product` card, `order` code), idempotency theo `client_msg_id`.
- Trả lời tự động theo cấu hình gian hàng: luật từ khóa, ngoài giờ làm việc, lời chào trong giờ làm việc, cooldown nguyên tử.
- Phân trang `before`, đồng bộ `after` bằng cả số `seq` và UUID tin nhắn, đồng bộ lại qua WebSocket `?after=`.
- Kiểm soát quyền: người mua khác và người bán gian hàng khác bị từ chối `PermissionDenied` trên đọc/gửi/cấu hình auto-reply.
- **2 instance sống (`chat-inst-a`, `chat-inst-b`) dùng chung Redis Pub/Sub**: người mua kết nối `chat-inst-a` bằng single-use ticket (replay trên `chat-inst-b` bị từ chối `Unauthenticated`), người bán kết nối `chat-inst-b`; gửi tin nhắn 2 chiều và `read_receipt` truyền qua lại thời gian thực giữa hai instance.

### 2.3 Bài thử tải 1.000 kết nối WebSocket đồng thời trên 1 instance (`services/chat/cmd/loadtest`)
Chạy 1 binary `services/chat` thật kết nối Postgres 16 + Redis 7 tạm (tài nguyên container tạm và binary tạm đã xóa sạch):
```bash
/tmp/b-chat-loadtest \
  -url="ws://127.0.0.1:${CHAT_HTTP_PORT}/ws" \
  -grpc-addr="127.0.0.1:${CHAT_GRPC_PORT}" \
  -conns=1000 -messages=100 -hold=5s -concurrency=100 \
  -jwt-secret="bench-chat-jwt-secret"
```
Đầu ra thật:
```text
Picked free ports: http=50717 grpc=48941 (postgres=32781 redis=32782)
Healthz: {"service":"chat","status":"ok"}
Readyz : {"service":"chat","status":"ready"}
Server RSS before loadtest: 21740 KB (~21 MB)
=== Chat WebSocket Load Test Report ===
target_url        : ws://127.0.0.1:50717/ws
delivery_mode     : chat_message_delivery
target_conns      : 1000
active_peak       : 1000
active_after_hold : 1000 (hold=5s)
ramp_duration     : 133ms
connect_latency   : p50=9.785ms p95=34.73ms p99=62.799ms max=65.143ms
messages          : sent=100 delivered=100 errors=0
delivery_latency  : p50=5.376361s p95=11.27031s p99=11.76416s max=11.831525s
connect_errors    : 0
unexpected_drops  : 0
Server RSS after 1,000-conn loadtest: 73504 KB (~71 MB)
Prometheus chat metrics:
chat_messages_total{auto_reply="false",kind="text",sender_role="buyer"} 100
chat_pubsub_events_total{direction="publish",type="message"} 100
chat_pubsub_events_total{direction="receive",type="message"} 100
chat_ws_connections_active 0
chat_ws_dropped_messages_total 0
```
*Ghi chú về `delivery_latency`*: 100 tin nhắn trong bài thử được bắn dồn cùng lúc từ **một** kết nối người mua (đều gắn mốc `t=0`) và được server xử lý tuần tự trên cùng socket để bảo toàn thứ tự FIFO, mỗi tin ghi giao dịch Postgres (`fsync`) + phát Redis Pub/Sub trong lúc 8 swarm khác chạy song song trên cùng ổ đĩa (~118 ms/tin liên tiếp; 100/100 giao nhận thành công, 0 lỗi, 0 frame bị drop).

### 2.4 Build Docker image đa giai đoạn (non-root)
```bash
docker build -f services/chat/Dockerfile -t b-chat-test-img .
docker inspect --format '{{.Config.User}}' b-chat-test-img
docker rmi -f b-chat-test-img
```
Đầu ra thật:
```text
Docker image built: user=appuser size_bytes=32335573 (~30 MB)
Temp image b-chat-test-img removed cleanly
```

---

## 3. Đối chiếu tiêu chí đo trong `docs/spec.md` và `.agents/briefs/m-b-chat.md`

| Tiêu chí | Trạng thái | Bằng chứng |
|---|---|---|
| Go + `coder/websocket` + Redis pub/sub + Postgres lưu tin | **Đạt** | `services/chat`, `go.mod`, `internal/ws`, `internal/hub`, `internal/store/pg_store.go` |
| Hội thoại người mua–gian hàng (gian hàng của site) | **Đạt** | `chat_conversations(buyer_id, shop_id, site_host)`, xác thực shop ACTIVE qua `UserService.GetShop` khi bật `USER_GRPC_ADDR` |
| Tin `text`, `image` (URL đã upload, kiểm tra loại + kích thước + chống SSRF), `product` card, mã `order` | **Đạt** | Unit test `validator`, `service` + integration test trên Postgres/HTTP server thật |
| Đồng bộ lại khi mất kết nối theo cursor (`before`/`after`, REST & WS) | **Đạt** | Unit + integration test với cả cursor `seq` và UUID tin nhắn |
| Trả lời tự động theo cấu hình gian hàng | **Đạt** | Greeting, ngoài giờ (theo múi giờ), luật từ khóa, cooldown nguyên tử |
| Đánh dấu đã đọc & read receipt thời gian thực | **Đạt** | `MarkRead` đơn điệu `GREATEST`, `unread_count`, sự kiện `read_receipt` qua Redis Pub/Sub |
| Quyền chặt 2 lớp ở gateway và service (chỉ người mua trong hội thoại & nhân viên gian hàng đó) | **Đạt** | `router_chat_test.go` + `service/chat_test.go` + `grpcserver/server_test.go` + `chat_integration_test.go` |
| 2 instance nhận tin của nhau qua Redis Pub/Sub | **Đạt** | `TestChatIntegrationPostgresRedisAndTwoInstances` PASS (`3.94s`, `-race`) |
| Mục tiêu **1.000 kết nối đồng thời trên 1 instance** | **Đạt** | `active_peak=1000`, `active_after_hold=1000` (5s), `ramp=133ms`, `connect p95=34.73ms`, `errors=0`, `drops=0`, RSS ~71 MB |
| Lint sạch, test xanh, Docker multi-stage non-root | **Đạt** | `buf lint`, `gofmt`, `golangci-lint` (0 lỗi), `go test -race` xanh, image `appuser` ~30 MB |

---

## 4. Phần chưa làm / Lưu ý tích hợp cho Swarm H (QA)

1. **Chưa chạy `make e2e` và chưa rebuild stack compose chung**: theo luật chung trong `m-all-contract.md`, chỉ Swarm H mới rebuild stack và chạy `make e2e`.
2. **Khởi tạo `chat_db`**: `infra/postgres/init-dbs.sql` không nằm trong file sở hữu của Swarm B; tuy nhiên `services/chat` đã có `db.EnsureDatabase` tự tạo `chat_db` khi khởi động nếu chưa tồn tại (đã kiểm chứng thật trong integration test với container Postgres sạch chỉ có DB mặc định `postgres`), nên sẽ tự hoạt động trên cả volume Postgres cũ lẫn mới.
3. **Thông báo đẩy khi người nhận offline (UC-B14 ngoại lệ)**: `services/chat` lưu đầy đủ tin nhắn + `unread_count` để người nhận đồng bộ khi online lại; hiện chưa phát sự kiện NATS sang `services/notification` vì nằm ngoài danh sách sự kiện chốt trong `m-all-contract.md` (có thể bổ sung sự kiện `chat.message_created` sau khi chốt hợp đồng với `notification`).
4. **Cảnh báo cấu trúc đã ghi `ponytail:`**:
   - `internal/hub/hub.go`: dùng 1 kênh Redis Pub/Sub `chat:events` cho mọi instance (ngưỡng ~5.000–10.000 sự kiện/giây toàn cụm; nâng cấp: shard theo `hash(shop_id)%N` hoặc Redis Sharded Pub/Sub).
   - `internal/db/db.go`: migration DDL idempotent chạy theo thứ tự từ vựng không có bảng `schema_migrations` riêng (chỉ phù hợp migration additive; nâng cấp: thêm bảng phiên bản hoặc `goose`).
