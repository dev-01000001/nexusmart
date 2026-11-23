# Rà soát nghiệp vụ: Vận chuyển (`shp`)

- **Phạm vi**: `UC-SHIP01`..`UC-SHIP08`, `UC-A12` (`docs/spec.md`), đối chiếu mục Giao hàng của Shopify/sàn, dữ liệu đối soát COD phía `shipping` (phối hợp `pay`).
- **Môi trường kiểm**: code `HEAD` (`0db2cdd`), stack `nexusmart-audit` (`gateway :48080`, `web :43000`, `shipping :48087`, `GHN_MODE=mock`, `GHN_ENABLED=true`, `MOCK_CARRIER_ENABLED=true`).

---

## 1. Bảng UC

| UC | Ưu tiên | Trạng thái | Backend (file:dòng) | REST | Web | Bằng chứng | Ghi chú |
|---|---|---|---|---|---|---|---|
| **UC-SHIP01** (2 cấp 34 tỉnh + mapping cũ → mới) | Cao | **MỘT PHẦN** | `services/shipping/internal/address/data.go:165-363`, `store/store.go:218-365`, `grpcserver/server.go:33-82` | `GET /api/v1/shipping/provinces`, `GET /api/v1/shipping/provinces/{code}/wards`, `POST /api/v1/shipping/addresses/map-legacy` (`router.go:134-136`) | `AddressForm.tsx` gọi `/provinces` và `/wards`; **không gọi `map-legacy`** (`rg` 0 kết quả trong `web/src/`) | `GET /provinces` → `200` (34 tỉnh mới đúng tên); `POST /addresses/map-legacy` (`Bình Dương`→`79`, `Quảng Nam`→`48`, `Hà Giang`→`08`, `BR-VT`→`79`, `Hải Dương`→`31`) → `200 found:true` | Chỉ có 115 phường/xã mẫu (3–8/tỉnh); tìm `?query=Ha Noi` trả rỗng (`SHP-B8`); `user` `CreateAddress` (`services/user/internal/service/server.go:1110-1159`) không kiểm tra mã với `shipping`; `web/src/lib/provinces.ts` lệch mã/phường (`SHP-D3`, `SHP-D4`). |
| **UC-SHIP02** (Tính phí vận chuyển khi checkout) | Cao | **LỖI** | `services/shipping/internal/service/shipping.go:81-116`, `carrier/ghn.go:64-129`, `carrier/mock_express.go:28-61`, `carrier/resilience.go:119-222`, `carrier/fallback.go:25-127`, `cache/quote_cache.go:43-128`, `services/order/internal/service/checkout.go:727-741` | `POST /api/v1/shipping/calculate-fee` (`router.go:159`) | `/gio-hang` (`gio-hang/page.tsx:183-194`), `/thanh-toan` (`CheckoutClient.tsx:295-306`), PDP `/san-pham/[slug]` (`ProductDetailClient.tsx:271` dùng mock) | `POST /calculate-fee` với payload Web (`from_province_code=""`, `shop_id=Kho Basic SG`, `to="79"`, COD 200k) → `200` trả `ghn=40000₫`; nhưng `POST /checkout` đơn O1 tính thật `shipping_fee_vnd=24000₫` (lệch 16.000₫). `to_province_code="99999"` vẫn trả `200` kèm 4 báo giá | Interface `Carrier` chung + 2 hãng + timeout 5s/retry backoff/circuit breaker + fallback + Redis cache 300s hoạt động tốt ở code/unit test; nhưng Web báo giá sai so với checkout (`SHP-B1`), không chặn địa chỉ ngoài vùng (`SHP-B7`), PDP dùng công thức giả (`SHP-D5`). |
| **UC-SHIP03** (Tạo vận đơn và in nhãn) | Cao | **LỖI** | `services/shipping/internal/service/shipping.go:132-214`, `store/store.go:370-478`, `services/order/internal/service/lifecycle.go:405-533` | `POST /api/v1/seller/orders/{id}/ship` (`router.go:181`), `GET /api/v1/shipping/shipments/{order_id}` (`router.go:160`) | `/seller/don-hang` (`page.tsx:75-76,191-196`), `/seller/don-hang/[id]` (`page.tsx:57-58,705-710`), `/don-hang/[id]` (`OrderDetailClient.tsx:138,602-605`) | O1 ship → `200`, `tracking=GHN-59EF135197`, `label=https://sandbox.ghn.local/labels/GHN-59EF135197.pdf`. O2 đặt `mock_express/STANDARD` (`19500₫`), seller ship `ghn/EXPRESS` → `order.shipping_fee_vnd=19500`, `shipment.fee_vnd=36000` | Lệch tiền phí giữa `order_db` và `shipping_db` khi seller đổi hãng (`SHP-B2`); chuyển ngay đơn sang `SHIPPING` khi vận đơn mới `CREATED` (chặn `UC-SHIP07`, `SHP-B3`); nhãn mock là URL `.local` chết và UI seller chỉ in text (`SHP-B8`); thiếu outbox retry tạo vận đơn khi API hãng lỗi. |
| **UC-SHIP04** (Cập nhật trạng thái qua webhook + polling) | Cao | **MỘT PHẦN** | `services/shipping/internal/webhook/handler.go:85-188`, `service/shipping.go:282-341,377-416`, `store/store.go:602-710`, `events/outbox.go`, `poller/poller.go`, `services/order/internal/service/handlers.go:199-250`, `services/notification/internal/service/notification.go:700-729` | `POST /webhooks/shipping/{carrier}` (`router.go:138`), `GET /api/v1/shipping/shipments/{order_id}` (`router.go:160`) | `/don-hang/[id]` (`OrderDetailClient.tsx:138`), `/seller/don-hang/[id]` (`page.tsx:85`) | O1: sai chữ ký → `401`, thiếu/lệch `X-Timestamp` (-600s) → `401`, `delivering` → `200 applied`, gửi lại cùng `event_id` → `200 duplicate`, sự kiện cũ hơn (-300s) → `200 stale`, `delivered` → đơn đổi `DELIVERED` (không tự `COMPLETED`). RBAC `GET /shipments/{id}`: buyer chủ/seller chủ/admin `200`, buyer khác/seller khác `403`. Web BFF trả khớp `shipping_db` | Lỗi không đối chiếu `{carrier}` trên URL với `shipment.carrier_code` (`SHP-B6`); job polling dự phòng bỏ qua carrier mock (chỉ chạy sandbox) và `ListActiveShipments` cứng `limit=100` không phân trang; thông báo chứa status tiếng Anh thô & lặp thông báo khi `delivered` (`SHP-B8`). |
| **UC-SHIP05** (Giao thất bại và hoàn hàng) | Trung bình | **MỘT PHẦN** | `services/shipping/internal/store/store.go:602-680`, `services/order/internal/service/handlers.go:223-249`, `services/order/internal/store/postgres.go:883-906` | Qua `POST /webhooks/shipping/{carrier}` (`delivery_fail`, `returning`, `returned`) | Không có UI giao lại/đổi lịch nhận; `/don-hang/[id]` chỉ hiển thị trạng thái `RETURNED` | O2 COD: webhook `delivery_fail` → `returning` → `returned` chuyển đơn `SHIPPING -> RETURNED`, cộng lại tồn SKU (`31 -> 30` ròng sau O1+O2); nhưng `cod_rejected_count` vẫn `0`, seller nhận `0` thông báo, `payment_db` giữ `payments.status='succeeded'` | Thiếu ghi nhận bom hàng COD (`cod_rejected_count` không có lệnh tăng ở `services/user`), thiếu báo seller, thiếu thao tác người mua yêu cầu giao lại, thiếu phát event/đảo bút toán COD (`SHP-B5`). Nhánh hoàn tiền `VIETQR` đã có code (`handlers.go:232-248`). |
| **UC-SHIP06** (Cấu hình vận chuyển gian hàng) | Trung bình | **THIẾU** | Không có (`rg "shop_shipping" proto/ services/` = 0) | Không có (`router.go` không có `/seller/shipping/*`) | `/seller/ho-so` (`web/src/app/(seller)/seller/ho-so/page.tsx`) tab "Giao hàng & Đổi trả" chỉ là mock in-memory (`sellerStore.ts`) | Kiểm `proto/nexusmart/shipping/v1/shipping.proto`, `services/shipping/migrations/000001_init.sql`, `router.go`, `web/src/app/(seller)/seller/ho-so/page.tsx`: không có bảng DB, RPC, endpoint | Không bật/tắt được hãng theo gian hàng, không sửa được địa chỉ kho lấy hàng, không cấu hình cân nặng mặc định/chính sách freeship thật (`SHP-D6`). |
| **UC-SHIP07** (Huỷ vận đơn) | Trung bình | **LỖI** | `services/shipping/internal/service/shipping.go:217-239,344-374`, `grpcserver/server.go:161-186`, `events/consumer.go` | Không có REST endpoint trong `services/gateway/internal/router/router.go` (`rg "CancelShipment" services/gateway/` = 0) | Không có nút/luồng huỷ vận đơn trên `/seller/don-hang` | O2 vừa tạo vận đơn (`shipment.status=CREATED`, chưa lấy hàng) nhưng `order.status` đã bị đẩy sang `SHIPPING`; cả buyer và seller gọi `POST /api/v1/orders/{id}/cancel` đều bị từ chối `409 FAILED_PRECONDITION`. O3 huỷ trước khi ship → consumer `order.cancelled` nhận sạch (`processed_events=10`, `DLQ=0`) | `CancelShipment` RPC và consumer `order.cancelled` đã viết và qua unit test, nhưng **không bao giờ kích hoạt được cho đơn đã có vận đơn** do mâu thuẫn máy trạng thái đơn + thiếu REST route (`SHP-B3`). Thiếu thông báo seller không đóng gói và cảnh báo admin khi lỗi. |
| **UC-SHIP08** (Tạo vận đơn trả hàng) | Trung bình | **THIẾU** | Không có (`rg "CreateReturnShipment" proto/ services/` = 0); `000001_init.sql:38` ép `order_id UNIQUE` | Không có route tạo/xem vận đơn trả hàng trong `router.go` | Không có UI tạo/theo dõi vận đơn chiều ngược ở `/don-hang/[id]` và `/seller/don-hang/[id]` | O1 ở `DELIVERED` gọi `POST /api/v1/orders/{id}/return` → `200 RETURN_REQUESTED`; thử gửi webhook `returned` lên vận đơn cũ → `200 outcome:"stale"` do `delivered` là trạng thái terminal | Chưa hỗ trợ nhiều vận đơn/đơn (chiều đi + chiều ngược), chưa có luồng seller duyệt trả hàng (`UC-S10`), đơn `RETURN_REQUESTED` bị kẹt không thể tiến tới `RETURNED` qua shipping (`SHP-B4`). |
| **UC-A12** (Cấu hình hãng vận chuyển – Admin) | Trung bình | **THIẾU** | Chỉ đọc env lúc khởi động (`services/shipping/internal/config/config.go:58-63`, `cmd/server/main.go:107-119`); bảng phí dự phòng hardcode (`fallback.go:113-127`) | Không có (`rg "admin/shipping|carrier_config|test_connection" proto/ services/gateway/` = 0) | `/admin/cau-hinh` (`web/src/app/(admin)/admin/cau-hinh/page.tsx:520-631`) là mock 100% (`auditStore.ts`, hiển thị SPX/NexusFast giả) | Kiểm `proto/nexusmart/shipping/v1/shipping.proto`, `000001_init.sql`, `router.go`, `admin/cau-hinh/page.tsx`: không có bảng DB cấu hình hãng/token mã hoá, không có RPC/API bật tắt hãng, bảng phí dự phòng hay nút kiểm tra kết nối | Cần cấu hình runtime + lưu token mã hoá + kiểm tra kết nối + audit log theo đặc tả `docs/spec.md:55` (`SHP-D6`). |

