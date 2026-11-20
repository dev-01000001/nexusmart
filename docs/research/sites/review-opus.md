# Phản biện ba báo cáo site riêng: shopify.md, tech.md, fit.md

> Quy ước: **`đã kiểm chứng`** = tôi đã mở lại nguồn chính thức trong phiên này (20/11/2026) hoặc đọc code ở file:dòng ghi kèm. **`suy luận`** = đánh giá của tôi. **`nguồn thứ cấp`** = chỉ đọc được qua cổng tỉnh/báo, chưa đọc toàn văn gốc. Báo cáo này không tóm tắt lại ba báo cáo kia, chỉ ghi chỗ đúng, chỗ sai và chỗ thiếu.

## 1. Kiểm chứng lại 15 nhận định quan trọng

| # | Nhận định (báo cáo) | Kết luận | Nguồn đã mở |
| --- | --- | --- | --- |
| 1 | OS 2.0: 25 section/template, 50 block/section, 1.000 JSON template, lồng 8 cấp (shopify) | **Đúng** | [Limits](https://shopify.dev/docs/storefronts/themes/architecture/limits) |
| 2 | 20 domain/store, Plus 1.000; TLS tự cấp miễn phí (shopify) | **Đúng** | [Add a domain](https://help.shopify.com/en/manual/domains/add-a-domain) |
| 3 | Pattern $15/tháng, "0% phí sàn" (shopify) | **Đúng nhưng gây hiểu lầm**: không có phí giao dịch, nhưng vẫn chịu phí xử lý Etsy Payments. Đơn site riêng vẫn có chi phí thanh toán | [Pattern fees](https://help.etsy.com/hc/en-us/articles/360000337067-Pricing-and-Fees-for-Pattern) |
| 4 | Caddy on-demand bắt buộc `ask` trả 2xx (tech, fit) | **Đúng**. Tài liệu còn yêu cầu `ask` trả về trong vài ms và *tránh truy vấn DNS* trong `ask`. Vì vậy phần "DNS poller" trong tech.md phải chạy nền tách khỏi `ask`, còn `ask` chỉ tra một bảng đã đánh index | [Caddyfile options](https://caddyserver.com/docs/caddyfile/options), [On-Demand TLS](https://caddyserver.com/on-demand-tls) |
| 5 | `strict_sni_host on` chống Host spoofing (tech) | **Không kiểm chứng được**. Trang chính thức chỉ cho thấy cú pháp trong khối `servers`. Theo hiểu biết của tôi (`suy luận`), tùy chọn này buộc Host khớp SNI và dùng cho TLS client auth, không giảm rủi ro cạn hạn mức CA. Không nên đưa vào tiêu chí nghiệm thu | như trên |
| 6 | Let's Encrypt: 300 order/3 giờ/tài khoản; 50 cert/registered domain/7 ngày; 5 cert cùng bộ định danh/7 ngày (tech) | **Đúng** | [Rate limits](https://letsencrypt.org/docs/rate-limits/) |
| 7 | "`*.nexusmart.vn` **bắt buộc** dùng wildcard để tránh trần 50/tuần" (tech) | **Sai một phần**. Tài liệu ngầm định lần gia hạn không bị tính như order mới. Trần thật là khoảng 50 shop *mới* mỗi tuần. Wildcard vẫn nên dùng (ít cert, không lộ tên shop trên CT log), nhưng cần DNS-01, tức phải build Caddy kèm module DNS. Điểm cả ba báo cáo bỏ sót: cert mặc định giảm còn 64 ngày từ 10/2/2027 và 45 ngày từ 16/2/2028, nên số lần gia hạn custom domain tăng khoảng 40%, cần bật ARI | [Rate limits](https://letsencrypt.org/docs/rate-limits/), [90→45 ngày](https://letsencrypt.org/2025/12/02/from-90-to-45) |
| 8 | Route group `(site)` không nằm trong URL, nên rewrite sang `(site)/[shop]` sai (tech) | **Đúng**. Ngoài ra, trang "Multi-tenant" mà fit/tech trích là `đã kiểm chứng` thực chất chỉ trỏ tới repo ví dụ, không chứa quy tắc nào | [Route Groups](https://nextjs.org/docs/15/app/api-reference/file-conventions/route-groups), [Multi-tenant](https://nextjs.org/docs/15/app/guides/multi-tenant) |
| 9 | `fetch` trong middleware không được cache; CSP `nonce` ép dynamic rendering (tech) | **Đúng cả hai** | [Caching](https://nextjs.org/docs/15/app/guides/caching), [CSP](https://nextjs.org/docs/15/app/guides/content-security-policy) |
| 10 | Nhiều pod "bắt buộc Redis `cacheHandler` + `cacheMaxMemorySize: 0`" (tech) | **Đúng một phần**. `cacheHandler` dùng để chia sẻ cache giữa nhiều container; Redis chỉ là một lựa chọn. Lưu ý thêm: cache key là opaque, handler không được hiểu key như pathname | [cacheHandler](https://nextjs.org/docs/15/app/api-reference/config/next-config-js/incrementalCacheHandlerPath) |
| 11 | Cloudflare for SaaS: 100 hostname miễn phí, $0,10/hostname thêm, trần 50.000, apex proxying chỉ ở Enterprise (tech) | **Đúng** (apex là add-on trả phí của Enterprise) | [Plans](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/plans/) |
| 12 | Redirect > `rel=canonical` > sitemap (tech) | **Đúng**: redirect và canonical là tín hiệu mạnh, sitemap là tín hiệu yếu. Không được khai canonical khác nhau qua hai kênh | [Consolidate duplicate URLs](https://developers.google.com/search/docs/crawling-indexing/consolidate-duplicate-urls) |
| 13 | Luật TMĐT 122/2025/QH15 có hiệu lực 01/07/2026 (fit) | **Đúng** (ban hành 10/12/2025). Nội dung các Điều 3/14/17 mà fit trích: **không kiểm chứng được**, vì tôi chưa đọc toàn văn | [Công báo](https://congbao.chinhphu.vn/van-ban/luat-so-122-2025-qh15-468683.htm) |
| 14 | NĐ 117/2025: khấu trừ GTGT 1% + TNCN 0,5% cho hàng hóa (fit) | **Đúng** về ngày hiệu lực 01/07/2025 (Công báo). Tỷ lệ chỉ khớp qua `nguồn thứ cấp` | [Công báo NĐ 117](https://congbao.chinhphu.vn/van-ban/nghi-dinh-so-117-2025-nd-cp-45045.htm) |
| 15 | NĐ 52/2024: thu hộ/chi hộ cần giấy phép trung gian thanh toán (TGTT), vốn ≥ 50 tỷ (fit) | **Đúng** theo `nguồn thứ cấp` (Điều 22 khoản 2), chưa mở PDF gốc. Việc áp quy định này cho sàn tự giữ tiền là `suy luận` hợp lý | PDF fit trích, chưa mở |

## 2. Chỗ báo cáo nói sai về code

Đã đối chiếu bằng grep và đọc file. Code graph được index trong phiên này, nhưng các nhận định bên dưới đều là chuỗi ký tự cụ thể nên grep đủ dùng.

| Nhận định | Code thật | Đánh giá |
| --- | --- | --- |
| tech §3: auth.go "xác thực JWT HS256 gắn `claims.ShopID`… RPC ghi lấy `shop_id` duy nhất từ token" | [auth.go:219-243](file:///home/dev/Documents/Nexus_Ecom/pkg/common/auth/auth.go#L219-L243) dựng `Claims` thẳng từ metadata `x-user-id`/`x-user-role`/`x-shop-id`, không kiểm chữ ký. [auth.go:261](file:///home/dev/Documents/Nexus_Ecom/pkg/common/auth/auth.go#L261) dùng nhánh này khi request không có bearer. [docker-compose.yml:89](file:///home/dev/Documents/Nexus_Ecom/docker-compose.yml#L89) (tương tự ở :122, :153, :185, :224, :300, :343) mở cổng gRPC ra host | **Sai, và nghiêm trọng**. Ai gọi được gRPC đều có thể tự xưng là bất kỳ shop nào. Mọi thành phần mới gọi gRPC (Next.js server, `storefront`) sẽ mở rộng lỗ này |
| fit §2: `order` "phí mặc định `500 bps`" | 500 là default của **danh mục** ([product 000001_init.sql:8](file:///home/dev/Documents/Nexus_Ecom/services/product/migrations/000001_init.sql#L8)). Order chép tỷ lệ theo từng SKU tại [checkout.go:966](file:///home/dev/Documents/Nexus_Ecom/services/order/internal/service/checkout.go#L966) và [:1054](file:///home/dev/Documents/Nexus_Ecom/services/order/internal/service/checkout.go#L1054). Default ở [order 0001_init.sql:82](file:///home/dev/Documents/Nexus_Ecom/services/order/migrations/0001_init.sql#L82) chỉ là phương án dự phòng | **Sai chỗ**. "Phí kênh riêng" phải sửa ở bước tính bps trong checkout.go, không sửa ở default |
| fit §2: voucher "chưa giới hạn kênh", đề xuất thêm cột `allowed_channels` | [promotion 001_init.sql:7-8](file:///home/dev/Documents/Nexus_Ecom/services/promotion/internal/store/migrations/001_init.sql#L7-L8) đã có `scope IN ('PLATFORM','SHOP','FREESHIP')` cùng `shop_id` | **Thừa**. Một quy tắc theo request ("STOREFRONT ⇒ chỉ nhận SHOP của đúng shop") là đủ, không cần cột mới |
| fit §1-2: giỏ `cart:<user_id>` "dễ lẫn", đề xuất đổi key thành `cart:<user_id>:shop:<shop_id>` | Key theo user ở [redis_store.go:14](file:///home/dev/Documents/Nexus_Ecom/services/cart/internal/store/redis_store.go#L14), nhưng `CartItem.shop_id` đã có sẵn ([cart.proto:10](file:///home/dev/Documents/Nexus_Ecom/proto/nexusmart/cart/v1/cart.proto#L10)) | **Đề xuất có hại**. Tách key làm vỡ giỏ đa shop của sàn và buộc migrate dữ liệu. Chỉ cần lọc khi đọc và khi checkout |
| shopify §7: ràng buộc "1 User → 1 shop_id (`user.proto`)" | Ràng buộc không nằm trong proto mà nằm ở `users.shop_id` ([user 0001_init.sql:11](file:///home/dev/Documents/Nexus_Ecom/services/user/migrations/0001_init.sql#L11)), claim đơn ([auth.go:39](file:///home/dev/Documents/Nexus_Ecom/pkg/common/auth/auth.go#L39)) và [server.go:855](file:///home/dev/Documents/Nexus_Ecom/services/user/internal/service/server.go#L855). Bảng `shops` vẫn cho phép một owner có nhiều shop (index không unique, :52) | **Sai vị trí**. Muốn gỡ ràng buộc phải sửa ba chỗ |
| fit §2: `user` "`GetShop` chỉ nhận UUID", thiếu `slug` | [user.proto:242-245](file:///home/dev/Documents/Nexus_Ecom/proto/nexusmart/user/v1/user.proto#L242-L245) nhận `shop_id` hoặc `owner_user_id`. Báo cáo bỏ sót `UNIQUE(LOWER(name))` ([0001_init.sql:51](file:///home/dev/Documents/Nexus_Ecom/services/user/migrations/0001_init.sql#L51)), có sẵn để sinh slug không trùng | Đúng ý chính, thiếu lối tắt |
| fit §2: "7 DB đã tách theo `shop_id`" | Cart dùng Redis. `shop_id` ở payment và promotion có kiểu `TEXT` ([payment 0001_init.sql:145](file:///home/dev/Documents/Nexus_Ecom/services/payment/migrations/0001_init.sql#L145)), không phải UUID | Đúng một nửa |
| fit §2: gateway "thiếu CORS động" | Gateway không có CORS nào (grep ra 0 kết quả) | Nếu web gọi qua BFF cùng origin thì không cần CORS. Đề xuất này thừa |
| fit: web 0 `fetch()`, không có `middleware.ts`/Dockerfile, không có trong compose/CI; số dòng file; màu và chuỗi cứng | Grep khớp hết: StoreContext 1.855 dòng, sellerStore 1.712, auditStore 254, [Button.module.css:73](file:///home/dev/Documents/Nexus_Ecom/web/src/components/Button.module.css#L73), [ShopFooter.tsx:93](file:///home/dev/Documents/Nexus_Ecom/web/src/components/ShopFooter.tsx#L93), [gian-hang/[slug]/page.tsx:790](file:///home/dev/Documents/Nexus_Ecom/web/src/app/(shop)/gian-hang/[slug]/page.tsx#L790), 44 `page.tsx`, `/orders/` ở [notification.go:395](file:///home/dev/Documents/Nexus_Ecom/services/notification/internal/service/notification.go#L395) | **Đúng** |
| fit: thêm `storefront` thành service thứ 12 | [spec.md:23](file:///home/dev/Documents/Nexus_Ecom/docs/spec.md#L23) quy định 11 microservice; [spec.md:129](file:///home/dev/Documents/Nexus_Ecom/docs/spec.md#L129) gán UC-S12 cho **User, Product**, ưu tiên Thấp | **Đúng**, và spec còn ủng hộ mạnh hơn mức fit nêu |

## 3. Mâu thuẫn giữa ba báo cáo và bên tôi chọn

| Chủ đề | Các bên | Chọn | Lý do |
| --- | --- | --- | --- |
| Đích rewrite | shopify: `(site)/[shop]` "đúng hướng"; tech: `/s/[host]` | **tech** | Route group không có trong URL (#8), nên rewrite cần một tiền tố thật |
| Service mới | shopify, tech: đồng ý `storefront`; fit: thành 12 service | **fit, cụ thể hơn**: module trong `user` | spec gán UC-S12 cho User/Product. Bảng cấu hình site vẫn nằm trong `user_db`, không phạm DB-per-service |
| Giỏ hàng | fit: tách key; shopify: tách hiển thị; tech: một giỏ chung | **Một giỏ, lọc theo shop** | Proto đã có `shop_id`; tách key làm vỡ sàn |
| Voucher trên site | fit: cột mới; hai bên còn lại: câu hỏi mở | **Dùng `scope` có sẵn** | Không cần migration |
| Phí đơn site | shopify: giảm phí kiểu Pattern; fit: "phí kênh riêng" | **MVP giữ tỷ lệ danh mục, chỉ ghi kênh bất biến** | Pattern vẫn có phí thanh toán (#3); quyết giá khi có dữ liệu |
| Wildcard | tech: "bắt buộc" | **Nên dùng, không bắt buộc** | #7 |
| Tiền qua NexusMart | shopify, tech: ổn; fit: vướng NĐ 52 | **fit đúng nhưng đặt sai khung** | Nếu đúng là cần phép thì sàn *hiện tại* cũng đã vướng; site riêng không tạo ra rủi ro này |
| Checkout ở đâu | tech: câu hỏi mở; shopify: `/thanh-toan` gắn logo shop + SSO | **MVP checkout trên `nexusmart.vn`** | Bỏ được SSO khỏi MVP (xem §5) |
| Lịch | tech: 2 pha, không tính việc nối web; fit: GĐ 0 cỡ L | **fit** | web vẫn 100% mock, đã kiểm chứng |

## 4. Lỗ hổng chưa ai nêu

**Bảo mật đa tenant**
- Giả mạo `x-shop-id` (§2, dòng đầu) là lỗ lớn nhất. Cách vá: interceptor chỉ chấp nhận metadata khi có JWT hợp lệ hoặc khi kết nối đi qua mTLS nội bộ; compose không mở cổng 5005x ra host. Phải vá **trước** khi có thêm client gRPC.
- JWT HS256 dùng secret đối xứng: bên nào giữ secret để *xác minh* cũng *ký* được token cho shop bất kỳ. Khi có BFF trên nhiều domain hoặc `storefront` xác minh token, phải chuyển sang EdDSA/RS256 kèm JWKS (`suy luận`).
- Cache poisoning theo Host: middleware phải trả 404 cho host không có trong bảng *trước khi* rewrite. Chỉ tin `X-Forwarded-Host` từ proxy, nếu không ISR có thể lưu trang của host giả.
- Cookie phiên trên `nexusmart.vn` phải là host-only, không đặt `Domain=.nexusmart.vn`. Nếu không, mọi subdomain của shop sẽ nhận token (`suy luận`).
- Lừa đảo bằng tên: slug kiểu `vietcombank-uudai`. Cần danh sách thương hiệu cấm và duyệt slug, không chỉ danh sách slug dành riêng như tech nêu.

**SSO qua nhiều tên miền**
- Logout: thu hồi `jti` chỉ vô hiệu access token 15 phút. Refresh token trên từng domain phải bị thu hồi theo `family_id` (đã có ở [user 0001_init.sql:26](file:///home/dev/Documents/Nexus_Ecom/services/user/migrations/0001_init.sql#L26)).
- `redirect_uri` phải khớp chính xác domain đã xác minh. Khi shop gỡ domain hay bị khóa, phải xóa domain đó khỏi danh sách cho phép ngay, nếu không sẽ thành open redirect.
- Dữ liệu cá nhân: đăng nhập chung nghĩa là shop thấy người mua của nền tảng. Luật BVDLCN 91/2025/QH15 (hiệu lực 01/01/2026, `nguồn thứ cấp`) đòi hỏi sự đồng ý cho từng mục đích xử lý. Chưa báo cáo nào nhắc.

**Trùng nội dung SEO**
- Một sản phẩm có tới ba URL: PDP trên sàn, `<slug>.nexusmart.vn`, custom domain. Quy tắc nên dùng: site subdomain đặt canonical về PDP sàn (cùng một thương hiệu nexusmart.vn); custom domain đặt canonical về chính nó và 301 từ subdomain sang. Mỗi host một sitemap, không khai canonical lệch nhau (#12).
- Hàng trăm site gần giống nhau (cùng template, cùng mô tả) có thể bị Google coi là nội dung giá trị thấp (`suy luận`). Mặc định nên `noindex` site chưa có nội dung riêng tối thiểu.

**Tồn kho dùng chung**
- Tồn nằm ở một chỗ: `skus.stock/reserved_stock/version` ([product 000001_init.sql:43-45](file:///home/dev/Documents/Nexus_Ecom/services/product/migrations/000001_init.sql#L43-L45)), nên hai kênh tự động dùng chung. Đây là điểm tốt. Rủi ro nằm ở flash sale: suất trừ bằng Lua trong `promotion` còn tồn ở `product`, nên site riêng bán giá thường có thể ăn mất hàng đã hứa cho suất flash. Cần giữ (reserve) tồn trong `product` cho suất flash ngay khi chiến dịch bắt đầu.
- `sales_channels` mà fit đề xuất chỉ quyết định *hiển thị*, không phân bổ tồn. Phân bổ tồn theo kênh không cần cho MVP.

**Khuyến mãi của sàn trên site riêng**
- Nếu cho dùng voucher PLATFORM, freeship 249k, Xu, thì sàn trả tiền cho lưu lượng mà shop sở hữu. Nếu chặn thì người mua thấy giá khác nhau giữa sàn và site. Cần quy tắc giá rõ: giá niêm yết như nhau, chỉ khác phần khuyến mãi do sàn tài trợ.
- [spec.md:87](file:///home/dev/Documents/Nexus_Ecom/docs/spec.md#L87) (UC-B14) cảnh báo "link ngoài sàn" trong chat. Site riêng chính là link ra ngoài sàn, nên chính sách này phải viết lại.

**Đối soát tiền**
- `sales_channel` phải được ghi bất biến lúc tạo đơn và chép sang `payment_shop_allocations` ([payment 0001_init.sql:141-153](file:///home/dev/Documents/Nexus_Ecom/services/payment/migrations/0001_init.sql#L141-L153)), nếu không đơn hoàn tiền sẽ bị tính sai kênh.
- `return_url` của VietQR/webhook phải nằm trong danh sách domain cho phép theo shop.
- Đối soát COD theo kênh cần dữ liệu từ `shipping`; chưa ai đề cập.

**Pháp lý**
- Không ai trả lời: khi site chạy trên domain riêng nhưng checkout là của NexusMart, NexusMart là "sàn" hay "nhà cung cấp hạ tầng"? Người bán có phải thông báo website riêng không? Cần đọc toàn văn Luật 122 (#13). Đến lúc đó, coi site riêng như một phần của sàn: khấu trừ thuế (#14) và trách nhiệm liên đới với nội dung giữ nguyên.
- Cần quy trình gỡ bỏ khi tên miền xâm phạm nhãn hiệu.

## 5. Kết luận

### 5.1 Kiến trúc khuyến nghị
1. Một app `web/`. `middleware.ts` chuẩn hóa Host, trả 404 cho host lạ, rewrite sang `app/(site)/s/[host]/…`, chặn `/s/*` trên domain chính. Trang site ẩn danh, render ISR, gắn tag `site:<id>`.
2. Cấu hình site nằm trong `user` (`user_db`): `sites`, `site_versions` (snapshot JSON), `domains`. Proto `user.proto` thêm `GetSiteByHost`, `SaveSiteDraft`, `PublishSite`, `RollbackSite`. Tách thành `storefront` khi có custom domain.
3. Theme là dữ liệu: token và section JSON, Go validate schema. Không có trường HTML.
4. MVP: nút mua trên site chuyển về `nexusmart.vn` (giỏ và checkout gắn thương hiệu shop, `?shop=` khóa checkout một shop). Không cần SSO, không cần guest cart.
5. Lõi: `sales_channel` + `storefront_shop_id` bất biến trên order và allocation. Đơn STOREFRONT chỉ được một shop và chỉ nhận voucher SHOP, nhờ trường `scope` có sẵn.
6. Sau đồ án: custom domain qua Caddy on-demand (`ask` tra bảng, poller DNS chạy riêng), SSO PKCE, JWT bất đối xứng.

### 5.2 MVP theo giai đoạn
Cỡ theo fit: S = 1–3 ngày, M = 4–7 ngày, L = 2–3 tuần.

| GĐ | Nội dung | Cỡ | Nghiệm thu đo được |
| --- | --- | :---: | --- |
| 0a | Vá tin cậy gRPC: bỏ nhánh metadata không JWT (hoặc yêu cầu mTLS), gỡ port 5005x khỏi compose | **S** | Test Go: gọi gRPC kèm `x-shop-id` giả, không có JWT, nhận `Unauthenticated`; `docker compose config` không mở cổng 5005x ra host |
| 0b | Nối `web/` vào gateway (theo fit GĐ 0) | **L** | 0 dùng `localStorage` cho giỏ/đơn; E2E mua VietQR chạy trên DB thật |
| 1 | Kênh bán trong lõi (order, payment, promotion) | **M** | Checkout STOREFRONT có SKU của shop khác hoặc voucher PLATFORM nhận `InvalidArgument`; `is_balanced=true`; báo cáo đối soát tách kênh cộng lại khớp tổng 100% |
| 2 | Site subdomain: slug, `sites`/`site_versions`, middleware, 6 section, nháp/xuất bản/rollback, sitemap/robots/canonical theo host | **L** | 2 shop render khác nhau theo Host; host lạ trả 404 mà không gọi backend; publish hiển thị ≤ 10 s; URL nháp trả 404 công khai; khóa shop thì site trả 404 ≤ 60 s; canonical khớp quy tắc ở §4 trên 100% PDP mẫu |
| 2b | Giữ tồn cho suất flash sale | **S** | k6: site và flash sale bán song song 200 VU, 0 lần bán quá tồn |
| 3 (sau đồ án) | Custom domain, Caddy `ask`, SSO PKCE, JWT EdDSA | **M+M** | `ask` p99 < 10 ms; domain chưa verify không được cấp cert (staging); logout thu hồi refresh family trên mọi domain |

### 5.3 Năm quyết định cho chủ dự án

| Quyết định | Khuyến nghị | Hệ quả từng lựa chọn |
| --- | --- | --- |
| 1. Cấu hình site nằm ở đâu | **Module trong `user`** | (a) `user`: đúng spec UC-S12, không thêm service, sau này tách ra tốn khoảng M. (b) `storefront` riêng: kiến trúc sạch, nhưng thành 12 service, thêm Dockerfile/CI/proto, trượt tháng 4–6. (c) Trong `product`: sai bounded context |
| 2. Checkout và đăng nhập | **MVP chuyển về `nexusmart.vn`** | (a) Rẻ nhất, mất thêm một bước chuyển trang. (b) SSO PKCE + BFF trên mỗi host: UX tốt, thêm khoảng M và kéo theo chuyển JWT bất đối xứng. (c) Cookie `.nexusmart.vn`: chỉ dùng được cho subdomain và lộ token cho mọi shop |
| 3. Phí đơn site riêng | **Giữ tỷ lệ danh mục, ghi kênh** | (a) Không phải đổi code tính phí, có dữ liệu để định giá sau. (b) Giảm %: hút shop, nhưng shop có động cơ kéo khách từ sàn sang site. (c) 0% + phí tháng: cần billing định kỳ, hiện chưa có |
| 4. Khuyến mãi sàn trên site | **Chỉ SHOP voucher và flash sale của shop** | (a) Ngân sách sàn không chảy sang kênh của shop. (b) Mở PLATFORM/Xu: tăng chuyển đổi nhưng sàn trợ giá cho kênh shop. (c) Mở có tính phí: phức tạp hóa đối soát |
| 5. Custom domain trong đồ án | **Không, chỉ subdomain** | (a) Bỏ được Caddy/SSO/DNS khỏi 6 tháng. (b) Caddy trong tháng 6: rủi ro hạn mức CA và cụm storage. (c) Cloudflare for SaaS: 100 hostname miễn phí, nhưng apex cần Enterprise (#11) |

### 5.4 Rủi ro, xếp từ cao xuống thấp

| Mức | Rủi ro | Giảm thiểu |
| --- | --- | --- |
| Nghiêm trọng | Giả mạo `shop_id` qua metadata gRPC, thành phần mới càng làm rộng thêm | GĐ 0a |
| Nghiêm trọng (thương mại) | Thu hộ không phép (NĐ 52/2024), áp dụng cho cả sàn hiện tại | Đi qua PSP có phép; đồ án giữ sandbox |
| Cao | Trượt mốc tháng 4–6 vì GĐ 0b cỡ L | Lùi GĐ 3 (custom domain, SSO) sang sau đồ án |
| Cao | Bán quá suất flash sale khi dùng chung tồn kho | GĐ 2b |
| Cao | Cache poisoning theo Host, lộ bản nháp | 404 trước rewrite, Draft Mode `no-store` |
| Trung bình | Trùng nội dung, site "mỏng" bị Google hạ hạng | Quy tắc canonical, `noindex` mặc định |
| Trung bình | Lừa đảo bằng slug/domain | Danh sách thương hiệu cấm, duyệt slug |
| Trung bình | Shop kéo khách khỏi sàn (né hoa hồng); UC-B14 mâu thuẫn | Viết lại chính sách, quyết định 3 |
| Thấp–TB | Cert 64/45 ngày, hạn mức LE, storage Caddy cụm | ARI, wildcard, storage chung |
| Thấp (đồ án) | Thiếu `tax_code`/`seller_type` cho khấu trừ NĐ 117 | Chừa cột dữ liệu, HĐĐT ngoài phạm vi |
