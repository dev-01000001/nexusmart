# Đợt 3b (A4–A8, B1 của w3-plan.md)

Nền: commit 3a trên `main` (local). Luật chung: `w2-rules.md`, `w2b-plan.md` §8, `w3-plan.md` §6–7, `w3a-plan.md` §3 (không agent nào rebuild/restart container, không `make up|seed|e2e|e2e-web|coverage`, không git ghi; test Postgres bằng `bash scripts/test-db.sh --run`). Thiết kế khảo sát: `w3b-design-{a4,a5,a6,a7,a8,b1}.md`; **mục này thắng khi khác thiết kế**.

## 1. Ba đợt con, mỗi đợt nghiệm thu và commit riêng
- **3b-1**: consumer DLQ/Drain (4 service), giỏ có món không còn bán, A4, A5.
- **3b-2**: A6 rồi A7.
- **3b-3**: A8 và B1 (product làm tuần tự trong một phiên).

Số migration/e2e: 3b-1 dùng product `000008_`, promotion `005_`, payment `0009_`, shipping `000006_`, order `0015_`, user `0008_` (nếu cần), notification `000005_` (nếu cần); e2e `41_` (A4 + giỏ), `42_` (A5), `43_` (consumer); e2e-web `09_` (A4 + giỏ), `10_` (A5). 3b-2 và 3b-3 lấy số kế tiếp.

## 2. Quyết định 3b-1 (không tự đổi; thấy sai thì ghi báo cáo)

**Consumer (T0)**
- **E1** Cả `shipping`, `notification`, `promotion`, `payment` theo mẫu `services/order/internal/events/consumer.go`: cờ dừng + chờ handler xong, KHÔNG `Drain()`/`Unsubscribe()` durable consumer; lỗi do đang tắt thì không Ack, không vào DLQ; DLQ ghi bằng context riêng có hạn, chỉ Ack khi ít nhất một nơi (`dead_letter_events` hoặc `NEXUSMART_DLQ`, có chờ PubAck) lưu được, không thì Nak có trễ; `MaxDeliver` lớn hơn ngưỡng DLQ để còn lượt ghi lại. `promotion` chuyển `QueueSubscribe` cùng durable; giữ tên durable cũ, nâng cấp không phát lại cả stream (test bằng NATS thật hoặc embedded: khởi động lại hai lần, đếm số lần xử lý). `payment` thiếu `order_id` thì vào DLQ, không Ack bỏ. `notification` giữ `sanitizeDLQPayload`.
- **E2** Thêm bảng `dead_letter_events` cho `promotion` và `payment` (cùng cột với `order`).

**Giỏ (T1)**
- **E3** `CartItem.unavailable=14`, `unavailable_reason=15` (`SKU_INACTIVE`|`SKU_NOT_FOUND`). Cart trả dòng ẩn với giá 0, KHÔNG đổi `selected` đã lưu, không cộng vào `total_items`/`subtotal_vnd`/`total_vnd`; web coi là không chọn được.
- **E4** `order` `from_cart=true`: có dòng `selected` mà `unavailable` → từ chối `SKU_INACTIVE` (cùng mã với đường `items`), không tự bỏ món rồi đặt phần còn lại. Không còn dòng nào → mã lỗi giỏ rỗng hiện có. Web: dòng "Không còn bán", khoá số lượng và chọn, có nút xoá; checkout của web (gửi `items`) bỏ qua dòng này.

