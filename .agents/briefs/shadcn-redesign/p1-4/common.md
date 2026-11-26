# Phase 1–4 — luật chung cho mọi swarm (đọc trước brief riêng)

> Người làm: **agy**, mỗi swarm một tiến trình. Mọi swarm dùng **chung một working tree trên `main`**. Người giao và
> review: Claude Code. agy **không commit, không push**. Không chạy `make *`, docker, `next build`.
> Chủ dự án đã duyệt `/design` ngày 26/11/2026 và cho chạy phase 1–4 song song.

**Spec:** `.agents/briefs/shadcn-redesign/design.md`.
- Đọc §5–§13c, §14–§16, cùng các mục brief riêng chỉ tới.
- Ảnh Figma ở `.agents/briefs/shadcn-redesign/figma/`.
- Mẫu dùng component có sẵn trong mã `/design`: `web/src/app/(design)/design/**`. `app/DesignAppClient.tsx` có 9 kiểu trang
  A1–A9, `site/DesignSiteClient.tsx` có khung site theo mẫu, `auth/` có `AuthSplit`.

Quy tắc dự án: `AGENTS.md`, `web/CLAUDE.md`, skill `design-system`.

## 1. Ràng buộc toàn cục

### Component
- Chỉ dùng component trong `web/src/components/ui/*` và `web/src/components/app/*`.
- Hai thư mục này **đóng băng**: không sửa, không `shadcn add`.
- Thiếu thứ gì thì ghi `THIẾU COMPONENT: <tên> — <dùng ở đâu, vì sao>` vào progress, rồi tạm dựng bằng primitive có sẵn
  trong file của mình.
- Component cũ ở `web/src/components/*` (`Button`, `Price`, `ProductCard`, `StateView`, `SvgChart`, `Icons`…):
  - không sửa;
  - file đã chuyển không import chúng nữa, trừ `NexusMark` trong `Icons`;
  - dùng bản mới: `Money` thay `Price`, `charts.tsx` / `KpiChartCard` thay `SvgChart`, `EmptyState` / `ErrorState` /
    `Skeleton` thay `StateView`, `ui/button` thay `Button`.

### CSS
- Một component **không** vừa dùng class Tailwind vừa dùng CSS Module.
- File đã chuyển không còn `import … .module.css` hay `styles.`.
- Không thêm CSS toàn cục. Không sửa `web/src/styles/*` (`ui.css`, `globals.css`, `tokens.css`).
- Không khai biến mới ngoài `--ui-*`. Selector của hệ mới luôn là `[data-ui="true"]`.
- `ui.css` chỉ import ở layout khu vực, do swarm chủ shell thêm.
- Màu chỉ qua tên Tailwind ánh xạ `--ui-*` (`bg-background`, `text-muted-foreground`, `border-border`, `bg-primary`…).
  Không hex rời. Site gian hàng lấy màu từ biến site (§11.1).

### Không thêm phụ thuộc
- Cấm `npm install` / `npm uninstall`.

### Chỉ đổi giao diện, không đổi logic
**Cấm sửa** các file logic sau (đọc thì được):
- `web/src/app/api/**`, `web/src/middleware.ts`;
- `web/src/lib/**`, trừ `src/lib/site/theme.ts` (chỉ S4a sửa, xem brief S4a);
- mọi `*server.ts` / `*.server.ts`, `*Helpers.ts`, `*Types.ts`, `types.ts`, `*Validators.ts`, `sellerValidation.ts`,
  `sellerReturns.ts`, `orderMeta.ts`, `orderView.ts`, `templateCatalog.ts`, `templateValidator.ts`, `siteEditorUtils.ts`,
  `checkoutErrors.ts`, `checkoutPayload.ts`, `cartEnrich.ts`, `chatClient.ts`, `sseParser.ts`, `assistantMarkdown.ts`,
  `StoreContext.tsx` (API);
- assertion logic trong `*.test.mjs` cạnh mã.

Cần đổi logic thì **không sửa**, chỉ ghi `CẦN SỬA LOGIC: file:dòng — lý do`. Claude sẽ quyết.

Test cạnh mã (`src/**/*.test.mjs`) trong thư mục của bạn:
- assertion về logic giữ nguyên;
- assertion đọc chi tiết CSS Module / tên class / component cũ mà bạn vừa thay thì được viết lại để kiểm **cùng bất
  biến** trên mã mới. Không xoá nếu không có cái thay.
- Ghi từng chỗ dạng `TEST VIẾT LẠI: file:dòng — bất biến`.

