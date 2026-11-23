# Báo cáo rà soát nghiệp vụ `usr` — Tài khoản, người bán, khách hàng, quản trị người dùng

- **Commit kiểm tra**: `0db2cddedf77ad3f326a3a4b0dbad459fd6ca843` (`HEAD`)
- **Stack kiểm chứng**: `nexusmart-audit` (Gateway `:48080`, Web BFF `:43000`, User ops `:48081`, Mailpit `:48025`, Postgres `user_db` / `shipping_db` / `promotion_db` / `order_db`, Redis chỉ đọc)
- **Unit test đọc tại host**: `go test` xanh trên `services/user/internal/{password,security,service}` và `services/gateway/internal/{middleware,router}`.

---

## 1. Bảng đối chiếu Use Case (14 UC phụ trách)

| UC | Ưu tiên | Trạng thái | Backend (file:dòng) | REST | Web | Bằng chứng | Ghi chú |
| --- | --- | --- | --- | --- | --- | --- | --- |
| UC-B01 | Cao | LỖI | `services/user/internal/service/server.go:131-305`, `services/user/internal/security/redis_store.go:28-180`, `services/user/internal/password/argon2.go:58-71` | `POST /api/v1/auth/register`, `/verify-otp`, `/resend-otp` | `/dang-ky`, `/api/auth/{register,verify-otp,resend-otp}` | `POST /api/v1/auth/register` -> `201` (`PENDING_VERIFICATION`), Mailpit nhận OTP 6 số (TTL Redis `~600s`), 5 lần sai OTP -> `429` và xoá OTP (`400 otp not found or expired`), gửi lại 3 lần `200`, lần 4 `429`, verify OTP mới -> `200`; `user_db.users` lưu `$argon2id$v=19$m=19456,t=2,p=1$...`; đăng ký với `phone:""` verify email OTP vẫn ra `phone_verified:true` | Lỗi P1 `USR-B7` (gán `phone_verified=true` cả khi `phone` rỗng); P2 `USR-B8` (lộ email tồn tại qua `verify-otp`/`resend-otp`), `USR-B9` (link `/verify-otp` 404), `USR-D4` (`/dang-ky` hứa tặng 15.000 Xu nhưng thực tế 0 Xu). |
| UC-B02 | Cao | MỘT PHẦN | `services/user/internal/service/server.go:307-515`, `services/gateway/internal/middleware/auth.go:46-85`, `pkg/common/auth/auth.go:98-186` | `POST /api/v1/auth/login`, `/refresh`, `/logout` | `/dang-nhap`, `/api/auth/{login,refresh,logout,session}` | `POST /api/v1/auth/login` -> `200`, JWT `HS256` có `exp-iat=900` (15p); xoay vòng refresh + dùng lại RT cũ -> `401 refresh token reuse detected` và thu hồi cả RT mới; `POST /logout` -> `GET /users/me` trả `401 access token has been revoked` (`user:revoked_jti:*` TTL ~880s); 5 lần sai MK -> `429` (`user:login_lock:*` TTL ~880s), nhập đúng MK khi đang khoá vẫn `429`; BFF đặt cookie `HttpOnly; SameSite=lax` và chặn CSRF `403` | Lõi xác thực/xoay vòng/thu hồi/chống brute-force chạy chuẩn. Thiếu lý do khoá & thông tin liên hệ khi tài khoản `LOCKED` đăng nhập, timing side-channel ~9ms vs ~100ms (`USR-B8`), và `/dang-nhap` không chuyển trang mặc định theo role (`USR-B10`). |
| UC-B11 | Trung bình | THIẾU | Không có (`rg -i "google|oauth|forgot|reset.?password" proto/ services/` chỉ khớp import `google/protobuf`, `google.golang.org/grpc`) | Không có (`POST /api/v1/auth/forgot-password`, `/reset-password`, `/google`, `GET /api/v1/auth/google` đều `404`) | Không có trong `/dang-nhap` (`web/src/app/(shop)/dang-nhap/page.tsx`) | Gọi 4 endpoint trên đều trả `404 NOT_FOUND` | Đã ghi nhận chưa làm ở `CORE_REPORT.md` §5; trên `HEAD` vẫn chưa có cả Google OAuth 2.0 lẫn quên/đặt lại mật khẩu. |
| UC-B19 | Trung bình | MỘT PHẦN | `services/user/internal/service/server.go:517-597` | `GET /api/v1/users/me`, `PATCH /api/v1/users/me` | `/tai-khoan` (`web/src/features/account/AccountClient.tsx:257-302,843-966`) | `PATCH /api/v1/users/me` cập nhật `full_name`, `phone`, `avatar_url` -> `200`, bỏ qua trường giả mạo `email`/`role`/`status`, chặn trùng SĐT -> `409`; `POST /users/me/change-password`, `PUT /users/me/password`, `GET /users/me/sessions` đều `404` | Mới có xem/sửa `full_name`, `phone`, `avatar_url`. Thiếu: ngày sinh (`birthday`), đổi email, xác thực OTP khi đổi SĐT (`USR-B7`), đổi mật khẩu, quản lý thiết bị/phiên & đăng xuất từ xa, audit log & email cảnh báo đổi thông tin nhạy cảm. |
| UC-B24 | Thấp | THIẾU | Không có (`rg -i "report|abuse|violation|flag" proto/ services/user/ services/gateway/internal/router/router.go`) | Không có | Không có trong `web/src/app/(shop)` | Soát `proto/`, `router.go`, `services/user/`, `web/src/app/(shop)` không có chức năng báo cáo sản phẩm/gian hàng vi phạm | Mức Thấp (spec `docs/spec.md:39`: chỉ đặc tả, không bắt buộc cài đặt). |
| UC-B27 | Thấp | THIẾU | Không có (`rg -i "delete.?account|export.?data|privacy|gdpr" proto/ services/user/ services/gateway/internal/router/router.go`) | Không có (`DELETE /api/v1/users/me` -> `405`, `POST /api/v1/users/me/export` -> `404`) | Không có trong `web/src/app/(shop)/tai-khoan` | `DELETE /api/v1/users/me` -> `405`, `POST /api/v1/users/me/export` -> `404` | Mức Thấp (không bắt buộc cài đặt theo spec). |
| UC-S01 | Cao | LỖI | `services/user/internal/service/server.go:833-982`, `services/gateway/internal/router/handlers_user.go:602-726` | `POST /api/v1/shops/register`, `GET /api/v1/shops`, `/shops/{id}`, `/shops/slug/{slug}`, `/shops/me`, `GET /api/v1/seller/shop` | `/tai-khoan` tab "Mở gian hàng", `/gian-hang/[slug]`, `/seller/ho-so` | `POST /api/v1/shops/register` -> `201` (`PENDING`), trùng tên -> `409`; shop `PENDING`/`REJECTED` ẩn khỏi public (`404`); shop `ACTIVE` công khai chỉ trả 7 trường an toàn (`id,name,slug,description,status,created_at,updated_at`); sau duyệt gọi `POST /auth/refresh` có `role:"seller"` & `shop_id` trong JWT và `GET /seller/shop` -> `200` | Lỗi P1 `USR-B2` (`REQUEST_INFO` bị kẹt không bổ sung được; sau `REJECT` không nộp lại được cùng tên shop cũ; `RegisterShop` chấp nhận rỗng `pickup_address_id`/`contact_phone`/`business_license_doc`); P2 `USR-B9` (link thông báo `/seller/dashboard` bị 404); KYC chỉ là 1 chuỗi text `business_license_doc`, không upload giấy tờ/CCCD. |
| UC-S11 | Thấp | THIẾU | Không có (`rg -i "penalty|strike|sanction|violation" proto/ services/user/ services/gateway/internal/router/router.go`) | Không có | Không có trang hiệu quả & điểm phạt trong `web/src/app/(seller)` | Soát `proto/`, `services/user/`, `router.go`, `web/src/app/(seller)` không có điểm phạt hay bảng chỉ số vận hành (tỷ lệ phản hồi/giao trễ/huỷ/khiếu nại điểm phạt) | Mức Thấp (không bắt buộc cài đặt theo spec). |
| UC-S14 | Thấp | THIẾU | Không có (`rg -i "staff|employee|sub.?account" proto/ services/user/ services/gateway/internal/router/router.go`) | Không có | Không có trong `web/src/app/(seller)` | Soát `proto/`, `services/user/`, `router.go`, `web/src/app/(seller)` không có quản lý nhân viên gian hàng hay phân quyền theo shop | Mức Thấp (không bắt buộc cài đặt theo spec). |
| UC-A01 | Cao | LỖI | `services/user/internal/service/server.go:984-1108`, `services/notification/internal/service/notification.go:336-366` | `GET /api/v1/admin/shops`, `POST /api/v1/admin/shops/{id}/review` | `/admin/gian-hang` (`200` với cookie admin) | `POST /api/v1/admin/shops/{id}/review` chạy cả `REQUEST_INFO`, `REJECT`, `APPROVE` -> `200`, gửi email Mailpit đủ 3 quyết định, tạo in-app notification, ghi `audit_logs`, và nâng `buyer -> seller` + gán `shop_id` khi `APPROVE` | Lỗi P1 `USR-B2` (`REQUEST_INFO` gây dead-end; backend cho `REJECT` với `reason:""`) và P1 `USR-B3` (cho `REJECT` cả shop đang `ACTIVE` mà không thu hồi role `seller` / `shop_id`; chưa có thao tác `SUSPENDED`). |
| UC-A03 | Cao | LỖI | `services/user/internal/service/server.go:635-831`, `services/gateway/internal/router/handlers_user.go:728-805` | `GET /api/v1/admin/users`, `PATCH /api/v1/admin/users/{id}/status`, `PATCH /api/v1/admin/users/{id}/role` | `/admin/cau-hinh` (`AdminUsersPanel.tsx`, `200` với cookie admin) | Tìm theo email/SĐT -> `200`; tự khoá / tự hạ quyền -> `409`; `admin` khoá/nâng `super_admin` -> `403`; khoá user chặn login `403` & chặn refresh `401`, **NHƯNG** JWT cũ của user bị khoá gọi `GET/PATCH /users/me` & `GET /cart` vẫn `200` (sửa được `full_name`!), và JWT cũ của admin vừa bị hạ quyền gọi `GET /admin/users` & `GET /admin/audit-logs` vẫn `200` | Lỗi **P0 `USR-B1`** (không thu hồi access token khi khoá hoặc hạ quyền; đổi role không thu hồi cả refresh token). Thiếu xem chi tiết/lịch sử hoạt động user (`GET /admin/users/{id}`), thiếu `super_admin` trên UI (`USR-B10`). |
| UC-A10 (phần phạt vi phạm) | Thấp | THIẾU | Không có (`rg -i "penalty|violation|sanction|strike" proto/ services/user/ services/gateway/internal/router/router.go`) | Không có | Không có phần phạt vi phạm shop ở `web/src/app/(admin)` (`/admin/kiem-duyet` chỉ mock kiểm duyệt đánh giá/Q&A) | Soát `proto/`, `services/user/`, `router.go`, `web/src/app/(admin)` không có hệ thống điểm phạt, ngưỡng hạn chế quyền hay khoá shop theo vi phạm | Mức Thấp (không bắt buộc cài đặt theo spec). |
| UC-A11 | Cao | LỖI | `services/user/internal/service/server.go:1326-1432`, `services/user/migrations/0001_init.sql:75-109` | `GET /api/v1/admin/audit-logs` | `/admin/audit-log` (`200` với cookie admin) | `GET /api/v1/admin/audit-logs?target_id=...&from_time=...&to_time=...` -> `200`; `PUT/DELETE /admin/audit-logs` -> `405`, `PATCH/DELETE /admin/audit-logs/{id}` -> `404`; DB có 2 trigger `trg_audit_logs_no_update` và `trg_audit_logs_no_delete`; thử gán role `"moderator"` -> `400` | Lỗi P1 `USR-B4` (chỉ 3 thao tác trong `user` ghi audit log, `WriteAuditLog` có 0 caller; `ip_address` ghi IP Docker gateway `172.20.0.15:...`; thiếu toàn bộ vai trò nội bộ kiểm duyệt/CSKH/kế toán/vận hành và duyệt 2 người; thiếu trigger `BEFORE TRUNCATE` & DB dùng superuser), P1 `USR-B5` (`WriteAuditLog` hở auth gRPC ẩn danh), P2 `USR-B10` (web thiếu lọc thời gian). |
| UC-SHIP01 (phần sổ địa chỉ CRUD) | Cao | LỖI | `services/user/internal/service/server.go:1110-1324`, `services/gateway/internal/router/handlers_user.go:477-600` | `GET/POST /api/v1/users/me/addresses`, `PUT/DELETE /api/v1/users/me/addresses/{id}` | `/tai-khoan` tab Địa chỉ (`AddressForm.tsx`) | CRUD + đổi mặc định chạy được (`201`/`200`), chặn IDOR buyer A sửa/xoá địa chỉ buyer B (`403`); **NHƯNG** tạo địa chỉ với `province_code:"INVALID_999"`, `ward_code:"FAKE_WARD_000"`, `recipient_phone:"NOT-A-PHONE"` vẫn trả `201`; `PUT` bỏ qua `is_default` tự reset về `false`; xoá địa chỉ `pickup_address_id` của shop `ACTIVE` trả `200` | Lỗi P1 `USR-B6` (không validate mã 34 tỉnh/phường và SĐT; `PUT` mất `is_default` & thiếu trường `legacy_*`; cho xoá địa chỉ kho đang gắn với shop `ACTIVE`; `AddressForm` chưa nối `/shipping/addresses/map-legacy`). |

