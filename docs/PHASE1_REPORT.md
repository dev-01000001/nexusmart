# Giai đoạn 1 – Kênh bán (Sales Channel), Vá tin cậy còn sót & Độ ổn định kiểm thử: Báo cáo nghiệm thu

- **Ngày nghiệm thu:** `2026-11-20`
- **Nhánh git:** `phase-0b` (commit nền `d399565`; **0 commit mới**, không push, không đổi nhánh)
- **Đặc tả:** `.agents/briefs/sites-1-sales-channel.md`
- **Xác minh độc lập:** `verifier` (chỉ đọc mã nguồn, chạy độc lập toàn bộ gate và ghi báo cáo này; không chỉnh sửa mã nguồn hay test)

---

## 1. Tóm tắt các hạng mục đã thực hiện

### A. Kênh bán (`sales_channel` + `storefront_shop_id`)

1. **A1 – Proto, DB Migration, Bất biến & Tương thích ngược:**
   - **Protobuf (`proto/nexusmart/{order,payment,promotion,cart}/v1/*.proto` + `gen/go/**`):**
     - Quy ước thống nhất: `sales_channel` kiểu `string` (`""` mặc định thành `"MARKETPLACE"` trên RPC ghi/kiểm tra và không lọc trên RPC đọc/tổng hợp/đối soát; giá trị hợp lệ là `"MARKETPLACE"` hoặc `"STOREFRONT"`, giá trị lạ trả `INVALID_ARGUMENT`); `storefront_shop_id` kiểu `string` (bắt buộc không rỗng và khớp shop khi `"STOREFRONT"`, bắt buộc rỗng khi `"MARKETPLACE"`).
     - `order.proto`: thêm `sales_channel`, `storefront_shop_id` trên `Order`, `CreateCheckoutRequest`, `CreateCheckoutResponse`; thêm bộ lọc `ListOrdersRequest.sales_channel`. Không một RPC cập nhật trạng thái nào (`CancelOrder`, `SellerConfirmOrder`, `SellerShipOrder`, `BuyerConfirmReceived`, `RequestReturn`, `MarkPaymentResult`) nhận hai trường này.
     - `payment.proto`: thêm `sales_channel`, `storefront_shop_id` trên `CreatePaymentRequest`, `Payment`, `ShopAllocation`, `ReconciliationItem`; thêm bộ lọc `sales_channel` trên `GetLedgerSummaryRequest`/`Response` và `RunReconciliationRequest`/`Response`.
     - `promotion.proto`: thêm `sales_channel`, `storefront_shop_id` trên `ListAvailableVouchersRequest`, `PreviewDiscountsRequest`, `ReservePromotionRequest`.
     - `cart.proto`: thêm `shop_id` tùy chọn trên `GetCartRequest` và `ClearItemsRequest`.
   - **SQL Migration cộng thêm & tương thích ngược:**
     - `services/order/migrations/0002_sales_channel.sql`: thêm `sales_channel TEXT NOT NULL DEFAULT 'MARKETPLACE'` và `storefront_shop_id TEXT NOT NULL DEFAULT ''` kèm `CHECK` ràng buộc nhất quán trên cả `checkout_groups` (đơn cha) và `orders` (đơn con), cùng trigger `BEFORE UPDATE` (`trg_checkout_groups_sales_channel_immutable`, `trg_orders_sales_channel_immutable`) ném lỗi nếu cố sửa sau khi tạo.
     - `services/payment/migrations/0002_sales_channel.sql`: thêm `sales_channel` + `storefront_shop_id` + `CHECK` + trigger `BEFORE UPDATE` (`trg_payments_sales_channel_immutable`, `trg_payment_allocations_sales_channel_immutable`) trên `payments` và `payment_shop_allocations`; thêm `sales_channel`, `storefront_shop_id`, `metadata JSONB` trên bảng chỉ-thêm `ledger_transactions` và `ledger_entries` (vốn đã có trigger `trg_ledger_*_immutable`); thêm cột kênh trên `reconciliation_runs` và `reconciliation_items`.
     - Toàn bộ bản ghi cũ trước Giai đoạn 1 tự động đọc ra `MARKETPLACE` / `""` trên DB thật (`order_db`: 186 đơn `MARKETPLACE`, `payment_db`: 189 phân bổ `MARKETPLACE`).
     - Payment retry (`supersede_previous`), hoàn tiền (`CreateRefund` đồng bộ & bất đồng bộ `order.cancelled`), và giải ngân ký quỹ (`ReleaseEscrow`) đều giữ nguyên kênh gốc của giao dịch và gắn `sales_channel`/`storefront_shop_id` vào metadata sổ cái.

2. **A2 – Quy tắc đơn `STOREFRONT` (kiểm ở `order` và `promotion`, không chỉ ở gateway/web):**
   - Trong `services/order/internal/service/checkout.go`:
     - Chuẩn hóa `sales_channel` (rỗng → `MARKETPLACE`, giá trị lạ → `INVALID_ARGUMENT`), chặn `STOREFRONT` thiếu `storefront_shop_id` hoặc `MARKETPLACE` có `storefront_shop_id`.
     - Với `STOREFRONT`: kiểm tra gian hàng `ACTIVE` qua `UserService.GetShop` **trước** khi giữ tồn; kiểm tra mọi dòng hàng đều thuộc `storefront_shop_id` **trước** `ReserveStock` (sai shop → `INVALID_ARGUMENT` với `STOREFRONT_SHOP_MISMATCH`, tồn kho không đổi `0` biến động).
     - Chặn sớm `coins_to_use > 0`, `platform_voucher_id`, `freeship_voucher_id`, và `shop_voucher_ids` khác shop, đồng thời truyền `sales_channel` + `storefront_shop_id` xuống `PromotionService.ReservePromotion` (nếu voucher sàn bị nhét vào `shop_voucher_ids`, `promotion` từ chối và `order` hoàn tồn qua `ReleaseStock`).
   - Trong `services/promotion/internal/service/promotion.go` (**0 migration DB mới**, tái sử dụng `scope` + `shop_id` theo `review-opus.md`):
     - `ListAvailableVouchers` trên `STOREFRONT` chỉ trả voucher `SHOP` của đúng `storefront_shop_id`.
     - `PreviewDiscounts` và `ReservePromotion` trên `STOREFRONT` từ chối rõ ràng với `INVALID_ARGUMENT`: voucher `PLATFORM`/`FREESHIP` (kể cả khi nhét vào `shop_voucher_ids` → `STOREFRONT_VOUCHER_NOT_ALLOWED`), voucher `SHOP` của gian hàng khác (`STOREFRONT_VOUCHER_SHOP_MISMATCH`), xu sàn (`STOREFRONT_COINS_NOT_ALLOWED`), dòng hàng hoặc suất flash sale khác shop (`STOREFRONT_SHOP_MISMATCH`, `STOREFRONT_FLASH_SALE_SHOP_MISMATCH`).

3. **A3 – Giỏ hàng (giữ nguyên 1 khóa Redis `cart:<user_id>`):**
   - `services/cart/internal/server/server.go`: `GetCart(shop_id)` lọc nhóm/tổng theo `shop_id` khi đọc mà không sửa Redis; `ClearItems(sku_ids, shop_id)` chỉ xóa SKU thuộc đúng `shop_id` (giao với `sku_ids` nếu có).
   - `services/order/internal/service/checkout.go`: checkout `STOREFRONT` truyền `shop_id=storefront_shop_id` khi đọc giỏ và khi xóa giỏ sau đặt đơn, giữ nguyên hàng của các gian hàng khác trong giỏ.
   - Gateway (`GET/DELETE /api/v1/cart?shop_id=`) và BFF (`GET/DELETE /api/cart?shop_id=`) chuyển tiếp tham số lọc.

