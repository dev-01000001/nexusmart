# Độ khớp Codebase, Pháp lý Việt Nam và Lộ trình Site Riêng

> Đối chiếu đề xuất site riêng kiểu Shopify với codebase NexusMart (`proto/`, `services/*`, `web/src/`, `docker-compose.yml`, `docs/spec.md`, `docs/CORE_REPORT.md`). Quy ước: **`đã kiểm chứng`** = đọc repo/nguồn chính thức; **`suy luận`** = đánh giá kỹ thuật/vận hành.

## 1. Kiểm chứng và phản biện đề xuất sơ bộ

1. **Next.js đa tenant + `middleware` theo `Host`**: Đúng [Next.js Multi-Tenant](https://nextjs.org/docs/app/guides/multi-tenant) (`đã kiểm chứng`), nhưng `web/` 100% mock (`0` `fetch()`), thiếu `middleware.ts`, `Dockerfile`, chưa vào `docker-compose.yml`/CI (`đã kiểm chứng`); phải nối `web/` → `gateway` trước (`suy luận`).
2. **Service Go `storefront`**: Đúng DB-per-service (`AGENTS.md`), nhưng thành **12 service**, vượt 11 service/5 người/6 tháng (`docs/spec.md`) khi `search`, `chat`, `ai-assistant`, `aiops` chưa dựng (`đã kiểm chứng`), làm ngay sẽ trượt Tháng 4–6 (`suy luận`).
3. **Theme JSON có schema, cấm code**: Khớp `tokens.css`, giảm XSS và rủi ro kiểm duyệt theo Luật `122/2025/QH15` (`đã kiểm chứng`, `suy luận`).
4. **Checkout dùng `order`/`payment`, tiền qua NexusMart**: Tái dùng tốt saga, sổ cái VND `8/8 E2E` (`docs/CORE_REPORT.md` — `đã kiểm chứng`), nhưng **vướng pháp lý thương mại**: thu hộ-chi hộ cần giấy phép TGTT (NĐ `52/2024/NĐ-CP`) và phát sinh khấu trừ thuế thay (NĐ `117/2025/NĐ-CP`) (`đã kiểm chứng`).
5. **Tài khoản dùng chung**: Cookie `HttpOnly; SameSite=Lax` ([Next.js Auth](https://nextjs.org/docs/app/guides/authentication) — `đã kiểm chứng`) bị khóa theo domain nên cần SSO redirect (`suy luận`); `cart` dùng `cart:<user_id>` không lọc `shop_id` (`đã kiểm chứng`), dễ lẫn hàng shop khác.
6. **Custom domain + Caddy on-demand TLS**: Đúng [Caddy Automatic HTTPS](https://caddyserver.com/docs/automatic-https) và [Caddyfile Options](https://caddyserver.com/docs/caddyfile/options) (`đã kiểm chứng`), bắt buộc có `ask` trả vài ms chống cạn hạn mức CA (`đã kiểm chứng`).

## 2. Bản đồ thay đổi Backend (`services/*`) và `proto/`

8 tệp `.proto` và 7 DB (`infra/postgres/init-dbs.sql`) đã tách theo `shop_id`, nhưng giả định một kênh sàn và tra cứu UUID (`đã kiểm chứng`).

| Service | Hiện trạng (`đã kiểm chứng`) | Sửa đổi cho Site riêng (`suy luận`) | Bổ sung `proto/` |
| --- | --- | --- | --- |
| **`gateway`** | `chi`, JWT+JTI, rate limit, proxy webhook/SSE; thiếu CORS động, tenant header. | Middleware `Host` (cache Redis) gắn `x-sales-channel`, `x-storefront-shop-id`; CORS động; `/api/v1/storefront/*`, `/internal/tls/allow`. | Gọi gRPC `StorefrontService`. |
| **`user`** | `shops` thiếu `slug`, MST, loại chủ thể; `GetShop` chỉ nhận UUID. | Thêm `slug` (UNIQUE), `tax_code`, `seller_type`, `legal_address`, `bct_notice_url`; SSO auth-code ngắn hạn. | `user.proto`: thêm trường trên vào `Shop`, `slug` ở `GetShopRequest`; `IssueSSOCode`/`ExchangeSSOCode`. |
| **`product`** | `products` thiếu `slug`, kênh bán, bộ sưu tập shop. | Thêm `slug` (UNIQUE theo `shop_id`), `sales_channels` (`MARKETPLACE`/`STOREFRONT`); tra `(shop_id, slug)`, lọc kênh. | `product.proto`: `slug`, `sales_channels` ở `Product`/Create/Update; `shop_id`+`slug` ở `GetProductRequest`; `sales_channel` ở `ListProductsRequest`. |
| **`cart`** | Redis `cart:<user_id>` gom mọi shop. | Cô lập/lọc theo `shop_id` (`cart:<user_id>:shop:<shop_id>`); chặn SKU shop khác. | `cart.proto`: `optional string shop_id` ở mọi RPC. |
| **`promotion`** | `vouchers` chưa giới hạn kênh bán. | Thêm `allowed_channels` chặn voucher sàn áp sang site riêng; lọc flash sale theo `shop_id`. | `promotion.proto`: `allowed_channels` ở `Voucher`/Create; `sales_channel` ở List/Preview/Reserve; `shop_id` ở `ListActiveFlashSalesRequest`. |
| **`order`** | `orders` thiếu kênh/domain; `CreateCheckout` không khóa 1 shop; phí mặc định `500 bps`. | Lưu `sales_channel`, `storefront_domain`; khi `STOREFRONT` khóa SKU theo `storefront_shop_id`, áp phí kênh riêng, gắn kênh vào NATS outbox. | `order.proto`: enum `SalesChannel`; `sales_channel`, `storefront_shop_id`, `storefront_domain` ở `CreateCheckoutRequest`, `Order`, `ListOrdersRequest`. |
| **`payment`** | Sổ cái VND chuẩn; `payment_shop_allocations` thiếu kênh và thuế khấu trừ. | Ghi `sales_channel` tách đối soát; thương mại hóa thêm tài khoản `tax_withholding_payable` trong `build_escrow_settle_tx`. | `payment.proto`: `sales_channel`, `tax_withheld_vnd` ở `ShopAllocation`; lọc kênh ở `RunReconciliationRequest`. |
| **`shipping`** | Chưa lưu cấu hình hãng/freeship theo shop (`UC-SHIP06`). | Thêm bảng `shop_shipping_configs` (hãng bật/tắt, ngưỡng freeship riêng). | `shipping.proto`: `GetShopShippingConfig`/`UpsertShopShippingConfig`. |
| **`notification`** | Cứng `"NexusMart - ..."`, `no-reply@nexusmart.local`, link `/orders/{id}` lệch `web/`. | Sửa link `/don-hang/{id}`, `/seller`; đọc `storefront_domain`, `shop_name` từ event dựng tiêu đề/link đúng domain shop. | `notification.proto`: `shop_id`, `storefront_domain` ở `Notification`. |
| **`storefront`** *(mới)* | Chưa có (`init-dbs.sql` có 7 DB). | Go (`50059/8089`, `storefront_db`+Redis): theme token JSON, section JSON, `draft`/`published`, DNS TXT, Caddy `ask`. | `storefront.proto`: `ResolveHost`, `GetPublishedSite`, `SaveDraft`, `PublishSite`, `VerifyDomainDNS`, `CheckDomainAllowed`. |

## 3. Tái sử dụng `web/` và lộ trình nối Gateway trước

Cả `44` route (`web/DESIGN_REPORT.md` — `đã kiểm chứng`) chạy mock cục bộ (`web/src/data/*`, `StoreContext.tsx` 1.855 dòng + `localStorage`, `sellerStore.ts` 1.712 dòng, `auditStore.ts` 254 dòng).

| Nhóm `web/src/` | Tái dùng trực tiếp | Tách theo Tenant & Theme Token (`đã kiểm chứng`) |
| --- | --- | --- |
| **Token & Layout** | `globals.css`, cấu trúc `tokens.css`. | Đưa màu cứng ở `Button.module.css` (`#142026`, `#f0d6d6`) và `ShopFooter.module.css` (`#f6bcbc`) về biến CSS; bỏ cứng `Work_Sans` và `metadata` `"NexusMart"` ở `app/layout.tsx`; ghi đè qua `[data-tenant-theme]` (WCAG ≥ 4.5:1). |
| **Header / Footer** | `OverlayPanel.module.css`, toast, mobile drawer. | Tách `ShopHeader` (cứng `NexusMark`, `MEGA_MENU_GROUPS`) và `ShopFooter` (cứng `1900 6845`, `hotro@nexusmart.vn`, cột sàn) thành `SiteHeader`/`SiteFooter` đọc `SiteConfig` + khối pháp lý shop. |
| **Catalog & PDP** | `ProductCard`, `Price`, `Button`, `StateView`, `SizeFinder`, `features/product/Pdp*`. | Thêm `lockedShopId` ở `CatalogExplorer`; bỏ `"Các xưởng dệt khác"` và `"Mở rộng ra toàn sàn"` ở `gian-hang/[slug]`; thay `(shop)/page.tsx` bằng `SectionRenderer` JSON. |
| **Giỏ, Checkout, Đơn** | `AddressForm`, `VietQrPreview`, `RetryPaymentBox`, `OrderLookupView`. | Thêm `singleShopId`: ẩn shop khác, ẩn freeship sàn `249k`, khóa voucher sàn theo kênh. |
| **Giữ trên sàn** | — | `/ve-nexusmart`, `/chien-dich/*`, `/hoi-vien`, `/the-qua-tang`, `(seller)`, `(admin)`; thêm `/seller/giao-dien`. |

**Trình tự nối Gateway trước (`suy luận`):** (1) Đưa `web/` vào `docker-compose.yml` + CI, dựng `/api/bff/*` lưu token `HttpOnly` cookie, tự refresh `401`, chuyển tiếp `Idempotency-Key`/`X-Trace-Id`; (2) Thay `StoreContext`: Auth/34 tỉnh → Catalog (thêm `slug`, dùng `sku_id` UUID) → Cart/Voucher → Checkout/VietQR SSE/Orders; (3) Nối `sellerStore`/`auditStore` vào `/api/v1/seller/*`, `/api/v1/admin/*`; gắn flag tắt UI chưa có backend (`Guest checkout`, `Ví NexusPay`, `Thẻ quà tặng`, `VAT draft`, `Đánh giá/Q&A`, `Hội viên` — `đã kiểm chứng` theo `docs/CORE_REPORT.md`).

## 4. Pháp lý và vận hành tại Việt Nam

Trọng tâm là **Luật TMĐT `122/2025/QH15`** (hiệu lực **01/07/2026**, chuyển tiếp tới 30/06/2027) và các nghị định thuế, thanh toán 2024–2025.

| Trụ cột | Quy định (`đã kiểm chứng`) | Nguồn chính thức | Hệ quả thiết kế (`suy luận`) |
| --- | --- | --- | --- |
| **Đăng ký / Thông báo** | Luật `122/2025/QH15` (Điều 3, 14): **nền tảng trung gian/tích hợp** phải là pháp nhân và **đăng ký**; **kinh doanh trực tiếp có đặt hàng** phải **thông báo** trước khi chạy (kế thừa NĐ `52/2013`, `85/2021`, logo `online.gov.vn`; 01/07/2025–28/02/2027 phân cấp UBND tỉnh theo NĐ `146/2025/NĐ-CP`). | [Công báo 122/2025](https://congbao.chinhphu.vn/van-ban/luat-so-122-2025-qh15-468683.htm), [PDF](https://congbaocdn.chinhphu.vn/180507251028987904/2026/1/27/122signed-17694826033991158478619.pdf), [BCT](https://moit.gov.vn/tin-tuc/thong-bao/huong-dan-doanh-nghiep-hoan-thien-thu-tuc-thong-bao-website-thuong-mai-dien-tu-voi-bo-cong-thuong.html) | Shop trên `nexusmart.vn` theo đăng ký sàn; khi gắn **domain riêng**, shop tự thông báo và công khai chủ sở hữu. `SiteFooter` khóa khối pháp lý + link `online.gov.vn`, chặn publish nếu thiếu. |
| **Hoá đơn & Thuế** | **NĐ `117/2025/NĐ-CP`** (01/07/2025, Điều 2, 4–7): nền tảng **có thanh toán** phải **khấu trừ, khai tháng, nộp thay** cho hộ/cá nhân (hàng hóa: **GTGT 1%**, **TNCN 0,5%** cư trú / **1%** không cư trú), cấp `01/CTKT-TMĐT`. **NĐ `70/2025/NĐ-CP`** & **TT `32/2025/TT-BTC`** (01/06/2025): HĐĐT máy tính tiền (≥ 1 tỷ/năm) hoặc **ủy nhiệm sàn lập HĐĐT** (`01/ĐKTĐ-HĐĐT`). | [NĐ 117/2025](https://congbao.chinhphu.vn/van-ban/nghi-dinh-so-117-2025-nd-cp-45045.htm), [Cục Thuế](https://chinhsachonline.chinhphu.vn/san-thuong-mai-dien-tu-nop-thue-thay-ho-kinh-doanh-co-can-xuat-hoa-don-81525.htm) | Thu hộ cả site riêng buộc lưu `seller_type`, `tax_code`, thêm bút toán khấu trừ và ủy nhiệm HĐĐT (DN tự xuất HĐĐT hàng; sàn xuất HĐĐT phí). Đồ án giữ HĐĐT **"Ngoài phạm vi"** (`docs/spec.md`), chỉ chừa cột dữ liệu. |
| **Nội dung người bán** | **Điều 17 Luật `122/2025/QH15`** & **Điều 39 Luật BVQLNTD `19/2023/QH15`**: xác thực điện tử người bán, **kiểm duyệt SP trước hiển thị**, lưu SP **≥ 1 năm**, hợp đồng **≥ 3 năm**, **liên đới bồi thường** khi vi phạm nghĩa vụ. | [PDF 122/2025](https://congbaocdn.chinhphu.vn/180507251028987904/2026/1/27/122signed-17694826033991158478619.pdf), [Luật 19/2023](https://congbao.chinhphu.vn/van-ban/luat-so-19-2023-qh15-39843.htm), [DMS](https://dms.gov.vn/tin-chi-tiet/-/chi-tiet/bao-ve-quyen-loi-nguoi-tieu-dung-trong-hoat-%C4%91ong-thuong-mai-%C4%91ien-tu-theo-luat-bao-ve-quyen-loi-nguoi-tieu-dung-nam-2023-87436-1.html) | Giữ duyệt SP + lọc từ cấm khi `PublishSite`; lưu snapshot ≥ 3 năm; khóa shop vô hiệu cả domain riêng. |
| **Thanh toán trung gian** | **NĐ `52/2024/NĐ-CP`** (Điều 22, 01/07/2024) & **TT `40/2024/TT-NHNN`**: **thu hộ, chi hộ** cần Giấy phép TGTT NHNN (vốn ≥ **50 tỷ đồng**, HTTT cấp 3, tài khoản đảm bảo tách bạch). | [NĐ 52/2024](https://congbaocdn.chinhphu.vn/CongBaoCP/VanBan/2024/5/41938/50259-1-2024679-68052-2024-nd-cp.pdf), [TT 40/2024](https://congbao.chinhphu.vn/van-ban/thong-tu-so-40-2024-tt-nhnn-42371.htm) | Thương mại hóa phải qua PSP/ngân hàng có phép NHNN hoặc thanh toán trực tiếp về shop; đồ án giữ sandbox/mock. |

## 5. Phân rã giai đoạn, E2E và tác động lộ trình 6 tháng

Cỡ: `S` = 1–3 ngày, `M` = 4–7 ngày, `L` = 2–3 tuần (`suy luận`).

| Giai đoạn | Hạng mục | Cỡ | Phụ thuộc | Chủ trì | Nghiệm thu đo được |
| --- | --- | :---: | --- | --- | --- |
| **GĐ 0: Nối `web/`** | Compose+CI `web/`; BFF cookie; nối luồng lõi; thêm `slug`; sửa `/don-hang/{id}`. | **L** | — | Người 1, 2, 3 | 100% luồng mua/bán chạy DB thật (`0` `localStorage` giỏ/đơn); p95 đọc `< 300ms`, tạo đơn `< 1s`. |
| **GĐ 1: Đa kênh lõi** | `sales_channel`, `storefront_shop_id` ở `proto` & 6 service; cô lập giỏ/checkout 1 shop; tách voucher & đối soát. | **M** | GĐ 0 | Người 2, 3 | `buf lint`/`make lint`/`make test` xanh; checkout `STOREFRONT` lẫn 2 shop trả `400/422`; `is_balanced=true`. |
| **GĐ 2: `storefront` & Theme** | `storefront` (`storefront_db`) hoặc module mỏng; JSON token + 6 section; `middleware.ts` subdomain; `SiteHeader`/`SiteFooter`; `/seller/giao-dien`. | **L** | GĐ 1 | Người 1, 2 | 2 shop render khác màu/logo/section theo `Host`; WCAG ≥ 4.5:1; `draft` không lộ; `0` JS/CSS tùy ý. |
| **GĐ 3: Domain, TLS, SSO** | DNS TXT/CNAME; Caddy `on_demand_tls` + `/internal/tls/allow`; CORS động; SSO chéo miền; email đúng domain. | **M** | GĐ 2 | Người 1, 2, 5 | `ask` `< 10ms` (`200`/`403`); cert staging đạt; thu hồi `jti` đồng bộ 2 miền. |
| **GĐ 4: E2E & tải** | Go E2E + Playwright đa tenant; `k6` `ResolveHost`; suspend khóa site riêng. | **M** | GĐ 2–3 | Người 1, 3, 5 | 100% E2E mới PASS; cache hit `> 95%`; p95 `< 300ms` @ 200 VU; khóa domain `< 60s`. |

**E2E cần thêm:** *Go (`tests/e2e/`)*: `09_storefront_isolation_test.go` (cô lập giỏ/voucher/checkout 1 shop), `10_storefront_channel_ledger_test.go` (tách kênh đối soát, cân bằng sổ cái, Mailpit đúng shop & `/don-hang/{id}`), `11_storefront_publish_suspend_test.go` (draft/published, suspend trả `403` ở `ask`). *Playwright*: 2 host khác theme/footer pháp lý; mua VietQR SSE trên site riêng; SSO chéo miền.

**Tác động lộ trình 6 tháng (`docs/spec.md` — `đã kiểm chứng`, `suy luận`):** Tháng 4–6 kín các mục **làm sâu** (`Search`, `Chat`, `AI`, `AIOps`, `K8s`, `k6`, `Playwright`), `UC-S12` mức **Thấp**. Chèn trọn GĐ 0–4 (~8–11 tuần công) sẽ **trượt 4–6 tuần** hoặc cắt AI/AIOps. Nên làm **GĐ 0** đầu Tháng 4, **GĐ 1 + GĐ 2 mỏng** (subdomain + 4 section) cuối Tháng 4, **lùi GĐ 3 (service thứ 12, custom domain, Caddy, SSO) sang Tháng 7–8**.

## 6. Khuyến nghị, Rủi ro và Câu hỏi còn mở

**Khuyến nghị cụ thể:** (1) Chốt **GĐ 0** nối `web/` → `gateway` + Compose/CI trước đa tenant; (2) Làm **2 nấc**: 6 tháng mở rộng `UC-S12` mức subdomain + JSON theme/section, lùi custom domain + Caddy + SSO sang hậu đồ án; (3) Khóa rào pháp lý sớm (`slug`, `tax_code`, `seller_type`, `bct_notice_url`, `SiteFooter` bắt buộc, duyệt trước publish, thương mại hóa qua PSP có phép NHNN).

**Rủi ro:** Vỡ mốc Tháng 4–6 (tranh Người 1/2/5); rò dữ liệu chéo tenant (cache `Host`, `cart:<user_id>`, voucher sàn); pháp lý thu hộ không phép (NĐ `52/2024`), sót khấu trừ thuế (NĐ `117/2025`), liên đới nội dung (Luật `122/2025`); lạm dụng Caddy `ask` cạn hạn mức CA.

**Câu hỏi còn mở:**
1. Site riêng làm MVP subdomain cho `UC-S12` trong 6 tháng hay cắt mục khác ở `docs/spec.md` để làm trọn custom domain + Caddy?
2. Đơn site riêng có hoa hồng riêng, dùng Xu/voucher sàn không; thương mại hóa có cho shop gắn cổng thanh toán riêng?
3. Có mở rộng backend cho Guest checkout (đã có UI `/thanh-toan`) hay ẩn khi nối API thật?
