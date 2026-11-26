# S1c — Người bán: Site Studio (phase 1, đợt 2)

Đọc trước: `common.md`, spec §9 A5/A7/A8, §10 (`/seller/site`, `/seller/site/xem-truoc`, `/seller/site/ten-mien`),
§11.1, §17 mục 2 và 11, Figma `06`.
Mẫu mã: `DesignAppClient.tsx` (A7, A8).
Shell do S1a làm, `SiteRenderer` trong khung xem trước do S4a làm: ảnh nghiệm thu chỉ chụp khi đã có cả
`signals/seller-shell.done` và `signals/site-shell.done`.

## Phạm vi (chỉ những file này; file khác là cấm)
- `web/src/app/(seller)/seller/site/**` (`page.tsx`, `ten-mien/page.tsx`, `xem-truoc/page.tsx` + `page.module.css`)
- `web/src/features/seller/site/`:
  - `SellerSiteEditorClient.tsx`, `SellerSiteEditor.module.css`, `SectionEditorPanel.tsx`;
  - `SellerSiteDomainsSection.tsx`, `SellerSiteDomains.module.css`;
  - `SitePreviewContent.tsx`, `SitePreviewFrame.tsx`, `TemplateCodeEditorTab.tsx`, `TemplateGalleryTab.tsx`.
- File mới chỉ trong `web/src/features/seller/site/` (thư mục này cả cây là của S1c).
- **Chỉ đọc:** `previewData.ts`, `siteEditorTypes.ts`, `siteEditorUtils.ts`, `siteServer.ts`, `siteSummary.ts`,
  `src/lib/site/**`. S4a đổi **giá trị** font trong `theme.ts`; bạn chỉ import.

## Việc
- **`/seller/site` (A7):**
  - toàn màn trong `AppShell`, menu trái thu icon;
  - thanh trên: tên site + `StatusBadge` (nháp / đã xuất bản), [Lưu nháp] [Xuất bản];
  - gom 6 tab cũ thành 5 chế độ (`Tabs`), **giữ đủ chức năng** như bảng A7:
    - **Mẫu** (A8, xem trước bằng `Dialog` lớn);
    - **Tuỳ chỉnh:** cột trái 360px chứa `Accordion` (màu + cảnh báo tương phản `checkThemeContrast`, **font (thêm ô chọn
      font theo §17.2**, lấy danh sách từ export của `theme.ts`), logo / banner, thông báo, bố cục 4 trang phụ, slots,
      section / block của 5 trang); bên phải là xem trước với nút 390 / 768 / 1440;
    - **Mã JSON:** đánh số dòng + bảng lỗi bấm để nhảy dòng + xem trước trực tiếp;
    - **Tên miền:** A5; ô subdomain chuyển về đây;
    - **Phiên bản:** A1 + [Khôi phục] có `AlertDialog`.
- **`/seller/site/xem-truoc`:** giữ trong khung người bán, khung xem trước + nút 390 / 768 / 1440.
- **`/seller/site/ten-mien`:** A5 + bảng bản ghi DNS (`Table`), nút sao chép giá trị.
- **A8 thẻ mẫu:**
  - 3 cột; nhãn "Đang dùng";
  - tên 24px, mô tả, ảnh / khung xem trước bo 8px;
  - [Dùng mẫu này] full width, danh sách đặc điểm;
  - không có giá tiền.

## Xong khi
- `rg "module.css|styles\." <phạm vi>` rỗng.
- Lệnh kiểm `common.md` §4 xanh trong phạm vi, kể cả `siteEditorUtils.test.mjs`, `siteSummary.test.mjs` (không sửa
  logic).
- Ảnh 390 / 768 / 1440 cho 3 route và 5 chế độ, kèm `Dialog` xem trước mẫu, `AlertDialog` khôi phục, bảng lỗi JSON.
- **Luồng đã đi.** Bản nháp của shop người bán là **dùng chung** với S4a. Trước khi lưu thử, ghi lại bản nháp gốc; xong
  thì trả về đúng như cũ. **Không xuất bản.**
  - đổi qua 5 chế độ;
  - đổi màu → thấy cảnh báo tương phản khi màu kém;
  - đổi font → xem trước đổi font;
  - sửa JSON sai → bảng lỗi, bấm nhảy dòng;
  - xem trước 3 khổ;
  - mở `Dialog` mẫu;
  - tên miền: thêm bản ghi `qa-s1c-….example.com` nếu luồng cho phép xoá lại, không thì chỉ mở form;
  - phiên bản: mở `AlertDialog` khôi phục rồi huỷ.
