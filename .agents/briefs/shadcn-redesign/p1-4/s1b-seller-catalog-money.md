# S1b — Người bán: hàng hoá + tiền (phase 1, đợt 2)

Đọc trước: `common.md`, spec §8.2, §9 A1/A3/A4/A5, §10 (các dòng của trang dưới), §13, Figma `04`.
Mẫu mã: `DesignAppClient.tsx` (A1 bảng có hàng mở rộng, A3 form + `SaveBar`, A5).
Shell do S1a làm: chỉ chụp ảnh nghiệm thu khi đã có `signals/seller-shell.done`.

## Phạm vi (chỉ những file này; file khác là cấm)
- `web/src/app/(seller)/seller/san-pham/**` (`page.tsx`, `page.module.css`, `moi/page.tsx`, `[id]/page.tsx`)
- `web/src/app/(seller)/seller/khuyen-mai/**`, `tai-chinh/**`, `bao-cao-ai/**`, `ho-so/**`
- `web/src/features/seller/`:
  - `SellerProductsClient.tsx`, `ProductFormView.tsx` + `.module.css`;
  - `SellerPromotionsClient.tsx`, `PromotionsDiscountProgramsTab.tsx`, `SellerFinanceClient.tsx`.
- File mới chỉ trong thư mục con mới `web/src/features/seller/catalog/` và `web/src/features/seller/money/`.
- **Chỉ đọc:** `SellerCommon.module.css`, `SellerUiPrimitives.tsx`, `sellerApiTypes.ts`, `types.ts`, `sellerServer.ts`,
  `sellerValidation.ts`, `sellerDedupe*`.

## Trang
| Route | Kiểu | Yêu cầu riêng (§10) |
|---|---|---|
| `/seller/san-pham` | A1 | Đúng Figma `04` nhưng **không** có checkbox lô, **không** có "Xuất CSV". Có: nhập CSV (`Dialog`), các tab của `SellerProductsClient`, lọc "sắp hết" (`?filter=…` giữ nguyên), xem nhanh (`Sheet`), hàng mở rộng sửa tồn từng SKU + [Lưu]. Tồn 0 màu `destructive` |
| `/seller/san-pham/moi`, `/seller/san-pham/[id]` | A3 | 2/3 + 1/3. Thẻ Thông tin / Hình ảnh / Biến thể & SKU / Giá; cột phụ Trạng thái + Phân loại. `SaveBar` khi có thay đổi. Lỗi dưới từng ô; bấm Lưu thì focus ô lỗi đầu tiên. Giữ upload ảnh, kiểm tra hiện có |
| `/seller/khuyen-mai` | A1 trong `Tabs` | Voucher / flash sale / chương trình giảm giá. Tạo / sửa bằng `Sheet` hoặc trang A3 tuỳ độ dài form |
| `/seller/tai-chinh` | `MetricStrip` + `Tabs` | Sổ cái (A1) + tài khoản ngân hàng (A5, **giữ luồng OTP**). Không thêm biểu đồ, lọc ngày hay xuất CSV |
| `/seller/bao-cao-ai` | A4 | Văn bản báo cáo trong thẻ, giữ cách tạo / tải báo cáo hiện có |
| `/seller/ho-so` | A5 | Khổ 800px, hàng 1/3 + 2/3, `SaveBar` |

## Xong khi
- `rg "module.css|styles\." <phạm vi>` rỗng, trừ phần chỉ đọc dùng chung (ghi rõ).
- Lệnh kiểm `common.md` §4 xanh trong phạm vi.
- Ảnh 390 / 768 / 1440 cho 7 route, mở thêm: `Sheet` xem nhanh, hàng sửa tồn, `Dialog` nhập CSV, `SaveBar` khi đang sửa,
  lỗi validate.
- Luồng đã đi:
  - đổi tab / lọc "sắp hết" / tìm / phân trang sản phẩm;
  - sửa tồn một SKU rồi trả lại số cũ;
  - tạo sản phẩm nháp `QA-S1b-…` (đủ ảnh, SKU, giá), sửa lại, kiểm `SaveBar` + lỗi;
  - nhập CSV: mở `Dialog`, tải file mẫu nếu có, không cần nhập thật;
  - khuyến mãi: đi qua 3 tab, mở form tạo rồi huỷ;
  - tài chính: 2 tab, mở form thêm tài khoản tới bước OTP nếu không làm hỏng tài khoản đang có (nếu có thì chỉ mở rồi huỷ);
  - báo cáo AI: mở;
  - hồ sơ: sửa một trường rồi trả lại.
