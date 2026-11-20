# Giai đoạn 0a – Vá tin cậy gRPC: báo cáo

Brief: `.agents/briefs/sites-0a-grpc-trust.md`. Ngày: 2026-11-20.

## 1. Nguyên nhân

1. Trong `pkg/common/auth/auth.go`, `ExtractIncomingGRPC` và `GRPCUnaryServerInterceptor` (bản cũ, dòng 218–269) dựng `Claims` trực tiếp từ metadata `x-user-id`, `x-user-role`, `x-shop-id` khi request không mang Bearer JWT, và không kiểm chữ ký.
2. `docker-compose.yml` công bố 8 cổng gRPC 50051–50058 trên `0.0.0.0`.
3. Payment (Rust) còn tệ hơn. `CallerAuth::from_request` (`services/payment/src/grpc.rs`) chỉ đọc metadata. Nếu request không có `x-user-role` thì mọi kiểm tra quyền (`require_admin`, `require_payment_read_access`, …) đều cho qua, và `ReleaseEscrow` không kiểm tra quyền gì cả.
4. Lời gọi nội bộ của order (saga bù trừ, consumer kết quả thanh toán, watchdog hết hạn) tự xưng `admin` bằng metadata trần (`WithInternalAdminContext`). Notification gọi `UserService.GetProfile` ẩn danh, và handler cho phép điều đó.

Kết quả: ai kết nối được tới một cổng 5005x đều có thể tự xưng là bất kỳ user, admin hay shop nào.

## 2. Đã khảo sát (grep mọi nơi gọi và mọi nơi set metadata)

| Nơi | Vai trò | Xử lý |
|---|---|---|
| 8 `services/*/cmd/*/main.go` | đăng ký `auth.GRPCUnaryServerInterceptor` | đổi sang `(userMgr, svcMgr)` |
| `services/gateway/internal/clients/clients.go` `OutgoingContext` | chuyển JWT người dùng và metadata x-user-* | chỉ chuyển Bearer |
| `services/order/internal/clients/clients.go` (interceptor client, `WithInternalAdminContext`) | chuyển JWT người mua, hoặc tự xưng admin bằng metadata | JWT người mua, hoặc token service |
| order: `service.go` `detachedCompensationContext`, `handlers.go:73,274`, `checkout.go:770`, `lifecycle.go:567` | lời gọi không có người dùng | dùng token service (call site giữ nguyên) |
| `services/notification/internal/userclient/client.go` | consumer gọi `GetProfile` ẩn danh | token service |
| `services/cart/cmd/cart/main.go` (gọi product/user) | ẩn danh, chỉ dùng RPC công khai `GetSKUsBatch`/`GetShop` | không đổi |
| `services/payment/src/grpc.rs` | tin metadata | xác minh JWT |
| `tests/e2e/06_flash_sale_test.go:102` | gọi gRPC từ host với `x-user-id` giả | JWT ký bằng khoá dev, chạy trong network compose |
| `order/internal/clients/clients_test.go`, `gateway/internal/router/router_test.go`, `pkg/common/auth/auth_test.go` | test kiểm metadata cũ | cập nhật |

## 3. Thay đổi (file:dòng, theo bản mới)

### Chỗ dùng chung, Go: [auth.go](../pkg/common/auth/auth.go)
- `:24-27` thêm `ServiceTokenTTL = 1 phút` và `ServiceRole`. Bỏ các hằng `MDUserID`, `MDUserRole`, `MDShopID`.
- `:33-44` thêm `internalRPCs`: danh sách RPC không bao giờ được chạy ẩn danh. Giá trị `true` nghĩa là RPC đó nhận thêm token service:
  `UserService/GetAddress`, `UserService/GetProfile`, `ProductService/ReleaseStock`, `ProductService/ConfirmStock`, `PromotionService/ReleasePromotion` (true); `ProductService/ReserveStock`, `PromotionService/ReservePromotion` (false, cần JWT người mua).
