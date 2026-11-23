# Đợt 2a – agent `gateway`
Luật: `w2-rules.md`. Hợp đồng: `docs/contracts.md` (bảng REST mới của 2a).
## Sở hữu
`services/gateway/**`, `tests/e2e/23_*.go`.
## Việc
1. USR-B1: middleware Auth đọc `user:revoked_jti:<jti>` và `user:revoked_before:<sub>` bằng một `MGET`; token `iat <= revoked_before` → 401 (thiếu `iat` coi như bị thu hồi khi có khoá). Áp cho cả route optional-auth và proxy SSE. Lỗi Redis → 503 như hiện tại.
2. Mọi REST mới trong contracts (returns, refunds, reconciliation queue, shipping settings, carriers, redelivery, forgot/reset password), đúng RBAC: buyer scope theo claims, seller scope ép `shop_id` từ claims, admin audit tự động (middleware sẵn có; kiểm ẩn token hãng và số tài khoản khỏi `before/after` audit và log). Thao tác tiền của admin bắt buộc `Idempotency-Key`. Rate limit chặt cho `forgot-password`/`reset-password` theo IP (dùng cơ chế sẵn có của nhóm auth).
3. PLT-B6: header bảo mật (`X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, `Content-Security-Policy` tối thiểu cho API), body quá lớn → 413, `/webhooks/` có rate limit riêng (đủ rộng cho hãng).
4. Tập lệnh lấy nhãn in (SHP-B8) nếu agent shipping yêu cầu: route proxy có kiểm quyền; nếu chưa có yêu cầu thì bỏ qua và ghi báo cáo.
## Test bắt buộc
Unit: MGET hai khoá, iat biên, 413, header, che trường nhạy cảm trong audit, RBAC từng route mới (buyer gọi route seller/admin → 403). E2E `23_gateway_w2_test.go`: token cũ sau khi bị khoá → 401; `forgot-password` bị giới hạn; thiếu `Idempotency-Key` → 400.
