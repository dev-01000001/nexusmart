# ADR-0001: Định dạng Template cho Site Gian hàng (`Declarative Theme JSON v2`)

- **Trạng thái:** Đã phê duyệt (biểu quyết 3/3 nhất trí)
- **Ngày chốt:** 2026-11-21
- **Phạm vi:** Swarm T (`services/user/internal/site/`, `services/gateway/`, `web/src/features/site/`, `web/src/app/(site)/`, `web/src/features/seller/site/`, `web/src/app/(seller)/seller/site/`)
- **Thành phần biểu quyết:** 3 worker độc lập (`prop-json`, `prop-liquid`, `prop-mdx`) trên bảng tin Swarm T (tin nhắn `#4`–`#10`).

---

## 1. Bối cảnh và Yêu cầu

Hệ thống Site riêng Giai đoạn 2 (`docs/PHASE2_REPORT.md`) đang lưu cấu hình site dưới dạng JSON strict v1 (`{theme, sections}` với 7 loại section phẳng, kiểm tra tương phản WCAG ≥ 4.5:1, không cho phép HTML/JS/CSS tùy ý, có bản nháp, xuất bản bất biến `site_versions`, và khôi phục phiên bản).

Yêu cầu mở rộng:
- Cung cấp thư viện **≥ 8 template khác nhau thực sự về bố cục** (không chỉ đổi màu): Thời trang tối giản, Editorial, Lưới dày, Xưởng thủ công, Thể thao, Cao cấp, Một trang, Catalog nhiều SKU — mỗi template phủ đủ 5 loại trang: **Trang chủ, Danh sách sản phẩm, Chi tiết sản phẩm (PDP), Giỏ hàng, Trang thông tin (Giới thiệu & Chính sách)**.
- Người bán có thể **xem trước và áp dụng template** vào bản nháp, chỉnh sửa bằng **trình sửa trực quan** (thêm/xóa/sắp xếp section và block, sửa settings, token màu/chữ có kiểm tra WCAG) và **chế độ sửa mã template** (kiểm tra schema, báo lỗi theo dòng, xem trước trực tiếp).
- Xuất bản và khôi phục giữ nguyên cơ chế `site_versions` bất biến hiện có; **4 site seed cũ (`bach-dang`, `sai-gon`, `da-nang`, `ha-noi`) chuyển đổi được không mất dữ liệu**.
- Tuân thủ nguyên tắc Ponytail: ưu tiên thư viện chuẩn Go/TypeScript, không thêm runtime dependency mới nếu không cần thiết.

### 5 tiêu chí đánh giá bắt buộc
1. **An toàn:** Người bán không chạy được HTML/JS/CSS tùy ý; không XSS (`<script>`, `javascript:`, `on*=`, style injection, SVG); không SSRF qua URL ảnh (chặn IP nội bộ, loopback, metadata cloud, scheme không an toàn); giới hạn kích thước và độ sâu.
2. **Dễ cho người bán:** Đồng bộ hai chiều mượt giữa trình sửa trực quan và chế độ sửa mã; báo lỗi chính xác theo dòng/cột; xem trước trực tiếp tức thì.
3. **Tốc độ render phía server (Next.js RSC):** Render nhanh, xác định, không biên dịch/eval runtime.
4. **Kiểm tra được bằng schema:** Bộ validator nghiêm ngặt đồng nhất giữa Go (`services/user`) và TypeScript (`web`).
5. **Phiên bản hóa & chuyển đổi dữ liệu cũ:** Lưu trữ gọn trong `JSONB` (`sites.draft_config`, `site_versions.config`) và nâng cấp sạch 4 site seed v1.

---

## 2. Ba phương án được đề xuất và tranh luận

