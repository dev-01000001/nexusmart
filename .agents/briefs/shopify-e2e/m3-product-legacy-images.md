# Swarm M3 — Luật ảnh sản phẩm mới chặn sửa sản phẩm cũ (hồi quy)

Đọc trước: `.agents/briefs/shopify-e2e/common.md`. Commit `3ecdb871` thêm luật ảnh ở `services/product/internal/server/server.go`
(`validateProductImageURL` ~2654, gọi ở CreateProduct, UpdateProduct ~869-877, CSV import `import_job.go`): chỉ nhận `/media/shops/<shop của
mình>/<sha256>.<ext>` hoặc `/products/<file>.<jpg|jpeg|png|webp>`.

## Lỗi
DB `product_db` của stack audit (giống dữ liệu thật đã seed): ảnh sản phẩm 12.489 `/products/…`, **17.727 `/models/…`** (ví dụ
`/models/model-ao-thun-oversize.jpg`), **1.136 `https://sandbox.nexusmart.local/…`**; `skus.image_url` tương tự (17.399 `/models/`, 884 `https://`).
Form sửa sản phẩm của web gửi lại toàn bộ `images` + `skus[].image_url` ⇒ người bán sửa giá/tồn của bất kỳ sản phẩm cũ nào bị 400
"must be a /media/shops/... or /products/... image path" / "contains invalid path characters".

## Sửa (đơn giản nhất, không nới luật cho ảnh mới)
1. `/models/` là thư mục ảnh tĩnh đóng gói sẵn (`web/public/models/`), như `/products/` (`web/public/products/`): nhận cùng điều kiện (một
   tầng tên file, đuôi jpg/jpeg/png/webp, không `..`, `//`, `?#`, `\`).
2. UpdateProduct: URL ảnh (sản phẩm hoặc SKU) **đã có sẵn trên chính sản phẩm đó** (`existing.Images`, `image_url` các SKU hiện có) thì được giữ
   nguyên dù không theo luật mới (dữ liệu cũ `https://…`); ảnh mới thêm vẫn qua `validateProductImageURL`. Vẫn kiểm độ dài/ký tự điều khiển.
   Không áp dụng cho CreateProduct và CSV import (ảnh mới phải theo luật).
3. Cập nhật câu mô tả luật trong `docs/contracts.md` (mục product images, tìm "`/products/`") cho khớp.

## Test (bắt buộc)
- Unit: `/models/x.jpg` nhận; `/models/../x.jpg`, `/models/a/b.jpg` từ chối; UpdateProduct giữ nguyên `https://legacy/...` đã có → OK; thêm
  `https://evil/...` mới → 400; SKU giữ `image_url` cũ → OK.
- Nếu có test pg integration UpdateProduct sẵn thì thêm một ca dữ liệu cũ.

## Được sửa
`services/product/**`, `docs/contracts.md` (chỉ đoạn luật ảnh). KHÔNG sửa `services/gateway/**`, `pkg/**`, `web/**` (swarm khác đang sửa).

## Kiểm (phải xanh, dán kết quả)
`cd services/product && gofmt -l . && golangci-lint run ./... && go test -race ./...`
