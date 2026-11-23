# Báo cáo rà soát nghiệp vụ — Worker `pay` (Thanh toán, sổ cái, hoàn tiền, đối soát, tiền của sàn)

- **Ngày rà soát**: 2026-11-23 · **Code HEAD**: `0db2cdd` · **Stack**: `nexusmart-audit` (Gateway `:48080`, Payment `:48086`, Web BFF `:43000`, Mailpit `:48025`, `payment_db`/`order_db`/`product_db`).
- **Tài khoản kiểm thử riêng**: `audit-pay-1@nexusmart.local` (ID `63a9f89b-991e-4d55-9b7b-daa48f8c71bc`), `audit-pay-2@nexusmart.local` (ID `5757ae30-6c2c-48be-80fb-b02bb3dc0c79`). Unit test host `cd services/payment && cargo test`: **35 passed, 0 failed**.

---

## 1. Bảng đối chiếu Use Case (11 UC phụ trách)

| UC | Ưu tiên | Trạng thái | Backend (`file:dòng`) | REST | Web | Bằng chứng | Ghi chú |
| --- | --- | --- | --- | --- | --- | --- | --- |
| **UC-B07** Thanh toán online | Cao | **MỘT PHẦN** | `services/payment/src/service.rs:114-425`, `src/vietqr.rs:176-201`; `proto/nexusmart/payment/v1/payment.proto:9-13` | `POST /api/v1/checkout`, `GET /api/v1/payments/{id}`, `GET /api/v1/payments/{id}/events`, `POST /webhooks/payment` | `/thanh-toan`, `/thanh-toan/cho-thanh-toan/[paymentId]` | VietQR mock end-to-end `201 -> 200` (đơn `70d205a0-...`, payment `c1c77d2d-...`, `718,000₫`); `rg -n "vnpay\|momo\|zalopay" proto/ services/` trả 0 dòng | Đủ VietQR mock; **thiếu hoàn toàn ví điện tử sandbox thứ ba** (VNPay/MoMo/ZaloPay) theo `docs/spec.md:27,80`. |
| **UC-B25** Thanh toán lại đơn chờ thanh toán | Cao | **LỖI** | `services/gateway/internal/router/handlers_payment.go:212-290`, `services/payment/src/service.rs:240-285,796-870`, `services/order/internal/service/handlers.go:273-307` | `POST /api/v1/payments/{id}/retry` | `web/src/features/commerce/RetryPaymentBox.tsx:230-303`, `/don-hang/[id]`, `/thanh-toan/cho-thanh-toan/[paymentId]` | Tái hiện trên stack: retry `FAILED` trên đơn `CANCELLED` trả `200` tạo zombie payment `12ee177e-...` (`PAY-B1`); retry `PENDING` không cập nhật `orders.payment_id` (`PAY-B5`); late webhook `P1` ghi đè `orders.payment_status=REFUNDED` (`PAY-B6`); đổi `VIETQR->COD` trả `409` | Dính 4 lỗi `PAY-B1`, `PAY-B5`, `PAY-B6`, `PAY-B10`; chặn đổi sang COD (`CORE_REPORT.md` §6). |
| **UC-PAY01** Thanh toán bằng QR chuyển khoản (VietQR) | Cao | **ĐỦ** | `services/payment/src/vietqr.rs:167-308`, `src/webhook_security.rs:24-248`, `src/http.rs:100-243`, `src/service.rs:588-925` | `POST /api/v1/checkout`, `POST /webhooks/vietqr`, `GET /api/v1/payments/{id}/events` | `/thanh-toan`, `/thanh-toan/cho-thanh-toan/[paymentId]`, `VietQrPreview.tsx` | Checkout `201` trả `qr_url`/`qr_code`/`provider_ref=NMB3FB66BFC7`/`expires_at=+15m`; webhook sai chữ ký/thiếu `X-Timestamp`/lệch `±350s` → `401`; replay 10x (5 song song + 5 tuần tự) → `fresh=1, dup=9`; SSE nhận live `payment.pending` → `payment.succeeded`; lệch tiền `717,000` vs `718,000` → `queued_for_reconciliation:amount_mismatch` | Chạy trên `VIETQR_MODE=mock`; nhánh late payment đụng `PAY-B6`/`PAY-B11`, xử lý queue thuộc `UC-A14`. |
| **UC-PAY03** Đối soát COD với hãng vận chuyển | Trung bình | **THIẾU** | Chỉ có hàm dead code `build_cod_reconcile_tx` (`services/payment/src/ledger.rs:221-237`); không có trong `payment.proto`, `shipping.proto`, `service.rs`, `workers.rs` | Không có (`rg -n "cod_reconcil" services/gateway/` = 0) | Không có (tab 3 `/admin/doi-soat` chỉ là `ADMIN_RECONCILIATION` mock tại `web/src/data/admin.ts:370`) | `rg -n "build_cod_reconcile_tx" services/` chỉ thấy định nghĩa và 1 unit test `ledger.rs:863`; `cod_receivable` trong `payment_db` có `credits = 0` | Chưa có job/API import đối soát COD với hãng vận chuyển; COD bị giải ngân trước khi thu hộ (`PAY-B4`, `PAY-B7`). |
| **UC-PAY04** Hoàn tiền (refund) | Trung bình | **LỖI** | `services/payment/src/service.rs:929-1156,1889-2032`, `src/ledger.rs:241-292`, `services/order/internal/store/postgres.go:941-1013` | `POST /api/v1/admin/payments/{id}/refund` | `/admin/doi-soat` tab "Hoàn tiền giao dịch" (`page.tsx:505-698`) | Hoàn `10,000₫` idempotent (`already_existed: false -> true`), chặn vượt cap (`409`), NHƯNG tái hiện: (1) hoàn một phần rồi hoàn tất đơn rút vượt escrow (`PAY-B2`), (2) hoàn tiền sau khi escrow đã release trả `200` (`PAY-B2`), (3) hoàn tiền đơn COD đã hủy trả `200` (`PAY-B4`), (4) hoàn một phần ghi đè `orders.payment_status=REFUNDED` (`PAY-B6`), (5) thiếu luồng duyệt/tất toán `refund_payable` (`PAY-B11`) | Thiếu quy trình buyer nhập TK hoàn tiền QR, thiếu duyệt hoàn tiền `pending`, thiếu audit log. |
| **UC-PAY05** Chi trả cho người bán (payout) | Thấp | **THIẾU** | Bảng `seller_payouts` (`migrations/0001_init.sql:253-264`), `request_seller_payout` (`service.rs:1278-1339`), `build_seller_payout_tx` (`ledger.rs:412-439`) — **chưa nối RPC/HTTP** | Không có (`rg -n "payout" proto/ services/gateway/` = 0) | Không có (`/seller/tai-chinh/page.tsx:356` ghi chú sẽ mở giai đoạn sau) | `SELECT count(*) FROM seller_payouts` = `0`; `payment.proto` chỉ có 8 RPC, không có RPC payout | Mức Thấp (spec không bắt buộc cài); phần giữ tiền escrow bị nhảy cóc (`PAY-B7`). |
| **UC-PAY07** Đối soát giao dịch online với cổng thanh toán | Trung bình | **MỘT PHẦN** | `services/payment/src/service.rs:1488-1738`, `src/workers.rs:244-270`, `src/vietqr.rs:230-239,419-476` | `POST /api/v1/admin/payments/reconcile` (+ nội bộ `POST :48086/internal/reconciliation/run`) | `/admin/doi-soat` tab 1 chạy thật; tab 3 lịch sử là mock (`ADMIN_RECONCILIATION`) | `POST /api/v1/admin/payments/reconcile` ngày UTC `2026-11-22` → `200` (`matched_count: 5`, ghi bảng `reconciliation_runs`); ngày VN `2026-11-23` → `matched_count: 0` (`PAY-B8`) | Dính lỗi lệch múi giờ VN/UTC (`PAY-B8`), thiếu API xem lịch sử `reconciliation_runs` & xuất báo cáo; `PayOs` trả list rỗng, `Mock` lưu RAM. |
| **UC-S17** Khai báo tài khoản ngân hàng và xem sổ số dư | Trung bình | **MỘT PHẦN** | `services/payment/src/ledger.rs:616-653`, `src/grpc.rs:369-398` (chỉ có `GetSellerBalance`) | `GET /api/v1/seller/balance` | `/seller/tai-chinh` (`page.tsx:50-366`) | `GET /api/v1/seller/balance` & BFF `/api/gw/seller/balance` trả `200` (`available_vnd=405100, pending_vnd=0, total_earned_vnd=405100` khớp `ledger_entries`); `rg -n "bank_account" proto/ services/gateway/ services/user/` = 0 | **Thiếu hoàn toàn** CRUD/OTP/khóa 24h tài khoản ngân hàng (web dùng mock `sellerStore.getShopBankAccount`) và API sổ giao dịch ledger theo shop; dính `PAY-B3`, `PAY-B7`, `PAY-B12`. |
| **UC-A09** Cấu hình phí sàn | Trung bình | **MỘT PHẦN** | `services/product/internal/server/server.go:50-154`, `services/order/internal/service/checkout.go:964-985,1054` | `GET /api/v1/categories`, `POST/PUT /api/v1/admin/categories` | `/admin/cau-hinh` tab `danh_muc` (`AdminCategoriesPanel.tsx`) gọi thật; tab `doi_tra` là mock | Tạo danh mục `AUDIT-pay` `750 bps` (`201`) → sửa `800 bps` (`200`) → xóa (`200`); đơn đặt chụp `500 bps` ghi `platform_fee_revenue=11450`; `audit-logs` giữ nguyên `13` (không ghi log) | Có % hoa hồng theo danh mục & không áp ngược đơn cũ; **thiếu** cấu hình phí thanh toán, ngày hiệu lực, audit log; lệch dữ liệu với tab mock `doi_tra` (`PAY-D3`). |
| **UC-A13** Cấu hình phương thức thanh toán | Trung bình | **THIẾU** | Chỉ đọc env tĩnh tại `services/payment/src/config.rs:100-145` và `services/order/internal/config/` | Không có (`rg -n "payment.*config\|payment_method.*toggle" proto/ services/gateway/` = 0) | `/admin/cau-hinh` tab `thanh_toan` (`page.tsx:1032-1208`) là 100% mock `useMockStore` | Đọc `router.go` (0 route admin config payment) và `.env:59-82` (chỉ có biến môi trường tĩnh) | Không bật/tắt động COD/QR/ví, không nhập khóa cổng che ký tự, không chỉnh hạn mức động; UI mock hiển thị sai (`PAY-D4`). |
| **UC-A14** Xử lý giao dịch thanh toán không khớp | Trung bình | **MỘT PHẦN** | Ghi queue: `services/payment/src/service.rs:683-733,1583-1600`; đọc nội bộ: `src/http.rs:281-288` | Không có trên Gateway; chỉ có nội bộ `GET :48086/internal/reconciliation/queue` (JWT admin) | Không có trên `/admin/doi-soat` | Webhook lệch `717,000` vs `718,000` ghi 1 dòng `reconciliation_queue` (`id=85336e7c-...`, `resolution_status=pending`), đọc được ở `:48086/internal/reconciliation/queue` (`200` với admin, `401` không token, `403` buyer/seller) | Thiếu gRPC RPC, thiếu route Gateway, thiếu thao tác gán đơn / hoàn tiền / đánh dấu treo / audit log và thiếu UI (`PAY-B9`). |

