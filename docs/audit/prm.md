# Báo cáo rà soát nghiệp vụ – Worker `prm` (Khuyến mãi, Xu, Flash Sale, Thông báo)

- **Commit rà soát**: `0db2cdd` (HEAD)
- **Stack kiểm chứng**: `nexusmart-audit` (Gateway `http://localhost:48080`, Web `http://localhost:43000`, Mailpit `http://localhost:48025`, NATS `http://localhost:48222`, Postgres/Redis chỉ đọc)
- **Phiên kiểm thử API**: `run_id=5A2FF3` (tài khoản riêng `audit-prm-1-5a2ff3@nexusmart.local`, `audit-prm-2-5a2ff3@nexusmart.local`; buyer demo `minhquan.nguyen@nexusmart.vn` chỉ đọc)

---

## 1. Bảng Use Case

| UC | Ưu tiên | Trạng thái | Backend (file:dòng) | REST | Web | Bằng chứng | Ghi chú |
|---|---|---|---|---|---|---|---|
| `UC-B15` | Cao | **LỖI** | `services/promotion/internal/calculator/calculator.go:100-355`, `services/promotion/internal/service/promotion.go:589-879`, `services/order/internal/service/checkout.go:230-380,1141-1165` | `GET /api/v1/vouchers`, `POST /api/v1/promotions/preview`, `POST /api/v1/checkout` | `/gio-hang`, `/thanh-toan` | `POST /promotions/preview` (200) gộp đủ `SHOP+PLATFORM+FREESHIP+xu` đúng thứ tự & chặn âm; `POST /checkout` + webhook `FAILED` (200) hoàn sạch 3 voucher + 10.000 xu. **Lỗi**: đơn `b242a315-...` `payment_shop_allocations.subtotal_vnd=115000` thay vì `180000` (shop gánh 65k giảm của sàn); đơn 0₫ `70d739fe-...` (`payment_id=""`) hoàn tất seller tăng 0₫; huỷ đơn con đa shop `109ecb60-...` không hoàn 9.487 xu & 1 lượt voucher shop. | Lỗi P0: `PRM-B1`, `PRM-B3`; lỗi P2: `PRM-B8`. `orders` không lưu `platform_cost_vnd`/`shop_cost_vnd`. |
| `UC-B16` | Trung bình | **MỘT PHẦN** | `services/promotion/internal/lua/reserve_atomic.lua:1-151`, `release_atomic.lua:1-64`, `services/promotion/internal/service/promotion.go:880-963` | `GET /api/v1/flash-sales`, `POST /api/v1/checkout` | `/`, `/flash-sale`, `/san-pham/[slug]`, `/thanh-toan` | `go test ./internal/lua ./internal/service` PASS (1.000 concurrent / 100 suất bán đúng 100). Live: mua suất live `201` (`flash_sale_discount_vnd=80000`), vượt `per_user_limit=1` trả `409 FLASH_USER_LIMIT_EXCEEDED`, hết suất trả `429 RESOURCE_EXHAUSTED FLASH_SOLD_OUT`, suất sắp mở trả `409 outside active window`, huỷ đơn trả lại suất `0→1`. | Lỗi `PRM-B12` (giỏ hàng tính giá gốc 200k thay vì giá flash 120k; `/thanh-toan` chấp nhận suất upcoming; không có hạn thanh toán ngắn riêng & vẫn cho COD), `PRM-D1`, `PRM-D4`. |
| `UC-B18` | Thấp *(không bắt buộc)* | **LỖI** | `services/promotion/internal/service/promotion.go:500-587`, `services/promotion/internal/events/consumer.go:1-190`, `services/order/internal/service/lifecycle.go:611` | `GET /api/v1/coins/balance`, `POST /api/v1/admin/coins/credit` | `/tai-khoan?tab=xu`, `/thanh-toan`, `/hoi-vien` *(mock)* | `GET /coins/balance` (demo buyer) trả `42000` + `recent_transactions`; `POST /admin/coins/credit` idempotent (`already_credited: true` ở lần 2); `order.completed` tự cộng xu qua NATS (`promotion_db.processed_events`). **Lỗi**: đơn 0₫ trả bằng xu (`70d739fe-...`) vẫn được cộng `+2000 xu` (`6000→8000`). | Lỗi P0 `PRM-B2` (thưởng xu tính trên `subtotal_vnd` gốc), thiếu thu hồi xu khi trả hàng, thiếu hạn dùng xu (`expires_at`) & job hết hạn, thiếu cộng xu đánh giá; `/hoi-vien` mock cứng 42.000 xu (`PRM-D5`). |
| `UC-B22` | Trung bình | **MỘT PHẦN** | `services/promotion/internal/service/promotion.go:258-340`, `services/promotion/internal/store/store.go:199-245` | `POST /api/v1/vouchers/{id}/save`, `GET /api/v1/vouchers` | `/gian-hang/[slug]`, `/san-pham/[slug]`, `/tai-khoan?tab=voucher` | `POST /vouchers/{id}/save` trả `200` (`saved: true`), gọi lại trả `200` idempotent, lưu mã hết hạn trả `409 voucher has expired`, `GET /vouchers` trả `saved: true`. | Thiếu nhắc voucher sắp hết hạn (bước 5 — nhóm `PROMOTION` không có publisher `PRM-B14`), thiếu điều kiện voucher khách mới; `GET /vouchers` trả cả voucher hết hạn/chưa mở (`PRM-B8`). |
| `UC-B23` | Trung bình | **LỖI** | `services/notification/internal/service/notification.go:70-260`, `services/notification/internal/grpcserver/server.go:1-200`, `services/gateway/internal/router/handlers_notification.go:81-206` | `GET /api/v1/notifications`, `POST /api/v1/notifications/read`, `GET/PUT /api/v1/notifications/preferences` | `/tai-khoan?tab=thong-bao`, `/tai-khoan?tab=cai-dat` | `GET /notifications` (200, `unread_count`), `POST /notifications/read` (200, `updated_count=1, unread_count=0`), `PUT /notifications/preferences` với `order_in_app=false, system_in_app=false, order_email=false` trả về `order_in_app=true, system_in_app=true, order_email=false` (guard hoạt động); huỷ đơn trên `b2` khi `order_email=false` vẫn tạo in-app và KHÔNG gửi mail (Mailpit `4→4`). | Lỗi P1 `PRM-B5` (COD báo thanh toán thành công giả), P2 `PRM-B6` (link 404 `/verify-otp`, `/seller/dashboard`, `/orders/<group_id>`), `PRM-B14` (không có Push/PROMOTION), `PRM-B15` (không có chuông Header, UI lệch default & không khoá checkbox guard). |
| `UC-S05` | Trung bình | **MỘT PHẦN** | `services/promotion/internal/service/promotion.go:98-215`, `services/promotion/internal/store/store.go:168-198` | `POST /api/v1/seller/vouchers`, `GET /api/v1/seller/vouchers` | `/seller/khuyen-mai` | `POST /seller/vouchers` trả `201`; mã trùng `409 ALREADY_EXISTS`; `ends_at <= starts_at` trả `400`; buyer gọi trả `403`. `GET /seller/vouchers` (200) **có trả cả voucher hết hạn và chưa bắt đầu** (xác nhận khác lời khai cũ ở `WEB_INTEGRATION_REPORT.md` §8). | Thiếu hoàn toàn API & UI sửa, bật/tắt (`is_active`), xoá voucher (`PATCH`/`DELETE /seller/vouchers/{id}` → `404`, `PRM-B9`). |
| `UC-S08` | Thấp *(không bắt buộc)* | **LỖI** | `services/promotion/internal/service/promotion.go:388-450`, `services/gateway/internal/router/handlers_promotion.go:527-562` | `POST /api/v1/seller/flash-sales/{id}/items` | `/seller/khuyen-mai` (tab Flash sale) | Kiểm quyền sở hữu SKU (`403 cannot register another shop's SKU`) và chống trùng khung giờ (`409 overlapping active flash sale window`). **Lỗi**: gửi `flash_price_vnd=250000` (> giá gốc 200k) và `total_slots=99` (> tồn kho 10) vẫn trả `201 PENDING`; sau khi duyệt `reserved_stock=0`. | Lỗi `PRM-B10` (không kiểm giá < giá gốc/30 ngày, không kiểm tồn kho, không khoá tồn kho), `PRM-B11` (seller không liệt kê được item `PENDING`/`REJECTED` sau tải lại trang). |
| `UC-S18` | Trung bình | **THIẾU** | Không có (`rg` trống trên `proto/`, `services/`) | Không có | `/seller/khuyen-mai` (tab Combo chỉ là mock `PromotionsBundleTab.tsx`) | `rg -i "shop_discount\|direct_discount\|discount_campaign\|product_discount\|ShopCampaign\|CreateCampaign" proto/ services/` → 0 kết quả; `router.go` không có route; `PromotionsBundleTab.tsx` lưu trạng thái giả vào `sellerStore.ts`. | `PRM-B13`. |
| `UC-A07` | Trung bình | **MỘT PHẦN** | `services/promotion/internal/service/promotion.go:98-185,341-498`, `services/promotion/internal/store/store.go:475-560` | `POST /api/v1/admin/vouchers`, `POST /api/v1/admin/flash-sales`, `POST /api/v1/admin/flash-sale-items/{item_id}/approve` | `/admin/thanh-vien` (`AdminPromoCoinsPanel.tsx`) | `POST /admin/vouchers` (201, seller gọi `403`), `POST /admin/flash-sales` (201), `POST /admin/flash-sale-items/{id}/approve` (200) hoạt động và đồng bộ Redis. | `PRM-B11` (Admin không có danh sách item `PENDING`, phải dán tay UUID `item_id` vào form duyệt), `PRM-B9` (không có phạm vi danh mục `category_id`, không tắt/xoá được voucher), thiếu báo cáo sau chương trình. |