| Tiêu chí | Phương án A: Declarative Theme JSON v2 (`prop-json`) | Phương án B: Sandboxed Liquid-Subset DSL (`prop-liquid`) | Phương án C: Component-Restricted MDX (`prop-mdx`) |
|---|---|---|---|
| **Mô tả** | JSON v2 khai báo `schema_version: 2`, `template_id`, `theme`, `layout`, `sections` (có `variant`, `settings`, `blocks[]`) và `pages.{catalog,pdp,cart,info}`. | Tập con DSL kiểu Liquid tự viết (`{% theme %}`, `{% layout %}`, `{% page %}`, `{% section %}`, `{% block %}`, `{{ shop.name }}`), biên dịch sang AST trước khi lưu. | Frontmatter JSON + thẻ PascalCase allowlist với prop hằng số (`<Hero>`, `<Block>`) + Markdown nội tuyến tối giản, cấm `{expr}` và `import/export`. |
| **An toàn** | **5/5** — Thuần dữ liệu (data-only), không parser ngôn ngữ/HTML/CSS. Dùng `encoding/json` (`DisallowUnknownFields`) + bộ quét token pre-decode (kích thước, độ sâu, cấm duplicate key) + bộ lọc XSS/SVG/SSRF. | **4.5/5** — Không cho HTML/JS/CSS, nhưng phải tự viết lexer/parser tùy chỉnh làm rộng bề mặt lỗi biên phân tích cú pháp. | **3.5/5** — MDX chuẩn biên dịch thành JS nên không dùng được; phải tự viết parser JSX+Markdown mới, bề mặt thoát chuỗi và lỗi cú pháp rộng nhất. |
| **Dễ cho người bán (Visual ↔ Code)** | **4.5/5** — Song ánh 1–1 không mất thông tin giữa trình sửa trực quan và mã JSON; bộ map dòng/cột gọn (~60 LOC) báo lỗi chính xác theo dòng; xem trước tức thì. Hạn chế nhỏ: dấu ngoặc JSON kém gọn hơn văn bản thuần. | **3.5/5** — Gõ tay gọn hơn JSON, có lỗi dòng/cột tự nhiên, nhưng đồng bộ ngược từ trình trực quan làm mất comment/khoảng trắng. | **3.5/5** — Thuận tiện cho đoạn văn dài nhiều định dạng, nhưng người bán không chuyên dễ dính lỗi đóng/mở thẻ và đồng bộ trực quan làm mất định dạng gốc. |
| **Tốc độ SSR (Next.js RSC)** | **5/5** — `JSON.parse` native (~0.1 ms) truyền thẳng vào React Server Components thuần; 0 chi phí biên dịch/thông dịch. | **4.5/5** — Nhanh nếu lưu kèm AST trong JSONB, thêm bước thay thế biến `{{ ... }}`. | **4.5/5** — Nhanh nếu lưu sẵn AST đã biên dịch trong JSONB, tốn thêm bước xử lý node Markdown nội tuyến. |
| **Kiểm tra schema (Go + TS, 0 dep mới)** | **5/5** — Dùng `encoding/json` chuẩn của Go và `JSON.parse` của TS, dễ giữ đồng bộ bằng bộ test fixture dùng chung. | **3/5** — Cần duy trì song song 2 bộ lexer/parser tự viết (~450 LOC mỗi ngôn ngữ) ở Go và TS. | **2.5/5** — Cần duy trì song song 2 bộ parser JSX-subset + Markdown (~600–900 LOC mỗi ngôn ngữ) ở Go và TS. |
| **Phiên bản hóa & 4 site seed cũ** | **5/5** — Lưu thẳng vào `JSONB` hiện tại (`sites`/`site_versions`); nâng cấp v1→v2 xác định, giữ nguyên `theme` và `sections` đầu trang nên tương thích cả `ListPublishedSites` và `seo.ts`. | **4/5** — Phải bọc `{source, ast}` trong JSONB; bộ chuyển đổi v1→DSL phức tạp hơn. | **4/5** — Phải bọc `{source, ast}` trong JSONB và escape ký tự Markdown/thẻ khi chuyển từ v1. |

---

## 3. Kết quả biểu quyết