---

## 2. Bảng đối chiếu Shopify / sàn thương mại điện tử

| Nghiệp vụ | UC hoặc ngoài phạm vi | Trạng thái | Ghi chú |
| --- | --- | --- | --- |
| **Thanh toán — Cổng chính (VietQR)** | `UC-PAY01` (`docs/spec.md:27,110`) | **ĐỦ** *(mock)* | VietQR mock (adapter có nhánh `payos`/`sepay`), HMAC-SHA256 + `X-Timestamp` + idempotent 10x + SSE live. |
| **Thanh toán — Cổng bên thứ ba (Ví điện tử sandbox)** | `UC-B07` (`docs/spec.md:27,80`) | **THIẾU** | Spec yêu cầu 1 ví điện tử sandbox (VNPay/MoMo/ZaloPay), hiện chỉ có `COD` và `VIETQR`. |
| **Thanh toán — Thủ công (COD, chuyển khoản)** | `UC-PAY02` (`docs/spec.md:111`), `UC-PAY01`, `UC-A14` | **MỘT PHẦN / LỖI** | COD checkout chạy (thuộc worker `ord`), nhưng bên `pay` lỗi không đảo ngược sổ cái khi hủy COD (`PAY-B4`) và thiếu đối soát tay (`PAY-B9`). |
| **Thanh toán — Trả góp** | Ngoài phạm vi đặc tả (`docs/spec.md:25,27` cột Ngoài phạm vi: "trả góp") | **Ngoài phạm vi** | Không cài đặt theo đúng spec. |
| **Thanh toán — Đa tiền tệ** | Ngoài phạm vi đặc tả (`docs/spec.md:27` chỉ VND nguyên; `docs/spec.md:39`; `AGENTS.md` luật 6 cần ADR) | **Ngoài phạm vi** | Toàn bộ proto/DB/code dùng `int64` VND (`*_vnd`), chưa có mã tiền tệ. |
| **Thanh toán — Hoàn tiền** | `UC-PAY04` (`docs/spec.md:113`) | **LỖI** | Có hoàn tiền toàn phần/một phần idempotent nhưng lỗi rút vượt escrow (`PAY-B2`), cho hoàn COD (`PAY-B4`), lệch trạng thái order (`PAY-B6`), chưa tất toán `refund_payable` (`PAY-B11`). |
| **Thanh toán — Khiếu nại chargeback** | Ngoài phạm vi đặc tả (`docs/spec.md:27` ngoài phạm vi "lưu thông tin thẻ"; `docs/spec.md:39` không có UC chargeback) | **Ngoài phạm vi** | Không lưu/xử lý thẻ ngân hàng; tranh chấp đơn hàng thuộc `UC-A05` (ngoài phạm vi worker `pay`). |
| **Thanh toán — Phân tích gian lận** | `UC-A16` (Thấp, `docs/spec.md:59`), `UC-PAY02` (`docs/spec.md:111`) | **Ngoài phạm vi `pay`** | `UC-A16` mức Thấp (đơn ảo/voucher, thuộc `ord`/`prm`); chống bom COD nằm ở `UC-PAY02` (`services/order`). |
| **Thanh toán — Đối soát và payout** | `UC-PAY07`, `UC-PAY03`, `UC-A14`, `UC-PAY05` | **MỘT PHẦN / THIẾU** | Có đối soát online cơ bản (`PAY-B8`); thiếu đối soát COD (`UC-PAY03`), thiếu xử lý hàng đợi (`UC-A14`), thiếu payout (`UC-PAY05`, Thấp). |
| **Tài chính — Tài khoản số dư (Balance)** | `UC-S17` (`docs/spec.md:134`) | **MỘT PHẦN** | Có API `GET /api/v1/seller/balance` dựa trên sổ cái; thiếu sổ giao dịch chi tiết theo shop & khai báo TK ngân hàng; lệch số khi có voucher sàn (`PAY-B3`). |
| **Tài chính — Vay vốn** | Ngoài phạm vi đặc tả (`docs/spec.md:39` không có trong tab Use case) | **Ngoài phạm vi** | Không cài đặt. |
| **Tài chính — Thuế / Hóa đơn điện tử** | Ngoài phạm vi đặc tả (`docs/spec.md:27` ngoài phạm vi "hoá đơn điện tử"; `docs/spec.md:117` UC-PAY08) | **Ngoài phạm vi** | Không cài đặt ở backend (web chỉ có phần mock VAT ở ngoài luồng thật). |
| **Tài chính — Báo cáo tài chính** | `UC-S06` (`docs/spec.md:123`), `UC-A06` (`docs/spec.md:49`); BI nâng cao ngoài phạm vi (`docs/spec.md:26`) | **MỘT PHẦN** | Có `GET /api/v1/admin/ledger/summary` (bảng cân đối tài khoản sổ cái); chưa có xuất báo cáo đối soát/tài chính CSV/PDF. |
| **Tiền của sàn — Hoa hồng** | `UC-A09` (`docs/spec.md:52`) | **MỘT PHẦN** | Có cấu hình `commission_rate_bps` theo danh mục (`500 bps` mặc định), snapshot vào đơn, ghi `platform_fee_revenue`; thiếu ngày hiệu lực & audit log. |
| **Tiền của sàn — Phí dịch vụ / phí thanh toán** | `UC-A09` (`docs/spec.md:52`: "và phí thanh toán") | **THIẾU** | Chưa có khái niệm/cấu hình/bút toán phí thanh toán hay phí dịch vụ khác ngoài hoa hồng danh mục. |
| **Tiền của sàn — Giữ tiền chờ giao thành công (escrow)** | `UC-B17` (`docs/spec.md:90`), `UC-PAY05` (`docs/spec.md:114`) | **LỖI / MỘT PHẦN** | Tiền có giữ ở `escrow_liability` tới khi `confirm-received`, nhưng giải ngân thẳng vào `seller_available` (không giữ chờ `seller_pending`, giải ngân cả COD chưa đối soát — `PAY-B7`) và rút vượt khi có hoàn một phần (`PAY-B2`). |
| **Tiền của sàn — Đối soát và chi trả cho người bán** | `UC-PAY03`, `UC-PAY05`, `UC-S17` | **THIẾU / MỘT PHẦN** | Thiếu đối soát COD, thiếu luồng rút tiền/duyệt payout, thiếu quản lý TK ngân hàng seller. |

