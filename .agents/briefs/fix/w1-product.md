# Đợt 1 – pha 1B – agent `product`

Luật chung: `rules.md`. Chi tiết lỗi: `docs/audit/cat.md`. Skill: `add-migration`.

## Sở hữu
`services/product/**`, `tests/e2e/18_*.go`.

## Việc
1. R10 (CAT-B1): tìm kiếm không phân biệt dấu: "ao thun" phải ra "Áo thun", "dam" ra "Đầm" (xử lý cả `đ`/`Đ`); thoát `%` và `_` trong từ khoá. Chọn cách đơn giản nhất chạy được với Postgres 16 của compose (ví dụ cột chuẩn hoá không dấu cập nhật khi tạo/sửa + backfill trong migration `000004_`, hoặc `unaccent` nếu chắc chắn tạo được extension); giữ các bộ lọc và phân trang hiện có.
2. CAT-B4: `PUT /api/v1/seller/products/{id}` khi đổi tồn kho SKU phải đi cùng đường với `PATCH /skus/{id}/stock`: kiểm version (có version trong payload thì so; không có thì dùng version đọc trong cùng transaction), ghi `stock_history`, đồng bộ trạng thái `OUT_OF_STOCK`/`APPROVED`. Không phá form seller hiện có.
3. CAT-B6: thêm lại biến thể có `sku_code` trùng một SKU đã INACTIVE của cùng sản phẩm → kích hoạt lại bản ghi cũ (cập nhật trường), không lỗi 409.
4. CAT-B11: `PATCH` tồn kho cho SKU INACTIVE → 409.
5. CAT-B10: admin từ chối sản phẩm bắt buộc có lý do (400 nếu rỗng).

## Test bắt buộc
Unit test cho 1–5 (kể cả hàm bỏ dấu). E2E `tests/e2e/18_product_rules_test.go`: tìm "ao thun" ra kết quả; PUT đổi tồn ghi `stock_history` và đổi trạng thái khi về 0; thêm lại SKU cùng code thành công.
