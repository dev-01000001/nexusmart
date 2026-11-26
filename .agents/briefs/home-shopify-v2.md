# Brief vòng 2: trang chủ nền tảng "y như" shopify.com/vn

Ngày: 2026-11-26. Người giao: Claude Code (manager). Coder: agy (một phiên mới, không swarm).
**Brief này thay hẳn `.agents/briefs/home-shopify.md`** (vòng 1). Chỗ nào hai brief khác nhau thì làm theo brief này.

## 1. Bối cảnh và mục tiêu

Vòng 1 (commit `a9a4b1f`, đang chạy trên production) đã dựng lại `/` theo bố cục Shopify. Chủ dự án xem xong vẫn chê:
**"UI/UX trang chủ chưa đẹp lắm, cứ thiết kế y như https://www.shopify.com/vn là được"**. Trước đó họ đã dặn: *chỉ đổi màu theme và logo*.

Vì sao vòng 1 chưa giống (Claude đã đo trên trang thật):
1. **Màu và chiều sâu.** Vòng 1 dùng một màu slate `--ink-1` (#152229) cho mọi khối, cấm gradient và bóng, và phủ hero bằng một lớp xám phẳng cộng blur. Kết quả là cả trang xám đục. Shopify thì khác:
   - nền gần đen `rgb(2,9,10)`, body `#000`;
   - mỗi dải nội dung một tông riêng: navy `rgb(0,10,30)`, pine `rgb(4,30,24)`, một dải gradient tím `linear-gradient(in oklab, rgb(44,0,127) 42%, rgb(0,10,29))`. Mỗi dải bo 48px ở hai góc trên và đè lên dải trước;
   - thẻ có quầng sáng, vòng tròn đồng tâm, lưới chấm mờ dần.
2. **Số đo.** Shopify đặt nội dung rộng 1260px ở màn 1440, tức lề 90px; vòng 1 chỉ khoảng 1150px. Các số khác (cỡ chữ, nút, bo góc) ở mục 4.
3. **Thiếu khối.** Vòng 1 chưa có các khối giao hàng (kiểu bản đồ), dải gradient "Sidekick", tường ứng dụng, khối terminal và dải pine của trang thanh toán.
4. **Mô phỏng UI quá nhỏ, chữ 11px không đọc được.** Shopify chỉ đặt ít phần tử trong mỗi mô phỏng, nhưng phần tử to, có khoảng thở và có quầng sáng.

**Mục tiêu:** đặt trang `/` cạnh Shopify thì bố cục, nhịp, cỡ chữ, màu nền từng dải, kiểu thẻ và kiểu mô phỏng phải giống. Chỉ khác ba thứ:
- màu nhấn là teal của NexusMart;
- logo là `NexusMark` + chữ NexusMart;
- nội dung là tính năng thật của NexusMart.

## 2. Ảnh tham chiếu (bắt buộc mở xem trước khi code)

Thư mục `.agents/briefs/home-shopify-v2/ref/`. Ảnh chụp ngày 26/11, mỗi ảnh là một màn hình cao 900px, xếp từ trên xuống:
- Shopify desktop: `shopify-1440-00.jpg` … `shopify-1440-11.jpg`, ảnh ghép tổng quan `shopify-desktop-sheet.jpg`.
- Shopify mobile: `shopify-390-00.jpg` … `shopify-390-11.jpg`, ảnh ghép `shopify-mobile-sheet.jpg`.
- Trang NexusMart hiện tại (production): `current-live-00.jpg` … `current-live-06.jpg`, ảnh ghép `current-sheet.jpg`.
- Script chụp: `node .agents/briefs/home-shopify-v2/ref/shot.cjs <url> <tiền-tố-đường-dẫn-ảnh> <độ-rộng>`. Script dùng `playwright-core` của `tests/playwright` và `/usr/bin/google-chrome`, chụp từng màn 900px, tên ảnh có hậu tố `-NN.jpg`. Ghi ảnh của bạn vào `.agents/briefs/home-shopify-v2/ref/agy/` (thư mục đã gitignore).

Bảng ánh xạ khối, đánh số theo ảnh desktop:

| # | Khối Shopify | Ảnh | Khối NexusMart (mục 6) |
|---|---|---|---|
| 1 | Hero "Trở thành / Ngôi sao AI" (dòng 2 đổi chữ luân phiên) | 00 | S1 Hero |
| 2 | "Bán hàng ở mọi nơi…" (đoạn chữ lớn hai tông) + hàng 3 ảnh khảm | 00–01 | S2 Danh bạ |
| 3 | Thẻ xanh "Thương hiệu của bạn đã tham gia trò chuyện" | 01–02 | S3 Trợ lý AI |
| 4 | "Hiện diện ở mọi nơi" — 3 thẻ hẹp/rộng/hẹp | 02–03 | S4 Một nơi cho cả cửa hàng |
| 5 | "Phát triển khắp thế giới" — một thẻ lớn | 03–04 | S5 Giao khắp 34 tỉnh thành |
| 6 | "Dành cho tất cả…" — 3 ảnh + nút viền | 04–05 | S6 Dành cho mọi quy mô |
| 7 | Dải gradient tím "Gặp gỡ Sidekick" — 2 thẻ | 05–06 | S7 Dải teal: Báo cáo AI + Chat |
| 8 | Dải navy "Tùy chỉnh mọi thứ bằng ứng dụng" — tường icon | 06 | S8 Tường công cụ |
| 9 | Navy "Tăng tốc mạnh mẽ nhờ AI" — cửa sổ terminal | 07 | S9 Site Studio |
| 10 | Dải pine "Không có nơi nào lý tưởng hơn…" + trang thanh toán | 08 | S10 Thanh toán |
| 11 | "Hoạt động ổn định" — quả địa cầu | 09 | S11 FAQ (thay chỗ quả địa cầu) |
| 12 | "Xây dựng nhanh chóng" — 3 bước + 2 ảnh | 10–11 | S12 Ba bước |
| 13 | Footer đen | 11 | S13 Footer |

Bỏ: nút video nổi góc phải dưới ("Lý do chúng tôi tạo ra Shopify") và viên "Edition Thu 2026" trên header.

## 3. Điều cấm tuyệt đối (bản quyền, trung thực)

- Không chép chữ, ảnh, video, logo, icon của Shopify hay thương hiệu khách của họ (Glossier, Mattel, Houseplant, KOTN, Walmart, TikTok, Instagram…). Không nhắc tên Shopify. Không dùng font `Shopify-Inter`.
- Không số liệu ("15%", "250 triệu+", số cửa hàng, uptime…), không lời chứng thực, không tên người thật, không logo đối tác.
- Không vẽ bản đồ Việt Nam hay bản đồ thế giới, kể cả bản đồ chấm. Bản đồ Việt Nam thiếu Hoàng Sa và Trường Sa là rủi ro nghiêm trọng. Khối giao hàng dùng lưới chấm trừu tượng (mục S5).
- Chỉ nói những gì nền tảng làm được thật, theo mục 7. Mô phỏng UI là hình minh họa: bọc bằng `role="img"` kèm `aria-label` bắt đầu bằng "Minh họa …". Phần tử con mang `aria-hidden`.
- Chỉ dùng ảnh có sẵn trong `web/public/`. Không tải ảnh hay video mới.

## 4. Hệ thống thị giác (số đo từ Shopify, áp đúng)

### Ngoại lệ luật "bề mặt phẳng" (chủ dự án duyệt gián tiếp qua yêu cầu "y như", Claude chốt 26/11)
- **Chỉ trang chủ `/`** (mọi file `PlatformHome*`, component con của trang chủ, nhánh `data-home="true"` của header) được dùng gradient, quầng sáng (radial-gradient), `mask-image` mờ dần và `box-shadow`.
- `(site)`, `(seller)`, `(admin)` và các trang `(shop)` khác (`/ve-nexusmart`, `/tro-giup`, đăng nhập…) **giữ nguyên luật cũ và giữ nguyên giao diện**.
- Ghi ngoại lệ này (2–3 dòng, nêu ngày 2026-11-26 và phạm vi) vào ba chỗ:
  - `web/CLAUDE.md`, ngay chỗ câu "Giữ bề mặt phẳng, không gradient hay shadow tĩnh";
  - `.claude/rules/frontend.md`, gạch đầu dòng đầu tiên;
  - comment đầu file `web/src/styles/tokens.css`, cạnh dòng "Zero gradients…".

### Token mới
Thêm vào `web/src/styles/tokens.css` một nhóm `/* Platform home (shopify-like) */`, tiền tố `--home-*`. Không đổi giá trị token cũ. Mọi giá trị trong CSS trang chủ đọc từ token (luật `.claude/rules/frontend.md`). Giá trị gợi ý:

| Token | Giá trị | Dùng cho |
|---|---|---|
| `--home-bg` | `#000` | nền ngoài cùng, footer |
| `--home-band` | `rgb(2 9 10)` | dải mặc định (danh bạ, S3–S6) |
| `--home-navy` | `rgb(0 10 30)` | dải S8–S9 |
| `--home-pine` | `rgb(3 30 28)` | dải S10–S11 (pine của Shopify ngả teal) |
| `--home-teal-deep` | khoảng `rgb(0 84 88)` | điểm 42% của gradient dải S7 (thay tím `rgb(44 0 127)`) |
| `--home-card` | khoảng `rgb(10 24 24)` | nền thẻ tối |
| `--home-ink` | `#fff` | chữ chính |
| `--home-ink-2` | `rgb(157 171 173)` | đoạn văn (đúng màu Shopify, ngả teal) |
| `--home-ink-3` | `rgb(161 161 170)` | chữ nhỏ, link footer |
| `--home-ink-dim` | `rgb(255 255 255 / 0.42)` | vế thứ hai của tiêu đề hai tông |
| `--home-accent` | dùng `--band-dark-accent` (#66ccd7) | số bước, chấm, viền pill đang chọn |
| `--home-glow` | `rgb(102 204 215 / 0.35)` | quầng sáng |
| `--home-radius-band` | `48px` | góc trên của dải |
| `--home-radius-card` | `8px` | thẻ |
| `--home-radius-media` | `5px` | ảnh trong thẻ, khảm |
| `--home-max` | `1260px` | độ rộng nội dung |
| `--home-gutter` | `clamp(16px, 6.25vw, 90px)` | lề, bằng 90px ở 1440 |
| `--home-space-section` | `clamp(64px, 9vw, 128px)` | padding dọc của khối |
| `--home-space-head` | `clamp(32px, 4.5vw, 64px)` | khoảng từ tiêu đề tới nội dung |

Thêm token cỡ chữ nếu cần. Không ghi cứng px trong CSS Module.

### Typography: Inter cho trang chủ
- Nạp **Inter** bằng `next/font/google` (cùng cơ chế với Archivo trong `src/app/layout.tsx`): `subsets: ["latin", "vietnamese"]`, `axes: ["opsz"]`, biến `--font-inter`, `display: "swap"`. Gắn biến ở `src/app/(shop)/layout.tsx` (lên `div.shell`) để header nhánh trang chủ dùng được.
- Chỉ trang chủ và nhánh trang chủ của header dùng Inter. Mọi trang khác vẫn Archivo.
- Thang chữ (desktop 1440; mobile thu theo `clamp`, đối chiếu `shopify-390-*`):

| Vai trò | Cỡ / weight / line-height / letter-spacing |
|---|---|
| `h1` hero, đoạn chữ lớn của danh bạ | `96px` / `300` / `1.08` / `-0.02em`; mobile khoảng `44px` |
| Tiêu đề khối (`h2`) | `56px` / `330` / `1.08` / `-0.03em`; mobile khoảng `32px` |
| Tiêu đề căn giữa của dải pine và dải 3 bước | `64px` / `330` / `1.08` / `-0.02em` |
| Tiêu đề phụ trong dải thanh toán, FAQ | `44px` / `330` / `1.1` / `-0.01em` |
| Đoạn hero | `24px` / `400` / `1.3` / `-0.02em`, rộng tối đa khoảng `420px` |
| Đoạn văn khối | `18px` / `400` / `1.4`, màu `--home-ink-2` |
| Tiêu đề thẻ (`h3`) | `20px` / `450` / `1.3`, trắng |
| Chữ thẻ, footer | `14–16px` / `420`, màu `--home-ink-3` |

### Nút, thẻ, ảnh
- Nút hero: viên thuốc, cao `56px`, padding `12px 24px`, chữ `18px` weight `550`. Nút chính nền trắng chữ đen; nút phụ viền trắng `1.5px`, nền trong suốt. Mobile 390: hai nút rộng 100%, xếp dọc (xem `shopify-390-00`).
- Nút header: viên thuốc trắng, cao `44px`, padding `8px 20px`, chữ `16px` weight `550`.
- Nút viền giữa trang ("Mở gian hàng" ở S6, S12): viên thuốc viền trắng, cao khoảng `48px`.
- Thẻ: bo `--home-radius-card`. Ảnh trong thẻ hay khảm: bo `--home-radius-media`. Bỏ bo 16px của vòng 1.
- Mỗi dải mới (S2, S7, S10, S12) bo `--home-radius-band` ở hai góc trên và kéo đè lên dải trước `48px` (margin-top âm). Riêng S2 đè lên hero.

### Chuyển động
- Dùng `data-reveal` sẵn có cho fade/rise nhẹ.
- Chữ luân phiên ở hero (S1) và ảnh sản phẩm trôi nhẹ ở S10 làm bằng CSS keyframes, không thêm JS hay thư viện.
- Tuỳ chọn: đoạn chữ lớn ở S2 chuyển từ xám sang trắng khi cuộn, bằng `animation-timeline: view()` bọc trong `@supports`. Trình duyệt không hỗ trợ thì hiện hai tông tĩnh.
- `prefers-reduced-motion: reduce`: tắt hết chuyển động. Hero chỉ hiện cụm chữ đầu, ảnh đứng yên.

## 5. Phạm vi file

**Được sửa, tạo, xoá:**
- `web/src/features/platform/**`, **trừ** các file sau (không sửa): `PlatformAuthShell.*`, `subdomain.ts`, `directory.server.ts`, `directoryModel.ts`, `legacy*.ts`, `NotFoundView.*`, `platform.test.mjs`.
- `web/src/app/(shop)/layout.tsx`: chỉ để gắn biến font Inter.
- `web/src/app/(shop)/page.tsx`: chỉ khi thật cần. Luồng `parseDirectoryPage` → `loadSiteDirectory` → `PlatformHome` giữ nguyên.
- `web/src/styles/tokens.css`: chỉ **thêm** token `--home-*` và comment ngoại lệ.
- `web/CLAUDE.md`, `.claude/rules/frontend.md`: chỉ thêm dòng ngoại lệ (mục 4).
- `web/DESIGN_REPORT.md`: cập nhật đoạn mô tả trang chủ cho khớp bản mới (ngắn).

**Cấm sửa:** mọi thứ khác, gồm `globals.css`, `(site)`, `(seller)`, `(admin)`, `web/src/components/**` (được **import** `Icons`, `Price`…; không sửa), `services/`, `tests/`, `docs/`, `package.json`, `package-lock.json`.

Header (`PlatformHeader.*`) và footer (`PlatformFooter.*`) dùng chung mọi trang `(shop)`. Chỉ đổi nhánh `data-home="true"` của header. Footer đổi được (S13), nhưng `/ve-nexusmart` và `/tro-giup` vẫn phải đọc rõ và không vỡ.

## 6. Từng khối

Mọi khối nằm trong container `width: min(var(--home-max), 100% - 2 * var(--home-gutter))`, căn giữa. Mép trái của logo header, hero, tiêu đề và thẻ phải thẳng một hàng.

### S0. Header (chỉ nhánh trang chủ)
Xem `shopify-1440-00`.
- Cao khoảng 72px. Logo ở bên trái, nav nằm ngay sau logo, **căn trái chứ không căn giữa** (Shopify: logo ở x=90, nav bắt đầu ở x≈248). Nav chữ `16px` trắng.
- Bên phải là "Đăng nhập" (link trắng) và nút "Mở gian hàng" (viên thuốc trắng 44px).
- Ở đầu trang nền trong suốt; cuộn xuống thì nền `--home-band` đặc, chữ vẫn trắng.
- Font Inter. Giữ nguyên menu tài khoản, drawer mobile, focus trap, ẩn/hiện khi cuộn, link hiện có.
- Mobile: logo, link chữ "Mở gian hàng" và nút menu (xem `shopify-390-00`).

### S1. Hero
Xem `shopify-1440-00`, `shopify-390-00`.
- Cao `100svh`, tối thiểu khoảng 640px, tối đa khoảng 900px. Ảnh nền tràn viền `/banners/campaign-office.jpg` bằng `next/image` với `fill` và `priority`, `object-fit: cover`, `object-position` để hai người nằm bên phải.
- **Bỏ lớp phủ phẳng 55% và bỏ blur.** Thay bằng scrim gradient: tối ở bên trái và đáy, trong ở giữa và bên phải. Ví dụ: `linear-gradient(90deg, rgb(2 9 10 / .7) 0%, rgb(2 9 10 / .3) 45%, transparent 70%)` chồng với `linear-gradient(0deg, rgb(2 9 10 / .55), transparent 35%)`. Ảnh phải tươi như của Shopify, chữ vẫn đạt AA (mục 9).
- `h1`, hai dòng, đặt ở khoảng 1/3 dưới của hero:
  - dòng 1 "Cửa hàng";
  - dòng 2 luân phiên ba cụm "mang tên bạn" → "trên tên miền riêng" → "nhận COD và VietQR", chuyển bằng fade/trượt lên khoảng 3 giây một cụm. Ba cụm xếp chồng trong cùng một ô grid để chiều cao không nhảy.
  - Tên truy cập của `h1` cố định: `<span className="sr-only">Cửa hàng mang tên bạn</span>`, các cụm hiển thị mang `aria-hidden`. Reduced motion: chỉ hiện "mang tên bạn".
- Đoạn văn `24px`: "Gõ tên thương hiệu, có ngay cửa hàng riêng. Giao diện riêng, khách riêng, thanh toán COD và VietQR."
- Hai nút: "Mở gian hàng" → `/dang-ky?role=seller`; "Xem các cửa hàng" → `#danh-ba-gian-hang`.

### S2. Danh bạ = "Bán hàng ở mọi nơi"
Xem `shopify-1440-00` (đáy) và `01`.
- Section **giữ `id="danh-ba-gian-hang"`** và `aria-labelledby="platform-directory-heading"`. Header, footer và `/ve-nexusmart` đều trỏ vào neo này.
- Nền `--home-band`, bo 48px ở góc trên, đè lên đáy hero.
- Tiêu đề là đoạn chữ lớn hai tông, cỡ 56px:
  - vế đầu trắng: "Bán trên địa chỉ của riêng bạn.";
  - vế sau `--home-ink-dim`: "Giao diện riêng. Khách riêng. Giỏ hàng và thanh toán ngay trên cửa hàng.";
  - `h2` chứa toàn bộ câu.
- Hàng khảm kiểu Shopify: ba ô **rộng/hẹp/rộng**, tỉ lệ chiều ngang khoảng 41% / 18% / 41%, cao khoảng `381px` ở 1440, bo 5px. Có hơn 3 ô thì lặp lại mẫu này theo hàng. Mobile: hàng cuộn ngang `scroll-snap`, thấy mép ô kế tiếp.
- Mỗi ô trông như **ảnh chụp một cửa hàng** chứ không phải ảnh người mẫu trơn:
  - **Có site thật** (`directory.ok`, `sites.length > 0`): ô là link mở site (giữ đích link hiện có). Ảnh `coverUrl`, không có thì dựng mô phỏng cửa hàng theo màu mẫu, có logo hoặc tên shop lớn và thanh menu nhỏ. Dưới ô ghi tên shop (16px trắng) và host (14px `--home-ink-3`). Giữ hiển thị "N gian hàng" và phân trang hiện có (`pageHref`, `buildPageList`, nút trước/sau, `aria-current`, trang vượt phạm vi báo "Trang này không có cửa hàng nào" + "Về trang đầu"), restyle theo trang mới.
  - **Rỗng** (`directory.ok`, total = 0; đây là trạng thái production hiện tại): ba ô mô phỏng cửa hàng dựng bằng HTML/CSS từ `SHOWCASE_TEMPLATES`:
    - ô rộng: cửa hàng trên máy tính, gồm thanh menu, ảnh lớn, `headline` của mẫu, nút;
    - ô hẹp: trang sản phẩm trên điện thoại, đứng dọc như ô KOTN của Shopify;
    - ô rộng thứ hai: một mẫu khác.
    - Mỗi ô có nhãn nhỏ thấy được "Mẫu giao diện · {label}". Dưới hàng khảm ghi "Chưa có cửa hàng nào xuất bản. Cửa hàng đầu tiên có thể là của bạn." kèm link "Mở gian hàng" → `/dang-ky?role=seller`.
  - **Lỗi** (`!directory.ok`): giữ nội dung `DirectoryErrorState` (có "Tải lại"), restyle; vẫn hiện ba ô mẫu như trạng thái rỗng.
- Được sửa hoặc thay `StorefrontStageMini` để dựng các ô mô phỏng. Code mô phỏng dùng chung một component, không chép ba lần.

### S3. Thẻ Trợ lý AI = "Thương hiệu của bạn đã tham gia trò chuyện"
Xem `shopify-1440-01/02`. Nằm trong cùng dải với S2.
- Một thẻ rộng hết container, cao khoảng 450px, bo 8px, padding khoảng 40px. Nền là gradient teal đậm kèm quầng sáng, ví dụ `radial-gradient(at 75% 40%, var(--home-glow), transparent 55%)` chồng lên `linear-gradient(160deg, rgb(6 44 46), rgb(3 22 24))`.
- Góc trên bên trái: một viên nhỏ có `IconSpark` và chữ "Trợ lý cửa hàng" (thay cho cụm logo AI của Shopify).
- Bên trái: tiêu đề 56px "Trợ lý AI trả lời khách thay bạn"; đoạn văn 18px "Khách hỏi size, chất liệu, tình trạng đơn ngay trên cửa hàng. Trợ lý chỉ xem được những gì khách đó được phép xem."
- Bên phải (mô phỏng):
  - thẻ ảnh `/models/model-polo-xanh-da.jpg` khoảng 204×227, bo 4px, dưới ảnh có nhãn 12px "Áo Polo Pique Xanh Đá";
  - bong bóng trắng chữ đen 14px nổi ở góc trên bên trái ảnh: "Áo polo này có size L không?";
  - bong bóng tối mờ phía dưới: "Còn size L màu xanh đá. Vải cotton pique, hợp người 68–74 kg."
- Mobile: chữ ở trên, mô phỏng ở dưới (xem `shopify-390-01`).

### S4. "Một nơi cho cả cửa hàng" = "Hiện diện ở mọi nơi"
Xem `shopify-1440-02/03`.
- Tiêu đề 56px, hai dòng, cả hai màu trắng: "Một nơi cho cả cửa hàng. / Không cần ghép nhiều công cụ."
- Ba thẻ trên một hàng, rộng 1 : 2 : 1 (303 / 606 / 303px ở 1440, khoảng cách 24px), cao khoảng 440px, bo 8px. Nền `--home-card`, kèm vòng tròn đồng tâm mảnh (SVG hoặc `repeating-radial-gradient`) và quầng sáng teal ở giữa. Dưới mỗi thẻ là `h3` 20px và đoạn 16px `--home-ink-2`.
  - **Thẻ 1, hẹp:**
    - Hình: mô phỏng cửa hàng tràn ra mép phải của thẻ. Một khối banner được chọn có khung viền teal và nhãn nhỏ "Banner", giống khung chọn khối trong trình chỉnh giao diện.
    - Chữ: "Giao diện mang dấu ấn riêng" / "8 mẫu giao diện trong Site Studio, xem trước rồi mới xuất bản."
  - **Thẻ 2, rộng:**
    - Hình: tâm vòng tròn là thẻ xác nhận đơn màu mint nhạt: dấu tích, "Cảm ơn bạn, Minh!", "Chi tiết đơn hàng", ảnh nhỏ `/products/ao-polo-pique-xanh-da.png`, "Áo Polo Pique Xanh Đá · Size L", giá `299.000₫` (dùng `formatVnd` hoặc `Price`), nút viền "Đơn hàng đã hoàn tất".
    - Quanh thẻ là 6 viên nhãn: "Thanh toán" (trên cùng, nổi rõ, viền trắng), "Vận chuyển", "Sản phẩm", "Khách hàng", "Tồn kho", "Khuyến mãi" (mờ dần).
    - Chữ: "Điều hành mọi thứ ở một nơi" / "Đơn hàng, tồn kho, thanh toán, khuyến mãi. Tất cả trong Kênh người bán."
  - **Thẻ 3, hẹp:**
    - Hình: 3–4 ô vuông kính tối bo góc nổi so le như ô Walmart/IG/TikTok của Shopify. Mỗi ô chứa một chữ ngắn hoặc icon từ `@/components/Icons`, ví dụ `IconStore`, "tên miền riêng", `.{rootDomain}`.
    - Chữ: "Địa chỉ của riêng bạn" / "Có sẵn địa chỉ ten-shop.{rootDomain}, hoặc gắn tên miền riêng." Lấy `rootDomain` từ prop.
- Mobile: ba thẻ xếp dọc, rộng 100% (xem `shopify-390-02/03`).

### S5. "Giao khắp 34 tỉnh thành" = "Phát triển khắp thế giới"
Xem `shopify-1440-03/04`.
- Tiêu đề 56px "Giao khắp 34 tỉnh thành".
- Một thẻ lớn rộng hết container, cao khoảng 540px, bo 8px. Nền tối ngả teal, **lưới chấm trừu tượng** (`radial-gradient` lặp, `mask-image` mờ về hai mép). Không vẽ bản đồ.
- Bên trái: cột dọc 5 viên tên tỉnh (Hà Nội, Hải Phòng, Đà Nẵng, TP.HCM, Cần Thơ), một viên nổi rõ, các viên còn lại mờ. Viên này thay cho cột cờ quốc gia của Shopify.
- Ở giữa: thẻ sản phẩm trắng có ảnh (chọn một ảnh `/models/*` chưa dùng ở đâu khác trong trang) và nút tối "Mua ngay". Cạnh thẻ có viên nổi "Đơn hàng 349.000₫ · Đà Nẵng".
- Bên phải: thẻ trắng "Phí giao hàng", liệt kê 4 dòng tỉnh/phường với phí (ví dụ 22.000₫ / 30.000₫ / 35.000₫) và 3 trạng thái "Đã lấy hàng · Đang giao · Đã giao", trạng thái hiện tại có chấm teal.
- Góc dưới bên trái thẻ, trên nền tối: `h3` "Phí ship theo tỉnh và phường" và đoạn "Khách thấy phí giao hàng ngay khi nhập địa chỉ. Bạn in nhãn và theo dõi đơn đang giao trong Kênh người bán."
- Mobile: theo `shopify-390-03/04`. Hình ở trên, chữ ở dưới, chỉ giữ thẻ sản phẩm và viên đơn hàng.

### S6. "Dành cho mọi quy mô" = "Dành cho tất cả…"
Xem `shopify-1440-04/05`.
- Tiêu đề 56px, rộng tối đa khoảng 724px: "Dành cho mọi quy mô, từ shop mới mở đến xưởng sản xuất".
- Ba thẻ ảnh bằng nhau (tỉ lệ khoảng 4:3, bo 5px). Dưới mỗi ảnh có `h3` 20px và một câu 16px. Không tên người, không con số.
  - "Shop mới bắt đầu" / "Bắt đầu với vài sản phẩm, mở rộng khi đơn tăng."
  - "Thương hiệu thời trang" / "Giữ nhận diện thương hiệu trên từng trang."
  - "Xưởng may" / "Bán thẳng sản phẩm của xưởng tới người mua."
- Nút viền viên thuốc căn giữa "Mở gian hàng" → `/dang-ky?role=seller`.
- Mobile: hàng cuộn ngang `scroll-snap`, thấy mép thẻ kế tiếp (xem `shopify-390-05`).

### S7. Dải teal gradient = "Gặp gỡ Sidekick"
Xem `shopify-1440-05/06`.
- Dải mới, bo 48px ở góc trên. Nền `linear-gradient(in oklab, var(--home-teal-deep) 42%, var(--home-navy) 100%)`, đúng cấu trúc gradient tím của Shopify, chỉ thay tím bằng teal.
- Tiêu đề 56px: "Kênh người bán, có AI làm cùng".
- Hai thẻ tối (nền gần đen pha trong suốt, bo 8px), rộng khoảng 2 : 1:
  - **Thẻ rộng:**
    - Hình: ghép 2–3 ảnh sản phẩm hoặc người mẫu nhỏ, ở giữa là ô nhập trắng chứa câu hỏi "Sản phẩm nào có tỷ lệ hoàn hủy cao nhất trong 30 ngày qua?" và nút gửi tròn teal. Thêm một ô số nhỏ mờ, chỉ ghi nhãn không ghi số thật, ví dụ "Doanh thu 30 ngày".
    - Chữ: `h3` "Báo cáo AI cho người bán" (kèm `IconSpark`) / "Hỏi bằng tiếng Việt về doanh thu, đơn hàng, hoàn hủy. AI trả lời từ số liệu cửa hàng của bạn."
  - **Thẻ hẹp:**
    - Hình: mô phỏng khung chat, gồm 2 tin của khách và 1 tin trả lời của shop.
    - Chữ: `h3` "Chat với khách" / "Trả lời tin nhắn của khách ngay trong Kênh người bán."
- Mobile: hai thẻ xếp dọc.

### S8. Dải navy "Đủ công cụ để vận hành cửa hàng" = "Tùy chỉnh mọi thứ bằng ứng dụng"
Xem `shopify-1440-06`, `shopify-390-06`.
- Nền `--home-navy`, nối tiếp S7 không có đường cắt.
- Bên trái: tiêu đề 56px "Đủ công cụ để vận hành cửa hàng"; đoạn văn 18px "Sản phẩm, nhập CSV, tồn kho, đơn hàng, in nhãn, đổi trả, voucher, flash sale, báo cáo. Có sẵn trong Kênh người bán, không phải cài thêm."
- Bên phải, tràn ra mép phải màn hình: **tường ô vuông** khoảng 8 hàng × 14–20 cột. Mỗi ô 44–56px, bo khoảng 12px.
  - Mỗi ô một nền màu lấy từ các bảng màu thật của `SHOWCASE_TEMPLATES` / `PLATFORM_TEMPLATES` (bg, accent, surface), bên trong là một icon trắng từ `@/components/Icons` (`IconPackage`, `IconTag`, `IconTruck`, `IconWallet`, `IconChat`, `IconGift`, `IconFileText`, `IconRefresh`, `IconDashboard`, `IconStore`, `IconShield`, `IconClock`, `IconSpark`, `IconSearch`, `IconStar`, `IconHeart`…).
  - Mờ dần về phía trên và bên trái bằng `mask-image`, như Shopify.
  - Toàn khối `aria-hidden="true"`. Không ghi tên ứng dụng hay logo nào.
- Mobile: tường nằm dưới chữ, tràn hai mép (xem `shopify-390-06`).

### S9. Navy "Tùy chỉnh giao diện. Không cần viết code." = "Tăng tốc mạnh mẽ nhờ AI"
Xem `shopify-1440-07`.
- Tiêu đề 56px hai dòng "Tùy chỉnh giao diện. / Không cần viết code."
- Đoạn văn 18px: "Site Studio có 8 mẫu giao diện. Chỉnh bản nháp, xem trước, xuất bản khi ưng và khôi phục phiên bản cũ khi cần. Gắn tên miền riêng ngay trong đó."
- Hình: một khung lớn rộng hết container, cao khoảng 480px, có quầng sáng navy/teal. Bên trong là **cửa sổ Site Studio**:
  - thanh tiêu đề có ba chấm và nhãn "Site Studio · Bản nháp";
  - cột trái liệt kê các khối ("Đầu trang", "Banner", "Bộ sưu tập", "Sản phẩm nổi bật", "Chân trang"), một khối đang chọn;
  - bên phải xem trước cửa hàng (tái dùng component mô phỏng ở S2).
- Quanh cửa sổ nổi 3 viên nhãn: "Chọn mẫu giao diện", "Xem trước rồi xuất bản", "Khôi phục phiên bản cũ". Thêm một huy hiệu tròn teal có `IconSliders`, thay huy hiệu teal của Shopify.
- Mobile: chỉ giữ cửa sổ, thu nhỏ, không có viên nhãn.

### S10. Dải pine: thanh toán = "Không có nơi nào lý tưởng hơn…" + trang thanh toán
Xem `shopify-1440-08`.
- Dải mới, bo 48px ở góc trên, nền `--home-pine`, kèm quầng sáng teal mờ ở góc.
- Tiêu đề căn giữa 64px: "Bán cho khách Việt, theo cách khách Việt mua".
- Bên dưới chia hai cột.
  - **Trái:**
    - `h3` 44px "Thanh toán quen thuộc với khách Việt";
    - hai cột "chỉ số" bằng chữ, không có số: nhãn nhỏ viết hoa 12px màu `--home-accent`, mỗi nhãn kèm icon ("THU TIỀN KHI GIAO" kèm `IconTruck`, "TỰ XÁC NHẬN KHI TIỀN VỀ" kèm `IconCheck`), bên dưới là chữ lớn 56px weight 300 "COD" và "VietQR";
    - đoạn văn có vạch trái teal: "Khách chọn thanh toán khi nhận hàng hoặc quét mã VietQR. Đơn chuyển khoản tự xác nhận khi tiền về, bạn không phải đối soát tay."
  - **Phải:**
    - thẻ trang thanh toán màu trắng, giữ nội dung mô phỏng vòng 1: tóm tắt đơn, chọn COD/VietQR, mã QR vẽ bằng `VietQrSvg`, tiền qua `formatVnd`/`Price`;
    - quanh thẻ có 6–8 ảnh sản phẩm `/products/*.png` cỡ 56–88px, bo 5px, xoay nhẹ, trôi lên xuống chậm bằng keyframes. Các ảnh này mang `aria-hidden` và `alt=""`.
- Mobile: theo `shopify-390-08`. Chữ ở trên, thẻ ở dưới, ảnh nổi ít đi.

### S11. FAQ (đặt vào chỗ quả địa cầu, vẫn trên nền pine)
- Bên trái: `h2` 44px "Câu hỏi thường gặp".
- Bên phải: giữ đúng 5 câu `PLATFORM_FAQS` và cơ chế `<details name="platform-faq">` (mở câu này thì đóng câu khác).
  - Mỗi dòng ngăn nhau bằng một đường mảnh `rgb(255 255 255 / .12)`.
  - Câu hỏi 20px trắng, dấu +/− ở bên phải; câu trả lời 16px `--home-ink-2`.
  - Không tô nền khi mở.
- Phần này dùng đường kẻ thay cho hộp: đây là chủ ý, không bị luật "đường kẻ trang trí" chặn.

### S12. "Lên sóng trong ba bước" = "Xây dựng nhanh chóng"
Xem `shopify-1440-10/11`.
- Dải mới, bo 48px ở góc trên. Nền gần đen kèm gradient như Shopify: `linear-gradient(rgb(2 8 8 / .5), transparent 20%, transparent 90%, rgb(2 8 8 / .5))` chồng với một `radial-gradient` teal mờ ở góc dưới bên phải.
- Tiêu đề căn giữa 64px "Lên sóng trong ba bước".
- Bên trái: 2 ảnh đứng so le, bo 5px, ảnh phải thấp hơn ảnh trái, dùng ảnh chưa xuất hiện ở khối khác.
- Bên phải: danh sách 3 bước. Số `01` `02` `03` màu `--home-accent` 16px; chữ bước 32px weight 330 trắng ("Chọn giao diện riêng", "Đăng sản phẩm lên kệ", "Nhận đơn, quét VietQR"); mỗi bước có đường mảnh bên dưới.
- Dưới danh sách là **`SubdomainPreview`**: giữ nguyên logic `checkSubdomain`/`suggestSubdomain`, đích link và kết quả kiểu `xuong-det-bach-dang.{rootDomain}`. Restyle theo trang mới, nút trắng viên thuốc.
- Mobile: theo `shopify-390-10/11`.

### S13. Footer (`PlatformFooter`)
Xem `shopify-1440-11`.
- Nền `--home-bg` đen. Bên trái là `NexusMark` trắng cỡ khoảng 32px. Bốn cột: tiêu đề 20px weight 450 trắng, link 14px `--home-ink-3`, mỗi link cách nhau khoảng 12px.
  - "NexusMart": Về NexusMart, Trợ giúp.
  - "Người bán": Mở gian hàng → `/dang-ky?role=seller`, Kênh người bán → `/seller`, Đăng nhập → `/dang-nhap`.
  - "Mua sắm": Các cửa hàng → `/#danh-ba-gian-hang`.
  - "Liên hệ": link email hiện có.
- Một đường mảnh rồi hàng dưới: "© 2026 NexusMart · Nền tảng cửa hàng độc lập". Không link pháp lý hay mạng xã hội giả.
- Không mất link nào đang có. Mobile: 2 cột.
- Footer dùng chung mọi trang `(shop)`: kiểm `/ve-nexusmart` và `/tro-giup` vẫn đẹp.

### Ảnh
- Không ảnh nào lặp lại trong trang, trừ ảnh sản phẩm nhỏ trong mô phỏng.
- Kho ảnh: `public/banners/*` (6 ảnh, 1365px), `public/atmosphere/*`, `public/models/*` (18 ảnh), `public/products/*` (18 ảnh PNG nền xám).
- Ảnh dùng `next/image` với `sizes` đúng. Ảnh trang trí để `alt=""`. Ảnh mang nội dung có `alt` tiếng Việt.

## 7. Nội dung thật được phép dùng

Được nhắc những tính năng sau:
- **Cửa hàng:** cửa hàng riêng trên tên miền phụ `*.{rootDomain}` hoặc tên miền riêng; danh bạ các cửa hàng đã xuất bản.
- **Site Studio:** 8 mẫu giao diện, chỉnh bản nháp, xem trước, xuất bản, khôi phục phiên bản cũ, gắn tên miền riêng.
- **Thanh toán:** COD và VietQR, đơn chuyển khoản tự xác nhận khi tiền về.
- **Giao hàng:** phí ship tính theo 34 tỉnh thành và phường.
- **Kênh người bán:** sản phẩm, nhập CSV, tồn kho, đơn hàng, in nhãn, trả hàng/đổi trả, khuyến mãi, voucher, flash sale, tài chính, Báo cáo AI (hỏi bằng tiếng Việt), chat với khách.
- **Trợ lý AI trên cửa hàng:** chỉ xem được những gì khách đó được phép xem.

Không dùng thuật ngữ kỹ thuật (JWT, saga, Redis…). Mọi chữ hiển thị bằng tiếng Việt tự nhiên.

## 8. Ràng buộc kỹ thuật

- CSS Modules + token. Không Tailwind, không thêm package npm. Inter qua `next/font/google` là font, không phải dependency.
- Mặc định là server component. Chỉ dùng `"use client"` khi cần state hoặc event (`SubdomainPreview`, header). Không truyền `onClick` từ server component. Chữ luân phiên và ảnh trôi làm bằng CSS, không cần client component.
- A11y:
  - đúng một `h1`, heading theo thứ tự hợp lý;
  - mỗi section có `aria-labelledby`;
  - focus thấy rõ trên mọi nền tối;
  - mô phỏng là `role="img"` kèm `aria-label` "Minh họa …";
  - trình đọc màn hình không đọc mã đơn, giá giả như nội dung thật.
- Hiệu năng:
  - chỉ ảnh hero dùng `priority`;
  - không video;
  - tường icon dựng bằng một mảng nhỏ lặp trong code, không viết tay hàng trăm phần tử;
  - cột First Load JS của route `/` trong bảng `next build` hiện là **121 kB** (Size 4.79 kB, đo ở HEAD `02b6ead`); sau khi sửa không được vượt 131 kB.
- Không để code chết: component hay CSS không còn ai dùng thì xoá. Kiểm bằng `rg` trước khi xoá.
- Prettier và ESLint sạch.

## 9. Tự kiểm trước khi báo xong

### Lệnh kiểm (chạy trong `web/`)
```bash
npm run lint
npm run format:check
npm run test:unit
npm run check:data
NEXT_DIST_DIR=.next-agy npm run build
git checkout -- tsconfig.json next-env.d.ts   # build với NEXT_DIST_DIR sửa cả hai file này
```

### So ảnh bằng mắt (bắt buộc)
- Chạy bản build vừa dựng trên một cổng trống, ví dụ 3411. **Không dùng cổng 3111, 3300, 4777, 8080.** Chụp bằng `shot.cjs` ở độ rộng 1440 và 390, rồi đặt từng khối cạnh ảnh Shopify tương ứng để so. Sửa tới khi bố cục, cỡ chữ, nhịp và màu từng dải khớp. Không có gateway thì danh bạ rơi vào trạng thái lỗi; như vậy vẫn đủ để soát hình.
- Tắt server bằng PID lấy từ `ss -ltnp | grep :3411`. Không dùng `pkill -f`, vì nó khớp cả shell đang chạy lệnh.
- Không tràn ngang ở 390, 768 và 1440.
- Tương phản: đo ở **điểm sáng nhất của ảnh hoặc gradient nằm sau chữ**, không đo ở màu trung bình. Chữ thường đạt từ 4.5:1, chữ lớn từ 3:1. Hero là chỗ dễ trượt nhất.

### Không được làm
`make up`, `make seed`, `make e2e*`, `docker`, commit, push. Claude Code sẽ tự chạy `make e2e-web` và review.

## 10. Báo cáo khi xong

- Danh sách file tạo, sửa, xoá.
- Kết quả từng lệnh kiểm (dán dòng tóm tắt) và First Load JS của `/` sau khi sửa.
- Đường dẫn các ảnh tự chụp.
- Chỗ nào lệch brief và vì sao; phần chưa làm.