---

## 2. Bảng đối chiếu Shopify / sàn

| Nghiệp vụ | UC hoặc ngoài phạm vi | Trạng thái | Ghi chú |
|---|---|---|---|
| **Vùng và biểu phí** | `UC-SHIP02`, `UC-SHIP06`, `UC-A12` (`docs/spec.md:55,137,141`) | **MỘT PHẦN** | Có phân vùng Bắc/Trung/Nam + nội tỉnh/nội miền/liên miền + phụ phí cân nặng/COD trong code mock và `FallbackTable` (`ghn.go:379-400`, `fallback.go:113-127`), nhưng chưa có cấu hình biểu phí/vùng runtime ở Admin (`UC-A12`) và Seller (`UC-SHIP06`). |
| **Phí tính theo hãng** | `UC-SHIP02` (`docs/spec.md:28,137`) | **LỖI** | Backend gọi song song `GHNAdapter` và `MockExpressAdapter` qua interface `Carrier` kèm cache Redis, nhưng Web gửi `from_province_code=""` làm giá hiển thị lệch +16.000₫ so với phí checkout thật (`SHP-B1`). |
| **Mua nhãn vận chuyển** | `UC-SHIP03`, `UC-S15` (`docs/spec.md:132,138`) | **LỖI** | `CreateShipment` có trả `tracking_code` và `label_url`, nhưng mock trả URL `.local` không tải/in được, UI seller chỉ hiển thị chữ thuần (`SHP-B8`), seller đổi hãng gây lệch phí (`SHP-B2`), và chưa có in/gộp nhãn hàng loạt (`UC-S15`). |
| **Giao nội thành (hoả tốc)** | **Ngoài phạm vi đặc tả** (`docs/spec.md:28`, tab *Phạm vi*, cột *Ngoài phạm vi*: *"giao hoả tốc nội thành"*) | **Ngoài phạm vi** | Không thuộc phạm vi triển khai; tuy nhiên PDP Web (`web/src/lib/sizing.ts:219`) đang hiển thị chuỗi mock `"Giao nhanh nội thành 2–4 giờ"` gây sai lệch kỳ vọng (`SHP-D5`). |
| **Nhận tại cửa hàng** | **Ngoài phạm vi đặc tả** (`docs/spec.md:39`: *"Mọi tính năng không có trong tab Use case"*) | **Ngoài phạm vi** | Không có UC nào trong tab *Use case* của `docs/spec.md`. |
| **Giao một phần** | **Ngoài phạm vi đặc tả** (`docs/spec.md:39`; `docs/spec.md:138` tạo 1 vận đơn/đơn con) | **Ngoài phạm vi** | Hệ thống có tách đơn theo nhiều gian hàng (`UC-B15`), nhưng không có giao một phần (tách nhiều kiện trong 1 đơn con); `shipments.order_id` có ràng buộc `UNIQUE`. |
| **Tracking** | `UC-SHIP04` (`docs/spec.md:28,139`) | **MỘT PHẦN** | Có webhook HMAC/chống replay/idempotent/chống sai thứ tự, đồng bộ timeline sang `order` và hiển thị ở Web buyer/seller khớp `shipping_db`, có job polling cho sandbox; còn lỗi không khớp carrier URL (`SHP-B6`) và thiếu phân trang poller. |
| **Tích hợp 3PL** | `UC-SHIP02`, `UC-SHIP03`, `UC-SHIP04`, `UC-A12` (`docs/spec.md:28,55,137-139`) | **MỘT PHẦN** | Đã có interface `Carrier` chung (`carrier.go:118-126`), 2 adapter (`GHNAdapter` gồm mock + code sandbox, `MockExpressAdapter`) và `ResilientCarrier` (timeout 5s, retry backoff, circuit breaker). Stack chỉ chạy mock, chưa có GHTK thật; quản lý kho 3PL thuộc mục *Ngoài phạm vi* (`docs/spec.md:28`). |

