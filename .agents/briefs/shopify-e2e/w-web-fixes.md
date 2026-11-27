# Swarm W — Sửa lỗi hành trình trên web (F1, F1b, F2, F4, F6)

Đọc trước: `.agents/briefs/shopify-e2e/common.md`, `web/CLAUDE.md`, skill `design-system`.

Manager chạy hành trình thật trên Chrome (stack audit `http://nexusmart.localhost:43000`, site `http://<sub>.nexusmart.localhost:43000`) và
thấy các lỗi dưới. Sửa gốc, không vá triệu chứng. KHÔNG đổi proto/gateway/service Go/Rust (swarm khác đang sửa); nếu thật sự cần backend thì
dừng và báo.

## F1 — Duyệt shop xong, người bán vẫn bị coi là buyer
Hiện tại: người mua đăng ký → khai hồ sơ mở gian hàng ở `/seller` (`web/src/features/seller/SellerShopOnboarding.tsx`) → admin duyệt → người bán
vào `/seller/san-pham/moi` thấy "Không thể tải thông tin sản phẩm — Bạn không có quyền thực hiện thao tác này", menu vẫn "Đăng ký gian hàng".
Phải tự đăng xuất/đăng nhập. Nguyên nhân: access token cũ mang `role=buyer`, không có shop. `RefreshToken` của user service ĐÃ đọc lại role và
shop từ DB (`services/user/internal/service/server.go:773-840`, `SignAccessToken(user.ID, user.Role, effectiveSellerShopID(user))`).
Cần:
- Khi trang onboarding thấy shop đã `ACTIVE` (lần tải trang, hoặc đang mở trang lúc admin duyệt — có thể thăm dò nhẹ khi tab được focus/30 s),
  BFF làm mới phiên bằng refresh token (theo cơ chế xoay vòng hiện có trong `web/src/lib/server/session*`/`web/src/app/api/auth/**`), rồi tải
  lại để menu người bán hiện đủ. Hiện trạng thái "Gian hàng đã được duyệt" + nút "Bắt đầu bán hàng" (→ `/seller`).
- Lưới an toàn: trang/route BFF khu `(seller)` nhận lỗi quyền do claims cũ mà người dùng có shop ACTIVE thì làm mới phiên một lần rồi thử lại;
  không lặp vô hạn, không nới quyền (quyền vẫn do JWT mới quyết định).
- Không đổi thời hạn token, không đọc role từ client.

## F1b — Đã đăng nhập mà mở `/dang-nhap` thì về `/cua-hang`
Đã đăng nhập vào `/dang-nhap` (host gốc) → điều hướng theo vai trò: seller (có shop hoặc đang onboarding) → `/seller`, vai trò quản trị → `/admin`,
buyer → `/` (hoặc `?next=` hợp lệ cùng host như hiện có). Tìm chỗ đang chuyển `/cua-hang` và sửa đúng chỗ.

## F2 — Cả nền tảng gắn cứng ngành may mặc
Thấy trên hành trình của một xưởng gốm:
- `web/src/features/seller/ProductFormView.tsx`: form "Thêm sản phẩm mới" điền sẵn 4 SKU "Trắng tinh / S, M, L, XL — 189.000₫", mô tả quần áo
  ("đồ mặc hằng ngày… co rút… giặt"), ảnh áo thun chọn sẵn; trục biến thể cố định "Màu sắc" + "Kích cỡ" (combobox "Size 2XL").
- Giỏ/thanh toán/chi tiết đơn hiện "Màu: Men lam Size BỘ 7 MÓN" (nhãn cứng + viết hoa giá trị).
- Đổi trả (`web/src/features/account/OrderDetailClientView.tsx`): lý do "Sản phẩm có lỗi may hoặc bề mặt vải", gợi ý "đổi size", placeholder
  "Áo còn nguyên tem mác…".
- Còn nữa trong: `web/src/features/site/SiteInfoView.tsx`, `web/src/features/site/templateCatalog.ts`, `web/src/features/seller/site/siteEditorUtils.ts`,
  `web/src/features/seller/site/SellerSiteEditorClient.tsx`, `web/src/app/(seller)/seller/chat/SellerChatClient.tsx`, `web/src/lib/site/types.ts`
  (grep `Trắng tinh|lỗi may|Áo còn nguyên|đổi size|chất vải|giặt`). `web/src/data/*.ts` là dữ liệu mẫu cho test/mock: chỉ sửa nếu UI thật đọc nó.
Cần (mô hình như Shopify, KHÔNG đổi proto — SKU đã có `attributes map<string,string>`):
- Sản phẩm mới bắt đầu TRỐNG: 1 dòng biến thể mặc định ("Mặc định", không thuộc tính), không mô tả mẫu, không chọn sẵn ảnh.
- Tối đa 3 "tùy chọn" do người bán tự đặt tên (ví dụ "Màu men", "Quy cách", "Dung tích") và giá trị; sinh tổ hợp biến thể như Shopify (cho
  sửa giá/tồn/khối lượng/mã SKU từng dòng, xoá dòng). Lưu vào `attributes` với khoá = tên tùy chọn người bán nhập. Gợi ý nhanh (chip) "Màu sắc",
  "Kích cỡ" vẫn được, nhưng không bắt buộc.
- Tương thích ngược: sản phẩm cũ dùng khoá `color`/`size`/`color_code` (seed 20.000 SP, tìm kiếm lọc màu dùng `color`/`color_code`) — hiển thị
  `color`→"Màu sắc", `size`→"Kích cỡ", ẩn `color_code`; khoá khác hiện đúng tên. Không viết hoa giá trị. Sửa form vẫn giữ nguyên khoá cũ.