---

## 2. Bảng đối chiếu Shopify / sàn

| Nghiệp vụ | UC hoặc ngoài phạm vi | Trạng thái | Ghi chú |
| --- | --- | --- | --- |
| Thiết lập cửa hàng: đăng ký | `UC-S01` (Cao) | LỖI | Đã có `POST /api/v1/shops/register` + `/tai-khoan`, nhưng mắc `USR-B2` (`REQUEST_INFO` kẹt, `REJECT` không nộp lại được tên cũ, trường bắt buộc bị bỏ lỏng). |
| Thiết lập cửa hàng: gói dịch vụ và billing | Ngoài phạm vi đặc tả (`docs/spec.md:39` — không có trong tab Use case; sàn thu phí hoa hồng đơn ở `UC-A09`) | — | Sàn không bán gói thuê bao cửa hàng. |
| Thiết lập cửa hàng: tên miền riêng | Ngoài phạm vi đặc tả (`docs/spec.md:39` — không có trong tab Use case) | — | Hiện chỉ hỗ trợ đường dẫn slug `/gian-hang/[slug]`. |
| Thiết lập cửa hàng: nhân viên và phân quyền | `UC-S14` (Thấp, nhân viên gian hàng) & `UC-A11` (Cao, nhân viên sàn) | THIẾU / LỖI | Chưa có nhân viên gian hàng (`UC-S14`) và chưa có tài khoản/vai trò nhân viên nội bộ sàn (`UC-A11`). |
| Thiết lập cửa hàng: chính sách cửa hàng | Một phần `UC-SHIP06` (Trung bình, vận chuyển/freeship — `shp` kiểm) & `UC-S12` (Thấp); chính sách văn bản riêng của shop ngoài phạm vi backend hiện tại | THIẾU | Chưa có API/DB lưu chính sách đổi trả/freeship/xưởng riêng cho shop (phần `/seller/ho-so` hiện giữ mock cục bộ trong `sellerStore.ts`). |
| Thiết lập cửa hàng: cài đặt thuế và tiền tệ | Ngoài phạm vi đặc tả (`docs/spec.md:27` cột Ngoài phạm vi: *"hoá đơn điện tử"*, tiền chỉ dùng số nguyên VND; `AGENTS.md` luật 6: thêm mã tiền tệ cần ADR; `docs/spec.md:39` không có UC cấu hình thuế/đa tiền tệ) | — | Toàn hệ thống cố định `int64` VND (`*_vnd`), chưa có cấu hình thuế/đa tiền tệ. |
| Khách hàng: hồ sơ | `UC-B19` (Trung bình) | MỘT PHẦN | Có `GET/PATCH /users/me` cho `full_name`, `phone`, `avatar_url`; thiếu ngày sinh, đổi email, đổi mật khẩu, xác thực OTP khi đổi SĐT, quản lý phiên. |
| Khách hàng: tài khoản | `UC-B01` (Cao), `UC-B02` (Cao), `UC-B11` (Trung bình) | LỖI / MỘT PHẦN / THIẾU | Đăng ký email+OTP, đăng nhập, refresh rotation, logout hoạt động (còn `USR-B7`, `USR-B8`, `USR-B10`); thiếu hoàn toàn Google OAuth và quên/đặt lại mật khẩu (`UC-B11`). |
| Khách hàng: phân nhóm (segment) | Ngoài phạm vi đặc tả (`docs/spec.md:29` cột Ngoài phạm vi: *"chương trình thành viên theo hạng"*; `docs/spec.md:39` không có UC segment) | — | Hạng hội viên 4 quý trên web hiện là mock. |
| Khách hàng: tag | Ngoài phạm vi đặc tả (`docs/spec.md:39` — không có trong tab Use case) | — | Không có trong đặc tả. |
| Khách hàng: đồng ý nhận tin | Liên quan `UC-B23` (Trung bình, tuỳ chọn kênh thông báo — `plt` kiểm chi tiết); consent marketing riêng không có trong `user` | MỘT PHẦN | Chỉ có bảng `notification_preferences` (bật/tắt email/in-app/push theo nhóm), chưa có lưu vết thời điểm đồng ý nhận tin tiếp thị ở `user_db`. |
| Khách hàng: xoá dữ liệu theo yêu cầu | `UC-B27` (Thấp) | THIẾU | Chưa cài đặt (mức Thấp). |
| Người bán (sàn): đăng ký và xét duyệt | `UC-S01` (Cao), `UC-A01` (Cao) | LỖI | Có quy trình đăng ký & duyệt cơ bản nhưng hỏng nhánh `REQUEST_INFO`, nộp lại sau `REJECT` cùng tên và state guard `ACTIVE -> REJECT` (`USR-B2`, `USR-B3`). |
| Người bán (sàn): xác minh danh tính (KYC) | Một phần `UC-S01` / `UC-A01` (Cao — *"giấy tờ"*) | MỘT PHẦN | Chỉ lưu một cột chuỗi tự do `shops.business_license_doc` (không bắt buộc); chưa có upload tệp giấy phép/CCCD hay bảng/quy trình xác minh danh tính (KYC). |
| Người bán (sàn): xếp hạng shop | `UC-S11` (Thấp), `UC-B21` (Trung bình) | THIẾU | Backend chưa tính/lưu điểm xếp hạng, tỷ lệ phản hồi, tỷ lệ huỷ hay giao trễ; `/gian-hang/[slug]` hiển thị số liệu tĩnh từ `web/src/data/shops.ts`. |
| Người bán (sàn): xử lý vi phạm | `UC-A10` (Thấp, phần phạt vi phạm), `UC-B24` (Thấp), `UC-S11` (Thấp) | THIẾU | Chưa có báo cáo vi phạm, thang điểm phạt, hay khoá/tạm ngưng shop do vi phạm (`SUSPENDED` chưa có endpoint kích hoạt). |