4. **A4 – Gateway + BFF Checkout:**
   - Gateway (`services/gateway/internal/router/handlers_order.go`, `sales_channel.go`): nhận và validate `sales_channel` + `storefront_shop_id` trên `POST /api/v1/checkout` (`400 INVALID_ARGUMENT` nếu sai) rồi chuyển xuống `OrderService.CreateCheckout`.
   - BFF (`web/src/app/api/checkout/route.ts` + `web/src/features/commerce/checkoutPayload.ts`): luôn loại bỏ `sales_channel`/`storefront_shop_id` từ body trình duyệt và khóa cứng `sales_channel: "MARKETPLACE"` trong Giai đoạn 1.

5. **A5 – Hiển thị kênh & Lọc đối soát/sổ cái admin:**
   - Toàn bộ API đơn hàng (buyer, seller, admin) ở `order`, `gateway`, và `web` BFF đều trả `sales_channel` (đơn cũ mặc định `MARKETPLACE`) và `storefront_shop_id`, kèm lọc `?sales_channel=` có validate.
   - Giao diện Seller (`/seller/don-hang`, `/seller/don-hang/[id]`, `/seller`, `/seller/tai-chinh`) và Admin (`/admin`, `/admin/doi-soat`) hiển thị nhãn kênh tiếng Việt rõ ràng (`Sàn NexusMart` / `Site riêng`, `data-sales-channel`).
   - `services/payment` (`GetLedgerSummary`, `RunReconciliation`), Gateway (`/api/v1/admin/ledger/summary`, `/api/v1/admin/payments/reconcile`), và Admin BFF + UI (`/admin/doi-soat`, `data-channel-tab`) hỗ trợ lọc theo kênh (`Tất cả kênh` / `Sàn NexusMart` / `Site riêng`). `RunReconciliation` đối chiếu giao dịch nhà cung cấp với toàn bộ payment nội bộ trước khi lọc theo kênh để tránh sinh nhầm `missing_local` chéo kênh.

6. **A6 – Sổ cái cân đối & Không đổi công thức phí:**
   - Công thức hoa hồng theo danh mục giữ nguyên 100%. Sổ cái ghi kép VND duy trì `is_balanced = true` cho cả tổng thể lẫn từng kênh, và `MARKETPLACE + STOREFRONT == ALL` khớp chính xác 100%.

---

### B. Vá phần tin cậy còn sót

1. **B1 – Đóng toàn bộ RPC nội bộ & HTTP nội bộ còn tin ẩn danh (rá soát đủ 8/8 gRPC service Go + Rust):**
   - Cập nhật `internalRPCs` trong `pkg/common/auth/auth.go` và vá handler loại bỏ mọi nhánh bỏ qua khi `claims == nil`:
     - **`user`**: `ValidateUser` (`false`), `WriteAuditLog` (`true`), `Logout` (`false`), `GetProfile` (`true`), `GetAddress` (`true`) — tất cả bắt buộc claims hợp lệ & kiểm quyền sở hữu/admin.
     - **`product`**: `UpdateStock` (`true` — cho phép chủ shop, admin, hoặc token `order-service` hoàn tồn), `ReserveStock` (`false`), `ReleaseStock` (`true` + bắt buộc `requireAdmin`), `ConfirmStock` (`true` + bắt buộc `requireAdmin`).
     - **`cart`**: cả 5/5 RPC (`GetCart`, `AddItem`, `UpdateItemQty`, `RemoveItem`, `ClearItems` → `false`) bỏ nhánh `requireAuth=false`, bắt buộc JWT hợp lệ & kiểm quyền sở hữu.
     - **`promotion`**: 10/10 RPC không công khai vào `internalRPCs` (`CreditCoins`, `SaveVoucher`, `GetCoinBalance`, `PreviewDiscounts`, `ReservePromotion`, `CreateVoucher`, `CreateFlashSale`, `RegisterFlashSaleItem`, `ApproveFlashSaleItem` → `false`; `ReleasePromotion` → `true` + bắt buộc `isAdmin`). Vá lỗ hổng nghiêm trọng `CreditCoins` ẩn danh và IDOR `user_id` trên `ListAvailableVouchers` công khai.
     - **`order`**: toàn bộ RPC người dùng kiểm `requireAuthenticatedCaller`; `MarkPaymentResult` (`true`) chỉ nhận admin JWT hoặc service token.
     - **`shipping`**: `CreateShipment`, `CancelShipment`, `GetShipment` (3/3 → `false`) bỏ nhánh `if claims, ok := ...; ok` cho qua ẩn danh, bắt buộc JWT + kiểm `shop_id`/vai trò.
     - **`notification`**: 4/4 RPC (`ListNotifications`, `MarkRead`, `GetPreferences`, `UpdatePreferences` → `false`) chặn ẩn danh & từ chối service token.
     - **`payment` (Rust)**: toàn bộ 8/8 gRPC RPC qua `CallerAuth::from_request` (HS256 JWT; bỏ các fallback `Default`/`has_role` fail-open cũ; chặn `shop_id` rỗng ở `GetSellerBalance`); đồng thời vá các route HTTP nội bộ (`GET /payments/{id}/events` yêu cầu end-user JWT + kiểm sở hữu; `/internal/ledger/summary`, `/internal/reconciliation/queue`, `/internal/reconciliation/run` yêu cầu admin end-user JWT, từ chối ẩn danh/header giả/service token).
   - Bổ sung đầy đủ unit/bufconn rejection test trên cả 8 service và 18 live compose-network probe trong `tests/e2e/11_grpc_trust_b1_test.go`.

2. **B2 – Gateway `ClientIP` & `TRUSTED_PROXY_CIDRS`:**
   - `services/gateway/internal/middleware/ratelimit.go` + `logging.go` + `config/config.go`: mặc định `TRUSTED_PROXY_CIDRS=""` (không tin bất kỳ header proxy nào, luôn lấy `RemoteAddr`). Khi `RemoteAddr` thuộc CIDR tin cậy, phân tích `X-Forwarded-For` từ phải sang trái để lấy IP không tin cậy ngoài cùng bên phải (chống giả mạo chuỗi XFF nhiều chặng), fallback `X-Real-IP`.
   - `docker-compose.yml`: tạo mạng nội bộ riêng `bff` (`172.31.250.0/28`, `internal: true`) chỉ nối `gateway` (`gateway-bff`, `TRUSTED_PROXY_CIDRS=172.31.250.0/28`) và `web` (`GATEWAY_URL=http://gateway-bff:8080`). Traffic từ host qua cổng `28080` (`172.30.13.1`) và từ container khác (`e2e`) đều là untrusted nên giả mạo `X-Forwarded-For`/`X-Real-IP` bị bỏ qua hoàn toàn.

3. **B3 – `handleSellerGetOrder` từ chối `shop_id` rỗng:**
   - `services/gateway/internal/router/handlers_order.go` (và các seller handler lân cận): thêm `requireSellerShopID` và kiểm `orderShopID == "" || orderShopID != shopID`, chặn triệt để bypass `"" == ""` cả qua router lẫn khi gọi trực tiếp handler. Có unit test (`TestSellerGetOrderRejectsEmptyShopIDEvenIfOrderShopIDEmpty`) và kiểm thử trực tiếp trong E2E (`09_sales_channel_test.go`).

4. **B4 – `docker-compose.yml` khóa cổng nội bộ về `127.0.0.1`:**
   - Bind chặt về `127.0.0.1` cho Postgres (`5432`), Redis (`26379`), NATS (`4222`, `8222`), Mailpit (`1025`, `8025`), OTel (`4317`, `24318`, `8889`) và tất cả 8 cổng HTTP nội bộ 808x (`8081`, `28082`, `28083`, `8084`, `28085`, `8086`, `8087`, `8088`).
   - Giữ nguyên `gateway` (`28080`) và `web` (`3300`); giữ `0` cổng gRPC `5005x` publish ra host.

