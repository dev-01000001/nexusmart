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
- **E20** PAY03: file CSV UTF-8 (BOM được) `tracking_code,cod_amount_vnd,delivered_at[,order_code]`, ≤ 2 MiB, ≤ 5.000 dòng, số tiền chỉ chữ số. Dòng khớp (mã vận đơn, đúng tiền, đơn `DELIVERED`/`COMPLETED`) ghi Nợ `gateway_clearing` / Có `cod_receivable` một lần (khoá idempotent theo payment+order+shop, không theo file); lệch tiền, mã lạ, chưa giao → `reconciliation_queue`, không bút toán. `order_code` chỉ dùng khi allocation chưa có mã vận đơn. Luồng `order.completed` → `seller_pending` KHÔNG đổi (không cộng tiền người bán lần hai, không mở tiền trước hạn trả hàng); ghi chỗ lệch với bước 3–4 của UC vào báo cáo. Cùng file tải hai lần → 0 bút toán mới, tổng nợ = tổng có.
- **E21** A12 còn lại: bỏ hằng số phí mẫu ở web, đọc từ API; không làm `api_token` trong `AdminTestCarrier`.

## 3. Phân công 3b-1
`contract` (proto, `make proto`, `pkg/common/auth`, subject `review.>` ở Go và Rust, `docs/contracts.md`, compose, `.env.example`) chạy trước, một mình. Rồi nhóm 1: product, shipping, order, payment, user; nhóm 2: cart, promotion, notification, gateway; web sau khi gateway xong hợp đồng JSON. QA e2e cuối. Review độc lập hai lượt: payment, order, product (từ cấm + quyền đánh giá), consumer 4 service.

Test mới chạy lặp: Go `-race -count=5`, Rust 50 lượt; test phải có ca tấn công/đua nêu ở mục 3 của thiết kế. Máy chỉ 14,6 GB RAM: tối đa 4 agent cùng lúc (2026-11-24 đo load 133 khi 6 agent cùng index codebase-memory).
