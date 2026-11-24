# 3a – agent `promotion` (sở hữu `services/promotion/**`, `tests/e2e/34_*.go`)
Theo `w3a-plan.md` D3, D4. Chi tiết lỗi: `w2b-review2-promotion-result.md` (Promo P0 #2, P0 #3, P2 #16).
1. D3: `ReservePromotion` chỉ nhận token service của `order`; bỏ nhánh không có `shop_items` nếu không còn nơi gọi (kiểm `rg`), còn thì giữ và cũng đòi token service. Test: JWT người mua, JWT admin, service khác, ẩn danh → từ chối và Redis/PG không đổi.
2. D4: `PreviewDiscounts` tự lấy giá/shop/danh mục; giá giả, shop sai, SKU ẩn → không ảnh hưởng hoặc lỗi. Product lỗi → `Unavailable`, không rơi về giá client.
3. Giới hạn mỗi người khi mất Redis: `CommitReservation` kiểm `per_user_limit` của voucher, suất flash và chương trình shop bằng PG trong cùng transaction (khoá đúng để hai checkout đồng thời của một người không vượt). Vượt → lỗi đúng mã cũ và hoàn Redis. Test Postgres thật + miniredis/Redis giả bị xoá giữa chừng, và test đua hai goroutine.
4. `UpdateVoucher`: khoá dòng trước khi đọc lượt dùng; có reservation đang RESERVED thì không đổi được luật (loại, giá trị, per_user_limit, danh mục, tắt ngân sách). Test đua với Reserve.
5. `AdjustVoucherKeys` lỗi sau commit: có đường tự phục hồi (job đối chiếu định kỳ hoặc outbox), đưa Redis về đúng số PG, không tăng suất quá quota. Khởi động lại cũng phải hội tụ.
6. Job nhắc voucher sắp hết hạn loại người đã dùng hết `per_user_limit`.
7. Test chéo shop: shop B gọi `UpdateShopDiscountProgram`/`EndShopDiscountProgram`/`UpdateVoucher` của shop A → 403, dữ liệu không đổi.
8. `make coverage` mục promotion ≥ 60% (nay 61,2%), không giảm.
E2E 34: gọi thẳng gRPC promotion (mẫu `26_`) với JWT người mua + giá giả → PermissionDenied; checkout thật có voucher vẫn chạy.
9. Bài chập chờn 50% (manager đo 15/30 lượt đỏ): `store_pg_test.go` `NonReplaceUpdatePersistsWarning` gán cảnh báo vào `cur.Items[0]` rồi đòi ở `skuA`, trong khi `Items` xếp theo `created_at, id` (UUIDv5 ngẫu nhiên theo SKU). Sửa bài test chọn item theo `SKUID == skuA` (không nới assertion); rà các bài Postgres khác có cùng kiểu dựa vào thứ tự. Chứng minh 50/50 lượt xanh trên Postgres thật.