5. **B5 – `web` chỉ tin `x-forwarded-host`/`x-forwarded-proto` khi bật env:**
   - `web/src/lib/server/config.ts`, `http.ts`, `gateway.ts`, `web/src/middleware.ts`: cổng sau `TRUST_PROXY_HEADERS === "true"` (mặc định `false`). Mặc định dùng `Host` + protocol gốc của request và không chuyển tiếp `x-forwarded-for`/`x-real-ip` từ client lên gateway. Giữ toàn bộ kiểm tra CSRF xanh và thêm unit test (`security.test.mjs`) + E2E (`03_sales_channel_and_proxy.test.mjs`).

---

### C. Độ ổn định kiểm thử & Giữ nguyên giới hạn rate limit

- Giữ nguyên cấu hình mặc định của stack (`RATE_LIMIT_IP_PER_MIN=300`, `RATE_LIMIT_USER_PER_MIN=180`, `RATE_LIMIT_FLASH_SALE_PER_MIN=60`) và **không** dùng giả mạo `X-Forwarded-For`.
- Truyền `REDIS_ADDR=${REDIS_ADDR:-redis:6379}` cho hai container test `e2e` và `e2e-web`; bổ sung helper RESP2 thuần stdlib (0 dependency mới) tại `tests/e2e/redis_cleanup.go` (gọi ở `main_test.go`) và `tests/e2e-web/lib/redis.mjs` (gọi ở `before`/`after` của 3 suite) để xóa khóa `rl:*` của chính lượt chạy.
- Bổ sung `tests/e2e/12_ratelimit_test.go` chứng minh rate limit thật vẫn trả `429 RATE_LIMITED` sau đúng `60` request hợp lệ vào `/api/v1/flash-sales`, đồng thời chứng minh 4 kiểu giả mạo `X-Forwarded-For`/`X-Real-IP` từ IP không tin cậy vẫn bị chặn `429` rồi tự dọn `rl:*` trong `t.Cleanup`.

---

## 2. Bằng chứng từng Gate (đầu ra thật nguyên văn)

### Gate 1 – Bất biến kho mã nguồn, Chính sách & Môi trường

```text
=== FINAL INVARIANTS CHECK ===
branch: phase-0b
head: d399565
web/.next: 1795172876 2026-11-20 18:07:56.179789882 +0700
next dirs in web/:
drwxr-xr-x   8 dev dev   4096 Nov 20 18:07 .next
drwxr-xr-x   8 dev dev   4096 Nov 20 16:43 .next-platform
ports 3111/4777:
LISTEN 0      5                                   127.0.0.1:4777       0.0.0.0:*    users:(("python3",pid=983103,fd=3))          
LISTEN 0      5                                 100.93.43.5:4777       0.0.0.0:*    users:(("socat",pid=2938570,fd=5))           
LISTEN 0      511                               100.93.43.5:3111       0.0.0.0:*    users:(("next-server (v1",pid=1979251,fd=21))
docker compose 5005x count: 0
```

Kiểm tra bổ sung:
- `git diff -- web/package.json web/package-lock.json '**/go.mod' 'go.mod' services/payment/Cargo.toml services/payment/Cargo.lock`: **trống (0 dependency mới)**.
- `.github/workflows/ci.yml`: chỉ có `on: workflow_dispatch:` (không có `push`/`pull_request`/`schedule`).
- `curl -sSI -X OPTIONS -H 'Origin: https://evil.example' http://localhost:28080/healthz | grep -i access-control`: `0 Access-Control headers`.
- `git diff -- web/next-env.d.ts web/tsconfig.json | wc -l`: `0`.
- `web/.next` giữ nguyên mtime so với mốc nền trước Giai đoạn 1 (`1795172876 2026-11-20 18:07:56.179789882 +0700`); các tiến trình trên cổng `3111` (`pid=1979251`) và `4777` (`pid=983103`, `pid=2938570`) không bị đụng chạm.

---

### Gate 2 – `make lint` & Kiểm tra lint E2E

```text
=== make lint ===
buf lint
==> gofmt & golangci-lint pkg/common
==> gofmt & golangci-lint gen/go
==> gofmt & golangci-lint services/gateway
==> gofmt & golangci-lint services/user
==> gofmt & golangci-lint services/product
==> gofmt & golangci-lint services/cart
==> gofmt & golangci-lint services/promotion
==> gofmt & golangci-lint services/order
==> gofmt & golangci-lint services/shipping
==> gofmt & golangci-lint services/notification
==> rustfmt & clippy services/payment
    Finished `dev` profile [unoptimized + debuginfo] target(s) in 0.36s
=== tests/e2e lint ===
gofmt clean
=== tests/e2e-web syntax ===
e2e-web syntax PASS
```

---

### Gate 3 – `make test` (10 Go module `-race` + Rust `cargo test`)

