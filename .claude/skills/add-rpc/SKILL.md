---
name: add-rpc
description: Thêm hoặc sửa RPC gRPC trong NexusMart (proto, buf/make proto, handler Go hoặc Rust payment, kiểm quyền đối tượng, allowlist service token, REST gateway và docs/contracts.md). Dùng khi mở rộng API nội bộ hoặc công khai cho một service.
disable-model-invocation: true
---

# Thêm hoặc sửa RPC gRPC

Đầu vào (`$ARGUMENTS`): `<domain>.<RpcName>` (ví dụ: `order.CancelOrder`, `payment.CreateRefund`, `notification.MarkRead`).

## 1. Cập nhật Protobuf và sinh code

- [ ] Sửa `proto/nexusmart/<domain>/v1/<domain>.proto` (mẫu `proto/nexusmart/notification/v1/notification.proto`):
  - Tiền tệ bắt buộc `int64` với hậu tố `*_vnd` (cấm `float`/`double`).
  - Thời điểm dùng `google.protobuf.Timestamp`.
  - Định danh người bán dùng `shop_id` (không dùng `seller_id`).
- [ ] Chạy `buf lint` (theo `buf.yaml`) và `make proto` (`buf lint`, `buf generate` ra `gen/go/nexusmart/<domain>/v1/` theo `buf.gen.yaml`, rồi `cd gen/go && go mod tidy`).
- [ ] Nếu `<domain>` là `payment` (Rust): chạy `cd services/payment && cargo build` để `services/payment/build.rs` biên dịch lại `payment.proto` bằng `tonic-build` (cần `protoc` ở `~/.local/bin/protoc` hoặc `PROTOC`).

## 2. Implement handler đúng thư mục của service

Kiểm tra cấu trúc thực tế của service đích trước khi sửa:

| Service | File handler gRPC |
| --- | --- |
| `order` | `services/order/internal/grpcserver/server.go` (+ nghiệp vụ ở `internal/service/`) |
| `shipping` | `services/shipping/internal/grpcserver/server.go` |
| `notification` | `services/notification/internal/grpcserver/server.go` |
| `product` | `services/product/internal/server/server.go` |
| `cart` | `services/cart/internal/server/server.go` |
| `user` | `services/user/internal/service/server.go` |
| `promotion` | `services/promotion/internal/service/promotion.go` |
| `payment` (Rust) | `services/payment/src/grpc.rs` (`impl PaymentServiceTrait for PaymentGrpcServer`) |

- [ ] Implement method RPC mới, validate input trả đúng mã lỗi gRPC (`codes.InvalidArgument`, `codes.NotFound`, `codes.FailedPrecondition`...).
- [ ] Kiểm tra xác thực và **quyền theo đối tượng (object-level RBAC)**:
  - Go: đọc danh tính từ `auth.ClaimsFromContext(ctx)` (mẫu `resolveAuthorizedUserID` ở `services/notification/internal/grpcserver/server.go`), kiểm tra `claims.UserID()`, `claims.ShopID`, hoặc `auth.HasRole(claims, "admin", "super_admin")`. Cấm tin metadata `x-user-*`.
  - Rust (`payment`): gọi `CallerAuth::from_request(&self.auth, &request, "<RpcName>")` và các hàm `require_*` trong `services/payment/src/grpc.rs`, ghi nhận metrics qua `self.record_metrics(...)`.

## 3. Cho phép RPC nội bộ (nếu gọi bằng token service)

Chỉ làm bước này khi RPC được gọi từ saga, background job hoặc consumer không có JWT người dùng cuối:

- [ ] Go: thêm `"/nexusmart.<domain>.v1.<Domain>Service/<RpcName>": true` vào map `internalRPCs` trong `pkg/common/auth/auth.go` (đặt `false` nếu bắt buộc JWT người dùng và cấm anonymous lẫn service token). Cập nhật test trong `pkg/common/auth/grpc_trust_test.go`.
- [ ] Rust (`payment`): thêm tên RPC trần `"<RpcName>"` vào `SERVICE_METHODS` trong `services/payment/src/auth.rs` và bổ sung assertion trong unit test cùng file.

## 4. Test handler gRPC

- [ ] Viết unit test theo mẫu `services/notification/internal/grpcserver/server_test.go` (gọi trực tiếp với `auth.WithClaims`) và `services/product/internal/server/grpc_trust_test.go` (chạy qua `bufconn` + `auth.GRPCUnaryServerInterceptor` thật):
  - Không JWT / giả mạo metadata `x-user-*` → `Unauthenticated`.
  - Truy cập tài nguyên của user/shop khác → `PermissionDenied`.
  - Chủ sở hữu / `admin` → thành công.
  - Nếu là RPC nội bộ: service token hợp lệ được phép, sai key/hết hạn bị từ chối.

## 5. Expose REST ở API Gateway (nếu client cần)

- [ ] Thêm DTO và handler trong `services/gateway/internal/router/handlers_<domain>.go` (mẫu `services/gateway/internal/router/handlers_notification.go`):
  - Dùng `ctx, cancel := h.clients.OutgoingContext(r.Context())` để chuyển tiếp JWT người dùng và timeout.
  - Đọc JSON bằng `httpx.DecodeJSON`, trả lỗi bằng `httpx.WriteGRPCError`/`httpx.WriteError`, trả kết quả bằng `httpx.WriteJSON` (envelope phân trang chuẩn `{items, page, page_size, total}` nếu là danh sách).
- [ ] Đăng ký route đúng nhóm quyền (`Public`, `opt`, `authed`, `/seller`, `/admin`) trong `services/gateway/internal/router/router.go`.
- [ ] Thêm test route, RBAC và JSON response vào `services/gateway/internal/router/router_test.go` dùng fake gRPC client.

## 6. Cập nhật hợp đồng

- [ ] Cập nhật bảng REST tương ứng trong `docs/contracts.md` §4 (4.1–4.8) với method, path, Auth/RBAC, request và response JSON.

## Gate

```bash
make proto
make lint
make test
```

Nếu sửa `payment`, kiểm tra thêm:

```bash
cd services/payment && cargo build && cargo test
```