---

## 3. Lỗi

### `SHP-B1` (P1) – Phí vận chuyển Web báo ở giỏ hàng/checkout lệch +16.000₫ so với phí thu thật khi đặt đơn (`UC-SHIP02`)

- **Tái hiện**:
  ```bash
  # 1. Payload giống hệt Web (/gio-hang, /thanh-toan) gửi: from_province_code rỗng, chỉ truyền shop_id Kho Basic Sài Gòn (tỉnh 79)
  curl -s -X POST http://localhost:48080/api/v1/shipping/calculate-fee \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"shop_id":"98c480c1-4a11-4c33-a0e0-8e807ecb4c08","from_province_code":"","from_ward_code":"","to_province_code":"79","to_ward_code":"79001","weight_grams":250,"cod_amount_vnd":200000}'
  # 2. Đặt đơn COD thật với cùng shop (Kho Basic SG) và địa chỉ nhận tại TP.HCM (79/79001)
  curl -s -X POST http://localhost:48080/api/v1/checkout \
    -H "Authorization: Bearer $TOKEN" -H "Idempotency-Key: audit-shp-b1" -H "Content-Type: application/json" \
    -d '{"address_id":"<ADDR_79_ID>","items":[{"sku_id":"3416cd7d-9811-484b-a478-3a52e420e4d1","quantity":1}],"payment_method":"COD","shipping_choices":[{"shop_id":"98c480c1-4a11-4c33-a0e0-8e807ecb4c08","carrier_code":"ghn","service_code":"STANDARD"}]}'
  ```
- **Mong đợi**: `calculate-fee` dùng địa chỉ lấy hàng thật của shop (tỉnh `79`) để báo giá nội tỉnh `24.000₫` (`ghn/STANDARD`, đã gồm 2.000₫ COD) / `19.500₫` (`mock_express/STANDARD`), khớp 100% với `orders[0].shipping_fee_vnd` khi checkout.
- **Kết quả thật**: Bước 1 trả `200` với `ghn/STANDARD = 40.000₫` (`mock_express/STANDARD = 35.500₫`) và lưu key cache rỗng `shipping:quotes:v1:::79:79001:300:1`; bước 2 (`O1`) tính thật `shipping_fee_vnd = 24.000₫` (chênh **16.000₫**; tương tự tuyến nội tỉnh Hà Nội và sai lệch ngược với khách miền Trung).
- **Nguyên nhân gốc**: `web/src/app/(shop)/thanh-toan/CheckoutClient.tsx:295-306` và `web/src/app/(shop)/gio-hang/page.tsx:183-194` cố tình gửi `from_province_code: ""`; phía backend `services/shipping/internal/service/shipping.go:81-116` không đọc `req.ShopID` để tra kho shop, khiến `address.RegionOfProvince("")` (`services/shipping/internal/address/data.go:81-88`) rơi về `RegionCentral` và `routeZoneFee` (`carrier/ghn.go:379-387`) tính giá liên miền. Ngoài ra `cache/quote_cache.go:43-62` không đưa `ShopID` vào cache key.