- `:56` thêm claim `service` vào `Claims`.
- `:122-176` thêm `SignServiceToken` (kèm comment `ponytail:` nêu giới hạn hiện tại và hướng nâng cấp lên mTLS hoặc JWT bất đối xứng + JWKS), `VerifyServiceToken` và `verify`. `VerifyAccessToken` giờ từ chối token có claim `service`, còn `VerifyServiceToken` từ chối token không có claim này. Vì vậy hai loại token không lẫn được nhau, kể cả khi cấu hình nhầm hai khoá trùng nhau.
- `:249-266` `InjectOutgoingGRPC(ctx, rawBearer)` chỉ gắn `authorization`, không gửi x-user-* nữa.
- `:268-311` viết lại `GRPCUnaryServerInterceptor(users, svc)`. Đã xoá `ExtractIncomingGRPC`. Hành vi mới:
  - JWT người dùng hợp lệ: đặt Claims và giữ raw token để chuyển tiếp xuống dưới.
  - Token service hợp lệ trên RPC được phép: đặt Claims, không giữ raw token (token service không bị chuyển tiếp tiếp).
  - Token service trên RPC không được phép: `PermissionDenied`.
  - Token sai: `Unauthenticated`.
  - Không có token: ẩn danh (handler tự kiểm quyền), riêng `internalRPCs` thì trả `Unauthenticated`.
  - Metadata x-user-* bị bỏ qua hoàn toàn.

### Order và notification
- [order clients.go](../services/order/internal/clients/clients.go) `:84` `DialAll(cfg, svc)`. `:168-181` `WithInternalAdminContext` giờ chỉ đánh dấu ctx bằng `Service: "order-service"` và xoá raw token. `:229-237` interceptor client ký token service (TTL 1 phút, khoá riêng) cho ctx đã đánh dấu, các trường hợp khác chuyển JWT người dùng.
- [order main.go](../services/order/cmd/server/main.go) `:85-89`, `:91`, `:156`.
- [notification userclient](../services/notification/internal/userclient/client.go) `:29-34`, `:69-74` gửi token service `notification-service` mỗi lần gọi `GetProfile`. [notification main.go](../services/notification/cmd/server/main.go) `:84-89`, `:91`, `:121`.
- Các main khác chỉ thêm `svcMgr` (đọc `SERVICE_JWT_SECRET`, dừng khởi động nếu rỗng) và truyền vào interceptor: [user](../services/user/cmd/user/main.go) `:101-105,121`, [product](../services/product/cmd/product/main.go) `:75-79,98`, [cart](../services/cart/cmd/cart/main.go) `:75-79,119`, [promotion](../services/promotion/cmd/server/main.go) `:64-69,118`, [shipping](../services/shipping/cmd/server/main.go) `:146-151,157`.
- [gateway clients.go](../services/gateway/internal/clients/clients.go) `:122-131` chỉ chuyển Bearer.

### Payment (Rust)
- [auth.rs](../services/payment/src/auth.rs) là file mới. `GrpcAuth::authenticate` (`:65`) tự xác minh HS256 (hmac + sha2 + base64), kiểm `alg` và `exp`, và yêu cầu có `sub` và role. Token service chỉ được nhận ở `SERVICE_METHODS` (`:17`): `GetPayment`, `CancelPayment`, `CreateRefund`, `ReleaseEscrow`. Có comment `ponytail:` và unit test.
- [grpc.rs](../services/payment/src/grpc.rs):
  - `:41` `from_request(auth, request, method) -> Result`. Mọi handler đều gọi nó trước tiên.
  - Ở mọi handler, `let caller = caller?;` chạy ngay đầu khối `async`, nên metrics RED vẫn ghi nhận cả lỗi auth.
  - `:434` `ReleaseEscrow` giờ yêu cầu `require_admin`.
  - Test đã chuyển sang dùng JWT thật. Thêm test metadata giả bị trả `Unauthenticated`.
- [config.rs](../services/payment/src/config.rs) `:40-43,76-77,115-116` thêm `jwt_secret` và `service_jwt_secret` (có che khi in Debug). [main.rs](../services/payment/src/main.rs) `:11,85-86`. [lib.rs](../services/payment/src/lib.rs) `:1`. [Cargo.toml](../services/payment/Cargo.toml) `:11` thêm `base64 = "0.22"` (bản 0.22.1 vốn đã có trong lock).

