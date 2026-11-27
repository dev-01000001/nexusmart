# Báo cáo Thiết kế & Nghiệm thu Redesign Giao diện Web — NexusMart

> Tài liệu nghiệm thu đợt chuyển đổi toàn bộ `web/` sang **shadcn/ui + Tailwind CSS v4** (2026-11-26 → 2026-11-27), theo đặc tả [`.agents/briefs/shadcn-redesign/design.md`](../.agents/briefs/shadcn-redesign/design.md) và [ADR-0011](../docs/adr/0011-shadcn-ui-tailwind-v4.md). Hướng dẫn làm việc hằng ngày nằm tại [`web/CLAUDE.md`](./CLAUDE.md) và [`.claude/skills/design-system/SKILL.md`](../.claude/skills/design-system/SKILL.md). Hồ sơ tích hợp và nghiệm thu các giai đoạn trước đợt redesign lưu tại [`docs/PHASE4_REPORT.md`](../docs/PHASE4_REPORT.md) và [`docs/WEB_INTEGRATION_REPORT.md`](../docs/WEB_INTEGRATION_REPORT.md).

---

## 1. Kiến trúc UI cuối sau redesign

- **Một thư viện UI thống nhất**: **shadcn/ui** (`web/components.json`: style `new-york`, gói `radix-ui`, icon `lucide-react`, `cmdk`, `sonner`) với **35 primitive** trong `web/src/components/ui/` và **15 pattern + 3 helper** trong `web/src/components/app/`.
- **CSS toàn cục, preflight & PostCSS**: Tailwind CSS v4 với `web/postcss.config.mjs` chỉ khai `"@tailwindcss/postcss": { optimize: false }` (không dùng `postcss-flexbugs-fixes` hay `postcss-preset-env`), **preflight bật toàn cục** (`@import "tailwindcss/preflight.css" layer(base);`), file CSS toàn cục duy nhất là `web/src/styles/ui.css` nạp ở `web/src/app/layout.tsx` (chứa nền `html`/`body`, nền site tối `html:has([data-site-root][data-site-theme-mode="dark"])`, `:focus-visible`, CSS Reveal/PageTransition). Biến và hoạt ảnh riêng trang chủ tách vào `web/src/features/platform/home/home-tokens.css` (scope `[data-platform-public="true"]` ở `web/src/app/(shop)/(public)/layout.tsx`) và `web/src/features/platform/home/home-keyframes.css`.
- **Token `--ui-*` & font**:
  - Toàn bộ màu ứng dụng (Frost & Slate + Teal `#0D7480`), 6 tông nhãn (`success`, `attention`, `warning`, `info`, `critical`, `neutral` cho cả chế độ sáng và tối), bo góc (`--ui-radius: 0.75rem`) và 3 bậc bóng (`shadow-card`, `shadow-button`, `shadow-overlay`) định nghĩa qua `--ui-*` và ánh xạ bằng `@theme inline`.
  - Phần ứng dụng dùng **Inter** (`web/src/lib/fonts.ts`, `preload: false`; `web/src/app/(shop)/(public)/layout.tsx` bật `axes: ["opsz"]` cho trang chủ `/`). Mọi số liệu và tiền VND (`Money` / `formatVnd`) dùng `tabular-nums`.
  - Site gian hàng giữ đủ **8 mẫu Template JSON v2** (gồm **2 mẫu nền tối**: `sport-active`, `luxury-maison`) và **5 font thật** (`Work Sans`, `Be Vietnam Pro`, `Inter`, `IBM Plex Sans`, `Plus Jakarta Sans`) qua `resolveSiteUiFontStack` / `buildSiteUiVars` (`web/src/lib/site/theme.ts`). `SiteThemeRoot` (`web/src/features/site/shell/SiteThemeRoot.tsx`) gắn `data-site-root` và `data-site-theme-mode="light"|"dark"`; bề mặt site giữ phẳng, chỉ overlay có bóng.
