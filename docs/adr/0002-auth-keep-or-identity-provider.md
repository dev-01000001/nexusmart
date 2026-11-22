# ADR-0002: Auth trong `services/user` — giữ tự viết hay chuyển sang Identity Provider

- **Trạng thái**: Đề xuất
- **Ngày**: 2026-11-22

## Bối cảnh

Xác thực và phân quyền hiện tự viết gọn trong `services/user`, dùng chung qua `pkg/common/auth` (Go) và `services/payment/src/auth.rs` (Rust):

- `services/user/internal/password/argon2.go` (108 dòng): băm mật khẩu Argon2id theo tham số OWASP (19 MiB, 2 vòng, 1 luồng), so sánh constant-time.
- `services/user/internal/security/redis_store.go` (243 dòng): OTP băm SHA-256 + Lua script nguyên tử (tối đa 5 lần thử, giới hạn gửi lại), khoá tạm 15 phút sau 5 lần sai, danh sách `user:revoked_jti:<jti>`.
- `services/user/internal/service/server.go` (1.378 dòng tổng cho `UserService`; riêng nhánh auth `Register`, `VerifyOTP`, `ResendOTP`, `Login`, `RefreshToken`, `Logout` chiếm dòng 131–515, ~385 dòng): xoay vòng refresh token theo `family_id` trong `refresh_tokens` (`services/user/migrations/0001_init.sql`, 109 dòng), phát hiện replay (cả tuần tự lẫn đồng thời ngoài TX rollback) và thu hồi toàn bộ family.
- `pkg/common/auth/auth.go` (361 dòng) & `services/payment/src/auth.rs` (242 dòng): ký/xác minh JWT HS256 (user TTL 15 phút, claims `sub`, `role`, `shop_id`, `jti`; service token TTL 1 phút cho `internalRPCs`/`SERVICE_METHODS`).
- `services/gateway/internal/middleware/auth.go` (116 dòng): xác minh JWT + kiểm `user:revoked_jti:<jti>` fail-closed `503` (riêng `user` cũng tự kiểm trong `requireAuthClaims`, 7 service còn lại chỉ kiểm chữ ký và `exp`).
- **Kiểm thử phủ**: unit test tại `services/user/internal/password/argon2_test.go`, `security/redis_store_test.go`, `service/server_test.go` (1.123 dòng: OTP, refresh rotation/concurrent replay, lockout, logout JTI, last-admin guard), `pkg/common/auth/{auth_test.go,grpc_trust_test.go}`, `services/payment/src/auth.rs` (`mod tests`), `services/gateway/internal/middleware/middleware_test.go`. E2E tại `tests/e2e/fixtures_test.go` (chạy qua `02`–`08`: register, OTP Mailpit, login, refresh), `02_cod_checkout_test.go` (`403` RBAC), `06_flash_sale_test.go` (gRPC JWT). Chưa có E2E riêng cho logout JTI hay lockout.
- **Khoảng trống đã biết**: `docs/SECURITY_0A_REPORT.md` §5 ghi nhận khoá HS256 đối xứng dùng chung giữa các service; `docs/CORE_REPORT.md` §5 ghi nhận `UC-B11` (Google OAuth 2.0 + quên mật khẩu) chưa triển khai. **Nơi triển khai chưa chốt**, ảnh hưởng chi phí tự vận hành HA của IdP ngoài.

## Ứng viên

- **Giữ auth trong `services/user`**: nâng cấp nội bộ (EdDSA + JWKS thay HS256, bổ sung Google OAuth 2.0 và reset mật khẩu theo `UC-B11`).
- **Keycloak** (`keycloak/keycloak`): **Apache-2.0**; bảo trì tích cực (CNCF/Red Hat, Java/Quarkus + JDK 21); bản mới nhất `26.8.2` (2026-11-05; nhánh `26.8.0` từ 2026-10-01). Đủ OIDC/OAuth2/SAML, social login, MFA, passkeys, Admin API. Client/SDK: Go dùng `github.com/coreos/go-oidc/v3` (OIDC/JWKS) + `github.com/Nerzal/gocloak/v13` (Admin API, cộng đồng); Rust dùng crate `openidconnect` (OIDC) + crate `keycloak` (Admin API, cộng đồng).
- **Zitadel** (`zitadel/zitadel`): **AGPL-3.0** cho core server từ v3 (2025-03; SDK và `proto/` giữ Apache-2.0/MIT); bảo trì tích cực (viết bằng Go, lưu PostgreSQL); bản mới nhất `v4.20.5` (2026-11-18; nhánh `v4.20.0` từ 2026-10-22). Có gRPC/REST API, OIDC/SAML, passkeys, multi-tenant. Client/SDK: Go chính thức `github.com/zitadel/zitadel-go/v3` (Apache-2.0); Rust có crate cộng đồng `zitadel` (MIT/Apache-2.0) hoặc `openidconnect`.