```text
==> go test pkg/common
ok  	github.com/nexusmart/core/pkg/common/auth	1.038s
ok  	github.com/nexusmart/core/pkg/common/env	(cached)
ok  	github.com/nexusmart/core/pkg/common/logger	(cached)
ok  	github.com/nexusmart/core/pkg/common/metrics	(cached)
ok  	github.com/nexusmart/core/pkg/common/telemetry	(cached)
==> go test gen/go
?   	github.com/nexusmart/core/gen/go/nexusmart/cart/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/notification/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/order/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/payment/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/product/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/promotion/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/shipping/v1	[no test files]
?   	github.com/nexusmart/core/gen/go/nexusmart/user/v1	[no test files]
==> go test services/gateway
?   	github.com/nexusmart/core/services/gateway/cmd/gateway	[no test files]
?   	github.com/nexusmart/core/services/gateway/internal/clients	[no test files]
?   	github.com/nexusmart/core/services/gateway/internal/config	[no test files]
ok  	github.com/nexusmart/core/services/gateway/internal/httpx	(cached)
ok  	github.com/nexusmart/core/services/gateway/internal/middleware	1.016s
ok  	github.com/nexusmart/core/services/gateway/internal/proxy	1.014s
ok  	github.com/nexusmart/core/services/gateway/internal/router	1.045s
==> go test services/user
?   	github.com/nexusmart/core/services/user/cmd/user	[no test files]
?   	github.com/nexusmart/core/services/user/internal/config	[no test files]
?   	github.com/nexusmart/core/services/user/internal/db	[no test files]
?   	github.com/nexusmart/core/services/user/internal/events	[no test files]
ok  	github.com/nexusmart/core/services/user/internal/password	(cached)
ok  	github.com/nexusmart/core/services/user/internal/security	(cached)
?   	github.com/nexusmart/core/services/user/migrations	[no test files]
ok  	github.com/nexusmart/core/services/user/internal/service	2.116s
==> go test services/product
?   	github.com/nexusmart/core/services/product/cmd/product	[no test files]
?   	github.com/nexusmart/core/services/product/internal/db	[no test files]
?   	github.com/nexusmart/core/services/product/internal/store	[no test files]
?   	github.com/nexusmart/core/services/product/migrations	[no test files]
ok  	github.com/nexusmart/core/services/product/internal/server	1.053s
==> go test services/cart
?   	github.com/nexusmart/core/services/cart/cmd/cart	[no test files]
ok  	github.com/nexusmart/core/services/cart/internal/resilience	(cached)
?   	github.com/nexusmart/core/services/cart/internal/store	[no test files]
ok  	github.com/nexusmart/core/services/cart/internal/server	1.204s
==> go test services/promotion
?   	github.com/nexusmart/core/services/promotion/cmd/server	[no test files]
?   	github.com/nexusmart/core/services/promotion/internal/config	[no test files]
?   	github.com/nexusmart/core/services/promotion/internal/domain	[no test files]
?   	github.com/nexusmart/core/services/promotion/internal/store	[no test files]
ok  	github.com/nexusmart/core/services/promotion/internal/calculator	1.012s
ok  	github.com/nexusmart/core/services/promotion/internal/events	(cached)
ok  	github.com/nexusmart/core/services/promotion/internal/lua	(cached)
ok  	github.com/nexusmart/core/services/promotion/internal/service	1.529s
==> go test services/order
?   	github.com/nexusmart/core/services/order/cmd/server	[no test files]
?   	github.com/nexusmart/core/services/order/internal/config	[no test files]
?   	github.com/nexusmart/core/services/order/internal/db	[no test files]
?   	github.com/nexusmart/core/services/order/internal/grpcserver	[no test files]
?   	github.com/nexusmart/core/services/order/internal/store	[no test files]
?   	github.com/nexusmart/core/services/order/migrations	[no test files]
ok  	github.com/nexusmart/core/services/order/internal/clients	1.163s
ok  	github.com/nexusmart/core/services/order/internal/events	1.018s
ok  	github.com/nexusmart/core/services/order/internal/service	1.040s
ok  	github.com/nexusmart/core/services/order/internal/statemachine	1.013s
==> go test services/shipping
?   	github.com/nexusmart/core/services/shipping/cmd/server	[no test files]
?   	github.com/nexusmart/core/services/shipping/internal/cache	[no test files]
?   	github.com/nexusmart/core/services/shipping/internal/config	[no test files]
?   	github.com/nexusmart/core/services/shipping/internal/events	[no test files]
ok  	github.com/nexusmart/core/services/shipping/internal/address	(cached)
ok  	github.com/nexusmart/core/services/shipping/internal/carrier	(cached)
?   	github.com/nexusmart/core/services/shipping/internal/poller	[no test files]
?   	github.com/nexusmart/core/services/shipping/internal/store	[no test files]
?   	github.com/nexusmart/core/services/shipping/migrations	[no test files]
ok  	github.com/nexusmart/core/services/shipping/internal/grpcserver	1.022s
ok  	github.com/nexusmart/core/services/shipping/internal/service	(cached)
ok  	github.com/nexusmart/core/services/shipping/internal/webhook	(cached)
==> go test services/notification
?   	github.com/nexusmart/core/services/notification/cmd/server	[no test files]
?   	github.com/nexusmart/core/services/notification/internal/config	[no test files]
?   	github.com/nexusmart/core/services/notification/internal/db	[no test files]
?   	github.com/nexusmart/core/services/notification/internal/store	[no test files]
?   	github.com/nexusmart/core/services/notification/internal/userclient	[no test files]
?   	github.com/nexusmart/core/services/notification/migrations	[no test files]
ok  	github.com/nexusmart/core/services/notification/internal/consumer	(cached)
ok  	github.com/nexusmart/core/services/notification/internal/grpcserver	1.020s
ok  	github.com/nexusmart/core/services/notification/internal/mailer	(cached)
ok  	github.com/nexusmart/core/services/notification/internal/service	1.012s
==> cargo test services/payment
    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.24s
     Running unittests src/lib.rs (target/debug/deps/nexusmart_payment-e82bf25ec01a60de)

running 39 tests
test domain::tests::test_multi_shop_single_suborder_cancel_refund_scope ... ok
test auth::tests::rejects_empty_secrets ... ok
test domain::tests::test_reconciliation_classifications ... ok
test domain::tests::test_allocation_split_exact_vnd ... ok
test domain::tests::test_allocation_rejects_excessive_commission ... ok
test domain::tests::test_refund_cap_enforced ... ok
test domain::tests::test_per_suborder_refund_cap_prevents_multi_shop_double_refund ... ok
test grpc::tests::test_parse_optional_uuid_validation ... ok
test grpc::tests::test_proto_timestamp_roundtrip ... ok
test domain::tests::test_reconciliation_channel_matches_partitions_100_percent ... ok
test grpc::tests::test_to_proto_payment_legacy_default_and_storefront_mapping ... ok
test domain::tests::test_storefront_allocation_validation_rejects_cross_shop ... ok
test ledger::tests::test_unbalanced_transaction_rejected ... ok
test ledger::tests::test_zero_or_negative_or_single_line_rejected ... ok
test telemetry::tests::test_extract_trace_id_from_w3c_traceparent ... ok
test domain::tests::test_sales_channel_validation_and_defaults ... ok
test ledger::tests::test_sales_channel_ledger_partitioning_matches_unfiltered_100_percent ... ok
test ledger::tests::test_full_lifecycle_ledger_stays_balanced_across_all_scenarios ... ok
test webhook_security::tests::test_extract_order_code_from_sepay_transfer_content ... ok
test auth::tests::spoofed_metadata_without_jwt_is_unauthenticated ... ok
test webhook_security::tests::test_contracts_canonical_webhook_payload_deserializes ... ok
test webhook_security::tests::test_webhook_10x_idempotency_simulation ... ok
test webhook_security::tests::test_webhook_amount_mismatch_and_late_payment_decisions ... ok
test workers::tests::test_nats_retry_until_max_deliver_then_dlq ... ok
test auth::tests::user_jwt_wins_over_metadata ... ok
test webhook_security::tests::test_replay_protection_timestamp_window ... ok
test webhook_security::tests::test_hmac_verification_valid_and_invalid ... ok
test auth::tests::http_auth_enforces_user_jwt_and_admin_role ... ok
test telemetry::tests::test_masking_pii_and_secrets ... ok
test auth::tests::service_tokens_are_verified_and_scoped ... ok
test vietqr::tests::test_vietqr_mock_deterministic_payload_and_valid_hmac ... ok
test grpc::tests::test_grpc_sales_channel_validation_on_create_summary_and_reconcile ... ok
test grpc::tests::test_grpc_validation_and_red_metrics_recorded ... ok
test grpc::tests::test_b1_all_rpcs_reject_anonymous_and_spoofed_metadata_and_disallowed_service_tokens ... ok
test grpc::tests::test_grpc_defense_in_depth_rbac_enforcement ... ok
test http::tests::test_healthz_and_red_metrics_no_permissive_cors ... ok
test http::tests::test_webhook_rejects_missing_sig_missing_timestamp_invalid_sig_and_stale_ts ... ok
test http::tests::test_b1_http_sse_and_internal_routes_reject_anonymous_spoofed_and_non_admin ... ok
test vietqr::tests::test_circuit_breaker_transitions ... ok

test result: ok. 39 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.04s

     Running tests/sales_channel_db_integration.rs (target/debug/deps/sales_channel_db_integration-0cb2110ad8e260d1)

running 1 test
test test_pg_migration_immutability_and_channel_filtered_ledger_and_reconciliation ... ok

test result: ok. 1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.01s
```

---

### Gate 4 – Chi tiết test từ chối B1, B2 (XFF), B3 (`shop_id` rỗng) & A1–A4 trên từng Go service