### `SHP-B2` (P1) – Seller đổi hãng/dịch vụ khi tạo vận đơn gây lệch phí giữa `order_db` và `shipping_db` (`UC-SHIP03`)

- **Tái hiện**:
  ```bash
  # 1. Buyer checkout chọn mock_express/STANDARD (shipping_fee_vnd = 19500)
  curl -s -X POST http://localhost:48080/api/v1/checkout \
    -H "Authorization: Bearer $BUYER_TOKEN" -H "Idempotency-Key: audit-shp-b2" -H "Content-Type: application/json" \
    -d '{"address_id":"<ADDR_79_ID>","items":[{"sku_id":"3416cd7d-9811-484b-a478-3a52e420e4d1","quantity":1}],"payment_method":"COD","shipping_choices":[{"shop_id":"98c480c1-4a11-4c33-a0e0-8e807ecb4c08","carrier_code":"mock_express","service_code":"STANDARD"}]}'
  # 2. Seller xác nhận rồi tạo vận đơn bằng ghn/EXPRESS (UI seller cũng mặc định ghn/STANDARD bất kể buyer chọn gì)
  curl -s -X POST http://localhost:48080/api/v1/seller/orders/<ORDER_ID>/confirm -H "Authorization: Bearer $SELLER_TOKEN"
  curl -s -X POST http://localhost:48080/api/v1/seller/orders/<ORDER_ID>/ship \
    -H "Authorization: Bearer $SELLER_TOKEN" -H "Content-Type: application/json" \
    -d '{"carrier_code":"ghn","service_code":"EXPRESS"}'
  # 3. Đọc lại vận đơn
  curl -s http://localhost:48080/api/v1/shipping/shipments/<ORDER_ID> -H "Authorization: Bearer $SELLER_TOKEN"
  ```
- **Mong đợi**: Không cho đổi sang hãng/dịch vụ có phí khác với lựa chọn đã chốt ở đơn (hoặc giữ nhất quán `shipping_fee_vnd` và tiền thu hộ/đối soát); UI seller phải điền sẵn `order.carrier_code`/`order.service_code` mà buyer đã chọn.
- **Kết quả thật**: Trên đơn `O2` (`ddf5eb45-f4d7-43f2-abe0-17879c742662`), `order_db` vẫn giữ `shipping_fee_vnd = 19.500₫` và `cod_amount_vnd = 238.500₫` (nhưng đổi `carrier_code=ghn/EXPRESS`), trong khi `shipping_db.shipments.fee_vnd = 36.000₫` — lệch **16.500₫** không có bút toán bù trừ.
- **Nguyên nhân gốc**: `services/order/internal/service/lifecycle.go:405-533` chấp nhận `req.CarrierCode`/`req.ServiceCode` ghi đè không kiểm tra đối chiếu phí; Web `web/src/app/(seller)/seller/don-hang/page.tsx:75-76` và `[id]/page.tsx:57-58` hardcode mặc định `CARRIER_OPTIONS[0]` (`ghn/STANDARD`).

### `SHP-B3` (P2) – Không thể huỷ vận đơn (`UC-SHIP07`) trong luồng thật do chuyển trạng thái `SHIPPING` quá sớm và thiếu REST route

- **Tái hiện**:
  ```bash
  # Ngay sau khi seller tạo vận đơn (shipment.status vẫn là CREATED, hãng chưa lấy hàng):
  curl -s -X POST http://localhost:48080/api/v1/orders/<ORDER_ID>/cancel \
    -H "Authorization: Bearer $BUYER_TOKEN" -H "Content-Type: application/json" -d '{"reason":"huy truoc lay hang"}'
  curl -s -X POST http://localhost:48080/api/v1/orders/<ORDER_ID>/cancel \
    -H "Authorization: Bearer $SELLER_TOKEN" -H "Content-Type: application/json" -d '{"reason":"shop huy"}'
  ```
- **Mong đợi**: Theo `UC-SHIP03` bước 4–5 và `UC-SHIP07` (`docs/spec.md:138,142`), khi mới tạo vận đơn (`CREATED`), hãng chưa lấy hàng nên đơn chưa ở trạng thái `SHIPPING` và vẫn cho phép huỷ đơn → phát `order.cancelled` → `shipping` gọi hãng huỷ vận đơn sang `cancelled`.
- **Kết quả thật**: Cả buyer lẫn seller trên đơn `O2` đều bị chặn với `409 {"error":{"code":"FAILED_PRECONDITION","message":"invalid order status transition: cannot transition from SHIPPING to CANCELLED"}}`. Ngoài ra không tồn tại REST endpoint nào gọi `Shipping.CancelShipment` trên Gateway.
- **Nguyên nhân gốc**: `services/order/internal/service/lifecycle.go:496-517` gọi `TransitionOrderShipped` chuyển ngay đơn sang `SHIPPING` khi tạo vận đơn; `services/order/internal/statemachine/statemachine.go:28-31` cấm `SHIPPING -> CANCELLED`; `services/gateway/internal/router/router.go:130-185` không khai báo route cho `CancelShipment`.

### `SHP-B4` (P2) – Đơn `RETURN_REQUESTED` sau `DELIVERED` bị kẹt vĩnh viễn phía vận chuyển vì thiếu vận đơn trả hàng (`UC-SHIP08`)

