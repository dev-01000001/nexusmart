# 3b-3 — Agent `shipping` (A8: G3–G5)

- **Sở hữu**: `services/shipping/**`. Đọc `w2-rules.md`, `w3b-plan.md` §5 (**G3–G5, thắng thiết kế**), `w3b-design-a8.md`, `docs/contracts.md`.
- **Cấm**: sửa `proto/`, `pkg/common/`, service khác, docker, `make up|seed|e2e|coverage`, git ghi. **KHÔNG thêm RPC sửa địa chỉ** (G3). **KHÔNG làm bản HTML gộp** (G5).
- **Migration**: `services/shipping/migrations/000007_w3b3_shipment_label_fields.sql` (**phải idempotent `IF NOT EXISTS`** vì runner chạy lại toàn bộ `.sql` khi khởi động): bổ sung `to_name`, `to_phone`, `to_street_address`, `from_name`, `from_phone`, `from_street_address TEXT NOT NULL DEFAULT ''` trên `shipments` để in nhãn PDF A6 đủ thông tin người gửi/nhận.

## Phạm vi bắt buộc
1. **Phân quyền `CreateShipment` cho G3 (`internal/grpcserver/server.go`)**:
   - `pkg/common/auth` đã bật `CreateShipment: true`. Handler cho phép: (a) `seller` đúng `claims.ShopID == req.ShopId`, (b) nhân viên có `orders.review.resolve`, hoặc (c) token service có đúng **`claims.Service == "order-service"`** (từ chối mọi service khác bằng `PermissionDenied`).
   - Lưu đầy đủ `from_address`/`to_address` vào `shipments` và `shipment_create_jobs`.
2. **`CreateShipmentRequest.enqueue_only` (UC-S15, G4)**:
   - Khi `enqueue_only == true`: kiểm tra điều kiện tiên quyết không thử lại được (vd thiếu phường GHN ở chế độ `GHN_MODE=sandbox` → `FailedPrecondition` ngay, 0 job; đơn đã có vận đơn sống → `already_existed=true`), ngược lại ghi thẳng vào bảng `shipment_create_jobs` hiện có của A5 và trả ngay `queued=true, job_id` mà **không** gọi hãng đồng bộ. Worker A5 có sẵn sẽ tạo vận đơn bất đồng bộ và phát `shipment.updated(status="created")` hoặc `shipment.create_failed`.
3. **In nhãn gộp PDF A6 tiếng Việt (`POST /labels/batch`, G5)**:
   - Dùng `github.com/signintech/gopdf` (MIT, chọn phiên bản chạy được trên Go `1.22.2`) + một phông TrueType OFL hỗ trợ đủ tiếng Việt (vd Noto Sans / Be Vietnam Pro / DejaVu Sans, nhúng qua `go:embed` kèm file `LICENSE` giấy phép OFL cạnh phông).
   - Mount `POST /labels/batch` (sau `auth.HTTPMiddleware(jwtMgr, true)`) trên HTTP `:8087`: từ chối service token (`401`/`403`); nhận `{"tracking_codes":[...]}` (`1..50` mã, sai → `400`).
   - Nạp toàn bộ vận đơn: thiếu mã nào → `404`; nếu caller là `seller` và có **dù chỉ 1 mã** không thuộc `claims.ShopID` → **`403` cả lô** (nhân viên cần `orders.read` hoặc `carriers.read`; `buyer` bị từ chối `403`).
   - Sinh 1 file `application/pdf` nhiều trang kích thước A6 (`105×148 mm`), mỗi trang 1 vận đơn đủ dấu tiếng Việt (mã vận đơn, mã đơn, hãng/dịch vụ, người gửi/nhận, tỉnh/phường, COD VND, khối lượng), header `Content-Disposition: inline; filename="shipping-labels-batch.pdf"`, `X-Content-Type-Options: nosniff`, `Cache-Control: private, no-store`.

## Tiêu chí đo được & Ca tấn công/đua bắt buộc
- Tấn công G3: token service `product-service` gọi `CreateShipment` → `PermissionDenied`; token `order-service` → thành công.
- Đua G4: `CreateShipment(enqueue_only=true)` đồng thời với `order.cancelled` / `CancelShipment` → job chuyển `cancelled`, không sinh vận đơn mồ côi.
- Tấn công G5: `POST /labels/batch` với 0 mã hoặc 51 mã → `400`; 4 mã shop A + 1 mã shop B gọi bằng JWT shop A → `403` cả lô; JWT buyer hoặc service token → `401`/`403`; PDF trả về bắt đầu bằng `%PDF-`, số trang = số mã hợp lệ, nhúng phông và giữ đúng chuỗi tiếng Việt có dấu (không lỗi ký tự).
- Kiểm: `gofmt`, `golangci-lint`, `go build ./...`, Postgres thật qua `bash scripts/test-db.sh --run` với `go test -race ./...` (0 SKIP) và test mới `-race -count=5`.