## Tiêu chí

| Tiêu chí | Giữ auth trong `user` | Keycloak | Zitadel |
|---|---|---|---|
| Độ phức tạp vận hành | Thấp (dùng sẵn `user_db` + Redis) | Cao (JVM/Quarkus, DB riêng, cluster Infinispan, backup/upgrade) | Trung bình (binary Go + Postgres riêng, nâng cấp theo v4.x) |
| Rủi ro chuyển đổi | Thấp | Cao (đồng bộ 2 chiều `users`/`shop_id`/`status`, đổi luồng OTP/Mailpit, sửa 9 service sang JWKS) | Cao (tương tự Keycloak + rà soát pháp lý AGPL-3.0) |
| Khớp gRPC + Go/Rust | Rất tốt (gRPC nội bộ sẵn có) | Tốt cho verify JWT qua JWKS ở Go/Rust; Admin API là REST | Rất tốt (gRPC + Go SDK chính thức, JWKS cho Rust) |
| Chi phí vận hành | Rất thấp | Cao (RAM JVM >= 1–2 GB/node + HA) | Trung bình (nhẹ hơn JVM nhưng vẫn thêm service stateful) |
| Bảo mật | Đạt lõi; cần lên EdDSA/JWKS để bỏ HS256 chung | Rất mạnh (OIDC chuẩn, MFA/WebAuthn, xoay khoá JWKS tự động) | Rất mạnh (OIDC/passkeys/audit sẵn) |

## Quyết định

- **Đề xuất của coder**: **Giữ auth tự viết trong `services/user`** ở giai đoạn hiện tại. Phần lõi auth nhỏ gọn (~736 dòng trong `user` + 603 dòng verify ở Go/Rust), gắn chặt dữ liệu nghiệp vụ (`shop_id`, `cod_rejected_count`, `PENDING_VERIFICATION`, `audit_logs`), và đã có bộ test hồi quy đầy đủ. Thay vì kéo IdP ngoài, khắc phục rủi ro lớn nhất bằng cách nâng JWT từ HS256 lên **EdDSA + JWKS** (cho phép 8 service Go và `payment` Rust verify không giữ secret ký) và tự cài Google OAuth 2.0 + quên mật khẩu (`UC-B11`).
- **Điều kiện kích hoạt để chuyển sang IdP**: khi phát sinh yêu cầu enterprise SSO (SAML/OIDC federation nhiều tổ chức), MFA/passkeys/WebAuthn đầy đủ, nhiều ứng dụng client dùng chung danh tính, hoặc yêu cầu compliance tách kho credential khỏi app DB. Khi đạt ngưỡng đó, ưu tiên **Keycloak** (Apache-2.0, chuẩn OIDC không vướng AGPL-3.0); chỉ chọn **Zitadel** nếu không sửa core và xác nhận tuân thủ AGPL-3.0 (hoặc dùng bản managed).
- **Phụ thuộc**: ADR nơi triển khai (chưa chốt) — tự host IdP HA tốn nhân lực vận hành; nếu lên cloud có thể đánh giá managed IdP.
- **Chủ dự án quyết**: (trống)

## Hệ quả

- **Giữ auth trong `user` (đề xuất)**: không tách đôi nguồn sự thật `users`, giữ tốc độ phát triển và bộ E2E hiện có, chi phí hạ tầng bằng 0; đánh đổi phải tự bảo trì các luồng auth mới (`UC-B11`, nâng cấp EdDSA/JWKS) và kiểm tra bảo mật kỹ mỗi lần sửa.
- **Chuyển sang Keycloak**: có sẵn OIDC/JWKS/social/MFA chuẩn ngành, nhưng tăng nặng RAM/vận hành JVM và phải thiết kế cơ chế đồng bộ `shop_id`/trạng thái khoá tài khoản với `user_db`.
- **Chuyển sang Zitadel**: gọn tài nguyên hơn Keycloak và hợp stack Go/gRPC, nhưng vướng license **AGPL-3.0** ở core server và vẫn phải chịu chi phí tách dữ liệu định danh khỏi `user_db`.

## Ngày kiểm tra trạng thái ứng viên

- **Ngày kiểm tra**: 2026-11-22
- **Nguồn**:
  - Keycloak: https://github.com/keycloak/keycloak, https://github.com/keycloak/keycloak/releases, https://github.com/coreos/go-oidc, https://github.com/Nerzal/gocloak, https://crates.io/crates/openidconnect, https://crates.io/crates/keycloak
  - Zitadel: https://github.com/zitadel/zitadel, https://github.com/zitadel/zitadel/releases, https://github.com/zitadel/zitadel-go, https://crates.io/crates/zitadel
