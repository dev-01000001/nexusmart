# Swarm P — Người bán rút tiền về ngân hàng (UC-PAY05), từ backend tới UI

Đọc trước: `.agents/briefs/shopify-e2e/common.md`.

## Vì sao
Hành trình hiện dừng ở chỗ tiền nằm trong ví `seller_available:<shop>` sau khi đơn hoàn tất (escrow tự giải phóng qua `order.completed`), nhưng
người bán không có cách rút về ngân hàng, admin không có hàng đợi chi trả. Có sẵn khoảng 70%: bảng `seller_payouts`
(`services/payment/migrations/0001_init.sql:253`), `request_seller_payout` (`services/payment/src/service.rs:4877`, đang ghi thẳng
`completed`, phải sửa), `build_seller_payout_tx` (`services/payment/src/ledger.rs:612`), RPC nội bộ `user.GetShopBankAccountForPayout`
(`proto/nexusmart/user/v1/user.proto:948`, có khoá 24h sau khi đổi tài khoản).

## Hợp đồng (manager đã chốt — làm đúng như vậy)
### gRPC `nexusmart.payment.v1.PaymentService` (thêm)
- `RequestSellerPayout(RequestSellerPayoutRequest{shop_id, amount_vnd int64, idempotency_key, requested_by_user_id}) → {SellerPayout payout}`
- `ListSellerPayouts(ListSellerPayoutsRequest{shop_id, status, page, page_size}) → {repeated SellerPayout items, page, page_size, total int64}`
  (`shop_id` rỗng chỉ hợp lệ với admin).
- `DecideSellerPayout(DecideSellerPayoutRequest{payout_id, decision ("APPROVE"|"REJECT"), reason, decided_by_user_id}) → {SellerPayout payout}`
- `message SellerPayout {id, shop_id, amount_vnd int64, status ("PENDING"|"COMPLETED"|"REJECTED"), bank_name, bank_account_masked,
  account_holder_name, reject_reason, transfer_ref, requested_by, decided_by, created_at, decided_at}` — kiểu thời gian theo phong cách đang dùng
  trong `payment.proto`.
- `GetSellerBalanceResponse` thêm `int64 payout_pending_vnd = 5;`.
- Seller gọi bằng JWT người dùng: `shop_id` phải bằng shop trong claims (khác → PERMISSION_DENIED). Admin: vai trò có quyền tài chính theo
  `pkg/common/auth/capabilities.json` — thêm `payouts.read` = `["super_admin","admin","accountant","ops"]`, `payouts.decide` =
  `["super_admin","admin","accountant"]` (giống `refunds.read`/`refunds.approve`); xem cách Rust + gateway đọc capabilities hiện có.
### REST (gateway)
- `GET /api/v1/seller/payouts?page&page_size` (seller, shop từ JWT) → `200 {"items":[...],"page","page_size","total"}`
- `POST /api/v1/seller/payouts` (seller, header `Idempotency-Key` bắt buộc) body `{"amount_vnd":int}` → `201 {"payout":{...}}`;
  `400 INVALID_ARGUMENT` (amount ≤ 0, thiếu key); `409 INSUFFICIENT_BALANCE` (kèm `available_vnd`); `409 PAYOUT_PENDING_EXISTS` (mỗi shop chỉ
  một yêu cầu PENDING); `412 BANK_ACCOUNT_NOT_READY` (chưa có TK ngân hàng VERIFIED hoặc đang trong khoá 24h). Lặp lại cùng key → trả bản cũ.
- `GET /api/v1/admin/payouts?status&shop_id&page&page_size` (`payouts.read`) → như trên.
- `POST /api/v1/admin/payouts/{id}/decision` body `{"decision":"APPROVE"|"REJECT","reason":string}` (`payouts.decide`; REJECT bắt buộc
  reason) → `200 {"payout":{...}}`; `409 FAILED_PRECONDITION` nếu không còn PENDING.