---

## 3. Danh sách lỗi

### `USR-B1` (P0) — Khoá tài khoản (`LOCKED`) và hạ quyền (`admin -> buyer`) không thu hồi access token đang hoạt động
- **Tái hiện**:
  1. Đăng nhập tài khoản `audit-usr-admtgt-4220@nexusmart.local` lấy `$TGT_AT`, `$TGT_RT`.
  2. Admin khoá tài khoản:
     ```bash
     curl -X PATCH http://localhost:48080/api/v1/admin/users/$TGT_ID/status \
       -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
       -d '{"status":"LOCKED","reason":"AUDIT-usr test lock reason"}'
     # -> 200 OK
     ```
  3. Dùng `$TGT_AT` cũ gọi API bảo vệ:
     ```bash
     curl http://localhost:48080/api/v1/users/me -H "Authorization: Bearer $TGT_AT"
     # -> 200 OK (trả về status:"LOCKED"!)
     curl -X PATCH http://localhost:48080/api/v1/users/me -H "Authorization: Bearer $TGT_AT" \
       -H "Content-Type: application/json" -d '{"full_name":"Modified While Locked!"}'
     # -> 200 OK (sửa được hồ sơ khi đang bị khoá!)
     curl http://localhost:48080/api/v1/cart -H "Authorization: Bearer $TGT_AT"
     # -> 200 OK
     ```
  4. Nâng tài khoản trên lên `admin`, đăng nhập lấy `$TGT_ADMIN_AT`, sau đó `super_admin` hạ quyền về `buyer`:
     ```bash
     curl -X PATCH http://localhost:48080/api/v1/admin/users/$TGT_ID/role \
       -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
       -d '{"role":"buyer","reason":"AUDIT-usr demote to buyer"}'
     # -> 200 OK
     curl "http://localhost:48080/api/v1/admin/users?page_size=2" -H "Authorization: Bearer $TGT_ADMIN_AT"
     # -> 200 OK (total: 20)
     curl "http://localhost:48080/api/v1/admin/audit-logs?page_size=2" -H "Authorization: Bearer $TGT_ADMIN_AT"
     # -> 200 OK (total: 13)
     ```
- **Mong đợi vs thật**: Theo `UC-A03` (*"Hệ thống thu hồi token của tài khoản bị khoá. Thay đổi có hiệu lực ngay"*), mọi token (access + refresh) của tài khoản bị khoá hoặc bị hạ quyền phải bị từ chối ngay (`401`/`403`). Thực tế, tài khoản bị khoá vẫn dùng access token cũ sửa hồ sơ/truy cập dịch vụ trong tối đa 15 phút; cựu admin sau khi bị hạ quyền vẫn đọc được toàn bộ danh sách người dùng và audit log trong tối đa 15 phút, và `AdminUpdateUserRole` cũng không thu hồi refresh token.
- **Vị trí nghi là gốc**: `services/user/internal/service/server.go:725-729` (chỉ thu hồi refresh token), `services/user/internal/service/server.go:791-822` (không thu hồi cả refresh lẫn access token), `services/gateway/internal/middleware/auth.go:65-78` và `services/user/internal/service/server.go:89-103` (chỉ kiểm `user:revoked_jti:<jti>`, không kiểm mốc thu hồi theo `user_id` hay trạng thái tài khoản).