---

## 3. Danh sách lỗi (`PAY-B<n>`)

### `PAY-B1` [P0] — Retry payment `FAILED` tạo giao dịch thu tiền hợp lệ trên đơn hàng đã bị `CANCELLED` (mất tiền người mua)
- **Tái hiện**:
  1. Tạo đơn VietQR (`$ORDER_ID`, `$PAYMENT_ID`, `$GROUP_ID`, `$AMOUNT`).
  2. Bắn webhook `FAILED` ký HMAC hợp lệ → chờ ~1s để `order` chuyển đơn sang `CANCELLED` (và nhả tồn kho/khuyến mãi):
     ```bash
     curl -sS -X POST http://localhost:48080/webhooks/vietqr \
       -H "Content-Type: application/json" -H "X-Signature: $SIG" -H "X-Timestamp: $TS" \
       -d "{\"provider\":\"vietqr_mock\",\"provider_tx_id\":\"TX-FAIL-1\",\"payment_id\":\"$PAYMENT_ID\",\"order_group_id\":\"$GROUP_ID\",\"amount_vnd\":0,\"status\":\"FAILED\"}"
     ```
  3. Gọi retry trên payment đã `FAILED`:
     ```bash
     curl -sS -X POST http://localhost:48080/api/v1/payments/$PAYMENT_ID/retry \
       -H "Authorization: Bearer $BUYER_TOKEN" -H "Content-Type: application/json" \
       -d '{"method":"VIETQR"}'
     ```
  4. Bắn webhook `SUCCEEDED` với `$NEW_PAYMENT_ID` vừa trả về.
- **Mong đợi**: Bước 3 phải bị từ chối (`409 Conflict`) vì đơn hàng đã chuyển `CANCELLED` và tồn kho đã nhả (theo UC-B25 tiền điều kiện "Đơn pending"). Hoặc nếu tiền vẫn về sau khi hủy, phải tự động hoàn tiền (`late_payment_refund`).
- **Kết quả thật**: Bước 3 trả **`200 OK`** với payment mới `12ee177e-fcdd-4cea-afc1-488325dba7cb` (`PENDING`). Bước 4 trả **`200 OK`** (`status: "processed"`), `payment_db` ghi `status = 'succeeded'` và hạch toán `Debit gateway_clearing 255,000 / Credit escrow_liability 255,000`, trong khi đơn `d1251d5a-61c2-4b99-a70f-2cb1d1ed89b3` trong `order_db` **vẫn `CANCELLED` (`payment_status = 'FAILED'`)** — người mua mất tiền, không có đơn và không có hoàn tiền tự động.
- **Gốc nghi ngờ**: `services/gateway/internal/router/handlers_payment.go:252-283` cho phép retry khi `existing.Status == PAYMENT_STATUS_FAILED` mà không kiểm tra trạng thái đơn ở `OrderService`; `services/payment/src/service.rs:240-285` chỉ kiểm tra có payment `succeeded` chứ không kiểm tra đơn đã hủy.

### `PAY-B2` [P0] — Hoàn tiền một phần hoặc sau khi hoàn tất đơn làm `escrow_liability` bị rút vượt và chi trả thừa cho người bán
- **Tái hiện**:
  1. Tạo đơn VietQR 2 gian hàng (`c1c77d2d-4202-40d0-8ed8-f16fdc92f728`, tổng `718,000₫`: shop1 `255,000₫`, shop2 `463,000₫`), thanh toán `SUCCEEDED`.
  2. *Trường hợp A (hoàn một phần trước khi hoàn tất)*: Admin hoàn `10,000₫` cho đơn con shop2 (`11374f99-fad1-4a48-a2ab-3cdeb66401d5`):
     ```bash
     curl -sS -X POST http://localhost:48080/api/v1/admin/payments/$PAYMENT_ID/refund \
       -H "Authorization: Bearer $ADMIN_TOKEN" -H "Idempotency-Key: rf-partial-1" \
       -H "Content-Type: application/json" \
       -d "{\"order_id\":\"$ORD_SHOP2\",\"amount_vnd\":10000,\"reason\":\"Partial refund\"}"
     ```
     Sau đó ship → giao → buyer `confirm-received` đơn con shop2.
  3. *Trường hợp B (hoàn tiền sau khi đã `COMPLETED` và giải ngân)*: Sau khi đơn con shop1 (`70d205a0-...`) đã `COMPLETED` (seller1 đã nhận `217,550₫` vào `seller_available`), Admin gọi `POST /api/v1/admin/payments/$PAYMENT_ID/refund` hoàn `5,000₫` cho đơn con shop1.
- **Mong đợi**:
  - Trường hợp A: Khi `ReleaseEscrow`, chỉ được quyết toán phần còn lại trong escrow (`463,000 - 10,000 = 453,000₫`), không được rút quá tổng tiền đơn đã đóng vào `escrow_liability`.
  - Trường hợp B: Khi `escrow_status == 'released'`, phải chặn hoặc trừ ngược vào `seller_available:<shop_id>` / `platform_fee_revenue`, tuyệt đối không rút thêm từ `escrow_liability` của đơn khác.