- **`UiScope` & tách bundle trên site**:
  - `UiScope` (`web/src/components/ui/ui-scope.tsx`) giữ vai trò cung cấp portal container (`usePortalContainer()`) cho Radix; không còn CSS reset bám `data-ui`.
  - Các tuyến `(site)` và `features/{site,search,commerce,account,chat,assistant}` import thẳng từng file (không qua barrel `@/components/app` hay `@/features/site`) và nạp lazy `SiteMobileDrawer`, `SiteSearchModal`, `SiteToaster`, widget chat/trợ lý bằng `next/dynamic` để giữ First Load JS trang chủ site và PDP ≤ 296 kB.
- **Quy ước kỹ thuật giữ nguyên**:
  - Ngày giờ chuẩn hóa UTC+7 (`Asia/Ho_Chi_Minh`) qua `web/src/lib/format.ts` tránh lệch hydration.
  - Điều hướng an toàn sau đăng nhập/đăng xuất (`window.location.assign` trong `AuthForms.tsx` và `SessionProvider.tsx`; kiểm tra đích qua `safeNextPath` / `safeInternalPath` trong `web/src/features/account/accountTypes.ts`).
  - Hiệu ứng cuộn một lần `RevealObserver` (`web/src/components/Reveal.tsx`, đánh dấu `data-shown`) và chuyển trang chỉ dùng `opacity` qua `PageTransition` (`web/src/components/PageTransition.tsx`), tự tắt khi `prefers-reduced-motion: reduce`.

---

## 2. Kết quả nghiệm thu Phase 0–4

### 2.1 Phase 0 — Nền kỹ thuật (`2026-11-26`)

- Dựng trọn bộ 35 primitive `web/src/components/ui/`, 15 pattern `web/src/components/app/`, ánh xạ 21 biến `--ui-*` cho 8 mẫu site và chốt mốc đo First Load JS trước redesign (`c265058`).
- Cổng tĩnh xanh: `eslint` 0 lỗi, `prettier` sạch, `test:unit` **510/510**, `check:data` xanh, `next build` xanh; các khu chưa chuyển giữ 0 đổi hình.

### 2.2 Phase 1–4 — Chuyển đổi toàn bộ 53 trang (`2026-11-27`, HEAD `4542ec3`, bản production `43120`)

| Hạng mục kiểm định               | Công cụ / Phạm vi                                                               | Kết quả                                                                                                                                                                                                                                                                                                                                         |
| -------------------------------- | ------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Cổng tĩnh & build                | `npm run lint`, `npm run format:check`, `npm run check:data`, `next build`      | **PASS** (0 lỗi lint, format sạch, build xanh; `tsc` 0 lỗi trong `web/src`)                                                                                                                                                                                                                                                                     |
| Unit test                        | `npm run test:unit` (31 suites, gồm 13 test viết lại theo bất biến ở `4542ec3`) | **510/510 PASS** (`0` fail, `0` skipped)                                                                                                                                                                                                                                                                                                        |
| Luồng chức năng — Nền tảng       | `node tests/ui-flows/run.mjs --port 43120 --area platform`                      | **5/5 PASS** (đăng nhập hợp lệ/sai, đăng ký OTP Mailpit, quên mật khẩu, 2 redirect `308`, FAQ `/tro-giup`)                                                                                                                                                                                                                                      |
| Luồng chức năng — Quản trị       | `node tests/ui-flows/run.mjs --port 43120 --area admin`                         | **6/6 PASS** (gian hàng, kiểm duyệt 2 tab, cấu hình 7 tab, đối soát 5 tab, audit log, form flash sale A3 + `SaveBar`)                                                                                                                                                                                                                           |
| Luồng chức năng — Người bán      | `node tests/ui-flows/run.mjs --port 43120 --area seller`                        | **12/12 PASS** (6 nhóm lọc đơn, xem nhanh `Sheet`, chi tiết A2, lọc sắp hết, sửa tồn inline, tạo/dọn nháp SP, 3 tab khuyến mãi, sổ cái, 2 tab tương tác, Site Studio, hồ sơ, `Ctrl+K`)                                                                                                                                                          |
| Luồng chức năng — Site gian hàng | `node tests/ui-flows/run.mjs --port 43120 --area site` (4 site seed × 9 luồng)  | **36/36 PASS** (`bach-dang`, `sai-gon`, `da-nang`, `ha-noi`: danh mục, tìm kiếm, PDP + `Sheet` giỏ, giỏ, COD, danh sách đơn, tra cứu, 4 tab tài khoản, voucher + flash sale)                                                                                                                                                                    |
| **Tổng luồng chức năng 4 khu**   | Quét cuối trên bản production `43120`                                           | **59/59 PASS** (`0` FAIL)                                                                                                                                                                                                                                                                                                                       |
| 8 mẫu site × luồng COD đầy đủ    | `node tests/ui-flows/run.mjs --port 43120 --area templates` (`d05d35a`)         | **8/8 PASS** (`minimal-fashion`, `editorial`, `dense-grid`, `craft-atelier`, `sport-active`, `luxury-maison`, `one-page-launch`, `multi-sku-catalog`): lưu nháp + xuất bản từ Site Studio trên shop `seller-catalog`, mỗi mẫu đi trọn PDP → giỏ → đặt đơn COD sinh mã đơn thật; 80 ảnh nghiệm thu `390px`/`1440px`, 0 lỗi console, 0 tràn ngang |
| Trực quan & chat WebSocket       | `.agents/tools/ui-shots.mjs` (390/768/1440) + đo chat 30s nhàn                  | **PASS** — Seller 18/18 đạt (`h1=1`, 0 problem); chat 30s nhàn: 1 `ws-ticket`, 1 kết nối WS (không vòng lặp); `sai-gon` 0 problem                                                                                                                                                                                                               |