### 1.1. Email & thông báo theo luồng nghiệp vụ

| Luồng (UC) | Trạng thái | Event NATS & Consumer | Mailpit & In-App (kết quả kiểm sống) | Ghi chú / Lỗi |
|---|---|---|---|---|
| Đăng ký & OTP (`UC-B01`) | **LỖI** | `user.registered` (`services/user/internal/service/server.go:194,292` → `notification.go:312-344`) | Có email OTP (`CWKJ5T7pz3UPWRBXoKYDy4`) và in-app `"Chào mừng đến với NexusMart"`. | **P1 `PRM-B7`**: OTP lộ thẳng ở `Subject: "NexusMart Verification OTP: 108314"`, toàn bộ email OTP bằng tiếng Anh; **P2 `PRM-B6`**: link `/verify-otp` trả 404; `ResendOTP` phát lại `user.registered` gây trùng thông báo chào mừng; `user` không dùng outbox. |
| Duyệt shop (`UC-A01`) | **MỘT PHẦN** | `user.shop_reviewed` (`user/server.go:1096` → `notification.go:346-380`) | Đã có 9 `processed_events` và thông báo `"Kết quả duyệt gian hàng Xưởng Dệt Bạch Đằng"` trên `seller.bachdang@nexusmart.local`. | **P2 `PRM-B6`**: link `/seller/dashboard` sai route (route thật là `/seller`); **P1/P3 `PRM-B7`**: Subject tiếng Anh + enum thô (`APPROVE`). |
| Đặt hàng (`UC-B06`) | **LỖI** | `order.created`, `order.confirmed`, `order.cancelled` (`order/checkout.go`, `lifecycle.go` → `notification.go:382-535`) | Buyer nhận đủ email + in-app với đúng mã đơn & số tiền. | **P1 `PRM-B5`**: đơn COD phát kèm email & in-app giả `"Thanh toán ... qua COD thành công"` ngay lúc đặt; **P2 `PRM-B14`**: seller nhận **0 thông báo** khi có đơn mới/huỷ; Subject tiếng Anh (`PRM-B7`). |
| Thanh toán (`UC-B07`, `UC-PAY01`, `UC-PAY06`) | **LỖI** | `payment.succeeded`, `payment.failed`, `payment.expired` (`payment/src/service.rs` → `notification.go:595-670`) | Buyer nhận email + in-app cho `succeeded` (`kHmG2wYP...`) và `failed` (`UjexXtNr...`); `expired` đã soát code & `go test`. | **P2 `PRM-B6`**: link là `/orders/<checkout_group_id>` (`GET /api/v1/orders/38141b76-...` → 404); **P1 `PRM-B5`**: COD phát `payment.succeeded` giả; Subject tiếng Anh (`PRM-B7`). |
| Giao hàng (`UC-S04`, `UC-SHIP04`) | **MỘT PHẦN** | `order.shipped`, `order.delivered`, `order.completed`, `shipment.updated` (`notification.go:536-594,699-736`) | Buyer nhận email + in-app cho `shipped`, `delivered`, `completed` (ghi đúng mã vận đơn & `+2000 xu`); `shipment.updated` (`delivering`, `delivered`) chỉ tạo in-app, **không gửi email**. | **P2 `PRM-B14`**: `shipment.updated` không gửi email và im lặng nuốt sự kiện (vẫn ghi `processed_events`) nếu thiếu `order_participants`; Subject tiếng Anh, enum thô `delivering`/`delivered`/`mock_express` (`PRM-B7`). |
| Hoàn tiền (`UC-PAY04`) | **MỘT PHẦN** | `payment.refunded` (`payment/src/service.rs:2129` → `notification.go:671-698`) | Buyer nhận email (`b462JjK9...`) & in-app `"Hoàn tiền thành công"` (`1000 VND`, đúng `refund_id`, đúng link sub-order `b242a315-...`). | Subject tiếng Anh `"NexusMart - Payment Refunded (1000 VND)"`, số tiền không dấu phân cách (`PRM-B7`); không có sự kiện/thông báo cho `order.return_requested` (`UC-S10`). |

*Tổng hợp trạng thái 9 UC chính*: **ĐỦ: 0 · MỘT PHẦN: 4** (`UC-B16`, `UC-B22`, `UC-S05`, `UC-A07`) **· LỖI: 4** (`UC-B15`, `UC-B18`, `UC-B23`, `UC-S08`) **· THIẾU: 1** (`UC-S18`) **· CHƯA KIỂM: 0**.

---

## 2. Bảng đối chiếu Shopify / sàn