### `USR-B2` (P1) — Hồ sơ gian hàng bị kẹt vĩnh viễn sau `REQUEST_INFO`, không nộp lại được tên cũ sau `REJECT`, và thiếu ràng buộc trường bắt buộc
- **Tái hiện**:
  1. Buyer đăng ký gian hàng `"AUDIT-usr Shop 4220"` (`POST /api/v1/shops/register` -> `201`, `status:"PENDING"`).
  2. Admin yêu cầu bổ sung:
     ```bash
     curl -X POST http://localhost:48080/api/v1/admin/shops/$SHOP_ID/review \
       -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
       -d '{"decision":"REQUEST_INFO","reason":"AUDIT-usr bổ sung bản scan GPKD"}'
     # -> 200 OK (status vẫn là "PENDING")
     ```
  3. Chủ shop thử bổ sung/nộp lại:
     ```bash
     curl -X PATCH http://localhost:48080/api/v1/shops/me -H "Authorization: Bearer $BUYER_TOKEN" -d '{"business_license_doc":"GPKD-NEW"}'
     # -> 405 METHOD_NOT_ALLOWED
     curl -X POST http://localhost:48080/api/v1/shops/register -H "Authorization: Bearer $BUYER_TOKEN" \
       -H "Content-Type: application/json" -d '{"name":"AUDIT-usr Shop 4220 Updated","business_license_doc":"GPKD-NEW"}'
     # -> 409 ALREADY_EXISTS: "user already has a pending or active shop"
     ```
  4. Admin từ chối với lý do rỗng:
     ```bash
     curl -X POST http://localhost:48080/api/v1/admin/shops/$SHOP_ID/review \
       -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
       -d '{"decision":"REJECT","reason":""}'
     # -> 200 OK (status:"REJECTED", rejection_reason:"")
     ```
  5. Chủ shop nộp lại bằng đúng tên cũ `"AUDIT-usr Shop 4220"`:
     ```bash
     curl -X POST http://localhost:48080/api/v1/shops/register -H "Authorization: Bearer $BUYER_TOKEN" \
       -H "Content-Type: application/json" -d '{"name":"AUDIT-usr Shop 4220","business_license_doc":"GPKD-V2"}'
     # -> 409 ALREADY_EXISTS: "shop name is already taken"
     ```
  6. Đăng ký gian hàng bỏ trống mọi trường trừ `name`:
     ```bash
     curl -X POST http://localhost:48080/api/v1/shops/register -H "Authorization: Bearer $BUYER2_TOKEN" \
       -H "Content-Type: application/json" -d '{"name":"AUDIT-usr Bare 4220"}'
     # -> 201 Created (pickup_address_id:"", contact_phone:"", business_license_doc:"")
     ```
- **Mong đợi vs thật**: Theo `UC-A01` và `UC-S01`, khi admin chọn `REQUEST_INFO` chủ shop phải bổ sung được giấy tờ; khi `REJECT` phải bắt buộc có lý do và cho phép nộp lại hồ sơ (kể cả giữ tên shop cũ); khi đăng ký phải có địa chỉ kho, SĐT liên hệ, giấy tờ. Thực tế `REQUEST_INFO` làm hồ sơ treo cứng (cả API lẫn web `AccountClient.tsx:1291,1310` còn dán nhãn nhầm `"Lý do từ chối"` và ẩn form), `REJECT` rỗng vẫn `200`, nộp lại tên cũ bị `409`, và cho tạo shop rỗng địa chỉ kho/SĐT/giấy phép.
- **Vị trí nghi là gốc**: `services/user/internal/service/server.go:842-876,1039-1045`, `services/user/migrations/0001_init.sql:51`, `services/user/migrations/0002_add_shop_slug.sql:9`, `web/src/features/account/AccountClient.tsx:1289-1310`.

### `USR-B3` (P1) — `AdminReviewShop` cho phép `REJECT` gian hàng đang `ACTIVE` mà không thu hồi quyền `seller`, và thiếu cơ chế `SUSPENDED`
- **Tái hiện**:
  1. Admin duyệt `AUDIT-usr Bare 4220` (`APPROVE` -> `200`, `status:"ACTIVE"`, chủ shop thành `role:"seller"`, `shop_id:"288ec568-..."`).
  2. Admin tiếp tục gọi `POST /api/v1/admin/shops/$BARE_SHOP_ID/review` với `{"decision":"REJECT","reason":"AUDIT-usr reject active shop"}` -> **`200 OK`**, `shop.status:"REJECTED"`.
  3. Gọi `GET /api/v1/users/me` của chủ shop -> vẫn là **`role:"seller"`**, **`shop_id:"288ec568-7d1a-4e0b-b47a-5d0b9e3c2aa9"`**.
- **Mong đợi vs thật**: Không được phép `REJECT` hồ sơ đã `ACTIVE` (chỉ có thể tạm ngưng `SUSPENDED` qua thao tác riêng có thu hồi/khoá quyền bán), hoặc khi tước hoạt động phải đồng bộ quyền người bán. Hiện không có state guard trong `AdminReviewShop` và không có bất kỳ RPC/endpoint nào chuyển shop sang `SUSPENDED` dù `ShopStatus` enum và tab "Tạm ngưng" trên `/admin/gian-hang` đã khai báo.
- **Vị trí nghi là gốc**: `services/user/internal/service/server.go:1049-1076`, `web/src/app/(admin)/admin/gian-hang/page.tsx:260-268` (nút "Từ chối" vẫn bật khi `shop.status === "ACTIVE"`!).

### `USR-B4` (P1) — Audit log thiếu gần toàn bộ thao tác quản trị ngoài `user` service, ghi sai IP nguồn, thiếu vai trò nội bộ và duyệt 2 người (`UC-A11`)
- **Tái hiện**:
  1. Kiểm tra `user_db.audit_logs` ngay sau `make seed` (đã tạo 6 danh mục, duyệt 18 sản phẩm, tạo voucher sàn/freeship, tạo 3 khung flash sale + duyệt 16 item, cộng 42.000 Xu cho buyer demo, duyệt 4 shop):
     ```bash
     docker compose exec -T postgres psql -U nexusmart -d user_db \
       -c "SELECT action, target_type, ip_address FROM audit_logs;"
     # Kết quả trước khi worker chạy test: đúng 4 dòng admin.shop.review, ip_address = 172.20.0.15:43034
     ```
  2. Thử gán vai trò nội bộ theo `UC-A11`:
     ```bash
     curl -X PATCH http://localhost:48080/api/v1/admin/users/$TGT_ID/role \
       -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
       -d '{"role":"moderator","reason":"test internal role"}'
     # -> 400 INVALID_ARGUMENT: "target_user_id and valid role are required"
     ```
  3. Kiểm tra trigger và quyền DB trên `audit_logs`: chỉ có `BEFORE UPDATE` và `BEFORE DELETE` (không có `BEFORE TRUNCATE`), user kết nối DB `nexusmart` có `rolsuper = t` và đủ quyền `UPDATE/DELETE/TRUNCATE/TRIGGER`.
- **Mong đợi vs thật**: Theo `UC-A11` (Cao) và `docs/spec.md:26`, **mọi thao tác quản trị** phải ghi audit log kèm IP thật; phải có tài khoản nhân viên sàn với 4 vai trò nội bộ (kiểm duyệt, CSKH, kế toán, vận hành) và duyệt 2 người cho thao tác nhạy cảm (hoàn tiền lớn, khoá gian hàng). Thực tế `WriteAuditLog` có **0 lời gọi** trong toàn repo (`rg "WriteAuditLog" services/`), chỉ 3 thao tác trong `services/user` ghi log, `ip_address` luôn là IP container Docker của gateway (`172.20.0.15:...`), và không có vai trò nội bộ hay quy trình duyệt 2 người.
- **Vị trí nghi là gốc**: `services/user/internal/service/server.go:740,819,1087,1326-1379`, `services/user/internal/service/helpers.go:122-127`, `proto/nexusmart/user/v1/user.proto:9-15`, `services/user/migrations/0001_init.sql:93-109`, các handler `/admin/*` ở `services/gateway/internal/router/handlers_{product,promotion,payment}.go`.

