# Redesign UI/UX toàn web – đặc tả chung (2026-11-24)

Yêu cầu của chủ dự án: (1) một font chung, đẹp; (2) thiết kế lại toàn bộ layout đẹp hơn, đa dạng, bắt mắt; (3) animation mượt, hiện đại; (4) ít chữ, đơn giản, tối giản.
Nền: nhánh `redesign/ui` tách từ `phase-0b` (bản đang chạy ở nexusmartai.store). Chỉ đổi giao diện: KHÔNG đổi API, schema, proto, service Go/Rust, luồng nghiệp vụ, đường dẫn.

## 0. Bệnh của bản cũ (đã chụp, đừng lặp lại)
- Font lẫn trong cùng một trang: `sai-gon` tiêu đề Be Vietnam Pro, nút Work Sans; `da-nang` IBM Plex.
- Cửa hàng nào cũng mở đầu "chữ trái, ảnh phải". 8 giao diện nhìn như một.
- Hàng 2 sản phẩm, hàng 1 voucher để trống nửa màn hình. Vạch trái màu trên thẻ, gạch chân, viền, hộp trong hộp.
- Trang chủ lặp một ý ở 5 mục, đoạn văn 3–4 dòng khắp nơi, chú thích ảnh vô nghĩa.

## 1. Ý tưởng
**Nền tảng thì lặng, mỗi cửa hàng mang màu của mình.** Trên trang này, *địa chỉ `ten-ban.nexusmartai.store`* trở thành *cánh cửa*: gõ tên thương hiệu thì cửa hàng trước mặt đổi tên, đổi màu, đổi bố cục theo; kéo xuống là các cửa hàng thật mở ra toàn màn hình.

## 2. Font: Archivo, duy nhất
- `next/font/google` `Archivo`, variable (không truyền `weight`), `axes: ["wdth"]`, `subsets: ["latin","vietnamese"]`, `variable: "--font-archivo"`, `display: "swap"`. Nạp một lần ở `src/app/layout.tsx`. Xoá Work Sans, Be Vietnam Pro, IBM Plex, mọi stack monospace (trừ ô soạn JSON template).
- Mọi chỗ (nền tảng, cửa hàng, người bán, quản trị, email OTP nếu ở web) dùng `--font-sans: var(--font-archivo), system-ui, sans-serif`. `resolveSiteFontStack()` luôn trả Archivo; giữ enum/schema `font_family` nguyên vẹn (backend ép `allowedFonts`) và gửi lại nguyên giá trị đang lưu; ẩn ô chọn font khỏi trình chỉnh sửa.
- Đa dạng bằng **trục độ rộng + độ đậm**, không bằng font thứ hai. Giọng (token): `--type-wide` (wdth 118, 650), `--type-normal` (100, 560), `--type-tight` (80, 680), `--type-poster` (66, 800). Dùng `font-stretch` + `font-weight`.
- Tiếng Việt có dấu hai tầng (Ỗ, Ữ, ậ): display `line-height` ≥ 1.04, mặt nạ dòng phải chừa `padding-block: .12em`; không cắt dấu ở bất kỳ cỡ nào.
- Số tiền/bảng: `font-variant-numeric: tabular-nums`. Ký tự `₫` phải hiện bằng Archivo.
- 5 bậc chữ cho trang, 5 bậc cho app (token). Không cỡ dưới 13px. Thân bài ≥ 16px ở trang công khai, 14px ở app.

## 3. Màu
Giữ màu thương hiệu Frost and Slate (`--ink`, `--accent` teal). Nền tảng: nền gần trắng, chữ ink, teal chỉ cho hành động chính; màu rực đến từ theme thật của cửa hàng (các "sân khấu" màu phẳng). Cửa hàng: chỉ dùng biến `--site-*` sẵn có. Không gradient, không shadow lúc nghỉ, không glass/blur nền, không viền trang trí, không vạch màu bên trái thẻ. Phân lớp bằng bậc màu và khoảng thở. Tương phản ≥ 4.5:1.
Bo góc: trang công khai 0 hoặc tròn hẳn (nút pill) theo `radius` theme; app 8/6px; sheet 16px.

