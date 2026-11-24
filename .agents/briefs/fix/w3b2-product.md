# 3b-2 — Agent `product` (A7)

- **Sở hữu**: `services/product/**`. Đọc `w2-rules.md`, `w3b-plan.md` §4 (F9, F12), `w3b-design-a7.md`, `docs/contracts.md`.
- **Cấm**: sửa `proto/`, `pkg/common/`, service khác, docker, `make up|seed|e2e|coverage`, git ghi.
- **Migration**: `services/product/migrations/000009_shop_suspended_and_consumer.sql`:
  - `products`: thêm `shop_suspended BOOLEAN NOT NULL DEFAULT FALSE`, `shop_status_version BIGINT NOT NULL DEFAULT 0`, partial index `(shop_id) WHERE shop_suspended = TRUE`.
  - Tạo bảng `processed_events (event_id TEXT PRIMARY KEY, event_type TEXT NOT NULL, processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW())` và `dead_letter_events` (cùng schema với `order` theo E1/E2). Chạy `sqlc generate` sau khi sửa `queries/product.sql`.

## Phạm vi bắt buộc
1. **Consumer `user.shop_status_changed` (F12 + E1)**: thêm NATS JetStream durable `QueueSubscribe` consumer trong `services/product/internal/events/` theo đúng mẫu E1 (`stopping` flag, đợi handler xong, KHÔNG `Drain()`/`Unsubscribe()`, DLQ context tách rời + PubAck, Nak nếu cả 2 nơi lưu DLQ lỗi, `MaxDeliver` > ngưỡng DLQ).
   - Xử lý idempotent qua `processed_events(event_id)` trong cùng DB transaction.
   - Guard theo `version`: chỉ cập nhật `shop_suspended = (to_status == "SUSPENDED")` và `shop_status_version = version` cho mọi sản phẩm của `shop_id` khi `version > shop_status_version` (event cũ đến sau bị bỏ qua an toàn, không ghi đè trạng thái mới).
   - **Bất biến F12**: KHÔNG đổi cột `products.status` gốc (`APPROVED`, `HIDDEN`, `PENDING`, `REJECTED`, `OUT_OF_STOCK`). Khi mở lại (`ACTIVE`), sản phẩm từng `APPROVED` hiện lại công khai, sản phẩm người bán tự ẩn (`HIDDEN`) vẫn giữ nguyên `HIDDEN`.
2. **Ẩn sản phẩm & chặn thao tác người bán khi `shop_suspended = true` (F12)**:
   - Public `ListProducts` / `GetProduct` (kể cả theo slug): lọc bỏ `shop_suspended = TRUE` (sản phẩm của shop bị khoá trả `404` công khai, không trả discontinued stub nếu chưa bị ẩn/xoá? Lưu ý: kiểm tra nhất quán với E13 — không lộ sản phẩm đang bán của shop bị khoá).
   - `GetSKUsBatch`: với SKU thuộc sản phẩm có `shop_suspended = TRUE`, trả `is_active = false` và ẩn chi tiết cho non-owner/non-admin theo D2 (để `cart`/`order` báo `SKU_INACTIVE`).
   - Seller `CreateProduct`, `UpdateProduct`, `UpdateStock` khi shop đang có cờ `shop_suspended` (lưu bảng/trạng thái shop hoặc kiểm qua `UserService.GetShop` / `shop_suspended` trên sản phẩm): trả `FailedPrecondition` `SHOP_SUSPENDED` (không cho tạo/sửa sản phẩm mới khi shop bị khoá — F12; dùng `UserService.GetShop` cho `CreateProduct` khi shop chưa có sản phẩm nào).
   - Trả trường proto `Product.shop_suspended` cho chủ shop và admin (luôn `false` trên công khai).
3. **RBAC theo `pkg/common/auth` (F9)**: thay mọi kiểm tra `admin|super_admin` bằng `auth.HasCapability`:
   - `ListCategories` (admin commission view): `categories.read`; `Create/Update/DeleteCategory`: `categories.write`.
   - `ListCategoryFeeSchedules`: `fees.category.read`; `SetCategoryFeeSchedule`: `fees.category.write`.
   - `ListProducts`/`GetProduct` (admin scope): `products.read`; `AdminReviewProduct`: `products.review`.
   - `ListBannedKeywords`/`PreviewBannedKeyword`: `banned_keywords.read`; `Create/Update/DeleteBannedKeyword`/`RescanProductsForBannedContent`: `banned_keywords.write`.

## Tiêu chí nghiệm thu & Test bắt buộc (gồm ca tấn công/đua)
- Out-of-order F12: gửi `user.shop_status_changed(SUSPENDED, v=2)` rồi `ACTIVE(v=1)` → sản phẩm vẫn `shop_suspended = true`; gửi `ACTIVE(v=3)` → sản phẩm `APPROVED` hiện lại công khai, sản phẩm seller tự `HIDDEN` vẫn `HIDDEN`.
- Replay/idempotency + DLQ (E1): lặp cùng `event_id` → 1 lần xử lý; payload thiếu `shop_id` → vào `dead_letter_events` + `NEXUSMART_DLQ`.
- Seller bị khoá shop: `CreateProduct` / `UpdateProduct` / `UpdateStock` → `FailedPrecondition` `SHOP_SUSPENDED`.
- Ma trận RBAC F9: 6 vai trò × mọi RPC admin của `product` (dương & âm: `moderator` duyệt SP/từ cấm OK nhưng tạo danh mục/đổi phí → `PermissionDenied`; `accountant` đổi phí danh mục OK nhưng duyệt SP → `PermissionDenied`; `ops` tạo danh mục OK nhưng duyệt SP/đổi phí → `PermissionDenied`).
- Kiểm: `gofmt`, `golangci-lint`, `go build ./...`, Postgres thật `bash scripts/test-db.sh --run` với `go test -race ./...` (0 SKIP) và test mới `-race -count=5`.