| Nghiệp vụ | UC hoặc ngoài phạm vi | Trạng thái | Ghi chú |
|---|---|---|---|
| Mã giảm giá và giảm tự động | Mã giảm giá: `UC-B15`, `UC-S05`, `UC-A07`; Giảm tự động trên SP: `UC-S18` | **MỘT PHẦN / THIẾU** | Có mã voucher (cố định & phần trăm), nhưng thiếu hoàn toàn giảm trực tiếp trên sản phẩm (`UC-S18`, `PRM-B13`) và lỗi hạch toán/bật-tắt voucher (`PRM-B1`, `PRM-B9`). |
| Mua X tặng Y | Ngoài phạm vi đặc tả (`docs/spec.md:29`: *"Combo, mua kèm..."*; `docs/spec.md:39`: *"Mọi tính năng không có trong tab Use case"*) | **Ngoài phạm vi** | Không có backend; chỉ có UI mock ở `/seller/khuyen-mai` (`PromotionsBundleTab.tsx`) và `/flash-sale` (`COMBO_OFFERS`). |
| Miễn phí vận chuyển | Mã freeship: `UC-B15`; Freeship ngưỡng shop: `UC-SHIP06` | **MỘT PHẦN** | `promotion` hỗ trợ mã `FREESHIP` (áp đúng lên phí ship). Không có rule tự động freeship theo ngưỡng giá trị đơn (dù `web/src/data/vouchers.ts:111` mock `PLATFORM_FREESHIP_THRESHOLD=249000`). |
| Kết hợp khuyến mãi | `UC-B15` (thứ tự: Flash sale → Voucher shop → Voucher sàn → Freeship → Xu) | **LỖI** | Quy tắc kết hợp định nghĩa rõ trong `calculator.go:100-355`, tổng giảm bị chặn không vượt `subtotal + shipping`. Tuy nhiên hạch toán bên chịu chi phí sai (`PRM-B1`) và huỷ một phần không hoàn khuyến mãi (`PRM-B3`). |
| Email marketing | Ngoài phạm vi đặc tả (`docs/spec.md:39`; không có trong tab Use case dòng 43–143) | **Ngoài phạm vi** | `rg` xác nhận không có backend. |
| Automation (marketing automation) | Ngoài phạm vi đặc tả (`docs/spec.md:39`) | **Ngoài phạm vi** | Không có trong đặc tả và không có code. |
| Form thu lead | Ngoài phạm vi đặc tả (`docs/spec.md:39`) | **Ngoài phạm vi** | Không có trong đặc tả và không có code. |
| Chat Inbox có nhân viên bán hàng AI | `UC-B14`, `UC-S09`, `UC-AI01`, `UC-AI10`, `UC-AI12` | **Ngoài phạm vi `prm`** | Thuộc nhóm Chat/AI; ở phần `notification`, chưa có consumer sự kiện `chat.*` để gửi push/thông báo tin nhắn mới khi offline. |
| Hợp tác influencer | Ngoài phạm vi đặc tả (`docs/spec.md:25`: *"Livestream, game, affiliate, trả góp"*; `docs/spec.md:39`) | **Ngoài phạm vi** | Không có trong đặc tả và không có code (`rg` trống). |
| Mẫu email và SMS giao dịch | Email giao dịch: `UC-B01`, `UC-A01`, `UC-B07`, `UC-B08`, `UC-B23`, `UC-S04`, `UC-PAY04`, `UC-SHIP04`; **SMS giao dịch**: Ngoài phạm vi (`docs/spec.md:39,74` chỉ đặc tả OTP qua email) | **LỖI** (Email) / **Ngoài phạm vi** (SMS) | Email giao dịch cứng trong Go (không quản lý mẫu động; trang mẫu Email/Zalo ZNS tại `/admin/cau-hinh` là mock), lộ OTP ở Subject & tiếng Anh (`PRM-B7`), COD báo thanh toán thành công giả (`PRM-B5`), thiếu email `shipment.updated` & thiếu thông báo seller (`PRM-B14`). |
| Chính sách cửa hàng (phần thông báo/tuân thủ) | `UC-AI11` (FAQ chính sách), `UC-B27` (quyền riêng tư, Thấp) | **Ngoài phạm vi `prm`** | Không có quản trị chính sách ở `promotion`/`notification`; web chỉ hiển thị nội dung tĩnh (`/tro-giup`, `/ve-nexusmart`, trang gian hàng). |
| Flash sale (toàn sàn) | `UC-A07`, `UC-B16` | **MỘT PHẦN** | Khung giờ & trừ suất atomic Lua Redis hoạt động đúng; còn lỗi giỏ hàng/upcoming/hạn thanh toán (`PRM-B12`), seed vượt tồn kho (`PRM-D1`) và khối Deal sốc/Combo mock (`PRM-D4`). |
| Voucher sàn và voucher shop | `UC-A07`, `UC-S05`, `UC-B15`, `UC-B22` | **LỖI** | Đủ 3 scope `PLATFORM`/`SHOP`/`FREESHIP` + ví voucher, nhưng lỗi cost bearer (`PRM-B1`), hoàn một phần (`PRM-B3`), `budget_vnd` (`PRM-B8`), thiếu sửa/tắt/xoá & phạm vi danh mục (`PRM-B9`). |
| Đăng ký sản phẩm vào chiến dịch | Flash sale: `UC-S08`, `UC-A07`; Chiến dịch sàn khác (CSR/BST): Ngoài phạm vi (`docs/spec.md:39`) | **LỖI** (`UC-S08`) / **Ngoài phạm vi** (CSR/BST) | `UC-S08` thiếu kiểm giá/tồn kho & khoá kho (`PRM-B10`), Admin không có danh sách item `PENDING` (`PRM-B11`). Tab Chiến dịch Sợi Xanh/Mega-menu ở `/seller/khuyen-mai` (`PromotionsCampaignTab.tsx`) là mock. |

---

## 3. Lỗi

### `PRM-B1` [P0] — Sai hạch toán bên chịu chi phí giảm giá sàn & đơn 0₫ (trả bằng xu/voucher) khiến người bán nhận 0₫
- **Tái hiện**:
  1. Đặt đơn có voucher sàn (`-15.000₫`) + xu (`-50.000₫`) + voucher shop (`-20.000₫`) trên sản phẩm `200.000₫`, ship `26.000₫`:
     ```bash
     curl -sS -X POST http://localhost:48080/api/v1/checkout \
       -H "Authorization: Bearer $TOKEN" -H "Idempotency-Key: prm-b1-case1" -H "Content-Type: application/json" \
       -d '{"address_id":"<ADDR_ID>","from_cart":false,"items":[{"sku_id":"<SKU_ID>","quantity":1}],"shop_voucher_ids":{"<SHOP_ID>":"<SHOP_V_ID>"},"platform_voucher_id":"<PLAT_V_ID>","coins_to_use":50000,"payment_method":"VIETQR","shipping_choices":[{"shop_id":"<SHOP_ID>","carrier_code":"mock_express","service_code":"standard"}]}'
     ```
  2. Đặt đơn 0₫ bằng xu (`subtotal=200.000`, `shipping=26.000`, `coins_to_use=226000`), sau đó giao hàng & `POST /api/v1/orders/{id}/confirm-received`, rồi kiểm `GET /api/v1/seller/balance`:
     ```bash
     curl -sS -X POST http://localhost:48080/api/v1/checkout \
       -H "Authorization: Bearer $TOKEN" -H "Idempotency-Key: prm-b1-zero" -H "Content-Type: application/json" \
       -d '{"address_id":"<ADDR_ID>","from_cart":false,"items":[{"sku_id":"<SKU_ID>","quantity":1}],"coins_to_use":226000,"payment_method":"VIETQR","shipping_choices":[{"shop_id":"<SHOP_ID>","carrier_code":"mock_express","service_code":"standard"}]}'
     ```
- **Mong đợi** (`UC-B15` hậu điều kiện): Đơn lưu rõ bên chịu chi phí (`platform_cost_vnd` vs `shop_cost_vnd`); giảm giá do sàn tài trợ (voucher sàn, freeship sàn, xu) do sàn bù vào sổ cái/escrow, người bán phải nhận phần doanh thu sau voucher shop (`180.000₫` trước hoa hồng ở trường hợp 1; `200.000₫` trước hoa hồng ở đơn 0₫).
- **Thực tế**:
  - Trường hợp 1 (đơn `b242a315-2dc0-4fcd-a534-8d654dded350`): `payment_db.payment_shop_allocations` ghi `subtotal_vnd = 115000` (`total_payable_vnd - shipping`), tức người bán gánh luôn `15.000₫` voucher sàn + `50.000₫` xu! Bảng `orders` không có cột `platform_cost_vnd`/`shop_cost_vnd`.
  - Trường hợp 2 (đơn `70d739fe-1530-4e12-9e27-62677cc5a07c`): trả `201` với `payment_id: ""`, `payment_status: "SUCCEEDED"`, `total_payable_vnd: 0`. Sau khi giao và `confirm-received` (HTTP 200), `GET /api/v1/seller/balance` giữ nguyên `available_vnd: 818470, total_earned_vnd: 818470` (tăng **0₫**).
- **File:dòng**: `services/order/internal/service/checkout.go:283-288,341,1141-1155`, `services/order/migrations/0001_init.sql:29-59`.

### `PRM-B2` [P0] — Thưởng xu tính trên `subtotal_vnd` gốc trước giảm giá (kể cả đơn 0₫ trả bằng xu), cho phép cày xu
- **Tái hiện**: Hoàn tất đơn 0₫ `70d739fe-1530-4e12-9e27-62677cc5a07c` (`subtotal_vnd=200000`, `coins_discount_vnd=226000`, `total_payable_vnd=0`):
  ```bash
  curl -sS -X POST http://localhost:48080/api/v1/orders/<ORDER_ID>/confirm-received -H "Authorization: Bearer $TOKEN"
  curl -sS http://localhost:48080/api/v1/coins/balance -H "Authorization: Bearer $TOKEN"
  ```