**A4**
- **E5** Tên người đánh giá: `product` gọi `UserService.GetProfile` bằng JWT người mua, lưu bản che (`"N. Linh"`: chữ đầu của từ đầu + từ cuối; một từ thì chữ đầu + `***`); `user` lỗi → "Người mua đã xác thực". Không `depends_on` vòng; dial lười, timeout 3 s.
- **E6** Quyền đánh giá lấy từ `OrderService.GetOrder` (JWT người mua): `buyer_id == claims.sub`, trạng thái `COMPLETED`, dòng thuộc đơn, sản phẩm/shop/SKU lấy từ đơn. `UNIQUE(order_item_id)`. Một dòng một đánh giá, không sửa/xoá trong 3b. Ghi đánh giá + cộng `rating_sum`/`review_count` + outbox `review.created` trong một transaction có khoá dòng `products` trước. Điểm ra API là `rating_avg_x100` (int), không số thực.
- **E7** Ảnh: ≤ 5 URL, mỗi URL ≤ 500 ký tự, chỉ `https://` hoặc đường dẫn nội bộ `/products/…`; server không tải ảnh; web render có `rel`/`referrerPolicy` và không nhận `javascript:`/`data:`.
- **E8** Từ cấm trong đánh giá (mức BLOCK lẫn REVIEW) → 400 `BANNED_CONTENT`, không hàng đợi duyệt đánh giá.
- **E9** So khớp từ cấm (một hàm ở `product`, dùng cho tạo/sửa/duyệt sản phẩm, đánh giá, xem trước, quét lại): NFKC → bỏ ký tự định dạng (Cf) và dấu kết hợp lạ → gấp homoglyph Cyrillic/Hy Lạp → gấp leet (`0→o 1→i 3→e 4→a 5→s 7→t @→a $→s`) → gộp chữ lặp ≥ 3 → tách token theo ký tự không phải chữ số. Khớp khi: (a) chuỗi token của từ cấm nằm liền nhau theo biên token (token có dấu so đúng dấu; token không dấu so bản bỏ dấu); (b) một token bằng bản dính liền của từ cấm (dính liền ≥ 4 ký tự); (c) chuỗi token dài ≤ 2 ký tự liền nhau, ghép lại chứa bản dính liền (≥ 4 ký tự). KHÔNG so khớp chuỗi con trên toàn văn dính liền (chặn nhầm "căn sàn" vì "cần sa"). Test phải có cả ca lách lẫn ca không được chặn.
- **E10** Sản phẩm: BLOCK → từ chối tạo/sửa 400 `BANNED_CONTENT` kèm từ khớp; REVIEW → tạo như thường (vốn PENDING), sửa sản phẩm đang APPROVED → về PENDING; `flagged_keywords` lưu để kiểm duyệt viên thấy. `AdminReviewProduct(APPROVE)` không chặn REVIEW, chặn BLOCK và danh mục cấm. Danh mục cấm: cờ `categories.is_prohibited`, tạo/sửa sản phẩm vào danh mục đó (hoặc danh mục con) → 400.
- **E11** Xem trước trả số sản phẩm + tối đa 20 mẫu. Quét lại chạy theo lô 500 sản phẩm có con trỏ, web gọi tiếp tới hết; BLOCK → `REJECTED` + `product.reviewed`, REVIEW → `PENDING`; chạy lại không phát event lần hai cho sản phẩm đã xử lý.
- **E12** KHÔNG làm `CheckBannedContent` (chờ chat B2), không trả lời đánh giá của người bán, không UI danh mục cấm riêng (một ô chọn trong màn sửa danh mục).
- **E13** B04 ngừng bán: slug của sản phẩm từng công khai nay `HIDDEN`/xoá/`REJECTED` → 200 `{discontinued:true, category_id, name?}` (chỉ có `name` khi HIDDEN), không SKU/giá; slug lạ hoặc chưa từng duyệt → 404. "Từng công khai" phải có căn cứ trong DB (cột/bản ghi duyệt), không đoán theo trạng thái hiện tại.
- **E14** Thẻ sản phẩm và PDP: sao thật từ API; chưa có đánh giá thì ghi "Chưa có đánh giá", bỏ sao tĩnh và `SAMPLE_REVIEWS`. B1 sau dùng các trường này.

