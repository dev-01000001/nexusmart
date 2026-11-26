# Brief: làm lại trang chủ nền tảng theo bố cục trang chủ Shopify

Ngày: 2026-11-26. Người giao: Claude Code (manager). Coder: agy (một phiên, không swarm).

## Mục tiêu
Chủ dự án muốn trang chủ `/` của host gốc (nexusmartai.store) **giống trang chủ shopify.com/vn về bố cục và ngôn ngữ thị giác**, chỉ đổi màu theo theme NexusMart và đổi logo. Trang hiện tại ([PlatformHome.tsx](../../web/src/features/platform/PlatformHome.tsx)) bị chê: khối "Cửa hàng đang mở" trống chỉ là một hộp xám, "Ba bước lên sóng" có cột trái trống mênh mông, hero hở một khoảng lớn giữa tiêu đề và ô nhập, video khung cửi mờ và không ăn nhập.

Ảnh tham chiếu Shopify (viewport 1440×900, chụp lần lượt từ trên xuống): `.playwright-mcp/shop-00.jpg` … `shop-12.jpg`; ảnh ghép tổng quan `.playwright-mcp/shop-sheet-1.jpg`, `shop-sheet-2.jpg`, `shop-sheet-3.jpg`. Ảnh trang hiện tại: `.playwright-mcp/home-part0.png` … `home-part3.png`. **Mở xem các ảnh này trước khi code.**

## Điều cấm tuyệt đối (bản quyền và trung thực)
- Không chép chữ, ảnh, video, logo của Shopify hay thương hiệu khách của họ (Glossier, Steve Madden, Mattel, Houseplant, KOTN…), không chép số liệu ("15%", "250 triệu+"), không lời chứng thực có tên người, không nhắc tên Shopify trong giao diện.
- Không bịa số liệu, khách hàng, doanh thu, hay tính năng NexusMart không có. Chỉ nói những gì nền tảng làm được thật (liệt kê ở mục "Nội dung thật được phép dùng").
- Chỉ dùng ảnh có sẵn trong `web/public/` (không tải ảnh mới).

## Phạm vi file
Được sửa / tạo / xoá:
- `web/src/features/platform/**`, **trừ** `PlatformAuthShell.*`, `subdomain.ts`, `directory.server.ts`, `directoryModel.ts`, `legacy*.ts`, `NotFoundView.*`, `platform.test.mjs` (không sửa các file này).
- `web/src/app/(shop)/page.tsx` chỉ khi thật cần (luồng dữ liệu `parseDirectoryPage` → `loadSiteDirectory` → `PlatformHome` giữ nguyên).

Cấm sửa: mọi thứ khác — `web/src/styles/tokens.css`, `globals.css`, `(site)`, `(seller)`, `(admin)`, `web/src/components/**`, `services/`, `tests/`, `docs/`, `package.json`.

## Bố cục mới (trên xuống), ánh xạ từ Shopify
Container nội dung: `max-width` khoảng 1200px, căn giữa, lề hai bên `var(--margin)` (Shopify đặt nội dung trong cột giữa khoảng 1110px, mép trái thẳng hàng giữa header, hero và các section).

0. **Header (chỉ trên trang chủ)** — `PlatformHeader` đã có `isHome` và `data-surface="transparent"` khi ở đầu trang chủ. Chỉnh nhánh trang chủ: nền trong suốt, chữ trắng trên hero; khi cuộn xuống thì nền tối đặc (`--ink-1`), không chuyển sang nền sáng. Nút "Mở gian hàng" là viên thuốc trắng chữ tối, "Đăng nhập" là link chữ trắng (giống "Bắt đầu miễn phí"/"Đăng nhập" của Shopify). Logo `NexusMark` + chữ NexusMart màu trắng. Trang khác (`/ve-nexusmart`, `/tro-giup`, trang đăng nhập…) **giữ nguyên như hiện tại**. Giữ nguyên menu tài khoản, drawer mobile, focus trap, ẩn/hiện khi cuộn.
1. **Hero** (cao `min(100svh, 900px)`, tối thiểu ~640px): ảnh nền tràn viền `/banners/campaign-office.jpg` (`object-fit: cover`, người nằm bên phải, chữ bên trái). Phủ một lớp màu tối **phẳng** (ví dụ `color-mix(in srgb, var(--ink-1) 55%, transparent)`), không gradient. Được phép `filter: blur(2–4px)` + `transform: scale(1.04)` cho ảnh để giống hero mờ của Shopify và che việc ảnh chỉ rộng 1365px. Nội dung:
   - `h1` "Cửa hàng / mang tên bạn" (2 dòng), trắng, nét mảnh (xem Typography).
   - Đoạn phụ 2 dòng, ví dụ: "Gõ tên thương hiệu, có ngay cửa hàng riêng trên địa chỉ của bạn. / Giao diện riêng, khách riêng, thanh toán COD và VietQR."
   - Hai nút viên thuốc: "Mở gian hàng" (trắng, chữ tối) → `/dang-ky?role=seller`; "Xem các cửa hàng" (viền trắng, chữ trắng) → `#danh-ba-gian-hang`.