### 2.3 First Load JS từng route chính (mốc `c265058` vs Phase 1–4 `4542ec3`)

Cả hai bản build cùng dùng `First Load JS shared by all = 103 kB` (đo từ bản production `base-c265058` và `4542ec3`). Ngân sách spec §15 tiêu chí 7 áp dụng trần **≤ +15% (≤ 296 kB)** cho trang chủ site và PDP:

| Route                             | Mốc `c265058` | Phase 1–4 (`4542ec3`) |      Chênh lệch | Ngân sách / Ghi chú                                  |
| --------------------------------- | ------------: | --------------------: | --------------: | ---------------------------------------------------- |
| `/` (Trang chủ nền tảng)          |        124 kB |            **118 kB** |   -6 kB (-4,8%) | Đạt (≤ 143 kB)                                       |
| `/dang-nhap` (Auth host gốc)      |        179 kB |            **158 kB** | -21 kB (-11,7%) | Giảm sau khi bỏ barrel `@/components/app`            |
| `/tro-giup`                       |        114 kB |            **133 kB** | +19 kB (+16,7%) | Thêm `Accordion` Radix                               |
| `/ve-nexusmart`                   |        113 kB |            **192 kB** | +79 kB (+69,9%) | Không đặt trần riêng                                 |
| `/s/[host]` (Trang chủ site)      |        258 kB |            **231 kB** | -27 kB (-10,5%) | **Đạt trần ≤ 296 kB**                                |
| `/s/[host]/san-pham`              |        258 kB |            **182 kB** | -76 kB (-29,5%) | Giảm nhờ nạp lazy `SiteRenderer` + bỏ barrel         |
| `/s/[host]/san-pham/[slug]` (PDP) |        258 kB |            **268 kB** |  +10 kB (+3,9%) | **Đạt trần ≤ 296 kB**                                |
| `/s/[host]/gio-hang`              |        224 kB |            **197 kB** | -27 kB (-12,1%) | Giảm rõ (lần đo trung gian ghi 196 kB)               |
| `/s/[host]/thanh-toan`            |        192 kB |            **218 kB** | +26 kB (+13,5%) | Thêm `Command`/`Popover` chọn tỉnh/phường            |
| `/s/[host]/don-hang`              |        180 kB |            **185 kB** |   +5 kB (+2,8%) | Ổn định                                              |
| `/s/[host]/tai-khoan`             |        194 kB |            **217 kB** | +23 kB (+11,9%) | 4 tab + `Sheet` + địa chỉ                            |
| `/s/[host]/dang-nhap` (Auth site) |        173 kB |            **142 kB** | -31 kB (-17,9%) | Giảm nhờ bỏ barrel                                   |
| `/seller`                         |        206 kB |            **268 kB** | +62 kB (+30,1%) | Không đặt trần; còn import barrel `@/components/app` |
| `/seller/don-hang`                |        202 kB |            **263 kB** | +61 kB (+30,2%) | Không đặt trần                                       |
| `/seller/san-pham`                |        203 kB |            **262 kB** | +59 kB (+29,1%) | Không đặt trần                                       |
| `/seller/site`                    |        333 kB |            **402 kB** | +69 kB (+20,7%) | Site Studio 5 chế độ + xem trước                     |
| `/admin`                          |        199 kB |            **288 kB** | +89 kB (+44,7%) | Không đặt trần; còn import barrel `@/components/app` |
| `/admin/cau-hinh`                 |        211 kB |            **307 kB** | +96 kB (+45,5%) | 7 tab quản trị cấu hình                              |
| `/admin/audit-log`                |        188 kB |            **237 kB** | +49 kB (+26,1%) | Không đặt trần                                       |

