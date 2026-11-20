# Mô hình Sản phẩm: Gian hàng thành Site riêng kiểu Shopify

> `đã kiểm chứng` = tài liệu chính thức kèm URL; `suy luận` = tổng hợp cho NexusMart (9 service Go/Rust, sổ cái VND, Next.js 44 route).

## 1. Giải phẫu Shopify (Online Store 2.0 & lớp Shop)

Shopify tách **lớp hiển thị dữ liệu** khỏi **lõi giao dịch chuẩn hóa** *(suy luận)*.

- **Theme, Section, Block & JSON Template**: OS 2.0 dùng `templates/*.json` (`sections` + mảng `order`) cho phép sửa bố cục mọi trang không cần code; Header/Footer dùng **Section groups** *(đã kiểm chứng: [OS 2.0](https://shopify.dev/docs/storefronts/themes/os20), [JSON Templates](https://shopify.dev/docs/storefronts/themes/architecture/templates/json-templates), [Section Groups](https://shopify.dev/docs/storefronts/themes/architecture/section-groups))*. Trần: **1.000** JSON template, **25** section/template, **50** block/section (**1.250**/template), lồng **8** cấp *(đã kiểm chứng: [Limits](https://shopify.dev/docs/storefronts/themes/architecture/limits))*.
- **Editor, Nháp/Live, Trang & Menu**: Tối đa **20 theme** (Basic–Advanced) hoặc **100** (Plus), chỉ **1 Live**; publish bản mới đẩy bản cũ về *Draft* để rollback tức thì *(đã kiểm chứng: [Adding](https://help.shopify.com/en/manual/online-store/themes/adding-themes), [Publishing](https://help.shopify.com/en/manual/online-store/themes/managing-themes/publishing-themes))*. Main menu hỗ trợ **3 cấp** (2 dropdown), Footer **1 cấp**, tối đa 1.000 menu *(đã kiểm chứng: [Menus](https://help.shopify.com/en/manual/online-store/menus-and-links/drop-down-menus))*.
- **Domain & TLS**: Cấp sẵn `*.myshopify.com` (đổi **1 lần**), gắn thêm **20** domain (**1.000** trên Plus) qua A/CNAME; **TLS tự động miễn phí** (CAA `letsencrypt.org`/`pki.goog`/`ssl.com`; không nhận SSL ngoài) *(đã kiểm chứng: [Domains](https://help.shopify.com/en/manual/domains), [Add Domain](https://help.shopify.com/en/manual/domains/add-a-domain), [TLS](https://help.shopify.com/en/manual/domains/managing-domains/secure-connections))*.
- **Checkout & Customer Accounts**: Mặc định **1 trang** (tùy chọn 3 trang), tách sang *Checkout & accounts editor*; `checkout.liquid` đã khai tử (Thank you/Order status đóng 28/8/2025; script tags non-Plus đóng 26/8/2026) *(đã kiểm chứng: [One-page](https://help.shopify.com/en/manual/checkout-settings/customize-checkout-configurations/one-page-checkout), [checkout.liquid](https://shopify.dev/docs/storefronts/themes/architecture/layouts/checkout-liquid))*. Gói Basic–Advanced chỉ đổi logo/màu/font và app sau mua; chỉ **Plus** mở UI Extensions trong checkout và Branding API *(đã kiểm chứng: [Checkout](https://help.shopify.com/en/manual/checkout-settings/customize-checkout-configurations))*. Customer Accounts mới dùng **OTP 6 số** không mật khẩu (phiên **365 ngày**), *Sign in with Shop*/Google/Facebook và 1 subdomain `account.<domain>` *(đã kiểm chứng: [Accounts](https://help.shopify.com/en/manual/customers/customer-accounts), [Sign-in](https://help.shopify.com/en/manual/customers/customer-accounts/upgrade/customization-options))*.
- **Shop app / Shop Pay**: Lớp khám phá chung (`shop.com` + app, Shop Cash) và ví định danh/checkout 1 chạm xuyên cửa hàng; SP Online Store tự vào Shop Catalog (redirect hoặc mua thẳng nếu bật Shop channel + có custom domain) *(đã kiểm chứng: [Shop](https://help.shopify.com/en/manual/online-sales-channels/shop), [Setup](https://help.shopify.com/en/manual/online-sales-channels/shop/setup), [Shop Pay](https://help.shopify.com/en/manual/payments/shop-pay))*.
- **Gói, Hạn mức & App Extension**: Basic `$29`, Grow `$79`, Advanced `$299`/th (năm), Plus từ `$2.300`; không giới hạn SP/băng thông, phân tầng theo nhân viên (`0/5/15/∞`), kho (`10/200`), phí cổng ngoài (`2%–0,2%`), API và checkout *(đã kiểm chứng: [Pricing](https://www.shopify.com/pricing))*. **Theme App Extensions** (`@app` block, trần 10MB/30 block, cấm trên checkout) gỡ sạch code khi uninstall *(đã kiểm chứng: [App Extensions](https://shopify.dev/docs/apps/build/online-store/theme-app-extensions/configuration))*.

## 2. So sánh đa nền tảng & mô hình Sàn + Site riêng

| Tiêu chí | Shopify (OS 2.0 + Shop) | Haravan / Sapo (VN) | Wix / Squarespace | Sàn thuần (Shopee Mall, LazMall, TikTok) | Sàn + Site riêng (Etsy Pattern / Amazon BwP) |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Giao diện** | JSON section/block + Liquid | 400+ theme; visual **+ HTML/CSS** | Wix: kéo-thả/Grid (16 cell); Sqsp: Fluid Engine | Module trang chủ; TikTok có AI auto-gen | Pattern: template gọn; BwP: nhúng site ngoài |
| **Nháp/Live** | 20–100 theme (1 Live) | Sapo: tối đa 8 theme | Nháp + chỉnh riêng Mobile | Nhiều nháp (Shopee tới 20, hẹn lịch) | Preview/Publish độc lập trang sàn |
| **Tên miền** | `*.myshopify.com` + custom, TLS | `*.mysapo.net` + custom, SSL | Custom + SSL miễn phí | **Không có**; cấm link ngoài sàn | Pattern: custom A/CNAME; Amazon Store không |
| **Checkout** | 1 trang, khóa code, Shop Pay | 15+ cổng VN, COD, GHN/GHTK | Khóa lõi (chỉ màu/logo/field) | 100% lõi sàn, escrow | Pattern: Etsy Payments; BwP: 3% + fulfillment |
| **Sàn chung** | Shop app (xây sau) | **Không** (chỉ sync sàn ngoài) | **Không** | Traffic lớn, mất sở hữu domain | Có cả sàn lẫn site đồng bộ tồn/đánh giá |
| **Giá** | `$29–$299`/th + `0,6–2%` | Haravan `680k`; Sapo `499k–600k` | Wix `$29`; Sqsp `$19`(+2%)–`$39` | Hoa hồng theo đơn | Pattern `$15`/th + **0% phí sàn** |

*Nguồn đã kiểm chứng*: [Haravan](https://www.haravan.com/pages/online-solutions), [Giá Haravan](https://www.haravan.com/pages/pricing/), [Giá Sapo](https://www.sapo.vn/bang-gia.html), [Sapo Theme](https://help.sapo.vn/cai-dat-giao-dien-cho-website-sapo), [Sapo Editor](https://help.sapo.vn/tuy-chinh-giao-dien-website-tren-sapo), [Sapo Domain](https://help.sapo.vn/them-ten-mien-vao-website-sapo-omniai), [Wix](https://www.wix.com/plans), [Wix Grid](https://support.wix.com/en/article/studio-editor-customizing-a-section-grid), [Wix Checkout](https://support.wix.com/en/article/customizing-the-checkout-page), [Squarespace](https://support.squarespace.com/hc/en-us/articles/206536797-Choose-the-right-Squarespace-plan), [Fluid Engine](https://support.squarespace.com/hc/en-us/articles/46306741295757-Design-mobile-page-layouts-in-Fluid-Engine), [Sqsp Checkout](https://support.squarespace.com/hc/en-us/articles/205815328-Styling-your-checkout-page), [Shopee](https://seller.shopee.sg/edu/article/7013), [Shopee VN](https://banhang.shopee.vn/edu/article/15272), [Shopee Premium](https://seller.shopee.sg/edu/article/6909/use-premium-shop-decoration), [LazMall](https://sellercenter.lazada.vn/apps/register/lazmall/channel), [TikTok](https://seller-us.tiktok.com/university/essay?knowledge_id=3029412015458091&lang=en), [Pattern](https://help.etsy.com/hc/en-us/articles/360000344088-What-is-Pattern), [Pattern Fees](https://help.etsy.com/hc/en-us/articles/360000337067-Pricing-and-Fees-for-Pattern), [Buy with Prime](https://buywithprime.amazon.com/pricing).

**Rút ra** *(suy luận)*: Haravan/Sapo mạnh cổng VN nhưng mở HTML/CSS dễ vỡ mobile và thiếu traffic nội sinh; Wix/Squarespace kéo-thả tự do tốn công sửa mobile gấp đôi; **Etsy + Pattern** kết hợp **Shop Pay** là mô hình sát nhất cho NexusMart — một nguồn tồn kho/đánh giá nuôi song song sàn và site riêng.

## 3. Phản biện 6 đề xuất sơ bộ của Manager *(suy luận)*

1. **Next.js đa tenant (`Host` -> `(site)/[shop]/...`)**: **Đúng hướng**, nhưng phải cô lập ngữ cảnh trên site riêng: ẩn tìm kiếm toàn sàn và khối *"Tương tự trên sàn"*, tách `sitemap.xml`/canonical theo domain.
2. **Service Go `storefront`**: **Hợp DB-per-service**, nhưng `user.proto` đang thiếu `Shop.slug` bất biến (hoặc đổi 1 lần). Phải nghe NATS để hạ site riêng ngay khi `ShopStatus=SUSPENDED`.
3. **Theme dữ liệu JSON, cấm code**: **Rất đúng** (khớp Shopify/Squarespace). Cần thêm **3–5 preset đồ basic** và kiểm tra tương phản màu tự động.
4. **Checkout dùng lõi `order`/`payment`, tiền qua NexusMart**: **Đúng về tin cậy** (sổ cái VND, escrow, COD/VietQR), **phản biện về phí**: thu cùng hoa hồng sàn cho đơn tự kéo về tên miền riêng sẽ đẩy xưởng sang Haravan/Sapo. Cần gắn `attribution_channel` (`marketplace`/`storefront`) để giảm phí cho đơn site riêng (kiểu Pattern 0% phí sàn).
5. **1 tài khoản người mua + song song sàn/site**: **Lợi thế lớn** (kiểu Shop Pay, tái dùng địa chỉ 34 tỉnh, `bodyProfile`, `cod_rejected_count`). **Phản biện**: giữ **Guest checkout**, tách giỏ hiển thị theo shop, và dùng SSO tập trung (`account.nexusmart.vn`) thay vì chia sẻ cookie chéo domain.
6. **Custom domain + DNS + Caddy TLS**: **Hợp lý**; cấp sẵn `<slug>.nexusmart.vn` ngay khi duyệt shop, chỉ mở custom domain khi `ACTIVE` kèm xác minh `TXT` chống chiếm domain và kiểm tra `CAA`.

## 4. Bộ tính năng MVP vs Đầy đủ *(suy luận)*

| Nhóm | Bộ Tối thiểu (MVP) | Bộ Đầy đủ (Full) |
| :--- | :--- | :--- |
| **Tên miền** | Subdomain `<slug>.nexusmart.vn` tự động. | Custom domain (≤5/shop), TXT/CNAME, Caddy TLS, 301. |
| **Theme/Editor** | 1 preset *Frost & Slate*, logo + 1 màu nhấn, 1 nháp/1 live, preview desktop/mobile, rollback 1 bước. | 3–5 preset basic, 8–10 nháp theo mùa, hẹn lịch xuất bản, undo/redo, link preview. |
| **Trang/Menu** | JSON trang chủ (**≤12 section**); Header/Footer cố định; Menu 2 cấp; 3 trang tĩnh (Giới thiệu, Đổi trả, Bảng size). | JSON cho BST/PDP/Lookbook; Mega-menu 3 cấp; không giới hạn trang; lọc GSM/chất vải. |
| **Catalog** | Đồng bộ 100% SP, SKU, bảng size (`/seller/ho-so`), đánh giá. | Chọn kênh từng SKU (`Sàn`/`Site`/`Cả hai`), combo độc quyền site riêng. |
| **Checkout/SSO** | Lõi `/thanh-toan` (COD, VietQR, Guest) mang logo/màu shop; SSO `account.nexusmart.vn`; giỏ tách theo shop. | Tùy biến brand checkout sâu; `account.<domain>`; gắn GA4/Meta/TikTok Pixel bằng ID. |
| **Gói & Phí** | Miễn phí subdomain cho shop `ACTIVE`. | Gói Thương hiệu (custom domain + ưu đãi phí giao dịch site riêng). |

## 5. Thư viện Section cho thời trang basic *(suy luận)*

Kế thừa `web/DESIGN_REPORT.md` (trần 12 section MVP / 20 Full, ≤12 block/section; `Header`/`Footer` là Section group cố định):

| Mã section | Chức năng cốt lõi | Giai đoạn |
| :--- | :--- | :---: |
| `announcement_bar` | Freeship xưởng, đổi trả 45 ngày, mã ưu đãi (1–3 dòng). | MVP |
| `hero_editorial` | Banner 16:9 / 3:4 ảnh người mặc + tiêu đề gọn + ≤2 CTA. | MVP |
| `category_grid` | Lưới 4–8 danh mục basic (Áo thun, Polo, Short, Jogger, Đồ lót, Tất). | MVP |
| `featured_products` | Lưới 4/8/12 SP động, swatch màu, hover người mặc, quick-add size. | MVP |
| `size_finder_band` | Nhúng `SizeFinder` theo bảng rập xưởng + 3 dáng người. | MVP |
| `workshop_trust_bar` | Hồ sơ xưởng Việt, QC & 4 cam kết (45 ngày, Freeship, COD đồng kiểm). | MVP |
| `voucher_flash_strip` | Thu thập voucher shop & Flash sale riêng của xưởng. | MVP |
| `fabric_gsm_showcase` | Cận cảnh sợi (Cotton Compact 220gsm, Pique, Modal, Bamboo) + video dệt. | Full |
| `combo_tiered_deals` | Combo đa size & mua nhiều giảm tầng tăng AOV đồ basic. | Full |
| `lookbook_collections` | Banner BST 2×2 (Hằng ngày, Công sở, Chạy bộ) gắn thẻ mua nhanh. | Full |
| `review_fit_gallery` | Đánh giá đồng bộ từ sàn kèm thanh độ vừa (*Chật / Đúng / Rộng*). | Full |
| `faq_care_guide` | FAQ 2 cột & quy chuẩn giặt/bảo quản theo chất liệu. | Full |

## 6. Onboarding người bán & Những gì KHÔNG nên làm *(suy luận)*

**Onboarding 5 bước (<10 phút)**:
1. **Tự khởi tạo**: Duyệt `ShopStatus=ACTIVE` → cấp `<slug>.nexusmart.vn` và dựng sẵn bản nháp từ dữ liệu sẵn có (logo, SP đã duyệt, bảng size — học TikTok Auto-generate & Pattern).
2. **Nhận diện**: Chọn preset, logo, 1 màu nhấn (chặn lỗi tương phản), xác nhận hotline/chính sách.
3. **Bố cục**: Bật/tắt, kéo xếp 5–7 section, xem trước song song `1440px`/`390px`.
4. **Xuất bản**: Duyệt link preview → bấm **Xuất bản**, tự gắn link từ `/gian-hang/[slug]`.
5. **Tên miền riêng (tùy chọn)**: Trỏ `TXT` + `CNAME`/`A` → xác minh DNS → Caddy cấp TLS → redirect 301.

**Những gì KHÔNG nên làm**:
- **Không mở HTML/CSS/JS/Liquid**: Vỡ mobile, rủi ro XSS lên phiên đăng nhập chung, tốn chi phí hỗ trợ.
- **Không kéo-thả tự do kiểu Wix Editor**: Phải sửa lại mobile thủ công, đội chi phí dựng editor.
- **Không tùy biến logic Checkout**: Bảo vệ chuyển đổi, Saga tồn kho, chặn bom COD và sổ cái ghi kép.
- **Không mở App Store bên thứ 3 sớm**: Làm tính năng gốc cho đồ basic thay vì gánh hạ tầng plugin.
- **Không lộ sản phẩm đối thủ trên site riêng**: Tắt mọi gợi ý liên gian hàng trên domain của shop.
- **Không đánh đồng hoa hồng sàn với đơn tự kéo về site riêng**: Sẽ mất xưởng về Haravan/Sapo.

## 7. Khuyến nghị, Rủi ro & Câu hỏi còn mở

**Khuyến nghị cụ thể**:
1. Định vị theo **Etsy Pattern + Shopify OS 2.0 thu gọn + Shop Pay nội địa**: một kho SP/tồn/đánh giá nuôi hai kênh, ưu đãi phí cho đơn site riêng.
2. Thêm `slug` duy nhất vào `Shop` (`user` service); `storefront_db` lưu `site_configs` (`draft_json`, `published_json`, `version`), `pages`, `menus`, `custom_domains` và nghe NATS `shop.suspended`.
3. Tái sử dụng `tokens.css`, `ProductCard`, `SizeFinder` và `/thanh-toan` của `web/` để ra nhanh 7 section MVP.

**Rủi ro chính**:
- *Leakage*: Xưởng kéo khách từ sàn ra site riêng để né hoa hồng.
- *Cross-domain SSO*: Chuyển hướng đăng nhập giữa `nexusmart.vn` và domain riêng chậm sẽ làm rớt đơn mobile.
- *Trùng lặp SEO*: Cùng 1 SP trên sàn và site riêng tự cạnh tranh nếu thiếu `canonical`.
- *Pháp lý domain*: Shop bị khóa (`SUSPENDED`) cần ngắt routing/TLS tức thì trên tên miền riêng.

**Câu hỏi còn mở**:
1. Đơn site riêng (`attribution_channel=storefront`) thu phí tháng + phí payment (kiểu Pattern) hay giảm % hoa hồng?
2. Khách trên site riêng có được dùng **Xu NexusMart** và **Voucher toàn sàn** hay chỉ voucher của xưởng?
3. `rel="canonical"` ưu tiên URL sàn chung hay tên miền riêng của xưởng?
4. Có gỡ ràng buộc `1 User -> 1 shop_id` (`user.proto`) để một xưởng mở nhiều thương hiệu con không?
