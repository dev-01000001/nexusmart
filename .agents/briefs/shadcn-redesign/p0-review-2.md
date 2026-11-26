# Phase 0 — review vòng 2 (Claude, 26/11/2026) = brief vòng 3

Vòng 2 đạt 6/6 mục của `p0-review-1.md`. Mình đã tự chạy lại:
- Gate: lint 0 lỗi, format sạch, 507/507 test, check:data, build xanh. First Load JS route cũ không đổi.
- Tiền tố: 26/27/9, bằng mốc cộng `ui.css`.
- Site không tải `ui.css`, không preload Inter.
- So pixel: admin, seller, site và PDP khớp mốc; danh sách PDP đã hết thụt.
- Menu `?kieu=a1` chỉ sáng một mục; tiêu đề SetupGuide ở 390px đọc đủ.

Còn 4 lỗi dưới đây. Đây là vòng cuối của phase 0.

**Phạm vi.** Chỉ sửa 5 file:
- `web/src/components/app/AppShell.tsx`
- `web/src/components/app/navMatch.ts`
- `web/src/components/app/navMatch.test.mjs`
- `web/src/components/app/SetupGuide.tsx`
- `web/postcss.config.mjs`

**Cấm:**
- Đụng file của phiên trang chủ (danh sách ở `p0-foundation.md`).
- Chạy lệnh git ghi, `checkout`/`restore`/`stash`/`clean`/`rm`.
- **Chạy `next build`**: máy thiếu RAM, Claude sẽ tự build.
- Đụng `web/.next-qa`.

## 1. [Lỗi thật] React #418 (hydration) trên mọi `/design/app` ở khổ 390px
- **Kịch bản:** đăng nhập quản trị, mở `/design/app?kieu=a2` ở viewport 390×900. Console có
  `Minified React error #418 … args[]=HTML`. Ở 768 và 1440 không có lỗi; vòng 1 cũng không có.
  Tái hiện được bằng `W=390 node .runs/shadcn-redesign/hydration-diff.mjs 43102 "/design/app?kieu=a2" <out>`.
- **Nguyên nhân:** `AppShell.tsx:455-467` bọc **cả** `AppShellInner` (gồm `children`) trong `React.Suspense`, nằm dưới
  `SidebarProvider`. Route động nên `useSearchParams` không bail out.
  - Nhưng shell lớn hơn `progressiveChunkSize` của React (12.8 KB), nên Fizz tách boundary ra stream sau. HTML server có
    `<!--$?--><template id="B:0">`: gửi shell fallback (`search=""`) trước, rồi gửi thêm cả shell thật.
  - Boundary đó hydrate **sau** khi effect `useIsMobile()` của `SidebarProvider` đã đổi sang `true` (dưới 768px). Kết
    quả render không khớp HTML server, nên ra #418.
  - Lỗi phụ: khi JS chưa chạy, HTML đầu là fallback, nên mục đang mở không sáng (`data-active="false"`) ở mọi khổ. Mỗi
    trang còn mang shell hai lần.
- **Sửa (cách đơn giản nhất):**
  - Xoá `AppShellSearchBridge` và `React.Suspense`, gọi `useSearchParams()` thẳng trong `AppShellInner`.
  - Bỏ prop `search` truyền tay, `AppShell` render `<AppShellInner {...props} />` như vòng 1.
  - Mọi route dùng AppShell đều động (seller, admin, `/design` đều cần phiên), nên Next cho phép. Nếu sau này một route
    tĩnh dùng AppShell, `next build` sẽ báo lỗi rõ ràng; thế là đủ.
  - **Không** dùng `suppressHydrationWarning`, không khởi tạo `useIsMobile` từ `window`, không dời `Suspense` sang chỗ
    khác quanh shell.

## 2. [Lỗi thật] Menu vẫn sáng 2 mục cấp 1 ở 3 link demo
- **Kịch bản:** `node .runs/shadcn-redesign/nav-active.mjs 43102` duyệt mọi link sidebar. Hiện 3 dòng `BAD`:
  ```
  /design/app?kieu=a1&muc=tra-hang    top=["Trả hàng","Sản phẩm & Kho hàng"]
  /design/app?kieu=a4&muc=khuyen-mai  top=["Khuyến mãi","Tài chính & Báo cáo AI"]
  /design/app?kieu=a5&muc=tuong-tac   top=["Tương tác","Hồ sơ gian hàng"]
  ```