- **Kết quả thật**:
  - Trường hợp A: `ReleaseEscrow` rút trọn **`463,000₫`** từ `escrow_liability` và trả đủ **`407,550₫`** cho seller2 (dù đã hoàn `10,000₫`).
  - Trường hợp B: `POST .../refund` trả **`200 OK`** (`refund_id = cbb9d6da-...`, `status = DONE`), rút tiếp `5,000₫` từ `escrow_liability` trong khi seller1 giữ nguyên `217,550₫`.
  - Tổng rút khỏi `escrow_liability` cho payment `718,000₫` lên tới **`733,000₫`** (`255,000 + 10,000 + 463,000 + 5,000`), rút lẹm `15,000₫` từ quỹ escrow của các đơn khác.
- **Gốc nghi ngờ**: `services/payment/src/service.rs:2305-2323` (`settle_order_to_pending_tx`) không trừ tổng tiền đã hoàn của sub-order trước khi quyết toán; `services/payment/src/service.rs:985-1090` (`create_refund`) không kiểm tra `payment_shop_allocations.escrow_status` (vẫn cho debit `escrow_liability` kể cả khi `escrow_status = 'released'`).

### `PAY-B3` [P0] — Voucher toàn sàn (`PLATFORM`) và Xu bị trừ thẳng vào doanh thu thực nhận của người bán
- **Tái hiện**:
  1. Buyer tạo đơn VietQR 1 sản phẩm (`subtotal = 229,000₫`, `shipping = 26,000₫`, `commission 5% = 11,450₫`) áp voucher toàn sàn giảm `30,000₫` (`total_payable_vnd = 225,000₫`):
     ```bash
     curl -sS -X POST http://localhost:48080/api/v1/checkout \
       -H "Authorization: Bearer $BUYER_TOKEN" -H "Idempotency-Key: platv-1" \
       -H "Content-Type: application/json" \
       -d "{\"address_id\":\"$ADDR_ID\",\"from_cart\":false,\"items\":[{\"sku_id\":\"$SKU_ID\",\"quantity\":1}],\"platform_voucher_id\":\"$PLATFORM_CODE\",\"payment_method\":\"VIETQR\"}"
     ```
  2. Thanh toán → ship → giao → buyer `confirm-received`.
  3. Kiểm tra `GET /api/v1/seller/balance` và `GET /api/v1/payments/$PAYMENT_ID`.
- **Mong đợi**: Theo `docs/spec.md:88` (UC-B15: "Đơn lưu chi tiết từng khoản giảm và bên chịu chi phí (sàn hay gian hàng)"), voucher sàn và xu do **sàn trợ giá**; người bán phải nhận `229,000 - 11,450 = 217,550₫` (phần `30,000₫` sàn hạch toán từ tài khoản chi phí khuyến mãi sàn), khớp với công thức hiển thị trên `/seller/tai-chinh`.
- **Kết quả thật**: Đơn `19a24ddb-9d3d-44f6-86e3-a951b14884d5` gửi sang payment `shop_allocations.subtotal_vnd = 199,000₫` (đã bị trừ `30,000₫` voucher sàn). Khi hoàn tất, `seller_available` chỉ tăng **`187,550₫`** (thiệt hại đúng `30,000₫` cho người bán), lệch so với `/seller/tai-chinh` tính **`217,550₫`**.
- **Gốc nghi ngờ**: `services/order/internal/service/checkout.go:1144-1154` tính `allocSub = ord.TotalPayableVND - allocShip` (gộp trừ cả `PlatformVoucherDiscountVND` và `CoinsDiscountVND` vào phần của shop); `services/payment/src/ledger.rs` chưa có tài khoản chi phí trợ giá sàn (`platform_promo_expense`).

### `PAY-B4` [P0] — Hủy đơn COD không đảo ngược sổ cái (`cod_receivable`/`escrow_liability`), payment vẫn `SUCCEEDED`, và Admin hoàn tiền được trên đơn COD chưa trả online
- **Tái hiện**:
  1. Tạo đơn COD (`e05ab0bc-8e97-4796-ba10-d38d1ef11590`, `pCod = d6460d4f-07bc-45be-b208-7c4d61ecca91`, `256,500₫`). Kiểm tra `GET /api/v1/admin/ledger/summary`: `cod_receivable` tăng ngay `+256,500₫` (`1,210,000 -> 1,466,500₫`).
  2. Hủy đơn COD:
     ```bash
     curl -sS -X POST http://localhost:48080/api/v1/orders/$ORD_COD/cancel \
       -H "Authorization: Bearer $BUYER_TOKEN" -H "Content-Type: application/json" \
       -d '{"reason":"Cancel COD"}'
     ```
  3. Kiểm tra `GET /api/v1/payments/$P_COD` và `GET /api/v1/admin/ledger/summary`.
  4. Admin thử hoàn tiền trên đơn COD đã hủy:
     ```bash
     curl -sS -X POST http://localhost:48080/api/v1/admin/payments/$P_COD/refund \
       -H "Authorization: Bearer $ADMIN_TOKEN" -H "Idempotency-Key: rf-cod-1" \
       -H "Content-Type: application/json" \
       -d "{\"order_id\":\"$ORD_COD\",\"amount_vnd\":1000,\"reason\":\"Test COD refund\"}"
     ```
- **Mong đợi**: Hủy đơn COD phải hủy giao dịch payment (hoặc không ghi công nợ `cod_receivable` trước khi giao/thu hộ) và đảo ngược bút toán `Debit escrow_liability / Credit cod_receivable`; cấm tạo refund tiền mặt/online trên phương thức `COD` (`409 Conflict`).
- **Kết quả thật**:
  - Sau khi đơn COD chuyển `CANCELLED`: `pCod.status` **vẫn `SUCCEEDED`**, `payment_shop_allocations.escrow_status` **vẫn `held`**, `cod_receivable` **giữ nguyên `1,466,500₫`** vĩnh viễn.
  - Bước 4 trả **`200 OK`** (`refund_id = 37430192-...`, `status = DONE`), ghi có `refund_payable +1,000₫` dù người mua chưa từng chuyển tiền.
- **Gốc nghi ngờ**: `services/payment/src/service.rs:194,380` đánh dấu COD là `succeeded` ngay lúc tạo đơn; `service.rs:1889` (`process_order_cancelled_event`) chỉ xử lý `"succeeded" if payment.method == "VIETQR"` và bỏ qua COD (`_ => {}`); `service.rs:985` (`create_refund`) không kiểm tra `payment.method != "COD"`; `services/order/internal/service/lifecycle.go:315-324` gọi `CancelPayment` cho COD bị fail im lặng (bỏ lỗi `_ =`) vì payment đang `succeeded`.

### `PAY-B5` [P1] — `POST /api/v1/payments/{id}/retry` không đồng bộ `orders.payment_id` và `payment_expires_at` về `order_db`
- **Tái hiện**:
  1. Tạo đơn VietQR (`ordA = 5602d03d-...`, `pA1 = 8db65c92-...`).
  2. Gọi `POST /api/v1/payments/$PA1/retry` với `{"method":"VIETQR"}` → nhận `pA2 = c06c975b-...` (`PENDING`).
  3. Gọi `GET /api/v1/orders/$ORD_A` và `GET /api/v1/payments/$PA1` trước khi thanh toán `pA2`.
