# Đợt 2b – agent `product` (sở hữu `services/product/**`, `tests/e2e/27_*.go`)
Theo `w2b-plan.md` §3, §8. Migration `000005_`.
1. Outbox ở product (mẫu `order`): `product.reviewed` trong cùng transaction với AdminReviewProduct; relay NATS, tương thích luật stream §8.
2. `sku_price_history` (ghi khi tạo/đổi giá, backfill giá hiện tại), `min_price_30d_vnd` trong GetSKUsBatch/GetProduct (giá thấp nhất hiệu lực 30 ngày gần nhất, gồm giá đang hiệu lực trước mốc).
3. CAT-B8 (xoá danh mục: 409 rõ ràng khi còn sản phẩm kể cả đã xoá mềm, hoặc chuyển hướng an toàn), CAT-B9 (cấm vòng lặp có khoá, lọc cha gồm mọi cấp con bằng recursive CTE, chuẩn hoá + duy nhất slug, UpdateCategory dùng presence flag không reset).
4. GetCategoryDescendants; SetCategoryFeeSchedule/ListCategoryFeeSchedules: `effective_from` ≥ hiện tại, không sửa quá khứ; commission hiệu lực tại thời điểm hỏi trả trong SKU.commission_rate_bps.
Test: unit + e2e 27 (duyệt sản phẩm → event phát, vòng lặp danh mục, lọc cha 3 cấp, lịch phí đến hạn đổi bps mà không đổi đơn cũ).