### Hạ tầng
- [docker-compose.yml](../docker-compose.yml):
  - Xoá 8 ánh xạ `${*_GRPC_PORT:-5005x}:5005x`.
  - Thêm `SERVICE_JWT_SECRET` (mặc định dev, ghi đè được) cho 8 service. Payment nhận thêm `JWT_SECRET`.
  - `:427` thêm service `e2e` (profile `e2e`, image `golang:1.22-alpine`) để chạy suite trong network compose.
  - Các override cổng host còn lại giữ nguyên cơ chế cũ.
- [Makefile](../Makefile) `:52-54`: `make e2e` = `docker compose --profile e2e run --rm --user $(id -u):$(id -g) e2e`, dùng mod cache của host.
- [.env.example](../.env.example): bỏ các biến `*_GRPC_PORT`, ghi chú gRPC chỉ dùng nội bộ, thêm `SERVICE_JWT_SECRET` (giá trị dev, phải khác `JWT_SECRET`).
- E2E:
  - [06_flash_sale_test.go](../tests/e2e/06_flash_sale_test.go) `:75-76,102-107`: đợt 1000 request vẫn gọi gRPC trực tiếp, nhưng trong network compose và mang JWT người mua.
  - [client.go](../tests/e2e/client.go) `:44-56` thêm `MintAccessToken` (chỉ dùng stdlib).
  - [config.go](../tests/e2e/config.go) `:32-33,68` thêm `JWTSecret`.

### Test mới
- [grpc_trust_test.go (auth)](../pkg/common/auth/grpc_trust_test.go):
  - metadata giả bị bỏ qua (RPC công khai) hoặc bị trả `Unauthenticated` (RPC nội bộ);
  - JWT shop A kèm `x-shop-id` shop B cho kết quả là shop A;
  - token service: sai khoá, ký bằng khoá user, hết hạn hoặc rác đều bị `Unauthenticated`; dùng sai RPC thì bị `PermissionDenied`.
- [grpc_trust_test.go (product)](../services/product/internal/server/grpc_trust_test.go) chạy stack gRPC thật (bufconn, interceptor và ProductService giống cmd/product):
  - `CreateCategory` (RPC admin) với `x-user-role: admin` giả, không JWT: `Unauthenticated`.
  - `CreateProduct` (RPC shop) với `x-user-role/x-shop-id` giả: `Unauthenticated`.
  - JWT shop A kèm `x-shop-id: shop-b`: sản phẩm thuộc `shop-a`. Nếu body ghi rõ `shop_id: shop-b` thì bị `PermissionDenied`.
  - `ReleaseStock` với token service sai khoá, token hết hạn hoặc không có token: `Unauthenticated`. Với token hợp lệ thì được nhận.
  - Token service gọi `CreateCategory`: `PermissionDenied`.
- [order clients_test.go](../services/order/internal/clients/clients_test.go): lời gọi của người mua chỉ mang JWT người mua, lời gọi nội bộ mang token service hợp lệ.
- Payment `auth::tests` (4 test) và `grpc::tests::test_grpc_spoofed_metadata_without_jwt_is_unauthenticated`.

## 4. Kết quả kiểm tra

| Lệnh | Kết quả |
|---|---|
| `make lint` (buf lint, gofmt và golangci-lint trên 10 module Go, `cargo fmt --check`, `cargo clippy --all-targets -- -D warnings`) | **PASS** (`LINT EXIT 0`) |
| `cd tests/e2e && gofmt -l . && golangci-lint run && go vet ./...` | **PASS** |
| `make test` (`go test -race ./...` trên 10 module, `cargo test`) | **PASS** (`TEST EXIT 0`; payment 31/31) |
| `docker compose config \| grep -c 'published: "5005'` | **0** |
| `make up` với `REDIS_PORT=26379 OTEL_HTTP_PORT=24318 GATEWAY_PORT=28080 PRODUCT_HTTP_PORT=28082 CART_HTTP_PORT=28083 ORDER_HTTP_PORT=28085` | **PASS**: 14/14 container chạy, mọi service ở trạng thái `healthy`, gateway `0.0.0.0:28080->8080`, các cổng 5005x chỉ còn hiện dạng `50051/tcp` (không có ánh xạ ra host) |
| `ss -ltn \| grep ':5005[0-9]'` trên host | **trống** |
| `make e2e` | **PASS 8/8** (8.06s): Health, COD, VietQR, Idempotency, Webhook, FlashSale 1000/100, SagaCompensation, Ledger |