- **Mong đợi**: `order.payment_id` trỏ sang `pA2` (và hạn thanh toán khớp `pA2.expires_at`), để tải lại `/don-hang/[id]` vẫn hiển thị mã QR `pA2` và watchdog hết hạn ở `order` kiểm tra đúng `pA2`.
- **Kết quả thật**: `GET /api/v1/orders/$ORD_A` vẫn trả `payment_id = 8db65c92-...` (`pA1`), trong khi `GET /api/v1/payments/$PA1` trả `status = CANCELLED` (`superseded`). Nếu người mua tải lại `/don-hang/[id]`, `RetryPaymentBox` nạp `pA1` (`CANCELLED`) nên ẩn QR lẫn nút retry (`canRetry = false`). Đồng thời, `Order.processExpiredGroup` (`services/order/internal/service/handlers.go:278-281`) gọi `Payment.GetPayment(PaymentId: pA1, OrderGroupId: grpA)` mà `service.rs:435-439` ưu tiên tra theo `payment_id` (bỏ qua `order_group_id`), nên watchdog sẽ thấy `pA1 = CANCELLED` và hủy đơn ngay cả khi `pA2` còn hạn hoặc vừa thành công trước khi event NATS về tới `order`.
- **Gốc nghi ngờ**: `services/gateway/internal/router/handlers_payment.go:273-289` chỉ gọi `Payment.CreatePayment` mà không cập nhật `order`; `services/payment/src/service.rs:435-439` không fallback sang payment active mới nhất trong `order_group_id` khi tra bằng `payment_id` đã `superseded`.

### `PAY-B6` [P1] — Webhook trễ của payment cũ (`superseded`) và hoàn tiền một phần ghi đè `orders.payment_status = 'REFUNDED'` lên đơn đang `CONFIRMED`/`COMPLETED`
- **Tái hiện**:
  - *Nhánh 1 (late webhook sau retry)*: Tiếp nối `PAY-B5`, thanh toán `pA2` thành công (`ordA` chuyển `CONFIRMED`, `payment_status = SUCCEEDED`, `payment_id = pA2`). Sau đó bắn webhook `SUCCEEDED` trễ cho `pA1` (đã `superseded`). Kiểm tra `GET /api/v1/orders/$ORD_A`.
  - *Nhánh 2 (hoàn tiền một phần)*: Hoàn `10,000₫` trên đơn con `463,000₫` (`11374f99-...`). Kiểm tra `GET /api/v1/orders/$ORD_SHOP2`.
- **Mong đợi**: Nhánh 1 phải giữ `ordA.payment_status = 'SUCCEEDED'` (vì `pA2` vẫn là giao dịch hợp lệ của đơn, chỉ hoàn tiền cho `pA1` dư thừa). Nhánh 2 không được ghi `REFUNDED` toàn phần khi mới hoàn một phần (phải là `PARTIALLY_REFUNDED` hoặc giữ `SUCCEEDED` kèm tổng đã hoàn).
- **Kết quả thật**: Cả hai nhánh đều ghi đè `orders.payment_status = 'REFUNDED'` (`ordA` đang `CONFIRMED` với `pA2 = SUCCEEDED`, `ordShop2` chỉ hoàn `10,000 / 463,000₫` với `payment_db.status = 'succeeded'`).
- **Gốc nghi ngờ**: `services/payment/src/service.rs:825-839` phát `payment.refunded` cho late payment với `order_id = p.order_id` (= `checkout_group_id`), không có cờ phân biệt `is_late_duplicate` hay `is_full_refund`; `services/order/internal/store/postgres.go:962-995` (`RecordPaymentRefunded`) luôn cập nhật vô điều kiện `payment_status = 'REFUNDED'` cho mọi đơn khớp `order_id` hoặc `checkout_group_id`.

### `PAY-B7` [P1] — `ReleaseEscrow` bỏ qua thời gian giữ tiền chờ (`seller_pending`) và giải ngân ngay vào `seller_available` kể cả đơn COD chưa đối soát thu hộ
- **Tái hiện**:
  1. Hoàn tất đơn VietQR `70d205a0-...` (`confirm-received`) → kiểm tra `GET /api/v1/seller/balance` và `ledger_entries` cho shop `61a74907-...`.
- **Mong đợi**: Theo `docs/spec.md:90,112,114` (UC-B17, UC-PAY03, UC-PAY05): khi đơn `COMPLETED`, tiền vào `seller_pending` chờ hết thời gian giữ tiền (escrow hold) và không có khiếu nại mới chuyển sang `seller_available`; riêng đơn COD chỉ ghi nhận cho seller sau khi đối soát thu hộ từ hãng vận chuyển (UC-PAY03).
- **Kết quả thật**: `seller_pending:61a74907-...` có `debit = 217,550`, `credit = 217,550`, **`balance = 0`** ngay lập tức; toàn bộ `217,550₫` chuyển thẳng sang `seller_available`. Cùng cơ chế này (`lifecycle.go:644-658`) cũng gọi thẳng `ReleaseEscrow` cho đơn COD khi buyer `confirm-received` dù `cod_receivable` chưa hề được hãng vận chuyển đối soát (`cod_receivable` credit = 0).
- **Gốc nghi ngờ**: `services/order/internal/service/lifecycle.go:644-658` gọi trực tiếp `Payment.ReleaseEscrow` ngay trong `BuyerConfirmReceived`; `services/payment/src/service.rs:1170-1260` chạy liền cả `settle_order_to_pending_tx` và `build_escrow_release_tx` trong cùng 1 transaction, không có trạng thái/job hẹn ngày nhả escrow và không kiểm `payment.method == 'COD'`.

### `PAY-B8` [P2] — Đối soát ngày (`UC-PAY07`) lệch múi giờ VN/UTC, `PayOs` luôn trả rỗng và `Mock` mất dữ liệu sau restart
- **Tái hiện**:
  - Lúc `2026-11-23 01:22+07:00` (`2026-11-22 18:22Z`), gọi `POST /api/v1/admin/payments/reconcile` với ngày mặc định mà Web `/admin/doi-soat` điền (`2026-11-23`) so với ngày UTC (`2026-11-22`):
    ```bash
    curl -sS -X POST http://localhost:48080/api/v1/admin/payments/reconcile \
      -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" -d '{"date":"2026-11-23"}'
    curl -sS -X POST http://localhost:48080/api/v1/admin/payments/reconcile \
      -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" -d '{"date":"2026-11-22"}'
    ```
- **Mong đợi**: Chạy đối soát bằng ngày mặc định trên `/admin/doi-soat` khớp đúng các giao dịch trong ngày hôm đó theo giờ Việt Nam (`Asia/Ho_Chi_Minh`); dữ liệu đối soát bền vững và hoạt động ở cả `payos`/`sepay`.
- **Kết quả thật**: Ngày `2026-11-23` trả `matched_count: 0` (run `f68421f7-...`), trong khi `2026-11-22` trả `matched_count: 5` (run `fac0cd92-...`). Ngoài ra soát code: `VietQrMode::PayOs` trả `Ok(Vec::new())` (`vietqr.rs:236`) khiến mọi giao dịch bị gắn `missing_provider`; `VietQrMode::Sepay` gán `occurred_at: Utc::now()` (`vietqr.rs:470`) không lọc được ngày quá khứ; `VietQrMode::Mock` giữ `mock_transactions` trong RAM nên khởi động lại container sẽ báo toàn bộ giao dịch cũ là `missing_provider`.
- **Gốc nghi ngờ**: `web/src/app/(admin)/admin/doi-soat/page.tsx:33-35` dùng `formatVnDayKey` (UTC+7) còn `services/payment/src/service.rs:1496,1517` so sánh `created_at::date = $1` (UTC); `services/payment/src/vietqr.rs:230-249,470`.