**A5**
- **E15** Phường: 34 tỉnh, 3.321 xã/phường/đặc khu theo Nghị quyết 202/2025/QH15 và QĐ 19/2025/QĐ-TTg; lấy từ cổng danh mục hành chính của cơ quan thống kê, đối chiếu số lượng và từng mã với một bộ MIT; commit dữ liệu + `SOURCES.md` (URL, ngày, SHA256). Số thật khác 3.321 thì báo, không chỉnh cho khớp. KHÔNG xoá mã phường cũ: mã mẫu không có trong danh mục → `is_active=false` (không liệt kê, vẫn tra được cho địa chỉ đã lưu). Nhúng `go:embed`, upsert theo lô, bỏ qua khi checksum không đổi.
- **E16** `wards.cod_supported` mặc định TRUE, FALSE cho Hoàng Sa và Trường Sa; không UI cấu hình.
- **E17** `POST /shipping/calculate-fee` công khai có giới hạn theo IP; khách chỉ chọn tỉnh → `to_ward_code` rỗng, shipping dùng phường đại diện của tỉnh và trả cờ ước tính; checkout vẫn bắt buộc phường. Kiểm có cache ngắn theo tuyến + cân nặng chưa (UC-SHIP02 bước 5); chưa có thì thêm cache Redis/nhớ trong TTL ngắn. Không lộ địa chỉ kho.
- **E18** SHIP03: hãng lỗi tạm (5xx/timeout/mạch hở) → lưu `shipment_create_jobs`, trả `queued=true`, worker thử lại có backoff (base 2 s, trần 5 phút, 8 lần), thuê bằng `FOR UPDATE SKIP LOCKED` + token; mã đơn gửi hãng bất biến nên không tạo đôi. Hãng từ chối (4xx) → lỗi ngay cho người bán, không job. Hết lượt → `FAILED`, báo người bán, bấm lại được. Đơn huỷ trong lúc chờ → job `cancelled`; vận đơn vừa tạo cho đơn đã huỷ → huỷ vận đơn bù.
- **E19** PAY02: `ORDER_COD_REQUIRE_PHONE_VERIFIED=true` mặc định; OTP SĐT lưu Redis (6 số, 5 phút, sai 5 lần huỷ, 60 s giữa hai lần gửi, 5 lần/giờ), SMS mock qua `notification` → Mailpit, OTP không vào log, tiêu đề, DLQ. Tỷ lệ bom hàng: `order` đếm đơn COD của người mua ở trạng thái cuối; ≥ 3 đơn và `rejected*10000 >= total*3000` → chặn COD; giữ chặn `cod_rejected_count >= 3`. Mã lỗi rõ: `COD_PHONE_NOT_VERIFIED`, `COD_AREA_NOT_SUPPORTED`, `COD_REJECTED_LIMIT_EXCEEDED`; web hiện lời và lối đi (xác minh SĐT hoặc đổi phương thức). Kiểm ở checkout, trước khi giữ hàng. e2e fixture xác minh SĐT thật qua Mailpit, không có cửa sau.
- **E19-bis** SĐT tài khoản hợp lệ kiểu VN luôn lưu `0xxxxxxxxx` (Register, UpdateProfile, VerifyPhoneOTP, bootstrap). Chỉ số ĐÃ XÁC MINH mới là duy nhất (unique index một phần, migration user `0008_`); tài khoản chưa xác minh không chiếm chỗ. Đổi số sang dạng viết khác của cùng số không làm mất `phone_verified`.
- **E20** PAY03: file CSV UTF-8 (BOM được) `tracking_code,cod_amount_vnd,delivered_at[,order_code]`, ≤ 2 MiB, ≤ 5.000 dòng, số tiền chỉ chữ số. Dòng khớp (mã vận đơn, đúng tiền, đơn `DELIVERED`/`COMPLETED`) ghi Nợ `gateway_clearing` / Có `cod_receivable` một lần (khoá idempotent theo payment+order+shop, không theo file); lệch tiền, mã lạ, chưa giao → `reconciliation_queue`, không bút toán. `order_code` chỉ dùng khi allocation chưa có mã vận đơn. Luồng `order.completed` → `seller_pending` KHÔNG đổi (không cộng tiền người bán lần hai, không mở tiền trước hạn trả hàng); ghi chỗ lệch với bước 3–4 của UC vào báo cáo. Cùng file tải hai lần → 0 bút toán mới, tổng nợ = tổng có.
- **E21** A12 còn lại: bỏ hằng số phí mẫu ở web, đọc từ API; không làm `api_token` trong `AdminTestCarrier`.

