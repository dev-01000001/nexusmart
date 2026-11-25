# 3b-3 — Agent `qa` (E2E A8 + Search B1 + k6)

- **Sở hữu**: `tests/e2e/**` và `tests/k6/search.js` (chạy sau khi các service và `gateway` hoàn thành). Đọc `w2-rules.md`, `w3b-plan.md` §5 (**G1–G14**), `w3b-design-a8.md` §6, `w3b-design-b1.md` §6, `docs/contracts.md`, và mục "ca e2e cần có" của các agent 3b-3.
- **Cấm**: sửa `proto/`, `services/`, `pkg/common/`, `web/`, docker, `make up|seed|e2e|e2e-web|coverage`, git ghi.
- **File mới**: `tests/e2e/46_a8_buyer_seller_w3b3_test.go` (A8), `tests/e2e/47_search_b1_w3b3_test.go` (B1), `tests/k6/search.js` (tải ≥ 20.000 SP). Giữ trần thời gian e2e (tái dùng fixture buyer đã xác minh SĐT 1 lần, poll điều kiện thay vì sleep cứng).

## Kịch bản bắt buộc — `46_a8_buyer_seller_w3b3_test.go` (A8: G1–G8)
1. **UC-B26 Đổi địa chỉ trước lấy hàng (G1–G3)**:
   - Đơn COD `PREPARING` đã có vận đơn `mock_express`: `preview_only=true` → thấy chênh lệch phí, không đổi DB; áp dụng tăng phí thiếu `accept_fee_increase` → `409 ADDRESS_FEE_CONFIRMATION_REQUIRED`; truyền sai `expected_new_shipping_fee_vnd` → `409 ADDRESS_FEE_CHANGED` có `quote` mới; truyền đúng → `200`, `shipment_recreated=true`, mã vận đơn mới cập nhật, COD & `total_payable_vnd` tăng đúng delta, seller nhận thông báo `order.address_updated` (không chứa số nhà/SĐT), giao thành công & hoàn tất → `ledger/summary` cân (`is_balanced=true`). Kiểm thêm ca giảm phí COD → giảm tổng đúng.
   - Đơn trả trước (`VIETQR`/`ZALOPAY`) `CONFIRMED`: tăng phí → `409 ADDRESS_FEE_INCREASE_PREPAID` (giữ nguyên địa chỉ cũ); bằng/giảm phí → `200` giữ nguyên `total_payable_vnd`. Đơn `PENDING` / `SHIPPING` → `409 ADDRESS_UPDATE_NOT_ALLOWED`. Buyer khác → `403`.
2. **UC-S15 Xử lý đơn lô & nhãn PDF A6 (G4–G5)**: 5 đơn `CONFIRMED` (huỷ 1 đơn, gửi 4+1 vào `POST /seller/orders/batch-ship`) → 4 `QUEUED`, 1 `SKIPPED` (`ORDER_CANCELLED`), chờ worker A5 tạo đủ 4 vận đơn; `POST /seller/orders/picking-list` khớp tổng SL từng SKU; `POST /shipping/labels/batch` trả `application/pdf` hợp lệ (`%PDF-`) nhiều trang A6; lẫn 1 đơn/vận đơn của shop khác → `403` cả lô.
3. **UC-S13 Nhập CSV (G6)**: file 500 dòng (490 hợp lệ + 10 lỗi giá âm/sai danh mục/trùng SKU/từ cấm + 1 ô `=CMD|...`) → `202`, chờ `READY` (`490/10`), tải `errors.csv` đủ 10 dòng và ô công thức có tiền tố `'`, bấm `confirm` 2 lần đồng thời/lặp → tạo đúng 490 SP `PENDING` (0 trùng); sai header hoặc 1.001 dòng → `400`. Shop khác đọc/confirm job → `403`.
4. **UC-B08 Tra cứu khách (G7)**: `POST /orders/lookup` đúng `(order_code, phone)` → `200`, không chứa `street_address`/`email`/`label_url`/`buyer_id`/phí; sai mã vs sai SĐT → cùng `404 ORDER_LOOKUP_NOT_FOUND`; lần thứ 11/phút cùng IP và lần thứ 6/10 phút cùng mã đơn → `429` + `Retry-After`.
5. **S03, S04, B15 (G8)**: sửa tồn + đặt/huỷ → `GET /seller/skus/{id}/stock-history` đủ `MANUAL_UPDATE`/`RESERVE`/`RELEASE` (shop khác `403`); tạo 25 đơn seller → phân trang `page=1/2` + `status_counts` đúng; `GET /vouchers` không còn `EDU15`/`SINHNHAT50`/`NEXUSVIP12`.

