# Sổ kiểm chứng của manager (P0/P1)

## cat (docs/audit/cat.md) — 17 UC: 0 ĐỦ, 8 MỘT PHẦN, 1 LỖI, 8 THIẾU
- CAT-B1 P1 XÁC NHẬN: keyword=ao%20thun → total 0; "áo thun" → 5 (ILIKE, không unaccent).
- CAT-B2 P1→P3 HẠ MỨC: chỉ admin thấy PENDING trong GET /products công khai; 4 token seller (kể cả chủ shop của SP PENDING) không thấy. Không lộ cho người không có quyền.
- CAT-B3 P1 XÁC NHẬN: services/product không có NATS/outbox (rg nats|outbox = 0 file) → duyệt SP không phát event, không báo seller.
- CAT-B4 P1 XÁC NHẬN: UpdateProduct ghi Stock qua UpdateSKUDetails, không version, không stock_history.
- CAT-B5 P1 khớp code UI (ProductFormView PENDING → HIDDEN); để P2 (UI lệch luật backend, không mất dữ liệu).
- CAT-B6 P1 khớp: UNIQUE (product_id, sku_code) ở 000001_init.sql:52 + gỡ SKU = INACTIVE; để P2.

## plt (docs/audit/plt.md) — 28 UC: 0 ĐỦ, 4 MỘT PHẦN, 1 LỖI, 23 THIẾU
- PLT-B1 P1 XÁC NHẬN: ratelimit.go ClientIP lấy hop đầu X-Forwarded-For → né rate limit IP. Gateway publish 0.0.0.0.
- PLT-B2 P1→P2: httpx/response.go default branch trả nguyên msg gRPC (lộ lỗi DB khi gọi thẳng gateway; BFF đã che).
- PLT-B3 P1→P2: otel :48889/metrics = 0 byte; service tự có /metrics (order 48085 có grpc_errors_total). Thiếu Prometheus scrape.
- PLT-B4 P1→P2: dòng log có trace_id 40 phút: gateway 1994, user 154, payment 83, promotion 7, shipping 2, order/product/cart/notification 0.

## usr (docs/audit/usr.md) — 14 UC: 0 ĐỦ, 2 MỘT PHẦN, 6 LỖI, 6 THIẾU
- USR-B1 P0→P2: lock đã RevokeAllUserRefreshTokens (server.go:725-729); chỉ còn access token ≤15 phút (đánh đổi JWT spec chọn). Cải thiện: denylist Redis TTL 15m ở gateway khi lock/hạ quyền.
- USR-B4 P1 XÁC NHẬN: WriteAuditLog 0 caller ngoài định nghĩa (server.go:1326) → product/promotion/payment admin ops không audit.
- USR-B5 P1→P2: WriteAuditLog/ValidateUser không trong internalRPCs → gọi ẩn danh được trong mạng compose (gRPC không publish host).
- USR-B7 P1→P2: ActivateUser PhoneVerified: true sau OTP email (server.go:245).
- USR-B2, B3, B6: chưa tự kiểm (đọc tái hiện trong báo cáo khi lập kế hoạch).

## shp (docs/audit/shp.md) — 9 UC: 0 ĐỦ, 3 MỘT PHẦN, 3 LỖI, 3 THIẾU (lượt 1 bị bộ lọc Gemini chặn, resume cùng conversation 95aba917 thì xong)
- SHP-B1 P1 XÁC NHẬN: calculate-fee from_province="" → 35.500/40.000; from_province=79 → 19.500/24.000; CheckoutClient.tsx:295-306 gửi "" cố định.
- SHP-B2 P1→P2 XÁC NHẬN: order ddf5eb45 fee 19.500 ghn/EXPRESS vs shipments.fee_vnd 36.000. Buyer trả đúng giá đã chọn; lệch đối soát hãng.