### `PAY-B9` [P2] — Hàng đợi giao dịch không khớp (`UC-A14`) không nối qua Gateway và không có thao tác xử lý / UI Admin
- **Tái hiện**:
  1. Bắn webhook hợp lệ lệch số tiền `717,000₫` cho đơn `718,000₫` → `200 OK` (`queued_for_reconciliation:amount_mismatch`), `reconciliation_queue` có dòng `85336e7c-...`.
  2. Thử truy cập qua Gateway hoặc `/admin/doi-soat`: không có endpoint `/api/v1/admin/reconciliation/queue` hay `/api/v1/admin/reconciliation/queue/{id}/resolve`; chỉ đọc được ở `GET http://localhost:48086/internal/reconciliation/queue` (cổng nội bộ).
- **Mong đợi**: Theo `docs/spec.md:57` (UC-A14), Admin kế toán mở hàng đợi trên Admin Portal, tìm đơn, gán giao dịch vào đơn / đánh dấu hoàn tiền / treo, phát event và ghi audit log.
- **Kết quả thật**: Không có RPC trong `proto/nexusmart/payment/v1/payment.proto`, không có route Gateway (`services/gateway/internal/router/router.go:271-275`), không có UI trên `/admin/doi-soat`.
- **Gốc nghi ngờ**: Chưa triển khai tầng gRPC/Gateway/Web và nghiệp vụ `resolve` cho bảng `reconciliation_queue` (`services/payment/migrations/0001_init.sql:203-217`).

### `PAY-B10` [P2] — UI `RetryPaymentBox` hiển thị nút "Thanh toán lại qua VietQR" cho trạng thái `EXPIRED` nhưng backend luôn từ chối `409`
- **Tái hiện**: Xem `web/src/features/commerce/RetryPaymentBox.tsx:300-303` (`canRetry` bật cho `EXPIRED`) và `web/src/app/(shop)/thanh-toan/cho-thanh-toan/[paymentId]/PaymentWaitingClient.tsx:157` hướng dẫn tạo lại mã QR khi hết hạn; đối chiếu `services/gateway/internal/router/handlers_payment.go:252-256` chỉ cho phép `PENDING`/`FAILED` (trả `409 FAILED_PRECONDITION` cho `EXPIRED`).
- **Mong đợi**: Theo UC-B25, giao dịch đã quá hạn thì đơn đã bị hủy, UI phải ẩn nút tạo lại QR và hiển thị nút "Mua lại" thay vì gọi API để nhận lỗi `409`.
- **Kết quả thật**: UI hiển thị nút gọi `POST /api/v1/payments/{id}/retry` và chỉ báo lỗi sau khi người dùng bấm.
- **Gốc nghi ngờ**: `web/src/features/commerce/RetryPaymentBox.tsx:300-303` và `PaymentWaitingClient.tsx:157`.

### `PAY-B11` [P2] — Khoản `refund_payable` không có bước tất toán chi tiền và bản ghi hoàn tiền `pending` từ webhook trễ bị kẹt vĩnh viễn
- **Tái hiện**:
  1. Bắn webhook trễ cho `pA1` (`superseded`) → bảng `refunds` tạo dòng `a2a2bb3f-...` với `status = 'pending'`, `amount_vnd = 255,000`.
  2. Hoàn tiền thủ công `10,000₫` (`073ffa7b-...`) → `refunds.status = 'completed'`, ghi `Debit escrow_liability / Credit refund_payable`.
  3. Kiểm tra `GET /api/v1/admin/ledger/summary`: `refund_payable` chỉ có `credit = 464,000₫`, `debit = 0₫`; `gateway_clearing` `credit = 0₫`.
- **Mong đợi**: Theo `docs/spec.md:113` (UC-PAY04), hoàn tiền QR phải cho buyer nhập TK nhận, admin duyệt & xác nhận chuyển khoản (hạch toán `Debit refund_payable / Credit gateway_clearing`), chuyển trạng thái `pending -> completed` và ghi audit log.
- **Kết quả thật**: `build_refund_settle_tx` (`services/payment/src/ledger.rs:271-292`) là dead code; không có API/UI xem hoặc tất toán các bản ghi `refunds` đang `pending`; `CreateRefund` cũng không ghi audit log.
- **Gốc nghi ngờ**: `services/payment/src/service.rs:808-824,1048-1091`; thiếu RPC `ListRefunds` / `SettleRefund`.

### `PAY-B12` [P3] — Bảng đối chiếu ở `/seller/tai-chinh` sai công thức dòng tổng cộng và mô tả sai ý nghĩa `pending_vnd`
- **Tái hiện**: Soát `web/src/app/(seller)/seller/tai-chinh/page.tsx:88-95,168-175,206-210,256-260,306-318`.
- **Mong đợi**: Dòng tổng cột "Chiết khấu & phí sàn" và công thức đầu bảng phải cộng đủ `flash_sale_discount_vnd + shop_voucher_discount_vnd + commission_vnd` khớp từng dòng đơn; mô tả thẻ `pending_vnd` phải đúng bản chất sổ cái (`seller_pending`).
- **Kết quả thật**: Từng dòng bảng hiển thị `deductions = flash + shop_voucher + commission`, nhưng header và `<tfoot>` chỉ hiển thị `visibleCommission` (bỏ qua chiết khấu shop & flash sale), nên `visibleSubtotal - visibleCommission != visibleNet`. Thẻ `pending_vnd` ghi chú "Tạm giữ cho các đơn đang xử lý & giao" dù giá trị thực tế bằng `0₫` cho các đơn đang giao (`escrow_liability` không nằm trong `pending_vnd`).
- **Gốc nghi ngờ**: `web/src/app/(seller)/seller/tai-chinh/page.tsx:88-91,174,207-209,311-315`.

---

## 4. Vấn đề dữ liệu (`PAY-D<n>`)

- **`PAY-D1` — Không nhất quán trạng thái giữa `order_db` và `payment_db`**: Kiểm tra đối chiếu trực tiếp 6 đơn của `audit-pay-1`:
  - `11374f99-...` (hoàn một phần `10,000/463,000₫`): `order_db.payment_status = REFUNDED` vs `payment_db.status = succeeded`.
  - `5602d03d-...` (đã trả bằng `P2`, bị late webhook `P1`): `order_db.status = CONFIRMED, payment_status = REFUNDED` vs `payment_db` `P2.status = succeeded`.
  - `d1251d5a-...` (retry `FAILED`): `order_db.status = CANCELLED, payment_status = FAILED` vs `payment_db` có `12ee177e-...` `status = succeeded` (`255,000₫`).
  - `e05ab0bc-...` (COD hủy): `order_db.status = CANCELLED` vs `payment_db.status = succeeded`, `escrow_status = held`.