## Kịch bản bắt buộc — `47_search_b1_w3b3_test.go` & `tests/k6/search.js` (B1: G9–G14)
1. **Đồng bộ & Rating thật (< 5s, G9, G11)**: tạo SP biến thể 2 mức giá (`PENDING`) → không hiện trên `/api/v1/search`; admin `APPROVE` → hiện trong `< 5s` kèm `min..max_price_vnd`, 0 `commission_rate_bps`; thêm review thật → `rating_avg_x100`/`review_count` cập nhật `< 5s`; sửa `HIDDEN`/`DELETE`/`REJECT` → biến mất `< 5s`. Kiểm `product.category_updated`.
2. **Tiếng Việt, gõ sai, đồng nghĩa & Hybrid RRF (G12)**: truy vấn `ao thun`, `duong`, `ao thn`, `ao phong`, `do lot` trả đúng SP; query mô tả ≥ 3 từ (`mode=auto`/`hybrid`) có `used_vector=true`; query mã SKU ưu tiên keyword.
3. **Gian bị khoá (G10)**: khoá gian (duyệt 2 người A7) → toàn bộ SP gian biến mất khỏi `GET /search` & `/search/suggest` trong `< 5s`; mở khoá → hiện lại trong `< 5s` (SP tự ẩn vẫn ẩn).
4. **Gợi ý, lịch sử & bảo mật (G14)**: `/search/suggest?q=ao` phản hồi `< 80ms`; user đăng nhập tìm kiếm → `/search/history` hiện mục → xoá 1 mục và xoá hết; buyer B xoá lịch sử buyer A bị chặn; filter injection `shop_id="x\" OR status=\"HIDDEN"` trả 0 kết quả/400.
5. **Reindex & RBAC (G11)**: `POST /admin/search/reindex` trả `202` → `GET .../reindex/{job_id}` đạt `COMPLETED`, không phục hồi SP đã xoá; kiểm quyền `super_admin`/`admin`/`ops` được phép, `moderator`/`cskh`/`accountant`/buyer/seller → `403`.
6. **`tests/k6/search.js`**: kịch bản tải tìm kiếm/lọc/gợi ý trên ≥ 20.000 SP với ngưỡng `http_req_duration` `p(95)<200` và `http_req_failed` `<0.005`.

## Kiểm tra
- `cd tests/e2e && gofmt -l . && go vet ./... && go test -c ./...` (không chạy `make e2e`; manager chạy sau).

## Bổ sung sau review của manager (bắt buộc có ca)
- Đổi địa chỉ: chỉ `address_id` (không còn `custom_address`); địa chỉ của người khác → `403/404`; hai lần bấm cùng lúc không có `Idempotency-Key` → một lần đổi; sau khi đổi, `order.address_updated` đúng cờ (`shipment_queued` khi có vận đơn cũ), tổng nhóm thanh toán và COD của vận đơn mới khớp `total_payable_vnd`.
- Tra đơn: body có dữ liệu thừa sau JSON (`{...} x`) vẫn bị tính vào giới hạn theo mã; khoá trùng/khác hoa thường không lách được.
- Nhập CSV: dòng có `\u0000` trong thuộc tính không làm kẹt job; job thứ hai khi job đầu đang chạy → `409 IMPORT_JOB_IN_PROGRESS`; tên tệp dài có dấu không hỏng UTF-8.
- Tìm kiếm: xoá/ẩn SP khi Meilisearch đang dừng, bật lại → SP không còn trong kết quả trong ≤ 60 s; `sort_by=price_asc` với truy vấn ≥ 3 từ không trả SP lạc đề; từ khoá không khớp SP nào (chuỗi số điện thoại, email) không bao giờ hiện ở `/search/suggest` của người khác; lọc cỡ "XL (60kg)" trả 200.
- Search dừng hẳn: `/api/v1/search` trả `degraded_mode=product_fallback`, vẫn áp lọc `size`, `color`, `in_stock_only`; gian bị khoá không hiện.
- Ca dừng/bật container không chạy trong `make e2e` (manager tự làm ở bước thử lỗi); ca nào cần thì ghi vào danh sách thử lỗi, không gọi docker từ e2e.