- **Nguyên nhân:** `isNavItemActive` khớp khi query của href là **tập con** của query hiện tại. Mỗi mục được xét riêng,
  nên `?kieu=a4` (Tài chính) cũng khớp `?kieu=a4&muc=khuyen-mai`.
  - Phase 1 cũng gặp lỗi này ở mục con, ví dụ `/seller/san-pham` và `/seller/san-pham?view=ton-kho` cùng sáng.
  - Test hiện tại chỉ xét từng mục nên không bắt được.
- **Sửa:**
  - Thêm vào `navMatch.ts` một hàm thuần, export, ví dụ
    `resolveActiveNav(items, pathname, search, homeHref) → { activeHref: string | null; activeChildHref: string | null }`.
    `items` có kiểu tối thiểu `{ href; match?; children?: { href }[] }`.
  - **Điểm của một href:**
    - `null` nếu `isNavItemActive` trả false;
    - nếu khớp: `[số tham số query của href đã khớp, độ dài path của href]`, so theo thứ tự từ điển.
  - **Mục cấp 1:**
    - điểm của mục = điểm cao nhất giữa chính nó (theo `match` của nó) và các mục con (theo `"prefix"`, như hiện tại);
    - chỉ mục điểm cao nhất được sáng;
    - hoà thì mục đứng trước trong danh sách thắng (ghi trong comment).
  - **Mục con:** cùng luật, xét các con của mục đang sáng (theo `"exact"`, như hiện tại); chỉ một con sáng.
  - `AppShell.tsx` gọi resolver một lần cho cả danh sách. Nhóm làm phẳng `groups.flatMap(g => g.items)`, giữ thứ tự.
    Thay 3 chỗ gọi `isNavItemActive` ở dòng ~299, ~308, ~369 bằng so sánh với kết quả resolver.
  - Không cần sửa `mobileNav` của trang demo: đó là slot do khu vực tự dựng. Phase 1 sẽ dùng `resolveActiveNav` cho
    thanh dưới của người bán.
- **Test:** thêm vào `navMatch.test.mjs` một test resolver trên **cả danh sách**:
  - chép đúng 13 href của `SELLER_NAV_GROUPS` trong `DesignAppClient.tsx`;
  - với mỗi href làm URL hiện tại, assert đúng một `activeHref` như mong đợi; gồm 3 URL `BAD` ở trên và
    `?kieu=a1&view=ton-kho` (cha "Sản phẩm & Kho hàng", con "Tồn kho theo SKU");
  - một ca kiểu phase 1: các con `/seller/san-pham` + `/seller/san-pham?view=ton-kho`, URL `?view=ton-kho`, chỉ con
    thứ hai sáng.

## 3. [Lint] `postcss.config.mjs` export ẩn danh
- Mẫu mình đưa ở vòng 1 là `export default { … }`, nên lint báo `import/no-anonymous-default-export` (warning mới).
- Sửa: `const config = { … }; export default config;`, giữ nguyên nội dung plugin và comment.

## 4. [HTML] `<div>` trong `<button>`
- `SetupGuide.tsx:145`: `<button>` chỉ được chứa phrasing content. Đổi `<div className="flex min-w-0 flex-1 flex-col …">`
  thành `<span>`, giữ nguyên class. Class `flex` đã làm nó thành block, nên hình không đổi.

## Kiểm cuối của bạn
```bash
cd web && npm run lint && npm run format:check && npm run test:unit && npm run check:data
```
- Lint phải **0 problem**, kể cả warning.
- Không chạy `next build` (Claude tự build `.next-qa` rồi chạy `design-shots.mjs`, `hydration-diff.mjs`, `nav-active.mjs`).

**Báo cáo:**
- file đã sửa theo mục;
- dòng tổng kết của từng lệnh;
- chữ ký và luật hoà của `resolveActiveNav`;
- chỗ làm khác brief và lý do.