### Giữ đủ hành vi
- Mọi nút, form, bộ lọc, phân trang, thao tác lô, drawer/sheet, phím tắt, query `?…` trên URL, redirect, gọi BFF,
  trạng thái loading/lỗi/trống.
- **Giữ nguyên chữ hiển thị** của nút, tiêu đề, nhãn ô nhập (trừ chỗ spec đổi). Swarm QA tìm phần tử theo vai trò + tên
  hiển thị.
- Tách component nhỏ hơn thì được; bỏ chức năng thì không.

### Nội dung và a11y
- Nội dung theo §12; a11y và responsive theo §13.
- Mỗi trang đúng một `h1`; nút icon có `aria-label` tiếng Việt; vùng chạm ≥ 40px trên mobile; không tràn ngang ở 390.

### Toaster
- `Toaster` (sonner) đặt ở layout khu vực, do swarm chủ shell thêm. Trên site gian hàng nạp bằng `next/dynamic`.

## 2. Luật working tree chung (đã từng hỏng một lần)

**Ghi file**
- Chỉ ghi file trong phạm vi brief riêng. File mới chỉ tạo trong **thư mục con riêng** brief cho phép.
- Không `prettier --write` hay `eslint --fix` cho cả repo. Chỉ chạy trên **file của mình**.

**Lệnh git**
- Cấm git ghi: `add`, `commit`, `stash`, `checkout`, `restore`, `reset`, `clean`. Cấm `rm` file của người khác.

**File dùng chung, chỉ đọc, không xoá**
- `features/seller/SellerCommon.module.css`, `features/seller/SellerUiPrimitives.tsx`;
- `features/admin/adminShared.module.css`, `features/admin/AdminUi.tsx`, `features/admin/StatusPill.tsx`;
- `features/site/site.module.css`, `features/site/siteTemplates.module.css`, `features/commerce/commerce.module.css`.

File của mình thôi dùng chúng là đủ. Claude dọn ở vòng cuối.

**Xoá `.module.css`**
- Chỉ xoá file `.module.css` **riêng của mình** (nằm trong phạm vi brief).
- Trước khi xoá, `rg "<tên file>" web/src` phải không còn ai import.

**Export dùng chéo giữ nguyên chữ ký** (tên, props, kiểu)
- `SiteProductCard` (S4b), `AuthForms` (S3), widget `SiteChatWidget` / `SiteAssistantWidget` (S4d).
- Các export của `theme.ts`: S4a chỉ được đổi **giá trị** font.
- Barrel `index.ts`: mỗi file có đúng một chủ ghi trong bảng ở mục 3.

## 3. Bảng sở hữu (ai sửa gì)

| Swarm | Phạm vi |
|---|---|
| S1a người bán: shell + vận hành | `(seller)/layout.*`, `/seller`, đơn hàng, trả hàng, chat, tương tác, onboarding |
| S1b người bán: hàng hoá + tiền | sản phẩm, khuyến mãi, tài chính, báo cáo AI, hồ sơ |
| S1c người bán: Site Studio | `/seller/site/**`, `features/seller/site/**` |
| S2a quản trị: shell + vận hành | `(admin)/layout.*`, `/admin`, gian hàng, kiểm duyệt, sites, khiếu nại |
| S2b quản trị: cấu hình + tiền | flash sale, đối soát, cấu hình (7 tab), audit log |
| S3 nền tảng + đăng nhập | `(shop)/**`, `features/platform/**`, `AuthForms.*` (cây trang chủ để vòng sau) |
| S4a site: shell + mẫu | layout site, `SiteShell` / `SiteChrome` / `SiteRenderer`, trang chủ, giới thiệu, chính sách, lỗi, `theme.ts` (font) |
| S4b site: danh mục + PDP + tìm kiếm | `san-pham/**`, `tim-kiem`, `SitePdpView` / `SiteCatalogView` / `SiteSearchView` / `SiteProductCard`, `features/search/**`, `features/assistant/ProductReviewSummary.*` |
| S4c site: giỏ + thanh toán | `gio-hang`, `thanh-toan/**`, `flash-sale`, `voucher`, `features/commerce/**` (trừ `OrderLookupView.*`) |
| S4d site: đơn + tài khoản + widget | `don-hang/**`, `tra-cuu-*`, `tai-khoan`, 4 trang auth của site, `features/account/**` (trừ `AuthForms.*`), `OrderLookupView.*`, `features/chat/**`, `features/assistant/**` (trừ `ProductReviewSummary.*`) |
| S9 QA | `tests/**`, `web/scripts/*.test.mjs` |

Danh sách file chính xác nằm trong brief riêng. File không có trong danh sách của bạn là **cấm**.