- **Mong đợi** (`UC-B18` & nguyên tắc không hoàn tiền trên tiền ảo): Xu thưởng phải tính trên giá trị tiền mặt thực trả cho tiền hàng sau khi trừ flash sale, voucher và xu (với đơn trả 0₫ bằng xu, thưởng xu = 0).
- **Thực tế**: Đơn 0₫ trả bằng xu sau `confirm-received` vẫn cộng `+2.000 xu` (`balance: 6000 → 8000`, `coin_transactions.reason = order_completed_reward`); đơn `b242a315-...` thực trả tiền hàng `115.000₫` cũng nhận `+2.000 xu` (= 1% của `200.000₫` gốc).
- **File:dòng**: `services/order/internal/service/lifecycle.go:611`, `services/order/internal/service/service.go:205-214`.

### `PRM-B3` [P0] — Huỷ một phần đơn con trong checkout đa gian hàng (và đơn `RETURNED`) không hoàn voucher shop, suất flash sale và xu
- **Tái hiện**: Checkout COD 2 gian hàng (`Shop 1` áp voucher shop `-15.000₫`, tổng dùng `20.000 xu` phân bổ `9.487 xu` cho Shop 1 và `10.513 xu` cho Shop 2), rồi huỷ riêng đơn con Shop 1:
  ```bash
  curl -sS -X POST http://localhost:48080/api/v1/orders/<SHOP1_ORDER_ID>/cancel \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"reason":"AUDIT-prm partial cancel shop1"}'
  curl -sS http://localhost:48080/api/v1/coins/balance -H "Authorization: Bearer $TOKEN"
  curl -sS http://localhost:48080/api/v1/vouchers -H "Authorization: Bearer $TOKEN"
  ```
- **Mong đợi** (`UC-B15` ngoại lệ: *"Đơn bị huỷ: hoàn lại voucher và xu nếu còn hạn"*; `docs/spec.md:29`): Hoàn lại `9.487 xu`, 1 lượt voucher shop (và suất flash sale nếu có) của đơn con đã huỷ.
- **Thực tế** (group `0a3f7ee9-4110-4441-922c-5416a8797bf6`, huỷ `109ecb60-...` → `200`): số dư xu giữ nguyên `232.000` (mất trắng `9.487 xu`), `AUDIT-PRM-SH2-5A2FF3` vẫn `used_count: 1`, `promotion_db.promotion_reservations` giữ nguyên `RESERVED`. Ngoài ra nhánh `ORDER_STATUS_RETURNED` không gọi `ReleasePromotion` nên đơn trả hàng cũng không hoàn xu.
- **File:dòng**: `services/order/internal/service/lifecycle.go:283-298` (chỉ gọi `compensateReleasePromotion` khi `!hasActiveSibling`), `services/order/internal/service/handlers.go:223-249`.

### `PRM-B4` [P1] — Thiếu kiểm tra phân quyền ở tầng gRPC `PromotionService` khi thiếu JWT claims
- **Tái hiện**: Soát mã nguồn `CreditCoins` (`promotion.go:555-560`): chỉ kiểm `if !isAdmin(claims)` nằm bên trong `if claims, ok := auth.ClaimsFromContext(ctx); ok`. Khi một client nội bộ gọi gRPC `PromotionService.CreditCoins` không đính kèm JWT metadata (`ok == false`), hàm bỏ qua hoàn toàn kiểm tra quyền và cộng xu cho `req.UserId`. Tương tự `SaveVoucher`, `GetCoinBalance`, `PreviewDiscounts` gọi `resolveUserID` (`promotion.go:1387-1400`) rơi về `reqUserID` khi không có claims. *(Lưu ý: tầng REST đã có gateway chặn — đã kiểm sống `POST /api/v1/admin/vouchers` bằng token seller trả `403`, `POST /api/v1/seller/vouchers` bằng token buyer trả `403`.)*
- **Mong đợi** (`AGENTS.md`: *"RBAC ở gateway và service"*; `docs/contracts.md` §3): Service bắt buộc từ chối `Unauthenticated`/`PermissionDenied` nếu thiếu claims hợp lệ cho RPC người dùng/admin; RPC nội bộ phải đăng ký trong `internalRPCs`.
- **Thực tế**: Bỏ qua kiểm tra khi `ok == false` ở tầng service.
- **File:dòng**: `services/promotion/internal/service/promotion.go:555-560,1387-1400`, `pkg/common/auth/auth.go:36-45`.

### `PRM-B5` [P1] — Đơn COD phát email & thông báo in-app sai `"Thanh toán ... qua COD thành công"` ngay lúc vừa đặt đơn
- **Tái hiện**: Đặt bất kỳ đơn COD nào (`POST /api/v1/checkout` với `"payment_method":"COD"`), sau 2 giây gọi:
  ```bash
  curl -sS http://localhost:48080/api/v1/notifications -H "Authorization: Bearer $TOKEN"
  curl -sS "http://localhost:48025/api/v1/search?query=to:<BUYER_EMAIL>"
  ```
- **Mong đợi** (`UC-PAY02` bước 3–5): Đơn COD chuyển `CONFIRMED` chờ thu hộ khi giao; KHÔNG được báo "Thanh toán thành công" trước khi shipper thu tiền.
- **Thực tế** (xác nhận trên 3 đơn COD của `b1` & `b2`, VD Mailpit `kQUVKXQMxsVsJK2N4onQyZ`, `ikPCF2RnSNvANu2haj6PN6`): Ngay sau khi tạo đơn, người mua nhận đồng thời 3 email: `Order Created`, `Order Confirmed`, và **`NexusMart - Payment Succeeded (428000 VND)`** với nội dung `"Thanh toán 428000 VND qua COD thành công (mã giao dịch: NMB5EFB0A3F3)."`!
- **File:dòng**: `services/payment/src/service.rs:391` (phát `payment.succeeded` ngay khi `CreatePayment` cho COD), `services/notification/internal/service/notification.go:595-612`.

### `PRM-B6` [P2] — Link trong thông báo in-app trỏ tới trang 404 (`/verify-otp`, `/seller/dashboard`, `/orders/<checkout_group_id>`)
- **Tái hiện**:
  ```bash
  # 1. Link từ user.registered -> /verify-otp (404)
  curl -sS -o /dev/null -w "%{http_code}\n" http://localhost:43000/verify-otp
  # 2. Link từ payment.succeeded / payment.failed -> /orders/<checkout_group_id> -> GET /api/v1/orders/<checkout_group_id> (404)
  curl -sS -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer $TOKEN" \
    http://localhost:48080/api/v1/orders/38141b76-e064-442a-82c9-bb644197aa46
  ```
- **Mong đợi** (`UC-B23` bước 2): Bấm thông báo phải mở đúng trang đích (`/dang-ky`, `/seller`, `/don-hang/<order_id>` hoặc `/don-hang`).
- **Thực tế**:
  - `user.registered` gán `link: "/verify-otp"` → `http://localhost:43000/verify-otp` trả **HTTP 404**.
  - `user.shop_reviewed` gán `link: "/seller/dashboard"` → `web/src/app/(seller)/seller/` không có thư mục `dashboard/` (chỉ có `/seller/page.tsx`) nên sau khi đăng nhập seller trả 404.
  - `payment.succeeded` / `payment.failed` / `payment.expired` gán `link: "/orders/<checkout_group_id>"` (vì `checkout.go:1159` gửi `OrderId: checkoutGroupID`), web đổi sang `/don-hang/<checkout_group_id>` gọi `GET /api/v1/orders/<checkout_group_id>` trả **HTTP 404**.
- **File:dòng**: `services/notification/internal/service/notification.go:330,359,605,632,659`, `services/order/internal/service/checkout.go:1159`, `web/src/features/account/orderMeta.ts:334-346`.

