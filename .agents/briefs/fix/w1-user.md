# Đợt 1 – pha 1B – agent `user`

Luật chung: `rules.md`. Hợp đồng mới: `UserService.RecordCODRejection`. Chi tiết lỗi: `docs/audit/usr.md`.

## Sở hữu
`services/user/**`, `tests/e2e/15_*.go`.

## Việc
1. R6: cài `RecordCODRejection` (chỉ token service): idempotent theo `order_id` (bảng mới, migration `0003_`), tăng `users.cod_rejected_count` đúng một lần mỗi đơn.
2. USR-B2: hồ sơ gian hàng ở REQUEST_INFO hoặc REJECTED: người bán nộp lại qua chính `POST /api/v1/shops/register` (cập nhật hồ sơ cũ, trạng thái về PENDING, cho dùng lại tên shop cũ của chính mình); REJECT và REQUEST_INFO bắt buộc có lý do; `RegisterShop` bắt buộc `pickup_address_id` (thuộc chính người đó), `contact_phone` hợp lệ, giấy tờ (trường giấy tờ hiện có).
3. USR-B3: `AdminReviewShop` chỉ chuyển từ PENDING sang ACTIVE/REJECTED/REQUEST_INFO; quyết định trên shop ACTIVE → 409. (Tạm ngưng shop SUSPENDED cần đổi proto: ghi vào báo cáo cho đợt 2.)
4. USR-B6: sổ địa chỉ kiểm `province_code`/`ward_code` thuộc dữ liệu 34 tỉnh của `shipping` (dùng client gRPC tới shipping nếu đã có, `SHIPPING_GRPC_ADDR`), kiểm SĐT Việt Nam; `PUT` không tự đặt `is_default = false` khi client không gửi trường đó (nếu proto không phân biệt được, ghi rõ trong báo cáo và giữ an toàn: không đổi cờ mặc định khi chỉ sửa trường khác); chặn xoá địa chỉ đang là `pickup_address_id` của shop ACTIVE (409).
5. USR-B7: xác minh OTP email không đặt `phone_verified = true`; đổi SĐT qua `PATCH /users/me` đặt `phone_verified = false`.
6. USR-B5 (sau allowlist của pha 1A): `WriteAuditLog` chỉ nhận admin hoặc token service, có trường IP do gateway truyền thì dùng IP đó (chỉ tin khi gọi bằng token service hoặc từ gateway); `ValidateUser` theo đúng quyết định của pha 1A.

## Test bắt buộc
Unit test cho từng mục 1–6. E2E `tests/e2e/15_user_rules_test.go`: nộp lại hồ sơ sau REQUEST_INFO; REJECT shop ACTIVE → 409; địa chỉ mã tỉnh sai → 400; xoá địa chỉ kho của shop ACTIVE → 409.
