# Đợt 2b – agent `user` (sở hữu `services/user/**`, `tests/e2e/30_*.go`)
Theo `w2b-plan.md` §4 (UC-S17, A06), §8. Migration `0005_`.
1. Tài khoản ngân hàng shop: AddShopBankAccount (chỉ chủ shop ACTIVE), OTP email (event `user.shop_bank_account_otp`, OTP hash, hạn, giới hạn lần, không log), VerifyShopBankAccountOTP; tên chuẩn hoá không dấu khớp tên chủ shop → ACTIVE, không khớp → PENDING_REVIEW (admin duyệt); đổi tài khoản → khoá rút `USER_BANK_PAYOUT_LOCK_HOURS`; chỉ một tài khoản hiệu lực; số tài khoản che ở mọi phản hồi trừ GetShopBankAccountForPayout (chỉ service token, từ chối JWT người dùng); audit.
2. GetPlatformUserStats theo khoảng ngày VN (người dùng mới, shop mới/đang hoạt động).
Test: unit + e2e 30 (OTP qua Mailpit, sai OTP quá lần, tên không khớp → chờ duyệt, đổi tài khoản khoá rút, shop khác 403, số tài khoản không lộ trong log/phản hồi).