```text
=== VERBOSE B1/B2/B3/A1-A4 UNIT TEST RUNS ===
=== RUN   TestGRPCInterceptorIgnoresSpoofedIdentityMetadata
--- PASS: TestGRPCInterceptorIgnoresSpoofedIdentityMetadata (0.00s)
=== RUN   TestGRPCInterceptorServiceTokens
--- PASS: TestGRPCInterceptorServiceTokens (0.01s)
=== RUN   TestGRPCInterceptorInternalRPCMatrix
--- PASS: TestGRPCInterceptorInternalRPCMatrix (0.00s)
PASS
ok  	github.com/nexusmart/core/pkg/common/auth	0.014s
=== RUN   TestUserGRPCTrust_B1RejectsAnonymousAndUnauthorizedCallers
--- PASS: TestUserGRPCTrust_B1RejectsAnonymousAndUnauthorizedCallers (0.11s)
PASS
ok  	github.com/nexusmart/core/services/user/internal/service	0.113s
=== RUN   TestGRPCTrust_SpoofedMetadataAndServiceTokens
--- PASS: TestGRPCTrust_SpoofedMetadataAndServiceTokens (0.02s)
PASS
ok  	github.com/nexusmart/core/services/product/internal/server	0.018s
=== RUN   TestGRPCTrust_CartService_B1
--- PASS: TestGRPCTrust_CartService_B1 (0.02s)
=== RUN   TestCartShopFilterReadAndClearLeavesOtherShopsIntact
--- PASS: TestCartShopFilterReadAndClearLeavesOtherShopsIntact (0.00s)
PASS
ok  	github.com/nexusmart/core/services/cart/internal/server	0.024s
=== RUN   TestGRPCTrust_PromotionService_B1
--- PASS: TestGRPCTrust_PromotionService_B1 (0.03s)
=== RUN   TestService_SalesChannel_StorefrontRules
=== RUN   TestService_SalesChannel_StorefrontRules/platform_voucher_field
=== RUN   TestService_SalesChannel_StorefrontRules/freeship_voucher_field
=== RUN   TestService_SalesChannel_StorefrontRules/smuggled_platform_voucher_in_shop_map
=== RUN   TestService_SalesChannel_StorefrontRules/smuggled_freeship_voucher_in_shop_map
=== RUN   TestService_SalesChannel_StorefrontRules/other_shop_voucher_id_under_own_key
=== RUN   TestService_SalesChannel_StorefrontRules/other_shop_voucher_map_key
=== RUN   TestService_SalesChannel_StorefrontRules/platform_coins
=== RUN   TestService_SalesChannel_StorefrontRules/other_shop_item
=== RUN   TestService_SalesChannel_StorefrontRules/other_shop_flash_sale_item
--- PASS: TestService_SalesChannel_StorefrontRules (0.00s)
PASS
ok  	github.com/nexusmart/core/services/promotion/internal/service	0.037s
=== RUN   TestSalesChannel_DefaultMarketplaceAndValidation
--- PASS: TestSalesChannel_DefaultMarketplaceAndValidation (0.00s)
=== RUN   TestSalesChannel_StorefrontHappyPathCODAndVietQR
--- PASS: TestSalesChannel_StorefrontHappyPathCODAndVietQR (0.00s)
=== RUN   TestSalesChannel_StorefrontRejectsInactiveShopAndCrossShopItemsZeroStockChange
--- PASS: TestSalesChannel_StorefrontRejectsInactiveShopAndCrossShopItemsZeroStockChange (0.00s)
=== RUN   TestSalesChannel_StorefrontRejectsPlatformPromotions
--- PASS: TestSalesChannel_StorefrontRejectsPlatformPromotions (0.00s)
=== RUN   TestSalesChannel_StorefrontCartScopingLeavesOtherShopItemsIntact
--- PASS: TestSalesChannel_StorefrontCartScopingLeavesOtherShopItemsIntact (0.00s)
=== RUN   TestSalesChannel_ImmutabilityAndLegacyBackwardCompat
--- PASS: TestSalesChannel_ImmutabilityAndLegacyBackwardCompat (0.00s)
=== RUN   TestOrderGRPCTrust_BufconnB1Enforcement
--- PASS: TestOrderGRPCTrust_BufconnB1Enforcement (0.00s)
PASS
ok  	github.com/nexusmart/core/services/order/internal/service	0.009s
=== RUN   TestShippingGRPCRBACAndShopOwnership
--- PASS: TestShippingGRPCRBACAndShopOwnership (0.00s)
=== RUN   TestShippingGRPCTrust_B1RejectsAnonymousAndServiceTokens
--- PASS: TestShippingGRPCTrust_B1RejectsAnonymousAndServiceTokens (0.00s)
PASS
ok  	github.com/nexusmart/core/services/shipping/internal/grpcserver	0.008s
=== RUN   TestNotificationGRPCAuthAndOwnershipRBAC
--- PASS: TestNotificationGRPCAuthAndOwnershipRBAC (0.00s)
=== RUN   TestNotificationGRPCTrust_BufconnRejectsAnonymousAndServiceTokens
--- PASS: TestNotificationGRPCTrust_BufconnRejectsAnonymousAndServiceTokens (0.00s)
PASS
ok  	github.com/nexusmart/core/services/notification/internal/grpcserver	0.007s
=== RUN   TestClientIPExtraction
--- PASS: TestClientIPExtraction (0.00s)
=== RUN   TestParseTrustedProxiesInvalidCIDR
--- PASS: TestParseTrustedProxiesInvalidCIDR (0.00s)
=== RUN   TestRateLimitCannotBeBypassedBySpoofedXFF
--- PASS: TestRateLimitCannotBeBypassedBySpoofedXFF (0.00s)
PASS
ok  	github.com/nexusmart/core/services/gateway/internal/middleware	0.003s
=== RUN   TestSellerGetOrderRejectsEmptyShopIDEvenIfOrderShopIDEmpty
--- PASS: TestSellerGetOrderRejectsEmptyShopIDEvenIfOrderShopIDEmpty (0.00s)
=== RUN   TestSalesChannelGatewayContractAndValidation
--- PASS: TestSalesChannelGatewayContractAndValidation (0.00s)
PASS
ok  	github.com/nexusmart/core/services/gateway/internal/router	0.012s
```

---

### Gate 5 – Web (`lint`, `format:check`, `tsc --noEmit`, 44 unit test, build với `NEXT_DIST_DIR=.next-verifier`)

