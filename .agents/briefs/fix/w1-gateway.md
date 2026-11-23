# Đợt 1 – pha 1B – agent `gateway`

Luật chung: `rules.md`. Hợp đồng mới: `OrderService.RetryPayment`. Chi tiết lỗi: `docs/audit/plt.md`, `docs/audit/usr.md` (USR-B4), `docs/audit/cat.md` (CAT-B2).

## Sở hữu
`services/gateway/**`, `tests/e2e/16_*.go`; trong `docker-compose.yml` và `.env.example` CHỈ thêm biến `TRUSTED_PROXIES` cho service gateway.

## Việc
1. R9 (PLT-B1): `ClientIP` chỉ đọc `X-Forwarded-For`/`X-Real-IP` khi `RemoteAddr` thuộc danh sách proxy tin cậy `TRUSTED_PROXIES` (CIDR hoặc tên host phân giải định kỳ; compose đặt `TRUSTED_PROXIES=web`); mặc định rỗng = chỉ dùng `RemoteAddr`. Khi tin XFF, lấy địa chỉ gần nhất không thuộc proxy tin cậy (duyệt từ phải sang trái), không lấy hop đầu.
2. PLT-B2: lỗi gRPC Internal/Unknown/DataLoss trả thông điệp chung "internal server error" (log bản gốc kèm trace_id); giữ thông điệp cho lỗi phía client (InvalidArgument, NotFound, FailedPrecondition...).
3. R8 (USR-B4, CAT-B7, PLT-B11): middleware cho mọi request thay đổi dữ liệu (POST/PUT/PATCH/DELETE) dưới `/api/v1/admin/` trả 2xx → gọi `UserService.WriteAuditLog` (actor, role từ claims đã xác minh; action = method + mẫu route; target lấy từ tham số route; IP = `ClientIP` đã sửa ở mục 1). Không chặn phản hồi khi ghi audit lỗi, nhưng log lỗi và thử lại có giới hạn. Bỏ qua các route mà `user` service đã tự ghi audit (liệt kê trong code) để không ghi trùng.
4. R3: `POST /api/v1/payments/{id}/retry` gọi `OrderService.RetryPayment` bằng JWT người mua, rồi `PaymentService.GetPayment` payment mới để trả đúng shape JSON cũ (web không phải đổi). Bỏ gọi `CreatePayment` trực tiếp ở handler.
5. CAT-B2: `GET /api/v1/products` và các route sản phẩm công khai luôn chỉ trả sản phẩm công khai, kể cả khi có token admin/seller (admin dùng `/api/v1/admin/products`, seller dùng `/api/v1/seller/products`).
6. PLT-B5: `RequestLogger` ghi được `user_id` (sửa thứ tự middleware).

## Test bắt buộc
Unit test: `ClientIP` với/không proxy tin cậy và chuỗi XFF nhiều hop; ánh xạ lỗi Internal; middleware audit gọi WriteAuditLog đúng tham số, bỏ qua GET và route của user; retry handler gọi RetryPayment. E2E `tests/e2e/16_gateway_rules_test.go`: lỗi Internal không lộ thông điệp nội bộ; admin duyệt sản phẩm → có bản ghi audit; token admin gọi danh sách sản phẩm công khai không thấy PENDING.