### 2.4 Đối chiếu pixel trang chủ `/` (S3b) và nguyên nhân lệch ≤ 6/255

- So sánh 41 ảnh cuộn theo viewport (`.agents/tools/scroll-shots.mjs`, `390 / 768 / 1440`, `reducedMotion: "reduce"`) giữa bản gốc `c265058` (`43101`) và bản sau redesign:
  - Trên bản dev: `YAVG = 0` trên toàn trang (chỉ khác icon `☰` ở `390px`/`768px` do đổi sang `lucide-react`).
  - Trên bản production (`4542ec3`): đối chứng `43101 ↔ 43101` đạt `0` ở `41/41` ảnh, còn `43101 ↔ 43120` lệch nhẹ ở `29/41` ảnh với biên độ **`YMAX ≤ 6/255`** (ngoài icon `☰`).
- **Nguyên nhân đã xác minh**: trình tối ưu Lightning CSS tích hợp trong `@tailwindcss/postcss` (chỉ chạy ở bản production) rút gọn `rgb(2 9 10 / 0.62)` thành hex 8 ký tự `#02090a9e` (làm tròn alpha 1/255); khi chồng nhiều lớp gradient trên `/` dẫn tới sai lệch tối đa 6 mức sáng.
- **Cách xử lý (đã chốt ở Phase 5 — P5a `a3e5e75`)**: đặt `"@tailwindcss/postcss": { optimize: false }` trong `web/postcss.config.mjs`. Trên bản production, lệch gradient trang chủ giảm từ `≤ 6/255` xuống **`≤ 1/255`**; tổng CSS đạt **`230.521 B raw / 38.000 B gzip`** (so mặc định `230.443 B raw / 38.041 B gzip`, giảm `41 B gzip`). Tailwind v4 vẫn tự phát tiền tố `-webkit-` và Next.js vẫn minify CSS khi build production.

---

## 3. Kết quả nghiệm thu Phase 5 (Dọn dẹp, preflight toàn cục & kiểm định cuối)

### 3.1 Phần đã hoàn tất trước khi chốt bản cuối

- **Dọn mã chết & toast site tối (P5b, commit `00af733`)**:
  - Xoá 33 tệp cũ: toàn bộ 11 file CSS Module, các component tự dựng đã thay thế và bộ 10 tệp tuyến duyệt nội bộ trước đây; gỡ tiền tố tuyến nội bộ khỏi `ROOT_PROTECTED_PREFIXES` (`web/src/lib/site/host.ts`).
  - Nâng cấp `web/src/features/site/shell/SiteToasterInner.tsx` đọc `data-site-theme-mode` từ `[data-site-root]` (kèm `MutationObserver`) và ánh xạ toàn bộ kiểu Sonner sang `--ui-*` (`--ui-popover`, `--ui-popover-foreground`, `--ui-border`, `--ui-radius`, `--ui-font`, `--ui-shadow-overlay`), khắc phục triệt để lỗi toast nền trắng trên 2 mẫu site tối `sport-active` và `luxury-maison`.
  - Viết lại 5 bài test theo bất biến trên mã mới (`Money.tsx`, `SiteProductCard.tsx`, `AdminReturnsClient.tsx`, `AdminUsersPanel.tsx`, `AdminReconciliationClient.tsx`); `tsc` 0 lỗi trong `web/src`, `npm run lint` 0 lỗi, `npm run test:unit` **505/505 PASS** tại thời điểm P5b.