- **Tái hiện**:
  ```bash
  # 1. Trên đơn O1 đã DELIVERED, buyer gửi yêu cầu trả hàng
  curl -s -X POST http://localhost:48080/api/v1/orders/<ORDER_ID>/return \
    -H "Authorization: Bearer $BUYER_TOKEN" -H "Content-Type: application/json" \
    -d '{"reason":"AUDIT-shp test return","evidence_urls":[]}'
  # 2. Thử gửi webhook returned cho vận đơn hiện có (ký HMAC hợp lệ bằng $SHIPPING_WEBHOOK_SECRET và $TS hiện tại)
  curl -s -X POST http://localhost:48080/webhooks/shipping/ghn \
    -H "Content-Type: application/json" -H "X-Timestamp: $TS" -H "X-Signature: $SIG" \
    -d '{"event_id":"evt-ret-after-dlv","tracking_code":"<TRACKING>","raw_status":"returned","description":"Hoàn hàng","occurred_at":"2026-11-22T18:24:00Z"}'
  ```
- **Mong đợi**: Có thể tạo vận đơn chiều ngược (`UC-SHIP08`) lấy từ địa chỉ người mua về kho người bán và cập nhật hành trình qua webhook để đưa đơn sang `RETURNED`.
- **Kết quả thật**: Bước 1 chuyển đơn sang `RETURN_REQUESTED` (`return_request_id=1b61dc87-...`), nhưng không có RPC/route tạo vận đơn trả hàng; bước 2 bị từ chối trạng thái với `200 {"ok":true,"outcome":"stale","status":"delivered"}`.
- **Nguyên nhân gốc**: `services/shipping/migrations/000001_init.sql:38` đặt `order_id TEXT NOT NULL UNIQUE` trên `shipments`; `services/shipping/internal/store/store.go:638-643` khoá mọi trạng thái terminal (`delivered`); `proto/nexusmart/shipping/v1/shipping.proto` chưa có `CreateReturnShipment`.

### `SHP-B5` (P2) – Giao thất bại & hoàn hàng đơn COD (`UC-SHIP05`) không tăng `cod_rejected_count`, không báo seller và không đảo trạng thái COD

- **Tái hiện**:
  ```bash
  # 1. Trên đơn O2 (COD, đã ship), lần lượt gửi 3 webhook hợp lệ (ký bằng $SHIPPING_WEBHOOK_SECRET):
  #    raw_status="delivery_fail" -> "returning" -> "returned" tới POST /webhooks/shipping/ghn
  # 2. Kiểm tra hồ sơ buyer và thông báo seller sau 2 giây:
  curl -s http://localhost:48080/api/v1/users/me -H "Authorization: Bearer $BUYER_TOKEN"
  curl -s http://localhost:48080/api/v1/notifications -H "Authorization: Bearer $SELLER_TOKEN"
  ```
- **Mong đợi**: Theo `UC-SHIP05` (`docs/spec.md:140`), khi giao thất bại cần thông báo cho cả buyer và seller; buyer có lựa chọn giao lại/đổi giờ; khi đơn COD hoàn hàng phải tăng `cod_rejected_count` để áp rule chặn COD (`UC-PAY02`) và xử lý xong trạng thái công nợ COD.
- **Kết quả thật**: Đơn `O2` chuyển sang `RETURNED` và tồn kho SKU được cộng lại đúng (`31 -> 30` sau O1+O2), nhưng: (a) `audit-shp-1.cod_rejected_count` vẫn bằng **`0`**; (b) `seller.saigon` nhận **`0`** thông báo mới về giao thất bại/hoàn hàng; (c) không có API/UI yêu cầu giao lại; (d) trong `payment_db`, bản ghi `payments` của đơn `O2` (`68e9cd2d-...`, COD `238.500₫`) vẫn ở trạng thái `succeeded`.
- **Nguyên nhân gốc**: `services/order/internal/service/handlers.go:223-249` chỉ gọi `RestockOrderItems` và `CreateRefund` (khi `VIETQR`), không phát event `order.returned` (`services/order/internal/store/postgres.go:883-906`) và không gọi `user` tăng `cod_rejected_count` (kiểm `rg "cod_rejected_count" services/user/` không có lệnh `UPDATE` nào); `services/notification/internal/service/notification.go:718-729` chỉ gửi thông báo cho `order.BuyerId`.

### `SHP-B6` (P2) – Webhook vận chuyển không kiểm tra `{carrier}` trên URL khớp với `shipment.carrier_code`

- **Tái hiện**:
  ```bash
  # Đơn O2 tạo vận đơn với carrier_code="ghn" (tracking_code="GHN-7110F983B8").
  # Gửi webhook với đường dẫn sai hãng (/webhooks/shipping/fake_carrier), ký bằng $SHIPPING_WEBHOOK_SECRET và $TS hợp lệ:
  curl -s -X POST http://localhost:48080/webhooks/shipping/fake_carrier \
    -H "Content-Type: application/json" -H "X-Timestamp: $TS" -H "X-Signature: $SIG" \
    -d '{"event_id":"evt-wrong-carrier","tracking_code":"GHN-7110F983B8","raw_status":"delivery_fail","description":"Test","occurred_at":"2026-11-22T18:23:30Z"}'
  ```
- **Mong đợi**: Từ chối (`400`/`403`) khi `{carrier}` trên đường dẫn không khớp hãng sở hữu vận đơn, và mỗi hãng có secret xác thực riêng.
- **Kết quả thật**: Trả `200 {"ok":true,"outcome":"applied","status":"failed"}`, cập nhật thành công vận đơn `ghn` và lưu `webhook_events.carrier_code = 'fake_carrier'`.
- **Nguyên nhân gốc**: `services/shipping/internal/webhook/handler.go:85-188` dùng chung 1 biến `SHIPPING_WEBHOOK_SECRET` cho mọi `{carrier}`; `services/shipping/internal/store/store.go:602-665` (`ApplyWebhookEvent`) không kiểm tra `params.CarrierCode == sh.CarrierCode`.

### `SHP-B7` (P2) – `CalculateFee` không từ chối địa chỉ ngoài vùng phục vụ và cân nặng `<= 0` (`UC-SHIP02`)

- **Tái hiện**:
  ```bash
  curl -s -X POST http://localhost:48080/api/v1/shipping/calculate-fee \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"shop_id":"98c480c1-4a11-4c33-a0e0-8e807ecb4c08","from_province_code":"79","from_ward_code":"79001","to_province_code":"99999","to_ward_code":"99999","weight_grams":250,"cod_amount_vnd":0}'
  curl -s -X POST http://localhost:48080/api/v1/shipping/calculate-fee \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"shop_id":"98c480c1-4a11-4c33-a0e0-8e807ecb4c08","from_province_code":"79","from_ward_code":"79001","to_province_code":"79","to_ward_code":"79001","weight_grams":0,"cod_amount_vnd":0}'
  ```