2. **Danh bạ** = khối "Bán hàng ở mọi nơi" của Shopify. Section **giữ `id="danh-ba-gian-hang"`** và `aria-labelledby="platform-directory-heading"` (header, footer và `/ve-nexusmart` đều trỏ vào neo này). Nền `--ink-1`, bo góc trên ~40px, kéo đè lên đáy hero ~40px như Shopify. Tiêu đề hai tông: phần đầu trắng "Bán trên địa chỉ của riêng bạn." + phần sau tông xám "Giao diện riêng. Khách riêng. Thanh toán ngay trên cửa hàng." Dưới là một hàng thẻ ảnh bo góc kiểu khảm (rộng/hẹp/rộng như Shopify), trên màn hẹp thành hàng cuộn ngang `scroll-snap`.
   - `directory.ok` và có site: thẻ là cửa hàng thật (tên, nhãn địa chỉ host bên dưới giống nhãn "stevemadden.com" của Shopify, bấm mở site), tối đa 6 thẻ; giữ phân trang hiện có (`pageHref`, `buildPageList`, nút trước/sau, `aria-current`) restyle tối; hiện số "N gian hàng".
   - `directory.ok` và rỗng: hiện 4 thẻ từ `SHOWCASE_TEMPLATES` (ảnh + tên thương hiệu mẫu + bảng màu mẫu). **Mỗi thẻ có nhãn rõ "Mẫu giao diện · {label}"** để không ai tưởng là cửa hàng thật. Kèm một dòng "Chưa có cửa hàng nào xuất bản. Cửa hàng đầu tiên có thể là của bạn." và link "Mở gian hàng" → `/dang-ky?role=seller`.
   - `!directory.ok`: hiện trạng thái lỗi (giữ nội dung `DirectoryErrorState`, restyle tối) và vẫn hiện các thẻ mẫu như trên.
3. **Khối thẻ lớn "trợ lý AI"** (= "Thương hiệu của bạn đã tham gia trò chuyện"): một thẻ lớn bo góc nền teal pha tối (`color-mix(in srgb, var(--accent) 22%, var(--ink-1))`). Trái: tiêu đề "Trợ lý AI trả lời khách thay bạn" + đoạn "Khách hỏi size, chất liệu, tình trạng đơn ngay trên cửa hàng. Trợ lý chỉ xem được những gì khách đó được phép xem." Phải: mô phỏng bằng HTML/CSS một bong bóng chat của khách ("Áo polo này có size L không?") + ảnh sản phẩm `/models/model-polo-xanh-da.jpg` + câu trả lời ngắn. Không dùng ảnh chụp màn hình.
4. **"Một nơi cho cả cửa hàng"** (= "Hiện diện ở mọi nơi"): tiêu đề hai tông "Một nơi cho cả cửa hàng. / Không cần ghép nhiều công cụ." + 3 thẻ (ảnh/mô phỏng UI phía trên, tiêu đề + 1–2 câu phía dưới):
   - "Giao diện mang dấu ấn riêng" — mô phỏng mini một cửa hàng (tái dùng một layout stage từ `HeroLiveDoor`); "8 mẫu giao diện, xem trước rồi mới xuất bản, gắn được tên miền riêng."
   - "Đơn hàng ở một chỗ" — mô phỏng danh sách 3 đơn với trạng thái; "Đơn, tồn kho, in nhãn giao hàng và đổi trả trong Kênh người bán."
   - "Giao khắp 34 tỉnh thành" — mô phỏng ô tính phí ship (tỉnh, phường, phí); "Tính phí theo tỉnh và phường, theo dõi trạng thái giao hàng."