**Bổ sung sau khi soát hợp đồng (2026-11-24 16:40)**
- **E17-bis** Giới hạn `calculate-fee`: khách ẩn danh theo IP, người đã đăng nhập theo user id (khoá riêng), cùng `RATE_LIMIT_SHIPPING_FEE_PER_MIN`; không để nhiều người chung IP chặn nhau ở checkout.
- **E20-bis** `file_sha256` chỉ để ghi nhận phiên; KHÔNG dùng để bỏ qua cả file: tải lại cùng file sau khi đơn đã giao thì dòng `NOT_DELIVERED` trước đó phải khớp. Mục hàng đợi cũ của dòng đó tự đóng khi khớp. Dòng trùng mã trong cùng file: dòng sau là `already_reconciled` (đúng tiền) hoặc `amount_mismatch`.
- **E6-bis** `ListProductReviews` công khai để trống `buyer_id`, `order_id`, `order_item_id`, `sku_id`; chỉ `ListOrderReviews` (chủ đơn) trả đủ.
- **E9-bis** (2026-11-24 18:35) Từ cấm ≥ 2 token, dính liền ≥ 4: token văn bản không dấu so bản bỏ dấu, token có dấu so đúng dấu ("ma tuy" khớp "ma túy", "ma tùy" không). Từ cấm một token có dấu vẫn so đúng dấu. Quét cả thuộc tính SKU và slug. Sản phẩm admin đã duyệt chỉ về PENDING khi có từ REVIEW mới; HIDDEN không đổi vì REVIEW.
- **E9-ter** Không quét slug tự sinh; slug chỉ quét khi là slug tuỳ chỉnh mới gửi. So token theo chữ (giữ â ă ê ô ơ ư đ) + dấu thanh tách vị trí; dấu kết hợp trên phụ âm và chữ ngoài bảng chữ cái tiếng Việt hạ về chữ gốc; ≥ 3 chữ cùng gốc liền nhau gộp một. Luật ghép chữ rời phải trùng ranh giới token và không mâu thuẫn dấu. Rescan chỉ khớp slug → PENDING, không REJECT.
- **E9-quater** (2026-11-24 20:25) Căn chuỗi: mỗi token từ cấm khớp một token văn bản HOẶC ≥ 2 token liền nhau dài ≤ 2 ký tự ghép lại, cùng điều kiện dấu và ranh giới ("h.a.n.g cấm", "th.u.0.c c.a.m xk"); chỉ khi bản dính liền ≥ 4.
- **E20-ter** (20:25) Cột `order_code` trong file COD là mã gửi hãng (`client_order_code` = `order_id`). Trước khi giao, dòng không có mã này là `MISSING_LOCAL`, có thì `NOT_DELIVERED`.
- **E10-bis** `flagged_keywords`, từ khoá khớp: chỉ người bán chủ shop và admin thấy; API công khai để trống ngay ở `product`, không chờ gateway lọc.

## 3. Phân công 3b-1
`contract` chạy trước, một mình. Rồi mọi agent service chạy cùng lúc (chủ dự án 2026-11-24: "chạy tối đa đi") trong `nm3b.slice` có trần chung RAM 10 GB, swap 6 GB, 16 nhân để OOM không giết dịch vụ ngoài repo; không bật stack khi agent đang chạy:
- nhóm 1: product, shipping, order, payment;
- nhóm 2: user, cart, promotion, notification (bắt đầu khi có chỗ trống, `user` trước);
- nhóm 3: gateway (sau nhóm 1–2), rồi web;
- QA cuối: **sở hữu mọi file e2e backend** `41_` (đánh giá, từ cấm, ngừng bán, giỏ có món ẩn), `42_` (phí công khai, phường, job vận đơn, OTP SĐT, COD, đối soát COD), `43_` (consumer: tắt/bật service không mất event) và sửa fixture. Agent service KHÔNG viết e2e, ghi "ca e2e cần có" trong báo cáo. `web` sở hữu `09_`, `10_` của e2e-web.