```text
=== web lint ===
npm notice run nexusmart-web@0.1.0 lint
npm notice run eslint .
=== web format:check ===
npm notice run nexusmart-web@0.1.0 format:check
npm notice run prettier --check "src/**/*.{ts,tsx,css}"
Checking formatting...
All matched files use Prettier code style!
=== web tsc ===
npm notice run nexusmart-web@0.1.0 npx
npm notice run 'tsc' --noEmit

=== ALL 5 WEB UNIT TEST FILES ===
✔ mergeCategories keeps storefront slugs only, backend name wins (1.495679ms)
✔ mergeShops matches profile by name and keeps generic shops (0.344645ms)
✔ mergeProducts: real price/stock/sku ids, content kept, foreign categories dropped (0.937417ms)
✔ mergeProducts: unknown product built from attributes, invalid sizes skipped (0.298081ms)
✔ mergeVouchers keeps copy for known codes and real quota (0.303845ms)
✔ buildFlashSlots groups SKU items per product with VN clock and status (0.479889ms)
✔ mergeProducts keeps OUT_OF_STOCK products with 0 available_stock (0.115745ms)
✔ buildCatalogLookups and find helpers resolve by slug, id, backendId and skuId (0.15584ms)
✔ checkLiveVoucher validates real quota, min order, expiry, shop scope and discount cap (12.084856ms)
✔ filterStorefrontShops, mergeVouchers, and buildFlashSlots isolate storefront data from foreign E2E fixtures (0.511998ms)
✔ createTtlCache deduplicates in-flight calls, respects TTL, and never caches errors (0.467916ms)
✔ safeNextPath allows safe relative paths and rejects open redirects or auth loops (0.702846ms)
✔ mapGatewayAddress converts gateway DTO to AddressView (0.835518ms)
✔ toStorefrontProvinceCode maps numeric backend province codes (0.209625ms)
✔ maps a COD order with catalog presentation data (2.246006ms)
✔ falls back to backend names when the product is unknown to the catalog (0.233503ms)
✔ subtotal shown excludes the flash sale discount already applied to line prices (0.191106ms)
✔ VietQR pending order exposes payment due time and retry state (0.245692ms)
✔ cancel reason is shown only when the buyer cancelled (1.137739ms)
✔ return request reason strips the backend prefix; delivered allows confirm/return (0.508207ms)
✔ status helpers (0.320328ms)
✔ preserves sales_channel and storefront_shop_id with legacy MARKETPLACE default (0.26252ms)
✔ translatePromotionWarning maps English promotion warnings to clean Vietnamese without raw internals (9.972946ms)
✔ mapCheckoutError maps out-of-stock, COD limit, voucher exhausted, and empty cart safely (0.269042ms)
✔ mapCartError and mapVoucherError return clean buyer messages and status codes (0.156844ms)
✔ enrichCartGroups pairs backend cart groups with catalog presentation while preserving real prices and stock (0.892924ms)
✔ voucher formatting and eligibility reasons handle fixed, percent, exhausted, and min-order vouchers (0.36042ms)
✔ buildMarketplaceCheckoutPayload strips client sales_channel and storefront_shop_id and always sends MARKETPLACE (0.653329ms)
✔ salesChannel helpers normalize, validate filters, and provide clean Vietnamese labels (0.179339ms)
✔ assertSameOrigin accepts matching same-origin Origin header (17.356262ms)
✔ assertSameOrigin rejects foreign Origin with 403 BAD_ORIGIN (0.44293ms)
✔ assertSameOrigin rejects Origin: null (0.238574ms)
✔ assertSameOrigin rejects cross-site and same-site Sec-Fetch-Site (0.400529ms)
✔ assertSameOrigin rejects missing Origin and Referer (0.171958ms)
✔ assertSameOrigin rejects protocol mismatch between Origin and request (0.19452ms)
✔ assertSameOrigin accepts same-origin Referer only when Origin header is absent (0.38146ms)
✔ errorFromResponse and errorResponse never leak gateway internals or trace_id (2.632293ms)
✔ cookie configuration is HttpOnly, SameSite=Lax, host-only (no Domain) (0.314515ms)
✔ accessExpiresSoon detects missing, malformed, expired, and valid JWTs (0.407116ms)
✔ applyAuthCookiesToHeader replaces or removes auth cookies while preserving others (0.152883ms)
✔ toGatewayAddressBody normalizes camelCase/snake_case and never includes user_id (0.156583ms)
✔ TRUST_PROXY_HEADERS defaults to false and ignores spoofed x-forwarded-host/proto in CSRF checks (0.529545ms)
✔ assertSameOrigin honors x-forwarded-host/proto ONLY when TRUST_PROXY_HEADERS=true (0.302627ms)
✔ buildLoginRedirectUrl and proxy helpers ignore x-forwarded-* by default and honor when TRUST_PROXY_HEADERS=true (0.376727ms)
ℹ tests 44
ℹ suites 0
ℹ pass 44
ℹ fail 0
ℹ duration_ms 180.802701

=== web build (NEXT_DIST_DIR=.next-verifier) ===
   ▲ Next.js 15.5.29

   Creating an optimized production build ...
 ✓ Compiled successfully in 10.5s
 ✓ Linting and checking validity of types 
 ✓ Collecting page data 
 ✓ Generating static pages (47/47)
 ✓ Collecting build traces 
 ✓ Finalizing page optimization 
+ First Load JS shared by all                                        103 kB
ƒ Middleware                                                        35.8 kB
=== web/.next mtime post-build ===
1795172876 2026-11-20 18:07:56.179789882 +0700
=== git diff web/next-env.d.ts web/tsconfig.json ===
0
```

---

### Gate 6 – Chuỗi E2E liền mạch (`make e2e && make seed && make e2e-web && make e2e-web && make e2e-web`)

Lệnh chạy nguyên văn không ngắt quãng: `make e2e && make seed && make e2e-web && make e2e-web && make e2e-web` → **EXIT 0**.

#### 6.1 `make e2e` (12/12 PASS trong `8.764s`)