5. **Thanh toán** (= khối checkout của Shopify, **không có số liệu**): tiêu đề "Thanh toán quen thuộc với khách Việt". Trái: hai mục lớn "COD — Thu tiền khi giao hàng" và "VietQR — Khách quét mã, đơn tự xác nhận khi tiền về". Phải: mô phỏng trang thanh toán (tóm tắt đơn với giá dạng `₫` số nguyên, lựa chọn COD/VietQR, mã QR). Tái dùng `VietQrSvg` từ `PlatformPinnedStory.tsx` (chuyển sang file mới nếu xoá file cũ).
6. **"Dành cho mọi quy mô"** (= "Dành cho tất cả, từ doanh nhân đến doanh nghiệp lớn"): 3 thẻ ảnh bo góc + tiêu đề + 1 câu, **không tên người, không con số**:
   - "Xưởng may" — `/banners/vietnam-workshop.jpg` — "Bán thẳng sản phẩm của xưởng tới người mua."
   - "Thương hiệu thời trang" — `/banners/campaign-everyday.jpg` — "Giữ nhận diện thương hiệu trên từng trang."
   - "Shop mới bắt đầu" — `/banners/campaign-green-fiber.jpg` — "Bắt đầu với vài sản phẩm, mở rộng khi đơn tăng."
   - Nút viền viên thuốc căn giữa "Mở gian hàng" → `/dang-ky?role=seller`.
7. **FAQ**: giữ nguyên 5 câu hỏi/đáp hiện có (`PLATFORM_FAQS`) và cơ chế `<details name="platform-faq">`, restyle tối theo phong cách Shopify (tiêu đề trái, danh sách phải, không nền màu đặc khi mở; chữ trắng/xám).
8. **"Lên sóng trong ba bước"** (= "Xây dựng nhanh chóng trên Shopify"): tiêu đề căn giữa; trái 2 ảnh so le (`/models/model-ao-thun-trang.jpg`, `/banners/fabric-closeup.jpg`); phải danh sách số `01 Chọn giao diện riêng`, `02 Đăng sản phẩm lên kệ`, `03 Nhận đơn, quét VietQR` (số màu teal sáng, chữ lớn nét mảnh, cách nhau bằng khoảng trắng). Dưới danh sách đặt **`SubdomainPreview`** (ô gõ tên thương hiệu → slug + nút mở gian hàng, giữ nguyên logic `checkSubdomain`/`suggestSubdomain` và đích link hiện có), restyle tối.
9. **Footer** (`PlatformFooter`, dùng chung mọi trang (shop)): kiểu Shopify — nền tối, logo `NexusMark` trái, các cột link: "NexusMart" (Về NexusMart, Trợ giúp), "Người bán" (Mở gian hàng `/dang-ky?role=seller`, Kênh người bán `/seller`, Đăng nhập `/dang-nhap`), "Mua sắm" (Các cửa hàng `/#danh-ba-gian-hang`), "Liên hệ" (`hotro@nexusmart.vn`). Hàng dưới: "© 2026 NexusMart · Nền tảng cửa hàng độc lập". Bỏ chữ NexusMart khổng lồ. Không mất link nào đang có.

Bỏ khỏi trang: `PlatformPinnedStory` (cuộn ghim), `PlatformLoomSection` + `AmbientLoomVideo` (video khung cửi), `PlatformClosingCta`, hero cũ `HeroLiveDoor` (phần stage dùng lại thì chuyển sang component mới). Sau khi xong, **xoá file không còn ai import** (kiểm bằng `rg` trước khi xoá; `web/src/data/media.ts` giữ nguyên). Không để code chết.