- PDP site (`web/src/features/site/**` chọn biến thể): nhóm tùy chọn dựng động từ khoá `attributes` của các SKU, không giả định màu/size.
- Giỏ, thanh toán, chi tiết đơn (buyer, seller, admin), email/phiếu nếu web dựng: một hàm định dạng dùng chung "Tên: giá trị · Tên: giá trị".
- Lý do đổi trả trung tính: "Sản phẩm lỗi hoặc hư hỏng", "Giao sai sản phẩm hoặc phân loại", "Không đúng mô tả", "Thiếu hàng hoặc phụ kiện",
  "Lý do khác" (giữ giá trị mã gửi backend nếu backend kiểm danh sách — kiểm `services/order` trước, KHÔNG sửa backend; nếu backend ràng buộc
  chuỗi cũ thì báo).
- Nội dung mặc định của mẫu site / chat trả lời nhanh: bỏ chữ chỉ đúng với may mặc khi hiện cho mọi shop (thay bằng câu trung tính); giữ tên mẫu.
- Ảnh: CHƯA làm tải ảnh lên (đang chờ duyệt ADR lưu trữ). Chỉ bỏ chọn sẵn ảnh áo và để thư viện ảnh mẫu là tùy chọn.

## F4 — Hàng đợi kiểm duyệt sản phẩm của admin bị trần 100
`web/src/app/(admin)/admin/kiem-duyet/AdminModerationClient.tsx:120` tải `/api/admin/products?page=1&page_size=100` rồi đếm/phân trang ở trình
duyệt: backend có 2.974 PENDING / 24.034 tổng nhưng UI hiện "Tất cả 100 · Chờ duyệt 87", không xem được sản phẩm thứ 101 trở đi. Sửa: lọc trạng
thái + phân trang + tìm kiếm phía server (gateway đã có `status`, `keyword`, `page`, `page_size` — xem `docs/contracts.md` mục admin products),
số trên tab lấy từ `total` của API (mỗi trạng thái một lời gọi `page_size=1`, hoặc cách gọn hơn nếu có), badge menu khớp số PENDING. Giữ phím
J/K và dialog kiểm duyệt.

## F6 — Wizard đổi trả bỏ qua bước xác nhận
`web/src/features/account/OrderDetailClientView.tsx` (~2764-2779): footer đổi nút "Tiếp theo" (`type="button"`) thành "Gửi yêu cầu đổi trả"
(`type="submit"`) cùng vị trí. Trên Chrome thật, bấm "Tiếp theo" ở bước 2 (Bằng chứng) là yêu cầu được GỬI LUÔN (toast "Đã gửi yêu cầu đổi trả…"),
khách không bao giờ thấy bước 3 "Xác nhận" (React tái dùng cùng nút DOM, `type` đổi sang submit trong lúc xử lý click ⇒ form submit). Sửa gốc
(ví dụ `key` khác nhau cho hai nút, hoặc chặn submit khi chưa ở bước 3 trong `onSubmit`), kiểm các wizard/sheet khác cùng mẫu (grep
`returnWizardStep|WizardStep|< 3 ?`) và thêm test.

## Được sửa
`web/src/features/seller/ProductFormView*`, `web/src/features/seller/SellerShopOnboarding*`, `web/src/features/seller/products/**`,
`web/src/app/(seller)/seller/san-pham/**`, `web/src/app/(seller)/seller/page.tsx`, `web/src/app/(seller)/layout.tsx`,
`web/src/features/seller/shell/**`, `web/src/features/account/**`, `web/src/features/site/**`, `web/src/features/commerce/**`,
`web/src/features/platform/auth/**`, `web/src/app/(shop)/dang-nhap/**`, `web/src/app/(site)/s/[host]/**` (chỉ hiển thị biến thể/đổi trả),
`web/src/features/seller/site/**`, `web/src/app/(seller)/seller/chat/**`, `web/src/app/(admin)/admin/kiem-duyet/**`,
`web/src/features/admin/shell/**`, `web/src/app/api/auth/**`, `web/src/app/api/admin/products/**`, `web/src/lib/server/session*`,
`web/src/lib/server/auth*`, `web/src/lib/site/**`, `web/src/lib/format*`, `web/src/components/app/**` (chỉ khi thêm hàm định dạng dùng chung),
test tương ứng trong `web/` (`*.test.mjs`).
CẤM: `web/src/features/seller/SellerFinance*`, `(seller)/seller/tai-chinh`, `(admin)/admin/doi-soat`, `web/src/app/api/seller/payouts`,
`web/src/app/api/admin/payouts`, `web/src/features/admin/AdminReconciliation*` (swarm P), mọi thứ ngoài `web/`.

## Kiểm (phải xanh, dán kết quả)
`cd web && npm run lint && npm run format:check && npm run test:unit && npm run check:data`, `NEXT_DIST_DIR=.next-w npm run build`
(hoàn nguyên tsconfig/next-env). Test mới: hàm định dạng biến thể (khoá cũ/mới), sinh tổ hợp biến thể, chuyển hướng theo vai trò, điều kiện
làm mới phiên (không lặp). Nhãn e2e-web đang bám: grep `tests/e2e-web` và `tests/ui-flows` chữ bạn đổi, liệt kê trong báo cáo (đừng sửa test ở đó).