```text
GOMODCACHE="$(go env GOMODCACHE)" docker compose --profile e2e run --rm --user "$(id -u):$(id -g)" e2e
=== RUN   TestE2E_HealthAndObservability
--- PASS: TestE2E_HealthAndObservability (0.06s)
=== RUN   TestE2E_MultiShopCheckout_COD
--- PASS: TestE2E_MultiShopCheckout_COD (2.79s)
=== RUN   TestE2E_MultiShopCheckout_VietQRMock
--- PASS: TestE2E_MultiShopCheckout_VietQRMock (0.71s)
=== RUN   TestE2E_OrderIdempotency
--- PASS: TestE2E_OrderIdempotency (0.40s)
=== RUN   TestE2E_PaymentWebhookIdempotencyAndSecurity
--- PASS: TestE2E_PaymentWebhookIdempotencyAndSecurity (1.02s)
=== RUN   TestE2E_FlashSale_1000Concurrent_100Slots
    06_flash_sale_test.go:150: FlashSale 1000 concurrent requests completed in 260.793656ms: succeeded=100 failed=900
--- PASS: TestE2E_FlashSale_1000Concurrent_100Slots (0.69s)
=== RUN   TestE2E_PaymentFailed_SagaCompensation
    07_saga_compensation_test.go:141: Saga compensation verified: stock=30 voucher_quota=5 coins=10000 all 100% restored
--- PASS: TestE2E_PaymentFailed_SagaCompensation (0.72s)
=== RUN   TestE2E_LedgerAlwaysBalanced
    08_ledger_test.go:9: Double-Entry Ledger Summary: total_debits_vnd=55973600 total_credits_vnd=55973600 is_balanced=true accounts=26
--- PASS: TestE2E_LedgerAlwaysBalanced (0.00s)
=== RUN   TestE2E_SalesChannel
    09_sales_channel_test.go:369: Cross-shop sole item rejected cleanly: HTTP 400 body={"error":{"code":"INVALID_ARGUMENT","message":"STOREFRONT_SHOP_MISMATCH: all checkout items must belong to storefront shop 9637a5a4-2cc5-4375-bb0a-08a0633ef9b9","trace_id":"62cba41510adedc74935fac7790b63e4"}}
    09_sales_channel_test.go:385: Cross-shop mixed items rejected cleanly: HTTP 400 body={"error":{"code":"INVALID_ARGUMENT","message":"STOREFRONT_SHOP_MISMATCH: all checkout items must belong to storefront shop 9637a5a4-2cc5-4375-bb0a-08a0633ef9b9","trace_id":"452ab2abc64581c160fa612b4457936c"}}
    09_sales_channel_test.go:571: PLATFORM voucher on STOREFRONT rejected cleanly: HTTP 400 body={"error":{"code":"INVALID_ARGUMENT","message":"STOREFRONT_PLATFORM_VOUCHER_NOT_ALLOWED: platform vouchers cannot be used on STOREFRONT orders","trace_id":"f84f5a3d75c7ee982cb4913dd9d585d1"}}
    09_sales_channel_test.go:571: FREESHIP voucher on STOREFRONT rejected cleanly: HTTP 400 body={"error":{"code":"INVALID_ARGUMENT","message":"STOREFRONT_FREESHIP_VOUCHER_NOT_ALLOWED: platform freeship vouchers cannot be used on STOREFRONT orders","trace_id":"99ef88831e27e3ac0ad876eb48e0f48b"}}
    09_sales_channel_test.go:571: Other-shop SHOP voucher on STOREFRONT rejected cleanly: HTTP 400 body={"error":{"code":"INVALID_ARGUMENT","message":"STOREFRONT_VOUCHER_SHOP_MISMATCH: shop voucher SHPV0750819146 belongs to shop \"1c5a7bc6-3b1a-4642-93c8-0b7320d18908\", not storefront shop \"9637a5a4-2cc5-4375-bb0a-08a0633ef9b9\"","trace_id":"d5fc47d19f51527b1d8f2bb147a274b1"}}
    09_sales_channel_test.go:571: Platform coins on STOREFRONT rejected cleanly: HTTP 400 body={"error":{"code":"INVALID_ARGUMENT","message":"STOREFRONT_COINS_NOT_ALLOWED: platform coins cannot be used on STOREFRONT orders","trace_id":"2a1c0be1bd3e82de80ed90f0724cfeea"}}
--- PASS: TestE2E_SalesChannel (1.77s)
    --- PASS: TestE2E_SalesChannel/DefaultCheckout_ReadsAsMarketplace (0.02s)
    --- PASS: TestE2E_SalesChannel/Storefront_COD_CartFilter_SelectiveClear_And_Immutability (0.45s)
    --- PASS: TestE2E_SalesChannel/Storefront_VietQRMock_Webhook_And_Refund (0.38s)
    --- PASS: TestE2E_SalesChannel/Storefront_CrossShopItemRejected_ZeroStockChange (0.10s)
    --- PASS: TestE2E_SalesChannel/Storefront_InvalidAndInactiveShopRejected_ZeroStockChange (0.37s)
    --- PASS: TestE2E_SalesChannel/Storefront_PlatformVoucher_Freeship_OtherShopVoucher_Coins_Rejected (0.08s)
=== RUN   TestE2E_LedgerAndReconciliation_ChannelFilter
    10_ledger_channel_test.go:21: Ledger totals: ALL=(debits=57972600 credits=57972600 balanced=true) MARKETPLACE=(debits=54522600 credits=54522600 balanced=true) STOREFRONT=(debits=3450000 credits=3450000 balanced=true)
    10_ledger_channel_test.go:66: Reconciliation (2026-11-20): ALL=(items=33 matched=9) MARKETPLACE=(items=31 matched=7) STOREFRONT=(items=2 matched=2)
--- PASS: TestE2E_LedgerAndReconciliation_ChannelFilter (0.02s)
    --- PASS: TestE2E_LedgerAndReconciliation_ChannelFilter/LedgerSummary_ByChannel_BalancedAndExactSum (0.01s)
    --- PASS: TestE2E_LedgerAndReconciliation_ChannelFilter/Reconciliation_ByChannel_ExactSum (0.02s)
=== RUN   TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata
--- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata (0.50s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/ProductService.UpdateStock (0.06s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/CartService.ClearItems (0.06s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/CartService.GetCart (0.00s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/ShippingService.CreateShipment (0.06s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/ShippingService.CancelShipment (0.00s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/ShippingService.GetShipment (0.00s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/UserService.ValidateUser (0.06s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/UserService.WriteAuditLog (0.00s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/OrderService.MarkPaymentResult (0.06s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/NotificationService.ListNotifications (0.06s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/NotificationService.MarkRead (0.00s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PromotionService.CreditCoins (0.08s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PromotionService.PreviewDiscounts (0.00s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PromotionService.GetCoinBalance (0.00s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PromotionService.SaveVoucher (0.00s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PaymentService.GetLedgerSummary (0.05s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PaymentService.ReleaseEscrow (0.00s)
    --- PASS: TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata/PaymentService.RunReconciliation (0.00s)
=== RUN   TestE2E_RateLimit_Real429AndNoXFFBypass
    12_ratelimit_test.go:69: Genuine 429 observed after 60 allowed requests: X-RateLimit-Limit="60" X-RateLimit-Remaining="0" Retry-After="60" body={"error":{"code":"RATE_LIMITED","message":"rate limit exceeded, please retry later","trace_id":"084288171ea8386cef14356ec3d118aa"}}
--- PASS: TestE2E_RateLimit_Real429AndNoXFFBypass (0.07s)
PASS
ok  	github.com/nexusmart/core/tests/e2e	8.764s
```

#### 6.2 `make seed` (Idempotent PASS)

```text
node --no-warnings=MODULE_TYPELESS_PACKAGE_JSON tools/seed/seed.mjs
==> Seeding NexusMart backend via http://localhost:28080 (Mailpit: http://localhost:8025)
==> Seed complete:
  users:      created=0, reused=5
  shops:      created=0, reused=4
  categories: created=0, reused=6
  products:   created=0, reused=18 (totalSkus=159)
  vouchers:   created=0, reused=11
  flashSales: windowsCreated=0, windowsReused=3, itemsCreated=0, itemsReused=76
```

#### 6.3 `make e2e-web` × 3 lượt liên tiếp (13/13 PASS cả 3 lượt, 0 lỗi 429 ngoài ý muốn)

```text
# Run 1 (duration_ms 2929.07555):
▶ 01. Buyer Auth Lifecycle, Unauthenticated Page Guards & Seeded Storefront SSR (304.36546ms)
  ✔ (a) registers a fresh buyer, verifies Mailpit OTP, logs in with HttpOnly host-only cookies and no token leak, refreshes, and logs out
  ✔ (j) redirects unauthenticated visitors away from protected pages to /dang-nhap
  ✔ (j) renders seeded storefront pages with HTTP 200 and real Vietnamese catalog data
▶ 02. Buyer Commerce & Security Suite (2020.001312ms)
  ✔ (b)(c)(d) adds items from 2 seeded shops, applies a real voucher, checks out COD, and verifies orders
  ✔ (e) completes VietQR mock payment flow: checkout -> pending payment -> external HMAC bank webhook -> poll PAID & CONFIRMED
  ✔ (f) checkout idempotency: resending same Idempotency-Key returns same single order without duplicates
  ✔ (g) object-level authorization: Buyer A and Buyer B cannot read or mutate each other's orders or payments
  ✔ (h) unauthenticated writes and private reads return 401 without leaking internal details
  ✔ (i) CSRF protection rejects foreign/missing Origin and cross-site Fetch-Metadata with 403
  ✔ (j) enforces server-side RBAC for Buyer, Seller, and Admin pages & BFF APIs
▶ 03. Phase 1 Sales Channel BFF Contract, Channel Filters & Proxy Header Hardening (449.331214ms)
  ✔ (a) BFF POST /api/checkout ignores client sales_channel=STOREFRONT and forces MARKETPLACE; GET /api/cart?shop_id filters without mutating cart
  ✔ (b) Seller orders and Admin ledger/reconciliation channel filter endpoints & SSR pages work via BFF
  ✔ (c) B5 proxy header hardening: spoofed X-Forwarded-Host / X-Forwarded-Proto cannot bypass CSRF or hijack redirects
ℹ tests 13 | suites 3 | pass 13 | fail 0 | duration_ms 2929.07555

# Run 2 (duration_ms 2486.804139):
ℹ tests 13 | suites 3 | pass 13 | fail 0 | duration_ms 2486.804139

# Run 3 (duration_ms 2082.536838):
ℹ tests 13 | suites 3 | pass 13 | fail 0 | duration_ms 2082.536838
```

---

### Gate 7 – B4 Phơi bày mạng (`ss -ltnp` & `docker compose ps`) & Kiểm tra Trigger DB thật