### `USR-B5` (P1) — `WriteAuditLog` và `ValidateUser` không nằm trong `internalRPCs`, bỏ qua kiểm tra xác thực cho lời gọi gRPC nội bộ ẩn danh
- **Tái hiện** (kiểm chứng trực tiếp trên code `HEAD`, cổng gRPC `50051` không mở ra host):
  - `pkg/common/auth/auth.go:36-45`: `internalRPCs` chỉ có `GetAddress`, `GetProfile` của `UserService`; không có `WriteAuditLog` và `ValidateUser`.
  - `pkg/common/auth/auth.go:288-293`: nếu `rawBearer == ""` và `!internal`, interceptor cho request đi thẳng vào handler không có claims.
  - `services/user/internal/service/server.go:1329`: `if _, ok := auth.ClaimsFromContext(ctx); ok { s.requireAdminClaims(ctx) ... }` -> lời gọi không kèm Bearer token bỏ qua hoàn toàn kiểm tra admin và chèn thẳng bản ghi vào `audit_logs` với `actor_id`/`actor_role` tuỳ ý. Tương tự `ValidateUser` (`server.go:604`) bỏ qua kiểm tra chủ sở hữu khi không có claims.
- **Mong đợi vs thật**: Theo `AGENTS.md` luật 10 và `docs/contracts.md` §3, RPC nội bộ phải nằm trong `internalRPCs` và bắt buộc JWT người dùng hoặc service token ký bằng `SERVICE_JWT_SECRET`, không chấp nhận gọi ẩn danh.
- **Vị trí nghi là gốc**: `pkg/common/auth/auth.go:36-45,288-293`, `services/user/internal/service/server.go:604,1329`.

### `USR-B6` (P1) — Sổ địa chỉ không validate mã 34 tỉnh/phường và SĐT, `PUT` tự xoá cờ `is_default`, thiếu trường `legacy_*`, cho xoá địa chỉ kho của shop `ACTIVE`
- **Tái hiện**:
  1. Tạo địa chỉ với mã tỉnh/phường giả và SĐT sai:
     ```bash
     curl -X POST http://localhost:48080/api/v1/users/me/addresses \
       -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
       -d '{"recipient_name":"AUDIT-usr Fake","recipient_phone":"NOT-A-PHONE","province_code":"INVALID_999","province_name":"Tỉnh Ảo","ward_code":"FAKE_WARD_000","ward_name":"Phường Ảo","street_address":"Địa chỉ ảo","is_default":false}'
     # -> 201 Created (lưu nguyên province_code:"INVALID_999", ward_code:"FAKE_WARD_000", recipient_phone:"NOT-A-PHONE")
     ```
  2. Cập nhật địa chỉ đang là mặc định nhưng bỏ trống `is_default`:
     ```bash
     curl -X PUT http://localhost:48080/api/v1/users/me/addresses/$DEFAULT_ADDR_ID \
       -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
       -d '{"street_address":"Số 22 đường Xuân Thủy"}'
     # -> 200 OK (is_default tự đổi từ true thành false!)
     ```
  3. Xoá địa chỉ đang làm `pickup_address_id` của gian hàng `ACTIVE`:
     ```bash
     curl -X DELETE http://localhost:48080/api/v1/users/me/addresses/$SHOP_PICKUP_ADDR_ID \
       -H "Authorization: Bearer $SELLER_TOKEN"
     # -> 200 OK {"ok":true} (shops.pickup_address_id trở thành tham chiếu mồ côi!)
     ```
- **Mong đợi vs thật**: Theo `UC-SHIP01` (Cao), địa chỉ phải đúng chuẩn danh mục 34 tỉnh/phường để hãng vận chuyển nhận được, hỗ trợ gợi ý từ địa chỉ cũ (bảng mapping), không làm mất địa chỉ mặc định khi sửa một phần, và không cho xoá địa chỉ kho đang gắn với shop hoạt động. Thực tế `services/user` không gọi `ShippingService` để kiểm mã hành chính, không validate SĐT, `UpdateAddressRequest` (`user.proto:322-333`) thiếu các trường `legacy_*` (lệch `docs/contracts.md:135`), `PUT` gán thẳng `req.GetIsDefault()` (`false`), `DeleteAddress` không kiểm tham chiếu `shops.pickup_address_id`, và `web/src/features/commerce/AddressForm.tsx` chưa nối `POST /api/v1/shipping/addresses/map-legacy`.
- **Vị trí nghi là gốc**: `services/user/internal/service/server.go:1119-1156,1265-1285,1293-1324`, `proto/nexusmart/user/v1/user.proto:322-333`, `web/src/features/commerce/AddressForm.tsx`.

### `USR-B7` (P1) — Xác thực OTP email tự động đánh dấu `phone_verified = true` kể cả khi SĐT rỗng, và đổi SĐT không cần OTP
- **Tái hiện**:
  1. Đăng ký với `phone: ""`:
     ```bash
     curl -X POST http://localhost:48080/api/v1/auth/register -H "Content-Type: application/json" \
       -d '{"full_name":"AUDIT-usr NoPhone","email":"audit-usr-nophone-4220@nexusmart.local","phone":"","password":"AuditUser123!"}'
     # -> 201 Created
     curl -X POST http://localhost:48080/api/v1/auth/verify-otp -H "Content-Type: application/json" \
       -d '{"email":"audit-usr-nophone-4220@nexusmart.local","otp":"$OTP"}'
     # -> 200 OK (user.phone:"", user.phone_verified:true!)
     ```
  2. Đổi SĐT qua `PATCH /api/v1/users/me` (`{"phone":"0987004220"}`) -> `200 OK`, cập nhật ngay không qua OTP và vẫn giữ `phone_verified: true`.
- **Mong đợi vs thật**: OTP trong `UC-B01` gửi qua email chỉ xác thực email; không được gán `phone_verified = true` khi chưa xác thực OTP SĐT (đặc biệt khi `phone == ""`). Theo `UC-B19`, đổi SĐT phải xác thực bằng OTP gửi tới số mới. Lỗi này đồng thời làm vô hiệu tiền điều kiện COD của `UC-PAY02` (*"số điện thoại đã xác thực OTP"*).
- **Vị trí nghi là gốc**: `services/user/internal/service/server.go:243-246` (`PhoneVerified: true` hardcode), `services/user/internal/service/server.go:569-588`.

### `USR-B8` (P2) — Dò tài khoản qua `verify-otp`/`resend-otp`/`register` (SĐT) & timing side-channel trên `login`; thiếu lý do khoá khi tài khoản `LOCKED` đăng nhập
- **Tái hiện**:
  ```bash
  curl -X POST http://localhost:48080/api/v1/auth/verify-otp -H "Content-Type: application/json" \
    -d '{"email":"nonexist-4220@nexusmart.local","otp":"123456"}'
  # -> 404 NOT_FOUND: "user not found"

  curl -X POST http://localhost:48080/api/v1/auth/verify-otp -H "Content-Type: application/json" \
    -d '{"email":"minhquan.nguyen@nexusmart.vn","otp":"123456"}'
  # -> 409 FAILED_PRECONDITION: "account is not pending verification"

  curl -X POST http://localhost:48080/api/v1/auth/resend-otp -H "Content-Type: application/json" \
    -d '{"email":"nonexist-4220@nexusmart.local"}'
  # -> 404 NOT_FOUND ("user not found") vs minhquan.nguyen@nexusmart.vn -> 409 ("account is not pending verification")

  curl -X POST http://localhost:48080/api/v1/auth/login -H "Content-Type: application/json" \
    -d '{"email":"nonexist-4220@nexusmart.local","password":"WrongPassword123!"}'
  # -> 401 trong ~9 ms (vs 96–109 ms với email tồn tại vì bỏ qua Argon2id!)

  curl -X POST http://localhost:48080/api/v1/auth/login -H "Content-Type: application/json" \
    -d '{"email":"audit-usr-admtgt-4220@nexusmart.local","password":"AuditUser123!"}' # khi đang LOCKED
  # -> 403 PERMISSION_DENIED: "account is locked" (không có lý do khoá & cách liên hệ)
  ```