## 4. Kiểm tra (chạy trong `web/`)

Lệnh nặng phải bọc `flock` để các swarm không cùng chạy một lúc (máy 16 GB):

```bash
cd web
flock /tmp/nm-web-ts.lock npx tsc --noEmit -p . 2>&1 | grep -E '<regex đường dẫn của bạn>'   # phải RỖNG
flock /tmp/nm-web-ts.lock npx eslint <các đường dẫn của bạn>                                   # 0 problem
npx prettier --check <các đường dẫn của bạn>                                                   # sạch
flock /tmp/nm-web-ts.lock node --test <test cạnh mã của bạn>                                   # xanh
flock /tmp/nm-web-ts.lock npm run test:unit 2>&1 | grep -E '^not ok|# (pass|fail)'             # xem mục dưới
```

- **"Xanh" nghĩa là:** 0 lỗi tsc, lint, test **trong file của bạn**. Lỗi tsc ở file swarm khác thì bỏ qua (họ đang sửa).
- **Test `web/scripts/*.test.mjs` vỡ vì bám CSS Module / tên class / component cũ trong phạm vi của bạn:** không sửa. Ghi
  `TEST VỠ: file:dòng — bất biến cần giữ` vào progress và báo cáo. S9 sẽ viết lại.
- **Không chạy `next build`.** Claude tự build bản production để nghiệm thu.
- **Xem trên trình duyệt:** dùng server dev chung do Claude chạy, `http://nexusmart.localhost:43110`.
  - Site: `http://<sub>.nexusmart.localhost:43110`, các sub `bach-dang`, `sai-gon`, `da-nang`, `ha-noi`.
  - Không tự chạy server khác, không khởi động lại server này. Server chết thì ghi vào progress và chờ.
  - **Bản cũ đóng băng** (commit `c265058`, cùng dữ liệu) ở cổng **43101**, dùng để đối chiếu hành vi cũ.
- **Chụp ảnh:**
  ```bash
  flock /tmp/nm-web-browser.lock node ../.agents/tools/ui-shots.mjs --port 43110 --login seller \
    --routes /seller,/seller/don-hang --out ../.runs/shadcn-redesign/p1-4/<swarm>/shots
  ```
  Đọc `report.txt` (HTTP, lỗi console / hydration, tràn ngang, số `h1`) và **tự xem ảnh**, so với spec + Figma.
- **Đi luồng:** Playwright ở `/home/tai/.npm/_npx/9833c18b2d85bc59/node_modules/playwright/index.mjs`, kênh `chrome`.
  Bấm qua các luồng chính của trang mình trên cổng 43110, vẫn bọc `flock /tmp/nm-web-browser.lock`.
- **Tài khoản dev** (stack audit, dữ liệu dùng thử, được tạo/sửa):
  - quản trị: `admin@nexusmart.local` / `AdminDevPassword123!`;
  - người bán: `seller@nexusmart.local` / `SellerDemo123!`;
  - người mua: `buyer@nexusmart.local` / `BuyerDemo123!`.
  - Đối tượng tạo thử đặt tên bắt đầu `QA-<swarm>`.
  - Không xoá shop, site hay sản phẩm đã seed. **Không đổi mẫu đã xuất bản** của 4 site seed.

## 5. Tín hiệu, tiến độ, báo cáo

**Tín hiệu**
- Swarm chủ shell (S1a, S2a, S4a) viết file `.runs/shadcn-redesign/p1-4/signals/<seller|admin|site>-shell.done` ngay
  khi shell của khu vực đã chạy và tự kiểm xong. Nội dung một dòng: đã có gì.
- Swarm khác trong khu vực chỉ chụp ảnh nghiệm thu sau khi có file này.

**Tiến độ**
- Ghi `.runs/shadcn-redesign/p1-4/<swarm>/progress.md` sau **mỗi trang**: xong / đang làm / còn lại, cùng các dòng
  `THIẾU COMPONENT`, `CẦN SỬA LOGIC`, `TEST VỠ`.
- Bị ngắt thì phiên sau làm tiếp từ file này.

**Báo cáo cuối**
1. Trang / file đã chuyển, theo route.
2. Dòng tổng kết từng lệnh kiểm ở mục 4.
3. Đường dẫn ảnh, kèm số vấn đề trong `report.txt`.
4. Luồng đã đi, mỗi luồng kèm kết quả.
5. `THIẾU COMPONENT`, `CẦN SỬA LOGIC`, `TEST VỠ`.
6. Chỗ làm khác spec và lý do.
7. `rg` chứng minh hai điều trong phạm vi: không còn `.module.css`, không còn import component cũ.