| Worker | Hạng 1 | Hạng 2 | Hạng 3 | Lý do chính |
|---|---|---|---|---|
| `prop-json` | **A** | B | C | Dùng JSON chuẩn không thêm dependency, bề mặt bảo mật nhỏ nhất, đồng bộ trực quan ↔ mã 1–1 không mất dữ liệu, render RSC tức thì và chuyển đổi v1 không mất mát. |
| `prop-liquid` | **A** | B | C | Tự duy trì 2 parser Go/TS cho Liquid-subset hay MDX đều tăng rủi ro lỗi phân tích cú pháp và làm lệch vòng đồng bộ trình sửa trực quan; A thắng rõ ở mọi tiêu chí. |
| `prop-mdx` | **A** | B | C | MDX không biểu thức thực chất là cú pháp XML bọc lên AST của A nhưng trả giá bằng 2 parser phức tạp và bề mặt thoát chuỗi lớn nhất; A tối ưu nhất theo Ponytail. |

**Tổng kết:** **Phương án A (`Declarative Theme JSON v2`) thắng tuyệt đối 3–0.**

---

## 4. Quyết định kiến trúc và Đặc tả Schema v2

### 4.1 Cấu trúc JSON chuẩn (`schema_version: 2`)
Cấu hình v2 giữ lại 2 khóa cấp gốc `theme` và `sections` (của trang chủ) để tương thích ngược với truy vấn SQL (`v.config->'theme'->>...`) và logic SEO hiện có, đồng thời mở rộng thêm:
- `schema_version`: `2` (nếu vắng mặt khi gọi API cũ -> kiểm tra theo v1 và hỗ trợ chuyển đổi xác định `ConvertV1ToV2`).
- `template_id`: mã định danh template nền tảng (ví dụ `minimal-fashion`, `editorial`, `dense-grid`, `craft-atelier`, `sport-active`, `luxury-maison`, `one-page-launch`, `multi-sku-catalog`, hoặc `legacy-v1`).
- `theme`: 5 màu `#RRGGBB` (bắt buộc WCAG 2.1 ≥ 4.5:1 cho `bg/text`, `surface/text`, `accent/accent_text`), `font_family` thuộc allowlist 5 phông chữ, `logo_url`, `banner_url`, `brand_name`, `tagline`, cùng token hình học tùy chọn `radius` (`none|sm|md|lg`) và `density` (`compact|balanced|airy`).
- `layout`: cấu hình khung giao diện toàn site (`archetype`, `header_style`, `footer_style`, `container_width`, `card_style`, `show_announcement`, `announcement_text`, và `slots: { search, chat, assistant }` để bật/tắt thành phần từ Swarm G).
- `sections`: danh sách section trang chủ (tối đa 20). Mỗi section gồm `id`, `type` (7 loại gốc `hero`, `featured_products`, `collection`, `text`, `banner`, `voucher_strip`, `policies` + 4 loại mới `lookbook`, `metrics_strip`, `sku_matrix`, `testimonials`), `visible`, `variant` (enum theo loại section), `settings` (`surface`, `padding`, `columns`, `align`, `max_items`), `props` (định kiểu chặt theo loại), và `blocks[]` (tối đa 12 block định kiểu chặt: `heading`, `paragraph`, `badge`, `cta`, `image`, `stat`, `policy`, `quote`, `feature`).
- `pages`: cấu hình bố cục + section bổ sung (tối đa 8 section/trang) cho 4 trang còn lại:
  - `catalog`: `layout`, `columns`, `show_filters`, `show_search`, `show_sku_matrix`, `intro_title`, `intro_subtitle`, `sections`
  - `pdp`: `layout`, `gallery_style`, `show_vouchers`, `show_sku_table`, `show_story_band`, `show_related`, `sections`
  - `cart`: `layout`, `show_vouchers`, `show_addons`, `trust_note`, `sections`
  - `info`: `layout`, `show_factory_profile`, `show_toc`, `show_showcase`, `sections`