- **CSS toàn cục, preflight & PostCSS (P5a, commit `a3e5e75`)**:
  - Bật `@import "tailwindcss/preflight.css" layer(base);` trong `web/src/styles/ui.css` và nạp ở `web/src/app/layout.tsx`; gom rule nền `html`/`body`, nền site tối (`html:has([data-site-root][data-site-theme-mode="dark"])`), `:focus-visible`, CSS Reveal/PageTransition vào `ui.css`; bỏ reset `[data-ui="true"]` và override `revert-layer`.
  - Xoá `globals.css`, `tokens.css`, font Archivo và `SITE_FONT_STACK` (`web/src/lib/site/theme.ts`), đổi `.num` sang `tabular-nums`, chuyển biến `--home-*` sang `web/src/features/platform/home/home-tokens.css` (scope `[data-platform-public="true"]` ở `web/src/app/(shop)/(public)/layout.tsx`) cùng `home-keyframes.css`.
  - Gỡ `postcss-flexbugs-fixes` và `postcss-preset-env` khỏi `web/postcss.config.mjs`, chốt `"@tailwindcss/postcss": { optimize: false }`: lệch gradient trang chủ `/` trên bản production còn `≤ 1/255` (từ `6/255`), CSS `230.521 B raw / 38.000 B gzip` (mặc định `230.443 B / 38.041 B`); Tailwind v4 vẫn tự phát `-webkit-`, Next.js vẫn minify CSS và hết cảnh báo `autoprefixer`.
  - Viết lại `web/src/styles/ui-vars.test.mjs` cho cấu trúc CSS cuối; tại mốc commit P5a: `tsc` 0 lỗi trong `web/src`, `npm run lint` 0 lỗi, `npm run test:unit` **507/507 PASS**, `npm run check:data` **11/11 PASS**.

### 3.2 Số liệu nghiệm thu cuối Phase 5

<!-- P5 -->

Mục này chờ điền số liệu nghiệm thu cuối sau khi hoàn tất S9d, build production HEAD cuối và `make e2e-web`:

- **Cổng tĩnh & test tự động:** <!-- P5: `tsc --noEmit` (0 lỗi `web/src`), `npm run lint`, `npm run format:check`, `npm run test:unit`, `npm run check:data`, `next build` -->
- **First Load JS sau Phase 5:** <!-- P5: cập nhật bảng `/`, `/s/[host]`, `/s/[host]/san-pham/[slug]`, `/s/[host]/gio-hang`, `/seller`, `/admin` so với mốc `c265058` và Phase 1–4 -->
- **Đối chiếu pixel production HEAD cuối vs mốc `43102` (6 khu) & `/` vs `43101`:** <!-- P5: số `SAME / DIFF / SIZE`, phân loại (a)/(b)/(c), xác nhận 0 hồi quy loại (c) -->
- **Trạng thái tương tác (`tests/ui-flows/states.mjs`):** <!-- P5: kết quả chụp & so pixel overlay mở (`⌘K`, `Dialog`, `Sheet`, `Popover`, `DropdownMenu`, `Tooltip`, `Sonner`), vòng Tab focus và `Reveal` -->
- **Luồng UI & E2E BFF:** <!-- P5: kết quả `tests/ui-flows/run.mjs` (`platform`, `admin`, `seller`, `site`, `templates`) và `make e2e-web` -->

---

## 4. Phần chưa đạt, điểm làm khác spec có lý do & tồn đọng ngoài phạm vi

### 4.1 Phần chưa đạt / chờ quyết định sản phẩm