- `GET /api/v1/seller/balance` (đã có, `docs/contracts.md:363`) trả thêm `payout_pending_vnd`.
- Ghi vào `docs/contracts.md` cùng thay đổi.
### Sổ cái (ghi kép, int64)
- Yêu cầu: `Nợ seller_available:<shop> / Có seller_payout_hold:<shop>` (giữ tiền ngay, chống rút trùng); trạng thái `PENDING`.
- Duyệt: `Nợ seller_payout_hold:<shop> / Có gateway_clearing`; `COMPLETED`, `transfer_ref = "MOCKBANK-<uuid>"` (chưa có ngân hàng thật; không
  gọi ra ngoài).
- Từ chối: `Nợ seller_payout_hold:<shop> / Có seller_available:<shop>`; `REJECTED` + `reject_reason`.
- Tất cả trong một transaction với cập nhật `seller_payouts`; khoá hàng (`FOR UPDATE`) khi quyết định; idempotent theo `idempotency_key`.
- Migration mới `services/payment/migrations/0019_seller_payout_flow.sql` (forward-only, không sửa file cũ): cột thiếu (bank_name,
  account_holder_name, requested_by, decided_by, reject_reason, transfer_ref, decided_at), unique partial index một PENDING/shop. Dữ liệu cũ
  trạng thái `completed`/`pending`/`rejected` chữ thường: chuẩn hoá hoặc ánh xạ khi đọc — chọn một, ghi rõ.
- Thông tin ngân hàng lấy từ `user.GetShopBankAccountForPayout` bằng token service (nếu payment chưa có client user thì thêm; RPC phải nằm
  trong allowlist `internalRPCs` của user — kiểm `pkg/common/auth/auth.go`). Không tin số tài khoản client gửi.

## Web
- `/seller/tai-chinh`: thẻ số dư (chờ đối soát / khả dụng / đang rút), nút "Rút tiền" mở dialog: số tiền (mặc định = khả dụng, nhập số nguyên
  VND), hiện TK nhận đã che số; chưa có TK VERIFIED thì báo + link tới chỗ khai TK (trang hồ sơ hiện có). Sinh `Idempotency-Key` phía client
  mỗi lần mở dialog. Bảng lịch sử rút tiền (thời gian, số tiền, TK, trạng thái, lý do từ chối).
- `/admin/doi-soat`: thêm tab "Chi trả người bán": danh sách (lọc trạng thái, mặc định PENDING), nút Duyệt / Từ chối (lý do), xác nhận bằng
  AlertDialog, hiện tổng tiền chờ chi.
- BFF tường minh mới: `web/src/app/api/seller/payouts/route.ts` (GET, POST — chuyển `Idempotency-Key`), `web/src/app/api/admin/payouts/route.ts`
  (GET), `web/src/app/api/admin/payouts/[id]/decision/route.ts` (POST). Theo mẫu BFF hiện có (CSRF Origin, session, thông báo lỗi tiếng Việt).

## Được sửa
`proto/nexusmart/payment/v1/payment.proto`, `gen/go/**` (qua `make proto`), `services/payment/**`, `services/gateway/**`,
`services/user/**` (chỉ khi cần cho client/allowlist GetShopBankAccountForPayout), `pkg/common/auth/**`, `docs/contracts.md`,
`web/src/features/seller/SellerFinance*`, `web/src/app/(seller)/seller/tai-chinh/**`, `web/src/app/(admin)/admin/doi-soat/**`,
`web/src/features/admin/AdminReconciliation*`, `web/src/app/api/seller/payouts/**`, `web/src/app/api/admin/payouts/**`, `web/src/lib/**` chỉ
khi thêm kiểu/route BFF mới (không sửa logic session/auth), `tests/e2e/29_seller_payouts_test.go` (mới).

## Kiểm (phải xanh, dán kết quả)
`make proto`; `cd services/payment && cargo fmt --check && cargo clippy -- -D warnings && cargo test`; `cd services/gateway && go test -race ./...`;
`cd pkg/common && go test -race ./...`; web lint/format/test:unit. Test bắt buộc: Rust — yêu cầu vượt số dư, rút trùng key, một PENDING/shop,
duyệt/từ chối đúng bút toán (tổng nợ = tổng có, số dư khả dụng trước/sau), quyết định hai lần → lỗi; gateway — route + quyền (seller khác shop,
buyer, admin thiếu capability). Viết `tests/e2e/29_seller_payouts_test.go` theo mẫu `tests/e2e/20_payment_ops_test.go` (không tự chạy e2e;
manager chạy).