### 4.2 Các ràng buộc bảo mật bắt buộc (từ vòng biểu quyết)
1. **Kiểm tra cấu trúc JSON trước giải mã (pre-decode token scan):**
   - Giới hạn kích thước payload `<= 128 KiB` (`MaxConfigBytes = 128 * 1024`).
   - Giới hạn độ sâu lồng nhau `<= 8` (`MaxJSONDepth = 8`).
   - **Từ chối khóa trùng lặp (duplicate keys)** trong cùng một object JSON để chặn JSON parser smuggling giữa Go và JS.
   - `DisallowUnknownFields()` và từ chối trailing tokens ở mọi cấp.
2. **Chặn XSS, Style Injection và SVG tại tầng lưu lẫn tầng hiển thị:**
   - Mọi trường văn bản từ chối chuỗi chứa `<script`, `</script`, `<style`, `<svg`, `</svg`, `<iframe`, `<object`, `<embed`, `<link`, `<meta`, thuộc tính sự kiện `on<event>=`, `javascript:`, `vbscript:`, `data:`, `expression(`, `url(`, `@import`, `-moz-binding`.
   - Không có trường HTML/CSS thô nào trong schema; mọi màu sắc qua regex `^#[0-9A-Fa-f]{6}$` và các biến CSS có phạm vi `--site-*`.
   - Frontend tuyệt đối không dùng `dangerouslySetInnerHTML`, không chèn `<style>` từ dữ liệu người bán.
3. **Chặn SSRF và SVG trên URL ảnh (`ValidateImageURL`) & liên kết (`ValidateSafeURL`):**
   - Chỉ chấp nhận chuỗi rỗng (nếu tùy chọn), đường dẫn tương đối bắt đầu bằng đúng một dấu `/` (không `//`, không `\`, không khoảng trắng/ký tự điều khiển), hoặc URL `https://` hợp lệ không có `userinfo`.
   - `ValidateImageURL` từ chối thêm:
     - Ảnh SVG: đuôi `.svg`, `.svgz` (không phân biệt hoa/thường, kiểm tra trên path đã giải mã trước `?`/`#`) và `image/svg` / `svg+xml` trong path/query.
     - Mục tiêu SSRF cho host ngoài: mọi địa chỉ IP trực tiếp (IPv4, IPv6, IPv4-mapped IPv6 `::ffff:...`, dạng số nguyên/hex/octal như `2130706433` hoặc `0x7f000001`), loopback/private/link-local/metadata (`localhost`, `*.localhost`, `127.0.0.0/8`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `169.254.0.0/16`, `metadata.google.internal`, `*.local`, `*.internal`, `*.localdomain`, `*.home.arpa`, và hostname không chứa dấu chấm).
4. **Kiểm tra bộ chuyển đổi v1 → v2:**
   - Bộ test đơn vị Go và TS bắt buộc chạy kiểm chứng chuyển đổi đầy đủ cả 4 cấu hình seed v1 (`bach-dang`, `sai-gon`, `da-nang`, `ha-noi`) sang v2 hợp lệ, giữ nguyên toàn bộ bảng màu, phông chữ, thương hiệu và các section gốc.

---

## 5. Hệ quả và Đánh đổi

- **Ưu điểm:** Không thêm thư viện bên thứ ba ở Go hay Next.js; tốc độ SSR gần như bằng không; đồng bộ trực quan ↔ mã JSON không mất dữ liệu; bề mặt tấn công XSS/SSRF/DoS hẹp nhất trong 3 phương án; tương thích hoàn toàn với `site_versions` bất biến.
- **Đánh đổi chấp nhận được:** Người bán không thể viết vòng lặp hay biểu thức điều kiện tùy ý (`{% if %}`), và văn bản giàu định dạng phải dùng khối có cấu trúc thay vì Markdown tự do. Sự đa dạng bố cục của 8+ template đạt được qua hệ thống `archetype` + `layout` + `section.variant` + `blocks[]` được kỹ sư thiết kế sẵn và kiểm soát WCAG/responsive.
- **Lộ trình nâng cấp (nếu cần sau này):** Nếu cần soạn thảo văn bản giàu định dạng hơn trong tương lai, chỉ bổ sung kiểu block `rich_paragraph` chứa mảng span `{text, bold, italic}` thuần JSON — vẫn không cần parser HTML/MDX.