1. **Lệch pixel trang chủ `/` trên bản production**: ở Phase 1–4 lệch tới `YMAX ≤ 6/255` do Lightning CSS ép alpha sang hex 8-bit; **đã giảm xuống `≤ 1/255`** ở Phase 5 (P5a `a3e5e75`) bằng `"@tailwindcss/postcss": { optimize: false }` (còn lệch nhỏ `≤ 1/255` do làm tròn phối màu trình duyệt + icon `☰` `lucide-react` ở `390px`/`768px`, 0 hồi quy hình).
2. **First Load JS khu Kênh người bán (+21% đến +30%) và Cổng quản trị (+26% đến +46%)**: tăng do nhiều màn hình vẫn import qua barrel `@/components/app`. Spec §15 chỉ đặt trần +15% (≤ 296 kB) cho trang chủ site và PDP (cả hai đều đạt); việc tách import từng file ở hai khu nội bộ để lại là tối ưu tuỳ chọn sau redesign.
3. **Tìm nhanh theo tiền tố mã ở Cổng quản trị (`DS-`, `KN-`, `SP-/SKU-/NM-`, `FS-`, `site:`)**: ở `AppShell` mới, `⌘K` tìm trực tiếp danh sách mục điều hướng theo quyền RBAC (đúng spec §8.1), không tự suy luận chuyển hướng theo mã như shell cũ. Nếu muốn khôi phục cần bổ sung prop fallback cho `AppShell` (chờ chủ dự án chốt).
4. **3 cảnh báo build `autoprefixer` ("outdated direction syntax") ở Phase 1–4**: **đã hết** sau khi P5a (`a3e5e75`) gỡ `postcss-preset-env` khỏi `web/postcss.config.mjs`.
5. **Tự thử lại khi gặp HTTP 429 trong `tests/ui-flows/lib.mjs`**: `.agents/tools/ui-shots.mjs` và `tests/ui-flows/templates.mjs` đã tự chờ 60s khi chạm giới hạn tần suất; `tests/ui-flows/lib.mjs` chưa có cơ chế chờ 429 cho luồng site dài (lượt cuối `ha-noi` đôi khi cần chạy lại riêng).
6. **Site thử nghiệm `kho-dai-ly-nexusmart` để lại sau S9c**: giao diện `/seller/site` và BFF `web/src/app/api/seller/site/*` chưa có thao tác "Gỡ xuất bản", nên site dùng chạy 8 mẫu trên shop `seller-catalog` giữ nguyên trạng thái `PUBLISHED` v8 (4 site seed `bach-dang`, `sai-gon`, `da-nang`, `ha-noi` còn nguyên vẹn).

### 4.2 Điểm làm khác spec có lý do

- **`SiteSearchBar` trên site gian hàng**: giữ ô tìm kiếm trực tiếp cùng phím tắt/nút `⌘K` mở `SiteSearchCommandDialog` (`CommandDialog`) để vừa bám hành vi tìm kiếm tại chỗ vừa đáp ứng trải nghiệm lệnh nhanh.
- **Toast Sonner trên site tối**: phát hiện ở nghiệm thu Phase 4 (S9c) rằng toast dùng kiểu tối chung chưa hài hoà với nền tối của `sport-active`/`luxury-maison`; đã cập nhật ở Phase 5 (P5b) sang cơ chế đọc `data-site-theme-mode` và dùng `--ui-*` của chính site.

### 4.3 Tồn đọng backend / nghiệp vụ có sẵn (ngoài phạm vi đợt redesign UI)

- Phí vận chuyển hiển thị lúc báo giá có thể khác phí tính khi tạo đơn do `web/src/app/api/checkout/shipping-quote/route.ts` đang lấy tỉnh người mua làm nơi gửi.
- Đơn COD mới đặt hiển thị trạng thái đã thanh toán theo dữ liệu backend trả về (`web/src/features/account/orderView.ts`).
- Giá flash sale trong giỏ giữ trong bộ nhớ trình duyệt (`StoreContext`), tải lại trang cần đồng bộ lại từ API.
- Chat realtime cần cấu hình `NEXT_PUBLIC_CHAT_WS_URL` khi truy cập từ trình duyệt ngoài mạng Docker nội bộ.
- Lịch sử tìm kiếm (`/api/v1/search/history`) đã có ở backend nhưng chưa mở UI trên `(site)`.