- **Mong đợi vs thật**: `verify-otp` và `resend-otp` không được phân biệt `404` vs `409` làm lộ toàn bộ danh sách email và trạng thái kích hoạt; `login` nên chạy dummy Argon2id khi không thấy email để tránh lộ qua độ trễ (~10x); khi tài khoản `LOCKED` đăng nhập đúng mật khẩu, `UC-B02` yêu cầu hiển thị lý do khoá và cách liên hệ. Ngoài ra, khoá brute-force chỉ tính theo `email` (5 lần sai -> khoá 15p) giúp kẻ tấn công khoá tạm bất kỳ tài khoản nào (kể cả admin) mà không có giới hạn tốc độ riêng chặt hơn cho `/auth/login` theo IP.
- **Vị trí nghi là gốc**: `services/user/internal/service/server.go:153-158,221-230,264-273,322-354`, `services/user/migrations/0001_init.sql:3-17` (thiếu cột `lock_reason`).

### `USR-B9` (P2) — Link trong thông báo in-app `user.registered` (`/verify-otp`) và `user.shop_reviewed` (`/seller/dashboard`) bị `404` trên web
- **Tái hiện**:
  1. Gọi `GET /api/v1/notifications` bằng token của `audit-usr-1-4220@nexusmart.local` -> nhận 4 thông báo với `link: "/verify-otp"` và `link: "/seller/dashboard"` (kể cả thông báo `REQUEST_INFO` và `REJECT`!).
  2. Truy cập trên web:
     ```bash
     curl -s -o /dev/null -w "%{http_code}\n" http://localhost:43000/verify-otp
     # -> 404
     curl -s -o /dev/null -w "%{http_code}\n" -H "Cookie: $SELLER_COOKIE" http://localhost:43000/seller/dashboard
     # -> 404
     ```
- **Mong đợi vs thật**: Link thông báo phải dẫn đúng route web đang tồn tại (`/dang-ky`, `/seller`, hoặc `/tai-khoan` khi shop bị `REJECT`/`REQUEST_INFO`). Thực tế cả 2 link đều trả `404` và `normalizeNotificationHref` chỉ xử lý `/orders*`.
- **Vị trí nghi là gốc**: `services/notification/internal/service/notification.go:330,359`, `web/src/features/account/orderMeta.ts:334-346`.

### `USR-B10` (P2) — Lệch điều hướng theo role trên web, thiếu lọc thời gian audit log, thiếu `super_admin` trên UI, và sai lệch nhỏ trong `docs/contracts.md`
- **Tái hiện / bằng chứng**:
  1. **Điều hướng đăng nhập (`UC-B02`)**: `web/src/app/(shop)/dang-nhap/page.tsx:39,88` mặc định `safeNext = sanitizeNextPath(rawNext, "/")`, chuyển cả `seller` và `admin`/`super_admin` về `/` thay vì `/seller` hoặc `/admin` khi không có `?next=`.
  2. **Bộ lọc thời gian audit log (`UC-A11`)**: `web/src/app/(admin)/admin/audit-log/page.tsx:130-173` không có ô lọc `from_time`/`to_time` dù backend `handlers_user.go:862-884` đã hỗ trợ; `docs/contracts.md:148` cũng ghi thiếu 2 tham số query này.
  3. **Thiếu `super_admin` trong UI quản trị người dùng**: `web/src/features/admin/AdminUsersPanel.tsx:68,174-178,299-303` chỉ có `buyer`/`seller`/`admin` (chọn `super_admin` bị gán nhầm dropdown về `buyer`); `docs/contracts.md:145` cũng thiếu `super_admin`.
  4. **Đổi vai trò `seller` thủ công lệch `shop_id`**: `AdminUpdateUserRole` (`services/user/internal/service/server.go:802-805`) đổi `buyer -> seller` mà `shop_id = NULL` (mọi `/seller/*` trả `403 seller account has no active shop`), và đổi `seller -> buyer` không xoá `users.shop_id`.
  5. **Lệch RBAC trong `docs/contracts.md:137`**: tài liệu ghi `POST /api/v1/shops/register` chỉ cho `buyer, seller`, nhưng `services/gateway/internal/router/router.go:171` cho cả `admin, super_admin`.

---

## 4. Dữ liệu (`USR-D<n>`)

### `USR-D1` — Địa chỉ seed của buyer demo `minhquan.nguyen@nexusmart.vn` lệch phường và thiếu địa chỉ thứ 2 so với `web/src/data/orders.ts`
- **So sánh**:
  - `web/src/data/orders.ts:204-225` khai báo 2 địa chỉ cho Nguyễn Minh Quân:
    1. `Số 248 đường Võ Văn Tần`, **`Phường Xuân Hòa`**, `Thành phố Hồ Chí Minh` (`isDefault: true`).
    2. `Ngõ 85 phố Xuân Thủy, nhà số 14`, `Phường Cầu Giấy`, `Thành phố Hà Nội` (`isDefault: false`).
  - `user_db.addresses` & `GET /api/v1/users/me/addresses` hiện chỉ có **1 địa chỉ** và bị lưu thành `ward_code = "79001"`, **`ward_name = "Phường Sài Gòn"`** (lệch so với `Phường Xuân Hòa`) do `shipping_db.wards` của TP.HCM (`79`) chỉ seed 8 phường đại diện không có `Phường Xuân Hòa` nên `tools/seed/seed.mjs:362` tự rơi về `wards[0]`. Ngoài ra `seed.mjs:865-872` chỉ nạp `INITIAL_ADDRESSES[0]`, bỏ qua địa chỉ thứ 2 tại Hà Nội (dù `01003 Phường Cầu Giấy` có sẵn trong `shipping_db`).

### `USR-D2` — Địa chỉ kho của 4 shop seed bị lặp địa giới trong `street_address` và `Kho Basic Sài Gòn` bị sai phường
- **So sánh**:
  - `web/src/data/shops.ts:248`: `Kho Basic Sài Gòn` có `warehouseAddress: "Số 118 đường Nguyễn Văn Hưởng, Phường Thảo Điền, Thành phố Hồ Chí Minh"`.
  - `user_db.addresses` của `seller.saigon@nexusmart.local` lưu `ward_code = "79001"`, **`ward_name = "Phường Sài Gòn"`** (vì `shipping_db` thiếu `Phường Thảo Điền`), trong khi `street_address` chứa nguyên chuỗi có `Phường Thảo Điền, Thành phố Hồ Chí Minh`.
  - Cả 4 shop seed (`bachdang`, `saigon`, `danang`, `hanoi`) đều có `street_address` chứa sẵn tên Phường và Thành phố (`tools/seed/seed.mjs:484`), nên khi ghép `[street_address, ward_name, province_name]` trên UI bị lặp 2 lần (hoặc mâu thuẫn 2 phường khác nhau đối với `Kho Basic Sài Gòn`).

### `USR-D3` — Bảng `audit_logs` sau `make seed` thiếu toàn bộ thao tác quản trị ngoài `user` và lưu IP Docker nội bộ
- **Thực tế DB**: `SELECT * FROM audit_logs` ngay sau seed chỉ có 4 dòng `admin.shop.review` (thiếu 6 danh mục, 18 duyệt sản phẩm, voucher, 3 khung/16 item flash sale, 1 lệnh cộng 42.000 Xu) và mọi dòng đều mang `ip_address = "172.20.0.15:43034"` (IP container gateway).