- **Mong đợi**: Báo lỗi không hỗ trợ địa chỉ / ngoài vùng phục vụ (`400`/`422`) theo ngoại lệ `UC-SHIP02` (`docs/spec.md:137`) và từ chối `weight_grams <= 0`.
- **Kết quả thật**: Cả hai đều trả `200 OK` kèm 4 báo giá (tỉnh `"99999"` trả `34.000–50.000₫` và được cache vào Redis key `shipping:quotes:v1:79:79001:99999:99999:300:0`; `weight_grams=0` trả `18.000–34.000₫`).
- **Nguyên nhân gốc**: `services/shipping/internal/service/shipping.go:81-116` và `grpcserver/server.go:84-100` không kiểm tra sự tồn tại của `province_code`/`ward_code` trong DB và tự nâng cân nặng `<= 0` sang mặc định thay vì trả lỗi hợp lệ.

### `SHP-B8` (P3) – Tìm kiếm tỉnh/phường phân biệt dấu, nhãn in `.local` chết / không mở được ở Seller, thông báo vận chuyển tiếng Anh thô & bị trùng

- **Tái hiện**:
  ```bash
  curl -s "http://localhost:48080/api/v1/shipping/provinces?query=Ha%20Noi"
  curl -s "http://localhost:48080/api/v1/shipping/provinces?query=H%C3%A0%20N%E1%BB%99i"
  ```
- **Mong đợi**: Tìm không dấu `"Ha Noi"` vẫn ra `"Thành phố Hà Nội"`; trang seller có nút/liên kết in nhãn dùng được; thông báo hiển thị tiếng Việt tự nhiên và không lặp 2 thông báo giống nhau khi `delivered`.
- **Kết quả thật**: `?query=Ha Noi` trả `{"provinces":[]}` trong khi `?query=Hà Nội` trả `01 Thành phố Hà Nội`; `label_url` mock trỏ tới domain giả `https://sandbox.ghn.local/labels/...pdf` và `/seller/don-hang/[id]` chỉ render `<span>` chữ thuần; buyer `audit-shp-1` nhận thông báo tiếng Anh thô `"Cập nhật vận chuyển (delivering)"` và nhận 2 thông báo cùng lúc khi giao xong (`order.delivered` + `shipment.updated (delivered)`).
- **Nguyên nhân gốc**: `services/shipping/internal/store/store.go:230,267` dùng `ILIKE` không bỏ dấu; `carrier/ghn.go:147` & `carrier/mock_express.go:84` sinh URL `.local`; `web/src/app/(seller)/seller/don-hang/[id]/page.tsx:705-710` không dùng thẻ `<a>`; `services/notification/internal/service/notification.go:700-729` ghép trực tiếp chuỗi enum `status`.

---

## 4. Dữ liệu

- **`SHP-D1`** – **Địa chỉ kho lấy hàng của shop seed lệch phường và lặp địa danh**: Trong `user_db`, shop `Kho Basic Sài Gòn` (`98c480c1-4a11-4c33-a0e0-8e807ecb4c08`) có `street_address = "Số 118 đường Nguyễn Văn Hưởng, Phường Thảo Điền, Thành phố Hồ Chí Minh"`, nhưng `ward_code = "79001"`, `ward_name = "Phường Sài Gòn"` (do `shipping_db.wards` không có `Phường Thảo Điền` nên `tools/seed/seed.mjs` rơi về phường đầu tiên). Ngoài ra cả 4 shop seed đều lặp tên phường và tỉnh/thành trong `street_address`.
- **`SHP-D2`** – **Địa chỉ buyer demo lệch phường giữa Web seed và `user_db`**: Tài khoản `minhquan.nguyen@nexusmart.vn` khai báo ở `web/src/data/orders.ts:211` là `"Phường Xuân Hòa"` nhưng trong `user_db.addresses` bị lưu thành `ward_code = "79001"`, `ward_name = "Phường Sài Gòn"` (vì thiếu phường này trong `shipping_db.wards`).
- **`SHP-D3`** – **Danh mục phường/xã và mã GHN chưa đầy đủ**: `shipping_db.wards` chỉ có **115** phường/xã mẫu cho cả 34 tỉnh (3–8 phường/tỉnh, thiếu hàng nghìn đơn vị cấp xã sau sáp nhập); khoảng 101/115 bản ghi trong `services/shipping/internal/address/data.go:203-280` dùng `ghn_district_id`/`ghn_ward_code` giữ chỗ, không gọi được GHN sandbox thật.
- **`SHP-D4`** – **Danh mục tỉnh/phường tĩnh phía Web lệch chuẩn `shipping_db`**: `web/src/lib/provinces.ts` dùng mã chữ (`HN`, `HCM`, `DN`...) thay vì mã GSO (`01`, `79`, `48`...) và danh sách phường khác với `shipping_db` (có `Phường Thảo Điền`, `Phường Xuân Hòa`, `Phường Bến Nghé` không có trong DB).
- **`SHP-D5`** – **PDP (`/san-pham/[slug]`) hiển thị hãng, phí ship và chính sách freeship giả mâu thuẫn backend & phạm vi**: `ProductDetailClient.tsx:271` gọi `estimateDelivery()` (`web/src/lib/sizing.ts:196-254`), HTML trả thật từ `:43000` chứa hãng `SPX` (backend không có), `"Giao nhanh nội thành 2-4 giờ"` (ngoài phạm vi `docs/spec.md:28`), và mốc freeship tự động 199k/249k (backend chỉ giảm ship qua voucher freeship).
- **`SHP-D6`** – **Dữ liệu cấu hình vận chuyển Seller & Admin trên Web là mock hoàn toàn**: `/seller/ho-so` tab "Giao hàng & Đổi trả" (`sellerStore.ts`) và `/admin/cau-hinh` mục "Vùng & hãng vận chuyển" (`auditStore.ts`, hiển thị hãng giả `SPX`, `NexusFast`) không đọc/ghi backend và không khớp danh sách hãng thực tế (`ghn`, `mock_express`).

