# Worker `usr` – tài khoản, người bán, khách hàng, quản trị người dùng

Luật chung: `.agents/briefs/audit/rules.md`. Báo cáo: `docs/audit/usr.md`, mã lỗi `USR-B<n>`, dữ liệu `USR-D<n>`, đề xuất `USR-F<n>`.

## UC phụ trách (chi tiết luồng chính/ngoại lệ trong `docs/spec.md` tab Use case)
UC-B01, UC-B02, UC-B11, UC-B19, UC-B27, UC-S01, UC-S14, UC-A01, UC-A03, UC-A11, UC-A10 (phần phạt vi phạm), UC-S11, UC-B24, UC-SHIP01 (phần sổ địa chỉ CRUD của người mua/người bán; phần đơn vị hành chính và bảng ánh xạ do `shp` kiểm).

## Đối chiếu Shopify/sàn
- Thiết lập cửa hàng: đăng ký, gói dịch vụ và billing, tên miền, nhân viên và phân quyền, chính sách cửa hàng, cài đặt thuế và tiền tệ.
- Khách hàng: hồ sơ, tài khoản, phân nhóm (segment), tag, đồng ý nhận tin, xoá dữ liệu theo yêu cầu.
- Người bán (sàn): đăng ký và xét duyệt, xác minh danh tính (KYC), xếp hạng shop, xử lý vi phạm.

## Phải kiểm trên stack
- Đăng ký email + OTP (hết hạn, nhập sai nhiều lần, gửi lại), đăng nhập, refresh token xoay vòng (dùng lại refresh cũ phải bị từ chối), logout thu hồi, access token 15 phút (đọc claim `exp`), mật khẩu argon2id (SELECT cột hash trong `user_db`, xem tiền tố `$argon2id$`), quên/đặt lại mật khẩu, đổi mật khẩu, Google login.
- Dò tài khoản (thông báo lỗi đăng ký/đăng nhập có lộ email tồn tại không), chống brute force đăng nhập/OTP.
- Người mua đăng ký gian hàng → admin duyệt/từ chối kèm lý do/yêu cầu bổ sung → role đổi sang `seller` và `shop_id` vào claim → email thông báo kết quả (Mailpit). Giấy tờ/KYC có lưu không.
- Admin: danh sách/khoá/mở người dùng, đổi vai trò, vai trò nội bộ, audit log chỉ ghi thêm (thử UPDATE/DELETE qua API có bị chặn không; trong DB có trigger/quyền chặn không), audit log có ghi cho mọi thao tác quản trị không (duyệt shop, duyệt sản phẩm, khoá user, tạo voucher sàn...).
- Phân quyền theo đối tượng: buyer A đọc/sửa địa chỉ, hồ sơ, đơn của buyer B; seller shop A sửa dữ liệu shop B; buyer gọi API admin/seller; JWT sai chữ ký; token service dùng từ ngoài gateway.
- Sổ địa chỉ: tạo/sửa/xoá/mặc định, validate tỉnh/phường theo 34 tỉnh mới.
- Hồ sơ shop công khai không lộ dữ liệu riêng tư (email, SĐT, CCCD, tài khoản ngân hàng của chủ shop).
- Dữ liệu: 4 shop seed và buyer demo khớp `web/src/data/shops.ts`, `orders.ts` (tên, slug, địa chỉ, trạng thái active, role).
