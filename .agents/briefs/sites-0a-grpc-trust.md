# Giai đoạn 0a – Vá tin cậy gRPC (bảo mật, làm trước mọi việc khác)

Đọc `AGENTS.md` và `docs/research/sites/review-opus.md` (mục 2 dòng đầu, mục 4 "Bảo mật đa tenant").

## Lỗi
- `pkg/common/auth/auth.go` (`ExtractIncomingGRPC`, `GRPCUnaryServerInterceptor`, khoảng dòng 218–262): khi request không có Bearer JWT, interceptor dựng `Claims` thẳng từ metadata `x-user-id`, `x-user-role`, `x-shop-id` mà không kiểm chữ ký.
- `docker-compose.yml` công bố các cổng gRPC 50051–50058 ra host (`0.0.0.0`).
- Hệ quả: ai kết nối được tới gRPC đều tự xưng được là bất kỳ user, admin, shop nào.

## Việc phải làm
1. Trước khi sửa, grep MỌI nơi gọi interceptor và mọi nơi set các metadata này (gateway, lời gọi service-tới-service, saga của order, consumer, test e2e). Sửa một lần ở chỗ dùng chung.
2. Danh tính người dùng chỉ được tin khi có JWT hợp lệ (gateway chuyển tiếp Bearer của người dùng). Bỏ nhánh tin metadata trần.
3. Lời gọi nội bộ không có người dùng (saga, job, consumer): dùng một danh tính service có xác thực. Chọn cách đơn giản nhất mà đúng: token service ký bằng khoá riêng cho nội bộ (khác khoá ký token người dùng), TTL ngắn, có claim phân biệt `service`; service nhận chỉ cho phép danh tính service ở các RPC nội bộ cần thiết. Không dựng mTLS/PKI trong giai đoạn này; ghi `ponytail:` comment nêu trần và đường nâng cấp (mTLS, JWT bất đối xứng + JWKS).
4. Áp dụng tương đương cho `services/payment` (Rust) nếu nó tin metadata theo cách tương tự.
5. `docker-compose.yml`: không công bố cổng gRPC ra host. Chỉ gateway (và các cổng HTTP cần cho dev như mailpit) được công bố. Test e2e phải đi qua gateway hoặc chạy trong network của compose. Giữ nguyên cơ chế override cổng host qua biến môi trường cho các cổng còn công bố (máy này dùng gateway ở 28080).
6. Không secret thật trong repo; cập nhật `.env.example`.

## Tiêu chí nghiệm thu
- Test Go mới: gọi một RPC cần quyền shop (và một RPC admin) với `x-shop-id`/`x-user-role` giả, không JWT → `Unauthenticated`. JWT hợp lệ của shop A kèm `x-shop-id` của shop B → bị từ chối hoặc metadata bị bỏ qua (kết quả thuộc shop A).
- Token service giả (sai khoá, hết hạn) → `Unauthenticated`.
- `docker compose config` không còn ánh xạ cổng host cho 5005x; sau `make up`, `ss -ltn` trên host không có 5005x.
- `gofmt`, `golangci-lint`, `go test ./...` mọi module, `cargo clippy -- -D warnings`, `cargo test` sạch.
- `make up` khoẻ mạnh và `make e2e` xanh 8/8 (dùng các override cổng host hiện có; gateway 28080). Không chạy `make down` (xoá volume) nếu không cần.
- Ghi `docs/SECURITY_0A_REPORT.md`: nguyên nhân, mọi nơi đã sửa (file:dòng), kết quả từng lệnh kiểm tra, phần còn lại.

## Ràng buộc
- Không sửa `web/`, `.taste-skill/`, `tools/`. Một server web của manager chạy ở cổng 3111 và monitor ở 4777: không kill.
- Không `sudo`. Thay đổi nhỏ nhất đúng gốc; không tái cấu trúc ngoài phạm vi.