### `PRM-B7` [P1] — Lộ mã bí mật OTP ngay trên tiêu đề email (`Subject`), email OTP 100% tiếng Anh, và toàn bộ tiêu đề email giao dịch bằng tiếng Anh + số tiền/enum thô
- **Tái hiện**: Đăng ký tài khoản qua `POST /api/v1/auth/register` rồi đọc email trong Mailpit (`GET http://localhost:48025/api/v1/message/<ID>`).
- **Mong đợi** (`AGENTS.md` che secret/PII; yêu cầu tiếng Việt rõ ràng): Không bao giờ đặt mã OTP trong `Subject` (dễ lộ trên lock-screen, thanh thông báo, log mail server); toàn bộ tiêu đề & nội dung email bằng tiếng Việt, định dạng tiền có dấu phân cách (VD `141.000₫`), không để enum thô.
- **Thực tế** (Mailpit `CWKJ5T7pz3UPWRBXoKYDy4`, `kHmG2wYP...`, `KpQuWMHH...`):
  - Email OTP: `Subject: "NexusMart Verification OTP: 108314"`, body `"Hello Audit Prm One, Your NexusMart verification OTP code is: 108314..."` (100% tiếng Anh, lộ OTP ở Subject).
  - Tất cả 10 mẫu email giao dịch khác có Subject tiếng Anh (`"NexusMart - Order Created ..."`, `"NexusMart - Payment Succeeded (141000 VND)"`, `"NexusMart - Shop review update (...): APPROVE"`), số tiền dính liền (`141000 VND`), chuỗi thô `mock_express`, `APPROVE`, `delivering`, `delivered`.
- **File:dòng**: `services/notification/internal/mailer/mailer.go:86-105`, `services/notification/internal/service/notification.go:362-698`.

### `PRM-B8` [P2] — Lệch kiểm tra `budget_vnd` & `min_order_vnd` giữa `GET /vouchers` với `Preview/Reserve`, và `GET /vouchers` công khai trả cả voucher hết hạn/chưa bắt đầu
- **Tái hiện**:
  1. Tạo voucher `AUDIT-PRM-PLT-5A2FF3` (`10%`, `max_discount=15000`, `budget_vnd=20000`). Sau 1 đơn dùng `15.000₫` (ngân sách còn `5.000₫ < 15.000₫`), gọi bằng buyer thứ 2:
     ```bash
     curl -sS "http://localhost:48080/api/v1/vouchers?order_amount_vnd=200000" -H "Authorization: Bearer $TOKEN"
     curl -sS -X POST http://localhost:48080/api/v1/promotions/preview \
       -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
       -d '{"shop_items":[{"shop_id":"<SHOP_ID>","subtotal_vnd":200000,"shipping_fee_vnd":25000,"items":[{"sku_id":"<SKU_ID>","quantity":1,"unit_price_vnd":200000}]}],"platform_voucher_id":"<PLAT_V_ID>"}'
     ```
- **Mong đợi** (`UC-A07` ngoại lệ: *"Ngân sách voucher cạn: voucher tự dừng"*; `UC-B15` bước 1): Voucher không đủ ngân sách cho mức giảm phải báo `eligible: false`; danh sách voucher người mua mặc định không nên lẫn voucher đã hết hạn lâu/chưa phát hành.
- **Thực tế**:
  - `GET /vouchers?order_amount_vnd=200000` trả `used_budget_vnd: 15000, eligible: true, ineligible_reason: ""`, nhưng `POST /promotions/preview` trả `discount: 0, warnings: ["voucher AUDIT-PRM-PLT-5A2FF3 budget exhausted"]` (vì `promotion.go:1250` chỉ kiểm `remBudget <= 0`).
  - `GET /vouchers` trả cả `AUDIT-PRM-EXP-5A2FF3` (hết hạn) và `AUDIT-PRM-UPC-5A2FF3` (chưa bắt đầu) vì `store.go:168-178` không lọc `is_active`/thời gian.
  - Ngoài ra `GET /vouchers` kiểm `min_order_vnd` trên `order_amount_vnd` trước giảm, còn `calculator.go:229,261` kiểm voucher sàn sau voucher shop và freeship sau voucher sàn; `CheckoutClient.tsx:1132-1135` chỉ kiểm `subtotalVnd >= min_order_vnd && remaining_quota > 0 && is_active`, bỏ qua `per_user_limit`, khung giờ và `budget_vnd`.
- **File:dòng**: `services/promotion/internal/service/promotion.go:1235-1259`, `services/promotion/internal/store/store.go:168-178`, `services/promotion/internal/calculator/calculator.go:229,261`, `web/src/app/(shop)/thanh-toan/CheckoutClient.tsx:1132-1135`.

### `PRM-B9` [P2] — Thiếu API/UI bật-tắt, sửa, xoá voucher (`UC-S05`, `UC-A07`) và thiếu phạm vi danh mục `category_id` cho voucher (`UC-A07`)
- **Tái hiện**:
  ```bash
  curl -sS -X PATCH http://localhost:48080/api/v1/seller/vouchers/<VOUCHER_ID> \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{"is_active":false}'
  curl -sS -X DELETE http://localhost:48080/api/v1/seller/vouchers/<VOUCHER_ID> \
    -H "Authorization: Bearer $TOKEN"
  ```
- **Mong đợi** (`UC-S05`, `UC-A07`): Người bán/admin có thể tạm dừng/kích hoạt/xoá voucher đã tạo; voucher toàn sàn hỗ trợ cấu hình danh mục áp dụng (`UC-A07` bước 1).
- **Thực tế**: Cả `PATCH` và `DELETE` trả `404 route not found`; `proto/nexusmart/promotion/v1/promotion.proto` và bảng `vouchers` (`001_init.sql:5-27`) không có RPC cập nhật/xoá và không có cột danh mục.
- **File:dòng**: `proto/nexusmart/promotion/v1/promotion.proto`, `services/promotion/internal/store/migrations/001_init.sql:5-27`, `services/gateway/internal/router/router.go:227-228,263`.

### `PRM-B10` [P2] — Đăng ký Flash Sale không kiểm giá (< giá gốc / 30 ngày), không kiểm tồn kho SKU, và không khoá tồn kho khi duyệt (`UC-S08`)
- **Tái hiện** (SKU `c90fbe0a-...` có giá gốc `200.000₫`, tồn kho `10`):
  ```bash
  curl -sS -X POST http://localhost:48080/api/v1/seller/flash-sales/<FLASH_SALE_ID>/items \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"sku_id":"c90fbe0a-713a-4e79-a0b5-71f9bef2ed64","flash_price_vnd":250000,"total_slots":99,"per_user_limit":2}'
  ```
- **Mong đợi** (`UC-S08` bước 3, 5 & ngoại lệ): Từ chối nếu `flash_price_vnd >= giá gốc` (hoặc >= giá thấp nhất 30 ngày) hoặc `total_slots > available_stock`; khi Admin duyệt phải khoá số suất từ tồn kho `product` cho tới hết khung giờ.
- **Thực tế**: Backend trả **HTTP 201** (`flash_price_vnd: 250000, total_slots: 99, status: "PENDING"`)! Sau khi Admin duyệt item hợp lệ `a9170299-...` (`total_slots=2`), kiểm `GET /api/v1/products/de5888f9-...` thấy `reserved_stock: 0` (không khoá tồn kho).
- **File:dòng**: `services/gateway/internal/router/handlers_promotion.go:527-562`, `services/promotion/internal/service/promotion.go:388-450`.

### `PRM-B11` [P2] — Admin và Seller không có API liệt kê item Flash Sale `PENDING`/`REJECTED`; Admin phải nhập tay UUID `item_id` (`UC-A07`, `UC-S08`)
- **Tái hiện**: Sau khi seller đăng ký item `a9170299-...` (`PENDING`), gọi bằng token **Admin**:
  ```bash
  curl -sS "http://localhost:48080/api/v1/flash-sales?include_upcoming=true" -H "Authorization: Bearer $TOKEN"
  ```
