# Đợt 1 – vòng sửa 2 (sau review độc lập)

Luật chung vẫn theo `.agents/briefs/fix/rules.md` (sở hữu, test trước sửa sau, chỉ rebuild service mình, không git write). Phát hiện gốc: `.agents/briefs/fix/review-B-payment.md`, `.agents/briefs/fix/review-C-others.md` (mục đánh số trong file). Mỗi mục: test tái hiện đỏ → sửa → xanh; báo cáo dán dòng đỏ/xanh và lệnh kiểm.

| Agent | Mục phải sửa |
|---|---|
| payment | review-B: toàn bộ P0 (1, 2, 3), P1 (4, 5), P2 (6, 7, 8, 9), P3 (10) và mục over-engineering (xoá dead code `AllocationSplit`/`compute_allocation_split` và các mục khác nếu đúng). P0-2: kiểm `existing.user_id`, `order_group_id`, `amount_vnd`, `method` khi trùng `idempotency_key` (khác → Conflict); áp cùng nguyên tắc cho `create_refund` (P2-8). |
| web | review-C: P1-2 (form đăng ký/nộp lại hồ sơ shop theo luật backend mới: hiện form khi `PENDING` có `rejection_reason`, nhãn "Yêu cầu bổ sung", bắt buộc địa chỉ lấy hàng và giấy tờ, regex SĐT khớp backend `services/user` `isValidVNPhone`), P2-6 (`types.ts` dùng `\|\|` thay `??` cho chuỗi rỗng), P2-7 (`canRetryPayment` và `needsVietQrPayment` không cho thanh toán lại khi đã quá hạn). |
| gateway | review-C: P1-4 (gửi metadata `x-client-ip` = `ClientIP` đã sửa cho mọi gọi gRPC xuất phát từ HTTP request, ít nhất 3 route admin do `user` tự audit), P2-9 (che thông điệp `Unavailable`/`DeadlineExceeded`), P2-10 (route sản phẩm công khai fail-closed với `UNSPECIFIED`, `total` khớp danh sách sau lọc). |
| user | review-C: P2-5 (`ResubmitShop` thêm điều kiện trạng thái trong `WHERE`, không còn dòng → 409), P2-13 (`peerIPFromContext` tách host bằng `net.SplitHostPort`; thiếu `peer` thì không tin `x-client-ip`). |
| shipping | review-C: P2-8 (`USER_GRPC_ADDR` mặc định `localhost:50051` trong code; trong `docker-compose.yml` CHỈ khối `shipping` thêm `USER_GRPC_ADDR: user:50051` và `depends_on` user healthy; `.env.example` thêm biến). |
| product | review-C: P2-12 (`tests/e2e/18_product_rules_test.go` không assert `Total == 1` trên dữ liệu dùng chung; dùng từ khoá có tag riêng hoặc kiểm có chứa đúng id). |
| contract | review-C: P2-11 (`docs/contracts.md` `RecordCODRejection` chỉ token service). |
| qa | (a) `tests/e2e/client.go` (`DoAbsolute`) và `tests/e2e-web/helpers.mjs` (hàm gửi request chung): gặp HTTP 429 thì đợi theo `Retry-After` (mặc định 5 giây nếu thiếu) rồi thử lại, tổng thời gian chờ mỗi request tối đa 120 giây, log một dòng mỗi lần chờ; không thử lại các mã khác. (b) review-C P1-3: `tools/seed/seed.mjs` `listAllAdminProducts` dùng `/api/v1/admin/products` (kiểm shape phản hồi), chạy `make seed` hai lần liên tiếp trên stack `nexusmart-audit` để chứng minh idempotent (lần 2 toàn `existing`). (c) Chạy `make e2e` đầy đủ một lần và dán kết quả (agent khác có thể đang rebuild service của họ: ghi rõ nếu đỏ vì lý do đó). Sở hữu: `tests/e2e/client.go`, `tests/e2e-web/helpers.mjs`, `tools/seed/**`. |

## Bổ sung sau review A (`.agents/briefs/fix/review-A-order-promotion.md`)
| Agent | Mục phải sửa |
|---|---|
| order | review-A: P0 (1, 2, 3, 4), P1-7, P2 (8, 9, 10), P3-12 và mục over-engineering của order. Thêm (từ review-B P0-2, phòng thủ chiều sâu): `RetryPayment` gắn tiền tố nhóm đơn vào idempotency key gửi payment (ví dụ `retry:<checkout_group_id>:<key client>`; key rỗng thì sinh key ổn định theo nhóm + payment hiện tại). |
| promotion | review-A: P1-5 (trang voucher seller không bị lọc theo tiêu chí người mua khi seller xem voucher của chính shop mình), P1-6 (`ListAvailableVouchers` không tin `user_id` trong request khác claims, trừ admin/service), P2-11 (freeship không bị loại sai vì ngân sách) và mục over-engineering của promotion. |
