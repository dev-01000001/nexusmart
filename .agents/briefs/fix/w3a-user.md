# 3a – agent `user` (sở hữu `services/user/**`, `tests/e2e/38_*.go`)
Theo `w3a-plan.md` D5, D6.
1. D5 đầy đủ (migration `0007_`, backfill, chỉ payout giải mã). Test Postgres thật: DB cũ có dòng rõ → khởi động → không còn dạng rõ, `GetShopBankAccountForPayout` trả đúng số; chạy backfill hai lần và hai bản song song an toàn; khoá sai → không khởi động/không ghi đè; dòng mới không có dạng rõ. `BANK_ACCOUNT_KEY` trống → không khởi động.
2. Kiểm log, audit, event, lỗi: không có số tài khoản đầy đủ hay bản mã.
3. D6 cả hai RPC, test đua: admin xem A, seller đổi sang B (OTP xong), admin duyệt A → 409 và không dòng nào VERIFIED sai; full_name đổi giữa lúc xem và duyệt → 409.
E2E 38: duyệt thiếu `bank_account_id` → 400, duyệt bản cũ → 409.