```text
nexusmart compose ports in ss -ltnp:
LISTEN 0      4096                                  0.0.0.0:28080      0.0.0.0:*
LISTEN 0      4096                                  0.0.0.0:3300       0.0.0.0:*
LISTEN 0      4096                                127.0.0.1:1025       0.0.0.0:*
LISTEN 0      4096                                127.0.0.1:24318      0.0.0.0:*
LISTEN 0      4096                                127.0.0.1:26379      0.0.0.0:*
LISTEN 0      4096                                127.0.0.1:28082      0.0.0.0:*
LISTEN 0      4096                                127.0.0.1:28083      0.0.0.0:*
LISTEN 0      4096                                127.0.0.1:28085      0.0.0.0:*
LISTEN 0      4096                                127.0.0.1:4222       0.0.0.0:*
LISTEN 0      4096                                127.0.0.1:4317       0.0.0.0:*
LISTEN 0      4096                                127.0.0.1:5432       0.0.0.0:*
LISTEN 0      4096                                127.0.0.1:8025       0.0.0.0:*
LISTEN 0      4096                                127.0.0.1:8081       0.0.0.0:*
LISTEN 0      4096                                127.0.0.1:8084       0.0.0.0:*
LISTEN 0      4096                                127.0.0.1:8086       0.0.0.0:*
LISTEN 0      4096                                127.0.0.1:8087       0.0.0.0:*
LISTEN 0      4096                                127.0.0.1:8088       0.0.0.0:*
LISTEN 0      4096                                127.0.0.1:8222       0.0.0.0:*
LISTEN 0      4096                                127.0.0.1:8889       0.0.0.0:*
```

```text
=== DOCKER COMPOSE PS (15/15 healthy) ===
nexus_ecom-cart-1             50053/tcp, 127.0.0.1:28083->8083/tcp   Up (healthy)
nexus_ecom-gateway-1          0.0.0.0:28080->8080/tcp                Up (healthy)
nexus_ecom-mailpit-1          127.0.0.1:1025->1025/tcp, 127.0.0.1:8025->8025/tcp, 1110/tcp   Up (healthy)
nexus_ecom-nats-1             127.0.0.1:4222->4222/tcp, 127.0.0.1:8222->8222/tcp, 6222/tcp   Up (healthy)
nexus_ecom-notification-1     127.0.0.1:8088->8088/tcp, 50058/tcp    Up (healthy)
nexus_ecom-order-1            50055/tcp, 127.0.0.1:28085->8085/tcp   Up (healthy)
nexus_ecom-otel-collector-1   127.0.0.1:4317->4317/tcp, 127.0.0.1:8889->8889/tcp, 55678-55679/tcp, 127.0.0.1:24318->4318/tcp   Up
nexus_ecom-payment-1          127.0.0.1:8086->8086/tcp, 50056/tcp    Up (healthy)
nexus_ecom-postgres-1         127.0.0.1:5432->5432/tcp               Up (healthy)
nexus_ecom-product-1          50052/tcp, 127.0.0.1:28082->8082/tcp   Up (healthy)
nexus_ecom-promotion-1        127.0.0.1:8084->8084/tcp, 50054/tcp    Up (healthy)
nexus_ecom-redis-1            127.0.0.1:26379->6379/tcp              Up (healthy)
nexus_ecom-shipping-1         127.0.0.1:8087->8087/tcp, 50057/tcp    Up (healthy)
nexus_ecom-user-1             127.0.0.1:8081->8081/tcp, 50051/tcp    Up (healthy)
nexus_ecom-web-1              0.0.0.0:3300->3000/tcp                 Up (healthy)
```

```text
=== LIVE POSTGRES IMMUTABILITY TRIGGERS ===
order_db:
  trg_checkout_groups_sales_channel_immutable | checkout_groups
  trg_orders_sales_channel_immutable          | orders
payment_db:
  trg_ledger_entries_immutable                    | ledger_entries
  trg_ledger_transactions_immutable               | ledger_transactions
  trg_payments_sales_channel_immutable            | payments
  trg_payment_allocations_sales_channel_immutable | payment_shop_allocations
```

---

## 3. Phần chưa làm (theo phạm vi) & Các điểm lệch / hạn chế đã biết

### 3.1 Phần cố ý chưa làm trong Giai đoạn 1 (đúng phạm vi đặc tả)
- **Site riêng theo subdomain (`<slug>.nexusmart.vn`), bảng `sites`/`site_versions`, tự suy ra `sales_channel=STOREFRONT` từ `Host` ở BFF:** thuộc Giai đoạn 2 (`docs/research/sites/review-opus.md` §5.2). Ở Giai đoạn 1, BFF `web/src/app/api/checkout` cố ý khóa cứng `MARKETPLACE` theo yêu cầu A4.
- **Thay đổi tỷ lệ hoa hồng/phí cho kênh `STOREFRONT`:** cố ý giữ nguyên tỷ lệ danh mục theo yêu cầu A6.
- **Nâng cấp JWT bất đối xứng (EdDSA + JWKS) / mTLS nội bộ:** vẫn dùng chung khóa đối xứng HS256 (`JWT_SECRET`, `SERVICE_JWT_SECRET`, đã ghi chú `ponytail:`); theo lộ trình `review-opus.md` §5.2, phần này thuộc Giai đoạn 3.

### 3.2 Các điểm lệch nhỏ & Hạn chế đã biết
1. **Mạng `bff` cô lập cho B2 (`172.31.250.0/28`) thay vì tin toàn bộ mạng compose mặc định:** Thay vì đặt `TRUSTED_PROXY_CIDRS` bằng cả dải mạng `default` của Docker Compose (vốn cũng chứa gateway bridge `172.30.13.1` nhận traffic từ cổng công khai `28080` và container `e2e`), `docker-compose.yml` tạo thêm mạng nội bộ riêng `bff` chỉ gắn `web` và `gateway`. Cách làm này **chặt hơn** gợi ý trong brief vì đảm bảo tuyệt đối người gọi từ host (`28080`) và mọi container khác không thể giả mạo `X-Forwarded-For`.
2. **Test tích hợp Postgres của Rust (`services/payment/tests/sales_channel_db_integration.rs`):** Khi chạy `make test` mặc định không có biến `PAYMENT_TEST_DATABASE_URL`, test này tự bỏ qua sạch để không làm hỏng CI offline. Trong Giai đoạn 1, tính tương thích ngược của migration `0002_sales_channel.sql`, trigger bất biến DB, và tổng khớp 100% của sổ cái/đối soát theo kênh đã được xác minh trực tiếp trên Postgres 16 thật (qua `make e2e` và truy vấn `pg_trigger` trên cụm đang chạy, bên cạnh lượt chạy DB tạm độc lập của `@payment`).
3. **Chạy unit test `web` bằng Node 22:** Vì mã TypeScript của Next.js dùng import tương đối không đuôi `.ts`, lệnh `node --test` cần loader sẵn có của repo `tools/seed/ts-resolve.mjs` (đã ghi ở đầu `web/src/lib/server/security.test.mjs`). Khi chạy kèm loader này, đủ 5 file test (`44/44`) đều pass.
4. **`web/next-env.d.ts` khi build với `NEXT_DIST_DIR` riêng:** Next.js 15 tự ghi lại dòng tham chiếu `routes.d.ts` trong `web/next-env.d.ts` theo `NEXT_DIST_DIR` khi chạy `next build`; trong bước kiểm định, `verifier` đã xóa `web/.next-verifier` và khôi phục `web/next-env.d.ts` (`git diff = 0`) để không để lại thay đổi ngoài ý muốn. Thư mục `web/.next-platform` có sẵn từ Giai đoạn 0b (`Nov 20 16:43`) được giữ nguyên không can thiệp.
5. **Các cổng khác trên host trong `ss -ltnp`:** Trên máy dev hiện tại có tiến trình ngoài NexusMart đang nghe ở `0.0.0.0:6379`, `0.0.0.0:8082` (`media-api`), `*:8083` (`job-handler`), `0.0.0.0:8085`, `127.0.0.1:4318`. Toàn bộ dịch vụ NexusMart tương ứng dùng biến override cổng (`26379`, `28082`, `28083`, `28085`, `24318`) và đều bind chặt về `127.0.0.1`.