Trần thời gian e2e: `make e2e` 3a là 697 s trên trần 1.200 s. Người mua COD của fixture chỉ xác minh SĐT **một lần mỗi tài khoản** (OTP đọc từ Mailpit, không cửa sau, không hạ giới hạn); QA báo thời gian trước/sau, vượt 1.000 s thì gộp fixture chứ không nâng trần. `.runs/w3a/browser/buyer1.json` và người mua e2e-web cũng phải xác minh SĐT.

Bài nhạy tải: `promotion` `R5_Item6…` (chờ 25×15 ms) và lua 1.000 goroutine đỏ khi load > 50. Không sửa test để qua; chạy lại khi load < 10 rồi mới tính đỏ. `waitForCommittedReservation` là trần thật của sản phẩm (w3-plan §10), không sửa trong 3b-1.

Review độc lập hai lượt: payment, order, product (từ cấm + quyền đánh giá), consumer 4 service. Test mới chạy lặp: Go `-race -count=5`, Rust 50 lượt; phải có ca tấn công/đua nêu ở mục 3 của thiết kế.

## 4. Quyết định 3b-2 (A6 + A7; chốt 2026-11-24 theo uỷ quyền; mục này thắng khi khác thiết kế a6/a7)

Chạy A6 và A7 **song song theo service**, nghiệm thu và commit một lần. Hợp đồng (cả A6 và A7) làm trước trong một lượt. Số: payment `0010_` (A6) rồi `0011_` (A7), order `0016_`, user `0009_`, product `000009_`; e2e `44_` (A6), `45_` (A7); e2e-web `11_` (A6), `12_` (A7). Câu phải chốt trong a6 §4 và a7 §4: theo phương án đề xuất, trừ chỗ ghi khác ở dưới.

**A6 — ZaloPay và phương thức thanh toán**
- **F1** Chỉ `mock` và `sandbox` qua `ZALOPAY_MODE` (mặc định `mock`). Khoá chỉ từ env, trống thì `sandbox` không khởi động được; KHÔNG commit khoá nào, kể cả khoá test công khai. Không gọi sandbox thật trong đợt này (phải hỏi chủ dự án).
- **F2** IPN `/webhooks/zalopay`: HMAC-SHA256(`key2`, chuỗi `data` nguyên byte) so thời gian hằng. MAC sai → từ chối, không ghi gì. MAC đúng nhưng `server_time` lệch quá 300 s → KHÔNG xác nhận trực tiếp từ body; gọi `/v2/query` rồi xử lý theo kết quả truy vấn (không mất tiền khi ZaloPay gọi lại muộn, vẫn giữ luật chống replay 300 s của AGENTS.md). `zp_trans_id` là `provider_tx_id` UNIQUE; IPN lặp 10 lần → đúng một bút toán. `amount` lệch → hàng đợi đối soát, không xác nhận. `app_id`/`app_trans_id` phải khớp giao dịch của ta.
- **F3** Hết hạn: scanner gọi `/v2/query` trước khi huỷ; `1` → xác nhận; `2`, `3`, lỗi mạng → huỷ như thường, tiền về muộn đi đường late-refund có sẵn. Truyền `expire_duration_seconds` theo hạn của ta cho ZaloPay.
- **F4** Hoàn tiền về ví: lưu `m_refund_id` trước khi gọi (thử lại không hoàn hai lần), `return_code=3` → worker poll `query_refund`; lỗi vĩnh viễn → `manual_review`. Sổ cái chỉ tất toán khi `1`.
- **F5** Đổi phương thức lúc trả lại (B25): giữ hạn gốc của checkout group; giao dịch cũ bị thay thế; tiền về muộn của giao dịch cũ → late refund, không xác nhận đơn. Sang COD phải chạy lại **toàn bộ** kiểm tra COD của E19 (SĐT đã xác minh, phường hỗ trợ, tỷ lệ từ chối, hạn mức) và cấm với flash sale. Phí thanh toán tính lại theo phương thức mới, tổng người mua trả không đổi. Hai lệnh đổi song song, hoặc đổi đúng lúc hết hạn → đúng một kết quả.
- **F6** `payment_method_configs` là nguồn duy nhất của bật/tắt, min, max, hạn phút. Bỏ `ORDER_COD_MAX_VND` và `ORDER_PAYMENT_EXPIRY_MINUTES` (seed cùng giá trị cũ 5.000.000 và 15). `ORDER_FLASH_PAYMENT_EXPIRY_MINUTES` giữ (luật flash sale). `order` đọc trực tiếp `payment` mỗi checkout/retry, KHÔNG cache. `payment` không đọc được cấu hình → từ chối checkout, không đoán mặc định.
- **F7** Cấu hình có `expected_version` (optimistic lock, ghi đè nhầm → 409), không tắt được phương thức bật cuối cùng, kiểm toán đủ trước/sau. Phương thức bị tắt: không tạo đơn mới và không đổi sang; đơn dở vẫn nhận IPN và xử lý xong.
- **F8** Web: gỡ hẳn dữ liệu mẫu tab `thanh_toan` và nhãn "Ví NexusPay". Trang chờ có nút mở `pay_url`, chỉ chấp nhận `https` và host ZaloPay trong danh sách cho phép (mock dùng trang mô phỏng cùng origin); không để server trả `pay_url` tuỳ ý.