---

## 5. Đề xuất sửa

| Mã | Giải quyết | Việc cụ thể (file, RPC, migration, route) | Cỡ | Đổi proto / Migration / ADR |
|---|---|---|---|---|
| **SHP-F1** | `SHP-B1`, `SHP-B7`, `SHP-D5`, `UC-SHIP02` | Sửa `CheckoutClient.tsx:295-306` & `gio-hang/page.tsx:183-194` truyền đúng `from_province_code`/`from_ward_code` của shop (hoặc cho `shipping` tra kho theo `shop_id` qua `UserService`); bổ sung validate `province_code`/`ward_code` tồn tại trong `shipping_db`, từ chối `weight_grams <= 0`, thêm `shop_id` vào cache key (`cache/quote_cache.go:43-62`); bỏ `estimateDelivery()` mock ở PDP (`ProductDetailClient.tsx`, `sizing.ts`) thay bằng gọi `/shipping/calculate-fee`. | **M** | Proto: Không; Migration: Không; ADR: Không |
| **SHP-F2** | `SHP-B2`, `SHP-B8`, `UC-SHIP03` | Trong `services/order/internal/service/lifecycle.go:405-533` mặc định dùng `order.CarrierCode`/`order.ServiceCode` đã chốt, từ chối ghi đè hãng/dịch vụ gây tăng/lệch phí; Web `/seller/don-hang` và `[id]/page.tsx` khởi tạo dropdown từ đơn thay vì `CARRIER_OPTIONS[0]` và hiển thị link/nút in nhãn; bổ sung endpoint tải PDF nhãn mock ở chế độ dev thay cho URL `.local`. | **S** | Proto: Không; Migration: Không; ADR: Không |
| **SHP-F3** | `SHP-B3`, `UC-SHIP03`, `UC-SHIP07` | Tách bước tạo vận đơn (`CREATED` – giữ đơn ở `PREPARING` hoặc thêm `READY_TO_SHIP`) khỏi bước hãng đã lấy hàng (chỉ chuyển đơn sang `SHIPPING` khi webhook `picking`/`delivering`); cho phép `CancelOrder` khi vận đơn chưa lấy hàng và/hoặc mở REST `POST /api/v1/seller/orders/{id}/shipment/cancel` gọi `Shipping.CancelShipment`; thêm outbox retry cho `CreateShipment` khi API hãng lỗi và thông báo seller khi vận đơn bị huỷ. | **M** | Proto: Có (`order.proto` nếu thêm trạng thái); Migration: Có (bảng outbox tạo vận đơn); ADR: Không |
| **SHP-F4** | `SHP-B6`, `SHP-B8`, `UC-SHIP04` | Kiểm tra `params.CarrierCode == sh.CarrierCode` trong `store.go:602-665`, hỗ trợ webhook secret riêng theo từng hãng trong `config.go` & `webhook/handler.go`; phân trang/cursor cho `ListActiveShipments` trong `poller.go`; Việt hoá thông báo trong `notification.go:700-729` và bỏ thông báo trùng khi `delivered`. | **S** | Proto: Không; Migration: Không; ADR: Không |
| **SHP-F5** | `SHP-B5`, `UC-SHIP05`, phối hợp `UC-PAY03` | Thêm RPC/consumer tăng `cod_rejected_count` ở `services/user` khi đơn COD `RETURNED`; phát `order.returned` từ `order` để `payment` đảo bút toán `cod_receivable`; báo seller khi giao thất bại/hoàn hàng; thêm luồng giao lại; bổ sung `cod_amount_vnd`/`fee_vnd` vào `shipment.updated`, thêm cột `cod_collected_vnd`/`cod_settled_at` và RPC `ListShipmentsForCODReconciliation` trên `shipping` cho `payment` đối soát `UC-PAY03`. | **M** | Proto: Có (`shipping.proto`, `user.proto`); Migration: Có (`shipping_db`); ADR: Không |
| **SHP-F6** | `SHP-B4`, `UC-SHIP08` | Bỏ `UNIQUE(order_id)` trên `shipping_db.shipments`, thêm cột `direction` (`forward`/`reverse`) + `UNIQUE(order_id, direction)`; thêm RPC `CreateReturnShipment` trong `shipping.proto`, route gateway, tích hợp duyệt trả hàng `UC-S10` trong `order` và UI vận đơn trả hàng trên Web. | **L** | Proto: Có (`shipping.proto`, `order.proto`); Migration: Có (`shipping_db`, `order_db`); ADR: Không |
| **SHP-F7** | `UC-SHIP06`, `UC-A12`, `SHP-D6` | Thêm bảng `shop_shipping_configs`, `carrier_configs` (mã hoá API token), `fallback_fee_rules` trong `shipping_db`; thêm RPC & REST `/api/v1/seller/shipping/config`, `/api/v1/admin/shipping/carriers` (kèm kiểm tra kết nối hãng + audit log); tích hợp `CalculateFee` lọc theo hãng shop bật; nối Web `/seller/ho-so` và `/admin/cau-hinh` với API thật. | **L** | Proto: Có (`shipping.proto`); Migration: Có (`shipping_db`); ADR: **Có** nếu chọn hệ thống KMS/Vault mã hoá token hoặc tích hợp hãng mới (GHTK) theo `AGENTS.md` luật 11 |
| **SHP-F8** | `UC-SHIP01`, `SHP-B8`, `SHP-D1`..`D4` | Nạp bộ dữ liệu phường/xã đầy đủ cho 34 tỉnh + mã địa chỉ GHN thật; thêm tìm kiếm không dấu (`unaccent` hoặc cột chuẩn hoá) trong `ListProvinces`/`ListWards`; tích hợp `POST /api/v1/shipping/addresses/map-legacy` vào `AddressForm.tsx`; kiểm tra mã địa chỉ tại `services/user` `CreateAddress`; loại bỏ `web/src/lib/provinces.ts` lệch chuẩn và cập nhật seed địa chỉ 4 shop + buyer demo. | **M** | Proto: Không; Migration: Có (`shipping_db`); ADR: Không |