## Ngôn ngữ thị giác
- **Màu (chỉ dùng token sẵn có, có thể pha bằng `color-mix`)**: nền trang `--ink-1`; thẻ `--band-dark` / `--band-dark-surface`; chữ chính `--band-dark-ink`; tông xám thứ hai của tiêu đề hai tông `color-mix(in srgb, var(--band-dark-ink) 55%, var(--ink-1))`; điểm nhấn nhỏ (số bước, chấm trạng thái, link) `--band-dark-accent`; nền khối nhấn `color-mix(in srgb, var(--accent) 22%, var(--ink-1))`. Nút chính: nền trắng `--bg-0`, chữ `--ink-1`. Mọi cặp chữ/nền đạt WCAG AA (chữ thường ≥ 4.5:1, chữ lớn ≥ 3:1).
- **Typography**: chỉ Archivo (đã nạp ở `src/app/layout.tsx`, trục `wdth`, `wght` biến thiên). Tiêu đề hiển thị kiểu Inter mảnh của Shopify: `font-stretch: 100%`, `font-weight` 300–350, `letter-spacing` −0.02em đến −0.035em, `line-height` ~1.05. `h1` khoảng `clamp(3rem, 6.2vw, 5.75rem)`; tiêu đề section khoảng `clamp(2rem, 3.6vw, 3.25rem)`; chữ thân 16–18px weight 400. Không dùng các lớp `type-wide` / `type-tight` trên trang này (chữ rộng/hẹp là nguyên nhân lệch tông hiện tại).
- **Hình khối**: thẻ và ảnh bo góc ~16px, nút viên thuốc (`border-radius: 999px`). Không gradient, không đổ bóng tĩnh, không đường kẻ trang trí.
- **Khoảng cách**: section dọc ~`clamp(72px, 10vw, 128px)`; mép trái mọi khối thẳng hàng.
- **Chuyển động**: dùng cơ chế sẵn có `data-reveal` (RevealObserver toàn cục) cho fade/rise nhẹ; tôn trọng `prefers-reduced-motion`. Không thêm thư viện.
- Mobile 390px: hero chữ xếp chồng, nút xuống dòng gọn; mọi hàng thẻ thành cuộn ngang hoặc xếp dọc; không tràn ngang trang ở 390 / 768 / 1440.
- Ảnh dùng `next/image` (`fill` + `sizes` hợp lý); ảnh hero `priority`, có `alt` tiếng Việt; ảnh trang trí thuần thì `alt=""`.

## Nội dung thật được phép dùng
Cửa hàng riêng trên tên miền phụ hoặc tên miền riêng; 8 mẫu giao diện, xem trước, xuất bản, khôi phục bản cũ; thanh toán COD và VietQR tự xác nhận; tính phí ship theo 34 tỉnh / phường; Kênh người bán (sản phẩm, nhập CSV, tồn kho, đơn, in nhãn, đổi trả, khuyến mãi, flash sale, báo cáo AI); trợ lý AI trên cửa hàng; chat với khách; danh bạ các cửa hàng đã xuất bản. Không dùng thuật ngữ kỹ thuật (JWT, saga, Redis…).

## Ràng buộc kỹ thuật
- CSS Modules, không Tailwind, không thêm dependency.
- Component client (`"use client"`) chỉ khi cần state/event (ví dụ ô nhập). Không truyền `onClick` từ server component.
- Giữ a11y: một `h1`, thứ tự heading hợp lý, mỗi section có `aria-labelledby`, nút/link có tên rõ, focus thấy được trên nền tối.
- Prettier/ESLint sạch.

## Lệnh kiểm phải chạy xanh trước khi báo xong (chạy trong `web/`)
```bash
npm run lint
npm run format:check
npm run test:unit
npm run check:data
NEXT_DIST_DIR=.next-agy npm run build
git checkout -- tsconfig.json   # build với NEXT_DIST_DIR có thể chèn dòng vào tsconfig.json
```
Không chạy `make up`, `make seed`, `make e2e*`, không rebuild/khởi động container, không `docker`, không commit, không push.

## Báo cáo khi xong
- Danh sách file tạo/sửa/xoá.
- Kết quả từng lệnh kiểm (dán dòng tóm tắt).
- Chỗ nào lệch brief và vì sao; phần chưa làm.
