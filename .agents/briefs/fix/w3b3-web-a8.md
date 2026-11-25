# 3b-3 — Agent `web-a8` (A8 + lỗi nhỏ 3b-2 `AdminApprovalsPanel`)

- **Sở hữu (KHÔNG ĐỤNG FILE CỦA `web-b1`)**:
  - `web/src/lib/a8-types.ts` (tạo mới cho DTO A8 — **KHÔNG** sửa `gateway-types.ts` hay `lib/catalog.ts`)
  - `web/src/app/(seller)/seller/don-hang/**`, `web/src/app/(seller)/seller/san-pham/**`
  - `web/src/features/account/OrderDetailClient.tsx`, `web/src/features/account/AccountMockTabs.tsx` (**KHÔNG** sửa `serverData.ts`)
  - `web/src/features/commerce/OrderLookupView.tsx`, `web/src/mock/guestOrderLookup.ts`
  - `web/src/features/checkout/CheckoutSuggestionStrip.tsx`, `web/src/features/catalog/SizeFinder.tsx`
  - `web/src/data/vouchers.ts`, `web/src/data/membership.ts`, `web/src/mock/MockStoreContext.tsx`, `web/src/app/(shop)/hoi-vien/page.tsx`, `web/scripts/check-data.mjs`, `tools/seed/seed.mjs`
  - `web/src/features/admin/AdminApprovalsPanel.tsx`
  - `tests/e2e-web/13_a8_buyer_seller.test.mjs` (**KHÔNG** sửa `tests/e2e-web/helpers.mjs`; đặt helper riêng trong file `13_`).
- **Đọc**: `web/CLAUDE.md`, skill `design-system`, `w2-rules.md`, `w3b-plan.md` §5 (**G1–G8**), `w3b-design-a8.md`, `docs/contracts.md`.
- **Cấm**: sửa `proto/`, `services/`, `pkg/common/`, file thuộc `web-b1`, docker, `make up|seed|e2e|e2e-web`, git ghi.

## Phạm vi bắt buộc
1. **Sửa lỗi 3b-2 `AdminApprovalsPanel.tsx`**: dòng 149–176 thêm `id` vào `fetchedUserIdsRef.current` TRƯỚC khi `fetchShopOwnerById` xong; khi effect chạy lại đặt `cancelled = true` làm bỏ kết quả nhưng `id` vẫn nằm trong `fetchedUserIdsRef`, khiến tên rơi về mã 8 ký tự tới lúc tải lại trang. Chỉ đánh dấu đã tra sau khi nhận kết quả (hoặc gỡ khỏi ref trong cleanup/catch khi bị huỷ) để tên nhân viên luôn hiện đúng.
2. **UC-B08 `/tra-cuu-don-hang` (G7)**: gỡ hoàn toàn mock `guestOrderLookup.ts` và 4 mã giả `NM-261120-*`; nối `POST /api/gw/orders/lookup` (`order_code`, `phone`); hiển thị trạng thái/timeline thật và chỉ phường/tỉnh (không số nhà/SĐT đầy đủ/nhãn); xử lý `404` và `429`.
3. **UC-B26 `OrderDetailClient.tsx` (G1–G3)**: với đơn `CONFIRMED`/`PREPARING`, thêm nút/modal "Sửa địa chỉ" gọi `PATCH /api/gw/orders/{id}/address` 2 bước (`preview_only=true` trước → hiển thị chênh lệch phí). COD tăng phí: cảnh báo `+X₫`, nút "Giữ địa chỉ cũ" và "Xác nhận trả thêm" gửi `accept_fee_increase=true` + `expected_new_shipping_fee_vnd` (nếu `409 ADDRESS_FEE_CHANGED` → hiển thị báo giá mới để xác nhận lại). Trả trước tăng phí: báo rõ giữ địa chỉ cũ hoặc huỷ đặt lại (không phụ thu); bằng hoặc giảm: ghi rõ tổng tiền đã thanh toán giữ nguyên.
4. **S04 & UC-S15 `/seller/don-hang` (G4–G5, G8)**: phân trang server (`page`, `page_size=20`, tổng thật, bỏ cứng `page_size=100`), hiện `status_counts` thật trên tab, thêm lọc hãng/phương thức/từ khoá/hạn giao; tick chọn lô ≤ 50 đơn, nút "Tạo vận đơn hàng loạt" (`POST /seller/orders/batch-ship`) hiển thị bảng `QUEUED`/`SKIPPED` + theo dõi `shipment_job_status`, nút "In danh sách nhặt hàng" (`POST /seller/orders/picking-list`) và nút "In nhãn gộp PDF" tải/mở `application/pdf` từ `POST /shipping/labels/batch`.
5. **S03 & UC-S13 `/seller/san-pham` (G6, G8)**: phân trang server (bỏ cắt 100 SP); thêm ngăn kéo "Lịch sử tồn" (`GET /seller/skus/{id}/stock-history` có lọc loại/thời gian, phân trang); thêm modal "Nhập file CSV" 2 bước (tải mẫu CSV, upload ≤ 1 MiB/1.000 dòng, poll tiến độ job, xem bảng lỗi theo dòng + nút tải `errors.csv`, nút "Xác nhận nhập dòng hợp lệ", ghi rõ chỉ hỗ trợ tạo mới, SP mới vào tab "Chờ duyệt").
6. **B15 gỡ 3 voucher hứa sai (G8)**: gỡ `EDU15`, `SINHNHAT50`, `NEXUSVIP12` khỏi `tools/seed/seed.mjs` (kèm gọi `DELETE` dọn trên volume cũ nếu còn tồn tại), `web/src/data/vouchers.ts`, `membership.ts`, `AccountMockTabs.tsx`, `MockStoreContext.tsx`, `hoi-vien/page.tsx`, và `web/scripts/check-data.mjs`.
7. **§10 giao diện giá (`CheckoutSuggestionStrip.tsx`, `SizeFinder.tsx`)**: hỗ trợ hiển thị giá đã giảm kèm giá gốc gạch ngang (`originalPrice` / discount prop từ dữ liệu mà `web-b1` truyền qua `catalog.ts` và các trang `(shop)`).
8. **E2E Web**: viết `tests/e2e-web/13_a8_buyer_seller.test.mjs` cho các kịch bản thật trên 390/768/1440 (manager chạy sau).

## Tiêu chí đo được & Kiểm tra
- Không còn `guestOrderLookup` trên `/tra-cuu-don-hang`; không còn `EDU15`/`SINHNHAT50`/`NEXUSVIP12` trong seed/vouchers; `AdminApprovalsPanel` không mất tên sau effect re-run; `npm run check:data`, `npm run lint`, `npm run format:check`, `npm run build` đều xanh.