---

## 6. Đã kiểm gì, chưa kiểm gì

### Đã kiểm
- **Mã nguồn `HEAD` (`0db2cdd`) & unit test**: đọc toàn bộ `services/shipping/`, `proto/nexusmart/shipping/v1/shipping.proto`, gateway `handlers_shipping.go`/`handlers_order.go`/`router.go`, các luồng liên quan ở `order`, `user`, `notification`, `payment` và `web/`. Chạy `cd services/shipping && go test -v -race ./...` → **PASS** (5 package có test đều xanh; `cache`, `config`, `events`, `poller`, `store` chưa có file test).
- **Stack chạy thật `nexusmart-audit`**:
  - Kiểm `GET /provinces` (34 tỉnh), `GET /provinces?query=...` (có dấu/không dấu), `GET /provinces/{code}/wards`, `POST /addresses/map-legacy` (7 tổ hợp tỉnh sáp nhập + không tồn tại).
  - Đăng ký 2 buyer riêng `audit-shp-1@nexusmart.local` (xác thực OTP qua Mailpit `:48025`, tạo địa chỉ TP.HCM `79/79001`) và `audit-shp-2@nexusmart.local` (kiểm RBAC chéo).
  - Kiểm `POST /api/v1/shipping/calculate-fee` với 5 tổ hợp (giống Web `from=""`, có `from="79"`, hàng nặng 1600g, tỉnh không tồn tại `99999`, cân nặng `0`).
  - Chạy thật 3 đơn COD trên shop `Kho Basic Sài Gòn`:
    - **O1** (`4d19357d-...`, `GHN-59EF135197`): checkout → ship → kiểm RBAC 5 vai trò trên `GET /shipments/{id}` → kiểm webhook (sai chữ ký `401`, thiếu/lệch `X-Timestamp` `401`, `delivering` `200`, gửi trùng `event_id` `duplicate`, sự kiện cũ hơn `stale`, `delivered` → đơn sang `DELIVERED` không tự `COMPLETED`, kiểm thông báo buyer) → gọi `POST /orders/{id}/return` (`RETURN_REQUESTED`) và kiểm chứng khoá terminal.
    - **O2** (`ddf5eb45-...`, `GHN-7110F983B8`): checkout `mock_express/STANDARD` (`19500₫`) → seller ship ghi đè `ghn/EXPRESS` (`36000₫`) → thử huỷ đơn ngay khi `CREATED` (bị `409` cả buyer và seller) → kiểm webhook sai đường dẫn `/webhooks/shipping/fake_carrier` (`200 applied`) → `returning` → `returned` → kiểm tồn SKU hoàn về đúng, `cod_rejected_count=0`, thông báo seller = 0 và trạng thái `payment_db`.
    - **O3** (`22a4c11e-...`): huỷ trước khi ship → kiểm consumer `order.cancelled` xử lý sạch (`processed_events`, `dead_letter_events=0`, NATS `NEXUSMART_DLQ=0`).
  - Kiểm Web BFF `:43000`: đăng nhập buyer & seller qua `/api/auth/login`, kiểm `/api/gw/shipping/shipments/{id}`, `/don-hang/{id}`, `/seller/don-hang/{id}`, HTML PDP `/san-pham/ao-khoac-gio-nhe-gap-gon-xam-tro`.
  - Đọc chỉ đọc DB (`shipping_db`, `user_db`, `payment_db`), Redis (`shipping:quotes:*`), NATS monitor (`:48222/jsz`), log JSON `shipping` (có `trace_id` và che `signature_present: "[REDACTED]"`).

### Dữ liệu đối soát COD phía `shipping` (phối hợp `pay` / `UC-PAY03`)
- **Đang lưu trong `shipping_db.shipments`**: `order_id`, `shop_id`, `carrier_code`, `service_code`, `tracking_code`, `status`, `fee_vnd`, `cod_amount_vnd`, `updated_at` (và mốc thời gian giao trong `shipment_events`).
- **Đang cung cấp ra ngoài**: chỉ có `GetShipment(order_id | tracking_code)` (tra đơn lẻ) và event NATS `shipment.updated` (`events/envelope.go:28-38`) — **payload event này không chứa `cod_amount_vnd` và `fee_vnd`**.
- **Còn thiếu cho `UC-PAY03`**: chưa có RPC liệt kê/xuất danh sách vận đơn COD theo phiên hoặc khoảng ngày (`ListShipments`), chưa có cột `cod_collected_vnd` / `cod_settled_at`, chưa có adapter tải/import bảng kê đối soát từ hãng; phía `payment` cũng chưa subscribe `shipment.updated` hay `order.returned` (`run_reconciliation` hiện chỉ lọc `method = 'VIETQR'`).

### Chưa kiểm (kèm lý do)
- **GHN Sandbox thật (`GHN_MODE=sandbox`)**: không gọi dịch vụ ngoài thật theo luật rà soát; chỉ kiểm qua đọc code `services/shipping/internal/carrier/ghn.go:177-377` và unit test mock HTTP `carrier_test.go` (ghi nhận thêm trong code sandbox: `sandboxCreateShipment` hardcode `ServiceTypeID: 2` bỏ qua `EXPRESS`, không truyền `from_district_id`/`from_ward_code`).
- **Job polling dự phòng chạy thật (`poller.go`)**: ở chế độ `mock`, `PollActiveShipments` bỏ qua mọi carrier có `IsMock() == true` (`shipping.go:392`); chỉ kiểm qua đọc code.
- **Circuit breaker mở mạch & `FallbackTable` trên stack chạy thật**: hai carrier mock không phát sinh lỗi mạng; đã kiểm qua code (`resilience.go`, `fallback.go`) và unit test.
- **Hoàn tiền đơn `VIETQR` khi giao thất bại (`UC-SHIP05` bước 5)**: mới kiểm code (`services/order/internal/service/handlers.go:232-248`), chưa chạy thử đơn VietQR hoàn hàng riêng.
- **Tương tác trình duyệt (Playwright)**: kiểm UI bằng đọc mã nguồn TSX + gọi trực tiếp HTTP/HTML và BFF API trên `http://localhost:43000`.