- **`PAY-D2` — Lệch số dư seller giữa bảng `/seller/tai-chinh` và ledger khi có voucher sàn**: Đơn `19a24ddb-...` (`subtotal 229,000₫`, `platform_voucher 30,000₫`, `commission 11,450₫`) hiển thị `217,550₫` ở `computeOrderNetVnd` nhưng ledger chỉ ghi `187,550₫` vào `seller_available` (lệch `30,000₫` do `PAY-B3`). Riêng với đơn không có voucher sàn/xu (`70d205a0-...`), số tiền khớp tuyệt đối giữa `order_db` (`255,000₫`), `payment_db` (`255,000₫`), Web và ledger (`217,550₫`).
- **`PAY-D3` — Lệch tỷ lệ hoa hồng danh mục giữa DB seed và tab mock trên `/admin/cau-hinh`**: `product_db.categories` (seed từ `tools/seed/seed.mjs:574`) đặt cả 6 danh mục là `500 bps` (`5,0%`, hiển thị ở tab "Danh mục & hoa hồng"), còn tab mock "Đổi trả & hoa hồng" (`web/src/data/admin.ts:622,625`) hiển thị `Quần jogger = 5,5%`, `Đồ lót = 6,0%`.
- **`PAY-D4` — Dữ liệu cấu hình/lịch sử/ngân hàng mock trên Web mâu thuẫn với hệ thống thật**: `/admin/cau-hinh` tab "Thanh toán & bảo vệ" hiện cổng "Ví thanh toán nội bộ NexusPay" (không tồn tại) và hạn thanh toán lại `30 phút` (thực tế `PAYMENT_EXPIRY_MINUTES=15`); `/admin/doi-soat` tab 3 hiện lô mẫu `ADMIN_RECONCILIATION` (có kênh `vi_sandbox` và `cod`) không có trong `reconciliation_runs`; `/seller/tai-chinh` hiện tài khoản ngân hàng tĩnh mẫu.
- **`PAY-D5` — Sổ cái ghi kép đúng cấu trúc kỹ thuật nhưng số dư nghiệp vụ bị nhiễm sai số**: SQL kiểm tra toàn bộ `payment_db` xác nhận **0 cột kiểu số thực** (`real`/`double`/`numeric`), **28/28 `ledger_transactions` cân bằng** (`unbalanced_tx = 0`, `SUM(debit) = SUM(credit) = 7,156,290₫`), `GET /api/v1/seller/balance` khớp 100% SQL trên `ledger_entries`. Tuy nhiên do `PAY-B1`, `PAY-B2`, `PAY-B4`, `PAY-B11`, số dư thực tế các tài khoản `escrow_liability` (bị rút vượt `15,000₫` ở payment `c1c77d2d-...`), `cod_receivable` (tồn `256,500₫` đơn hủy + chưa tất toán) và `refund_payable` (`464,000₫` chưa từng debit) bị sai lệch nghiệp vụ.

---

## 5. Đề xuất sửa (`PAY-F<n>`)

| Mã | Giải quyết | Công việc cụ thể | Cỡ | Proto / Migration / ADR |
| --- | --- | --- | --- | --- |
| **`PAY-F1`** | `PAY-B1`, `PAY-B5`, `PAY-B10` (UC-B25) | Trong `services/gateway/internal/router/handlers_payment.go:handleRetryPayment`: gọi `Order.GetOrder` kiểm tra mọi đơn con của `order_group_id` còn `PENDING` & chưa quá hạn trước khi cho retry (chặn retry khi đơn `CANCELLED` hoặc `payment == FAILED`); thêm RPC nội bộ `OrderService.UpdatePendingPayment` cập nhật `orders.payment_id` & `payment_expires_at` đồng bộ sau retry; sửa `services/payment/src/service.rs:get_payment` khi nhận cả `payment_id` và `order_group_id` nếu `payment_id` đã `superseded` thì ưu tiên trả bản ghi `succeeded`/`pending` mới nhất của group; bỏ `EXPIRED` khỏi `canRetry` ở `web/src/features/commerce/RetryPaymentBox.tsx:300-303` & sửa copy `PaymentWaitingClient.tsx:157`. | **M** | Cần đổi `order.proto` (thêm RPC nội bộ + `internalRPCs`); không migration, không ADR. |
| **`PAY-F2`** | `PAY-B2`, `PAY-B6`, `PAY-B11` (UC-PAY04) | Trong `services/payment/src/service.rs`: (1) `create_refund` kiểm tra `payment.method != "COD"` và chặn nếu `escrow_status != 'held'` (hoặc thêm bút toán thu hồi `seller_available` nếu hoàn sau release); (2) `settle_order_to_pending_tx` trừ tổng `refunds` đã ghi nhận của `order_id` khỏi số tiền giải ngân; (3) phân biệt `refund_kind` (`partial` / `full` / `late_duplicate`) trong event `payment.refunded` để `services/order/internal/store/postgres.go:RecordPaymentRefunded` không ghi đè `REFUNDED` lên đơn còn hiệu lực hoặc mới hoàn một phần (`PARTIALLY_REFUNDED`); (4) thêm RPC `ListRefunds` + `SettleRefund` gọi `build_refund_settle_tx` (và lưu TK ngân hàng nhận hoàn cho VietQR) + ghi audit log. | **L** | Cần đổi `payment.proto`, thêm migration `0002_refund_settlement.sql` (cột TK nhận hoàn/loại hoàn), cập nhật `docs/contracts.md`; không ADR. |
| **`PAY-F3`** | `PAY-B3`, `PAY-D2` (Tiền của sàn / UC-S17) | Trong `services/order/internal/service/checkout.go:buildCreatePaymentRequest` và `services/payment/src/ledger.rs`: tách rõ `platform_subsidy_vnd = platform_voucher_discount_vnd + coins_discount_vnd` trên `ShopAllocation`, giữ nguyên `subtotal_vnd = gross_subtotal - flash_sale - shop_voucher` cho shop; thêm tài khoản sổ cái `platform_promo_expense` (`EXPENSE`) để khi xác nhận thanh toán ghi `Debit gateway_clearing (payable) + Debit platform_promo_expense (subsidy) / Credit escrow_liability (payable + subsidy)`, đảm bảo người bán nhận đủ tiền hàng trừ chiết khấu shop & hoa hồng, khớp `/seller/tai-chinh`. | **M** | Cần đổi `payment.proto` (`ShopAllocation`), thêm migration `0003_platform_promo_account.sql` seed `platform_promo_expense`; không ADR. |
| **`PAY-F4`** | `PAY-B4`, `PAY-B7`, `UC-PAY03` | Sửa vòng đời COD và escrow: (1) không đánh dấu COD `succeeded` khi chưa giao, hoặc khi `order.cancelled` / `order.returned` đối với COD thì `process_order_cancelled_event` chuyển payment sang `cancelled`, đánh dấu allocation `refunded` và ghi bút toán đảo `Debit escrow_liability / Credit cod_receivable`; (2) bỏ lời gọi `ReleaseEscrow` tức thì trong `Order.BuyerConfirmReceived` (chỉ phát `order.completed` để `payment` chuyển `held -> pending_release` vào `seller_pending`), bổ sung worker nhả `seller_pending -> seller_available` sau `ESCROW_HOLD_DAYS` nếu không có khiếu nại; (3) triển khai `UC-PAY03`: RPC/endpoint `ReconcileCOD` (import/đối soát theo `tracking_code` + `cod_amount_vnd` với `shipping`), gọi `build_cod_reconcile_tx` rồi mới cộng `seller_pending`. | **L** | Cần đổi `payment.proto`, thêm migration bảng `cod_reconciliation_batches`/`items` và cột `release_eligible_at` trên `payment_shop_allocations`; không ADR. |
| **`PAY-F5`** | `PAY-B8`, `PAY-B9`, `UC-PAY07`, `UC-A14` | (1) Chuẩn hóa múi giờ `Asia/Ho_Chi_Minh` (`AT TIME ZONE 'Asia/Ho_Chi_Minh'`) trong `run_reconciliation` và `vietqr.rs` (sửa `Sepay` parse `transaction_date`, `PayOs` query theo payment pending/succeeded thay vì trả rỗng, `Mock` đọc thêm từ `webhook_events` thay vì chỉ RAM); (2) thêm RPC `ListReconciliationRuns`, `ListReconciliationQueue`, `ResolveReconciliationQueueItem` (gán đơn / tạo refund / đánh dấu treo + ghi audit log); (3) mở REST Gateway `/api/v1/admin/payments/reconciliation/{runs,queue,queue/{id}/resolve}` và nối thật vào `/admin/doi-soat` (thay tab 3 mock). | **M** | Cần đổi `payment.proto`, cập nhật `docs/contracts.md`; không migration lớn (dùng bảng `reconciliation_*` có sẵn), không ADR. |
| **`PAY-F6`** | `UC-B07` (Ví điện tử sandbox) | Tích hợp 1 ví điện tử sandbox (MoMo hoặc VNPay sandbox theo `docs/spec.md:27,153-155`) theo skill `integrate-provider`: mở rộng `PaymentMethod` (`MOMO`/`VNPAY`), adapter có chế độ `mock` mặc định + `sandbox` qua env (timeout/retry/CB), webhook IPN HMAC + idempotent `provider_tx_id`, thêm tùy chọn ở `/thanh-toan`. | **L** | Cần đổi `payment.proto`, `order.proto`, migration mở rộng `CHECK (method IN (...))` ở `payment_db`/`order_db`; không cần hạ tầng mới (chỉ REST sandbox) nên không bắt buộc ADR trừ khi thêm thành phần hạ tầng mới. |
| **`PAY-F7`** | `UC-S17`, `PAY-B12`, `UC-PAY05` | (1) Thêm bảng `seller_bank_accounts` (OTP qua `user`/`notification`, kiểm khớp tên chủ shop, cờ khóa rút 24h `withdraw_locked_until`) + RPC `ListSellerLedgerEntries` (lọc từ `ledger_entries` theo `shop_id`) và nối `/seller/tai-chinh` (bỏ mock `getShopBankAccount`, sửa công thức footer `visibleDeductions` và mô tả `pending_vnd`); (2) nếu triển khai `UC-PAY05` (Thấp): nối `RequestSellerPayout` (status `pending`) + `AdminApprovePayout` (status `completed`, gọi `build_seller_payout_tx` + audit log). | **L** | Cần đổi `payment.proto`, thêm migration `seller_bank_accounts`; không ADR. |
| **`PAY-F8`** | `UC-A09`, `UC-A13`, `PAY-D3`, `PAY-D4` | (1) `UC-A09`: thêm bảng/RPC lịch hiệu lực phí hoa hồng & phí thanh toán (`fee_schedules` có `effective_from`), ghi audit log khi đổi phí, gỡ bảng hoa hồng mock trùng lặp ở `/admin/cau-hinh` tab `doi_tra`; (2) `UC-A13`: thêm bảng `payment_method_configs` (bật/tắt COD/VietQR/ví, `cod_max_vnd`, `payment_expiry_minutes`, `min_order_vnd`), khóa cổng lưu trong secret store (chỉ trả dạng che), `order`/`checkout` đọc cấu hình động, nối thật tab `thanh_toan` trên `/admin/cau-hinh`. | **L** | Cần đổi `payment.proto`/`product.proto`, thêm migration `fee_schedules` & `payment_method_configs`; nếu dùng KMS/Vault làm secret store cho khóa cổng thì **bắt buộc chạy skill `evaluate-oss` và ghi ADR vào `docs/adr/` trước khi code** (AGENTS.md luật 11). |