## 4. Chữ: cắt mạnh
- Ngân sách: màn đầu ≤ 18 từ (không kể nhãn nút/điều hướng). Mỗi mục: 1 tiêu đề ≤ 6 từ + tối đa 1 câu ≤ 14 từ, hoặc không câu nào. Tiêu đề ≤ 2 dòng ở mọi cỡ màn.
- Xoá: chú thích ảnh trang trí, câu giải thích điều người dùng đã thấy, mô tả dưới mọi tiêu đề bảng, văn cam kết lặp, "Quản lý và theo dõi…", mục trùng ý.
- Giữ: giá, tồn, size, màu, trạng thái đơn, lỗi form, chính sách bắt buộc (đưa vào accordion/`<details>`), nhãn form, văn bản trợ năng (`aria-label`, `sr-only`), nội dung do người bán nhập (không xoá dữ liệu; có thể thu gọn sau "Xem thêm").
- Thẻ sản phẩm: ảnh, tên (1 dòng), giá. Chất liệu/định lượng chuyển vào PDP. Nhãn "Sẵn hàng" bỏ (chỉ hiện "Hết hàng"/"Còn N" khi N ≤ 5).
- Tiếng Việt tự nhiên, câu ngắn, không khẩu hiệu sáo, không thuật ngữ nội bộ, không bịa số liệu/khách hàng.

## 5. Chuyển động (CSS trước, không thêm dependency)
Token: `--ease-out cubic-bezier(.22,1,.36,1)`, `--ease-expo cubic-bezier(.16,1,.3,1)`, `--ease-in-out cubic-bezier(.76,0,.24,1)`, `--ease-exit cubic-bezier(.55,.055,.675,.19)`; `--t-press 140ms`, `--t-hover 200ms`, `--t-ui 280ms`, `--t-reveal 800ms`, `--t-open 1200ms`; so le 70–90ms.
- Chỉ `transform`, `opacity`, `clip-path` (và `grid-template-columns` cho đúng dãy "cánh cửa" ≤ 6 phần tử). 60fps, không layout shift.
- Nội dung luôn có trong HTML và nhìn thấy khi không có JS: trạng thái ẩn chỉ áp dưới `html.js` + `prefers-reduced-motion: no-preference` (script một dòng trong `layout.tsx`).
- Cuộn: ưu tiên `animation-timeline: view()` trong `@supports`, dự phòng IntersectionObserver dùng chung (gộp các observer trùng hiện có thành MỘT hook/thành phần).
- Đa dạng theo nội dung: ảnh mở từ `clip-path`; tiêu đề lên từng dòng trong mặt nạ; danh sách vào từng hàng; số liệu chạy một lần; thẻ sản phẩm hover đổi ảnh thứ hai (nếu có) + nhích scale ≤ 1.04. Cấm đóng dấu cùng một fade-up cho mọi khối.
- Chuyển trang: `template.tsx` mỗi route group (vào 320ms, CSS). Thử `experimental.viewTransition` + `<ViewTransition>` cho ảnh thẻ → ảnh PDP; CHỈ giữ nếu build sạch, không lỗi console, dự phòng tốt ở trình duyệt không hỗ trợ; không thì bỏ, ghi lý do.
- Nút/liên kết/thẻ/ô nhập đều có phản hồi hover, focus, press. Drawer, dialog, menu, toast, accordion mở/đóng có chuyển động và đóng nhanh hơn mở. Trạng thái tải là skeleton đúng hình khối, không spinner giữa trang.
- `prefers-reduced-motion: reduce`: giữ trạng thái cuối, bỏ di chuyển.
- Không preloader, không chặn cuộn, không hijack wheel, không nảy/elastic.