## pay (docs/audit/pay.md) — 11 UC: 1 ĐỦ, 5 MỘT PHẦN, 2 LỖI, 3 THIẾU
- PAY-B1 P0 XÁC NHẬN (DB): order d1251d5a CANCELLED/FAILED; payment 12ee177e cùng group succeeded 255.000, không hoàn.
- PAY-B2 P0 XÁC NHẬN (ledger): escrow_liability credit 718.000, debit 255.000+10.000+463.000+5.000 = 733.000 (rút vượt 15.000). Tổng sổ cái vẫn cân 8.267.290.
- PAY-B3 P0 XÁC NHẬN (DB): order 19a24ddb subtotal 229.000, platform voucher 30.000 → allocation subtotal 199.000, commission 11.450 → seller thiếu 30.000.
- PAY-B4 P0 XÁC NHẬN (DB): COD e05ab0bc CANCELLED, payment d6460d4f succeeded, escrow held, refund 1.000 completed trên COD chưa trả; order.payment_status bị ghi REFUNDED (dính PAY-B6).
- PAY-B5..B7 P1: chưa tự kiểm từng cái (B5 trùng ghi chú 0b; B6 thấy dấu vết ở B4).

## prm (docs/audit/prm.md) — 9 UC: 0 ĐỦ, 4 MỘT PHẦN, 4 LỖI, 1 THIẾU
- PRM-B1 P0 XÁC NHẬN: (a) trùng PAY-B3; (b) đơn 70d739fe COMPLETED trả bằng xu, payment_id rỗng, 0 allocation → seller 0₫ cho đơn 200.000.
- PRM-B2 P0→P2: thưởng xu 1% trên subtotal gốc (UC-B18 mức Thấp); tiêu xu vẫn lỗ ròng nên không "cày miễn phí". Sửa công thức.
- PRM-B3 P0→P1 XÁC NHẬN: promotion_reservations giữ theo checkout group (0a3f7ee9, 20.000 xu RESERVED); đơn con 109ecb60 CANCELLED (9.487 xu + voucher 15.000) không hoàn. RETURNED cũng không hoàn.
- PRM-B4 P1→P2: thiếu authz gRPC khi không có claims (nội bộ, giống USR-B5).
- PRM-B5 P1→P2: COD báo "thanh toán thành công" lúc đặt (cùng gốc COD succeeded ở PAY-B4).
- PRM-B7 P1→P2: OTP trong Subject, email tiếng Anh.

## ord (docs/audit/ord.md) — 14 UC: 1 ĐỦ, 2 MỘT PHẦN, 5 LỖI, 6 THIẾU
- ORD-B1 P0 = PAY-B3 + PRM-B1(b) (xác nhận chéo bằng đơn khác: thiếu 11.730₫).
- ORD-B2 P0 = PAY-B4 (+ nhánh RETURNED COD cũng không đảo sổ cái).
- ORD-B3 P0→P1 = PRM-B3 (mất 3.270 xu ở đơn khác).
- ORD-B4 P1 XÁC NHẬN: không có request hash trong services/order (rg request_hash|payload_hash = 0).
- ORD-B5 P1 = PAY-B5.
- ORD-B6 P1 XÁC NHẬN: không có UPDATE tăng users.cod_rejected_count ở đâu → luật chặn bom COD vô hiệu.
- ORD-B7 P1→P2: statemachine RETURN_REQUESTED→COMPLETED (statemachine.go:36-39) để BuyerConfirmReceived đi qua; return_requests mồ côi.

## Pha 1A (contract) — manager review 2026-11-23
- Proto khớp brief; gen/go 7 file, header protoc không đổi; build 9 module Go ok; ValidateUser=false an toàn vì order chuyển tiếp JWT người mua (clients.go:229-236). Patch: wave1-1A.patch.

## Đợt 3 (adr) — 2026-11-23
- 5 ADR Đề xuất 0004–0008 + chỉ mục README. Cấu trúc đủ mục. Phiên bản/ngày (MinIO archived, ingress-nginx EOL, giá LLM) do agy tra web: cần chủ dự án đối chiếu khi duyệt. Câu hỏi mở chính: nhà cung cấp LLM nào có credit + trần ngân sách; Meilisearch hay thuần Postgres; nơi chạy k8s khi demo.