---

## 6. Đã kiểm gì, chưa kiểm gì

### Đã kiểm (có bằng chứng thực thi)
- **Code & unit test**: Đọc toàn bộ `services/payment/src/*`, `migrations/0001_init.sql`, `proto/nexusmart/payment/v1/payment.proto`, `services/gateway/internal/router/{router,handlers_payment}.go`, `services/gateway/internal/proxy/proxy.go`, `services/order/internal/service/{checkout,lifecycle,handlers}.go`, `services/order/internal/store/postgres.go`, `services/product/internal/server/server.go`, và toàn bộ trang web liên quan (`/thanh-toan`, `/thanh-toan/cho-thanh-toan/[paymentId]`, `RetryPaymentBox`, `VietQrPreview`, `/seller/tai-chinh`, `/admin/doi-soat`, `/admin/cau-hinh`). Chạy `cargo test` tại `services/payment`: 35/35 pass.
- **Stack `nexusmart-audit` (live API + Web BFF + SQL + NATS)**:
  - VietQR 2 gian hàng (`201`), RBAC `GET /api/v1/payments/{id}` (`401`/`403`/`200`), QR/ref/expiry `+15m`.
  - Route nội bộ `:48086/internal/{ledger/summary,reconciliation/queue,reconciliation/run}` (không token `401`, giả header `x-user-role: admin` `401`, buyer `403`, seller `403`, admin `200`).
  - SSE trực tiếp `:48086/payments/{id}/events` và qua Gateway `:48080/api/v1/payments/{id}/events` (RBAC `401`/`403`/`200` + nhận live event `payment.pending` → `payment.succeeded`).
  - Webhook HMAC: thiếu/sai `X-Signature` (`401`), thiếu `X-Timestamp` (`401`), lệch `±350s` (`401`), lệch số tiền (`queued_for_reconciliation:amount_mismatch` + kiểm `reconciliation_queue`), replay 10x (5 song song + 5 tuần tự → `1 fresh, 9 duplicate`, 1 dòng `webhook_events`, 1 bút toán ledger).
  - Escrow & hoa hồng `500 bps`: hoàn tất đơn con shop1 → kiểm `platform_fee_revenue`, `shipping_fee_payable`, `seller_pending` (`0₫`), `seller_available` (`217,550₫`).
  - Hoàn tiền một phần + replay idempotency + chặn vượt cap (`409`) + hoàn tất đơn sau hoàn một phần (`PAY-B2`) + hoàn tiền sau khi escrow đã release (`PAY-B2`).
  - Retry `PENDING` (chặn buyer khác `403`, chặn đổi `COD` `409`, `orders.payment_id` lệch `PAY-B5`, late webhook `P1` gây `PAY-B6`), retry `FAILED` trên đơn `CANCELLED` (`PAY-B1`), hủy đơn COD + refund trên COD (`PAY-B4`), đơn dùng voucher sàn (`PAY-B3`), đối soát ngày UTC vs VN (`PAY-B8`), CRUD danh mục hoa hồng & audit log (`UC-A09`).
  - Web BFF `:43000`: đăng nhập cookie HttpOnly (buyer/seller/admin), `/api/gw/seller/balance`, `/api/gw/admin/ledger/summary`, chặn `/api/gw/webhooks/payment` (`403 FORBIDDEN_PATH`), SSR `200` các trang `/thanh-toan/cho-thanh-toan/[id]`, `/seller/tai-chinh`, `/admin/doi-soat`, `/admin/cau-hinh`.
  - SQL `payment_db` & `order_db` + NATS JetStream (`NEXUSMART` 131 msg, `NEXUSMART_DLQ` 0 msg, `outbox_events` 23/23 `sent`, 0 lỗi/panic trong log `payment`).

### Chưa kiểm (kèm lý do)
- **Tự hủy thực tế khi hết hạn thanh toán `PAYMENT_EXPIRY_MINUTES=15`** trên stack: không được sửa `.env`/restart stack hoặc sửa DB (luật cứng), nên chỉ xác minh `expires_at = created_at + 15m` trên stack và soát code `scan_expired_payments` (`service.rs:1363-1485`) + `Order.RunExpiryWatchdog` (`handlers.go:255-317`).
- **Tích hợp sandbox thật của payOS / SePay**: stack chạy `VIETQR_MODE=mock` và luật cấm gọi dịch vụ ngoài thật; đã soát trực tiếp code adapter tại `services/payment/src/vietqr.rs:310-539` (phát hiện lỗi `PayOs::list_provider_transactions` trả rỗng và `Sepay` gán `Utc::now()`).
- **Trình duyệt Playwright thao tác click giao diện thực tế**: chỉ kiểm SSR HTML và BFF API qua HTTP trên `:43000` (phần rà soát UI trực quan thuộc worker `web`).