### `USR-D4` — Dữ liệu `phone_verified = true` cho tài khoản không có SĐT & trang `/dang-ky` hiển thị tặng `15.000 Xu` không có thật
- **Thực tế**:
  - `user_db.users`: `admin@nexusmart.local` có `phone = ""` nhưng `phone_verified = t`; mọi tài khoản mới đăng ký không nhập SĐT sau khi verify email OTP đều có `phone = ""` và `phone_verified = t`.
  - `web/src/app/(shop)/dang-ky/page.tsx:278,397` ghi *"Thành viên mới nhận ngay 15.000 Xu (tương đương 15.000₫)"*, nhưng kiểm chứng thực tế trên `audit-usr-1-4220@nexusmart.local` qua `GET /api/v1/coins/balance` trả **`balance: 0`**, `recent_transactions: []`.

### `USR-D5` — Đối chiếu 4 shop seed & buyer demo (phần khớp và chênh lệch trường mở rộng)
- **Khớp hoàn toàn**: 4 shop trong `user_db.shops` và `GET /api/v1/shops` khớp `web/src/data/shops.ts` về tên, slug (`xuong-det-bach-dang`, `kho-basic-sai-gon`, `soi-tu-nhien-da-nang`, `det-kim-ha-noi`), mô tả (`description == story`), trạng thái `ACTIVE`, và 4 tài khoản seller có `role = seller`, `status = ACTIVE`, `shop_id` gắn đúng; buyer demo `minhquan.nguyen@nexusmart.vn` khớp `full_name` (`Nguyễn Minh Quân`), `phone` (`0912486305`), `role = buyer`, `status = ACTIVE`, và số dư `42.000 Xu` (`promotion_db.coin_accounts`).
- **Chênh lệch nguồn dữ liệu**: `user_db.users` thiếu trường `birthday` (`1996-11-24` trong `orders.ts:201`); trang `/gian-hang/[slug]` vẫn dùng dữ liệu mẫu từ `shops.ts` cho các chỉ số vận hành (tỷ lệ hoàn tất, thời gian phản hồi, hồ sơ nhà máy/QC, bảng size).

---

## 5. Đề xuất sửa

| Mã | Giải quyết | Công việc cụ thể | Cỡ | Đổi proto / Migration / ADR |
| --- | --- | --- | --- | --- |
| `USR-F1` | `USR-B1` (P0), `UC-A03` | Thêm cơ chế thu hồi toàn bộ access token theo `user_id` trong Redis (`user:revoked_before:<user_id>` lưu unix timestamp, TTL = `JWT_ACCESS_TTL`). Ghi mốc này khi `AdminUpdateUserStatus(LOCKED)` và `AdminUpdateUserRole` (đồng thời gọi `RevokeAllUserRefreshTokens` khi đổi role). Kiểm tra `claims.IssuedAt < revokedBefore` tại `services/gateway/internal/middleware/auth.go` (fail-closed) và `services/user/internal/service/server.go:requireAuthClaims`. | S | Proto: Không · Migration: Không · ADR: Không (dùng Redis sẵn có) |
| `USR-F2` | `USR-B2`, `USR-B3`, `UC-S01`, `UC-A01` | Thêm `UpdateShop` (`PATCH /api/v1/shops/me`) cho phép chủ shop cập nhật & nộp lại hồ sơ khi bị `REQUEST_INFO` (hoặc thêm trạng thái `SHOP_STATUS_NEEDS_INFO`) và khi bị `REJECTED` (tái sử dụng bản ghi thay vì tạo dòng trùng; đổi unique index tên/slug sang `WHERE status IN ('PENDING','ACTIVE')`); bắt buộc `reason != ""` cho `REJECT`/`REQUEST_INFO`; chặn `AdminReviewShop` đổi shop đang `ACTIVE` sang `REJECTED`, thêm RPC `SuspendShop` (chuyển `SUSPENDED` + gỡ quyền bán + thu hồi token); bắt buộc `pickup_address_id`, `contact_phone`, `business_license_doc` khi đăng ký; sửa `AccountClient.tsx` và `/admin/gian-hang`. | M | Proto: **Có** (`UpdateShop`, `SuspendShop`, tuỳ chọn `SHOP_STATUS_NEEDS_INFO`) · Migration: **Có** (sửa partial unique index trên `shops`) · ADR: Không cho luồng text; **Cần ADR** (luật 11 `AGENTS.md`) nếu bổ sung upload tệp KYC/CCCD/GPKD lên object storage mới (MinIO/S3) |
| `USR-F3` | `USR-B4`, `USR-B5`, `UC-A11` | (a) Đưa `/nexusmart.user.v1.UserService/WriteAuditLog` (`true`) và `ValidateUser` (`true`) vào `internalRPCs` (`pkg/common/auth/auth.go`), xoá nhánh bỏ qua auth khi thiếu claims; (b) Chuyển tiếp IP client qua gRPC metadata từ gateway (`x-client-ip`) để ghi đúng `audit_logs.ip_address`; (c) Gọi `WriteAuditLog` (từ gateway sau mỗi thao tác `/admin/*` thành công hoặc từ từng service) cho `product`, `category`, `voucher`, `flash-sale`, `coins`, `refund`, `reconcile`; (d) Thêm trigger `BEFORE TRUNCATE` trên `audit_logs` và tách DB user ứng dụng không phải superuser; (e) Thêm lọc `from_time`/`to_time` trên `/admin/audit-log`; (f) Bổ sung vai trò nội bộ (`moderator`, `cs`, `accountant`, `ops`), tạo nhân viên sàn và bảng/luồng duyệt 2 người (4-eyes) cho hoàn tiền lớn & khoá shop. | L (a–e: M; f: L) | Proto: **Có** (cho phần f: enum role nội bộ + RPC staff/approval) · Migration: **Có** (trigger truncate + phần f) · ADR: Không |
| `USR-F4` | `USR-B6`, `UC-SHIP01`, `USR-D1`, `USR-D2` | Kiểm tra `province_code`/`ward_code` hợp lệ theo `ShippingService` (tại gateway hoặc từ `services/user`), validate định dạng SĐT VN; cập nhật `UpdateAddressRequest` dùng `optional bool is_default` và bổ sung `legacy_*`; chặn `DeleteAddress` nếu đang là `pickup_address_id` của shop `PENDING`/`ACTIVE`; tích hợp `POST /api/v1/shipping/addresses/map-legacy` vào `web/src/features/commerce/AddressForm.tsx`; bổ sung `Phường Xuân Hòa`, `Phường Thảo Điền` vào `shipping_db` và sửa `tools/seed/seed.mjs` tách `street_address` khỏi phường/tỉnh + seed đủ 2 địa chỉ cho buyer demo. | M | Proto: **Có** (`UpdateAddressRequest`) · Migration: **Có** (bổ sung phường trong `shipping_db`) · ADR: Không |
| `USR-F5` | `USR-B7`, `UC-B01`, `UC-B19`, `UC-B11` | Tách cột `email_verified` khỏi `phone_verified` trong `user_db.users` (verify email OTP chỉ bật `email_verified=true`, không tự bật `phone_verified=true`); bổ sung luồng quên/đặt lại mật khẩu qua token email 15p (`forgot-password`, `reset-password` thu hồi toàn bộ refresh token + đặt `revoked_before`), đổi mật khẩu (`change-password`), đổi email/SĐT cần OTP, cột `birthday`, danh sách phiên (`refresh_tokens` kèm user-agent/IP) và Google OAuth 2.0 (`UC-B11`). | L | Proto: **Có** · Migration: **Có** (`email_verified`, `birthday`, `password_reset_tokens`, `oauth_identities`, metadata phiên) · ADR: Không cho OAuth2/email; **Cần ADR** nếu tích hợp SMS OTP thật cho SĐT |
| `USR-F6` | `USR-B8`, `USR-B9`, `USR-B10`, `USR-D4` | Chuẩn hoá mã/thông báo lỗi của `verify-otp`/`resend-otp` chống dò email; chạy Argon2id dummy verify khi email không tồn tại trên `Login`; bổ sung rate limit riêng cho `/api/v1/auth/*` theo IP và cột `lock_reason` trả kèm khi tài khoản `LOCKED` đăng nhập; sửa link thông báo `/verify-otp` -> `/dang-ky` và `/seller/dashboard` -> `/seller` (hoặc `/tai-khoan` khi `REJECT`/`REQUEST_INFO`) trong `services/notification` + `normalizeNotificationHref`; sửa `/dang-nhap` chuyển trang mặc định theo role; gỡ/chỉnh câu hứa "nhận ngay 15.000 Xu" trên `/dang-ky`; bổ sung `super_admin` trên `AdminUsersPanel.tsx` và đồng bộ `docs/contracts.md:135,137,145,148`. | S | Proto: Tuỳ chọn (`lock_reason` trên `User`) · Migration: Tuỳ chọn (`lock_reason`) · ADR: Không |
| `USR-F7` | `UC-B24`, `UC-B27`, `UC-S11`, `UC-S14`, `UC-A10` | Nhóm UC ưu tiên **Thấp** (spec ghi chỉ đặc tả, không bắt buộc cài đặt): xếp vào backlog sau khi hoàn tất P0/P1 và các UC Cao/Trung bình (báo cáo vi phạm, điểm phạt shop, nhân viên gian hàng, xuất/ẩn danh dữ liệu cá nhân). | L | Proto: **Có** · Migration: **Có** · ADR: **Cần ADR** nếu dùng object storage ngoài để lưu gói dữ liệu xuất của `UC-B27` |