Saga bù trừ trả lại 100% (stock, voucher, xu) và luồng COD/VietQR đều chạy được. Điều này xác nhận order → product/promotion/payment đi được bằng token service. Ngoài ra đã chạy một probe tạm trong network compose rồi xoá file: kết quả trên service thật đúng như mong đợi:

```
payment GetLedgerSummary spoofed admin metadata            -> Unauthenticated
user GetProfile spoofed metadata                           -> Unauthenticated
promotion ReleasePromotion spoofed metadata                -> Unauthenticated
payment GetPayment forged service token (wrong key)        -> Unauthenticated
payment GetPayment expired service token                   -> Unauthenticated
payment GetLedgerSummary valid service token (not allowed) -> PermissionDenied
payment GetPayment valid service token (allowed)           -> NotFound
user GetProfile valid service token (allowed)              -> NotFound
user AdminListUsers valid service token (not allowed)      -> PermissionDenied
```

Probe cũng xác nhận token do Go ký được Rust xác minh đúng.

## 5. Phần còn lại / rủi ro đã biết

1. Vẫn dùng một khoá HS256 chung cho mọi service. Bất kỳ service nào giữ `SERVICE_JWT_SECRET` đều ký được token service, và token không gắn với kết nối mạng cụ thể. Đã ghi `ponytail:` ở `auth.go:125` và `auth.rs:4`. Hướng nâng cấp: mTLS nội bộ, hoặc JWT EdDSA riêng cho từng service + JWKS. Tương tự, khoá JWT người dùng vẫn là HS256 đối xứng (review §4).
2. Token service mang role `admin`. Interceptor chỉ chấp nhận nó ở đúng danh sách RPC nội bộ, nhưng ở những RPC đó thì handler coi nó như admin (ví dụ `GetAddress` và `GetProfile` đọc được của bất kỳ user nào). Đây là quyền mà order/notification thực sự cần.
3. Một số handler vẫn coi người gọi không có claims là nội bộ đáng tin, ví dụ `ProductService.UpdateStock`, `CartService.ClearItems`, `ShippingService.CreateShipment`, `UserService.ValidateUser`, và `resolveUserID` trong promotion. Từ host thì không còn với tới được vì cổng đã đóng, nhưng trong network compose vẫn gọi ẩn danh được. Cách vá rẻ nhất: thêm các RPC này vào `internalRPCs` sau khi đã truy xem order gọi chúng với ctx nào. Phần này để cho giai đoạn sau, vì phải lần từng luồng gọi.
4. Downstream chỉ kiểm chữ ký và hạn của JWT người dùng, không kiểm thu hồi `jti`. Chỉ gateway kiểm việc này, như trước đây.
5. Vẫn công bố ra host như cũ, ngoài phạm vi tiêu chí 5005x: các cổng HTTP ops của từng service (808x: healthz, metrics, webhook nội bộ của payment), postgres 5432, redis, nats 4222/8222, otel 4317/4318/8889, mailpit. Nên bind `127.0.0.1` hoặc bỏ hẳn ở bước sau.
6. Cách chạy e2e đã đổi. `make e2e` giờ chạy trong container `golang:1.22-alpine` trên network compose, không còn chạy từ host với `GATEWAY_URL=http://localhost:28080`. Các override cổng host chỉ còn ảnh hưởng tới `make up`. Nếu chạy `go test` trực tiếp từ host, bài 06 sẽ fail vì gRPC không còn với tới được, và đó là hành vi đúng.