- **Mong đợi** (`UC-A07` bước 4): Admin thấy hàng đợi item `PENDING` để xem thông tin và bấm duyệt/từ chối; seller thấy trạng thái chờ duyệt/từ chối sau khi tải lại trang.
- **Thực tế**: `GET /api/v1/flash-sales?include_upcoming=true` trả khung `796c0adc-...` với `items: []` (ẩn hoàn toàn item `PENDING` vì `store.go:513` hardcode `WHERE fsi.status = 'APPROVED'`). UI `/admin/thanh-vien` buộc Admin dán tay UUID `item_id`.
- **File:dòng**: `services/promotion/internal/store/store.go:513`, `web/src/features/admin/AdminPromoCoinsPanel.tsx:680-734`, `web/src/app/(seller)/seller/khuyen-mai/page.tsx:61-63`.

### `PRM-B12` [P2] — Giỏ hàng không áp giá Flash Sale, trang `/thanh-toan` nhận cả suất sắp mở (upcoming), và không có hạn thanh toán ngắn riêng (`UC-B16`)
- **Tái hiện**: Với SKU `7318ec3d-...` đang trong khung Flash Sale live (giá flash `120.000₫`, giá gốc `200.000₫`):
  ```bash
  curl -sS -X POST http://localhost:48080/api/v1/cart/items \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"sku_id":"7318ec3d-0df9-4f60-a5a0-c489e1387beb","quantity":1}'
  ```
- **Mong đợi** (`UC-B16`): Giá flash sale áp dụng nhất quán khi mua (hoặc cảnh báo rõ nếu giỏ chỉ bán giá thường); không cho chọn suất sắp mở ở `/thanh-toan`; đơn flash sale có hạn thanh toán ngắn (và kiểm soát COD).
- **Thực tế**: `POST /api/v1/cart/items` trả `price_vnd: 200000` (mua qua `/gio-hang` mất giá flash 120k); `thanh-toan/page.tsx:121-165` đọc `/flash-sales?include_upcoming=true` không kiểm `starts_at <= now < ends_at` nên vẫn hiện giá flash cho suất sắp mở rồi mới lỗi `409 outside active window` khi bấm đặt; `checkout.go:290` dùng chung 15 phút và cho đặt COD với Flash Sale.
- **File:dòng**: `services/cart/internal/server/server.go`, `web/src/app/(shop)/thanh-toan/page.tsx:121-165`, `services/order/internal/service/checkout.go:283-292`.

### `PRM-B13` [P2] — Thiếu hoàn toàn nghiệp vụ `UC-S18` (Tạo chương trình giảm giá trực tiếp của gian hàng)
- **Tái hiện**: Kiểm tra bằng `rg` trên toàn bộ `proto/`, `services/` và mở `/seller/khuyen-mai` tab Combo/Chiến dịch.
- **Mong đợi** (`UC-S18`): Người bán tạo giảm giá trực tiếp theo sản phẩm/SKU có hẹn giờ, giới hạn mỗi người, ưu tiên flash sale nếu trùng khung, và cảnh báo tăng giá gốc ngay trước khi giảm.
- **Thực tế**: Không tồn tại schema, proto, service hay REST endpoint nào; UI tab Combo trong `/seller/khuyen-mai` là mock lưu bộ nhớ cục bộ (`sellerStore.ts`).
- **File:dòng**: `proto/nexusmart/promotion/v1/promotion.proto`, `services/gateway/internal/router/router.go:226-231`, `web/src/features/seller/PromotionsBundleTab.tsx:1-80`.

### `PRM-B14` [P2] — Thiếu thông báo đơn hàng cho người bán, thiếu email `shipment.updated`, thiếu kênh Push & nhóm `PROMOTION`, và nguy cơ nuốt sự kiện `shipment.updated`
- **Tái hiện**: Sau khi đặt, xác nhận, giao, hoàn tất và huỷ nhiều đơn của shop `Xưởng Dệt Bạch Đằng`, gọi bằng token `seller.bachdang@nexusmart.local`:
  ```bash
  curl -sS http://localhost:48080/api/v1/notifications -H "Authorization: Bearer $SELLER_TOKEN"
  ```
- **Mong đợi** (`UC-S04`, `UC-S10`, `UC-B22`, `UC-B23`, `UC-SHIP04`): Người bán nhận thông báo khi có đơn mới/huỷ/trả hàng; `PROMOTION` gửi nhắc voucher sắp hết hạn; `shipment.updated` không bị nuốt khi đến trước `order.created`.
- **Thực tế**:
  - `GET /notifications` của seller chỉ có 2 thông báo cũ (`Chào mừng đến với NexusMart`, `Kết quả duyệt gian hàng`) — **0 thông báo đơn hàng**.
  - `shipment.updated` (`notification.go:718-729`) đặt `email: nil` (không gửi email) và tại `notification.go:195-197` nếu chưa tìm thấy `userID` trong `order_participants`, hàm trả `nil` rồi consumer vẫn ghi `processed_events` → mất thông báo vĩnh viễn.
  - `order.return_requested` không phát event NATS; nhóm `PROMOTION` không có publisher; các cờ `*_push` trong `notification_preferences` không có tầng gửi push; `ResendOTP` (`user/server.go:292`) tái phát `user.registered` tạo trùng in-app chào mừng.
- **File:dòng**: `services/notification/internal/service/notification.go:195-197,382-594,718-729`, `services/user/internal/service/server.go:292`.

### `PRM-B15` [P3] — Thiếu biểu tượng thông báo trên Header, lệch UI cài đặt thông báo và mâu thuẫn chính sách Xu/Flash trên Web
- **Tái hiện**: Mở `/tai-khoan?tab=cai-dat`, `/thanh-toan`, `/san-pham/ao-thun-cotton-compact-trang`, `/` trên `http://localhost:43000`.
- **Mong đợi**: Có lối vào trung tâm thông báo ở Header; cài đặt thông báo khoá/chú thích rõ `order_in_app` & `system_in_app` không cho tắt; checkout cho nhập số xu tuỳ chọn và đồng bộ rule Xu/Flash với backend.
- **Thực tế**:
  - `ShopHeader.tsx` không có chuông thông báo.
  - `AccountClient.tsx:83-94,1582-1688` đặt mặc định `promotion_email/push=false, system_push=false` (backend trả `true`), cho bỏ tick `order_in_app`/`system_in_app` rồi bị backend âm thầm ép lại `true`.
  - `CheckoutClient.tsx` chỉ có checkbox dùng toàn bộ `coinBalance` (không cho nhập số xu lẻ).
  - `ProductDetailClient.tsx:307-310` tính xu ước tính theo `membershipTier.cashbackRate` mock thay vì 1% của backend.
  - `HomeClient.tsx:592-593` ghi cứng `"Tối đa 3 món ưu đãi mỗi đơn"` dù `per_user_limit` thật là `1` hoặc `2`.
- **File:dòng**: `web/src/components/ShopHeader.tsx`, `web/src/features/account/AccountClient.tsx:83-94,1582-1688`, `web/src/app/(shop)/thanh-toan/CheckoutClient.tsx`, `web/src/app/(shop)/san-pham/[slug]/ProductDetailClient.tsx:307-310`, `web/src/app/(shop)/HomeClient.tsx:592-593`.

---

## 4. Dữ liệu

### `PRM-D1` — 16/16 item Flash Sale seed có `total_slots` vượt xa tồn kho thực tế của SKU (`product_db.skus.stock`)
- **Bằng chứng DB (`promotion_db` vs `product_db`)**:
  - `BD-AT01-XANHTHAN-S`: `total_slots=120` vs `stock=10` (gấp 12x)
  - `BD-AT03-XANHTHAN-2XL`: `total_slots=90` vs `stock=5` (gấp 18x)
  - `HN-JT12-DEN-2XL`: `total_slots=70` vs `stock=4` (gấp 17.5x)
  - `HN-JK13-DEN-2XL`: `total_slots=50` vs `stock=4` (gấp 12.5x)
  - `DN-SL11-XAMDA-S`: `total_slots=70` vs `stock=10` (gấp 7x); `SG-SD09-XAMDA-L`: `90` vs `15` (6x); ngay cả SKU tồn cao nhất `HN-TT16-COMBO-5MAU-M` cũng chỉ có `stock=42` vs `total_slots=160` (3.8x).
