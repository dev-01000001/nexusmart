# Q lượt 1 — bổ sung cho `q-qa.md` (đọc `q-qa.md` + `common.md` trước)

Trạng thái: W, P, M1, M2 đã xong và commit (`3ecdb871` backend, `f496b6c5` web). P2 (giải ngân sớm, `p2-escrow-release.md`) và M3 (ảnh cũ,
`m3-product-legacy-images.md`) đang chạy. Stack audit CHƯA dựng lại web mới ⇒ lượt này **KHÔNG chạy gì trên stack** (không make e2e/e2e-web/seed,
không chạy journey/ui-flows vào cổng 43000, không docker). Chỉ viết code test + chạy `node --check`/lint file test. Manager sẽ dựng lại stack rồi
nối tiếp phiên này để bạn chạy gate (Việc 2 của `q-qa.md`).

## Việc 1a — `tests/ui-flows/journey.mjs` (theo `q-qa.md` Việc 1), mẫu: `.runs/shopify-e2e/journey/journey.mjs` (+ `render.sh`)
Bỏ hai chỗ vòng lỗi: (1) s04 đăng xuất/đăng nhập lại khi nav còn "Đăng ký gian hàng" — nay sau khi admin duyệt, mở `/seller` phải tự đồng bộ
phiên (`web/src/features/seller/shell/SellerSessionSync.tsx`) và hiện menu người bán trong ≤ 20 s, không thì FAIL "F1"; (2) xoá 3 SKU áo mẫu —
nay form sản phẩm mới trống (`web/src/features/seller/ProductFormView.tsx`, `ProductFormView.variants.ts`): tạo tùy chọn "Màu men" với 2 giá trị
"Men lam", "Men rạn" ⇒ 2 biến thể, điền giá/tồn/khối lượng từng dòng (đọc nhãn thật trong source, locator role + chữ).
Thêm/sửa bước:
- s04 thêm **tải ảnh lên** bằng `MediaUploader` (`web/src/components/app/MediaUploader.tsx`, `setInputFiles` vào input file của nó) với 2 ảnh
  PNG/JPEG sinh tại chỗ (dùng `sharp` nếu `web/node_modules` có, hoặc PNG tối thiểu tự dựng như `zlib` — miễn ảnh hợp lệ ≥ 64×64); soát API:
  sản phẩm vừa tạo có `images[0]` dạng `/media/shops/<shop_id>/<sha256>.(png|jpg)` và `GET http://<site host>/media/...` trả 200 `image/*`.
- s06 (Site Studio) thêm: "Tải ảnh lên" cho Ảnh logo (`SingleMediaUploadButton`), xuất bản; soát trang site có `<img src="/media/shops/...">`.
- Sau s13 (hoàn tiền), thêm chuỗi tiền về người bán:
  - s15 người bán `/seller/tai-chinh` khai tài khoản ngân hàng (UI trong `web/src/features/seller/SellerFinanceClient.tsx`; tên chủ TK = họ tên
    người bán viết hoa không dấu hoặc có dấu như backend so khớp — xem `services/user` so `verified_owner_name`), OTP lấy từ Mailpit (email
    tiêu đề có "ngân hàng"), soát API `GET /api/v1/seller/bank-account` → `status: VERIFIED`.
  - s16 admin `/admin/doi-soat` thẻ "Giải ngân sớm" (P2 đang làm; nhãn chốt: tiêu đề "Giải ngân sớm", ô label "Mã đơn hàng", nút "Giải ngân",
    AlertDialog "Xác nhận giải ngân sớm") nhập id đơn COD đã COMPLETED; soát API `GET /api/v1/seller/balance` của người bán: `available_vnd` tăng
    đúng bằng `released_amount_vnd` (> 0), `pending_vnd` giảm tương ứng.
  - s17 người bán "Rút tiền" (dialog trong SellerFinanceClient) rút toàn bộ khả dụng; soát API: payout `PENDING`, balance `available_vnd` = 0,
    `payout_pending_vnd` = số rút.
  - s18 admin tab "Chi trả người bán" ở `/admin/doi-soat` → Duyệt + AlertDialog; soát API `GET /api/v1/admin/payouts?shop_id=` → `COMPLETED`,
    `transfer_ref` bắt đầu `MOCKBANK-`; balance người bán `payout_pending_vnd` = 0.
- Hợp đồng REST đầy đủ ở `docs/contracts.md` (tìm `seller/payouts`, `seller/bank-account`, `seller/media`, `escrow/release`).

## Việc 1b — fixture ảnh e2e
Luật ảnh mới (product) chỉ nhận `/media/shops/<own>/<sha256>.<ext>`, `/products/<file>`, và (sau M3) `/models/<file>`. Các file đang dùng
`https://sandbox.nexusmart.local/img/...` (đã thấy `tests/e2e/29_seller_payouts_test.go:90` → 400): `16_gateway_rules`, `18_product_rules`,
`27_product_w2b`, `33_notification_w2b`, `35_product_w3a`, `39_gateway_w3a`, `41_reviews_moderation_w3b`, `45_admin_rbac_approvals_w3b2`,
`47_search_b1_w3b3`, `fixtures_test.go`. Đổi sang ảnh có thật trong `web/public/products/` (chọn tên file có thật). Ca nào CỐ Ý kiểm URL ngoài
bị từ chối thì giữ và sửa kỳ vọng thành 400. Ảnh review (`product_reviews.image_urls`) theo luật riêng của review — đừng đổi nếu không lỗi.
Chạy `cd tests/e2e && go vet ./... && gofmt -l .` (biên dịch được, chưa chạy).

## Báo cáo
Danh sách bước journey (id, tiêu đề, phép soát API), file e2e đã sửa + lý do từng chỗ, những nhãn UI bạn không chắc (để manager soát trên trình duyệt).