**A7 — vai trò nội bộ và duyệt hai người**
- **F9** Ma trận quyền ở **một** bảng: `pkg/common/auth` (Go, hằng `capability → roles`) và bản sao trong `services/payment/src/auth.rs`, có test đối chiếu hai bản cùng một file dữ liệu. Service kiểm quyền bằng bảng này, gateway chỉ là lớp ngoài. Test bảng: mọi route admin × 6 vai trò (dương và âm).
- **F10** Chỉ `super_admin` tạo nhân viên và đổi vai trò. `admin` giữ quyền vận hành như cũ. Không ai tự nâng quyền, không tác động tài khoản có vai trò bằng hoặc cao hơn mình (kể cả `cskh` khoá tài khoản nhân viên). Giữ luật `super_admin` cuối cùng.
- **F11** Duyệt hai người theo miền (`shop_suspension_approvals` ở `user`, `refunds.status=pending_approval` ở `payment`), maker ≠ checker ở cả ứng dụng và CHECK của DB; gateway gộp danh sách. Người duyệt phải có quyền duyệt **lúc duyệt** (bị giáng chức sau khi lập yêu cầu thì không được). Áp dụng cho hoàn tay của admin và trả hàng ≥ `PAYMENT_LARGE_REFUND_APPROVAL_VND` (5.000.000); không chặn hoàn tự động của saga. **Gom theo payment trong 24 giờ**: tổng các lần hoàn tay của cùng payment ≥ ngưỡng cũng phải duyệt (chống chia nhỏ).
- **F12** Khoá gian hàng: `order` gọi `GetShop` kiểm `ACTIVE` đồng bộ lúc checkout (chốt cuối); `product` nhận `user.shop_status_changed` ẩn sản phẩm qua cờ `shop_suspended`, không đổi `status` gốc; sản phẩm người bán tự ẩn vẫn ẩn khi mở lại; đơn đang chạy xử lý bình thường; thu hồi refresh token. Event qua **outbox** của `user`, idempotent, có version để event cũ đến sau không đè event mới.
- **F13** Nhật ký hoạt động người dùng: append-only, chỉ ghi sự kiện auth và thay đổi hồ sơ/trạng thái; IP lưu đã che một phần; lưu tối đa 180 ngày (job xoá). `admin` xem được, nhưng không chứa mật khẩu, OTP, token. Danh sách đơn gần đây gọi `order` bằng JWT của người xem.
- **F14** Buộc đổi mật khẩu lần đầu của nhân viên: cắt; `super_admin` tạo tài khoản rồi gửi thư đặt lại mật khẩu (không ai biết mật khẩu ban đầu). Xuất CSV audit: cắt.
- **F15** Web: menu, huy hiệu và trang theo vai trò; mở thẳng trang không đủ quyền thấy màn hình từ chối, không gọi API sẽ 403. Gỡ dữ liệu mẫu mà A7 thay; phần mẫu ngoài A7 để B-track.