## 6. Luật bố cục
Một lề ngoài bằng nhau hai bên; không mục nào dồn một phía để trống phía kia; không khoảng trống quá nửa màn hình; không hàng lưới mồ côi (1–2 sản phẩm thì đổi sang bố cục lớn lấp đầy hàng); hai mục liền nhau không cùng bố cục; không hàng thẻ giống hệt kiểu "3 icon"; không nhãn nhỏ trên tiêu đề; không đánh số 01/02/03; đúng một `h1`. Mobile 390 là bố cục riêng, mục tiêu chạm ≥ 44px, không tràn ngang ở 360.
Ảnh có sẵn trong `web/public` (banners, models, products, atmosphere + video loom). Không tải ảnh ngoài, không vẽ ảnh giả.

## 7. Bất biến (test phụ thuộc, KHÔNG được làm vỡ)
- Trang gốc `/`: có "NexusMart", có "Mở gian hàng", KHÔNG có giá tiền (chuỗi số kèm ₫/đ), KHÔNG link `/san-pham/`, `/gio-hang`, `/thanh-toan`, `/tim-kiem`, `/danh-muc`, `/flash-sale`, `/voucher`, KHÔNG "Thêm vào giỏ", không slug sản phẩm; danh bạ SSR từ gateway có link + tên mọi site trang đó, phân trang `?trang=`.
- Mọi cửa hàng: "Vận hành trên … NexusMart" (cách nhau ≤ 120 ký tự HTML); các thuộc tính `data-site-*` (root, theme-mode, archetype, header, footer, container, card…); biến màu theme trong HTML; tên shop, hero title, mã voucher hiện trong HTML trang chủ site; preview bản nháp.
- Mọi `id`, `name`, `role`, `aria-label`, nhãn nút mà `tests/playwright/specs/buyer-flow.spec.ts` và `tests/e2e-web/*.mjs` dùng (đọc cả hai trước khi sửa). Không sửa test để hợp với UI.
- Không đổi `src/app/api/**`, `src/middleware.ts`, `src/lib/server/**`, `src/lib/site/{host,domains,cache,seo}.ts`, hình dạng dữ liệu, SEO metadata/JSON-LD, luồng CSRF/phiên.

## 8. Luật làm việc
- Chỉ sửa file mình sở hữu (ghi trong brief). Cần file của người khác: báo trong báo cáo, không sửa.
- Không `git commit/push/checkout/stash/reset/restore`. Không `docker compose` (trừ brief QA). Không `sudo`, không kill tiến trình không phải của mình, không đụng `/home/tai/Document/nexusmart*` khác.
- Không thêm dependency. CSS Modules + token, không Tailwind.
- Máy ít RAM và hay khởi động lại: ghi file đều tay; KHÔNG chạy `next build` ngoài khoá: `flock /tmp/nm-redesign-build.lock env NEXT_DIST_DIR=.next-<tên> npm run build` rồi xoá `.next-<tên>` và hoàn nguyên phần Next tự chèn vào `tsconfig.json`/`next-env.d.ts` bằng sửa tay.
- Xem thật: `next dev` dùng chung do manager chạy ở cổng 53301 (`http://nexusmart.localhost:53301`, `http://bach-dang.nexusmart.localhost:53301`…). Chụp bằng `google-chrome --headless=new --window-size=W,H --screenshot=…` ở 390, 768, 1440 vào `.runs/shots/<tên>/` và MỞ ẢNH RA NHÌN trước khi báo xong. Dev server không chạy thì tự chạy ở cổng trong brief, tắt khi xong.
- Gate trước khi báo xong (trong `web/`): `npx eslint <file sửa>`, `npx prettier --check <file sửa>`, `npx tsc --noEmit`, `npm run test:unit`.
- Báo cáo cuối: file đã đổi, việc đã làm, chưa làm, lệnh + đầu ra thật, số từ màn đầu trước/sau, đường dẫn ảnh chụp.