---

## 6. Đã kiểm gì, chưa kiểm gì

### Đã kiểm trên code `HEAD` (`0db2cdd`) và stack thật `nexusmart-audit`
- **Xác thực & phiên**: đăng ký email + OTP Mailpit, đăng ký với `phone:""`, nhập sai OTP 5 lần (bị xoá OTP), gửi lại OTP 4 lần (giới hạn 3 lần/15p), xác thực bằng OTP mới nhất, đăng nhập, kiểm tra claim JWT (`alg=HS256`, `exp-iat=900`, `sub`, `role`, `shop_id`, `jti`, `iss`), xoay vòng refresh token + phát hiện tái sử dụng thu hồi cả family, logout thu hồi cả JTI trên Redis (`user:revoked_jti:*`) lẫn refresh token, kiểm tra hash `$argon2id$v=19$m=19456,t=2,p=1$...` trong `user_db.users`, kiểm tra log JSON + `trace_id` + che email (`mi***@nexusmart.vn`).
- **Dò tài khoản & chống brute-force**: đo mã lỗi & thời gian phản hồi trên `register`, `verify-otp`, `resend-otp`, `login`; kiểm tra khoá tạm 15 phút (`user:login_lock:*` TTL ~880s) sau 5 lần sai mật khẩu.
- **Đăng ký & duyệt gian hàng**: trùng tên shop (`409`), `REQUEST_INFO` (email Mailpit + kẹt không bổ sung được), `REJECT` với lý do rỗng (`200`) + không nộp lại được tên cũ (`409`), `APPROVE` (email Mailpit + in-app notification + nâng role `buyer -> seller` & `shop_id` sau refresh), `REJECT` shop đang `ACTIVE` (`200` mà không thu hồi role `seller`), kiểm tra lộ dữ liệu trên `/api/v1/shops`, `/shops/{id}`, `/shops/slug/{slug}` cho shop `PENDING`/`REJECTED`/`ACTIVE`.
- **Quản trị người dùng & audit log**: tìm kiếm theo email/SĐT, tự khoá/tự hạ quyền (`409`), `admin` khoá/nâng `super_admin` (`403`), khoá user & hạ quyền `admin -> buyer` kèm kiểm chứng JWT cũ còn hiệu lực (`USR-B1`), thử vai trò nội bộ `"moderator"` (`400`), thử `PUT/PATCH/DELETE` audit log qua API (`404`/`405`), kiểm tra trigger & quyền role DB `nexusmart` trên `audit_logs`, đối chiếu độ phủ `audit_logs` sau `make seed` và soát `WriteAuditLog` toàn repo.
- **Phân quyền đối tượng (IDOR) & bảo mật token**: buyer A sửa/xoá địa chỉ buyer B (`403`), buyer A đọc/huỷ đơn buyer B (`403`), seller A đọc/sửa/xoá sản phẩm/tồn kho/đơn hàng shop B (`403`), `GET /seller/products?shop_id=<shopB>` ép đúng `shopA`, buyer gọi API admin/seller (`403`), JWT sai chữ ký / `alg:none` / hết hạn / thiếu `exp` (`401`), service token ký bằng `SERVICE_JWT_SECRET` gọi gateway (`401`), giả header `x-user-id`/`x-user-role`/`x-shop-id` (`401`/`403`), kiểm tra BFF web (cookie `HttpOnly`/`SameSite=lax`, CSRF `403`, `/api/gw/auth/*` `403`, buyer vào `/admin` & `/seller` trả `404`, link `/verify-otp` & `/seller/dashboard` trả `404`, các trang `/tai-khoan`, `/seller`, `/seller/ho-so`, `/admin`, `/admin/gian-hang`, `/admin/cau-hinh`, `/admin/audit-log` trả `200`).
- **Sổ địa chỉ & dữ liệu seed**: CRUD + đổi mặc định, tạo mã tỉnh/phường giả & SĐT giả (`201`), `PUT` bỏ qua `is_default`, xoá địa chỉ kho đang gắn shop `ACTIVE`, đối chiếu 4 shop seed + buyer demo giữa `user_db`, `shipping_db`, `promotion_db`, API và `web/src/data/{shops,orders}.ts`.
- **Unit test Go**: `go test` trên `services/user` và `services/gateway` (tất cả PASS).

### Chưa kiểm (kèm lý do)
- **Chờ hết hạn tự nhiên 10 phút của OTP và 15 phút của access token / login lockout trên stack sống**: không chờ đủ 10–15 phút thực tế và không chỉnh/xoá TTL Redis (luật chỉ đọc Redis). Đã kiểm chứng gián tiếp bằng đọc TTL thực trên Redis (`user:otp:*` ~600s, `user:login_lock:*` ~900s, `user:revoked_jti:*` ~900s), kiểm claim `exp - iat = 900`, kiểm JWT tự ký có `exp` quá khứ bị gateway từ chối `401`, và chạy unit test `services/user/internal/{security,service}`.
- **Chặn khoá admin cuối cùng bằng tài khoản khác trên stack sống**: stack chỉ có 1 tài khoản admin seed duy nhất (`admin@nexusmart.local`); không thể khoá admin cuối cùng bằng chính nó vì đã bị chặn trước bởi luật tự khoá (`server.go:690-692`), và không đụng tài khoản seed. Nhánh `CountActiveAdmins <= 1` (`server.go:708-716,792-799`) đã được xác minh qua đọc code và unit test `services/user/internal/service`.
- **Khai thác trực tiếp gRPC `WriteAuditLog`/`ValidateUser` ẩn danh trên mạng nội bộ (`USR-B5`)**: cổng gRPC `50051` không publish ra host theo thiết kế compose; phát hiện được chứng minh chắc chắn từ code `pkg/common/auth/auth.go:36-45,288-293` và `services/user/internal/service/server.go:604,1329`.
- **Hết thời hạn thử lại/render trình duyệt Playwright đầy đủ**: chỉ kiểm HTTP SSR/BFF và đọc mã nguồn React; kiểm tra giao diện trực quan đa kích thước thuộc phạm vi worker `web`.
