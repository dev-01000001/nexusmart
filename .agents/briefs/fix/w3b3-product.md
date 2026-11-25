# 3b-3 — Agent `product` (Tuần tự trong 1 phiên: A8 trước, B1 sau)

- **Sở hữu**: `services/product/**`. Đọc `w2-rules.md`, `w3b-plan.md` §5 (**G6, G8, G9, G11**), `w3b-design-a8.md`, `w3b-design-b1.md`, `docs/contracts.md`.
- **Cấm**: sửa `proto/`, `pkg/common/`, service khác, docker, `make up|seed|e2e|coverage`, git ghi.
- **Migration**: `services/product/migrations/000010_w3b3_stock_import_and_index.sql` (gộp sạch 1 file cho cả A8 và B1):
  1. `CREATE INDEX IF NOT EXISTS idx_stock_history_sku_created ON stock_history (sku_id, created_at DESC, id DESC);`
  2. `product_import_jobs` (`id PK, shop_id, created_by, filename, raw_csv BYTEA, status, total_rows, valid_rows, invalid_rows, created_rows, failed_rows, error_message, claimed_until TIMESTAMPTZ, claim_token TEXT, confirmed_at, finished_at, created_at, updated_at`) + partial index `WHERE status IN ('QUEUED','CONFIRMING')`.
  3. `product_import_rows` (`id PK, job_id FK, row_number INT, status, error_code, error_message, product_name, sku_code, category_id, price_vnd BIGINT, stock INT, weight_grams INT, created_product_id, raw_row_json JSONB`, `UNIQUE(job_id, row_number)`).
  4. `CREATE INDEX IF NOT EXISTS idx_products_public_id ON products (id ASC) WHERE deleted_at IS NULL AND status IN ('APPROVED','OUT_OF_STOCK');`
  Chạy `sqlc generate` sau khi sửa `queries/product.sql`.

## Pha 1 — A8 (hoàn thành trước)
1. **Dùng `pkg/common/textnorm`**: chuyển `internal/store/search.go` sang gọi `textnorm.{FoldVietnameseRune,NormalizeSearchText,BuildProductSearchText,EscapeLikePattern}` (giữ hành vi không đổi, test cũ xanh).
2. **`ListStockHistory` (S03, G8)**: lọc `product_id`/`sku_id`/`change_type`/`from_time`/`to_time`, phân trang, sắp `(created_at DESC, id DESC)`. Seller luôn ép `shop_id = claims.ShopID`; tra `sku_id`/`product_id` gian khác → `PermissionDenied` (403). Staff cần `products.read`.
3. **Nhập CSV sản phẩm (`Create/Get/ListRows/ConfirmProductImportJob`, UC-S13, G6)**:
   - Chỉ **tạo mới** (cập nhật hàng loạt để lại, ghi báo cáo). UTF-8 (chấp nhận BOM), `≤ PRODUCT_IMPORT_MAX_BYTES` (1 MiB), `≤ PRODUCT_IMPORT_MAX_ROWS` (1.000 dòng). Sai UTF-8 / sai tiêu đề / quá 1.000 dòng / quá 1 MiB → `InvalidArgument` cả file với mã `IMPORT_INVALID_UTF8` / `IMPORT_INVALID_HEADER` / `IMPORT_TOO_MANY_ROWS` / `IMPORT_FILE_TOO_LARGE`.
   - Luôn hai bước (không có tự xác nhận; `auto_confirm` sẽ bị gỡ khỏi proto). `raw_csv` xoá về NULL khi job kết thúc.
   - Worker nền có lease (`FOR UPDATE SKIP LOCKED` + `claimed_until`/`claim_token`, chết giữa chừng chạy lại an toàn không nhân đôi): bước 1 validate từng dòng (tên, giá `int64 > 0`, tồn `>= 0`, cân nặng, danh mục tồn tại & không cấm, từ cấm BLOCK/REVIEW, trùng SKU trong file/DB) → `READY`; bước 2 `ConfirmProductImportJob` chuyển nguyên tử `READY -> CONFIRMING` dưới `FOR UPDATE` (bấm 2 lần trả `already_confirmed=true`, không tạo trùng; nếu không `READY` hoặc 0 dòng hợp lệ → `FailedPrecondition` `IMPORT_JOB_NOT_READY`) → tạo sản phẩm ở trạng thái `PENDING` qua đúng luật từ cấm/danh mục cấm (G6).

## Pha 2 — B1 (sau khi Pha 1 xanh)
4. **`ListProductsForIndex` (G9, G11)**: keyset `WHERE id > $1 AND deleted_at IS NULL AND status IN ('APPROVED','OUT_OF_STOCK') ORDER BY id ASC LIMIT $2` (`page_size <= 500`); chỉ cho token service hoặc staff có `search.reindex`/`products.read` (buyer/seller → `PermissionDenied`). Trả đủ `IndexableProduct` (1..23, gồm `rating_avg_x100`, `review_count`, `default_sku_id`, `shop_suspended`, `category_ancestor_ids`, `colors`, `sizes`); cấm `commission_rate_bps`/`flagged_keywords`.
5. **Outbox đồng bộ `search` (G9, G11)**: phát trong cùng TX giữ khoá `GetProductByIDForUpdate` (và thứ tự khoá nhất quán `products -> skus` trên luồng tồn kho để tránh deadlock; `LockCategoryTree` trên mọi thao tác danh mục):
   - `product.upserted`: tại `AdminReviewProduct(APPROVE)`, `UpdateProduct` (đang công khai), `UpdateStock`/`ConfirmStock`/`RestockOrderItems` khi `status` hoặc `in_stock` đổi, và `CreateReview` khi đổi `rating_sum`/`review_count` (G9). Payload KHÔNG có `commission_rate_bps`/`flagged_keywords`.
   - `product.deleted`: tại `DeleteProduct`, `AdminReviewProduct(REJECT)`, rescan `REJECT`, và `UpdateProduct` chuyển rời trạng thái công khai (`HIDDEN`/`PENDING`).
   - `product.category_updated`: tại `CreateCategory`, `UpdateCategory`, `DeleteCategory` (`aggregate_id = "cat:"+id`).

## Tiêu chí đo được & Ca tấn công/đua bắt buộc
- Tấn công A8: seller B đọc `ListStockHistory` / `GetProductImportJob` / `ListProductImportRows` / `ConfirmProductImportJob` của seller A → `PermissionDenied`; CSV 1.001 dòng / sai header / > 1 MiB → `InvalidArgument`; dòng chứa từ cấm BLOCK hoặc danh mục cấm bị đánh `INVALID`, không tạo được SP.
- Đua A8: 2 `ConfirmProductImportJob` song song trên job 50 dòng → đúng 50 SP tạo mới, 0 trùng; mô phỏng worker hết lease chạy lại → không nhân đôi SP.
- Tấn công & đua B1: buyer/seller gọi `ListProductsForIndex` → `PermissionDenied`; response & payload outbox không chứa `commission_rate_bps`/`flagged_keywords`; `UpdateProduct` + `DeleteProduct` + `UpdateStock` chạy song song (`-race -count=5`) → 0 deadlock, outbox `seq` giữ thứ tự.
- Kiểm: `gofmt`, `golangci-lint`, `go build ./...`, Postgres thật qua `bash scripts/test-db.sh --run` với `go test -race ./...` (0 SKIP) và test mới `-race -count=5`.