## Pha 1B
- notification (conv 7e654029): XONG 3 mục. OTP vẫn tách được (regex ngữ cảnh ở seed/e2e/e2e-web đọc cả body). Migration 000002 idempotent. Chờ kiểm tích hợp 1C.
- shipping (conv 2fb34736): XONG SHP-B1/B6/B7, e2e 17_ đỏ (40.000 vs 24.000) → xanh theo báo cáo. Thêm client user (GetShop + GetAddress bằng service token). Bỏ default 500g ở CalculateFee: checkout an toàn vì order tự default 500g (checkout.go:793-795); còn lệch nhỏ SKU 0g: web dùng max(100) vs order 500 (0/181 SKU hiện tại) → đợt 2.
- user (conv a7e1d56d): XONG 6 mục (RecordCODRejection chỉ service token, nộp lại hồ sơ shop, chặn duyệt shop không PENDING, kiểm địa chỉ qua shipping fail-closed, phone_verified, WriteAuditLog/ValidateUser). Không hồi quy: order không dùng phone_verified; seed/e2e/web gửi đủ trường RegisterShop; SĐT seed khớp regex. SuspendShop cần proto → đợt 2.
- gateway (conv 298c5ba5): XONG 6 mục (TRUSTED_PROXIES=web duyệt XFF phải→trái, che lỗi Internal, audit tập trung /api/v1/admin/* bỏ 3 route user tự ghi, retry qua OrderService.RetryPayment, route sản phẩm công khai luôn public-only, RequestLogger có user_id). E2E 16_ đỏ 4/4 → xanh theo báo cáo. Seller/admin dùng route riêng → không hồi quy. Rủi ro còn tồn P2: BFF web lấy hop đầu x-forwarded-for do client đặt (session.ts:75-80) và web mở thẳng 0.0.0.0 → qua đường web vẫn né được rate limit IP; cần reverse proxy tin cậy ghi đè XFF trước web (ADR-0008).
- product (conv d91f50bd): XONG 5 mục. Kiểm live: "ao thun" 9, "dam" 5 (ra Đầm), "%" chỉ khớp 5 SP có chữ % thật (thoát wildcard đúng). Migration 000004 backfill bằng translate(). DB audit đã có 54 SP APPROVED do e2e các agent tạo → reset trước khi nghiệm thu.
- promotion (conv 4c75465c): XONG 5 mục (ReleasePromotionShop idempotent qua released_shops_json + Lua release_partial_atomic, shop_id cho item flash, authz CreditCoins/Release* admin|service, user-claims bắt buộc cho Save/GetBalance/Preview/Reserve, ListAvailableVouchers lọc đúng). Xu thưởng chỉ cộng ở consumer promotion (order chỉ tính coins_reward) → không cộng đôi. CẦN TỰ CHẠY LẠI khi máy rảnh: TestService_1000Concurrent_FlashSale_100Slots_AndRelease từng có 15 lỗi lúc load ~102.
- order (conv 709a4aac): XONG 12 mục; e2e 12_ 6/6 nhánh xanh xuyên payment/promotion/gateway mới, cộng OrderIdempotency, CancelConfirmedCOD, MultiShop VietQR xanh. D kẹp ≤ R+Ship; thêm orders.refunded_vnd (hoàn cộng dồn); request_hash rỗng cho dòng cũ vẫn replay. Lệch luật nhỏ: tự sửa docs/contracts.md dòng order.returned (nội dung đúng) → soát contracts khi gộp.
- payment (conv 4b02c795): XONG 6 mục; 48 test Rust xanh; e2e 13_ đỏ 3/3 → xanh; 8 e2e thanh toán cũ xanh, ledger cân, 0/23 payment escrow âm. Theo dõi: (1) migration 0002 unique index một payment succeeded/nhóm sẽ lỗi trên DB đã dính PAY-B1 → đã gửi sửa bọc DO-block (không rebuild); (2) gateway chưa map platform_discount_vnd ra JSON payment (tuỳ chọn, gom vào follow-up gateway); (3) trả hàng sau khi nhận (COD đã thu) để đợt 2 (ponytail).

## Việc tồn cho pha 1C
- docs/contracts.md:332 order.returned thiếu checkout_group_id, payment_method, from_status, total_payable_vnd (struct OrderReturnedPayload có; sửa của order bị ghi đè).
- gateway handlers_payment.go: map platform_discount_vnd ra JSON GET /api/v1/payments/{id}.
- Allocation order đã review: R, D (kẹp ≤ R+Ship, lưới an toàn khớp total_payable), C kẹp ≤ R ở cả applyDiscountsAndTotals (checkout.go:1091) và buildShopAllocation.
- Migration payment 0002 sửa xong bằng DO-block (thử 2 nhánh trên PG16, rollback). 48 test Rust xanh.

## Pha 1C — gate và review
- make lint 0, make test 0 (35 package Go ok, payment 48). Full make e2e: 01–12 xanh, 13–18 đỏ do HTTP 429 (rate limit 300/phút/IP, bộ e2e dài hơn); e2e-web 13/21 do 429. Không phải hồi quy chức năng → cần retry 429 trong harness (client.go DoAbsolute, helpers.mjs) + chạy lại theo lô.
- Review C (agy): P1 thật: (a) form đăng ký/nộp lại shop ở web chưa theo luật mới (REQUEST_INFO không có form, giấy tờ ghi "không bắt buộc", regex SĐT lệch); (b) seed.mjs listAllAdminProducts dùng GET /api/v1/products công khai → mất idempotency; (c) 3 route admin do user tự audit ghi IP container gateway (gateway không gửi x-client-ip). P1 BFF-XFF = rủi ro tồn đã ghi (cần reverse proxy). P2: ResubmitShop race WHERE; web `??` chuỗi rỗng ở types.ts; canRetryPayment khi quá hạn; shipping USER_GRPC_ADDR compose; gateway che 503/504; public product fail-closed UNSPECIFIED; contracts RecordCODRejection quyền; e2e 18 assert ==1 dễ flaky; user peerIPFromContext host:port + fail-closed.
- Chạy lại theo lô (nghỉ 65s giữa lô): e2e 13–18 xanh hết (9 test), e2e-web 21/21 xanh → chức năng đợt 1 xanh; đỏ trước đó chỉ do rate limit harness.
- Review B (agy, payment): P0-1 race COD delivered/confirm-received (tiền kẹt escrow hoặc seller_pending); P0-2 IDOR idempotency_key toàn cục (MANAGER XÁC NHẬN service.rs:145-160 + key client đi qua RetryPayment); P0-3 race huỷ đơn con với webhook thành công VietQR đa shop; P1 đối soát/quét hết hạn thiếu `AND status='pending'` + khoá nhóm, deadlock AB-BA; P2 0đ RETURNED treo held, supersede nhóm chết, replay create_refund, trạng thái refunded lệch; P3 e2e 13 kiểm escrow toàn cục; dead code AllocationSplit.
- Vòng sửa 2 đã giao (w1-round2.md): payment (review-B toàn bộ), web, gateway, user, shipping, product, contract, qa (retry 429 harness + seed listAllAdminProducts dùng route admin). Order/promotion chờ review A.
- Review A (agy, order+promotion): P0-1 hoàn một phần rồi trả hàng/huỷ → CreateRefund toàn bộ → 409 bị nuốt (mất phần còn lại); P0-2 race payment.failed/expired với RetryPayment (nhả tồn/khuyến mãi rồi pay-2 thành công, ConfirmStock lỗi vĩnh viễn); P0-3 RetryPayment đa pod/ngắt kết nối → bù trừ huỷ nhầm payment mới; P0-4 lỗi ListOrdersByCheckoutGroup bị nuốt → nhả khuyến mãi cả nhóm, TOCTOU CancelOrder vs payment.succeeded; P1 seller voucher page bị lọc kiểu người mua (hồi quy PRM-B8), ListAvailableVouchers tin user_id (IDOR đọc), HandleShipmentUpdated nuốt lỗi bù trừ sau commit; P2 refunded_vnd nhóm, snapshot idempotency giữ pay cũ, COD_PENDING cũ, freeship ngân sách.
- Vòng sửa 2 giao thêm order (review-A + namespace key retry) và promotion (review-A P1-5, P1-6, P2-11).
- Xong vòng 2: contract (RecordCODRejection), user (ResubmitShop WHERE, SplitHostPort), product (e2e 18 bền), gateway (x-client-ip mọi gRPC, che 503/504, fail-closed public), shipping (USER_GRPC_ADDR compose), qa (retry 429 harness: 10/10 vượt qua trong make e2e thật; seed route admin, chạy 2 lần toàn existing).
- payment vòng 2 (conv 4b02c795): XONG review-B P0-1..3, P1-4..5, P2-6..9, P3-10 + 6 mục ponytail; e2e TestE2E_PaymentMoney_Round2_ReviewB đỏ → xanh (P1-5 tái hiện deadlock thật trước khi sửa). Không migration mới.
- promotion vòng 2 (conv 4c75465c): XONG review-A P1-5, P1-6, P2-11 + gộp helper release_plan.
- ĐỢT 2 (mới phát sinh): huỷ một đơn con VietQR chưa trả → payment huỷ cả QR (chặn P0-3) nhưng RetryPayment đòi mọi đơn con PENDING → đơn con còn lại không trả được, tự huỷ khi hết hạn. Cần: huỷ một đơn con chưa trả thì huỷ cả nhóm (UI báo rõ) hoặc cho retry phần còn lại.
- order vòng 2 (conv 709a4aac): XONG review-A P0-1..4, P1-7, P2-8..10, P3-12, P3-13 + namespace key retry (review-B P0-2); migration 0004_retry_idempotency; unit TestReviewA_* đỏ → xanh.
- web vòng 2 (conv 582f6657): XONG review-C P1-2 (form shop REQUEST_INFO, bắt buộc giấy tờ/địa chỉ kho, regex SĐT khớp backend), P2-6, P2-7; tự bắt thêm lỗi Mua ngay flash sale khi catalog > 100 SP (phân trang); e2e-web thêm 5i. Ghi chú: build web lấy Google Fonts lúc build (từng lỗi mạng thoáng qua).
- Pha 1C lần cuối: reset stack + rebuild + seed + make e2e/e2e-web + make lint/test + web static gates (đang chạy).

## NGHIỆM THU ĐỢT 1 (manager, 2026-11-23 ~03:35, stack nexusmart-audit reset sạch, HEAD 0db2cdd + diff đợt 1)
- make lint 0; make test 0 (36 package Go ok, payment 51 test Rust); web lint/format:check/check:data ok (build qua docker build web).
- make seed (reset) ok; make e2e 0: 22 test top-level PASS trong 73s (harness tự đợi 429: 3 lần); make e2e-web 22/22.
- Bất biến tiền trên DB sau e2e: 0 giao dịch sổ cái lệch (dr=cr=11.334.105); 0 nhóm có >1 payment succeeded; 0 payment có Σ(R+Ship−D) ≠ amount; 0/13 nhóm escrow âm (min 0) — đo theo nhóm (đo theo payment ra 1 âm giả vì payment superseded dùng chung order_id với payment thành công); 0 đơn COD huỷ còn escrow held.
- Playwright: /flash-sale 1440px và PDP combo 390px không còn chữ combo/deal/giảm theo bậc, không tràn ngang; console chỉ favicon.ico 404 (có từ trước). Giá gạch PDP 269.000₫ lấy từ web/src/data (backend chưa có original_price_vnd) → CAT-D1 đợt 2.
- Patch: scratchpad wave1-final.patch (125 file, +11.464/−3.502). Chưa commit (chờ chủ dự án).
- Rủi ro còn tồn: (1) BFF tin x-forwarded-for client tự đặt khi web mở thẳng ra ngoài → cần reverse proxy tin cậy (ADR-0008); (2) huỷ một đơn con VietQR chưa trả làm cả nhóm không trả được (đợt 2); (3) trả hàng sau khi đã nhận (COD đã thu / VietQR) chưa có luồng hoàn tiền đầy đủ (đợt 2: returns); (4) pending → available chưa có job thời gian giữ tiền (PAY-B7, đợt 2); (5) web build kéo Google Fonts lúc build.

## Review độc lập lần 2 (sau vòng sửa 2) — theo khuyến nghị advisor
- payment (review2-P-payment.md): không P0 mới; P1: refund thanh toán muộn trùng do 2 tiền tố key (late-webhook vs late-autoheal) → nghĩa vụ hoàn gấp đôi; order.completed trả Conflict khi COD awaiting → DLQ nếu order.delivered mất (seller không bao giờ quyết toán); race order.cancelled vs supersede để P2 pending sống. P2: deadlock ledger_accounts FOR KEY SHARE/FOR UPDATE; đối soát khoá nhiều nhóm một tx; chuyển trùng bị "matched"; khoá cod-collect trùng order_id. P3: replay refund order_id=group; status refunded đa shop.
- order (review2-O-order.md): không P0 mới; P1: rò rỉ reserved_stock/khuyến mãi khi downstream lỗi trên đường EventID=="" hoặc đơn đã SHIPPING trước redelivery; payment_status bị hạ từ REFUNDED về SUCCEEDED/EXPIRED khi sự kiện đảo thứ tự; RetryPayment đa replica khác key lưu payment_id superseded và huỷ payment sống; compensateCancelledOrder cộng tồn ảo (ReleaseStock lỗi / huỷ lần lượt trước ConfirmStock).
- Vòng sửa 3 (w1-round3.md) đã giao: payment (review2-P toàn bộ), order (review2-O + quyết định huỷ đơn con VietQR chưa trả = huỷ cả nhóm), web (hộp xác nhận huỷ cả nhóm).
- Vòng sửa 3: order (review2-O P1-1..4a, P2, P3; pg_advisory_xact_lock theo nhóm cho RetryPayment; huỷ đơn con VietQR chưa trả = huỷ cả nhóm, 1 lần ReleaseStock/ReleasePromotion/CancelPayment), payment (review2-P P1-1..3, P2-4..7, P3-8..9; một khoá late-webhook:{provider_tx_id}; thu COD ngay trong order.completed; xử lý race supersede; khoá seller balances trước settle; đối soát mỗi nhóm một tx; chuyển trùng → late refund), web (hộp xác nhận huỷ cả nhóm, e2e-web 5j). Không migration mới.

## NGHIỆM THU CUỐI ĐỢT 1 (sau vòng 3, stack reset sạch)
- make lint 0; make test 0 (36 package Go ok, payment 53 test Rust); web lint/format/check:data ok; tsconfig không đổi.
- make seed ok; make e2e 0: 23 test top-level PASS (74s).
- Bất biến DB: 0 giao dịch lệch; dr = cr = 16.759.105; 0 nhóm >1 payment succeeded; 0 allocation lệch amount; 0/28 nhóm escrow âm (min 0); 0 refund thanh toán muộn trùng.
- make e2e-web 22/23: 5i đỏ do fetch trong trang bị 429 khi suite chạy dồn; chạy riêng 5i xanh 2,2s → giao qa làm waitFor của trình duyệt hiểu rate limit, chạy đầy đủ 2 lần.
- qa (conv 6f0bb4a2): waitFor trình duyệt hiểu rate limit (đợi + tải lại, trần 150s) trong helpers.mjs. make e2e-web lần 1: 23/23; lần 2: 22/23 — 5d đỏ do lỗi THẬT của web: /thanh-toan (page.tsx:63) và /thanh-toan/cho-thanh-toan/[paymentId] (page.tsx:31) gọi requireUser không bắt GatewayError → 429 thành HTTP 500. Đã giao web sửa theo mẫu tai-khoan/page.tsx:65-93 + chạy e2e-web 2 lần.
- web (conv 582f6657): bắt GatewayError ở /thanh-toan, /thanh-toan/cho-thanh-toan/[paymentId], (seller)/layout, (admin)/layout; chuyển tiếp Retry-After. make e2e-web đầy đủ 23/23 HAI lần liên tiếp (lần 2 có 429 thật, trang hiện thông báo thân thiện rồi tự hồi phục). Manager chạy lại web lint/format/check:data: ok; tsconfig không đổi.
- ĐỢT 1 NGHIỆM THU: lint, test, e2e 23/23, e2e-web 23/23 ×2, bất biến tiền DB đều đạt. Chưa commit.