- **Nguyên nhân**: `tools/seed/seed.mjs` sao chép `totalQuota` cấp sản phẩm từ `web/src/data/flash-sale.ts` gán lên 1 SKU duy nhất mà không cắt theo `sku.stock`, đồng thời backend thiếu kiểm tra tồn kho (`PRM-B10`).

### `PRM-D2` — 3 voucher seed có mô tả hạn chế đối tượng (`EDU15`, `SINHNHAT50`, `NEXUSVIP12`) và `NEXUS10` (`excludeOutlet`) không có kiểm tra điều kiện trên backend
- **Bằng chứng sống**: Tài khoản mới tạo `audit-prm-1-5a2ff3@nexusmart.local` (không có email `.edu`, chưa có đơn/hạng thành viên) gọi `POST /api/v1/promotions/preview` đơn `600.000₫`:
  - `EDU15` → giảm `50.000₫`, `warnings: []`
  - `SINHNHAT50` → giảm `50.000₫`, `warnings: []`
  - `NEXUSVIP12` → giảm `72.000₫`, `warnings: []`
- **Nguyên nhân**: `promotion_db.vouchers` chỉ có `scope='PLATFORM'` (không hỗ trợ điều kiện email `.edu`, hạng thành viên — vốn là *Ngoài phạm vi* theo `docs/spec.md:29` — hay loại trừ hàng outlet), nhưng `seed.mjs` vẫn seed cả 3 mã này ra toàn sàn.

### `PRM-D3` — Lệch siêu dữ liệu giữa `web/src/data/{vouchers,flash-sale}.ts` với `promotion_db` / API
- **Vouchers**: `vouchers.ts` ghi `expiresAt: 2026-11-30`, còn DB là `2026-12-22` (và `2026-12-31` cho 3 mã đặc biệt) do `seed.mjs` gia hạn động +30 ngày; `remainingUses` trong file tĩnh khác DB (`used_count=0`, `per_user_limit=5`, `budget_vnd=0`).
- **Flash sales**: `flash-sale.ts` có 4 khung cố định (`09:00` đã kết thúc, `12:00`, `18:00`, `21:00`, 20 item); DB có 3 khung động (`00:00–13:00`, `13:00–19:00`, `19:00–01:00`, 8+4+4 = 16 item, bỏ khung `09:00`).

### `PRM-D4` — Trang `/flash-sale` trộn dữ liệu thật với Deal sốc & Combo mock dẫn tới sai giá khi thêm vào giỏ
- `web/src/app/(shop)/flash-sale/FlashSaleClient.tsx:115-117,189-263` hiển thị thêm khối `Deal sốc` và `Combo tiết kiệm` từ mock nhưng bấm nút mua lại gọi `addToCart` với SKU thật → giỏ hàng tính nguyên giá niêm yết gốc thay vì giá Deal sốc/Combo quảng cáo trên trang.

### `PRM-D5` — Trang `/hoi-vien` và PDP hiển thị số dư & quy tắc Xu mock trái ngược backend
- `web/src/app/(shop)/hoi-vien/page.tsx:39` gán cứng `const coins = INITIAL_USER_COINS` (`42.000 Xu`, không gọi `GET /api/v1/coins/balance`), nên mọi tài khoản (kể cả buyer mới 0 xu) đều thấy `42.000 Xu` cùng lịch sử Xu giả.
- Văn bản trên `/hoi-vien` và `vouchers.ts:110` nêu quy tắc hoàn `1–6%`, nhân đôi Thứ Sáu, trừ tối đa `20%` tiền hàng sau voucher, hạn dùng 12 tháng — mâu thuẫn hoàn toàn với backend (`1%` cố định trên `subtotal_vnd`, trừ tới `100%` kể cả ship, không có hạn dùng).

---

## 5. Đề xuất sửa

| Mã | Giải quyết | Việc cụ thể (file, RPC, migration, route) | Cỡ | Proto | Migration | ADR |
|---|---|---|---|---|---|---|
| `PRM-F1` | `PRM-B1`, `UC-B15` | Lưu `platform_cost_vnd`, `shop_cost_vnd` trong `orders` (migration mới `services/order/migrations/0002_*.sql`); sửa `buildCreatePaymentRequest` (`services/order/internal/service/checkout.go:1141-1165`) tính `ShopAllocation.SubtotalVnd = subtotal - flash_discount - shop_voucher_discount` và truyền `platform_subsidy_vnd` sang `Payment` (tạo bút toán ghi kép tài khoản chi phí khuyến mãi sàn, kể cả khi `grandTotalPayableVND == 0` vẫn tạo bản ghi thanh toán/bù trừ để seller nhận đủ tiền). | L | Có (`payment.proto`) | Có (`order_db`, `payment_db`) | Không |
| `PRM-F2` | `PRM-B2`, `UC-B18` | Sửa `services/order/internal/service/lifecycle.go:611`: tính thưởng xu trên tiền hàng thực trả sau mọi giảm giá (`max(0, total_payable_vnd - max(0, shipping_fee_vnd - freeship_discount_vnd))`), trả `0` khi đơn thanh toán `0₫`. | S | Không | Không | Không |
| `PRM-F3` | `PRM-B3`, `UC-B15` | Mở rộng `PromotionService.ReleasePromotion` hỗ trợ hoàn theo từng `sub_order_id` (hoặc `shop_id` + số xu tương ứng của đơn con), sửa `release_atomic.lua` lưu khóa theo sub-order; cập nhật `services/order/internal/service/lifecycle.go:283-298` và `handlers.go:223-249` gọi hoàn khuyến mãi/xu cho đơn con bị huỷ hoặc `RETURNED`. | M | Có (`promotion.proto`) | Có (`promotion_db`) | Không |
| `PRM-F4` | `PRM-B4` | Sửa `services/promotion/internal/service/promotion.go:555-560,1387-1400`: bắt buộc có `claims` hợp lệ cho `CreditCoins` (yêu cầu role `admin`/`super_admin`), `SaveVoucher`, `GetCoinBalance`, `PreviewDiscounts`; đăng ký rõ RPC nội bộ trong `pkg/common/auth/auth.go`. Thêm unit test gọi gRPC không có token phải trả `Unauthenticated`. | S | Không | Không | Không |
| `PRM-F5` | `PRM-B5`, `UC-B06`, `UC-B23` | Trong `services/payment/src/service.rs:391` không phát `payment.succeeded` ngay khi tạo đơn COD (hoặc phân biệt sự kiện `payment.cod_pending`), chỉ phát `payment.succeeded` khi đối soát/thu hộ COD xong; đồng thời `notification.go:595-612` bỏ qua hoặc đổi thông điệp nếu `method == COD` chưa thu tiền. | S | Không | Không | Không |
| `PRM-F6` | `PRM-B6`, `PRM-B7`, `UC-B01`, `UC-B23` | Sửa `mailer.go:86-105` & `notification.go:312-736`: bỏ OTP khỏi `Subject` email, Việt hoá 100% tiêu đề & nội dung, format tiền có dấu chấm (`141.000₫`), dịch enum (`APPROVE`, `delivering`, `mock_express`). Sửa link `/verify-otp` → `/dang-ky`, `/seller/dashboard` → `/seller`; với `payment.*` lưu kèm danh sách `order_ids` hoặc trỏ `/orders` (`/don-hang`) khi đa gian hàng. | S | Không | Không | Không |
| `PRM-F7` | `PRM-B8`, `PRM-B9`, `UC-S05`, `UC-A07` | Thêm RPC `UpdateVoucher` (bật/tắt `is_active`, sửa hạn/quota) & `DeleteVoucher` + REST `PATCH/DELETE /api/v1/seller/vouchers/{id}` và `/api/v1/admin/vouchers/{id}`; thêm cột `category_ids` vào `vouchers`; lọc mặc định voucher đang chạy trên `GET /api/v1/vouchers` (giữ `include_inactive` cho seller/admin); sửa `voucherToProto` (`promotion.go:1250`) so `remBudget` với mức giảm dự kiến; sửa lọc UI `CheckoutClient.tsx:1132-1135`. | M | Có (`promotion.proto`) | Có (`promotion_db`) | Không |
| `PRM-F8` | `PRM-B10`, `PRM-B11`, `PRM-D1`, `UC-S08`, `UC-A07` | Khi `RegisterFlashSaleItem`: kiểm `flash_price_vnd < sku.price_vnd` và `total_slots <= sku.available_stock` (thêm gRPC kiểm/khoá tồn kho ở `product` khi duyệt). Thêm `ListFlashSaleItems` (lọc `status=PENDING|APPROVED|REJECTED`, `shop_id`) + REST `GET /api/v1/admin/flash-sale-items` và `GET /api/v1/seller/flash-sale-items` để Admin duyệt trực tiếp không cần dán UUID. Sửa `tools/seed/seed.mjs` cắt `total_slots <= sku.stock`. | M | Có (`promotion.proto`, `product.proto`) | Không | Không |
| `PRM-F9` | `PRM-B12`, `PRM-D4`, `UC-B16` | `CartService` tra giá Flash Sale live (hoặc gắn cờ & truyền `flash_sale_item_id` từ `/gio-hang` sang `/thanh-toan`); `thanh-toan/page.tsx:121-165` chỉ gắn `flashPriceVnd` khi `starts_at <= now < ends_at`; cấu hình hạn thanh toán ngắn riêng cho đơn có flash sale (VD 5 phút) và cấu hình chặn COD nếu cần; gỡ hoặc tách rõ khối Deal sốc/Combo mock trên `/flash-sale`. | M | Không | Không | Không |
| `PRM-F10` | `PRM-B13`, `UC-S18` | Bổ sung tính năng chương trình giảm giá trực tiếp của gian hàng (`shop_discount_campaigns` & `shop_discount_items` trong `promotion_db`), RPC CRUD + áp giá tự động (ưu tiên Flash Sale khi trùng SKU), REST `/api/v1/seller/discount-campaigns`, tích hợp hiển thị giá gạch & thay tab mock trên `/seller/khuyen-mai`. | L | Có (`promotion.proto`) | Có (`promotion_db`) | Không |
| `PRM-F11` | `PRM-B14`, `PRM-B15`, `UC-B23`, `UC-B22` | Bổ sung thông báo đơn hàng (`order.created`, `order.cancelled`, `order.return_requested`) cho chủ gian hàng (`shop_owner_id`); với `shipment.updated` nếu chưa có `order_participants` thì trả lỗi để JetStream retry thay vì nuốt sự kiện, và bổ sung gửi email khi `delivered`/`failed`; tách `ResendOTP` khỏi `user.registered` (hoặc không tạo lại in-app chào mừng); thêm job nhắc voucher sắp hết hạn (`PROMOTION`); thêm chuông thông báo trên `ShopHeader.tsx` và khoá checkbox `order_in_app`/`system_in_app` trên `AccountClient.tsx`. *(Lưu ý: triển khai kênh Push FCM/APNs/WebPush là năng lực hạ tầng mới → cần chạy skill `evaluate-oss` và ghi ADR trước khi code theo `AGENTS.md` luật 11.)* | M | Không | Không | **Có** *(nếu làm Push)* |
| `PRM-F12` | `PRM-D2`, `PRM-D3`, `PRM-D5` | Dọn 3 voucher mock hạng thành viên/sinh viên (`EDU15`, `SINHNHAT50`, `NEXUSVIP12`) khỏi seed hoặc thêm cờ điều kiện thật (`allowed_email_domain`, loại trừ outlet); nối `/hoi-vien` với `GET /api/v1/coins/balance` (gỡ cứng `INITIAL_USER_COINS`), đồng bộ copy quy tắc Xu (1%, hạn dùng) và `per_user_limit` thật trên `HomeClient.tsx` & `ProductDetailClient.tsx`. | S | Không | Không | Không |

---

## 6. Đã kiểm gì, chưa kiểm gì

### Đã kiểm
- **Mã nguồn HEAD `0db2cdd`**: Toàn bộ `proto/nexusmart/{promotion,notification}/v1/`, `services/promotion/`, `services/notification/`, `services/gateway/internal/router/{router,handlers_promotion,handlers_notification}.go`, phần tích hợp trong `services/order/`, `services/cart/`, `services/user/`, `services/payment/src/service.rs`, `tools/seed/seed.mjs`, `web/src/data/{vouchers,flash-sale}.ts`, và các trang web `/flash-sale`, `/gio-hang`, `/thanh-toan`, `/tai-khoan`, `/hoi-vien`, `/gian-hang/[slug]`, `/san-pham/[slug]`, `/seller/khuyen-mai`, `/admin/thanh-vien`, `/admin/cau-hinh`, `ShopHeader.tsx`.
- **Unit & concurrency tests trên host**:
  - `cd services/promotion && go test -v ./...` → **PASS** (gồm `TestFlashSale_1000Concurrent_100Slots_ExactNoOversell` và `TestService_1000Concurrent_FlashSale_100Slots_AndRelease`).
  - `cd services/notification && go test -v ./...` → **PASS** (consumer, grpcserver, mailer, service).
- **Kiểm chứng trực tiếp trên stack `nexusmart-audit`** (script `run_id=5A2FF3` + truy vấn chỉ đọc Postgres/Redis/NATS/Mailpit):
  - Buyer demo `minhquan.nguyen@nexusmart.vn` (chỉ đọc): `42.000 xu`, 11 voucher seed, 3 khung flash sale (16 item).
  - Toàn bộ luồng: đăng ký + OTP Mailpit, tạo/lưu/preview/checkout kết hợp 3 loại voucher + xu, RBAC 403, tạo/duyệt/mua/vượt hạn mức/hết suất/ngoài khung giờ/huỷ hoàn suất flash sale, saga bù trừ khi thanh toán VietQR `FAILED` (hoàn voucher + ngân sách + xu), chu trình đầy đủ VietQR → giao hàng → `confirm-received` → cộng xu thưởng + hoàn tiền một phần, đơn 0₫ trả bằng xu, huỷ một phần đa gian hàng, tắt `order_email` chặn gửi mail thật, `EnforcePreferenceGuard`, đối chiếu `promotion_db`, `notification_db`, `payment_db.payment_shop_allocations`, `product_db.skus`, Redis `promo:*`, NATS `NEXUSMART`/`NEXUSMART_DLQ` (`0` DLQ), log `promotion`/`notification` (không lộ OTP/email thô trong log).
  - Kiểm tra HTTP các route web `http://localhost:43000`: `/verify-otp` (`404`), `/seller/dashboard` (redirect `/dang-nhap`, không tồn tại page route), `/flash-sale`, `/hoi-vien`, `/gian-hang/xuong-det-bach-dang`, `/san-pham/ao-thun-cotton-compact-trang` (`200`).

### Chưa kiểm (kèm lý do)
- **Kiểm tải 1.000 yêu cầu đồng thời qua Gateway**: Không chạy theo luật chung (rate limit 300 req/phút dùng chung 8 worker); đã xác minh tính nguyên tử bằng đọc `reserve_atomic.lua`, `tests/e2e/06_flash_sale_test.go` và chạy `go test` concurrency trên host.
- **Tự huỷ đơn quá hạn thanh toán thực tế (`payment.expired` sau 15 phút)**: Không chờ 15 phút hay sửa cấu hình/DB trên stack dùng chung; đã soát code watchdog `services/order/internal/service/handlers.go:255` và handler `notification.go:650-669`.
- **Tương tác trình duyệt thực (Playwright click UI)**: Chỉ kiểm tra HTTP SSR trên `http://localhost:43000` và đọc toàn bộ mã nguồn React/TS; kiểm trình duyệt thuộc phạm vi worker `web`.
- **Gọi gRPC trực tiếp không JWT tới `PromotionService`**: Cổng gRPC không publish ra host; lỗ hổng `PRM-B4` xác nhận bằng soát mã nguồn `promotion.go:555-560,1387-1400` và `pkg/common/auth/auth.go:36-45`.
- **Tạo shop mới để kích hoạt lại `user.shop_reviewed`**: Đã xác minh trực tiếp qua 9 bản ghi `processed_events`, email Mailpit có sẵn và `GET /api/v1/notifications` của `seller.bachdang@nexusmart.local`.
